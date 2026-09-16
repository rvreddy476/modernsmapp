// DB-backed admin-service token tests (admin console Wave 2 — Content apps,
// Q&A): every token-path write lands with exactly one moderation_actions row
// whose actor is the token's signed act claim, in the same transaction; the
// legacy allowlist route still writes; the stats route counts what is seeded.
// Requires TEST_PG_DSN on a database whose name ends in "_test" (qa_it_test);
// skipped when unset.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/qa-service/database"
	"github.com/atpost/qa-service/internal/service"
	"github.com/atpost/qa-service/internal/store"
	pgstore "github.com/atpost/qa-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testDatabaseName refuses any DSN whose database does not end in _test.
func testDatabaseName(dsn string) (string, bool) {
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		return "", false
	}
	return cfg.Database, true
}

func TestTestDatabaseGuard(t *testing.T) {
	for name, c := range map[string]struct {
		dsn string
		ok  bool
	}{
		"qa_it_test":   {"postgres://u:p@localhost:5432/qa_it_test", true},
		"atpost":       {"postgres://u:p@localhost:5432/atpost", false},
		"qa_test_copy": {"postgres://u:p@localhost:5432/qa_test_copy", false},
		"unparseable":  {"postgres://u:p@localhost:bad/qa_it_test", false},
	} {
		if _, got := testDatabaseName(c.dsn); got != c.ok {
			t.Fatalf("guard(%s) = %v, want %v", name, got, c.ok)
		}
	}
}

func setupAdminTokenIT(t *testing.T) (*adminTokenRig, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping qa-service admin token integration tests")
	}
	if _, ok := testDatabaseName(dsn); !ok {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pgstore.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rg := newAdminTokenRig(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	New(service.New(store.New(pool), nil)).WithServiceAuth(rg.v).RegisterRoutes(r)
	rg.r = r
	return rg, pool
}

func itQuestion(t *testing.T, pool *pgxpool.Pool, createdAt time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO questions (author_id, title, slug, created_at, updated_at)
		VALUES ($1, 'IT admin question', $2, $3, $3) RETURNING id`,
		uuid.New(), "it-admin-"+uuid.NewString(), createdAt).Scan(&id); err != nil {
		t.Fatalf("seed question: %v", err)
	}
	return id
}

func itAnswer(t *testing.T, pool *pgxpool.Pool, questionID uuid.UUID, createdAt time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO answers (question_id, author_id, body, created_at, updated_at)
		VALUES ($1, $2, 'IT answer', $3, $3) RETURNING id`, questionID, uuid.New(), createdAt).Scan(&id); err != nil {
		t.Fatalf("seed answer: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE questions SET answer_count = answer_count + 1 WHERE id = $1`, questionID); err != nil {
		t.Fatal(err)
	}
	return id
}

func itComment(t *testing.T, pool *pgxpool.Pool, answerID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var id uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO answer_comments (answer_id, author_id, body) VALUES ($1, $2, 'IT comment') RETURNING id`,
		answerID, uuid.New()).Scan(&id); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE answers SET comment_count = comment_count + 1 WHERE id = $1`, answerID); err != nil {
		t.Fatal(err)
	}
	return id
}

func itReport(t *testing.T, pool *pgxpool.Pool, targetType string, targetID uuid.UUID, reason, status string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO moderation_reports (reporter_id, target_type, target_id, reason, status)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, uuid.New(), targetType, targetID, reason, status).Scan(&id); err != nil {
		t.Fatalf("seed report: %v", err)
	}
	return id
}

type actionRow struct {
	Actor    uuid.UUID
	Action   string
	ReportID *uuid.UUID
	Source   string
}

func actionRowsFor(t *testing.T, pool *pgxpool.Pool, targetID uuid.UUID) []actionRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT actor_id, action_type, report_id, COALESCE(metadata->>'source', '')
		FROM moderation_actions WHERE target_id = $1 ORDER BY created_at`, targetID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []actionRow
	for rows.Next() {
		var a actionRow
		if err := rows.Scan(&a.Actor, &a.Action, &a.ReportID, &a.Source); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func envelopeData(t *testing.T, w *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, w.Body.String())
	}
	return env.Data
}

// itCall sends one admin-service token request with a forged gateway identity
// and the key riding along.
func itCall(t *testing.T, rg *adminTokenRig, perm, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	hdr := gatewayUser(uuid.New())
	hdr["X-Admin-Id"] = uuid.NewString()
	hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceQA, []string{perm}, rg.actor.String())
	return adminServe(rg.r, method, InternalAdminPrefix+path, body, hdr)
}

func TestAdminTokenIT_EveryWriteRecordsActOnce(t *testing.T) {
	rg, pool := setupAdminTokenIT(t)
	ctx := context.Background()
	now := time.Now()

	qHide := itQuestion(t, pool, now)
	qLock := itQuestion(t, pool, now)
	qDup, qDupOf := itQuestion(t, pool, now), itQuestion(t, pool, now)
	qMerge, qInto := itQuestion(t, pool, now), itQuestion(t, pool, now)
	qParent := itQuestion(t, pool, now)
	aHide := itAnswer(t, pool, qParent, now)
	aParent := itAnswer(t, pool, qParent, now)
	cHide := itComment(t, pool, aParent)
	resolveTarget, dismissTarget := uuid.New(), uuid.New()
	rResolve := itReport(t, pool, "user", resolveTarget, "it-abuse", "pending")
	rDismiss := itReport(t, pool, "answer", dismissTarget, "it-spam", "reviewed")
	rLinked := itReport(t, pool, "question", qHide, "it-spam", "pending")

	expectOne := func(target uuid.UUID, action string, report *uuid.UUID) {
		t.Helper()
		rows := actionRowsFor(t, pool, target)
		if len(rows) != 1 {
			t.Fatalf("%s: %d moderation_actions rows for %s, want exactly 1: %+v", action, len(rows), target, rows)
		}
		got := rows[0]
		if got.Actor != rg.actor || got.Action != action || got.Source != store.AdminActionSource {
			t.Fatalf("%s row = %+v, want actor %s source %s", action, got, rg.actor, store.AdminActionSource)
		}
		if (report == nil) != (got.ReportID == nil) || (report != nil && *report != *got.ReportID) {
			t.Fatalf("%s row report_id = %v, want %v", action, got.ReportID, report)
		}
	}
	ok := func(w *httptest.ResponseRecorder, what string) {
		t.Helper()
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s, want 200", what, w.Code, w.Body.String())
		}
	}
	scalar := func(sql string, args ...any) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&s); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return s
	}

	// A refused token writes nothing.
	wrong := bearer(rg.mint(t, rg.admin, AudienceQA, []string{PermQuestionsModerate}, rg.actor.String()))
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/questions/"+qMerge.String()+"/merge",
		`{"reason":"IT","merge_into_id":"`+qInto.String()+`"}`, wrong); w.Code != http.StatusForbidden {
		t.Fatalf("merge with questions.moderate: status=%d, want 403", w.Code)
	}
	if rows := actionRowsFor(t, pool, qMerge); len(rows) != 0 || scalar(`SELECT status FROM questions WHERE id = $1`, qMerge) != "open" {
		t.Fatalf("a refused merge changed something: rows=%d", len(rows))
	}

	ok(itCall(t, rg, PermReportsAct, http.MethodPost, "/reports/"+rResolve.String()+"/resolve", `{"reason":"IT resolved"}`), "resolve")
	expectOne(resolveTarget, store.ActionReportResolve, &rResolve)
	if got := scalar(`SELECT status || ':' || reviewed_by::text FROM moderation_reports WHERE id = $1`, rResolve); got != "resolved:"+rg.actor.String() {
		t.Fatalf("resolved report = %s, want resolved by act", got)
	}

	ok(itCall(t, rg, PermReportsAct, http.MethodPost, "/reports/"+rDismiss.String()+"/dismiss", `{"reason":"IT dismissed"}`), "dismiss")
	expectOne(dismissTarget, store.ActionReportDismiss, &rDismiss)

	ok(itCall(t, rg, PermQuestionsModerate, http.MethodPost, "/questions/"+qHide.String()+"/hide",
		`{"reason":"IT hide","report_id":"`+rLinked.String()+`"}`), "hide question")
	expectOne(qHide, store.ActionHide, &rLinked)
	if got := scalar(`SELECT status || ':' || (deleted_at IS NOT NULL)::text FROM questions WHERE id = $1`, qHide); got != "deleted:true" {
		t.Fatalf("hidden question = %s", got)
	}

	ok(itCall(t, rg, PermQuestionsModerate, http.MethodPost, "/questions/"+qLock.String()+"/lock", `{"reason":"IT lock"}`), "lock")
	expectOne(qLock, store.ActionLock, nil)
	if got := scalar(`SELECT status || ':' || closed_by::text FROM questions WHERE id = $1`, qLock); got != "closed:"+rg.actor.String() {
		t.Fatalf("locked question = %s, want closed by act", got)
	}

	ok(itCall(t, rg, PermQuestionsModerate, http.MethodPost, "/questions/"+qDup.String()+"/duplicate",
		`{"reason":"IT dup","duplicate_of_id":"`+qDupOf.String()+`"}`), "duplicate")
	expectOne(qDup, store.ActionMarkDuplicate, nil)
	if got := scalar(`SELECT marked_by::text FROM question_duplicates WHERE question_id = $1`, qDup); got != rg.actor.String() {
		t.Fatalf("duplicate marked_by = %s, want act", got)
	}

	ok(itCall(t, rg, PermQuestionsMerge, http.MethodPost, "/questions/"+qMerge.String()+"/merge",
		`{"reason":"IT merge","merge_into_id":"`+qInto.String()+`"}`), "merge")
	expectOne(qMerge, store.ActionMerge, nil)
	if got := scalar(`SELECT status || ':' || merged_into_id::text FROM questions WHERE id = $1`, qMerge); got != "merged:"+qInto.String() {
		t.Fatalf("merged question = %s", got)
	}

	ok(itCall(t, rg, PermAnswersModerate, http.MethodPost, "/answers/"+aHide.String()+"/hide", `{"reason":"IT hide answer"}`), "hide answer")
	expectOne(aHide, store.ActionHide, nil)
	if got := scalar(`SELECT answer_count::text FROM questions WHERE id = $1`, qParent); got != "1" {
		t.Fatalf("answer_count after hide = %s, want 1", got)
	}

	ok(itCall(t, rg, PermCommentsModerate, http.MethodPost, "/comments/"+cHide.String()+"/hide", `{"reason":"IT hide comment"}`), "hide comment")
	expectOne(cHide, store.ActionHide, nil)
	if got := scalar(`SELECT comment_count::text FROM answers WHERE id = $1`, aParent); got != "0" {
		t.Fatalf("comment_count after hide = %s, want 0", got)
	}

	// Repeats conflict and write no second row; a missing target is 404 and writes none.
	repeats := []struct{ perm, path, body string }{
		{PermQuestionsModerate, "/questions/" + qHide.String() + "/hide", `{"reason":"again"}`},
		{PermQuestionsModerate, "/questions/" + qLock.String() + "/lock", `{"reason":"again"}`},
		{PermQuestionsModerate, "/questions/" + qDup.String() + "/duplicate", `{"reason":"again","duplicate_of_id":"` + qDupOf.String() + `"}`},
		{PermQuestionsMerge, "/questions/" + qMerge.String() + "/merge", `{"reason":"again","merge_into_id":"` + qInto.String() + `"}`},
		{PermAnswersModerate, "/answers/" + aHide.String() + "/hide", `{"reason":"again"}`},
		{PermCommentsModerate, "/comments/" + cHide.String() + "/hide", `{"reason":"again"}`},
		{PermReportsAct, "/reports/" + rResolve.String() + "/dismiss", `{"reason":"again"}`},
	}
	for _, rp := range repeats {
		if w := itCall(t, rg, rp.perm, http.MethodPost, rp.path, rp.body); w.Code != http.StatusConflict {
			t.Fatalf("repeat %s: status=%d body=%s, want 409", rp.path, w.Code, w.Body.String())
		}
	}
	for target, action := range map[uuid.UUID]string{qHide: store.ActionHide, qLock: store.ActionLock, qDup: store.ActionMarkDuplicate,
		qMerge: store.ActionMerge, aHide: store.ActionHide, cHide: store.ActionHide, resolveTarget: store.ActionReportResolve} {
		if rows := actionRowsFor(t, pool, target); len(rows) != 1 {
			t.Fatalf("%s after repeat: %d rows, want 1", action, len(rows))
		}
	}
	ghost := uuid.New()
	if w := itCall(t, rg, PermAnswersModerate, http.MethodPost, "/answers/"+ghost.String()+"/hide", `{"reason":"ghost"}`); w.Code != http.StatusNotFound {
		t.Fatalf("hide missing answer: status=%d, want 404", w.Code)
	}
	if rows := actionRowsFor(t, pool, ghost); len(rows) != 0 {
		t.Fatalf("a missing target wrote %d rows", len(rows))
	}

	// Same transaction: when the audit insert fails (report_id names no
	// report), the hide rolls back too.
	qAtomic := itQuestion(t, pool, now)
	if w := itCall(t, rg, PermQuestionsModerate, http.MethodPost, "/questions/"+qAtomic.String()+"/hide",
		`{"reason":"IT atomic","report_id":"`+uuid.NewString()+`"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("hide with a dangling report_id: status=%d body=%s, want 400", w.Code, w.Body.String())
	}
	if got := scalar(`SELECT status || ':' || (deleted_at IS NOT NULL)::text FROM questions WHERE id = $1`, qAtomic); got != "open:false" {
		t.Fatalf("question after a failed audit insert = %s, want open:false (rolled back)", got)
	}
	if rows := actionRowsFor(t, pool, qAtomic); len(rows) != 0 {
		t.Fatalf("failed hide wrote %d rows", len(rows))
	}

	// History read, narrowed to the actor.
	w := itCall(t, rg, PermAuditRead, http.MethodGet, "/actions?actor_id="+rg.actor.String()+"&limit=100", "")
	ok(w, "actions")
	var hist []store.ModerationAction
	if err := json.Unmarshal(envelopeData(t, w), &hist); err != nil {
		t.Fatal(err)
	}
	if len(hist) != 8 {
		t.Fatalf("history for act: %d rows, want 8", len(hist))
	}
}

// The LEGACY allowlist route still works on a real database, with the
// X-User-ID moderator as the actor.
func TestAdminTokenIT_LegacyAllowlistLockStillWrites(t *testing.T) {
	moderator := uuid.New()
	t.Setenv("QA_MODERATOR_USER_IDS", moderator.String())
	rg, pool := setupAdminTokenIT(t)
	q := itQuestion(t, pool, time.Now())
	hdr := gatewayUser(moderator)
	delete(hdr, "X-Internal-Service-Key")
	w := adminServe(rg.r, http.MethodPost, "/v1/qa/admin/questions/"+q.String()+"/lock", `{"reason":"legacy"}`, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy lock: status=%d body=%s", w.Code, w.Body.String())
	}
	rows := actionRowsFor(t, pool, q)
	if len(rows) != 1 || rows[0].Actor != moderator || rows[0].Action != store.ActionLock {
		t.Fatalf("legacy rows = %+v, want one lock by %s", rows, moderator)
	}
	if w := adminServe(rg.r, http.MethodPost, "/v1/qa/admin/questions/"+q.String()+"/lock", `{"reason":"legacy"}`, gatewayUser(uuid.New())); w.Code != http.StatusForbidden {
		t.Fatalf("legacy non-moderator: status=%d, want 403", w.Code)
	}
}

func adminStatsVia(t *testing.T, rg *adminTokenRig) store.AdminStats {
	t.Helper()
	w := itCall(t, rg, PermStatsRead, http.MethodGet, "/stats", "")
	if w.Code != http.StatusOK {
		t.Fatalf("stats: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	var out store.AdminStats
	if err := json.Unmarshal(envelopeData(t, w), &out); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	return out
}

func TestAdminTokenIT_Stats(t *testing.T) {
	rg, pool := setupAdminTokenIT(t)
	ctx := context.Background()
	now := time.Now()

	// Token only: a gateway superadmin gets nothing; another permission neither.
	if w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/stats", "", gatewayUser(uuid.New())); w.Code != http.StatusUnauthorized {
		t.Fatalf("stats without a token: status=%d, want 401", w.Code)
	}
	if w := itCall(t, rg, PermAuditRead, http.MethodGet, "/stats", ""); w.Code != http.StatusForbidden {
		t.Fatalf("stats with audit.read: status=%d, want 403", w.Code)
	}

	before := adminStatsVia(t, rg)

	// Questions: two today, one three days ago, one ten days ago.
	qToday1, qToday2 := itQuestion(t, pool, now), itQuestion(t, pool, now)
	qOld3 := itQuestion(t, pool, now.Add(-72*time.Hour))
	itQuestion(t, pool, now.Add(-240*time.Hour))
	// Answers: one today, one three days ago, one ten days ago.
	aToday := itAnswer(t, pool, qToday1, now)
	itAnswer(t, pool, qToday1, now.Add(-72*time.Hour))
	itAnswer(t, pool, qToday1, now.Add(-240*time.Hour))

	// Hidden in 7 days: one question and one answer by the console; an old
	// hide eight days ago does not count; a user's own delete does not either.
	if w := itCall(t, rg, PermQuestionsModerate, http.MethodPost, "/questions/"+qToday2.String()+"/hide", `{"reason":"IT"}`); w.Code != http.StatusOK {
		t.Fatalf("hide: %d %s", w.Code, w.Body.String())
	}
	if w := itCall(t, rg, PermAnswersModerate, http.MethodPost, "/answers/"+aToday.String()+"/hide", `{"reason":"IT"}`); w.Code != http.StatusOK {
		t.Fatalf("hide answer: %d %s", w.Code, w.Body.String())
	}
	if _, err := pool.Exec(ctx, `INSERT INTO moderation_actions (actor_id, action_type, target_type, target_id, created_at)
		VALUES ($1, 'hide', 'question', $2, now() - interval '8 days')`, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE questions SET deleted_at = now(), status = 'deleted' WHERE id = $1`, itQuestion(t, pool, now.Add(-240*time.Hour))); err != nil {
		t.Fatal(err)
	}

	// Locked: one by the console; one closed by its author does not count.
	if w := itCall(t, rg, PermQuestionsModerate, http.MethodPost, "/questions/"+qToday1.String()+"/lock", `{"reason":"IT"}`); w.Code != http.StatusOK {
		t.Fatalf("lock: %d %s", w.Code, w.Body.String())
	}
	if _, err := pool.Exec(ctx, `UPDATE questions SET status = 'closed', closed_by = author_id WHERE id = $1`, qOld3); err != nil {
		t.Fatal(err)
	}

	// Open reports: reasons unique to this run.
	r1, r2 := "it-r1-"+uuid.NewString()[:8], "it-r2-"+uuid.NewString()[:8]
	itReport(t, pool, "question", uuid.New(), r1, "pending")
	itReport(t, pool, "question", uuid.New(), r1, "pending")
	itReport(t, pool, "answer", uuid.New(), r1, "reviewed")
	itReport(t, pool, "answer", uuid.New(), r2, "resolved")
	itReport(t, pool, "answer", uuid.New(), r2, "dismissed")

	after := adminStatsVia(t, rg)

	deltas := map[string][2]int{
		"open_reports_total":            {after.OpenReportsTotal - before.OpenReportsTotal, 3},
		"hidden_questions_last_7_days":  {after.HiddenQuestionsLast7Days - before.HiddenQuestionsLast7Days, 1},
		"hidden_answers_last_7_days":    {after.HiddenAnswersLast7Days - before.HiddenAnswersLast7Days, 1},
		"locked_questions":              {after.LockedQuestions - before.LockedQuestions, 1},
		"questions_created_today":       {after.QuestionsCreatedToday - before.QuestionsCreatedToday, 2},
		"questions_created_last_7_days": {after.QuestionsCreatedLast7Days - before.QuestionsCreatedLast7Days, 3},
		"answers_created_today":         {after.AnswersCreatedToday - before.AnswersCreatedToday, 1},
		"answers_created_last_7_days":   {after.AnswersCreatedLast7Days - before.AnswersCreatedLast7Days, 2},
	}
	for name, v := range deltas {
		if v[0] != v[1] {
			t.Errorf("%s delta = %d, want %d", name, v[0], v[1])
		}
	}
	if after.OpenReportsByReason[r1] != 3 {
		t.Errorf("open reports for %s = %d, want 3", r1, after.OpenReportsByReason[r1])
	}
	if n, ok := after.OpenReportsByReason[r2]; ok {
		t.Errorf("closed reports counted as open: %s = %d", r2, n)
	}
	if after.GeneratedAt.Before(after.DayStartsAt) || after.GeneratedAt.Sub(after.DayStartsAt) > 24*time.Hour {
		t.Errorf("day_starts_at %s is not within the day before generated_at %s", after.DayStartsAt, after.GeneratedAt)
	}
}
