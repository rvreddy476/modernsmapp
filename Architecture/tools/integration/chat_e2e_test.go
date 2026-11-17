//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// chatSend posts one message as userID through the gateway, the way a client
// does: the bearer token only, plus the Idempotency-Key header the send
// route requires (message-service internal/service/message_send.go SendRich
// refuses a send without one). The shared HTTPClient cannot set a header, so
// this one call is built here.
func chatSend(t *testing.T, ctx context.Context, baseURL string, userID uuid.UUID, convID string, body map[string]any) *Envelope {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	path := "/v1/chat/conversations/" + convID + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, bytes.NewReader(buf))
	if err != nil {
		t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+mintToken(userID, ""))
	req.Header.Set("Idempotency-Key", uuid.NewString())
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("POST %s: transport error: %v", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("POST %s: read body: %v", path, err)
	}
	env := &Envelope{Status: resp.StatusCode}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, env); err != nil {
			t.Fatalf("POST %s: got %d with a body that is not the API envelope: %s", path, resp.StatusCode, string(raw))
		}
	}
	return env
}

// TestE2E_Chat_DirectMessage exercises the chat write/read path through the
// gateway → message-service → Scylla, on the path the product allows:
//
//	B follows A → A opens a direct conversation with B (a chat, not a
//	request) → A sends a message → B reads the conversation and sees it
//
// Who may message whom is the DM consent model (message-service
// internal/service/dm_consent.go, graph-service internal/permission
// resolveMessage): following someone is consent to hear from them, so B
// following A is what lets A open a direct chat. Without it A gets a message
// request at best. Both sides' settings must also be readable (identity
// user-service), or every step fails closed.
//
// So A and B must be real accounts: rows in user-service's users table (a
// follow of an unknown id is refused) and in identity's usr.users and
// usr.user_settings (the settings authority). The e2e workflow seeds both and
// passes the ids (E2EUsers). This flow used to mint two random ids, which the
// product refuses with 403 MESSAGING_NOT_ALLOWED; the next flow pins that.
//
// Real-time WS push (chat-ws-gateway) is validated separately in the UI
// layer; here we assert durable delivery (the message persists and is
// readable by the other member), which is the backbone guarantee.
func TestE2E_Chat_DirectMessage(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, bID := E2EUsers(t)

	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. B follows A: B's consent to hear from A. Whether the edge really
	// exists is decided by step 2, which can only be a direct chat with it.
	b.MustOK(t, ctx, "POST", "/v1/graph/follow", map[string]any{"user_id": aID.String()})

	// 2. A opens a direct conversation with B. It must be a chat, not a
	// message request: is_request=true would mean graph did not see B's
	// follow or could not read a setting.
	// The route refuses a create with no Idempotency-Key (400
	// MISSING_IDEMPOTENCY_KEY).
	convRaw := a.WithHeader("Idempotency-Key", uuid.NewString()).MustOK(t, ctx, "POST", "/v1/chat/conversations/direct", map[string]any{
		"other_user_id": bID.String(),
	})
	var conv struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		IsRequest bool   `json:"is_request"`
	}
	if err := json.Unmarshal(convRaw, &conv); err != nil || conv.ID == "" {
		t.Fatalf("create direct conversation: id missing (err=%v raw=%s)", err, string(convRaw))
	}
	if conv.Type != "direct" || conv.IsRequest {
		t.Fatalf("create direct conversation: type=%q is_request=%v, want a direct chat that is not a request (raw=%s)", conv.Type, conv.IsRequest, string(convRaw))
	}

	// 3. A sends a message.
	msg := "e2e-chat-" + uuid.NewString()
	sent := chatSend(t, ctx, urls.APIGateway, aID, conv.ID, map[string]any{"type": "text", "text": msg})
	if sent.Status < 200 || sent.Status >= 300 {
		errMsg := ""
		if sent.Error != nil {
			errMsg = sent.Error.Code + ": " + sent.Error.Message
		}
		t.Fatalf("send message: expected 2xx, got %d (%s)", sent.Status, errMsg)
	}
	var sentMsg struct {
		MsgID    string `json:"msg_id"`
		SenderID string `json:"sender_id"`
	}
	if err := json.Unmarshal(sent.Data, &sentMsg); err != nil || sentMsg.MsgID == "" || sentMsg.SenderID != aID.String() {
		t.Fatalf("send message: msg_id/sender_id wrong (err=%v raw=%s)", err, string(sent.Data))
	}

	// 4. B (the other member) reads the conversation and sees the message.
	Eventually(t, 20*time.Second, "message delivered to the other member", func() bool {
		env, err := b.Do(ctx, "GET", "/v1/chat/conversations/"+conv.ID+"/messages", nil)
		return err == nil && env.Status == 200 &&
			strings.Contains(string(env.Data), msg) && strings.Contains(string(env.Data), sentMsg.MsgID)
	})
}

// TestE2E_Chat_UnknownSenderRefused pins the fail-closed side of the same
// rule: a signed-in id with no readable settings (here, an id no account
// owns) cannot open a conversation with a real user. message-service refuses
// creation when the sender's own chat policy is unknown (internal/service
// conversations.go CreateDirectConversation, policy.go GetChatPolicy), with
// 403 MESSAGING_NOT_ALLOWED.
func TestE2E_Chat_UnknownSenderRefused(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	_, bID := E2EUsers(t)

	stranger := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := stranger.MustDo(t, ctx, "POST", "/v1/chat/conversations/direct", map[string]any{
		"other_user_id": bID.String(),
	})
	code := ""
	if env.Error != nil {
		code = env.Error.Code
	}
	if env.Status != http.StatusForbidden || code != "MESSAGING_NOT_ALLOWED" {
		t.Fatalf("unknown sender opening a chat: got %d %q, want 403 MESSAGING_NOT_ALLOWED", env.Status, code)
	}
}
