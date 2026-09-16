package store

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Admin sessions (admin console Wave 0, A2).

// AddSessionAMR records that an authentication method was completed on a live
// session (POST /v1/auth/step-up adds "otp"). Idempotent: a method already
// present is not duplicated. A revoked session is not touched, and reports
// ErrSessionNotLive so a step-up cannot revive it.
func (s *Store) AddSessionAMR(ctx context.Context, sessionID uuid.UUID, method string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth.sessions
		SET amr = CASE WHEN $2 = ANY(COALESCE(amr, '{}')) THEN amr
		               ELSE array_append(COALESCE(amr, '{}'), $2) END
		WHERE session_id = $1 AND is_active = TRUE AND revoked_at IS NULL`,
		sessionID, method)
	if err != nil {
		return fmt.Errorf("add session amr: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSessionNotLive
	}
	return nil
}

// ErrSessionNotLive: the session is revoked, inactive or absent.
var ErrSessionNotLive = fmt.Errorf("session is not live")

// revokeAllSessionsTx revokes every live session of a user inside tx and
// returns their ids, so the caller can mark each one revoked in Redis
// (sess_revoked:<sid>) after the commit.
func revokeAllSessionsTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, `
		UPDATE auth.sessions
		SET revoked_at = NOW(), is_active = FALSE
		WHERE user_id = $1 AND is_active = TRUE AND revoked_at IS NULL
		RETURNING session_id`, userID)
	if err != nil {
		return nil, fmt.Errorf("revoke sessions: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("revoke sessions: scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RevokeAllSessionsAudited is the superadmin force logout: every live session
// of the target is revoked and the audit row is written in the SAME
// transaction, so a logout without its record (or a record without the
// logout) cannot exist. The audit detail gains " sessions_revoked=N".
func (s *Store) RevokeAllSessionsAudited(ctx context.Context, userID uuid.UUID, audit RoleAudit) ([]uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("force logout: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	ids, err := revokeAllSessionsTx(ctx, tx, userID)
	if err != nil {
		return nil, fmt.Errorf("force logout: %w", err)
	}
	if err := insertAuditTx(ctx, tx, audit, fmt.Sprintf("%s sessions_revoked=%d", audit.Detail, len(ids))); err != nil {
		return nil, fmt.Errorf("force logout: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("force logout: commit: %w", err)
	}
	return ids, nil
}

// insertAuditTx writes one allowed admin_audit row inside tx.
func insertAuditTx(ctx context.Context, tx pgx.Tx, audit RoleAudit, detail string) error {
	var actor, target *uuid.UUID
	if audit.ActorID != uuid.Nil {
		actor = &audit.ActorID
	}
	if audit.TargetID != uuid.Nil {
		target = &audit.TargetID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth.admin_audit (actor_id, action, target_id, detail, allowed)
		VALUES ($1, $2, $3, $4, TRUE)`, actor, audit.Action, target, detail); err != nil {
		return fmt.Errorf("audit: %w", err)
	}
	return nil
}

// HolderCandidate is one ACTIVE account that may hold an admin permission:
// its active role rows (platform-wide and app-scoped) and whether TOTP is
// enrolled. The permission itself is resolved by the caller through the
// catalogue, never in SQL.
type HolderCandidate struct {
	UserID           uuid.UUID
	TwoFactorEnabled bool
	Grants           []RoleGrant
}

// AdminHolderCandidates returns every active account that holds an active
// admin role row, plus the given env allowlist ids (whose roles come from
// configuration, not rows). Accounts that are not account_status='active'
// are excluded: a deactivated or suspended admin is not a second approver.
func (s *Store) AdminHolderCandidates(ctx context.Context, envUserIDs []uuid.UUID) ([]HolderCandidate, error) {
	if envUserIDs == nil {
		envUserIDs = []uuid.UUID{}
	}
	rows, err := s.db.Query(ctx, `
		WITH ids AS (
			SELECT user_id FROM auth.user_roles
			WHERE role = ANY($2) AND `+activeRole+`
			UNION
			SELECT unnest($1::uuid[])
		)
		SELECT u.user_id, COALESCE(u.two_factor_enabled, FALSE),
		       r.role, COALESCE(r.app, ''), r.expires_at
		FROM ids
		JOIN auth.users u ON u.user_id = ids.user_id AND u.account_status = $3
		LEFT JOIN auth.user_roles r
		       ON r.user_id = u.user_id AND (r.expires_at IS NULL OR r.expires_at > NOW())`,
		envUserIDs, roles.AdminRoles(), AccountStatusActive)
	if err != nil {
		return nil, fmt.Errorf("admin holder candidates: %w", err)
	}
	defer rows.Close()

	byID := map[uuid.UUID]*HolderCandidate{}
	var order []uuid.UUID
	for rows.Next() {
		var (
			id        uuid.UUID
			totp      bool
			role, app *string
			expiresAt *time.Time
		)
		if err := rows.Scan(&id, &totp, &role, &app, &expiresAt); err != nil {
			return nil, fmt.Errorf("admin holder candidates: scan: %w", err)
		}
		c, ok := byID[id]
		if !ok {
			c = &HolderCandidate{UserID: id, TwoFactorEnabled: totp}
			byID[id] = c
			order = append(order, id)
		}
		if role != nil {
			g := RoleGrant{Role: *role, ExpiresAt: expiresAt}
			if app != nil {
				g.App = *app
			}
			c.Grants = append(c.Grants, g)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin holder candidates: %w", err)
	}
	out := make([]HolderCandidate, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}
