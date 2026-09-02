package service

// Account control — TikTok-style "Deactivate" (reversible by logging in) and
// "Delete permanently" (30-day recovery window, then the purge state machine
// in internal/purge takes over).
//
// Both entry points re-verify the password even though the caller already
// holds a session: an unattended signed-in device must not be enough to
// deactivate or schedule deletion of someone's account.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrInvalidPassword — the re-verification password did not match.
	// Mapped to 401 INVALID_PASSWORD by the handler.
	ErrInvalidPassword = errors.New("password verification failed")
	// ErrAccountPurged — terminal. Every service has erased this user; the
	// credential row is anonymised. Mapped to 403 ACCOUNT_PURGED.
	ErrAccountPurged = errors.New("this account has been permanently deleted")
	// ErrAccountPendingPurge — the 30-day window has elapsed and the purge
	// pipeline is running (or about to). Logging in can no longer rescue the
	// account, because downstream erasure may already be partial — restoring
	// the auth row would resurrect an account with holes in it. Mapped to
	// 403 ACCOUNT_PENDING_PURGE.
	ErrAccountPendingPurge = errors.New(
		"this account's deletion grace period has ended and permanent deletion is in progress")
	// ErrLifecycleConflict — the account was not in a state that allows the
	// requested transition (e.g. deactivating a suspended account). Mapped
	// to 409 ACCOUNT_STATE_CONFLICT.
	ErrLifecycleConflict = errors.New("the account is not in a state that allows this action")
)

// lifecycleAction is what a login must do before minting a session.
type lifecycleAction int

const (
	lifecycleProceed        lifecycleAction = iota // no transition needed
	lifecycleReactivate                            // deactivated → active
	lifecycleCancelDeletion                        // pending_deletion (window open) → active
)

// resolveLoginLifecycle is the PURE state machine: given the account status
// and purge schedule, decide what a successful password login does. Kept free
// of I/O so the transition table can be tested exhaustively.
//
// A pending_deletion row with a NULL scheduled_purge_date can exist from the
// pre-worker era (SR-7 deliberately nulled the column). There is no date to
// wait for and no way to know what downstream consumers did with the old
// user.deletion_requested event, so it is treated as past-window: fail closed
// with ErrAccountPendingPurge rather than resurrect it.
func resolveLoginLifecycle(status string, scheduledPurge *time.Time, now time.Time) (lifecycleAction, error) {
	switch status {
	case store.AccountStatusDeactivated:
		return lifecycleReactivate, nil
	case store.AccountStatusPendingDeletion:
		if scheduledPurge != nil && scheduledPurge.After(now) {
			return lifecycleCancelDeletion, nil
		}
		return lifecycleProceed, ErrAccountPendingPurge
	case store.AccountStatusPurged:
		return lifecycleProceed, ErrAccountPurged
	default:
		// active, suspended, pending_verification, anything else: no
		// lifecycle transition here. Suspended and pending are enforced by
		// their own gates (createSessionForUser / the LB-5 check).
		return lifecycleProceed, nil
	}
}

// applyLoginLifecycle executes the state machine for a password-verified
// login and mutates `user` in place so the rest of the login path (and the
// response body) sees the post-transition state.
func (s *Service) applyLoginLifecycle(ctx context.Context, user *store.User) error {
	action, err := resolveLoginLifecycle(user.AccountStatus, user.ScheduledPurgeDate, time.Now())
	if err != nil {
		return err
	}

	switch action {
	case lifecycleReactivate:
		done, rerr := s.store.ReactivateUser(ctx, user.ID)
		if rerr != nil {
			return fmt.Errorf("reactivate on login: %w", rerr)
		}
		// done=false means a concurrent login already reactivated — the
		// desired state holds either way.
		user.AccountStatus = store.AccountStatusActive
		user.DeactivatedAt = nil
		s.log.Info("account reactivated by login", "user_id", user.ID, "raced", !done)

	case lifecycleCancelDeletion:
		done, cerr := s.store.CancelDeletion(ctx, user.ID)
		if cerr != nil {
			return fmt.Errorf("cancel deletion on login: %w", cerr)
		}
		if !done {
			// The guarded UPDATE found no open window: between the read and
			// the write the date elapsed (or a worker/admin moved the row
			// on). Re-fail closed rather than mint a session for an account
			// that may be mid-purge.
			return ErrAccountPendingPurge
		}
		user.AccountStatus = store.AccountStatusActive
		user.DeletionRequestedAt = nil
		user.ScheduledPurgeDate = nil
		s.log.Info("scheduled deletion cancelled by login", "user_id", user.ID)
	}
	return nil
}

// reverifyPassword loads the user and compares the supplied password.
// Failures are uniformly ErrInvalidPassword so the endpoint does not become
// an oracle for account state.
func (s *Service) reverifyPassword(ctx context.Context, userID uuid.UUID, password string) (*store.User, error) {
	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.PasswordHash == "" {
		return nil, ErrInvalidPassword
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidPassword
	}
	return user, nil
}

// DeactivateAccount re-verifies the password, revokes EVERY session (before
// the flip, same A14 ordering as deletion, so no stolen refresh token can
// outlive the state change), then marks the account deactivated and emits
// user.deactivated. Reactivation is the next successful login — there is
// deliberately no reactivate endpoint.
func (s *Service) DeactivateAccount(ctx context.Context, userID uuid.UUID, password string) error {
	if _, err := s.reverifyPassword(ctx, userID, password); err != nil {
		return err
	}

	sessions, _ := s.store.ListActiveSessions(ctx, userID)
	if _, err := s.store.RevokeAllSessions(ctx, userID); err != nil {
		return fmt.Errorf("deactivate: failed to revoke sessions: %w", err)
	}
	for _, sess := range sessions {
		s.cacheRevoke(ctx, sess.ID)
	}

	if err := s.store.DeactivateUser(ctx, userID); err != nil {
		if errors.Is(err, store.ErrLifecycleConflict) {
			return ErrLifecycleConflict
		}
		return err
	}
	s.log.Info("account deactivated", "user_id", userID)
	return nil
}

// DeletionSchedule is what DELETE /v1/auth/account returns.
type DeletionSchedule struct {
	AccountStatus      string    `json:"account_status"`
	ScheduledPurgeDate time.Time `json:"scheduled_purge_date"`
	CancelByLoggingIn  bool      `json:"cancel_by_logging_in"`
}

// DeleteAccount re-verifies the password, revokes every session, then flips
// the account to pending_deletion with a purge date 30 days out and emits
// user.deletion_scheduled. NOTHING is erased at request time: the
// irreversible purge starts only when internal/purge's worker emits
// user.purge_requested after the window closes, and completes only when every
// required service has acked.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID, password string) (*DeletionSchedule, error) {
	if _, err := s.reverifyPassword(ctx, userID, password); err != nil {
		return nil, err
	}

	sessions, _ := s.store.ListActiveSessions(ctx, userID)
	if revoked, err := s.store.RevokeAllSessions(ctx, userID); err != nil {
		return nil, fmt.Errorf("account deletion: failed to revoke sessions: %w", err)
	} else {
		s.log.Info("account deletion: revoked sessions", "user_id", userID, "revoked", revoked)
	}
	for _, sess := range sessions {
		s.cacheRevoke(ctx, sess.ID)
	}

	purgeDate, err := s.store.ScheduleDeletion(ctx, userID)
	if err != nil {
		if errors.Is(err, store.ErrLifecycleConflict) {
			return nil, ErrLifecycleConflict
		}
		return nil, fmt.Errorf("failed to schedule deletion: %w", err)
	}
	s.log.Info("account deletion scheduled",
		"user_id", userID, "scheduled_purge_date", purgeDate.UTC().Format(time.RFC3339))

	return &DeletionSchedule{
		AccountStatus:      store.AccountStatusPendingDeletion,
		ScheduledPurgeDate: purgeDate.UTC(),
		CancelByLoggingIn:  true,
	}, nil
}
