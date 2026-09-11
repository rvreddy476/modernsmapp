package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// The payout rail's store (plan Phase 4A, 4C, 4D)
// ---------------------------------------------------------------------------

// ErrIllegalTransition: TransitionPayoutRequest matched no row in the
// expected state. Either the row moved under the caller or the pair is
// not one the state machine allows; the caller re-reads and decides.
var ErrIllegalTransition = errors.New("ILLEGAL_PAYOUT_TRANSITION")

// PayoutRequestPatch is what a transition may set alongside the status.
// nil fields are left untouched.
type PayoutRequestPatch struct {
	ProviderReference *string
	ProviderStatus    *string
	SubmittedAt       *time.Time
	LastReconciledAt  *time.Time
	ProcessedAt       *time.Time
	UTR               *string
	FailureReason     *string
}

// TransitionPayoutRequest is the one write of payout_requests.status:
// UPDATE ... WHERE id=$1 AND status=$2. Zero rows is ErrIllegalTransition.
// The allowed table lives in service/payout_state.go and is checked
// before this is called; the WHERE is what makes a concurrent mover lose.
func (s *Store) TransitionPayoutRequest(ctx context.Context, id uuid.UUID, from, to string, patch PayoutRequestPatch) error {
	return s.TransitionPayoutRequestTx(ctx, s.db, id, from, to, patch)
}

// TransitionPayoutRequestTx is TransitionPayoutRequest on the caller's
// transaction, so the ledger effects of a transition commit with it.
func (s *Store) TransitionPayoutRequestTx(ctx context.Context, db DBTX, id uuid.UUID, from, to string, patch PayoutRequestPatch) error {
	if from == "" || to == "" {
		return ErrIllegalTransition
	}
	tag, err := db.Exec(ctx, `
		UPDATE payout_requests
		SET status             = $3,
		    provider_reference = COALESCE($4, provider_reference),
		    provider_status    = COALESCE($5, provider_status),
		    submitted_at       = COALESCE($6, submitted_at),
		    last_reconciled_at = COALESCE($7, last_reconciled_at),
		    processed_at       = COALESCE($8, processed_at),
		    utr                = COALESCE($9, utr),
		    failure_reason     = COALESCE($10, failure_reason)
		WHERE id = $1 AND status = $2
	`, id, from, to,
		patch.ProviderReference, patch.ProviderStatus, patch.SubmittedAt, patch.LastReconciledAt,
		patch.ProcessedAt, patch.UTR, patch.FailureReason)
	if err != nil {
		return fmt.Errorf("transition payout request %s %s->%s: %w", id, from, to, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrIllegalTransition
	}
	return nil
}

// TouchPayoutRequestTx records a provider status and reconciliation time
// on a row that is staying in its state. It is the same-state twin of a
// transition: WHERE status=$2 still guards it, and a zero-row result is
// reported so the caller knows nothing was updated.
func (s *Store) TouchPayoutRequestTx(ctx context.Context, db DBTX, id uuid.UUID, status string, providerStatus string, utr *string, at time.Time) (bool, error) {
	tag, err := db.Exec(ctx, `
		UPDATE payout_requests
		SET provider_status = $3, utr = COALESCE($4, utr), last_reconciled_at = $5
		WHERE id = $1 AND status = $2
	`, id, status, providerStatus, utr, at)
	if err != nil {
		return false, fmt.Errorf("touch payout request %s: %w", id, err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkPayoutSubmitAttempt counts an attempt to reach the provider for a
// reserved row, BEFORE the call is made: a crash between the call and the
// commit still leaves a count behind, and the next attempt looks before
// it submits.
func (s *Store) MarkPayoutSubmitAttempt(ctx context.Context, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE payout_requests SET retry_count = retry_count + 1 WHERE id = $1 AND status = 'reserved'
	`, id)
	if err != nil {
		return fmt.Errorf("mark payout submit attempt %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrIllegalTransition
	}
	return nil
}

// ListPayoutRequestsByStatus returns up to limit rows in status, oldest
// first, optionally only those requested before a cut-off.
func (s *Store) ListPayoutRequestsByStatus(ctx context.Context, status string, requestedBefore *time.Time, limit int) ([]PayoutRequestRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+payoutRequestColumns+`
		FROM payout_requests
		WHERE status = $1 AND ($2::timestamptz IS NULL OR requested_at < $2)
		ORDER BY requested_at ASC
		LIMIT $3
	`, status, requestedBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPayoutRequestRows(rows)
}

// ListPayoutRequestsToReconcile returns submitted and processing rows not
// reconciled (or, never reconciled, not submitted) since `before`.
func (s *Store) ListPayoutRequestsToReconcile(ctx context.Context, before time.Time, limit int) ([]PayoutRequestRow, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+payoutRequestColumns+`
		FROM payout_requests
		WHERE status IN ('submitted', 'processing')
		  AND provider_reference IS NOT NULL
		  AND COALESCE(last_reconciled_at, submitted_at, requested_at) < $1
		ORDER BY COALESCE(last_reconciled_at, submitted_at, requested_at) ASC
		LIMIT $2
	`, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectPayoutRequestRows(rows)
}

func collectPayoutRequestRows(rows pgx.Rows) ([]PayoutRequestRow, error) {
	var out []PayoutRequestRow
	for rows.Next() {
		r, err := scanPayoutRequestRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetPayoutRequestForUpdateTx locks and returns one request by id, or
// nil. Convergence reads the row under this lock so two deliveries of
// different events cannot both see the same "before" state.
func (s *Store) GetPayoutRequestForUpdateTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*PayoutRequestRow, error) {
	r, err := scanPayoutRequestRow(tx.QueryRow(ctx,
		`SELECT `+payoutRequestColumns+` FROM payout_requests WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// GetPayoutRequestByProviderReferenceTx returns the request that adopted
// a provider payout id, locked, or nil.
func (s *Store) GetPayoutRequestByProviderReferenceTx(ctx context.Context, tx pgx.Tx, ref string) (*PayoutRequestRow, error) {
	r, err := scanPayoutRequestRow(tx.QueryRow(ctx,
		`SELECT `+payoutRequestColumns+` FROM payout_requests WHERE provider_reference = $1 FOR UPDATE`, ref))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// Ledger effects of convergence
// ---------------------------------------------------------------------------

// ReleasePendingPayoutTx is the paid effect on the creator's ledger:
// pending_payout -= gross. The guard is in SQL; zero rows means the
// ledger does not hold what the request says it reserved.
func (s *Store) ReleasePendingPayoutTx(ctx context.Context, db DBTX, userID uuid.UUID, grossPaise int64) error {
	tag, err := db.Exec(ctx, `
		UPDATE creator_ledger
		SET pending_payout = pending_payout - $2, updated_at = NOW()
		WHERE user_id = $1 AND pending_payout >= $2
	`, userID, grossPaise)
	if err != nil {
		return fmt.Errorf("release pending payout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInsufficientFunds
	}
	return nil
}

// ReturnPendingPayoutTx is the failed/reversed-in-flight effect:
// pending_payout -= gross, balance += gross.
func (s *Store) ReturnPendingPayoutTx(ctx context.Context, db DBTX, userID uuid.UUID, grossPaise int64) error {
	tag, err := db.Exec(ctx, `
		UPDATE creator_ledger
		SET pending_payout = pending_payout - $2, balance = balance + $2, updated_at = NOW()
		WHERE user_id = $1 AND pending_payout >= $2
	`, userID, grossPaise)
	if err != nil {
		return fmt.Errorf("return pending payout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInsufficientFunds
	}
	return nil
}

// CreditBalanceTx is the reversed-after-paid effect: pending_payout was
// already released, so only balance += gross.
func (s *Store) CreditBalanceTx(ctx context.Context, db DBTX, userID uuid.UUID, grossPaise int64) error {
	tag, err := db.Exec(ctx, `
		UPDATE creator_ledger SET balance = balance + $2, updated_at = NOW() WHERE user_id = $1
	`, userID, grossPaise)
	if err != nil {
		return fmt.Errorf("credit balance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("credit balance: no ledger row for %s", userID)
	}
	return nil
}

// SetTransactionStatusTx sets a transaction's status and appends a note
// to its description.
func (s *Store) SetTransactionStatusTx(ctx context.Context, db DBTX, id uuid.UUID, status, note string) error {
	_, err := db.Exec(ctx, `
		UPDATE transactions
		SET status = $2,
		    description = CASE WHEN $3 = '' THEN description ELSE COALESCE(description, '') || ' [' || $3 || ']' END
		WHERE id = $1
	`, id, status, note)
	if err != nil {
		return fmt.Errorf("set transaction %s status %s: %w", id, status, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// payout_provider_events
// ---------------------------------------------------------------------------

// InsertProviderEvent records a delivery. inserted is false when
// (provider, event_id) was already there: a replay, and the caller stops.
func (s *Store) InsertProviderEvent(ctx context.Context, provider, eventID, eventType, providerRef string, payload []byte) (bool, error) {
	var ref *string
	if providerRef != "" {
		ref = &providerRef
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO payout_provider_events (provider, event_id, event_type, provider_reference, payload)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (provider, event_id) DO NOTHING
	`, provider, eventID, eventType, ref, payload)
	if err != nil {
		return false, fmt.Errorf("insert provider event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// MarkProviderEventConsumedTx stamps the event with the request it
// updated. Called ONLY after a request row was actually updated, and on
// the same transaction, so the stamp and the update commit together.
func (s *Store) MarkProviderEventConsumedTx(ctx context.Context, db DBTX, provider, eventID string, requestID uuid.UUID) error {
	_, err := db.Exec(ctx, `
		UPDATE payout_provider_events
		SET consumed_at = NOW(), payout_request_id = $3
		WHERE provider = $1 AND event_id = $2
	`, provider, eventID, requestID)
	if err != nil {
		return fmt.Errorf("mark provider event consumed: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// creator_payout_accounts
// ---------------------------------------------------------------------------

// GetPayoutContactID returns the creator's cached RazorpayX contact id,
// or "" when none is cached.
func (s *Store) GetPayoutContactID(ctx context.Context, userID uuid.UUID) (string, error) {
	var id string
	err := s.db.QueryRow(ctx, `SELECT rzp_contact_id FROM creator_payout_accounts WHERE user_id = $1`, userID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return id, nil
}

// UpsertPayoutContactID caches the creator's contact id.
func (s *Store) UpsertPayoutContactID(ctx context.Context, userID uuid.UUID, contactID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO creator_payout_accounts (user_id, rzp_contact_id)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET rzp_contact_id = EXCLUDED.rzp_contact_id, updated_at = NOW()
	`, userID, contactID)
	return err
}

// ---------------------------------------------------------------------------
// payout_methods: the rail's columns
// ---------------------------------------------------------------------------

// SetPayoutMethodFundAccount records the provider's fund account id.
func (s *Store) SetPayoutMethodFundAccount(ctx context.Context, methodID uuid.UUID, fundAccountID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_methods SET rzp_fund_account_id = $2, updated_at = NOW() WHERE id = $1
	`, methodID, fundAccountID)
	return err
}

// SetPayoutMethodVerified marks the method verified as of `at`
// (RazorpayX's fund-account validation reported the account active).
func (s *Store) SetPayoutMethodVerified(ctx context.Context, methodID uuid.UUID, at time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE payout_methods SET is_verified = true, verified_at = $2, updated_at = NOW() WHERE id = $1
	`, methodID, at)
	return err
}

// DeletePayoutMethodByID removes a method regardless of owner; used only
// to undo a capture the provider refused outright.
func (s *Store) DeletePayoutMethodByID(ctx context.Context, methodID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM payout_methods WHERE id = $1`, methodID)
	return err
}

// GetPayoutMethodByID returns a method by id whoever owns it, or nil.
// The submitter uses this: the request row already names the owner.
func (s *Store) GetPayoutMethodByID(ctx context.Context, methodID uuid.UUID) (*PayoutMethod, error) {
	m, err := scanPayoutMethod(s.db.QueryRow(ctx,
		`SELECT `+payoutMethodColumns+` FROM payout_methods WHERE id = $1`, methodID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}
