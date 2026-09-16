package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
)

const datingStatePath = "/internal/v1/chat/dating-match/state"

// datingStateStub implements only HasOpenDatingMatch; every other ChatService
// call panics through the nil embedded interface, so a handler that reached
// anything else would fail loudly.
type datingStateStub struct {
	ChatService
	mu    sync.Mutex
	calls int
	open  bool
	err   error
}

func (s *datingStateStub) HasOpenDatingMatch(_ context.Context, _, _ uuid.UUID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.open, s.err
}

func datingStateBody(a, b uuid.UUID) string {
	return `{"user_a":"` + a.String() + `","user_b":"` + b.String() + `"}`
}

func openMatchFrom(t *testing.T, response *httptest.ResponseRecorder) bool {
	t.Helper()
	var envelope struct {
		Data struct {
			OpenMatch bool `json:"open_match"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %s: %v", response.Body.String(), err)
	}
	return envelope.Data.OpenMatch
}

func TestDatingMatchStateRefusesAnonymous(t *testing.T) {
	stub := &datingStateStub{open: true}
	router := newDatingMatchRouter(t, stub)
	body := datingStateBody(uuid.New(), uuid.New())

	if got := postDatingMatch(router, datingStatePath, body, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous request returned %d body=%s", got.Code, got.Body.String())
	}
	if got := postDatingMatch(router, datingStatePath, body, map[string]string{"X-Internal-Service-Key": "wrong"}); got.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key returned %d body=%s", got.Code, got.Body.String())
	}
	jwtOnly := map[string]string{"Authorization": "Bearer " + userToken(t, uuid.New())}
	if got := postDatingMatch(router, datingStatePath, body, jwtOnly); got.Code == http.StatusOK {
		t.Fatalf("user JWT without key was admitted: %d", got.Code)
	}
	if stub.calls != 0 {
		t.Fatalf("service reached %d times by refused callers", stub.calls)
	}
}

// The gateway shape: the edge injects the real internal key AND the verified
// identity headers, so the key alone never proves a service caller.
func TestDatingMatchStateRefusesUserIdentity(t *testing.T) {
	stub := &datingStateStub{open: true}
	router := newDatingMatchRouter(t, stub)
	user := uuid.New()
	body := datingStateBody(user, uuid.New())

	full := map[string]string{
		"X-Internal-Service-Key": datingMatchTestKey,
		"Authorization":          "Bearer " + userToken(t, user),
		"X-User-Id":              user.String(),
		"X-Verified-User-Id":     user.String(),
		"X-Scopes":               "user",
	}
	if got := postDatingMatch(router, datingStatePath, body, full); got.Code != http.StatusForbidden {
		t.Fatalf("user JWT + identity headers + key returned %d body=%s", got.Code, got.Body.String())
	}
	for _, header := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role", "Authorization"} {
		t.Run(header, func(t *testing.T) {
			value := user.String()
			if header == "Authorization" {
				value = "Bearer " + userToken(t, user)
			}
			headers := map[string]string{"X-Internal-Service-Key": datingMatchTestKey, header: value}
			if got := postDatingMatch(router, datingStatePath, body, headers); got.Code != http.StatusForbidden {
				t.Fatalf("key + %s returned %d body=%s", header, got.Code, got.Body.String())
			}
		})
	}
	if stub.calls != 0 {
		t.Fatalf("service reached %d times by user callers", stub.calls)
	}
}

func TestDatingMatchStateAnswersTheService(t *testing.T) {
	headers := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}
	a, b := uuid.New(), uuid.New()

	t.Run("open match", func(t *testing.T) {
		stub := &datingStateStub{open: true}
		got := postDatingMatch(newDatingMatchRouter(t, stub), datingStatePath, datingStateBody(a, b), headers)
		if got.Code != http.StatusOK {
			t.Fatalf("returned %d body=%s", got.Code, got.Body.String())
		}
		if !openMatchFrom(t, got) {
			t.Fatal("open match reported as closed")
		}
	})

	t.Run("no match or closed match", func(t *testing.T) {
		stub := &datingStateStub{open: false}
		got := postDatingMatch(newDatingMatchRouter(t, stub), datingStatePath, datingStateBody(a, b), headers)
		if got.Code != http.StatusOK {
			t.Fatalf("returned %d body=%s", got.Code, got.Body.String())
		}
		if openMatchFrom(t, got) {
			t.Fatal("closed/absent match reported as open")
		}
	})

	t.Run("lookup failure is an error, never a false 200", func(t *testing.T) {
		stub := &datingStateStub{open: true, err: errors.New("db down")}
		got := postDatingMatch(newDatingMatchRouter(t, stub), datingStatePath, datingStateBody(a, b), headers)
		if got.Code != http.StatusInternalServerError {
			t.Fatalf("returned %d body=%s", got.Code, got.Body.String())
		}
	})

	t.Run("malformed ids are rejected before the service", func(t *testing.T) {
		stub := &datingStateStub{open: true}
		router := newDatingMatchRouter(t, stub)
		for _, body := range []string{`{"user_a":"nope","user_b":"` + b.String() + `"}`, `{"user_a":"` + a.String() + `"}`, `{}`} {
			if got := postDatingMatch(router, datingStatePath, body, headers); got.Code != http.StatusBadRequest {
				t.Fatalf("body %s returned %d", body, got.Code)
			}
		}
		if stub.calls != 0 {
			t.Fatalf("service reached %d times with malformed input", stub.calls)
		}
	})
}

// The probe must not be reachable through the gateway's /v1/chat prefix.
func TestDatingMatchStateIsNotOnGatewayPrefix(t *testing.T) {
	stub := &datingStateStub{open: true}
	router := newDatingMatchRouter(t, stub)
	user := uuid.New()
	body := datingStateBody(user, uuid.New())

	anon := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}
	if got := postDatingMatch(router, "/v1/chat/dating-match/state", body, anon); got.Code == http.StatusOK {
		t.Fatalf("anonymous gateway-shaped call on /v1/chat returned %d", got.Code)
	}
	authed := map[string]string{
		"X-Internal-Service-Key": datingMatchTestKey,
		"Authorization":          "Bearer " + userToken(t, user),
		"X-User-Id":              user.String(),
	}
	if got := postDatingMatch(router, "/v1/chat/dating-match/state", body, authed); got.Code != http.StatusNotFound {
		t.Fatalf("logged-in gateway-shaped call on /v1/chat returned %d", got.Code)
	}
	if stub.calls != 0 {
		t.Fatalf("service reached %d times via /v1/chat", stub.calls)
	}
}
