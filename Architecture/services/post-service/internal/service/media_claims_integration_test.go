//go:build integration

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The media claim protocol (media_claims.go) against a real Postgres, with
// media-service's claim table faked in memory: every holder kind claims
// before it writes, releases what it stops holding, releases a dead row's
// claims, and two concurrent edits of one row never leave its current cover
// unclaimed.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run MediaClaimsIT -v
//
// Refuses any database whose name does not end in _test.

func openMediaClaimsServiceDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := openHubMediaDB(t) // users, media_assets, the real schema; _test only
	for _, ddl := range []string{
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS hls_master_key TEXT`,
	} {
		if _, err := pool.Exec(context.Background(), ddl); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

func claimsITService(t *testing.T, pool *pgxpool.Pool) (*Service, *fakeClaims) {
	t.Helper()
	svc := New(postgres.New(pool), nil, nil)
	claims := newFakeClaims()
	svc.SetMediaClaims(claims)
	return svc, claims
}

func claimsITUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func claimsITAsset(t *testing.T, pool *pgxpool.Pool, owner uuid.UUID, kind string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status)
		VALUES ($1, $2, $3, 'ready', 'passed')`, id, owner, kind); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, id) })
	return id
}

// claimsITDeletePost removes a test post and the engagement counts row its
// insert trigger created (post_engagement_counts.post_id REFERENCES posts).
// It reports whether both deletes succeeded (failures are t.Errorf'd).
func claimsITDeletePost(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) bool {
	t.Helper()
	bg := context.Background()
	ok := true
	for _, stmt := range []string{
		`DELETE FROM post_engagement_counts WHERE post_id = $1`,
		`DELETE FROM posts WHERE id = $1`,
	} {
		if _, err := pool.Exec(bg, stmt, id); err != nil {
			t.Errorf("delete post %s: %q: %v", id, stmt, err)
			ok = false
		}
	}
	return ok
}

func TestMediaClaimsIT_ReelDraftLifecycle(t *testing.T) {
	pool := openMediaClaimsServiceDB(t)
	svc, claims := claimsITService(t, pool)
	ctx := context.Background()
	author := claimsITUser(t, pool)
	source := claimsITAsset(t, pool, author, "video")
	cover1 := claimsITAsset(t, pool, author, "image")
	cover2 := claimsITAsset(t, pool, author, "image")

	d, err := svc.CreateDraft(ctx, &CreateDraftInput{AuthorID: author, MediaID: &source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reel_drafts WHERE id = $1`, d.ID) })
	sameIDs(t, "after create", claims.held(postgres.HolderReelDraft, d.ID), source)

	c1 := cover1.String()
	if _, err := svc.UpdateDraft(ctx, d.ID, author, &UpdateDraftInput{CoverMediaID: &c1}); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "after the first cover", claims.held(postgres.HolderReelDraft, d.ID), source, cover1)

	c2 := cover2.String()
	if _, err := svc.UpdateDraft(ctx, d.ID, author, &UpdateDraftInput{CoverMediaID: &c2}); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "after the cover changed", claims.held(postgres.HolderReelDraft, d.ID), source, cover2)

	// Someone else's delete is a no-op in the store and releases nothing.
	if err := svc.DeleteDraft(ctx, d.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "after a stranger's delete", claims.held(postgres.HolderReelDraft, d.ID), source, cover2)

	if err := svc.DeleteDraft(ctx, d.ID, author); err != nil {
		t.Fatal(err)
	}
	if got := claims.held(postgres.HolderReelDraft, d.ID); len(got) != 0 {
		t.Fatalf("a deleted draft kept its claims: %v", got)
	}
}

func TestMediaClaimsIT_ReelDraftRefusesAStrangersAsset(t *testing.T) {
	pool := openMediaClaimsServiceDB(t)
	svc, claims := claimsITService(t, pool)
	ctx := context.Background()
	author, other := claimsITUser(t, pool), claimsITUser(t, pool)
	theirs := claimsITAsset(t, pool, other, "video")

	if _, err := svc.CreateDraft(ctx, &CreateDraftInput{AuthorID: author, MediaID: &theirs}); err == nil {
		t.Fatal("a draft was created on someone else's upload")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM reel_drafts WHERE author_id = $1`, author).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(*claims.log) != 0 {
		t.Fatalf("rows=%d claim calls=%v; nothing may be written or claimed", n, *claims.log)
	}
}

func TestMediaClaimsIT_EventCoverClaimNamesTheRowAndIsCompensated(t *testing.T) {
	pool := openMediaClaimsServiceDB(t)
	svc, claims := claimsITService(t, pool)
	ctx := context.Background()
	creator := claimsITUser(t, pool)
	cover := claimsITAsset(t, pool, creator, "image")

	e, err := svc.CreateEvent(ctx, CreateEventInput{CreatorID: creator, Title: "launch", StartsAt: time.Now(), CoverMediaID: &cover})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM events WHERE id = $1`, e.ID) })
	sameIDs(t, "event cover claim", claims.held(postgres.HolderEventCover, e.ID), cover)

	// A creator who is not in users fails the insert (events.creator_id
	// REFERENCES users): the claim it made first is released.
	ghost := uuid.New()
	ghostCover := claimsITAsset(t, pool, ghost, "image")
	before := len(claims.claims)
	if _, err := svc.CreateEvent(ctx, CreateEventInput{CreatorID: ghost, Title: "x", StartsAt: time.Now(), CoverMediaID: &ghostCover}); err == nil {
		t.Fatal("the insert was expected to fail")
	}
	for k, ids := range claims.claims {
		if ids[ghostCover] {
			t.Fatalf("a failed insert left its claim (%s %s); claims before=%d", k.kind, k.ref, before)
		}
	}
}

func TestMediaClaimsIT_PurgedPostReleasesItsCover(t *testing.T) {
	pool := openMediaClaimsServiceDB(t)
	svc, claims := claimsITService(t, pool)
	ctx := context.Background()
	author := claimsITUser(t, pool)
	cover := claimsITAsset(t, pool, author, "image")
	live, purged := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{live, purged} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, cover_media_id, created_at, updated_at)
			VALUES ($1, $2, 'c', 'public', 'post', 'approved', $3, NOW(), NOW())`, id, author, cover); err != nil {
			t.Fatal(err)
		}
		_ = claims.Claim(ctx, postgres.HolderPostCover, id, []uuid.UUID{cover})
	}
	t.Cleanup(func() { claimsITDeletePost(t, pool, live) })
	// The purge's end state: the row is gone. What PurgePost deletes is
	// proven by the store's purge ITs; this package's database lacks the
	// tables only cmd/server's ensureSchema creates (reel_crosspost), so
	// the row and the engagement counts row its insert trigger made are
	// removed directly. A failure here fails the test: a post left behind
	// would make the release a no-op and the assertion below meaningless.
	if !claimsITDeletePost(t, pool, purged) {
		t.FailNow()
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE id = $1`, purged).Scan(&left); err != nil || left != 0 {
		t.Fatalf("the purged post is still there (rows=%d, err=%v)", left, err)
	}

	for _, id := range []uuid.UUID{live, purged} {
		if err := svc.ReleasePurgedPostClaims(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if got := claims.held(postgres.HolderPostCover, purged); len(got) != 0 {
		t.Fatalf("the purged post kept its cover claim: %v", got)
	}
	sameIDs(t, "the live post's claim", claims.held(postgres.HolderPostCover, live), cover)
}

// Many concurrent cover changes of one post: whatever the row ends up naming
// is claimed. Without the holder lock, one edit's release of the cover it
// replaced can land after another edit set that same cover again.
func TestMediaClaimsIT_ConcurrentCoverEditsNeverLeaveTheCoverUnclaimed(t *testing.T) {
	pool := openMediaClaimsServiceDB(t)
	svc, claims := claimsITService(t, pool)
	ctx := context.Background()
	author := claimsITUser(t, pool)
	covers := []uuid.UUID{
		claimsITAsset(t, pool, author, "image"), claimsITAsset(t, pool, author, "image"),
		claimsITAsset(t, pool, author, "image"),
	}
	post := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at)
		VALUES ($1, $2, 'c', 'public', 'post', 'approved', NOW(), NOW())`, post, author); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { claimsITDeletePost(t, pool, post) })

	store := postgres.New(pool)
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(cover uuid.UUID) {
			defer wg.Done()
			c := cover
			_ = svc.setPostCoverClaimed(ctx, post, c, func() error { return store.UpdatePostCoverMedia(ctx, post, &c) })
		}(covers[i%len(covers)])
	}
	wg.Wait()

	var final uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT cover_media_id FROM posts WHERE id = $1`, post).Scan(&final); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "claims after the race", claims.held(postgres.HolderPostCover, post), final)
}
