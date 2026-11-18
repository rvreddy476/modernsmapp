package postgres

import (
	"context"
	"time"
)

// FanoutQueueStats is the shape of the fan-out job table at one moment, for
// the queue gauges (lv-21). Completed and failed rows are not counted: the
// failures are counted as they happen (fanout_jobs_settled_total).
type FanoutQueueStats struct {
	// Due: pending and available now, waiting for a worker to claim it.
	// OldestDue is measured from available_at: how long the oldest due job
	// has gone unclaimed. A worker that is down or wedged shows here.
	Due       int64
	OldestDue time.Duration
	// Waiting: deferred because the post is not visible yet (wait_reason
	// set). OldestWaiting is measured from created_at; the wait ends at
	// about 72 h (fanoutMaxWaits), so it is bounded by design.
	Waiting       int64
	OldestWaiting time.Duration
	// Backoff: pending after a transient failure, not due yet.
	Backoff int64
	// Running: claimed by a worker. OldestRunning is measured from
	// claimed_at, which every page refreshes; past 10 minutes the job is
	// reclaimed as stale.
	Running       int64
	OldestRunning time.Duration
}

// FanoutQueueStats reads the counts and oldest ages of the unfinished
// fan-out jobs in one statement.
func (s *Store) FanoutQueueStats(ctx context.Context) (FanoutQueueStats, error) {
	var st FanoutQueueStats
	var due, waiting, running float64
	err := s.db.QueryRow(ctx, `
		SELECT
		  COUNT(*) FILTER (WHERE status = 'pending' AND available_at <= NOW()),
		  COALESCE(EXTRACT(EPOCH FROM NOW() - MIN(available_at)
		    FILTER (WHERE status = 'pending' AND available_at <= NOW())), 0)::float8,
		  COUNT(*) FILTER (WHERE status = 'pending' AND available_at > NOW() AND wait_reason <> ''),
		  COALESCE(EXTRACT(EPOCH FROM NOW() - MIN(created_at)
		    FILTER (WHERE status = 'pending' AND available_at > NOW() AND wait_reason <> '')), 0)::float8,
		  COUNT(*) FILTER (WHERE status = 'pending' AND available_at > NOW() AND wait_reason = ''),
		  COUNT(*) FILTER (WHERE status = 'running'),
		  COALESCE(EXTRACT(EPOCH FROM NOW() - MIN(claimed_at) FILTER (WHERE status = 'running')), 0)::float8
		FROM subscriber_fanout_jobs
		WHERE status IN ('pending', 'running')`).
		Scan(&st.Due, &due, &st.Waiting, &waiting, &st.Backoff, &st.Running, &running)
	if err != nil {
		return FanoutQueueStats{}, err
	}
	st.OldestDue = secondsToDuration(due)
	st.OldestWaiting = secondsToDuration(waiting)
	st.OldestRunning = secondsToDuration(running)
	return st, nil
}

func secondsToDuration(s float64) time.Duration {
	if s <= 0 {
		return 0
	}
	return time.Duration(s * float64(time.Second))
}
