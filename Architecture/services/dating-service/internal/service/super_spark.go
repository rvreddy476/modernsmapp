// Super Spark (mechanic M3, DATING_SUPER_SPARK_ENABLED): a spark the
// recipient sees marked and first in their incoming list. It passes every
// gate an ordinary spark passes (risk, age, profile status, block, decline
// cooldown, note moderation, the spark allowance) and then is charged: the
// daily allowance first (MechanicsConfig.SuperSparkDailyLimitFree, or
// ...Pass for a pass holder), then one purchased Super Spark. Packs are
// bought like every other product, through payments-service.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Daily Super Spark allowances per store.SuperSparkQuotaWindow.
const (
	DefaultSuperSparkDailyLimitFree = 1
	DefaultSuperSparkDailyLimitPass = 5
)

// SuperSparkLimitError: the daily allowance is used and no purchased Super
// Spark is left. Maps to 429 SUPER_SPARK_LIMIT_REACHED.
type SuperSparkLimitError struct {
	Limit    int
	ResetsAt *time.Time
}

func (e *SuperSparkLimitError) Error() string { return "super spark limit reached; try again later" }

// superSparkDailyLimit is the caller's daily Super Spark allowance: the pass
// allowance while they hold an unexpired pass, the free one otherwise (and
// when the pass lookup fails).
func (s *Service) superSparkDailyLimit(ctx context.Context, userID uuid.UUID) int {
	premium, err := s.store.IsPremium(ctx, userID)
	if err != nil {
		slog.Warn("super spark limit: premium lookup failed; using the free allowance", "user_id", userID, "error", err)
		return s.mechanics.SuperSparkDailyLimitFree
	}
	if premium {
		return s.mechanics.SuperSparkDailyLimitPass
	}
	return s.mechanics.SuperSparkDailyLimitFree
}

// superSparkLimitError builds the refusal with when the allowance resets.
func (s *Service) superSparkLimitError(ctx context.Context, userID uuid.UUID, limit int) error {
	out := &SuperSparkLimitError{Limit: limit}
	if _, oldest, _, err := s.store.SuperSparkUsage(ctx, userID); err == nil && oldest != nil {
		resets := oldest.Add(store.SuperSparkQuotaWindow).UTC()
		out.ResetsAt = &resets
	}
	return out
}

// SuperSparkAllowance is the caller's daily Super Spark allowance right now
// and their purchased balance.
func (s *Service) SuperSparkAllowance(ctx context.Context, userID uuid.UUID) (Allowance, int, error) {
	limit := s.superSparkDailyLimit(ctx, userID)
	used, oldest, balance, err := s.store.SuperSparkUsage(ctx, userID)
	if err != nil {
		return Allowance{}, 0, err
	}
	return allowanceOf(limit, used, oldest, store.SuperSparkQuotaWindow), balance, nil
}

// CreateSuperSpark is CreateSpark sent as a Super Spark.
func (s *Service) CreateSuperSpark(ctx context.Context, fromUserID, toUserID uuid.UUID, targetKind, targetRef, note string) (*store.Spark, *uuid.UUID, error) {
	if !s.mechanics.SuperSpark {
		return nil, nil, ErrMechanicDisabled
	}
	sp, matchID, err := s.createSpark(ctx, fromUserID, toUserID, targetKind, targetRef, note, true)
	if err == nil {
		s.recordDeckAction(ctx, fromUserID, toUserID, store.DeckActionSuperSpark)
	}
	return sp, matchID, err
}
