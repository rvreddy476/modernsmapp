// DB-backed admin-service token tests (admin console Wave 2 — Chat, groups):
// a decision writes exactly one group_admin_audit row whose actor is the
// token's act claim, and stats count what is seeded. Requires TEST_PG_DSN on
// a "_test" database (group_it_test); skipped when unset.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/atpost/group-service/database"
	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupGroupAdminIT(t *testing.T) (*adminTokenRig, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping group-service admin token integration tests")
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
	// The full migration chain assumes post-service's tables in the same
	// database (002 alters posts), so apply the base schema plus the two
	// migrations this surface needs: 006 creates group_reports, 012 adds the
	// review columns and the audit table. Both are idempotent.
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		t.Fatalf("setup.sql: %v", err)
	}
	for _, name := range []string{"migrations/006_full_groups.sql", "migrations/012_admin_report_review.sql"} {
		sql, err := database.Migrations.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	// TRUNCATE is not a row trigger event, so the append-only audit table
	// can be reset on a test database.
	if _, err := pool.Exec(ctx, `TRUNCATE group_admin_audit, group_reports`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	rg := newAdminTokenRig(t)
	rg.r = adminTokenRouter(store.New(pool), rg.v, adminTestInternalKey)
	return rg, pool
}

func seedGroup(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO groups (name, creator_id) VALUES ('IT group', $1) RETURNING id`, uuid.New()).Scan(&id); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	return id
}

func seedGroupReport(t *testing.T, pool *pgxpool.Pool, groupID uuid.UUID, status, reviewedAgo string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	q := `INSERT INTO group_reports (group_id, reporter_id, target_type, target_id, reason, status)
		VALUES ($1, $2, 'post', $3, 'harassment', $4) RETURNING id`
	args := []any{groupID, uuid.NewString(), uuid.New(), status}
	if reviewedAgo != "" {
		q = `INSERT INTO group_reports (group_id, reporter_id, target_type, target_id, reason, status, reviewed_by, reviewed_at)
			VALUES ($1, $2, 'post', $3, 'harassment', $4, $5, NOW() - $6::interval) RETURNING id`
		args = append(args, uuid.NewString(), reviewedAgo)
	}
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&id); err != nil {
		t.Fatalf("seed report: %v", err)
	}
	return id
}

func groupAuditRows(t *testing.T, pool *pgxpool.Pool, target uuid.UUID) (actors []uuid.UUID, actions []string) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT actor_id, action FROM group_admin_audit WHERE target_id = $1 ORDER BY created_at`, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a uuid.UUID
		var s string
		if err := rows.Scan(&a, &s); err != nil {
			t.Fatal(err)
		}
		actors, actions = append(actors, a), append(actions, s)
	}
	return actors, actions
}

func TestGroupAdminIT_DecisionAuditsTheRealActor(t *testing.T) {
	rg, pool := setupGroupAdminIT(t)
	ctx := context.Background()
	group := seedGroup(t, pool)
	upheld := seedGroupReport(t, pool, group, "pending", "")
	dismissed := seedGroupReport(t, pool, group, "pending", "")

	decide := func(id uuid.UUID, body, perm string) int {
		hdr := forgedEdge(uuid.New())
		hdr[ServiceAuthHeader] = "Bearer " + rg.mint(t, rg.admin, AudienceChat, []string{perm}, rg.actor.String())
		return adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/reports/"+id.String()+"/decision", body, hdr).Code
	}

	for _, tc := range []struct {
		id     uuid.UUID
		body   string
		action string
		status string
	}{
		{upheld, `{"decision":"uphold","reason":"harassment confirmed"}`, store.AuditGroupReportUpheld, store.ReportUpheld},
		{dismissed, `{"decision":"dismiss","reason":"no violation"}`, store.AuditGroupReportDismissed, store.ReportDismissed},
	} {
		if code := decide(tc.id, tc.body, PermReportsAct); code != http.StatusOK {
			t.Fatalf("%s: status=%d, want 200", tc.action, code)
		}
		actors, actions := groupAuditRows(t, pool, tc.id)
		if len(actors) != 1 || actors[0] != rg.actor || actions[0] != tc.action {
			t.Fatalf("%s: audit actors=%v actions=%v, want one by %s", tc.action, actors, actions, rg.actor)
		}
		var reviewedBy, status string
		if err := pool.QueryRow(ctx, `SELECT reviewed_by, status FROM group_reports WHERE id = $1`, tc.id).Scan(&reviewedBy, &status); err != nil {
			t.Fatal(err)
		}
		if reviewedBy != rg.actor.String() || status != tc.status {
			t.Fatalf("report reviewed_by=%s status=%s, want %s %s", reviewedBy, status, rg.actor, tc.status)
		}
	}

	// Refusals write nothing.
	if code := decide(upheld, `{"decision":"dismiss","reason":"second opinion"}`, PermReportsAct); code != http.StatusConflict {
		t.Fatalf("re-decision: status=%d, want 409", code)
	}
	if code := decide(uuid.New(), adminBody, PermReportsAct); code != http.StatusNotFound {
		t.Fatalf("unknown report: status=%d, want 404", code)
	}
	fresh := seedGroupReport(t, pool, group, "pending", "")
	if code := decide(fresh, `{"decision":"uphold","reason":"  "}`, PermReportsAct); code != http.StatusUnprocessableEntity {
		t.Fatalf("blank reason: status=%d, want 422", code)
	}
	if code := decide(fresh, adminBody, PermReportsRead); code != http.StatusForbidden {
		t.Fatalf("read-only token: status=%d, want 403", code)
	}
	if actors, _ := groupAuditRows(t, pool, upheld); len(actors) != 1 {
		t.Fatalf("upheld audit rows=%d after refused re-decision, want 1", len(actors))
	}
	if actors, _ := groupAuditRows(t, pool, fresh); len(actors) != 0 {
		t.Fatalf("refused decisions wrote %d audit rows", len(actors))
	}
	if _, err := pool.Exec(ctx, `DELETE FROM group_admin_audit WHERE target_id = $1`, upheld); err == nil {
		t.Fatal("group_admin_audit accepted a DELETE")
	}
}

func TestGroupAdminIT_ReadsAndStats(t *testing.T) {
	rg, pool := setupGroupAdminIT(t)
	group := seedGroup(t, pool)
	first := seedGroupReport(t, pool, group, "pending", "")
	seedGroupReport(t, pool, group, "pending", "")
	seedGroupReport(t, pool, group, "upheld", "1 day")
	seedGroupReport(t, pool, group, "dismissed", "5 days")
	seedGroupReport(t, pool, group, "dismissed", "6 days")
	seedGroupReport(t, pool, group, "upheld", "9 days")

	get := func(path, perm string) json.RawMessage {
		w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+path, "", bearer(rg.mint(t, rg.admin, AudienceChat, []string{perm}, rg.actor.String())))
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", path, w.Code, w.Body.String())
		}
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		return env.Data
	}

	var stats store.GroupAdminStats
	if err := json.Unmarshal(get("/stats", PermStatsRead), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.OpenReports != 2 || stats.ReportsDecided7d != 3 || stats.ReportsUpheld7d != 1 || stats.ReportsDismissed7d != 2 || stats.OldestOpenReportAt == nil {
		t.Fatalf("stats=%+v, want open 2, decided 3 (upheld 1, dismissed 2)", stats)
	}
	var queue []store.GroupReport
	if err := json.Unmarshal(get("/reports", PermReportsRead), &queue); err != nil {
		t.Fatal(err)
	}
	if len(queue) != 2 {
		t.Fatalf("pending queue len=%d, want 2", len(queue))
	}
	var all []store.GroupReport
	if err := json.Unmarshal(get("/reports?status=all", PermReportsRead), &all); err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("all queue len=%d, want 6", len(all))
	}
	var one store.GroupReport
	if err := json.Unmarshal(get("/reports/"+first.String(), PermReportsRead), &one); err != nil {
		t.Fatal(err)
	}
	if one.ID != first || one.GroupID != group || one.GroupName == nil || *one.GroupName != "IT group" {
		t.Fatalf("report detail=%+v, want %s in group %s", one, first, group)
	}
}
