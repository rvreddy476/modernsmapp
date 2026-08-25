package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestSyncMemberToChatUsesExactInternalProtocol(t *testing.T) {
	conversationID, userID := uuid.New(), uuid.New()
	requests := make(chan *http.Request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests <- request.Clone(request.Context())
		if request.Method == http.MethodPost {
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode add body: %v", err)
			}
			if body["user_id"] != userID.String() {
				t.Errorf("unexpected user_id: %q", body["user_id"])
			}
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	service := &Service{
		messageServiceURL:  server.URL,
		internalServiceKey: "internal-key",
		chatClient:         server.Client(),
	}
	if err := service.syncMemberToChat(context.Background(), conversationID, userID, true); err != nil {
		t.Fatal(err)
	}
	add := <-requests
	if add.Method != http.MethodPost || add.URL.Path != "/internal/v1/chat/groups/conversations/"+conversationID.String()+"/members" {
		t.Fatalf("unexpected add request: %s %s", add.Method, add.URL.Path)
	}
	if add.Header.Get("X-Internal-Service-Key") != "internal-key" {
		t.Fatal("internal service key missing from add")
	}

	if err := service.syncMemberToChat(context.Background(), conversationID, userID, false); err != nil {
		t.Fatal(err)
	}
	remove := <-requests
	if remove.Method != http.MethodDelete || remove.URL.Path != "/internal/v1/chat/groups/conversations/"+conversationID.String()+"/members/"+userID.String() {
		t.Fatalf("unexpected remove request: %s %s", remove.Method, remove.URL.Path)
	}
}

func TestSyncMemberToChatPropagatesFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	service := &Service{messageServiceURL: server.URL, internalServiceKey: "key", chatClient: server.Client()}
	if err := service.syncMemberToChat(context.Background(), uuid.New(), uuid.New(), false); err == nil {
		t.Fatal("chat sync failure was swallowed")
	}
}
