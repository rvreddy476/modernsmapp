//go:build integration

package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/group-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
In-group post search against a real Postgres.

The structural guards in group_post_search_test.go pin the shape of the query.
This pins what Postgres does with it, and there is one claim here that only a
database can settle: that a two-word query does not raise.

  syntax error in tsquery: "book club"

is what to_tsquery returns for the most ordinary input a search box produces,
and it is why SearchGroups has been a 500 for its whole life. No amount of
reading the Go source proves websearch_to_tsquery does not do the same; running
it does.

Runs only under `-tags integration`, and only ever against a database whose
name ends in _test — the fixtures write rows, and this service's scratch
convention is GROUP_POSTGRES_DSN pointing at a *_test database.
*/

type postSearchFixture struct {
	store    *Store
	pool     *pgxpool.Pool
	groupID  uuid.UUID
	author   uuid.UUID
	viewer   uuid.UUID
	postIDs  map[string]uuid.UUID
	anonPost uuid.UUID
	anonAlia uuid.UUID
}

// newPostSearchFixture connects, SKIPS when there is no scratch database
// configured, and REFUSES any database whose name does not end in _test.
func newPostSearchFixture(t *testing.T) (*postSearchFixture, func()) {
	t.Helper()
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("GROUP_POSTGRES_DSN is not set: no scratch database to run against")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: integration tests only run against a *_test database", cfg.ConnConfig.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, database.SetupSQL); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	// Bring an older scratch schema forward for the columns this fixture
	// touches, plus migration 015's index. Every statement is a no-op on an
	// up-to-date database.
	patch, err := database.Migrations.ReadFile("migrations/015_group_post_search.sql")
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'published';
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS is_anonymous BOOLEAN NOT NULL DEFAULT FALSE;
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS anon_alias UUID;
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS cross_post_group_id UUID;
	`+string(patch)); err != nil {
		pool.Close()
		t.Fatal(err)
	}

	f := &postSearchFixture{
		store:    New(pool),
		pool:     pool,
		groupID:  uuid.New(),
		author:   uuid.New(),
		viewer:   uuid.New(),
		postIDs:  map[string]uuid.UUID{},
		anonAlia: uuid.New(),
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO groups (id, name, description, creator_id, visibility) VALUES ($1, $2, '', $3, 'public')`,
		f.groupID, "post-search-"+f.groupID.String()[:8], f.author); err != nil {
		pool.Close()
		t.Fatal(err)
	}

	base := time.Now().Add(-24 * time.Hour)
	seed := []struct {
		key    string
		title  string
		body   string
		status string
	}{
		// The point of the test: a two-word phrase, in the body.
		{"club", "Reading group", "The book club meets on Tuesday", "published"},
		// Title-only match, and a NULL body — which coalesce must survive.
		{"titleonly", "Book swap", "", "published"},
		// Matches the words but is soft-deleted; must never surface.
		{"deleted", "Book club archive", "old book club notes", "deleted"},
		// Matches the words but is awaiting a moderator; must never surface.
		{"pending", "Book club proposal", "a new book club", "pending_approval"},
		// Matches nothing.
		{"other", "Cycling", "we ride on Sundays", "published"},
	}
	for i, s := range seed {
		id := uuid.New()
		var body any = s.body
		if s.body == "" {
			// NULL, not '' — the case coalesce exists for.
			body = nil
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO group_posts (id, group_id, author_id, content_type, title, body, status, created_at, updated_at)
			 VALUES ($1, $2, $3, 'text', $4, $5, $6, $7, $7)`,
			id, f.groupID, f.author.String(), s.title, body, s.status,
			base.Add(time.Duration(-i)*time.Hour)); err != nil {
			pool.Close()
			t.Fatal(err)
		}
		f.postIDs[s.key] = id
	}

	// One anonymous post that matches, to prove the mask survives this path.
	f.anonPost = uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO group_posts (id, group_id, author_id, content_type, title, body, status, is_anonymous, anon_alias, created_at, updated_at)
		 VALUES ($1, $2, $3, 'text', NULL, $4, 'published', TRUE, $5, $6, $6)`,
		f.anonPost, f.groupID, f.author.String(), "anonymous book club confession",
		f.anonAlia, base.Add(-10*time.Hour)); err != nil {
		pool.Close()
		t.Fatal(err)
	}

	cleanup := func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM group_posts WHERE group_id = $1`, f.groupID)
		pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, f.groupID)
		pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, f.groupID)
		pool.Close()
	}
	return f, cleanup
}

func idsOf(posts []GroupPostV2) map[uuid.UUID]bool {
	set := map[uuid.UUID]bool{}
	for _, p := range posts {
		set[p.ID] = true
	}
	return set
}

// The whole reason websearch_to_tsquery was chosen. Under to_tsquery this call
// returns a Postgres syntax error rather than rows.
func TestSearchGroupPostsV2AcceptsOrdinaryMultiWordInput(t *testing.T) {
	f, cleanup := newPostSearchFixture(t)
	defer cleanup()
	ctx := context.Background()

	for _, q := range []string{
		"book club",              // two bare words: the to_tsquery crash
		`"book club"`,            // a quoted phrase
		"book or cycling",        // websearch's OR
		"book -cycling",          // websearch's negation
		"&",                      // a bare operator, which to_tsquery also rejects
		"!",                      //
		"book & club",            //
		")((",                    //
		"  ",                     // whitespace only
		"日本語 読書",                 // non-Latin
		strings.Repeat("a", 300), // long
	} {
		if _, err := f.store.SearchGroupPostsV2(ctx, f.groupID, q, f.viewer.String(), 20, 0); err != nil {
			t.Errorf("SearchGroupPostsV2(%q) errored: %v — a search box must not be able to produce a Postgres syntax error", q, err)
		}
	}
}

func TestSearchGroupPostsV2FindsMatchesAndNothingElse(t *testing.T) {
	f, cleanup := newPostSearchFixture(t)
	defer cleanup()
	ctx := context.Background()

	posts, err := f.store.SearchGroupPostsV2(ctx, f.groupID, "book club", f.viewer.String(), 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := idsOf(posts)

	if !got[f.postIDs["club"]] {
		t.Error("the matching published post was not returned")
	}
	if got[f.postIDs["other"]] {
		t.Error("a post matching neither word was returned")
	}

	// The two that must never surface, whatever they contain.
	if got[f.postIDs["deleted"]] {
		t.Error("a soft-deleted post was returned — search must not read what the feed refuses to show")
	}
	if got[f.postIDs["pending"]] {
		t.Error("a post awaiting approval was returned — it is not in the group yet")
	}

	// A post whose body is NULL still matches on its title. Without coalesce
	// the concatenation is NULL and this post is invisible for ever.
	titleOnly, err := f.store.SearchGroupPostsV2(ctx, f.groupID, "swap", f.viewer.String(), 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !idsOf(titleOnly)[f.postIDs["titleonly"]] {
		t.Error("a post with a NULL body did not match on its title — coalesce is missing from the search vector")
	}
}

// An empty result is an empty slice, and never an error.
func TestSearchGroupPostsV2WithNoMatchesReturnsAnEmptySlice(t *testing.T) {
	f, cleanup := newPostSearchFixture(t)
	defer cleanup()

	posts, err := f.store.SearchGroupPostsV2(context.Background(), f.groupID,
		"zzzznothingmatchesthis", f.viewer.String(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if posts == nil {
		t.Error("a search with no hits returned nil, which serialises as null rather than []")
	}
	if len(posts) != 0 {
		t.Errorf("got %d posts for a query that matches nothing", len(posts))
	}
}

// The mask is on the type, so it applies here too — but this is the path a
// reader added today takes, and it is cheap to prove rather than assume.
func TestSearchGroupPostsV2MasksAnonymousAuthors(t *testing.T) {
	f, cleanup := newPostSearchFixture(t)
	defer cleanup()

	posts, err := f.store.SearchGroupPostsV2(context.Background(), f.groupID,
		"confession", f.viewer.String(), 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	var anon *GroupPostV2
	for i := range posts {
		if posts[i].ID == f.anonPost {
			anon = &posts[i]
		}
	}
	if anon == nil {
		t.Fatal("the anonymous post did not match its own body text")
	}
	// The ROW keeps the real author — bans and ownership need it.
	if anon.AuthorID != f.author.String() {
		t.Errorf("the row lost the real author: %q", anon.AuthorID)
	}
	// The WIRE must not.
	wire, err := json.Marshal(anon)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), f.author.String()) {
		t.Errorf("search serialised the real author of an anonymous post: %s", wire)
	}
	if !strings.Contains(string(wire), f.anonAlia.String()) {
		t.Errorf("search did not serialise the post's alias: %s", wire)
	}
}

// The feed's caps, applied identically.
func TestSearchGroupPostsV2PaginationMatchesTheFeedsCaps(t *testing.T) {
	f, cleanup := newPostSearchFixture(t)
	defer cleanup()
	ctx := context.Background()

	// Over the cap clamps to 20 rather than erroring, exactly as
	// ListGroupPostsV2 does. The fixture has fewer than 20 matching rows, so
	// this asserts the call succeeds and is bounded.
	if _, err := f.store.SearchGroupPostsV2(ctx, f.groupID, "book", f.viewer.String(), 10000, 0); err != nil {
		t.Errorf("an over-cap limit errored instead of clamping: %v", err)
	}
	if _, err := f.store.SearchGroupPostsV2(ctx, f.groupID, "book", f.viewer.String(), 0, -5); err != nil {
		t.Errorf("a zero limit and negative offset errored instead of clamping: %v", err)
	}

	page1, err := f.store.SearchGroupPostsV2(ctx, f.groupID, "book", f.viewer.String(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	page2, err := f.store.SearchGroupPostsV2(ctx, f.groupID, "book", f.viewer.String(), 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 1 || len(page2) != 1 {
		t.Fatalf("expected one row per page, got %d and %d", len(page1), len(page2))
	}
	if page1[0].ID == page2[0].ID {
		t.Error("offset had no effect: both pages returned the same post")
	}
}
