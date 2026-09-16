package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Grievance is one IT Rules 2021 grievance-redressal ticket.
type Grievance struct {
	ID              uuid.UUID  `json:"id"`
	ComplainantID   uuid.UUID  `json:"complainant_id"`
	Subject         string     `json:"subject"`
	AboutEntityType *string    `json:"about_entity_type,omitempty"`
	AboutEntityID   *uuid.UUID `json:"about_entity_id,omitempty"`
	Description     string     `json:"description"`
	Status          string     `json:"status"`
	AssignedTo      *uuid.UUID `json:"assigned_to,omitempty"`
	ResolutionNotes string     `json:"resolution_notes,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	AcknowledgedAt  *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
	DueAt           time.Time  `json:"due_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

const grievanceCols = `id, complainant_id, subject, about_entity_type, about_entity_id,
	description, status, assigned_to, resolution_notes, created_at,
	acknowledged_at, resolved_at, due_at, updated_at`

func scanGrievance(row pgx.Row) (*Grievance, error) {
	var g Grievance
	err := row.Scan(
		&g.ID, &g.ComplainantID, &g.Subject, &g.AboutEntityType, &g.AboutEntityID,
		&g.Description, &g.Status, &g.AssignedTo, &g.ResolutionNotes, &g.CreatedAt,
		&g.AcknowledgedAt, &g.ResolvedAt, &g.DueAt, &g.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// CreateGrievance inserts a new grievance ticket.
func (s *ReportStore) CreateGrievance(ctx context.Context, g *Grievance) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.grievances (id, complainant_id, subject, about_entity_type,
			about_entity_id, description, status, due_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, g.ID, g.ComplainantID, g.Subject, g.AboutEntityType, g.AboutEntityID,
		g.Description, g.Status, g.DueAt)
	return err
}

// GetGrievance fetches a grievance by id.
func (s *ReportStore) GetGrievance(ctx context.Context, id uuid.UUID) (*Grievance, error) {
	return scanGrievance(s.db.QueryRow(ctx,
		`SELECT `+grievanceCols+` FROM trust.grievances WHERE id = $1`, id))
}

// ListGrievances returns grievances for the officer queue, newest first.
// An empty status returns all; otherwise it filters by that status.
func (s *ReportStore) ListGrievances(ctx context.Context, status string, limit, offset int) ([]Grievance, error) {
	return s.queryGrievances(ctx, `
		SELECT `+grievanceCols+` FROM trust.grievances
		WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, status, limit, offset)
}

// ListGrievancesByUser returns the grievances a given user has filed.
func (s *ReportStore) ListGrievancesByUser(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Grievance, error) {
	return s.queryGrievances(ctx, `
		SELECT `+grievanceCols+` FROM trust.grievances
		WHERE complainant_id = $1
		ORDER BY created_at DESC LIMIT $2 OFFSET $3
	`, userID, limit, offset)
}

func (s *ReportStore) queryGrievances(ctx context.Context, query string, args ...interface{}) ([]Grievance, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grievance
	for rows.Next() {
		g, err := scanGrievance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, rows.Err()
}

// ErrNoChange is returned when a grievance update would change nothing.
var ErrNoChange = errors.New("the update changes nothing")

// GrievanceUpdate is one officer change to a grievance. Empty Status keeps
// the status, nil Notes keeps the notes, nil AssignedTo keeps the officer.
type GrievanceUpdate struct {
	Status     string
	Notes      *string
	AssignedTo *uuid.UUID
	// Allow checks a status transition; nil allows any.
	Allow func(from, to string) bool
}

func grievanceTerminal(status string) bool {
	return status == "resolved" || status == "rejected"
}

// UpdateGrievance locks the grievance, applies the change and writes its
// audit row (including the from/to officer) in one transaction.
// acknowledged_at and resolved_at are stamped on the first transition into
// those states. A resolved or rejected grievance is not changed.
func (s *ReportStore) UpdateGrievance(ctx context.Context, id uuid.UUID, up GrievanceUpdate, meta AuditMeta) (*Grievance, error) {
	if err := meta.Actor.Validate(); err != nil {
		return nil, err
	}
	var out *Grievance
	err := withTx(ctx, s.db.Begin, func(tx pgx.Tx) error {
		var prevStatus, prevNotes string
		var prevAssignee *uuid.UUID
		if err := tx.QueryRow(ctx, `
			SELECT status, assigned_to, resolution_notes
			FROM trust.grievances WHERE id = $1 FOR UPDATE
		`, id).Scan(&prevStatus, &prevAssignee, &prevNotes); err != nil {
			return err
		}
		if grievanceTerminal(prevStatus) {
			return fmt.Errorf("%w: grievance is %s", ErrInvalidTransition, prevStatus)
		}
		newStatus := prevStatus
		if up.Status != "" {
			if up.Allow != nil && !up.Allow(prevStatus, up.Status) {
				return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, prevStatus, up.Status)
			}
			newStatus = up.Status
		}
		newAssignee := prevAssignee
		if up.AssignedTo != nil {
			a := *up.AssignedTo
			newAssignee = &a
		}
		newNotes := prevNotes
		if up.Notes != nil {
			newNotes = *up.Notes
		}
		assigneeChanged := !sameUUID(prevAssignee, newAssignee)
		if up.Status == "" && !assigneeChanged && newNotes == prevNotes {
			return ErrNoChange
		}
		g, err := scanGrievance(tx.QueryRow(ctx, `
			UPDATE trust.grievances
			SET status = $2,
			    resolution_notes = $3,
			    assigned_to = $4,
			    acknowledged_at = CASE WHEN acknowledged_at IS NULL AND $2 <> 'open'
			                           THEN NOW() ELSE acknowledged_at END,
			    resolved_at = CASE WHEN $2 IN ('resolved', 'rejected')
			                       THEN NOW() ELSE resolved_at END,
			    updated_at = NOW()
			WHERE id = $1
			RETURNING `+grievanceCols, id, newStatus, newNotes, newAssignee))
		if err != nil {
			return err
		}
		action := "grievance.noted"
		switch {
		case up.Status != "":
			action = "grievance." + up.Status
		case assigneeChanged:
			action = "grievance.assigned"
		}
		if err := insertAudit(ctx, tx, meta, auditChange{
			Action:         action,
			TargetType:     AuditTargetGrievance,
			TargetID:       id,
			PrevStatus:     prevStatus,
			NewStatus:      g.Status,
			PrevAssignee:   prevAssignee,
			NewAssignee:    g.AssignedTo,
			PrevResolution: prevNotes,
			NewResolution:  g.ResolutionNotes,
		}); err != nil {
			return err
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func sameUUID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ListOverdueGrievances is the 15-day breach queue: grievances past due_at
// that are still open or acknowledged, most overdue first.
func (s *ReportStore) ListOverdueGrievances(ctx context.Context, limit, offset int) ([]Grievance, error) {
	return s.queryGrievances(ctx, `
		SELECT `+grievanceCols+` FROM trust.grievances
		WHERE status IN ('open', 'acknowledged') AND due_at < NOW()
		ORDER BY due_at ASC, id ASC LIMIT $1 OFFSET $2
	`, limit, offset)
}
