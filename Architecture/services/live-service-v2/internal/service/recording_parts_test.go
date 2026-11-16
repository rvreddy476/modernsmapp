package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// partEgressEnded is LiveKit's egress_ended for egress id, which wrote key.
func partEgressEnded(st *postgres.LiveStream, egressID, key, status string, d time.Duration) WebhookEvent {
	return WebhookEvent{ID: uuid.NewString(), Event: "egress_ended", Egress: &EgressResult{
		EgressID: egressID, RoomName: st.LiveKitRoom, Status: status,
		Location: "https://s3/live-recordings/" + key, Filename: key, DurationNs: int64(d),
	}}
}

// encoderPublishing makes LiveKit list the stream's encoder as publishing.
func encoderPublishing(r *rig, st *postgres.LiveStream, on bool) {
	r.lk.mu.Lock()
	defer r.lk.mu.Unlock()
	if !on {
		delete(r.lk.rooms, st.LiveKitRoom)
		return
	}
	r.lk.rooms[st.LiveKitRoom] = []livekit.Participant{{Identity: EncoderIdentity(st.ID), Tracks: []livekit.ParticipantTrack{{Sid: "TR_v"}}}}
}

func mustWebhook(t *testing.T, r *rig, ev WebhookEvent) {
	t.Helper()
	if err := r.svc.HandleWebhook(ctx, ev); err != nil {
		t.Fatal(err)
	}
}

func partStates(r *rig, id uuid.UUID) []string {
	var out []string
	for _, p := range r.store.RecordingParts(id) {
		out = append(out, p.State)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestEncoderReconnectIntoNewRoomRecordsAgain: the encoder drops, LiveKit
// closes the room (its egress ends with it), the encoder reconnects into a
// new room: a second egress records the rest under its own key; the import
// waits for the end and lists both parts in order.
func TestEncoderReconnectIntoNewRoomRecordsAgain(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	r.svc.s3Bucket = "live-recordings"
	st := goLiveWithEncoder(t, r, host)
	key1 := "recordings/" + st.ID.String() + ".mp4"
	key2 := "recordings/" + st.ID.String() + "_part2.mp4"
	if r.lk.egressStarts != 1 || r.lk.egressKeys[0] != key1 {
		t.Fatalf("first egress: starts=%d keys=%v", r.lk.egressStarts, r.lk.egressKeys)
	}

	// The encoder drops; the room closes; its egress ends with part 1.
	mustWebhook(t, r, encoderEvent(st, "participant_left"))
	mustStatus(t, r, st.ID, stReconnecting, "")
	mustWebhook(t, r, WebhookEvent{ID: uuid.NewString(), Event: "room_finished", Room: st.LiveKitRoom})
	mustWebhook(t, r, partEgressEnded(st, "EG_1", key1, "EGRESS_COMPLETE", 2*time.Minute))
	if r.lk.egressStarts != 1 {
		t.Fatalf("an egress started while the encoder was away: %d", r.lk.egressStarts)
	}
	if len(r.store.Imports) != 0 {
		t.Fatal("the import was queued while the stream was still on air")
	}

	// The encoder is back, in a new room under the same name.
	mustWebhook(t, r, encoderEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stLive, "")
	if r.lk.egressStarts != 2 || r.lk.egressKeys[1] != key2 {
		t.Fatalf("no second egress after the reconnect: starts=%d keys=%v", r.lk.egressStarts, r.lk.egressKeys)
	}
	if got := partStates(r, st.ID); !equalStrings(got, []string{postgres.PartComplete, postgres.PartRecording}) {
		t.Fatalf("parts = %v", got)
	}

	// The host ends: the recording egress is stopped; the import waits for it.
	if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	if n := len(r.lk.egressStops); n == 0 || r.lk.egressStops[n-1] != "EG_2" {
		t.Fatalf("egress stops = %v", r.lk.egressStops)
	}
	if len(r.store.Imports) != 0 {
		t.Fatal("the import was queued before the last part finished")
	}
	mustWebhook(t, r, partEgressEnded(st, "EG_2", key2, "EGRESS_COMPLETE", 40*time.Minute))
	j := r.store.Imports[st.ID]
	if j == nil || j.ObjectKey != key2 || j.DurationMs != (42*time.Minute).Milliseconds() || j.Bucket != "live-recordings" ||
		!equalStrings(j.ObjectKeys, []string{key1, key2}) {
		t.Fatalf("import = %+v, want both parts (longest as the fallback key)", j)
	}
	if r.lk.egressStarts != 2 {
		t.Fatalf("egress started after the end: %d", r.lk.egressStarts)
	}
}

// TestLateEgressEndedAfterReconnectRecordsAgain: the old room's egress_ended
// arrives after the encoder is already back and live. It starts the next
// part only while LiveKit shows the encoder publishing.
func TestLateEgressEndedAfterReconnectRecordsAgain(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := goLiveWithEncoder(t, r, host)
	key1 := recordingPartKey(st.ID, 1)
	mustWebhook(t, r, encoderEvent(st, "participant_left"))
	mustWebhook(t, r, encoderEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stLive, "")
	if r.lk.egressStarts != 1 {
		t.Fatalf("part 1 still records, yet starts = %d", r.lk.egressStarts)
	}
	encoderPublishing(r, st, true)
	mustWebhook(t, r, partEgressEnded(st, "EG_1", key1, "EGRESS_COMPLETE", time.Minute))
	if r.lk.egressStarts != 2 {
		t.Fatalf("late egress_ended did not start part 2: starts = %d", r.lk.egressStarts)
	}

	// The same with nobody publishing (e.g. the room was just deleted by an
	// admin stop that has not committed yet): nothing starts.
	r2 := newRig(host)
	st2 := goLiveWithEncoder(t, r2, host)
	mustWebhook(t, r2, partEgressEnded(st2, "EG_1", recordingPartKey(st2.ID, 1), "EGRESS_COMPLETE", time.Minute))
	if r2.lk.egressStarts != 1 {
		t.Fatalf("an egress started with no host publishing: %d", r2.lk.egressStarts)
	}
	// The sweeper's reconcile records a publishing host that has no part.
	encoderPublishing(r2, st2, true)
	if err := r2.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if r2.lk.egressStarts != 2 {
		t.Fatalf("reconcile did not start the next part: %d", r2.lk.egressStarts)
	}
	if err := r2.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if r2.lk.egressStarts != 2 {
		t.Fatalf("reconcile started a part while one records: %d", r2.lk.egressStarts)
	}
}

// TestDuplicateEgressEndedIsIdempotent: the same event redelivered, and the
// same egress reported again under a new event id, change nothing.
func TestDuplicateEgressEndedIsIdempotent(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := goLiveWithEncoder(t, r, host)
	encoderPublishing(r, st, true)
	ev := partEgressEnded(st, "EG_1", recordingPartKey(st.ID, 1), "EGRESS_COMPLETE", time.Minute)
	mustWebhook(t, r, ev)
	mustWebhook(t, r, ev) // same id
	again := partEgressEnded(st, "EG_1", recordingPartKey(st.ID, 1), "EGRESS_FAILED", time.Second)
	mustWebhook(t, r, again) // new id, same egress
	if r.lk.egressStarts != 2 {
		t.Fatalf("starts = %d, want 2", r.lk.egressStarts)
	}
	parts := r.store.RecordingParts(st.ID)
	if len(parts) != 2 || parts[0].State != postgres.PartComplete || parts[0].DurationMs != time.Minute.Milliseconds() || parts[1].State != postgres.PartRecording {
		t.Fatalf("parts = %+v", parts)
	}

	// After the end, the last part's egress_ended twice queues one import.
	if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	end := partEgressEnded(st, "EG_2", recordingPartKey(st.ID, 2), "EGRESS_COMPLETE", 30*time.Second)
	mustWebhook(t, r, end)
	end.ID = uuid.NewString()
	mustWebhook(t, r, end)
	if j := r.store.Imports[st.ID]; j == nil || j.ObjectKey != recordingPartKey(st.ID, 1) {
		t.Fatalf("import = %+v, want the longest part (1)", j)
	}
	if r.lk.egressStarts != 2 {
		t.Fatalf("starts = %d after the end", r.lk.egressStarts)
	}
}

// TestNoRecordingAfterEndOrAdminStop: once a stream has ended — by the host
// or an admin — no webhook or sweep starts an egress.
func TestNoRecordingAfterEndOrAdminStop(t *testing.T) {
	for _, admin := range []bool{false, true} {
		host := uuid.New()
		r := newRig(host)
		st := goLiveWithEncoder(t, r, host)
		if admin {
			if _, err := r.svc.AdminStopStream(ctx, uuid.New(), st.ID, "violence"); err != nil {
				t.Fatal(err)
			}
		} else if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
			t.Fatal(err)
		}
		mustStatus(t, r, st.ID, stEnded, map[bool]string{false: ReasonHostEnded, true: ReasonAdminStopped}[admin])
		// LiveKit still lists the encoder (a stale answer) and every
		// recording signal arrives late.
		encoderPublishing(r, st, true)
		mustWebhook(t, r, encoderEvent(st, "track_published"))
		mustWebhook(t, r, partEgressEnded(st, "EG_1", recordingPartKey(st.ID, 1), "EGRESS_COMPLETE", time.Minute))
		if err := r.svc.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if r.lk.egressStarts != 1 {
			t.Fatalf("admin=%v: egress started after the end: %d", admin, r.lk.egressStarts)
		}
		if j := r.store.Imports[st.ID]; j == nil || j.ObjectKey != recordingPartKey(st.ID, 1) {
			t.Fatalf("admin=%v: import = %+v", admin, j)
		}
		// Reserving directly also refuses an ended stream.
		r.svc.ensureRecording(ctx, r.store.Stream(st.ID))
		if r.lk.egressStarts != 1 {
			t.Fatalf("admin=%v: ensureRecording started an ended stream", admin)
		}
	}
}

// TestRecordingPartCapAndStartFailure: a failed start records a failed
// part (the next signal may try again), and a stream stops at the cap.
func TestRecordingPartCapAndStartFailure(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	r.lk.egressErr = errors.New("egress down")
	st := goLiveWithEncoder(t, r, host)
	if got := partStates(r, st.ID); !equalStrings(got, []string{postgres.PartFailed}) {
		t.Fatalf("parts = %v", got)
	}
	r.lk.egressErr = nil
	encoderPublishing(r, st, true)
	for i := 0; i < maxRecordingParts+5; i++ {
		if err := r.svc.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		n := r.lk.egressStarts
		mustWebhook(t, r, partEgressEnded(st, fmt.Sprintf("EG_%d", n), "", "EGRESS_FAILED", 0))
	}
	if got := len(r.store.RecordingParts(st.ID)); got != maxRecordingParts {
		t.Fatalf("parts = %d, want the cap %d", got, maxRecordingParts)
	}
}
