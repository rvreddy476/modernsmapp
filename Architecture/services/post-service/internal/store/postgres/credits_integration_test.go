//go:build integration

// Video credits (2026-10-23) on the real schema: migration 070's CHECKs, the
// replace inside the create and edit transactions (status kept for the same
// user and role, the ledger, the request events), the uploader's daily
// tagging limit under concurrency, the consent transitions and their event,
// the search projection, and the two lists' filters.
//
//	CREDITS_POSTGRES_DSN=postgres://…/credits_it_test go test -tags integration ./internal/store/postgres/ -run Credits -v
//
// The database name must end in _test.

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openCreditsDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CREDITS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("CREDITS_POSTGRES_DSN is required")
	}
	name := dsn[strings.LastIndex(dsn, "/")+1:]
	if i := strings.Index(name, "?"); i >= 0 {
		name = name[:i]
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against a database whose name does not end in _test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL, processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending', duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`); err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_text TEXT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN NOT NULL DEFAULT FALSE`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap real schema: %v", err)
	}
	// The migration is idempotent: applying it again changes nothing.
	ddl, err := database.Migrations.ReadFile("migrations/070_video_credits.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(ddl)); err != nil {
		t.Fatalf("re-apply 070: %v", err)
	}
	return pool
}

func creditsLongVideo(author uuid.UUID, visibility string) *store.Post {
	return &store.Post{
		ID: uuid.New(), AuthorID: author, Text: "credits proof", Visibility: visibility, ContentType: "long_video",
		ContentTypeExplicit: true, PostType: "video", AppOrigin: "posttube", Title: "Credits proof", Language: "en",
		License: "standard", RemixSetting: "allow", CommentModeration: "none", CommentAccess: "everyone",
		ReviewStatus: "approved", CreatedAt: time.Now().UTC(),
	}
}

func creditOutboxCount(t *testing.T, pool *pgxpool.Pool, eventType string, postID uuid.UUID) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM post_outbox_events WHERE event_type = $1 AND aggregate_id = $2`, eventType, postID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCredits_MigrationConstraints(t *testing.T) {
	pool := openCreditsDB(t)
	ctx := context.Background()
	s := store.New(pool)
	p := creditsLongVideo(uuid.New(), "public")
	if err := s.CreatePostWithEventIdempotent(ctx, p, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	insert := func(status string, user *uuid.UUID, name string, pos int) error {
		_, err := pool.Exec(ctx, `INSERT INTO post_credits (id, post_id, position, name, role, user_id, status) VALUES ($1,$2,$3,$4,'Actor',$5,$6)`,
			uuid.New(), p.ID, pos, name, user, status)
		return err
	}
	u := uuid.New()
	for label, err := range map[string]error{
		"bad status":       insert("maybe", &u, "n", 0),
		"pending w/o user": insert("pending", nil, "n", 0),
		"unlinked w/ user": insert("unlinked", &u, "n", 0),
		"empty name":       insert("unlinked", nil, "", 0),
		"position 30":      insert("unlinked", nil, "n", 30),
	} {
		if err == nil {
			t.Errorf("%s accepted", label)
		}
	}
	if err := insert("pending", &u, "n", 0); err != nil {
		t.Fatalf("valid row refused: %v", err)
	}
	for label, q := range map[string]string{
		"4 genres":    `UPDATE posts SET genres = '{a,b,c,d}' WHERE id = $1`,
		"year 1899":   `UPDATE posts SET release_year = 1899 WHERE id = $1`,
		"empty house": `UPDATE posts SET production_house = '' WHERE id = $1`,
	} {
		if _, err := pool.Exec(ctx, q, p.ID); err == nil {
			t.Errorf("%s accepted", label)
		}
	}
	// Deleting the post takes its credits. setup.sql's insert trigger gave the
	// post a post_engagement_counts row, whose FK has no ON DELETE (the purge
	// path deletes it first), so it goes before the post, as in PurgePost.
	if _, err := pool.Exec(ctx, `DELETE FROM post_engagement_counts WHERE post_id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM posts WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_credits WHERE post_id = $1`, p.ID).Scan(&n)
	if n != 0 {
		t.Fatal("credits outlived their post")
	}
}

func TestCredits_CreateEditKeepsStatusAndAnnounces(t *testing.T) {
	pool := openCreditsDB(t)
	ctx := context.Background()
	s := store.New(pool)
	owner, asha, ravi := uuid.New(), uuid.New(), uuid.New()
	year, house := 2024, "Blue Hour"
	p := creditsLongVideo(owner, "public")
	p.Genres, p.ReleaseYear, p.ProductionHouse = []string{"drama"}, &year, &house
	p.CreditsWrite = &store.CreditsWrite{PostTitle: p.Title, UploaderName: "Owner", Candidates: []store.CreditCandidate{
		{Name: "Asha", Role: "Director", UserID: &asha},
		{Name: "Me", Role: "Producer", UserID: &owner, Self: true},
		{Name: "Plain", Role: "Music"},
	}}
	if err := s.CreatePostWithEventIdempotent(ctx, p, "", nil, nil); err != nil {
		t.Fatal(err)
	}
	if r := p.CreditsWrite.Result; len(r.Credits) != 3 || len(r.NewPending) != 1 || r.Skipped != 0 {
		t.Fatalf("create result = %+v", r)
	}
	if creditOutboxCount(t, pool, events.EventPostCreditRequested, p.ID) != 1 {
		t.Fatal("one request event expected (not for the self-credit or the text)")
	}
	stored, err := s.ListPostCredits(ctx, p.ID)
	if err != nil || len(stored) != 3 || stored[0].Status != store.CreditPending || stored[1].Status != store.CreditAccepted {
		t.Fatalf("stored = %+v %v", stored, err)
	}
	ashaCredit := stored[0].ID

	// Asha accepts; the uploader edits the list (adds Ravi, renames Asha's
	// credit, same role): Asha's row keeps its id and accepted status, and
	// only Ravi is asked.
	if st, err := s.RespondToCredit(ctx, ashaCredit, asha, store.CreditActionAccept); err != nil || st != store.CreditAccepted {
		t.Fatalf("accept = %q %v", st, err)
	}
	w := &store.CreditsWrite{Candidates: []store.CreditCandidate{
		{Name: "Asha Rao", Role: "director", UserID: &asha},
		{Name: "Ravi", Role: "Actor", UserID: &ravi},
	}}
	genres := []string{"drama", "romance"}
	if _, err := s.UpdatePostFields(ctx, p.ID, owner, store.PostEditPatch{Credits: w, Genres: &genres}); err != nil {
		t.Fatal(err)
	}
	stored, _ = s.ListPostCredits(ctx, p.ID)
	if len(stored) != 2 || stored[0].ID != ashaCredit || stored[0].Status != store.CreditAccepted || stored[0].Name != "Asha Rao" {
		t.Fatalf("after edit = %+v", stored)
	}
	if creditOutboxCount(t, pool, events.EventPostCreditRequested, p.ID) != 2 || creditOutboxCount(t, pool, events.EventPostCreditAccepted, p.ID) != 1 {
		t.Fatal("events after edit")
	}
	// The edit re-projected search with the new names.
	var payload []byte
	if err := pool.QueryRow(ctx, `SELECT payload FROM post_outbox_events WHERE event_type = $1 AND aggregate_id = $2 ORDER BY created_at DESC LIMIT 1`,
		events.PostSearchEligibilityChanged, p.ID).Scan(&payload); err != nil {
		t.Fatalf("no search event on a credits edit: %v", err)
	}
	var elig events.PostSearchEligibilityChangedPayload
	_ = json.Unmarshal(payload, &elig)
	if strings.Join(elig.CreditNames, ",") != "Asha Rao,Ravi" || strings.Join(elig.Genres, ",") != "drama,romance" || elig.ReleaseYear != 2024 {
		t.Fatalf("search payload = %+v", elig.PostCreditSearchFields)
	}

	// Consent: only the tagged person, idempotent, one accepted event.
	if _, err := s.RespondToCredit(ctx, ashaCredit, owner, store.CreditActionDecline); !errors.Is(err, store.ErrCreditNotFound) {
		t.Fatalf("the uploader answered for Asha: %v", err)
	}
	if st, _ := s.RespondToCredit(ctx, ashaCredit, asha, store.CreditActionAccept); st != store.CreditAccepted {
		t.Fatal("repeated accept")
	}
	if creditOutboxCount(t, pool, events.EventPostCreditAccepted, p.ID) != 1 {
		t.Fatal("a repeated accept announced twice")
	}
	if st, err := s.RespondToCredit(ctx, ashaCredit, asha, store.CreditActionRemove); err != nil || st != store.CreditUnlinked {
		t.Fatalf("remove = %q %v", st, err)
	}
	stored, _ = s.ListPostCredits(ctx, p.ID)
	if stored[0].UserID != nil || stored[0].Name != "Asha Rao" {
		t.Fatalf("after remove = %+v", stored[0])
	}
	if _, err := s.RespondToCredit(ctx, ashaCredit, asha, store.CreditActionRemove); !errors.Is(err, store.ErrCreditNotFound) {
		t.Fatal("a removed link can still be answered")
	}
}

func TestCredits_DailyLimitHoldsUnderConcurrency(t *testing.T) {
	pool := openCreditsDB(t)
	ctx := context.Background()
	s := store.New(pool)
	owner := uuid.New()
	var wg sync.WaitGroup
	results := make([]store.CreditReplaceResult, 3)
	for i := range results {
		p := creditsLongVideo(owner, "public")
		var cands []store.CreditCandidate
		for j := 0; j < 10; j++ {
			id := uuid.New()
			cands = append(cands, store.CreditCandidate{Name: "n", Role: "Actor", UserID: &id})
		}
		p.CreditsWrite = &store.CreditsWrite{Candidates: cands}
		wg.Add(1)
		go func(i int, p *store.Post) {
			defer wg.Done()
			if err := s.CreatePostWithEventIdempotent(ctx, p, "", nil, nil); err != nil {
				t.Error(err)
			}
			results[i] = p.CreditsWrite.Result
		}(i, p)
	}
	wg.Wait()
	pending, skipped := 0, 0
	for _, r := range results {
		pending += len(r.NewPending)
		skipped += r.Skipped
	}
	if pending != store.CreditTagsPerDay || skipped != 30-store.CreditTagsPerDay {
		t.Fatalf("pending=%d skipped=%d, want %d and %d", pending, skipped, store.CreditTagsPerDay, 30-store.CreditTagsPerDay)
	}
	// Removing tags does not give the slots back.
	var ledger int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_credit_tag_events WHERE uploader_id = $1`, owner).Scan(&ledger)
	if ledger != store.CreditTagsPerDay {
		t.Fatalf("ledger = %d", ledger)
	}
}

func TestCredits_ListsFilter(t *testing.T) {
	pool := openCreditsDB(t)
	ctx := context.Background()
	s := store.New(pool)
	owner, person := uuid.New(), uuid.New()
	mk := func(visibility string, status string, mutate string) uuid.UUID {
		p := creditsLongVideo(owner, visibility)
		if err := s.CreatePostWithEventIdempotent(ctx, p, "", nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO post_credits (id, post_id, position, name, role, user_id, status) VALUES ($1,$2,0,'P','Actor',$3,$4)`,
			uuid.New(), p.ID, person, status); err != nil {
			t.Fatal(err)
		}
		if mutate != "" {
			if _, err := pool.Exec(ctx, mutate, p.ID); err != nil {
				t.Fatal(err)
			}
		}
		return p.ID
	}
	public := mk("public", "accepted", "")
	mk("private", "accepted", "")
	mk("unlisted", "accepted", "")
	mk("public", "accepted", `UPDATE posts SET deleted_at = NOW() WHERE id = $1`)
	mk("public", "accepted", `UPDATE posts SET tier_required_id = gen_random_uuid() WHERE id = $1`)
	mk("public", "accepted", `UPDATE posts SET review_status = 'pending' WHERE id = $1`)
	mk("public", "accepted", `UPDATE posts SET content_type = 'flick' WHERE id = $1`)
	mk("public", "pending", "")
	mk("public", "declined", "")
	ids, _, err := s.ListAppearancePostIDs(ctx, person, nil, 20)
	if err != nil || len(ids) != 1 || ids[0] != public {
		t.Fatalf("appearances = %v %v, want only %s", ids, err, public)
	}
	reqs, _, err := s.ListCreditRequests(ctx, person, nil, 20)
	if err != nil || len(reqs) != 1 {
		t.Fatalf("requests = %+v %v, want the one pending", reqs, err)
	}
	if other, _, _ := s.ListCreditRequests(ctx, owner, nil, 20); len(other) != 0 {
		t.Fatal("the uploader sees the tagged person's requests")
	}
	// Paging: two pages of one.
	mk("public", "accepted", "")
	page1, next, _ := s.ListAppearancePostIDs(ctx, person, nil, 1)
	page2, _, _ := s.ListAppearancePostIDs(ctx, person, next, 1)
	if len(page1) != 1 || next == nil || len(page2) != 1 || page1[0] == page2[0] {
		t.Fatalf("paging = %v %v %v", page1, next, page2)
	}
}
