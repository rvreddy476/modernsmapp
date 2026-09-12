package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
)

// Tube channel subscriptions (2026-09-12): the routes exist, the static
// /subscriptions list is not swallowed by /:ref, and the bell refuses every
// value the fan-out does not read.

func TestChannelSubscriptionRoutesRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{}).RegisterRoutes(r)
	want := map[string]bool{
		"POST /v1/channels/:ref/subscribe":                 false,
		"DELETE /v1/channels/:ref/subscribe":               false,
		"GET /v1/channels/:ref/subscription":               false,
		"PATCH /v1/channels/:ref/subscription":             false,
		"GET /v1/channels/subscriptions":                   false,
		"GET /internal/channels/by-owner/:userId":          false,
		"GET /internal/channels/:channelId/subscriber-ids": false,
		"GET /internal/users/:userId/subscribed-owner-ids": false,
	}
	for _, info := range r.Routes() {
		key := info.Method + " " + info.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, seen := range want {
		if !seen {
			t.Errorf("route %q not registered", route)
		}
	}
}

// GET /v1/channels/subscriptions must dispatch to the list handler, not to
// GetChannelByRef with ref="subscriptions". The list handler demands a
// caller (401 without X-User-Id); the by-ref handler on an unwired service
// answers 500, so the status tells the two apart without a store.
func TestSubscriptionsListIsNotCapturedByRef(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(&service.Service{}, nil).RegisterRoutes(r)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/channels/subscriptions", nil))
	if rec.Code != http.StatusUnauthorized || errorCode(t, rec) != "UNAUTHORIZED" {
		t.Fatalf("GET /v1/channels/subscriptions without a caller: %d %s (want 401 UNAUTHORIZED from the list handler)", rec.Code, rec.Body.String())
	}
}

// The bell accepts 'all' and 'none' only. 'highlights' (the retired tier)
// and 'uploads' (the value user-service's fan-out compared against, which
// the CHECK never allowed) are 400 before any store or graph call.
func TestNotifyOnRejectsRetiredAndNeverValidValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(&service.Service{}, nil).RegisterRoutes(r)

	for _, tc := range []struct {
		method, path, body string
	}{
		{http.MethodPatch, "/v1/channels/call.b/subscription", `{"notify_on":"highlights"}`},
		{http.MethodPatch, "/v1/channels/call.b/subscription", `{"notify_on":"uploads"}`},
		{http.MethodPatch, "/v1/channels/call.b/subscription", `{}`},
		{http.MethodPost, "/v1/channels/call.b/subscribe", `{"notify_on":"highlights"}`},
		{http.MethodPost, "/v1/channels/call.b/subscribe", `{"notify_on":"uploads"}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-Id", "11111111-1111-4111-8111-111111111111")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_NOTIFY_ON" {
			t.Errorf("%s %s %s: got %d %s, want 400 INVALID_NOTIFY_ON", tc.method, tc.path, tc.body, rec.Code, rec.Body.String())
		}
	}
}

func TestWriteSubscriptionErrorCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{service.ErrCannotSubscribeSelf, http.StatusBadRequest, "CANNOT_SUBSCRIBE_SELF"},
		{service.ErrInvalidNotifyOn, http.StatusBadRequest, "INVALID_NOTIFY_ON"},
		{service.ErrChannelNotFound, http.StatusNotFound, "NOT_FOUND"},
		{service.ErrNotSubscribed, http.StatusNotFound, "NOT_SUBSCRIBED"},
		{service.ErrGraphUnavailable, http.StatusBadGateway, "GRAPH_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/channels/x/subscribe", nil)
			if !writeSubscriptionError(c, tc.err) {
				t.Fatalf("%v not handled", tc.err)
			}
			if rec.Code != tc.status || errorCode(t, rec) != tc.code {
				t.Fatalf("got %d %s want %d %s", rec.Code, errorCode(t, rec), tc.status, tc.code)
			}
		})
	}
}
