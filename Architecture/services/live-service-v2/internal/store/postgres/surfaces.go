package postgres

// Live surfaces (2 Oct 2026, migration 005): discovery filters, editing a
// scheduled stream, reminders, free hearts and top supporters, and the
// founding creator badge.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// UserCard is a person as the live surfaces show them: the host of a stream
// (`creator`) or a supporter (`user`). Only user_id is always present; the
// rest comes from the profile lookup and is left off when it did not answer.
type UserCard struct {
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name,omitempty"`
	Handle    string    `json:"handle,omitempty"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	// Badges are the creator badges the user holds ("founding_creator");
	// omitted when none, and while the user is platform live-banned.
	Badges []string `json:"badges,omitempty"`
}

// ErrNotOnAir: the stream is not live or reconnecting (hearts).
var ErrNotOnAir = errors.New("stream is not on air")

// StreamFilter narrows a discovery listing. The zero value filters nothing.
type StreamFilter struct {
	// Orientation / Category: "" = any.
	Orientation string
	Category    string
	// CreatorIDs, when non-nil, keeps only streams by these creators. A
	// non-nil EMPTY list matches nothing (a viewer who follows nobody).
	CreatorIDs []uuid.UUID
}

// where appends the filter's conditions to conds/args.
func (f StreamFilter) where(conds []string, args []any) ([]string, []any) {
	if f.Orientation != "" {
		args = append(args, f.Orientation)
		conds = append(conds, fmt.Sprintf("orientation = $%d", len(args)))
	}
	if f.Category != "" {
		args = append(args, f.Category)
		conds = append(conds, fmt.Sprintf("category = $%d", len(args)))
	}
	if f.CreatorIDs != nil {
		args = append(args, f.CreatorIDs)
		conds = append(conds, fmt.Sprintf("creator_user_id = ANY($%d)", len(args)))
	}
	return conds, args
}

// Matches is the same filter over a row in memory (the in-memory store).
func (f StreamFilter) Matches(st *LiveStream) bool {
	if f.Orientation != "" && st.Orientation != f.Orientation {
		return false
	}
	if f.Category != "" && st.Category != f.Category {
		return false
	}
	if f.CreatorIDs != nil {
		for _, id := range f.CreatorIDs {
			if id == st.CreatorUserID {
				return true
			}
		}
		return false
	}
	return true
}

// Live list orders.
const (
	SortRecent  = "recent"  // started_at DESC, id DESC (the original order)
	SortViewers = "viewers" // viewer_count DESC, started_at DESC, id DESC
)

func (s *Store) queryStreams(ctx context.Context, q string, args ...any) ([]*LiveStream, error) {
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*LiveStream, 0)
	for rows.Next() {
		st, err := scanStream(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// UntimedSortKey stands in for a missing scheduled_at where unstarted
// streams are ordered and paged: after every real time, so the timeless ones
// come last, and a real value, so the keyset can resume among them.
var UntimedSortKey = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)

// ScheduledSortKey is the time an unstarted stream is ordered by.
func ScheduledSortKey(st *LiveStream) time.Time {
	if st.ScheduledAt == nil {
		return UntimedSortKey
	}
	return *st.ScheduledAt
}

// listUnstarted is ListScheduled with Unstarted set: every 'scheduled'
// stream, ordered by (scheduled_at or UntimedSortKey, id).
func (s *Store) listUnstarted(ctx context.Context, p ListScheduledParams, limit int) ([]*LiveStream, error) {
	const key = "COALESCE(scheduled_at, $1::timestamptz)"
	conds := []string{"status = 'scheduled'"}
	args := []any{UntimedSortKey}
	conds, args = p.Filter.where(conds, args)
	if p.ScheduledAfter != nil && p.IDAfter != nil {
		args = append(args, *p.ScheduledAfter, *p.IDAfter)
		n := len(args)
		conds = append(conds, fmt.Sprintf("(%s, id) > ($%d, $%d)", key, n-1, n))
	}
	args = append(args, limit)
	q := `SELECT ` + selectColumns + ` FROM live_streams WHERE ` + strings.Join(conds, " AND ") +
		fmt.Sprintf(" ORDER BY %s ASC, id ASC LIMIT $%d", key, len(args))
	return s.queryStreams(ctx, q, args...)
}

// ListPastParams pages a creator's past streams.
type ListPastParams struct {
	Limit       int
	EndedBefore *time.Time
	IDBefore    *uuid.UUID
	Filter      StreamFilter
}

// ListPast returns ended streams that were live (started_at set), newest
// end first. A stream that failed, or ended without ever going live, is not
// a past stream.
func (s *Store) ListPast(ctx context.Context, p ListPastParams) ([]*LiveStream, error) {
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	conds := []string{"status = 'ended'", "started_at IS NOT NULL", "ended_at IS NOT NULL"}
	var args []any
	conds, args = p.Filter.where(conds, args)
	if p.EndedBefore != nil && p.IDBefore != nil {
		args = append(args, *p.EndedBefore, *p.IDBefore)
		n := len(args)
		conds = append(conds, fmt.Sprintf("(ended_at, id) < ($%d, $%d)", n-1, n))
	}
	args = append(args, limit)
	q := `SELECT ` + selectColumns + ` FROM live_streams WHERE ` + strings.Join(conds, " AND ") +
		fmt.Sprintf(" ORDER BY ended_at DESC, id DESC LIMIT $%d", len(args))
	return s.queryStreams(ctx, q, args...)
}

// StreamPatch is an edit of a scheduled stream. A nil field is left alone.
// CoverMediaID and ScheduledAt can be cleared, so each has a Set flag: the
// value is written (NULL included) only when its flag is true.
type StreamPatch struct {
	Title       *string
	Description *string
	Category    *string
	Visibility  *string
	Orientation *string

	SetCoverMediaID bool
	CoverMediaID    *uuid.UUID
	SetScheduledAt  bool
	ScheduledAt     *time.Time
}

// Apply writes the patch onto a row in memory (the in-memory store).
func (p StreamPatch) Apply(st *LiveStream) {
	if p.Title != nil {
		st.Title = *p.Title
	}
	if p.Description != nil {
		st.Description = *p.Description
	}
	if p.Category != nil {
		st.Category = *p.Category
	}
	if p.Visibility != nil {
		st.Visibility = *p.Visibility
	}
	if p.Orientation != nil {
		st.Orientation = *p.Orientation
	}
	if p.SetCoverMediaID {
		st.CoverMediaID = p.CoverMediaID
	}
	if p.SetScheduledAt {
		st.ScheduledAt = p.ScheduledAt
	}
}

// UpdateScheduled edits a stream that is still 'scheduled'. The status is
// part of the UPDATE's WHERE, so a stream that started meanwhile is refused
// (ErrStateConflict), never edited. ErrNotFound when there is no such stream.
func (s *Store) UpdateScheduled(ctx context.Context, id uuid.UUID, p StreamPatch) (*LiveStream, error) {
	st, err := scanStream(s.db.QueryRow(ctx, `
		UPDATE live_streams SET
		    title          = COALESCE($2::text, title),
		    description    = COALESCE($3::text, description),
		    category       = COALESCE($4::text, category),
		    visibility     = COALESCE($5::text, visibility),
		    orientation    = COALESCE($6::text, orientation),
		    cover_media_id = CASE WHEN $7::boolean THEN $8::uuid ELSE cover_media_id END,
		    scheduled_at   = CASE WHEN $9::boolean THEN $10::timestamptz ELSE scheduled_at END,
		    updated_at     = NOW()
		WHERE id = $1 AND status = 'scheduled'
		RETURNING `+selectColumns,
		id, p.Title, p.Description, p.Category, p.Visibility, p.Orientation,
		p.SetCoverMediaID, p.CoverMediaID, p.SetScheduledAt, p.ScheduledAt))
	if errors.Is(err, ErrNotFound) {
		var exists bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM live_streams WHERE id = $1)`, id).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			return nil, ErrStateConflict
		}
		return nil, ErrNotFound
	}
	return st, err
}

// --- reminders ---

// ReminderStat is how many viewers asked to be reminded of a stream and
// whether the asking viewer is one of them.
type ReminderStat struct {
	Count int
	Set   bool
}

// SetReminder adds (set=true) or removes the viewer's reminder on a stream
// that is still 'scheduled' and returns the stream's reminder count. Both
// are idempotent. The stream row is locked for the check, so a reminder
// cannot be added to a stream that has started. ErrNotFound: no such
// stream; ErrStateConflict: it is not scheduled.
func (s *Store) SetReminder(ctx context.Context, streamID, userID uuid.UUID, set bool) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM live_streams WHERE id = $1 FOR SHARE`, streamID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if status != StatusScheduled {
		return 0, ErrStateConflict
	}
	if set {
		_, err = tx.Exec(ctx, `
			INSERT INTO live_stream_reminders (stream_id, user_id) VALUES ($1, $2)
			ON CONFLICT (stream_id, user_id) DO NOTHING`, streamID, userID)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM live_stream_reminders WHERE stream_id = $1 AND user_id = $2`, streamID, userID)
	}
	if err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*)::int FROM live_stream_reminders WHERE stream_id = $1`, streamID).Scan(&n); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return n, nil
}

// ReminderStats returns, for each of streamIDs that has a reminder, its
// count and whether viewerID (uuid.Nil = nobody) set one. A stream without
// reminders is absent from the map.
func (s *Store) ReminderStats(ctx context.Context, streamIDs []uuid.UUID, viewerID uuid.UUID) (map[uuid.UUID]ReminderStat, error) {
	out := map[uuid.UUID]ReminderStat{}
	if len(streamIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT stream_id, COUNT(*)::int, COALESCE(BOOL_OR(user_id = $2), FALSE)
		FROM live_stream_reminders
		WHERE stream_id = ANY($1)
		GROUP BY stream_id`, streamIDs, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var st ReminderStat
		if err := rows.Scan(&id, &st.Count, &st.Set); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// ListReminderUserIDs pages the viewers to remind, ordered by user id,
// strictly after `after` (uuid.Nil = from the start).
func (s *Store) ListReminderUserIDs(ctx context.Context, streamID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT user_id FROM live_stream_reminders
		WHERE stream_id = $1 AND user_id > $2
		ORDER BY user_id
		LIMIT $3`, streamID, after, limit)
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

// --- hearts ---

// HeartResult is what a batch of hearts did: Added is how many counted (0
// once the sender is at the per-stream cap), Total the stream's heart_count
// afterwards.
type HeartResult struct {
	Added int
	Total int64
}

// AddHearts counts n hearts from userID on a stream that is live or
// reconnecting, in one transaction: the sender's row grows by at most what
// is left under perUserCap, and live_streams.heart_count by exactly that
// much. The stream row is locked first, so two batches from the same sender
// cannot both read the same "hearts so far". Hearts past the cap are
// accepted and ignored (Added 0, no error).
func (s *Store) AddHearts(ctx context.Context, streamID, userID uuid.UUID, n, perUserCap int) (HeartResult, error) {
	var res HeartResult
	if n <= 0 {
		return res, errors.New("hearts: n must be positive")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	err = tx.QueryRow(ctx, `SELECT status, heart_count FROM live_streams WHERE id = $1 FOR UPDATE`, streamID).Scan(&status, &res.Total)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, ErrNotFound
	}
	if err != nil {
		return res, err
	}
	if status != StatusLive && status != StatusReconnecting {
		return res, ErrNotOnAir
	}
	var have int
	err = tx.QueryRow(ctx, `SELECT hearts FROM live_stream_hearts WHERE stream_id = $1 AND user_id = $2`, streamID, userID).Scan(&have)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return res, err
	}
	res.Added = HeartsUnderCap(have, n, perUserCap)
	if res.Added > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO live_stream_hearts (stream_id, user_id, hearts) VALUES ($1, $2, $3)
			ON CONFLICT (stream_id, user_id) DO UPDATE
			SET hearts = live_stream_hearts.hearts + EXCLUDED.hearts, updated_at = NOW()`,
			streamID, userID, res.Added); err != nil {
			return res, err
		}
		if err := tx.QueryRow(ctx,
			`UPDATE live_streams SET heart_count = heart_count + $2 WHERE id = $1 RETURNING heart_count`,
			streamID, res.Added).Scan(&res.Total); err != nil {
			return res, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return res, err
	}
	return res, nil
}

// HeartsUnderCap is how many of n hearts still count for a sender who has
// `have` on the stream: never more than what is left under perUserCap.
func HeartsUnderCap(have, n, perUserCap int) int {
	left := perUserCap - have
	if left <= 0 {
		return 0
	}
	if n > left {
		return left
	}
	return n
}

// Supporter is one row of a stream's top supporters.
type Supporter struct {
	UserID   uuid.UUID
	Hearts   int
	Messages int
	// FirstAt is the viewer's earliest activity on the stream: their first
	// heart or their first chat message that was not removed.
	FirstAt time.Time
}

// ListSupporters ranks a stream's viewers by hearts, then by chat messages
// that were not removed, then by who was active first. The host, viewers
// banned from the stream and viewers under a platform live ban are left
// out, and so is anyone with neither a heart nor a message.
func (s *Store) ListSupporters(ctx context.Context, streamID uuid.UUID, limit int) ([]Supporter, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.db.Query(ctx, `
		WITH h AS (
		    SELECT user_id, hearts, created_at AS first_at
		    FROM live_stream_hearts
		    WHERE stream_id = $1 AND hearts > 0
		), m AS (
		    SELECT user_id, COUNT(*)::int AS messages, MIN(created_at) AS first_at
		    FROM live_chat_messages
		    WHERE stream_id = $1 AND removed_at IS NULL
		    GROUP BY user_id
		), a AS (
		    SELECT COALESCE(h.user_id, m.user_id) AS user_id,
		           COALESCE(h.hearts, 0)   AS hearts,
		           COALESCE(m.messages, 0) AS messages,
		           LEAST(h.first_at, m.first_at) AS first_at
		    FROM h FULL OUTER JOIN m ON m.user_id = h.user_id
		)
		SELECT a.user_id, a.hearts, a.messages, a.first_at
		FROM a
		WHERE a.user_id <> (SELECT creator_user_id FROM live_streams WHERE id = $1)
		  AND NOT EXISTS (SELECT 1 FROM live_stream_bans b WHERE b.stream_id = $1 AND b.user_id = a.user_id)
		  AND NOT EXISTS (SELECT 1 FROM live_platform_bans p WHERE p.user_id = a.user_id)
		ORDER BY a.hearts DESC, a.messages DESC, a.first_at ASC, a.user_id ASC
		LIMIT $2`, streamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Supporter, 0)
	for rows.Next() {
		var sp Supporter
		if err := rows.Scan(&sp.UserID, &sp.Hearts, &sp.Messages, &sp.FirstAt); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// --- creator badges ---

// BadgeFoundingCreator is the badge of creators who streamed early.
const BadgeFoundingCreator = "founding_creator"

// DefaultFoundingMinLive is how long a stream must have been on air.
const DefaultFoundingMinLive = 5 * time.Minute

// FoundingRule decides which ended stream earns its creator the founding
// creator badge.
type FoundingRule struct {
	// MinLive is the least time on air, started_at to ended_at. Zero or
	// negative means DefaultFoundingMinLive.
	MinLive time.Duration
	// Until closes the founding window: a stream qualifies only when its
	// started_at is BEFORE it. nil = the window is open.
	Until *time.Time
}

func (r FoundingRule) minLive() time.Duration {
	if r.MinLive <= 0 {
		return DefaultFoundingMinLive
	}
	return r.MinLive
}

// Qualifies reports whether st — a stream as it is right after a status
// change — earns the badge: it ended (not failed), it was on air, for at
// least MinLive, and it started inside the founding window.
func (r FoundingRule) Qualifies(st *LiveStream) bool {
	if st == nil || st.Status != StatusEnded || st.StartedAt == nil || st.EndedAt == nil {
		return false
	}
	if r.Until != nil && !st.StartedAt.Before(*r.Until) {
		return false
	}
	return st.EndedAt.Sub(*st.StartedAt) >= r.minLive()
}

// SetFoundingRule sets the rule ApplyTransition grants the badge by. Call it
// once at start-up, before the store is used.
func (s *Store) SetFoundingRule(r FoundingRule) { s.founding = r }

func (s *Store) foundingRule() FoundingRule { return s.founding }

// grantFoundingBadge gives st's creator the badge, dated at the stream's
// end. The primary key makes it once per creator for ever: a creator who
// already has it — or had it revoked — is left as they are.
func grantFoundingBadge(ctx context.Context, q querier, st *LiveStream) error {
	_, err := q.Exec(ctx, `
		INSERT INTO live_creator_badges (user_id, badge, granted_at, stream_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, badge) DO NOTHING`,
		st.CreatorUserID, BadgeFoundingCreator, *st.EndedAt, st.ID)
	return err
}

// grantFoundingBadgeForUser grants from the user's earliest ended stream
// that qualifies under rule (streams ended by SQL rather than through
// ApplyTransition: the account-hide path).
func grantFoundingBadgeForUser(ctx context.Context, q querier, userID uuid.UUID, rule FoundingRule) error {
	_, err := q.Exec(ctx, `
		INSERT INTO live_creator_badges (user_id, badge, granted_at, stream_id)
		SELECT creator_user_id, $2, ended_at, id
		FROM live_streams
		WHERE creator_user_id = $1
		  AND status = 'ended'
		  AND started_at IS NOT NULL AND ended_at IS NOT NULL
		  AND ended_at - started_at >= make_interval(secs => $3)
		  AND ($4::timestamptz IS NULL OR started_at < $4::timestamptz)
		ORDER BY ended_at ASC, id ASC
		LIMIT 1
		ON CONFLICT (user_id, badge) DO NOTHING`,
		userID, BadgeFoundingCreator, rule.minLive().Seconds(), rule.Until)
	return err
}

// Badge is one badge a creator holds.
type Badge struct {
	Badge     string    `json:"badge"`
	GrantedAt time.Time `json:"granted_at"`
}

// visibleBadge is the rule every badge read shares: not revoked, and the
// holder is not under a platform live ban (the badge is hidden, not lost,
// while the ban stands).
const visibleBadge = `revoked_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM live_platform_bans p WHERE p.user_id = live_creator_badges.user_id)`

// BadgesFor returns the visible badges of each of userIDs; a user without
// any is absent from the map.
func (s *Store) BadgesFor(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	out := map[uuid.UUID][]string{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id, badge FROM live_creator_badges
		WHERE user_id = ANY($1) AND `+visibleBadge+`
		ORDER BY user_id, badge`, userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var b string
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		out[id] = append(out[id], b)
	}
	return out, rows.Err()
}

// ListBadges returns one user's visible badges.
func (s *Store) ListBadges(ctx context.Context, userID uuid.UUID) ([]Badge, error) {
	rows, err := s.db.Query(ctx, `
		SELECT badge, granted_at FROM live_creator_badges
		WHERE user_id = $1 AND `+visibleBadge+`
		ORDER BY badge`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Badge, 0)
	for rows.Next() {
		var b Badge
		if err := rows.Scan(&b.Badge, &b.GrantedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AdminRevokeBadge revokes a badge and writes the audit row in one
// transaction. The row is kept with revoked_at, which is what stops a later
// stream granting it again. revoked=false (and no audit row) when it was
// already revoked; ErrNotFound when the user never had it.
func (s *Store) AdminRevokeBadge(ctx context.Context, userID uuid.UUID, badge, reason string, audit AuditEntry) (revoked bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var revokedAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT revoked_at FROM live_creator_badges WHERE user_id = $1 AND badge = $2 FOR UPDATE`,
		userID, badge).Scan(&revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if revokedAt != nil {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE live_creator_badges
		SET revoked_at = NOW(), revoked_by = $3, revoked_reason = $4
		WHERE user_id = $1 AND badge = $2`, userID, badge, audit.ActorID, reason); err != nil {
		return false, err
	}
	if err := insertAudit(ctx, tx, audit); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}
