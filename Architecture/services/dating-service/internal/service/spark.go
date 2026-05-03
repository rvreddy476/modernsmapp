// Spark service — orchestrates Spark create / list / revoke and the
// mutual-Spark match-formation hand-off.
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

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

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

	// Lightweight existence check on the target. We don't crash if the
	// target profile is missing in test setups; just return invalid so the
	// caller (handler) maps to 400.
	if _, err := s.store.GetProfile(ctx, toUserID); err != nil && !errors.Is(err, store.ErrProfileNotFound) {
		// Real DB error.
		return nil, nil, fmt.Errorf("load target profile: %w", err)
	}

	sp, err := s.store.CreateSpark(ctx, fromUserID, toUserID, targetKind, targetRef, note)
	if err != nil {
		return nil, nil, err
	}

	// Always emit spark.created.
	if s.producer != nil {
		if perr := s.producer.PublishSparkCreated(ctx, sp.ID, fromUserID, toUserID, targetKind, targetRef, note); perr != nil {
			slog.Warn("publish spark.created failed", "spark_id", sp.ID, "error", perr)
		}
	}

	// Mutual-Spark check: did `toUserID` already Spark `fromUserID`?
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

// ListIncomingSparks returns sparks targeted at userID.
func (s *Service) ListIncomingSparks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*store.Spark, error) {
	return s.store.ListIncomingSparks(ctx, userID, limit, offset)
}

// RevokeSpark removes the spark only when ownerID matches the row's
// from_user_id. Used by the discover-screen "undo" affordance for sparks
// the user hasn't yet matched on.
func (s *Service) RevokeSpark(ctx context.Context, sparkID, ownerID uuid.UUID) error {
	return s.store.DeleteSpark(ctx, sparkID, ownerID)
}
