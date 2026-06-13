// Admin audit store — dating_admin_audit append-only log (§P0-8).
//
// Every admin action taken from /admin/dating writes one row here.
// The schema enforces immutability via a trigger that refuses
// UPDATE/DELETE — see database/setup.sql. This file just exposes
// insert + list helpers; there is intentionally no update or delete
// method.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AdminAuditEntry is one row of dating_admin_audit.
//
// Field semantics:
//   - ActorAdminID — the X-Admin-Id header value the gateway injected.
//     uuid.Nil is allowed (the service-layer wiring logs a warning when
//     the header is missing rather than failing the action) so the
//     audit trail never has a hole on the action itself.
//   - Action       — short verb such as "report_dismiss", "report_warn",
//     "report_restrict", "report_suspend", "photo_approved",
//     "photo_rejected". Free-form by design so future admin tools can
//     plug in without a schema change.
//   - TargetUserID — the user whose profile / photo was acted on. Nil
//     for actions that don't have a single target user.
//   - TargetResource — namespaced reference such as "report:<uuid>" or
//     "photo:<uuid>" so the audit row links back to the source object
//     after Postgres garbage-collects the underlying row.
//   - Reason / PolicyCode / InternalNotes — optional context the admin
//     supplied at action time. Nullable in the schema.
type AdminAuditEntry struct {
	ID             uuid.UUID `json:"id"`
	ActorAdminID   uuid.UUID `json:"actor_admin_id"`
	Action         string    `json:"action"`
	TargetUserID   uuid.UUID `json:"target_user_id,omitempty"`
	TargetResource string    `json:"target_resource,omitempty"`
	Reason         string    `json:"reason,omitempty"`
	PolicyCode     string    `json:"policy_code,omitempty"`
	InternalNotes  string    `json:"internal_notes,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// AdminAuditFilter narrows ListAdminAudit. Empty values are ignored —
// any combination of fields may be set.
type AdminAuditFilter struct {
	ActorAdminID uuid.UUID // zero = no filter
	TargetUserID uuid.UUID // zero = no filter
	Action       string    // empty = no filter
}

// InsertAdminAudit appends one row. Action is the only required field;
// everything else is optional (the schema accepts NULL on the optional
// columns). Returns the generated id + created_at on the input
// pointer for the caller to log.
//
// Callers MUST treat the insert as best-effort relative to the action
// itself: the action (report transition / photo flip) has already
// landed by the time we reach here. A failed audit insert is logged
// loudly but does NOT roll back the admin action — losing audit on a
// successful action is preferable to bouncing a moderator's click
// because a logging table was unhappy.
func (s *Store) InsertAdminAudit(ctx context.Context, e *AdminAuditEntry) error {
	if e == nil {
		return fmt.Errorf("invalid: entry required")
	}
	if strings.TrimSpace(e.Action) == "" {
		return fmt.Errorf("invalid: action required")
	}

	var targetUser any
	if e.TargetUserID != uuid.Nil {
		targetUser = e.TargetUserID
	}
	var targetResource, reason, policyCode, internalNotes any
	if e.TargetResource != "" {
		targetResource = e.TargetResource
	}
	if e.Reason != "" {
		reason = e.Reason
	}
	if e.PolicyCode != "" {
		policyCode = e.PolicyCode
	}
	if e.InternalNotes != "" {
		internalNotes = e.InternalNotes
	}

	err := s.db.QueryRow(ctx, `
        INSERT INTO dating_admin_audit
            (actor_admin_id, action, target_user_id, target_resource,
             reason, policy_code, internal_notes)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        RETURNING id, created_at`,
		e.ActorAdminID, e.Action, targetUser, targetResource,
		reason, policyCode, internalNotes,
	).Scan(&e.ID, &e.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert admin audit: %w", err)
	}
	return nil
}

// ListAdminAudit returns rows newest-first, paged. Filters are
// optional and AND-combined. limit is clamped to [1, 200] with a
// default of 50; offset is clamped to >= 0.
func (s *Store) ListAdminAudit(ctx context.Context, f AdminAuditFilter, limit, offset int) ([]*AdminAuditEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	args := []any{}
	where := []string{"1=1"}
	if f.ActorAdminID != uuid.Nil {
		args = append(args, f.ActorAdminID)
		where = append(where, fmt.Sprintf("actor_admin_id = $%d", len(args)))
	}
	if f.TargetUserID != uuid.Nil {
		args = append(args, f.TargetUserID)
		where = append(where, fmt.Sprintf("target_user_id = $%d", len(args)))
	}
	if strings.TrimSpace(f.Action) != "" {
		args = append(args, f.Action)
		where = append(where, fmt.Sprintf("action = $%d", len(args)))
	}
	args = append(args, limit, offset)
	q := `
        SELECT id, actor_admin_id, action, target_user_id, target_resource,
               reason, policy_code, internal_notes, created_at
        FROM dating_admin_audit
        WHERE ` + strings.Join(where, " AND ") + `
        ORDER BY created_at DESC
        LIMIT $` + fmt.Sprintf("%d", len(args)-1) +
		` OFFSET $` + fmt.Sprintf("%d", len(args))

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list admin audit: %w", err)
	}
	defer rows.Close()

	out := make([]*AdminAuditEntry, 0, limit)
	for rows.Next() {
		r := &AdminAuditEntry{}
		var targetUser uuid.NullUUID
		var targetResource, reason, policyCode, internalNotes sql.NullString
		if err := rows.Scan(
			&r.ID, &r.ActorAdminID, &r.Action, &targetUser, &targetResource,
			&reason, &policyCode, &internalNotes, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan admin audit: %w", err)
		}
		if targetUser.Valid {
			r.TargetUserID = targetUser.UUID
		}
		r.TargetResource = targetResource.String
		r.Reason = reason.String
		r.PolicyCode = policyCode.String
		r.InternalNotes = internalNotes.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ErrAdminAuditImmutable is returned when the database trigger
// rejects an UPDATE/DELETE attempt on dating_admin_audit. This is
// surfaced as a typed error so tests can assert the trigger fires.
// The store has no method that would normally trigger it — callers
// who try a raw query and get a Postgres P0001 should wrap with this
// sentinel.
var ErrAdminAuditImmutable = errors.New("dating_admin_audit is append-only")
