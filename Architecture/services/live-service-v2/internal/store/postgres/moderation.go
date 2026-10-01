package postgres

// Stream moderation v2 (1 Oct 2026): removed messages, per-stream bans and
// moderators, platform-wide live bans, viewer reports, and the admin
// actions over them. Admin actions write their live_admin_audit row in the
// same transaction as the change.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	// ErrDuplicate is a unique violation (a second report of one target).
	ErrDuplicate = errors.New("duplicate")
	// ErrReportResolved: the report was already resolved.
	ErrReportResolved = errors.New("report already resolved")
	// ErrReportRateLimited: the reporter filed too many reports recently.
	ErrReportRateLimited = errors.New("too many reports")
)

// StreamBan is one live_stream_bans row.
type StreamBan struct {
	StreamID uuid.UUID `json:"stream_id"`
	UserID   uuid.UUID `json:"user_id"`
	BannedBy uuid.UUID `json:"banned_by"`
	Reason   string    `json:"reason"`
	BannedAt time.Time `json:"created_at"`
}

// PlatformBan is one live_platform_bans row.
type PlatformBan struct {
	UserID   uuid.UUID `json:"user_id"`
	Reason   string    `json:"reason"`
	BannedBy uuid.UUID `json:"banned_by"`
	BannedAt time.Time `json:"banned_at"`
}

// Report is one live_reports row.
type Report struct {
	ID               uuid.UUID  `json:"id"`
	StreamID         uuid.UUID  `json:"stream_id"`
	ReporterID       uuid.UUID  `json:"reporter_id"`
	MessageID        *uuid.UUID `json:"message_id"`
	TargetUserID     uuid.UUID  `json:"target_user_id"`
	Reason           string     `json:"reason"`
	Note             string     `json:"note"`
	Status           string     `json:"status"`
	Resolution       *string    `json:"resolution"`
	ResolutionReason *string    `json:"resolution_reason"`
	ResolvedBy       *uuid.UUID `json:"resolved_by"`
	ResolvedAt       *time.Time `json:"resolved_at"`
	CreatedAt        time.Time  `json:"created_at"`
	// MessageText is the reported message's text (admin listing only).
	MessageText *string `json:"message_text,omitempty"`
}

const reportColumns = `id, stream_id, reporter_id, message_id, target_user_id, reason, note, status,
    resolution, resolution_reason, resolved_by, resolved_at, created_at`

func scanReport(row pgx.Row, extra ...any) (*Report, error) {
	var r Report
	err := row.Scan(append([]any{&r.ID, &r.StreamID, &r.ReporterID, &r.MessageID, &r.TargetUserID,
		&r.Reason, &r.Note, &r.Status, &r.Resolution, &r.ResolutionReason, &r.ResolvedBy,
		&r.ResolvedAt, &r.CreatedAt}, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// --- chat messages ---

// GetChatMessage returns one message of the stream, removed or not.
func (s *Store) GetChatMessage(ctx context.Context, streamID, messageID uuid.UUID) (*ChatMessage, error) {
	m := &ChatMessage{}
	err := s.db.QueryRow(ctx, `
		SELECT id, stream_id, user_id, text, is_pinned, pinned_at, created_at, removed_at
		FROM live_chat_messages WHERE id = $1 AND stream_id = $2`, messageID, streamID).
		Scan(&m.ID, &m.StreamID, &m.UserID, &m.Text, &m.IsPinned, &m.PinnedAt, &m.CreatedAt, &m.RemovedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// RemoveChatMessage hides a message for everyone (and unpins it). removed
// is false when it was already removed (idempotent).
func (s *Store) RemoveChatMessage(ctx context.Context, streamID, messageID, by uuid.UUID) (removed bool, err error) {
	return removeChatMessage(ctx, s.db, streamID, messageID, by)
}

func removeChatMessage(ctx context.Context, q querier, streamID, messageID, by uuid.UUID) (bool, error) {
	tag, err := q.Exec(ctx, `
		UPDATE live_chat_messages
		SET removed_at = NOW(), removed_by = $3, is_pinned = FALSE, pinned_at = NULL
		WHERE id = $1 AND stream_id = $2 AND removed_at IS NULL`, messageID, streamID, by)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	var exists bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM live_chat_messages WHERE id = $1 AND stream_id = $2)`,
		messageID, streamID).Scan(&exists); err != nil {
		return false, err
	}
	if !exists {
		return false, ErrNotFound
	}
	return false, nil
}

// --- moderators ---

// IsModerator reports whether userID moderates the stream.
func (s *Store) IsModerator(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM live_stream_moderators WHERE stream_id = $1 AND user_id = $2)`,
		streamID, userID).Scan(&ok)
	return ok, err
}

// ListModerators returns the stream's moderators, oldest first.
func (s *Store) ListModerators(ctx context.Context, streamID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM live_stream_moderators WHERE stream_id = $1 ORDER BY added_at, user_id`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ModeratorsFor returns the moderators of each of streamIDs in one query
// (streams without moderators are absent from the map).
func (s *Store) ModeratorsFor(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	out := map[uuid.UUID][]uuid.UUID{}
	if len(streamIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT stream_id, user_id FROM live_stream_moderators
		WHERE stream_id = ANY($1) ORDER BY stream_id, added_at, user_id`, streamIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sid, uid uuid.UUID
		if err := rows.Scan(&sid, &uid); err != nil {
			return nil, err
		}
		out[sid] = append(out[sid], uid)
	}
	return out, rows.Err()
}

// ReplaceModerators sets the moderator list to exactly userIDs.
func (s *Store) ReplaceModerators(ctx context.Context, streamID uuid.UUID, userIDs []uuid.UUID, by uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`DELETE FROM live_stream_moderators WHERE stream_id = $1 AND NOT (user_id = ANY($2))`,
		streamID, userIDs); err != nil {
		return err
	}
	for _, id := range userIDs {
		if _, err := tx.Exec(ctx, `
			INSERT INTO live_stream_moderators (stream_id, user_id, added_by) VALUES ($1, $2, $3)
			ON CONFLICT (stream_id, user_id) DO NOTHING`, streamID, id, by); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// --- stream bans ---

// BanFromStream bans userID from the stream (and drops any moderator seat).
func (s *Store) BanFromStream(ctx context.Context, streamID, userID, by uuid.UUID, reason string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := banFromStream(ctx, tx, streamID, userID, by, reason); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func banFromStream(ctx context.Context, q querier, streamID, userID, by uuid.UUID, reason string) error {
	if _, err := q.Exec(ctx, `
		INSERT INTO live_stream_bans (stream_id, user_id, banned_by, reason) VALUES ($1, $2, $3, $4)
		ON CONFLICT (stream_id, user_id) DO UPDATE SET banned_by = EXCLUDED.banned_by,
		    reason = EXCLUDED.reason, banned_at = NOW()`, streamID, userID, by, reason); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `DELETE FROM live_stream_moderators WHERE stream_id = $1 AND user_id = $2`, streamID, userID)
	return err
}

// UnbanFromStream lifts a stream ban. No error when absent.
func (s *Store) UnbanFromStream(ctx context.Context, streamID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM live_stream_bans WHERE stream_id = $1 AND user_id = $2`, streamID, userID)
	return err
}

// IsBannedFromStream reports a stream ban.
func (s *Store) IsBannedFromStream(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM live_stream_bans WHERE stream_id = $1 AND user_id = $2)`,
		streamID, userID).Scan(&ok)
	return ok, err
}

// ListStreamBans returns the stream's bans, newest first.
func (s *Store) ListStreamBans(ctx context.Context, streamID uuid.UUID) ([]StreamBan, error) {
	rows, err := s.db.Query(ctx, `
		SELECT stream_id, user_id, banned_by, reason, banned_at
		FROM live_stream_bans WHERE stream_id = $1 ORDER BY banned_at DESC`, streamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]StreamBan, 0)
	for rows.Next() {
		var b StreamBan
		if err := rows.Scan(&b.StreamID, &b.UserID, &b.BannedBy, &b.Reason, &b.BannedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// --- platform bans ---

// IsPlatformBanned reports a platform-wide live ban.
func (s *Store) IsPlatformBanned(ctx context.Context, userID uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM live_platform_bans WHERE user_id = $1)`, userID).Scan(&ok)
	return ok, err
}

// ListPlatformBans returns every platform live ban, newest first.
func (s *Store) ListPlatformBans(ctx context.Context, limit, offset int) ([]PlatformBan, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, reason, banned_by, banned_at FROM live_platform_bans
		ORDER BY banned_at DESC, user_id LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PlatformBan, 0)
	for rows.Next() {
		var b PlatformBan
		if err := rows.Scan(&b.UserID, &b.Reason, &b.BannedBy, &b.BannedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AdminSetPlatformBan bans (banned=true) or unbans userID platform-wide and
// writes the audit row in the same transaction.
func (s *Store) AdminSetPlatformBan(ctx context.Context, userID uuid.UUID, banned bool, reason string, audit AuditEntry) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if banned {
		_, err = tx.Exec(ctx, `
			INSERT INTO live_platform_bans (user_id, reason, banned_by) VALUES ($1, $2, $3)
			ON CONFLICT (user_id) DO UPDATE SET reason = EXCLUDED.reason,
			    banned_by = EXCLUDED.banned_by, banned_at = NOW()`, userID, reason, audit.ActorID)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM live_platform_bans WHERE user_id = $1`, userID)
	}
	if err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, audit); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ActiveStreamsOf returns the user's streams that are not ended/failed.
func (s *Store) ActiveStreamsOf(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id FROM live_streams
		WHERE creator_user_id = $1 AND status IN ('starting','live','reconnecting')`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// --- admin chat removal ---

// AdminRemoveChatMessage removes a message and writes the audit row in one
// transaction.
func (s *Store) AdminRemoveChatMessage(ctx context.Context, streamID, messageID uuid.UUID, audit AuditEntry) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	removed, err := removeChatMessage(ctx, tx, streamID, messageID, audit.ActorID)
	if err != nil {
		return false, err
	}
	if err := insertAudit(ctx, tx, audit); err != nil {
		return false, err
	}
	return removed, tx.Commit(ctx)
}

// --- reports ---

// NewReport is the input to CreateReport.
type NewReport struct {
	StreamID     uuid.UUID
	ReporterID   uuid.UUID
	MessageID    *uuid.UUID
	TargetUserID uuid.UUID
	Reason       string
	Note         string
}

// CreateReport files a report unless the reporter already reported this
// target (ErrDuplicate) or filed maxPerWindow reports within window
// (ErrReportRateLimited). The reporter's reports are serialised by an
// advisory lock so the count and the insert cannot race.
func (s *Store) CreateReport(ctx context.Context, r NewReport, maxPerWindow int, window time.Duration) (*Report, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('live_report:' || $1::text))`, r.ReporterID); err != nil {
		return nil, err
	}
	var recent int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM live_reports
		WHERE reporter_id = $1 AND created_at > NOW() - make_interval(secs => $2)`,
		r.ReporterID, window.Seconds()).Scan(&recent); err != nil {
		return nil, err
	}
	if recent >= maxPerWindow {
		return nil, ErrReportRateLimited
	}
	rep, err := scanReport(tx.QueryRow(ctx, `
		INSERT INTO live_reports (stream_id, reporter_id, message_id, target_user_id, reason, note)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+reportColumns, r.StreamID, r.ReporterID, r.MessageID, r.TargetUserID, r.Reason, r.Note))
	if isUniqueViolation(err) {
		return nil, ErrDuplicate
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return rep, nil
}

// ListReports lists reports by status (open | resolved | all), newest
// first, with the reported message's text.
func (s *Store) ListReports(ctx context.Context, status string, limit int) ([]*Report, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.stream_id, r.reporter_id, r.message_id, r.target_user_id, r.reason, r.note, r.status,
		       r.resolution, r.resolution_reason, r.resolved_by, r.resolved_at, r.created_at, m.text
		FROM live_reports r
		LEFT JOIN live_chat_messages m ON m.id = r.message_id AND m.stream_id = r.stream_id
		WHERE ($1 = 'all' OR r.status = $1)
		ORDER BY r.created_at DESC, r.id DESC
		LIMIT $2`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*Report, 0)
	for rows.Next() {
		var text *string
		rep, err := scanReport(rows, &text)
		if err != nil {
			return nil, err
		}
		rep.MessageText = text
		out = append(out, rep)
	}
	return out, rows.Err()
}

// ResolveAction is what an admin resolution does besides closing the report.
type ResolveAction struct {
	Action string // dismiss | remove_message | ban_user
	Reason string
}

// AdminResolveReport closes an open report, applies its action (remove the
// reported message, or ban the reported user from that stream) and writes
// the audit row, all in one transaction. check runs on the locked report
// before anything is written (the service refuses actions the report
// cannot take).
func (s *Store) AdminResolveReport(ctx context.Context, reportID uuid.UUID, act ResolveAction, check func(*Report) error, audit AuditEntry) (*Report, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rep, err := scanReport(tx.QueryRow(ctx, `SELECT `+reportColumns+` FROM live_reports WHERE id = $1 FOR UPDATE`, reportID))
	if err != nil {
		return nil, err
	}
	if rep.Status != "open" {
		return nil, ErrReportResolved
	}
	if check != nil {
		if err := check(rep); err != nil {
			return nil, err
		}
	}
	switch act.Action {
	case "remove_message":
		if rep.MessageID != nil {
			if _, err := removeChatMessage(ctx, tx, rep.StreamID, *rep.MessageID, audit.ActorID); err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
		}
	case "ban_user":
		if err := banFromStream(ctx, tx, rep.StreamID, rep.TargetUserID, audit.ActorID, act.Reason); err != nil {
			return nil, err
		}
	}
	out, err := scanReport(tx.QueryRow(ctx, `
		UPDATE live_reports SET status = 'resolved', resolution = $2, resolution_reason = $3,
		    resolved_by = $4, resolved_at = NOW()
		WHERE id = $1
		RETURNING `+reportColumns, reportID, act.Action, act.Reason, audit.ActorID))
	if err != nil {
		return nil, err
	}
	if err := insertAudit(ctx, tx, audit); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// AuditRow is one live_admin_audit row as read back (tests, console).
type AuditRow struct {
	ID         int64     `json:"id"`
	ActorID    uuid.UUID `json:"actor_id"`
	Action     string    `json:"action"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	Reason     string    `json:"reason"`
	At         time.Time `json:"at"`
}

// ListAudit returns the newest audit rows.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, actor_id, action, target_type, target_id, reason, at
		FROM live_admin_audit ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AuditRow, 0)
	for rows.Next() {
		var a AuditRow
		if err := rows.Scan(&a.ID, &a.ActorID, &a.Action, &a.TargetType, &a.TargetID, &a.Reason, &a.At); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
