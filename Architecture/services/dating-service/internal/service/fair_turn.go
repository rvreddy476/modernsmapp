// Fair turn (mechanic M11, DATING_FAIR_TURN_ENABLED).
//
// While a user owes replies in FairTurnLimit or more open matches — the
// newest message in each is the other person's — they cannot send new
// sparks (a Super Spark included). Accepting a spark, sparking back someone
// who sparked them, and passing stay open: those answer people rather than
// start something new.
// Chat-service keeps the count (POST /internal/v1/chat/dating-match/turns).
//
// A failed count lets the spark through: a chat outage must not stop people
// meeting, and nothing here protects anyone.
package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
)

// DefaultFairTurnLimit is DATING_FAIR_TURN_LIMIT's default.
const DefaultFairTurnLimit = 6

// TurnsCounter is the chat-service call behind the rule.
type TurnsCounter interface {
	DatingTurnsOwed(ctx context.Context, userID uuid.UUID) (int, error)
}

// FairTurnError is the refusal: the user owes Owed replies, Limit or more.
// Maps to 409 FAIR_TURN_LIMIT.
type FairTurnError struct {
	Owed  int
	Limit int
}

func (e *FairTurnError) Error() string {
	return fmt.Sprintf("reply to your matches first: %d are waiting on you", e.Owed)
}

// FairTurnView is the "fair_turn" member of GET /allowances.
type FairTurnView struct {
	Owed  int `json:"owed"`
	Limit int `json:"limit"`
	// Paused: new sparks are refused until Owed drops below Limit.
	Paused bool `json:"paused"`
}

// turnsOwed asks chat how many replies the user owes. ok is false when the
// rule is off or the count could not be had.
func (s *Service) turnsOwed(ctx context.Context, userID uuid.UUID) (int, bool) {
	if !s.mechanics.FairTurn {
		return 0, false
	}
	counter, isCounter := s.msgClient.(TurnsCounter)
	if !isCounter {
		return 0, false
	}
	owed, err := counter.DatingTurnsOwed(ctx, userID)
	if err != nil {
		slog.Warn("fair turn: count failed; letting the spark through", "user_id", userID, "error", err)
		return 0, false
	}
	return owed, true
}

// checkFairTurn refuses a new spark from userID to toUserID while userID
// owes too many replies. A spark that answers one — toUserID already sparked
// userID, so this one makes a match (accepting a spark included) — is never
// refused.
func (s *Service) checkFairTurn(ctx context.Context, userID, toUserID uuid.UUID) error {
	owed, ok := s.turnsOwed(ctx, userID)
	if !ok || owed < s.mechanics.FairTurnLimit {
		return nil
	}
	if answering, err := s.store.HasReverseSparks(ctx, userID, toUserID); err == nil && answering {
		return nil
	}
	return &FairTurnError{Owed: owed, Limit: s.mechanics.FairTurnLimit}
}

// fairTurnView is the allowances entry; nil when the rule is off or the
// count failed.
func (s *Service) fairTurnView(ctx context.Context, userID uuid.UUID) *FairTurnView {
	owed, ok := s.turnsOwed(ctx, userID)
	if !ok {
		return nil
	}
	return &FairTurnView{Owed: owed, Limit: s.mechanics.FairTurnLimit, Paused: owed >= s.mechanics.FairTurnLimit}
}
