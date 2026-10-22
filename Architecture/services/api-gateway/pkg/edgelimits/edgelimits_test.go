package edgelimits

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Security sweep #18: the server has a header timeout, an idle timeout and a
// header cap — and NOT a read/write timeout, which would cut off long
// uploads, SSE, websockets and proxied HLS.
func TestNewServerHasTheTimeouts(t *testing.T) {
	h := http.NotFoundHandler()
	s := NewServer(":0", h)
	if s.ReadHeaderTimeout <= 0 || s.ReadHeaderTimeout > 30*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want a short positive bound", s.ReadHeaderTimeout)
	}
	if s.IdleTimeout <= 0 {
		t.Fatalf("IdleTimeout = %v, want positive", s.IdleTimeout)
	}
	if s.MaxHeaderBytes <= 0 {
		t.Fatalf("MaxHeaderBytes = %d, want positive", s.MaxHeaderBytes)
	}
	if s.ReadTimeout != 0 || s.WriteTimeout != 0 {
		t.Fatalf("ReadTimeout=%v WriteTimeout=%v: a whole-body timeout breaks uploads and streams", s.ReadTimeout, s.WriteTimeout)
	}
	if s.Addr != ":0" || s.Handler == nil {
		t.Fatalf("addr/handler not set: %q %v", s.Addr, s.Handler)
	}
}

// readAll is a stand-in upstream: it reads the whole body and reports how
// much arrived, or 400 on a read error.
var readAll = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	n, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("X-Read", strconv.FormatInt(n, 10))
	w.WriteHeader(http.StatusOK)
})

func post(h http.Handler, path string, body io.Reader, length int64) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, body)
	req.ContentLength = length
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func wantTooLarge(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%s: status %d, want 413 (%s)", what, rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), Code) {
		t.Fatalf("%s: body %q does not carry %s", what, rec.Body.String(), Code)
	}
}

// A declared Content-Length over the cap on a non-upload route is refused
// before anything reads it.
func TestDeclaredBodyOverTheCapIsRefused(t *testing.T) {
	const max = 1 << 10
	h := LimitBody(max, readAll)
	wantTooLarge(t, "declared", post(h, "/v1/posts", bytes.NewReader(make([]byte, max+1)), max+1))
	if rec := post(h, "/v1/posts", bytes.NewReader(make([]byte, max)), max); rec.Code != http.StatusOK {
		t.Fatalf("a body of exactly the cap: %d", rec.Code)
	}
}

// A chunked body (no declared length) over the cap cannot be read past it:
// the upstream's read fails.
func TestChunkedBodyOverTheCapFailsToRead(t *testing.T) {
	const max = 1 << 10
	h := LimitBody(max, readAll)
	rec := post(h, "/v1/graph/follow", bytes.NewReader(make([]byte, 4*max)), -1)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("chunked over cap: status %d, want the upstream's read error (400)", rec.Code)
	}
}

// Upload routes are exempt: their upstream bounds them, and a 64 MiB chunk
// must pass the edge.
func TestUploadRoutesAreNotCapped(t *testing.T) {
	const max = 1 << 10
	h := LimitBody(max, readAll)
	for _, path := range []string{
		"/v1/media/upload/resumable/abc/chunk",
		"/v1/media/123/audio-tracks",
		"/v1/audio/voiceover",
		"/v1/subtitles/123",
		"/v1/uploads",
	} {
		if rec := post(h, path, bytes.NewReader(make([]byte, 4*max)), 4*max); rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d, want 200 (upload routes are exempt)", path, rec.Code)
		}
		if rec := post(h, path, bytes.NewReader(make([]byte, 4*max)), -1); rec.Code != http.StatusOK {
			t.Fatalf("%s chunked: status %d, want 200", path, rec.Code)
		}
	}
	// Segment boundary: neighbours of an exempt prefix are capped.
	for _, path := range []string{"/v1/audio-library/1/use", "/v1/mediax", "/v1/uploadsx"} {
		wantTooLarge(t, path, post(h, path, bytes.NewReader(make([]byte, 4*max)), 4*max))
	}
}

// Through a real reverse proxy, a chunked body over the cap answers 413
// (ProxyErrorHandler), not the default 502; a real upstream failure is
// still 502.
func TestProxyAnswers413ForAnOversizedChunkedBody(t *testing.T) {
	const max = 1 << 10
	upstream := httptest.NewServer(readAll)
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = ProxyErrorHandler
	h := LimitBody(max, proxy)

	wantTooLarge(t, "proxied chunked", post(h, "/v1/posts", bytes.NewReader(make([]byte, 64*max)), -1))
	if rec := post(h, "/v1/posts", bytes.NewReader(make([]byte, max/2)), -1); rec.Code != http.StatusOK {
		t.Fatalf("proxied small chunked body: %d", rec.Code)
	}

	dead := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "127.0.0.1:1"})
	dead.ErrorHandler = ProxyErrorHandler
	if rec := post(LimitBody(max, dead), "/v1/posts", bytes.NewReader([]byte("{}")), 2); rec.Code != http.StatusBadGateway {
		t.Fatalf("unreachable upstream: %d, want 502", rec.Code)
	}
}
