package consumers

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Live -> video consumer (2026-09-27): the payload decode and the retry
// decision, without Kafka.

func TestDecodeLiveVODEvent(t *testing.T) {
	stream, creator, media := uuid.New(), uuid.New(), uuid.New()
	in, ok := DecodeLiveVODEvent([]byte(`{"stream_id":"` + stream.String() + `","creator_id":"` + creator.String() +
		`","recording_url":" https://cdn/live-recordings/x.mp4 ","duration_sec":120,"media_asset_id":"` + media.String() + `","title":" Friday "}`))
	if !ok {
		t.Fatal("valid payload not ok")
	}
	if in.StreamID != stream || in.CreatorID != creator || in.RecordingURL != "https://cdn/live-recordings/x.mp4" ||
		in.DurationSec != 120 || in.MediaAssetID == nil || *in.MediaAssetID != media || in.Title != "Friday" {
		t.Fatalf("decoded: %+v", in)
	}
	// The shared payload alone (no extensions) is enough.
	in, ok = DecodeLiveVODEvent([]byte(`{"stream_id":"` + stream.String() + `","creator_id":"` + creator.String() + `","recording_url":"k","duration_sec":1}`))
	if !ok || in.MediaAssetID != nil || in.Title != "" {
		t.Fatalf("shared payload: ok=%v %+v", ok, in)
	}
	for _, bad := range []string{`{`, `{}`, `{"stream_id":"x","creator_id":"` + creator.String() + `"}`, `{"stream_id":"` + stream.String() + `"}`} {
		if _, ok := DecodeLiveVODEvent([]byte(bad)); ok {
			t.Errorf("%s decoded as ok", bad)
		}
	}
}

type fakeVODCreator struct {
	out   *service.LiveVODOutcome
	err   error
	calls int
}

func (f *fakeVODCreator) CreateLiveVODPost(_ context.Context, _ service.LiveVODInput) (*service.LiveVODOutcome, error) {
	f.calls++
	return f.out, f.err
}

func TestHandleLiveVODRetriesOnlyStoreErrors(t *testing.T) {
	ctx := context.Background()
	in := service.LiveVODInput{StreamID: uuid.New(), CreatorID: uuid.New()}
	boom := errors.New("db down")
	if err := handleLiveVOD(ctx, &fakeVODCreator{err: boom}, in); !errors.Is(err, boom) {
		t.Fatalf("store error must be returned for retry: %v", err)
	}
	if err := handleLiveVOD(ctx, &fakeVODCreator{out: &service.LiveVODOutcome{Skipped: "not ready"}}, in); err != nil {
		t.Fatalf("a skip must not be retried: %v", err)
	}
	if err := handleLiveVOD(ctx, &fakeVODCreator{out: &service.LiveVODOutcome{Post: &postgres.Post{ID: uuid.New()}, Created: true}}, in); err != nil {
		t.Fatalf("created: %v", err)
	}
	if err := handleLiveVOD(ctx, &fakeVODCreator{out: &service.LiveVODOutcome{Post: &postgres.Post{ID: uuid.New()}}}, in); err != nil {
		t.Fatalf("existing: %v", err)
	}
}
