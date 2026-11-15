package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Echo v2 (docs/designs/echo-v2-contract.md, migration 083): scheduled
// notes, views, bookmarks, quotes, trends, hashtag pages and search. Polls
// reuse the post poll tables (polls, poll_options, poll_votes; migration
// 045) through GetPollsByPostIDs, GetUserPollVotesBatch and InsertPollVote;
// bookmarks reuse saved_items (target_type 'post') through AddBookmark,
// RemoveBookmark and BatchIsBookmarked.
//
// Every listing here keeps content_type = 'note' and the viewer
// eligibility predicate (live, effectively approved, NOT scheduled), so a
// scheduled note is in none of them and no ordinary post is either
// (notes_v2_sql_test.go pins each builder).

// ── Scheduled notes ─────────────────────────────────────────────────────

// ScheduledNote is a note still waiting to publish (publish_at set), with
// the publisher's state (note_schedules).
type ScheduledNote struct {
	Note
	// PublishError is the last failed publish check's reason (nil: none).
	PublishError *string
	// Failed is true once the 24-hour retry window is spent.
	Failed bool
}

// scheduledNoteSQL selects the live scheduled notes; where is appended.
func scheduledNoteSQL(where string) string {
	return `SELECT ` + qualifiedCols("p", noteCols) + `, s.publish_error, (s.failed_at IS NOT NULL)
		FROM posts p
		LEFT JOIN note_schedules s ON s.note_id = p.id
		WHERE p.content_type = '` + NoteContentType + `' AND p.publish_at IS NOT NULL AND p.deleted_at IS NULL` + where
}

func scanScheduledNotes(rows pgx.Rows) ([]ScheduledNote, error) {
	var out []ScheduledNote
	for rows.Next() {
		var n ScheduledNote
		dest := append(noteScanDestinations(&n.Note), &n.PublishError, &n.Failed)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		n.Post.deriveScheduled()
		n.Post.deriveAudio()
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) attachScheduledMedia(ctx context.Context, notes []ScheduledNote) error {
	plain := make([]Note, len(notes))
	for i := range notes {
		plain[i] = notes[i].Note
	}
	if err := s.attachNoteMedia(ctx, plain); err != nil {
		return err
	}
	for i := range notes {
		notes[i].Post.Media = plain[i].Post.Media
	}
	return nil
}

// GetScheduledNote returns the live scheduled (or failed) note id, nil when
// there is none. The caller checks the author.
func (s *Store) GetScheduledNote(ctx context.Context, id uuid.UUID) (*ScheduledNote, error) {
	rows, err := s.db.Query(ctx, scheduledNoteSQL(` AND p.id = $1`), id)
	if err != nil {
		return nil, fmt.Errorf("get scheduled note: %w", err)
	}
	notes, err := scanScheduledNotes(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(notes) == 0 {
		return nil, nil
	}
	if err := s.attachScheduledMedia(ctx, notes); err != nil {
		return nil, err
	}
	return &notes[0], nil
}

// ListScheduledNotes is the author's Scheduled list, soonest first,
// keyset-paged on (publish_at, id).
func (s *Store) ListScheduledNotes(ctx context.Context, authorID uuid.UUID, cursor string, limit int) ([]ScheduledNote, string, error) {
	limit = clampNoteListLimit(limit)
	args := []any{authorID, limit + 1}
	where := ` AND p.author_id = $1`
	if at, key, ok := decodeKeysetCursor(cursor); ok {
		if id, err := uuid.Parse(key); err == nil {
			args = append(args, at, id)
			where += ` AND (p.publish_at, p.id) > ($3, $4)`
		}
	}
	rows, err := s.db.Query(ctx, scheduledNoteSQL(where)+` ORDER BY p.publish_at ASC, p.id ASC LIMIT $2`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list scheduled notes: %w", err)
	}
	notes, err := scanScheduledNotes(rows)
	rows.Close()
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(notes) > limit {
		last := notes[limit-1]
		next = encodeKeysetCursor(*last.Post.PublishAt, last.Post.ID.String())
		notes = notes[:limit]
	}
	if err := s.attachScheduledMedia(ctx, notes); err != nil {
		return nil, "", err
	}
	return notes, next, nil
}

// RescheduleNote moves the author's scheduled note to at, keeps its poll's
// duration (ends_at moves with it) and clears the publisher's state, so a
// failed note is armed again. false: no such scheduled note of authorID's.
func (s *Store) RescheduleNote(ctx context.Context, id, authorID uuid.UUID, at time.Time) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var old time.Time
	err = tx.QueryRow(ctx, `SELECT publish_at FROM posts
		WHERE id = $1 AND author_id = $2 AND content_type = '`+NoteContentType+`'
		  AND publish_at IS NOT NULL AND deleted_at IS NULL
		FOR UPDATE`, id, authorID).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reschedule note: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET publish_at = $2, updated_at = NOW() WHERE id = $1`, id, at); err != nil {
		return false, fmt.Errorf("reschedule note: %w", err)
	}
	if _, err := tx.Exec(ctx, shiftPollEndsSQL,
		id, at, old); err != nil {
		return false, fmt.Errorf("reschedule note poll: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM note_schedules WHERE note_id = $1`, id); err != nil {
		return false, fmt.Errorf("reschedule note state: %w", err)
	}
	return true, tx.Commit(ctx)
}

// claimDueNotesSQL claims up to $3 due scheduled notes: publish_at at or
// before $1, not failed, and not leased (next_attempt_at, when set, at or
// before $1). FOR UPDATE SKIP LOCKED makes two replicas claiming at once
// take disjoint rows; each claimed note is leased until $2, so no other
// replica takes it while this one works on it. The publish itself is a
// compare-and-set on publish_at (PublishScheduledNote), so even a lease
// that runs out mid-publish cannot publish a note twice.
const claimDueNotesSQL = `
	WITH due AS (
		SELECT p.id FROM posts p
		LEFT JOIN note_schedules s ON s.note_id = p.id
		WHERE p.content_type = '` + NoteContentType + `'
		  AND p.publish_at IS NOT NULL AND p.publish_at <= $1 AND p.deleted_at IS NULL
		  AND (s.note_id IS NULL OR (s.failed_at IS NULL AND (s.next_attempt_at IS NULL OR s.next_attempt_at <= $1)))
		ORDER BY p.publish_at ASC
		LIMIT $3
		FOR UPDATE OF p SKIP LOCKED
	)
	INSERT INTO note_schedules (note_id, next_attempt_at)
	SELECT id, $2 FROM due
	ON CONFLICT (note_id) DO UPDATE SET next_attempt_at = EXCLUDED.next_attempt_at
	RETURNING note_id`

// ClaimDueNotes answers claimDueNotesSQL.
func (s *Store) ClaimDueNotes(ctx context.Context, now, leaseUntil time.Time, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, claimDueNotesSQL, now, leaseUntil, limit)
	if err != nil {
		return nil, fmt.Errorf("claim due notes: %w", err)
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

// NotePublish is one scheduled note going live.
type NotePublish struct {
	ID  uuid.UUID
	Now time.Time
	// QuoteRepost, when set, is the quote's repost row, written now (a
	// scheduled quote holds no repost row while it waits).
	QuoteRepost *Repost
	// Events builds the outbox events given the row's new search_rev.
	Events func(rev int64) []ThreadOutboxEvent
}

// NotePublished reports a publish that committed.
type NotePublished struct {
	SearchRev          int64
	QuoteRepostWritten bool
}

// PublishScheduledNote makes one due scheduled note live in ONE
// transaction: the guarded flip (publish_at cleared, created_at and
// published_at moved to Now, search_rev bumped), the poll's ends_at moved
// by the same amount so it runs its full duration from publication, the
// quote's repost row, the outbox events and the publisher's state row
// removed. nil, nil: the note was not a due scheduled note any more
// (published by another replica, deleted, or rescheduled later).
func (s *Store) PublishScheduledNote(ctx context.Context, w *NotePublish) (*NotePublished, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var old time.Time
	err = tx.QueryRow(ctx, `SELECT publish_at FROM posts
		WHERE id = $1 AND content_type = '`+NoteContentType+`'
		  AND publish_at IS NOT NULL AND publish_at <= $2 AND deleted_at IS NULL
		FOR UPDATE`, w.ID, w.Now).Scan(&old)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("publish note: lock: %w", err)
	}
	out := &NotePublished{}
	if err := tx.QueryRow(ctx, `
		UPDATE posts SET publish_at = NULL, published_at = $2, created_at = $2, updated_at = $2,
			search_rev = search_rev + 1
		WHERE id = $1
		RETURNING search_rev`, w.ID, w.Now).Scan(&out.SearchRev); err != nil {
		return nil, fmt.Errorf("publish note: flip: %w", err)
	}
	if _, err := tx.Exec(ctx, shiftPollEndsSQL,
		w.ID, w.Now, old); err != nil {
		return nil, fmt.Errorf("publish note: poll: %w", err)
	}
	if r := w.QuoteRepost; r != nil {
		r.ID = uuid.New()
		r.Status = "active"
		r.CreatedAt, r.UpdatedAt = w.Now, w.Now
		var qt *string
		if r.QuoteText != "" {
			qt = &r.QuoteText
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO post_reposts (id, user_id, original_post_id, repost_type, quote_text,
				visibility, status, created_at, updated_at)
			VALUES ($1, $2, $3, 'quote', $4, $5, 'active', $6, $6)
			ON CONFLICT DO NOTHING
			RETURNING id`, r.ID, r.UserID, r.OriginalPostID, qt, r.Visibility, w.Now).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, fmt.Errorf("publish note: quote repost: %w", err)
		default:
			r.RepostType = "quote"
			out.QuoteRepostWritten = true
		}
	}
	if w.Events != nil {
		for _, evt := range w.Events(out.SearchRev) {
			if err := InsertOutboxEventTx(ctx, tx, evt.EventType, "post", evt.PostID, evt.Payload); err != nil {
				return nil, err
			}
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM note_schedules WHERE note_id = $1`, w.ID); err != nil {
		return nil, fmt.Errorf("publish note: state: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// recordNotePublishFailureSQL: $1 note, $2 reason, $3 now, $4 next
// attempt, $5 give-up cutoff (now - 24 h). The window starts at the first
// failure; a failure at or past it marks the note failed.
const recordNotePublishFailureSQL = `
	INSERT INTO note_schedules (note_id, publish_error, attempts, first_failed_at, next_attempt_at)
	VALUES ($1, $2, 1, $3, $4)
	ON CONFLICT (note_id) DO UPDATE SET
		publish_error   = EXCLUDED.publish_error,
		attempts        = note_schedules.attempts + 1,
		first_failed_at = COALESCE(note_schedules.first_failed_at, EXCLUDED.first_failed_at),
		next_attempt_at = EXCLUDED.next_attempt_at,
		failed_at       = CASE WHEN COALESCE(note_schedules.first_failed_at, EXCLUDED.first_failed_at) <= $5
		                       THEN $3 ELSE NULL END
	RETURNING failed_at IS NOT NULL`

// RecordNotePublishFailure keeps the note scheduled with reason, retried at
// retryAt, or marks it failed once its first failure is at or before
// giveUpBefore. Reports whether it is now failed.
func (s *Store) RecordNotePublishFailure(ctx context.Context, id uuid.UUID, reason string, now, retryAt, giveUpBefore time.Time) (bool, error) {
	var failed bool
	if err := s.db.QueryRow(ctx, recordNotePublishFailureSQL, id, reason, now, retryAt, giveUpBefore).Scan(&failed); err != nil {
		return false, fmt.Errorf("record note publish failure: %w", err)
	}
	return failed, nil
}

// ── Counts, views, bookmarks ────────────────────────────────────────────

// NoteCounts are a note's counts read from Postgres.
type NoteCounts struct {
	Quotes    int64
	Bookmarks int64
	// ThreadParts is the live parts of the thread this note roots (the
	// root included); 0 for a note that roots no thread.
	ThreadParts int64
}

// noteCountsSQL: per id, live published quotes (a scheduled quote is not
// counted until it publishes), saves, and the live parts of the thread it
// roots (idx_posts_thread).
var noteCountsSQL = `
	SELECT x.id,
		(SELECT COUNT(*) FROM posts q
		 WHERE q.quote_of_id = x.id AND q.content_type = '` + NoteContentType + `' AND ` + ViewerEligibleSQL("q") + `),
		(SELECT COUNT(*) FROM saved_items si WHERE si.target_type = 'post' AND si.target_id = x.id),
		(SELECT COUNT(*) FROM posts t
		 WHERE t.thread_root_id = x.id AND t.content_type = '` + NoteContentType + `' AND t.deleted_at IS NULL AND t.publish_at IS NULL)
	FROM unnest($1::uuid[]) AS x(id)`

// NoteCountsFor answers noteCountsSQL.
func (s *Store) NoteCountsFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]NoteCounts, error) {
	out := make(map[uuid.UUID]NoteCounts, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, noteCountsSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("note counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var c NoteCounts
		if err := rows.Scan(&id, &c.Quotes, &c.Bookmarks, &c.ThreadParts); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// recordNoteViewsSQL counts viewer $2's views of notes $1 at $3: only live
// published notes, never the author's own, and at most once per note per
// rolling 24 hours (the dedupe row's counted_at moves only when it is that
// old). The counter moves for exactly the rows counted.
var recordNoteViewsSQL = `
	WITH eligible AS (
		SELECT p.id FROM posts p
		WHERE p.id = ANY($1) AND p.content_type = '` + NoteContentType + `'
		  AND p.author_id <> $2 AND ` + ViewerEligibleSQL("p") + `
	), counted AS (
		INSERT INTO note_views (note_id, viewer_id, counted_at)
		SELECT id, $2, $3 FROM eligible
		ON CONFLICT (note_id, viewer_id) DO UPDATE SET counted_at = EXCLUDED.counted_at
		WHERE note_views.counted_at <= EXCLUDED.counted_at - INTERVAL '24 hours'
		RETURNING note_id
	)
	UPDATE posts SET note_view_count = note_view_count + 1
	WHERE id IN (SELECT note_id FROM counted)`

// RecordNoteViews answers recordNoteViewsSQL; it reports how many counted.
func (s *Store) RecordNoteViews(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID, now time.Time) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tag, err := s.db.Exec(ctx, recordNoteViewsSQL, ids, viewerID, now)
	if err != nil {
		return 0, fmt.Errorf("record note views: %w", err)
	}
	return tag.RowsAffected(), nil
}

// noteBookmarksSQL is the viewer's ($2) bookmarked notes, newest save
// first. Bookmarks are private: the list is keyed on the viewer alone.
func noteBookmarksSQL(cursor string) (string, []any) {
	query := `SELECT ` + qualifiedCols("p", noteCols) + `, si.created_at
		FROM saved_items si
		JOIN posts p ON p.id = si.target_id
		WHERE si.user_id = $2 AND si.target_type = 'post'
		  AND p.content_type = '` + NoteContentType + `' AND ` + ViewerEligibleSQL("p")
	var extra []any
	if at, key, ok := decodeKeysetCursor(cursor); ok {
		if id, err := uuid.Parse(key); err == nil {
			extra = append(extra, at, id)
			query += ` AND (si.created_at, p.id) < ($3, $4)`
		}
	}
	return query + ` ORDER BY si.created_at DESC, p.id DESC LIMIT $1`, extra
}

// ListNoteBookmarks answers noteBookmarksSQL.
func (s *Store) ListNoteBookmarks(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]Note, string, error) {
	limit = clampNoteListLimit(limit)
	query, extra := noteBookmarksSQL(cursor)
	return s.noteKeysetPage(ctx, query, append([]any{limit + 1, userID}, extra...), limit)
}

// ── Discovery: quotes, hashtag pages, search, trends ────────────────────

// NoteDiscoverQuery is one page of a note discovery listing. Exactly one of
// QuoteOfID, Hashtag and Search is set.
type NoteDiscoverQuery struct {
	QuoteOfID *uuid.UUID
	Hashtag   string
	Search    string
	// Top ranks by engagement (offset-paged); otherwise newest first.
	Top    bool
	Limit  int
	Cursor string
}

// noteTopMaxOffset bounds how deep a top-ranked page may go.
const noteTopMaxOffset = 1000

// noteTopScore is a note's engagement score for the Top tabs.
const noteTopScore = `(COALESCE(c.like_count, 0) + 2 * p.reply_count + 2 * COALESCE(c.repost_count, 0))`

func clampNoteListLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 50 {
		return 50
	}
	return limit
}

// noteDiscoverSQL builds a discovery query: pure, so the filters are
// pinned without a database. Every branch keeps public, published notes.
func noteDiscoverSQL(q NoteDiscoverQuery) (string, []any) {
	args := []any{q.Limit + 1}
	where := `p.content_type = '` + NoteContentType + `' AND p.visibility = 'public' AND ` + ViewerEligibleSQL("p")
	switch {
	case q.QuoteOfID != nil:
		args = append(args, *q.QuoteOfID)
		where += fmt.Sprintf(` AND p.quote_of_id = $%d`, len(args))
	case q.Hashtag != "":
		args = append(args, []string{strings.ToLower(q.Hashtag)})
		where += fmt.Sprintf(` AND p.hashtags @> $%d::text[]`, len(args))
	default:
		args = append(args, q.Search)
		where += fmt.Sprintf(` AND to_tsvector('simple'::regconfig, p.text) @@ websearch_to_tsquery('simple'::regconfig, $%d)`, len(args))
	}
	cols := qualifiedCols("p", noteCols)
	if q.Top {
		offset := decodeNoteOffset(q.Cursor)
		args = append(args, offset)
		return `SELECT ` + cols + `, p.created_at
			FROM posts p
			LEFT JOIN post_engagement_counts c ON c.post_id = p.id
			WHERE ` + where + `
			ORDER BY ` + noteTopScore + ` DESC, p.created_at DESC, p.id DESC
			LIMIT $1 OFFSET $` + strconv.Itoa(len(args)), args
	}
	if at, key, ok := decodeKeysetCursor(q.Cursor); ok {
		if id, err := uuid.Parse(key); err == nil {
			args = append(args, at, id)
			where += fmt.Sprintf(` AND (p.created_at, p.id) < ($%d, $%d)`, len(args)-1, len(args))
		}
	}
	return `SELECT ` + cols + `, p.created_at
		FROM posts p
		WHERE ` + where + `
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $1`, args
}

func decodeNoteOffset(cursor string) int {
	if !strings.HasPrefix(cursor, "o") {
		return 0
	}
	n, err := strconv.Atoi(cursor[1:])
	if err != nil || n < 0 {
		return 0
	}
	if n > noteTopMaxOffset {
		return noteTopMaxOffset
	}
	return n
}

// EncodeNoteOffset is a Top tab's cursor.
func EncodeNoteOffset(n int) string { return "o" + strconv.Itoa(n) }

// DiscoverNotes answers one page of q.
func (s *Store) DiscoverNotes(ctx context.Context, q NoteDiscoverQuery) ([]Note, string, error) {
	if q.QuoteOfID == nil && q.Hashtag == "" && strings.TrimSpace(q.Search) == "" {
		return nil, "", errors.New("discover notes: no filter")
	}
	q.Limit = clampNoteListLimit(q.Limit)
	query, args := noteDiscoverSQL(q)
	if !q.Top {
		return s.noteKeysetPage(ctx, query, args, q.Limit)
	}
	notes, _, err := s.scanNotePage(ctx, query, args)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(notes) > q.Limit {
		notes = notes[:q.Limit]
		if off := decodeNoteOffset(q.Cursor) + q.Limit; off < noteTopMaxOffset {
			next = EncodeNoteOffset(off)
		}
	}
	return notes, next, s.attachNoteMedia(ctx, notes)
}

// scanNotePage runs a note listing whose last column is its sort key.
func (s *Store) scanNotePage(ctx context.Context, query string, args []any) ([]Note, []time.Time, error) {
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("list notes: %w", err)
	}
	defer rows.Close()
	var notes []Note
	var keys []time.Time
	for rows.Next() {
		var n Note
		var key time.Time
		dest := append(noteScanDestinations(&n), &key)
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, fmt.Errorf("scan notes: %w", err)
		}
		n.Post.deriveScheduled()
		n.Post.deriveAudio()
		notes = append(notes, n)
		keys = append(keys, key)
	}
	return notes, keys, rows.Err()
}

// noteKeysetPage is scanNotePage cut to limit with a keyset cursor.
func (s *Store) noteKeysetPage(ctx context.Context, query string, args []any, limit int) ([]Note, string, error) {
	notes, keys, err := s.scanNotePage(ctx, query, args)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(notes) > limit {
		next = encodeKeysetCursor(keys[limit-1], notes[limit-1].Post.ID.String())
		notes = notes[:limit]
	}
	return notes, next, s.attachNoteMedia(ctx, notes)
}

// NoteTrend is one row of GET /v1/notes/trends.
type NoteTrend struct {
	Tag       string
	NoteCount int64
	Authors   int64
}

// noteTrendsSQL ranks the hashtags of public, published, top-level notes
// created at or after $1 by distinct authors, then notes; a tag needs at
// least $2 distinct authors. Notes only: ordinary posts never mix in.
var noteTrendsSQL = `
	SELECT lower(t.tag) AS tag, COUNT(*) AS note_count, COUNT(DISTINCT p.author_id) AS authors
	FROM posts p
	CROSS JOIN LATERAL unnest(p.hashtags) AS t(tag)
	WHERE p.content_type = '` + NoteContentType + `'
	  AND p.reply_to_id IS NULL
	  AND p.visibility = 'public'
	  AND ` + ViewerEligibleSQL("p") + `
	  AND p.created_at >= $1
	GROUP BY lower(t.tag)
	HAVING COUNT(DISTINCT p.author_id) >= $2
	ORDER BY authors DESC, note_count DESC, tag ASC
	LIMIT $3`

// NoteTrends answers noteTrendsSQL.
func (s *Store) NoteTrends(ctx context.Context, since time.Time, minAuthors, limit int) ([]NoteTrend, error) {
	rows, err := s.db.Query(ctx, noteTrendsSQL, since, minAuthors, limit)
	if err != nil {
		return nil, fmt.Errorf("note trends: %w", err)
	}
	defer rows.Close()
	out := []NoteTrend{}
	for rows.Next() {
		var t NoteTrend
		if err := rows.Scan(&t.Tag, &t.NoteCount, &t.Authors); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// IsNote reports whether id is a note row (any state). The generic poll
// routes (/v1/posts/:id/poll*) refuse notes with it: a note's poll is read
// and voted only through /v1/notes, which hide counts and refuse the
// author's own vote.
func (s *Store) IsNote(ctx context.Context, id uuid.UUID) (bool, error) {
	var ok bool
	err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM posts WHERE id = $1 AND content_type = '`+NoteContentType+`')`, id).Scan(&ok)
	return ok, err
}

// shiftPollEndsSQL moves a note's poll deadline by as much as the note moved
// ($2 new time, $3 old time). Both are typed: untyped, Postgres reads $2 as
// an interval and the update fails with SQLSTATE 42804 (CI, 15 Nov 2026).
const shiftPollEndsSQL = `UPDATE polls SET ends_at = $2::timestamptz + (ends_at - $3::timestamptz)
	WHERE post_id = $1 AND ends_at IS NOT NULL`
