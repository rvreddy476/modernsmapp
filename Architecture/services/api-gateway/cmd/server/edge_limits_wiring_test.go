package main

// NOTE: new files under cmd/server/ were git-ignored by the root `server` rule
// before the `!server/` exception; add with `git add -f` if in doubt.

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/atpost/api-gateway/pkg/edgelimits"
)

// Security sweep #18: every proxied route answers 413 — not the reverse
// proxy's default 502 — when a chunked body goes over the edge cap, because
// newRoute installs edgelimits.ProxyErrorHandler.
func TestProxiedRouteAnswers413ForAnOversizedBody(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	rt := newRoute("/v1/posts", target, "")
	if rt.proxy.ErrorHandler == nil {
		t.Fatal("newRoute leaves the default proxy error handler (502 for an oversized body)")
	}
	const max = 1 << 10
	h := edgelimits.LimitBody(max, rt.proxy)
	req := httptest.NewRequest(http.MethodPost, "/v1/posts", bytes.NewReader(make([]byte, 64*max)))
	req.ContentLength = -1
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunked body through a route: %d, want 413 (%s)", rec.Code, rec.Body.String())
	}
}
