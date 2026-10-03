package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	aihttp "github.com/atpost/ai-service/internal/http"
	"github.com/atpost/shared/health"
	"github.com/atpost/shared/o11y/metrics"
	"github.com/gin-gonic/gin"
)

const testInternalKey = "ai-router-test-key"

// testRouter builds the production router with the real health and metrics
// routes. The service is nil: GET /v1/ai/jobs/<not-a-uuid> answers 400 from
// the handler before it touches the service, so reaching the handler at all
// is the proof that the key gate let the request through.
func testRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	checker := health.New("ai-service")
	return newRouter(aihttp.New(nil), testInternalKey, nil, func(r *gin.Engine) {
		checker.RegisterRoutes(r)
		r.GET("/metrics", metrics.Handler())
	})
}

func serve(r http.Handler, path, key string) int {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRouterRequiresInternalKeyOnAIRoutes(t *testing.T) {
	r := testRouter(t)
	const path = "/v1/ai/jobs/not-a-uuid"

	if got := serve(r, path, ""); got != http.StatusUnauthorized {
		t.Fatalf("no key: status=%d, want 401", got)
	}
	if got := serve(r, path, "wrong-key"); got != http.StatusUnauthorized {
		t.Fatalf("wrong key: status=%d, want 401", got)
	}
	if got := serve(r, path, testInternalKey); got != http.StatusBadRequest {
		t.Fatalf("right key: status=%d, want 400 from the handler (request admitted)", got)
	}
}

func TestRouterLeavesHealthAndMetricsOpen(t *testing.T) {
	r := testRouter(t)
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		if got := serve(r, path, ""); got != http.StatusOK {
			t.Errorf("%s without key: status=%d, want 200", path, got)
		}
	}
}

func TestInternalKeyFromEnvFailsClosedInProduction(t *testing.T) {
	for _, env := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		for _, k := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
			t.Setenv(k, "")
		}
		t.Setenv(env, "prod")
		t.Setenv("INTERNAL_SERVICE_KEY", "")
		if _, err := internalKeyFromEnv(); err == nil {
			t.Fatalf("%s=prod with no key: want an error", env)
		}
		t.Setenv("INTERNAL_SERVICE_KEY", testInternalKey)
		if key, err := internalKeyFromEnv(); err != nil || key != testInternalKey {
			t.Fatalf("%s=prod with key: key=%q err=%v", env, key, err)
		}
	}

	t.Setenv("APP_ENV", "")
	t.Setenv("ENVIRONMENT", "")
	t.Setenv("ENV", "development")
	t.Setenv("INTERNAL_SERVICE_KEY", "")
	if key, err := internalKeyFromEnv(); err != nil || key != "" {
		t.Fatalf("development with no key: key=%q err=%v, want empty and no error", key, err)
	}
}
