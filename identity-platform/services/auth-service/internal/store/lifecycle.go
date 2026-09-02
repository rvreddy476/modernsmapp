package store

// Account control — deactivate / delete-with-30-day-window / purge.
//
// Every mutation here pairs the row change with its outbox event in ONE
// transaction, so a consumer can never observe an event without the state or
// the state without the event. Event names are string literals matching the
// constants registered in Architecture/shared/events/events.go
// (EventUserDeactivated etc.) — this module cannot import that one.
//
// State machine (account_status is free text; the allowed set is enforced in
// code, same as pending_verification):
//
//	active ──POST /account/deactivate──▶ deactivated ──login──▶ active
//	active ──DELETE /account──▶ pending_deletion (scheduled_purge_date = +30d)
//	pending_deletion ──login before purge date──▶ active
//	pending_deletion ──worker, date elapsed──▶ user.purge_requested … acks …
//	  ──all required services acked──▶ purged (terminal, row anonymised)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	AccountStatusDeactivated     = "deactivated"
	AccountStatusPendingDeletion = "pending_deletion"
	AccountStatusPurged          = "purged"
	AccountStatusSuspended       = "suspended"
)

// Outbox event types for the account-control lifecycle. Mirrors of the
// constants in Architecture/shared/events/events.go.
const (
	EventUserDeactivated       = "user.deactivated"
	EventUserReactivated       = "user.reactivated"
	EventUserDeletionScheduled = "user.deletion_scheduled"
	EventUserDeletionCancelled = "user.deletion_cancelled"
	EventUserPurgeRequested    = "user.purge_requested"
	EventUserPurged            = "user.purged"
)

// DeletionGracePeriod is the recovery window between "Delete account" and the
// purge worker's first user.purge_requested. TikTok-standard 30 days.
const DeletionGracePeriod = 30 * 24 * time.Hour

// purgeRequestInterval throttles re-emitting user.purge_requested for a user
// whose acks have not all arrived yet.
const purgeRequestInterval = 24 * time.Hour

// ErrLifecycleConflict means the guarded UPDATE matched no row: the account
// was not in the state the transition requires (already transitioned, or a
// concurrent request won). Callers treat it as "nothing to do" or map it to a
// client conflict — never as data loss.
var ErrLifecycleConflict = errors.New("account is not in the required state for this transition")

// outboxInsertTx writes one outbox row inside the caller's transaction.
func outboxInsertTx(ctx context.Context, tx pgx.Tx, eventType string, userID uuid.UUID, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", eventType, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO auth.outbox_events (event_type, partition_key, payload) VALUES ($1, $2, $3::jsonb)`,
		eventType, userID.String(), b,
	); err != nil {
		return fmt.Errorf("outbox insert %s: %w", eventType, err)
	}
	return nil
}

// DeactivateUser flips an ACTIVE account to 'deactivated' and emits
// user.deactivated. Guarded on account_status='active' so a suspended or
// pending-deletion account cannot be laundered into a merely-deactivated one.
func (s *Store) DeactivateUser(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("deactivate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	ct, err := tx.Exec(ctx, `
		UPDATE auth.users
		SET account_status = $2, deactivated_at = $3, updated_at = NOW()
		WHERE user_id = $1 AND account_status = $4
	`, userID, AccountStatusDeactivated, now, AccountStatusActive)
	if err != nil {
		return fmt.Errorf("deactivate: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrLifecycleConflict
	}

	if err := outboxInsertTx(ctx, tx, EventUserDeactivated, userID, map[string]any{
		"user_id":        userID.String(),
		"deactivated_at": now.Format(time.RFC3339),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReactivateUser is the login-side reverse of DeactivateUser. Guarded on
// account_status='deactivated'; a no-match is NOT an error for the login path
// (a concurrent login already reactivated), so it returns (false, nil) then.
func (s *Store) ReactivateUser(ctx context.Context, userID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("reactivate: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	ct, err := tx.Exec(ctx, `
		UPDATE auth.users
		SET account_status = $2, deactivated_at = NULL, updated_at = NOW()
		WHERE user_id = $1 AND account_status = $3
	`, userID, AccountStatusActive, AccountStatusDeactivated)
	if err != nil {
		return false, fmt.Errorf("reactivate: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return false, nil
	}

	if err := outboxInsertTx(ctx, tx, EventUserReactivated, userID, map[string]any{
		"user_id":        userID.String(),
		"reactivated_at": now.Format(time.RFC3339),
	}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// ScheduleDeletion flips an account to 'pending_deletion' with a purge date
// 30 days out, and emits user.deletion_scheduled — deliberately NOT
// user.deletion_requested (see the SoftDeleteUser removal note in users.go).
//
// Guarded on status IN ('active','deactivated'): a deactivated user may still
// choose permanent deletion, but a suspended account cannot use deletion to
// exit suspension, and a purged/pending row cannot re-enter the window.
func (s *Store) ScheduleDeletion(ctx context.Context, userID uuid.UUID) (time.Time, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("schedule deletion: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The purge date is computed here rather than as `$3 + interval` in SQL:
	// reusing one parameter as both a timestamptz assignment and an operand
	// makes Postgres refuse to deduce its type (SQLSTATE 42P08).
	now := time.Now().UTC()
	scheduled := now.Add(DeletionGracePeriod)
	var purgeDate time.Time
	err = tx.QueryRow(ctx, `
		UPDATE auth.users
		SET account_status = $2,
		    deletion_requested_at = $3,
		    scheduled_purge_date = $4,
		    deactivated_at = NULL,
		    purge_requested_at = NULL,
		    updated_at = NOW()
		WHERE user_id = $1 AND account_status IN ($5, $6)
		RETURNING scheduled_purge_date
	`, userID, AccountStatusPendingDeletion, now, scheduled,
		AccountStatusActive, AccountStatusDeactivated,
	).Scan(&purgeDate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return time.Time{}, ErrLifecycleConflict
		}
		return time.Time{}, fmt.Errorf("schedule deletion: %w", err)
	}

	if err := outboxInsertTx(ctx, tx, EventUserDeletionScheduled, userID, map[string]any{
		"user_id":              userID.String(),
		"requested_at":         now.Format(time.RFC3339),
		"scheduled_purge_date": purgeDate.UTC().Format(time.RFC3339),
	}); err != nil {
		return time.Time{}, err
	}
	return purgeDate, tx.Commit(ctx)
}

// CancelDeletion is the login-side rescue during the 30-day window. Guarded
// on status='pending_deletion' AND scheduled_purge_date > NOW(): once the
// window has elapsed the worker may already have emitted user.purge_requested
// and downstream erasure may be underway — a login can no longer save the
// account, so the guard refuses and the caller answers ACCOUNT_PENDING_PURGE.
// Returns (false, nil) when nothing matched.
func (s *Store) CancelDeletion(ctx context.Context, userID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("cancel deletion: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	ct, err := tx.Exec(ctx, `
		UPDATE auth.users
		SET account_status = $2,
		    deletion_requested_at = NULL,
		    scheduled_purge_date = NULL,
		    purge_requested_at = NULL,
		    updated_at = NOW()
		WHERE user_id = $1
		  AND account_status = $3
		  AND scheduled_purge_date IS NOT NULL
		  AND scheduled_purge_date > NOW()
	`, userID, AccountStatusActive, AccountStatusPendingDeletion)
	if err != nil {
		return false, fmt.Errorf("cancel deletion: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return false, nil
	}

	if err := outboxInsertTx(ctx, tx, EventUserDeletionCancelled, userID, map[string]any{
		"user_id":      userID.String(),
		"cancelled_at": now.Format(time.RFC3339),
	}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// ── Purge worker surface ────────────────────────────────────────────────────

// PurgeCandidate is one pending-deletion account whose window has elapsed.
type PurgeCandidate struct {
	UserID             uuid.UUID
	ScheduledPurgeDate time.Time
	PurgeRequestedAt   *time.Time
}

// ListPurgeDue returns accounts past their purge date and not yet purged.
// Uses the idx_users_pending_deletion partial index.
func (s *Store) ListPurgeDue(ctx context.Context, limit int) ([]PurgeCandidate, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, scheduled_purge_date, purge_requested_at
		FROM auth.users
		WHERE account_status = $1
		  AND scheduled_purge_date IS NOT NULL
		  AND scheduled_purge_date <= NOW()
		  AND purge_completed_at IS NULL
		ORDER BY scheduled_purge_date
		LIMIT $2
	`, AccountStatusPendingDeletion, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PurgeCandidate
	for rows.Next() {
		var c PurgeCandidate
		if err := rows.Scan(&c.UserID, &c.ScheduledPurgeDate, &c.PurgeRequestedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RequestPurge emits user.purge_requested for one due account, at most once
// per purgeRequestInterval. The throttle and the outbox insert commit
// together, so a crash between them cannot mark a request that was never
// enqueued. Returns whether an event was emitted this call.
func (s *Store) RequestPurge(ctx context.Context, userID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("request purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	ct, err := tx.Exec(ctx, `
		UPDATE auth.users
		SET purge_requested_at = $2, updated_at = NOW()
		WHERE user_id = $1
		  AND account_status = $3
		  AND scheduled_purge_date IS NOT NULL
		  AND scheduled_purge_date <= NOW()
		  AND purge_completed_at IS NULL
		  AND (purge_requested_at IS NULL OR purge_requested_at <= $4)
	`, userID, now, AccountStatusPendingDeletion, now.Add(-purgeRequestInterval))
	if err != nil {
		return false, fmt.Errorf("request purge: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return false, nil
	}

	if err := outboxInsertTx(ctx, tx, EventUserPurgeRequested, userID, map[string]any{
		"user_id":      userID.String(),
		"requested_at": now.Format(time.RFC3339),
	}); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// InsertPurgeAck records that one service has erased its slice. Idempotent
// (ON CONFLICT DO NOTHING) because Kafka delivery is at-least-once.
func (s *Store) InsertPurgeAck(ctx context.Context, userID uuid.UUID, service string, ackedAt time.Time) error {
	if ackedAt.IsZero() {
		ackedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.account_purge_acks (user_id, service, acked_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, service) DO NOTHING
	`, userID, service, ackedAt)
	return err
}

// GetPurgeAcks returns the set of services that have acked for a user.
func (s *Store) GetPurgeAcks(ctx context.Context, userID uuid.UUID) (map[string]struct{}, error) {
	rows, err := s.db.Query(ctx,
		`SELECT service FROM auth.account_purge_acks WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	acks := map[string]struct{}{}
	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err != nil {
			return nil, err
		}
		acks[svc] = struct{}{}
	}
	return acks, rows.Err()
}

// CompletePurge anonymises the credential row and flips it to the terminal
// 'purged' state, emitting user.purged — all in ONE transaction. The caller
// (purge worker) MUST have verified the full required ack set first; this
// method re-asserts only the row-state guard, not the ack policy.
//
//	email         → purged+<user_id>@purged.invalid (keeps the UNIQUE + the
//	                identity CHECK satisfied while freeing the real address)
//	phone         → NULL
//	password_hash → '' (no credential; login is impossible)
//	2FA           → disabled, both secret columns cleared
func (s *Store) CompletePurge(ctx context.Context, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("complete purge: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	now := time.Now().UTC()
	ct, err := tx.Exec(ctx, `
		UPDATE auth.users
		SET email = 'purged+' || user_id::text || '@purged.invalid',
		    phone = NULL,
		    password_hash = '',
		    two_factor_enabled = FALSE,
		    two_factor_secret = NULL,
		    two_factor_secret_encrypted = NULL,
		    login_provider = NULL,
		    recovery_email = NULL,
		    recovery_phone = NULL,
		    account_status = $2,
		    purge_completed_at = $3,
		    updated_at = NOW()
		WHERE user_id = $1
		  AND account_status = $4
		  AND purge_completed_at IS NULL
	`, userID, AccountStatusPurged, now, AccountStatusPendingDeletion)
	if err != nil {
		return fmt.Errorf("complete purge: %w", err)
	}
	if ct.RowsAffected() == 0 {
		// Already purged (idempotent re-run) or rescued by a concurrent
		// cancel — either way there is nothing to finish.
		return ErrLifecycleConflict
	}

	if err := outboxInsertTx(ctx, tx, EventUserPurged, userID, map[string]any{
		"user_id":   userID.String(),
		"purged_at": now.Format(time.RFC3339),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
