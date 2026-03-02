package health

import (
	"context"
	"fmt"
)

// Pinger is implemented by pgxpool.Pool and similar types.
type Pinger interface {
	Ping(ctx context.Context) error
}

// PingCheck returns a CheckFunc that calls Ping on the given dependency.
func PingCheck(p Pinger) CheckFunc {
	return func(ctx context.Context) error {
		return p.Ping(ctx)
	}
}

// RedisPingCheck returns a CheckFunc that uses a closure to ping Redis.
// Pass a function like: health.RedisPingCheck(func(ctx) error { return rdb.Ping(ctx).Err() })
func RedisPingCheck(pingFn func(ctx context.Context) error) CheckFunc {
	return pingFn
}

// ScyllaCheck returns a CheckFunc that runs a trivial CQL query.
// Pass a function like: health.ScyllaCheck(func(ctx) error { return session.Query("SELECT now() FROM system.local").Exec() })
func ScyllaCheck(checkFn func(ctx context.Context) error) CheckFunc {
	if checkFn == nil {
		return func(ctx context.Context) error {
			return fmt.Errorf("scylla session is nil")
		}
	}
	return checkFn
}
