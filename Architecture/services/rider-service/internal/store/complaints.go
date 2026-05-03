package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrComplaintNotFound is returned when GetComplaint finds no row.
var ErrComplaintNotFound = errors.New("complaint: not found")

// allowedComplaintCategories is the closed set of categories accepted by the
// rider_complaints.category column. Mirrors MOPEDU_SPEC §12.
var allowedComplaintCategories = map[string]bool{
	"driver_behavior":   true,
	"vehicle_condition": true,
	"route_deviation":   true,
	"fare_dispute":      true,
	"safety":            true,
	"other":             true,
}

// allowedComplaintStatuses is the closed set used by the CHECK constraint.
var allowedComplaintStatuses = map[string]bool{
	"open":         true,
	"under_review": true,
	"resolved":     true,
	"dismissed":    true,
}

// IsValidComplaintCategory exposes the category whitelist to the service
// layer so handlers can validate input before reaching the DB.
func IsValidComplaintCategory(category string) bool {
	return allowedComplaintCategories[category]
}

// IsValidComplaintStatus exposes the status whitelist to the service layer.
func IsValidComplaintStatus(status string) bool {
	return allowedComplaintStatuses[status]
}

// Complaint is one row in rider_complaints.
type Complaint struct {
	ID             uuid.UUID  `json:"id"`
	RideID         uuid.UUID  `json:"ride_id"`
	CustomerID     uuid.UUID  `json:"customer_id"`
	PartnerID      *uuid.UUID `json:"partner_id,omitempty"`
	Category       string     `json:"category"`
	Description    *string    `json:"description,omitempty"`
	Status         string     `json:"status"`
	ResolutionNote *string    `json:"resolution_note,omitempty"`
	ResolvedBy     *uuid.UUID `json:"resolved_by,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// CreateComplaintInput captures the customer-supplied fields.
type CreateComplaintInput struct {
	RideID      uuid.UUID
	CustomerID  uuid.UUID
	PartnerID   *uuid.UUID
	Category    string
	Description *string
}

// CreateComplaint inserts a complaint row in `open` status.
func (s *Store) CreateComplaint(ctx context.Context, in CreateComplaintInput) (*Complaint, error) {
	const q = `
        INSERT INTO rider_complaints (ride_id, customer_id, partner_id, category, description, status)
        VALUES ($1, $2, $3, $4, $5, 'open')
        RETURNING id, ride_id, customer_id, partner_id, category, description, status,
                  resolution_note, resolved_by, resolved_at, created_at`
	row := s.db.QueryRow(ctx, q, in.RideID, in.CustomerID, in.PartnerID, in.Category, in.Description)
	return scanComplaint(row)
}

// GetComplaint returns one complaint by id.
func (s *Store) GetComplaint(ctx context.Context, id uuid.UUID) (*Complaint, error) {
	const q = `
        SELECT id, ride_id, customer_id, partner_id, category, description, status,
               resolution_note, resolved_by, resolved_at, created_at
        FROM rider_complaints
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	c, err := scanComplaint(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrComplaintNotFound
		}
		return nil, err
	}
	return c, nil
}

// ListComplaintsByCustomer returns complaints raised by a customer, newest first.
func (s *Store) ListComplaintsByCustomer(ctx context.Context, customerID uuid.UUID, limit int) ([]Complaint, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
        SELECT id, ride_id, customer_id, partner_id, category, description, status,
               resolution_note, resolved_by, resolved_at, created_at
        FROM rider_complaints
        WHERE customer_id = $1
        ORDER BY created_at DESC
        LIMIT $2`
	rows, err := s.db.Query(ctx, q, customerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list complaints by customer: %w", err)
	}
	defer rows.Close()
	var out []Complaint
	for rows.Next() {
		c, err := scanComplaint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// ListComplaints returns complaints filtered by status (empty = all),
// newest first. Used by the admin queue.
func (s *Store) ListComplaints(ctx context.Context, status string, limit, offset int) ([]Complaint, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	q := `
        SELECT id, ride_id, customer_id, partner_id, category, description, status,
               resolution_note, resolved_by, resolved_at, created_at
        FROM rider_complaints
        WHERE ($1::text IS NULL OR status = $1)
        ORDER BY created_at DESC
        LIMIT $2 OFFSET $3`
	var statusPtr *string
	if status != "" {
		statusPtr = &status
	}
	rows, err := s.db.Query(ctx, q, statusPtr, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list complaints: %w", err)
	}
	defer rows.Close()
	var out []Complaint
	for rows.Next() {
		c, err := scanComplaint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// UpdateComplaintStatusInput is the input shape for UpdateComplaintStatus.
type UpdateComplaintStatusInput struct {
	ComplaintID uuid.UUID
	Status      string
	Note        *string
	ResolvedBy  uuid.UUID
}

// UpdateComplaintStatus moves the complaint to the new status. When status
// becomes a terminal value (resolved/dismissed) the resolved_by + resolved_at
// columns are stamped from the admin actor.
func (s *Store) UpdateComplaintStatus(ctx context.Context, in UpdateComplaintStatusInput) (*Complaint, error) {
	if !allowedComplaintStatuses[in.Status] {
		return nil, fmt.Errorf("invalid: status %q not allowed", in.Status)
	}
	const q = `
        UPDATE rider_complaints
        SET status          = $2,
            resolution_note = COALESCE($3, resolution_note),
            resolved_by     = CASE WHEN $2 IN ('resolved','dismissed') THEN $4 ELSE resolved_by END,
            resolved_at     = CASE WHEN $2 IN ('resolved','dismissed') THEN NOW() ELSE resolved_at END
        WHERE id = $1
        RETURNING id, ride_id, customer_id, partner_id, category, description, status,
                  resolution_note, resolved_by, resolved_at, created_at`
	row := s.db.QueryRow(ctx, q, in.ComplaintID, in.Status, in.Note, in.ResolvedBy)
	c, err := scanComplaint(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrComplaintNotFound
		}
		return nil, err
	}
	return c, nil
}

// CountOpenComplaints returns the number of complaints in `open` or
// `under_review`. Used by the admin dashboard counter.
func (s *Store) CountOpenComplaints(ctx context.Context) (int, error) {
	const q = `SELECT COUNT(*)::int FROM rider_complaints WHERE status IN ('open','under_review')`
	var n int
	if err := s.db.QueryRow(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("count open complaints: %w", err)
	}
	return n, nil
}

func scanComplaint(row pgx.Row) (*Complaint, error) {
	var c Complaint
	if err := row.Scan(
		&c.ID, &c.RideID, &c.CustomerID, &c.PartnerID, &c.Category, &c.Description, &c.Status,
		&c.ResolutionNote, &c.ResolvedBy, &c.ResolvedAt, &c.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &c, nil
}
