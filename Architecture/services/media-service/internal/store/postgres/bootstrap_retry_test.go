package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsTransientDDLConflict(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"deadlock", &pgconn.PgError{Code: "40P01"}, true},
		{"serialization", &pgconn.PgError{Code: "40001"}, true},
		{"wrapped deadlock", fmt.Errorf("apply media schema: %w", &pgconn.PgError{Code: "40P01"}), true},
		{"syntax error", &pgconn.PgError{Code: "42601"}, false},
		{"plain error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isTransientDDLConflict(c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
