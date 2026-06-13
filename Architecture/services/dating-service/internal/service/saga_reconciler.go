package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// MatchSagaReconciler periodically scans dating_matches for rows stuck
// in a half-formed saga state and retries the chat-side conversation
// handshake. P0-9 in dating/PRODUCTION_GAP_ANALYSIS.md.
//
// Saga states this reconciler recovers from:
//
//  1. CreateConversation failed → match row exists, conversation_id IS
//     NULL. We re-call CreateConversation (idempotent on match_id —
//     returns the existing conversation if a partial run already
//     created one) and then MarkMatchActive.
//  2. CreateConversation succeeded but MarkMatchActive failed → same
//     state as above (conversation_id NULL), but the next
//     CreateConversation returns the existing conversation id and the
//     MarkMatchActive call completes the flip.
//
// All operations are idempotent: re-running the reconciler on the same
// match yields no change.
//
// minAge prevents racing the live FormMatch path — only rows older
// than this delta are considered "stuck."
type MatchSagaReconciler struct {
	svc      *Service
	interval time.Duration
	minAge   time.Duration
	batch    int
	log      *slog.Logger
}

// NewMatchSagaReconciler returns a reconciler with sensible defaults.
func NewMatchSagaReconciler(svc *Service) *MatchSagaReconciler {
	return &MatchSagaReconciler{
		svc:      svc,
		interval: 60 * time.Second,
		minAge:   30 * time.Second,
		batch:    100,
		log:      slog.Default(),
	}
}

// Start runs the reconciler on its interval until ctx is cancelled.
// Call in a goroutine — never returns under normal operation.
func (r *MatchSagaReconciler) Start(ctx context.Context) {
	r.log.Info("match saga reconciler started",
		"interval", r.interval, "min_age", r.minAge, "batch", r.batch)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Reconcile(ctx); err != nil {
				r.log.Warn("match saga reconcile failed", "err", err)
			}
		}
	}
}

// Reconcile runs one pass over stuck matches. Exposed so tests + ops
// runbooks can trigger a sweep on demand without waiting for the
// ticker.
func (r *MatchSagaReconciler) Reconcile(ctx context.Context) error {
	pending, err := r.svc.store.ListSagaPendingMatches(ctx, r.minAge, r.batch)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}
	r.log.Info("match saga reconciler: found stuck matches", "count", len(pending))

	client := r.svc.msgClient
	if client == nil {
		client = NewHTTPMessageClient()
	}

	var repaired, failed int
	for _, m := range pending {
		if err := r.retryOne(ctx, client, m); err != nil {
			failed++
			r.log.Warn("match saga retry failed", "match_id", m.ID, "err", err)
			continue
		}
		repaired++
	}
	r.log.Info("match saga reconciler: pass complete",
		"repaired", repaired, "failed", failed, "total", len(pending))
	return nil
}

func (r *MatchSagaReconciler) retryOne(ctx context.Context, client MessageServiceClient, m *store.Match) error {
	resp, err := client.CreateConversation(ctx, CreateConversationRequest{
		Participants: []string{m.UserA.String(), m.UserB.String()},
		Type:         "dating_match",
		ContextID:    m.ID.String(),
	})
	if err != nil {
		return err
	}
	convID, err := uuid.Parse(resp.ConversationID)
	if err != nil {
		return err
	}
	return r.svc.store.MarkMatchActive(ctx, m.ID, convID)
}
