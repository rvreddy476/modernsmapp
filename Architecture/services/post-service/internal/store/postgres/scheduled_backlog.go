package postgres

import (
	"context"
	"time"
)

// ScheduledBacklog is what the schedule worker leaves behind after a tick,
// for its gauges (lv-21): scheduled posts whose publish_at has passed and
// that are still not live. Notes (noteschedule) and posts parked by a
// publish block (they wait for their author) are not counted.
type ScheduledBacklog struct {
	// Due: overdue and publishable now (no video, or its video is ready and
	// past review). The worker should have published these; OldestDue is
	// how far past publish_at the oldest one is.
	Due       int64
	OldestDue time.Duration
	// HeldVideo: overdue but held because the video is still transcoding or
	// in review (scheduledVideoReadySQL). Expected for a long video
	// scheduled close to its upload; OldestHeldVideo says how long.
	HeldVideo       int64
	OldestHeldVideo time.Duration
}

// ScheduledPostBacklog counts the overdue scheduled posts in one statement.
// Index: idx_posts_scheduled (publish_at).
func (s *Store) ScheduledPostBacklog(ctx context.Context, now time.Time) (ScheduledBacklog, error) {
	var b ScheduledBacklog
	var due, held float64
	err := s.db.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE ready),
		  COALESCE(EXTRACT(EPOCH FROM $1 - MIN(publish_at) FILTER (WHERE ready)), 0)::float8,
		  COUNT(*) FILTER (WHERE NOT ready),
		  COALESCE(EXTRACT(EPOCH FROM $1 - MIN(publish_at) FILTER (WHERE NOT ready)), 0)::float8
		FROM (
		  SELECT p.publish_at, `+scheduledVideoReadySQL("p")+` AS ready
		  FROM posts p
		  WHERE p.publish_at IS NOT NULL AND p.publish_at <= $1 AND p.deleted_at IS NULL
		    AND `+NotNoteSQL("p")+`
		    AND NOT EXISTS (SELECT 1 FROM post_publish_blocks b WHERE b.post_id = p.id)
		) overdue`, now).Scan(&b.Due, &due, &b.HeldVideo, &held)
	if err != nil {
		return ScheduledBacklog{}, err
	}
	b.OldestDue = secondsDuration(due)
	b.OldestHeldVideo = secondsDuration(held)
	return b, nil
}

func secondsDuration(s float64) time.Duration {
	if s <= 0 {
		return 0
	}
	return time.Duration(s * float64(time.Second))
}
