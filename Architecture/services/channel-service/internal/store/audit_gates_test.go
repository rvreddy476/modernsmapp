package store

import (
	"strings"
	"testing"
)

// Audit 2026-11-07, channel-service. These gates are SQL, so the SQL is
// pinned here; the behaviour is proven against a database in CI
// (internal/service/audit_gates_integration_test.go).

func mustContain(t *testing.T, name, sql string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(sql, p) {
			t.Errorf("%s must contain %q:\n%s", name, p, sql)
		}
	}
}

// CH-M2: comment delete and pin match the update in the path, never the
// comment id alone.
func TestCommentWritesAreScopedToTheUpdate(t *testing.T) {
	mustContain(t, "deleteCommentAsModeratorSQL", deleteCommentAsModeratorSQL, "WHERE id = $1 AND update_id = $2")
	mustContain(t, "deleteOwnCommentSQL", deleteOwnCommentSQL, "WHERE id = $1 AND update_id = $2 AND author_id = $3")
	mustContain(t, "pinCommentCheckSQL", pinCommentCheckSQL, "WHERE id = $1 AND update_id = $2")
	mustContain(t, "pinCommentSQL", pinCommentSQL, "WHERE id = $1 AND update_id = $2")
	mustContain(t, "unpinUpdateCommentsSQL", unpinUpdateCommentsSQL, "WHERE update_id = $1")
	// A reply's parent is caller input: it must be a comment on this update.
	mustContain(t, "addCommentSQL", addCommentSQL, "p.id = $5 AND p.update_id = $2")
}

// CH-M3: no write path can delete or overwrite a ban.
func TestMembershipWritesNeverTouchABan(t *testing.T) {
	mustContain(t, "removeMemberSQL", removeMemberSQL, "AND role <> 'banned'")
	mustContain(t, "addMemberSQL", addMemberSQL, "WHERE channel_members.role <> 'banned'")
	mustContain(t, "updateMemberRoleSQL", updateMemberRoleSQL, "AND role <> 'banned'")
}

// CH-L4: /my is one page — one ORDER BY and one LIMIT over both branches —
// lists active channels only, and never one the user is banned from.
func TestMyChannelsIsOnePageWithoutBans(t *testing.T) {
	q := myChannelsSQL
	mustContain(t, "myChannelsSQL", q, "UNION ALL", "cm.role <> 'banned'", "bc.owner_id <> $1", "ORDER BY listed_at DESC, id DESC")
	if n := strings.Count(q, "LIMIT"); n != 1 {
		t.Errorf("myChannelsSQL has %d LIMITs, want exactly 1 over the union", n)
	}
	if n := strings.Count(q, "bc.status = 'active'"); n != 2 {
		t.Errorf("both branches must list active channels only (found %d)", n)
	}
}

// CH-L1: the per-page lookups are batched and keep the ban filter.
func TestBatchedMembershipQueries(t *testing.T) {
	mustContain(t, "countSubscribersBatchSQL", countSubscribersBatchSQL, "channel_id = ANY($1)", "role <> 'banned'", "GROUP BY channel_id")
	mustContain(t, "membersOfSQL", membersOfSQL, "user_id = $1 AND channel_id = ANY($2)")
}
