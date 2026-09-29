//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A post's COVER at the media byte gate, against a real Postgres
// (2026-09-29): the cover is not in post_media, so until the by-media
// lookups matched posts.cover_media_id nothing carried it and every viewer
// but its uploader was refused, signed out included (the channel feed's
// artwork, the signed-out watch page's poster). Now a cover inherits the
// audience of the post it covers, and only that.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run CoverMedia -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).

type coverFixture struct {
	*anonMediaFixture
	cover uuid.UUID
}

func newCoverFixture(t *testing.T) *coverFixture {
	t.Helper()
	f := &coverFixture{anonMediaFixture: newAnonMediaFixture(t), cover: uuid.New()}
	f.set(t, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status) VALUES ($1,$2,'image','ready','passed')`, f.cover, f.author)
	f.set(t, `UPDATE posts SET cover_media_id = $1 WHERE id = $2`, f.cover, f.post)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = f.pool.Exec(bg, `UPDATE posts SET cover_media_id = NULL WHERE cover_media_id = $1`, f.cover)
		_, _ = f.pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, f.cover)
	})
	return f
}

// sees runs the single and batch paths for one asset and insists they agree.
func (f *coverFixture) sees(t *testing.T, viewer, media uuid.UUID) bool {
	t.Helper()
	ctx := context.Background()
	single, err := f.svc.ViewerMayAccessMedia(ctx, viewer, media)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := f.svc.ViewerMayAccessMediaBatch(ctx, viewer, []uuid.UUID{media})
	if err != nil {
		t.Fatal(err)
	}
	if single.Allowed != batch[media].Allowed {
		t.Fatalf("single (%v %s) and batch (%v %s) disagree", single.Allowed, single.Reason, batch[media].Allowed, batch[media].Reason)
	}
	return single.Allowed
}

func TestCoverMediaInheritsItsPostsAudience(t *testing.T) {
	f := newCoverFixture(t)
	post := func(col string, val any) { f.set(t, `UPDATE posts SET `+col+` = $1 WHERE id = $2`, val, f.post) }

	// The route GET /v1/internal/posts/by-media/:mediaId reads this.
	ids, err := f.svc.PostIDsByMediaID(context.Background(), f.cover)
	if err != nil || len(ids) != 1 || ids[0] != f.post {
		t.Fatalf("cover-only asset resolved to %v (%v), want [%s]", ids, err, f.post)
	}

	if !f.sees(t, uuid.Nil, f.cover) {
		t.Fatal("a stranger was refused the cover of a public post")
	}
	if !f.sees(t, f.viewer, f.cover) || !f.sees(t, f.author, f.cover) {
		t.Fatal("a signed-in viewer or the owner was refused the cover of a public post")
	}

	for _, tc := range []struct {
		name  string
		apply func()
		reset func()
	}{
		{"members-only", func() { post("tier_required_id", uuid.New()) }, func() { post("tier_required_id", nil) }},
		{"unlisted", func() { post("visibility", "unlisted") }, func() { post("visibility", "public") }},
		{"followers", func() { post("visibility", "followers") }, func() { post("visibility", "public") }},
		{"private", func() { post("visibility", "private") }, func() { post("visibility", "public") }},
		{"pending review", func() { post("review_status", "pending") }, func() { post("review_status", "approved") }},
		{"scheduled", func() { post("publish_at", hubNow.Add(24*time.Hour)) }, func() { post("publish_at", nil) }},
		{"age-restricted", func() { post("age_restricted", true) }, func() { post("age_restricted", false) }},
		{"soft-deleted", func() { post("deleted_at", hubNow) }, func() { post("deleted_at", nil) }},
		{"hidden author", func() {
			f.set(t, `INSERT INTO post_hidden_authors (user_id, reason) VALUES ($1, 'deactivated') ON CONFLICT DO NOTHING`, f.author)
		}, func() { f.set(t, `DELETE FROM post_hidden_authors WHERE user_id = $1`, f.author) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.apply()
			defer tc.reset()
			if f.sees(t, uuid.Nil, f.cover) {
				t.Fatalf("a stranger was served the cover of a %s post", tc.name)
			}
			if !f.sees(t, f.author, f.cover) {
				t.Fatalf("the owner was refused the cover of their own %s post", tc.name)
			}
		})
	}
	if !f.sees(t, uuid.Nil, f.cover) {
		t.Fatal("the cover did not come back after the resets")
	}

	// Taken off the post, the asset is nobody's but its uploader's again.
	f.set(t, `UPDATE posts SET cover_media_id = NULL WHERE id = $1`, f.post)
	if f.sees(t, uuid.Nil, f.cover) || f.sees(t, f.viewer, f.cover) {
		t.Fatal("an asset no post carries was served")
	}
}

// Naming somebody else's asset as your cover must not publish it: the
// create route and the cover-frame route store cover_media_id without an
// ownership check, so the by-media lookup is where this is held.
func TestCoverMediaCannotPublishAStrangersAsset(t *testing.T) {
	f := newCoverFixture(t)
	ctx := context.Background()
	victim, theirs := uuid.New(), uuid.New()
	f.set(t, `INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, victim)
	f.set(t, `INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status) VALUES ($1,$2,'image','ready','passed')`, theirs, victim)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = f.pool.Exec(bg, `UPDATE posts SET cover_media_id = NULL WHERE cover_media_id = $1`, theirs)
		_, _ = f.pool.Exec(bg, `DELETE FROM media_assets WHERE id = $1`, theirs)
		_, _ = f.pool.Exec(bg, `DELETE FROM users WHERE id = $1`, victim)
	})
	if f.sees(t, uuid.Nil, theirs) || f.sees(t, f.viewer, theirs) {
		t.Fatal("an unattached asset was served before the test began")
	}

	// The author's public post names the victim's private asset as its cover.
	f.set(t, `UPDATE posts SET cover_media_id = $1 WHERE id = $2`, theirs, f.post)

	if f.sees(t, uuid.Nil, theirs) {
		t.Fatal("a stranger was served an asset that a post by somebody else named as its cover")
	}
	if f.sees(t, f.viewer, theirs) || f.sees(t, f.author, theirs) {
		t.Fatal("a signed-in viewer (or the post's author) was served the victim's asset through the cover")
	}
	if !f.sees(t, victim, theirs) {
		t.Fatal("the uploader lost their own asset")
	}
	if ids, err := f.svc.PostIDsByMediaID(ctx, theirs); err != nil || len(ids) != 0 {
		t.Fatalf("the victim's asset resolved to %v (%v)", ids, err)
	}
}
