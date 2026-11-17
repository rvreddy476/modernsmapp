package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func probe(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// refuseAll stands in for a router whose every route is gated.
var refuseAll = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
})

func TestLivezNeverRunsADependencyCheck(t *testing.T) {
	c := New("svc", nil)
	ran := false
	c.Register("db", func(context.Context) error { ran = true; return errors.New("down") })

	rec := probe(c.Wrap(refuseAll), http.MethodGet, LivezPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("/livez with a failing dependency: status %d, want 200", rec.Code)
	}
	if ran {
		t.Fatal("/livez ran a dependency check")
	}
}

func TestReadyzIs200WithNoChecksAndWhenAllPass(t *testing.T) {
	c := New("svc", nil)
	if rec := probe(c.Wrap(refuseAll), http.MethodGet, ReadyzPath); rec.Code != http.StatusOK {
		t.Fatalf("/readyz with no checks: status %d, want 200", rec.Code)
	}
	c.Register("db", func(context.Context) error { return nil })
	rec := probe(c.Wrap(refuseAll), http.MethodGet, ReadyzPath)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"db":"up"`) {
		t.Fatalf("/readyz all up: status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestReadyzIs503WhenOneCheckFails(t *testing.T) {
	c := New("svc", nil)
	c.Register("db", func(context.Context) error { return nil })
	c.Register("redis", func(context.Context) error { return errors.New("dial tcp redis.internal:6379: refused") })

	rec := probe(c.Wrap(refuseAll), http.MethodGet, ReadyzPath)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz with redis down: status %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"redis":"down"`) || !strings.Contains(body, `"db":"up"`) {
		t.Fatalf("/readyz body does not name the failing dependency: %s", body)
	}
	if strings.Contains(body, "redis.internal") {
		t.Fatalf("/readyz body leaks the dependency error: %s", body)
	}
}

func TestReadyzTreatsAPanickingCheckAsDown(t *testing.T) {
	c := New("svc", nil)
	c.Register("db", func(context.Context) error { panic("nil pool") })
	if rec := probe(c.Wrap(refuseAll), http.MethodGet, ReadyzPath); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz with a panicking check: status %d, want 503", rec.Code)
	}
}

func TestReadyzGivesChecksADeadline(t *testing.T) {
	c := New("svc", nil)
	c.Register("db", func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("readiness check ran without a deadline")
		}
		return nil
	})
	probe(c.Wrap(refuseAll), http.MethodGet, ReadyzPath)
}

func TestWrapPassesEverythingElseThrough(t *testing.T) {
	c := New("svc", nil)
	h := c.Wrap(refuseAll)
	for _, path := range []string{"/", "/health", "/livez/", "/readyz/x", "/v1/chat/livez"} {
		if rec := probe(h, http.MethodGet, path); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s: status %d, want the inner router's 401", path, rec.Code)
		}
	}
	for _, path := range []string{LivezPath, ReadyzPath} {
		if rec := probe(h, http.MethodPost, path); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status %d, want 405", path, rec.Code)
		}
	}
}
