package claimsync

// The reconcile pass over real live_recording_imports rows (LIVE_V2_TEST_DSN
// on a *_test database, CI only). media-service is the claim-keeping fake.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/mediaclient"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

func TestIntegrationRecordingImportsAreClaimedWhileHeldAndReleasedAfter(t *testing.T) {
	st, _ := itStore(t)
	ctx := context.Background()
	creator := uuid.New()
	mk := func() uuid.UUID {
		id := uuid.New()
		if _, err := st.CreateStream(ctx, postgres.CreateStreamParams{
			ID: id, CreatorUserID: creator, LiveKitRoom: "stream_" + uuid.New().String(),
			Title: "it", Visibility: "public",
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.SetRecording(ctx, id, "https://s3/rec/"+id.String()+".mp4", 60, postgres.RecordingImport{
			Bucket: "live-recordings", ObjectKey: "recordings/" + id.String() + ".mp4", DurationMs: 60000,
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	noEvents := func(*postgres.LiveStream, postgres.RecordingImport) ([]postgres.OutboxEvent, error) { return nil, nil }

	processing, awaitingPost, found, failed := mk(), mk(), mk(), mk()
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if err := st.RetryImport(ctx, processing, &a, "processing", "", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteImport(ctx, awaitingPost, b, noEvents); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteImport(ctx, found, c, noEvents); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetRecordingPost(ctx, found, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := st.TerminateImport(ctx, failed, &d, "rejected", "media asset rejected"); err != nil {
		t.Fatal(err)
	}

	cl := newFakeClaims()
	kind := mediaclient.KindLiveRecordingImport
	// Releases lost earlier: a found post and a failed job still hold theirs.
	cl.held[key(kind, found.String())] = map[uuid.UUID]bool{c: true}
	cl.held[key(kind, failed.String())] = map[uuid.UUID]bool{d: true}

	if rep, err := Run(ctx, NewStoreSource(st, false), cl, true); err != nil || rep.Failed != 0 {
		t.Fatalf("apply: %v %s", err, rep)
	}
	if !cl.holds(kind, processing.String(), a) || !cl.holds(kind, awaitingPost.String(), b) {
		t.Fatalf("the backfill did not claim held recordings: %v", cl.held)
	}
	if len(cl.held[key(kind, found.String())]) != 0 || len(cl.held[key(kind, failed.String())]) != 0 {
		t.Fatalf("settled imports still hold their recordings: %v", cl.held)
	}

	ids, err := st.StreamIDsWithImportMedia(ctx, creator)
	if err != nil || len(ids) != 4 {
		t.Fatalf("the purge list must name every import with a media id: %v %v", ids, err)
	}
}
