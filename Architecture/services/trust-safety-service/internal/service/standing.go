package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Standing policy standing-v1 (Copyright Match plan section 6.4, P-5).
//
// trust-safety alone decides a user's standing; post-service enforces the
// answer and never re-derives it from severities. The policy is the
// founder's default today and is reversible by bumping StandingPolicyVersion
// and this table:
//
//	warning        never blocks
//	strike         3 or more active, issued in the last 90 days → suspended
//	severe_strike  1 active, issued in the last 90 days          → suspended
//	suspended_until (trust.user_trust_state) in the future      → suspended
//	otherwise                                                    → ok
//
// A suspension from strikes lasts until the newest counted strike expires;
// with a manual suspended_until as well, the later of the two wins.
// "restricted" is reserved and unused.
//
// The evaluation is a pure function of one snapshot and one clock, so it is
// deterministic and table-tested without a database.

// StandingPolicyVersion names the policy every standing answer carries.
const StandingPolicyVersion = "standing-v1"

// Standing values.
const (
	StandingOK         = "ok"
	StandingRestricted = "restricted"
	StandingSuspended  = "suspended"
)

// Severities, equal to the trust.user_strikes CHECK.
const (
	SeverityWarning      = "warning"
	SeverityStrike       = "strike"
	SeveritySevereStrike = "severe_strike"
)

const (
	// standingWindow is how far back a strike counts toward suspension.
	standingWindow = 90 * 24 * time.Hour
	// standingStrikeThreshold is the number of active `strike`s that suspend.
	standingStrikeThreshold = 3
)

// ErrUnknownSeverity is a strike with a severity this policy does not
// know. It cannot happen through the store (the CHECK refuses it), and if
// it does the answer is an error, never "ok": the caller fails closed.
var ErrUnknownSeverity = errors.New("strike has an unknown severity")

// Standing is one user's standing at EvaluatedAt.
type Standing struct {
	UserID        uuid.UUID
	Standing      string
	PolicyVersion string
	// SuspendedUntil is set exactly when Standing is suspended.
	SuspendedUntil *time.Time
	// ActiveStrikes are every strike that counts at EvaluatedAt (warnings
	// included), newest first; never nil.
	ActiveStrikes []postgres.UserStrike
	EvaluatedAt   time.Time
}

// EvaluateStanding applies standing-v1 to one snapshot at now. active must
// already be the strikes that are not voided and not expired at now (the
// store's StandingSnapshot); the window and thresholds are applied here.
func EvaluateStanding(userID uuid.UUID, now time.Time, snap *postgres.StandingSnapshot) (*Standing, error) {
	out := &Standing{
		UserID:        userID,
		Standing:      StandingOK,
		PolicyVersion: StandingPolicyVersion,
		ActiveStrikes: []postgres.UserStrike{},
		EvaluatedAt:   now,
	}
	if snap == nil {
		return out, nil
	}
	if snap.ActiveStrikes != nil {
		out.ActiveStrikes = snap.ActiveStrikes
	}

	windowStart := now.Add(-standingWindow)
	var (
		strikes, severe int
		until           time.Time
	)
	for i := range out.ActiveStrikes {
		st := &out.ActiveStrikes[i]
		if !st.Active(now) {
			return nil, fmt.Errorf("standing: strike %s is not active at %s", st.ID, now.Format(time.RFC3339))
		}
		switch st.Severity {
		case SeverityWarning:
			continue
		case SeverityStrike, SeveritySevereStrike:
		default:
			return nil, fmt.Errorf("%w: %q on strike %s", ErrUnknownSeverity, st.Severity, st.ID)
		}
		if st.CreatedAt.Before(windowStart) {
			continue
		}
		if st.Severity == SeveritySevereStrike {
			severe++
		} else {
			strikes++
		}
		if st.ExpiresAt.After(until) {
			until = st.ExpiresAt
		}
	}
	if strikes >= standingStrikeThreshold || severe >= 1 {
		out.Standing = StandingSuspended
		u := until
		out.SuspendedUntil = &u
	}
	if snap.SuspendedUntil != nil && snap.SuspendedUntil.After(now) {
		out.Standing = StandingSuspended
		if out.SuspendedUntil == nil || snap.SuspendedUntil.After(*out.SuspendedUntil) {
			u := *snap.SuspendedUntil
			out.SuspendedUntil = &u
		}
	}
	return out, nil
}

// Standing answers GET /v1/internal/standing/:userId: one query, one clock,
// the policy above.
func (s *Service) Standing(ctx context.Context, userID uuid.UUID) (*Standing, error) {
	if s.extras == nil {
		return nil, errors.New("standing is unavailable")
	}
	now := time.Now().UTC()
	snap, err := s.extras.StandingSnapshot(ctx, userID, now)
	if err != nil {
		return nil, err
	}
	return EvaluateStanding(userID, now, snap)
}
