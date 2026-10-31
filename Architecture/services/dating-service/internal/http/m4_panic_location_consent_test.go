package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Audit 2026-10-30 M4 — request-shape refusals on PATCH
// /v1/dating/safety/trusted-contacts/:contactId. These return before the
// store is reached, so no database is needed.

func patchTrustedContact(body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(nil, nil)).RegisterRoutes(r)
	req := httptest.NewRequest(http.MethodPatch, "/v1/dating/safety/trusted-contacts/"+uuid.NewString(), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestPatchTrustedContact_SharingNeedsCurrentConsentVersion(t *testing.T) {
	for _, body := range []string{
		`{"share_location_on_panic":true}`,
		`{"share_location_on_panic":true,"consent_version":""}`,
		`{"share_location_on_panic":true,"consent_version":"panic_location_v0"}`,
		`{"share_location_on_panic":true,"consent_version":"accepted"}`,
	} {
		w := patchTrustedContact(body)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status %d body %s, want 422", body, w.Code, w.Body.String())
		}
		var env struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if env.Error.Code != "PANIC_LOCATION_CONSENT_REQUIRED" ||
			env.Error.Details["consent_version"] != store.PanicLocationConsentVersion ||
			env.Error.Details["consent_text"] != store.PanicLocationConsentCopy {
			t.Fatalf("%s: body %s, want PANIC_LOCATION_CONSENT_REQUIRED with the current copy", body, w.Body.String())
		}
	}
}

func TestPatchTrustedContact_FlagIsRequired(t *testing.T) {
	for _, body := range []string{`{}`, `{"consent_version":"` + store.PanicLocationConsentVersion + `"}`} {
		w := patchTrustedContact(body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"INVALID_REQUEST"`) {
			t.Fatalf("%s: status %d body %s, want 400 INVALID_REQUEST", body, w.Code, w.Body.String())
		}
	}
	if w := patchTrustedContact(`{"share_location_on_panic":"yes"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: status %d, want 400", w.Code)
	}
}
