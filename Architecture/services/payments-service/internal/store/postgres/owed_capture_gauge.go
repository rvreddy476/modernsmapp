package postgres

import "context"

// OwedCaptureBacklog is what the owed-capture alarm gauges report (com-2):
// every payments.refund_required row not yet resolved, whenever it was
// recorded — rows written while the worker was off included.
type OwedCaptureBacklog struct {
	Unrefunded       int64
	Parked           int64
	OldestAgeSeconds float64
}

// owedCaptureBacklogSQL is also the read-only operator query (one row:
// unrefunded, parked, oldest age in seconds). It reads the partial index
// idx_refund_required_unresolved (migration 008).
const owedCaptureBacklogSQL = `SELECT count(*),
       count(*) FILTER (WHERE parked_at IS NOT NULL),
       COALESCE(EXTRACT(EPOCH FROM NOW() - min(detected_at)), 0)::float8
  FROM payments.refund_required
 WHERE resolved_at IS NULL`

// OwedCaptureBacklog counts the owed captures not yet refunded.
func (s *Store) OwedCaptureBacklog(ctx context.Context) (OwedCaptureBacklog, error) {
	var b OwedCaptureBacklog
	err := s.db.QueryRow(ctx, owedCaptureBacklogSQL).Scan(&b.Unrefunded, &b.Parked, &b.OldestAgeSeconds)
	return b, err
}
