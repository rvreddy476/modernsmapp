package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestGraphBlockCheckerInternalRouteContract(t *testing.T) {
	viewerID := uuid.New()
	blockedTargetID := uuid.New()
	unblockedTargetID := uuid.New()
	expectedKey := "test-internal-key"

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/v1/internal/graph/blocked-and-muted" {
			t.Errorf("path=%q want /v1/internal/graph/blocked-and-muted", r.URL.Path)
		}
		if gotUser := r.URL.Query().Get("user_id"); gotUser != viewerID.String() {
			t.Errorf("user_id=%q want %q", gotUser, viewerID.String())
		}
		if gotKey := r.Header.Get("X-Internal-Service-Key"); gotKey != expectedKey {
			t.Errorf("X-Internal-Service-Key=%q want %q", gotKey, expectedKey)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_ids": []string{blockedTargetID.String()},
		})
	}))
	defer server.Close()

	checker := NewGraphBlockChecker(server.URL, expectedKey)

	blocked, err := checker.BlockedEitherWay(context.Background(), viewerID, blockedTargetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("graph-service was not called")
	}
	if !blocked {
		t.Fatal("expected blockedTargetID to be reported as blocked")
	}

	// Cache should serve unblocked target without network error
	blocked, err = checker.BlockedEitherWay(context.Background(), viewerID, unblockedTargetID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if blocked {
		t.Fatal("expected unblockedTargetID to not be blocked")
	}
}

func TestGraphBlockCheckerFailsClosedOnGraphError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	checker := NewGraphBlockChecker(server.URL, "key")
	_, err := checker.BlockedEitherWay(context.Background(), uuid.New(), uuid.New())
	if err == nil {
		t.Fatal("expected error on graph-service 500")
	}
}
