package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each dependency on its own must turn /readyz to 503 and be named.
func TestReadinessFailsWhenAnyRequiredDependencyIsDown(t *testing.T) {
	up := func(context.Context) error { return nil }
	down := func(context.Context) error { return errors.New("unreachable") }
	notFound := http.NotFoundHandler()

	readyz := func(postgres, redis func(context.Context) error) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		newReadiness(nil, postgres, redis).Wrap(notFound).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return rec
	}

	if rec := readyz(up, up); rec.Code != http.StatusOK {
		t.Fatalf("everything up: status %d, want 200", rec.Code)
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"postgres": readyz(down, up),
		"redis":    readyz(up, down),
	} {
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"`+name+`":"down"`) {
			t.Errorf("%s down: status %d body %s, want 503 naming it", name, rec.Code, rec.Body.String())
		}
	}
}
