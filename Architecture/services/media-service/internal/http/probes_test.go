package http

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/shared/health"
	"github.com/gin-gonic/gin"
)

// probeEngine is the probe wiring main builds: the shared health routes behind
// this package's request logger, with a dependency that is down.
func probeEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	checker := health.New("media-service")
	checker.Register("postgres", func(context.Context) error { return errors.New("connection refused") })
	r := gin.New()
	r.Use(RequestLoggerMiddleware())
	checker.RegisterRoutes(r)
	r.GET("/v1/media/anything", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

// The kubelet calls GET /livez with no credentials
// (charts/atpost-service/values.yaml). It must be 200 while a dependency is
// down; the same outage shows on /readyz as 503.
func TestLivezIsOpenWhileADependencyIsDown(t *testing.T) {
	r := probeEngine()
	for path, want := range map[string]int{
		"/livez":   http.StatusOK,
		"/healthz": http.StatusOK,
		"/readyz":  http.StatusServiceUnavailable,
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != want {
			t.Fatalf("GET %s without credentials = %d, want %d", path, w.Code, want)
		}
	}
}

// The request logger used to skip only /healthz, so every /livez and /readyz
// probe would have been a log line. All three are skipped; real routes are
// still logged.
func TestRequestLoggerSkipsEveryProbePath(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	r := probeEngine()
	for _, path := range []string{"/livez", "/healthz", "/readyz"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if logs.Len() != 0 {
		t.Fatalf("probe requests were logged: %s", logs.String())
	}

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/media/anything", nil))
	if !strings.Contains(logs.String(), "/v1/media/anything") {
		t.Fatalf("an ordinary request was not logged: %q", logs.String())
	}
}
