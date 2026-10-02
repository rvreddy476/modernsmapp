package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Mechanic M5: the two chat calls carry the first movers and the opening
// answer exactly as chat-service's internal routes read them.

func TestHTTPMessageClient_CreateCarriesFirstMovers(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/chat/conversations/dating-match" || r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("request %s key=%q", r.URL.Path, r.Header.Get("X-Internal-Service-Key"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"conversation_id":"` + uuid.NewString() + `"}`))
	}))
	defer srv.Close()
	c := &httpMessageClient{baseURL: srv.URL, internalKey: "k", client: &http.Client{Timeout: 2 * time.Second}}
	a, b, m := uuid.New(), uuid.New(), uuid.New()

	if _, err := c.CreateConversation(context.Background(), CreateConversationRequest{
		Participants: []string{a.String(), b.String()}, ContextID: m.String(), FirstMoverIDs: []string{b.String()},
	}); err != nil {
		t.Fatal(err)
	}
	movers, _ := got["first_mover_ids"].([]any)
	if len(movers) != 1 || movers[0] != b.String() || got["match_id"] != m.String() {
		t.Fatalf("body = %v, want match_id and first_mover_ids [%s]", got, b)
	}

	got = nil
	if _, err := c.CreateConversation(context.Background(), CreateConversationRequest{Participants: []string{a.String(), b.String()}, ContextID: m.String()}); err != nil {
		t.Fatal(err)
	}
	if _, present := got["first_mover_ids"]; present {
		t.Fatalf("a match without a rule sent first_mover_ids: %v", got)
	}
}

func TestHTTPMessageClient_OpeningAnswer(t *testing.T) {
	status := http.StatusCreated
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/chat/conversations/dating-match/opening-answer" || r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("request %s key=%q", r.URL.Path, r.Header.Get("X-Internal-Service-Key"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":{"conversation_id":"c"}}`))
	}))
	defer srv.Close()
	c := &httpMessageClient{baseURL: srv.URL, internalKey: "k", client: &http.Client{Timeout: 2 * time.Second}}
	m, u := uuid.New(), uuid.New()

	if _, err := c.SendOpeningAnswer(context.Background(), m, u, "“Q”\nA", "key-1"); err != nil {
		t.Fatal(err)
	}
	if got["match_id"] != m.String() || got["sender_id"] != u.String() || got["text"] != "“Q”\nA" || got["idempotency_key"] != "key-1" {
		t.Fatalf("body = %v", got)
	}
	status = http.StatusConflict
	if _, err := c.SendOpeningAnswer(context.Background(), m, u, "x", "key-2"); !errors.Is(err, ErrOpeningAnswerRefusedByChat) {
		t.Fatalf("409: err=%v, want ErrOpeningAnswerRefusedByChat", err)
	}
	status = http.StatusInternalServerError
	if _, err := c.SendOpeningAnswer(context.Background(), m, u, "x", "key-3"); err == nil || errors.Is(err, ErrOpeningAnswerRefusedByChat) {
		t.Fatalf("500: err=%v, want a plain failure", err)
	}
}
