// Verification status (lane D10) — one read the app can poll after a selfie
// submission and on the profile screen.
package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Selfie states reported by GET /v1/dating/verification/status. They are the
// stored statuses with pending_review renamed to the client-facing "review",
// plus "none" for a user who has never submitted one.
const (
	SelfieStateNone    = "none"
	SelfieStatePending = "pending"
	SelfieStateReview  = "review"
	SelfieStatePassed  = "passed"
	SelfieStateFailed  = "failed"
)

// Next steps the client renders.
const (
	VerificationNextStepSelfie       = "submit_selfie"
	VerificationNextStepWaitForAdmin = "wait_for_review"
	VerificationNextStepRetryToday   = "retry_tomorrow"
	VerificationNextStepNone         = "none"
)

// SelfieStatus is the selfie half of the verification status.
type SelfieStatus struct {
	// State is none | pending | review | passed | failed.
	State string `json:"state"`
	// AttemptsLeftToday is the remaining attempts in the rolling
	// store.SelfieAttemptWindow; 0 means the next submission is refused
	// with 429 SELFIE_ATTEMPTS_EXCEEDED.
	AttemptsLeftToday int `json:"attempts_left_today"`
	AttemptsPerDay    int `json:"attempts_per_day"`
	WindowHours       int `json:"window_hours"`
}

// VerificationStatus is what GET /v1/dating/verification/status returns.
type VerificationStatus struct {
	Selfie SelfieStatus `json:"selfie"`
	// Aadhaar is the optional DigiLocker outcome ("verified" or empty).
	Aadhaar string `json:"aadhaar_status,omitempty"`
	// TrustTier and Verified are the badge the rest of the app reads.
	TrustTier string `json:"trust_tier"`
	Verified  bool   `json:"verified"`
	// ProfileStatus is the lane D2 status the selfie gates.
	ProfileStatus string `json:"profile_status,omitempty"`
	// NextStep is submit_selfie | wait_for_review | retry_tomorrow | none.
	NextStep string `json:"next_step"`
}

// selfieStateOf maps the stored status to the client-facing state.
func selfieStateOf(stored *string) string {
	if stored == nil {
		return SelfieStateNone
	}
	switch *stored {
	case store.SelfieStatusPassed:
		return SelfieStatePassed
	case store.SelfieStatusPendingReview:
		return SelfieStateReview
	case store.SelfieStatusFailed:
		return SelfieStateFailed
	case store.SelfieStatusPending:
		return SelfieStatePending
	}
	return SelfieStateNone
}

// GetVerificationStatus reports the selfie state, how many attempts are left
// in the window and what the user should do next. It never starts an attempt
// and never consumes a challenge.
func (s *Service) GetVerificationStatus(ctx context.Context, userID uuid.UUID) (*VerificationStatus, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	cfg := s.SelfieSettings()
	out := &VerificationStatus{
		Selfie: SelfieStatus{
			State:          SelfieStateNone,
			AttemptsPerDay: cfg.MaxAttemptsPerDay,
			WindowHours:    int(store.SelfieAttemptWindow.Hours()),
		},
		TrustTier: "phone",
		NextStep:  VerificationNextStepSelfie,
	}
	v, err := s.store.GetVerification(ctx, userID)
	if err != nil && !errors.Is(err, store.ErrVerificationNotFound) {
		return nil, err
	}
	if v != nil {
		out.Selfie.State = selfieStateOf(v.SelfieStatus)
		if v.AadhaarStatus != nil {
			out.Aadhaar = *v.AadhaarStatus
		}
	}
	used, err := s.store.CountSelfieAttemptsInWindow(ctx, userID)
	if err != nil {
		return nil, err
	}
	if left := cfg.MaxAttemptsPerDay - used; left > 0 {
		out.Selfie.AttemptsLeftToday = left
	}
	if p, perr := s.store.GetProfile(ctx, userID); perr == nil && p != nil {
		out.ProfileStatus = p.ProfileStatus
		out.TrustTier = p.TrustTier
	} else if perr != nil && !errors.Is(perr, store.ErrProfileNotFound) {
		return nil, perr
	}
	out.Verified = verifiedTier(out.TrustTier)
	switch {
	case out.Selfie.State == SelfieStatePassed:
		out.NextStep = VerificationNextStepNone
	case out.Selfie.State == SelfieStateReview:
		out.NextStep = VerificationNextStepWaitForAdmin
	case out.Selfie.AttemptsLeftToday == 0:
		out.NextStep = VerificationNextStepRetryToday
	default:
		out.NextStep = VerificationNextStepSelfie
	}
	return out, nil
}
