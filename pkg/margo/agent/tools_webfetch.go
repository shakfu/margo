package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
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

// ErrBlockedAddress is returned when a fetch resolves to an address on
// the local machine or a private network. The URL is chosen by the model,
// and a model's context can include text fetched from the open web, so
// "the model asked for it" is not evidence the user wanted it.
var ErrBlockedAddress = errors.New("refusing to fetch a non-public address")

// cgnatNet is RFC 6598 shared address space. net.IP.IsPrivate does not
// cover it, but it is routable only inside a carrier or LAN, so it is not
// a legitimate target for this tool.
var cgnatNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// blockedIP reports whether ip is somewhere a web fetch has no business
// reaching: the loopback interface, a private LAN, link-local space
// (which includes the 169.254.169.254 cloud-metadata endpoint), or a
// multicast / unspecified address.
func blockedIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		cgnatNet.Contains(ip)
}

// WebFetchOptions configures WebFetchTool.
type WebFetchOptions struct {
	// AllowPrivateNetwork lifts the address guard. Off by default; a
	// user who wants the agent reading http://localhost:8080 has to say
	// so, because the model cannot be the one to decide that.
	AllowPrivateNetwork bool
}

// newWebFetchClient builds the HTTP client used for a fetch. The address
// check lives in Dialer.Control rather than on the parsed URL because
// Control runs after DNS resolution with the address actually about to
// be dialled — a hostname that resolves to 127.0.0.1, or one that
// re-resolves between check and connect, is caught there and nowhere
// else.
func newWebFetchClient(allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	if !allowPrivate {
		dialer.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, address)
			}
			ip := net.ParseIP(host)
			if ip == nil || blockedIP(ip) {
				return fmt.Errorf("%w: %s", ErrBlockedAddress, host)
			}
			return nil
		}
	}
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: webFetchTimeout,
		},
		// Each redirect hop is a fresh URL the model never saw; re-check
		// the scheme on every one so an http(s) start cannot be bounced
		// into file:// or a custom scheme.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return validateFetchURL(req.URL)
		},
	}
}

// validateFetchURL enforces the scheme allowlist. Address-level checks
// happen in the dialer; this is only about the scheme.
func validateFetchURL(u *url.URL) error {
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return nil
	default:
		return fmt.Errorf("url must be http(s), got %q", u.Scheme)
	}
}

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
func WebFetchTool(opts WebFetchOptions) tool.StreamableTool {
	client := newWebFetchClient(opts.AllowPrivateNetwork)
	fn := func(ctx context.Context, in webFetchArgs) (*schema.StreamReader[string], error) {
		return streamWebFetch(ctx, in, client)
	}
	t, err := toolutils.InferStreamTool(
		"web_fetch",
		"Fetches an http(s) URL and streams its body back as text. Use for reading webpages, public docs, or any HTTP-served text content. HTML is reduced to readable text; binary or non-text responses are rejected. Only publicly routable addresses are reachable: loopback, LAN, and link-local addresses are refused.",
		fn,
	)
	if err != nil {
		// InferStreamTool only fails on bad reflection of the args type — a
		// programmer error in this fixed definition.
		panic(err)
	}
	return t
}

func streamWebFetch(ctx context.Context, in webFetchArgs, client *http.Client) (*schema.StreamReader[string], error) {
	if strings.TrimSpace(in.URL) == "" {
		return nil, fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(in.URL))
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if err := validateFetchURL(parsed); err != nil {
		return nil, err
	}
	maxBytes := in.MaxBytes
	if maxBytes <= 0 {
		maxBytes = webFetchDefaultMaxBytes
	}

	fetchCtx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "margo-web-fetch/1.0")
	req.Header.Set("Accept", "text/html,text/plain,application/json,text/*;q=0.9,*/*;q=0.1")

	resp, err := client.Do(req)
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
