package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakeTakedownStore struct {
	keys []string
	err  error
}

func (f *fakeTakedownStore) TakeDownSound(context.Context, uuid.UUID) ([]string, error) {
	return f.keys, f.err
}

// A takedown deletes the objects it recorded now, and names its refusals.
func TestTakeDownSoundReclaimsAndMapsRefusals(t *testing.T) {
	var reclaimed []string
	reclaim := func(_ context.Context, keys []string) { reclaimed = append(reclaimed, keys...) }
	n, err := takeDownSound(context.Background(), &fakeTakedownStore{keys: []string{"audio/u/m/audio.m4a", "audio/u/m/waveform.json"}}, reclaim, uuid.New())
	if err != nil || n != 2 || len(reclaimed) != 2 {
		t.Fatalf("takedown: n=%d err=%v reclaimed=%v", n, err, reclaimed)
	}
	for storeErr, want := range map[error]error{
		postgres.ErrSoundNotFound:        ErrSoundNotFound,
		postgres.ErrLibrarySoundTakedown: ErrLibrarySoundTakedown,
	} {
		reclaimed = nil
		if _, err := takeDownSound(context.Background(), &fakeTakedownStore{err: storeErr}, reclaim, uuid.New()); !errors.Is(err, want) {
			t.Fatalf("store %v: err = %v, want %v", storeErr, err, want)
		}
		if len(reclaimed) != 0 {
			t.Fatal("a refused takedown reclaimed objects")
		}
	}
	if _, err := takeDownSound(context.Background(), &fakeTakedownStore{err: errors.New("db down")}, reclaim, uuid.New()); err == nil ||
		errors.Is(err, ErrSoundNotFound) {
		t.Fatalf("a store fault must be a fault: %v", err)
	}
}

type fakeSweepStore struct {
	grace time.Duration
	limit int
	keys  []string
	err   error
}

func (f *fakeSweepStore) ReclaimUnusedRetiredSounds(_ context.Context, grace time.Duration, limit int) ([]string, error) {
	f.grace, f.limit = grace, limit
	return f.keys, f.err
}

// The sweep asks with the grace and the batch, and a failure costs the tick
// nothing (the reclaim drain still runs).
func TestRetiredSoundSweepUsesGraceAndBatch(t *testing.T) {
	store := &fakeSweepStore{keys: []string{"a", "b"}}
	if n := reclaimUnusedRetiredSounds(context.Background(), store); n != 2 {
		t.Fatalf("queued %d, want 2", n)
	}
	if store.grace != retiredSoundGrace || store.grace < time.Hour || store.limit != retiredSoundBatch {
		t.Fatalf("asked with grace %v limit %d", store.grace, store.limit)
	}
	if n := reclaimUnusedRetiredSounds(context.Background(), &fakeSweepStore{err: errors.New("down")}); n != 0 {
		t.Fatalf("a failed sweep queued %d", n)
	}
}

// A retired sound is never in a list (search, trending ask admitToTrack)
// and never usable, even though its row is still 'ready'-shaped otherwise.
func TestRetiredSoundIsNeverListedOrUsed(t *testing.T) {
	ctx := context.Background()
	retired := &postgres.AudioTrack{ID: uuid.New(), Title: "x retired", AudioKey: "audio/u/m/audio.m4a", Status: postgres.SoundStatusRetired}
	ok := librarySound("x ok", "")
	store := &reelSoundStore{tracks: map[uuid.UUID]*postgres.AudioTrack{retired.ID: retired, ok.ID: ok}}
	r := NewRecordReads(store, nil, nil)

	got, err := r.SearchAudioForViewer(ctx, uuid.Nil, "x", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.ID == retired.ID {
			t.Fatal("search listed a retired sound")
		}
	}
	if err := r.admitToTrack(ctx, uuid.New(), retired); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("trending/list admission of a retired sound: %v, want denied", err)
	}
	if err := r.UseAudioTrackAsViewer(ctx, uuid.New(), retired.ID); !errors.Is(err, delivery.ErrDeliveryDenied) || store.uses != 0 {
		t.Fatalf("use of a retired sound: %v (uses %d)", err, store.uses)
	}
	// Through a post the store does not show as carrying it: still denied.
	if _, err := r.AudioTrackForViewerVia(ctx, uuid.New(), retired.ID, uuid.New()); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("retired sound through a non-carrier: %v", err)
	}
}
