package service

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// For You filters blocks twice: once when the pool is built, and again on
// every page served from a stored pool. Each layer is tested on its own,
// because either one alone hides a broken other.

// The pool itself must exclude blocked authors.
func TestForYouPool_ExcludesBlockedAuthors(t *testing.T) {
	viewer, blocked, ok := uuid.New(), uuid.New(), uuid.New()
	s, _, meta, _ := notesService(t, &notesGraph{}, false)
	now := time.Now()
	for _, a := range []uuid.UUID{blocked, ok} {
		id := uuid.New()
		meta.notes[id] = postgres.NoteRow{PostID: id, AuthorID: a, CreatedAt: now, TopLevel: true, Visibility: "public"}
	}
	snap, err := s.buildForYouSnapshot(context.Background(), viewer, map[uuid.UUID]struct{}{blocked: {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Items) != 1 || snap.Items[0].AuthorID != ok {
		t.Fatalf("pool holds %+v; a blocked author's note was ranked", snap.Items)
	}
}

// A block made after the pool was stored must still hide that author on
// the next page: the stored order is reused, the block set is not.
func TestForYouPage_BlockAfterSnapshotStillHides(t *testing.T) {
	viewer, author := uuid.New(), uuid.New()
	g := &notesGraph{}
	s, _, meta, _ := notesService(t, g, false)
	s.rdb = redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	now := time.Now()
	for i := 0; i < 4; i++ {
		id := uuid.New()
		meta.notes[id] = postgres.NoteRow{PostID: id, AuthorID: author, CreatedAt: now.Add(-time.Duration(i) * time.Minute), TopLevel: true, Visibility: "public"}
	}
	first, next, err := s.GetNotesForYouPage(context.Background(), viewer, 2, ForYouCursor{})
	if err != nil || len(first) != 2 || next == nil {
		t.Fatalf("first page: %d items, next=%v, err=%v", len(first), next, err)
	}
	g.blocked = []uuid.UUID{author} // the viewer blocks the author mid-scroll
	page, _, err := s.GetNotesForYouPage(context.Background(), viewer, 2, *next)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 0 {
		t.Fatalf("served %d notes from an author blocked after the pool was stored", len(page))
	}
}
