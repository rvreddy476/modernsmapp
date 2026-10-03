// Rewind (mechanic M2, DATING_REWIND_ENABLED): undo the caller's last pass.
//
// Only a pass can be undone, only the most recent one, and only while nothing
// came after it: a spark is never undone (it may already have matched), and a
// second rewind does not walk further back. Free users get
// MechanicsConfig.RewindDailyLimitFree per rolling 24 hours; a pass holder is
// not limited. The allowance is checked in the same transaction as the undo.
package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/dating-service/internal/matcher"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// DefaultRewindDailyLimitFree is the free rewind allowance per
// store.RewindQuotaWindow.
const DefaultRewindDailyLimitFree = 1

// ErrMechanicDisabled: the route belongs to a mechanic whose flag is off.
// Maps to 404 MECHANIC_NOT_ENABLED, the same answer for every mechanic.
var ErrMechanicDisabled = errors.New("not_found: this feature is not enabled")

// ErrNothingToRewind is the store sentinel, re-exported for handlers. Maps to
// 409 REWIND_NOTHING_TO_UNDO.
var ErrNothingToRewind = store.ErrNothingToRewind

// RewindLimitError is the spent free allowance. Maps to 429
// REWIND_LIMIT_REACHED with the limit and when it resets.
type RewindLimitError struct {
	Limit    int
	ResetsAt *time.Time
}

func (e *RewindLimitError) Error() string { return "rewind limit reached; try again later" }

// Allowance is one daily allowance as a client shows it. Unlimited is true
// for a pass holder on an allowance a pass lifts; then the counts are
// omitted. RemainingToday is omitted at 0 and ResetsAt while nothing is used.
type Allowance struct {
	Unlimited      bool       `json:"unlimited"`
	DailyLimit     int        `json:"daily_limit,omitempty"`
	RemainingToday int        `json:"remaining_today,omitempty"`
	ResetsAt       *time.Time `json:"resets_at,omitempty"`
}

// allowanceOf builds an Allowance from a limit and the usage in its window.
func allowanceOf(limit, used int, oldest *time.Time, window time.Duration) Allowance {
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	a := Allowance{DailyLimit: limit, RemainingToday: remaining}
	if oldest != nil {
		resets := oldest.Add(window).UTC()
		a.ResetsAt = &resets
	}
	return a
}

// RewindResult is the body of POST /v1/dating/pulse/rewind.
type RewindResult struct {
	Rewound     bool      `json:"rewound"`
	CandidateID uuid.UUID `json:"candidate_id"`
	// Card is the person's deck card again, so the client can put it back on
	// top of the stack without refetching.
	Card      *PulseCard `json:"card,omitempty"`
	Allowance Allowance  `json:"allowance"`
}

// rewindLimit is the caller's rewind allowance: 0 (not limited) while they
// hold an unexpired pass, the free allowance otherwise — and when the pass
// lookup fails, because an outage never widens an allowance.
func (s *Service) rewindLimit(ctx context.Context, userID uuid.UUID) int {
	premium, err := s.store.IsPremium(ctx, userID)
	if err != nil {
		slog.Warn("rewind limit: premium lookup failed; using the free allowance", "user_id", userID, "error", err)
		return s.mechanics.RewindDailyLimitFree
	}
	if premium {
		return 0
	}
	return s.mechanics.RewindDailyLimitFree
}

// RewindAllowance is the caller's rewind allowance right now.
func (s *Service) RewindAllowance(ctx context.Context, userID uuid.UUID) (Allowance, error) {
	limit := s.rewindLimit(ctx, userID)
	if limit <= 0 {
		return Allowance{Unlimited: true}, nil
	}
	used, oldest, err := s.store.RewindUsage(ctx, userID)
	if err != nil {
		return Allowance{}, err
	}
	return allowanceOf(limit, used, oldest, store.RewindQuotaWindow), nil
}

// RewindLastPass undoes the caller's last pass and returns the card.
func (s *Service) RewindLastPass(ctx context.Context, viewerID uuid.UUID) (*RewindResult, error) {
	if !s.mechanics.Rewind {
		return nil, ErrMechanicDisabled
	}
	if err := s.requireAdult(ctx, viewerID); err != nil {
		return nil, err
	}
	if err := s.requireInteractiveProfile(ctx, viewerID); err != nil {
		return nil, err
	}
	candidateID, err := s.store.LastRewindablePass(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	// The person must still be someone the caller could be shown: an active
	// adult, not blocked either way. The same single refusal as a spark, and
	// it costs no rewind.
	if err := s.requireActiveAdultCandidate(ctx, candidateID); err != nil {
		return nil, err
	}
	if err := s.requireNotBlocked(ctx, viewerID, candidateID); err != nil {
		return nil, err
	}
	limit := s.rewindLimit(ctx, viewerID)
	if err := s.store.RewindPass(ctx, viewerID, candidateID, limit); err != nil {
		if errors.Is(err, store.ErrRewindLimited) {
			limited := &RewindLimitError{Limit: limit}
			if _, oldest, uerr := s.store.RewindUsage(ctx, viewerID); uerr == nil && oldest != nil {
				resets := oldest.Add(store.RewindQuotaWindow).UTC()
				limited.ResetsAt = &resets
			}
			return nil, limited
		}
		return nil, err
	}
	// The cached batch was built while the person was passed.
	s.InvalidatePulseCache(ctx, viewerID)

	out := &RewindResult{Rewound: true, CandidateID: candidateID}
	out.Card = s.cardFor(ctx, viewerID, candidateID)
	if out.Allowance, err = s.RewindAllowance(ctx, viewerID); err != nil {
		slog.Warn("rewind: allowance lookup failed after the rewind", "user_id", viewerID, "error", err)
		out.Allowance = Allowance{DailyLimit: limit}
	}
	return out, nil
}

// cardFor builds one candidate's deck card for the viewer, or nil when the
// viewer may not see them. Unscored: the card is being handed back, not
// ranked.
func (s *Service) cardFor(ctx context.Context, viewerID, candidateID uuid.UUID) *PulseCard {
	c, err := s.store.GetCandidateForViewer(ctx, viewerID, candidateID)
	if err != nil || c == nil {
		return nil
	}
	viewer, _ := s.viewerProfile(ctx, viewerID)
	matched, err := s.store.ListActiveMatchPartnerIDs(ctx, viewerID)
	if err != nil {
		matched = map[uuid.UUID]struct{}{}
	}
	prompts, photos := s.detailFor(ctx, []uuid.UUID{candidateID})
	card := s.buildCard(matcher.ScoredCandidate{Candidate: c, Reasons: []matcher.MatchReason{}}, viewer, matched, prompts[candidateID], photos[candidateID])
	return &card
}
