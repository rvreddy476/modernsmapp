//go:build integration

package postgres_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/atpost/post-service/database"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MTube contracts against a real Postgres (2026-09-27; migration 051).
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/store/postgres/ -run MTube -v
//
// Same rig as the channel-subscription suite (setup.sql + every migration
// on a database whose name ends in _test), so migration 051 itself is under
// test: the taxonomy remap, source / live_stream_id, the channel branding
// columns, playlists.kind with its partial unique index, the comment pin /
// heart columns, and the append-only edit audit.

type mtubeRig struct {
	pool  *pgxpool.Pool
	store *postgres.Store
	owner uuid.UUID
}

func newMTubeRig(t *testing.T) *mtubeRig {
	t.Helper()
	pool := openSubscriptionDB(t)
	ctx := context.Background()
	// Columns the ownership / recording lookups read that the minimal
	// media_assets fixture above does not declare.
	for _, ddl := range []string{
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS storage_key TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS hls_master_key TEXT`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_text TEXT DEFAULT ''`,
		`ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN DEFAULT FALSE`,
	} {
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	owner := uuid.New()
	newUser(t, pool, owner)
	return &mtubeRig{pool: pool, store: postgres.New(pool), owner: owner}
}

func (r *mtubeRig) newPost(t *testing.T, author uuid.UUID, contentType, visibility, category string) *postgres.Post {
	t.Helper()
	p := &postgres.Post{
		ID: uuid.New(), AuthorID: author, Text: "0:00 Intro\n1:00 Two\n2:00 Three", Visibility: visibility, ContentType: contentType,
		PostType: "video", AppOrigin: "tube", Title: "Title " + uuid.NewString()[:6], Category: category,
		ReviewStatus: "approved", CreatedAt: time.Now(), UpdatedAt: time.Now(), ContentTypeExplicit: true,
	}
	if err := r.store.CreatePost(context.Background(), p); err != nil {
		t.Fatalf("create post: %v", err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, p.ID)
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, p.ID)
	})
	return p
}

func (r *mtubeRig) newMedia(t *testing.T, uploader uuid.UUID, fileType, status, moderation, storageKey string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := r.pool.Exec(context.Background(), `
		INSERT INTO media_assets (id, uploader_id, file_type, processing_status, moderation_status, storage_key, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6, 90000)`, id, uploader, fileType, status, moderation, storageKey); err != nil {
		t.Fatalf("seed media: %v", err)
	}
	t.Cleanup(func() { _, _ = r.pool.Exec(context.Background(), `DELETE FROM media_assets WHERE id = $1`, id) })
	return id
}

func (r *mtubeRig) newComment(t *testing.T, postID, author uuid.UUID, parent *uuid.UUID, body string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var c *postgres.Comment
	var err error
	if parent == nil {
		c, err = r.store.CreateComment(ctx, postID, author, body)
	} else {
		c, _, err = r.store.CreateReply(ctx, *parent, author, body)
	}
	if err != nil {
		t.Fatalf("create comment: %v", err)
	}
	return c.ID
}

// ── A. taxonomy remap ───────────────────────────────────────────────────────

func TestMTubeMigrationRemapsLongVideoCategories(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	// Migration 051 already ran on bootstrap; re-run part A's statement
	// shape by seeding legacy values and applying the same UPDATE through
	// the migration file itself is what a fresh bootstrap did. Here we
	// verify the rule on rows inserted AFTER bootstrap by re-executing the
	// migration (it is idempotent by construction).
	p1 := r.newPost(t, r.owner, "long_video", "public", "film-&-animation")
	p2 := r.newPost(t, r.owner, "long_video", "public", "nonprofits-&-activism")
	p3 := r.newPost(t, r.owner, "long_video", "public", "cooking-shows")
	p4 := r.newPost(t, r.owner, "long_video", "public", "podcasts")
	p5 := r.newPost(t, r.owner, "flick", "public", "film-&-animation") // not a long video: untouched
	sql, err := migrationSQL("051_mtube_contracts.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, sql); err != nil {
		t.Fatalf("re-running migration 051: %v", err)
	}
	want := map[uuid.UUID]string{p1.ID: "film-animation", p2.ID: "other", p3.ID: "other", p4.ID: "podcasts", p5.ID: "film-&-animation"}
	for id, w := range want {
		var got string
		if err := r.pool.QueryRow(ctx, `SELECT category FROM posts WHERE id = $1`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("post %s category=%q want %q", id, got, w)
		}
	}
}

// ── B. live -> video ────────────────────────────────────────────────────────

func TestMTubeLiveVODPostIsIdempotentPerStream(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	stream := uuid.New()
	media := r.newMedia(t, r.owner, "video", "ready", "passed", "live-recordings/"+stream.String()+".mp4")

	found, err := r.store.FindMediaByStorageKeySuffix(ctx, "live-recordings/"+stream.String()+".mp4")
	if err != nil || found != media {
		t.Fatalf("suffix lookup: %s %v", found, err)
	}
	build := func() postgres.LiveVODInsert {
		p := &postgres.Post{ID: uuid.New(), AuthorID: r.owner, Visibility: "unlisted", ContentType: "long_video", PostType: "video",
			Title: "Live", ReviewStatus: "approved", CreatedAt: time.Now(), UpdatedAt: time.Now(), ContentTypeExplicit: true}
		return postgres.LiveVODInsert{Post: p, StreamID: stream, MediaID: media,
			VideoMetadata: &postgres.VideoMetadata{PostID: p.ID, DurationSeconds: 90, Orientation: "landscape", ComputedCategory: "long_video", FinalCategory: "long_video", UploadStatus: "ready", MediaAssetID: &media},
			EventType:     events.PostCreated, EventPayload: events.PostCreatedPayload{PostID: p.ID.String(), AuthorID: r.owner.String(), Visibility: "unlisted", ContentType: "long_video"}}
	}
	first, created, err := r.store.CreateLiveVODPost(ctx, build())
	if err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, first.ID)
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM posts WHERE id = $1`, first.ID)
	})
	second, created, err := r.store.CreateLiveVODPost(ctx, build())
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second: id=%s created=%v err=%v (want the first post, not created)", second.ID, created, err)
	}
	var source string
	var streamID uuid.UUID
	var mediaCount, vmCount, outboxCount int
	if err := r.pool.QueryRow(ctx, `SELECT source, live_stream_id FROM posts WHERE id = $1`, first.ID).Scan(&source, &streamID); err != nil {
		t.Fatal(err)
	}
	if source != "live" || streamID != stream {
		t.Fatalf("source=%q live_stream_id=%s", source, streamID)
	}
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_media WHERE post_id = $1`, first.ID).Scan(&mediaCount)
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM video_metadata WHERE post_id = $1`, first.ID).Scan(&vmCount)
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_outbox_events WHERE aggregate_id = $1 AND event_type = $2`, first.ID, events.PostCreated).Scan(&outboxCount)
	if mediaCount != 1 || vmCount != 1 || outboxCount != 1 {
		t.Fatalf("media=%d video_metadata=%d outbox=%d, want 1/1/1", mediaCount, vmCount, outboxCount)
	}
	// The unique index refuses a second row for the stream outright.
	if _, err := r.pool.Exec(ctx, `INSERT INTO posts (id, author_id, text, visibility, content_type, created_at, updated_at, live_stream_id) VALUES ($1, $2, '', 'unlisted', 'long_video', now(), now(), $3)`,
		uuid.New(), r.owner, stream); err == nil {
		t.Fatal("a second post for the same stream was accepted")
	}
	got, err := r.store.GetPostByLiveStream(ctx, stream)
	if err != nil || got == nil || got.ID != first.ID {
		t.Fatalf("GetPostByLiveStream: %v %v", got, err)
	}

	// The internal by-live-stream lookup (live-service-v2's
	// recording_post_id): the same post while it lives, flagged once it is
	// soft-deleted (GetPostByLiveStream stops seeing it), nil for a stream
	// with no post.
	ref, err := r.store.GetLiveStreamPostRef(ctx, stream)
	if err != nil || ref == nil || ref.PostID != first.ID || ref.Visibility != "unlisted" || ref.Deleted {
		t.Fatalf("GetLiveStreamPostRef: %+v %v", ref, err)
	}
	if none, err := r.store.GetLiveStreamPostRef(ctx, uuid.New()); err != nil || none != nil {
		t.Fatalf("a stream with no post: %+v %v", none, err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE posts SET deleted_at = now() WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	ref, err = r.store.GetLiveStreamPostRef(ctx, stream)
	if err != nil || ref == nil || ref.PostID != first.ID || !ref.Deleted {
		t.Fatalf("GetLiveStreamPostRef after the delete: %+v %v", ref, err)
	}
	if gone, err := r.store.GetPostByLiveStream(ctx, stream); err != nil || gone != nil {
		t.Fatalf("GetPostByLiveStream after the delete: %v %v", gone, err)
	}
}

// ── C. channel branding ─────────────────────────────────────────────────────

func TestMTubeChannelBrandingRoundTripsAndCounts(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	ch := &postgres.Channel{UserID: r.owner, Name: "Brand " + uuid.NewString()[:6], Handle: "brand." + strings.ToLower(uuid.NewString()[:8])}
	if err := r.store.CreateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM post_outbox_events WHERE aggregate_id = $1`, ch.UserID)
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM channels WHERE id = $1`, ch.ID)
	})
	if ch.Links == nil || len(ch.Links) != 0 || ch.ContactEmail != "" {
		t.Fatalf("fresh channel branding: %+v", ch)
	}
	banner := r.newMedia(t, r.owner, "image", "ready", "passed", "img/banner.jpg")
	featured := r.newPost(t, r.owner, "long_video", "public", "")
	links := []postgres.ChannelLink{{Title: "Site", URL: "https://example.com"}, {Title: "Shop", URL: "https://shop.example.com"}}
	email := "hello@example.com"
	updated, err := r.store.UpdateChannel(ctx, r.owner, postgres.ChannelPatch{BannerMediaID: &banner, Links: &links, ContactEmail: &email, FeaturedPostID: &featured.ID})
	if err != nil {
		t.Fatal(err)
	}
	if updated.BannerMediaID == nil || *updated.BannerMediaID != banner || len(updated.Links) != 2 || updated.Links[1].Title != "Shop" ||
		updated.ContactEmail != email || updated.FeaturedPostID == nil || *updated.FeaturedPostID != featured.ID {
		t.Fatalf("updated: %+v", updated)
	}
	// A patch that names nothing keeps everything; clears clear.
	kept, err := r.store.UpdateChannel(ctx, r.owner, postgres.ChannelPatch{})
	if err != nil || len(kept.Links) != 2 || kept.BannerMediaID == nil {
		t.Fatalf("kept: %+v %v", kept, err)
	}
	cleared, err := r.store.UpdateChannel(ctx, r.owner, postgres.ChannelPatch{ClearBanner: true, ClearFeatured: true, Links: &[]postgres.ChannelLink{}})
	if err != nil || cleared.BannerMediaID != nil || cleared.FeaturedPostID != nil || len(cleared.Links) != 0 {
		t.Fatalf("cleared: %+v %v", cleared, err)
	}
	// The JSONB CHECK refuses an eleventh link at the database.
	if _, err := r.pool.Exec(ctx, `UPDATE channels SET links = $2 WHERE id = $1`, ch.ID,
		`[{"t":1},{"t":2},{"t":3},{"t":4},{"t":5},{"t":6},{"t":7},{"t":8},{"t":9},{"t":10},{"t":11}]`); err == nil {
		t.Fatal("eleven links accepted by the CHECK")
	}
	// Every read path scans the branding columns.
	byHandle, err := r.store.GetChannelByHandle(ctx, ch.Handle)
	if err != nil || byHandle == nil || byHandle.Links == nil {
		t.Fatalf("by handle: %+v %v", byHandle, err)
	}
	batch, err := r.store.GetChannelsByUserIDs(ctx, []uuid.UUID{r.owner})
	if err != nil || batch[r.owner] == nil {
		t.Fatalf("batch: %v", err)
	}
	hits, err := r.store.SearchChannels(ctx, strings.ToLower(ch.Name[:5]), 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	// Counts: one public long video (featured), one short, one live VOD,
	// one public user playlist; private / unlisted / scheduled rows and
	// system playlists do not count.
	r.newPost(t, r.owner, "flick", "public", "")
	r.newPost(t, r.owner, "long_video", "private", "")
	live := r.newPost(t, r.owner, "long_video", "public", "")
	if _, err := r.pool.Exec(ctx, `UPDATE posts SET source = 'live', live_stream_id = $2 WHERE id = $1`, live.ID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	pl := &postgres.Playlist{CreatorID: r.owner, Title: "Public", Visibility: "public"}
	if err := r.store.CreatePlaylist(ctx, pl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM playlists WHERE creator_id = $1`, r.owner)
	})
	priv := &postgres.Playlist{CreatorID: r.owner, Title: "Private", Visibility: "private"}
	if err := r.store.CreatePlaylist(ctx, priv); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.GetOrCreateSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater); err != nil {
		t.Fatal(err)
	}
	counts, err := r.store.CountChannelContent(ctx, r.owner)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Videos != 2 || counts.Shorts != 1 || counts.Live != 1 || counts.Collections != 1 {
		t.Fatalf("counts=%+v want videos 2 (featured + live), shorts 1, live 1, collections 1", counts)
	}
	_ = hits
}

// ── D. system collections ───────────────────────────────────────────────────

func TestMTubeSystemPlaylistsAreOnePerOwnerAndIdempotent(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM playlists WHERE creator_id = $1`, r.owner)
	})

	q1, err := r.store.GetOrCreateSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater)
	if err != nil {
		t.Fatal(err)
	}
	q2, _ := r.store.GetOrCreateSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater)
	if q1.ID != q2.ID || q1.Kind != "watch_later" || q1.Title != "Queue" || q1.Visibility != "private" {
		t.Fatalf("queue: %+v / %+v", q1, q2)
	}
	loved, _ := r.store.GetOrCreateSystemPlaylist(ctx, r.owner, postgres.PlaylistKindLiked)
	if loved.ID == q1.ID || loved.Title != "Loved" {
		t.Fatalf("loved: %+v", loved)
	}
	// The partial unique index refuses a second Queue for the owner.
	if _, err := r.pool.Exec(ctx, `INSERT INTO playlists (creator_id, title, visibility, kind) VALUES ($1, 'Dup', 'private', 'watch_later')`, r.owner); err == nil {
		t.Fatal("second watch_later playlist accepted")
	}
	// The CHECK refuses an unknown kind.
	if _, err := r.pool.Exec(ctx, `INSERT INTO playlists (creator_id, title, visibility, kind) VALUES ($1, 'Bad', 'private', 'favourites')`, r.owner); err == nil {
		t.Fatal("unknown kind accepted")
	}
	// User playlists default to 'user' and read it back.
	user := &postgres.Playlist{CreatorID: r.owner, Title: "Mine", Visibility: "public"}
	if err := r.store.CreatePlaylist(ctx, user); err != nil {
		t.Fatal(err)
	}
	got, _ := r.store.GetPlaylist(ctx, user.ID)
	if got.Kind != "user" {
		t.Fatalf("user playlist kind=%q", got.Kind)
	}

	post := r.newPost(t, r.owner, "long_video", "public", "")
	post2 := r.newPost(t, r.owner, "long_video", "public", "")
	added, err := r.store.AddToSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater, post.ID)
	if err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	again, err := r.store.AddToSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater, post.ID)
	if err != nil || again {
		t.Fatalf("add again: %v %v (want idempotent)", again, err)
	}
	if _, err := r.store.AddToSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater, post2.ID); err != nil {
		t.Fatal(err)
	}
	in, _ := r.store.IsInSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater, post.ID)
	if !in {
		t.Fatal("viewer_queued false after add")
	}
	items, _ := r.store.GetPlaylistItems(ctx, q1.ID)
	if len(items) != 2 || items[0].PostID != post.ID || items[1].Position != 1 {
		t.Fatalf("items: %+v", items)
	}
	after, _ := r.store.GetPlaylist(ctx, q1.ID)
	if after.ItemCount != 2 {
		t.Fatalf("item_count=%d want 2", after.ItemCount)
	}
	removed, err := r.store.RemoveFromSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater, post.ID)
	if err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	removed, err = r.store.RemoveFromSystemPlaylist(ctx, r.owner, postgres.PlaylistKindWatchLater, post.ID)
	if err != nil || removed {
		t.Fatalf("remove again: %v %v (want idempotent)", removed, err)
	}
	if _, err := r.store.RemoveFromSystemPlaylist(ctx, uuid.New(), postgres.PlaylistKindLiked, post.ID); err != nil {
		t.Fatalf("remove from a never-created collection: %v", err)
	}
	n, _ := r.store.CountUserPlaylists(ctx, r.owner)
	if n != 1 {
		t.Fatalf("CountUserPlaylists=%d want 1 (system kinds excluded)", n)
	}
}

// ── E. comments: sort, pin, heart, inbox ────────────────────────────────────

func TestMTubeCommentsSortPinHeartAndInbox(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	fan, fan2 := uuid.New(), uuid.New()
	newUser(t, r.pool, fan)
	newUser(t, r.pool, fan2)
	post := r.newPost(t, r.owner, "long_video", "public", "")
	t.Cleanup(func() { _, _ = r.pool.Exec(context.Background(), `DELETE FROM comments WHERE post_id = $1`, post.ID) })

	oldest := r.newComment(t, post.ID, fan, nil, "first")
	time.Sleep(5 * time.Millisecond)
	popular := r.newComment(t, post.ID, fan2, nil, "popular")
	time.Sleep(5 * time.Millisecond)
	newest := r.newComment(t, post.ID, fan, nil, "newest")
	for _, u := range []uuid.UUID{fan, fan2, r.owner} {
		if err := r.store.SetCommentReaction(ctx, popular, u, "❤️"); err != nil {
			t.Fatal(err)
		}
	}
	reply := r.newComment(t, post.ID, r.owner, &oldest, "thanks")

	// Default order is newest, unchanged.
	page, _, err := r.store.ListComments(ctx, post.ID, nil, "", 10)
	if err != nil || len(page) != 3 || page[0].ID != newest || page[2].ID != oldest {
		t.Fatalf("newest: %v %v", ids(page), err)
	}
	if page[0].Pinned || page[0].HeartedByAuthor {
		t.Fatalf("fresh comment carries pinned/hearted: %+v", page[0])
	}
	// Top: the reacted comment leads.
	top, _, err := r.store.ListCommentsSorted(ctx, post.ID, nil, "", 10, postgres.CommentSortTop)
	if err != nil || top[0].ID != popular {
		t.Fatalf("top: %v %v", ids(top), err)
	}
	// Top paging is offset-based and does not repeat rows.
	p1, next, _ := r.store.ListCommentsSorted(ctx, post.ID, nil, "", 2, postgres.CommentSortTop)
	p2, next2, _ := r.store.ListCommentsSorted(ctx, post.ID, nil, next, 2, postgres.CommentSortTop)
	if len(p1) != 2 || next != "o:2" || len(p2) != 1 || next2 != "" || p2[0].ID == p1[0].ID || p2[0].ID == p1[1].ID {
		t.Fatalf("top paging: p1=%v next=%q p2=%v next2=%q", ids(p1), next, ids(p2), next2)
	}

	// Pin the oldest: it leads both orders and is never repeated.
	if err := r.store.PinComment(ctx, post.ID, reply); err != postgres.ErrCannotPinReply {
		t.Fatalf("pin a reply: %v", err)
	}
	if err := r.store.PinComment(ctx, post.ID, oldest); err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetCommentHeart(ctx, oldest, true); err != nil {
		t.Fatal(err)
	}
	page, _, _ = r.store.ListComments(ctx, post.ID, nil, "", 10)
	if page[0].ID != oldest || !page[0].Pinned || !page[0].HeartedByAuthor || page[1].ID != newest {
		t.Fatalf("pinned first (newest): %v pinned=%v hearted=%v", ids(page), page[0].Pinned, page[0].HeartedByAuthor)
	}
	top, _, _ = r.store.ListCommentsSorted(ctx, post.ID, nil, "", 10, postgres.CommentSortTop)
	if top[0].ID != oldest || top[1].ID != popular {
		t.Fatalf("pinned first (top): %v", ids(top))
	}
	// Newest paging with a pin: page 1 = [pinned, newest], page 2 = [popular], no repeat.
	n1, cur, _ := r.store.ListComments(ctx, post.ID, nil, "", 2)
	n2, cur2, _ := r.store.ListComments(ctx, post.ID, nil, cur, 2)
	if len(n1) != 2 || n1[0].ID != oldest || n1[1].ID != newest || len(n2) != 1 || n2[0].ID != popular || cur2 != "" {
		t.Fatalf("newest paging: n1=%v n2=%v cur2=%q", ids(n1), ids(n2), cur2)
	}
	// Re-pinning another comment moves the pin (one per post).
	if err := r.store.PinComment(ctx, post.ID, popular); err != nil {
		t.Fatal(err)
	}
	var pinnedCount int
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM comments WHERE post_id = $1 AND pinned_at IS NOT NULL`, post.ID).Scan(&pinnedCount)
	if pinnedCount != 1 {
		t.Fatalf("pinned rows=%d want 1", pinnedCount)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE comments SET pinned_at = now() WHERE id = $1`, oldest); err == nil {
		t.Fatal("the partial unique index let a second pin through")
	}
	if err := r.store.UnpinComment(ctx, popular); err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetCommentHeart(ctx, oldest, false); err != nil {
		t.Fatal(err)
	}
	ref, err := r.store.GetCommentOwnerRef(ctx, reply)
	if err != nil || ref.PostAuthor != r.owner || ref.ParentID == nil || *ref.ParentID != oldest {
		t.Fatalf("owner ref: %+v %v", ref, err)
	}

	// Inbox: the owner's posts' comments by others; `oldest` is answered
	// (the owner replied after it), `popular` and `newest` are not.
	rows, _, err := r.store.ListCreatorInbox(ctx, postgres.InboxQuery{AuthorID: r.owner, Status: postgres.InboxStatusUnanswered, Sort: postgres.InboxSortNewest, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Comment.ID != newest || rows[1].Comment.ID != popular || rows[0].AuthorReplied {
		t.Fatalf("unanswered inbox: %v", inboxIDs(rows))
	}
	if rows[0].Post.ID != post.ID || rows[0].Post.ContentType != "long_video" || rows[0].Post.Title != post.Title {
		t.Fatalf("inbox post ref: %+v", rows[0].Post)
	}
	all, _, _ := r.store.ListCreatorInbox(ctx, postgres.InboxQuery{AuthorID: r.owner, Status: postgres.InboxStatusAll, Sort: postgres.InboxSortNewest, Limit: 10})
	if len(all) != 3 || all[2].Comment.ID != oldest || !all[2].AuthorReplied {
		t.Fatalf("all inbox: %v replied=%v", inboxIDs(all), all[2].AuthorReplied)
	}
	rel, _, _ := r.store.ListCreatorInbox(ctx, postgres.InboxQuery{AuthorID: r.owner, Status: postgres.InboxStatusAll, Sort: postgres.InboxSortRelevant, Limit: 10})
	if rel[0].Comment.ID != popular || rel[0].Comment.ReactionCount != 3 {
		t.Fatalf("relevant inbox: %v count=%d", inboxIDs(rel), rel[0].Comment.ReactionCount)
	}
	flicksOnly, _, _ := r.store.ListCreatorInbox(ctx, postgres.InboxQuery{AuthorID: r.owner, Status: postgres.InboxStatusAll, ContentTypes: []string{"flick", "reel"}, Limit: 10})
	if len(flicksOnly) != 0 {
		t.Fatalf("content filter ignored: %v", inboxIDs(flicksOnly))
	}
	stranger, _, _ := r.store.ListCreatorInbox(ctx, postgres.InboxQuery{AuthorID: fan, Status: postgres.InboxStatusAll, Limit: 10})
	if len(stranger) != 0 {
		t.Fatalf("a non-author sees an inbox: %v", inboxIDs(stranger))
	}
}

// ── F. edit after publish + audit; tunes ────────────────────────────────────

func TestMTubeEditWritesAuditAndSearchRevisionAndIsOwnerGuarded(t *testing.T) {
	r := newMTubeRig(t)
	ctx := context.Background()
	post := r.newPost(t, r.owner, "long_video", "unlisted", "podcasts")
	t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DELETE FROM post_edit_audit WHERE post_id = $1`, post.ID)
	})
	title := "Renamed"
	vis := "public"
	kids := true
	tags := []string{"a", "b"}
	updated, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{Title: &title, Visibility: &vis, MadeForKids: &kids, Tags: &tags})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Renamed" || updated.Visibility != "public" || !updated.IsMadeForKids || len(updated.Tags) != 2 || updated.Category != "podcasts" {
		t.Fatalf("updated: %+v", updated)
	}
	var changes string
	var action string
	if err := r.pool.QueryRow(ctx, `SELECT action, changes::text FROM post_edit_audit WHERE post_id = $1 ORDER BY created_at DESC LIMIT 1`, post.ID).Scan(&action, &changes); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if action != "post.edit" || !strings.Contains(changes, `"visibility"`) || !strings.Contains(changes, `"unlisted"`) || !strings.Contains(changes, `"title"`) || strings.Contains(changes, `"category"`) {
		t.Fatalf("audit changes: %s %s", action, changes)
	}
	var searchRev int64
	var eligibility int
	_ = r.pool.QueryRow(ctx, `SELECT search_rev FROM posts WHERE id = $1`, post.ID).Scan(&searchRev)
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_outbox_events WHERE aggregate_id = $1 AND event_type = $2`, post.ID, events.PostSearchEligibilityChanged).Scan(&eligibility)
	if searchRev < 1 || eligibility < 1 {
		t.Fatalf("visibility change did not bump search: rev=%d events=%d", searchRev, eligibility)
	}
	// The audit is append-only at the database.
	if _, err := r.pool.Exec(ctx, `DELETE FROM post_edit_audit WHERE post_id = $1`, post.ID); err == nil {
		t.Fatal("post_edit_audit row deleted; the append-only trigger is missing")
	}
	// A no-op patch writes no audit row.
	var before int
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_edit_audit WHERE post_id = $1`, post.ID).Scan(&before)
	if _, err := r.store.UpdatePostFields(ctx, post.ID, r.owner, postgres.PostEditPatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	var after int
	_ = r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM post_edit_audit WHERE post_id = $1`, post.ID).Scan(&after)
	if after != before {
		t.Fatalf("no-op edit wrote an audit row (%d -> %d)", before, after)
	}
	// The store's own guards: not owned, not found.
	if _, err := r.store.UpdatePostFields(ctx, post.ID, uuid.New(), postgres.PostEditPatch{Title: &title}); err != postgres.ErrPostEditNotOwned {
		t.Fatalf("stranger: %v", err)
	}
	if _, err := r.store.UpdatePostFields(ctx, uuid.New(), r.owner, postgres.PostEditPatch{Title: &title}); err != pgx.ErrNoRows {
		t.Fatalf("missing: %v", err)
	}
	// The bulk route writes through UpdatePostFields with its own audit
	// action (2026-09-28; hub_batch_integration_test.go covers the rest).
	other := r.newPost(t, r.owner, "flick", "public", "")
	ghost := uuid.New()
	private := "private"
	for _, id := range []uuid.UUID{post.ID, other.ID} {
		if _, err := r.store.UpdatePostFields(ctx, id, r.owner, postgres.PostEditPatch{Visibility: &private, AuditAction: "post.bulk_edit"}); err != nil {
			t.Fatalf("bulk write %s: %v", id, err)
		}
	}
	if _, err := r.store.UpdatePostFields(ctx, ghost, r.owner, postgres.PostEditPatch{Visibility: &private, AuditAction: "post.bulk_edit"}); err != pgx.ErrNoRows {
		t.Fatalf("ghost: %v", err)
	}
	authors, _ := r.store.PostAuthorsByIDs(ctx, []uuid.UUID{post.ID, ghost})
	if authors[post.ID] != r.owner || len(authors) != 1 {
		t.Fatalf("authors: %v", authors)
	}
	counts, err := r.store.CountCreatorContent(ctx, r.owner)
	if err != nil || counts.Videos != 1 || counts.Shorts != 1 {
		t.Fatalf("creator counts: %+v %v", counts, err)
	}
	if err := r.store.UpdatePostCategory(ctx, post.ID, "kids"); err != nil {
		t.Fatal(err)
	}
	got, _ := r.store.GetPost(ctx, post.ID)
	if got.Category != "kids" {
		t.Fatalf("category=%q", got.Category)
	}

	// Tunes: private, batchable.
	viewer := uuid.New()
	newUser(t, r.pool, viewer)
	if err := r.store.CreateTune(ctx, viewer, post.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.pool.Exec(context.Background(), `DELETE FROM tunes WHERE user_id = $1`, viewer) })
	batch, err := r.store.BatchHasTune(ctx, viewer, []uuid.UUID{post.ID, other.ID})
	if err != nil || !batch[post.ID] || batch[other.ID] {
		t.Fatalf("batch tunes: %v %v", batch, err)
	}
	// Scheduled posts are hidden from the uploads list unless asked for.
	sched := r.newPost(t, r.owner, "long_video", "private", "")
	if _, err := r.pool.Exec(ctx, `UPDATE posts SET publish_at = now() + interval '1 day' WHERE id = $1`, sched.ID); err != nil {
		t.Fatal(err)
	}
	live, _, _ := r.store.GetUploadsByContentTypes(ctx, r.owner, []string{"long_video", "video"}, 10, "")
	withSched, _, _ := r.store.GetUploadsByContentTypesOpts(ctx, r.owner, []string{"long_video", "video"}, 10, "", true)
	if len(withSched) != len(live)+1 {
		t.Fatalf("include_scheduled: live=%d with=%d", len(live), len(withSched))
	}
}

// migrationSQL reads one embedded migration file (the same bytes the
// runner applies), so a test can re-apply an idempotent migration.
func migrationSQL(name string) (string, error) {
	b, err := fs.ReadFile(database.Migrations, "migrations/"+name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func ids(cs []postgres.Comment) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func inboxIDs(rows []postgres.InboxRow) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Comment.ID)
	}
	return out
}
