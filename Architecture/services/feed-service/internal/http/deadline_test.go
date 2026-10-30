package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// Audit L7: every feed request runs under an overall deadline.
func TestRequestDeadline_EveryRouteHasOne(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(withRequestDeadline(2 * time.Second))
	var left time.Duration
	var had bool
	r.GET("/x", func(c *gin.Context) {
		var dl time.Time
		dl, had = c.Request.Context().Deadline()
		left = time.Until(dl)
		c.Status(http.StatusOK)
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !had || left > 2*time.Second || left < time.Second {
		t.Fatalf("deadline present=%v in %v, want about 2s", had, left)
	}
}

func TestRequestDeadline_Env(t *testing.T) {
	t.Setenv("FEED_REQUEST_DEADLINE", "")
	if requestDeadline() != DefaultRequestDeadline {
		t.Fatal("unset FEED_REQUEST_DEADLINE must use the default")
	}
	t.Setenv("FEED_REQUEST_DEADLINE", "4s")
	if requestDeadline() != 4*time.Second {
		t.Fatal("FEED_REQUEST_DEADLINE=4s ignored")
	}
}

func TestRegisterRoutes_InstallsTheDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil).RegisterRoutes(r)
	// The deadline middleware is on the engine: a /v1/feed route answers
	// under a context with a deadline. legacyDebugGone needs no service.
	var had bool
	r.GET("/probe", func(c *gin.Context) { _, had = c.Request.Context().Deadline() })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))
	if !had {
		t.Fatal("RegisterRoutes does not put a deadline on requests")
	}
}
