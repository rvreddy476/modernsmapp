package postgres

// The store side of the cover claim protocol against a real Postgres
// (LIVE_V2_TEST_DSN on a *_test database, CI only).

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIntegrationCreateStreamKeepsTheChosenID(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	id, cover := uuid.New(), uuid.New()
	st, err := s.CreateStream(ctx, CreateStreamParams{
		ID: id, CreatorUserID: uuid.New(), LiveKitRoom: "stream_" + uuid.New().String(),
		Title: "it", Visibility: "public", CoverMediaID: &cover,
	})
	if err != nil {
		t.Fatal(err)
	}
	if st.ID != id {
		t.Fatalf("row id %s, want the chosen %s: the cover claim names the chosen id", st.ID, id)
	}
	// uuid.Nil still lets the database choose.
	if other := itStream(t, s, uuid.New()); other.ID == uuid.Nil {
		t.Fatal("no id assigned")
	}
}

// Two writers of one stream's cover never overlap; another stream is not held up.
func TestIntegrationMediaHolderLockSerialises(t *testing.T) {
	s, _ := integrationStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	unlock, err := s.LockMediaHolder(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.LockMediaHolder(ctx, b)
	if err != nil {
		t.Fatalf("another stream's lock waited: %v", err)
	}
	other()

	got := make(chan struct{})
	go func() {
		u, err := s.LockMediaHolder(ctx, a)
		if err == nil {
			u()
		}
		close(got)
	}()
	select {
	case <-got:
		unlock()
		t.Fatal("a second writer of the same stream did not wait for the first")
	case <-time.After(300 * time.Millisecond):
	}
	unlock()
	unlock() // idempotent
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not released by unlock")
	}
}

func TestIntegrationCoverRowsAndCreatorStreams(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	creator := uuid.New()
	cover := uuid.New()
	with := itStream(t, s, creator)
	without := itStream(t, s, creator)
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET cover_media_id = $2 WHERE id = $1`, with.ID, cover); err != nil {
		t.Fatal(err)
	}

	ids, err := s.StreamIDsWithCover(ctx, creator)
	if err != nil || len(ids) != 1 || ids[0] != with.ID {
		t.Fatalf("StreamIDsWithCover = %v, %v; want only %s", ids, err, with.ID)
	}

	seen := func(all bool) map[uuid.UUID]*uuid.UUID {
		out := map[uuid.UUID]*uuid.UUID{}
		after := uuid.Nil
		for {
			rows, err := s.CoverClaimRows(ctx, after, 200, all)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				out[r.StreamID] = r.Cover
			}
			if len(rows) < 200 {
				return out
			}
			after = rows[len(rows)-1].StreamID
		}
	}
	covered := seen(false)
	if c, ok := covered[with.ID]; !ok || c == nil || *c != cover {
		t.Fatalf("the stream with a cover was not listed with it: %v", c)
	}
	if _, ok := covered[without.ID]; ok {
		t.Fatal("a stream without a cover was listed without -all")
	}
	if c, ok := seen(true)[without.ID]; !ok || c != nil {
		t.Fatal("-all did not list the stream without a cover")
	}

	row, err := s.CoverClaimRowFor(ctx, with.ID)
	if err != nil || row.Cover == nil || *row.Cover != cover {
		t.Fatalf("re-read = %+v, %v", row, err)
	}
	gone, err := s.CoverClaimRowFor(ctx, uuid.New())
	if err != nil || gone.Cover != nil {
		t.Fatalf("a missing stream = %+v, %v; want no cover and no error", gone, err)
	}
}
