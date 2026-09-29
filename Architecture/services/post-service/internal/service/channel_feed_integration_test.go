//go:build integration

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The channel feed end to end against a real Postgres (2026-09-29): the
// production wiring (New), the real store predicate, the hidden-author
// list, and a fake media-service record source.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run ChannelFeedAgainstPostgres -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).
func TestChannelFeedAgainstPostgres(t *testing.T) {
	f := newAnonMediaFixture(t)
	ctx := context.Background()
	handle := "feed." + uuid.NewString()[:8]
	f.set(t, `INSERT INTO channels (user_id, name, handle, description, contact_email) VALUES ($1, 'Feed Proof', $2, 'About the feed', 'hello@example.com')`, f.author, handle)
	f.set(t, `UPDATE posts SET title = 'Episode one', category = 'podcasts', language = 'en', published_at = NOW() WHERE id = $1`, f.post)
	unlisted := uuid.New()
	f.set(t, `INSERT INTO posts (id, author_id, text, title, visibility, content_type, review_status, created_at, updated_at)
		VALUES ($1, $2, 'link only', 'Link only', 'unlisted', 'long_video', 'approved', NOW(), NOW())`, unlisted, f.author)
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = f.pool.Exec(bg, `DELETE FROM posts WHERE id = $1`, unlisted)
		_, _ = f.pool.Exec(bg, `DELETE FROM channels WHERE user_id = $1`, f.author)
	})
	media := newFakeFeedMedia()
	media.records[f.media] = readyRecord(FeedMediaVariant{Name: "720p", Mime: "video/mp4", SizeBytes: sizep(7200), ObjectKey: "k/720p"})
	f.svc.feedMedia = media

	for _, ref := range []string{handle, "@" + handle, f.author.String()} {
		feed, err := f.svc.ChannelFeed(ctx, ref, "", 0)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		if feed.Channel.UserID != f.author || feed.Channel.Handle != handle || feed.Channel.About != "About the feed" ||
			feed.Channel.ContactEmail != "hello@example.com" || feed.Channel.Language != "en" || feed.Channel.DominantCategory != "podcasts" {
			t.Fatalf("%s: channel = %+v", ref, feed.Channel)
		}
		if len(feed.Items) != 1 || feed.Items[0].ID != f.post || feed.Items[0].MediaID != f.media ||
			feed.Items[0].Title != "Episode one" || feed.Items[0].Enclosure.Path != "/v1/media/"+f.media.String()+"/serve/720p" {
			t.Fatalf("%s: items = %+v", ref, feed.Items)
		}
	}
	if narrowed, err := f.svc.ChannelFeed(ctx, handle, "documentary", 0); err != nil || len(narrowed.Items) != 0 {
		t.Fatalf("category with no videos: %+v %v", narrowed, err)
	}

	// A hidden owner is no channel at all.
	f.set(t, `INSERT INTO post_hidden_authors (user_id, reason) VALUES ($1, 'deactivated') ON CONFLICT DO NOTHING`, f.author)
	// The account gate caches an answer for 3 s; a new service asks again.
	hidden := New(f.svc.pgStore, nil, nil)
	hidden.feedMedia = media
	if _, err := hidden.ChannelFeed(ctx, handle, "", 0); !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("hidden owner: %v, want ErrChannelNotFound", err)
	}
}
