package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// CallPolicy delegates to graph-service's permission matrix — the same
// authority that already resolves who_can_call AND block-in-either-direction
// for every other surface. These tests pin the request shape (actor header,
// internal key, actions=call), the single generic refusal, and fail-closed
// behavior on every degraded answer.

func permissionServer(t *testing.T, respond func(w http.ResponseWriter)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/permissions/check" {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("actions") != "call" {
			t.Errorf("wrong actions: %s", r.URL.Query().Get("actions"))
		}
		if r.Header.Get("X-Internal-Service-Key") != "internal-key" {
			t.Error("missing internal key")
		}
		if r.Header.Get("X-User-Id") == "" {
			t.Error("missing caller actor header")
		}
		w.Header().Set("Content-Type", "application/json")
		respond(w)
	}))
}

func TestCallPolicyAllowsWhenTheMatrixAllows(t *testing.T) {
	server := permissionServer(t, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"data":{"decisions":{"call":{"allowed":true}}}}`))
	})
	defer server.Close()
	policy := NewCallPolicy(server.URL, "internal-key")
	if err := policy.CanCall(context.Background(), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("allowed decision denied: %v", err)
	}
}

// Every denial — block, privacy_no_one, privacy_connections_only — surfaces
// as the SAME generic error. The caller must not be able to distinguish
// "blocked me" from "privacy settings".
func TestCallPolicyDenialsAreGenericRegardlessOfReason(t *testing.T) {
	for _, reason := range []string{"blocked", "privacy_no_one", "privacy_connections_only"} {
		reason := reason
		server := permissionServer(t, func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(
				`{"data":{"decisions":{"call":{"allowed":false,"reason":"` + reason + `"}}}}`))
		})
		policy := NewCallPolicy(server.URL, "internal-key")
		err := policy.CanCall(context.Background(), uuid.New(), uuid.New())
		server.Close()
		if !errors.Is(err, ErrCallNotAllowed) {
			t.Fatalf("reason %q: want the one generic refusal, got %v", reason, err)
		}
	}
}

// who_can_call is enforced through the matrix — the exact gap this policy
// used to have: the old relationship heuristic allowed any connection to
// call even when the callee had set who_can_call=no_one.
func TestCallPolicyHonorsWhoCanCallNoOne(t *testing.T) {
	server := permissionServer(t, func(w http.ResponseWriter) {
		_, _ = w.Write([]byte(
			`{"data":{"decisions":{"call":{"allowed":false,"reason":"privacy_no_one"}}}}`))
	})
	defer server.Close()
	policy := NewCallPolicy(server.URL, "internal-key")
	if err := policy.CanCall(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrCallNotAllowed) {
		t.Fatalf("who_can_call=no_one did not deny: %v", err)
	}
}

func TestCallPolicyFailsClosedOnDegradedAnswers(t *testing.T) {
	cases := map[string]func(w http.ResponseWriter){
		"http error": func(w http.ResponseWriter) { w.WriteHeader(http.StatusBadGateway) },
		"garbage":    func(w http.ResponseWriter) { _, _ = w.Write([]byte("<html>")) },
		"no call decision": func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"data":{"decisions":{}}}`))
		},
	}
	for name, respond := range cases {
		server := permissionServer(t, respond)
		policy := NewCallPolicy(server.URL, "internal-key")
		err := policy.CanCall(context.Background(), uuid.New(), uuid.New())
		server.Close()
		if !errors.Is(err, ErrGraphUnavailable) {
			t.Fatalf("%s: want fail-closed unavailable, got %v", name, err)
		}
	}

	// Unreachable graph: transport failure also fails closed.
	policy := NewCallPolicy("http://127.0.0.1:1", "internal-key")
	if err := policy.CanCall(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrGraphUnavailable) {
		t.Fatalf("unreachable graph: want fail-closed unavailable, got %v", err)
	}
}

func TestCallPolicySelfCallAndDisabledGateAllow(t *testing.T) {
	// Empty URL = intentionally disabled (tests / isolated rigs).
	policy := NewCallPolicy("", "")
	if err := policy.CanCall(context.Background(), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("disabled gate denied: %v", err)
	}
	// Self is always allowed (no self-lookup round trip).
	self := uuid.New()
	strict := NewCallPolicy("http://127.0.0.1:1", "k")
	if err := strict.CanCall(context.Background(), self, self); err != nil {
		t.Fatalf("self call denied: %v", err)
	}
}
