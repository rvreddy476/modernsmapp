package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// Dating mechanic M11 — POST /internal/v1/chat/dating-match/turns.

const datingTurnsPath = "/internal/v1/chat/dating-match/turns"

// datingTurnsStub implements only DatingTurnsOwed; anything else panics
// through the nil embedded interface.
type datingTurnsStub struct {
	ChatService
	calls int
	owed  int
	err   error
	got   uuid.UUID
}

func (s *datingTurnsStub) DatingTurnsOwed(_ context.Context, userID uuid.UUID) (int, error) {
	s.calls++
	s.got = userID
	return s.owed, s.err
}

func owedFrom(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var env struct {
		Data struct {
			Owed int `json:"owed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return env.Data.Owed
}

func TestDatingTurnsAnswersTheCount(t *testing.T) {
	stub := &datingTurnsStub{owed: 4}
	router := newDatingMatchRouter(t, stub)
	user := uuid.New()
	got := postDatingMatch(router, datingTurnsPath, `{"user_id":"`+user.String()+`"}`, map[string]string{"X-Internal-Service-Key": datingMatchTestKey})
	if got.Code != http.StatusOK || owedFrom(t, got) != 4 || stub.got != user {
		t.Fatalf("turns: %d %s (asked for %s)", got.Code, got.Body.String(), stub.got)
	}
}

// Guard: only a service caller with the key and no user identity.
func TestDatingTurnsRefusesNonServiceCallers(t *testing.T) {
	stub := &datingTurnsStub{owed: 1}
	router := newDatingMatchRouter(t, stub)
	user := uuid.New()
	body := `{"user_id":"` + user.String() + `"}`
	for name, headers := range map[string]map[string]string{
		"anonymous":    nil,
		"wrong key":    {"X-Internal-Service-Key": "wrong"},
		"user JWT":     {"Authorization": "Bearer " + userToken(t, user)},
		"key and user": {"X-Internal-Service-Key": datingMatchTestKey, "X-User-Id": user.String()},
	} {
		if got := postDatingMatch(router, datingTurnsPath, body, headers); got.Code == http.StatusOK {
			t.Fatalf("%s was admitted: %d", name, got.Code)
		}
	}
	if stub.calls != 0 {
		t.Fatalf("service reached %d times by refused callers", stub.calls)
	}
}

func TestDatingTurnsRejectsBadInput(t *testing.T) {
	stub := &datingTurnsStub{}
	router := newDatingMatchRouter(t, stub)
	key := map[string]string{"X-Internal-Service-Key": datingMatchTestKey}
	for _, body := range []string{`{}`, `{"user_id":"nope"}`, `{"user_id":"` + uuid.Nil.String() + `"}`} {
		if got := postDatingMatch(router, datingTurnsPath, body, key); got.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", body, got.Code)
		}
	}
	stub.err = errors.New("down")
	if got := postDatingMatch(router, datingTurnsPath, `{"user_id":"`+uuid.New().String()+`"}`, key); got.Code != http.StatusInternalServerError {
		t.Fatalf("a failed count answered %d", got.Code)
	}
}
