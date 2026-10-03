package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Mechanic M9: the read-receipts push and the call check reach chat's
// internal routes with the internal key and the shapes chat reads.

func TestHTTPMessageClient_MatchExtras(t *testing.T) {
	type seen struct {
		path string
		body map[string]string
	}
	var got []seen
	open := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("%s without the internal key", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]string
		_ = json.Unmarshal(raw, &b)
		got = append(got, seen{r.URL.Path, b})
		if r.URL.Path == "/internal/v1/chat/dating-match/state" {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]bool{"open_match": open}})
			return
		}
		_, _ = w.Write([]byte(`{"data":{"updated":true}}`))
	}))
	defer srv.Close()
	c := &httpMessageClient{baseURL: srv.URL, internalKey: "k", client: &http.Client{Timeout: 2 * time.Second}}
	m, u, v := uuid.New(), uuid.New(), uuid.New()
	until := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	if err := c.SetReadReceipts(context.Background(), m, u, &until); err != nil {
		t.Fatal(err)
	}
	if err := c.SetReadReceipts(context.Background(), m, u, nil); err != nil {
		t.Fatal(err)
	}
	ok, err := c.MatchCallable(context.Background(), u, v)
	if err != nil || !ok {
		t.Fatalf("callable = %v err=%v, want true", ok, err)
	}
	open = false
	if ok, _ := c.MatchCallable(context.Background(), u, v); ok {
		t.Fatalf("callable = true, want false")
	}

	if got[0].path != "/internal/v1/chat/conversations/dating-match/read-receipts" ||
		got[0].body["match_id"] != m.String() || got[0].body["user_id"] != u.String() || got[0].body["until"] != "2026-11-01T10:00:00Z" {
		t.Fatalf("push = %+v", got[0])
	}
	if _, present := got[1].body["until"]; present {
		t.Fatalf("a nil until sent %q, want it absent", got[1].body["until"])
	}
	if got[2].path != "/internal/v1/chat/dating-match/state" || got[2].body["user_a"] != u.String() || got[2].body["user_b"] != v.String() {
		t.Fatalf("state = %+v", got[2])
	}
}

// Mechanic M11: the turns count reaches chat's internal route with the
// internal key and decodes chat's answer.
func TestHTTPMessageClient_DatingTurnsOwed(t *testing.T) {
	var path, user string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("%s without the internal key", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		var b map[string]string
		_ = json.Unmarshal(raw, &b)
		path, user = r.URL.Path, b["user_id"]
		_, _ = w.Write([]byte(`{"data":{"owed":7}}`))
	}))
	defer srv.Close()
	c := &httpMessageClient{baseURL: srv.URL, internalKey: "k", client: &http.Client{Timeout: 2 * time.Second}}
	u := uuid.New()
	owed, err := c.DatingTurnsOwed(context.Background(), u)
	if err != nil || owed != 7 || path != "/internal/v1/chat/dating-match/turns" || user != u.String() {
		t.Fatalf("owed=%d err=%v path=%s user=%s", owed, err, path, user)
	}
}
