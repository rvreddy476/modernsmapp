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

// CreateConversationRequest is the body sent to message-service.
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
// MESSAGE_SERVICE_URL (default http://message-service:8094) and
// INTERNAL_SERVICE_KEY are honored.
func NewHTTPMessageClient() MessageServiceClient {
	base := os.Getenv("MESSAGE_SERVICE_URL")
	if base == "" {
		base = "http://message-service:8094"
	}
	return &httpMessageClient{
		baseURL:     base,
		internalKey: os.Getenv("INTERNAL_SERVICE_KEY"),
		client:      &http.Client{Timeout: 6 * time.Second},
	}
}

func (c *httpMessageClient) CreateConversation(ctx context.Context, body CreateConversationRequest) (*CreateConversationResponse, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	url := c.baseURL + "/v1/messages/conversations"
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
		// Compensate: hard-delete the pending match so a future retry can
		// re-form it without dragging the orphan along.
		if dErr := s.store.DeleteMatch(ctx, matchID); dErr != nil {
			slog.Error("saga: compensation delete failed", "match_id", matchID, "error", dErr)
		}
		return nil, fmt.Errorf("create conversation: %w", mErr)
	}
	conversationID, perr := uuid.Parse(convResp.ConversationID)
	if perr != nil {
		if dErr := s.store.DeleteMatch(ctx, matchID); dErr != nil {
			slog.Error("saga: compensation delete failed (bad conv id)", "error", dErr)
		}
		return nil, fmt.Errorf("invalid conversation id from message-service: %w", perr)
	}

	// Step 3: terminal — flip to active.
	if err := s.store.MarkMatchActive(ctx, matchID, conversationID); err != nil {
		// Best-effort compensation: delete the row. We don't try to roll
		// back the conversation in message-service for v1; we log so ops
		// can clean it up. (Tracked as TODO in the saga design.)
		if dErr := s.store.DeleteMatch(ctx, matchID); dErr != nil {
			slog.Error("saga: compensation delete failed (mark active)", "error", dErr)
		}
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
