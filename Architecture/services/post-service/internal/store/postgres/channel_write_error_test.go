package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// The three unique indexes on channels each map to their own conflict; the
// name index must not fall through to the handle default (migration 067).
func TestMapChannelWriteErrorUniqueViolations(t *testing.T) {
	cases := map[string]error{
		"uq_channels_name_lower": ErrChannelNameTaken,
		"channels_handle_key":    ErrHandleTaken,
		"uq_channels_user":       ErrChannelExists,
	}
	for constraint, want := range cases {
		got := mapChannelWriteError(&pgconn.PgError{Code: "23505", ConstraintName: constraint})
		if !errors.Is(got, want) {
			t.Errorf("%s: got %v want %v", constraint, got, want)
		}
	}
}
