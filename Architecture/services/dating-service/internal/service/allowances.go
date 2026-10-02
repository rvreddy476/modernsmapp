// Allowances (mechanic M10): one read of every daily allowance the caller
// has, so a client can show "3 left today" and the "out of …" states with
// their reset time before an action is refused.
//
// A mechanic whose flag is off is ABSENT from the response, which is also how
// a client learns which mechanics are on. Sparks are always present.
package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// SuperSparkAllowance is the daily Super Spark allowance plus the purchased
// balance (omitted at 0), which is spent once the daily allowance is used.
type SuperSparkAllowance struct {
	Allowance
	PurchasedBalance int `json:"purchased_balance,omitempty"`
}

// AllowancesResponse is GET /v1/dating/allowances.
type AllowancesResponse struct {
	Sparks     Allowance            `json:"sparks"`
	Deck       *Allowance           `json:"deck,omitempty"`
	Rewind     *Allowance           `json:"rewind,omitempty"`
	SuperSpark *SuperSparkAllowance `json:"super_spark,omitempty"`
	// FairTurn (M11): how many matches wait on the caller's reply.
	FairTurn *FairTurnView `json:"fair_turn,omitempty"`
}

// SparkLimitError is the spent spark allowance, with when it starts to come
// back. It unwraps to store.ErrSparkRateLimited, so existing checks still
// match. Maps to 429 SPARK_RATE_LIMITED.
type SparkLimitError struct {
	Limit    int
	ResetsAt *time.Time
}

func (e *SparkLimitError) Error() string { return store.ErrSparkRateLimited.Error() }
func (e *SparkLimitError) Unwrap() error { return store.ErrSparkRateLimited }

// sparkLimitError builds the refusal for a sender whose allowance is spent.
func (s *Service) sparkLimitError(ctx context.Context, userID uuid.UUID, limit int) error {
	out := &SparkLimitError{Limit: limit}
	if _, oldest, err := s.store.SparkUsage(ctx, userID); err == nil && oldest != nil {
		resets := oldest.Add(store.SparkQuotaWindow).UTC()
		out.ResetsAt = &resets
	}
	return out
}

// Allowances returns every allowance the caller has right now.
func (s *Service) Allowances(ctx context.Context, userID uuid.UUID) (*AllowancesResponse, error) {
	if userID == uuid.Nil {
		return nil, errors.New("invalid: user_id required")
	}
	limit := s.sparkDailyLimit(ctx, userID)
	used, oldest, err := s.store.SparkUsage(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &AllowancesResponse{Sparks: allowanceOf(limit, used, oldest, store.SparkQuotaWindow)}

	if s.mechanics.DeckRefill {
		used, oldest, err := s.store.DeckUsage(ctx, userID)
		if err != nil {
			return nil, err
		}
		deck := allowanceOf(s.deckDailyLimit(ctx, userID), used, oldest, store.DeckQuotaWindow)
		out.Deck = &deck
	}
	if s.mechanics.Rewind {
		rewind, err := s.RewindAllowance(ctx, userID)
		if err != nil {
			return nil, err
		}
		out.Rewind = &rewind
	}
	if s.mechanics.SuperSpark {
		daily, balance, err := s.SuperSparkAllowance(ctx, userID)
		if err != nil {
			return nil, err
		}
		out.SuperSpark = &SuperSparkAllowance{Allowance: daily, PurchasedBalance: balance}
	}
	out.FairTurn = s.fairTurnView(ctx, userID)
	return out, nil
}
