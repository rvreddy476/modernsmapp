package postgres

import (
	"context"
	"time"
)

// RunExclusive runs fn only on the replica that takes the session advisory
// lock for name, and reports whether it ran. Another replica holding the lock
// means this one skips the pass (audit 2026-10-30 L3 dispatch scan, L6 fraud
// pass): the work is idempotent, so one replica doing it per tick is enough
// and N replicas doing it only multiplies the load.
//
// The lock is taken and released on one dedicated pool connection. If the
// unlock cannot be confirmed, the connection is closed, which releases every
// session lock it holds, so a lock never leaks back into the pool.
func (s *Store) RunExclusive(ctx context.Context, name string, fn func(context.Context) error) (bool, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Release()
	key := "food-service:worker:" + name
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&got); err != nil {
		return false, err
	}
	if !got {
		return false, nil
	}
	defer func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var released bool
		if err := conn.QueryRow(unlockCtx, `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, key).Scan(&released); err != nil || !released {
			_ = conn.Conn().Close(unlockCtx)
		}
	}()
	return true, fn(ctx)
}

// LatestFraudScoreAt is when the fraud worker last wrote a score, or nil when
// it never did (audit L6: a booting replica skips the pass when one ran
// recently).
func (s *Store) LatestFraudScoreAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := s.db.QueryRow(ctx, `SELECT MAX(computed_at) FROM food.fraud_scores`).Scan(&at); err != nil {
		return nil, err
	}
	return at, nil
}
