package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// POST /v1/notifications/bundle took user_id and actor_id from the body on a
// route the gateway serves to every signed-in user, with the internal key
// injected for the whole prefix: anyone could send a fake push to anyone. It
// had no caller and was removed (7 Nov 2026). It must stay unrouted.
func TestBundleRouteIsNotRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(nil, nil).RegisterRoutes(r)

	body := `{"user_id":"5d2c6a52-6f0e-4c1e-9a43-2a0b8e0b7f11","actor_id":"0f9a1d7e-3b7c-4f6a-8d2e-9c1b5a4e3d22","bundle_type":"like"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/notifications/bundle", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "0f9a1d7e-3b7c-4f6a-8d2e-9c1b5a4e3d22")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/notifications/bundle answered %d; want 404 or 405 (no such route)", w.Code)
	}

	// Control: the same router does serve a neighbouring route, so the 404
	// above is about /bundle and not an empty router. MarkRead refuses a
	// missing user before touching the service.
	ctl := httptest.NewRequest(http.MethodPost, "/v1/notifications/read", strings.NewReader(`{}`))
	cw := httptest.NewRecorder()
	r.ServeHTTP(cw, ctl)
	if cw.Code != http.StatusUnauthorized {
		t.Fatalf("control POST /v1/notifications/read answered %d; want 401", cw.Code)
	}
}
