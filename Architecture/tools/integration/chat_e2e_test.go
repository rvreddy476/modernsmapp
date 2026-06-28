//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Chat_DirectMessage exercises the chat write/read path through the
// gateway → message-service → Scylla:
//
//	A opens a direct conversation with B → A sends a message →
//	B reads the conversation and Eventually sees the message
//
// Real-time WS push (chat-ws-gateway) is validated separately in the UI layer;
// here we assert durable delivery (the message persists and is readable by the
// other member), which is the backbone guarantee.
func TestE2E_Chat_DirectMessage(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	aID := uuid.New()
	bID := uuid.New()
	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// 1. A creates a direct conversation with B.
	convRaw := a.MustOK(t, ctx, "POST", "/v1/chat/conversations/direct", map[string]any{
		"other_user_id": bID.String(),
	})
	var conv struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(convRaw, &conv); err != nil || conv.ID == "" {
		t.Fatalf("create direct conversation: id missing (err=%v raw=%s)", err, string(convRaw))
	}

	// 2. A sends a message.
	msg := "e2e-chat-" + uuid.NewString()
	a.MustOK(t, ctx, "POST", "/v1/chat/conversations/"+conv.ID+"/messages", map[string]any{
		"type": "text",
		"text": msg,
	})

	// 3. B (the other member) reads the conversation and sees the message.
	Eventually(t, 20*time.Second, "message delivered to the other member", func() bool {
		env, err := b.Do(ctx, "GET", "/v1/chat/conversations/"+conv.ID+"/messages", nil)
		return err == nil && env.Status == 200 && strings.Contains(string(env.Data), msg)
	})
}
