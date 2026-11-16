package service

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// seedPart puts one part row straight into the fake store.
func seedPart(r *rig, streamID uuid.UUID, part int, state string, d time.Duration) string {
	key := recordingPartKey(streamID, part)
	if r.store.Parts == nil {
		r.store.Parts = map[uuid.UUID][]*storetest.PartRow{}
	}
	r.store.Parts[streamID] = append(r.store.Parts[streamID], &storetest.PartRow{
		RecordingPart: postgres.RecordingPart{
			StreamID: streamID, Part: part, ObjectKey: key, State: state, DurationMs: d.Milliseconds(),
		},
		CreatedAt: r.clock.Now(),
	})
	return key
}

// importCall queues the stream's import, runs the import sweep once against
// a fake media-service that answers processing, and returns the request.
func importCall(t *testing.T, r *rig, streamID uuid.UUID) ImportRecordingRequest {
	t.Helper()
	if err := r.svc.queueImport(ctx, streamID); err != nil {
		t.Fatal(err)
	}
	if r.store.Imports[streamID] == nil {
		t.Fatal("no import queued")
	}
	imp := &fakeImporter{fn: func() (ImportResult, error) {
		return ImportResult{MediaID: uuid.New(), ProcessingStatus: "processing"}, nil
	}}
	r.svc.media = imp
	r.svc.RunImports(ctx)
	if len(imp.calls) != 1 {
		t.Fatalf("import calls = %d", len(imp.calls))
	}
	return imp.calls[0]
}

func endedStream(r *rig) *postgres.LiveStream {
	st := r.store.AddStreamStatus(uuid.New(), postgres.StatusEnded)
	now := r.clock.Now()
	r.store.Streams[st.ID].EndedAt = &now
	return st
}

// TestImportRequestListsCompletePartsInOrder: every complete part goes to
// media-service in part order (seeded out of order); failed parts are left
// out; key is the longest part; the duration is the sum.
func TestImportRequestListsCompletePartsInOrder(t *testing.T) {
	r := newRig()
	r.svc.s3Bucket = "live-recordings"
	st := endedStream(r)
	k3 := seedPart(r, st.ID, 3, postgres.PartComplete, 30*time.Second)
	k1 := seedPart(r, st.ID, 1, postgres.PartComplete, 60*time.Second)
	seedPart(r, st.ID, 2, postgres.PartFailed, 0)
	k4 := seedPart(r, st.ID, 4, postgres.PartComplete, 90*time.Second)
	seedPart(r, st.ID, 5, postgres.PartFailed, 5*time.Second)

	req := importCall(t, r, st.ID)
	if !equalStrings(req.Keys, []string{k1, k3, k4}) {
		t.Fatalf("keys = %v, want parts 1, 3, 4 in order", req.Keys)
	}
	if req.Key != k4 {
		t.Fatalf("key = %q, want the longest part %q", req.Key, k4)
	}
	if req.DurationMs != (180 * time.Second).Milliseconds() {
		t.Fatalf("duration = %d, want the sum of the complete parts", req.DurationMs)
	}
	if req.Bucket != "live-recordings" || req.SourceRef != st.ID.String() || req.Source != "live_recording" {
		t.Fatalf("request = %+v", req)
	}
}

// TestImportIgnoresStartingParts: a part still starting holds the import
// back; once the end is older than importWaitActive the import goes ahead
// without it.
func TestImportIgnoresStartingParts(t *testing.T) {
	r := newRig()
	st := endedStream(r)
	k1 := seedPart(r, st.ID, 1, postgres.PartComplete, time.Minute)
	k2 := seedPart(r, st.ID, 2, postgres.PartComplete, time.Minute)
	seedPart(r, st.ID, 3, postgres.PartStarting, 0)

	if err := r.svc.queueImport(ctx, st.ID); err != nil {
		t.Fatal(err)
	}
	if r.store.Imports[st.ID] != nil {
		t.Fatal("queued while a part was starting")
	}
	r.clock.Advance(importWaitActive + time.Minute)
	req := importCall(t, r, st.ID)
	if !equalStrings(req.Keys, []string{k1, k2}) {
		t.Fatalf("keys = %v, want the two complete parts only", req.Keys)
	}
}

// TestImportOnePartSendsNoKeys: one complete part (the rest failed) is the
// unchanged single-key request — no keys, no join.
func TestImportOnePartSendsNoKeys(t *testing.T) {
	r := newRig()
	st := endedStream(r)
	k1 := seedPart(r, st.ID, 1, postgres.PartComplete, time.Minute)
	seedPart(r, st.ID, 2, postgres.PartFailed, 0)

	req := importCall(t, r, st.ID)
	if req.Keys != nil || req.Key != k1 || req.DurationMs != time.Minute.Milliseconds() {
		t.Fatalf("request = %+v, want the single key", req)
	}
}

// TestPlanPartsImport: the pure plan — order, filter, longest (ties: the
// earliest), sum, and nothing complete.
func TestPlanPartsImport(t *testing.T) {
	p := func(n int, state string, ms int64) postgres.RecordingPart {
		return postgres.RecordingPart{Part: n, ObjectKey: "k" + string(rune('0'+n)), State: state, DurationMs: ms}
	}
	plan, ok := postgres.PlanPartsImport([]postgres.RecordingPart{
		p(2, postgres.PartComplete, 50), p(1, postgres.PartComplete, 50),
		p(3, postgres.PartRecording, 999), p(4, postgres.PartStarting, 999),
	})
	if !ok || !equalStrings(plan.Keys, []string{"k1", "k2"}) || plan.Longest.Part != 1 || plan.DurationMs != 100 {
		t.Fatalf("plan = %+v ok=%v", plan, ok)
	}
	if _, ok := postgres.PlanPartsImport([]postgres.RecordingPart{p(1, postgres.PartFailed, 10)}); ok {
		t.Fatal("planned an import with no complete part")
	}
}
