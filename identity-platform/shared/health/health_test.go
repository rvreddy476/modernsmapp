package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func get(r http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func deny(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }

func TestLivezAndReadyzAnswer200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	c := New("svc", nil)
	c.Register("postgres", func(context.Context) error { return nil })
	c.RegisterRoutes(r)

	for _, path := range []string{LivezPath, ReadyzPath} {
		if w := get(r, path); w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, w.Code)
		}
	}
}

func TestReadyzIs503AndLivezStays200WhenACheckFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	c := New("svc", nil)
	c.Register("postgres", func(context.Context) error { return errors.New("dial host=db.internal user=svc: refused") })
	c.Register("redis", func(context.Context) error { return nil })
	c.RegisterRoutes(r)

	w := get(r, ReadyzPath)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d, want 503", w.Code)
	}
	var body Result
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Checks["postgres"] != "down" || body.Checks["redis"] != "up" {
		t.Fatalf("checks = %v, want postgres down and redis up", body.Checks)
	}
	if strings.Contains(w.Body.String(), "db.internal") {
		t.Fatalf("the unauthenticated body carries the driver error: %s", w.Body.String())
	}
	if w := get(r, LivezPath); w.Code != http.StatusOK {
		t.Fatalf("GET /livez = %d, want 200: liveness must not depend on a check", w.Code)
	}
}

// Middleware registered BEFORE the probes must not be in their chain.
func TestProbesIgnoreMiddlewareRegisteredBefore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(deny)
	New("svc", nil).RegisterRoutes(r)

	for _, path := range []string{LivezPath, ReadyzPath} {
		if w := get(r, path); w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 behind an earlier r.Use", path, w.Code)
		}
	}
	// The gate itself still stands for everything else.
	r.GET("/other", func(c *gin.Context) { c.Status(http.StatusOK) })
	if w := get(r, "/other"); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /other = %d, want 401", w.Code)
	}
}

func TestProbesIgnoreMiddlewareRegisteredAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New("svc", nil).RegisterRoutes(r)
	r.Use(deny)

	for _, path := range []string{LivezPath, ReadyzPath} {
		if w := get(r, path); w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 with a later r.Use", path, w.Code)
		}
	}
}

// A deadline already expired on the request must not fail the checks.
func TestReadyzDoesNotInheritTheRequestDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	c := New("svc", nil)
	c.Register("postgres", func(ctx context.Context) error { return ctx.Err() })
	c.RegisterRoutes(r)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, ReadyzPath, nil).WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /readyz = %d, want 200", w.Code)
	}
}
