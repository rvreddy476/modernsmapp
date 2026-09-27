package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	postEvents "github.com/atpost/post-service/internal/events"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// MTube search facets on the post events (2026-09-27). The search lane
// added optional height / has_subtitles to PostCreatedPayload and
// PostSearchEligibilityChangedPayload; post-service stamps height from what
// it already measured and leaves has_subtitles unset, because no caption
// state is stored on this side (media_subtitles is media-service's).

func TestPostCreatedCarriesTheVideoHeightAndNoSubtitleClaim(t *testing.T) {
	svc := &Service{}
	pc := svc.buildPostCreatedPayload(context.Background(),
		&postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "long_video", Visibility: "public", ReviewStatus: "approved"},
		nil, 725, 2160, 1)
	if pc.Height != 2160 || pc.DurationMs != 725_000 {
		t.Fatalf("height=%d duration_ms=%d, want 2160 / 725000", pc.Height, pc.DurationMs)
	}
	if pc.HasSubtitles {
		t.Fatal("has_subtitles stamped although post-service stores no caption state")
	}
	raw, _ := json.Marshal(pc)
	if !strings.Contains(string(raw), `"height":2160`) || strings.Contains(string(raw), "has_subtitles") {
		t.Fatalf("wire shape: %s", raw)
	}

	// Unknown height stays off the wire (omitempty), exactly like an old
	// producer's event.
	none := svc.buildPostCreatedPayload(context.Background(),
		&postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "post", Visibility: "public"}, nil, 0, 0, 1)
	raw, _ = json.Marshal(none)
	if strings.Contains(string(raw), `"height"`) {
		t.Fatalf("an unknown height reached the wire: %s", raw)
	}
}

func TestLiveVODRecordsTheMeasuredHeight(t *testing.T) {
	ctx := context.Background()
	newRig := func() (*fakeLiveVODStore, *Service, LiveVODInput, uuid.UUID) {
		store := newFakeLiveVODStore()
		store.dims = map[uuid.UUID]postgres.MediaMetadata{}
		// A non-nil producer is all CreateLiveVODPost checks before asking
		// for the PostCreated payload; nothing is published here.
		svc := &Service{liveVOD: store, producer: &postEvents.Producer{}}
		stream, creator, media := uuid.New(), uuid.New(), uuid.New()
		store.media[media] = postgres.MediaOwnership{UploaderID: creator, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 90_000}
		store.bySuffix["live-recordings/"+stream.String()+".mp4"] = media
		return store, svc, LiveVODInput{StreamID: stream, CreatorID: creator, RecordingURL: "live-recordings/" + stream.String() + ".mp4", Title: "Friday build"}, media
	}

	t.Run("measured", func(t *testing.T) {
		store, svc, in, media := newRig()
		store.dims[media] = postgres.MediaMetadata{Kind: "video", Width: 1920, Height: 1080}
		if _, err := svc.CreateLiveVODPost(ctx, in); err != nil {
			t.Fatal(err)
		}
		ins := store.inserts[0]
		if ins.VideoMetadata.Height == nil || *ins.VideoMetadata.Height != 1080 || ins.VideoMetadata.Width == nil || *ins.VideoMetadata.Width != 1920 {
			t.Fatalf("video_metadata size not recorded: %+v", ins.VideoMetadata)
		}
		pc, ok := ins.EventPayload.(events.PostCreatedPayload)
		if !ok || ins.EventType != events.PostCreated {
			t.Fatalf("event: %s %T", ins.EventType, ins.EventPayload)
		}
		if pc.Height != 1080 || pc.HasSubtitles {
			t.Fatalf("PostCreated height=%d has_subtitles=%v", pc.Height, pc.HasSubtitles)
		}
	})

	t.Run("unmeasured stays unset", func(t *testing.T) {
		store, svc, in, _ := newRig()
		if _, err := svc.CreateLiveVODPost(ctx, in); err != nil {
			t.Fatal(err)
		}
		ins := store.inserts[0]
		if ins.VideoMetadata.Height != nil || ins.VideoMetadata.Width != nil {
			t.Fatalf("a size nobody measured was recorded: %+v", ins.VideoMetadata)
		}
		if pc := ins.EventPayload.(events.PostCreatedPayload); pc.Height != 0 {
			t.Fatalf("height=%d, want 0", pc.Height)
		}
	})
}
