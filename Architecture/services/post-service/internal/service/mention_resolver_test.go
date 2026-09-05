package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

// lookupUserByUsername is the resolver behind user.mentioned. Until
// 2026-09-05 it was broken four ways at once: never wired from main, pointed
// at identity-user (401 without the key, 404 with it), sent no internal key,
// and decoded {"user_id"} while user-service answers {"data":{"id"}}. These
// tests pin the contract against a fake app user-service.

func newMentionTestService(url string) *Service {
	return &Service{
		userServiceURL:     url,
		internalServiceKey: "test-key",
		httpClient:         &http.Client{Timeout: time.Second},
	}
}

func TestLookupUserByUsernameResolvesEnvelope(t *testing.T) {
	var seenPath, seenKey atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath.Store(r.URL.Path)
		seenKey.Store(r.Header.Get("X-Internal-Service-Key"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"data": map[string]any{
				"entityType": "user",
				"id":         "2d598287-eee7-40b4-a7f5-b46b9412e4e7",
				"username":   "call.usera",
			},
		})
	}))
	defer srv.Close()

	got, err := newMentionTestService(srv.URL+"/").lookupUserByUsername(context.Background(), "call.usera")
	if err != nil {
		t.Fatalf("lookupUserByUsername: %v", err)
	}
	if want := "2d598287-eee7-40b4-a7f5-b46b9412e4e7"; got != want {
		t.Fatalf("user id = %q, want %q", got, want)
	}
	if p := seenPath.Load(); p != "/v1/users/by-username/call.usera" {
		t.Fatalf("path = %v, want /v1/users/by-username/call.usera (trailing slash on base must not double)", p)
	}
	if k := seenKey.Load(); k != "test-key" {
		t.Fatalf("X-Internal-Service-Key = %v, want test-key", k)
	}
}

func TestLookupUserByUsernameNotFoundIsEmptyNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"User not found"}}`)) //nolint:errcheck
	}))
	defer srv.Close()

	got, err := newMentionTestService(srv.URL).lookupUserByUsername(context.Background(), "nobody")
	if err != nil || got != "" {
		t.Fatalf("got (%q, %v), want (\"\", nil) for an unknown handle", got, err)
	}
}

func TestLookupUserByUsernameRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// What identity-user returned to the old resolver without the key.
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"data":{"id":"should-not-be-trusted"}}`)) //nolint:errcheck
	}))
	defer srv.Close()

	got, err := newMentionTestService(srv.URL).lookupUserByUsername(context.Background(), "call.usera")
	if err == nil || got != "" {
		t.Fatalf("got (%q, %v), want an error and no id on 401", got, err)
	}
}

func TestLookupUserByUsernameUnconfigured(t *testing.T) {
	got, err := (&Service{}).lookupUserByUsername(context.Background(), "call.usera")
	if err != nil || got != "" {
		t.Fatalf("got (%q, %v), want (\"\", nil) with no URL", got, err)
	}
}

// The caption parser must accept the same handle alphabet the explicit
// `mentions` field and post_mentions do — `call.usera` is a real username —
// while not swallowing a sentence-ending period.
func TestExtractMentionsDottedHandles(t *testing.T) {
	got := extractMentions("cc @call.usera and @bob. also @Alice.b_2 @.")
	want := []string{"call.usera", "bob", "Alice.b_2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("extractMentions = %v, want %v", got, want)
	}
}
