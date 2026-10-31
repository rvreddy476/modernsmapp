package store

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/atpost/qa-service/database"
)

// One open report per (reporter, target): the insert's ON CONFLICT names the
// partial index's columns and predicate (Postgres infers the index only
// from both), the predicate is the one migration 011 indexes, and the
// open-report read uses it too.
func TestReportSQLMatchesTheOpenOnceIndex(t *testing.T) {
	body, err := fs.ReadFile(database.Migrations, "migrations/011_qa_moderation_reports_open_once.sql")
	if err != nil {
		t.Fatal(err)
	}
	mig := strings.Join(strings.Fields(string(body)), " ")
	wantIndex := "CREATE UNIQUE INDEX IF NOT EXISTS uq_moderation_reports_open_once ON moderation_reports (reporter_id, target_type, target_id) WHERE " + openReportPredicateSQL + ";"
	if !strings.Contains(mig, wantIndex) {
		t.Errorf("migration 011 does not build %q", wantIndex)
	}
	if !strings.Contains(mig, "LOCK TABLE moderation_reports IN SHARE ROW EXCLUSIVE MODE") {
		t.Error("migration 011 does not lock out writes between the fold and the index build")
	}
	if !strings.Contains(mig, "(keep.created_at, keep.id) < (dup.created_at, dup.id)") {
		t.Error("migration 011 does not keep the oldest open report")
	}
	ins := strings.Join(strings.Fields(createReportSQL), " ")
	wantConflict := "ON CONFLICT (reporter_id, target_type, target_id) WHERE " + openReportPredicateSQL + " DO NOTHING"
	if !strings.Contains(ins, wantConflict) {
		t.Errorf("createReportSQL lacks %q: %s", wantConflict, ins)
	}
	if !strings.HasSuffix(strings.Join(strings.Fields(openReportSQL), " "), "AND "+openReportPredicateSQL) {
		t.Errorf("openReportSQL does not select open reports only: %s", openReportSQL)
	}
	if openReportPredicateSQL != `status IN ('pending', 'reviewed')` {
		t.Errorf("open means pending or reviewed (AdminDecideReport, AdminStats): %s", openReportPredicateSQL)
	}
}
