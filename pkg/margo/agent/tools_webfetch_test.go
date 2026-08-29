package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWebFetchHTMLReduction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><style>body{color:red}</style><script>alert(1)</script></head><body><h1>Hello</h1><p>World &amp; friends</p></body></html>`)
	}))
	defer srv.Close()

	sr, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL}, testFetchClient())
	if err != nil {
		t.Fatalf("streamWebFetch: %v", err)
	}
	defer sr.Close()

	var b strings.Builder
	for {
		chunk, rerr := sr.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			t.Fatalf("Recv: %v", rerr)
		}
		b.WriteString(chunk)
	}
	got := b.String()
	if strings.Contains(got, "<script") || strings.Contains(got, "alert") {
		t.Errorf("script content survived reduction: %q", got)
	}
	if strings.Contains(got, "<style") || strings.Contains(got, "color:red") {
		t.Errorf("style content survived reduction: %q", got)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "World & friends") {
		t.Errorf("expected body text and decoded entity, got %q", got)
	}
}

func TestWebFetchStreamsPlainText(t *testing.T) {
	// Build a body large enough to span multiple chunkBytes boundaries.
	body := strings.Repeat("abcdefghij", 2000) // 20KB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	sr, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL}, testFetchClient())
	if err != nil {
		t.Fatalf("streamWebFetch: %v", err)
	}
	defer sr.Close()

	var b strings.Builder
	chunks := 0
	for {
		chunk, rerr := sr.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			t.Fatalf("Recv: %v", rerr)
		}
		b.WriteString(chunk)
		chunks++
	}
	if b.String() != body {
		t.Errorf("body mismatch: got %d bytes, want %d", b.Len(), len(body))
	}
	if chunks < 2 {
		t.Errorf("expected multiple chunks for a 20KB body, got %d", chunks)
	}
}

func TestWebFetchTruncates(t *testing.T) {
	body := strings.Repeat("x", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	sr, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL, MaxBytes: 1024}, testFetchClient())
	if err != nil {
		t.Fatalf("streamWebFetch: %v", err)
	}
	defer sr.Close()

	var b strings.Builder
	for {
		chunk, rerr := sr.Recv()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			t.Fatalf("Recv: %v", rerr)
		}
		b.WriteString(chunk)
	}
	if !strings.Contains(b.String(), "[truncated at 1024 bytes]") {
		t.Errorf("expected truncation marker, got %q", b.String())
	}
	// xxx body content alone should be exactly 1024 'x'.
	xs := strings.Count(b.String(), "x")
	if xs != 1024 {
		t.Errorf("expected 1024 'x' chars before marker, got %d", xs)
	}
}

func TestWebFetchRejectsBinaryContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte{0x00, 0x01, 0x02})
	}))
	defer srv.Close()

	_, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL}, testFetchClient())
	if err == nil {
		t.Fatalf("expected error for binary content type")
	}
	if !strings.Contains(err.Error(), "non-text") {
		t.Errorf("error %v should mention non-text content type", err)
	}
}

func TestWebFetchRejectsNonHTTP(t *testing.T) {
	_, err := streamWebFetch(context.Background(), webFetchArgs{URL: "file:///etc/passwd"}, testFetchClient())
	if err == nil {
		t.Fatalf("expected error for file:// URL")
	}
}

func TestWebFetchRejects4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL}, testFetchClient())
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}

// testFetchClient permits loopback so the httptest-backed tests above can
// reach their own server. Production callers get the guarded client.
func testFetchClient() *http.Client {
	return newWebFetchClient(true)
}

// TestWebFetchBlocksLoopback is the SSRF regression net: the default
// client must refuse the very server the other tests rely on reaching.
func TestWebFetchBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()

	_, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL}, newWebFetchClient(false))
	if err == nil {
		t.Fatal("expected loopback fetch to be refused")
	}
	if !strings.Contains(err.Error(), "non-public address") {
		t.Fatalf("got %v, want a blocked-address error", err)
	}
}

func TestBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.0.0.5", "172.16.0.1", "192.168.1.1",
		"169.254.169.254", // cloud metadata
		"0.0.0.0", "100.64.0.1", "fd00::1", "224.0.0.1",
	}
	for _, in := range blocked {
		if ip := net.ParseIP(in); ip == nil || !blockedIP(ip) {
			t.Errorf("blockedIP(%s) = false, want true", in)
		}
	}
	allowed := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700::1111"}
	for _, in := range allowed {
		if ip := net.ParseIP(in); ip == nil || blockedIP(ip) {
			t.Errorf("blockedIP(%s) = true, want false", in)
		}
	}
}

func TestValidateFetchURLRejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "gopher://x/", "ftp://x/"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %s: %v", raw, err)
		}
		if err := validateFetchURL(u); err == nil {
			t.Errorf("validateFetchURL(%s) = nil, want error", raw)
		}
	}
}
