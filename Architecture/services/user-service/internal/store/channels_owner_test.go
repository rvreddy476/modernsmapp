package store

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/google/uuid"
)

// Security sweep #28 (21 Oct 2026): the channel update/delete owner rule lives
// only in SQL (user_id bound to the caller) and had no test, so a refactor
// could drop it unnoticed. No database runs in unit tests, so this pins the
// two statements and the argument binding; channels_owner_integration_test.go
// proves the behaviour against Postgres.

var placeholder = regexp.MustCompile(`\$([0-9]+)`)

func maxPlaceholder(sql string) int {
	n := 0
	for _, m := range placeholder.FindAllStringSubmatch(sql, -1) {
		if v, _ := strconv.Atoi(m[1]); v > n {
			n = v
		}
	}
	return n
}

func TestChannelWritesAreOwnerBoundInSQL(t *testing.T) {
	updWhere := regexp.MustCompile(`(?is)\bWHERE\s+id\s*=\s*\$1\s+AND\s+user_id\s*=\s*\$12\s*$`)
	if !updWhere.MatchString(updateChannelSQL) {
		t.Fatalf("UPDATE channels is not bound to the owner (want WHERE id = $1 AND user_id = $12):\n%s", updateChannelSQL)
	}
	delWhere := regexp.MustCompile(`(?is)^\s*DELETE\s+FROM\s+channels\s+WHERE\s+id\s*=\s*\$1\s+AND\s+user_id\s*=\s*\$2\s*$`)
	if !delWhere.MatchString(deleteChannelSQL) {
		t.Fatalf("DELETE FROM channels is not bound to the owner (want WHERE id = $1 AND user_id = $2):\n%s", deleteChannelSQL)
	}
	for name, sql := range map[string]string{"update": updateChannelSQL, "delete": deleteChannelSQL} {
		if regexp.MustCompile(`(?i)\bOR\b`).MatchString(sql) {
			t.Fatalf("%s carries an OR that could widen the owner predicate:\n%s", name, sql)
		}
	}
}

// $12 is the caller, not the channel id or any body field.
func TestUpdateChannelBindsTheCallerAsOwner(t *testing.T) {
	channel, caller := uuid.New(), uuid.New()
	args := updateChannelArgs(&ChannelUpdate{ID: channel, UserID: caller})
	if got, want := len(args), maxPlaceholder(updateChannelSQL); got != want {
		t.Fatalf("%d args for %d placeholders", got, want)
	}
	if args[0] != channel {
		t.Fatalf("$1 = %v, want the channel id", args[0])
	}
	if args[11] != caller {
		t.Fatalf("$12 (owner) = %v, want the caller %v", args[11], caller)
	}
}
