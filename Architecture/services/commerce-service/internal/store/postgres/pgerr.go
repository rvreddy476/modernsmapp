package postgres

// PostgreSQL error classification.
//
// These exist so the checkout transaction can tell "a concurrent checkout
// took the last unit" from "the database is unreachable" and answer the
// customer differently. The old code could not: every store error became
// `INTERNAL_ERROR`, so an out-of-stock race and an outage looked identical
// to the client.

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL SQLSTATE codes we branch on.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateCheckViolation      = "23514"
	sqlStateForeignKeyViolation = "23503"
	sqlStateSerializationFail   = "40001"
	sqlStateDeadlockDetected    = "40P01"
	sqlStateLockNotAvailable    = "55P03"
	// insufficient_privilege is what the fence triggers in migration 012
	// raise, so a fenced write is distinguishable from a real permission
	// problem by its message rather than its code alone.
	sqlStateInsufficientPrivilege = "42501"
)

func pgCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func isUniqueViolation(err error) bool { return pgCode(err) == sqlStateUniqueViolation }

// isCheckViolation is how an oversell surfaces.
//
// LB-23 replaced `GREATEST(0, total_qty - qty)` — which absorbed an oversell
// and hid it — with `CHECK (reserved_qty <= total_qty)`. So the constraint,
// not the application, is what prevents the oversell, and this is how the
// application learns it was prevented.
func isCheckViolation(err error) bool { return pgCode(err) == sqlStateCheckViolation }

func isForeignKeyViolation(err error) bool { return pgCode(err) == sqlStateForeignKeyViolation }

// IsRetryable reports a transient concurrency failure.
//
// Under the row-level locking the checkout transaction takes, a deadlock
// should be impossible (locks are acquired in ascending variant_id order),
// but "should be impossible" is not "is impossible", and retrying a
// serialization failure is safe precisely because the whole checkout is one
// transaction: a retry starts from nothing.
func IsRetryable(err error) bool {
	switch pgCode(err) {
	case sqlStateSerializationFail, sqlStateDeadlockDetected, sqlStateLockNotAvailable:
		return true
	}
	return false
}

// IsFenced reports a write refused by a migration-012 fence trigger.
func IsFenced(err error) bool {
	if pgCode(err) != sqlStateInsufficientPrivilege {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return len(pgErr.Message) > 0
	}
	return false
}
