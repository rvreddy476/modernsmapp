//go:build integration

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

	"github.com/atpost/trust-safety-service/database"
	"github.com/atpost/trust-safety-service/internal/service"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openTrustTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("M7_TRUST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("M7_TRUST_POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if name := pool.Config().ConnConfig.Database; !strings.HasSuffix(name, "_test") {
		pool.Close()
		t.Fatalf("refusing to run against database %q: name must end in _test", name)
	}
	// Same advisory lock as the service package's opener: packages run in
	// parallel against one scratch database and their DDL must not interleave.
	tx, err := pool.Begin(context.Background())
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock(7710010)`); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	if _, err := tx.Exec(context.Background(), database.SetupSQL); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	for _, name := range []string{
		"migrations/002_case_workflow.sql",
		"migrations/004_trust_extras.sql",
		"migrations/005_report_categories.sql",
		"migrations/007_grievances.sql",
		"migrations/008_launch_report_and_appeal_integrity.sql",
		"migrations/009_dating_report_grievances.sql",
		"migrations/010_admin_audit.sql",
	} {
		raw, err := database.Migrations.ReadFile(name)
		if err != nil {
			pool.Close()
			t.Fatal(err)
		}
		if _, err := tx.Exec(context.Background(), string(raw)); err != nil {
			pool.Close()
			t.Fatalf("%s: %v", name, err)
		}
	}
	if err := tx.Commit(context.Background()); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func serve(r *gin.Engine, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func auditCount(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM trust.admin_audit WHERE target_id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAdminAuditRoutesEndToEnd(t *testing.T) {
	pool := openTrustTestDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := service.New(store, nil)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(svc).RegisterRoutes(r)

	// Report PATCH with no actor: 400 ACTOR_REQUIRED, no change, no audit.
	report, err := svc.FileReport(ctx, uuid.New(), uuid.New(), "post", "spam", "")
	if err != nil {
		t.Fatal(err)
	}
	w := serve(r, http.MethodPatch, "/v1/reports/"+report.ID.String(), `{"status":"reviewing"}`, map[string]string{"X-Scopes": "admin"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), codeActorRequired) {
		t.Fatalf("no actor status=%d body=%s", w.Code, w.Body.String())
	}
	if got, _ := store.GetReport(ctx, report.ID); got.Status != "open" || auditCount(t, pool, report.ID) != 0 {
		t.Fatal("report changed or audited without an actor")
	}
	admin := uuid.New()
	w = serve(r, http.MethodPatch, "/v1/reports/"+report.ID.String(), `{"status":"reviewing"}`,
		map[string]string{"X-Scopes": "admin", "X-User-Id": admin.String(), "X-Verified-User-Id": admin.String(), "X-Request-Id": "rq-report"})
	if w.Code != http.StatusOK {
		t.Fatalf("with actor status=%d body=%s", w.Code, w.Body.String())
	}
	var actor, requestID string
	if err := pool.QueryRow(ctx, `SELECT actor_user_id::text, request_id FROM trust.admin_audit WHERE target_id=$1`, report.ID).Scan(&actor, &requestID); err != nil {
		t.Fatal(err)
	}
	if actor != admin.String() || requestID != "rq-report" {
		t.Fatalf("audit actor=%s request=%s", actor, requestID)
	}

	// dating-service's service-only route still works and is recorded as
	// the service.
	body := `{"report_id":"` + uuid.NewString() + `","reporter_id":"` + uuid.NewString() + `","target_id":"` + uuid.NewString() + `","reason":"harassment","reported_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	w = serve(r, http.MethodPost, DatingReportGrievancePath, body, map[string]string{"X-Request-Id": "rq-dating"})
	if w.Code != http.StatusCreated {
		t.Fatalf("dating link status=%d body=%s", w.Code, w.Body.String())
	}
	var linked struct {
		Data struct {
			GrievanceID uuid.UUID `json:"grievance_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &linked); err != nil || linked.Data.GrievanceID == uuid.Nil {
		t.Fatalf("decode link: %v %s", err, w.Body.String())
	}
	gid := linked.Data.GrievanceID
	var actorType, actorService string
	if err := pool.QueryRow(ctx, `SELECT actor_type, actor_service FROM trust.admin_audit WHERE target_id=$1`, gid).Scan(&actorType, &actorService); err != nil {
		t.Fatal(err)
	}
	if actorType != "service" || actorService != service.DatingServiceActor {
		t.Fatalf("dating audit actor=%s/%s", actorType, actorService)
	}

	// Officer takes it, then hands it over; the history route shows both.
	officer, next := uuid.New(), uuid.New()
	hdr := map[string]string{"X-Scopes": "admin", "X-User-Id": officer.String()}
	if w = serve(r, http.MethodPatch, "/v1/grievances/"+gid.String(), `{"status":"acknowledged"}`, hdr); w.Code != http.StatusOK {
		t.Fatalf("acknowledge status=%d body=%s", w.Code, w.Body.String())
	}
	if w = serve(r, http.MethodPatch, "/v1/grievances/"+gid.String(), `{"assigned_to":"`+next.String()+`"}`, hdr); w.Code != http.StatusOK {
		t.Fatalf("reassign status=%d body=%s", w.Code, w.Body.String())
	}
	w = serve(r, http.MethodGet, "/v1/grievances/"+gid.String()+"/history", "", hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("history status=%d body=%s", w.Code, w.Body.String())
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
	if len(items) != 3 || items[0].Action != "grievance.created" ||
		items[1].Action != "grievance.acknowledged" || *items[1].NewAssignee != officer ||
		items[2].Action != "grievance.assigned" || *items[2].PrevAssignee != officer || *items[2].NewAssignee != next {
		t.Fatalf("history=%s", w.Body.String())
	}

	// Overdue queue through the route.
	overdueID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO trust.grievances (id, complainant_id, subject, description, status, due_at)
		VALUES ($1, $2, 'other', 'x', 'open', NOW() - interval '16 days')`, overdueID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	w = serve(r, http.MethodGet, "/v1/grievances?overdue=true&limit=100", "", hdr)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), overdueID.String()) || strings.Contains(w.Body.String(), gid.String()) {
		t.Fatalf("overdue status=%d body=%s", w.Code, w.Body.String())
	}
}
