package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

type fakeRow struct {
	n   int64
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*(dest[0].(*int64)) = r.n
	return nil
}

// fakeQuerier answers every query with n, or fails any query containing failOn.
type fakeQuerier struct {
	n       int64
	failOn  string
	queries []string
}

func (q *fakeQuerier) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	q.queries = append(q.queries, sql)
	if q.failOn != "" && strings.Contains(sql, q.failOn) {
		return fakeRow{err: errors.New("relation does not exist")}
	}
	return fakeRow{n: q.n}
}

func TestDashboardNeverQueriesAnotherServicesSchema(t *testing.T) {
	q := &fakeQuerier{n: 3}
	dashboardStats(context.Background(), q)
	for _, sql := range q.queries {
		if strings.Contains(sql, "social.") || strings.Contains(sql, "trust.") {
			t.Fatalf("dashboard queried a schema admin-service does not own: %s", sql)
		}
	}
}

func TestDashboardReportsAFailedSourceAsUnavailableNotZero(t *testing.T) {
	q := &fakeQuerier{n: 0, failOn: "admin.suspensions"}
	stats := dashboardStats(context.Background(), q)

	if stats.ActiveSuspensions != nil {
		t.Fatalf("active_suspensions = %d on a failed query, want nil", *stats.ActiveSuspensions)
	}
	if stats.Unavailable["active_suspensions"] != DashboardReasonQueryFailed {
		t.Fatalf("unavailable reason = %q", stats.Unavailable["active_suspensions"])
	}
	// A successful count of zero is a real zero.
	if stats.AdminWritesLast7d == nil || *stats.AdminWritesLast7d != 0 {
		t.Fatalf("admin_writes_last_7d = %v, want a real 0", stats.AdminWritesLast7d)
	}
	if _, listed := stats.Unavailable["admin_writes_last_7d"]; listed {
		t.Fatal("a successful count was marked unavailable")
	}

	body, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"active_suspensions":null`, `"total_users":null`, `"total_posts":null`, `"open_reports":null`} {
		if !strings.Contains(string(body), key) {
			t.Fatalf("missing %s in %s", key, body)
		}
	}
	for _, key := range []string{"total_users", "active_users_today", "total_posts", "new_users_last_7d", "open_reports", "reports_resolved_last_7d", "takedowns_last_7d"} {
		if stats.Unavailable[key] == "" {
			t.Fatalf("%s has no unavailable reason", key)
		}
	}
}
