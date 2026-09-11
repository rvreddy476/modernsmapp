package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"time"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Finalise reasons, mirrored by the CHECK constraint in migration 008.
const (
	FinalizePlayEnd    = "play_end"
	FinalizeInactivity = "inactivity"
	FinalizeSuperseded = "superseded"
)

// SessionUpdate is what one playback event contributes to its
// analytics.playback_sessions row. The ingest service fills only the
// fields the event type carries; zero values are "not reported" and lose
// to whatever the row already holds under GREATEST.
type SessionUpdate struct {
	// Kind is the model event name: play_start, watch_heartbeat,
	// milestone or play_end.
	Kind        string
	CreatorID   uuid.UUID
	ContentType string
	IsSelfView  bool

	ContentDurationMS int64
	WatchedMS         int64 // clamped running total
	WatchedMSReported int64 // the client's figure, audit only
	PlayheadMS        int64 // heartbeat
	IncrementMS       int64 // heartbeat: wall-clock playback this beat covered
	PlaybackSpeed     float64
	SeekIncrement     int
	LoopCount         int
	MaxContinuousMS   int64
	PercentViewed     float64
	EndReason         string // play_end only
}

// PlaybackSession is one row of analytics.playback_sessions.
type PlaybackSession struct {
	ActorID     uuid.UUID
	SessionID   uuid.UUID
	ContentID   uuid.UUID
	CreatorID   uuid.UUID
	ContentType string

	FirstSeen         time.Time
	LastSeen          time.Time
	ContentDurationMS int64
	WatchedMS         int64
	WatchedMSReported int64
	MaxPlayheadMS     int64
	MaxContinuousMS   int64
	LoopCount         int
	SeekCount         int
	PlaybackSpeed     float64
	Coverage          []byte
	CoveredMS         int64
	PercentViewed     float64
	PercentCovered    float64
	IsSelfView        bool
	EndReason         *string
	FinalizedAt       *time.Time
	FinalizeReason    *string
	IsDisplayView     bool
	ViewScore         float64
	Source            string
}

const sessionColumns = `
	actor_id, session_id, content_id, creator_id, content_type,
	first_seen, last_seen, content_duration_ms,
	watched_ms, watched_ms_reported, max_playhead_ms, max_continuous_ms,
	loop_count, seek_count, playback_speed, coverage, covered_ms,
	percent_viewed, percent_covered, is_self_view, end_reason,
	finalized_at, finalize_reason, is_display_view, view_score, source`

func scanSession(row pgx.Row) (*PlaybackSession, error) {
	var s PlaybackSession
	err := row.Scan(
		&s.ActorID, &s.SessionID, &s.ContentID, &s.CreatorID, &s.ContentType,
		&s.FirstSeen, &s.LastSeen, &s.ContentDurationMS,
		&s.WatchedMS, &s.WatchedMSReported, &s.MaxPlayheadMS, &s.MaxContinuousMS,
		&s.LoopCount, &s.SeekCount, &s.PlaybackSpeed, &s.Coverage, &s.CoveredMS,
		&s.PercentViewed, &s.PercentCovered, &s.IsSelfView, &s.EndReason,
		&s.FinalizedAt, &s.FinalizeReason, &s.IsDisplayView, &s.ViewScore, &s.Source,
	)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetPlaybackSession reads one session row. Tests and operators; the
// service never reads a session back on the ingest path.
func (s *Store) GetPlaybackSession(ctx context.Context, actorID, sessionID, contentID uuid.UUID) (*PlaybackSession, error) {
	return scanSession(s.db.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM analytics.playback_sessions
		 WHERE actor_id = $1 AND session_id = $2 AND content_id = $3`,
		actorID, sessionID, contentID))
}

// applySessionUpdate folds one accepted playback event into its session
// row, inside the ingest transaction so the receipt, the raw row and the
// session commit or roll back together.
//
// Three steps, in order:
//
//  1. A play_start supersedes any other open session this viewer has on
//     the same content: those are finalised now with reason 'superseded'
//     so a viewer who reopens a video is one session at a time.
//  2. The upsert. Totals take GREATEST, seek_count sums, the creator and
//     content type snapshot at first sight are never overwritten.
//  3. The row is re-read FOR UPDATE and the parts SQL cannot do are done
//     in Go: the coverage bitmap, the derived percentages, and — for a
//     play_end, or for any event landing on an already-finalised row —
//     the display-view flag and view score, recomputed on the same row.
//     That last clause is what makes a late play_end a correction and
//     never a second view: the primary key guarantees one row, and the
//     aggregators recompute whole buckets from rows.
func applySessionUpdate(ctx context.Context, tx pgx.Tx, event Event) error {
	u := event.Session
	if u == nil {
		return nil
	}
	if event.SessionID == uuid.Nil {
		return errors.New("playback session update without a session id")
	}
	at := event.Timestamp.UTC()

	if u.Kind == model.EventPlayStart {
		if err := supersedeOpenSessions(ctx, tx, event.UserID, event.ContentID, event.SessionID, event.ReceivedAt); err != nil {
			return fmt.Errorf("supersede open sessions: %w", err)
		}
	}

	var endReason *string
	if u.EndReason != "" {
		endReason = &u.EndReason
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO analytics.playback_sessions AS s (
			actor_id, session_id, content_id, creator_id, content_type,
			first_seen, last_seen, content_duration_ms,
			watched_ms, watched_ms_reported, max_playhead_ms, max_continuous_ms,
			loop_count, seek_count, playback_speed, percent_viewed,
			is_self_view, end_reason, source
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $6, $7,
			$8, $9, $10, $11,
			$12, $13, $14, $15,
			$16, $17, 'live'
		)
		ON CONFLICT (actor_id, session_id, content_id) DO UPDATE SET
			first_seen          = LEAST(s.first_seen, EXCLUDED.first_seen),
			last_seen           = GREATEST(s.last_seen, EXCLUDED.last_seen),
			content_duration_ms = GREATEST(s.content_duration_ms, EXCLUDED.content_duration_ms),
			watched_ms          = GREATEST(s.watched_ms, EXCLUDED.watched_ms),
			watched_ms_reported = GREATEST(s.watched_ms_reported, EXCLUDED.watched_ms_reported),
			max_playhead_ms     = GREATEST(s.max_playhead_ms, EXCLUDED.max_playhead_ms),
			max_continuous_ms   = GREATEST(s.max_continuous_ms, EXCLUDED.max_continuous_ms),
			loop_count          = GREATEST(s.loop_count, EXCLUDED.loop_count),
			seek_count          = s.seek_count + EXCLUDED.seek_count,
			playback_speed      = CASE WHEN EXCLUDED.playback_speed > 0
			                           THEN EXCLUDED.playback_speed ELSE s.playback_speed END,
			percent_viewed      = GREATEST(s.percent_viewed, EXCLUDED.percent_viewed),
			end_reason          = COALESCE(EXCLUDED.end_reason, s.end_reason)`,
		event.UserID, event.SessionID, event.ContentID, u.CreatorID, u.ContentType,
		at, u.ContentDurationMS,
		u.WatchedMS, u.WatchedMSReported, u.PlayheadMS, u.MaxContinuousMS,
		u.LoopCount, u.SeekIncrement, u.PlaybackSpeed, u.PercentViewed,
		u.IsSelfView, endReason,
	); err != nil {
		return fmt.Errorf("upsert playback session: %w", err)
	}

	row, err := scanSession(tx.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM analytics.playback_sessions
		 WHERE actor_id = $1 AND session_id = $2 AND content_id = $3
		 FOR UPDATE`,
		event.UserID, event.SessionID, event.ContentID))
	if err != nil {
		return fmt.Errorf("lock playback session: %w", err)
	}

	if u.Kind == model.EventWatchHeartbeat && u.IncrementMS > 0 {
		speed := u.PlaybackSpeed
		if speed <= 0 {
			speed = 1
		}
		mediaMS := int64(math.Round(float64(u.IncrementMS) * speed))
		row.Coverage = markCoverage(row.Coverage, row.ContentDurationMS, u.PlayheadMS-mediaMS, u.PlayheadMS)
	}
	deriveSessionMeasures(row)

	finalize := u.Kind == model.EventPlayEnd || row.FinalizedAt != nil
	if finalize {
		row.IsDisplayView, row.ViewScore = sessionFlags(row)
		if u.Kind == model.EventPlayEnd {
			reason := FinalizePlayEnd
			closedAt := event.ReceivedAt.UTC()
			row.FinalizeReason = &reason
			row.FinalizedAt = &closedAt
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE analytics.playback_sessions SET
			coverage = $4, covered_ms = $5,
			percent_viewed = $6, percent_covered = $7,
			finalized_at = $8, finalize_reason = $9,
			is_display_view = $10, view_score = $11
		WHERE actor_id = $1 AND session_id = $2 AND content_id = $3`,
		event.UserID, event.SessionID, event.ContentID,
		row.Coverage, row.CoveredMS,
		row.PercentViewed, row.PercentCovered,
		row.FinalizedAt, row.FinalizeReason,
		row.IsDisplayView, row.ViewScore,
	); err != nil {
		return fmt.Errorf("derive playback session: %w", err)
	}
	return nil
}

// supersedeOpenSessions finalises every open session the viewer has on
// this content other than the one that just started.
func supersedeOpenSessions(ctx context.Context, tx pgx.Tx, actorID, contentID, exceptSession uuid.UUID, at time.Time) error {
	rows, err := tx.Query(ctx,
		`SELECT `+sessionColumns+` FROM analytics.playback_sessions
		 WHERE actor_id = $1 AND content_id = $2 AND session_id <> $3
		   AND finalized_at IS NULL
		 FOR UPDATE`,
		actorID, contentID, exceptSession)
	if err != nil {
		return err
	}
	open, err := collectSessions(rows)
	if err != nil {
		return err
	}
	return finalizeSessions(ctx, tx, open, FinalizeSuperseded, at)
}

// FinalizeInactiveSessions closes every open session whose last event is
// older than `before`, computing the display-view flag and view score as
// of now. Rows are taken in batches under SKIP LOCKED so two instances
// ticking at once divide the work rather than blocking. Returns how many
// were closed.
func (s *Store) FinalizeInactiveSessions(ctx context.Context, before, now time.Time, batch int) (int, error) {
	if batch <= 0 {
		batch = 500
	}
	closed := 0
	for {
		n, err := s.finalizeInactiveBatch(ctx, before, now, batch)
		if err != nil {
			return closed, err
		}
		closed += n
		if n < batch {
			return closed, nil
		}
	}
}

func (s *Store) finalizeInactiveBatch(ctx context.Context, before, now time.Time, batch int) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`SELECT `+sessionColumns+` FROM analytics.playback_sessions
		 WHERE finalized_at IS NULL AND last_seen < $1
		 ORDER BY last_seen
		 LIMIT $2
		 FOR UPDATE SKIP LOCKED`,
		before, batch)
	if err != nil {
		return 0, err
	}
	stale, err := collectSessions(rows)
	if err != nil {
		return 0, err
	}
	if len(stale) == 0 {
		return 0, tx.Commit(ctx)
	}
	if err := finalizeSessions(ctx, tx, stale, FinalizeInactivity, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(stale), nil
}

func collectSessions(rows pgx.Rows) ([]*PlaybackSession, error) {
	defer rows.Close()
	var out []*PlaybackSession
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// finalizeSessions stamps the flags and the close on each row. The
// measures are re-derived first so a row that only ever saw heartbeats
// still carries its percentages.
func finalizeSessions(ctx context.Context, tx pgx.Tx, sessions []*PlaybackSession, reason string, at time.Time) error {
	at = at.UTC()
	for _, row := range sessions {
		deriveSessionMeasures(row)
		isDisplay, score := sessionFlags(row)
		if _, err := tx.Exec(ctx, `
			UPDATE analytics.playback_sessions SET
				covered_ms = $4, percent_viewed = $5, percent_covered = $6,
				finalized_at = $7, finalize_reason = $8,
				is_display_view = $9, view_score = $10
			WHERE actor_id = $1 AND session_id = $2 AND content_id = $3`,
			row.ActorID, row.SessionID, row.ContentID,
			row.CoveredMS, row.PercentViewed, row.PercentCovered,
			at, reason, isDisplay, score,
		); err != nil {
			return fmt.Errorf("finalize playback session (%s): %w", reason, err)
		}
	}
	return nil
}

// deriveSessionMeasures recomputes the columns that follow from the
// GREATEST'd totals and the coverage bitmap.
//
// percent_covered is the unique-coverage measure the quality score reads
// after cutover. When no heartbeat ever marked the bitmap — a client that
// only sent play_end, or a backfilled historical row — it falls back to
// percent_viewed rather than reading as zero, so a lost heartbeat stream
// cannot zero a view that play_end vouched for.
func deriveSessionMeasures(row *PlaybackSession) {
	if row.ContentDurationMS > 0 {
		pv := float64(row.WatchedMS) / float64(row.ContentDurationMS) * 100
		if pv > 100 {
			pv = 100
		}
		if pv > row.PercentViewed {
			row.PercentViewed = pv
		}
	}
	if len(row.Coverage) > 0 {
		covered := coverageSeconds(row.Coverage) * 1000
		if row.ContentDurationMS > 0 && covered > row.ContentDurationMS {
			covered = row.ContentDurationMS
		}
		row.CoveredMS = covered
		if row.ContentDurationMS > 0 {
			row.PercentCovered = math.Min(100, float64(covered)/float64(row.ContentDurationMS)*100)
		}
		return
	}
	covered := row.WatchedMS
	if row.ContentDurationMS > 0 && covered > row.ContentDurationMS {
		covered = row.ContentDurationMS
	}
	row.CoveredMS = covered
	row.PercentCovered = row.PercentViewed
}

// sessionFlags is the one place a session becomes a view. The display
// bar is model.IsDisplayView on the clamped totals; the score is the
// fraction of the content actually covered, which is what the quality
// weighted view count in the aggregates sums. A self-view keeps its
// honest flag here — the aggregators exclude it — so dashboards still
// see the creator's own plays.
func sessionFlags(row *PlaybackSession) (isDisplayView bool, viewScore float64) {
	isDisplayView = model.IsDisplayView(row.ContentType, row.ContentDurationMS, row.WatchedMS, row.PercentViewed, row.LoopCount)
	if !isDisplayView {
		return false, 0
	}
	return true, math.Min(row.PercentCovered, 100) / 100
}

// markCoverage sets one bit per second of content over [fromMS, toMS].
// A negative fromMS is a loop wrap: the tail of the previous pass is
// marked as its own range. With no known duration the bitmap grows to
// the playhead and is trimmed against the duration when it arrives.
func markCoverage(bitmap []byte, durationMS, fromMS, toMS int64) []byte {
	if toMS < 0 {
		return bitmap
	}
	capSeconds := int64(-1)
	if durationMS > 0 {
		capSeconds = (durationMS + 999) / 1000
		if toMS > durationMS {
			toMS = durationMS
		}
	}
	if fromMS < 0 {
		if durationMS > 0 {
			bitmap = markRange(bitmap, capSeconds, durationMS+fromMS, durationMS)
		}
		fromMS = 0
	}
	if fromMS > toMS {
		// A seek backwards inside one beat; the range is unknowable and
		// the totals already took the watch time. Mark the landing second.
		fromMS = toMS
	}
	return markRange(bitmap, capSeconds, fromMS, toMS)
}

func markRange(bitmap []byte, capSeconds, fromMS, toMS int64) []byte {
	if toMS < fromMS {
		return bitmap
	}
	first := fromMS / 1000
	last := (toMS + 999) / 1000 // exclusive
	if last == first {
		last = first + 1
	}
	if capSeconds >= 0 && last > capSeconds {
		last = capSeconds
	}
	if last <= first {
		return bitmap
	}
	if need := int((last + 7) / 8); len(bitmap) < need {
		grown := make([]byte, need)
		copy(grown, bitmap)
		bitmap = grown
	}
	for sec := first; sec < last; sec++ {
		bitmap[sec/8] |= 1 << uint(sec%8)
	}
	return bitmap
}

func coverageSeconds(bitmap []byte) int64 {
	var n int64
	for _, b := range bitmap {
		n += int64(bits.OnesCount8(b))
	}
	return n
}
