package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRealtimeBadTokenIsGeneric(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewRealtimeHandler([]byte("0123456789abcdef0123456789abcdef"), nil)
	r := gin.New()
	h.Register(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/realtime/sse?token=not.a.jwt", nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	body := strings.ToLower(w.Body.String())
	for _, leak := range []string{"segments", "malformed", "signature", "jwt", "token is"} {
		if strings.Contains(body, leak) {
			t.Fatalf("body leaks verifier text %q: %s", leak, body)
		}
	}
}
