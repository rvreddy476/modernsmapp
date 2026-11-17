//go:build integration

package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Migration 035 against the real schema (CI only, on a _test database;
// leasePool enforces it): drains, routed classes and the per-asset lock.

// REG-2: a drain is a finish plus one drain, and a new request resets it.
func TestTranscodeDrainCountsAsAFinishAndResetsPerRequest(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()
	id, _ := seedLeaseAsset(t, pool, nil, 0, 0, 0)

	if _, _, err := store.BeginTranscodeDelivery(ctx, id, "ev-a"); err != nil {
		t.Fatal(err)
	}
	if err := store.DrainTranscodeDelivery(ctx, id, "ev-a"); err != nil {
		t.Fatal(err)
	}
	// The kubelet killed it; the next start finds nothing lost.
	if s, f, err := store.BeginTranscodeDelivery(ctx, id, "ev-a"); err != nil || s != 2 || f != 1 {
		t.Fatalf("start after a drain = %d/%d %v, want 2/1 (no lost run)", s, f, err)
	}
	var drains int
	if err := pool.QueryRow(ctx, `SELECT transcode_delivery_drains FROM media_assets WHERE id = $1`, id).Scan(&drains); err != nil || drains != 1 {
		t.Fatalf("drains = %d %v, want 1", drains, err)
	}
	// Another request's drain does not count; a new request resets drains.
	if err := store.DrainTranscodeDelivery(ctx, id, "ev-other"); err != nil {
		t.Fatal(err)
	}
	if s, f, err := store.BeginTranscodeDelivery(ctx, id, "ev-b"); err != nil || s != 1 || f != 0 {
		t.Fatalf("new request = %d/%d %v", s, f, err)
	}
	if err := pool.QueryRow(ctx, `SELECT transcode_delivery_drains FROM media_assets WHERE id = $1`, id).Scan(&drains); err != nil || drains != 0 {
		t.Fatalf("drains after a new request = %d %v, want 0", drains, err)
	}
	if err := store.DrainTranscodeDelivery(ctx, uuid.New(), "ev-x"); err != nil {
		t.Fatalf("drain of a gone asset = %v, want a no-op", err)
	}
}

// N-1: a long video waiting does not age the short class, and a request
// nothing has routed yet (or routed under an older event) is short.
func TestOldestTranscodeAgesAreSplitByRoutedClass(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()
	long, longEv := seedLeaseAsset(t, pool, nil, 2*time.Hour, 0, 0)
	reel, reelEv := seedLeaseAsset(t, pool, nil, 3*time.Minute, 0, 0)
	stale, _ := seedLeaseAsset(t, pool, nil, 5*time.Minute, 0, 0)
	if err := store.RecordTranscodeClass(ctx, long, longEv, "long"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTranscodeClass(ctx, reel, reelEv, "short"); err != nil {
		t.Fatal(err)
	}
	// Routed long under an OLDER request; its current request is unrouted.
	if err := store.RecordTranscodeClass(ctx, stale, "ev-previous", "long"); err != nil {
		t.Fatal(err)
	}
	ages, err := store.OldestTranscodeAgesByClass(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if q := ages.Queued["long"]; q < 7100 || q > 7300 {
		t.Fatalf("long queued = %v, want about 7200", q)
	}
	if q := ages.Queued["short"]; q < 290 || q > 400 {
		t.Fatalf("short queued = %v, want about 300 (the unrouted re-queue, not the 2 h long video)", q)
	}
	if ages.Pending["long"] <= 0 || ages.Pending["short"] <= 0 {
		t.Fatalf("pending = %v, want both classes", ages.Pending)
	}
}

// REG-9: two requests of one asset never run at once; other assets do.
func TestTranscodeMediaLockSerialisesOneAsset(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()
	id := uuid.New()

	inFirst := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = store.WithTranscodeMediaLock(ctx, id, func() error {
			close(inFirst)
			<-release
			return nil
		})
	}()
	<-inFirst

	// Another asset is not held up.
	other := make(chan struct{})
	go func() { _ = store.WithTranscodeMediaLock(ctx, uuid.New(), func() error { close(other); return nil }) }()
	select {
	case <-other:
	case <-time.After(5 * time.Second):
		t.Fatal("another asset waited on this asset's lock")
	}

	// The reprocess of the same asset waits for the first run.
	second := make(chan struct{})
	go func() { _ = store.WithTranscodeMediaLock(ctx, id, func() error { close(second); return nil }) }()
	select {
	case <-second:
		t.Fatal("a second run of the same asset ran while the first held it")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the second run never got the lock")
	}
}
