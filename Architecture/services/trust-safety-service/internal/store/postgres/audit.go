package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrActorRequired is returned when a change has no verified human actor
// and no named service caller. Nothing is written.
var ErrActorRequired = errors.New("a verified actor is required")

// ErrInvalidTransition is returned from an audited update when the row's
// current status does not allow the requested change.
var ErrInvalidTransition = errors.New("invalid status transition")

// Audit target types (trust.admin_audit.target_type).
const (
	AuditTargetReport    = "report"
	AuditTargetAppeal    = "appeal"
	AuditTargetGrievance = "grievance"
)

// Actor is who made a change: a verified human or a named service caller.
// Exactly one of UserID and Service is set.
type Actor struct {
	UserID  uuid.UUID
	Service string
}

// UserActor is a verified human admin.
func UserActor(id uuid.UUID) Actor { return Actor{UserID: id} }

// ServiceActor is a named service acting without a human.
func ServiceActor(name string) Actor { return Actor{Service: strings.TrimSpace(name)} }

// Validate refuses an empty actor or one that is both a human and a service.
func (a Actor) Validate() error {
	human := a.UserID != uuid.Nil
	svc := strings.TrimSpace(a.Service) != ""
	if human == svc {
		return ErrActorRequired
	}
	return nil
}

// AuditMeta is the per-request context recorded with a change.
type AuditMeta struct {
	Actor     Actor
	Reason    string
	RequestID string
}

// AuditEntry is one trust.admin_audit row.
type AuditEntry struct {
	ID             uuid.UUID  `json:"id"`
	Seq            int64      `json:"seq"`
	ActorType      string     `json:"actor_type"`
	ActorUserID    *uuid.UUID `json:"actor_user_id,omitempty"`
	ActorService   *string    `json:"actor_service,omitempty"`
	Action         string     `json:"action"`
	TargetType     string     `json:"target_type"`
	TargetID       uuid.UUID  `json:"target_id"`
	PrevStatus     *string    `json:"prev_status,omitempty"`
	NewStatus      *string    `json:"new_status,omitempty"`
	PrevAssignee   *uuid.UUID `json:"prev_assignee,omitempty"`
	NewAssignee    *uuid.UUID `json:"new_assignee,omitempty"`
	PrevResolution *string    `json:"prev_resolution,omitempty"`
	NewResolution  *string    `json:"new_resolution,omitempty"`
	Reason         *string    `json:"reason,omitempty"`
	RequestID      *string    `json:"request_id,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// auditChange is the before/after of one change.
type auditChange struct {
	Action                        string
	TargetType                    string
	TargetID                      uuid.UUID
	PrevStatus, NewStatus         string
	PrevAssignee, NewAssignee     *uuid.UUID
	PrevResolution, NewResolution string
}

func nullIfEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

// insertAudit writes one audit row inside tx. The caller must roll the
// change back if this fails.
func insertAudit(ctx context.Context, tx pgx.Tx, meta AuditMeta, ch auditChange) error {
	if err := meta.Actor.Validate(); err != nil {
		return err
	}
	actorType := "user"
	var actorUser *uuid.UUID
	var actorService *string
	if meta.Actor.UserID != uuid.Nil {
		id := meta.Actor.UserID
		actorUser = &id
	} else {
		actorType = "service"
		name := strings.TrimSpace(meta.Actor.Service)
		actorService = &name
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO trust.admin_audit (actor_type, actor_user_id, actor_service, action,
			target_type, target_id, prev_status, new_status, prev_assignee, new_assignee,
			prev_resolution, new_resolution, reason, request_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`, actorType, actorUser, actorService, ch.Action, ch.TargetType, ch.TargetID,
		nullIfEmpty(ch.PrevStatus), nullIfEmpty(ch.NewStatus), ch.PrevAssignee, ch.NewAssignee,
		nullIfEmpty(ch.PrevResolution), nullIfEmpty(ch.NewResolution),
		nullIfEmpty(meta.Reason), nullIfEmpty(meta.RequestID))
	if err != nil {
		return fmt.Errorf("write admin audit: %w", err)
	}
	return nil
}

// ListAudit returns every audit row for one target, oldest first.
func (s *ReportStore) ListAudit(ctx context.Context, targetType string, targetID uuid.UUID) ([]AuditEntry, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, seq, actor_type, actor_user_id, actor_service, action, target_type,
		       target_id, prev_status, new_status, prev_assignee, new_assignee,
		       prev_resolution, new_resolution, reason, request_id, created_at
		FROM trust.admin_audit
		WHERE target_type = $1 AND target_id = $2
		ORDER BY seq ASC
	`, targetType, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.Seq, &e.ActorType, &e.ActorUserID, &e.ActorService,
			&e.Action, &e.TargetType, &e.TargetID, &e.PrevStatus, &e.NewStatus,
			&e.PrevAssignee, &e.NewAssignee, &e.PrevResolution, &e.NewResolution,
			&e.Reason, &e.RequestID, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// withTx runs fn in one transaction, committing only if fn succeeds.
func withTx(ctx context.Context, begin func(context.Context) (pgx.Tx, error), fn func(pgx.Tx) error) error {
	tx, err := begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
