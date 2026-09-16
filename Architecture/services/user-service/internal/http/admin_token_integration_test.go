// DB-backed business pages admin tests (admin console Wave 2 — Content):
// every page and document decision, on the admin-service token path and the
// legacy PAGES_ADMIN_USER_IDS path, writes exactly one page_admin_audit row
// with the real actor in the decision's transaction; the table is append-only;
// the stats route counts what is seeded. Requires TEST_PG_DSN on a database
// whose name ends in "_test" (user_it_test, -p 1); skipped when unset.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/user-service/database"
	"github.com/atpost/user-service/internal/pages"
	"github.com/atpost/user-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupPageAdminIT(t *testing.T) (*pageTokenRig, *pgxpool.Pool, *store.Store) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping user-service page admin integration tests")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatal("TEST_PG_DSN must parse and name a database ending in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	rg := newPageTokenRig(t)
	st := store.New(pool)
	rg.r = pageTokenRouter(st, rg.v, rg.allowed)
	return rg, pool, st
}

func itExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("seed %q: %v", sql, err)
	}
}

// seedITPage inserts an owner and a page in status, with the given decision
// timestamps (nil leaves them NULL), and returns the page id.
func seedITPage(t *testing.T, pool *pgxpool.Pool, status string, approvedAt, rejectedAt, suspendedAt *time.Time) uuid.UUID {
	t.Helper()
	owner, page := uuid.New(), uuid.New()
	itExec(t, pool, `INSERT INTO users (id, display_name, created_at, updated_at) VALUES ($1, 'IT owner', now(), now())`, owner)
	itExec(t, pool, `INSERT INTO business_pages (id, user_id, page_handle, page_name, category, status, page_type,
			submitted_at, approved_at, rejected_at, suspended_at)
		VALUES ($1, $2, $3, 'IT Page', 'shop', $4, 'business', now(), $5, $6, $7)`,
		page, owner, "it-"+page.String()[:13], status, approvedAt, rejectedAt, suspendedAt)
	return page
}

func seedITDoc(t *testing.T, pool *pgxpool.Pool, pageID uuid.UUID, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	itExec(t, pool, `INSERT INTO page_verification_documents (id, page_id, document_type, document_url, status)
		VALUES ($1, $2, 'identity_proof', 'https://docs.example/it', $3)`, id, pageID, status)
	return id
}

type pageAuditRow struct {
	Actor      uuid.UUID
	Via        string
	Action     string
	PageID     uuid.UUID
	DocumentID *uuid.UUID
	Prev, New  string
	Reason     *string
}

func pageAuditRows(t *testing.T, pool *pgxpool.Pool, pageID uuid.UUID) []pageAuditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT actor_user_id, via, action, page_id, document_id, prev_status, new_status, reason
		FROM page_admin_audit WHERE page_id = $1 ORDER BY seq`, pageID)
	if err != nil {
		t.Fatalf("audit rows: %v", err)
	}
	defer rows.Close()
	var out []pageAuditRow
	for rows.Next() {
		var r pageAuditRow
		if err := rows.Scan(&r.Actor, &r.Via, &r.Action, &r.PageID, &r.DocumentID, &r.Prev, &r.New, &r.Reason); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func pageStatus(t *testing.T, pool *pgxpool.Pool, pageID uuid.UUID) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM business_pages WHERE id=$1`, pageID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func expectOneAudit(t *testing.T, pool *pgxpool.Pool, pageID uuid.UUID, want pageAuditRow) {
	t.Helper()
	rows := pageAuditRows(t, pool, pageID)
	if len(rows) != 1 {
		t.Fatalf("page %s: %d audit rows, want 1: %+v", pageID, len(rows), rows)
	}
	got := rows[0]
	reason := ""
	if got.Reason != nil {
		reason = *got.Reason
	}
	wantReason := ""
	if want.Reason != nil {
		wantReason = *want.Reason
	}
	sameDoc := (got.DocumentID == nil) == (want.DocumentID == nil) &&
		(got.DocumentID == nil || *got.DocumentID == *want.DocumentID)
	if got.Actor != want.Actor || got.Via != want.Via || got.Action != want.Action || got.PageID != pageID ||
		got.Prev != want.Prev || got.New != want.New || reason != wantReason || !sameDoc {
		t.Fatalf("page %s audit=%+v (reason %q), want %+v (reason %q)", pageID, got, reason, want, wantReason)
	}
}

func strPtr(s string) *string { return &s }

func TestPageAdminIT_TokenDecisionsAuditTheActor(t *testing.T) {
	rg, pool, _ := setupPageAdminIT(t)
	all := rg.mint(t, rg.admin, AudienceSocial, AdminPermissions, rg.actor.String())
	decide := func(pageID uuid.UUID, suffix, body string) {
		t.Helper()
		hdr := forgedGateway(rg.allowed) // key + allowlisted header must not become the actor
		hdr[ServiceAuthHeader] = "Bearer " + all
		w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+pageID.String()+suffix, body, hdr)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", suffix, w.Code, w.Body.String())
		}
	}
	act := rg.actor
	via := store.ViaAdminService

	approve := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	decide(approve, "/approve", "")
	expectOneAudit(t, pool, approve, pageAuditRow{Actor: act, Via: via, Action: store.PageActionApprove, Prev: "pending_review", New: "approved"})

	reject := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	decide(reject, "/reject", `{"reason":"blurry proof"}`)
	expectOneAudit(t, pool, reject, pageAuditRow{Actor: act, Via: via, Action: store.PageActionReject, Prev: "pending_review", New: "rejected", Reason: strPtr("blurry proof")})

	suspend := seedITPage(t, pool, pages.StatusApproved, nil, nil, nil)
	decide(suspend, "/suspend", `{"reason":"scam reports"}`)
	expectOneAudit(t, pool, suspend, pageAuditRow{Actor: act, Via: via, Action: store.PageActionSuspend, Prev: "approved", New: "suspended", Reason: strPtr("scam reports")})

	disable := seedITPage(t, pool, pages.StatusSuspended, nil, nil, nil)
	decide(disable, "/disable", `{"reason":"repeat offender"}`)
	expectOneAudit(t, pool, disable, pageAuditRow{Actor: act, Via: via, Action: store.PageActionDisable, Prev: "suspended", New: "disabled", Reason: strPtr("repeat offender")})
	if s := pageStatus(t, pool, disable); s != pages.StatusDisabled {
		t.Fatalf("disabled page status=%s", s)
	}

	docPage := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	doc := seedITDoc(t, pool, docPage, "pending")
	decide(docPage, "/documents/"+doc.String()+"/reject", `{"reason":"expired id"}`)
	expectOneAudit(t, pool, docPage, pageAuditRow{Actor: act, Via: via, Action: store.DocumentActionReject, DocumentID: &doc, Prev: "pending", New: "rejected", Reason: strPtr("expired id")})

	docPage2 := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	doc2 := seedITDoc(t, pool, docPage2, "pending")
	decide(docPage2, "/documents/"+doc2.String()+"/approve", "")
	expectOneAudit(t, pool, docPage2, pageAuditRow{Actor: act, Via: via, Action: store.DocumentActionApprove, DocumentID: &doc2, Prev: "pending", New: "approved"})

	// Refused decisions write nothing: illegal transition, missing permission.
	w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+disable.String()+"/approve", "", bearer(all))
	if w.Code != http.StatusConflict {
		t.Fatalf("approve disabled: status=%d", w.Code)
	}
	moderateOnly := rg.mint(t, rg.admin, AudienceSocial, []string{PermPagesModerate}, act.String())
	w = adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+suspend.String()+"/approve", "", bearer(moderateOnly))
	if w.Code != http.StatusForbidden {
		t.Fatalf("reinstate with moderate only: status=%d", w.Code)
	}
	if n := len(pageAuditRows(t, pool, disable)) + len(pageAuditRows(t, pool, suspend)); n != 2 {
		t.Fatalf("refused decisions wrote audit rows: total %d, want 2", n)
	}
}

func TestPageAdminIT_LegacyDecisionsAuditTheActor(t *testing.T) {
	rg, pool, _ := setupPageAdminIT(t)
	allowed := map[string]string{"X-User-Id": rg.allowed.String()}
	via := store.ViaPagesAllowlist

	p := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+p.String()+"/reject", `{"reason":"wrong category"}`, allowed); w.Code != http.StatusOK {
		t.Fatalf("legacy reject: status=%d body=%s", w.Code, w.Body.String())
	}
	expectOneAudit(t, pool, p, pageAuditRow{Actor: rg.allowed, Via: via, Action: store.PageActionReject, Prev: "pending_review", New: "rejected", Reason: strPtr("wrong category")})

	for _, tc := range []struct {
		from, route, body, action, to string
	}{
		{pages.StatusPendingReview, "approve", "", store.PageActionApprove, "approved"},
		{pages.StatusApproved, "suspend", `{"reason":"spam"}`, store.PageActionSuspend, "suspended"},
		{pages.StatusApproved, "disable", "", store.PageActionDisable, "disabled"},
	} {
		id := seedITPage(t, pool, tc.from, nil, nil, nil)
		if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+id.String()+"/"+tc.route, tc.body, allowed); w.Code != http.StatusOK {
			t.Fatalf("legacy %s: status=%d body=%s", tc.route, w.Code, w.Body.String())
		}
		want := pageAuditRow{Actor: rg.allowed, Via: via, Action: tc.action, Prev: tc.from, New: tc.to}
		if tc.body != "" {
			want.Reason = strPtr("spam")
		}
		expectOneAudit(t, pool, id, want)
	}

	docPage := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	doc := seedITDoc(t, pool, docPage, "pending")
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+docPage.String()+"/documents/"+doc.String()+"/approve", "", allowed); w.Code != http.StatusOK {
		t.Fatalf("legacy document approve: status=%d body=%s", w.Code, w.Body.String())
	}
	expectOneAudit(t, pool, docPage, pageAuditRow{Actor: rg.allowed, Via: via, Action: store.DocumentActionApprove, DocumentID: &doc, Prev: "pending", New: "approved"})

	// Off the allowlist: refused, nothing written.
	other := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+other.String()+"/approve", "", map[string]string{"X-User-Id": uuid.NewString()}); w.Code != http.StatusForbidden {
		t.Fatalf("off-list approve: status=%d", w.Code)
	}
	if n := len(pageAuditRows(t, pool, other)); n != 0 || pageStatus(t, pool, other) != pages.StatusPendingReview {
		t.Fatalf("off-list approve wrote %d rows or changed status", n)
	}
}

// The decision and its audit row share one transaction: a failing audit
// insert leaves the page untouched.
func TestPageAdminIT_AuditFailureRollsBackDecision(t *testing.T) {
	_, pool, st := setupPageAdminIT(t)
	p := seedITPage(t, pool, pages.StatusApproved, nil, nil, nil)
	err := st.AdminSetPageStatus(context.Background(), store.PageStatusDecision{
		PageID: p, From: pages.StatusApproved, To: pages.StatusSuspended, Action: store.PageActionSuspend,
		Actor: uuid.New(), Via: "not-a-real-entry-point", Reason: "x",
	})
	if err == nil {
		t.Fatalf("an audit row with an invalid via was accepted")
	}
	if s := pageStatus(t, pool, p); s != pages.StatusApproved {
		t.Fatalf("page status=%s after a failed audit insert, want approved", s)
	}
	doc := seedITDoc(t, pool, p, "pending")
	err = st.AdminDecidePageDocument(context.Background(), store.PageDocumentDecision{
		DocumentID: doc, Status: "approved", Action: store.PageActionApprove, // page action on a document: CHECK fails
		Actor: uuid.New(), Via: store.ViaAdminService,
	})
	if err == nil {
		t.Fatalf("a document audit row with a page action was accepted")
	}
	var ds string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM page_verification_documents WHERE id=$1`, doc).Scan(&ds); err != nil || ds != "pending" {
		t.Fatalf("document status=%s err=%v after a failed audit insert, want pending", ds, err)
	}
}

func TestPageAdminIT_AuditIsAppendOnly(t *testing.T) {
	rg, pool, _ := setupPageAdminIT(t)
	p := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	tok := rg.mint(t, rg.admin, AudienceSocial, []string{PermPagesModerate}, rg.actor.String())
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+p.String()+"/approve", "", bearer(tok)); w.Code != http.StatusOK {
		t.Fatalf("approve: status=%d body=%s", w.Code, w.Body.String())
	}
	ctx := context.Background()
	for name, sql := range map[string]string{
		"update": `UPDATE page_admin_audit SET reason = 'rewritten' WHERE page_id = $1`,
		"delete": `DELETE FROM page_admin_audit WHERE page_id = $1`,
	} {
		if _, err := pool.Exec(ctx, sql, p); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: err=%v, want append-only refusal", name, err)
		}
	}
	if _, err := pool.Exec(ctx, `TRUNCATE page_admin_audit`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("truncate: err=%v, want append-only refusal", err)
	}
	if n := len(pageAuditRows(t, pool, p)); n != 1 {
		t.Fatalf("audit rows after refused tampering=%d, want 1", n)
	}
}

func TestPageAdminIT_Stats(t *testing.T) {
	rg, pool, _ := setupPageAdminIT(t)
	tok := rg.mint(t, rg.admin, AudienceSocial, []string{PermStatsRead}, rg.actor.String())
	read := func() store.PageAdminStats {
		t.Helper()
		w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok))
		if w.Code != http.StatusOK {
			t.Fatalf("stats: status=%d body=%s", w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				Pages store.PageAdminStats `json:"pages"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		return env.Data.Pages
	}
	before := read()

	now := time.Now()
	recent := now.Add(-2 * 24 * time.Hour)
	old := now.Add(-10 * 24 * time.Hour)
	// Queue: 2 pending review, one with 2 pending documents.
	q1 := seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	seedITPage(t, pool, pages.StatusPendingReview, nil, nil, nil)
	seedITDoc(t, pool, q1, "pending")
	seedITDoc(t, pool, q1, "pending")
	seedITDoc(t, pool, q1, "approved") // decided: not awaiting
	// Decisions: 3 approved recently (one later suspended), 1 approved long ago,
	// 1 rejected recently, 1 rejected long ago, 1 suspended long ago.
	seedITPage(t, pool, pages.StatusApproved, &recent, nil, nil)
	seedITPage(t, pool, pages.StatusApproved, &recent, nil, nil)
	seedITPage(t, pool, pages.StatusSuspended, &recent, nil, &recent)
	seedITPage(t, pool, pages.StatusApproved, &old, nil, nil)
	seedITPage(t, pool, pages.StatusRejected, nil, &recent, nil)
	seedITPage(t, pool, pages.StatusRejected, nil, &old, nil)
	seedITPage(t, pool, pages.StatusSuspended, &old, nil, &old)
	// A pending document on a disabled page is not awaiting review.
	gone := seedITPage(t, pool, pages.StatusDisabled, nil, nil, nil)
	seedITDoc(t, pool, gone, "pending")

	after := read()
	got := [5]int64{
		after.PendingReview - before.PendingReview,
		after.Approved7d - before.Approved7d,
		after.Rejected7d - before.Rejected7d,
		after.Suspended7d - before.Suspended7d,
		after.DocumentsPending - before.DocumentsPending,
	}
	want := [5]int64{2, 3, 1, 1, 2}
	if got != want {
		t.Fatalf("stats deltas [pending, approved7d, rejected7d, suspended7d, docs]=%v, want %v", got, want)
	}
	if after.WindowDays != 7 || after.AsOf.IsZero() {
		t.Fatalf("stats window=%d as_of=%v", after.WindowDays, after.AsOf)
	}
}
