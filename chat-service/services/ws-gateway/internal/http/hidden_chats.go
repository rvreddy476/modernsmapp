package http

// Hidden chats on the socket (contract pinned 24 Oct 2026).
//
// A member who hid a conversation receives NOTHING about it on their personal
// channel (message-service skips them). Their open hidden chat is delivered on
// the conversation room instead, and a room needs an entitlement that
// message-service issues for a hidden conversation only with a valid open
// token. The client sends
//
//	{"type":"conversation.subscribe","conversation_id":"…","open_token":"…"}
//
// with no entitlement; the gateway fetches the entitlement itself from
// POST /v1/chat/conversations/:id/subscription with the connection's own
// bearer token and X-Chat-Open: <open_token> — message-service's gate checks
// the open token (user + session bound) there, so the gateway never has to.
// The open token is remembered for that room only, and rides the typing
// calls the gateway forwards for it. The gateway never logs it.
//
// The entitlement still expires after roomauth.TTL (5 minutes) and is NOT
// renewed by the gateway (renewing would slide the open token forever and
// defeat the 15-minute idle re-lock). When the reconcile loop drops a room the
// client gets {"type":"subscription_expired","conversation_id":…} and
// re-subscribes while the chat is still open. A refused subscribe answers
// {"type":"subscription_refused","conversation_id":…,"reason":…}.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	nethttp "net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const entitlementFetchTimeout = 3 * time.Second

// errChatLocked is message-service's 403 CHAT_LOCKED on the entitlement call.
var errChatLocked = errors.New("chat locked")

// EntitlementFetcher obtains a conversation-room entitlement on the user's
// behalf (the subscribe frame carried an open token instead of one).
type EntitlementFetcher interface {
	FetchEntitlement(ctx context.Context, bearerToken string, conversationID uuid.UUID, openToken string) (string, error)
}

// HTTPEntitlementFetcher calls message-service's subscription route.
type HTTPEntitlementFetcher struct {
	BaseURL string
	Client  *nethttp.Client
}

func NewHTTPEntitlementFetcher(baseURL string, client *nethttp.Client) *HTTPEntitlementFetcher {
	if client == nil {
		client = &nethttp.Client{Timeout: entitlementFetchTimeout}
	}
	return &HTTPEntitlementFetcher{BaseURL: strings.TrimRight(baseURL, "/"), Client: client}
}

func (f *HTTPEntitlementFetcher) FetchEntitlement(ctx context.Context, bearerToken string, conversationID uuid.UUID, openToken string) (string, error) {
	if f == nil || f.BaseURL == "" {
		return "", fmt.Errorf("entitlement fetcher not configured")
	}
	url := fmt.Sprintf("%s/v1/chat/conversations/%s/subscription", f.BaseURL, conversationID)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	if openToken != "" {
		req.Header.Set("X-Chat-Open", openToken)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("entitlement call failed")
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error.Code == "CHAT_LOCKED" {
			return "", errChatLocked
		}
		return "", fmt.Errorf("entitlement call: status %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Data.Token == "" {
		return "", fmt.Errorf("entitlement call: bad response")
	}
	return out.Data.Token, nil
}

// fetchEntitlement asks message-service for the room entitlement on the
// connection's behalf. Returns "" and a client-facing reason on refusal
// ("" reason: nothing worth telling — e.g. no fetcher configured).
func (s *Server) fetchEntitlement(ctx context.Context, bearerToken, rawConversationID, openToken string) (string, string) {
	if s.opts.EntitlementFetcher == nil || bearerToken == "" {
		return "", ""
	}
	convID, err := uuid.Parse(rawConversationID)
	if err != nil {
		return "", ""
	}
	fctx, cancel := context.WithTimeout(ctx, entitlementFetchTimeout)
	defer cancel()
	token, err := s.opts.EntitlementFetcher.FetchEntitlement(fctx, bearerToken, convID, openToken)
	if errors.Is(err, errChatLocked) {
		return "", "chat_locked"
	}
	if err != nil {
		s.log.Warn("conversation.subscribe: entitlement fetch refused", "err", err, "conversation_id", convID)
		return "", "denied"
	}
	return token, ""
}

// openTokenKey carries a room's open token into the typing call.
type openTokenKey struct{}

func withOpenToken(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, openTokenKey{}, token)
}

func openTokenFrom(ctx context.Context) string {
	v, _ := ctx.Value(openTokenKey{}).(string)
	return v
}

// subscriptionFrame is a small control frame about one conversation room.
func subscriptionFrame(typ, conversationID, reason string) []byte {
	m := map[string]string{"type": typ, "conversation_id": conversationID}
	if reason != "" {
		m["reason"] = reason
	}
	raw, _ := json.Marshal(m)
	return raw
}

func offer(outbound chan<- []byte, frame []byte) {
	select {
	case outbound <- frame:
	default:
	}
}
