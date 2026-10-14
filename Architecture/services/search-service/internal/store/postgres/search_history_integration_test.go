//go:build integration

package postgres

// Search history against a real PostgreSQL (T-15): dedupe, refinement
// collapse, the per-user cap, ownership on delete, and the popular-searches
// threshold, window and redaction.
//
// Requires SEARCH_IT_POSTGRES_DSN naming a scratch database whose name ends
// in _test (search_it_test); skipped when unset, refused otherwise. The
// schema is bootstrapped from setup.sql + migrations, exactly as the
// service does on boot. The popular test TRUNCATEs search_history, which is
// only ever allowed on the _test database.
//
//	go test -tags integration ./internal/store/postgres/ -run SearchHistoryIT -count=1

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/search-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func searchHistoryITStore(t *testing.T) (*SearchExtrasStore, *pgxpool.Pool) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("SEARCH_IT_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("SEARCH_IT_POSTGRES_DSN not set; skipping search history integration tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("SEARCH_IT_POSTGRES_DSN does not parse")
	}
	if cfg.Database == "app" || !strings.HasSuffix(cfg.Database, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test (search_it_test)", cfg.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return NewExtrasStore(pool), pool
}

func historyQueries(t *testing.T, s *SearchExtrasStore, user uuid.UUID) []string {
	t.Helper()
	items, err := s.GetSearchHistory(context.Background(), user, 100)
	if err != nil {
		t.Fatalf("GetSearchHistory: %v", err)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Query)
	}
	return out
}

func TestSearchHistoryIT_DedupeMovesToTop(t *testing.T) {
	s, _ := searchHistoryITStore(t)
	ctx := context.Background()
	user := uuid.New()
	for _, q := range []string{"Red Saree", "cricket", "  red   SAREE "} {
		if err := s.RecordSearchHistory(ctx, user, q); err != nil {
			t.Fatalf("record %q: %v", q, err)
		}
	}
	got := historyQueries(t, s, user)
	if len(got) != 2 || got[0] != "red SAREE" || got[1] != "cricket" {
		t.Fatalf("history %q, want [red SAREE cricket] — one row, moved to the top, latest spelling", got)
	}
}

func TestSearchHistoryIT_DedupeKeepsTheID(t *testing.T) {
	s, _ := searchHistoryITStore(t)
	ctx := context.Background()
	user := uuid.New()
	_ = s.RecordSearchHistory(ctx, user, "cats")
	first, _ := s.GetSearchHistory(ctx, user, 1)
	_ = s.RecordSearchHistory(ctx, user, "dogs")
	_ = s.RecordSearchHistory(ctx, user, "Cats")
	again, _ := s.GetSearchHistory(ctx, user, 1)
	if len(first) != 1 || len(again) != 1 || first[0].ID != again[0].ID {
		t.Fatalf("re-search changed the entry id (%v → %v); a client holding the id could not delete it", first, again)
	}
}

func TestSearchHistoryIT_AnonymousAndShortAreIgnored(t *testing.T) {
	s, pool := searchHistoryITStore(t)
	ctx := context.Background()
	if err := s.RecordSearchHistory(ctx, uuid.Nil, "anonymous search"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM search_history WHERE user_id = $1`, uuid.Nil).Scan(&n); err != nil || n != 0 {
		t.Fatalf("anonymous rows=%d err=%v", n, err)
	}
	user := uuid.New()
	_ = s.RecordSearchHistory(ctx, user, " a ")
	_ = s.RecordSearchHistory(ctx, user, "   ")
	if got := historyQueries(t, s, user); len(got) != 0 {
		t.Fatalf("short queries recorded: %q", got)
	}
}

func TestSearchHistoryIT_RefinementCollapses(t *testing.T) {
	s, pool := searchHistoryITStore(t)
	ctx := context.Background()
	user := uuid.New()
	for _, q := range []string{"ca", "cat", "cats"} {
		_ = s.RecordSearchHistory(ctx, user, q)
	}
	if got := historyQueries(t, s, user); len(got) != 1 || got[0] != "cats" {
		t.Fatalf("history %q, want [cats]", got)
	}
	// A shorter query is its own search, not a refinement.
	_ = s.RecordSearchHistory(ctx, user, "ca")
	if got := historyQueries(t, s, user); len(got) != 2 {
		t.Fatalf("history %q, want [ca cats]", got)
	}
	// An old prefix (outside the refine window) is a separate search and stays.
	other := uuid.New()
	_ = s.RecordSearchHistory(ctx, other, "dog")
	if _, err := pool.Exec(ctx, `UPDATE search_history SET searched_at = now() - interval '2 minutes' WHERE user_id = $1`, other); err != nil {
		t.Fatal(err)
	}
	_ = s.RecordSearchHistory(ctx, other, "dog food")
	if got := historyQueries(t, s, other); len(got) != 2 {
		t.Fatalf("history %q, want [dog food, dog]", got)
	}
}

func TestSearchHistoryIT_CapKeepsNewest(t *testing.T) {
	s, _ := searchHistoryITStore(t)
	ctx := context.Background()
	user := uuid.New()
	total := MaxSearchHistory + 5
	for i := 0; i < total; i++ {
		// Distinct, non-prefix queries.
		if err := s.RecordSearchHistory(ctx, user, "query "+string(rune('a'+i))+" x"); err != nil {
			t.Fatal(err)
		}
	}
	got := historyQueries(t, s, user)
	if len(got) != MaxSearchHistory {
		t.Fatalf("kept %d rows, want %d", len(got), MaxSearchHistory)
	}
	if want := "query " + string(rune('a'+total-1)) + " x"; got[0] != want {
		t.Fatalf("newest = %q, want %q", got[0], want)
	}
	for _, q := range got {
		if q == "query a x" {
			t.Fatal("oldest entry survived the cap")
		}
	}
}

func TestSearchHistoryIT_DeleteOwnership(t *testing.T) {
	s, _ := searchHistoryITStore(t)
	ctx := context.Background()
	owner, stranger := uuid.New(), uuid.New()
	_ = s.RecordSearchHistory(ctx, owner, "keep me")
	_ = s.RecordSearchHistory(ctx, owner, "delete me")
	items, _ := s.GetSearchHistory(ctx, owner, 10)
	var target uuid.UUID
	for _, it := range items {
		if it.Query == "delete me" {
			target = it.ID
		}
	}
	if err := s.DeleteSearchHistoryItem(ctx, target, stranger); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger delete: %v, want ErrNotFound", err)
	}
	if err := s.DeleteSearchHistoryItem(ctx, uuid.New(), owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v, want ErrNotFound", err)
	}
	if err := s.DeleteSearchHistoryItem(ctx, target, owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if got := historyQueries(t, s, owner); len(got) != 1 || got[0] != "keep me" {
		t.Fatalf("history %q, want [keep me] — one delete must not clear the rest", got)
	}
}

func TestSearchHistoryIT_PopularThresholdWindowRedaction(t *testing.T) {
	s, pool := searchHistoryITStore(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE search_history`); err != nil {
		t.Fatal(err)
	}
	searchedBy := func(q string, n int) {
		for i := 0; i < n; i++ {
			if err := s.RecordSearchHistory(ctx, uuid.New(), q); err != nil {
				t.Fatal(err)
			}
		}
	}
	searchedBy("Diwali Lights", 6)    // popular
	searchedBy("cricket", 5)          // exactly at threshold
	searchedBy("my ex's name", 4)     // below threshold — never shown
	searchedBy("ravi@example.com", 9) // looks like an email
	searchedBy("call 98765 43210", 9) // looks like a phone number
	searchedBy("old news", 7)         // moved outside the window below
	if _, err := pool.Exec(ctx, `UPDATE search_history SET searched_at = now() - interval '8 days' WHERE query_key = 'old news'`); err != nil {
		t.Fatal(err)
	}
	// One person searching the same thing many times counts once.
	repeat := uuid.New()
	for i := 0; i < 10; i++ {
		_ = s.RecordSearchHistory(ctx, repeat, "lonely query")
	}

	got, err := s.PopularQueries(ctx, 7*24*time.Hour, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	var qs []string
	for _, p := range got {
		qs = append(qs, p.Query)
	}
	if strings.Join(qs, "|") != "diwali lights|cricket" {
		t.Fatalf("popular %q, want [diwali lights cricket]", qs)
	}

	// The floor holds even when a caller asks for less.
	low, err := s.PopularQueries(ctx, 7*24*time.Hour, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range low {
		if p.Users < PopularMinUsersFloor {
			t.Fatalf("%q shown with %d users, below the floor %d", p.Query, p.Users, PopularMinUsersFloor)
		}
		if p.Query == "lonely query" {
			t.Fatal("one person's query was shown as popular")
		}
	}
}
