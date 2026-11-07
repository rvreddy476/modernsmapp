package http

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Each handler refuses on its own, wherever it is mounted: with the group's
// middleware out of the way, a request with no verified user is a 401 and
// never reaches the service (a nil service here, so reaching it would panic).
func TestDeliveryHandlersRefuseWithoutIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{}
	r := gin.New()
	r.GET("/get", h.GetDeliveryProfile)
	r.PUT("/save", h.SaveDeliveryProfile)
	r.PUT("/doc/:kind", h.SaveDeliveryDocument)
	r.POST("/submit", h.SubmitDeliveryProfile)
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/get", ""},
		{"PUT", "/save", `{"full_name":"A","vehicle_type":"bike"}`},
		{"PUT", "/doc/selfie", `{"media_id":"00000000-0000-0000-0000-000000000001"}`},
		{"POST", "/submit", ""},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s => %d, want 401", tc.method, tc.path, w.Code)
			}
		})
	}
}
