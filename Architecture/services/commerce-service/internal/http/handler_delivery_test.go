package http

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeliveryRoutesRequireAuthenticatedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{}).registerDeliveryRoutes(r)
	for _, tc := range []struct{ method, path string }{{"GET", "/profile"}, {"PUT", "/profile"}, {"PUT", "/documents/selfie"}, {"POST", "/submit"}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, "/v1/commerce/delivery"+tc.path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s => %d", tc.method, tc.path, w.Code)
		}
	}
}
