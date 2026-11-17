package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// lv-6: a run that dies (OOM kill, eviction, crash) returns nothing, so
// what it leaves behind is a counted start with no finish. Three of those
// and the next delivery is recorded failed, with the reason, and committed.
func TestLostWorkersPastTheCapAreRecordedFailed(t *testing.T) {
	m := videoAsset()
	store := &fakeLockedStore{media: m, goneAfter: -1}
	for i := 0; i < defaultMaxLostWorkers; i++ { // three runs killed mid-job
		if _, _, err := store.BeginTranscodeDelivery(context.Background(), m.ID, "ev-1"); err != nil {
			t.Fatal(err)
		}
	}
	before := counterValue(transcodeQuarantined.WithLabelValues("unknown", quarantineLostWorker))
	err := lockedCall(store, &prefixBlobs{}, neverRunLost(t))
	if err != nil {
		t.Fatalf("err = %v, want nil (terminal failure recorded, offset committed)", err)
	}
	if len(store.completes) != 1 || store.completes[0] != "failed" {
		t.Fatalf("completes = %v, want one failed", store.completes)
	}
	if len(store.jobFailures) != 1 || !strings.Contains(store.jobFailures[0], "lost its worker") ||
		len(store.jobTargets) != 1 || store.jobTargets[0] != "pipeline" {
		t.Fatalf("reason not surfaced to the creator: targets=%v failures=%v", store.jobTargets, store.jobFailures)
	}
	if got := counterValue(transcodeQuarantined.WithLabelValues("unknown", quarantineLostWorker)) - before; got != 1 {
		t.Fatalf("lost_worker quarantine counter moved %v, want 1", got)
	}
}

func neverRunLost(t *testing.T) transcodeRun {
	return func(context.Context, *transcodeSubject, *jobBudget, *string) error {
		t.Fatal("transcode ran past the lost-worker cap")
		return nil
	}
}

// Below the cap the job still runs; the restart is counted.
func TestLostWorkersBelowTheCapStillRun(t *testing.T) {
	m := videoAsset()
	store := &fakeLockedStore{media: m, goneAfter: -1}
	for i := 0; i < defaultMaxLostWorkers-1; i++ {
		_, _, _ = store.BeginTranscodeDelivery(context.Background(), m.ID, "ev-1")
	}
	before := counterValue(transcodeStarts.WithLabelValues("unknown", startAfterLostWorker))
	ran := false
	_ = lockedCall(store, &prefixBlobs{}, func(_ context.Context, _ *transcodeSubject, _ *jobBudget, mod *string) error {
		ran, *mod = true, "passed"
		return nil
	})
	if !ran {
		t.Fatal("job below the cap did not run")
	}
	if got := counterValue(transcodeStarts.WithLabelValues("unknown", startAfterLostWorker)) - before; got != 1 {
		t.Fatalf("after_lost_worker starts moved %v, want 1", got)
	}
}

// A run that RETURNS (success, or a transient error the retry loop will
// retry) is finished, never lost: in-process retries do not use up the cap.
func TestReturnedRunsAreNeverCountedLost(t *testing.T) {
	m := videoAsset()
	store := &fakeLockedStore{media: m, goneAfter: -1}
	for i := 0; i < 2*defaultMaxLostWorkers; i++ {
		err := lockedCall(store, &prefixBlobs{}, func(context.Context, *transcodeSubject, *jobBudget, *string) error {
			return errors.New("scanner unavailable")
		})
		if err == nil || isPermanentTranscodeFailure(err) {
			t.Fatalf("run %d: err = %v, want a transient failure", i, err)
		}
	}
	if store.starts != 2*defaultMaxLostWorkers || store.finishes != store.starts {
		t.Fatalf("starts=%d finishes=%d, want every start finished", store.starts, store.finishes)
	}
	if len(store.completes) != 0 {
		t.Fatalf("a retried transient failure was recorded terminal: %v", store.completes)
	}
}

// The start is counted before any work: a failed count starts nothing.
func TestUncountedStartRunsNothing(t *testing.T) {
	store := &fakeLockedStore{media: videoAsset(), goneAfter: -1, beginErr: errors.New("db down")}
	err := lockedCall(store, &prefixBlobs{}, neverRunLost(t))
	if err == nil || isPermanentTranscodeFailure(err) {
		t.Fatalf("err = %v, want transient", err)
	}
}

// A clean SIGTERM drain is a finished run, and not a failure.
func TestDrainedRunIsFinishedAndTransient(t *testing.T) {
	store := &fakeLockedStore{media: videoAsset(), goneAfter: -1}
	err := lockedCall(store, &prefixBlobs{}, func(context.Context, *transcodeSubject, *jobBudget, *string) error {
		return errWorkerDraining
	})
	if !errors.Is(err, errWorkerDraining) || len(store.completes) != 0 {
		t.Fatalf("err=%v completes=%v, want the drain passed up, nothing recorded", err, store.completes)
	}
	if store.starts != 1 || store.finishes != 1 {
		t.Fatalf("starts=%d finishes=%d: a drained run must not count as lost", store.starts, store.finishes)
	}
}

func TestLostRuns(t *testing.T) {
	for _, c := range []struct{ starts, finishes, want int }{
		{1, 0, 0}, {2, 0, 1}, {4, 1, 2}, {3, 2, 0}, {2, 5, 0},
	} {
		if got := lostRuns(c.starts, c.finishes); got != c.want {
			t.Errorf("lostRuns(%d,%d) = %d, want %d", c.starts, c.finishes, got, c.want)
		}
	}
}

// counterValue reads one counter (client_golang's testutil is not vendored).
func counterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	if err := c.Write(&m); err != nil || m.Counter == nil {
		return 0
	}
	return m.Counter.GetValue()
}
