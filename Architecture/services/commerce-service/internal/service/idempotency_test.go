package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// isPostgresUniqueViolation underlies H4's race-recovery path: when
// CreateOrder loses a race against another retried checkout, we
// recover by re-reading the existing order instead of 500-ing. ONLY
// the 23505 (unique_violation) code triggers the recovery — any
// other pg error must propagate so we don't swallow real failures.
func TestIsPostgresUniqueViolation(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("boom"), false},
		{"pg 23505 unique_violation", &pgconn.PgError{Code: "23505"}, true},
		{"pg 23503 foreign_key_violation", &pgconn.PgError{Code: "23503"}, false},
		{"pg 23502 not_null_violation", &pgconn.PgError{Code: "23502"}, false},
		{"pg 40001 serialization_failure", &pgconn.PgError{Code: "40001"}, false},
		{"wrapped 23505", fmt.Errorf("create order: %w", &pgconn.PgError{Code: "23505"}), true},
		{"doubly wrapped 23505", fmt.Errorf("svc: %w", fmt.Errorf("store: %w", &pgconn.PgError{Code: "23505"})), true},
		{"wrapped 23503", fmt.Errorf("create order: %w", &pgconn.PgError{Code: "23503"}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isPostgresUniqueViolation(tc.err)
			if got != tc.want {
				t.Fatalf("isPostgresUniqueViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
