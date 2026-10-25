//go:build integration

// Saved collections and followed hashtags on the real schema (migration
// 074): the per-user limit, case-insensitive unique names, user scoping on
// every statement, move, rename keeping collection_name in step, delete
// falling back to the default, the bookmark scope; follow idempotency and
// limit, and the followed-posts eligibility.
//
//	SOCIAL_GAPS_POSTGRES_DSN=postgres://…/social_gaps_it_test go test -tags integration ./internal/store/postgres/ -run SocialGapsIT -v
//
// The database name must end in _test (openSocialGapsDB refuses anything
// else).

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	store "github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openSocialGapsDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("SOCIAL_GAPS_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SOCIAL_GAPS_POSTGRES_DSN is required")
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
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS media_assets (
			id UUID PRIMARY KEY, uploader_id UUID NOT NULL, file_type TEXT NOT NULL, processing_status TEXT NOT NULL,
			moderation_status TEXT NOT NULL DEFAULT 'pending', duration_seconds INTEGER, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
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
	return pool
}

func socialPost(t *testing.T, pool *pgxpool.Pool, author uuid.UUID, visibility string, tags []string, created time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	// posts.hashtags is NOT NULL DEFAULT '{}' (setup.sql): a nil slice is
	// sent as NULL, so "no tags" is the empty array.
	if tags == nil {
		tags = []string{}
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, hashtags, created_at, updated_at)
		VALUES ($1, $2, 'post', $3, 'post', 'approved', $4, $5, $5)`, id, author, visibility, tags, created); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, id) })
	return id
}

func cleanupSaved(t *testing.T, pool *pgxpool.Pool, users ...uuid.UUID) {
	t.Cleanup(func() {
		for _, u := range users {
			_, _ = pool.Exec(context.Background(), `DELETE FROM saved_items WHERE user_id = $1`, u)
			_, _ = pool.Exec(context.Background(), `DELETE FROM saved_collections WHERE user_id = $1`, u)
			_, _ = pool.Exec(context.Background(), `DELETE FROM hashtag_follows WHERE user_id = $1`, u)
		}
	})
}

func TestSocialGapsITSavedCollections(t *testing.T) {
	pool := openSocialGapsDB(t)
	st := store.New(pool)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	cleanupSaved(t, pool, alice, bob)

	// Limit: 50 fit, the 51st is refused; bob is unaffected.
	var first *store.SavedCollectionRow
	for i := 0; i < 50; i++ {
		c, err := st.CreateSavedCollection(ctx, alice, fmt.Sprintf("c%02d", i), 50)
		if err != nil {
			t.Fatalf("collection %d: %v", i+1, err)
		}
		if i == 0 {
			first = c
		}
	}
	if _, err := st.CreateSavedCollection(ctx, alice, "one more", 50); !errors.Is(err, store.ErrCollectionLimit) {
		t.Fatalf("51st: %v", err)
	}
	trips, err := st.CreateSavedCollection(ctx, bob, "Trips", 50)
	if err != nil {
		t.Fatal(err)
	}
	// Unique per user, case-insensitively; another user may reuse the name.
	if _, err := st.CreateSavedCollection(ctx, bob, "TRIPS", 50); !errors.Is(err, store.ErrCollectionNameTaken) {
		t.Fatalf("case-insensitive duplicate: %v", err)
	}
	food, _ := st.CreateSavedCollection(ctx, bob, "Food", 50)
	if _, err := st.RenameSavedCollection(ctx, bob, food.ID, "trips"); !errors.Is(err, store.ErrCollectionNameTaken) {
		t.Fatalf("rename onto a taken name: %v", err)
	}

	// Scoping: alice's id is bob's "not found" on every statement.
	if _, err := st.GetSavedCollection(ctx, bob, first.ID); !errors.Is(err, store.ErrCollectionNotFound) {
		t.Fatalf("get: %v", err)
	}
	if _, err := st.RenameSavedCollection(ctx, bob, first.ID, "x"); !errors.Is(err, store.ErrCollectionNotFound) {
		t.Fatalf("rename: %v", err)
	}
	if _, err := st.DeleteSavedCollection(ctx, bob, first.ID); !errors.Is(err, store.ErrCollectionNotFound) {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.SaveItem(ctx, bob, "post", uuid.New(), &first.ID); !errors.Is(err, store.ErrCollectionNotFound) {
		t.Fatalf("save into another's: %v", err)
	}

	// Save, move, rename, re-save, delete.
	author := uuid.New()
	p1 := socialPost(t, pool, author, "public", nil, time.Now())
	p2 := socialPost(t, pool, author, "public", nil, time.Now())
	it1, err := st.SaveItem(ctx, bob, "post", p1, nil)
	if err != nil || it1.CollectionID != nil || it1.CollectionName != store.DefaultCollectionName {
		t.Fatalf("plain save: %+v %v", it1, err)
	}
	if _, err := st.MoveSavedItem(ctx, alice, it1.ID, nil); !errors.Is(err, store.ErrSavedItemNotFound) {
		t.Fatalf("alice moving bob's item: %v", err)
	}
	if _, err := st.MoveSavedItem(ctx, bob, it1.ID, &first.ID); !errors.Is(err, store.ErrCollectionNotFound) {
		t.Fatalf("bob moving into alice's collection: %v", err)
	}
	moved, err := st.MoveSavedItem(ctx, bob, it1.ID, &trips.ID)
	if err != nil || moved.CollectionID == nil || *moved.CollectionID != trips.ID || moved.CollectionName != "Trips" {
		t.Fatalf("move: %+v %v", moved, err)
	}
	if _, err := st.SaveItem(ctx, bob, "post", p2, &trips.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.SaveItem(ctx, bob, "post", p1, nil); again.CollectionID == nil || *again.CollectionID != trips.ID {
		t.Fatalf("a plain re-save pulled the item out: %+v", again)
	}
	if _, err := st.RenameSavedCollection(ctx, bob, trips.ID, "Holidays"); err != nil {
		t.Fatal(err)
	}
	items, _, _ := st.ListSavedItems(ctx, bob, store.SavedScope{CollectionID: &trips.ID}, 20, "")
	if len(items) != 2 || items[0].CollectionName != "Holidays" {
		t.Fatalf("after rename: %+v", items)
	}
	// The legacy name filter still finds them.
	if legacy, _, _ := st.ListSavedItems(ctx, bob, store.SavedScope{CollectionName: "Holidays"}, 20, ""); len(legacy) != 2 {
		t.Fatalf("legacy name filter: %d", len(legacy))
	}
	posts, _, err := st.GetBookmarks(ctx, bob, nil, 20, "", store.SavedScope{CollectionID: &trips.ID})
	if err != nil || len(posts) != 2 {
		t.Fatalf("bookmarks in collection: %d %v", len(posts), err)
	}
	if def, _, _ := st.GetBookmarks(ctx, bob, nil, 20, "", store.SavedScope{DefaultOnly: true}); len(def) != 0 {
		t.Fatalf("default holds %d before the delete", len(def))
	}
	n, err := st.DeleteSavedCollection(ctx, bob, trips.ID)
	if err != nil || n != 2 {
		t.Fatalf("delete moved %d %v", n, err)
	}
	def, _, _ := st.GetBookmarks(ctx, bob, nil, 20, "", store.SavedScope{DefaultOnly: true})
	if len(def) != 2 {
		t.Fatalf("default holds %d after the delete, want 2", len(def))
	}
	var named int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM saved_items WHERE user_id = $1 AND (collection_id IS NOT NULL OR collection_name <> 'All Saved')`, bob).Scan(&named)
	if named != 0 {
		t.Fatalf("%d items kept a deleted collection", named)
	}
	defCount, cols, err := st.ListSavedCollections(ctx, bob)
	if err != nil || defCount != 2 || len(cols) != 1 || cols[0].Name != "Food" {
		t.Fatalf("list: %d %+v %v", defCount, cols, err)
	}
	// MoveSavedTarget addresses by post.
	if _, err := st.MoveSavedTarget(ctx, bob, "post", p1, &food.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MoveSavedTarget(ctx, bob, "post", uuid.New(), &food.ID); !errors.Is(err, store.ErrSavedItemNotFound) {
		t.Fatalf("move unsaved: %v", err)
	}
}

func TestSocialGapsITHashtagFollows(t *testing.T) {
	pool := openSocialGapsDB(t)
	st := store.New(pool)
	ctx := context.Background()
	viewer, author := uuid.New(), uuid.New()
	cleanupSaved(t, pool, viewer)

	created, err := st.FollowHashtag(ctx, viewer, "cricket", 2)
	if err != nil || !created {
		t.Fatalf("first follow: %v %v", created, err)
	}
	if created, err := st.FollowHashtag(ctx, viewer, "cricket", 2); err != nil || created {
		t.Fatalf("second follow must be a no-op: %v %v", created, err)
	}
	if _, err := st.FollowHashtag(ctx, viewer, "ipl", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FollowHashtag(ctx, viewer, "third", 2); !errors.Is(err, store.ErrHashtagFollowLimit) {
		t.Fatalf("past the limit: %v", err)
	}
	if _, err := st.FollowHashtag(ctx, viewer, "ipl", 2); err != nil {
		t.Fatalf("re-follow at the limit must succeed: %v", err)
	}
	page, next, err := st.ListFollowedHashtags(ctx, viewer, 1, "")
	if err != nil || len(page) != 1 || next == "" {
		t.Fatalf("page 1: %v %q %v", page, next, err)
	}
	page2, next2, _ := st.ListFollowedHashtags(ctx, viewer, 1, next)
	if len(page2) != 1 || page2[0].Tag == page[0].Tag || next2 != "" {
		t.Fatalf("page 2: %v %q", page2, next2)
	}

	now := time.Now()
	visible := socialPost(t, pool, author, "public", []string{"cricket"}, now.Add(-time.Hour))
	socialPost(t, pool, author, "followers", []string{"cricket"}, now.Add(-time.Hour))    // not public
	socialPost(t, pool, viewer, "public", []string{"cricket"}, now.Add(-time.Hour))       // the viewer's own
	socialPost(t, pool, author, "public", []string{"cricket"}, now.Add(-40*24*time.Hour)) // too old
	socialPost(t, pool, author, "public", []string{"football"}, now.Add(-time.Hour))      // not followed
	gone := socialPost(t, pool, author, "public", []string{"ipl"}, now.Add(-time.Hour))
	_, _ = pool.Exec(ctx, `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, gone) // deleted
	ipl := socialPost(t, pool, author, "public", []string{"ipl", "cricket"}, now.Add(-2*time.Hour))

	tags, _ := st.FollowedHashtagTags(ctx, viewer)
	posts, _, err := st.GetPostsByHashtags(ctx, tags, viewer, 20, "", now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 || posts[0].ID != visible || posts[1].ID != ipl {
		ids := []uuid.UUID{}
		for _, p := range posts {
			ids = append(ids, p.ID)
		}
		t.Fatalf("followed posts = %v, want [%v %v]", ids, visible, ipl)
	}
	if removed, _ := st.UnfollowHashtag(ctx, viewer, "cricket"); !removed {
		t.Fatal("unfollow")
	}
	if removed, _ := st.UnfollowHashtag(ctx, viewer, "cricket"); removed {
		t.Fatal("second unfollow must be a no-op")
	}
}

// The owner edit writes the filtered mention / tag lists and keeps
// post_mentions in step with mention_usernames.
func TestSocialGapsITEditMentionsAndTags(t *testing.T) {
	pool := openSocialGapsDB(t)
	st := store.New(pool)
	ctx := context.Background()
	author := uuid.New()
	post := socialPost(t, pool, author, "public", nil, time.Now())
	if _, err := pool.Exec(ctx, `UPDATE posts SET mention_usernames = ARRAY['old','kept'] WHERE id = $1`, post); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"old", "kept"} {
		if err := st.InsertMention(ctx, post, "post", u); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM post_mentions WHERE post_id = $1`, post) })

	mentions := []string{"kept", "new"}
	tagged := []uuid.UUID{uuid.New()}
	after, err := st.UpdatePostFields(ctx, post, author, store.PostEditPatch{MentionUsernames: &mentions, TaggedUserIDs: &tagged})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(after.MentionUsernames, ",") != "kept,new" || len(after.TaggedUserIDs) != 1 || after.TaggedUserIDs[0] != tagged[0] {
		t.Fatalf("row: mentions=%v tagged=%v", after.MentionUsernames, after.TaggedUserIDs)
	}
	rows, err := st.GetMentionsByPost(ctx, post)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.MentionedUserID] = true
	}
	if len(got) != 2 || !got["kept"] || !got["new"] || got["old"] {
		t.Fatalf("post_mentions = %v, want kept,new", got)
	}
	// Clearing both.
	none, noTags := []string{}, []uuid.UUID{}
	after, err = st.UpdatePostFields(ctx, post, author, store.PostEditPatch{MentionUsernames: &none, TaggedUserIDs: &noTags})
	if err != nil || len(after.MentionUsernames) != 0 || len(after.TaggedUserIDs) != 0 {
		t.Fatalf("clear: %v %v %v", after.MentionUsernames, after.TaggedUserIDs, err)
	}
	if rows, _ := st.GetMentionsByPost(ctx, post); len(rows) != 0 {
		t.Fatalf("post_mentions after clear: %d", len(rows))
	}
}
