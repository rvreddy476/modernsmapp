package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Dating lane D4: when a dating-match conversation gets its first message,
// dating-service must hear about it, or first_message_at stays NULL and the
// dating sweeper expires the match seven days after it formed (and chat then
// closes the conversation on dating.match.expired).
//
// Delivery is a DURABLE OBLIGATION, never a call on the send path:
//
//  1. completeMessageDelivery (itself replayed by the delivery repair worker
//     until it succeeds) records one chat.dating_first_message_notifications
//     row per conversation, ON CONFLICT DO NOTHING — the first delivered
//     message creates it, every later message is a no-op;
//  2. [Service.StartDatingFirstMessageWorker] claims due rows and calls
//     POST {DATING_SERVICE_URL}/v1/dating/internal/matches/{match_id}/first-message;
//  3. 2xx retires the row, a permanent refusal marks it terminal (logged
//     once, never retried), anything else backs off and retries.
//
// dating-service's RecordFirstMessage is idempotent (COALESCE on
// first_message_at, event only on the first stamp), so a retry after a lost
// response is harmless.

type datingFirstMessageStore interface {
	EnqueueDatingFirstMessage(ctx context.Context, n postgres.DatingFirstMessageNotification) (bool, error)
	ClaimDueDatingFirstMessages(ctx context.Context, limit int, lease time.Duration) ([]postgres.DatingFirstMessageNotification, error)
	MarkDatingFirstMessageDelivered(ctx context.Context, conversationID uuid.UUID, status int) error
	MarkDatingFirstMessageTerminal(ctx context.Context, conversationID uuid.UUID, status int, lastErr string) error
	DeferDatingFirstMessage(ctx context.Context, conversationID uuid.UUID, retryIn time.Duration, status int, lastErr string) error
}

const (
	datingFirstMessagePathFmt = "/v1/dating/internal/matches/%s/first-message"

	datingFirstMessageClaimBatch   = 50
	datingFirstMessageLease        = 2 * time.Minute
	datingFirstMessagePollInterval = 3 * time.Second
	datingFirstMessageCallTimeout  = 5 * time.Second

	datingFirstMessageBackoffBase = 5 * time.Second
	datingFirstMessageBackoffCap  = 10 * time.Minute
)

// SetDatingService wires the dating-service base URL the first-message worker
// calls. Empty disables the worker; obligations still accumulate durably and
// are delivered once it is configured.
func (s *Service) SetDatingService(datingServiceURL string) {
	s.datingServiceURL = strings.TrimRight(datingServiceURL, "/")
}

// enqueueDatingFirstMessage records the obligation for a delivered message in
// a dating conversation. An error fails the delivery, which then stays pending
// for the repair worker — the same contract as every outbox write beside it.
func (s *Service) enqueueDatingFirstMessage(ctx context.Context, intent *postgres.MessageDeliveryIntent) error {
	store, ok := s.convStore.(datingFirstMessageStore)
	if !ok {
		return fmt.Errorf("conversation store cannot record dating first-message notifications")
	}
	_, err := store.EnqueueDatingFirstMessage(ctx, postgres.DatingFirstMessageNotification{
		ConversationID: intent.ConversationID,
		MatchID:        *intent.MatchID,
		ActorID:        intent.SenderID,
		MessageID:      intent.MessageID,
	})
	return err
}

// StartDatingFirstMessageWorker delivers pending first-message notifications
// until ctx ends. Safe with several replicas (SKIP LOCKED + lease).
func (s *Service) StartDatingFirstMessageWorker(ctx context.Context) {
	if s.datingServiceURL == "" {
		s.log.Warn("DATING_SERVICE_URL is empty; dating first-message notifications will queue undelivered")
		return
	}
	if _, ok := s.convStore.(datingFirstMessageStore); !ok {
		s.log.Error("conversation store cannot deliver dating first-message notifications")
		return
	}
	ticker := time.NewTicker(datingFirstMessagePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runDatingFirstMessagePass(ctx); err != nil {
				s.log.Error("dating first-message pass failed", "err", err)
			}
		}
	}
}

type datingCallOutcome int

const (
	datingCallDelivered datingCallOutcome = iota
	datingCallTerminal
	datingCallRetry
)

func (s *Service) runDatingFirstMessagePass(ctx context.Context) error {
	store, ok := s.convStore.(datingFirstMessageStore)
	if !ok {
		return fmt.Errorf("conversation store cannot deliver dating first-message notifications")
	}
	due, err := store.ClaimDueDatingFirstMessages(ctx, datingFirstMessageClaimBatch, datingFirstMessageLease)
	if err != nil {
		return err
	}
	for _, n := range due {
		outcome, status, callErr := s.notifyDatingFirstMessage(ctx, n)
		l := s.log.With("conversation_id", n.ConversationID, "match_id", n.MatchID, "attempts", n.AttemptCount, "status", status)
		switch outcome {
		case datingCallDelivered:
			if err := store.MarkDatingFirstMessageDelivered(ctx, n.ConversationID, status); err != nil {
				// The lease brings the row back; dating is idempotent.
				l.Warn("dating first-message delivered but not recorded; will re-send", "err", err)
			}
		case datingCallTerminal:
			// Logged exactly once: a terminal row is never claimed again.
			l.Warn("dating refused first-message notification permanently; giving up", "err", callErr)
			if err := store.MarkDatingFirstMessageTerminal(ctx, n.ConversationID, status, errString(callErr)); err != nil {
				l.Warn("dating first-message terminal mark failed", "err", err)
			}
		default:
			l.Warn("dating first-message notification deferred", "err", callErr)
			if err := store.DeferDatingFirstMessage(ctx, n.ConversationID, datingFirstMessageBackoff(n.AttemptCount), status, errString(callErr)); err != nil {
				// Still safe: the claim lease is the fallback backoff.
				l.Warn("dating first-message defer failed", "err", err)
			}
		}
	}
	return nil
}

// notifyDatingFirstMessage makes one call. It sends the internal service key
// and deliberately NO end-user identity header: dating-service refuses any
// request on the internal family that carries X-User-Id.
func (s *Service) notifyDatingFirstMessage(ctx context.Context, n postgres.DatingFirstMessageNotification) (datingCallOutcome, int, error) {
	body, err := json.Marshal(map[string]string{"actor_id": n.ActorID.String()})
	if err != nil {
		return datingCallTerminal, 0, err
	}
	callCtx, cancel := context.WithTimeout(ctx, datingFirstMessageCallTimeout)
	defer cancel()
	url := s.datingServiceURL + fmt.Sprintf(datingFirstMessagePathFmt, n.MatchID)
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return datingCallRetry, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	// TODO(dating-service-tokens): prefer a service token once chat can mint
	// one — X-Service-Authorization: Bearer <token>, audience "dating", scope
	// "dating:match.first_message" — and keep the internal key only as the
	// legacy fallback. Needs a chat signing key pair and a SERVICE_CALLERS
	// entry on dating-service (SERVICE_CALLER_CHAT_KID/_PUBKEY/_OPS).
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: datingFirstMessageCallTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return datingCallRetry, 0, fmt.Errorf("call dating-service: %w", err)
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	outcome := classifyDatingFirstMessageStatus(resp.StatusCode)
	if outcome == datingCallDelivered {
		return outcome, resp.StatusCode, nil
	}
	return outcome, resp.StatusCode, fmt.Errorf("dating-service answered %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
}

// classifyDatingFirstMessageStatus maps dating-service's answer onto what the
// worker does next. 404 (match gone), 409, 410 (path retired) and malformed
// requests (400/422) cannot succeed by retrying. Everything else — 5xx, 429,
// and 401/403 (a credential misconfiguration an operator can fix) — retries
// with capped backoff.
func classifyDatingFirstMessageStatus(status int) datingCallOutcome {
	switch {
	case status >= 200 && status < 300:
		return datingCallDelivered
	case status == http.StatusBadRequest, status == http.StatusNotFound,
		status == http.StatusConflict, status == http.StatusGone,
		status == http.StatusUnprocessableEntity:
		return datingCallTerminal
	default:
		return datingCallRetry
	}
}

// datingFirstMessageBackoff doubles from the base per attempt, bounded so a
// long outage retries at the cap rather than giving up.
func datingFirstMessageBackoff(attempts int) time.Duration {
	backoff := datingFirstMessageBackoffBase
	for i := 1; i < attempts && backoff < datingFirstMessageBackoffCap; i++ {
		backoff *= 2
	}
	if backoff > datingFirstMessageBackoffCap {
		backoff = datingFirstMessageBackoffCap
	}
	return backoff
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
