package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/google/uuid"
)

// Security sweep #16: the two block-gate refusals keep their own status and
// code on the wire, and neither says "blocked".

type refusingTipService struct{ err error }

func (f refusingTipService) SendTip(context.Context, service.SendTipInput) (*service.TipResult, error) {
	return nil, f.err
}

func TestSendTipBlockRefusalsOnTheWire(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrTipNotAllowed, http.StatusForbidden, "TIP_NOT_ALLOWED"},
		{service.ErrTipUnavailable, http.StatusServiceUnavailable, "TIP_UNAVAILABLE"},
	} {
		h := New(nil).WithWritesEnabled(true)
		h.tips = refusingTipService{tc.err}
		req := httptest.NewRequest(http.MethodPost, "/v1/monetization/tips",
			strings.NewReader(`{"recipient_id":"`+uuid.NewString()+`","amount_paise":500}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", uuid.NewString())
		w := httptest.NewRecorder()
		routerFor(h).ServeHTTP(w, req)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"`+tc.code+`"`) {
			t.Fatalf("%v: %d %s, want %d %s", tc.err, w.Code, w.Body.String(), tc.status, tc.code)
		}
		if strings.Contains(strings.ToLower(w.Body.String()), "block") {
			t.Fatalf("%v: the refusal names the block: %s", tc.err, w.Body.String())
		}
	}
}
