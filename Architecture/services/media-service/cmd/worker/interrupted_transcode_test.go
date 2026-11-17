package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync/atomic"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// lv-5 (2026-11-18): the worker is interrupted mid-transcode at the layer the
// worker really runs — processTranscodeLocked (delivery accounting, lease,
// completion) around runRungs (the durable rung pipeline), with only ffmpeg
// faked. Once by a crash (the process dies: its context ends and nothing it
// writes afterwards lands) and once by a SIGTERM drain. The next delivery
// must reuse every finished rung, encode each remaining one exactly once,
// end with the same result as a run never interrupted, and count exactly one
// lost worker for the crash and none for the drain.

// crashStore drops every write once the process has "died".
type crashStore struct {
	*fakeLockedStore
	dead *atomic.Bool
}

func (c *crashStore) FinishTranscodeDelivery(ctx context.Context, id uuid.UUID, ev string) error {
	if c.dead.Load() {
		return errors.New("process gone")
	}
	return c.fakeLockedStore.FinishTranscodeDelivery(ctx, id, ev)
}
func (c *crashStore) DrainTranscodeDelivery(ctx context.Context, id uuid.UUID, ev string) error {
	if c.dead.Load() {
		return errors.New("process gone")
	}
	return c.fakeLockedStore.DrainTranscodeDelivery(ctx, id, ev)
}

// pipelineRun is transcodeVideo's rung step over fakes; *out gets the result.
func pipelineRun(t *testing.T, blobs *memBlobs, enc *fakeEncoder, draining <-chan struct{}, out **rungResult) transcodeRun {
	return func(ctx context.Context, s *transcodeSubject, _ *jobBudget, mod *string) error {
		rr := &rungRun{
			mediaID: s.ID, baseKey: fmt.Sprintf("user/%s/%s", s.UploaderID, s.ID), generation: 2,
			sourceETag: s.OriginalETag, input: "/in/original", scratch: t.TempDir(),
			meta:   &processing.VideoMeta{Height: 1080, Width: 1920, DurationFloat: 3600},
			jobIDs: map[string]uuid.UUID{},
			stopping: func() bool {
				select {
				case <-draining:
					return true
				default:
					return false
				}
			},
		}
		res, err := runRungs(ctx, rr, blobs, &fakeJobs{}, enc)
		if err != nil {
			return err
		}
		*out, *mod = res, "passed"
		return nil
	}
}

func deliver(ctx context.Context, store lockedStore, mediaID uuid.UUID, run transcodeRun) error {
	return processTranscodeLocked(ctx, events.EventEnvelope{EventID: "ev-1"},
		postgres.TranscodeRequestPayload{MediaGeneration: 2}, mediaID, store, &prefixBlobs{}, run)
}

// resultShape is a run's result in a comparable form.
func resultShape(res *rungResult) string {
	var vs []string
	for _, v := range res.variants {
		vs = append(vs, fmt.Sprintf("%s=%s:%d", v.Name, v.ObjectKey, *v.SizeBytes))
	}
	sort.Strings(vs)
	var keys []string
	for k := range res.hlsKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%v|%v|%v|%s", vs, res.hls, keys, res.blurHash)
}

func TestInterruptedTranscodeResumesFromItsDurableRungs(t *testing.T) {
	allUnits := []string{"thumb_150", "360p", "480p", "720p", "1080p", storyboardUnit}
	for _, mode := range []string{"crash", "drain"} {
		t.Run(mode, func(t *testing.T) {
			asset := videoAsset()

			// The reference: one run, never interrupted.
			var want *rungResult
			ref := &fakeLockedStore{media: asset, goneAfter: -1}
			if err := deliver(context.Background(), ref, asset.ID,
				pipelineRun(t, newMemBlobs(), &fakeEncoder{t: t}, nil, &want)); err != nil {
				t.Fatal(err)
			}

			dead := &atomic.Bool{}
			store := &crashStore{fakeLockedStore: &fakeLockedStore{media: asset, goneAfter: -1}, dead: dead}
			blobs := newMemBlobs()
			blobs.failPut = func(string) error {
				if dead.Load() {
					return errors.New("process gone")
				}
				return nil
			}
			ctx, crash := context.WithCancel(context.Background())
			defer crash()
			draining := make(chan struct{})
			if mode == "drain" {
				ctx = withDrainSignal(ctx, draining)
			}

			// First delivery: interrupted while 720p encodes (three units done).
			var first *rungResult
			enc1 := &fakeEncoder{t: t, onEncode: func(name string) {
				if name != "720p" {
					return
				}
				if mode == "crash" {
					dead.Store(true) // nothing written from here on lands
					crash()
					return
				}
				close(draining) // SIGTERM: the rung in hand finishes, no other starts
				waitFor(t, func() bool { _, _, d := store.counts(); return d == 1 })
			}}
			err := deliver(ctx, store, asset.ID, pipelineRun(t, blobs, enc1, draining, &first))
			if err == nil || first != nil {
				t.Fatalf("interrupted run returned err=%v result=%v", err, first)
			}
			if mode == "drain" && !errors.Is(err, errWorkerDraining) {
				t.Fatalf("drain err = %v", err)
			}

			// The next delivery, on a healthy worker.
			dead.Store(false)
			lostBefore := counterValue(transcodeStarts.WithLabelValues("unknown", startAfterLostWorker))
			var second *rungResult
			enc2 := &fakeEncoder{t: t}
			if err := deliver(context.Background(), store, asset.ID, pipelineRun(t, blobs, enc2, nil, &second)); err != nil {
				t.Fatal(err)
			}

			done := map[string][]string{
				"crash": {"thumb_150", "360p", "480p"},         // 720p's upload died with the process
				"drain": {"thumb_150", "360p", "480p", "720p"}, // 720p finished inside the drain
			}[mode]
			if !slices.Equal(enc1.encoded[:len(done)], done) {
				t.Fatalf("first run encoded %v", enc1.encoded)
			}
			rest := allUnits[len(done):]
			if !slices.Equal(enc2.encoded, rest) {
				t.Fatalf("resumed run encoded %v, want each remaining unit once: %v", enc2.encoded, rest)
			}
			if !slices.Equal(second.reused, done) {
				t.Fatalf("resumed run reused %v, want %v", second.reused, done)
			}
			if got, w := resultShape(second), resultShape(want); got != w {
				t.Fatalf("resumed result differs from an uninterrupted run:\n got %s\nwant %s", got, w)
			}
			lost := counterValue(transcodeStarts.WithLabelValues("unknown", startAfterLostWorker)) - lostBefore
			if wantLost := map[string]float64{"crash": 1, "drain": 0}[mode]; lost != wantLost {
				t.Fatalf("%s: lost-worker restarts moved %v, want %v", mode, lost, wantLost)
			}
			if s, f, _ := store.counts(); s != 2 || f != map[string]int{"crash": 1, "drain": 2}[mode] {
				t.Fatalf("%s: starts=%d finishes=%d", mode, s, f)
			}
			if len(store.completes) != 1 || store.completes[0] != "ready" {
				t.Fatalf("completes = %v, want one ready", store.completes)
			}
		})
	}
}
