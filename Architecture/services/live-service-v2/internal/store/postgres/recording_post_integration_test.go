package postgres

// The recording post lookup against a real Postgres (2 Oct 2026; migration
// 006). Same rules as integration_test.go: LIVE_V2_TEST_DSN, a database
// whose name ends in _test.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// itRecorded makes an ended stream with a recording whose import is in the
// given state ("done" announces vod_ready).
func itRecorded(t *testing.T, s *Store, state string) *LiveStream {
	t.Helper()
	ctx := context.Background()
	st := itStream(t, s, uuid.New())
	if _, err := s.SetRecording(ctx, st.ID, "https://s3/live-recordings/recordings/"+st.ID.String()+".mp4", 90,
		RecordingImport{Bucket: "live-recordings", ObjectKey: "recordings/" + st.ID.String() + ".mp4", DurationMs: 90000}); err != nil {
		t.Fatal(err)
	}
	switch state {
	case ImportDone:
		if err := s.CompleteImport(ctx, st.ID, uuid.New(), func(*LiveStream, RecordingImport) ([]OutboxEvent, error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
	case ImportFailed:
		if err := s.TerminateImport(ctx, st.ID, nil, "failed", "it"); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func itPostID(t *testing.T, s *Store, id uuid.UUID) *uuid.UUID {
	t.Helper()
	st, err := s.GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return st.RecordingPostID
}

func itLookupState(t *testing.T, s *Store, id uuid.UUID) (string, int) {
	t.Helper()
	state, attempts, err := s.PostLookupState(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return state, attempts
}

// itClaimed claims everything due and reports the claim for id, if any.
func itClaimed(t *testing.T, s *Store, id uuid.UUID) (PostLookup, bool) {
	t.Helper()
	due, err := s.ClaimDuePostLookups(context.Background(), 100000, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range due {
		if l.StreamID == id {
			return l, true
		}
	}
	return PostLookup{}, false
}

// TestIntegrationSetRecordingPostIsGuarded: the id is written once, only
// while recording_post_id is NULL and the lookup is still pending.
func TestIntegrationSetRecordingPostIsGuarded(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()

	st := itRecorded(t, s, ImportDone)
	if state, attempts := itLookupState(t, s, st.ID); state != PostLookupPending || attempts != 0 {
		t.Fatalf("a fresh job: %s %d", state, attempts)
	}
	first, other := uuid.New(), uuid.New()
	stored, err := s.SetRecordingPost(ctx, st.ID, first)
	if err != nil || !stored {
		t.Fatalf("first: stored=%v err=%v", stored, err)
	}
	if got := itPostID(t, s, st.ID); got == nil || *got != first {
		t.Fatalf("recording_post_id = %v", got)
	}
	if state, _ := itLookupState(t, s, st.ID); state != PostLookupFound {
		t.Fatalf("state = %s", state)
	}
	// A second answer does not replace the first.
	stored, err = s.SetRecordingPost(ctx, st.ID, other)
	if err != nil || stored {
		t.Fatalf("second: stored=%v err=%v", stored, err)
	}
	if got := itPostID(t, s, st.ID); got == nil || *got != first {
		t.Fatalf("recording_post_id was overwritten: %v", got)
	}

	// The NULL guard on its own: an id already on the row while the lookup
	// is still pending is kept, and the lookup is settled.
	preset := itRecorded(t, s, ImportDone)
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET recording_post_id = $2 WHERE id = $1`, preset.ID, first); err != nil {
		t.Fatal(err)
	}
	stored, err = s.SetRecordingPost(ctx, preset.ID, other)
	if err != nil || stored {
		t.Fatalf("preset: stored=%v err=%v", stored, err)
	}
	if got := itPostID(t, s, preset.ID); got == nil || *got != first {
		t.Fatalf("a stored id was overwritten: %v", got)
	}
	if state, _ := itLookupState(t, s, preset.ID); state != PostLookupFound {
		t.Fatalf("preset state = %s", state)
	}

	// The pending guard on its own: a lookup stopped because the post was
	// deleted is not reopened by a late answer.
	stopped := itRecorded(t, s, ImportDone)
	if err := s.StopPostLookup(ctx, stopped.ID, PostLookupDeleted); err != nil {
		t.Fatal(err)
	}
	stored, err = s.SetRecordingPost(ctx, stopped.ID, other)
	if err != nil || stored || itPostID(t, s, stopped.ID) != nil {
		t.Fatalf("a stopped lookup stored an id: stored=%v err=%v id=%v", stored, err, itPostID(t, s, stopped.ID))
	}

	// An import that is not done announced no recording: nothing is stored.
	for _, state := range []string{ImportPending, ImportFailed} {
		st := itRecorded(t, s, state)
		stored, err := s.SetRecordingPost(ctx, st.ID, other)
		if err != nil || stored || itPostID(t, s, st.ID) != nil {
			t.Fatalf("%s import: stored=%v err=%v id=%v", state, stored, err, itPostID(t, s, st.ID))
		}
	}
	// No import job at all.
	bare := itStream(t, s, uuid.New())
	if stored, err := s.SetRecordingPost(ctx, bare.ID, other); err != nil || stored || itPostID(t, s, bare.ID) != nil {
		t.Fatalf("no job: stored=%v err=%v", stored, err)
	}
}

// TestIntegrationPostLookupClaims: only finished imports are due; a claim
// leases the row; a retry schedules the next one and counts; the age is
// measured from when the import finished.
func TestIntegrationPostLookupClaims(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()

	done := itRecorded(t, s, ImportDone)
	pending := itRecorded(t, s, ImportPending)
	failed := itRecorded(t, s, ImportFailed)
	if _, err := pool.Exec(ctx, `UPDATE live_recording_imports SET done_at = NOW() - INTERVAL '25 hours' WHERE stream_id = $1`, done.ID); err != nil {
		t.Fatal(err)
	}

	due, err := s.ClaimDuePostLookups(ctx, 100000, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var mine *PostLookup
	for i, l := range due {
		if l.StreamID == pending.ID || l.StreamID == failed.ID {
			t.Fatalf("an import that is not done was claimed: %+v", l)
		}
		if l.StreamID == done.ID {
			mine = &due[i]
		}
	}
	if mine == nil || mine.Attempts != 0 || mine.Age < 25*time.Hour || mine.Age > 26*time.Hour {
		t.Fatalf("claim: %+v", mine)
	}
	if _, again := itClaimed(t, s, done.ID); again {
		t.Fatal("claimed again inside the lease")
	}

	// A retry in the past is due at once and counts an attempt; one in the
	// future is not due.
	if err := s.RetryPostLookup(ctx, done.ID, -time.Second); err != nil {
		t.Fatal(err)
	}
	l, ok := itClaimed(t, s, done.ID)
	if !ok || l.Attempts != 1 {
		t.Fatalf("after a retry: %+v %v", l, ok)
	}
	if err := s.RetryPostLookup(ctx, done.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, ok := itClaimed(t, s, done.ID); ok {
		t.Fatal("claimed inside the backoff")
	}
	if _, attempts := itLookupState(t, s, done.ID); attempts != 2 {
		t.Fatalf("attempts = %d", attempts)
	}

	// The limit bounds one claim.
	a, b := itRecorded(t, s, ImportDone), itRecorded(t, s, ImportDone)
	one, err := s.ClaimDuePostLookups(ctx, 1, time.Minute)
	if err != nil || len(one) != 1 {
		t.Fatalf("limit 1: %d %v", len(one), err)
	}
	_, _ = a, b

	// Settled lookups are never due.
	if err := itForceDue(pool, done.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.StopPostLookup(ctx, done.ID, PostLookupGaveUp); err != nil {
		t.Fatal(err)
	}
	if _, ok := itClaimed(t, s, done.ID); ok {
		t.Fatal("a lookup that gave up was claimed")
	}
}

func itForceDue(pool *pgxpool.Pool, id uuid.UUID) error {
	_, err := pool.Exec(context.Background(),
		`UPDATE live_recording_imports SET post_lookup_next_at = NOW() - INTERVAL '1 second', post_lookup_last_at = NULL WHERE stream_id = $1`, id)
	return err
}

// TestIntegrationPostLookupReadClaim: the detail read's claim is granted at
// most once per gap, only while pending, and leaves the sweeper's schedule
// alone.
func TestIntegrationPostLookupReadClaim(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()

	st := itRecorded(t, s, ImportDone)
	if _, ok, err := s.ClaimPostLookup(ctx, st.ID, 10*time.Second); err != nil || !ok {
		t.Fatalf("first read: %v %v", ok, err)
	}
	if _, ok, err := s.ClaimPostLookup(ctx, st.ID, 10*time.Second); err != nil || ok {
		t.Fatalf("second read inside the gap: %v %v", ok, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE live_recording_imports SET post_lookup_last_at = NOW() - INTERVAL '11 seconds' WHERE stream_id = $1`, st.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimPostLookup(ctx, st.ID, 10*time.Second); err != nil || !ok {
		t.Fatalf("after the gap: %v %v", ok, err)
	}
	// The sweeper still finds it due, and its claim starts a gap for reads.
	if _, ok := itClaimed(t, s, st.ID); !ok {
		t.Fatal("a read claim took the sweeper's turn")
	}
	if _, ok, _ := s.ClaimPostLookup(ctx, st.ID, 10*time.Second); ok {
		t.Fatal("a read right after the sweeper's lookup asked again")
	}

	for _, state := range []string{ImportPending, ImportFailed} {
		other := itRecorded(t, s, state)
		if _, ok, err := s.ClaimPostLookup(ctx, other.ID, 0); err != nil || ok {
			t.Fatalf("%s import: %v %v", state, ok, err)
		}
	}
	if err := s.StopPostLookup(ctx, st.ID, PostLookupDeleted); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimPostLookup(ctx, st.ID, 0); err != nil || ok {
		t.Fatalf("a stopped lookup: %v %v", ok, err)
	}
	if _, ok, err := s.ClaimPostLookup(ctx, uuid.New(), 0); err != nil || ok {
		t.Fatalf("an unknown stream: %v %v", ok, err)
	}
}

// TestIntegrationStopPostLookup: deleted clears an id stored meanwhile and
// ends the lookup; gave_up ends only a pending lookup.
func TestIntegrationStopPostLookup(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()

	found := itRecorded(t, s, ImportDone)
	post := uuid.New()
	if stored, err := s.SetRecordingPost(ctx, found.ID, post); err != nil || !stored {
		t.Fatal(stored, err)
	}
	if err := s.StopPostLookup(ctx, found.ID, PostLookupGaveUp); err != nil {
		t.Fatal(err)
	}
	if state, _ := itLookupState(t, s, found.ID); state != PostLookupFound || itPostID(t, s, found.ID) == nil {
		t.Fatalf("giving up undid a found lookup: %s %v", state, itPostID(t, s, found.ID))
	}
	if err := s.StopPostLookup(ctx, found.ID, PostLookupDeleted); err != nil {
		t.Fatal(err)
	}
	if state, _ := itLookupState(t, s, found.ID); state != PostLookupDeleted || itPostID(t, s, found.ID) != nil {
		t.Fatalf("deleted: %s %v", state, itPostID(t, s, found.ID))
	}

	pending := itRecorded(t, s, ImportDone)
	if err := s.StopPostLookup(ctx, pending.ID, PostLookupGaveUp); err != nil {
		t.Fatal(err)
	}
	if state, _ := itLookupState(t, s, pending.ID); state != PostLookupGaveUp {
		t.Fatalf("gave up: %s", state)
	}
	if err := s.StopPostLookup(ctx, pending.ID, "found"); err == nil {
		t.Fatal("an arbitrary state was accepted")
	}
	if _, _, err := s.PostLookupState(ctx, uuid.New()); err != ErrNotFound {
		t.Fatalf("unknown stream: %v", err)
	}
}
