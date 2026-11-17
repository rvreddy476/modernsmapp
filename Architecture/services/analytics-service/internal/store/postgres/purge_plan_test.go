package postgres

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/analytics-service/database"
)

// reels-8: every column in the analytics schema that holds a person's id
// has a decision in PurgePlan. A new table or column with a user id and no
// entry fails here, before it can ship as data account deletion leaves
// behind.

// userIDColumn is what counts as a person's id. Content, session, event and
// mission ids are not people.
var userIDColumn = regexp.MustCompile(`^(user|actor|viewer|creator|author|follower|followee|owner|account|member|reactor|subject|target_user|blocked|blocker)_id$`)

var (
	sqlLineComment = regexp.MustCompile(`--[^\n]*`)
	createTableRe  = regexp.MustCompile(`create table (?:if not exists )?([a-z0-9_.]+)\s*\(`)
	addColumnRe    = regexp.MustCompile(`alter table (?:if exists )?(?:only )?([a-z0-9_.]+)\s+add column (?:if not exists )?([a-z0-9_]+)`)
)

// userIDColumns returns "table.column" for every person-id column the DDL
// declares, from CREATE TABLE column lists and ALTER TABLE ADD COLUMN.
func userIDColumns(ddl string) []string {
	sql := sqlLineComment.ReplaceAllString(strings.ToLower(ddl), "")
	found := map[string]struct{}{}
	for _, m := range createTableRe.FindAllStringSubmatchIndex(sql, -1) {
		table := sql[m[2]:m[3]]
		// Walk to the matching close paren of the column list.
		depth, start, end := 1, m[1], -1
		for i := start; i < len(sql) && end < 0; i++ {
			switch sql[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = i
				}
			}
		}
		if end < 0 {
			continue
		}
		// Split on top-level commas only.
		body, depth, last := sql[start:end], 0, 0
		var parts []string
		for i, ch := range body {
			switch ch {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 0 {
					parts = append(parts, body[last:i])
					last = i + 1
				}
			}
		}
		parts = append(parts, body[last:])
		for _, p := range parts {
			fields := strings.Fields(p)
			if len(fields) == 0 {
				continue
			}
			if userIDColumn.MatchString(fields[0]) {
				found[table+"."+fields[0]] = struct{}{}
			}
		}
	}
	for _, m := range addColumnRe.FindAllStringSubmatch(sql, -1) {
		if userIDColumn.MatchString(m[2]) {
			found[m[1]+"."+m[2]] = struct{}{}
		}
	}
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func analyticsDDL(t *testing.T) string {
	t.Helper()
	var all strings.Builder
	all.WriteString(database.SetupSQL)
	err := fs.WalkDir(database.Migrations, "migrations", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(database.Migrations, path)
		all.WriteString("\n")
		all.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return all.String()
}

func planIndex() map[string]PurgeTarget {
	idx := map[string]PurgeTarget{}
	for _, p := range PurgePlan {
		idx[p.Table+"."+p.Column] = p
	}
	return idx
}

func TestEveryUserIDColumnHasAPurgeDecision(t *testing.T) {
	cols := userIDColumns(analyticsDDL(t))
	if len(cols) < 10 {
		t.Fatalf("DDL scan found only %d user-id columns (%v); the parser is broken", len(cols), cols)
	}
	plan := planIndex()
	for _, c := range cols {
		p, ok := plan[c]
		if !ok {
			t.Errorf("%s holds a user id and has no entry in PurgePlan: decide delete or retain (with a reason)", c)
			continue
		}
		if p.Action != PurgeDelete && p.Action != PurgeRetain {
			t.Errorf("%s: unknown action %q", c, p.Action)
		}
		if p.Action == PurgeRetain && strings.TrimSpace(p.Reason) == "" {
			t.Errorf("%s is retained with no reason", c)
		}
	}
	// No stale entries: everything in the plan exists in the schema.
	declared := map[string]bool{}
	for _, c := range cols {
		declared[c] = true
	}
	for k := range plan {
		if !declared[k] {
			t.Errorf("PurgePlan names %s, which no migration declares", k)
		}
	}
}

// The detector itself: a new table or column with a user id is seen.
func TestUserIDColumnScanSeesNewColumns(t *testing.T) {
	got := userIDColumns(`
		-- a comment mentioning viewer_id is ignored
		CREATE TABLE IF NOT EXISTS analytics.new_thing (
			id UUID PRIMARY KEY,
			viewer_id UUID NOT NULL,
			payload JSONB DEFAULT '{}'::jsonb,
			UNIQUE (viewer_id, id)
		);
		ALTER TABLE analytics.old_thing ADD COLUMN IF NOT EXISTS owner_id UUID;
		ALTER TABLE analytics.old_thing ADD COLUMN IF NOT EXISTS content_id UUID;`)
	want := []string{"analytics.new_thing.viewer_id", "analytics.old_thing.owner_id"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scan = %v, want %v", got, want)
	}
}

// Every delete decision has a statement that filters on its column, and
// the receipts run before the sessions and events they are found through.
func TestPurgeDeleteStatementsCoverThePlan(t *testing.T) {
	order := map[string]int{}
	for i, p := range PurgePlan {
		if p.Action != PurgeDelete {
			continue
		}
		if _, seen := order[p.Table]; !seen {
			order[p.Table] = i
		}
		var sql string
		if p.Table == "analytics.ingest_receipts" {
			sql = receiptsDeleteSQL
		} else {
			sql = purgeDeleteSQL(p)
		}
		if !strings.Contains(sql, "DELETE FROM "+p.Table+" ") {
			t.Errorf("%s: statement does not delete from the table: %s", p.Table, sql)
		}
		if !strings.Contains(sql, p.Column+" = $1") {
			t.Errorf("%s.%s: statement does not filter on the column: %s", p.Table, p.Column, sql)
		}
		if !strings.Contains(sql, "LIMIT $") {
			t.Errorf("%s: delete is not bounded: %s", p.Table, sql)
		}
	}
	for _, later := range []string{"analytics.playback_sessions", "analytics.events_raw"} {
		if order["analytics.ingest_receipts"] >= order[later] {
			t.Errorf("ingest_receipts must be purged before %s (it is found through its content ids)", later)
		}
	}
	for _, sql := range []string{userContentIDsSQL, watchSessionKeysSQL} {
		if !strings.Contains(sql, "actor_id = $1") || !strings.Contains(sql, "user_id = $1") {
			t.Errorf("key lookup must read both the sessions and the raw events of the user: %s", sql)
		}
	}
}

// The viewer's own history is deleted, never merely retained: a change to
// any of these decisions is a privacy change and must fail here first.
func TestViewerHistoryIsDeleted(t *testing.T) {
	plan := planIndex()
	for _, c := range []string{
		"analytics.events_raw.user_id",
		"analytics.playback_sessions.actor_id",
		"analytics.ingest_receipts.actor_id",
		"analytics.creator_follower_daily.creator_id",
		"user_streaks.user_id", "user_badges.user_id", "mission_progress.user_id",
		"loyalty_points.user_id", "point_transactions.user_id",
	} {
		if p, ok := plan[c]; !ok || p.Action != PurgeDelete {
			t.Errorf("%s must be PurgeDelete, got %+v", c, p)
		}
	}
}
