package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// A malformed ?user_id= is refused before the page is read: the handler has
// no store here, so reaching it would panic.
func TestGetPageOwnerRefusesAMalformedUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	r.GET("/internal/pages/:pageId/owner", h.GetPageOwner)

	for _, q := range []string{"not-a-uuid", uuid.Nil.String()} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/internal/pages/"+uuid.NewString()+"/owner?user_id="+q, nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("user_id=%s: status %d, want 400", q, w.Code)
		}
	}
}
