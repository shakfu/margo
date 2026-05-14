package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebFetchHTMLReduction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><head><style>body{color:red}</style><script>alert(1)</script></head><body><h1>Hello</h1><p>World &amp; friends</p></body></html>`)
	}))
	defer srv.Close()

	sr, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL})
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

	sr, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL})
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

	sr, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL, MaxBytes: 1024})
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

	_, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL})
	if err == nil {
		t.Fatalf("expected error for binary content type")
	}
	if !strings.Contains(err.Error(), "non-text") {
		t.Errorf("error %v should mention non-text content type", err)
	}
}

func TestWebFetchRejectsNonHTTP(t *testing.T) {
	_, err := streamWebFetch(context.Background(), webFetchArgs{URL: "file:///etc/passwd"})
	if err == nil {
		t.Fatalf("expected error for file:// URL")
	}
}

func TestWebFetchRejects4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := streamWebFetch(context.Background(), webFetchArgs{URL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 error, got %v", err)
	}
}
