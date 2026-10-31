//go:build integration

// The media claim holders (media_holders.go) on the real schema: the per-row
// lock really waits, each holder kind re-reads what its row holds and whether
// it is live, the pager walks every row that names media, and the orphan
// pager reads media-service's media_claims for this claimant only.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run MediaClaimsIT -v
//
// Refuses any database whose name does not end in _test.

package postgres_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openMediaClaimsDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse: %v", err)
	}
	if db := cfg.Database; db == "app" || !strings.HasSuffix(db, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", db)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS users (id UUID PRIMARY KEY)`,
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL,
			processing_status TEXT NOT NULL, moderation_status TEXT NOT NULL DEFAULT 'pending',
			duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS hls_master_key TEXT`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	// media-service's table (its migration 031), so the orphan pager reads
	// the shape it reads in the shared app database. No FK here: this
	// database's media_assets is a stub.
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS media_claims (
		media_id UUID NOT NULL, claimant TEXT NOT NULL, claim_kind TEXT NOT NULL,
		claim_ref TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (media_id, claimant, claim_kind, claim_ref))`); err != nil {
		t.Fatal(err)
	}
	return pool
}

func claimsITUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `INSERT INTO users (id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func claimsITPost(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, cover *uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, cover_media_id, created_at, updated_at)
		VALUES ($1, $2, 'cover', 'public', 'post', 'approved', $3, NOW(), NOW())`, id, author, cover); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM post_engagement_counts WHERE post_id = $1`, id)
		_, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, id)
	})
	return id
}

func claimsITDraft(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, media, cover *uuid.UUID, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO reel_drafts (id, author_id, media_id, cover_media_id, status) VALUES ($1, $2, $3, $4, $5)`,
		id, author, media, cover, status); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM reel_drafts WHERE id = $1`, id) })
	return id
}

func TestMediaClaimsIT_HolderLockSerialises(t *testing.T) {
	pool := openMediaClaimsDB(t)
	s := store.New(pool)
	ctx := context.Background()
	ref := uuid.New()

	unlock, err := s.LockMediaHolder(ctx, store.HolderPostCover, ref)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		u2, err := s.LockMediaHolder(ctx, store.HolderPostCover, ref)
		if err == nil {
			u2()
		}
		close(got)
	}()
	select {
	case <-got:
		t.Fatal("a second writer of the same row got the lock while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	// Another kind on the same id (a draft and the post it published as)
	// does not wait.
	other, err := s.LockMediaHolder(ctx, store.HolderReelDraft, ref)
	if err != nil {
		t.Fatal(err)
	}
	other()
	unlock()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the second writer never got the lock after the first released it")
	}
	unlock() // idempotent
}

func TestMediaClaimsIT_MediaHeldByEveryKind(t *testing.T) {
	pool := openMediaClaimsDB(t)
	s := store.New(pool)
	ctx := context.Background()
	author := claimsITUser(t, pool)
	cover, source, draftCover := uuid.New(), uuid.New(), uuid.New()

	post := claimsITPost(t, pool, author, &cover)
	h, err := s.MediaHeldBy(ctx, store.HolderPostCover, post)
	if err != nil || !h.Live || len(h.MediaIDs) != 1 || h.MediaIDs[0] != cover {
		t.Fatalf("post: %+v err=%v", h, err)
	}
	// A soft-deleted post is restorable: still live.
	if _, err := pool.Exec(ctx, `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, post); err != nil {
		t.Fatal(err)
	}
	if h, _ := s.MediaHeldBy(ctx, store.HolderPostCover, post); !h.Live {
		t.Fatal("a soft-deleted post must keep its cover claim until the purge")
	}
	if h, err := s.MediaHeldBy(ctx, store.HolderPostCover, uuid.New()); err != nil || h.Live || len(h.MediaIDs) != 0 {
		t.Fatalf("a purged post: %+v err=%v", h, err)
	}

	for status, live := range map[string]bool{"draft": true, "processing": true, "deleted": false, "published": false} {
		d := claimsITDraft(t, pool, author, &source, &draftCover, status)
		h, err := s.MediaHeldBy(ctx, store.HolderReelDraft, d)
		if err != nil || h.Live != live || len(h.MediaIDs) != 2 {
			t.Fatalf("draft %s: %+v err=%v, want live=%v and both ids", status, h, err, live)
		}
	}

	var event uuid.UUID
	if err := pool.QueryRow(ctx, `
		INSERT INTO events (creator_id, title, starts_at, cover_media_id) VALUES ($1, 'e', NOW(), $2) RETURNING id`,
		author, cover).Scan(&event); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM events WHERE id = $1`, event) })
	if h, err := s.MediaHeldBy(ctx, store.HolderEventCover, event); err != nil || !h.Live || h.MediaIDs[0] != cover {
		t.Fatalf("event: %+v err=%v", h, err)
	}
}

func TestMediaClaimsIT_CreateEventKeepsTheCallersID(t *testing.T) {
	pool := openMediaClaimsDB(t)
	s := store.New(pool)
	ctx := context.Background()
	author := claimsITUser(t, pool)
	id := uuid.New()
	e, err := s.CreateEvent(ctx, &store.Event{ID: id, CreatorID: author, Title: "t", StartsAt: time.Now(), Status: "upcoming"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM events WHERE id = $1`, id) })
	if e.ID != id {
		t.Fatalf("event id = %s, want the id the cover claim named (%s)", e.ID, id)
	}
}

func TestMediaClaimsIT_HoldingPageWalksEveryRowThatNamesMedia(t *testing.T) {
	pool := openMediaClaimsDB(t)
	s := store.New(pool)
	ctx := context.Background()
	author := claimsITUser(t, pool)
	cover := uuid.New()
	want := map[uuid.UUID]bool{}
	for i := 0; i < 3; i++ {
		want[claimsITPost(t, pool, author, &cover)] = true
	}
	noCover := claimsITPost(t, pool, author, nil)

	seen := map[uuid.UUID]bool{}
	after := uuid.Nil
	for {
		page, err := s.MediaHoldingPage(ctx, store.HolderPostCover, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range page {
			seen[h.Ref] = true
		}
		if len(page) < 2 {
			break
		}
		after = page[len(page)-1].Ref
	}
	for id := range want {
		if !seen[id] {
			t.Fatalf("post %s with a cover was not paged", id)
		}
	}
	if seen[noCover] {
		t.Fatal("a post with no cover was paged")
	}
}

func TestMediaClaimsIT_ClaimedRefsPageReadsOnlyThisClaimant(t *testing.T) {
	pool := openMediaClaimsDB(t)
	s := store.New(pool)
	ctx := context.Background()
	claimant := "post-service-it-" + uuid.NewString()[:8] // isolated from other suites' rows
	a, b := uuid.New(), uuid.New()
	for _, row := range [][3]string{
		{claimant, "post_cover", a.String()},
		{claimant, "post_cover", a.String()}, // a second asset, same holder
		{claimant, "reel_draft", b.String()},
		{claimant, "post_cover", "not-a-uuid"},
		{"channel-service", "channel_update", uuid.NewString()},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO media_claims (media_id, claimant, claim_kind, claim_ref)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, uuid.New(), row[0], row[1], row[2]); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM media_claims WHERE claimant = $1`, claimant) })

	var got []store.ClaimedRef
	cursor := store.ClaimCursor{}
	for pages := 0; ; pages++ {
		refs, next, more, err := s.ClaimedRefsPage(ctx, claimant, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, refs...)
		if !more {
			break
		}
		if pages > 10 {
			t.Fatal("the pager does not advance")
		}
		cursor = next
	}
	if len(got) != 2 {
		t.Fatalf("refs = %+v, want the two distinct holders (no duplicate, no malformed ref, no other claimant)", got)
	}
	kinds := map[string]uuid.UUID{}
	for _, r := range got {
		kinds[r.Kind] = r.Ref
	}
	if kinds["post_cover"] != a || kinds["reel_draft"] != b {
		t.Fatalf("refs = %+v", got)
	}
}

func claimsITPostDraft(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, payload, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO post_drafts (id, author_id, post_type, payload, status) VALUES ($1, $2, 'post', $3::jsonb, $4)`,
		id, author, payload, status); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM post_drafts WHERE id = $1`, id) })
	return id
}

// A composer draft's cover lives only in its JSONB payload: the holder
// re-read and the pager read it there, parse it in Go (a legacy non-UUID
// value holds nothing and fails nothing), and apply the draft liveness rule.
func TestMediaClaimsIT_PostDraftCoverIsReadFromThePayload(t *testing.T) {
	pool := openMediaClaimsDB(t)
	s := store.New(pool)
	ctx := context.Background()
	author, cover := uuid.New(), uuid.New()
	withCover := `{"text":"t","cover_media_id":"` + cover.String() + `"}`

	for status, live := range map[string]bool{"draft": true, "publishing": true, "blocked": true, "published": false, "deleted": false} {
		d := claimsITPostDraft(t, pool, author, withCover, status)
		h, err := s.MediaHeldBy(ctx, store.HolderPostDraftCover, d)
		if err != nil || h.Live != live || len(h.MediaIDs) != 1 || h.MediaIDs[0] != cover {
			t.Fatalf("draft %s: %+v err=%v, want live=%v holding the cover", status, h, err, live)
		}
	}
	noCover := claimsITPostDraft(t, pool, author, `{"text":"t"}`, "draft")
	if h, err := s.MediaHeldBy(ctx, store.HolderPostDraftCover, noCover); err != nil || !h.Live || len(h.MediaIDs) != 0 {
		t.Fatalf("no cover: %+v err=%v", h, err)
	}
	legacy := claimsITPostDraft(t, pool, author, `{"text":"t","cover_media_id":"not-a-uuid"}`, "draft")
	if h, err := s.MediaHeldBy(ctx, store.HolderPostDraftCover, legacy); err != nil || len(h.MediaIDs) != 0 {
		t.Fatalf("legacy cover: %+v err=%v", h, err)
	}
	if h, err := s.MediaHeldBy(ctx, store.HolderPostDraftCover, uuid.New()); err != nil || h.Live || len(h.MediaIDs) != 0 {
		t.Fatalf("a gone draft: %+v err=%v", h, err)
	}
	walks := claimsITPostDraft(t, pool, author, withCover, "draft")

	seen := map[uuid.UUID]store.MediaHolding{}
	after := uuid.Nil
	for {
		page, err := s.MediaHoldingPage(ctx, store.HolderPostDraftCover, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range page {
			seen[h.Ref] = h
		}
		if len(page) < 2 {
			break
		}
		after = page[len(page)-1].Ref
	}
	if h, ok := seen[walks]; !ok || !h.Live || len(h.MediaIDs) != 1 || h.MediaIDs[0] != cover {
		t.Fatalf("a draft with a cover was not paged: %+v ok=%v", h, ok)
	}
	if _, ok := seen[noCover]; ok {
		t.Fatal("a draft with no cover was paged")
	}
	if h, ok := seen[legacy]; !ok || len(h.MediaIDs) != 0 {
		t.Fatalf("a legacy cover must be paged holding nothing (its claims go): %+v ok=%v", h, ok)
	}
}

// Series and playlist covers (founder, 31 Oct 2026): create keeps the id the
// cover claim named; the holder re-read and the pager read the series'
// cover_media_id and the media id inside the playlist's cover_url serve
// path; a gone row is not live and holds nothing.
func TestMediaClaimsIT_SeriesAndPlaylistCovers(t *testing.T) {
	pool := openMediaClaimsDB(t)
	s := store.New(pool)
	ctx := context.Background()
	creator := claimsITUser(t, pool)
	cover := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status) VALUES ($1, $2, 'image', 'ready')`,
		cover, creator); err != nil {
		t.Fatal(err)
	}
	seriesID, playlistID, emptyID := uuid.New(), uuid.New(), uuid.New()
	vs := &store.VideoSeries{ID: seriesID, CreatorID: creator, Title: "s", CoverMediaID: &cover, IsPublic: true}
	if err := s.CreateVideoSeries(ctx, vs); err != nil {
		t.Fatal(err)
	}
	path := "/v1/media/" + cover.String() + "/serve"
	pl := &store.Playlist{ID: playlistID, CreatorID: creator, Title: "p", CoverURL: &path, Visibility: "public"}
	if err := s.CreatePlaylist(ctx, pl); err != nil {
		t.Fatal(err)
	}
	empty := ""
	if err := s.CreatePlaylist(ctx, &store.Playlist{ID: emptyID, CreatorID: creator, Title: "e", CoverURL: &empty, Visibility: "public"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM playlists WHERE id = ANY($1)`, []uuid.UUID{playlistID, emptyID})
		_, _ = pool.Exec(bg, `DELETE FROM video_series WHERE id = $1`, seriesID)
		_, _ = pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, cover)
	})
	if vs.ID != seriesID || pl.ID != playlistID {
		t.Fatalf("create ids = %s / %s, want the ids the claims named", vs.ID, pl.ID)
	}
	for kind, ref := range map[string]uuid.UUID{store.HolderVideoSeriesCover: seriesID, store.HolderPlaylistCover: playlistID} {
		h, err := s.MediaHeldBy(ctx, kind, ref)
		if err != nil || !h.Live || len(h.MediaIDs) != 1 || h.MediaIDs[0] != cover {
			t.Fatalf("%s: %+v err=%v, want live holding the cover", kind, h, err)
		}
		if h, err := s.MediaHeldBy(ctx, kind, uuid.New()); err != nil || h.Live || len(h.MediaIDs) != 0 {
			t.Fatalf("%s gone row: %+v err=%v", kind, h, err)
		}
		seen := map[uuid.UUID]store.MediaHolding{}
		after := uuid.Nil
		for {
			page, err := s.MediaHoldingPage(ctx, kind, after, 50)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range page {
				seen[row.Ref] = row
			}
			if len(page) < 50 {
				break
			}
			after = page[len(page)-1].Ref
		}
		if row, ok := seen[ref]; !ok || !row.Live || len(row.MediaIDs) != 1 || row.MediaIDs[0] != cover {
			t.Fatalf("%s: the row with a cover was not paged: %+v ok=%v", kind, row, ok)
		}
		if _, ok := seen[emptyID]; ok {
			t.Fatalf("%s: a playlist with no cover was paged", kind)
		}
	}
	if h, err := s.MediaHeldBy(ctx, store.HolderPlaylistCover, emptyID); err != nil || !h.Live || len(h.MediaIDs) != 0 {
		t.Fatalf("an empty playlist cover: %+v err=%v", h, err)
	}
}
