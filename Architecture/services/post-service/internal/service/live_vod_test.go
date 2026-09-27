package service

import (
	"context"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Live -> video (2026-09-27): the decision path behind the vod_ready
// consumer, with an in-memory store. The idempotency lock and the real
// INSERT are the integration test's.

type fakeLiveVODStore struct {
	byStream map[uuid.UUID]*postgres.Post
	media    map[uuid.UUID]postgres.MediaOwnership
	bySuffix map[string]uuid.UUID
	dims     map[uuid.UUID]postgres.MediaMetadata
	inserts  []postgres.LiveVODInsert
}

func newFakeLiveVODStore() *fakeLiveVODStore {
	return &fakeLiveVODStore{byStream: map[uuid.UUID]*postgres.Post{}, media: map[uuid.UUID]postgres.MediaOwnership{}, bySuffix: map[string]uuid.UUID{}}
}

func (f *fakeLiveVODStore) GetPostByLiveStream(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	return f.byStream[id], nil
}

func (f *fakeLiveVODStore) BatchGetMediaOwnership(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaOwnership, error) {
	out := map[uuid.UUID]postgres.MediaOwnership{}
	for _, id := range ids {
		if m, ok := f.media[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func (f *fakeLiveVODStore) BatchGetMediaMetadata(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.MediaMetadata, error) {
	out := map[uuid.UUID]postgres.MediaMetadata{}
	for _, id := range ids {
		if m, ok := f.dims[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func (f *fakeLiveVODStore) FindMediaByStorageKeySuffix(_ context.Context, suffix string) (uuid.UUID, error) {
	return f.bySuffix[suffix], nil
}

func (f *fakeLiveVODStore) CreateLiveVODPost(_ context.Context, in postgres.LiveVODInsert) (*postgres.Post, bool, error) {
	if p, ok := f.byStream[in.StreamID]; ok {
		return p, false, nil
	}
	f.inserts = append(f.inserts, in)
	f.byStream[in.StreamID] = in.Post
	return in.Post, true, nil
}

func TestCreateLiveVODPostMakesOneUnlistedLongVideo(t *testing.T) {
	store := newFakeLiveVODStore()
	svc := &Service{liveVOD: store}
	ctx := context.Background()
	stream, creator, media := uuid.New(), uuid.New(), uuid.New()
	store.media[media] = postgres.MediaOwnership{UploaderID: creator, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "passed", DurationMs: 90_000}
	store.bySuffix["live-recordings/"+stream.String()+".mp4"] = media

	in := LiveVODInput{StreamID: stream, CreatorID: creator, RecordingURL: "http://minio:9000/bucket/live-recordings/" + stream.String() + ".mp4", Title: "Friday build"}
	out, err := svc.CreateLiveVODPost(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Created || out.Skipped != "" || out.Post == nil {
		t.Fatalf("first delivery: %+v", out)
	}
	p := out.Post
	if p.ContentType != "long_video" || p.Visibility != "unlisted" || p.AuthorID != creator || p.Title != "Friday build" || !p.ContentTypeExplicit {
		t.Fatalf("post: %+v", p)
	}
	if p.ReviewStatus != "approved" {
		t.Fatalf("review_status=%q want approved for a passed asset", p.ReviewStatus)
	}
	ins := store.inserts[0]
	if ins.StreamID != stream || ins.MediaID != media || ins.VideoMetadata == nil || ins.VideoMetadata.DurationSeconds != 90 || ins.VideoMetadata.FinalCategory != "long_video" {
		t.Fatalf("insert: %+v", ins)
	}
	// No producer wired: no event type asked for.
	if ins.EventType != "" {
		t.Fatalf("event without a producer: %q", ins.EventType)
	}

	// Redelivery: same post, nothing created.
	again, err := svc.CreateLiveVODPost(ctx, in)
	if err != nil || again.Created || again.Post.ID != p.ID {
		t.Fatalf("redelivery: %+v %v", again, err)
	}
	if len(store.inserts) != 1 {
		t.Fatalf("inserts=%d want 1", len(store.inserts))
	}
}

func TestCreateLiveVODPostSkipsWhatCannotBecomeAVideo(t *testing.T) {
	store := newFakeLiveVODStore()
	svc := &Service{liveVOD: store}
	ctx := context.Background()
	creator := uuid.New()
	processing, image := uuid.New(), uuid.New()
	store.media[processing] = postgres.MediaOwnership{UploaderID: creator, Kind: "video", ProcessingStatus: "processing", ModerationStatus: "pending"}
	store.media[image] = postgres.MediaOwnership{UploaderID: creator, Kind: "image", ProcessingStatus: "ready", ModerationStatus: "passed"}

	cases := []struct {
		name string
		in   LiveVODInput
	}{
		{"no ids", LiveVODInput{}},
		{"unregistered recording", LiveVODInput{StreamID: uuid.New(), CreatorID: creator, RecordingURL: "https://cdn/live-recordings/x.mp4"}},
		{"media not ready", LiveVODInput{StreamID: uuid.New(), CreatorID: creator, MediaAssetID: &processing}},
		{"media not a video", LiveVODInput{StreamID: uuid.New(), CreatorID: creator, MediaAssetID: &image}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := svc.CreateLiveVODPost(ctx, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if out.Skipped == "" || out.Created || out.Post != nil {
				t.Fatalf("want skipped, got %+v", out)
			}
		})
	}
	if len(store.inserts) != 0 {
		t.Fatalf("a skipped event inserted: %+v", store.inserts)
	}
	// Pending moderation is NOT a skip: the post is made and held.
	pending := uuid.New()
	store.media[pending] = postgres.MediaOwnership{UploaderID: creator, Kind: "video", ProcessingStatus: "ready", ModerationStatus: "pending"}
	out, err := svc.CreateLiveVODPost(ctx, LiveVODInput{StreamID: uuid.New(), CreatorID: creator, MediaAssetID: &pending})
	if err != nil || !out.Created || out.Post.ReviewStatus != "pending" {
		t.Fatalf("pending moderation: %+v %v", out, err)
	}
	if out.Post.Title == "" {
		t.Fatal("a titleless stream still gets a title")
	}
}

func TestRecordingURLHelpers(t *testing.T) {
	id := uuid.New()
	if got := MediaIDFromRecordingURL("https://gw/v1/media/" + id.String() + "/hls/master.m3u8"); got != id {
		t.Fatalf("media id from url = %s", got)
	}
	if got := MediaIDFromRecordingURL("https://cdn/live-recordings/" + id.String() + ".mp4"); got != uuid.Nil {
		t.Fatalf("a bare file name is not a media id: %s", got)
	}
	cases := map[string]string{
		"https://cdn.example/live-recordings/abc.mp4":         "live-recordings/abc.mp4",
		"http://minio:9000/bucket/live-recordings/abc.mp4":    "live-recordings/abc.mp4",
		"live-recordings/abc.mp4":                             "live-recordings/abc.mp4",
		"/live-recordings/abc.mp4":                            "live-recordings/abc.mp4",
		"https://cdn.example/deep/er/live-recordings/abc.mp4": "live-recordings/abc.mp4",
		"":                     "",
		"https://cdn.example/": "",
	}
	for in, want := range cases {
		if got := RecordingObjectKey(in); got != want {
			t.Errorf("RecordingObjectKey(%q)=%q want %q", in, got, want)
		}
	}
}
