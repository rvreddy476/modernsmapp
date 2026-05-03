package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AuditLog is one row in rider_admin_audit_logs. Captures who did what to
// what, plus the request envelope (path / method / ip / user-agent / body
// summary / response status / latency). The middleware writes one row per
// /v1/rider/admin/* request; service-layer audit calls also work because
// the request-context columns are nullable.
type AuditLog struct {
	ID             uuid.UUID  `json:"id"`
	AdminUserID    uuid.UUID  `json:"admin_user_id"`
	Action         string     `json:"action"`
	EntityType     string     `json:"entity_type"`
	EntityID       *uuid.UUID `json:"entity_id,omitempty"`
	OldValue       []byte     `json:"old_value,omitempty"`
	NewValue       []byte     `json:"new_value,omitempty"`
	IPAddress      *string    `json:"ip_address,omitempty"`
	UserAgent      *string    `json:"user_agent,omitempty"`
	RequestPath    *string    `json:"request_path,omitempty"`
	RequestMethod  *string    `json:"request_method,omitempty"`
	RequestBody    *string    `json:"request_body,omitempty"`
	ResponseStatus *int       `json:"response_status,omitempty"`
	LatencyMS      *int       `json:"latency_ms,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

// RecordAuditInput is the input shape for RecordAudit. Optional fields are
// pointers so the middleware (full request context) and service layer
// (action only) can share the same writer without zero values polluting
// the audit row.
type RecordAuditInput struct {
	AdminUserID    uuid.UUID
	Action         string
	EntityType     string
	EntityID       *uuid.UUID
	OldValue       []byte
	NewValue       []byte
	IPAddress      *string
	UserAgent      *string
	RequestPath    *string
	RequestMethod  *string
	RequestBody    *string
	ResponseStatus *int
	LatencyMS      *int
}

// RecordAudit inserts one audit row. action + entity_type are required
// (they're NOT NULL in the schema). Returns the inserted row.
func (s *Store) RecordAudit(ctx context.Context, in RecordAuditInput) (*AuditLog, error) {
	if in.AdminUserID == uuid.Nil {
		return nil, fmt.Errorf("audit: admin_user_id required")
	}
	if in.Action == "" {
		return nil, fmt.Errorf("audit: action required")
	}
	if in.EntityType == "" {
		return nil, fmt.Errorf("audit: entity_type required")
	}
	const q = `
        INSERT INTO rider_admin_audit_logs (
            admin_user_id, action, entity_type, entity_id,
            old_value, new_value, ip_address, user_agent,
            request_path, request_method, request_body,
            response_status, latency_ms
        ) VALUES (
            $1, $2, $3, $4,
            $5, $6, $7, $8,
            $9, $10, $11,
            $12, $13
        )
        RETURNING id, admin_user_id, action, entity_type, entity_id,
                  old_value, new_value, ip_address, user_agent,
                  request_path, request_method, request_body,
                  response_status, latency_ms, created_at`
	row := s.db.QueryRow(ctx, q,
		in.AdminUserID, in.Action, in.EntityType, in.EntityID,
		in.OldValue, in.NewValue, in.IPAddress, in.UserAgent,
		in.RequestPath, in.RequestMethod, in.RequestBody,
		in.ResponseStatus, in.LatencyMS,
	)
	return scanAudit(row)
}

// AuditFilter is the listing query for ListAuditLogs.
type AuditFilter struct {
	Actor      *uuid.UUID
	Action     string
	EntityType string
	Since      *time.Time
	Limit      int
	Offset     int
}

// ListAuditLogs returns audit rows matching the filter, newest first.
// Bounded at 500 rows per call so a runaway query never tar-pits the DB.
func (s *Store) ListAuditLogs(ctx context.Context, f AuditFilter) ([]AuditLog, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	q := `
        SELECT id, admin_user_id, action, entity_type, entity_id,
               old_value, new_value, ip_address, user_agent,
               request_path, request_method, request_body,
               response_status, latency_ms, created_at
        FROM rider_admin_audit_logs
        WHERE ($1::uuid IS NULL OR admin_user_id = $1)
          AND ($2::text IS NULL OR action = $2)
          AND ($3::text IS NULL OR entity_type = $3)
          AND ($4::timestamptz IS NULL OR created_at >= $4)
        ORDER BY created_at DESC
        LIMIT $5 OFFSET $6`
	var actionPtr, entityPtr *string
	if f.Action != "" {
		actionPtr = &f.Action
	}
	if f.EntityType != "" {
		entityPtr = &f.EntityType
	}
	rows, err := s.db.Query(ctx, q, f.Actor, actionPtr, entityPtr, f.Since, f.Limit, f.Offset)
	if err != nil {
		return nil, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()
	var out []AuditLog
	for rows.Next() {
		a, err := scanAudit(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// GetAuditLog returns one audit row by id.
func (s *Store) GetAuditLog(ctx context.Context, id uuid.UUID) (*AuditLog, error) {
	const q = `
        SELECT id, admin_user_id, action, entity_type, entity_id,
               old_value, new_value, ip_address, user_agent,
               request_path, request_method, request_body,
               response_status, latency_ms, created_at
        FROM rider_admin_audit_logs
        WHERE id = $1`
	row := s.db.QueryRow(ctx, q, id)
	a, err := scanAudit(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("audit: not found")
		}
		return nil, err
	}
	return a, nil
}

func scanAudit(row pgx.Row) (*AuditLog, error) {
	var a AuditLog
	if err := row.Scan(
		&a.ID, &a.AdminUserID, &a.Action, &a.EntityType, &a.EntityID,
		&a.OldValue, &a.NewValue, &a.IPAddress, &a.UserAgent,
		&a.RequestPath, &a.RequestMethod, &a.RequestBody,
		&a.ResponseStatus, &a.LatencyMS, &a.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &a, nil
}
