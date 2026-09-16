package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// guardEngine builds the production route table over a store with NO
// database. A request that got past the guards into a handler that writes
// would dereference the nil pool and fail the test, so a clean 4xx/5xx here
// is also proof nothing was written.
func guardEngine(t *testing.T, key string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(postgres.New(nil), nil, ""))
	if key != "" {
		h = h.WithInternalKey(key)
	}
	h.RegisterRoutes(r)
	return r
}

func serve(r *gin.Engine, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

var someID = uuid.NewString()

// One representative route per file that registers /v1/commerce/internal
// routes, plus the money route.
var guardedRoutes = []struct{ method, path string }{
	{http.MethodPost, "/v1/commerce/internal/sellers/" + someID + "/approve"},        // handler_onboarding.go
	{http.MethodPost, "/v1/commerce/internal/cod-remittances/" + someID + "/settle"}, // handler.go (registered in onboarding)
	{http.MethodPost, "/v1/commerce/internal/banners"},                               // handler_storefront.go
	{http.MethodGet, "/v1/commerce/internal/compliance-gaps"},                        // handler_submissions.go
	{http.MethodGet, "/v1/commerce/internal/payouts/pending"},                        // read, still guarded
	{http.MethodPost, "/v1/commerce/internal/attribute-schema/publish"},              // catalogue authoring
}

func TestInternalRoutesFailClosedWithoutAKey(t *testing.T) {
	r := guardEngine(t, "")
	for _, rt := range guardedRoutes {
		// Even a caller presenting some key, and an actor, gets 503.
		w := serve(r, rt.method, rt.path, map[string]string{
			InternalServiceKeyHeader: "anything",
			"X-User-Id":              uuid.NewString(),
		})
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), CodeInternalKeyNotConfigured) {
			t.Errorf("%s %s with no configured key: %d %s, want 503 %s",
				rt.method, rt.path, w.Code, w.Body.String(), CodeInternalKeyNotConfigured)
		}
		// And with no key header at all.
		if w := serve(r, rt.method, rt.path, nil); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s with no configured key and no header: %d, want 503", rt.method, rt.path, w.Code)
		}
	}
}

func TestInternalRoutesRefuseAWrongOrMissingKey(t *testing.T) {
	r := guardEngine(t, "right-key")
	for _, rt := range guardedRoutes {
		for name, hdr := range map[string]map[string]string{
			"wrong key":   {InternalServiceKeyHeader: "wrong-key", "X-User-Id": uuid.NewString()},
			"prefix key":  {InternalServiceKeyHeader: "right-ke", "X-User-Id": uuid.NewString()},
			"missing key": {"X-User-Id": uuid.NewString()},
		} {
			if w := serve(r, rt.method, rt.path, hdr); w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s (%s): %d %s, want 401", rt.method, rt.path, name, w.Code, w.Body.String())
			}
		}
	}
}

// Every human admin action that writes refuses a missing, malformed or nil
// actor before touching the store.
func TestHumanAdminActionsRequireARealActor(t *testing.T) {
	r := guardEngine(t, "right-key")
	actions := []struct{ method, path string }{
		{http.MethodPost, "/v1/commerce/internal/sellers/" + someID + "/approve"},
		{http.MethodPost, "/v1/commerce/internal/sellers/" + someID + "/reject"},
		{http.MethodPost, "/v1/commerce/internal/sellers/" + someID + "/request-changes"},
		{http.MethodPost, "/v1/commerce/internal/sellers/" + someID + "/suspend"},
		{http.MethodPost, "/v1/commerce/internal/sellers/" + someID + "/unsuspend"},
		{http.MethodPost, "/v1/commerce/internal/sellers/" + someID + "/kyc/verify"},
		{http.MethodPost, "/v1/commerce/internal/products/" + someID + "/approve"},
		{http.MethodPost, "/v1/commerce/internal/products/" + someID + "/reject"},
		{http.MethodPost, "/v1/commerce/internal/products/" + someID + "/request-changes"},
		{http.MethodPost, "/v1/commerce/internal/cod-remittances/" + someID + "/settle"},
		{http.MethodPost, "/v1/commerce/internal/banners"},
		{http.MethodPut, "/v1/commerce/internal/banners/" + someID},
		{http.MethodDelete, "/v1/commerce/internal/banners/" + someID},
	}
	for _, a := range actions {
		for name, actor := range map[string]string{
			"missing":   "",
			"nil uuid":  uuid.Nil.String(),
			"malformed": "not-a-uuid",
		} {
			hdr := map[string]string{InternalServiceKeyHeader: "right-key"}
			if actor != "" {
				hdr["X-User-Id"] = actor
			}
			w := serve(r, a.method, a.path, hdr)
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), CodeActorRequired) {
				t.Errorf("%s %s (%s actor): %d %s, want 400 %s",
					a.method, a.path, name, w.Code, w.Body.String(), CodeActorRequired)
			}
		}
	}
}
