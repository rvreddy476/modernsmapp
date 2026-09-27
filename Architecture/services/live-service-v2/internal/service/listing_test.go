package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// GET /v1/livestream/streams?status= (MTube, 2026-09-27): the upcoming
// list and the live-then-upcoming list, over an in-memory store that
// applies the same WHERE / ORDER BY / keyset the SQL does, so what is
// under test is the service's visibility filter and paging.

type listingStore struct {
	Store // nil: any other method panics, which is the point
	rows  []*postgres.LiveStream
	calls map[string]int
}

func (f *listingStore) ListLive(_ context.Context, p postgres.ListLiveParams) ([]*postgres.LiveStream, error) {
	f.calls["live"]++
	var out []*postgres.LiveStream
	for _, st := range f.rows {
		if st.Status != "live" || st.StartedAt == nil {
			continue
		}
		if p.StartedBefore != nil && !(st.StartedAt.Before(*p.StartedBefore) || (st.StartedAt.Equal(*p.StartedBefore) && st.ID.String() < p.IDBefore.String())) {
			continue
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(*out[j].StartedAt) {
			return out[i].StartedAt.After(*out[j].StartedAt)
		}
		return out[i].ID.String() > out[j].ID.String()
	})
	if len(out) > p.Limit {
		out = out[:p.Limit]
	}
	return out, nil
}

func (f *listingStore) ListScheduled(_ context.Context, p postgres.ListScheduledParams) ([]*postgres.LiveStream, error) {
	f.calls["scheduled"]++
	var out []*postgres.LiveStream
	for _, st := range f.rows {
		if st.Status != "scheduled" || st.ScheduledAt == nil || !st.ScheduledAt.After(p.Now) {
			continue
		}
		if p.ScheduledAfter != nil && !(st.ScheduledAt.After(*p.ScheduledAfter) || (st.ScheduledAt.Equal(*p.ScheduledAfter) && st.ID.String() > p.IDAfter.String())) {
			continue
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ScheduledAt.Equal(*out[j].ScheduledAt) {
			return out[i].ScheduledAt.Before(*out[j].ScheduledAt)
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	if len(out) > p.Limit {
		out = out[:p.Limit]
	}
	return out, nil
}

var listingNow = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)

func (f *listingStore) add(title, status, visibility string, creator uuid.UUID, at time.Duration) *postgres.LiveStream {
	t := listingNow.Add(at)
	st := &postgres.LiveStream{ID: uuid.New(), CreatorUserID: creator, Title: title, Status: status, Visibility: visibility}
	if status == "live" {
		st.StartedAt = &t
	} else {
		st.ScheduledAt = &t
	}
	f.rows = append(f.rows, st)
	return st
}

func newListingService(graph *fakeGraph) (*Service, *listingStore) {
	store := &listingStore{calls: map[string]int{}}
	return &Service{store: store, graph: graph, now: func() time.Time { return listingNow }}, store
}

func titles(streams []*postgres.LiveStream) string {
	out := make([]string, len(streams))
	for i, st := range streams {
		out[i] = st.Title
	}
	return strings.Join(out, ",")
}

func TestListScheduledIsUpcomingVisibleAndSoonestFirst(t *testing.T) {
	creator, follower, stranger := uuid.New(), uuid.New(), uuid.New()
	graph := &fakeGraph{follows: map[string]bool{follower.String() + ":" + creator.String(): true}}
	svc, store := newListingService(graph)
	store.add("later", "scheduled", visibilityPublic, creator, 48*time.Hour)
	store.add("soon", "scheduled", visibilityPublic, creator, time.Hour)
	store.add("past", "scheduled", visibilityPublic, creator, -time.Hour) // start time passed, never went live
	store.add("fans", "scheduled", visibilityFollowers, creator, 2*time.Hour)
	store.add("paid", "scheduled", visibilityPaid, creator, 3*time.Hour)
	store.add("on air", "live", visibilityPublic, creator, -10*time.Minute)

	cases := map[string]struct {
		viewer uuid.UUID
		want   string
	}{
		"anonymous":   {uuid.Nil, "soon,later"},
		"stranger":    {stranger, "soon,later"},
		"follower":    {follower, "soon,fans,later"},
		"the creator": {creator, "soon,fans,later"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := svc.ListScheduled(context.Background(), tc.viewer, 20, "")
			if err != nil {
				t.Fatal(err)
			}
			if got := titles(res.Streams); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			for _, st := range res.Streams {
				if st.ScheduledAt == nil || !st.ScheduledAt.After(listingNow) {
					t.Fatalf("%s: scheduled_at %v is not upcoming", st.Title, st.ScheduledAt)
				}
			}
		})
	}
}

// A run of rows the viewer may not see must not end the listing: the
// cursor resumes after the last row read, not the last row shown.
func TestListScheduledPagesPastHiddenRows(t *testing.T) {
	creator := uuid.New()
	svc, store := newListingService(&fakeGraph{})
	for i := 1; i <= 4; i++ {
		store.add("hidden", "scheduled", visibilityFollowers, creator, time.Duration(i)*time.Hour)
	}
	store.add("a", "scheduled", visibilityPublic, creator, 5*time.Hour)
	store.add("b", "scheduled", visibilityPublic, creator, 6*time.Hour)

	var seen []string
	cursor := ""
	for page := 0; page < 10; page++ {
		res, err := svc.ListScheduled(context.Background(), uuid.Nil, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if got := titles(res.Streams); got != "" {
			seen = append(seen, got)
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if got := strings.Join(seen, ","); got != "a,b" {
		t.Fatalf("paged %q, want a,b (the hidden run ended the listing?)", got)
	}
}

func TestListStreamsAllIsLiveThenUpcomingAcrossPages(t *testing.T) {
	creator := uuid.New()
	svc, store := newListingService(&fakeGraph{})
	store.add("s2", "scheduled", visibilityPublic, creator, 2*time.Hour)
	store.add("live-new", "live", visibilityPublic, creator, -5*time.Minute)
	store.add("s1", "scheduled", visibilityPublic, creator, time.Hour)
	store.add("live-old", "live", visibilityPublic, creator, -50*time.Minute)
	store.add("s3", "scheduled", visibilityPublic, creator, 3*time.Hour)

	all, err := svc.ListStreams(context.Background(), uuid.Nil, StreamStatusAll, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(all.Streams); got != "live-new,live-old,s1,s2,s3" || all.NextCursor != "" {
		t.Fatalf("one page: %q next=%q", got, all.NextCursor)
	}

	var pages []string
	cursor := ""
	for i := 0; i < 10; i++ {
		res, err := svc.ListStreams(context.Background(), uuid.Nil, StreamStatusAll, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		pages = append(pages, titles(res.Streams))
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if got := strings.Join(pages, " | "); got != "live-new,live-old | s1,s2 | s3" {
		t.Fatalf("pages %q", got)
	}

	// A page that straddles the two lists.
	res, err := svc.ListStreams(context.Background(), uuid.Nil, StreamStatusAll, 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(res.Streams); got != "live-new,live-old,s1" || !strings.HasPrefix(res.NextCursor, allScheduledCursorPrefix) {
		t.Fatalf("straddle: %q next=%q", got, res.NextCursor)
	}
}

func TestListStreamsStatusRouting(t *testing.T) {
	svc, store := newListingService(&fakeGraph{})
	store.add("on air", "live", visibilityPublic, uuid.New(), -time.Minute)
	for _, status := range []string{"", StreamStatusLive} {
		store.calls = map[string]int{}
		res, err := svc.ListStreams(context.Background(), uuid.Nil, status, 20, "")
		if err != nil || titles(res.Streams) != "on air" || store.calls["scheduled"] != 0 {
			t.Fatalf("status %q: %v %v calls=%v", status, res, err, store.calls)
		}
	}
	store.calls = map[string]int{}
	if _, err := svc.ListStreams(context.Background(), uuid.Nil, "ended", 20, ""); !errors.Is(err, ErrInvalidStatusFilter) {
		t.Fatalf("unknown status: %v", err)
	}
	if len(store.calls) != 0 {
		t.Fatalf("an unknown status reached the store: %v", store.calls)
	}
}
