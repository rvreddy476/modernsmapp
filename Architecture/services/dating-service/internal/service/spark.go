// Spark service — orchestrates Spark create / list / revoke / decline and
// the mutual-Spark match-formation hand-off.
//
// On CreateSpark we always emit dating.spark.created. If the recipient
// previously Sparked the actor (HasReverseSparks == true) we synchronously
// invoke the match service's saga to form a match and emit
// dating.spark.matched with the resulting match id.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/moderation"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// ErrCandidateUnavailable is the one refusal for acting on another user who
// is blocked (either way), not active, deleted, suspended, restricted or
// under 18. It is deliberately the same for every cause, so a refusal never
// reveals a block. Maps to 404 CANDIDATE_UNAVAILABLE.
var ErrCandidateUnavailable = errors.New("not_found: candidate unavailable")

// ErrSparkNoteRefused is returned when a spark note fails the layer-1
// moderation filter in strict mode. Maps to 400 SPARK_NOTE_REFUSED.
var ErrSparkNoteRefused = errors.New("invalid: spark notes cannot contain phone numbers, email addresses or links")

// ErrSparkRateLimited is the store sentinel, re-exported for handlers.
var ErrSparkRateLimited = store.ErrSparkRateLimited

// DefaultSparkDailyLimit is how many new sparks a free user may send in a
// rolling store.SparkQuotaWindow.
const DefaultSparkDailyLimit = 50

// sparkDailyLimit is the per-user spark allowance. Every user gets the free
// limit today; this is the hook a premium allowance plugs into later.
func (s *Service) sparkDailyLimit(_ context.Context, _ uuid.UUID) int {
	return DefaultSparkDailyLimit
}

// checkSparkNote runs the layer-1 moderation filter in strict mode: a phone
// number, email address or external link is refused, as is anything the
// filter itself would block.
func checkSparkNote(note string) error {
	if strings.TrimSpace(note) == "" {
		return nil
	}
	verdict := moderation.ScanMessage(note)
	for _, p := range verdict.Patterns {
		switch p {
		case moderation.PatternPhone, moderation.PatternEmail, moderation.PatternURL:
			return ErrSparkNoteRefused
		}
	}
	if verdict.ActionTaken == "block" {
		return ErrSparkNoteRefused
	}
	return nil
}

// requireNotBlocked returns ErrCandidateUnavailable when either user has
// blocked the other.
func (s *Service) requireNotBlocked(ctx context.Context, a, b uuid.UUID) error {
	blocked, err := s.store.IsBlockedEitherWay(ctx, a, b)
	if err != nil {
		return err
	}
	if blocked {
		return ErrCandidateUnavailable
	}
	return nil
}

// requireActiveAdultCandidate returns ErrCandidateUnavailable unless the
// user has a live profile in status 'active' with a birth date putting them
// at 18+. Deleted (soft or hard), suspended, restricted, paused, held and
// onboarding profiles all fail.
func (s *Service) requireActiveAdultCandidate(ctx context.Context, candidateID uuid.UUID) error {
	p, err := s.store.GetProfile(ctx, candidateID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			return ErrCandidateUnavailable
		}
		return fmt.Errorf("load candidate profile: %w", err)
	}
	if p.ProfileStatus != store.ProfileStatusActive || p.BirthDate == nil ||
		ageYears(*p.BirthDate, time.Now()) < MinDatingAgeYears {
		return ErrCandidateUnavailable
	}
	return nil
}

// CreateSpark inserts a Spark and triggers the mutual-Spark saga when
// applicable. Returns the persisted Spark and an optional matchID — when
// non-nil, a match was formed as a side effect.
func (s *Service) CreateSpark(ctx context.Context, fromUserID, toUserID uuid.UUID, targetKind, targetRef, note string) (*store.Spark, *uuid.UUID, error) {
	if fromUserID == uuid.Nil {
		return nil, nil, fmt.Errorf("invalid: fromUserID required")
	}
	if toUserID == uuid.Nil {
		return nil, nil, fmt.Errorf("invalid: toUserID required")
	}
	if fromUserID == toUserID {
		return nil, nil, fmt.Errorf("invalid: cannot spark yourself")
	}

	// §P0-7 Phase A risk gate. Runs BEFORE requireAdult so a flagged
	// account never accidentally reveals "we'd let you spark if you
	// were 18". chat_hold / admin_review / suspend → ErrRiskBlocked
	// (403). require_recheck → ErrRiskRecheck (400). reduce_reach
	// and allow proceed (reduce_reach decays discovery rank, not the
	// spark itself).
	switch level, rerr := s.GetUserRiskLevel(ctx, fromUserID); {
	case rerr != nil:
		// Best-effort: don't fail the spark when the risk lookup
		// itself errors — that would turn a Postgres blip into a
		// platform-wide spark outage. Log and continue.
		slog.Warn("spark risk lookup failed", "from_user_id", fromUserID, "error", rerr)
	case level == store.RiskLevelChatHold,
		level == store.RiskLevelAdminReview,
		level == store.RiskLevelSuspend:
		return nil, nil, ErrRiskBlocked
	case level == store.RiskLevelRequireRecheck:
		return nil, nil, ErrRiskRecheck
	}

	// P0-5: the actor sending a spark must themselves be a verified
	// adult. The candidate-side age gate lives in the discovery query,
	// but a spark to a known userID bypasses discovery — so gate it
	// here too. Returns ErrUnderage with a clean 4xx mapping.
	if err := s.requireAdult(ctx, fromUserID); err != nil {
		return nil, nil, err
	}

	// §P1-1 profile-status gate. Restricted/suspended/pending-review
	// profiles cannot create new sparks. The discovery query already
	// hides them from inbound surfaces; this gate closes the
	// known-target-id loophole. The risk gate above catches
	// risk_level=admin_review for risk-scored accounts; this gate
	// covers admin-driven restrict / suspend / pending_review even
	// when no risk row exists yet.
	if err := s.requireInteractiveProfile(ctx, fromUserID); err != nil {
		return nil, nil, err
	}

	// Lane D3: the note passes layer-1 moderation in strict mode.
	if err := checkSparkNote(note); err != nil {
		return nil, nil, err
	}

	// Lane D3: the recipient must be an active 18+ profile, and the pair
	// must not be blocked either way. Both refusals are the same
	// ErrCandidateUnavailable, so the sender cannot tell a block apart.
	if err := s.requireActiveAdultCandidate(ctx, toUserID); err != nil {
		return nil, nil, err
	}
	if err := s.requireNotBlocked(ctx, fromUserID, toUserID); err != nil {
		return nil, nil, err
	}

	// Lane D3: the rolling spark allowance is enforced in the same
	// transaction as the insert.
	sp, err := s.store.CreateSparkWithQuota(ctx, fromUserID, toUserID, targetKind, targetRef, note, s.sparkDailyLimit(ctx, fromUserID))
	if err != nil {
		return nil, nil, err
	}

	// Always emit spark.created.
	if s.producer != nil {
		if perr := s.producer.PublishSparkCreated(ctx, sp.ID, fromUserID, toUserID, targetKind, targetRef, note); perr != nil {
			slog.Warn("publish spark.created failed", "spark_id", sp.ID, "error", perr)
		}
	}

	// Mutual-Spark check: did `toUserID` Spark `fromUserID` since the
	// pair's last match closed (and not have it declined)?
	mutual, herr := s.store.HasReverseSparks(ctx, fromUserID, toUserID)
	if herr != nil {
		// We log but don't fail — the Spark itself was persisted.
		slog.Warn("has reverse sparks failed", "error", herr)
		return sp, nil, nil
	}
	if !mutual {
		return sp, nil, nil
	}

	// Mutual: form the match (saga). Pass spark target as JSON metadata.
	target := map[string]any{
		"target_kind": targetKind,
		"target_ref":  targetRef,
	}
	match, ferr := s.FormMatch(ctx, fromUserID, toUserID, target)
	if ferr != nil {
		// Match formation failed (e.g. message-service unreachable). The
		// Spark stays persisted so a retry has a chance to complete the
		// match later.
		slog.Warn("form match after mutual spark failed", "error", ferr)
		return sp, nil, nil
	}

	// Emit spark.matched alongside match.formed (which the match service
	// already emitted internally).
	if s.producer != nil {
		_ = s.producer.PublishSparkMatched(ctx, match.ID, match.UserA, match.UserB)
	}
	mid := match.ID
	return sp, &mid, nil
}

// ListIncomingSparks returns sparks targeted at userID that the recipient
// may see (not declined, not blocked, sender not deleted or suspended).
func (s *Service) ListIncomingSparks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*store.Spark, error) {
	return s.store.ListIncomingSparks(ctx, userID, limit, offset)
}

// RevokeSpark removes the spark only when ownerID matches the row's
// from_user_id. Used by the discover-screen "undo" affordance for sparks
// the user hasn't yet matched on.
func (s *Service) RevokeSpark(ctx context.Context, sparkID, ownerID uuid.UUID) error {
	return s.store.DeleteSpark(ctx, sparkID, ownerID)
}

// DeclineSpark lets the recipient decline a spark aimed at them. Idempotent.
// It emits nothing, so the sender is never notified; anyone other than the
// recipient gets store.ErrSparkNotFound.
func (s *Service) DeclineSpark(ctx context.Context, sparkID, recipientID uuid.UUID) (*store.Spark, error) {
	if sparkID == uuid.Nil || recipientID == uuid.Nil {
		return nil, fmt.Errorf("invalid: spark id and recipient required")
	}
	return s.store.DeclineSpark(ctx, sparkID, recipientID)
}
