// Panic incidents (Dating plan lane D8).
//
// Every panic and every meet check-in "help" lands here. The row is written
// before anything is published, so an incident exists for responders even
// when Kafka or notification-service is down.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Panic incident sources.
const (
	PanicSourcePanic       = "panic"
	PanicSourceMeetCheckIn = "meet_checkin"
	PanicSourceLegacy      = "legacy"
)

// Panic incident statuses.
const (
	PanicStatusOpen         = "open"
	PanicStatusAcknowledged = "acknowledged"
	PanicStatusResolved     = "resolved"
)

// Panic defaults: a second trigger within DefaultPanicDedupeWindow updates
// the same incident; above DefaultPanicDailyLimit incidents in
// PanicQuotaWindow a new incident is flagged suspected_abuse and not paged.
const (
	DefaultPanicDedupeWindow = 2 * time.Minute
	DefaultPanicDailyLimit   = 5
	PanicQuotaWindow         = 24 * time.Hour
)

// ErrPanicAlreadyResolved is returned when resolving a resolved incident.
var ErrPanicAlreadyResolved = errors.New("invalid: panic incident already resolved")

// PanicIncident is one row of dating_panic_incidents, coordinates included.
// Only the admin detail route serialises it.
type PanicIncident struct {
	ID               uuid.UUID      `json:"id"`
	UserID           uuid.UUID      `json:"user_id"`
	Source           string         `json:"source"`
	MeetID           *uuid.UUID     `json:"meet_id,omitempty"`
	Latitude         *float64       `json:"latitude,omitempty"`
	Longitude        *float64       `json:"longitude,omitempty"`
	Context          map[string]any `json:"context,omitempty"`
	TriggerCount     int            `json:"trigger_count"`
	FirstTriggeredAt time.Time      `json:"first_triggered_at"`
	LastTriggeredAt  time.Time      `json:"last_triggered_at"`
	Status           string         `json:"status"`
	SuspectedAbuse   bool           `json:"suspected_abuse"`
	AcknowledgedAt   *time.Time     `json:"acknowledged_at,omitempty"`
	AcknowledgedBy   *uuid.UUID     `json:"acknowledged_by,omitempty"`
	ResolvedAt       *time.Time     `json:"resolved_at,omitempty"`
	ResolvedBy       *uuid.UUID     `json:"resolved_by,omitempty"`
	ResolutionNote   *string        `json:"resolution_note,omitempty"`
	Anonymised       bool           `json:"anonymised"`
	CreatedAt        time.Time      `json:"created_at"`
}

// HasLocation reports whether the incident holds a point.
func (p *PanicIncident) HasLocation() bool {
	return p != nil && p.Latitude != nil && p.Longitude != nil
}

// PanicIncidentSummary is the admin list row. It carries no coordinates and
// no free-form context: those are only in the audited detail view.
type PanicIncidentSummary struct {
	ID               uuid.UUID  `json:"id"`
	UserID           uuid.UUID  `json:"user_id"`
	Source           string     `json:"source"`
	MeetID           *uuid.UUID `json:"meet_id,omitempty"`
	TriggerCount     int        `json:"trigger_count"`
	FirstTriggeredAt time.Time  `json:"first_triggered_at"`
	LastTriggeredAt  time.Time  `json:"last_triggered_at"`
	Status           string     `json:"status"`
	SuspectedAbuse   bool       `json:"suspected_abuse"`
	HasLocation      bool       `json:"has_location"`
	AcknowledgedAt   *time.Time `json:"acknowledged_at,omitempty"`
	ResolvedAt       *time.Time `json:"resolved_at,omitempty"`
	Anonymised       bool       `json:"anonymised"`
	CreatedAt        time.Time  `json:"created_at"`
}

const panicIncidentCols = `id, user_id, source, meet_id, latitude, longitude, context,
    trigger_count, first_triggered_at, last_triggered_at, status, suspected_abuse,
    acknowledged_at, acknowledged_by, resolved_at, resolved_by, resolution_note,
    anonymised_at IS NOT NULL, created_at`

func scanPanicIncident(row pgx.Row) (*PanicIncident, error) {
	p := &PanicIncident{}
	var raw []byte
	if err := row.Scan(&p.ID, &p.UserID, &p.Source, &p.MeetID, &p.Latitude, &p.Longitude, &raw,
		&p.TriggerCount, &p.FirstTriggeredAt, &p.LastTriggeredAt, &p.Status, &p.SuspectedAbuse,
		&p.AcknowledgedAt, &p.AcknowledgedBy, &p.ResolvedAt, &p.ResolvedBy, &p.ResolutionNote,
		&p.Anonymised, &p.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPanicNotFound
		}
		return nil, fmt.Errorf("scan panic incident: %w", err)
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &p.Context)
	}
	if len(p.Context) == 0 {
		p.Context = nil
	}
	return p, nil
}

// RecordPanicParams is one trigger. Latitude and Longitude are both set or
// both nil (the service drops a half or out-of-range point).
type RecordPanicParams struct {
	UserID       uuid.UUID
	Source       string
	MeetID       *uuid.UUID
	Latitude     *float64
	Longitude    *float64
	Context      map[string]any
	DedupeWindow time.Duration
	DailyLimit   int
}

// RecordPanicOutcome says what the trigger did. Page is true only for a new
// incident within the daily limit: a deduplicated trigger and a suspected
// abuse incident never page again.
type RecordPanicOutcome struct {
	Incident *PanicIncident
	Created  bool
	Page     bool
}

// RecordPanicIncident writes one trigger. Under a per-user advisory lock it
// either updates the user's unresolved incident last triggered within the
// dedupe window, or inserts a new incident, flagged suspected_abuse when the
// user already has DailyLimit incidents in the last 24h.
func (s *Store) RecordPanicIncident(ctx context.Context, p RecordPanicParams) (*RecordPanicOutcome, error) {
	if p.UserID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	switch p.Source {
	case PanicSourcePanic, PanicSourceMeetCheckIn:
	default:
		return nil, fmt.Errorf("invalid: panic source %q", p.Source)
	}
	if (p.Latitude == nil) != (p.Longitude == nil) {
		p.Latitude, p.Longitude = nil, nil
	}
	window := p.DedupeWindow
	if window <= 0 {
		window = DefaultPanicDedupeWindow
	}
	limit := p.DailyLimit
	if limit <= 0 {
		limit = DefaultPanicDailyLimit
	}
	ctxJSON := []byte(`{}`)
	if len(p.Context) > 0 {
		b, err := json.Marshal(p.Context)
		if err != nil {
			return nil, fmt.Errorf("invalid: panic context: %w", err)
		}
		ctxJSON = b
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin panic: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "dating_panic:"+p.UserID.String()); err != nil {
		return nil, fmt.Errorf("lock panic: %w", err)
	}

	var existing uuid.UUID
	err = tx.QueryRow(ctx, `
        SELECT id FROM dating_panic_incidents
        WHERE user_id = $1 AND status <> 'resolved'
          AND last_triggered_at > now() - make_interval(secs => $2)
        ORDER BY last_triggered_at DESC
        LIMIT 1
        FOR UPDATE`, p.UserID, window.Seconds()).Scan(&existing)
	switch {
	case err == nil:
		inc, err := scanPanicIncident(tx.QueryRow(ctx, `
            UPDATE dating_panic_incidents
            SET trigger_count     = trigger_count + 1,
                last_triggered_at = now(),
                latitude  = CASE WHEN $2::float8 IS NOT NULL THEN $2::float8 ELSE latitude END,
                longitude = CASE WHEN $2::float8 IS NOT NULL THEN $3::float8 ELSE longitude END,
                context   = context || $4::jsonb,
                meet_id   = COALESCE(meet_id, $5)
            WHERE id = $1
            RETURNING `+panicIncidentCols, existing, p.Latitude, p.Longitude, ctxJSON, p.MeetID))
		if err != nil {
			return nil, fmt.Errorf("update panic incident: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit panic: %w", err)
		}
		return &RecordPanicOutcome{Incident: inc}, nil
	case errors.Is(err, pgx.ErrNoRows):
	default:
		return nil, fmt.Errorf("find open panic incident: %w", err)
	}

	var recent int
	if err := tx.QueryRow(ctx, `
        SELECT COUNT(*)::int FROM dating_panic_incidents
        WHERE user_id = $1 AND created_at > now() - make_interval(secs => $2)`,
		p.UserID, PanicQuotaWindow.Seconds()).Scan(&recent); err != nil {
		return nil, fmt.Errorf("count panic incidents: %w", err)
	}
	suspected := recent >= limit
	inc, err := scanPanicIncident(tx.QueryRow(ctx, `
        INSERT INTO dating_panic_incidents
            (user_id, source, meet_id, latitude, longitude, context, suspected_abuse, page_required)
        VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8)
        RETURNING `+panicIncidentCols,
		p.UserID, p.Source, p.MeetID, p.Latitude, p.Longitude, ctxJSON, suspected, !suspected))
	if err != nil {
		return nil, fmt.Errorf("insert panic incident: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit panic: %w", err)
	}
	return &RecordPanicOutcome{Incident: inc, Created: true, Page: !suspected}, nil
}

// MarkPanicPaged records that dating.safety.panic was published.
func (s *Store) MarkPanicPaged(ctx context.Context, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_panic_incidents SET paged_at = now()
        WHERE id = $1 AND paged_at IS NULL`, id); err != nil {
		return fmt.Errorf("mark panic paged: %w", err)
	}
	return nil
}

// panicRepageGrace leaves the request path time to publish before the
// sweeper treats an incident as unpaged.
const panicRepageGrace = 30 * time.Second

// ListPanicIncidentsAwaitingPage returns unresolved incidents that should
// have paged but were never marked paged, younger than maxAge.
func (s *Store) ListPanicIncidentsAwaitingPage(ctx context.Context, maxAge time.Duration, limit int) ([]*PanicIncident, error) {
	if limit <= 0 {
		limit = 100
	}
	if maxAge <= 0 {
		maxAge = time.Hour
	}
	rows, err := s.db.Query(ctx, `
        SELECT `+panicIncidentCols+`
        FROM dating_panic_incidents
        WHERE page_required AND paged_at IS NULL AND status <> 'resolved'
          AND anonymised_at IS NULL
          AND created_at > now() - make_interval(secs => $1)
          AND created_at < now() - make_interval(secs => $2)
        ORDER BY created_at
        LIMIT $3`, maxAge.Seconds(), panicRepageGrace.Seconds(), limit)
	if err != nil {
		return nil, fmt.Errorf("list unpaged panic incidents: %w", err)
	}
	defer rows.Close()
	var out []*PanicIncident
	for rows.Next() {
		inc, err := scanPanicIncident(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	return out, rows.Err()
}

// GetPanicIncident returns one incident with its coordinates.
func (s *Store) GetPanicIncident(ctx context.Context, id uuid.UUID) (*PanicIncident, error) {
	if id == uuid.Nil {
		return nil, ErrPanicNotFound
	}
	return scanPanicIncident(s.db.QueryRow(ctx,
		`SELECT `+panicIncidentCols+` FROM dating_panic_incidents WHERE id = $1`, id))
}

// ListPanicIncidents is the admin queue: newest first, paginated, optionally
// one status, never coordinates.
func (s *Store) ListPanicIncidents(ctx context.Context, status string, limit, offset int) ([]*PanicIncidentSummary, error) {
	switch status {
	case "", PanicStatusOpen, PanicStatusAcknowledged, PanicStatusResolved:
	default:
		return nil, fmt.Errorf("invalid: status must be open, acknowledged or resolved")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, source, meet_id, trigger_count, first_triggered_at, last_triggered_at,
               status, suspected_abuse, (latitude IS NOT NULL AND longitude IS NOT NULL),
               acknowledged_at, resolved_at, anonymised_at IS NOT NULL, created_at
        FROM dating_panic_incidents
        WHERE ($1 = '' OR status = $1)
        ORDER BY created_at DESC, id
        LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list panic incidents: %w", err)
	}
	defer rows.Close()
	out := make([]*PanicIncidentSummary, 0, limit)
	for rows.Next() {
		p := &PanicIncidentSummary{}
		if err := rows.Scan(&p.ID, &p.UserID, &p.Source, &p.MeetID, &p.TriggerCount, &p.FirstTriggeredAt,
			&p.LastTriggeredAt, &p.Status, &p.SuspectedAbuse, &p.HasLocation,
			&p.AcknowledgedAt, &p.ResolvedAt, &p.Anonymised, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan panic incident summary: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AcknowledgePanicIncident moves an open incident to acknowledged. It returns
// the row and true when this call acknowledged it; an incident that is no
// longer open returns (row, false, ErrPanicAlreadyAcked); a missing one
// ErrPanicNotFound.
func (s *Store) AcknowledgePanicIncident(ctx context.Context, id, adminID uuid.UUID) (*PanicIncident, bool, error) {
	if id == uuid.Nil {
		return nil, false, fmt.Errorf("invalid: panic_id required")
	}
	inc, err := scanPanicIncident(s.db.QueryRow(ctx, `
        UPDATE dating_panic_incidents
        SET status = 'acknowledged', acknowledged_at = now(), acknowledged_by = $2
        WHERE id = $1 AND status = 'open'
        RETURNING `+panicIncidentCols, id, adminID))
	if err == nil {
		return inc, true, nil
	}
	if !errors.Is(err, ErrPanicNotFound) {
		return nil, false, fmt.Errorf("acknowledge panic incident: %w", err)
	}
	existing, gerr := s.GetPanicIncident(ctx, id)
	if gerr != nil {
		return nil, false, gerr
	}
	return existing, false, ErrPanicAlreadyAcked
}

// ResolvePanicIncident closes an open or acknowledged incident with a note,
// stamping the acknowledgement too when it was skipped. A resolved incident
// returns (row, false, ErrPanicAlreadyResolved).
func (s *Store) ResolvePanicIncident(ctx context.Context, id, adminID uuid.UUID, note string) (*PanicIncident, bool, error) {
	if id == uuid.Nil {
		return nil, false, fmt.Errorf("invalid: panic_id required")
	}
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	inc, err := scanPanicIncident(s.db.QueryRow(ctx, `
        UPDATE dating_panic_incidents
        SET status = 'resolved', resolved_at = now(), resolved_by = $2, resolution_note = $3,
            acknowledged_at = COALESCE(acknowledged_at, now()),
            acknowledged_by = COALESCE(acknowledged_by, $2)
        WHERE id = $1 AND status <> 'resolved'
        RETURNING `+panicIncidentCols, id, adminID, notePtr))
	if err == nil {
		return inc, true, nil
	}
	if !errors.Is(err, ErrPanicNotFound) {
		return nil, false, fmt.Errorf("resolve panic incident: %w", err)
	}
	existing, gerr := s.GetPanicIncident(ctx, id)
	if gerr != nil {
		return nil, false, gerr
	}
	return existing, false, ErrPanicAlreadyResolved
}
