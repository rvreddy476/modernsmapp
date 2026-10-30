package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/atpost/chat-message-service/internal/ratelimit"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// DM consent model (founder-approved 20 Oct 2026, contract §4-§5).
//
// Nobody lands in someone's Chats without consent; following someone is
// consent to hear from them. graph-service decides each attempt (direct
// chat, message request, or denied with a reason); this file holds chat's
// side: the reasons, the fail-closed request rate limit, the one-transaction
// open/reopen/promote, the 30-day expiry sweep, and the accepted-chat state
// graph asks for when deciding calls.

// Deny reasons graph-service returns on a `message` decision (contract §4).
// They reach the client in the create-conversation error body.
const (
	ReasonUnavailable   = "unavailable"
	ReasonMessagesOff   = "messages_off"
	ReasonYouPaused     = "you_paused"
	ReasonNoOne         = "no_one"
	ReasonFollowersOnly = "followers_only"
	ReasonPeopleIFollow = "people_i_follow"
)

// normalizeDenyReason passes the contract's reasons through, reports a
// block as "unavailable" (a block is never revealed) and drops anything
// else, so an internal value can never leak to a client.
func normalizeDenyReason(r string) string {
	switch r {
	case ReasonUnavailable, ReasonMessagesOff, ReasonYouPaused, ReasonNoOne, ReasonFollowersOnly, ReasonPeopleIFollow:
		return r
	case "blocked":
		return ReasonUnavailable
	}
	return ""
}

// MessagingNotAllowedError is ErrMessagingNotAllowed with graph's reason.
// errors.Is(err, ErrMessagingNotAllowed) holds for it.
type MessagingNotAllowedError struct {
	Reason string
}

func (e *MessagingNotAllowedError) Error() string { return ErrMessagingNotAllowed.Error() }
func (e *MessagingNotAllowedError) Unwrap() error { return ErrMessagingNotAllowed }

// notAllowed wraps a reason; without one it is the bare sentinel.
func notAllowed(reason string) error {
	if reason == "" {
		return ErrMessagingNotAllowed
	}
	return &MessagingNotAllowedError{Reason: reason}
}

// --- Rate limit (contract §5: 20 new requests per sender per 24 h) ---

// rateLimitAllower is the limiter surface (ratelimit.Limiter; tests fake it).
type rateLimitAllower interface {
	Allow(ctx context.Context, limit ratelimit.Limit, userID string) (bool, error)
}

// ErrRateLimiterUnavailable is returned when a fail-closed limit cannot be
// checked because the limiter store is down. Mapped to HTTP 503 + Retry-After.
var ErrRateLimiterUnavailable = errors.New("can't check the request limit right now; try again shortly")

// checkRateLimitFailClosed is checkRateLimit for limits that must hold even
// when Redis is down: a limiter error REFUSES the action (and is logged)
// instead of allowing it. Used for message-request creation, the cold
// outreach spam vector.
func (s *Service) checkRateLimitFailClosed(ctx context.Context, limit ratelimit.Limit, userID uuid.UUID) error {
	if s.rateLimiter == nil {
		return nil // no limiter configured (local tooling); nothing to fail
	}
	allowed, err := s.rateLimiter.Allow(ctx, limit, userID.String())
	if err != nil {
		s.log.Error("rate limiter check failed — failing CLOSED", "err", err, "action", limit.Action, "user_id", userID)
		return ErrRateLimiterUnavailable
	}
	if !allowed {
		return ErrRateLimited
	}
	return nil
}

// --- Open a direct conversation under the decision ---

// directOpener is the Postgres store's one-transaction open (reached by a
// checked type assertion so older fakes keep the legacy path).
type directOpener interface {
	OpenDirectConversation(ctx context.Context, actorID, otherID uuid.UUID, mode postgres.DirectOpenMode, beforeNewRequest func(context.Context) error) (postgres.DirectOpenResult, error)
}

// openDirect applies the permission decision through the store: a direct
// chat when allowed (promoting a request conversation the actor sent, or a
// finished one), otherwise a request — brand new, revived after a lifted
// block, or reopened after the last one was deleted (outside the cooldown
// the caller already enforced) or expired. The rate limit is charged only
// when a new request is actually written, and fails closed.
func (s *Service) openDirect(ctx context.Context, opener directOpener, userID, otherID uuid.UUID, allowed bool) (*ConversationResponse, error) {
	mode := postgres.OpenDirect
	if !allowed {
		mode = postgres.OpenAsRequest
	}
	res, err := opener.OpenDirectConversation(ctx, userID, otherID, mode, func(ctx context.Context) error {
		return s.checkRateLimitFailClosed(ctx, messageRequestRateLimit, userID)
	})
	if err != nil {
		return nil, err
	}
	if res.Outcome.IsNewRequest() {
		// Tell the recipient a request exists (see the legacy path for why
		// this is emitted here). A brand-new conversation keeps the key
		// delivery.go also uses, so the two paths cannot double-notify; a
		// reopened request is a NEW request and gets its own key.
		key := "message-request:" + res.ConversationID.String()
		if res.Outcome == postgres.RequestReopened {
			key += ":" + strconv.FormatInt(res.RequestCreatedAt.UnixMilli(), 10)
		}
		payload := sharedEvents.MessageRequestPayload{
			ConversationID: res.ConversationID.String(),
			SenderID:       userID.String(),
			ReceiverID:     otherID.String(),
			OccurredAt:     time.Now().UTC(),
		}
		if err := s.deliveryStore().InsertOutboxEventOnce(ctx, key, sharedEvents.MessageRequestCreated, payload); err != nil {
			return nil, fmt.Errorf("queue message request event: %w", err)
		}
	}
	return s.getConversationResponse(ctx, res.ConversationID)
}

// --- Accepted-chat state (graph-service calls under accepted_chats_only) ---

type directChatStateStore interface {
	DirectChatAccepted(ctx context.Context, userA, userB uuid.UUID) (bool, error)
}

// DirectChatAccepted reports whether the unordered pair holds a live 1:1
// chat both agreed to (see the store method for the exact rule). Errors
// propagate; graph-service fails closed on them.
func (s *Service) DirectChatAccepted(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	if userA == uuid.Nil || userB == uuid.Nil {
		return false, invalidInput("two user ids are required")
	}
	if userA == userB {
		return false, nil
	}
	st, ok := s.convStore.(directChatStateStore)
	if !ok {
		return false, errors.New("direct-chat state is not available")
	}
	return st.DirectChatAccepted(ctx, userA, userB)
}

// --- 30-day request expiry ---

const (
	// DefaultRequestExpiryPollInterval and DefaultRequestExpiryBatch are the
	// sweep defaults (config: MESSAGE_REQUEST_EXPIRY_POLL_INTERVAL /
	// MESSAGE_REQUEST_EXPIRY_BATCH).
	DefaultRequestExpiryPollInterval = 10 * time.Minute
	DefaultRequestExpiryBatch        = 200
	// requestExpiryMaxRounds bounds one pass (a backlog drains over ticks).
	requestExpiryMaxRounds = 10
)

type requestExpiryStore interface {
	ExpireDueMessageRequests(ctx context.Context, limit int) ([]postgres.ExpiredRequest, error)
}

// StartMessageRequestExpiryWorker expires unanswered requests until ctx
// ends. Safe with several replicas: each claim is one UPDATE over a
// FOR UPDATE SKIP LOCKED selection, guarded on status = 'pending'.
func (s *Service) StartMessageRequestExpiryWorker(ctx context.Context, interval time.Duration, batch int) {
	if interval <= 0 {
		interval = DefaultRequestExpiryPollInterval
	}
	if batch <= 0 {
		batch = DefaultRequestExpiryBatch
	}
	if _, ok := s.convStore.(requestExpiryStore); !ok {
		s.log.Warn("message-request expiry worker not started: store lacks the sweep")
		return
	}
	s.log.Info("message-request expiry worker started", "interval", interval.String(), "batch", batch)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if n, err := s.runRequestExpiryPass(ctx, batch); err != nil {
			s.log.Error("message-request expiry pass failed", "err", err)
		} else if n > 0 {
			s.log.Info("message requests expired", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runRequestExpiryPass expires due requests in batches (at most
// requestExpiryMaxRounds of them) and returns how many it expired. Each
// recipient gets a `message_request_removed` frame so an open Requests list
// drops the row; the sender is told nothing.
func (s *Service) runRequestExpiryPass(ctx context.Context, batch int) (int, error) {
	st, ok := s.convStore.(requestExpiryStore)
	if !ok {
		return 0, nil
	}
	total := 0
	for round := 0; round < requestExpiryMaxRounds; round++ {
		expired, err := st.ExpireDueMessageRequests(ctx, batch)
		if err != nil {
			return total, err
		}
		total += len(expired)
		for _, e := range expired {
			s.publishRequestRemoved(ctx, e.ReceiverID, e.ConversationID, "expired")
		}
		if len(expired) < batch {
			break
		}
	}
	return total, nil
}

// publishRequestRemoved tells ONE user (the recipient) a request left their
// Requests. Best-effort: the list query already excludes it.
func (s *Service) publishRequestRemoved(ctx context.Context, userID, convID uuid.UUID, reason string) {
	if s.rdb == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{
		"type": "message_request_removed",
		"payload": map[string]interface{}{
			"conversation_id": convID.String(),
			"reason":          reason,
		},
	})
	if ch, ok := s.personalChannelFor(ctx, userID, convID); ok {
		s.rdb.Publish(ctx, ch, payload)
	}
}
