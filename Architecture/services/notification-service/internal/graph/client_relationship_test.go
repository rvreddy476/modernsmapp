package graph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// EitherBlocked asks graph-service's relationship route as an in-cluster
// caller and reads a block in either direction as blocked.
func TestEitherBlocked_WireContractAndBothDirections(t *testing.T) {
	user, other := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"no block", `{"data":{"follows":true,"blocked":false,"blocked_by":false}}`, false},
		{"other blocked user", `{"data":{"blocked":true,"blocked_by":false}}`, true},
		{"user blocked other", `{"data":{"blocked":false,"blocked_by":true}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/graph/relationship" {
					t.Errorf("path = %s", r.URL.Path)
				}
				if r.URL.Query().Get("user_id") != user.String() || r.URL.Query().Get("other_id") != other.String() {
					t.Errorf("query = %s", r.URL.RawQuery)
				}
				if r.Header.Get("X-Internal-Service-Key") != "k" {
					t.Errorf("internal key missing")
				}
				if r.Header.Get("X-Graph-Write-Source") != "" {
					t.Errorf("the edge header must never be sent: graph would bind the actor to X-User-Id")
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			got, err := New(srv.URL, "k").EitherBlocked(context.Background(), user, other)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("EitherBlocked = %v, want %v", got, tc.want)
			}
		})
	}
}

// "Could not ask" is an error, never "no block": a non-200, a body without
// data, and an unconfigured client.
func TestEitherBlocked_FailuresAreErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, `{"data":{"blocked":false}}`},
		{"no data", http.StatusOK, `{"error":"x"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			if _, err := New(srv.URL, "").EitherBlocked(context.Background(), uuid.New(), uuid.New()); err == nil {
				t.Fatal("want an error")
			}
		})
	}
	var nilClient *Client
	if _, err := nilClient.EitherBlocked(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("nil client must be an error")
	}
	if _, err := New("", "").EitherBlocked(context.Background(), uuid.New(), uuid.New()); err == nil {
		t.Fatal("unconfigured client must be an error")
	}
}
