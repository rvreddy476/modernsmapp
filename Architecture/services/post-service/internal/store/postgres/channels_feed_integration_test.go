//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Channel feed (RSS publishing, 2026-09-29) against a real Postgres.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run 'ChannelFeed|CoverMedia' -v
//
// Same rig as the MTube suite (setup.sql + every migration on a database
// whose name ends in _test). The feed is one document for the whole
// internet, so the predicate IS the feature: one owner is seeded with a
// video for every clause that must keep a row out, and each is asserted on
// its own. The Go mirror of the predicate is proved over in-memory rows in
// service/channel_feed_test.go.

type feedSeed struct {
	contentType string
	visibility  string
	category    string
	language    string
	review      string
	age         time.Duration // published this long ago
	pinned      bool
	noMedia     bool
}

type feedRig struct {
	*mtubeRig
	now time.Time
}

func newFeedRig(t *testing.T) *feedRig {
	t.Helper()
	return &feedRig{mtubeRig: newMTubeRig(t), now: time.Now().UTC().Truncate(time.Second)}
}

// seed inserts one post by author with a ready, passed video attached
// (unless noMedia) and returns its id and its media id.
func (r *feedRig) seed(t *testing.T, author uuid.UUID, s feedSeed) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	if s.contentType == "" {
		s.contentType = "long_video"
	}
	if s.visibility == "" {
		s.visibility = "public"
	}
	if s.review == "" {
		s.review = "approved"
	}
	postID, mediaID := uuid.New(), uuid.Nil
	published := r.now.Add(-s.age)
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO posts (id, author_id, text, title, visibility, content_type, review_status, category, language,
		                   is_pinned, created_at, updated_at, published_at)
		VALUES ($1, $2, 'feed proof', $3, $4, $5, $6, $7, $8, $9, $10, $10, $10)`,
		postID, author, "Episode "+postID.String()[:6], s.visibility, s.contentType, s.review, s.category, s.language,
		s.pinned, published); err != nil {
		t.Fatalf("seed post: %v", err)
	}
	if !s.noMedia {
		mediaID = r.newMedia(t, author, "video", "ready", "passed", "user/"+author.String()+"/"+postID.String()+"/original")
		if _, err := r.pool.Exec(ctx, `INSERT INTO post_media (post_id, media_id, kind, position) VALUES ($1, $2, 'video', 0)`, postID, mediaID); err != nil {
			t.Fatalf("seed post_media: %v", err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		// A restriction row holds the post (ON DELETE RESTRICT); release the
		// count with it so the deferred consistency trigger agrees.
		if tx, err := r.pool.Begin(bg); err == nil {
			_, _ = tx.Exec(bg, `DELETE FROM post_restrictions WHERE post_id = $1`, postID)
			_, _ = tx.Exec(bg, `UPDATE posts SET active_restriction_count = 0 WHERE id = $1`, postID)
			_ = tx.Commit(bg)
		}
		_, _ = r.pool.Exec(bg, `DELETE FROM post_media WHERE post_id = $1`, postID)
		_, _ = r.pool.Exec(bg, `DELETE FROM post_engagement_counts WHERE post_id = $1`, postID)
		_, _ = r.pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, postID)
	})
	return postID, mediaID
}

func (r *feedRig) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := r.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// restrict places one active restriction on the post the way the
// restriction writer does: the row and the recount in one transaction.
func (r *feedRig) restrict(t *testing.T, postID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_restrictions (restriction_id, post_id, source, case_id, issuer, state, case_revision, last_decision_id, policy_version, reason_code)
		VALUES ($1, $2, 'copyright', $3, 'trust-safety-service', 'active', 1, $4, 'feed-test', 'test')`,
		uuid.New(), postID, uuid.New(), uuid.New()); err != nil {
		t.Fatalf("seed restriction: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE posts SET active_restriction_count = (SELECT COUNT(*) FROM post_restrictions WHERE post_id = $1 AND state = 'active') WHERE id = $1`, postID); err != nil {
		t.Fatalf("recount: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit restriction: %v", err)
	}
}

func (r *feedRig) list(t *testing.T, owner uuid.UUID, category string, limit int) []postgres.ChannelFeedVideo {
	t.Helper()
	rows, err := r.store.ListChannelFeedVideos(context.Background(), owner, category, limit)
	if err != nil {
		t.Fatalf("ListChannelFeedVideos: %v", err)
	}
	return rows
}

func feedIDs(rows []postgres.ChannelFeedVideo) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Post.ID)
	}
	return out
}

func feedHas(rows []postgres.ChannelFeedVideo, id uuid.UUID) bool {
	for _, row := range rows {
		if row.Post.ID == id {
			return true
		}
	}
	return false
}

func TestChannelFeedListsOnlyWhatAStrangerMayPlay(t *testing.T) {
	r := newFeedRig(t)
	other := uuid.New()
	newUser(t, r.pool, other)

	// What belongs in the feed.
	newest, newestMedia := r.seed(t, r.owner, feedSeed{category: "science-tech", language: "en", age: 1 * time.Hour})
	podcast, _ := r.seed(t, r.owner, feedSeed{category: "podcasts", language: "en", age: 2 * time.Hour})
	legacy, _ := r.seed(t, r.owner, feedSeed{contentType: "video", category: "podcasts", language: "te", age: 3 * time.Hour})
	pinnedOlder, _ := r.seed(t, r.owner, feedSeed{category: "science-tech", language: "en", age: 96 * time.Hour, pinned: true})

	// One row per clause that must keep a video out.
	unlisted, _ := r.seed(t, r.owner, feedSeed{visibility: "unlisted", age: 4 * time.Hour})
	followers, _ := r.seed(t, r.owner, feedSeed{visibility: "followers", age: 5 * time.Hour})
	private, _ := r.seed(t, r.owner, feedSeed{visibility: "private", age: 6 * time.Hour})
	members, _ := r.seed(t, r.owner, feedSeed{age: 7 * time.Hour})
	r.exec(t, `UPDATE posts SET tier_required_id = $2 WHERE id = $1`, members, uuid.New())
	scheduled, _ := r.seed(t, r.owner, feedSeed{age: 8 * time.Hour})
	r.exec(t, `UPDATE posts SET publish_at = NOW() + interval '1 day' WHERE id = $1`, scheduled)
	overdue, _ := r.seed(t, r.owner, feedSeed{age: 9 * time.Hour})
	r.exec(t, `UPDATE posts SET publish_at = NOW() - interval '1 day' WHERE id = $1`, overdue)
	ageRestricted, _ := r.seed(t, r.owner, feedSeed{age: 10 * time.Hour})
	r.exec(t, `UPDATE posts SET age_restricted = TRUE WHERE id = $1`, ageRestricted)
	deleted, _ := r.seed(t, r.owner, feedSeed{age: 11 * time.Hour})
	r.exec(t, `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, deleted)
	rejected, _ := r.seed(t, r.owner, feedSeed{review: "rejected", age: 12 * time.Hour})
	pending, _ := r.seed(t, r.owner, feedSeed{review: "pending", age: 13 * time.Hour})
	restricted, _ := r.seed(t, r.owner, feedSeed{age: 14 * time.Hour})
	r.restrict(t, restricted)
	flick, _ := r.seed(t, r.owner, feedSeed{contentType: "flick", age: 15 * time.Hour})
	plain, _ := r.seed(t, r.owner, feedSeed{contentType: "post", age: 16 * time.Hour})
	otherAuthor, _ := r.seed(t, other, feedSeed{category: "podcasts", age: 30 * time.Minute})

	rows := r.list(t, r.owner, "", 0)

	for name, id := range map[string]uuid.UUID{
		"unlisted (visibility = 'public')":                  unlisted,
		"followers-only (visibility = 'public')":            followers,
		"private (visibility = 'public')":                   private,
		"members-only (tier_required_id IS NULL)":           members,
		"scheduled (publish_at IS NULL)":                    scheduled,
		"scheduled, time passed, not yet published":         overdue,
		"age-restricted (age_restricted = FALSE)":           ageRestricted,
		"soft-deleted (deleted_at IS NULL)":                 deleted,
		"rejected (effective status approved)":              rejected,
		"pending review (effective status approved)":        pending,
		"restricted by a hold (effective status, not base)": restricted,
		"a flick (content_type)":                            flick,
		"a plain post (content_type)":                       plain,
		"another author's video (author_id)":                otherAuthor,
	} {
		if feedHas(rows, id) {
			t.Errorf("%s: post %s is in the feed", name, id)
		}
	}

	// Newest first by publication; the pin does not lift the old video.
	want := []uuid.UUID{newest, podcast, legacy, pinnedOlder}
	got := feedIDs(rows)
	if len(got) != len(want) {
		t.Fatalf("feed = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("feed order = %v, want %v (newest first, pins ignored)", got, want)
		}
	}

	// The primary video and the whole post ride on the row.
	first := rows[0]
	if first.MediaID == nil || *first.MediaID != newestMedia || first.DurationMs != 90000 ||
		first.ProcessingStatus != "ready" || first.ModerationStatus != "passed" {
		t.Fatalf("primary video = %+v", first)
	}
	if first.Post.AuthorID != r.owner || first.Post.Visibility != "public" || first.Post.Category != "science-tech" ||
		first.Post.Language != "en" || first.Post.PublishedAt == nil || first.Post.EffectiveReviewStatus() != "approved" {
		t.Fatalf("post on the row = %+v", first.Post)
	}
	if !rows[3].Post.IsPinned {
		t.Fatalf("the pinned row lost its pin flag: the order test proves nothing")
	}

	// ?category= narrows; an id nothing carries is an empty feed.
	narrowed := feedIDs(r.list(t, r.owner, "podcasts", 0))
	if len(narrowed) != 2 || narrowed[0] != podcast || narrowed[1] != legacy {
		t.Fatalf("category=podcasts = %v, want [%s %s]", narrowed, podcast, legacy)
	}
	if empty := r.list(t, r.owner, "documentary", 0); empty == nil || len(empty) != 0 {
		t.Fatalf("category with no videos = %#v, want an empty list", empty)
	}
	if rows := r.list(t, uuid.New(), "", 0); rows == nil || len(rows) != 0 {
		t.Fatalf("an owner with no videos = %#v", rows)
	}

	// limit: honoured, and never above the ceiling.
	if two := feedIDs(r.list(t, r.owner, "", 2)); len(two) != 2 || two[0] != newest || two[1] != podcast {
		t.Fatalf("limit=2 = %v", two)
	}
	if all := r.list(t, r.owner, "", 5000); len(all) != 4 {
		t.Fatalf("limit=5000 returned %d rows", len(all))
	}

	// The feed-wide facts read the same rows: three of the four eligible
	// videos are "en" and the categories tie 2-2, broken by id order. None
	// of the excluded rows may vote.
	r.exec(t, `UPDATE posts SET category = 'comedy', language = 'hi' WHERE id = ANY($1)`,
		[]uuid.UUID{unlisted, followers, private, members, scheduled, overdue, ageRestricted, deleted, rejected, pending, restricted, flick, plain})
	facts, err := r.store.ChannelFeedFacts(context.Background(), r.owner)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Language != "en" || facts.DominantCategory != "podcasts" {
		t.Fatalf("facts = %+v, want en / podcasts", facts)
	}
	r.exec(t, `UPDATE posts SET category = 'science-tech' WHERE id = $1`, legacy)
	if facts, err = r.store.ChannelFeedFacts(context.Background(), r.owner); err != nil || facts.DominantCategory != "science-tech" {
		t.Fatalf("facts = %+v (%v), want science-tech once it leads 3-1", facts, err)
	}
	if none, err := r.store.ChannelFeedFacts(context.Background(), uuid.New()); err != nil || none.Language != "" || none.DominantCategory != "" {
		t.Fatalf("facts of an owner with nothing = %+v (%v)", none, err)
	}
}

// The primary video is the first VIDEO in carousel order, and a post with
// no video has none.
func TestChannelFeedPrimaryVideo(t *testing.T) {
	r := newFeedRig(t)
	ctx := context.Background()
	post, first := r.seed(t, r.owner, feedSeed{age: time.Hour})
	image := r.newMedia(t, r.owner, "image", "ready", "passed", "user/x/cover")
	second := r.newMedia(t, r.owner, "video", "processing", "pending", "user/x/second")
	// Move the first video to ordinal 1, put an image at 0 and a second
	// video at 2.
	r.exec(t, `UPDATE post_media SET position = 1 WHERE post_id = $1 AND media_id = $2`, post, first)
	r.exec(t, `INSERT INTO post_media (post_id, media_id, kind, position) VALUES ($1, $2, 'image', 0), ($1, $3, 'video', 2)`, post, image, second)
	bare, _ := r.seed(t, r.owner, feedSeed{age: 2 * time.Hour, noMedia: true})

	rows, err := r.store.ListChannelFeedVideos(ctx, r.owner, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Post.ID != post || rows[1].Post.ID != bare {
		t.Fatalf("rows = %v", feedIDs(rows))
	}
	if rows[0].MediaID == nil || *rows[0].MediaID != first || rows[0].ProcessingStatus != "ready" {
		t.Fatalf("primary video = %+v, want the first video %s (not the image, not the later video)", rows[0], first)
	}
	if rows[1].MediaID != nil || rows[1].DurationMs != 0 {
		t.Fatalf("a post with no video reported one: %+v", rows[1])
	}
}

// ── covers in the by-media lookups ─────────────────────────────────────────

func sameIDs(got []uuid.UUID, want ...uuid.UUID) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[uuid.UUID]int{}
	for _, id := range got {
		seen[id]++
	}
	for _, id := range want {
		if seen[id] != 1 {
			return false
		}
	}
	return true
}

func TestCoverMediaResolvesToItsPost(t *testing.T) {
	r := newFeedRig(t)
	ctx := context.Background()
	stranger := uuid.New()
	newUser(t, r.pool, stranger)

	post, video := r.seed(t, r.owner, feedSeed{age: time.Hour})
	cover := r.newMedia(t, r.owner, "image", "ready", "passed", "user/owner/cover")
	r.exec(t, `UPDATE posts SET cover_media_id = $2 WHERE id = $1`, post, cover)

	// A cover-only asset resolves to the post it covers.
	ids, err := r.store.PostIDsByMediaID(ctx, cover)
	if err != nil || !sameIDs(ids, post) {
		t.Fatalf("cover -> %v (%v), want [%s]", ids, err, post)
	}
	// An attachment still resolves, once, even when it is also the cover.
	second, secondVideo := r.seed(t, r.owner, feedSeed{age: 2 * time.Hour})
	r.exec(t, `UPDATE posts SET cover_media_id = $2 WHERE id = $1`, second, secondVideo)
	if ids, err = r.store.PostIDsByMediaID(ctx, secondVideo); err != nil || !sameIDs(ids, second) {
		t.Fatalf("attachment that is also the cover -> %v (%v), want [%s] once", ids, err, second)
	}
	// One asset covering two posts resolves to both.
	third, _ := r.seed(t, r.owner, feedSeed{age: 3 * time.Hour})
	r.exec(t, `UPDATE posts SET cover_media_id = $2 WHERE id = $1`, third, cover)
	if ids, err = r.store.PostIDsByMediaID(ctx, cover); err != nil || !sameIDs(ids, post, third) {
		t.Fatalf("shared cover -> %v (%v), want [%s %s]", ids, err, post, third)
	}

	// The batch form agrees.
	batch, err := r.store.PostIDsByMediaIDs(ctx, []uuid.UUID{cover, video, secondVideo, uuid.New()})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch) != 3 || !sameIDs(batch[cover], post, third) || !sameIDs(batch[video], post) || !sameIDs(batch[secondVideo], second) {
		t.Fatalf("batch = %v", batch)
	}

	// A soft-deleted post stops standing for its cover.
	r.exec(t, `UPDATE posts SET deleted_at = NOW() WHERE id = $1`, third)
	if ids, err = r.store.PostIDsByMediaID(ctx, cover); err != nil || !sameIDs(ids, post) {
		t.Fatalf("after deleting one post: cover -> %v (%v), want [%s]", ids, err, post)
	}
	if batch, err = r.store.PostIDsByMediaIDs(ctx, []uuid.UUID{cover}); err != nil || !sameIDs(batch[cover], post) {
		t.Fatalf("after deleting one post: batch -> %v (%v)", batch, err)
	}

	// A post cannot stand for an asset its author did not upload: naming a
	// stranger's asset as your cover does not make it yours to publish.
	theirs := r.newMedia(t, stranger, "image", "ready", "passed", "user/stranger/private-photo")
	thief, _ := r.seed(t, r.owner, feedSeed{age: 4 * time.Hour})
	r.exec(t, `UPDATE posts SET cover_media_id = $2 WHERE id = $1`, thief, theirs)
	if ids, err = r.store.PostIDsByMediaID(ctx, theirs); err != nil || len(ids) != 0 {
		t.Fatalf("a stranger's asset named as a cover resolved to %v (%v)", ids, err)
	}
	if batch, err = r.store.PostIDsByMediaIDs(ctx, []uuid.UUID{theirs}); err != nil || len(batch[theirs]) != 0 {
		t.Fatalf("a stranger's asset named as a cover resolved in the batch to %v (%v)", batch, err)
	}

	// The review-gate release reads attachments only.
	if ids, err = r.store.PostIDsAttachingMedia(ctx, cover); err != nil || len(ids) != 0 {
		t.Fatalf("PostIDsAttachingMedia(cover) = %v (%v), want none", ids, err)
	}
	if ids, err = r.store.PostIDsAttachingMedia(ctx, video); err != nil || !sameIDs(ids, post) {
		t.Fatalf("PostIDsAttachingMedia(video) = %v (%v), want [%s]", ids, err, post)
	}
}

// Migration 057 gives the cover lookup its index.
func TestCoverMediaLookupIsIndexed(t *testing.T) {
	r := newFeedRig(t)
	var def string
	if err := r.pool.QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE tablename = 'posts' AND indexname = 'idx_posts_cover_media'`).Scan(&def); err != nil {
		t.Fatalf("idx_posts_cover_media: %v", err)
	}
	t.Log(def)
}
