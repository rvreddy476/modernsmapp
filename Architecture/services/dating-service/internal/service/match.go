// Match service — implements the cross-service saga that forms a match.
//
// Saga steps (Risk R5 in plan §9):
//  1. Begin tx, CreateMatchPending → matchID, commit.
//  2. POST message-service /v1/messages/conversations to allocate the chat.
//  3. On 2xx → MarkMatchActive(matchID, conversationID); emit match.formed.
//  4. On non-2xx / network → DeleteMatch(matchID) compensation; bubble error.
//
// Idempotency: a request-id header (the matchID) lets message-service
// dedupe retries; on retry we still see a 2xx with the same conversation_id.
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// MessageServiceClient is the saga's hand-off to message-service. Real
// callers wire it from main.go; tests inject a fake.
type MessageServiceClient interface {
	CreateConversation(ctx context.Context, req CreateConversationRequest) (*CreateConversationResponse, error)
}

// CreateConversationRequest is the body sent to chat-service's
// /v1/chat/conversations/dating-match endpoint. The participants list
// is the matched pair; ContextID carries the dating match_id so the
// chat side can be idempotent on retries.
type CreateConversationRequest struct {
	Participants []string `json:"participants"`
	Type         string   `json:"type"`
	ContextID    string   `json:"context_id"`
}

// CreateConversationResponse is what message-service returns. We accept both
// `{conversation_id: ...}` and `{data: {id: ...}}` shapes.
type CreateConversationResponse struct {
	ConversationID string `json:"conversation_id"`
}

// httpMessageClient is the production MessageServiceClient.
type httpMessageClient struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPMessageClient wires the message-service client from env vars.
// MESSAGE_SERVICE_URL (default points at the canonical chat-service
// container on port 8092 — the Architecture message-service was archived
// 2026-05-25 per dating/PRODUCTION_GAP_ANALYSIS.md P0-3) and
// INTERNAL_SERVICE_KEY are honored.
func NewHTTPMessageClient() MessageServiceClient {
	base := os.Getenv("MESSAGE_SERVICE_URL")
	if base == "" {
		base = "http://chat-message-service:8092"
	}
	return &httpMessageClient{
		baseURL:     base,
		internalKey: os.Getenv("INTERNAL_SERVICE_KEY"),
		client:      &http.Client{Timeout: 6 * time.Second},
	}
}

func (c *httpMessageClient) CreateConversation(ctx context.Context, body CreateConversationRequest) (*CreateConversationResponse, error) {
	// Marshal into chat-service's dating-match shape:
	//   {user_a, user_b, match_id}
	// Participants[0] / Participants[1] map to user_a / user_b; the
	// chat-side store normalises ordering so either ordering is safe.
	// ContextID carries the dating match_id for idempotency.
	if len(body.Participants) != 2 {
		return nil, fmt.Errorf("dating match requires exactly 2 participants, got %d", len(body.Participants))
	}
	dm := struct {
		UserA   string `json:"user_a"`
		UserB   string `json:"user_b"`
		MatchID string `json:"match_id"`
	}{UserA: body.Participants[0], UserB: body.Participants[1], MatchID: body.ContextID}
	buf, err := json.Marshal(dm)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := c.baseURL + "/v1/chat/conversations/dating-match"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	// Idempotency: the caller passes the matchID as ContextID — we mirror
	// that into the request id so a retry sees the same key.
	req.Header.Set("X-Request-Id", body.ContextID)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("message-service request: %w", err)
	}
	defer resp.Body.Close()
	body2, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("message-service status %d: %s", resp.StatusCode, string(body2))
	}
	// Try direct shape first.
	var direct CreateConversationResponse
	if err := json.Unmarshal(body2, &direct); err == nil && direct.ConversationID != "" {
		return &direct, nil
	}
	// Fallback: enveloped
	var env struct {
		Data struct {
			ID             string `json:"id"`
			ConversationID string `json:"conversation_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body2, &env); err == nil {
		if env.Data.ConversationID != "" {
			return &CreateConversationResponse{ConversationID: env.Data.ConversationID}, nil
		}
		if env.Data.ID != "" {
			return &CreateConversationResponse{ConversationID: env.Data.ID}, nil
		}
	}
	return nil, fmt.Errorf("message-service: empty conversation id in response")
}

// SetMessageClient wires the saga's external dependency. main.go calls this
// after constructing the http client; tests inject fakes.
func (s *Service) SetMessageClient(c MessageServiceClient) {
	s.msgClient = c
}

// FormMatch is the Spark service's hand-off when mutual interest is detected.
// It implements the saga and returns the active Match.
func (s *Service) FormMatch(ctx context.Context, userA, userB uuid.UUID, sparkTarget map[string]any) (*store.Match, error) {
	if userA == uuid.Nil || userB == uuid.Nil {
		return nil, fmt.Errorf("invalid: both user ids required")
	}
	if userA == userB {
		return nil, fmt.Errorf("invalid: cannot match a user with themselves")
	}

	// §P1-1: do not form a match if either side is restricted /
	// suspended / pending_review. The CreateSpark gate already blocks
	// the spark itself, but FormMatch can also be invoked by the saga
	// reconciler retrying an older mutual-spark — at which point one
	// of the parties may have been moderated. Skip silently for the
	// reconciler path; surface the sentinel for direct callers. Both
	// halves checked because a restricted recipient still has stale
	// sparks from when they were active.
	if err := s.requireInteractiveProfile(ctx, userA); err != nil {
		return nil, err
	}
	if err := s.requireInteractiveProfile(ctx, userB); err != nil {
		return nil, err
	}

	// If a match already exists between these two users (any status), reuse
	// it rather than create a duplicate. Idempotency for retried mutual-Spark.
	if existing, err := s.store.GetMatchByUsers(ctx, userA, userB); err == nil && existing != nil {
		if existing.Status == "matched" || existing.Status == "conversing" {
			return existing, nil
		}
	} else if err != nil && !errors.Is(err, store.ErrMatchNotFound) {
		return nil, fmt.Errorf("lookup existing match: %w", err)
	}

	// Step 1: pending row.
	tx, err := s.store.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	matchID, err := s.store.CreateMatchPending(ctx, tx, userA, userB, sparkTarget)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit pending match: %w", err)
	}

	// Step 2: message-service handshake.
	client := s.msgClient
	if client == nil {
		client = NewHTTPMessageClient()
	}
	convResp, mErr := client.CreateConversation(ctx, CreateConversationRequest{
		Participants: []string{userA.String(), userB.String()},
		Type:         "dating_match",
		ContextID:    matchID.String(),
	})
	if mErr != nil {
		// P0-9: don't hard-delete the pending match. Keep it in status
		// 'matched' with NULL conversation_id so the SagaReconciler can
		// retry the chat-side handshake on its next tick — the
		// chat-service endpoint is idempotent on match_id. Hard-delete
		// here lost matches whenever chat-service was briefly down.
		slog.Warn("saga: create conversation failed; leaving pending for reconciler",
			"match_id", matchID, "error", mErr)
		return nil, fmt.Errorf("create conversation: %w", mErr)
	}
	conversationID, perr := uuid.Parse(convResp.ConversationID)
	if perr != nil {
		// Bad ID from chat-service is a protocol error, not transient —
		// reconciler retry won't fix it. Hard-delete remains the right
		// call here.
		if dErr := s.store.DeleteMatch(ctx, matchID); dErr != nil {
			slog.Error("saga: compensation delete failed (bad conv id)", "error", dErr)
		}
		return nil, fmt.Errorf("invalid conversation id from message-service: %w", perr)
	}

	// Step 3: terminal — flip to active.
	if err := s.store.MarkMatchActive(ctx, matchID, conversationID); err != nil {
		// P0-9: don't delete here either. The chat conversation already
		// exists keyed on match_id (idempotent); the reconciler will
		// pick this row up by status='matched' AND conversation_id IS
		// NULL on its next tick. It re-calls CreateConversation (no-op
		// — returns the same id) and re-runs MarkMatchActive.
		slog.Warn("saga: mark active failed; leaving pending for reconciler",
			"match_id", matchID, "conversation_id", conversationID, "error", err)
		return nil, fmt.Errorf("mark match active: %w", err)
	}

	// Reload + emit match.formed.
	match, err := s.store.GetMatch(ctx, matchID)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		if perr := s.producer.PublishMatchFormed(ctx, matchID, match.UserA, match.UserB, conversationID); perr != nil {
			slog.Warn("publish match.formed failed", "match_id", matchID, "error", perr)
		}
	}
	return match, nil
}

// ListMatches returns the caller's matches, filtered by status bucket.
func (s *Service) ListMatches(ctx context.Context, userID uuid.UUID, status string) ([]*store.Match, error) {
	return s.store.ListMatchesForUser(ctx, userID, status)
}

// GetMatch returns a single match (caller must be a participant; checked
// at the handler).
func (s *Service) GetMatch(ctx context.Context, matchID uuid.UUID) (*store.Match, error) {
	return s.store.GetMatch(ctx, matchID)
}

// CloseMatch flips status to closed. Only a participant may close.
func (s *Service) CloseMatch(ctx context.Context, matchID, closedBy uuid.UUID) error {
	m, err := s.store.GetMatch(ctx, matchID)
	if err != nil {
		return err
	}
	if closedBy != m.UserA && closedBy != m.UserB {
		return fmt.Errorf("forbidden: only a participant may close this match")
	}
	if err := s.store.CloseMatch(ctx, matchID, closedBy); err != nil {
		return err
	}
	if s.producer != nil {
		_ = s.producer.PublishMatchClosed(ctx, matchID, closedBy, m.UserA, m.UserB)
	}
	return nil
}

// ExtendMatch is premium-gated. Returns forbidden if the requester is not
// premium; otherwise extends by 7 days.
func (s *Service) ExtendMatch(ctx context.Context, matchID, requestingUserID uuid.UUID) error {
	premium, err := s.store.IsPremium(ctx, requestingUserID)
	if err != nil {
		return err
	}
	if !premium {
		return fmt.Errorf("forbidden: premium required to extend a match")
	}
	m, err := s.store.GetMatch(ctx, matchID)
	if err != nil {
		return err
	}
	if requestingUserID != m.UserA && requestingUserID != m.UserB {
		return fmt.Errorf("forbidden: only a participant may extend this match")
	}
	return s.store.ExtendMatch(ctx, matchID, 7)
}

// RecordFirstMessage is invoked by the message-service consumer when a
// message is sent in this match's conversation. We stamp first_message_at
// once and emit dating.match.first_message.
func (s *Service) RecordFirstMessage(ctx context.Context, matchID, actorID uuid.UUID) error {
	m, err := s.store.GetMatch(ctx, matchID)
	if err != nil {
		return err
	}
	already := m.FirstMessageAt != nil
	if err := s.store.RecordFirstMessage(ctx, matchID, time.Now()); err != nil {
		return err
	}
	if !already && s.producer != nil {
		recipient := m.UserA
		if actorID == m.UserA {
			recipient = m.UserB
		}
		_ = s.producer.PublishMatchFirstMessage(ctx, matchID, actorID, recipient)
	}
	return nil
}

// ExpireStaleMatches is called by the match-expirer cron. Returns the count
// of matches transitioned and emits one event per match.
func (s *Service) ExpireStaleMatches(ctx context.Context) (int, error) {
	expired, err := s.store.ExpireStaleMatches(ctx)
	if err != nil {
		return 0, err
	}
	if s.producer != nil {
		for _, m := range expired {
			_ = s.producer.PublishMatchExpired(ctx, m.ID, m.UserA, m.UserB)
		}
	}
	return len(expired), nil
}

// MarkQuietMatches is called by the match-expirer cron. Returns the count
// of matches transitioned and emits one event per match.
func (s *Service) MarkQuietMatches(ctx context.Context) (int, error) {
	quieted, err := s.store.MarkQuietMatches(ctx)
	if err != nil {
		return 0, err
	}
	if s.producer != nil {
		for _, m := range quieted {
			_ = s.producer.PublishMatchQuiet(ctx, m.ID, m.UserA, m.UserB)
		}
	}
	return len(quieted), nil
}
