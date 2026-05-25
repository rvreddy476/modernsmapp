// Safety store — spec §15 safety center: panic, location share, safe-meet,
// check-in, block, report.
//
// Design rule (CRITICAL RULES #6): every safety-adjacent code path is
// explicit. We persist *before* emitting events so the panic / report
// endpoints cannot be silently dropped if Kafka is unhappy.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SafetyEvent is one row of dating_safety_events.
type SafetyEvent struct {
	ID        uuid.UUID      `json:"id"`
	UserID    uuid.UUID      `json:"user_id"`
	Kind      string         `json:"kind"`
	Details   map[string]any `json:"details,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// LocationShare is a transient share-link record. The store row captures
// the static initiator location at creation time; the moving WebSocket
// updates land in Redis (see service.LocationShareKey) and are not
// persisted in Postgres.
type LocationShare struct {
	ShareID   uuid.UUID `json:"share_id"`
	UserID    uuid.UUID `json:"user_id"`
	ContactID uuid.UUID `json:"contact_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Meet is one row of dating_meets — a scheduled in-person meet.
type Meet struct {
	ID            uuid.UUID  `json:"id"`
	UserID        uuid.UUID  `json:"user_id"`
	WithUserID    uuid.UUID  `json:"with_user_id"`
	ScheduledAt   time.Time  `json:"scheduled_at"`
	Venue         *string    `json:"venue,omitempty"`
	Latitude      *float64   `json:"latitude,omitempty"`
	Longitude     *float64   `json:"longitude,omitempty"`
	CheckInStatus *string    `json:"check_in_status,omitempty"`
	CheckedInAt   *time.Time `json:"checked_in_at,omitempty"`
	NoShowAt      *time.Time `json:"no_show_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Report is a row of dating_reports.
type Report struct {
	ID         uuid.UUID `json:"id"`
	ReporterID uuid.UUID `json:"reporter_id"`
	TargetID   uuid.UUID `json:"target_id"`
	Category   string    `json:"category"`
	Details    string    `json:"details"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// ErrMeetNotFound is returned when no meet matches the lookup.
var ErrMeetNotFound = errors.New("not_found: meet not found")

// RecordSafetyEvent inserts a safety event and returns the generated id.
// Callers must persist before emitting Kafka — this method must succeed
// before any event is published, otherwise we lose the audit trail.
func (s *Store) RecordSafetyEvent(ctx context.Context, userID uuid.UUID, kind string, details map[string]any) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: user_id required")
	}
	if kind == "" {
		return fmt.Errorf("invalid: kind required")
	}
	var raw []byte
	if details != nil {
		buf, err := json.Marshal(details)
		if err != nil {
			return fmt.Errorf("marshal details: %w", err)
		}
		raw = buf
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_safety_events (user_id, kind, details)
        VALUES ($1, $2, $3)`, userID, kind, raw); err != nil {
		return fmt.Errorf("record safety event: %w", err)
	}
	return nil
}

// CreateLiveLocationShare allocates a share_id with the given duration and
// records the bookkeeping safety event. The location-stream itself runs
// over WebSocket in S5; v1 keeps the static at-creation snapshot in Redis
// (set by the service layer keyed `dating:location_share:<share_id>`).
func (s *Store) CreateLiveLocationShare(ctx context.Context, userID, contactID uuid.UUID, durationMinutes int) (*LocationShare, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	if contactID == uuid.Nil {
		return nil, fmt.Errorf("invalid: contact_id required")
	}
	if durationMinutes <= 0 || durationMinutes > 24*60 {
		durationMinutes = 60
	}
	share := &LocationShare{
		ShareID:   uuid.New(),
		UserID:    userID,
		ContactID: contactID,
		ExpiresAt: time.Now().Add(time.Duration(durationMinutes) * time.Minute),
	}
	if err := s.RecordSafetyEvent(ctx, userID, "location_share_created", map[string]any{
		"share_id":         share.ShareID.String(),
		"contact_id":       contactID.String(),
		"duration_minutes": durationMinutes,
	}); err != nil {
		return nil, err
	}
	return share, nil
}

// ScheduleMeet inserts a dating_meets row.
func (s *Store) ScheduleMeet(ctx context.Context, userID, withUserID uuid.UUID, when time.Time, lat, lng float64, venue string) (uuid.UUID, error) {
	if userID == uuid.Nil || withUserID == uuid.Nil {
		return uuid.Nil, fmt.Errorf("invalid: user ids required")
	}
	if userID == withUserID {
		return uuid.Nil, fmt.Errorf("invalid: cannot schedule a meet with yourself")
	}
	if when.IsZero() {
		return uuid.Nil, fmt.Errorf("invalid: scheduled time required")
	}
	var venuePtr *string
	if venue != "" {
		venuePtr = &venue
	}
	var latPtr, lngPtr *float64
	if lat != 0 || lng != 0 {
		latPtr, lngPtr = &lat, &lng
	}
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `
        INSERT INTO dating_meets (user_id, with_user_id, scheduled_at, venue, latitude, longitude)
        VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id`,
		userID, withUserID, when, venuePtr, latPtr, lngPtr).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("schedule meet: %w", err)
	}
	return id, nil
}

// MeetCheckIn records a user's confirmation that a meet is going OK
// (status='safe') or escalates to help (status='help').
func (s *Store) MeetCheckIn(ctx context.Context, meetID, userID uuid.UUID, status string) error {
	switch status {
	case "safe", "help":
	default:
		return fmt.Errorf("invalid: status must be safe|help")
	}
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_meets
        SET check_in_status = $3, checked_in_at = now()
        WHERE id = $1 AND user_id = $2`, meetID, userID, status)
	if err != nil {
		return fmt.Errorf("meet check in: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMeetNotFound
	}
	return nil
}

// GetMeet returns one row by id.
func (s *Store) GetMeet(ctx context.Context, id uuid.UUID) (*Meet, error) {
	row := s.db.QueryRow(ctx, `
        SELECT id, user_id, with_user_id, scheduled_at, venue, latitude, longitude,
               check_in_status, checked_in_at, no_show_at, created_at
        FROM dating_meets WHERE id = $1`, id)
	m := &Meet{}
	if err := row.Scan(
		&m.ID, &m.UserID, &m.WithUserID, &m.ScheduledAt, &m.Venue, &m.Latitude, &m.Longitude,
		&m.CheckInStatus, &m.CheckedInAt, &m.NoShowAt, &m.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMeetNotFound
		}
		return nil, fmt.Errorf("scan meet: %w", err)
	}
	return m, nil
}

// MarkMeetNoShow stamps no_show_at — used by the S5 follow-up worker.
func (s *Store) MarkMeetNoShow(ctx context.Context, meetID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
        UPDATE dating_meets
        SET no_show_at = now()
        WHERE id = $1 AND check_in_status IS NULL AND no_show_at IS NULL`,
		meetID)
	if err != nil {
		return fmt.Errorf("mark no show: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrMeetNotFound
	}
	return nil
}

// BlockUser inserts a row into dating_blocks. Idempotent on (user_id,
// blocked_id). Propagation to graph-service is the service layer's job.
func (s *Store) BlockUser(ctx context.Context, userID, targetUserID uuid.UUID) error {
	if userID == uuid.Nil || targetUserID == uuid.Nil {
		return fmt.Errorf("invalid: user ids required")
	}
	if userID == targetUserID {
		return fmt.Errorf("invalid: cannot block yourself")
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_blocks (user_id, blocked_id)
        VALUES ($1, $2)
        ON CONFLICT DO NOTHING`, userID, targetUserID); err != nil {
		return fmt.Errorf("block user: %w", err)
	}
	return nil
}

// CreateReport persists a report into dating_reports. The service layer
// emits dating.report.created for trust-safety-service intake afterwards.
func (s *Store) CreateReport(ctx context.Context, reporterID, targetID uuid.UUID, category, details string) (*Report, error) {
	if reporterID == uuid.Nil || targetID == uuid.Nil {
		return nil, fmt.Errorf("invalid: reporter and target ids required")
	}
	if reporterID == targetID {
		return nil, fmt.Errorf("invalid: cannot report yourself")
	}
	if category == "" {
		return nil, fmt.Errorf("invalid: category required")
	}
	r := &Report{
		ReporterID: reporterID,
		TargetID:   targetID,
		Category:   category,
		Details:    details,
	}
	err := s.db.QueryRow(ctx, `
        INSERT INTO dating_reports (reporter_id, target_id, category, details)
        VALUES ($1, $2, $3, $4)
        RETURNING id, created_at`,
		reporterID, targetID, category, details).Scan(&r.ID, &r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}
	return r, nil
}

// ListReports returns dating_reports ordered newest-first with simple
// filters. Used by /admin/dating/reports. Pagination via limit+offset
// is fine here because the table grows slowly (one row per report)
// compared to a high-volume timeline.
func (s *Store) ListReports(ctx context.Context, status, category string, limit, offset int) ([]*Report, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{}
	where := []string{"1=1"}
	if status != "" {
		args = append(args, status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if category != "" {
		args = append(args, category)
		where = append(where, fmt.Sprintf("category = $%d", len(args)))
	}
	args = append(args, limit, offset)
	q := `
        SELECT id, reporter_id, target_id, category, details, status, created_at
        FROM dating_reports
        WHERE ` + strings.Join(where, " AND ") + `
        ORDER BY created_at DESC
        LIMIT $` + fmt.Sprintf("%d", len(args)-1) +
		` OFFSET $` + fmt.Sprintf("%d", len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()
	out := make([]*Report, 0, limit)
	for rows.Next() {
		r := &Report{}
		var details sql.NullString
		var st sql.NullString
		if err := rows.Scan(&r.ID, &r.ReporterID, &r.TargetID, &r.Category, &details, &st, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}
		r.Details = details.String
		r.Status = st.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimMeetsDueForReminder finds dating_meets whose scheduled_at sits
// inside the [now+leadMin, now+leadMax] window AND whose
// reminder_fired_at is NULL. Atomically marks reminder_fired_at =
// NOW() and returns the claimed rows so the sweeper can emit one
// safe_meet.reminder event per row without risk of double-firing
// across replicas. §P1-6.
func (s *Store) ClaimMeetsDueForReminder(ctx context.Context, leadMin, leadMax time.Duration, limit int) ([]*Meet, error) {
	if limit <= 0 {
		limit = 200
	}
	now := time.Now()
	rows, err := s.db.Query(ctx, `
        UPDATE dating_meets
        SET reminder_fired_at = NOW()
        WHERE id IN (
            SELECT id FROM dating_meets
            WHERE reminder_fired_at IS NULL
              AND scheduled_at BETWEEN $1 AND $2
              AND check_in_status IS NULL
            ORDER BY scheduled_at ASC
            LIMIT $3
            FOR UPDATE SKIP LOCKED
        )
        RETURNING id, user_id, with_user_id, scheduled_at, venue,
                  latitude, longitude, check_in_status, checked_in_at,
                  no_show_at, created_at
    `, now.Add(leadMin), now.Add(leadMax), limit)
	if err != nil {
		return nil, fmt.Errorf("claim reminder meets: %w", err)
	}
	defer rows.Close()
	out := make([]*Meet, 0, limit)
	for rows.Next() {
		m := &Meet{}
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.WithUserID, &m.ScheduledAt, &m.Venue,
			&m.Latitude, &m.Longitude, &m.CheckInStatus, &m.CheckedInAt,
			&m.NoShowAt, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed meet: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ClaimMeetsMissedCheckIn finds dating_meets where scheduled_at is at
// least `gracePeriod` ago AND check_in_status is still NULL AND
// missed_check_in_fired_at is NULL. Atomically claims them with the
// same FOR UPDATE SKIP LOCKED pattern so replicas don't double-fire.
// §P1-6.
func (s *Store) ClaimMeetsMissedCheckIn(ctx context.Context, gracePeriod time.Duration, limit int) ([]*Meet, error) {
	if limit <= 0 {
		limit = 200
	}
	cutoff := time.Now().Add(-gracePeriod)
	rows, err := s.db.Query(ctx, `
        UPDATE dating_meets
        SET missed_check_in_fired_at = NOW()
        WHERE id IN (
            SELECT id FROM dating_meets
            WHERE missed_check_in_fired_at IS NULL
              AND check_in_status IS NULL
              AND scheduled_at < $1
            ORDER BY scheduled_at ASC
            LIMIT $2
            FOR UPDATE SKIP LOCKED
        )
        RETURNING id, user_id, with_user_id, scheduled_at, venue,
                  latitude, longitude, check_in_status, checked_in_at,
                  no_show_at, created_at
    `, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("claim missed-checkin meets: %w", err)
	}
	defer rows.Close()
	out := make([]*Meet, 0, limit)
	for rows.Next() {
		m := &Meet{}
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.WithUserID, &m.ScheduledAt, &m.Venue,
			&m.Latitude, &m.Longitude, &m.CheckInStatus, &m.CheckedInAt,
			&m.NoShowAt, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed meet: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// GetReportByID returns a single dating_reports row by id. Used by
// the report-status notification fanout so the publisher can scope
// the event to the reporter. Returns (nil, ErrReportNotFound) on miss.
func (s *Store) GetReportByID(ctx context.Context, reportID uuid.UUID) (*Report, error) {
	if reportID == uuid.Nil {
		return nil, fmt.Errorf("invalid: report_id required")
	}
	row := s.db.QueryRow(ctx, `
        SELECT id, reporter_id, target_id, category, details, status, created_at
        FROM dating_reports
        WHERE id = $1`, reportID)
	r := &Report{}
	var details sql.NullString
	var st sql.NullString
	if err := row.Scan(&r.ID, &r.ReporterID, &r.TargetID, &r.Category, &details, &st, &r.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReportNotFound
		}
		return nil, fmt.Errorf("scan report: %w", err)
	}
	r.Details = details.String
	r.Status = st.String
	return r, nil
}

// SetReportStatus moves a report through the §P0-8 state machine.
// Returns ErrReportNotFound when the row is missing.
func (s *Store) SetReportStatus(ctx context.Context, reportID uuid.UUID, status string) error {
	switch status {
	case "submitted", "under_review", "investigating", "actioned",
		"resolved", "dismissed", "closed_no_action":
	default:
		return fmt.Errorf("invalid: status %q", status)
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE dating_reports SET status = $2 WHERE id = $1`,
		reportID, status)
	if err != nil {
		return fmt.Errorf("update report status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrReportNotFound
	}
	return nil
}

// ErrReportNotFound is returned when SetReportStatus targets a row
// that does not exist.
var ErrReportNotFound = errors.New("not_found: report not found")

// ErrPanicAlreadyAcked is returned by AcknowledgePanic when the row
// already has acknowledged_at stamped. Idempotency at the store layer
// — the admin handler reads it as a soft success.
var ErrPanicAlreadyAcked = errors.New("invalid: panic event already acknowledged")

// AcknowledgePanic stamps acknowledged_at + acknowledged_by on a
// safety_events row whose kind='panic'. Returns the affected row
// (with the user_id needed for the notification fanout) and a flag
// indicating whether this call actually performed the ack (true) vs
// hit an already-acked row (false). Missing row → ErrPanicNotFound.
func (s *Store) AcknowledgePanic(ctx context.Context, panicID, adminID uuid.UUID) (*SafetyEvent, bool, error) {
	if panicID == uuid.Nil {
		return nil, false, fmt.Errorf("invalid: panic_id required")
	}
	// CTE update returns the row when this call performed the ack.
	// The COALESCE plus the WHERE clause ensures we don't overwrite an
	// existing acknowledged_at. We follow up with a SELECT so we can
	// distinguish "row missing" (ErrPanicNotFound) from "row exists,
	// already acked" (return existing row + acked=false).
	var actor any
	if adminID != uuid.Nil {
		actor = adminID
	}
	row := s.db.QueryRow(ctx, `
        UPDATE dating_safety_events
        SET acknowledged_at = NOW(),
            acknowledged_by = $2
        WHERE id = $1
          AND kind = 'panic'
          AND acknowledged_at IS NULL
        RETURNING id, user_id, kind, details, created_at`, panicID, actor)
	e := &SafetyEvent{}
	var raw []byte
	if err := row.Scan(&e.ID, &e.UserID, &e.Kind, &raw, &e.CreatedAt); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, false, fmt.Errorf("ack panic: %w", err)
		}
		// No row updated — either the id doesn't exist, isn't a panic,
		// or was already acked. Look up the row to differentiate.
		existing := s.db.QueryRow(ctx, `
            SELECT id, user_id, kind, details, created_at
            FROM dating_safety_events
            WHERE id = $1 AND kind = 'panic'`, panicID)
		ex := &SafetyEvent{}
		var exRaw []byte
		if err2 := existing.Scan(&ex.ID, &ex.UserID, &ex.Kind, &exRaw, &ex.CreatedAt); err2 != nil {
			if errors.Is(err2, pgx.ErrNoRows) {
				return nil, false, ErrPanicNotFound
			}
			return nil, false, fmt.Errorf("scan existing panic: %w", err2)
		}
		if len(exRaw) > 0 {
			_ = json.Unmarshal(exRaw, &ex.Details)
		}
		return ex, false, ErrPanicAlreadyAcked
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &e.Details)
	}
	return e, true, nil
}

// ErrPanicNotFound is returned when AcknowledgePanic is called against
// an id that is not a panic safety_event.
var ErrPanicNotFound = errors.New("not_found: panic event not found")

// ListPanicEvents returns recent dating_safety_events of kind 'panic'
// newest-first across all users. Used by /admin/dating/panic for the
// on-call queue.
func (s *Store) ListPanicEvents(ctx context.Context, limit int) ([]*SafetyEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, kind, details, created_at
        FROM dating_safety_events
        WHERE kind = 'panic'
        ORDER BY created_at DESC
        LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list panic events: %w", err)
	}
	defer rows.Close()
	out := make([]*SafetyEvent, 0, limit)
	for rows.Next() {
		e := &SafetyEvent{}
		var raw []byte
		if err := rows.Scan(&e.ID, &e.UserID, &e.Kind, &raw, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan panic event: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Details)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListSafetyEventsForUser returns the user's full safety_events history
// newest-first. Used by the DPDP data exporter (§15.8). Telemetry queries
// also call into this to compute the off-app meet rate.
func (s *Store) ListSafetyEventsForUser(ctx context.Context, userID uuid.UUID) ([]*SafetyEvent, error) {
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, kind, details, created_at
        FROM dating_safety_events
        WHERE user_id = $1
        ORDER BY created_at DESC
        LIMIT 500`, userID)
	if err != nil {
		return nil, fmt.Errorf("list safety events for user: %w", err)
	}
	defer rows.Close()
	out := make([]*SafetyEvent, 0, 16)
	for rows.Next() {
		e := &SafetyEvent{}
		var raw []byte
		if err := rows.Scan(&e.ID, &e.UserID, &e.Kind, &raw, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan safety event: %w", err)
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Details)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountSafetyEventsByKindWindow counts safety events of a particular kind
// within the supplied window. Used by the north-star telemetry job to
// compute the 30-day off-app-meet rate.
func (s *Store) CountSafetyEventsByKindWindow(ctx context.Context, kind string, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FROM dating_safety_events
        WHERE kind = $1 AND created_at >= $2`, kind, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count safety events: %w", err)
	}
	return n, nil
}

// CountSafetyMeetCheckInsSafeWindow counts safety_events with kind
// 'meet_check_in' and details->>'status' = 'safe' within the window. The
// north-star query treats this as the off-app-meet success metric.
func (s *Store) CountSafetyMeetCheckInsSafeWindow(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(*) FROM dating_safety_events
        WHERE kind = 'meet_check_in'
          AND details->>'status' = 'safe'
          AND created_at >= $1`, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count safe meet check-ins: %w", err)
	}
	return n, nil
}

// ListMeetsForReminder returns meets whose scheduled_at falls in the
// window (now+11.5h, now+12.5h] and which haven't been checked-in or
// no-show'd. The Phase 1 sweeper fires dating.safe_meet.reminder for
// each row roughly 12h before the meet. Window width gives the
// per-minute ticker enough overlap that a single missed tick doesn't
// drop the reminder; the consumer dedups via Redis.
func (s *Store) ListMeetsForReminder(ctx context.Context, now time.Time, limit int) ([]*Meet, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, with_user_id, scheduled_at, venue, latitude, longitude,
               check_in_status, checked_in_at, no_show_at, created_at
        FROM dating_meets
        WHERE scheduled_at > $1
          AND scheduled_at <= $2
          AND check_in_status IS NULL
          AND no_show_at IS NULL
        ORDER BY scheduled_at ASC
        LIMIT $3`,
		now.Add(11*time.Hour+30*time.Minute),
		now.Add(12*time.Hour+30*time.Minute),
		limit)
	if err != nil {
		return nil, fmt.Errorf("list meets for reminder: %w", err)
	}
	defer rows.Close()
	out := make([]*Meet, 0, limit)
	for rows.Next() {
		m := &Meet{}
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.WithUserID, &m.ScheduledAt, &m.Venue, &m.Latitude, &m.Longitude,
			&m.CheckInStatus, &m.CheckedInAt, &m.NoShowAt, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListMeetsForMissedCheckIn returns meets whose scheduled_at is more
// than 30 minutes ago, that haven't been checked-in / no-show'd. The
// Phase 1 sweeper fires dating.safe_meet.missed_check_in for each row.
// We cap the look-back to 6h so a long-ago abandoned meet doesn't get
// re-notified indefinitely after a Redis dedup flush.
func (s *Store) ListMeetsForMissedCheckIn(ctx context.Context, now time.Time, limit int) ([]*Meet, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, with_user_id, scheduled_at, venue, latitude, longitude,
               check_in_status, checked_in_at, no_show_at, created_at
        FROM dating_meets
        WHERE scheduled_at < $1
          AND scheduled_at > $2
          AND check_in_status IS NULL
          AND no_show_at IS NULL
        ORDER BY scheduled_at ASC
        LIMIT $3`,
		now.Add(-30*time.Minute),
		now.Add(-6*time.Hour),
		limit)
	if err != nil {
		return nil, fmt.Errorf("list meets for missed check-in: %w", err)
	}
	defer rows.Close()
	out := make([]*Meet, 0, limit)
	for rows.Next() {
		m := &Meet{}
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.WithUserID, &m.ScheduledAt, &m.Venue, &m.Latitude, &m.Longitude,
			&m.CheckInStatus, &m.CheckedInAt, &m.NoShowAt, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PendingMeetsForCheckin returns meets whose scheduled_at is older than
// `before` and which have not yet been checked-in or no-show'd. The S5
// no-show worker iterates this list.
func (s *Store) PendingMeetsForCheckin(ctx context.Context, before time.Time, limit int) ([]*Meet, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, with_user_id, scheduled_at, venue, latitude, longitude,
               check_in_status, checked_in_at, no_show_at, created_at
        FROM dating_meets
        WHERE scheduled_at < $1
          AND check_in_status IS NULL
          AND no_show_at IS NULL
        ORDER BY scheduled_at ASC
        LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("pending meets: %w", err)
	}
	defer rows.Close()
	out := make([]*Meet, 0, limit)
	for rows.Next() {
		m := &Meet{}
		if err := rows.Scan(
			&m.ID, &m.UserID, &m.WithUserID, &m.ScheduledAt, &m.Venue, &m.Latitude, &m.Longitude,
			&m.CheckInStatus, &m.CheckedInAt, &m.NoShowAt, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
