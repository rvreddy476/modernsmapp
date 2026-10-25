//go:build integration

package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// A saved draft publishes FROM the draft row (2026-10-21). The upload
// studio patches the kind it measured (content_type) and the fields below
// onto the draft, then publishes it; every one of them used to be dropped
// on the way, so a long video saved as a draft came out as a flick.
// Migration 069 is under test.
//
//	POSTGRES_DSN=postgres://…/post_it_test go test -tags integration ./internal/service/ -run ReelDraftKindIT -v
//
// Refuses any database whose name does not end in _test (openHubMediaDB).

// studioDraftPatch is what saveDraftWithCover sends for a long video,
// decoded the way the PATCH handler decodes it.
const studioDraftPatch = `{
  "title": "A long talk",
  "caption": "the whole talk #golang",
  "hashtags": ["golang", "talks"],
  "content_type": "long_video",
  "seo_title": "Long talk, searchable",
  "recording_date": "2026-10-01",
  "recording_location": "Hyderabad"
}`

func reelDraftKindService(t *testing.T, f *soundsITFixture) *Service {
	t.Helper()
	// CreatePost touches the cache after the insert; a client that cannot
	// connect fails those calls without reaching any Redis.
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	svc := New(f.store, nil, rdb)
	svc.WithStoryAudience(NewStoryAudience(noRelationships{}))
	// A draft's source asset is claimed with media-service before the row
	// names it (media_claims.go); media-service is faked here.
	svc.SetMediaClaims(newFakeClaims())
	// A long video is listed under a channel.
	f.set(t, `INSERT INTO channels (user_id, name, handle) VALUES ($1, 'Draft Proof', $2) ON CONFLICT DO NOTHING`,
		f.stranger, "draft."+uuid.NewString()[:8])
	t.Cleanup(func() { _, _ = f.pool.Exec(context.Background(), `DELETE FROM channels WHERE user_id = $1`, f.stranger) })
	return svc
}

func reelDraftPublishers(svc *Service, author uuid.UUID) map[string]func(ctx context.Context, id uuid.UUID) (*postgres.Post, error) {
	return map[string]func(ctx context.Context, id uuid.UUID) (*postgres.Post, error){
		"published now": func(ctx context.Context, id uuid.UUID) (*postgres.Post, error) {
			return svc.PublishDraft(ctx, id, author, nil)
		},
		"published by the schedule worker": func(ctx context.Context, id uuid.UUID) (*postgres.Post, error) {
			return svc.publishClaimedReelDraft(ctx, id, author)
		},
	}
}

func TestReelDraftKindITLongVideoDraftStaysALongVideo(t *testing.T) {
	f := newSoundsITFixture(t)
	svc := reelDraftKindService(t, f)
	ctx := context.Background()

	for name, publish := range reelDraftPublishers(svc, f.stranger) {
		media := f.video(t, f.stranger)
		d, err := svc.CreateDraft(ctx, &CreateDraftInput{AuthorID: f.stranger, MediaID: &media, Visibility: "public"})
		if err != nil {
			t.Fatal(err)
		}
		f.posts = append(f.posts, d.ID)
		var patch UpdateDraftInput
		if err := json.Unmarshal([]byte(studioDraftPatch), &patch); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.UpdateDraft(ctx, d.ID, f.stranger, &patch); err != nil {
			t.Fatalf("%s: patch: %v", name, err)
		}

		post, err := publish(ctx, d.ID)
		if err != nil || post == nil {
			t.Fatalf("%s: post=%v err=%v", name, post, err)
		}
		stored, err := f.store.GetPost(ctx, post.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.ContentType != "long_video" {
			t.Errorf("%s: content_type = %q, want long_video", name, stored.ContentType)
		}
		if stored.SEOTitle != "Long talk, searchable" {
			t.Errorf("%s: seo_title = %q", name, stored.SEOTitle)
		}
		if stored.RecordingDate == nil || stored.RecordingDate.Format("2006-01-02") != "2026-10-01" || stored.RecordingLocation != "Hyderabad" {
			t.Errorf("%s: recording = %v / %q", name, stored.RecordingDate, stored.RecordingLocation)
		}
		if tags := map[string]bool{}; true {
			for _, h := range stored.Hashtags {
				tags[h] = true
			}
			if !tags["golang"] || !tags["talks"] {
				t.Errorf("%s: hashtags = %v, want golang and talks", name, stored.Hashtags)
			}
		}
	}
}

// A draft that never said its kind (every draft from before migration 069)
// is classified from the measured video, and stays a flick while the video
// is not measured — what every legacy draft published as.
func TestReelDraftKindITUnsaidKindIsMeasured(t *testing.T) {
	f := newSoundsITFixture(t)
	svc := reelDraftKindService(t, f)
	ctx := context.Background()

	for _, c := range []struct {
		name          string
		duration      *int
		width, height int
		want          string
	}{
		{"unmeasured", nil, 0, 0, "flick"},
		{"short portrait", intp(40), 1080, 1920, "flick"},
		{"ten-minute landscape", intp(600), 1920, 1080, "long_video"},
	} {
		media := f.video(t, f.stranger)
		f.set(t, `UPDATE media_assets SET duration_seconds = $2, width = $3, height = $4 WHERE id = $1`,
			media, c.duration, c.width, c.height)
		d, err := svc.CreateDraft(ctx, &CreateDraftInput{AuthorID: f.stranger, MediaID: &media, Visibility: "public"})
		if err != nil {
			t.Fatal(err)
		}
		f.posts = append(f.posts, d.ID)
		title := "Unsaid " + c.name
		if _, err := svc.UpdateDraft(ctx, d.ID, f.stranger, &UpdateDraftInput{Title: &title}); err != nil {
			t.Fatal(err)
		}
		post, err := svc.PublishDraft(ctx, d.ID, f.stranger, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if post.ContentType != c.want {
			t.Errorf("%s: content_type = %q, want %q", c.name, post.ContentType, c.want)
		}
	}
}

// The column holds a reel or a long video and nothing else; a patch with
// any other kind is refused and writes nothing.
func TestReelDraftKindITMigration069(t *testing.T) {
	f := newSoundsITFixture(t)
	svc := reelDraftKindService(t, f)
	ctx := context.Background()
	media := f.video(t, f.stranger)
	d, err := svc.CreateDraft(ctx, &CreateDraftInput{AuthorID: f.stranger, MediaID: &media, Visibility: "public"})
	if err != nil {
		t.Fatal(err)
	}
	f.posts = append(f.posts, d.ID)
	if got, _ := f.store.GetDraft(ctx, d.ID); got == nil || got.ContentType != nil {
		t.Fatalf("a new draft's content_type = %v, want NULL", got)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE reel_drafts SET content_type = 'reel' WHERE id = $1`, d.ID); err == nil {
		t.Fatal("chk_reel_drafts_content_type let 'reel' in")
	}
	bad, caption := "post", "must not be written"
	if _, err := svc.UpdateDraft(ctx, d.ID, f.stranger, &UpdateDraftInput{ContentType: &bad, Caption: &caption}); err == nil {
		t.Fatal("content_type post was accepted")
	}
	if got, _ := f.store.GetDraft(ctx, d.ID); got == nil || got.ContentType != nil || got.Caption == caption {
		t.Fatalf("a refused patch wrote: %+v", got)
	}
}

func intp(v int) *int { return &v }
