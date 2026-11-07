package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// The default consumes both classes, so one dev worker still transcodes
// everything; each class reads its own topic and group.
func TestTranscodeClassesConfig(t *testing.T) {
	both := []transcodeClass{classShort, classLong}
	for v, want := range map[string][]transcodeClass{
		"":             both,
		"all":          both,
		" short,long ": both,
		"long,short":   both,
		"SHORT":        {classShort},
		"long":         {classLong},
		"short, short": {classShort},
	} {
		got, err := parseTranscodeClasses(v)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("parseTranscodeClasses(%q) = %v, %v; want %v", v, got, err, want)
		}
	}
	for _, bad := range []string{"reels", "short,", "short;long", "none"} {
		if got, err := parseTranscodeClasses(bad); err == nil {
			t.Fatalf("parseTranscodeClasses(%q) = %v, want an error", bad, got)
		}
	}
}

// Which class goes to which topic: short stays on the intake the relay
// publishes to (with the consumer group it always had), long has its own.
func TestTranscodeClassRouting(t *testing.T) {
	if longTopic != "media.events.long" || intakeTopic != "media.events" {
		t.Fatalf("topics %q / %q", intakeTopic, longTopic)
	}
	if topic, group := consumerFor(classShort); topic != "media.events" || group != "media-transcode-worker" {
		t.Fatalf("short consumes %s as %s", topic, group)
	}
	if topic, group := consumerFor(classLong); topic != "media.events.long" || group != "media-transcode-worker-long" {
		t.Fatalf("long consumes %s as %s", topic, group)
	}
	if topicForClass(classShort) != intakeTopic || topicForClass(classLong) != longTopic {
		t.Fatal("a class is routed to the other class's topic")
	}
	reel := float64(processing.ReelMaxDurationSeconds)
	for seconds, want := range map[float64]transcodeClass{
		3:          classShort,
		reel:       classShort, // the shorts boundary is inclusive
		reel + 0.5: classLong,
		4 * 3600:   classLong,
		0:          classLong, // unknown
		-1:         classLong,
	} {
		if got := classForDuration(seconds); got != want {
			t.Fatalf("classForDuration(%v) = %s, want %s", seconds, got, want)
		}
	}
}

// ── the router ────────────────────────────────────────────────────────

type routerStore struct {
	m   *postgres.MediaAsset
	err error
}

func (s routerStore) GetMedia(context.Context, uuid.UUID) (*postgres.MediaAsset, error) {
	return s.m, s.err
}

// routerBlobs serves one object; downloads counts bytes read.
type routerBlobs struct {
	data  []byte
	read  int
	stats int
}

func (b *routerBlobs) StatObject(context.Context, string) (blob.ObjectInfo, error) {
	b.stats++
	if b.data == nil {
		return blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	return blob.ObjectInfo{Size: int64(len(b.data))}, nil
}

func (b *routerBlobs) ReadObjectRange(_ context.Context, _ string, start, end int64) ([]byte, error) {
	if end >= int64(len(b.data)) {
		end = int64(len(b.data)) - 1
	}
	b.read += int(end - start + 1)
	return b.data[start : end+1], nil
}

type routerWriter struct {
	msgs []kafka.Message
	err  error
}

func (w *routerWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	if w.err != nil {
		return w.err
	}
	w.msgs = append(w.msgs, msgs...)
	return nil
}

// mp4Of is a faststart MP4 header (ftyp, moov/mvhd) of seconds, then an
// mdat of padding bytes.
func mp4Of(seconds uint32, padding int) []byte {
	box := func(typ string, payload []byte) []byte {
		b := make([]byte, 8, 8+len(payload))
		binary.BigEndian.PutUint32(b, uint32(8+len(payload)))
		copy(b[4:], typ)
		return append(b, payload...)
	}
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[12:], 1)
	binary.BigEndian.PutUint32(mvhd[16:], seconds)
	out := box("ftyp", []byte("isom0000"))
	out = append(out, box("moov", box("mvhd", mvhd))...)
	return append(out, box("mdat", make([]byte, padding))...)
}

func transcodeRequest(t *testing.T, mediaID uuid.UUID) kafka.Message {
	t.Helper()
	payload, _ := json.Marshal(postgres.TranscodeRequestPayload{
		MediaTranscodeRequestedPayload: events.MediaTranscodeRequestedPayload{MediaAssetID: mediaID.String()},
	})
	value, err := json.Marshal(events.EventEnvelope{EventID: "ev-1", EventType: events.MediaTranscodeRequested, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Key: []byte("ev-1"), Value: value, Headers: []kafka.Header{{Key: "h", Value: []byte("v")}}}
}

func video(durationMs int) *postgres.MediaAsset {
	m := &postgres.MediaAsset{ID: uuid.New(), FileType: "video", StorageKey: "user/u/m/original"}
	if durationMs > 0 {
		m.DurationMs = &durationMs
	}
	return m
}

// A long request is forwarded byte for byte to the long topic and never
// downloaded; a reel stays and is transcoded where it was read.
func TestShortConsumerForwardsLongRequests(t *testing.T) {
	cases := []struct {
		name        string
		store       routerStore
		blobs       *routerBlobs
		wantForward bool
	}{
		{"reel by its header", routerStore{m: video(0)}, &routerBlobs{data: mp4Of(30, 1<<20)}, false},
		{"10-minute reel (the boundary)", routerStore{m: video(0)}, &routerBlobs{data: mp4Of(600, 64)}, false},
		{"long video by its header", routerStore{m: video(0)}, &routerBlobs{data: mp4Of(2*3600, 1<<20)}, true},
		{"long video by the row (a re-run)", routerStore{m: video(25 * 60_000)}, &routerBlobs{}, true},
		{"reel by the row", routerStore{m: video(20_000)}, &routerBlobs{}, false},
		{"length unknown (WebM)", routerStore{m: video(0)}, &routerBlobs{data: append([]byte{0x1A, 0x45, 0xDF, 0xA3}, make([]byte, 60)...)}, true},
		{"original missing", routerStore{m: video(0)}, &routerBlobs{}, true},
		{"row not read", routerStore{err: errors.New("db down")}, &routerBlobs{}, true},
		{"asset gone: recorded here", routerStore{err: postgres.ErrMediaNotFound}, &routerBlobs{}, false},
		{"not a video: refused here", routerStore{m: &postgres.MediaAsset{FileType: "image"}}, &routerBlobs{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &routerWriter{}
			r := &longRouter{store: tc.store, blobs: tc.blobs, forward: w}
			m := transcodeRequest(t, uuid.New())
			got := r.forwardIfLong(context.Background(), m)
			if got != tc.wantForward || len(w.msgs) != map[bool]int{true: 1, false: 0}[tc.wantForward] {
				t.Fatalf("forwarded = %v (%d writes), want %v", got, len(w.msgs), tc.wantForward)
			}
			if got && (string(w.msgs[0].Value) != string(m.Value) || string(w.msgs[0].Key) != string(m.Key) || len(w.msgs[0].Headers) != 1) {
				t.Fatalf("forwarded message changed: %+v", w.msgs[0])
			}
			if tc.blobs.read > 4096 {
				t.Fatalf("classification read %d bytes of the original", tc.blobs.read)
			}
		})
	}
}

// A write the long topic refuses leaves the request to be transcoded here:
// slower for the reels behind it, never lost.
func TestForwardFailureTranscodesHere(t *testing.T) {
	w := &routerWriter{err: errors.New("unknown topic")}
	r := &longRouter{store: routerStore{m: video(3_600_000)}, blobs: &routerBlobs{}, forward: w}
	if r.forwardIfLong(context.Background(), transcodeRequest(t, uuid.New())) {
		t.Fatal("reported forwarded after the write failed")
	}
}

// Anything that is not a readable transcode request is the local
// handler's to decide (skip, or quarantine as permanent).
func TestRouterLeavesOtherMessagesAlone(t *testing.T) {
	w := &routerWriter{}
	r := &longRouter{store: routerStore{m: video(3_600_000)}, blobs: &routerBlobs{}, forward: w}
	other, _ := json.Marshal(events.EventEnvelope{EventID: "x", EventType: events.MediaTranscodeCompleted})
	noID, _ := json.Marshal(events.EventEnvelope{EventID: "y", EventType: events.MediaTranscodeRequested, Payload: []byte(`{"media_id":"nope"}`)})
	for _, v := range [][]byte{[]byte("not json"), other, noID} {
		if r.forwardIfLong(context.Background(), kafka.Message{Value: v}) || len(w.msgs) != 0 {
			t.Fatalf("forwarded %q", v)
		}
	}
}
