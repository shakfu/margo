package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// webFetchDefaultMaxBytes caps a single fetch at ~256KB of text. The number
// is small on purpose: a successful fetch becomes part of the next model
// turn's prompt, and uncapped HTML easily exhausts a context window.
const webFetchDefaultMaxBytes = 256 * 1024

// webFetchTimeout bounds a single fetch. Streaming tools are visible mid-
// flight, so a long timeout is acceptable; we still want to prevent runaway
// hangs against unresponsive hosts.
const webFetchTimeout = 30 * time.Second

// webFetchChunkBytes is the read/emit granularity. Small enough that the UI
// sees several chunks for typical pages (~tens of KB), large enough that we
// don't drown the event channel for big payloads.
const webFetchChunkBytes = 4 * 1024

type webFetchArgs struct {
	URL      string `json:"url" jsonschema:"description=Absolute http(s) URL to fetch"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"description=Optional truncation cap in bytes; defaults to 262144"`
}

// WebFetchTool fetches an http(s) URL and streams the response body in
// chunks. HTML content is best-effort reduced to readable text (scripts and
// styles stripped, tags removed, whitespace collapsed); other content types
// stream through unchanged. The body is truncated to MaxBytes (default
// ~256KB) before chunking so the agent can't pull an unbounded payload into
// its next prompt.
func WebFetchTool() tool.StreamableTool {
	t, err := toolutils.InferStreamTool(
		"web_fetch",
		"Fetches an http(s) URL and streams its body back as text. Use for reading webpages, public docs, or any HTTP-served text content. HTML is reduced to readable text; binary or non-text responses are rejected.",
		streamWebFetch,
	)
	if err != nil {
		// InferStreamTool only fails on bad reflection of the args type — a
		// programmer error in this fixed definition.
		panic(err)
	}
	return t
}

func streamWebFetch(ctx context.Context, in webFetchArgs) (*schema.StreamReader[string], error) {
	if strings.TrimSpace(in.URL) == "" {
		return nil, fmt.Errorf("url is required")
	}
	if !strings.HasPrefix(in.URL, "http://") && !strings.HasPrefix(in.URL, "https://") {
		return nil, fmt.Errorf("url must be http(s)")
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = webFetchDefaultMaxBytes
	}

	fetchCtx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, in.URL, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "margo-web-fetch/1.0")
	req.Header.Set("Accept", "text/html,text/plain,application/json,text/*;q=0.9,*/*;q=0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("fetch: %w", err)
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("http %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}

	ctype := strings.ToLower(resp.Header.Get("Content-Type"))
	if !isTextContentType(ctype) {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("non-text content type: %s", ctype)
	}
	isHTML := strings.Contains(ctype, "html")

	sr, sw := schema.Pipe[string](4)
	go func() {
		// One cancel covers both the http request and the read loop. Close
		// the body before cancel so the underlying transport can clean up.
		defer cancel()
		defer resp.Body.Close()
		defer sw.Close()

		limited := io.LimitReader(resp.Body, int64(maxBytes)+1)
		// For HTML we need the full payload before we can reliably strip
		// tags (a chunk boundary may fall inside a tag). For plain text /
		// JSON we can stream chunk-by-chunk without buffering. Both paths
		// honour the maxBytes cap.
		if isHTML {
			body, _ := io.ReadAll(limited)
			truncated := len(body) > maxBytes
			if truncated {
				body = body[:maxBytes]
			}
			text := htmlToText(string(body))
			emitChunks(sw, text, webFetchChunkBytes)
			if truncated {
				sw.Send(fmt.Sprintf("\n\n[truncated at %d bytes]", maxBytes), nil)
			}
			return
		}

		buf := make([]byte, webFetchChunkBytes)
		read := 0
		for {
			n, rerr := limited.Read(buf)
			if n > 0 {
				toSend := n
				if read+n > maxBytes {
					toSend = maxBytes - read
				}
				if toSend > 0 {
					if closed := sw.Send(string(buf[:toSend]), nil); closed {
						return
					}
					read += toSend
				}
				if read >= maxBytes {
					sw.Send(fmt.Sprintf("\n\n[truncated at %d bytes]", maxBytes), nil)
					return
				}
			}
			if rerr == io.EOF {
				return
			}
			if rerr != nil {
				sw.Send("", rerr)
				return
			}
		}
	}()
	return sr, nil
}

// isTextContentType returns true for content types the agent can reasonably
// consume as text. We reject binary types up front so the stream doesn't
// emit garbage bytes that bloat the next model prompt.
func isTextContentType(ctype string) bool {
	if ctype == "" {
		// Server omitted Content-Type. Allow it — many text endpoints do.
		return true
	}
	if strings.HasPrefix(ctype, "text/") {
		return true
	}
	if strings.Contains(ctype, "json") || strings.Contains(ctype, "xml") {
		return true
	}
	return false
}

func emitChunks(sw *schema.StreamWriter[string], s string, chunkSize int) {
	for i := 0; i < len(s); i += chunkSize {
		end := i + chunkSize
		if end > len(s) {
			end = len(s)
		}
		if closed := sw.Send(s[i:end], nil); closed {
			return
		}
	}
}

// Go's RE2 doesn't support backreferences, so script/style are matched with
// two separate non-greedy patterns instead of a single alternation with \1.
var (
	scriptRe    = regexp.MustCompile(`(?is)<script[^>]*>.*?</\s*script\s*>`)
	styleRe     = regexp.MustCompile(`(?is)<style[^>]*>.*?</\s*style\s*>`)
	tagRe       = regexp.MustCompile(`<[^>]+>`)
	wsRe        = regexp.MustCompile(`[ \t\f\v]+`)
	blankLineRe = regexp.MustCompile(`\n\s*\n\s*\n+`)
)

// htmlToText is a deliberately crude HTML-to-text reducer: drops <script> /
// <style> blocks entirely, strips remaining tags, decodes a handful of common
// entities, and collapses whitespace. The goal is "good enough for an LLM
// summary"; we are not building a renderer.
func htmlToText(html string) string {
	s := scriptRe.ReplaceAllString(html, "")
	s = styleRe.ReplaceAllString(s, "")
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
	).Replace(s)
	s = wsRe.ReplaceAllString(s, " ")
	s = blankLineRe.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
