package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	dto "github.com/prometheus/client_model/go"
)

// ── REG-2: a drain is recorded before the wait and is never a lost worker ─


func (f *fakeLockedStore) counts() (starts, finishes, drains int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.finishes, f.drains
}

// The kubelet kills the run at the end of the grace period: the run never
// returns. Because the drain was recorded the moment SIGTERM arrived, the
// next start finds no lost run. Without the drain the same kill is lost.
func TestRunKilledDuringItsDrainIsNotALostWorker(t *testing.T) {
	for _, drained := range []bool{true, false} {
		store := &fakeLockedStore{media: videoAsset(), goneAfter: -1}
		id := store.media.ID
		if _, err := beginDelivery(context.Background(), store, id, "ev-1"); err != nil {
			t.Fatal(err)
		}
		draining := make(chan struct{})
		run := newDeliveryRun(store, id, "ev-1")
		run.watchDrain(context.Background(), draining)
		if drained {
			close(draining) // SIGTERM
			waitFor(t, func() bool { _, _, d := store.counts(); return d == 1 })
		}
		// SIGKILL: run.end is never called.
		lost, err := beginDelivery(context.Background(), store, id, "ev-1")
		if err != nil {
			t.Fatal(err)
		}
		if want := map[bool]int{true: 0, false: 1}[drained]; lost != want {
			t.Fatalf("drained=%v: lost = %d, want %d", drained, lost, want)
		}
	}
}

// A drained run that then returns is accounted for ONCE: counting it twice
// would hide a real lost run after it.
func TestDrainedRunThatReturnsIsCountedOnce(t *testing.T) {
	store := &fakeLockedStore{media: videoAsset(), goneAfter: -1}
	draining := make(chan struct{})
	ctx := withDrainSignal(context.Background(), draining)
	err := processTranscodeLocked(ctx, events.EventEnvelope{EventID: "ev-1"}, postgres.TranscodeRequestPayload{},
		store.media.ID, store, &prefixBlobs{},
		func(context.Context, *transcodeSubject, *jobBudget, *string) error {
			close(draining)
			waitFor(t, func() bool { _, _, d := store.counts(); return d == 1 })
			return errWorkerDraining
		})
	if !errors.Is(err, errWorkerDraining) {
		t.Fatalf("err = %v", err)
	}
	if s, f, d := store.counts(); s != 1 || f != 1 || d != 1 {
		t.Fatalf("starts=%d finishes=%d drains=%d, want 1/1/1", s, f, d)
	}
}

// A drain record that cannot be written leaves the finish to the return.
func TestUnwrittenDrainFallsBackToTheFinish(t *testing.T) {
	store := &fakeLockedStore{media: videoAsset(), goneAfter: -1, drainErr: errors.New("db down")}
	draining := make(chan struct{})
	ctx := withDrainSignal(context.Background(), draining)
	attempted := make(chan struct{})
	_ = processTranscodeLocked(ctx, events.EventEnvelope{EventID: "ev-1"}, postgres.TranscodeRequestPayload{},
		store.media.ID, store, &prefixBlobs{},
		func(context.Context, *transcodeSubject, *jobBudget, *string) error {
			close(draining)
			time.Sleep(20 * time.Millisecond) // let the drain write fail
			close(attempted)
			return errWorkerDraining
		})
	<-attempted
	if s, f, d := store.counts(); s != 1 || f != 1 || d != 0 {
		t.Fatalf("starts=%d finishes=%d drains=%d, want 1/1/0", s, f, d)
	}
}

// ── N-1: each class's gauge carries that class's own age ──────────────

type agesProbe struct{ ages postgres.TranscodeAgesByClass }

func (p agesProbe) OldestTranscodeAgesByClass(context.Context) (postgres.TranscodeAgesByClass, error) {
	return p.ages, nil
}

func gaugeValue(t *testing.T, g interface{ Write(*dto.Metric) error }) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil || m.Gauge == nil {
		t.Fatalf("gauge not readable: %v", err)
	}
	return m.Gauge.GetValue()
}

func TestQueueGaugesCarryEachClassesOwnAge(t *testing.T) {
	// A long video has waited two hours; no reel is waiting.
	refreshReadinessGauges(context.Background(), agesProbe{postgres.TranscodeAgesByClass{
		Pending: map[string]float64{"long": 9000, "short": 30},
		Queued:  map[string]float64{"long": 7200},
	}})
	if got := gaugeValue(t, oldestQueuedWait.WithLabelValues("short")); got != 0 {
		t.Fatalf("short queue wait = %v: a waiting long video would page the reel alert", got)
	}
	if got := gaugeValue(t, oldestQueuedWait.WithLabelValues("long")); got != 7200 {
		t.Fatalf("long queue wait = %v, want 7200", got)
	}
	if s, l := gaugeValue(t, oldestPendingAge.WithLabelValues("short")), gaugeValue(t, oldestPendingAge.WithLabelValues("long")); s != 30 || l != 9000 {
		t.Fatalf("pending ages short=%v long=%v, want 30 and 9000", s, l)
	}
}

type classSpy struct {
	mu  sync.Mutex
	got map[string]string // event id → class
}

func (c *classSpy) RecordTranscodeClass(_ context.Context, _ uuid.UUID, eventID, class string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.got == nil {
		c.got = map[string]string{}
	}
	c.got[eventID] = class
	return nil
}

// The router records the queue each request actually waits in.
func TestRouterRecordsTheClassItRoutedInto(t *testing.T) {
	for _, tc := range []struct {
		name  string
		m     *postgres.MediaAsset
		werr  error
		class string
	}{
		{"reel stays short", video(20_000), nil, "short"},
		{"long video forwarded", video(25 * 60_000), nil, "long"},
		{"long video not forwarded waits on short", video(25 * 60_000), errors.New("unknown topic"), "short"},
	} {
		spy := &classSpy{}
		r := &longRouter{store: routerStore{m: tc.m}, blobs: &routerBlobs{}, forward: &routerWriter{err: tc.werr}, classes: spy}
		r.forwardIfLong(context.Background(), transcodeRequest(t, uuid.New()))
		if spy.got["ev-1"] != tc.class {
			t.Fatalf("%s: recorded %q, want %q", tc.name, spy.got["ev-1"], tc.class)
		}
	}
}

// ── reels-5: a quarantined request fails its asset ───────────────────

type quarantineFake struct {
	*fakeLockedStore
	current     string
	quarantined int
	qErr        error
}

func (q *quarantineFake) QuarantineTranscode(context.Context, kafka.Message, error) error {
	if q.qErr != nil {
		return q.qErr
	}
	q.quarantined++
	return nil
}
func (q *quarantineFake) CurrentTranscodeRequest(context.Context, uuid.UUID) (string, bool, error) {
	return q.current, q.current != "", nil
}

func requestFor(t *testing.T, mediaID uuid.UUID, eventID string) kafka.Message {
	t.Helper()
	payload, _ := json.Marshal(postgres.TranscodeRequestPayload{
		MediaTranscodeRequestedPayload: events.MediaTranscodeRequestedPayload{MediaAssetID: mediaID.String()},
		MediaGeneration:                4,
	})
	v, err := json.Marshal(events.EventEnvelope{EventID: eventID, EventType: events.MediaTranscodeRequested, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Value: v}
}

func TestQuarantinedRequestFailsItsAssetWithTheReason(t *testing.T) {
	q := &quarantineFake{fakeLockedStore: &fakeLockedStore{media: videoAsset(), goneAfter: -1}}
	m := requestFor(t, q.media.ID, "ev-1")
	ok := retryUntilDurable(context.Background(), m, func() error {
		return permanentTranscode(errors.New("asset has an unexpected storage key"))
	}, quarantineAndFail(q), fastRetry)
	if !ok || q.quarantined != 1 {
		t.Fatalf("ok=%v quarantined=%d", ok, q.quarantined)
	}
	if len(q.completes) != 1 || q.completes[0] != "failed" {
		t.Fatalf("completes = %v: the asset would show processing for ever", q.completes)
	}
	if len(q.jobFailures) != 1 || !strings.Contains(q.jobFailures[0], "unexpected storage key") ||
		len(q.jobTargets) != 1 || q.jobTargets[0] != "pipeline" {
		t.Fatalf("creator-visible reason missing: %v %v", q.jobTargets, q.jobFailures)
	}
}

func TestQuarantineLeavesAloneWhatItMustNotFail(t *testing.T) {
	image := videoAsset()
	image.FileType = "image"
	cases := map[string]*quarantineFake{
		"superseded by a re-queue": {fakeLockedStore: &fakeLockedStore{media: videoAsset(), goneAfter: -1}, current: "ev-newer"},
		"not a video":              {fakeLockedStore: &fakeLockedStore{media: image, goneAfter: -1}},
		"asset gone":               {fakeLockedStore: &fakeLockedStore{media: videoAsset(), goneAfter: 0}},
	}
	for name, q := range cases {
		if err := quarantineAndFail(q)(context.Background(), requestFor(t, q.media.ID, "ev-1"), errors.New("x")); err != nil {
			t.Fatalf("%s: err = %v", name, err)
		}
		if q.quarantined != 1 || len(q.completes) != 0 {
			t.Fatalf("%s: quarantined=%d completes=%v, want the row only", name, q.quarantined, q.completes)
		}
	}
	// No media id at all: the row is the whole record.
	q := &quarantineFake{fakeLockedStore: &fakeLockedStore{media: videoAsset(), goneAfter: -1}}
	if err := quarantineAndFail(q)(context.Background(), kafka.Message{Value: []byte("{not json")}, errors.New("x")); err != nil || q.quarantined != 1 || len(q.completes) != 0 {
		t.Fatalf("unreadable message: err=%v quarantined=%d completes=%v", err, q.quarantined, q.completes)
	}
}

// Neither write may be skipped: a failure of either keeps the offset.
func TestQuarantineThatCannotFailTheAssetDoesNotCommit(t *testing.T) {
	q := &quarantineFake{fakeLockedStore: &fakeLockedStore{media: videoAsset(), goneAfter: -1, complete: errors.New("db down")}}
	if err := quarantineAndFail(q)(context.Background(), requestFor(t, q.media.ID, "ev-1"), errors.New("x")); err == nil {
		t.Fatal("an unrecorded failure was reported durable")
	}
	q2 := &quarantineFake{fakeLockedStore: &fakeLockedStore{media: videoAsset(), goneAfter: -1}, qErr: errors.New("db down")}
	if err := quarantineAndFail(q2)(context.Background(), requestFor(t, q2.media.ID, "ev-1"), errors.New("x")); err == nil || len(q2.completes) != 0 {
		t.Fatalf("err=%v completes=%v: the row must be durable first", err, q2.completes)
	}
	// Already applied / stale generation: the asset's state is someone else's.
	for _, e := range []error{postgres.ErrTranscodeAlreadyApplied, postgres.ErrTranscodeGenerationStale} {
		q3 := &quarantineFake{fakeLockedStore: &fakeLockedStore{media: videoAsset(), goneAfter: -1, complete: e}}
		if err := quarantineAndFail(q3)(context.Background(), requestFor(t, q3.media.ID, "ev-1"), errors.New("x")); err != nil {
			t.Fatalf("%v: err = %v", e, err)
		}
	}
}

// ── REG-9: one asset, one transcode at a time ────────────────────────

type mediaLockSpy struct{ ids []uuid.UUID }

func (l *mediaLockSpy) WithTranscodeMediaLock(_ context.Context, id uuid.UUID, fn func() error) error {
	l.ids = append(l.ids, id)
	return fn()
}

// Two requests of one asset (an upload and its reprocess) take the SAME lock.
func TestTranscodeLockIsTheAssetsNotTheEvents(t *testing.T) {
	id := uuid.New()
	l := &mediaLockSpy{}
	ran := 0
	for range 2 {
		if err := withTranscodeLock(context.Background(), l, id, func() error { ran++; return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if ran != 2 || len(l.ids) != 2 || l.ids[0] != id || l.ids[1] != id {
		t.Fatalf("ran=%d locks=%v, want both runs under media %s", ran, l.ids, id)
	}
	// The same asset parsed afresh (another delivery's payload) gets the same
	// key; another asset gets a different one.
	if postgres.TranscodeMediaLockKey(id) != postgres.TranscodeMediaLockKey(uuid.MustParse(id.String())) ||
		postgres.TranscodeMediaLockKey(id) == postgres.TranscodeMediaLockKey(uuid.New()) {
		t.Fatal("lock key is not per asset")
	}
}
