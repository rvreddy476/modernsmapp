//go:build integration

package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/group-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// viewerFlagsFixture is one group, two members, and four published posts whose
// created_at values are spread far enough apart that paging is deterministic.
// posts[0] is the newest, so a limit=2 page 1 holds posts[0..1] and page 2
// holds posts[2..3].
type viewerFlagsFixture struct {
	store   *Store
	groupID uuid.UUID
	viewerA uuid.UUID
	viewerB uuid.UUID
	posts   []uuid.UUID
}

// newViewerFlagsFixture connects, refuses any database whose name does not end
// in _test, patches the schema forward, and seeds an isolated group.
func newViewerFlagsFixture(t *testing.T) (*viewerFlagsFixture, func()) {
	t.Helper()
	dsn := os.Getenv("GROUP_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("GROUP_POSTGRES_DSN is required")
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
	// Bring an older scratch database forward for the columns this fixture
	// touches; every statement is a no-op on an up-to-date schema.
	if _, err := pool.Exec(ctx, `
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS privacy_level TEXT NOT NULL DEFAULT 'public';
		ALTER TABLE groups ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
		ALTER TABLE group_posts ADD COLUMN IF NOT EXISTS status VARCHAR(20) DEFAULT 'published';
	`); err != nil {
		pool.Close()
		t.Fatal(err)
	}

	f := &viewerFlagsFixture{
		store:   New(pool),
		groupID: uuid.New(),
		viewerA: uuid.New(),
		viewerB: uuid.New(),
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO groups (id, name, description, creator_id, visibility) VALUES ($1, $2, '', $3, 'public')`,
		f.groupID, "viewer-flags-"+f.groupID.String()[:8], f.viewerA); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	for _, m := range []uuid.UUID{f.viewerA, f.viewerB} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO group_members (group_id, user_id, role) VALUES ($1, $2, 'member')`,
			f.groupID, m); err != nil {
			pool.Close()
			t.Fatal(err)
		}
	}
	base := time.Now().Add(-24 * time.Hour)
	for i := 0; i < 4; i++ {
		id := uuid.New()
		// i == 0 is the newest post.
		createdAt := base.Add(time.Duration(-i) * time.Hour)
		if _, err := pool.Exec(ctx,
			`INSERT INTO group_posts (id, group_id, author_id, content_type, body, status, created_at, updated_at)
			 VALUES ($1, $2, $3, 'text', $4, 'published', $5, $5)`,
			id, f.groupID, f.viewerA.String(), "post body", createdAt); err != nil {
			pool.Close()
			t.Fatal(err)
		}
		f.posts = append(f.posts, id)
	}

	cleanup := func() {
		ctx := context.Background()
		for _, p := range f.posts {
			pool.Exec(ctx, `DELETE FROM group_post_sparks WHERE post_id = $1`, p)
			pool.Exec(ctx, `DELETE FROM group_post_echoes WHERE post_id = $1`, p)
			pool.Exec(ctx, `DELETE FROM group_post_stashes WHERE post_id = $1`, p)
		}
		pool.Exec(ctx, `DELETE FROM group_posts WHERE group_id = $1`, f.groupID)
		pool.Exec(ctx, `DELETE FROM group_members WHERE group_id = $1`, f.groupID)
		pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, f.groupID)
		pool.Close()
	}
	return f, cleanup
}

// engage records one kind of reaction by one viewer on one post.
func (f *viewerFlagsFixture) engage(t *testing.T, kind string, postID uuid.UUID, viewer uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var err error
	switch kind {
	case "spark":
		err = f.store.SparkGroupPost(ctx, postID, viewer.String(), false)
	case "echo":
		err = f.store.EchoGroupPost(ctx, postID, viewer.String(), "share")
	case "stash":
		err = f.store.StashGroupPost(ctx, postID, viewer.String())
	default:
		t.Fatalf("unknown engagement kind %q", kind)
	}
	if err != nil {
		t.Fatalf("%s on %s: %v", kind, postID, err)
	}
}

// flagOf reads the viewer_* flag matching kind.
func flagOf(kind string, p GroupPostV2) bool {
	switch kind {
	case "spark":
		return p.ViewerSparked
	case "echo":
		return p.ViewerEchoed
	case "stash":
		return p.ViewerStashed
	}
	return false
}

func findPost(posts []GroupPostV2, id uuid.UUID) *GroupPostV2 {
	for i := range posts {
		if posts[i].ID == id {
			return &posts[i]
		}
	}
	return nil
}

var engagementKinds = []string{"spark", "echo", "stash"}

// ── ListMyGroupsFeed ─────────────────────────────────────────

func TestListMyGroupsFeedViewerFlagsAreViewerScoped(t *testing.T) {
	for _, kind := range engagementKinds {
		t.Run(kind, func(t *testing.T) {
			f, cleanup := newViewerFlagsFixture(t)
			defer cleanup()
			ctx := context.Background()

			engaged, untouched := f.posts[0], f.posts[1]
			f.engage(t, kind, engaged, f.viewerA)

			mine, err := f.store.ListMyGroupsFeed(ctx, f.viewerA, 50, 0)
			if err != nil {
				t.Fatal(err)
			}
			got := findPost(mine, engaged)
			if got == nil {
				t.Fatalf("engaged post missing from own feed")
			}
			if !flagOf(kind, *got) {
				t.Fatalf("viewer who %sed the post got viewer_%sed=false", kind, kind)
			}
			if other := findPost(mine, untouched); other == nil || flagOf(kind, *other) {
				t.Fatalf("untouched post reported viewer_%sed=true", kind)
			}

			theirs, err := f.store.ListMyGroupsFeed(ctx, f.viewerB, 50, 0)
			if err != nil {
				t.Fatal(err)
			}
			theirCopy := findPost(theirs, engaged)
			if theirCopy == nil {
				t.Fatalf("engaged post missing from the other member's feed")
			}
			if flagOf(kind, *theirCopy) {
				t.Fatalf("a different viewer got viewer_%sed=true for someone else's %s", kind, kind)
			}
		})
	}
}

func TestListMyGroupsFeedViewerFlagsOnSecondPage(t *testing.T) {
	for _, kind := range engagementKinds {
		t.Run(kind, func(t *testing.T) {
			f, cleanup := newViewerFlagsFixture(t)
			defer cleanup()
			ctx := context.Background()

			// posts[2] falls on page 2 at limit=2.
			engaged := f.posts[2]
			f.engage(t, kind, engaged, f.viewerA)

			page1, err := f.store.ListMyGroupsFeed(ctx, f.viewerA, 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			if findPost(page1, engaged) != nil {
				t.Fatalf("fixture drifted: engaged post appeared on page 1")
			}
			for _, p := range page1 {
				if flagOf(kind, p) {
					t.Fatalf("page 1 post %s reported viewer_%sed=true", p.ID, kind)
				}
			}

			page2, err := f.store.ListMyGroupsFeed(ctx, f.viewerA, 2, 2)
			if err != nil {
				t.Fatal(err)
			}
			got := findPost(page2, engaged)
			if got == nil {
				t.Fatalf("engaged post missing from page 2 (got %d posts)", len(page2))
			}
			if !flagOf(kind, *got) {
				t.Fatalf("page 2 lost viewer_%sed: got false", kind)
			}

			theirPage2, err := f.store.ListMyGroupsFeed(ctx, f.viewerB, 2, 2)
			if err != nil {
				t.Fatal(err)
			}
			if theirCopy := findPost(theirPage2, engaged); theirCopy == nil || flagOf(kind, *theirCopy) {
				t.Fatalf("a different viewer got viewer_%sed=true on page 2", kind)
			}
		})
	}
}

func TestListMyGroupsFeedAnonymousViewerGetsNoFlagsAndNoError(t *testing.T) {
	f, cleanup := newViewerFlagsFixture(t)
	defer cleanup()
	ctx := context.Background()

	for _, kind := range engagementKinds {
		f.engage(t, kind, f.posts[0], f.viewerA)
	}

	// uuid.Nil is the "no signed-in viewer" case: no membership, so no rows,
	// and certainly no error.
	posts, err := f.store.ListMyGroupsFeed(ctx, uuid.Nil, 50, 0)
	if err != nil {
		t.Fatalf("anonymous viewer errored: %v", err)
	}
	for _, p := range posts {
		if p.GroupID == f.groupID {
			t.Fatalf("anonymous viewer saw post %s from a group they do not belong to", p.ID)
		}
		if p.ViewerSparked || p.ViewerEchoed || p.ViewerStashed {
			t.Fatalf("anonymous viewer got a true viewer_* flag on post %s", p.ID)
		}
	}

	// A signed-in non-member is the same story: the feed never shows them the
	// group's posts, so they can never inherit its flags.
	stranger, err := f.store.ListMyGroupsFeed(ctx, uuid.New(), 50, 0)
	if err != nil {
		t.Fatalf("non-member viewer errored: %v", err)
	}
	if got := findPost(stranger, f.posts[0]); got != nil {
		t.Fatalf("non-member saw the group's post in their feed")
	}
}

// ── ListGroupPostsV2 ─────────────────────────────────────────

func TestListGroupPostsV2ViewerFlagsAreViewerScoped(t *testing.T) {
	for _, kind := range engagementKinds {
		t.Run(kind, func(t *testing.T) {
			f, cleanup := newViewerFlagsFixture(t)
			defer cleanup()
			ctx := context.Background()

			engaged, untouched := f.posts[0], f.posts[1]
			f.engage(t, kind, engaged, f.viewerA)

			mine, err := f.store.ListGroupPostsV2(ctx, f.groupID, nil, f.viewerA.String(), 50, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(mine) != 4 {
				t.Fatalf("expected 4 posts, got %d — the viewer joins must not duplicate or drop rows", len(mine))
			}
			got := findPost(mine, engaged)
			if got == nil || !flagOf(kind, *got) {
				t.Fatalf("viewer who %sed the post got viewer_%sed=false", kind, kind)
			}
			if other := findPost(mine, untouched); other == nil || flagOf(kind, *other) {
				t.Fatalf("untouched post reported viewer_%sed=true", kind)
			}

			theirs, err := f.store.ListGroupPostsV2(ctx, f.groupID, nil, f.viewerB.String(), 50, 0)
			if err != nil {
				t.Fatal(err)
			}
			if theirCopy := findPost(theirs, engaged); theirCopy == nil || flagOf(kind, *theirCopy) {
				t.Fatalf("a different viewer got viewer_%sed=true for someone else's %s", kind, kind)
			}
		})
	}
}

func TestListGroupPostsV2ViewerFlagsOnSecondPage(t *testing.T) {
	for _, kind := range engagementKinds {
		t.Run(kind, func(t *testing.T) {
			f, cleanup := newViewerFlagsFixture(t)
			defer cleanup()
			ctx := context.Background()

			engaged := f.posts[2]
			f.engage(t, kind, engaged, f.viewerA)

			page1, err := f.store.ListGroupPostsV2(ctx, f.groupID, nil, f.viewerA.String(), 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(page1) != 2 || findPost(page1, engaged) != nil {
				t.Fatalf("fixture drifted: page 1 = %d posts, engaged post present = %v", len(page1), findPost(page1, engaged) != nil)
			}
			for _, p := range page1 {
				if flagOf(kind, p) {
					t.Fatalf("page 1 post %s reported viewer_%sed=true", p.ID, kind)
				}
			}

			page2, err := f.store.ListGroupPostsV2(ctx, f.groupID, nil, f.viewerA.String(), 2, 2)
			if err != nil {
				t.Fatal(err)
			}
			got := findPost(page2, engaged)
			if got == nil {
				t.Fatalf("engaged post missing from page 2 (got %d posts)", len(page2))
			}
			if !flagOf(kind, *got) {
				t.Fatalf("page 2 lost viewer_%sed: got false", kind)
			}

			theirPage2, err := f.store.ListGroupPostsV2(ctx, f.groupID, nil, f.viewerB.String(), 2, 2)
			if err != nil {
				t.Fatal(err)
			}
			if theirCopy := findPost(theirPage2, engaged); theirCopy == nil || flagOf(kind, *theirCopy) {
				t.Fatalf("a different viewer got viewer_%sed=true on page 2", kind)
			}
		})
	}
}

func TestListGroupPostsV2AnonymousViewerGetsFalseFlags(t *testing.T) {
	f, cleanup := newViewerFlagsFixture(t)
	defer cleanup()
	ctx := context.Background()

	for _, kind := range engagementKinds {
		f.engage(t, kind, f.posts[0], f.viewerA)
	}

	// An empty viewer id is the anonymous / no-viewer-in-scope case: the list
	// still comes back in full, with every flag false and no error.
	posts, err := f.store.ListGroupPostsV2(ctx, f.groupID, nil, "", 50, 0)
	if err != nil {
		t.Fatalf("anonymous viewer errored: %v", err)
	}
	if len(posts) != 4 {
		t.Fatalf("anonymous viewer got %d posts, want 4", len(posts))
	}
	for _, p := range posts {
		if p.ViewerSparked || p.ViewerEchoed || p.ViewerStashed {
			t.Fatalf("anonymous viewer got a true viewer_* flag on post %s", p.ID)
		}
	}

	// A signed-in non-member reading a public group is the same: all false.
	nonMember, err := f.store.ListGroupPostsV2(ctx, f.groupID, nil, uuid.New().String(), 50, 0)
	if err != nil {
		t.Fatalf("non-member viewer errored: %v", err)
	}
	for _, p := range nonMember {
		if p.ViewerSparked || p.ViewerEchoed || p.ViewerStashed {
			t.Fatalf("non-member got a true viewer_* flag on post %s", p.ID)
		}
	}
}

// ── GetGroupPostV2ForViewer ──────────────────────────────────

func TestGetGroupPostV2ForViewerFlagsAreViewerScoped(t *testing.T) {
	for _, kind := range engagementKinds {
		t.Run(kind, func(t *testing.T) {
			f, cleanup := newViewerFlagsFixture(t)
			defer cleanup()
			ctx := context.Background()

			engaged := f.posts[0]
			f.engage(t, kind, engaged, f.viewerA)

			mine, err := f.store.GetGroupPostV2ForViewer(ctx, engaged, f.viewerA.String())
			if err != nil {
				t.Fatal(err)
			}
			if !flagOf(kind, *mine) {
				t.Fatalf("viewer who %sed the post got viewer_%sed=false", kind, kind)
			}

			theirs, err := f.store.GetGroupPostV2ForViewer(ctx, engaged, f.viewerB.String())
			if err != nil {
				t.Fatal(err)
			}
			if flagOf(kind, *theirs) {
				t.Fatalf("a different viewer got viewer_%sed=true", kind)
			}

			anon, err := f.store.GetGroupPostV2ForViewer(ctx, engaged, "")
			if err != nil {
				t.Fatalf("anonymous viewer errored: %v", err)
			}
			if anon.ViewerSparked || anon.ViewerEchoed || anon.ViewerStashed {
				t.Fatalf("anonymous viewer got a true viewer_* flag")
			}
			if anon.ID != engaged {
				t.Fatalf("anonymous read returned the wrong post")
			}
		})
	}
}
