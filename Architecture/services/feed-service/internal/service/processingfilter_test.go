package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// Instant publish: a processing post reaches its author and nobody else,
// on every surface, even from the hydration cache.

func TestProcessingFilter_DropsForNonAuthorsKeepsForAuthor(t *testing.T) {
	author := uuid.New()
	viewer := uuid.New()
	processingID := uuid.New()
	publishedID := uuid.New()

	page := func() []HydratedPost {
		return []HydratedPost{
			{ID: processingID, AuthorID: author, IsProcessing: true},
			{ID: publishedID, AuthorID: author, IsProcessing: false},
		}
	}

	// A follower: the processing reel is dropped, the published one stays.
	got := applyProcessingFilter(viewer, page())
	if len(got) != 1 || got[0].ID != publishedID {
		t.Fatalf("non-author must not receive a processing post; got %+v", got)
	}

	// The author: both stay, in order, with the flag intact so the client
	// can render "improving quality".
	got = applyProcessingFilter(author, page())
	if len(got) != 2 || got[0].ID != processingID || !got[0].IsProcessing {
		t.Fatalf("author must keep their own processing post; got %+v", got)
	}
}

func TestProcessingFilter_RepostOfProcessingPostIsJudgedByOriginalAuthor(t *testing.T) {
	author := uuid.New()
	reposter := uuid.New()
	item := HydratedPost{ID: uuid.New(), AuthorID: author, IsProcessing: true, IsRepost: true, RepostedBy: &reposter}

	if got := applyProcessingFilter(reposter, []HydratedPost{item}); len(got) != 0 {
		t.Fatal("a repost of a processing post must not surface it to the reposter")
	}
	if got := applyProcessingFilter(author, []HydratedPost{item}); len(got) != 1 {
		t.Fatal("the original author still sees it")
	}
}

// is_processing, the per-media pipeline state and the playback URL are
// decoded from post-service / media-service by field name; a missing
// declaration would silently drop them between the services and Android.
func TestHydratedPostCarriesProcessingAndPlaybackFields(t *testing.T) {
	raw := `{"id":"` + uuid.New().String() + `","author_id":"` + uuid.New().String() + `",` +
		`"is_processing":true,"media":[{"media_id":"` + uuid.New().String() + `","kind":"video",` +
		`"processing_status":"processing","moderation_status":"pending","position":0}]}`
	var hp HydratedPost
	if err := json.Unmarshal([]byte(raw), &hp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !hp.IsProcessing {
		t.Fatal("is_processing dropped on decode")
	}
	if hp.Media[0].ProcessingStatus != "processing" || hp.Media[0].ModerationStatus != "pending" {
		t.Fatalf("per-media pipeline state dropped: %+v", hp.Media[0])
	}

	hp.Media[0].PlaybackURL = "https://signed/original.mp4"
	hp.Media[0].PlaybackKind = "original"
	out, err := json.Marshal(hp)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var item map[string]any
	if err := json.Unmarshal(out, &item); err != nil {
		t.Fatalf("decode feed item: %v", err)
	}
	if item["is_processing"] != true {
		t.Fatalf("is_processing must be emitted, got %v", item["is_processing"])
	}
	media := item["media"].([]any)[0].(map[string]any)
	for key, want := range map[string]string{
		"processing_status": "processing",
		"moderation_status": "pending",
		"playback_url":      "https://signed/original.mp4",
		"playback_kind":     "original",
	} {
		if media[key] != want {
			t.Errorf("media.%s = %v, want %q", key, media[key], want)
		}
	}

	// A delivery DTO from media-service decodes the same two fields.
	var d mediaDelivery
	if err := json.Unmarshal([]byte(`{"media_id":"`+uuid.New().String()+`","kind":"video","status":"processing","playback_url":"u","playback_kind":"original"}`), &d); err != nil {
		t.Fatalf("decode delivery: %v", err)
	}
	if d.PlaybackURL != "u" || d.PlaybackKind != "original" {
		t.Fatalf("delivery playback fields dropped: %+v", d)
	}
}
