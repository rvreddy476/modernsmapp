package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The stub must never allow: every media-access question is a 404, which
// media-service reads as a clean deny (delivery/authz.go).
func TestDeniesEveryMediaAccessPath(t *testing.T) {
	h := handler()
	for _, path := range []string{
		"/internal/v1/chat/media-access",
		"/v1/commerce/internal/media-access",
		"/v1/internal/groups/media-access",
		"/v1/internal/media-access",
		"/anything",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: got %d, want 404", path, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/health: got %d, want 200", rec.Code)
	}
}

func TestPortsDefault(t *testing.T) {
	t.Setenv("E2E_DENY_PORTS", "")
	if got := ports(); len(got) != 3 || got[0] != "8092" {
		t.Fatalf("ports() = %v", got)
	}
}
