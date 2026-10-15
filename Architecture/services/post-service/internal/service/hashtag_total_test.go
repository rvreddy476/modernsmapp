package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fakeHashtagScan plays the store's bounded per-author count over a fixed
// set of rows (in scan order), honouring the exclusion list and the cap.
type fakeHashtagScan struct {
	rows   []uuid.UUID // one entry per eligible post: its author
	cap    int
	rounds int
}

func (f *fakeHashtagScan) count(excluded []uuid.UUID) (map[uuid.UUID]int, bool, error) {
	f.rounds++
	skip := make(map[uuid.UUID]bool, len(excluded))
	for _, a := range excluded {
		skip[a] = true
	}
	out := map[uuid.UUID]int{}
	scanned := 0
	for _, a := range f.rows {
		if skip[a] {
			continue
		}
		if scanned == f.cap+1 {
			break
		}
		out[a]++
		scanned++
	}
	return out, scanned > f.cap, nil
}

func repeatAuthor(a uuid.UUID, n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = a
	}
	return out
}

func gateAllowing(allowed ...uuid.UUID) func([]uuid.UUID) (map[uuid.UUID]bool, bool) {
	set := map[uuid.UUID]bool{}
	for _, a := range allowed {
		set[a] = true
	}
	return func(authors []uuid.UUID) (map[uuid.UUID]bool, bool) {
		out := map[uuid.UUID]bool{}
		for _, a := range authors {
			out[a] = set[a]
		}
		return out, true
	}
}

func TestSettleHashtagTotal(t *testing.T) {
	cap := postgres.HashtagTotalCap
	public, private, viewer := uuid.New(), uuid.New(), uuid.New()

	t.Run("a small tag is exact and drops denied authors", func(t *testing.T) {
		scan := &fakeHashtagScan{rows: append(repeatAuthor(public, 3), repeatAuthor(private, 2)...), cap: cap}
		n, capped, err := settleHashtagTotal(&viewer, scan.count, gateAllowing(public))
		if err != nil || n != 3 || capped || scan.rounds != 1 {
			t.Fatalf("got %d capped=%v rounds=%d (%v), want 3 in one round", n, capped, scan.rounds, err)
		}
	})

	t.Run("the viewer's own posts always count", func(t *testing.T) {
		scan := &fakeHashtagScan{rows: append(repeatAuthor(viewer, 2), public), cap: cap}
		n, _, err := settleHashtagTotal(&viewer, scan.count, gateAllowing())
		if err != nil || n != 2 {
			t.Fatalf("got %d (%v), want the viewer's 2", n, err)
		}
	})

	t.Run("a full scan of visible posts is capped", func(t *testing.T) {
		scan := &fakeHashtagScan{rows: repeatAuthor(public, cap+50), cap: cap}
		n, capped, err := settleHashtagTotal(nil, scan.count, gateAllowing(public))
		if err != nil || n != cap || !capped {
			t.Fatalf("got %d capped=%v (%v), want the cap", n, capped, err)
		}
	})

	t.Run("a full scan padded by a private author narrows and stays exact", func(t *testing.T) {
		// The private author's posts come first and fill the bound; only
		// 10 visible posts exist. Without narrowing this would read capped.
		rows := append(repeatAuthor(private, cap+1), repeatAuthor(public, 10)...)
		scan := &fakeHashtagScan{rows: rows, cap: cap}
		n, capped, err := settleHashtagTotal(&viewer, scan.count, gateAllowing(public))
		if err != nil || n != 10 || capped || scan.rounds != 2 {
			t.Fatalf("got %d capped=%v rounds=%d (%v), want exactly 10 after one narrowing", n, capped, scan.rounds, err)
		}
	})

	t.Run("an unresolved gate gives no count", func(t *testing.T) {
		scan := &fakeHashtagScan{rows: repeatAuthor(public, 2), cap: cap}
		down := func([]uuid.UUID) (map[uuid.UUID]bool, bool) { return map[uuid.UUID]bool{}, false }
		if _, _, err := settleHashtagTotal(&viewer, scan.count, down); !errors.Is(err, ErrHashtagTotalUnsettled) {
			t.Fatalf("err = %v, want ErrHashtagTotalUnsettled", err)
		}
	})

	t.Run("a tag that never settles gives no count", func(t *testing.T) {
		// Every round's full scan is a fresh denied author.
		var rows []uuid.UUID
		for i := 0; i < hashtagTotalMaxRounds+1; i++ {
			rows = append(rows, repeatAuthor(uuid.New(), cap+1)...)
		}
		scan := &fakeHashtagScan{rows: rows, cap: cap}
		if _, _, err := settleHashtagTotal(&viewer, scan.count, gateAllowing()); !errors.Is(err, ErrHashtagTotalUnsettled) || scan.rounds != hashtagTotalMaxRounds {
			t.Fatalf("err = %v rounds=%d, want ErrHashtagTotalUnsettled after %d", err, scan.rounds, hashtagTotalMaxRounds)
		}
	})

	t.Run("a store error is returned", func(t *testing.T) {
		boom := errors.New("db down")
		failing := func([]uuid.UUID) (map[uuid.UUID]int, bool, error) { return nil, false, boom }
		if _, _, err := settleHashtagTotal(nil, failing, gateAllowing()); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
}

// The list's author gate against the real graph client: a private author
// is dropped for a stranger and for the signed-out viewer, kept for
// themself; order is kept.
func TestHashtagPostsVisibleTo(t *testing.T) {
	public, private, stranger := uuid.New(), uuid.New(), uuid.New()
	var calls int32
	srv := fakeGraphCan(t, map[string]bool{public.String(): true}, &calls, nil)
	defer srv.Close()
	s := newGatedService(srv.URL)
	posts := []postgres.Post{{ID: uuid.New(), AuthorID: public}, {ID: uuid.New(), AuthorID: private}, {ID: uuid.New(), AuthorID: public}}

	for name, viewer := range map[string]*uuid.UUID{"stranger": &stranger, "signed out": nil} {
		got := s.hashtagPostsVisibleTo(context.Background(), viewer, posts)
		if len(got) != 2 || got[0].ID != posts[0].ID || got[1].ID != posts[2].ID {
			t.Fatalf("%s: got %v, want the two public posts in order", name, got)
		}
	}
	if got := s.hashtagPostsVisibleTo(context.Background(), &private, posts); len(got) != 3 {
		t.Fatalf("the private author lost their own post: %d", len(got))
	}
}
