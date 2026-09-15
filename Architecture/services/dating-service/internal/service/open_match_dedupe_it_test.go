// Boot cleanup of duplicate open matches: dev closes the extras with one
// dating.match.closed each and the keep rule; non-dev refuses without the
// flag and closes nothing; non-dev with the flag closes; no duplicates means
// no events and the index ensured. Skipped without TEST_PG_DSN. Each test
// drops uq_dating_matches_open_pair to seed duplicates and restores it.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// itPool opens a second pool on TEST_PG_DSN for raw seeding (call after
// newD3Svc, which skips without the DSN and checks the *_test name).
func itPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), os.Getenv("TEST_PG_DSN"))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func dropOpenPairIndex(t *testing.T, pool *pgxpool.Pool, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DROP INDEX IF EXISTS `+store.OpenPairIndexName); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `
            WITH ranked AS (
                SELECT id, row_number() OVER (PARTITION BY user_a, user_b ORDER BY id) AS rn
                FROM dating_matches WHERE status IN ('matched','conversing','quiet'))
            UPDATE dating_matches SET status = 'closed', closed_at = now()
            WHERE id IN (SELECT id FROM ranked WHERE rn > 1)`); err != nil {
			t.Errorf("cleanup duplicates: %v", err)
		}
		if _, err := pool.Exec(ctx, store.OpenPairIndexDDL); err != nil {
			t.Errorf("restore index: %v", err)
		}
	})
	if c, err := st.CountDuplicateOpenMatches(ctx); err != nil || c.ExtraRows != 0 {
		t.Fatalf("baseline duplicates = %+v, %v; want none", c, err)
	}
}

func seedOpenMatch(t *testing.T, pool *pgxpool.Pool, x, y uuid.UUID, withConversation bool, lastActivity time.Time) uuid.UUID {
	t.Helper()
	a, b := x, y
	if y.String() < x.String() {
		a, b = y, x
	}
	var conv *uuid.UUID
	if withConversation {
		c := uuid.New()
		conv = &c
	}
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(), `
        INSERT INTO dating_matches (user_a, user_b, status, conversation_id, matched_at)
        VALUES ($1, $2, 'matched', $3, $4) RETURNING id`, a, b, conv, lastActivity).Scan(&id); err != nil {
		t.Fatalf("seed match: %v", err)
	}
	return id
}

type closedEvent struct {
	MatchID  string `json:"match_id"`
	ClosedBy string `json:"closed_by"`
	UserA    string `json:"user_a"`
	UserB    string `json:"user_b"`
}

func matchClosedSince(t *testing.T, rec *recordingWriter, from int) []closedEvent {
	t.Helper()
	var out []closedEvent
	for _, ev := range rec.events(t)[from:] {
		if ev.EventType != "dating.match.closed" {
			continue
		}
		var p closedEvent
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("decode match.closed: %v", err)
		}
		out = append(out, p)
	}
	return out
}

func matchState(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (status string, closedBy *uuid.UUID, reason *string, closedAt *time.Time) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), `
        SELECT status, closed_by, close_reason, closed_at FROM dating_matches WHERE id = $1`, id).
		Scan(&status, &closedBy, &reason, &closedAt); err != nil {
		t.Fatalf("load match %s: %v", id, err)
	}
	return
}

func indexExists(t *testing.T, st *store.Store) bool {
	t.Helper()
	ok, err := st.OpenPairIndexExists(context.Background())
	if err != nil {
		t.Fatalf("index check: %v", err)
	}
	return ok
}

func TestDedupe_DevClosesExtrasWithOneEventEachAndKeepRule(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	pool := itPool(t)
	ctx := context.Background()
	dropOpenPairIndex(t, pool, st)
	now := time.Now()

	p1a, p1b := uuid.New(), uuid.New()
	keep1 := seedOpenMatch(t, pool, p1a, p1b, true, now.Add(-10*24*time.Hour)) // has the conversation
	extra1 := seedOpenMatch(t, pool, p1a, p1b, false, now.Add(-time.Hour))
	extra2 := seedOpenMatch(t, pool, p1a, p1b, false, now.Add(-5*24*time.Hour))
	p2a, p2b := uuid.New(), uuid.New()
	keep2 := seedOpenMatch(t, pool, p2a, p2b, false, now.Add(-time.Hour)) // most recent activity
	extra3 := seedOpenMatch(t, pool, p2a, p2b, false, now.Add(-3*time.Hour))
	if _, err := pool.Exec(ctx, `INSERT INTO dating_sparks (from_user_id, to_user_id, target_kind, target_ref) VALUES ($1, $2, 'photo', '0')`, p1a, p1b); err != nil {
		t.Fatalf("seed spark: %v", err)
	}
	before := len(rec.events(t))

	report, err := svc.ReconcileOpenMatchDuplicates(ctx, OpenMatchDedupePolicy{Env: "dev", LocalEnv: true})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.Groups != 2 || report.ExtraRows != 3 || report.Closed != 3 || !report.IndexCreated {
		t.Fatalf("report = %+v; want 2 groups, 3 extra, 3 closed, index created", report)
	}

	events := matchClosedSince(t, rec, before)
	if len(events) != 3 {
		t.Fatalf("%d dating.match.closed events, want exactly one per closed row (3)", len(events))
	}
	wantPairs := map[string][2]uuid.UUID{extra1.String(): {p1a, p1b}, extra2.String(): {p1a, p1b}, extra3.String(): {p2a, p2b}}
	seen := map[string]bool{}
	for _, ev := range events {
		pair, ok := wantPairs[ev.MatchID]
		if !ok || seen[ev.MatchID] {
			t.Fatalf("unexpected or repeated match.closed for %s", ev.MatchID)
		}
		seen[ev.MatchID] = true
		if ev.ClosedBy != store.SystemActorID.String() {
			t.Fatalf("closed_by = %s, want the system actor", ev.ClosedBy)
		}
		gotPair := map[string]bool{ev.UserA: true, ev.UserB: true}
		if !gotPair[pair[0].String()] || !gotPair[pair[1].String()] {
			t.Fatalf("match.closed %s carries the wrong pair", ev.MatchID)
		}
	}
	for _, id := range []uuid.UUID{extra1, extra2, extra3} {
		status, closedBy, reason, closedAt := matchState(t, pool, id)
		if status != "closed" || closedBy == nil || *closedBy != store.SystemActorID ||
			reason == nil || *reason != store.CloseReasonDuplicate || closedAt == nil {
			t.Fatalf("extra %s = %s by %v reason %v at %v; want closed by the system actor as duplicate", id, status, closedBy, reason, closedAt)
		}
	}
	for _, id := range []uuid.UUID{keep1, keep2} {
		if status, _, _, _ := matchState(t, pool, id); status != "matched" {
			t.Fatalf("kept match %s is %s; keep rule broken", id, status)
		}
	}
	if !indexExists(t, st) {
		t.Fatalf("unique index missing after the cleanup")
	}
	if n := sparksBetween(t, st, p1a, p1b); n != 1 {
		t.Fatalf("duplicate cleanup deleted the kept match's sparks (%d left)", n)
	}
	if c, _ := st.CountDuplicateOpenMatches(ctx); c.ExtraRows != 0 {
		t.Fatalf("duplicates remain: %+v", c)
	}
}

func TestDedupe_NonDevWithoutFlagRefusesWithCountsAndClosesNothing(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	pool := itPool(t)
	ctx := context.Background()
	dropOpenPairIndex(t, pool, st)
	now := time.Now()
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ids := []uuid.UUID{
		seedOpenMatch(t, pool, a, b, true, now),
		seedOpenMatch(t, pool, a, b, false, now),
		seedOpenMatch(t, pool, a, b, false, now),
		seedOpenMatch(t, pool, c, d, false, now),
		seedOpenMatch(t, pool, c, d, false, now),
	}
	before := len(rec.events(t))

	for _, env := range []string{"prod", "staging", ""} {
		report, err := svc.ReconcileOpenMatchDuplicates(ctx, OpenMatchDedupePolicy{Env: env})
		if !errors.Is(err, ErrDuplicateOpenMatches) {
			t.Fatalf("ENV=%q: err=%v, want ErrDuplicateOpenMatches", env, err)
		}
		msg := err.Error()
		for _, want := range []string{"2 pairs hold 3 extra open matches", "DATING_DEDUPE_OPEN_MATCHES=true", "count-duplicate-matches.sql"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("ENV=%q: error %q lacks %q", env, msg, want)
			}
		}
		if report.Groups != 2 || report.ExtraRows != 3 || report.Closed != 0 || report.IndexCreated {
			t.Fatalf("ENV=%q: report = %+v", env, report)
		}
	}
	if n := len(rec.events(t)) - before; n != 0 {
		t.Fatalf("a refused boot emitted %d events", n)
	}
	for _, id := range ids {
		if status, _, _, _ := matchState(t, pool, id); status != "matched" {
			t.Fatalf("match %s was closed (%s) without the flag", id, status)
		}
	}
	if indexExists(t, st) {
		t.Fatalf("index created while duplicates remain")
	}
}

func TestDedupe_NonDevWithFlagClosesWithEvents(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	pool := itPool(t)
	ctx := context.Background()
	dropOpenPairIndex(t, pool, st)
	a, b := uuid.New(), uuid.New()
	keep := seedOpenMatch(t, pool, a, b, true, time.Now().Add(-48*time.Hour))
	extra := seedOpenMatch(t, pool, a, b, false, time.Now())
	before := len(rec.events(t))

	report, err := svc.ReconcileOpenMatchDuplicates(ctx, OpenMatchDedupePolicy{Env: "prod", FlagEnabled: true})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.Groups != 1 || report.ExtraRows != 1 || report.Closed != 1 || !report.IndexCreated {
		t.Fatalf("report = %+v", report)
	}
	events := matchClosedSince(t, rec, before)
	if len(events) != 1 || events[0].MatchID != extra.String() {
		t.Fatalf("events = %+v; want one match.closed for %s", events, extra)
	}
	if status, _, _, _ := matchState(t, pool, extra); status != "closed" {
		t.Fatalf("extra is %s", status)
	}
	if status, _, _, _ := matchState(t, pool, keep); status != "matched" {
		t.Fatalf("kept match is %s", status)
	}
	if !indexExists(t, st) {
		t.Fatalf("index missing")
	}
}

func TestDedupe_NoDuplicatesNoEventsIndexEnsured(t *testing.T) {
	svc, st, rec := newD3Svc(t)
	pool := itPool(t)
	ctx := context.Background()
	dropOpenPairIndex(t, pool, st)
	seedOpenMatch(t, pool, uuid.New(), uuid.New(), false, time.Now())
	before := len(rec.events(t))

	report, err := svc.ReconcileOpenMatchDuplicates(ctx, OpenMatchDedupePolicy{Env: "prod"})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report != (OpenMatchDedupeReport{IndexCreated: true}) {
		t.Fatalf("report = %+v; want nothing found and the index created", report)
	}
	again, err := svc.ReconcileOpenMatchDuplicates(ctx, OpenMatchDedupePolicy{Env: "prod"})
	if err != nil || again != (OpenMatchDedupeReport{}) {
		t.Fatalf("second boot = %+v, %v; want a no-op", again, err)
	}
	if n := len(rec.events(t)) - before; n != 0 {
		t.Fatalf("no duplicates but %d events", n)
	}
	if !indexExists(t, st) {
		t.Fatalf("index not ensured")
	}
}
