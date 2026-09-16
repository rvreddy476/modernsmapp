//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

// GET /v1/admin/audit's query against a real Postgres (admin_it_test).
func TestListAuditTrail_FiltersRestrictionAndCursor(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	appA, appB := "ita_"+suffix, "itb_"+suffix
	actor := uuid.NewString()
	start := time.Now().Add(-time.Minute)

	write := func(app, op, outcome string) {
		t.Helper()
		if err := store.RecordAdminWrite(ctx, postgres.AdminAuditEntry{
			Actor: actor, App: app, Operation: op, TargetType: "thing", TargetID: uuid.NewString(),
			Reason: "private reason", Outcome: outcome, StatusCode: 200, Payload: map[string]any{"secret": "x"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 5; i++ {
		op, outcome := "op.read", postgres.AuditOutcomeSuccess
		if i%2 == 1 {
			op, outcome = "op.write", postgres.AuditOutcomeDenied
		}
		write(appA, op, outcome)
	}
	for i := 0; i < 3; i++ {
		write(appB, "op.read", postgres.AuditOutcomeSuccess)
	}

	// Restricted to appA, paged two at a time: 2, 2, 1, newest first, no repeats.
	seen := map[string]bool{}
	var last time.Time
	cursor, pages := "", 0
	for {
		page, err := store.ListAuditTrail(ctx, postgres.AuditTrailFilter{Apps: []string{appA}, Actor: actor, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, e := range page.Items {
			if e.App != appA || e.Actor != actor || seen[e.ID] {
				t.Fatalf("row %+v (repeat=%v)", e, seen[e.ID])
			}
			if !last.IsZero() && e.CreatedAt.After(last) {
				t.Fatalf("not newest first: %v after %v", e.CreatedAt, last)
			}
			seen[e.ID], last = true, e.CreatedAt
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 5 || pages != 3 {
		t.Fatalf("rows=%d pages=%d, want 5 rows over 3 pages", len(seen), pages)
	}

	count := func(f postgres.AuditTrailFilter) int {
		t.Helper()
		f.Actor, f.Limit = actor, 200
		page, err := store.ListAuditTrail(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Items)
	}
	now := time.Now().Add(time.Minute)
	future := now.Add(time.Hour)
	for name, c := range map[string]struct {
		f    postgres.AuditTrailFilter
		want int
	}{
		"every app":                       {postgres.AuditTrailFilter{}, 8},
		"app filter":                      {postgres.AuditTrailFilter{App: appB}, 3},
		"restriction wins over app":       {postgres.AuditTrailFilter{Apps: []string{appA}, App: appB}, 0},
		"empty restriction reads nothing": {postgres.AuditTrailFilter{Apps: []string{}}, 0},
		"operation":                       {postgres.AuditTrailFilter{App: appA, Operation: "op.write"}, 2},
		"outcome":                         {postgres.AuditTrailFilter{Apps: []string{appA, appB}, Outcome: postgres.AuditOutcomeSuccess}, 6},
		"date range":                      {postgres.AuditTrailFilter{App: appA, From: &start, To: &now}, 5},
		"from the future":                 {postgres.AuditTrailFilter{From: &future}, 0},
		"before the rows":                 {postgres.AuditTrailFilter{To: &start}, 0},
	} {
		if got := count(c.f); got != c.want {
			t.Fatalf("%s: %d rows, want %d", name, got, c.want)
		}
	}

	if _, err := store.ListAuditTrail(ctx, postgres.AuditTrailFilter{Cursor: "not-a-cursor"}); !errors.Is(err, postgres.ErrInvalidAuditCursor) {
		t.Fatalf("bad cursor err = %v", err)
	}
}
