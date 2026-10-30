package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Match chat state (audit H2 backstop) ─────────────────────────────────────
//
// chat-service's message-service asks this before it accepts a message in a
// dating conversation whose closed_at is still NULL. It reads the
// authoritative state, independent of whether a closure event has reached
// chat yet: the match must be open, the pair unblocked, and neither
// participant suspended, restricted, deleted or on a chat-blocking risk
// level, and neither held in pending_review (reported not-open while the
// review lasts; the match itself stays open).

// Reasons a match's chat is not open (MatchChatState.Reason).
const (
	ChatClosedReasonMatch     = "match_closed"
	ChatClosedReasonBlocked   = "blocked"
	ChatClosedReasonSuspended = "suspended"
	// ChatClosedReasonRestricted: a member is restricted (includes an
	// under-18 identity birth date).
	ChatClosedReasonRestricted = "restricted"
	ChatClosedReasonDeleted    = "deleted"
	ChatClosedReasonRiskHold   = "risk_hold"
	// ChatClosedReasonPendingReview: a member is in pending_review (an
	// underage report, or a moderator's review). A HOLD, not a closure: the
	// match stays open and chat resumes when a moderator clears the member.
	ChatClosedReasonPendingReview = "pending_review"
)

// MatchChatState is whether a match's members may chat.
type MatchChatState struct {
	MatchID uuid.UUID
	Open    bool
	// Reason is empty when Open; otherwise one of the ChatClosedReason
	// constants. It names the first rule that failed, never which member.
	Reason string
}

// matchChatFacts is what DecideMatchChat needs, read in one statement.
type matchChatFacts struct {
	Status  string
	Blocked bool
	// Per participant: profile_status ("" = no profile row), soft-deleted,
	// and risk level ("" = never evaluated).
	ProfileStatus [2]string
	Deleted       [2]bool
	RiskLevel     [2]string
}

// decideMatchChat applies the rules in a fixed order.
func decideMatchChat(f matchChatFacts) (bool, string) {
	switch f.Status {
	case "matched", "conversing", "quiet":
	default:
		return false, ChatClosedReasonMatch
	}
	if f.Blocked {
		return false, ChatClosedReasonBlocked
	}
	for i := 0; i < 2; i++ {
		switch {
		case f.ProfileStatus[i] == "" || f.Deleted[i] || f.ProfileStatus[i] == ProfileStatusDeleted:
			return false, ChatClosedReasonDeleted
		case f.ProfileStatus[i] == ProfileStatusSuspended:
			return false, ChatClosedReasonSuspended
		case f.ProfileStatus[i] == ProfileStatusRestricted:
			return false, ChatClosedReasonRestricted
		case RiskLevelBlocksChat(f.RiskLevel[i]):
			return false, ChatClosedReasonRiskHold
		}
	}
	// The reversible hold last, so a permanent reason on either member wins
	// (chat caches a hold briefly and a closure for longer).
	for i := 0; i < 2; i++ {
		if f.ProfileStatus[i] == ProfileStatusPendingReview {
			return false, ChatClosedReasonPendingReview
		}
	}
	return true, ""
}

// GetMatchChatState answers whether matchID's members may chat now.
// ErrMatchNotFound when the match does not exist (chat treats it as closed).
func (s *Store) GetMatchChatState(ctx context.Context, matchID uuid.UUID) (*MatchChatState, error) {
	if matchID == uuid.Nil {
		return nil, ErrMatchNotFound
	}
	var (
		f                matchChatFacts
		statusA, statusB *string
		delA, delB       *bool
		riskA, riskB     *string
	)
	err := s.db.QueryRow(ctx, `
        SELECT m.status,
               `+blockedPairPredicate("m.user_a", "m.user_b")+`,
               pa.profile_status, pa.deleted_at IS NOT NULL,
               pb.profile_status, pb.deleted_at IS NOT NULL,
               ra.risk_level, rb.risk_level
        FROM dating_matches m
        LEFT JOIN dating_profiles pa ON pa.user_id = m.user_a
        LEFT JOIN dating_profiles pb ON pb.user_id = m.user_b
        LEFT JOIN dating_account_risk ra ON ra.user_id = m.user_a
        LEFT JOIN dating_account_risk rb ON rb.user_id = m.user_b
        WHERE m.id = $1`, matchID).Scan(&f.Status, &f.Blocked,
		&statusA, &delA, &statusB, &delB, &riskA, &riskB)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMatchNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("match chat state: %w", err)
	}
	f.ProfileStatus = [2]string{deref(statusA), deref(statusB)}
	f.Deleted = [2]bool{delA != nil && *delA, delB != nil && *delB}
	f.RiskLevel = [2]string{deref(riskA), deref(riskB)}
	open, reason := decideMatchChat(f)
	return &MatchChatState{MatchID: matchID, Open: open, Reason: reason}, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
