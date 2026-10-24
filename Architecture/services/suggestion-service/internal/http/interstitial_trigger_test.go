package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The friend_accept interstitial was the pop-up after accepting a connection
// request. Connect was retired on 24 Oct 2026 and graph-service answers 410
// to an accept, so this answers 410 too, before any service call (the
// Handler here has no service: reaching it would panic).
func TestInterstitialTriggerValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	for _, c := range []struct {
		trigger string
		want    int
	}{
		{"friend_accept", http.StatusGone},
		{"", http.StatusBadRequest},
		{"connect", http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest(http.MethodGet,
			"/v1/suggestions/interstitial?trigger_type="+c.trigger+"&trigger_user_id="+uuid.NewString(), nil)
		ctx.Request.Header.Set("X-User-Id", uuid.NewString())
		h.GetInterstitialSuggestions(ctx)
		if w.Code != c.want {
			t.Errorf("trigger_type=%q: status %d, want %d (%s)", c.trigger, w.Code, c.want, w.Body.String())
		}
	}
}
