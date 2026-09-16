//go:build integration

package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const injectAuditFailure = "inject-audit-failure"

func auditRows(t *testing.T, store *postgres.ReportStore, targetType string, id uuid.UUID) []postgres.AuditEntry {
	t.Helper()
	rows, err := store.ListAudit(context.Background(), targetType, id)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// installAuditFailure makes any audit insert whose reason is
// injectAuditFailure raise, so the change it belongs to must roll back.
func installAuditFailure(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	runLockedDDL(t, pool,
		`DROP TRIGGER IF EXISTS it_fail_audit ON trust.admin_audit`,
		`CREATE OR REPLACE FUNCTION trust.it_fail_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN IF NEW.reason = 'inject-audit-failure' THEN RAISE EXCEPTION 'injected audit failure'; END IF; RETURN NEW; END $$`,
		`CREATE TRIGGER it_fail_audit BEFORE INSERT ON trust.admin_audit FOR EACH ROW EXECUTE FUNCTION trust.it_fail_audit()`,
	)
	t.Cleanup(func() {
		runLockedDDL(t, pool,
			`DROP TRIGGER IF EXISTS it_fail_audit ON trust.admin_audit`,
			`DROP FUNCTION IF EXISTS trust.it_fail_audit()`)
	})
}

// runLockedDDL applies statements in one transaction under the schema
// setup advisory lock, so it never interleaves with another package's setup.
func runLockedDDL(t *testing.T, pool *pgxpool.Pool, stmts ...string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7710010)`); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func adminMeta(id uuid.UUID, reason string) postgres.AuditMeta {
	return postgres.AuditMeta{Actor: postgres.UserActor(id), Reason: reason, RequestID: "req-" + id.String()[:8]}
}

func TestReportChangesAreAuditedWithRealActorInSameTransaction(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := New(store, nil)
	report, err := svc.FileReport(ctx, uuid.New(), uuid.New(), "post", "spam", "")
	if err != nil {
		t.Fatal(err)
	}

	// Missing actor: refused, nothing changes, nothing is audited.
	if _, err := svc.UpdateReport(ctx, postgres.AuditMeta{}, report.ID.String(), "reviewing", nil, ""); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("no actor err=%v", err)
	}
	if got, _ := store.GetReport(ctx, report.ID); got.Status != "open" {
		t.Fatalf("status changed without actor: %s", got.Status)
	}
	if rows := auditRows(t, store, postgres.AuditTargetReport, report.ID); len(rows) != 0 {
		t.Fatalf("audit rows without actor: %d", len(rows))
	}

	admin, assignee := uuid.New(), uuid.New()
	if _, err := svc.UpdateReport(ctx, adminMeta(admin, ""), report.ID.String(), "reviewing", &assignee, ""); err != nil {
		t.Fatal(err)
	}
	rows := auditRows(t, store, postgres.AuditTargetReport, report.ID)
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1", len(rows))
	}
	r := rows[0]
	if r.ActorType != "user" || r.ActorUserID == nil || *r.ActorUserID != admin || r.ActorService != nil {
		t.Fatalf("actor=%s/%v/%v", r.ActorType, r.ActorUserID, r.ActorService)
	}
	if r.Action != "report.reviewing" || *r.PrevStatus != "open" || *r.NewStatus != "reviewing" ||
		r.PrevAssignee != nil || r.NewAssignee == nil || *r.NewAssignee != assignee ||
		r.RequestID == nil || *r.RequestID != "req-"+admin.String()[:8] {
		t.Fatalf("row=%+v", r)
	}

	// Audit failure rolls the report change back.
	installAuditFailure(t, pool)
	if _, err := svc.UpdateReport(ctx, adminMeta(admin, injectAuditFailure), report.ID.String(), "resolved", &assignee, injectAuditFailure); err == nil {
		t.Fatal("change committed although its audit row failed")
	}
	if got, _ := store.GetReport(ctx, report.ID); got.Status != "reviewing" || got.ResolvedAt != nil {
		t.Fatalf("report changed despite audit failure: %s", got.Status)
	}
	if rows := auditRows(t, store, postgres.AuditTargetReport, report.ID); len(rows) != 1 {
		t.Fatalf("rows after failed change=%d want 1", len(rows))
	}

	// Invalid transition: refused, no audit row.
	if _, err := svc.UpdateReport(ctx, adminMeta(admin, ""), report.ID.String(), "reviewing", nil, ""); !errors.Is(err, postgres.ErrInvalidTransition) {
		t.Fatalf("invalid transition err=%v", err)
	}
	if _, err := svc.UpdateReport(ctx, adminMeta(admin, ""), report.ID.String(), "resolved", &assignee, "done"); err != nil {
		t.Fatal(err)
	}
	rows = auditRows(t, store, postgres.AuditTargetReport, report.ID)
	if len(rows) != 2 || rows[1].Action != "report.resolved" || *rows[1].PrevStatus != "reviewing" || *rows[1].NewResolution != "done" {
		t.Fatalf("second row=%+v (n=%d)", rows[len(rows)-1], len(rows))
	}
}

func TestAppealReviewIsAuditedWithRealActorInSameTransaction(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	owner, postID := uuid.New(), uuid.New()
	svc := New(store, nil)
	svc.SetExtrasStore(postgres.NewExtrasStore(pool))
	svc.SetPostModerationClient(&fakePostModeration{subject: &PostModerationSubject{PostID: postID, AuthorID: owner, ReviewStatus: "rejected", ContentRevision: 1}})
	appeal, err := svc.SubmitAppeal(ctx, owner, "post", postID.String(), "please")
	if err != nil {
		t.Fatal(err)
	}
	status := func() string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT status FROM trust.content_appeals WHERE id=$1`, appeal.ID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}

	if err := svc.ReviewAppeal(ctx, appeal.ID, "under_review", "", postgres.AuditMeta{}); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("no actor err=%v", err)
	}
	if err := svc.ReviewAppeal(ctx, appeal.ID, "under_review", "", postgres.AuditMeta{Actor: postgres.ServiceActor("some-service")}); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("service reviewer err=%v", err)
	}
	if status() != "open" || len(auditRows(t, store, postgres.AuditTargetAppeal, appeal.ID)) != 0 {
		t.Fatal("appeal changed or audited without a human actor")
	}

	installAuditFailure(t, pool)
	reviewer := uuid.New()
	if err := svc.ReviewAppeal(ctx, appeal.ID, "under_review", injectAuditFailure, adminMeta(reviewer, injectAuditFailure)); err == nil {
		t.Fatal("appeal change committed although its audit row failed")
	}
	if status() != "open" || len(auditRows(t, store, postgres.AuditTargetAppeal, appeal.ID)) != 0 {
		t.Fatal("appeal changed despite audit failure")
	}

	if err := svc.ReviewAppeal(ctx, appeal.ID, "under_review", "looking", adminMeta(reviewer, "looking")); err != nil {
		t.Fatal(err)
	}
	rows := auditRows(t, store, postgres.AuditTargetAppeal, appeal.ID)
	if len(rows) != 1 || rows[0].ActorUserID == nil || *rows[0].ActorUserID != reviewer ||
		rows[0].Action != "appeal.under_review" || *rows[0].PrevStatus != "open" || *rows[0].NewStatus != "under_review" ||
		*rows[0].Reason != "looking" {
		t.Fatalf("appeal rows=%+v", rows)
	}
	if status() != "under_review" {
		t.Fatalf("status=%s", status())
	}
}

func TestGrievanceAssignmentHistoryKeepsEveryChangeInOrder(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := New(store, nil)
	g, err := svc.FileGrievance(ctx, uuid.New(), "account", "", "", "my account was taken over")
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := uuid.New(), uuid.New(), uuid.New()

	if _, err := svc.UpdateGrievance(ctx, g.ID, GrievanceChange{Status: "acknowledged", AssignedTo: &a}, postgres.AuditMeta{}); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("no actor err=%v", err)
	}
	if got, _ := store.GetGrievance(ctx, g.ID); got.Status != "open" || got.AssignedTo != nil {
		t.Fatal("grievance changed without actor")
	}

	installAuditFailure(t, pool)
	fail := injectAuditFailure
	if _, err := svc.UpdateGrievance(ctx, g.ID, GrievanceChange{Status: "acknowledged", AssignedTo: &a, Notes: &fail}, adminMeta(a, fail)); err == nil {
		t.Fatal("grievance change committed although its audit row failed")
	}
	if got, _ := store.GetGrievance(ctx, g.ID); got.Status != "open" || got.AssignedTo != nil || got.AcknowledgedAt != nil {
		t.Fatal("grievance changed despite audit failure")
	}

	steps := []struct {
		actor  uuid.UUID
		change GrievanceChange
	}{
		{a, GrievanceChange{Status: "acknowledged", AssignedTo: &a}}, // A takes it
		{a, GrievanceChange{AssignedTo: &b}},                         // A hands to B
		{uuid.New(), GrievanceChange{AssignedTo: &c}},                // a lead moves it to C
		{c, GrievanceChange{Status: "resolved", AssignedTo: &c}},     // C resolves
	}
	for i, s := range steps {
		if _, err := svc.UpdateGrievance(ctx, g.ID, s.change, adminMeta(s.actor, "")); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	// A terminal grievance cannot be reassigned; nothing is recorded.
	if _, err := svc.UpdateGrievance(ctx, g.ID, GrievanceChange{AssignedTo: &a}, adminMeta(a, "")); !errors.Is(err, postgres.ErrInvalidTransition) {
		t.Fatalf("reassign resolved err=%v", err)
	}

	history, err := svc.GrievanceHistory(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(steps) {
		t.Fatalf("history=%d want %d", len(history), len(steps))
	}
	wantFrom := []*uuid.UUID{nil, &a, &b, &c}
	wantTo := []uuid.UUID{a, b, c, c}
	wantAction := []string{"grievance.acknowledged", "grievance.assigned", "grievance.assigned", "grievance.resolved"}
	for i, h := range history {
		if i > 0 && h.Seq <= history[i-1].Seq {
			t.Fatalf("history out of order at %d", i)
		}
		if h.ActorUserID == nil || *h.ActorUserID != steps[i].actor {
			t.Fatalf("row %d actor=%v want %s", i, h.ActorUserID, steps[i].actor)
		}
		if h.Action != wantAction[i] {
			t.Fatalf("row %d action=%s want %s", i, h.Action, wantAction[i])
		}
		if (wantFrom[i] == nil) != (h.PrevAssignee == nil) || (wantFrom[i] != nil && *h.PrevAssignee != *wantFrom[i]) {
			t.Fatalf("row %d from=%v want %v", i, h.PrevAssignee, wantFrom[i])
		}
		if h.NewAssignee == nil || *h.NewAssignee != wantTo[i] {
			t.Fatalf("row %d to=%v want %s", i, h.NewAssignee, wantTo[i])
		}
	}
	final, _ := store.GetGrievance(ctx, g.ID)
	if final.AssignedTo == nil || *final.AssignedTo != c || final.Status != "resolved" {
		t.Fatalf("current officer=%v status=%s", final.AssignedTo, final.Status)
	}

	// The audit table is append-only.
	if _, err := pool.Exec(ctx, `UPDATE trust.admin_audit SET action='x' WHERE target_id=$1`, g.ID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("update err=%v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM trust.admin_audit WHERE target_id=$1`, g.ID); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("delete err=%v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO trust.admin_audit(actor_type, action, target_type, target_id) VALUES ('user','x','grievance',$1)`, g.ID); err == nil {
		t.Fatal("user audit row without an actor id was accepted")
	}
}

func TestOverdueGrievancesAreOnlyUnresolvedPastDue(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := New(store, nil)
	past, future := time.Now().Add(-48*time.Hour), time.Now().Add(48*time.Hour)
	ids := map[string]uuid.UUID{}
	for _, row := range []struct {
		name, status string
		due          time.Time
	}{
		{"open_past", "open", past},
		{"ack_past", "acknowledged", past.Add(time.Hour)},
		{"resolved_past", "resolved", past},
		{"rejected_past", "rejected", past},
		{"open_future", "open", future},
	} {
		id := uuid.New()
		ids[row.name] = id
		if _, err := pool.Exec(ctx, `INSERT INTO trust.grievances (id, complainant_id, subject, description, status, due_at)
			VALUES ($1, $2, 'other', 'x', $3, $4)`, id, uuid.New(), row.status, row.due); err != nil {
			t.Fatal(err)
		}
	}
	list, err := svc.ListOverdueGrievances(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uuid.UUID]int{}
	for i, g := range list {
		if g.Status != "open" && g.Status != "acknowledged" {
			t.Fatalf("resolved grievance in overdue list: %s", g.Status)
		}
		if !g.DueAt.Before(time.Now()) {
			t.Fatalf("not-yet-due grievance in overdue list: %s", g.DueAt)
		}
		if i > 0 && g.DueAt.Before(list[i-1].DueAt) {
			t.Fatal("overdue list not ordered by due_at")
		}
		seen[g.ID] = i
	}
	_, openPast := seen[ids["open_past"]]
	_, ackPast := seen[ids["ack_past"]]
	if !openPast || !ackPast {
		t.Fatalf("missing overdue grievances: open=%v ack=%v", openPast, ackPast)
	}
	for _, n := range []string{"resolved_past", "rejected_past", "open_future"} {
		if _, ok := seen[ids[n]]; ok {
			t.Fatalf("%s must not be overdue", n)
		}
	}
	if seen[ids["open_past"]] > seen[ids["ack_past"]] {
		t.Fatal("most overdue grievance must come first")
	}
}

func TestDatingReportGrievanceIsAuditedAsTheService(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := New(store, nil)
	in := DatingReportGrievanceInput{ReportID: uuid.New(), ReporterID: uuid.New(), TargetID: uuid.New(), Reason: "harassment", ReportedAt: time.Now()}
	g, created, err := svc.LinkDatingReportGrievance(ctx, in, "req-dating")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	again, created, err := svc.LinkDatingReportGrievance(ctx, in, "req-dating-retry")
	if err != nil || created || again.ID != g.ID {
		t.Fatalf("retry created=%v err=%v", created, err)
	}
	rows := auditRows(t, store, postgres.AuditTargetGrievance, g.ID)
	if len(rows) != 1 {
		t.Fatalf("rows=%d want 1", len(rows))
	}
	r := rows[0]
	if r.ActorType != "service" || r.ActorService == nil || *r.ActorService != DatingServiceActor || r.ActorUserID != nil ||
		r.Action != "grievance.created" || *r.NewStatus != "open" || *r.RequestID != "req-dating" {
		t.Fatalf("row=%+v", r)
	}
}
