package postgres

import (
	"context"
	"errors"

	"github.com/atpost/admin-service/internal/approvals"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const approvalColumns = `id::text, app, operation, target_type, target_id, payload, payload_hash,
	requester::text, requester_reason, required_permission, status, approver::text, decision_reason,
	result_status, result_outcome, created_at, decided_at, executed_at, expires_at`

func scanApproval(row pgx.Row) (*approvals.Approval, error) {
	var a approvals.Approval
	var payload []byte
	err := row.Scan(&a.ID, &a.App, &a.Operation, &a.TargetType, &a.TargetID, &payload, &a.PayloadHash,
		&a.Requester, &a.RequesterReason, &a.RequiredPermission, &a.Status, &a.Approver, &a.DecisionReason,
		&a.ResultStatus, &a.ResultOutcome, &a.CreatedAt, &a.DecidedAt, &a.ExecutedAt, &a.ExpiresAt)
	if err != nil {
		return nil, err
	}
	a.Payload = payload
	return &a, nil
}

// CreateApproval inserts a pending approval, filling id, created_at and
// expires_at.
func (s *Store) CreateApproval(ctx context.Context, a *approvals.Approval) error {
	if _, err := uuid.Parse(a.Requester); err != nil {
		return errors.New("approval requester must be a user id")
	}
	a.ID = uuid.NewString()
	a.Status = approvals.StatusPending
	return s.db.QueryRow(ctx, `
		INSERT INTO admin.approvals
			(id, app, operation, target_type, target_id, payload, payload_hash, requester,
			 requester_reason, required_permission, status, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, 'pending', NOW(), NOW() + make_interval(secs => $11))
		RETURNING created_at, expires_at`,
		a.ID, a.App, a.Operation, a.TargetType, a.TargetID, []byte(a.Payload), a.PayloadHash, a.Requester,
		a.RequesterReason, a.RequiredPermission, approvals.TTL.Seconds(),
	).Scan(&a.CreatedAt, &a.ExpiresAt)
}

func (s *Store) GetApproval(ctx context.Context, id string) (*approvals.Approval, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, approvals.ErrNotFound
	}
	a, err := scanApproval(s.db.QueryRow(ctx, `SELECT `+approvalColumns+` FROM admin.approvals WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, approvals.ErrNotFound
	}
	return a, err
}

func (s *Store) ClaimApproval(ctx context.Context, id, approver, reason string) (*approvals.Approval, error) {
	a, err := scanApproval(s.db.QueryRow(ctx, `
		UPDATE admin.approvals
		   SET status = 'approved', approver = $2, decision_reason = NULLIF($3, ''), decided_at = NOW()
		 WHERE id = $1 AND status = 'pending' AND expires_at > NOW()
		RETURNING `+approvalColumns, id, approver, reason))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, approvals.ErrNotClaimable
	}
	return a, err
}

func (s *Store) FinishApproval(ctx context.Context, id string, resultStatus int, resultOutcome string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE admin.approvals
		   SET status = 'executed', executed_at = NOW(), result_status = $2, result_outcome = $3
		 WHERE id = $1 AND status = 'approved'`, id, resultStatus, resultOutcome)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return approvals.ErrNotClaimable
	}
	return nil
}

func (s *Store) CloseApproval(ctx context.Context, id, decider, reason string) (*approvals.Approval, error) {
	a, err := scanApproval(s.db.QueryRow(ctx, `
		UPDATE admin.approvals
		   SET status = 'rejected', approver = $2, decision_reason = NULLIF($3, ''),
		       decided_at = COALESCE(decided_at, NOW())
		 WHERE id = $1 AND (status = 'approved' OR (status = 'pending' AND expires_at > NOW()))
		RETURNING `+approvalColumns, id, decider, reason))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, approvals.ErrNotClaimable
	}
	return a, err
}

func (s *Store) ExpireApproval(ctx context.Context, id string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE admin.approvals SET status = 'expired'
		 WHERE id = $1 AND status = 'pending' AND expires_at <= NOW()`, id)
	return err
}

func (s *Store) ListPendingApprovals(ctx context.Context, perms []string, excludeRequester string, limit int) ([]approvals.Approval, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if _, err := uuid.Parse(excludeRequester); err != nil {
		return nil, errors.New("caller must be a user id")
	}
	// Expire what is due first, so the list never offers a dead request.
	if _, err := s.db.Exec(ctx, `
		UPDATE admin.approvals SET status = 'expired'
		 WHERE status = 'pending' AND expires_at <= NOW()`); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT `+approvalColumns+` FROM admin.approvals
		 WHERE status = 'pending' AND expires_at > NOW()
		   AND required_permission = ANY($1) AND requester <> $2
		 ORDER BY created_at DESC
		 LIMIT $3`, perms, excludeRequester, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []approvals.Approval{}
	for rows.Next() {
		a, err := scanApproval(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}
