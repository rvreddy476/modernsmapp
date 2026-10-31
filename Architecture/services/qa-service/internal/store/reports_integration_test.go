// DB-backed proof of migration 011 (one open report per reporter and
// target, 31 Oct 2026): it folds earlier duplicates into the oldest open
// report and then builds the partial unique index. TEST_PG_DSN on a "_test"
// database, CI only.
package store

import (
	"io/fs"
	"testing"
	"time"

	"github.com/atpost/qa-service/database"
	"github.com/google/uuid"
)

// The migration runs again here on a copy of the pre-index world, inside a
// transaction that is rolled back: the index is dropped (only in this
// transaction; other tests wait on its lock rather than see it missing),
// duplicates are seeded, and the migration's own SQL folds them and
// rebuilds the index.
func TestReportsIT_MigrationFoldsDuplicatesIntoTheOldest(t *testing.T) {
	r := setupContentIT(t)
	body, err := fs.ReadFile(database.Migrations, "migrations/011_qa_moderation_reports_open_once.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := r.pool.Begin(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(r.ctx) }()
	if _, err := tx.Exec(r.ctx, `DROP INDEX IF EXISTS uq_moderation_reports_open_once`); err != nil {
		t.Fatalf("drop index: %v", err)
	}

	reporter, target, otherTarget := uuid.New(), uuid.New(), uuid.New()
	base := time.Now().Add(-time.Hour)
	seed := func(target uuid.UUID, targetType, status string, age int) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := tx.QueryRow(r.ctx, `
			INSERT INTO moderation_reports (reporter_id, target_type, target_id, reason, status, created_at)
			VALUES ($1, $2, $3, 'spam', $4, $5) RETURNING id`,
			reporter, targetType, target, status, base.Add(time.Duration(age)*time.Minute)).Scan(&id); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return id
	}
	closedEarlier := seed(target, "answer", "resolved", 0)
	oldest := seed(target, "answer", "reviewed", 1)
	dup1 := seed(target, "answer", "pending", 2)
	dup2 := seed(target, "answer", "reviewed", 3)
	dismissedLater := seed(target, "answer", "dismissed", 4)
	sameIDOtherType := seed(target, "comment", "pending", 5)
	single := seed(otherTarget, "answer", "pending", 6)

	if _, err := tx.Exec(r.ctx, string(body)); err != nil {
		t.Fatalf("migration 011: %v", err)
	}

	status := func(id uuid.UUID) (string, bool) {
		t.Helper()
		var st string
		var resolved *time.Time
		if err := tx.QueryRow(r.ctx, `SELECT status, resolved_at FROM moderation_reports WHERE id = $1`, id).Scan(&st, &resolved); err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		return st, resolved != nil
	}
	for _, c := range []struct {
		name string
		id   uuid.UUID
		want string
	}{
		{"oldest open stays open", oldest, "reviewed"},
		{"later pending copy", dup1, "dismissed"},
		{"later reviewed copy", dup2, "dismissed"},
		{"earlier closed report untouched", closedEarlier, "resolved"},
		{"later closed report untouched", dismissedLater, "dismissed"},
		{"same id, another target type", sameIDOtherType, "pending"},
		{"single open report", single, "pending"},
	} {
		if got, _ := status(c.id); got != c.want {
			t.Errorf("%s: status %q, want %q", c.name, got, c.want)
		}
	}
	for _, id := range []uuid.UUID{dup1, dup2} {
		if _, resolved := status(id); !resolved {
			t.Errorf("folded copy %s has no resolved_at", id)
		}
	}
	var n int
	if err := tx.QueryRow(r.ctx, `SELECT COUNT(*)::int FROM moderation_reports WHERE reporter_id = $1`, reporter).Scan(&n); err != nil || n != 7 {
		t.Fatalf("the fold deleted rows: %d left, err=%v", n, err)
	}

	// The rebuilt index holds: a further open copy is refused.
	if _, err := tx.Exec(r.ctx, `SAVEPOINT dup`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(r.ctx, `INSERT INTO moderation_reports (reporter_id, target_type, target_id, reason) VALUES ($1, 'answer', $2, 'spam')`,
		reporter, target); err == nil {
		t.Fatal("an open duplicate was accepted after the migration")
	}
	if _, err := tx.Exec(r.ctx, `ROLLBACK TO SAVEPOINT dup`); err != nil {
		t.Fatal(err)
	}
}

// The store's insert, on the real index: a repeat returns the open report,
// and a report beside a closed one is new.
func TestReportsIT_StoreCreateReportIsIdempotentWhileOpen(t *testing.T) {
	r := setupContentIT(t)
	reporter, target := uuid.New(), uuid.New()
	first, created, err := r.st.CreateReport(r.ctx, reporter, "question", target, "spam", "")
	if err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	again, created, err := r.st.CreateReport(r.ctx, reporter, "question", target, "other", "")
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("repeat: created=%v err=%v id=%v, want %s", created, err, again, first.ID)
	}
	open, err := r.st.OpenReport(r.ctx, reporter, "question", target)
	if err != nil || open == nil || open.ID != first.ID {
		t.Fatalf("open report: %v %v", open, err)
	}
	if _, err := r.st.AdminDecideReport(r.ctx, uuid.New(), first.ID, "resolved", ""); err != nil {
		t.Fatal(err)
	}
	if open, err := r.st.OpenReport(r.ctx, reporter, "question", target); err != nil || open != nil {
		t.Fatalf("a resolved report is still open: %v %v", open, err)
	}
	next, created, err := r.st.CreateReport(r.ctx, reporter, "question", target, "spam", "")
	if err != nil || !created || next.ID == first.ID {
		t.Fatalf("after resolve: created=%v err=%v", created, err)
	}
}
