// In-match extras (mechanic M9).
//
// Read receipts (DATING_READ_RECEIPTS_ENABLED): an opt-in for pass holders.
// While a user has it on and holds a pass, they see when their matches read
// their messages; chat-service enforces it from the until-time dating sends
// (the pass expiry), so a pass running out ends it without any job. The
// setting is pushed to chat when it changes, when a match forms and when a
// payment grants or revokes a pass.
//
// Calls after an exchange (DATING_CALL_AFTER_EXCHANGE_ENABLED): a call can
// start only once both people have sent at least one message. chat-service
// keeps the stamps and answers graph-service's call grant; the match view's
// can_call is that same answer, so the button and the grant agree.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// ErrReadReceiptsRequirePass maps to 403 READ_RECEIPTS_REQUIRE_PASS.
var ErrReadReceiptsRequirePass = errors.New("forbidden: read receipts come with a pass")

// ReadReceiptsSetter pushes a member's read-receipt until-time to chat.
type ReadReceiptsSetter interface {
	SetReadReceipts(ctx context.Context, matchID, userID uuid.UUID, until *time.Time) error
}

// MatchCallChecker asks chat whether a pair may call now.
type MatchCallChecker interface {
	MatchCallable(ctx context.Context, userA, userB uuid.UUID) (bool, error)
}

// ReadReceiptsState is GET/PUT /v1/dating/read-receipts.
type ReadReceiptsState struct {
	// Enabled is the user's choice; Active is whether it applies now (it
	// needs a pass); Available is whether they hold one.
	Enabled   bool `json:"enabled"`
	Active    bool `json:"active"`
	Available bool `json:"available"`
}

// GetReadReceipts returns the caller's read-receipts state.
func (s *Service) GetReadReceipts(ctx context.Context, userID uuid.UUID) (*ReadReceiptsState, error) {
	if !s.mechanics.ReadReceipts {
		return nil, ErrMechanicDisabled
	}
	enabled, err := s.store.ReadReceiptsEnabled(ctx, userID)
	if err != nil {
		return nil, err
	}
	pass := s.holdsPass(ctx, userID)
	return &ReadReceiptsState{Enabled: enabled, Active: enabled && pass, Available: pass}, nil
}

// PutReadReceipts sets the caller's opt-in. Turning it on needs a pass;
// turning it off never does.
func (s *Service) PutReadReceipts(ctx context.Context, userID uuid.UUID, enabled bool) (*ReadReceiptsState, error) {
	if !s.mechanics.ReadReceipts {
		return nil, ErrMechanicDisabled
	}
	if enabled && !s.holdsPass(ctx, userID) {
		return nil, ErrReadReceiptsRequirePass
	}
	if err := s.store.SetReadReceiptsEnabled(ctx, userID, enabled); err != nil {
		return nil, err
	}
	s.syncReadReceipts(ctx, userID)
	return s.GetReadReceipts(ctx, userID)
}

// readReceiptsUntil is until when chat should show the user read receipts:
// their pass expiry while the mechanic is on, they opted in and the pass is
// active; nil otherwise. A failed lookup is nil (receipts withheld).
func (s *Service) readReceiptsUntil(ctx context.Context, userID uuid.UUID) *time.Time {
	if !s.mechanics.ReadReceipts {
		return nil
	}
	enabled, err := s.store.ReadReceiptsEnabled(ctx, userID)
	if err != nil || !enabled {
		return nil
	}
	ent, err := s.store.GetPremiumEntitlement(ctx, userID)
	if err != nil || !ent.PassActive || ent.PassExpiresAt == nil {
		return nil
	}
	until := ent.PassExpiresAt.UTC()
	return &until
}

// syncReadReceipts pushes the user's until-time to every open match's
// conversation. Best effort: a failed push leaves chat's previous value,
// which can only be the old pass expiry or nil.
func (s *Service) syncReadReceipts(ctx context.Context, userID uuid.UUID) {
	if !s.mechanics.ReadReceipts {
		return
	}
	setter, ok := s.msgClient.(ReadReceiptsSetter)
	if !ok {
		return
	}
	matches, err := s.store.OpenMatchesWithConversation(ctx, userID)
	if err != nil {
		slog.Warn("read receipts: open matches lookup failed", "user_id", userID, "error", err)
		return
	}
	until := s.readReceiptsUntil(ctx, userID)
	for _, m := range matches {
		if err := setter.SetReadReceipts(ctx, m.ID, userID, until); err != nil {
			slog.Warn("read receipts: push to chat failed", "match_id", m.ID, "user_id", userID, "error", err)
		}
	}
}

// syncMatchReadReceipts pushes both members' until-times for one new match.
func (s *Service) syncMatchReadReceipts(ctx context.Context, m *store.Match) {
	if !s.mechanics.ReadReceipts || m == nil {
		return
	}
	setter, ok := s.msgClient.(ReadReceiptsSetter)
	if !ok {
		return
	}
	for _, u := range []uuid.UUID{m.UserA, m.UserB} {
		if err := setter.SetReadReceipts(ctx, m.ID, u, s.readReceiptsUntil(ctx, u)); err != nil {
			slog.Warn("read receipts: push to chat failed", "match_id", m.ID, "user_id", u, "error", err)
		}
	}
}

// canCall is the match view's can_call: chat's answer, false on any error
// (the call grant fails closed the same way).
func (s *Service) canCall(ctx context.Context, m *store.Match) *bool {
	if !s.mechanics.CallAfterExchange || m == nil {
		return nil
	}
	answer := false
	if checker, ok := s.msgClient.(MatchCallChecker); ok {
		if open, err := checker.MatchCallable(ctx, m.UserA, m.UserB); err == nil {
			answer = open
		} else {
			slog.Warn("can_call: chat state lookup failed", "match_id", m.ID, "error", err)
		}
	}
	return &answer
}
