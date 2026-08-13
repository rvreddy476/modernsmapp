package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestCallPolicyUsesCanonicalGraphShapeAndSymmetricBlock(t *testing.T) {
	blockedBy := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Internal-Service-Key") != "internal-key" {
			t.Error("missing internal key")
		}
		writer.Header().Set("Content-Type", "application/json")
		if blockedBy {
			_, _ = writer.Write([]byte(`{"data":{"is_connection":true,"blocked_by":true}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"data":{"is_connection":true}}`))
	}))
	defer server.Close()
	policy := NewCallPolicy(server.URL, "internal-key")
	if err := policy.CanCall(context.Background(), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("canonical connection denied: %v", err)
	}
	blockedBy = true
	if err := policy.CanCall(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrBlockedByTarget) {
		t.Fatalf("reverse-direction block did not deny: %v", err)
	}
}
