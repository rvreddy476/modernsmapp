//go:build integration

// Admin-service token path against trust_safety_it_test: the audit actor is
// the token's act claim (never a header), each change writes exactly one
// trust.admin_audit row, refusals change nothing, the queues list through the
// token family, and the stats counts move by exactly what was seeded.
//
// The stats test reads global counts; run integration packages with -p 1 so
// another package's fixtures cannot land between the two readings.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type noPostModeration struct{}

func (noPostModeration) GetSubject(context.Context, uuid.UUID) (*service.PostModerationSubject, error) {
	return nil, errors.New("post moderation is not used by these tests")
}

func (noPostModeration) OverturnAppeal(context.Context, *postgres.ContentAppeal, uuid.UUID, int64, string) error {
	return errors.New("post moderation is not used by these tests")
}

func newIntegrationTokenRig(t *testing.T) (*adminTokenRig, *pgxpool.Pool, *service.Service) {
	t.Helper()
	pool := openTrustTestDB(t)
	svc := service.New(postgres.New(pool), nil)
	svc.SetExtrasStore(postgres.NewExtrasStore(pool))
	svc.SetPostModerationClient(noPostModeration{})
	rg := newAdminTokenRig(t)
	rg.r = newTokenTestRouter(t, New(svc).WithServiceAuth(rg.v))
	return rg, pool, svc
}

// withForgedIdentity adds the internal key and a full admin identity for
// someone else, next to the token.
func withForgedIdentity(tok string, forged uuid.UUID, requestID string) map[string]string {
	hdr := gatewayAdmin(forged, "superadmin admin")
	hdr["X-Admin-Id"] = forged.String()
	hdr[ServiceAuthHeader] = "Bearer " + tok
	hdr["X-Request-Id"] = requestID
	return hdr
}

func auditActors(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT actor_type || ':' || coalesce(actor_user_id::text, actor_service) FROM trust.admin_audit WHERE target_id=$1 ORDER BY seq`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func insertGrievance(t *testing.T, pool *pgxpool.Pool, status string, due time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO trust.grievances (id, complainant_id, subject, description, status, due_at)
		VALUES ($1, $2, 'other', 'seeded', $3, NOW() + $4::interval)`, id, uuid.New(), status, due.String()); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertAppeal(t *testing.T, pool *pgxpool.Pool, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO trust.content_appeals (id, user_id, content_type, content_id, action_taken, appeal_reason, status)
		VALUES ($1, $2, 'post', $3, 'removed', 'seeded', $4)`, id, uuid.New(), uuid.New(), status); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAdminTokenIntegration_ActorIsActAndEachChangeAuditedOnce(t *testing.T) {
	rg, pool, svc := newIntegrationTokenRig(t)
	ctx := context.Background()
	actor := rg.actor.String()
	forged := uuid.New()
	want := []string{"user:" + actor}

	// Report: a PATCH with the wrong permission changes nothing.
	report, err := svc.FileReport(ctx, uuid.New(), uuid.New(), "post", "spam", "")
	if err != nil {
		t.Fatal(err)
	}
	path := InternalAdminPrefix + "/reports/" + report.ID.String()
	readTok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsRead}, actor)
	if w := serveAdmin(rg.r, http.MethodPatch, path, `{"status":"reviewing"}`, withForgedIdentity(readTok, forged, "rq-refused")); w.Code != http.StatusForbidden {
		t.Fatalf("patch with reports.read: status=%d body=%s", w.Code, w.Body.String())
	}
	// The edge's key plus a forged admin identity, no token: refused.
	if w := serveAdmin(rg.r, http.MethodPatch, path, `{"status":"reviewing"}`, gatewayAdmin(forged, "admin")); w.Code != http.StatusUnauthorized {
		t.Fatalf("patch without token: status=%d body=%s", w.Code, w.Body.String())
	}
	if got := auditActors(t, pool, report.ID); len(got) != 0 {
		t.Fatalf("refusals wrote audit rows: %v", got)
	}
	actTok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermReportsAct}, actor)
	w := serveAdmin(rg.r, http.MethodPatch, path, `{"status":"reviewing","resolution_notes":"looking"}`, withForgedIdentity(actTok, forged, "rq-token-report"))
	if w.Code != http.StatusOK {
		t.Fatalf("report patch: status=%d body=%s", w.Code, w.Body.String())
	}
	if got := auditActors(t, pool, report.ID); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("report audit=%v, want %v (forged %s)", got, want, forged)
	}
	var requestID string
	if err := pool.QueryRow(ctx, `SELECT request_id FROM trust.admin_audit WHERE target_id=$1`, report.ID).Scan(&requestID); err != nil || requestID != "rq-token-report" {
		t.Fatalf("request id=%q err=%v", requestID, err)
	}
	if w := serveAdmin(rg.r, http.MethodGet, path, "", bearer(readTok)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"reviewing"`) {
		t.Fatalf("report get: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := serveAdmin(rg.r, http.MethodGet, InternalAdminPrefix+"/reports?limit=100", "", bearer(readTok)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), report.ID.String()) {
		t.Fatalf("report list: status=%d body=%s", w.Code, w.Body.String())
	}

	// Appeal, with the moderator's catalogue permission only.
	appealID := insertAppeal(t, pool, "open")
	appealTok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermAppealsAct}, actor)
	w = serveAdmin(rg.r, http.MethodPatch, InternalAdminPrefix+"/appeals/"+appealID.String(), `{"status":"under_review","note":"checking"}`, withForgedIdentity(appealTok, forged, "rq-appeal"))
	if w.Code != http.StatusOK {
		t.Fatalf("appeal patch: status=%d body=%s", w.Code, w.Body.String())
	}
	if got := auditActors(t, pool, appealID); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("appeal audit=%v, want %v", got, want)
	}
	var reviewedBy string
	if err := pool.QueryRow(ctx, `SELECT coalesce(reviewed_by::text, '') FROM trust.content_appeals WHERE id=$1`, appealID).Scan(&reviewedBy); err != nil {
		t.Fatal(err)
	}
	if reviewedBy != "" && reviewedBy != actor {
		t.Fatalf("appeal reviewed_by=%s, want the token actor", reviewedBy)
	}
	if w := serveAdmin(rg.r, http.MethodGet, InternalAdminPrefix+"/appeals?status=under_review&limit=100", "", bearer(appealTok)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), appealID.String()) {
		t.Fatalf("appeal list: status=%d body=%s", w.Code, w.Body.String())
	}

	// Grievance: acknowledge (the actor becomes the officer), hand over, history.
	gid := insertGrievance(t, pool, "open", 24*time.Hour)
	next := uuid.New()
	gTok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermGrievancesAct}, actor)
	gPath := InternalAdminPrefix + "/grievances/" + gid.String()
	if w := serveAdmin(rg.r, http.MethodPatch, gPath, `{"status":"acknowledged"}`, withForgedIdentity(gTok, forged, "rq-g1")); w.Code != http.StatusOK {
		t.Fatalf("grievance ack: status=%d body=%s", w.Code, w.Body.String())
	}
	if got := auditActors(t, pool, gid); len(got) != 1 {
		t.Fatalf("grievance ack audit rows=%v, want 1", got)
	}
	if w := serveAdmin(rg.r, http.MethodPatch, gPath, `{"assigned_to":"`+next.String()+`"}`, withForgedIdentity(gTok, forged, "rq-g2")); w.Code != http.StatusOK {
		t.Fatalf("grievance hand-over: status=%d body=%s", w.Code, w.Body.String())
	}
	w = serveAdmin(rg.r, http.MethodGet, gPath+"/history", "", bearer(gTok))
	if w.Code != http.StatusOK {
		t.Fatalf("history: status=%d body=%s", w.Code, w.Body.String())
	}
	var hist struct {
		Data struct {
			Items []postgres.AuditEntry `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &hist); err != nil {
		t.Fatal(err)
	}
	items := hist.Data.Items
	if len(items) != 2 ||
		items[0].Action != "grievance.acknowledged" || *items[0].ActorUserID != rg.actor || *items[0].NewAssignee != rg.actor ||
		items[1].Action != "grievance.assigned" || *items[1].ActorUserID != rg.actor || *items[1].PrevAssignee != rg.actor || *items[1].NewAssignee != next {
		t.Fatalf("history=%s (forged %s)", w.Body.String(), forged)
	}
	if w := serveAdmin(rg.r, http.MethodGet, gPath, "", bearer(gTok)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), next.String()) {
		t.Fatalf("grievance get: status=%d body=%s", w.Code, w.Body.String())
	}
	overdue := insertGrievance(t, pool, "open", -48*time.Hour)
	w = serveAdmin(rg.r, http.MethodGet, InternalAdminPrefix+"/grievances?overdue=true&limit=100", "", bearer(gTok))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), overdue.String()) || strings.Contains(w.Body.String(), gid.String()) {
		t.Fatalf("overdue list: status=%d body=%s", w.Code, w.Body.String())
	}

	// Read-only queues.
	struck := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO trust.user_strikes (user_id, reason, severity, created_by) VALUES ($1, 'seeded', 'strike', $2)`, struck, uuid.New()); err != nil {
		t.Fatal(err)
	}
	reads := []struct {
		perm, path, contains string
	}{
		{PermStrikesManage, "/strikes/" + struck.String(), struck.String()},
		{PermVerificationReview, "/verification-requests?status=pending", `"items"`},
		{PermMediaLabelsRead, "/media-labels/" + uuid.NewString(), `"items"`},
		{PermKeywordFiltersRead, "/keyword-filters", `"items"`},
	}
	for _, rd := range reads {
		tok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{rd.perm}, actor)
		if w := serveAdmin(rg.r, http.MethodGet, InternalAdminPrefix+rd.path, "", bearer(tok)); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), rd.contains) {
			t.Fatalf("%s: status=%d body=%s", rd.path, w.Code, w.Body.String())
		}
	}

	// The legacy route still works exactly as before, actor from X-User-Id.
	legacyReport, err := svc.FileReport(ctx, uuid.New(), uuid.New(), "post", "spam", "")
	if err != nil {
		t.Fatal(err)
	}
	legacyAdmin := uuid.New()
	if w := serveAdmin(rg.r, http.MethodPatch, "/v1/reports/"+legacyReport.ID.String(), `{"status":"reviewing"}`, gatewayAdmin(legacyAdmin, "admin")); w.Code != http.StatusOK {
		t.Fatalf("legacy patch: status=%d body=%s", w.Code, w.Body.String())
	}
	if got := auditActors(t, pool, legacyReport.ID); len(got) != 1 || got[0] != "user:"+legacyAdmin.String() {
		t.Fatalf("legacy audit=%v", got)
	}
}

type statsBody struct {
	Data postgres.AdminStats `json:"data"`
}

func readStats(t *testing.T, rg *adminTokenRig) postgres.AdminStats {
	t.Helper()
	tok := rg.mint(t, rg.admin, AudienceTrustSafety, []string{PermStatsRead}, rg.actor.String())
	w := serveAdmin(rg.r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok))
	if w.Code != http.StatusOK {
		t.Fatalf("stats: status=%d body=%s", w.Code, w.Body.String())
	}
	var b statsBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b.Data
}

func TestAdminTokenIntegration_StatsCounts(t *testing.T) {
	rg, pool, svc := newIntegrationTokenRig(t)
	ctx := context.Background()
	before := readStats(t, rg)

	for i := 0; i < 3; i++ {
		r, err := svc.FileReport(ctx, uuid.New(), uuid.New(), "post", "spam", "")
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			if _, err := pool.Exec(ctx, `UPDATE trust.reports SET status='reviewing' WHERE id=$1`, r.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	insertAppeal(t, pool, "open")
	insertAppeal(t, pool, "under_review")
	insertAppeal(t, pool, "upheld")

	insertGrievance(t, pool, "open", -48*time.Hour)        // overdue
	insertGrievance(t, pool, "acknowledged", -time.Hour)   // overdue
	insertGrievance(t, pool, "resolved", -48*time.Hour)    // closed: neither
	insertGrievance(t, pool, "open", 24*time.Hour)         // due soon
	insertGrievance(t, pool, "acknowledged", 47*time.Hour) // due soon
	insertGrievance(t, pool, "rejected", 24*time.Hour)     // closed: neither
	insertGrievance(t, pool, "open", 72*time.Hour)         // open, not soon

	if _, err := pool.Exec(ctx, `INSERT INTO trust.user_strikes (user_id, reason, severity, created_at) VALUES
		($1, 'seeded', 'warning', NOW() - interval '1 day'),
		($2, 'seeded', 'strike', NOW() - interval '8 days')`, uuid.New(), uuid.New()); err != nil {
		t.Fatal(err)
	}

	after := readStats(t, rg)
	checks := []struct {
		name      string
		got, want int64
	}{
		{"open_reports_by_status.open", after.OpenReportsByStatus["open"] - before.OpenReportsByStatus["open"], 2},
		{"open_reports_by_status.reviewing", after.OpenReportsByStatus["reviewing"] - before.OpenReportsByStatus["reviewing"], 1},
		{"open_reports", after.OpenReports - before.OpenReports, 3},
		{"open_appeals", after.OpenAppeals - before.OpenAppeals, 2},
		{"open_grievances", after.OpenGrievances - before.OpenGrievances, 5},
		{"grievances_overdue", after.GrievancesOverdue - before.GrievancesOverdue, 2},
		{"grievances_due_within_48h", after.GrievancesDueSoon - before.GrievancesDueSoon, 2},
		{"strikes_last_7_days", after.StrikesLast7Days - before.StrikesLast7Days, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s moved by %d, want %d (before=%+v after=%+v)", c.name, c.got, c.want, before, after)
		}
	}
	if after.GeneratedAt.IsZero() {
		t.Error("generated_at missing")
	}
}
