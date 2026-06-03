package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleProbeLivez(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/livez", nil)
	if !handleProbe(rec, req, 0) {
		t.Fatal("/livez should be served by handleProbe")
	}
	if rec.Code != 200 {
		t.Errorf("livez code=%d want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"alive"`) {
		t.Errorf("livez body=%q want alive", rec.Body.String())
	}
}

func TestHandleProbeReadyz(t *testing.T) {
	t.Run("ready when routes registered", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/readyz", nil)
		if !handleProbe(rec, req, 5) {
			t.Fatal("/readyz should be served")
		}
		if rec.Code != 200 {
			t.Errorf("readyz code=%d want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"routes":5`) {
			t.Errorf("readyz body=%q want routes:5", rec.Body.String())
		}
	})
	t.Run("503 when no routes", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/readyz", nil)
		if !handleProbe(rec, req, 0) {
			t.Fatal("/readyz should be served")
		}
		if rec.Code != 503 {
			t.Errorf("readyz code=%d want 503", rec.Code)
		}
	})
}

func TestHandleProbeHealthAlias(t *testing.T) {
	for _, path := range []string{"/health", "/v1/health"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		if !handleProbe(rec, req, 1) {
			t.Fatalf("%s should be served", path)
		}
		if rec.Code != 200 {
			t.Errorf("%s code=%d want 200", path, rec.Code)
		}
	}
}

func TestHandleProbePassthrough(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/posts", nil)
	if handleProbe(rec, req, 5) {
		t.Fatal("non-probe paths should not be served by handleProbe")
	}
}
