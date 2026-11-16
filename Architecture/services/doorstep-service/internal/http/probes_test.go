package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/shared/health"
	"github.com/gin-gonic/gin"
)

// downChecker is the shared probe set with a dependency that is down, so a
// 200 on /livez proves liveness runs no dependency check.
func downChecker(name string) *health.Checker {
	c := health.New(name)
	c.Register("postgres", func(context.Context) error { return errors.New("connection refused") })
	return c
}

// assertProbesOpen sends each probe with no credentials at all, as the kubelet
// does (charts/atpost-service/values.yaml probes GET /livez and GET /readyz).
func assertProbesOpen(t *testing.T, r *gin.Engine) {
	t.Helper()
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/livez", http.StatusOK},
		{"/healthz", http.StatusOK},
		// 503 is the dependency check answering; 401 or 404 would be a gate.
		{"/readyz", http.StatusServiceUnavailable},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != tc.want {
			t.Fatalf("GET %s without credentials = %d, want %d", tc.path, w.Code, tc.want)
		}
	}
}

// main registers the shared probes and then the handler, which puts the
// internal key on its own groups. Built in that order over the real route
// table, the probes answer a caller with no key while /v1/doorstep refuses it.
func TestLivezIsOpenWithoutKeyWhileADependencyIsDown(t *testing.T) {
	rg := newRig(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	downChecker("doorstep-service").RegisterRoutes(r)
	New(nil, testInternalKey).WithServiceAuth(rg.v).RegisterRoutes(r)

	assertProbesOpen(t, r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/doorstep/catalogue", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/doorstep/catalogue without key = %d, want 401: the gate is not installed, so this test proves nothing", w.Code)
	}
}
