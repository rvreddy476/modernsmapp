package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Audit 2026-10-30 L3: every delivery-operations handler stops after a failed
// getUserID. The /v1/commerce/delivery group's middleware refuses first in
// production; this pins the handlers' own backstop by mounting them WITHOUT
// that middleware. A handler that carried on would reach the (nil) service
// with uuid.Nil and panic, or write a second body after the 401.
func TestDeliveryOperationsStopAfterAMissingUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{}).registerDeliveryOperations(r.Group("/bare"), r)
	id := uuid.NewString()
	for _, tc := range []struct{ method, path string }{
		{"GET", "/availability"}, {"PUT", "/availability"}, {"PUT", "/location"},
		{"GET", "/offers"}, {"POST", "/offers/" + id + "/accept"}, {"POST", "/offers/" + id + "/decline"},
		{"GET", "/jobs"}, {"GET", "/jobs/current"}, {"GET", "/jobs/" + id}, {"GET", "/jobs/" + id + "/contact"},
		{"POST", "/jobs/" + id + "/pickup"}, {"POST", "/jobs/" + id + "/deliver"}, {"POST", "/jobs/" + id + "/failed"},
		{"POST", "/jobs/" + id + "/support"}, {"GET", "/earnings"},
		{"PUT", "/orders/" + id + "/location"}, {"GET", "/orders/" + id + "/jobs"},
		{"PUT", "/sellers/" + id + "/location"}, {"GET", "/sellers/" + id + "/location"},
		{"POST", "/seller/jobs/" + id + "/ready"}, {"GET", "/customer/jobs/" + id + "/code"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, "/bare"+tc.path, strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s => %d, want 401", tc.method, tc.path, w.Code)
		}
		if n := strings.Count(w.Body.String(), `"UNAUTHORIZED"`); n != 1 || strings.Count(w.Body.String(), "}{") != 0 {
			t.Fatalf("%s %s wrote more than one response:\n%s", tc.method, tc.path, w.Body.String())
		}
	}
}
