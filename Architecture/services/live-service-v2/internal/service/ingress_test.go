package service

// Going live from streaming software (1 Oct 2026): source validation, the
// ingress routes' rules, the stream key staying out of everything, and the
// encoder's place in the lifecycle. Over the memory store, a recording
// LiveKit and a fake clock.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

func encoderStream(t *testing.T, r *rig, host uuid.UUID) *postgres.LiveStream {
	t.Helper()
	st, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "obs", Source: "encoder"})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// encoderEvent is a webhook from the stream's encoder participant.
func encoderEvent(st *postgres.LiveStream, event string) WebhookEvent {
	return WebhookEvent{ID: uuid.NewString(), Event: event, Room: st.LiveKitRoom,
		ParticipantIdentity: EncoderIdentity(st.ID), ParticipantTrackSIDs: []string{"TR_v"}, TrackSID: "TR_v"}
}

// goLiveWithEncoder: ingress, start, the encoder's track.
func goLiveWithEncoder(t *testing.T, r *rig, host uuid.UUID) *postgres.LiveStream {
	t.Helper()
	st := encoderStream(t, r, host)
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.StartStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.HandleWebhook(ctx, encoderEvent(st, "track_published")); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r, st.ID, stLive, "")
	return r.store.Stream(st.ID)
}

// TestCreateStreamSource: absent is device; device and encoder are accepted
// in any case and padding; anything else is refused and stores nothing.
func TestCreateStreamSource(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	for in, want := range map[string]string{"": "device", "device": "device", " Encoder ": "encoder", "ENCODER": "encoder"} {
		st, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x", Source: in})
		if err != nil || st.Source != want {
			t.Fatalf("source %q: %+v %v", in, st, err)
		}
		if st.HasIngress == nil || *st.HasIngress {
			t.Fatalf("source %q: a new stream's has_ingress = %v", in, st.HasIngress)
		}
	}
	n := len(r.store.Streams)
	for _, bad := range []string{"rtmp", "obs", "whip", "device,encoder"} {
		if _, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x", Source: bad}); !errors.Is(err, ErrInvalidSource) {
			t.Fatalf("source %q: %v", bad, err)
		}
	}
	if len(r.store.Streams) != n {
		t.Fatalf("a refused source stored a stream")
	}
}

// TestIngressIdempotentAndReset: the first call issues an RTMP ingress into
// the stream's room as encoder_<stream id>; later calls return the same one;
// delete then create is a new key.
func TestIngressIdempotentAndReset(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)

	first, err := r.svc.CreateIngress(ctx, st.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	if first.ServerURL != "rtmps://ingress.test/x" || first.IngressID != "ING_1" || !strings.HasPrefix(first.StreamKey, testKeyMarker) {
		t.Fatalf("first = %+v", *first)
	}
	if len(r.lk.ingressCreates) != 1 {
		t.Fatalf("creates = %d", len(r.lk.ingressCreates))
	}
	req := r.lk.ingressCreates[0]
	if req.Room != st.LiveKitRoom || req.Identity != "encoder_"+st.ID.String() || req.ParticipantName != "Host" {
		t.Fatalf("ingress request = %+v", req)
	}
	// The identity is built from the row id, not from the id in the room name
	// (they differ), and it is not the host's user id.
	if req.Identity == "encoder_"+strings.TrimPrefix(st.LiveKitRoom, "stream_") || req.Identity == host.String() {
		t.Fatalf("encoder identity = %s", req.Identity)
	}
	row := r.store.Stream(st.ID)
	if row.IngressID == nil || *row.IngressID != "ING_1" || row.EncoderIdentity == nil || *row.EncoderIdentity != req.Identity {
		t.Fatalf("row = %+v", row)
	}

	for i := 0; i < 3; i++ {
		again, err := r.svc.CreateIngress(ctx, st.ID, host)
		if err != nil || *again != *first {
			t.Fatalf("call %d: %+v %v", i+2, again, err)
		}
	}
	if len(r.lk.ingressCreates) != 1 {
		t.Fatalf("an existing ingress was re-issued: %d creates", len(r.lk.ingressCreates))
	}

	// Reset key.
	if err := r.svc.DeleteIngress(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	if row := r.store.Stream(st.ID); row.IngressID != nil || row.EncoderIdentity == nil {
		t.Fatalf("after delete: ingress=%v identity=%v", row.IngressID, row.EncoderIdentity)
	}
	if len(r.lk.ingressDeletes) != 1 || r.lk.ingressDeletes[0] != "ING_1" {
		t.Fatalf("deletes = %v", r.lk.ingressDeletes)
	}
	if err := r.svc.DeleteIngress(ctx, st.ID, host); err != nil || len(r.lk.ingressDeletes) != 1 {
		t.Fatalf("deleting nothing: %v, deletes %v", err, r.lk.ingressDeletes)
	}
	second, err := r.svc.CreateIngress(ctx, st.ID, host)
	if err != nil || second.IngressID == first.IngressID || second.StreamKey == first.StreamKey {
		t.Fatalf("reset: %+v %v", second, err)
	}

	// LiveKit lost the ingress: a new one is issued instead of a dead key.
	delete(r.lk.ingresses, second.IngressID)
	third, err := r.svc.CreateIngress(ctx, st.ID, host)
	if err != nil || third.IngressID == second.IngressID {
		t.Fatalf("after LiveKit lost it: %+v %v", third, err)
	}
	if row := r.store.Stream(st.ID); row.IngressID == nil || *row.IngressID != third.IngressID {
		t.Fatalf("row ingress = %v, want %s", row.IngressID, third.IngressID)
	}
}

// TestIngressGuards: host only, the start gate, encoder streams only, and
// only before the stream is on air.
func TestIngressGuards(t *testing.T) {
	host, stranger := uuid.New(), uuid.New()
	r := newRig(host, stranger)
	st := encoderStream(t, r, host)

	if _, err := r.svc.CreateIngress(ctx, st.ID, stranger); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("a stranger got the key: %v", err)
	}
	if err := r.svc.DeleteIngress(ctx, st.ID, stranger); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("a stranger deleted the ingress: %v", err)
	}
	if _, err := r.svc.CreateIngress(ctx, uuid.New(), host); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("missing stream: %v", err)
	}

	// The pilot gate and the live ban, as on start.
	outsider := uuid.New()
	theirs := r.store.AddStreamStatus(outsider, stScheduled)
	r.store.Streams[theirs.ID].Source = SourceEncoder
	if _, err := r.svc.CreateIngress(ctx, theirs.ID, outsider); !errors.Is(err, ErrLiveNotEnabled) {
		t.Fatalf("a host outside the pilot got a key: %v", err)
	}
	r.store.PlatformBans[host] = postgres.PlatformBan{UserID: host}
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); !errors.Is(err, ErrLiveBanned) {
		t.Fatalf("a live-banned host got a key: %v", err)
	}
	delete(r.store.PlatformBans, host)

	// A device stream has no key.
	dev, _ := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "cam"})
	if _, err := r.svc.CreateIngress(ctx, dev.ID, host); !errors.Is(err, ErrNotEncoderStream) {
		t.Fatalf("a device stream got a key: %v", err)
	}

	// State: any status but ended. On air the same key is read back (a host
	// who reloaded the page mid-stream).
	for status, ok := range map[string]bool{
		stScheduled: true, stStarting: true, stFailed: true, stLive: true, stReconnecting: true,
		stEnded: false,
	} {
		s := r.store.AddStreamStatus(host, stScheduled)
		r.store.Streams[s.ID].Source = SourceEncoder
		before, err := r.svc.CreateIngress(ctx, s.ID, host)
		if err != nil {
			t.Fatal(err)
		}
		r.store.Streams[s.ID].Status = status
		got, err := r.svc.CreateIngress(ctx, s.ID, host)
		if ok && (err != nil || *got != *before) {
			t.Fatalf("%s: %+v %v, want the same ingress", status, got, err)
		}
		if !ok && !errors.Is(err, ErrStateConflict) {
			t.Fatalf("%s: got %v, want a state conflict", status, err)
		}
		// With none on the row: issued, except on an ended stream.
		r.store.Streams[s.ID].IngressID = nil
		_, err = r.svc.CreateIngress(ctx, s.ID, host)
		if ok && (err != nil || r.store.Stream(s.ID).IngressID == nil) {
			t.Fatalf("%s without an ingress: %v", status, err)
		}
		if !ok && (!errors.Is(err, ErrStateConflict) || r.store.Stream(s.ID).IngressID != nil) {
			t.Fatalf("%s without an ingress: %v", status, err)
		}
	}
	if len(r.lk.ingressCreates) != 11 {
		t.Fatalf("creates = %d, want 11 (6 streams, 5 re-issues; none for the ended one)", len(r.lk.ingressCreates))
	}

	// LiveKit refuses: 502's error, nothing recorded.
	r.lk.ingressErr = errors.New("twirp: resource_exhausted")
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); !errors.Is(err, ErrIngressUnavailable) {
		t.Fatalf("LiveKit down: %v", err)
	}
	if r.store.Stream(st.ID).IngressID != nil {
		t.Fatal("a failed create recorded an ingress")
	}
	r.lk.ingressErr = nil
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	// Reading an existing one back fails: unavailable, and the ingress stays.
	r.lk.ingressErr = errors.New("timeout")
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); !errors.Is(err, ErrIngressUnavailable) {
		t.Fatalf("read back while LiveKit is down: %v", err)
	}
	r.lk.ingressErr = nil
	// Delete fails: unavailable, and the row keeps the ingress.
	r.lk.ingressDeleteErr = errors.New("timeout")
	if err := r.svc.DeleteIngress(ctx, st.ID, host); !errors.Is(err, ErrIngressUnavailable) {
		t.Fatalf("delete while LiveKit is down: %v", err)
	}
	if r.store.Stream(st.ID).IngressID == nil {
		t.Fatal("a failed delete forgot the ingress")
	}
}

// TestIngressLostRaceDeletesTheSpare: two requests create at once; the one
// that does not get recorded deletes its ingress and answers the recorded
// one, so no key exists that the row does not know.
func TestIngressLostRaceDeletesTheSpare(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)
	winner := &livekit.Ingress{ID: "ING_WINNER", URL: "rtmps://ingress.test/x", StreamKey: testKeyMarker + "winner"}
	r.lk.onIngressCreate = func(*livekit.Ingress) {
		r.lk.onIngressCreate = nil
		r.lk.mu.Lock()
		r.lk.ingresses[winner.ID] = winner
		r.lk.mu.Unlock()
		if ok, err := r.store.SetIngress(ctx, st.ID, winner.ID, EncoderIdentity(st.ID)); err != nil || !ok {
			t.Errorf("the concurrent request was not recorded: %v %v", ok, err)
		}
	}
	got, err := r.svc.CreateIngress(ctx, st.ID, host)
	if err != nil || got.IngressID != winner.ID || got.StreamKey != winner.StreamKey {
		t.Fatalf("got %+v %v, want the recorded ingress", got, err)
	}
	if len(r.lk.ingressDeletes) != 1 || r.lk.ingressDeletes[0] != "ING_1" {
		t.Fatalf("the spare ingress was not deleted: %v", r.lk.ingressDeletes)
	}
}

// TestIngressForAStreamThatEndedMeanwhile: the stream ends while LiveKit is
// creating the ingress. Nothing is recorded, the new ingress is deleted, and
// the host is told the stream is over — no key survives an ended stream.
func TestIngressForAStreamThatEndedMeanwhile(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)
	r.lk.onIngressCreate = func(*livekit.Ingress) {
		if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
			t.Errorf("end: %v", err)
		}
	}
	if got, err := r.svc.CreateIngress(ctx, st.ID, host); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("got %+v %v, want a state conflict", got, err)
	}
	if r.store.Stream(st.ID).IngressID != nil {
		t.Fatal("an ended stream recorded an ingress")
	}
	if len(r.lk.ingresses) != 0 || len(r.lk.ingressDeletes) != 1 {
		t.Fatalf("the ingress of an ended stream was left in LiveKit: %v", r.lk.ingresses)
	}
}

// TestStreamKeyNeverLeaks: through a whole encoder broadcast, with LiveKit
// failing along the way, the key is in the ingress answer and nowhere else:
// no log line, stream row, start answer, outbox event or room frame.
func TestStreamKeyNeverLeaks(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	host, mod := uuid.New(), uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)
	_ = r.store.ReplaceModerators(ctx, st.ID, []uuid.UUID{mod}, host)

	var seen []string // everything a client or a log reader can see
	add := func(v any) {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		seen = append(seen, string(b))
	}

	res, err := r.svc.CreateIngress(ctx, st.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	key := res.StreamKey
	if !strings.HasPrefix(key, testKeyMarker) {
		t.Fatalf("key = %q", key)
	}
	// Printing or logging the answer itself does not print the key.
	seen = append(seen, fmt.Sprintf("%v %+v %#v %s", res, *res, res, res))
	ing := &livekit.Ingress{ID: res.IngressID, URL: res.ServerURL, StreamKey: key}
	seen = append(seen, fmt.Sprintf("%v %+v %#v %s", ing, *ing, ing, ing))
	slog.Info("answer", "result", *res, "ingress", *ing, "result_ptr", res)

	// Failures on the way are logged without it.
	r.lk.ingressErr = errors.New("livekit unreachable")
	_, _ = r.svc.CreateIngress(ctx, st.ID, host)
	r.lk.ingressErr = nil
	r.lk.ingressDeleteErr = errors.New("livekit unreachable")
	_ = r.svc.DeleteIngress(ctx, st.ID, host)

	start, err := r.svc.StartStream(ctx, st.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	add(start)
	if err := r.svc.HandleWebhook(ctx, encoderEvent(st, "track_published")); err != nil {
		t.Fatal(err)
	}
	for _, viewer := range []uuid.UUID{host, mod, uuid.New(), uuid.Nil} {
		row, err := r.svc.GetStream(ctx, st.ID, viewer)
		if err != nil {
			t.Fatal(err)
		}
		add(row)
		list, err := r.svc.ListLiveNow(ctx, viewer, 20, "")
		if err != nil || len(list.Streams) != 1 {
			t.Fatalf("list: %+v %v", list, err)
		}
		add(list)
	}
	admin, _ := r.store.ListByStatuses(ctx, []string{stLive}, 10)
	add(admin)
	// The end: the ingress delete fails (logged), the sweeper retries it.
	ended, err := r.svc.EndStream(ctx, st.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	add(ended)
	_ = r.svc.Sweep(ctx)
	r.lk.ingressDeleteErr = nil
	_ = r.svc.Sweep(ctx)
	if r.store.Stream(st.ID).IngressID != nil {
		t.Fatal("the sweeper did not delete the ingress")
	}

	for _, e := range r.store.Outbox {
		seen = append(seen, string(e.Payload), e.PartitionKey, e.IdempotencyKey)
	}
	if len(r.store.Outbox) != 2 {
		t.Fatalf("outbox = %v", outboxTypes(r))
	}
	if len(r.ev.events) == 0 {
		t.Fatal("no room frames were published")
	}
	for _, e := range r.ev.events {
		add(e)
	}
	add(r.store.Audits)
	if logs.Len() == 0 {
		t.Fatal("nothing was logged: the capture is not wired")
	}
	seen = append(seen, logs.String())

	for _, s := range seen {
		if strings.Contains(s, key) || strings.Contains(s, testKeyMarker) {
			t.Fatalf("the stream key leaked into: %s", s)
		}
		// Rows and frames do not carry the ingress id either (has_ingress is
		// all a client is told).
		if strings.Contains(s, `"ingress_id"`) || strings.Contains(s, `"stream_key"`) || strings.Contains(s, `"encoder_identity"`) {
			t.Fatalf("an ingress field is on the wire: %s", s)
		}
	}
}

// TestHasIngressOnlyForHostAndModerators.
func TestHasIngressOnlyForHostAndModerators(t *testing.T) {
	host, mod, viewer := uuid.New(), uuid.New(), uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)
	_ = r.store.ReplaceModerators(ctx, st.ID, []uuid.UUID{mod}, host)
	has := func(who uuid.UUID) *bool {
		row, err := r.svc.GetStream(ctx, st.ID, who)
		if err != nil {
			t.Fatal(err)
		}
		if row.Source != SourceEncoder {
			t.Fatalf("source = %q", row.Source)
		}
		return row.HasIngress
	}
	if h := has(host); h == nil || *h {
		t.Fatalf("host before the ingress: %v", h)
	}
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	for _, who := range []uuid.UUID{host, mod} {
		if h := has(who); h == nil || !*h {
			t.Fatalf("%s: has_ingress = %v, want true", who, h)
		}
	}
	for _, who := range []uuid.UUID{viewer, uuid.Nil} {
		if h := has(who); h != nil {
			t.Fatalf("a viewer is told has_ingress = %v", *h)
		}
	}
}

// TestEncoderStartHasNoPublisherToken: an encoder stream's start answers
// 'starting' with source encoder and no publisher token; a device stream
// still gets its token. The host may watch their own encoder stream.
func TestEncoderStartHasNoPublisherToken(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	enc := encoderStream(t, r, host)
	res, err := r.svc.StartStream(ctx, enc.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	if res.PublisherToken != "" || res.Source != SourceEncoder || res.Stream.Status != stStarting || res.Stream.Source != SourceEncoder {
		t.Fatalf("encoder start = %+v", res)
	}
	if res.Room != enc.LiveKitRoom || res.ServerURL == "" {
		t.Fatalf("encoder start room/url = %q %q", res.Room, res.ServerURL)
	}
	// A rejoin gets none either.
	if again, err := r.svc.StartStream(ctx, enc.ID, host); err != nil || again.PublisherToken != "" {
		t.Fatalf("second start = %+v %v", again, err)
	}
	tok, err := r.svc.IssueViewerToken(ctx, enc.ID, host)
	if err != nil || tok.Token == "" || tok.Room != enc.LiveKitRoom {
		t.Fatalf("the host cannot watch their own stream: %+v %v", tok, err)
	}

	dev, _ := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "cam"})
	dres, err := r.svc.StartStream(ctx, dev.ID, host)
	if err != nil || dres.PublisherToken == "" || dres.Source != SourceDevice {
		t.Fatalf("device start = %+v %v", dres, err)
	}
}

// TestEncoderMakesItLiveAndIsNotAViewer: the encoder's track is the host's
// media; the encoder and the watching host are never viewers; the host
// closing their own view does not interrupt the stream; the encoder leaving
// does, and the grace ends it host_lost.
func TestEncoderMakesItLiveAndIsNotAViewer(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.StartStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	st = r.store.Stream(st.ID)

	// Someone else's encoder_ identity is not this stream's host.
	other := encoderEvent(st, "track_published")
	other.ParticipantIdentity = EncoderIdentity(uuid.New())
	_ = r.svc.HandleWebhook(ctx, other)
	mustStatus(t, r, st.ID, stStarting, "")

	_ = r.svc.HandleWebhook(ctx, encoderEvent(st, "participant_joined"))
	_ = r.svc.HandleWebhook(ctx, hostEvent(st, "participant_joined")) // the host, watching
	if got := r.store.Stream(st.ID); got.ViewerCount != 0 || got.ViewerPeak != 0 || r.store.ViewerEvents != 0 {
		t.Fatalf("the encoder or the host was counted: count=%d peak=%d events=%d", got.ViewerCount, got.ViewerPeak, r.store.ViewerEvents)
	}
	if err := r.svc.HandleWebhook(ctx, encoderEvent(st, "track_published")); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r, st.ID, stLive, "")
	if got := outboxTypes(r); len(got) != 1 || got[0] != "live.stream.started" {
		t.Fatalf("outbox = %v", got)
	}
	if r.lk.egressStarts != 1 {
		t.Fatalf("egress starts = %d", r.lk.egressStarts)
	}

	viewer := uuid.NewString()
	_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "participant_joined", Room: st.LiveKitRoom, ParticipantIdentity: viewer})
	if got := r.store.Stream(st.ID); got.ViewerCount != 1 {
		t.Fatalf("viewer count = %d, want 1", got.ViewerCount)
	}

	// The host closes their own view: still live, still one viewer.
	_ = r.svc.HandleWebhook(ctx, hostEvent(st, "participant_left"))
	mustStatus(t, r, st.ID, stLive, "")
	if got := r.store.Stream(st.ID); got.ViewerCount != 1 {
		t.Fatalf("viewer count after the host left = %d", got.ViewerCount)
	}

	// One of two encoder tracks goes: still live. The last one: reconnecting.
	ev := encoderEvent(st, "track_unpublished")
	ev.ParticipantTrackSIDs, ev.TrackSID = []string{"TR_a", "TR_v"}, "TR_a"
	_ = r.svc.HandleWebhook(ctx, ev)
	mustStatus(t, r, st.ID, stLive, "")
	_ = r.svc.HandleWebhook(ctx, encoderEvent(st, "participant_left"))
	mustStatus(t, r, st.ID, stReconnecting, "")
	if got := r.store.Stream(st.ID); got.ViewerCount != 1 || r.store.ViewerEvents != 1 {
		t.Fatalf("the encoder leaving changed the viewers: count=%d events=%d", got.ViewerCount, r.store.ViewerEvents)
	}
	// Back within the grace.
	_ = r.svc.HandleWebhook(ctx, encoderEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stLive, "")
	// Gone for good.
	_ = r.svc.HandleWebhook(ctx, encoderEvent(st, "participant_connection_aborted"))
	r.clock.Advance(59 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stReconnecting, "")
	r.clock.Advance(2 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stEnded, ReasonHostLost)
}

// TestEncoderIdentityIsNothingOnADeviceStream: on a device stream only the
// creator is the host, whatever a participant calls itself.
func TestEncoderIdentityIsNothingOnADeviceStream(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := r.store.AddStreamStatus(host, stStarting)
	_ = r.svc.HandleWebhook(ctx, encoderEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stStarting, "")
	// Even with an identity on the row.
	ident := EncoderIdentity(st.ID)
	r.store.Streams[st.ID].EncoderIdentity = &ident
	_ = r.svc.HandleWebhook(ctx, encoderEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stStarting, "")
	// And the creator leaving a device stream is still the host being lost.
	live := r.store.AddStream(host)
	_ = r.svc.HandleWebhook(ctx, hostEvent(live, "participant_left"))
	mustStatus(t, r, live.ID, stReconnecting, "")
}

// TestReconcileAcceptsTheEncoder: the sweeper's LiveKit reconcile takes the
// encoder's published track as the host's media, and its absence as the
// host being lost.
func TestReconcileAcceptsTheEncoder(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.StartStream(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	// No webhook arrived; LiveKit shows the encoder publishing and the host
	// watching without tracks.
	r.lk.rooms[st.LiveKitRoom] = []livekit.Participant{
		{Identity: host.String()},
		{Identity: EncoderIdentity(st.ID), Tracks: []livekit.ParticipantTrack{{Sid: "TR_v"}}},
	}
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stLive, "")
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stLive, "") // and it does not flap

	// The encoder is gone; the host is still watching. On an encoder stream
	// only the encoder's tracks are the media, whatever the creator's
	// connection shows.
	r.lk.rooms[st.LiveKitRoom] = []livekit.Participant{{Identity: host.String(), Tracks: []livekit.ParticipantTrack{{Sid: "TR_x"}}}}
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stReconnecting, "")
	_ = r.svc.HandleWebhook(ctx, hostEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stReconnecting, "")

	// Another stream's encoder in a device stream's room is not its host.
	dev := r.store.AddStreamStatus(host, stStarting)
	r.lk.rooms[dev.LiveKitRoom] = []livekit.Participant{
		{Identity: EncoderIdentity(dev.ID), Tracks: []livekit.ParticipantTrack{{Sid: "TR_v"}}},
	}
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, dev.ID, stStarting, "")
}

// TestEncoderPublishingBeforeStart: the host presses Start Streaming in OBS
// before Start here. The encoder's events while 'scheduled' change nothing,
// and once the stream is 'starting' it does not wait for an event that
// already happened: the sweeper's reconcile, or the encoder's next track,
// takes it live.
func TestEncoderPublishingBeforeStart(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	publishing := func(st *postgres.LiveStream) {
		r.lk.rooms[st.LiveKitRoom] = []livekit.Participant{
			{Identity: EncoderIdentity(st.ID), Tracks: []livekit.ParticipantTrack{{Sid: "TR_v"}, {Sid: "TR_a"}}},
		}
	}
	early := func() *postgres.LiveStream {
		st := encoderStream(t, r, host)
		if _, err := r.svc.CreateIngress(ctx, st.ID, host); err != nil {
			t.Fatal(err)
		}
		for _, ev := range []string{"participant_joined", "track_published", "track_published"} {
			if err := r.svc.HandleWebhook(ctx, encoderEvent(st, ev)); err != nil {
				t.Fatalf("%s while scheduled: %v", ev, err)
			}
		}
		publishing(st)
		_ = r.svc.Sweep(ctx)
		mustStatus(t, r, st.ID, stScheduled, "") // nothing is live before Start
		if got := r.store.Stream(st.ID); got.ViewerCount != 0 || got.StartedAt != nil {
			t.Fatalf("a scheduled stream changed: %+v", got)
		}
		if _, err := r.svc.StartStream(ctx, st.ID, host); err != nil {
			t.Fatal(err)
		}
		mustStatus(t, r, st.ID, stStarting, "")
		return st
	}

	// By the sweeper's reconcile.
	a := early()
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, a.ID, stLive, "")
	if got := outboxTypes(r); len(got) != 1 || got[0] != "live.stream.started" {
		t.Fatalf("outbox = %v", got)
	}

	// By the encoder's next track event.
	b := early()
	_ = r.svc.HandleWebhook(ctx, encoderEvent(b, "track_published"))
	mustStatus(t, r, b.ID, stLive, "")
}

// TestHostWatchesOwnStreamWhateverItsVisibility: the creator gets a viewer
// token for their own stream while it is starting, live or reconnecting —
// followers-only included — and it is a viewer token (the fake's), never a
// publisher token.
func TestHostWatchesOwnStreamWhateverItsVisibility(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	for _, vis := range []string{visibilityPublic, visibilityFollowers} {
		st, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x", Visibility: vis, Source: "encoder"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.svc.IssueViewerToken(ctx, st.ID, host); !errors.Is(err, ErrStreamNotLive) {
			t.Fatalf("%s scheduled: %v", vis, err)
		}
		for _, status := range []string{stStarting, stLive, stReconnecting} {
			r.store.Streams[st.ID].Status = status
			tok, err := r.svc.IssueViewerToken(ctx, st.ID, host)
			if err != nil || tok.Token != "viewer-token" || tok.Room != st.LiveKitRoom {
				t.Fatalf("%s %s: %+v %v", vis, status, tok, err)
			}
		}
		// Someone who does not follow is still refused a followers-only stream.
		_, err = r.svc.IssueViewerToken(ctx, st.ID, uuid.New())
		if vis == visibilityFollowers && !errors.Is(err, ErrNotFollower) {
			t.Fatalf("a non-follower got a token: %v", err)
		}
	}
	if r.graph.calls != 2 {
		t.Fatalf("graph calls = %d: the host's own token must not ask the graph", r.graph.calls)
	}
}

// TestCancelScheduledEncoderStream: POST /end on a stream that never
// started is a clean cancel — ended, host_ended, no live.stream.ended (it
// was never live), the ingress deleted.
func TestCancelScheduledEncoderStream(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); err != nil {
		t.Fatal(err)
	}
	ended, err := r.svc.EndStream(ctx, st.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	if ended.Status != stEnded || ended.EndedReason == nil || *ended.EndedReason != ReasonHostEnded ||
		ended.StartedAt != nil || ended.EndedAt == nil || ended.HasIngress == nil || *ended.HasIngress {
		t.Fatalf("cancelled row = %+v", ended)
	}
	if len(r.store.Outbox) != 0 {
		t.Fatalf("a stream that was never live emitted %v", outboxTypes(r))
	}
	if len(r.lk.ingressDeletes) != 1 || len(r.lk.ingresses) != 0 || r.store.Stream(st.ID).IngressID != nil {
		t.Fatalf("the ingress outlived the cancel: %v", r.lk.ingressDeletes)
	}
	// It stays ended: no key, no start, and ending again is a no-op.
	if _, err := r.svc.CreateIngress(ctx, st.ID, host); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("a key for a cancelled stream: %v", err)
	}
	if _, err := r.svc.StartStream(ctx, st.ID, host); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("a cancelled stream started: %v", err)
	}
	if again, err := r.svc.EndStream(ctx, st.ID, host); err != nil || again.Status != stEnded {
		t.Fatalf("ending twice: %+v %v", again, err)
	}
	// The same from 'failed' (a start that timed out, then cancelled): the
	// row stays failed (Next's table) and keeps its ingress for a retry.
	f := encoderStream(t, r, host)
	_, _ = r.svc.CreateIngress(ctx, f.ID, host)
	_, _ = r.svc.StartStream(ctx, f.ID, host)
	r.clock.Advance(601 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, f.ID, stFailed, ReasonNoMedia)
	if got, err := r.svc.EndStream(ctx, f.ID, host); err != nil || got.Status != stFailed || got.HasIngress == nil || !*got.HasIngress {
		t.Fatalf("end on a failed stream: %+v %v", got, err)
	}
	// ...but not for ever: abandoned for a day, the sweeper deletes it.
	r.clock.Advance(23 * time.Hour)
	_ = r.svc.Sweep(ctx)
	if r.store.Stream(f.ID).IngressID == nil {
		t.Fatal("a failed stream lost its ingress within the day")
	}
	r.clock.Advance(2 * time.Hour)
	_ = r.svc.Sweep(ctx)
	if r.store.Stream(f.ID).IngressID != nil || len(r.lk.ingresses) != 0 {
		t.Fatalf("an abandoned failed stream kept its ingress: %v", r.lk.ingresses)
	}
	// A late retry gets a new key.
	if _, err := r.svc.CreateIngress(ctx, f.ID, host); err != nil || r.store.Stream(f.ID).IngressID == nil {
		t.Fatalf("a key for a retry after the sweep: %v", err)
	}
}

// TestEncoderStartTimeout: on one fake clock a device stream fails after
// LIVE_START_TIMEOUT while an encoder stream waits LIVE_ENCODER_START_TIMEOUT.
func TestEncoderStartTimeout(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	if r.svc.limits.StartTimeout != 120*time.Second || r.svc.limits.EncoderStartTimeout != 10*time.Minute {
		t.Fatalf("default limits = %+v", r.svc.limits)
	}
	dev, _ := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "cam"})
	enc := encoderStream(t, r, host)
	key, err := r.svc.CreateIngress(ctx, enc.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{dev.ID, enc.ID} {
		if _, err := r.svc.StartStream(ctx, id, host); err != nil {
			t.Fatal(err)
		}
	}
	r.clock.Advance(121 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, dev.ID, stFailed, ReasonNoMedia)
	mustStatus(t, r, enc.ID, stStarting, "")

	r.clock.Advance(478 * time.Second) // 9m59s
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, enc.ID, stStarting, "")
	r.clock.Advance(2 * time.Second) // 10m01s
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, enc.ID, stFailed, ReasonNoMedia)
	// A failed start keeps the key: "Try again" needs no new paste, and the
	// sweeper leaves it alone.
	_ = r.svc.Sweep(ctx)
	if r.store.Stream(enc.ID).IngressID == nil || len(r.lk.ingressDeletes) != 0 {
		t.Fatalf("the failed stream lost its ingress: %v", r.lk.ingressDeletes)
	}
	if again, err := r.svc.CreateIngress(ctx, enc.ID, host); err != nil || *again != *key {
		t.Fatalf("the key after a failed start: %+v %v", again, err)
	}
	if res, err := r.svc.StartStream(ctx, enc.ID, host); err != nil || res.Stream.Status != stStarting || res.Stream.HasIngress == nil || !*res.Stream.HasIngress {
		t.Fatalf("restart: %+v %v", res, err)
	}
	if again, err := r.svc.CreateIngress(ctx, enc.ID, host); err != nil || *again != *key {
		t.Fatalf("the key after the restart: %+v %v", again, err)
	}

	// A configured value is used.
	svc := New(r.store, r.lk, r.graph, nil, Config{EncoderStartTimeout: 3 * time.Minute})
	if svc.limits.EncoderStartTimeout != 3*time.Minute {
		t.Fatalf("configured encoder start timeout = %s", svc.limits.EncoderStartTimeout)
	}
}

// TestDecideForEncoder: the table is Next's, except the encoder start
// timeout and room_finished while waiting for the encoder.
func TestDecideForEncoder(t *testing.T) {
	lim := Limits{StartTimeout: 120 * time.Second, ReconnectGrace: 60 * time.Second, EncoderStartTimeout: 600 * time.Second}
	row := func(source, status string) *postgres.LiveStream {
		return &postgres.LiveStream{Source: source, Status: status}
	}
	cases := []struct {
		source, status string
		trig           Trigger
		age            time.Duration
		to, reason     string
	}{
		{SourceDevice, stStarting, TrigTimeout, 120 * time.Second, stFailed, ReasonNoMedia},
		{SourceEncoder, stStarting, TrigTimeout, 120 * time.Second, stStarting, ""},
		{SourceEncoder, stStarting, TrigTimeout, 599 * time.Second, stStarting, ""},
		{SourceEncoder, stStarting, TrigTimeout, 600 * time.Second, stFailed, ReasonNoMedia},
		{SourceEncoder, stReconnecting, TrigTimeout, 60 * time.Second, stEnded, ReasonHostLost},
		{SourceDevice, stStarting, TrigRoomFinished, 0, stEnded, ReasonRoomFinished},
		{SourceDevice, stReconnecting, TrigRoomFinished, 0, stEnded, ReasonRoomFinished},
		{SourceEncoder, stStarting, TrigRoomFinished, 0, stStarting, ""},
		{SourceEncoder, stReconnecting, TrigRoomFinished, 0, stReconnecting, ""},
		{SourceEncoder, stLive, TrigRoomFinished, 0, stEnded, ReasonRoomFinished},
		{SourceEncoder, stLive, TrigHostEnd, 0, stEnded, ReasonHostEnded},
		{SourceEncoder, stStarting, TrigHostTrack, 0, stLive, ""},
	}
	for _, tc := range cases {
		d, ok := decide(row(tc.source, tc.status), tc.trig, tc.age, lim)
		if !ok || d.To != tc.to || d.Reason != tc.reason {
			t.Errorf("decide(%s %s, trig %d, %s) = %+v,%v want %s/%s", tc.source, tc.status, tc.trig, tc.age, d, ok, tc.to, tc.reason)
		}
	}
}

// TestRoomClosingDoesNotEndAWaitingEncoderStream: LiveKit closes the empty
// room before the encoder connects, and again after it drops; the stream
// keeps waiting for its own timeouts.
func TestRoomClosingDoesNotEndAWaitingEncoderStream(t *testing.T) {
	host := uuid.New()
	r := newRig(host)
	st := encoderStream(t, r, host)
	_, _ = r.svc.CreateIngress(ctx, st.ID, host)
	_, _ = r.svc.StartStream(ctx, st.ID, host)
	finished := func() {
		_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "room_finished", Room: st.LiveKitRoom})
	}
	finished()
	mustStatus(t, r, st.ID, stStarting, "")
	_ = r.svc.HandleWebhook(ctx, encoderEvent(st, "track_published"))
	mustStatus(t, r, st.ID, stLive, "")
	_ = r.svc.HandleWebhook(ctx, encoderEvent(st, "participant_left"))
	finished()
	mustStatus(t, r, st.ID, stReconnecting, "")
	r.clock.Advance(61 * time.Second)
	_ = r.svc.Sweep(ctx)
	mustStatus(t, r, st.ID, stEnded, ReasonHostLost)

	// A device stream is unchanged: the room closing ends it.
	dev := r.store.AddStreamStatus(host, stStarting)
	_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "room_finished", Room: dev.LiveKitRoom})
	mustStatus(t, r, dev.ID, stEnded, ReasonRoomFinished)
}

// TestIngressDeletedAtEnd: every way an encoder stream ends deletes its
// ingress; a delete LiveKit refuses is retried by the sweeper.
func TestIngressDeletedAtEnd(t *testing.T) {
	host := uuid.New()
	r := newRig(host)

	gone := func(st *postgres.LiveStream, how string) {
		t.Helper()
		row := r.store.Stream(st.ID)
		if row.IngressID != nil {
			t.Fatalf("%s: the row still has ingress %s", how, *row.IngressID)
		}
		if _, held := r.lk.ingresses[*st.IngressID]; held {
			t.Fatalf("%s: LiveKit still has ingress %s", how, *st.IngressID)
		}
	}

	a := goLiveWithEncoder(t, r, host)
	ended, err := r.svc.EndStream(ctx, a.ID, host)
	if err != nil {
		t.Fatal(err)
	}
	gone(a, "host end")
	if ended.HasIngress == nil || *ended.HasIngress {
		t.Fatalf("the end answer says has_ingress = %v", ended.HasIngress)
	}

	b := goLiveWithEncoder(t, r, host)
	_ = r.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "room_finished", Room: b.LiveKitRoom})
	mustStatus(t, r, b.ID, stEnded, ReasonRoomFinished)
	gone(b, "room finished")

	c := goLiveWithEncoder(t, r, host)
	if _, err := r.svc.AdminStopStream(ctx, uuid.New(), c.ID, "violence"); err != nil {
		t.Fatal(err)
	}
	gone(c, "admin stop")

	// Ended before it ever started.
	d := encoderStream(t, r, host)
	_, _ = r.svc.CreateIngress(ctx, d.ID, host)
	d = r.store.Stream(d.ID)
	if _, err := r.svc.EndStream(ctx, d.ID, host); err != nil {
		t.Fatal(err)
	}
	gone(d, "ended while scheduled")

	// LiveKit refuses the delete: the stream still ends, the row keeps the
	// ingress id, and the sweeper deletes it once LiveKit answers.
	e := goLiveWithEncoder(t, r, host)
	r.lk.ingressDeleteErr = errors.New("livekit unreachable")
	if _, err := r.svc.EndStream(ctx, e.ID, host); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, r, e.ID, stEnded, ReasonHostEnded)
	if row := r.store.Stream(e.ID); row.IngressID == nil {
		t.Fatal("a failed delete forgot the ingress id")
	}
	_ = r.svc.Sweep(ctx)
	if row := r.store.Stream(e.ID); row.IngressID == nil {
		t.Fatal("the ingress id was cleared while LiveKit still refused")
	}
	r.lk.ingressDeleteErr = nil
	_ = r.svc.Sweep(ctx)
	gone(e, "sweeper retry")
}
