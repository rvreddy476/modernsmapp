// DB-backed proof of the report rules (31 Oct 2026): one open report per
// (reporter, target), idempotent repeats and races, a new report once the
// first is closed, and reports only on what the reporter can see and did
// not write. The decision is unit tested in guards_test.go; the SQL shape in
// store/reports_sql_test.go. TEST_PG_DSN on a "_test" database, CI only.
package service

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func (r *integrityRig) comment(answerID, author uuid.UUID) uuid.UUID {
	r.t.Helper()
	var id uuid.UUID
	if err := r.pool.QueryRow(r.ctx, `INSERT INTO answer_comments (answer_id, author_id, body) VALUES ($1, $2, 'comment') RETURNING id`,
		answerID, author).Scan(&id); err != nil {
		r.t.Fatalf("seed comment: %v", err)
	}
	return id
}

func (r *integrityRig) reportRows(reporter uuid.UUID, targetID uuid.UUID) (open, total int) {
	r.t.Helper()
	open = r.intOf(`SELECT COUNT(*)::int FROM moderation_reports WHERE reporter_id = $1 AND target_id = $2 AND status IN ('pending', 'reviewed')`, reporter, targetID)
	total = r.intOf(`SELECT COUNT(*)::int FROM moderation_reports WHERE reporter_id = $1 AND target_id = $2`, reporter, targetID)
	return open, total
}

// A repeat while the first report is open returns it and files nothing;
// once a moderator dismisses it, the reporter may report again.
func TestReportsIT_OneOpenReportPerTarget(t *testing.T) {
	r := setupIntegrityIT(t)
	asker, answerer, reporter := uuid.New(), uuid.New(), uuid.New()
	a := r.answer(r.question(asker, qOpt{}), answerer, false)

	first, created, err := r.svc.CreateReport(r.ctx, reporter, "answer", a, "spam", "first")
	if err != nil || !created {
		t.Fatalf("first report: created=%v err=%v", created, err)
	}
	for i := 0; i < 3; i++ {
		again, created, err := r.svc.CreateReport(r.ctx, reporter, "answer", a, "abuse", "again")
		if err != nil {
			t.Fatalf("repeat %d: %v", i, err)
		}
		if created || again.ID != first.ID || again.Reason != "spam" {
			t.Fatalf("repeat %d: created=%v id=%s reason=%q, want the first report %s back", i, created, again.ID, again.Reason, first.ID)
		}
	}
	if open, total := r.reportRows(reporter, a); open != 1 || total != 1 {
		t.Fatalf("after repeats: open=%d total=%d, want 1/1", open, total)
	}

	// Another reporter on the same answer is a separate report.
	other, created, err := r.svc.CreateReport(r.ctx, uuid.New(), "answer", a, "spam", "")
	if err != nil || !created || other.ID == first.ID {
		t.Fatalf("another reporter: created=%v err=%v", created, err)
	}

	if _, err := r.st.AdminDecideReport(r.ctx, uuid.New(), first.ID, "dismissed", "not spam"); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	second, created, err := r.svc.CreateReport(r.ctx, reporter, "answer", a, "spam", "it is back")
	if err != nil || !created || second.ID == first.ID {
		t.Fatalf("report after dismissal: created=%v err=%v", created, err)
	}
	if open, total := r.reportRows(reporter, a); open != 1 || total != 2 {
		t.Fatalf("after dismissal: open=%d total=%d, want 1/2", open, total)
	}
}

// Concurrent first reports from one reporter file one row, and every caller
// gets that row back.
func TestReportsIT_ConcurrentFirstReportsFileOne(t *testing.T) {
	r := setupIntegrityIT(t)
	reporter := uuid.New()
	q := r.question(uuid.New(), qOpt{})

	const n = 12
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, n)
	createdFlags := make([]bool, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rep, created, err := r.st.CreateReport(r.ctx, reporter, "question", q, "spam", "")
			errs[i], createdFlags[i] = err, created
			if rep != nil {
				ids[i] = rep.ID
			}
		}(i)
	}
	close(start)
	wg.Wait()
	created := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("caller %d got report %s, caller 0 got %s", i, ids[i], ids[0])
		}
		if createdFlags[i] {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d callers were told they created the report, want 1", created)
	}
	if open, total := r.reportRows(reporter, q); open != 1 || total != 1 {
		t.Fatalf("open=%d total=%d, want 1/1", open, total)
	}
}

// The database itself refuses a second open report on the same target, but
// not one beside a closed report, and not a second reporter.
func TestReportsIT_UniqueIndexOnOpenReports(t *testing.T) {
	r := setupIntegrityIT(t)
	reporter, target := uuid.New(), uuid.New()
	insert := func(who uuid.UUID, status string) error {
		_, err := r.pool.Exec(r.ctx, `INSERT INTO moderation_reports (reporter_id, target_type, target_id, reason, status) VALUES ($1, 'answer', $2, 'spam', $3)`,
			who, target, status)
		return err
	}
	if err := insert(reporter, "pending"); err != nil {
		t.Fatal(err)
	}
	if err := insert(reporter, "pending"); err == nil {
		t.Fatal("a second pending report on the same target was accepted")
	}
	if err := insert(reporter, "reviewed"); err == nil {
		t.Fatal("a reviewed report beside a pending one was accepted")
	}
	if err := insert(reporter, "dismissed"); err != nil {
		t.Fatalf("a dismissed report beside an open one: %v", err)
	}
	if err := insert(reporter, "resolved"); err != nil {
		t.Fatalf("a resolved report beside an open one: %v", err)
	}
	if err := insert(uuid.New(), "pending"); err != nil {
		t.Fatalf("another reporter: %v", err)
	}
	var valid bool
	if err := r.pool.QueryRow(r.ctx, `
		SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
		WHERE c.relname = 'uq_moderation_reports_open_once'`).Scan(&valid); err != nil || !valid {
		t.Fatalf("uq_moderation_reports_open_once valid=%v err=%v", valid, err)
	}
}

// Only what the reporter can see, and did not write: anything hidden is the
// same 404 (never a 403 that confirms it exists), and nothing is filed.
func TestReportsIT_TargetMustBeVisibleAndNotOwn(t *testing.T) {
	r := setupIntegrityIT(t)
	asker, answerer, commenter, reporter := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	q := r.question(asker, qOpt{})
	a := r.answer(q, answerer, false)
	cm := r.comment(a, commenter)

	priv := r.community("private")
	privQ := r.question(asker, qOpt{community: &priv})
	privA := r.answer(privQ, answerer, false)
	privC := r.comment(privA, commenter)
	pub := r.community("public")
	pendingQ := r.question(asker, qOpt{community: &pub, status: "pending_approval"})
	deletedA := r.answer(q, answerer, false)
	if _, err := r.pool.Exec(r.ctx, `UPDATE answers SET deleted_at = now() WHERE id = $1`, deletedA); err != nil {
		t.Fatal(err)
	}
	deletedC := r.comment(a, commenter)
	if _, err := r.pool.Exec(r.ctx, `UPDATE answer_comments SET deleted_at = now() WHERE id = $1`, deletedC); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(r.ctx, `INSERT INTO qa_profiles (user_id) VALUES ($1), ($2) ON CONFLICT DO NOTHING`, answerer, reporter); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name, targetType string
		target           uuid.UUID
		want             string
	}{
		{"missing question", "question", uuid.New(), "not_found: question not found"},
		{"missing answer", "answer", uuid.New(), "not_found: answer not found"},
		{"missing comment", "comment", uuid.New(), "not_found: comment not found"},
		{"user without a Q&A profile", "user", uuid.New(), "not_found: user not found"},
		{"private community question", "question", privQ, "not_found: question not found"},
		{"answer in a private community", "answer", privA, "not_found: answer not found"},
		{"comment in a private community", "comment", privC, "not_found: comment not found"},
		{"question awaiting approval", "question", pendingQ, "not_found: question not found"},
		{"deleted answer", "answer", deletedA, "not_found: answer not found"},
		{"deleted comment", "comment", deletedC, "not_found: comment not found"},
		{"oneself", "user", reporter, "invalid: you cannot report yourself"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := r.svc.CreateReport(r.ctx, reporter, c.targetType, c.target, "spam", "")
			if err == nil || err.Error() != c.want {
				t.Fatalf("got %v, want %q", err, c.want)
			}
			if _, total := r.reportRows(reporter, c.target); total != 0 {
				t.Fatalf("a refused report filed %d rows", total)
			}
		})
	}
	for _, c := range []struct {
		name, targetType string
		author           uuid.UUID
		target           uuid.UUID
		want             string
	}{
		{"own question", "question", asker, q, "invalid: you cannot report your own question"},
		{"own answer", "answer", answerer, a, "invalid: you cannot report your own answer"},
		{"own comment", "comment", commenter, cm, "invalid: you cannot report your own comment"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := r.svc.CreateReport(r.ctx, c.author, c.targetType, c.target, "spam", "")
			if err == nil || err.Error() != c.want {
				t.Fatalf("got %v, want %q", err, c.want)
			}
		})
	}

	// What the reporter can see, written by someone else, is filed.
	for _, c := range []struct {
		targetType string
		target     uuid.UUID
	}{{"question", q}, {"answer", a}, {"comment", cm}, {"user", answerer}} {
		if _, created, err := r.svc.CreateReport(r.ctx, reporter, c.targetType, c.target, "spam", ""); err != nil || !created {
			t.Fatalf("%s: created=%v err=%v", c.targetType, created, err)
		}
	}
	// A member of the private community may report what it holds.
	r.member(priv, reporter, "member")
	if _, created, err := r.svc.CreateReport(r.ctx, reporter, "answer", privA, "spam", ""); err != nil || !created {
		t.Fatalf("member reporting a private-community answer: created=%v err=%v", created, err)
	}
}

// A repeat is answered before the daily report limit is counted: only the
// report that filed a row used budget.
func TestReportsIT_RepeatDoesNotSpendTheReportLimit(t *testing.T) {
	r := setupIntegrityIT(t)
	lim := &countingLimiter{hits: map[string]int{}}
	r.svc.WithRateLimiter(lim)
	reporter := uuid.New()
	a := r.answer(r.question(uuid.New(), qOpt{}), uuid.New(), false)
	for i := 0; i < 5; i++ {
		if _, _, err := r.svc.CreateReport(r.ctx, reporter, "answer", a, "spam", ""); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	key := "qa:rl:report:86400:" + reporter.String()
	if got := lim.hits[key]; got != 1 {
		t.Fatalf("%d hits counted against %s, want 1 (the repeats spent budget)", got, key)
	}
}
