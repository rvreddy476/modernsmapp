//go:build integration

package service

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// GET /v1/reels/liked against a real Postgres: migration 063 lets 'like'
// in, the like index writes, the SQL short-form / live filter, the read
// gate on top, and keyset paging.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run LikedReelsAgainstPostgres -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).
func TestLikedReelsAgainstPostgres(t *testing.T) {
	pool := openHubMediaDB(t)
	ctx := context.Background()
	store := postgres.New(pool)
	svc := New(store, nil, nil) // no graph-service URL: the account gate is open, per-post visibility still applies

	viewer, author := uuid.New(), uuid.New()
	type fixture struct {
		name, contentType, visibility string
		authorID                      uuid.UUID
		deleted, scheduled            bool
		reviewStatus                  string
	}
	fixtures := []fixture{
		{name: "flick", contentType: "flick", visibility: "public", authorID: author},
		{name: "reel", contentType: "reel", visibility: "public", authorID: author},
		{name: "unlisted", contentType: "flick", visibility: "unlisted", authorID: author},
		{name: "longVideo", contentType: "video", visibility: "public", authorID: author},
		{name: "textPost", contentType: "post", visibility: "public", authorID: author},
		{name: "deleted", contentType: "flick", visibility: "public", authorID: author, deleted: true},
		{name: "scheduled", contentType: "flick", visibility: "public", authorID: author, scheduled: true},
		{name: "rejected", contentType: "flick", visibility: "public", authorID: author, reviewStatus: "rejected"},
		{name: "private", contentType: "flick", visibility: "private", authorID: author},
		{name: "closeFriends", contentType: "flick", visibility: "close_friends", authorID: author},
		{name: "staged", contentType: "flick", visibility: "staged", authorID: author},
		{name: "ownPrivate", contentType: "flick", visibility: "private", authorID: viewer},
	}
	ids := map[string]uuid.UUID{}
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range ids {
			_, _ = pool.Exec(bg, `DELETE FROM reactions WHERE target_type = 'post' AND target_id = $1`, id)
			_, _ = pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, id)
		}
	})
	base := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	for i, f := range fixtures {
		id := uuid.New()
		ids[f.name] = id
		review := f.reviewStatus
		if review == "" {
			review = "approved"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, review_status, created_at, updated_at)
			VALUES ($1, $2, 'liked fixture', $3, $4, $5, NOW(), NOW())`, id, f.authorID, f.visibility, f.contentType, review); err != nil {
			t.Fatalf("insert %s: %v", f.name, err)
		}
		if f.deleted {
			if _, err := pool.Exec(ctx, `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
		if f.scheduled {
			if _, err := pool.Exec(ctx, `UPDATE posts SET publish_at = NOW() + interval '1 day' WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
		}
		// Every like path's index write; "like" itself fails without 063.
		if err := svc.indexReaction(ctx, id, viewer, "like", true); err != nil {
			t.Fatalf("index like on %s: %v", f.name, err)
		}
		// Pin the liked-at times: fixtures[0] oldest, the last one newest.
		if _, err := pool.Exec(ctx, `UPDATE reactions SET created_at = $3 WHERE target_type = 'post' AND target_id = $1 AND user_id = $2`,
			id, viewer, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	list := func(limit int, cursor string) ([]string, string) {
		t.Helper()
		got, next, err := svc.ListLikedReels(ctx, viewer, limit, cursor)
		if err != nil {
			t.Fatalf("ListLikedReels(%d, %q): %v", limit, cursor, err)
		}
		return got, next
	}
	names := func(ns ...string) []string {
		out := make([]string, len(ns))
		for i, n := range ns {
			out[i] = ids[n].String()
		}
		return out
	}

	// Newest like first; only short-form, live, approved posts the viewer
	// may open: the long video, text post, deleted, scheduled, rejected,
	// someone else's private, close-friends and staged posts are gone; the viewer's own
	// private reel and an unlisted one stay.
	want := names("ownPrivate", "unlisted", "reel", "flick")
	got, next := list(50, "")
	if !reflect.DeepEqual(got, want) || next != "" {
		t.Fatalf("liked = %v next %q, want %v and no cursor", got, next, want)
	}

	// Keyset paging, two at a time.
	p1, next := list(2, "")
	if !reflect.DeepEqual(p1, want[:2]) || next == "" {
		t.Fatalf("page 1 = %v next %q, want %v and a cursor", p1, next, want[:2])
	}
	p2, next := list(2, next)
	if !reflect.DeepEqual(p2, want[2:]) {
		t.Fatalf("page 2 = %v, want %v", p2, want[2:])
	}
	if next != "" {
		p3, n3 := list(2, next)
		if len(p3) != 0 || n3 != "" {
			t.Fatalf("page 3 = %v next %q, want empty and the end", p3, n3)
		}
	}

	// A repeat like keeps its place; an unlike removes it.
	if err := svc.indexReaction(ctx, ids["flick"], viewer, "love", true); err != nil {
		t.Fatal(err)
	}
	if got, _ := list(50, ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("after a repeat like = %v, want %v", got, want)
	}
	if err := svc.indexReaction(ctx, ids["reel"], viewer, "", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := list(50, ""); !reflect.DeepEqual(got, names("ownPrivate", "unlisted", "flick")) {
		t.Fatalf("after unlike = %v", got)
	}

	// The multi-reaction toggle writes the same table, and "like" now
	// passes the check: liking the reel through it brings it back, newest.
	toggle, err := store.ToggleReaction(ctx, "post", ids["reel"], viewer, "like", nil)
	if err != nil {
		t.Fatalf("ToggleReaction like (the type check must accept it): %v", err)
	}
	if !toggle.IsSet {
		t.Fatalf("ToggleReaction = %+v, want set", toggle)
	}
	if got, _ := list(1, ""); !reflect.DeepEqual(got, names("reel")) {
		t.Fatalf("after the toggle = %v, want the reel first", got)
	}

	// Someone else's list is theirs.
	if got, _, err := svc.ListLikedReels(ctx, author, 50, ""); err != nil || len(got) != 0 {
		t.Fatalf("author's liked list = %v (%v), want empty", got, err)
	}
}
