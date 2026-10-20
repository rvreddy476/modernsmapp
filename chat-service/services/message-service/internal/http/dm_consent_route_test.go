package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DM consent model route tests: the accepted-chat state route's gates and
// shape, the create-conversation error body with graph's reason, and the
// requests list / count shapes.

const directChatStatePath = "/internal/v1/chat/direct-chat/state"

// consentStub implements only what these routes call; anything else panics
// through the nil embedded interface.
type consentStub struct {
	ChatService
	mu        sync.Mutex
	pairs     [][2]uuid.UUID
	accepted  bool
	createErr error
	page      *service.RequestPage
	gotQuery  service.RequestListQuery
	counts    *service.RequestCounts
}

func (s *consentStub) DirectChatAccepted(_ context.Context, a, b uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pairs = append(s.pairs, [2]uuid.UUID{a, b})
	return s.accepted, nil
}

func (s *consentStub) CreateDirectConversation(context.Context, uuid.UUID, uuid.UUID, string) (*service.ConversationResponse, error) {
	return nil, s.createErr
}

func (s *consentStub) ListRequestsPage(_ context.Context, _ uuid.UUID, q service.RequestListQuery) (*service.RequestPage, error) {
	s.gotQuery = q
	return s.page, nil
}

func (s *consentStub) CountRequests(context.Context, uuid.UUID) (*service.RequestCounts, error) {
	return s.counts, nil
}

func newConsentRouter(t *testing.T, svc ChatService, key string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := gin.New()
	router.Use(AuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: datingMatchTestSecret, InternalServiceKey: datingMatchTestKey}, log))
	New(svc, log).WithInternalServiceKey(key).RegisterRoutes(router)
	return router
}

func TestDirectChatStateGates(t *testing.T) {
	stub := &consentStub{accepted: true}
	router := newConsentRouter(t, stub, datingMatchTestKey)
	a, b := uuid.New(), uuid.New()
	body := `{"user_a":"` + a.String() + `","user_b":"` + b.String() + `"}`

	if got := postDatingMatch(router, directChatStatePath, body, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("no key: %d %s", got.Code, got.Body.String())
	}
	if got := postDatingMatch(router, directChatStatePath, body, map[string]string{"X-Internal-Service-Key": "wrong"}); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key: %d %s", got.Code, got.Body.String())
	}
	if got := postDatingMatch(router, directChatStatePath, body, map[string]string{"Authorization": "Bearer " + userToken(t, a)}); got.Code == http.StatusOK {
		t.Fatalf("a user token must never read the state: %d %s", got.Code, got.Body.String())
	}
	if got := postDatingMatch(router, directChatStatePath, body, map[string]string{
		"X-Internal-Service-Key": datingMatchTestKey, "X-User-Id": a.String()}); got.Code != http.StatusForbidden {
		t.Fatalf("key plus user identity: %d %s", got.Code, got.Body.String())
	}
	if len(stub.pairs) != 0 {
		t.Fatal("a refused request reached the service")
	}
	unconfigured := newConsentRouter(t, stub, "")
	if got := postDatingMatch(unconfigured, directChatStatePath, body, map[string]string{"X-Internal-Service-Key": ""}); got.Code == http.StatusOK {
		t.Fatalf("unconfigured key must fail closed: %d", got.Code)
	}
	for _, bad := range []string{`{}`, `{"user_a":"x","user_b":"` + b.String() + `"}`,
		`{"user_a":"00000000-0000-0000-0000-000000000000","user_b":"` + b.String() + `"}`} {
		if got := postDatingMatch(router, directChatStatePath, bad, map[string]string{"X-Internal-Service-Key": datingMatchTestKey}); got.Code != http.StatusBadRequest {
			t.Fatalf("bad body %s: %d", bad, got.Code)
		}
	}
}

func TestDirectChatStateShape(t *testing.T) {
	for _, want := range []bool{true, false} {
		stub := &consentStub{accepted: want}
		router := newConsentRouter(t, stub, datingMatchTestKey)
		a, b := uuid.New(), uuid.New()
		got := postDatingMatch(router, directChatStatePath, `{"user_a":"`+a.String()+`","user_b":"`+b.String()+`"}`,
			map[string]string{"X-Internal-Service-Key": datingMatchTestKey})
		if got.Code != http.StatusOK {
			t.Fatalf("state: %d %s", got.Code, got.Body.String())
		}
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(got.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if v, ok := env.Data["accepted"].(bool); !ok || v != want || len(env.Data) != 1 {
			t.Fatalf("body %s, want {\"data\":{\"accepted\":%v}}", got.Body.String(), want)
		}
		if len(stub.pairs) != 1 || stub.pairs[0] != [2]uuid.UUID{a, b} {
			t.Fatalf("service saw %v", stub.pairs)
		}
	}
}

func userRequest(router http.Handler, method, path, body string, user uuid.UUID, t *testing.T) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userToken(t, user))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestCreateConversationErrorCarriesReason(t *testing.T) {
	user, other := uuid.New(), uuid.New()
	body := `{"other_user_id":"` + other.String() + `"}`
	for _, tc := range []struct {
		err    error
		status int
		code   string
		reason string
	}{
		{&service.MessagingNotAllowedError{Reason: "followers_only"}, 403, "MESSAGING_NOT_ALLOWED", "followers_only"},
		{service.ErrMessagingNotAllowed, 403, "MESSAGING_NOT_ALLOWED", ""},
		{service.ErrRateLimiterUnavailable, 503, "RETRY_LATER", ""},
		{service.ErrRateLimited, 429, "RATE_LIMITED", ""},
	} {
		router := newConsentRouter(t, &consentStub{createErr: tc.err}, datingMatchTestKey)
		rec := userRequest(router, http.MethodPost, "/v1/chat/conversations/direct", body, user, t)
		var env struct {
			Error struct {
				Code    string            `json:"code"`
				Reason  string            `json:"reason"`
				Details map[string]string `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &env)
		if rec.Code != tc.status || env.Error.Code != tc.code || env.Error.Reason != tc.reason ||
			(tc.reason != "" && env.Error.Details["reason"] != tc.reason) {
			t.Errorf("%v: %d %s", tc.err, rec.Code, rec.Body.String())
		}
		if tc.code == "RETRY_LATER" && rec.Header().Get("Retry-After") == "" {
			t.Error("retry-later must send Retry-After")
		}
	}
}

func TestRequestsListAndCountShapes(t *testing.T) {
	user := uuid.New()
	row := service.RequestRow{Direction: "incoming", Preview: "hi"}
	row.ID = uuid.New()
	stub := &consentStub{page: &service.RequestPage{Rows: []service.RequestRow{row}, NextCursor: "abc", HasMore: true},
		counts: &service.RequestCounts{Count: 3, FilteredCount: 1}}
	router := newConsentRouter(t, stub, datingMatchTestKey)

	rec := userRequest(router, http.MethodGet, "/v1/chat/requests?filtered=true&limit=5&cursor=abc", "", user, t)
	if rec.Code != 200 {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []map[string]any `json:"data"`
		Meta map[string]any   `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 1 || env.Data[0]["direction"] != "incoming" || env.Data[0]["preview"] != "hi" ||
		env.Meta["next_cursor"] != "abc" || env.Meta["has_more"] != true {
		t.Fatalf("list body = %s", rec.Body.String())
	}
	if stub.gotQuery.Filter != "filtered" || stub.gotQuery.Limit != 5 || stub.gotQuery.Cursor != "abc" || stub.gotQuery.Direction != "" {
		t.Fatalf("query = %+v", stub.gotQuery)
	}
	// The old spelling still works; an empty direction is the service's
	// incoming default.
	userRequest(router, http.MethodGet, "/v1/chat/requests?filter=filtered&direction=all&offset=7", "", user, t)
	if stub.gotQuery.Filter != "filtered" || stub.gotQuery.Direction != "all" || stub.gotQuery.Offset != 7 {
		t.Fatalf("legacy query = %+v", stub.gotQuery)
	}
	if rec := userRequest(router, http.MethodGet, "/v1/chat/requests?filtered=maybe", "", user, t); rec.Code != 400 {
		t.Fatalf("bad filtered: %d", rec.Code)
	}

	rec = userRequest(router, http.MethodGet, "/v1/chat/requests/count", "", user, t)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"data":{"count":3,"filtered_count":1,"capped":false}`) {
		t.Fatalf("count: %d %s", rec.Code, rec.Body.String())
	}
}
