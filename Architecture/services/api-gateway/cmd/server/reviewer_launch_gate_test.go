package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReviewerLaunchGateDefaultResponseContract(t *testing.T) {
	// Pin the exact response emitted by the core handler branch without
	// requiring a reviewer upstream. This fails if the route silently falls
	// through to a missing deployment and becomes a generic 502.
	path := "/v1/reviewer/assignments/next"
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if !serveReviewerLaunchGate(res, req, false) {
		t.Fatal("reviewer path was not handled")
	}
	if res.Code != http.StatusServiceUnavailable || !bytes.Contains(res.Body.Bytes(), []byte("REVIEWER_PROGRAM_UNAVAILABLE")) {
		t.Fatalf("reviewer launch gate = %d %q", res.Code, res.Body.String())
	}

	next := httptest.NewRecorder()
	if serveReviewerLaunchGate(next, req, true) {
		t.Fatal("enabled reviewer route was intercepted")
	}
}
