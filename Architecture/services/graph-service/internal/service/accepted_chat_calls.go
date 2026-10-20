package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AcceptedChatProvider answers whether two users hold an ACCEPTED 1:1 chat:
// a direct conversation, or a message request the recipient accepted. That
// state lives in chat message-service, not in the graph — accepting a
// request no longer creates a graph connection (DM consent contract §3/§5),
// so who_can_call=accepted_chats_only can only be decided with it.
//
// Same three-valued contract as [DatingMatchProvider]: (true, nil) grants,
// (false, nil) denies, (_, err) must be treated as DENY.
type AcceptedChatProvider interface {
	HasAcceptedDirectChat(ctx context.Context, userA, userB uuid.UUID) (bool, error)
}

// AcceptedChatCacheTTL bounds how stale a cached accepted-chat answer may be.
// Three seconds, matching [PrivacyCacheTTL] and [DatingMatchCacheTTL]: there
// is no invalidation channel, so the TTL is the window in which a deleted or
// blocked chat could still place a call.
const AcceptedChatCacheTTL = 3 * time.Second

// WithAcceptedChatCalls wires the accepted-chat call grant. enabled is the
// ACCEPTED_CHAT_CALLS_ENABLED kill switch; provider nil is equivalent to off.
// Off means the fact is never fetched and never true, so calls under
// accepted_chats_only stay connection-only (the pre-contract approximation).
func (s *Service) WithAcceptedChatCalls(enabled bool, provider AcceptedChatProvider) *Service {
	s.acceptedChatCallsEnabled = enabled && provider != nil
	s.acceptedChats = provider
	return s
}

// acceptedChatFact resolves the AcceptedChat fact, cached in Redis for
// [AcceptedChatCacheTTL]. It FAILS CLOSED in every uncertain case — flag off,
// no provider, transport error, non-200, undecodable body — because the only
// thing this fact can do is grant a call.
func (s *Service) acceptedChatFact(ctx context.Context, actorID, targetID uuid.UUID) bool {
	if !s.acceptedChatCallsEnabled || s.acceptedChats == nil {
		return false
	}
	if actorID == targetID || actorID == uuid.Nil || targetID == uuid.Nil {
		return false
	}

	cacheKey := "accepted_chat:" + datingPairKey(actorID, targetID)
	if s.rdb != nil {
		if val, err := s.rdb.Get(ctx, cacheKey).Result(); err == nil {
			switch val {
			case "1":
				return true
			case "0":
				return false
			}
		}
	}

	accepted, err := s.acceptedChats.HasAcceptedDirectChat(ctx, actorID, targetID)
	if err != nil {
		log.Printf("[graph] accepted-chat fetch failed for %s<->%s, treating as no chat: %v", actorID, targetID, err)
		return false
	}

	if s.rdb != nil {
		v := "0"
		if accepted {
			v = "1"
		}
		s.rdb.Set(ctx, cacheKey, v, AcceptedChatCacheTTL)
	}
	return accepted
}

// httpAcceptedChatProvider asks chat message-service's internal-only
// endpoint POST /internal/v1/chat/direct-chat/state
// {"user_a","user_b"} -> {"data":{"accepted":true|false}}.
//
// NOTE: that endpoint does not exist yet in message-service; the flag stays
// off until it ships. "accepted" must be true only for a non-deleted 1:1
// conversation between the pair whose message request (if any) is accepted,
// and false while a request is pending, declined, expired or blocked.
type httpAcceptedChatProvider struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPAcceptedChatProvider builds the message-service-backed provider.
func NewHTTPAcceptedChatProvider(baseURL, internalKey string, client *http.Client) (AcceptedChatProvider, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("MESSAGE_SERVICE_URL is required when ACCEPTED_CHAT_CALLS_ENABLED=true")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("MESSAGE_SERVICE_URL must be an absolute http(s) URL, got %q", baseURL)
	}
	if strings.TrimSpace(internalKey) == "" {
		return nil, errors.New("INTERNAL_SERVICE_KEY is required when ACCEPTED_CHAT_CALLS_ENABLED=true")
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &httpAcceptedChatProvider{baseURL: baseURL, internalKey: internalKey, client: client}, nil
}

func (p *httpAcceptedChatProvider) HasAcceptedDirectChat(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	payload, err := json.Marshal(map[string]string{
		"user_a": userA.String(),
		"user_b": userB.String(),
	})
	if err != nil {
		return false, err
	}
	// Ids in the body, not the URL, so they stay out of access logs.
	url := p.baseURL + "/internal/v1/chat/direct-chat/state"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", p.internalKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("message-service returned %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Accepted *bool `json:"accepted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, fmt.Errorf("decode direct-chat response: %w", err)
	}
	// A 200 without the field is an unknown answer: an error, so the caller
	// fails closed without caching a fabricated negative.
	if envelope.Data.Accepted == nil {
		return false, errors.New("direct-chat response missing accepted")
	}
	return *envelope.Data.Accepted, nil
}
