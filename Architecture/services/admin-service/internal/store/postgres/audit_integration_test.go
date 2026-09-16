//go:build integration

package postgres_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/atpost/admin-service/database"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// admin.audit_log against a real Postgres.
//
//	POSTGRES_DSN=postgres://…/admin_it_test go test -tags integration ./internal/store/postgres/ -v
//
// Runs the real schema path (setup.sql + every migration), so migration 002 —
// the attribution columns and the append-only trigger — is itself under test.

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse")
	}
	switch db := cfg.Database; {
	case db == "" || db == "app" || !strings.HasSuffix(db, "_test"):
		t.Fatalf("refusing to run integration tests against database %q: the name must end in _test (e.g. admin_it_test)", db)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Twice: the schema path must be idempotent across boots.
	for i := 0; i < 2; i++ {
		if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
			t.Fatalf("bootstrap %d: %v", i, err)
		}
	}
	return pool
}

func TestRecordAdminWriteStoresEveryField(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	actor := uuid.NewString()
	target := uuid.NewString()

	for _, e := range []postgres.AdminAuditEntry{
		{Actor: actor, App: "commerce", Operation: "seller.reject", TargetType: "seller", TargetID: target,
			Reason: "fake docs", RequestID: "req-ok", Outcome: postgres.AuditOutcomeSuccess, StatusCode: 204,
			Payload: map[string]any{"notes": "n"}},
		{Actor: actor, App: "commerce", Operation: "seller.reject", TargetType: "seller", TargetID: target,
			RequestID: "req-fail", Outcome: postgres.AuditOutcomeFailure, StatusCode: 0,
			Payload: map[string]any{"error": "dial tcp: refused"}},
	} {
		if err := store.RecordAdminWrite(ctx, e); err != nil {
			t.Fatalf("record %s: %v", e.RequestID, err)
		}
	}

	rows, err := pool.Query(ctx, `
		SELECT admin_actor, app, action, entity_type, entity_id, reason, request_id, outcome, status_code
		FROM admin.audit_log WHERE entity_id = $1 ORDER BY request_id DESC`, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var a, app, op, tt, tid, rid, outcome string
		var reason *string
		var status int
		if err := rows.Scan(&a, &app, &op, &tt, &tid, &reason, &rid, &outcome, &status); err != nil {
			t.Fatal(err)
		}
		if a != actor || app != "commerce" || op != "seller.reject" || tt != "seller" || tid != target {
			t.Fatalf("attribution wrong: %s %s %s %s %s", a, app, op, tt, tid)
		}
		r := "<nil>"
		if reason != nil {
			r = *reason
		}
		got = append(got, fmt.Sprintf("%s|%s|%s|%d", rid, outcome, r, status))
	}
	want := []string{"req-ok|success|fake docs|204", "req-fail|failure|<nil>|0"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rows = %v, want %v", got, want)
	}

	if err := store.RecordAdminWrite(ctx, postgres.AdminAuditEntry{App: "commerce", Operation: "x", TargetType: "seller", Outcome: "success"}); err == nil {
		t.Fatal("an entry with no actor was accepted")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO admin.audit_log (id, admin_actor, action, entity_type, entity_id, outcome)
		VALUES ($1, 'a', 'x', 'y', 'z', 'maybe')`, uuid.New()); err == nil {
		t.Fatal("an outcome outside the allowed set was accepted")
	}
}

func TestAuditLogIsAppendOnly(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	target := uuid.NewString()
	if err := store.RecordAdminWrite(ctx, postgres.AdminAuditEntry{Actor: uuid.NewString(), App: "commerce",
		Operation: "product.approve", TargetType: "product", TargetID: target, Outcome: postgres.AuditOutcomeSuccess, StatusCode: 204}); err != nil {
		t.Fatal(err)
	}

	for name, sql := range map[string]string{
		"update": `UPDATE admin.audit_log SET outcome = 'failure' WHERE entity_id = '` + target + `'`,
		"delete": `DELETE FROM admin.audit_log WHERE entity_id = '` + target + `'`,
	} {
		if _, err := pool.Exec(ctx, sql); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s of a recent row: err = %v, want append-only refusal", name, err)
		}
	}
	if _, err := pool.Exec(ctx, `TRUNCATE admin.audit_log`); err == nil || !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("truncate: err = %v, want append-only refusal", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM admin.audit_log WHERE entity_id = $1 AND outcome = 'success'`, target).Scan(&n); err != nil || n != 1 {
		t.Fatalf("row after refused mutations: n=%d err=%v", n, err)
	}

	// The CERT-In retention sweep may still remove rows past the 180-day floor.
	old := uuid.NewString()
	if _, err := pool.Exec(ctx, `INSERT INTO admin.audit_log (id, admin_actor, app, action, entity_type, entity_id, outcome, status_code, created_at)
		VALUES ($1, 'a', 'commerce', 'seller.approve', 'seller', $2, 'success', 204, NOW() - INTERVAL '200 days')`, uuid.New(), old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PurgeAuditLogsOlderThan(ctx, 180); err != nil {
		t.Fatalf("retention sweep: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM admin.audit_log WHERE entity_id = $1`, old).Scan(&n); err != nil || n != 0 {
		t.Fatalf("aged row survived the sweep: n=%d err=%v", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM admin.audit_log WHERE entity_id = $1`, target).Scan(&n); err != nil || n != 1 {
		t.Fatalf("recent row removed by the sweep: n=%d err=%v", n, err)
	}
}

func TestDashboardOnARealDatabase(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	if err := store.RecordAdminWrite(ctx, postgres.AdminAuditEntry{Actor: uuid.NewString(), App: "commerce",
		Operation: "seller.suspend", TargetType: "seller", TargetID: uuid.NewString(), Outcome: postgres.AuditOutcomeFailure, StatusCode: 500}); err != nil {
		t.Fatal(err)
	}

	stats, err := store.GetDashboardStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for key, reason := range stats.Unavailable {
		if reason == postgres.DashboardReasonQueryFailed {
			t.Fatalf("%s failed against the real schema", key)
		}
	}
	if stats.ActiveSuspensions == nil || stats.AdminWritesLast7d == nil || stats.AdminWriteFailuresLast7d == nil {
		t.Fatalf("own-table counts missing: %+v", stats)
	}
	if *stats.AdminWritesLast7d < 1 || *stats.AdminWriteFailuresLast7d < 1 {
		t.Fatalf("counts do not see the row just written: writes=%d failures=%d", *stats.AdminWritesLast7d, *stats.AdminWriteFailuresLast7d)
	}
	if stats.TotalUsers != nil || stats.Unavailable["total_users"] != postgres.DashboardReasonOwnedElsewhere {
		t.Fatalf("total_users should be unavailable: %+v", stats)
	}
}
