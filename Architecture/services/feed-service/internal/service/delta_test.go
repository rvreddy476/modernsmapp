package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// The "N new posts" pill must count what a refresh would show.

func TestFreshForPill_CountsOnlyWhatTheFeedWouldShow(t *testing.T) {
	viewer, followed, stranger, blockedAuthor := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	since := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	newer, older := since.Add(time.Minute), since.Add(-time.Minute)
	item := func(author uuid.UUID, at time.Time) FeedItem {
		return FeedItem{PostID: uuid.New(), AuthorID: author, CreatedAt: at}
	}
	candidates := []FeedItem{
		item(followed, newer),      // counted
		item(stranger, newer),      // counted on home, not on following
		item(blockedAuthor, newer), // blocked or muted: never counted
		item(viewer, newer),        // the viewer's own: never counted
		item(followed, older),      // already seen
		item(followed, since),      // exactly the anchor: already seen
	}
	blocked := map[uuid.UUID]struct{}{blockedAuthor: {}}

	home := freshForPill(append([]FeedItem(nil), candidates...), viewer, since, blocked, false, nil)
	if len(home) != 2 {
		t.Fatalf("home pill counted %d, want 2 (followed + stranger)", len(home))
	}
	following := freshForPill(append([]FeedItem(nil), candidates...), viewer, since, blocked, true, []uuid.UUID{followed})
	if len(following) != 1 || following[0].AuthorID != followed {
		t.Fatalf("following pill counted %v, want only the followed author's new post", following)
	}
	if none := freshForPill(append([]FeedItem(nil), candidates...), viewer, since, blocked, true, nil); len(none) != 0 {
		t.Fatalf("a viewer who follows nobody got a following count of %d", len(none))
	}
}

func TestParseDeltaAnchor(t *testing.T) {
	for _, ok := range []string{"2026-10-13T09:00:00Z", "2026-10-13T09:00:00.123456789Z", "2026-10-13T14:30:00+05:30", "2026-10-13"} {
		if _, err := ParseDeltaAnchor(ok); err != nil {
			t.Errorf("anchor %q rejected: %v", ok, err)
		}
	}
	// A post id is what newest_anchor used to hold; it is not an anchor.
	if _, err := ParseDeltaAnchor(uuid.New().String()); !errors.Is(err, ErrInvalidDeltaAnchor) {
		t.Errorf("post-id anchor: err = %v, want ErrInvalidDeltaAnchor", err)
	}
}

func TestComputeFeedDelta_RefusesFeedsThatAreNotTheViewers(t *testing.T) {
	s := &Service{}
	for _, ft := range []string{"group", "group_channel", "channel", "community", "community_space", "bogus"} {
		if _, err := s.ComputeFeedDelta(context.Background(), uuid.New(), ft, "2026-10-13T09:00:00Z"); !errors.Is(err, ErrUnsupportedDeltaFeed) {
			t.Errorf("feed_type %q: err = %v, want ErrUnsupportedDeltaFeed", ft, err)
		}
	}
}

func TestDropTombstoned(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	s := &Service{rdb: rdb}
	kept, deleted := FeedItem{PostID: uuid.New()}, FeedItem{PostID: uuid.New()}
	if err := mr.Set(DeletedTombstoneKey(deleted.PostID), "1"); err != nil {
		t.Fatal(err)
	}
	got := s.dropTombstoned(context.Background(), []FeedItem{kept, deleted})
	if len(got) != 1 || got[0].PostID != kept.PostID {
		t.Fatalf("dropTombstoned = %v, want only the live post", got)
	}

	// Redis down: count as before rather than fail the pill.
	mr.Close()
	if got := s.dropTombstoned(context.Background(), []FeedItem{kept, deleted}); len(got) != 2 {
		t.Fatalf("on a Redis error kept %d of 2", len(got))
	}
}
