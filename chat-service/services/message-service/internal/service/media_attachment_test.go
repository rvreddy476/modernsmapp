package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestValidateChatAttachmentUsesOwnerScopedInternalAuthority(t *testing.T) {
	referenceID, uploaderID, mediaID := uuid.New(), uuid.New(), uuid.New()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/media/internal/chat-attachment/reserve" {
			t.Errorf("unexpected path %s", request.URL.Path)
		}
		if request.Header.Get("X-Internal-Service-Key") != "internal-key" {
			t.Error("missing internal key")
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["reference_id"] != referenceID.String() || body["uploader_id"] != uploaderID.String() || body["media_id"] != mediaID.String() {
			t.Errorf("unexpected reservation body: %v", body)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	service := &Service{mediaServiceURL: server.URL, internalServiceKey: "internal-key", httpClient: server.Client()}
	if err := service.reserveChatAttachment(context.Background(), referenceID, uploaderID, mediaID); err != nil {
		t.Fatal(err)
	}
}

func TestValidateChatAttachmentFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	service := &Service{mediaServiceURL: server.URL, internalServiceKey: "key", httpClient: server.Client()}
	if err := service.reserveChatAttachment(context.Background(), uuid.New(), uuid.New(), uuid.New()); !errors.Is(err, ErrMediaNotAllowed) {
		t.Fatalf("expected ErrMediaNotAllowed, got %v", err)
	}

	unconfigured := &Service{httpClient: server.Client()}
	if err := unconfigured.reserveChatAttachment(context.Background(), uuid.New(), uuid.New(), uuid.New()); !errors.Is(err, ErrMediaNotAllowed) {
		t.Fatalf("unconfigured media authority did not fail closed: %v", err)
	}
}
