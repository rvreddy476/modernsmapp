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
	"time"

	"github.com/atpost/chat-message-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	datingMatchTestKey    = "internal-key"
	datingMatchTestSecret = "jwt-secret"
	datingMatchPath       = "/internal/v1/chat/conversations/dating-match"
)

// datingMatchStub implements only CreateDatingMatchConversation; any other
// ChatService call panics through the nil embedded interface.
type datingMatchStub struct {
	ChatService
	mu      sync.Mutex
	calls   int
	byMatch map[uuid.UUID]uuid.UUID
}

func (s *datingMatchStub) CreateDatingMatchConversation(_ context.Context, userA, userB, matchID uuid.UUID) (*service.ConversationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.byMatch == nil {
		s.byMatch = map[uuid.UUID]uuid.UUID{}
	}
	id, ok := s.byMatch[matchID]
	if !ok {
		id = uuid.New()
		s.byMatch[matchID] = id
	}
	return &service.ConversationResponse{ID: id, Type: "direct"}, nil
}

func newDatingMatchRouter(t *testing.T, svc ChatService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := gin.New()
	// The production middleware chain, so the tests exercise the real
	// JWT-vs-internal-key decision rather than the handler alone.
	router.Use(AuthMiddlewareWithKeys(JWTKeySet{ActiveSecret: datingMatchTestSecret, InternalServiceKey: datingMatchTestKey}, log))
	New(svc, log).WithInternalServiceKey(datingMatchTestKey).RegisterRoutes(router)
	return router
}

func datingMatchBody(a, b, match uuid.UUID) string {
	return `{"user_a":"` + a.String() + `","user_b":"` + b.String() + `","match_id":"` + match.String() + `"}`
}

func postDatingMatch(router http.Handler, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		request.Header.Set(k, v)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func userToken(t *testing.T, userID uuid.UUID) string {
	return buildHS256Token(t, []byte(datingMatchTestSecret), map[string]any{
		"sub": userID.String(),
		"exp": time.Now().Add(5 * time.Minute).Unix(),
	})
}

func TestDatingMatchRouteRefusesAnonymous(t *testing.T) {
	stub := &datingMatchStub{}
	router := newDatingMatchRouter(t, stub)
	body := datingMatchBody(uuid.New(), uuid.New(), uuid.New())

	if got := postDatingMatch(router, datingMatchPath, body, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request returned %d body=%s", got.Code, got.Body.String())
	}
	if got := postDatingMatch(router, datingMatchPath, body, map[string]string{"X-Internal-Service-Key": "wrong"}); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key returned %d body=%s", got.Code, got.Body.String())
	}
	// A user JWT alone (no key) is not a service credential.
	jwtOnly := map[string]string{"Authorization": "Bearer " + userToken(t, uuid.New())}
	if got := postDatingMatch(router, datingMatchPath, body, jwtOnly); got.Code == http.StatusOK || got.Code == http.StatusCreated {
		t.Fatalf("user JWT without key was admitted: %d", got.Code)
	}
	if stub.calls != 0 {
		t.Fatalf("service reached %d times by refused callers", stub.calls)
	}
}

// TestDatingMatchRouteRefusesUserIdentity is the gateway shape: the edge
// injects the real internal key AND the verified identity headers.
func TestDatingMatchRouteRefusesUserIdentity(t *testing.T) {
	stub := &datingMatchStub{}
	router := newDatingMatchRouter(t, stub)
	user := uuid.New()
	body := datingMatchBody(user, uuid.New(), uuid.New())

	full := map[string]string{
		"X-Internal-Service-Key": datingMatchTestKey,
		"Authorization":          "Bearer " + userToken(t, user),
		"X-User-Id":              user.String(),
		"X-Verified-User-Id":     user.String(),
		"X-Scopes":               "user",
	}
	if got := postDatingMatch(router, datingMatchPath, body, full); got.Code != http.StatusForbidden {
		t.Fatalf("user JWT + identity headers + key returned %d body=%s", got.Code, got.Body.String())
	}
	for _, header := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "Authorization"} {
		t.Run(header, func(t *testing.T) {
			value := user.String()
			if header == "Authorization" {
				value = "Bearer " + userToken(t, user)
			}
			headers := map[string]string{"X-Internal-Service-Key": datingMatchTestKey, header: value}
			if got := postDatingMatch(router, datingMatchPath, body, headers); got.Code != http.StatusForbidden {
				t.Fatalf("key + %s returned %d body=%s", header, got.Code, got.Body.String())
			}
		})
	}
	if stub.calls != 0 {
		t.Fatalf("service reached %d times by user callers", stub.calls)
	}
}

func TestDatingMatchRouteAdmitsServiceKeyAndIsIdempotent(t *testing.T) {
	stub := &datingMatchStub{}
	router := newDatingMatchRouter(t, stub)
	a, b, match := uuid.New(), uuid.New(), uuid.New()
	headers := map[string]string{"X-Internal-Service-Key": datingMatchTestKey, "X-Request-Id": match.String()}

	first := postDatingMatch(router, datingMatchPath, datingMatchBody(a, b, match), headers)
	if first.Code != http.StatusOK && first.Code != http.StatusCreated {
		t.Fatalf("service call returned %d body=%s", first.Code, first.Body.String())
	}
	second := postDatingMatch(router, datingMatchPath, datingMatchBody(b, a, match), headers)
	if second.Code != http.StatusOK && second.Code != http.StatusCreated {
		t.Fatalf("repeat call returned %d body=%s", second.Code, second.Body.String())
	}
	firstID, secondID := conversationIDFrom(t, first), conversationIDFrom(t, second)
	if firstID == "" || firstID != secondID {
		t.Fatalf("repeat call returned a different conversation: %q vs %q", firstID, secondID)
	}
}

func TestDatingMatchRouteIsNotOnGatewayPrefix(t *testing.T) {
	stub := &datingMatchStub{}
	router := newDatingMatchRouter(t, stub)
	user := uuid.New()
	body := datingMatchBody(user, uuid.New(), uuid.New())
	// What the gateway would forward for an anonymous caller and for a
	// logged-in caller on the old public path.
	anon := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}
	if got := postDatingMatch(router, "/v1/chat/conversations/dating-match", body, anon); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous gateway-shaped call on /v1/chat returned %d", got.Code)
	}
	authed := map[string]string{"X-Internal-Service-Key": datingMatchTestKey, "Authorization": "Bearer " + userToken(t, user), "X-User-Id": user.String()}
	if got := postDatingMatch(router, "/v1/chat/conversations/dating-match", body, authed); got.Code != http.StatusNotFound {
		t.Fatalf("logged-in gateway-shaped call on /v1/chat returned %d", got.Code)
	}
	for _, route := range router.Routes() {
		if strings.HasSuffix(route.Path, "/dating-match") && route.Path != datingMatchPath {
			t.Fatalf("dating-match registered on a non-internal path: %s", route.Path)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("service reached %d times via /v1/chat", stub.calls)
	}
}

func conversationIDFrom(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return envelope.Data.ID
}
