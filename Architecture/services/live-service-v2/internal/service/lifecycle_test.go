package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

var ctx = context.Background()

var testLimits = Limits{StartTimeout: 120 * time.Second, ReconnectGrace: 60 * time.Second}

// TestNextTable is the whole state machine: every status under every
// trigger, and both timeouts on each side of their limit.
func TestNextTable(t *testing.T) {
	type want struct {
		to, reason string
		ok         bool
	}
	same := func(s string) want { return want{to: s, ok: true} }
	refuse := want{}
	all := []string{stScheduled, stStarting, stLive, stReconnecting, stEnded, stFailed}
	table := map[Trigger]map[string]want{
		TrigStart: {
			stScheduled: {to: stStarting, ok: true}, stFailed: {to: stStarting, ok: true},
			stStarting: same(stStarting), stLive: same(stLive), stReconnecting: same(stReconnecting),
			stEnded: refuse,
		},
		TrigHostTrack: {
			stStarting: {to: stLive, ok: true}, stReconnecting: {to: stLive, ok: true}, stLive: same(stLive),
			stScheduled: refuse, stEnded: refuse, stFailed: refuse,
		},
		TrigHostLost: {
			stLive: {to: stReconnecting, ok: true}, stStarting: same(stStarting), stReconnecting: same(stReconnecting),
			stScheduled: refuse, stEnded: refuse, stFailed: refuse,
		},
		TrigHostEnd: {
			stScheduled: {stEnded, ReasonHostEnded, true}, stStarting: {stEnded, ReasonHostEnded, true},
			stLive: {stEnded, ReasonHostEnded, true}, stReconnecting: {stEnded, ReasonHostEnded, true},
			stEnded: same(stEnded), stFailed: same(stFailed),
		},
		TrigRoomFinished: {
			stStarting: {stEnded, ReasonRoomFinished, true}, stLive: {stEnded, ReasonRoomFinished, true},
			stReconnecting: {stEnded, ReasonRoomFinished, true},
			stScheduled:    same(stScheduled), stEnded: same(stEnded), stFailed: same(stFailed),
		},
		TrigAdminStop: {
			stScheduled: {stEnded, ReasonAdminStopped, true}, stStarting: {stEnded, ReasonAdminStopped, true},
			stLive: {stEnded, ReasonAdminStopped, true}, stReconnecting: {stEnded, ReasonAdminStopped, true},
			stEnded: refuse, stFailed: refuse,
		},
	}
	for trig, rows := range table {
		for _, cur := range all {
			w, listed := rows[cur]
			if !listed {
				t.Fatalf("trigger %d: status %s missing from the table", trig, cur)
			}
			d, ok := Next(cur, trig, 0, testLimits)
			if ok != w.ok || (ok && (d.To != w.to || d.Reason != w.reason)) {
				t.Errorf("Next(%s, %d) = %+v,%v want %+v", cur, trig, d, ok, w)
			}
		}
	}
	// Timeouts, both sides of each limit.
	timeouts := []struct {
		cur        string
		age        time.Duration
		to, reason string
	}{
		{stStarting, 119 * time.Second, stStarting, ""},
		{stStarting, 120 * time.Second, stFailed, ReasonNoMedia},
		{stReconnecting, 59 * time.Second, stReconnecting, ""},
		{stReconnecting, 60 * time.Second, stEnded, ReasonHostLost},
		{stLive, time.Hour, stLive, ""},
		{stScheduled, time.Hour, stScheduled, ""},
		{stEnded, time.Hour, stEnded, ""},
	}
	for _, tc := range timeouts {
		d, ok := Next(tc.cur, TrigTimeout, tc.age, testLimits)
		if !ok || d.To != tc.to || d.Reason != tc.reason {
			t.Errorf("timeout %s after %s = %+v,%v want %s/%s", tc.cur, tc.age, d, ok, tc.to, tc.reason)
		}
	}
}

func hostEvent(st *postgres.LiveStream, event string) WebhookEvent {
	return WebhookEvent{ID: uuid.NewString(), Event: event, Room: st.LiveKitRoom,
		ParticipantIdentity: st.CreatorUserID.String(), ParticipantTrackSIDs: []string{"TR_v"}, TrackSID: "TR_v"}
}

func outboxTypes(r *rig) []string {
	var out []string
	for _, e := range r.store.Outbox {
		out = append(out, e.EventType)
	}
	return out
}

func mustStatus(t *testing.T, r *rig, id uuid.UUID, status, reason string) {
	t.Helper()
	st := r.store.Stream(id)
	got := ""
	if st.EndedReason != nil {
		got = *st.EndedReason
	}
	if st.Status != status || got != reason {
		t.Fatalf("status = %s/%q, want %s/%q", st.Status, got, status, reason)
	}
}

// TestStartIsNotLive: POST /start answers 'starting'; nothing says live
// until the HOST publishes a track, and a viewer's track does not count.
func TestStartIsNotLive(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := r.svc.StartStream(ctx, st.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stream.Status != stStarting || res.PublisherToken == "" {
		t.Fatalf("start answered %+v", res.Stream)
	}
	if len(r.store.Outbox) != 0 {
		t.Fatalf("start enqueued %v", outboxTypes(r))
	}
	// Not in the live listing.
	list, _ := r.svc.ListLiveNow(ctx, uuid.Nil, 20, "")
	if len(list.Streams) != 0 {
		t.Fatalf("a starting stream is listed as live")
	}
	// A viewer's track is not the host's.
	ev := hostEvent(res.Stream, "track_published")
	ev.ParticipantIdentity = uuid.NewString()
	if err := r.svc.HandleWebhook(ctx, ev); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r, st.ID, stStarting, "")
	// The host's is.
	if err := r.svc.HandleWebhook(ctx, hostEvent(res.Stream, "track_published")); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r, st.ID, stLive, "")
	if got := outboxTypes(r); len(got) != 1 || got[0] != "live.stream.started" {
		t.Fatalf("outbox = %v", got)
	}
	if r.lk.egressStarts != 1 {
		t.Fatalf("egress starts = %d", r.lk.egressStarts)
	}
	if len(r.ev.ofType(EventStatusChanged)) != 2 {
		t.Fatalf("status.changed events = %d", len(r.ev.ofType(EventStatusChanged)))
	}
}

// TestReconnectGraceByFakeClock: host lost -> reconnecting; back -> live
// (no second started event); lost again and the grace runs out -> ended
// host_lost, ended event, room closed.
func TestReconnectGraceByFakeClock(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStreamStatus(host, stStarting)
	_ = r.svc.HandleWebhook(ctx, hostEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stLive, "")

	_ = r.svc.HandleWebhook(ctx, hostEvent(st, "participant_left"))
	mustStatus(t, r, st.ID, stReconnecting, "")
	_ = r.svc.HandleWebhook(ctx, hostEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stLive, "")
	if got := outboxTypes(r); len(got) != 1 {
		t.Fatalf("reconnect re-announced the start: %v", got)
	}

	_ = r.svc.HandleWebhook(ctx, hostEvent(st, "participant_connection_aborted"))
	mustStatus(t, r, st.ID, stReconnecting, "")
	r.clock.Advance(59 * time.Second)
	if err := r.svc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r, st.ID, stReconnecting, "")
	r.clock.Advance(2 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stEnded, ReasonHostLost)
	if got := outboxTypes(r); len(got) != 2 || got[1] != "live.stream.ended" {
		t.Fatalf("outbox = %v", got)
	}
	if len(r.lk.deleted) != 1 || r.lk.deleted[0] != st.LiveKitRoom {
		t.Fatalf("room not closed: %v", r.lk.deleted)
	}
	// A late track from the ended host changes nothing.
	_ = r.svc.HandleWebhook(ctx, hostEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stEnded, ReasonHostLost)
}

// TestStartTimeoutFailsWithNoMedia: starting for LIVE_START_TIMEOUT with
// no host track -> failed(no_media), no events; a new start is allowed.
func TestStartTimeoutFailsWithNoMedia(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStreamStatus(host, stScheduled)
	if _, err := r.svc.StartStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	r.clock.Advance(119 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stStarting, "")
	r.clock.Advance(2 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stFailed, ReasonNoMedia)
	if len(r.store.Outbox) != 0 {
		t.Fatalf("a stream that never went live emitted %v", outboxTypes(r))
	}
	res, err := r.svc.StartStream(ctx, st.ID, host)
	if err != nil || res.Stream.Status != stStarting || res.Stream.EndedReason != nil || res.Stream.EndedAt != nil {
		t.Fatalf("restart after failure: %+v %v", res, err)
	}
}

// TestEndReasons: host end, room_finished and admin stop each record their
// own ended_reason.
func TestEndReasons(t *testing.T) {
	host := uuid.New()
	r := newRig(host)

	a := r.store.AddStream(host)
	if _, err := r.svc.EndStream(ctx, a.ID, host); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r, a.ID, stEnded, ReasonHostEnded)
	if _, err := r.svc.EndStream(ctx, a.ID, uuid.New()); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("a stranger ended the stream: %v", err)
	}

	b := r.store.AddStream(host)
	_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: "rf", Event: "room_finished", Room: b.LiveKitRoom})
	mustStatus(t, r, b.ID, stEnded, ReasonRoomFinished)

	c := r.store.AddStream(host)
	admin := uuid.New()
	res, err := r.svc.AdminStopStream(ctx, admin, c.ID, "violence")
	if err != nil || !res.RoomClosed {
		t.Fatalf("admin stop: %+v %v", res, err)
	}
	mustStatus(t, r, c.ID, stEnded, ReasonAdminStopped)
	if _, err := r.svc.AdminStopStream(ctx, admin, c.ID, "again"); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("stopping an ended stream: %v", err)
	}
}

// TestTrackUnpublishedKeepsLiveWhileAnotherTrackRemains.
func TestTrackUnpublishedKeepsLiveWhileAnotherTrackRemains(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	ev := hostEvent(st, "track_unpublished")
	ev.ParticipantTrackSIDs = []string{"TR_a", "TR_v"}
	ev.TrackSID = "TR_v"
	_ = r.svc.HandleWebhook(ctx, ev)
	mustStatus(t, r, st.ID, stLive, "")
	ev = hostEvent(st, "track_unpublished")
	ev.ParticipantTrackSIDs = []string{"TR_a"}
	ev.TrackSID = "TR_a"
	_ = r.svc.HandleWebhook(ctx, ev)
	mustStatus(t, r, st.ID, stReconnecting, "")
}

// TestReconcileAgainstLiveKit: a lost webhook cannot keep a Live badge on a
// vanished host, nor keep a publishing host in starting; a LiveKit error
// changes nothing.
func TestReconcileAgainstLiveKit(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	live := r.store.AddStream(host)
	starting := r.store.AddStreamStatus(host, stStarting)
	r.lk.rooms[starting.LiveKitRoom] = []livekit.Participant{{Identity: host.String(), Tracks: []livekit.ParticipantTrack{{Sid: "TR"}}}}
	// live's room: the host is there without tracks.
	r.lk.rooms[live.LiveKitRoom] = []livekit.Participant{{Identity: host.String()}}

	r.lk.listErr = errors.New("livekit down")
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, live.ID, stLive, "")
	mustStatus(t, r, starting.ID, stStarting, "")

	r.lk.listErr = nil
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, live.ID, stReconnecting, "")
	mustStatus(t, r, starting.ID, stLive, "")

	// Room gone entirely: reconnecting stays until the grace, then ends.
	delete(r.lk.rooms, live.LiveKitRoom)
	r.clock.Advance(61 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, live.ID, stEnded, ReasonHostLost)
}

// TestWebhookIdempotentOnEventID: a redelivered event id is not applied
// twice.
func TestWebhookIdempotentOnEventID(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	ev := WebhookEvent{ID: "EV_1", Event: "participant_joined", Room: st.LiveKitRoom, ParticipantIdentity: uuid.NewString()}
	for i := 0; i < 3; i++ {
		if err := r.svc.HandleWebhook(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if r.store.ViewerEvents != 1 {
		t.Fatalf("applied %d times", r.store.ViewerEvents)
	}
}

// TestViewerCountExcludesHostAndPeakIsMax.
func TestViewerCountExcludesHostAndPeakIsMax(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	a, b := uuid.NewString(), uuid.NewString()
	join := func(id string) {
		_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "participant_joined", Room: st.LiveKitRoom, ParticipantIdentity: id})
	}
	leave := func(id string) {
		_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "participant_left", Room: st.LiveKitRoom, ParticipantIdentity: id})
	}
	join(host.String())
	join(a)
	join(b)
	join(b)           // duplicate join
	join("EG_egress") // not a user identity
	got := r.store.Stream(st.ID)
	if got.ViewerCount != 2 || got.ViewerPeak != 2 {
		t.Fatalf("count=%d peak=%d, want 2/2", got.ViewerCount, got.ViewerPeak)
	}
	leave(a)
	got = r.store.Stream(st.ID)
	if got.ViewerCount != 1 || got.ViewerPeak != 2 {
		t.Fatalf("after a leave: count=%d peak=%d, want 1/2", got.ViewerCount, got.ViewerPeak)
	}
	// End: peak survives in the ended event.
	if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	var env struct {
		Payload struct {
			ViewerPeak  int    `json:"viewer_peak"`
			EndedReason string `json:"ended_reason"`
		} `json:"payload"`
	}
	for _, e := range r.store.Outbox {
		if e.EventType == "live.stream.ended" {
			_ = json.Unmarshal(e.Payload, &env)
		}
	}
	if env.Payload.ViewerPeak != 2 || env.Payload.EndedReason != ReasonHostEnded {
		t.Fatalf("ended payload = %+v", env.Payload)
	}
}

// TestViewerCountPublishedAtMostEvery5s.
func TestViewerCountPublishedAtMostEvery5s(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStream(host)
	for i := 0; i < 5; i++ {
		_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "participant_joined", Room: st.LiveKitRoom, ParticipantIdentity: uuid.NewString()})
		r.clock.Advance(time.Second)
	}
	if n := len(r.ev.ofType(EventViewerCount)); n != 1 {
		t.Fatalf("published %d viewer.count in 5s, want 1", n)
	}
	r.clock.Advance(time.Second)
	_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "participant_joined", Room: st.LiveKitRoom, ParticipantIdentity: uuid.NewString()})
	evs := r.ev.ofType(EventViewerCount)
	if len(evs) != 2 || evs[1]["viewer_count"] != float64(6) {
		t.Fatalf("second window: %v", evs)
	}
}

// TestOutboxAtomicWithTransition: when the outbox insert fails the status
// change is not committed either.
func TestOutboxAtomicWithTransition(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStreamStatus(host, stStarting)
	r.store.FailOutbox = true
	if err := r.svc.HandleWebhook(ctx, hostEvent(st, "track_published")); err == nil {
		t.Fatal("expected the webhook to fail (and be retried)")
	}
	mustStatus(t, r, st.ID, stStarting, "")
	r.store.FailOutbox = false
	if err := r.svc.HandleWebhook(ctx, hostEvent(st, "track_published")); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r, st.ID, stLive, "")
	if len(r.store.Outbox) != 1 || r.store.Outbox[0].IdempotencyKey != "live.started:"+st.ID.String() ||
		r.store.Outbox[0].PartitionKey != host.String() {
		t.Fatalf("outbox = %+v", r.store.Outbox)
	}
}

// TestRoomEventBodyIsFlat: payload fields at the top level, and again under
// payload.
func TestRoomEventBodyIsFlat(t *testing.T) {
	id := uuid.New()
	body, err := roomEventBody(id, EventChatRemoved, map[string]any{"message_id": "m1"}, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	p, _ := m["payload"].(map[string]any)
	if m["type"] != EventChatRemoved || m["stream_id"] != id.String() || m["message_id"] != "m1" || p["message_id"] != "m1" {
		t.Fatalf("body = %s", body)
	}
}
