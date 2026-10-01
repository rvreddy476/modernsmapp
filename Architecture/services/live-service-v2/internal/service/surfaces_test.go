package service

// Live surfaces (2 Oct 2026): orientation and category, the host card,
// PATCH, discovery and its viewer check, reminders, and the started event.
// Over the in-memory store (internal/storetest), which applies the same
// filters, orders and keysets as the SQL.

import (
	"context"
	"encoding/json"
	"errors"

	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	sharedevents "github.com/atpost/shared/events"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// --- fakes of the other services ---

type fakeProfiles struct {
	m     map[uuid.UUID]Profile
	err   error
	calls int
	asked []uuid.UUID
}

func (f *fakeProfiles) Profiles(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]Profile, error) {
	f.calls++
	f.asked = append(f.asked, ids...)
	if f.err != nil {
		return nil, f.err
	}
	out := map[uuid.UUID]Profile{}
	for _, id := range ids {
		if p, ok := f.m[id]; ok {
			out[id] = p
		}
	}
	return out, nil
}

type fakeCategories struct {
	list  []Category
	err   error
	calls int
}

func (f *fakeCategories) Categories(context.Context) ([]Category, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.list, nil
}

type fakeFollowing struct {
	ids   map[uuid.UUID][]uuid.UUID
	err   error
	calls int
}

func (f *fakeFollowing) FollowedCreatorIDs(_ context.Context, viewer uuid.UUID) ([]uuid.UUID, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.ids[viewer], nil
}

// batchFakeGraph is fakeGraph plus the one-call-for-many read.
type batchFakeGraph struct {
	*fakeGraph
	batchCalls int
	batchErr   error
	omit       map[uuid.UUID]bool // creators left out of the answer
}

func (g *batchFakeGraph) Relationships(_ context.Context, viewer uuid.UUID, creators []uuid.UUID) (map[uuid.UUID]Relationship, error) {
	g.batchCalls++
	if g.batchErr != nil {
		return nil, g.batchErr
	}
	out := map[uuid.UUID]Relationship{}
	for _, c := range creators {
		if g.omit[c] {
			continue
		}
		k := viewer.String() + ":" + c.String()
		out[c] = Relationship{Follows: g.follows[k], Blocked: g.blocked[k]}
	}
	return out, nil
}

var testTaxonomy = []Category{{Slug: "music", Label: "Music"}, {Slug: "gaming", Label: "Gaming"}, {Slug: "science-tech", Label: "Science & tech"}}

// surfRig is the rig with the service's clock pinned to the fake clock and
// the other services faked.
type surfRig struct {
	*rig
	profiles   *fakeProfiles
	categories *fakeCategories
	following  *fakeFollowing
}

func newSurfRig(pilot ...uuid.UUID) *surfRig {
	r := newRig(pilot...)
	s := &surfRig{
		rig:        r,
		profiles:   &fakeProfiles{m: map[uuid.UUID]Profile{}},
		categories: &fakeCategories{list: testTaxonomy},
		following:  &fakeFollowing{ids: map[uuid.UUID][]uuid.UUID{}},
	}
	r.svc.now = r.clock.Now
	r.svc.profiles, r.svc.categories, r.svc.following = s.profiles, s.categories, s.following
	return s
}

// live adds a public stream on air and sets what the listings read.
func (s *surfRig) live(creator uuid.UUID, viewers int, orientation, category string) *postgres.LiveStream {
	st := s.store.AddStream(creator)
	row := s.store.Streams[st.ID]
	row.ViewerCount, row.Orientation, row.Category = viewers, orientation, category
	s.clock.Advance(time.Second) // distinct started_at per stream
	return s.store.Stream(st.ID)
}

// scheduled adds a public scheduled stream `in` from now.
func (s *surfRig) scheduled(creator uuid.UUID, in time.Duration) *postgres.LiveStream {
	st := s.store.AddStreamStatus(creator, stScheduled)
	at := s.clock.Now().Add(in)
	s.store.Streams[st.ID].ScheduledAt = &at
	return s.store.Stream(st.ID)
}

func ids(rows []*postgres.LiveStream) []uuid.UUID {
	out := make([]uuid.UUID, len(rows))
	for i, st := range rows {
		out[i] = st.ID
	}
	return out
}

func sameIDs(t *testing.T, what string, got []*postgres.LiveStream, want ...*postgres.LiveStream) {
	t.Helper()
	if !reflect.DeepEqual(ids(got), ids(want)) {
		t.Fatalf("%s: got %v, want %v", what, ids(got), ids(want))
	}
}

// --- create: orientation and category ---

func TestCreateStreamOrientation(t *testing.T) {
	host := uuid.New()
	r := newSurfRig(host)
	for in, want := range map[string]string{"": "landscape", "landscape": "landscape", "portrait": "portrait", " Portrait ": "portrait"} {
		st, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x", Orientation: in})
		if err != nil || st.Orientation != want {
			t.Fatalf("orientation %q: %+v %v, want %s", in, st, err, want)
		}
		if got := r.store.Stream(st.ID).Orientation; got != want {
			t.Fatalf("orientation %q stored as %q", in, got)
		}
	}
	for _, bad := range []string{"square", "vertical", "wide"} {
		if _, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x", Orientation: bad}); !errors.Is(err, ErrInvalidOrientation) {
			t.Fatalf("orientation %q: %v, want ErrInvalidOrientation", bad, err)
		}
	}
	if n := len(r.store.Streams); n != 4 {
		t.Fatalf("a refused create left a row: %d streams", n)
	}
}

func TestCreateStreamCategory(t *testing.T) {
	host := uuid.New()
	r := newSurfRig(host)
	st, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x", Category: " Music "})
	if err != nil || st.Category != "music" {
		t.Fatalf("known category: %+v %v", st, err)
	}
	st, err = r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x"})
	if err != nil || st.Category != "" {
		t.Fatalf("no category: %+v %v", st, err)
	}
	if _, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x", Category: "knitting"}); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("unknown category: %v", err)
	}
}

// TestCategoryCacheAndUnreachable: the taxonomy is fetched once per ten
// minutes; while post-service is down only slugs seen before pass.
func TestCategoryCacheAndUnreachable(t *testing.T) {
	r := newSurfRig()
	valid := func(slug string) error { _, err := r.svc.validCategory(ctx, slug); return err }

	if err := valid("music"); err != nil {
		t.Fatal(err)
	}
	if err := valid("gaming"); err != nil {
		t.Fatal(err)
	}
	if r.categories.calls != 1 {
		t.Fatalf("taxonomy fetched %d times inside the cache window", r.categories.calls)
	}
	r.clock.Advance(categoryCacheTTL - time.Second)
	_ = valid("music")
	if r.categories.calls != 1 {
		t.Fatalf("refetched before the TTL: %d", r.categories.calls)
	}
	// Past the TTL it asks again, and sees a category added meanwhile.
	r.categories.list = append(append([]Category{}, testTaxonomy...), Category{Slug: "podcasts", Label: "Podcasts"})
	r.clock.Advance(2 * time.Second)
	if err := valid("podcasts"); err != nil || r.categories.calls != 2 {
		t.Fatalf("after the TTL: err=%v calls=%d", err, r.categories.calls)
	}

	// post-service goes away: seen slugs still pass, unseen ones do not.
	r.categories.err = errors.New("connection refused")
	r.clock.Advance(categoryCacheTTL + time.Second)
	if err := valid("music"); err != nil {
		t.Fatalf("a slug seen before was refused while post-service is down: %v", err)
	}
	if err := valid("knitting"); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("an unseen slug passed while post-service is down: %v", err)
	}
	// ...and it is not hammered: one failed attempt per retry window.
	failed := r.categories.calls
	_ = valid("music")
	if r.categories.calls != failed {
		t.Fatalf("retried inside the backoff: %d -> %d", failed, r.categories.calls)
	}
	r.clock.Advance(categoryRetryAfter + time.Second)
	_ = valid("music")
	if r.categories.calls != failed+1 {
		t.Fatalf("did not retry after the backoff: %d", r.categories.calls)
	}

	// Never reached at all: nothing but the empty category is accepted.
	cold := newSurfRig()
	cold.categories.err = errors.New("down")
	if _, err := cold.svc.validCategory(ctx, "music"); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("a category passed with no taxonomy ever seen: %v", err)
	}
	if got, err := cold.svc.validCategory(ctx, "  "); err != nil || got != "" {
		t.Fatalf("the empty category must always pass: %q %v", got, err)
	}
	cold.svc.categories = nil
	if _, err := cold.svc.validCategory(ctx, "music"); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("a category passed with no taxonomy source: %v", err)
	}
}

// --- the host card ---

func TestCreatorCardOnEveryRow(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newSurfRig(host)
	r.profiles.m[host] = Profile{Name: "Asha Rao", Handle: "asha", AvatarURL: "/v1/media/avatar/1"}
	want := postgres.UserCard{UserID: host, Name: "Asha Rao", Handle: "asha", AvatarURL: "/v1/media/avatar/1"}

	created, err := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x"})
	if err != nil || created.Creator == nil || !reflect.DeepEqual(*created.Creator, want) {
		t.Fatalf("create: %+v %v", created.Creator, err)
	}
	started, _ := r.svc.StartStream(ctx, created.ID, host)
	if started.Stream.Creator == nil || started.Stream.Creator.Name != "Asha Rao" {
		t.Fatalf("start: %+v", started.Stream.Creator)
	}
	_ = r.svc.HandleWebhook(ctx, hostEvent(started.Stream, "track_published"))
	got, _ := r.svc.GetStream(ctx, created.ID, viewer)
	if got.Creator == nil || !reflect.DeepEqual(*got.Creator, want) {
		t.Fatalf("get: %+v", got.Creator)
	}
	list, _ := r.svc.DiscoverLive(ctx, uuid.Nil, DiscoverParams{})
	if len(list.Streams) != 1 || list.Streams[0].Creator == nil || list.Streams[0].Creator.Handle != "asha" {
		t.Fatalf("live list: %+v", list.Streams)
	}
	legacy, _ := r.svc.ListLiveNow(ctx, uuid.Nil, 20, "")
	if len(legacy.Streams) != 1 || legacy.Streams[0].Creator == nil {
		t.Fatalf("status=all's live phase has no creator card")
	}
	ended, _ := r.svc.EndStream(ctx, created.ID, host)
	if ended.Creator == nil || ended.Creator.Name != "Asha Rao" {
		t.Fatalf("end: %+v", ended.Creator)
	}
	// The stored row is never touched.
	if r.store.Streams[created.ID].Creator != nil {
		t.Fatal("the creator card leaked into the store")
	}
}

// TestCreatorCardLookupFailureIsNotAnError: a failed lookup, or a profile
// the lookup does not return, leaves the card with the user id only.
func TestCreatorCardLookupFailureIsNotAnError(t *testing.T) {
	host := uuid.New()
	r := newSurfRig(host)
	st := r.live(host, 3, "landscape", "")

	r.profiles.err = errors.New("identity-profile is down")
	got, err := r.svc.GetStream(ctx, st.ID, uuid.Nil)
	if err != nil {
		t.Fatalf("a failed profile lookup failed the read: %v", err)
	}
	if got.Creator == nil || !reflect.DeepEqual(*got.Creator, postgres.UserCard{UserID: host}) {
		t.Fatalf("card after a failed lookup: %+v", got.Creator)
	}
	// A failure is not cached: the next read asks again and gets the name.
	r.profiles.err = nil
	r.profiles.m[host] = Profile{Name: "Asha"}
	got, _ = r.svc.GetStream(ctx, st.ID, uuid.Nil)
	if got.Creator.Name != "Asha" {
		t.Fatalf("the failure was cached: %+v", got.Creator)
	}

	// No profile for the user (deleted account): user id only, no error.
	ghost := uuid.New()
	gs := r.live(ghost, 1, "landscape", "")
	got, err = r.svc.GetStream(ctx, gs.ID, uuid.Nil)
	if err != nil || !reflect.DeepEqual(*got.Creator, postgres.UserCard{UserID: ghost}) {
		t.Fatalf("missing profile: %+v %v", got.Creator, err)
	}

	// No profile source at all.
	r.svc.profiles = nil
	r.clock.Advance(cardCacheTTL + time.Second)
	got, err = r.svc.GetStream(ctx, st.ID, uuid.Nil)
	if err != nil || !reflect.DeepEqual(*got.Creator, postgres.UserCard{UserID: host}) {
		t.Fatalf("no profile source: %+v %v", got.Creator, err)
	}
}

func TestCreatorCardCachedSixtySeconds(t *testing.T) {
	host := uuid.New()
	r := newSurfRig(host)
	r.profiles.m[host] = Profile{Name: "Old name"}
	st := r.live(host, 1, "landscape", "")
	for i := 0; i < 3; i++ {
		_, _ = r.svc.GetStream(ctx, st.ID, uuid.Nil)
	}
	if r.profiles.calls != 1 {
		t.Fatalf("profile lookups inside 60s: %d, want 1", r.profiles.calls)
	}
	r.profiles.m[host] = Profile{Name: "New name"}
	r.clock.Advance(cardCacheTTL - time.Second)
	got, _ := r.svc.GetStream(ctx, st.ID, uuid.Nil)
	if got.Creator.Name != "Old name" || r.profiles.calls != 1 {
		t.Fatalf("before the TTL: %+v calls=%d", got.Creator, r.profiles.calls)
	}
	r.clock.Advance(2 * time.Second)
	got, _ = r.svc.GetStream(ctx, st.ID, uuid.Nil)
	if got.Creator.Name != "New name" || r.profiles.calls != 2 {
		t.Fatalf("after the TTL: %+v calls=%d", got.Creator, r.profiles.calls)
	}
	// One batch for a whole list, each creator asked once.
	r.profiles.asked = nil
	r.clock.Advance(cardCacheTTL + time.Second)
	other := uuid.New()
	r.live(other, 5, "landscape", "")
	r.live(other, 4, "landscape", "")
	before := r.profiles.calls
	_, _ = r.svc.DiscoverLive(ctx, uuid.Nil, DiscoverParams{})
	if r.profiles.calls != before+1 || len(r.profiles.asked) != 2 {
		t.Fatalf("a list made %d lookups for %d users", r.profiles.calls-before, len(r.profiles.asked))
	}
}

// --- PATCH ---

func TestUpdateStreamHostOnlyWhileScheduled(t *testing.T) {
	host, stranger := uuid.New(), uuid.New()
	r := newSurfRig(host)
	st, _ := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "Before", Category: "music"})
	title := "After"

	if _, err := r.svc.UpdateStream(ctx, st.ID, stranger, UpdateStreamParams{Title: &title}); !errors.Is(err, ErrNotCreator) {
		t.Fatalf("a stranger edited the stream: %v", err)
	}
	if _, err := r.svc.UpdateStream(ctx, uuid.New(), host, UpdateStreamParams{Title: &title}); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("unknown stream: %v", err)
	}
	if got := r.store.Stream(st.ID).Title; got != "Before" {
		t.Fatalf("a refused edit changed the title to %q", got)
	}

	for _, status := range []string{stStarting, stLive, stReconnecting, stEnded, stFailed} {
		other := r.store.AddStreamStatus(host, status)
		if _, err := r.svc.UpdateStream(ctx, other.ID, host, UpdateStreamParams{Title: &title}); !errors.Is(err, ErrStateConflict) {
			t.Fatalf("edit while %s: %v, want ErrStateConflict", status, err)
		}
		if got := r.store.Stream(other.ID).Title; got == title {
			t.Fatalf("a %s stream was edited", status)
		}
		// The state is checked before the fields: 409, not 422.
		bad := "square"
		if _, err := r.svc.UpdateStream(ctx, other.ID, host, UpdateStreamParams{Orientation: &bad}); !errors.Is(err, ErrStateConflict) {
			t.Fatalf("an invalid edit of a %s stream: %v, want ErrStateConflict", status, err)
		}
	}
}

func TestUpdateStreamFields(t *testing.T) {
	host := uuid.New()
	r := newSurfRig(host)
	cover := uuid.New()
	when := r.clock.Now().Add(48 * time.Hour)
	st, _ := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "Before", Description: "d", Category: "music", CoverMediaID: &cover, ScheduledAt: &when})

	str := func(s string) *string { return &s }
	newCover := uuid.New()
	later := when.Add(time.Hour)
	got, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{
		Title: str("  After "), Description: str("new"), Category: str("gaming"),
		Visibility: str("followers"), Orientation: str("portrait"),
		SetCoverMediaID: true, CoverMediaID: &newCover, SetScheduledAt: true, ScheduledAt: &later,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "After" || got.Description != "new" || got.Category != "gaming" || got.Visibility != "followers" ||
		got.Orientation != "portrait" || got.CoverMediaID == nil || *got.CoverMediaID != newCover || !got.ScheduledAt.Equal(later) {
		t.Fatalf("after the edit: %+v", got)
	}
	if got.Creator == nil || got.ReminderCount == nil {
		t.Fatalf("the PATCH answer is not a full row: %+v", got)
	}

	// An empty patch changes nothing; absent fields are left alone.
	same, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{})
	if err != nil || same.Title != "After" || same.Category != "gaming" || same.CoverMediaID == nil || same.ScheduledAt == nil {
		t.Fatalf("an empty patch changed the row: %+v %v", same, err)
	}
	// Clearing: category "" and the two nullable fields.
	cleared, err := r.svc.UpdateStream(ctx, st.ID, host, UpdateStreamParams{Category: str(""), SetCoverMediaID: true, SetScheduledAt: true})
	if err != nil || cleared.Category != "" || cleared.CoverMediaID != nil || cleared.ScheduledAt != nil {
		t.Fatalf("clearing: %+v %v", cleared, err)
	}

	refusals := []struct {
		name string
		p    UpdateStreamParams
		want error
	}{
		{"paid visibility", UpdateStreamParams{Visibility: str("paid")}, ErrPaidVisibility},
		{"unknown visibility", UpdateStreamParams{Visibility: str("friends")}, ErrInvalidVisibility},
		{"empty visibility", UpdateStreamParams{Visibility: str("")}, ErrInvalidVisibility},
		{"unknown orientation", UpdateStreamParams{Orientation: str("square")}, ErrInvalidOrientation},
		{"empty orientation", UpdateStreamParams{Orientation: str(" ")}, ErrInvalidOrientation},
		{"unknown category", UpdateStreamParams{Category: str("knitting")}, ErrInvalidCategory},
		{"empty title", UpdateStreamParams{Title: str("   ")}, ErrInvalidTitle},
	}
	before := *r.store.Stream(st.ID)
	for _, tc := range refusals {
		if _, err := r.svc.UpdateStream(ctx, st.ID, host, tc.p); !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	after := *r.store.Stream(st.ID)
	if before.Title != after.Title || before.Visibility != after.Visibility || before.Orientation != after.Orientation || before.Category != after.Category {
		t.Fatalf("a refused patch changed the row: %+v -> %+v", before, after)
	}
}

// --- discovery ---

func TestDiscoverLiveFiltersAndSort(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	r := newSurfRig()
	wideMusic := r.live(a, 10, "landscape", "music")
	tallMusic := r.live(b, 50, "portrait", "music")
	tallGaming := r.live(c, 30, "portrait", "gaming")
	wideNone := r.live(a, 40, "landscape", "")
	r.store.AddStreamStatus(a, stStarting) // not on air
	r.scheduled(a, time.Hour)

	list := func(p DiscoverParams) []*postgres.LiveStream {
		t.Helper()
		res, err := r.svc.DiscoverLive(ctx, uuid.Nil, p)
		if err != nil {
			t.Fatalf("%+v: %v", p, err)
		}
		return res.Streams
	}
	sameIDs(t, "default sort is by viewers", list(DiscoverParams{}), tallMusic, wideNone, tallGaming, wideMusic)
	sameIDs(t, "sort=viewers", list(DiscoverParams{Sort: "viewers"}), tallMusic, wideNone, tallGaming, wideMusic)
	sameIDs(t, "sort=recent", list(DiscoverParams{Sort: "recent"}), wideNone, tallGaming, tallMusic, wideMusic)
	sameIDs(t, "orientation=portrait", list(DiscoverParams{Orientation: "portrait"}), tallMusic, tallGaming)
	sameIDs(t, "orientation=landscape", list(DiscoverParams{Orientation: "landscape"}), wideNone, wideMusic)
	sameIDs(t, "category=music", list(DiscoverParams{Category: "music"}), tallMusic, wideMusic)
	sameIDs(t, "both", list(DiscoverParams{Category: "music", Orientation: "portrait"}), tallMusic)
	sameIDs(t, "a category nobody is in", list(DiscoverParams{Category: "podcasts"}))

	if _, err := r.svc.DiscoverLive(ctx, uuid.Nil, DiscoverParams{Sort: "trending"}); !errors.Is(err, ErrInvalidSort) {
		t.Fatalf("unknown sort: %v", err)
	}
	if _, err := r.svc.DiscoverLive(ctx, uuid.Nil, DiscoverParams{Orientation: "square"}); !errors.Is(err, ErrInvalidOrientation) {
		t.Fatalf("unknown orientation: %v", err)
	}
	for _, st := range list(DiscoverParams{}) {
		if st.Status != stLive && st.Status != stReconnecting {
			t.Fatalf("a %s stream is in the live list", st.Status)
		}
	}
}

// TestDiscoveryViewerCheck: every discovery route applies visibility and
// blocks; a signed-out caller sees public streams only.
func TestDiscoveryViewerCheck(t *testing.T) {
	pubHost, folHost, blockHost := uuid.New(), uuid.New(), uuid.New()
	follower, stranger := uuid.New(), uuid.New()
	r := newSurfRig()
	pub := r.live(pubHost, 10, "landscape", "music")
	fol := r.live(folHost, 20, "landscape", "gaming")
	r.store.Streams[fol.ID].Visibility = visibilityFollowers
	blocked := r.live(blockHost, 30, "landscape", "music")
	paid := r.live(uuid.New(), 99, "landscape", "music")
	r.store.Streams[paid.ID].Visibility = visibilityPaid
	r.graph.follows[follower.String()+":"+folHost.String()] = true
	r.graph.blocked[follower.String()+":"+blockHost.String()] = true

	cases := []struct {
		name   string
		viewer uuid.UUID
		want   []*postgres.LiveStream
		cats   map[string]int // category -> live_count
	}{
		{"signed out", uuid.Nil, []*postgres.LiveStream{blocked, pub}, map[string]int{"music": 2}},
		{"stranger", stranger, []*postgres.LiveStream{blocked, pub}, map[string]int{"music": 2}},
		{"follower who blocked a host", follower, []*postgres.LiveStream{fol, pub}, map[string]int{"music": 1, "gaming": 1}},
		{"the followers-only host", folHost, []*postgres.LiveStream{blocked, fol, pub}, map[string]int{"music": 2, "gaming": 1}},
	}
	for _, tc := range cases {
		res, err := r.svc.DiscoverLive(ctx, tc.viewer, DiscoverParams{})
		if err != nil {
			t.Fatal(err)
		}
		sameIDs(t, tc.name+": live list", res.Streams, tc.want...)

		cats, err := r.svc.LiveCategories(ctx, tc.viewer, "")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, c := range cats {
			got[c.Slug] = c.LiveCount
		}
		if !reflect.DeepEqual(got, tc.cats) {
			t.Fatalf("%s: categories %v, want %v", tc.name, got, tc.cats)
		}

		creators, err := r.svc.LiveCreators(ctx, tc.viewer, 50, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(creators) != len(tc.want) {
			t.Fatalf("%s: %d live creators, want %d", tc.name, len(creators), len(tc.want))
		}
		for i, c := range creators {
			if c.StreamID != tc.want[i].ID {
				t.Fatalf("%s: live creators %d = %s, want %s", tc.name, i, c.StreamID, tc.want[i].ID)
			}
		}
	}

	// The same rule on a creator's own listing and on upcoming.
	for _, v := range []struct {
		viewer uuid.UUID
		n      int
	}{{uuid.Nil, 0}, {stranger, 0}, {follower, 1}, {folHost, 1}} {
		res, err := r.svc.UserStreams(ctx, v.viewer, folHost, UserStreamsLive, 20, "")
		if err != nil || len(res.Streams) != v.n {
			t.Fatalf("followers-only host's live tab for %s: %d rows %v, want %d", v.viewer, len(res.Streams), err, v.n)
		}
	}
	up := r.scheduled(folHost, time.Hour)
	r.store.Streams[up.ID].Visibility = visibilityFollowers
	for _, v := range []struct {
		viewer uuid.UUID
		n      int
	}{{uuid.Nil, 0}, {stranger, 0}, {follower, 1}} {
		res, err := r.svc.DiscoverUpcoming(ctx, v.viewer, DiscoverParams{})
		if err != nil || len(res.Streams) != v.n {
			t.Fatalf("upcoming for %s: %d rows %v, want %d", v.viewer, len(res.Streams), err, v.n)
		}
	}
}

// TestDiscoveryUndecidableRelationshipHidesTheRow: when the graph cannot
// answer, a signed-in viewer sees none of other people's streams.
func TestDiscoveryUndecidableRelationshipHidesTheRow(t *testing.T) {
	viewer := uuid.New()
	r := newSurfRig()
	r.live(uuid.New(), 5, "landscape", "music")
	own := r.live(viewer, 1, "landscape", "music")
	r.graph.err = errors.New("graph down")
	res, err := r.svc.DiscoverLive(ctx, viewer, DiscoverParams{})
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "graph down", res.Streams, own)
}

// TestDiscoveryBatchesRelationships: with a graph client that can batch,
// one call answers a whole page; a failed batch hides other people's rows.
func TestDiscoveryBatchesRelationships(t *testing.T) {
	viewer, blockedHost := uuid.New(), uuid.New()
	r := newSurfRig()
	g := &batchFakeGraph{fakeGraph: r.graph}
	r.svc.graph = g
	a := r.live(uuid.New(), 30, "landscape", "")
	r.live(blockedHost, 20, "landscape", "")
	c := r.live(uuid.New(), 10, "landscape", "")
	own := r.live(viewer, 1, "landscape", "")
	r.graph.blocked[viewer.String()+":"+blockedHost.String()] = true

	res, err := r.svc.DiscoverLive(ctx, viewer, DiscoverParams{})
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "batched", res.Streams, a, c, own)
	if g.batchCalls != 1 || g.calls != 0 {
		t.Fatalf("relationship reads: %d batch, %d single; want 1 batch", g.batchCalls, g.calls)
	}
	// Signed out: no graph call at all.
	if _, err := r.svc.DiscoverLive(ctx, uuid.Nil, DiscoverParams{}); err != nil || g.batchCalls != 1 {
		t.Fatalf("a signed-out list asked the graph: %d %v", g.batchCalls, err)
	}
	// A creator the answer leaves out cannot be ruled out as a block.
	g.omit = map[uuid.UUID]bool{a.CreatorUserID: true}
	res, _ = r.svc.DiscoverLive(ctx, viewer, DiscoverParams{})
	sameIDs(t, "a creator missing from the batch", res.Streams, c, own)
	g.omit = nil
	g.batchErr = errors.New("graph down")
	res, _ = r.svc.DiscoverLive(ctx, viewer, DiscoverParams{})
	sameIDs(t, "batch failed", res.Streams, own)
}

func TestDiscoverFollowing(t *testing.T) {
	viewer, followed, subscribed, other := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newSurfRig()
	f := r.live(followed, 5, "landscape", "music")
	s := r.live(subscribed, 9, "portrait", "music")
	r.live(other, 100, "landscape", "music")
	upF := r.scheduled(followed, time.Hour)
	r.scheduled(other, 2*time.Hour)
	// Followed users and subscribed channel owners arrive as one list.
	r.following.ids[viewer] = []uuid.UUID{followed, subscribed}

	res, err := r.svc.DiscoverLive(ctx, viewer, DiscoverParams{Following: true})
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "following", res.Streams, s, f)
	res, _ = r.svc.DiscoverLive(ctx, viewer, DiscoverParams{Following: true, Orientation: "portrait"})
	sameIDs(t, "following + orientation", res.Streams, s)
	res, _ = r.svc.DiscoverUpcoming(ctx, viewer, DiscoverParams{Following: true})
	sameIDs(t, "following upcoming", res.Streams, upF)

	// Without the filter the third stream is there.
	res, _ = r.svc.DiscoverLive(ctx, viewer, DiscoverParams{})
	if len(res.Streams) != 3 {
		t.Fatalf("unfiltered: %d", len(res.Streams))
	}

	empty := func(what string, viewer uuid.UUID) {
		t.Helper()
		for name, call := range map[string]func() (*ListLiveResult, error){
			"live": func() (*ListLiveResult, error) {
				return r.svc.DiscoverLive(ctx, viewer, DiscoverParams{Following: true})
			},
			"upcoming": func() (*ListLiveResult, error) {
				return r.svc.DiscoverUpcoming(ctx, viewer, DiscoverParams{Following: true})
			},
		} {
			res, err := call()
			if err != nil || res.Streams == nil || len(res.Streams) != 0 || res.NextCursor != "" {
				t.Fatalf("%s (%s): %+v %v, want an empty list", what, name, res, err)
			}
		}
	}
	// Fail closed: the lookup failed, so nothing — never the unfiltered list.
	r.following.err = errors.New("post-service down")
	empty("lookup failed", viewer)
	r.following.err = nil
	empty("follows nobody", uuid.New())
	before := r.following.calls
	empty("signed out", uuid.Nil)
	if r.following.calls != before {
		t.Fatal("a signed-out Following list asked for a follow list")
	}
	r.svc.following = nil
	empty("not configured", viewer)
}

// TestDiscoverLivePaging: both orders walk every visible stream exactly
// once, and a run of hidden streams does not end the walk early.
func TestDiscoverLivePaging(t *testing.T) {
	viewer, blockedHost := uuid.New(), uuid.New()
	r := newSurfRig()
	r.graph.blocked[viewer.String()+":"+blockedHost.String()] = true
	want := map[uuid.UUID]bool{}
	for i := 0; i < 9; i++ {
		st := r.live(uuid.New(), i%4, "landscape", "music") // ties on viewer_count
		want[st.ID] = true
	}
	for i := 0; i < 8; i++ { // more hidden rows than one over-fetched batch
		r.live(blockedHost, 100+i, "landscape", "music")
	}
	for _, sortBy := range []string{"viewers", "recent"} {
		seen := map[uuid.UUID]int{}
		cursor, pages := "", 0
		var prev *postgres.LiveStream
		for {
			res, err := r.svc.DiscoverLive(ctx, viewer, DiscoverParams{Sort: sortBy, Limit: 2, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Streams) > 2 {
				t.Fatalf("%s: a page of %d with limit 2", sortBy, len(res.Streams))
			}
			for _, st := range res.Streams {
				seen[st.ID]++
				if prev != nil && sortBy == "viewers" && st.ViewerCount > prev.ViewerCount {
					t.Fatalf("viewers order broken: %d after %d", st.ViewerCount, prev.ViewerCount)
				}
				if prev != nil && sortBy == "recent" && st.StartedAt.After(*prev.StartedAt) {
					t.Fatalf("recent order broken")
				}
				prev = st
			}
			pages++
			if res.NextCursor == "" || pages > 40 {
				break
			}
			cursor = res.NextCursor
		}
		if len(seen) != len(want) {
			t.Fatalf("%s: walked %d streams in %d pages, want %d", sortBy, len(seen), pages, len(want))
		}
		for id, n := range seen {
			if !want[id] || n != 1 {
				t.Fatalf("%s: stream %s seen %d times (visible=%v)", sortBy, id, n, want[id])
			}
		}
	}
	// A malformed cursor is the head of the list, not an error.
	res, err := r.svc.DiscoverLive(ctx, viewer, DiscoverParams{Cursor: "vnonsense", Limit: 2})
	if err != nil || len(res.Streams) != 2 {
		t.Fatalf("malformed cursor: %+v %v", res, err)
	}
}

func TestDiscoverUpcoming(t *testing.T) {
	host, viewer, fan := uuid.New(), uuid.New(), uuid.New()
	r := newSurfRig()
	soon := r.scheduled(host, time.Hour)
	later := r.scheduled(host, 30*time.Hour)
	r.store.Streams[later.ID].Orientation = "portrait"
	r.store.Streams[later.ID].Category = "music"
	r.scheduled(host, -time.Minute)            // its time passed
	r.store.AddStreamStatus(host, stScheduled) // no time at all
	r.live(host, 3, "landscape", "")
	started := r.scheduled(host, 2*time.Hour)
	r.store.Streams[started.ID].Status = stStarting

	for _, u := range []uuid.UUID{viewer, fan} {
		if _, err := r.svc.SetReminder(ctx, later.ID, u, true); err != nil {
			t.Fatal(err)
		}
	}
	res, err := r.svc.DiscoverUpcoming(ctx, viewer, DiscoverParams{})
	if err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "upcoming, soonest first", res.Streams, soon, later)
	for _, st := range res.Streams {
		if st.Status != stScheduled || !st.ScheduledAt.After(r.clock.Now()) {
			t.Fatalf("not upcoming: %+v", st)
		}
		if st.Creator == nil {
			t.Fatal("an upcoming row has no creator card")
		}
	}
	if s := res.Streams[0]; s.ReminderSet == nil || *s.ReminderSet || s.ReminderCount == nil || *s.ReminderCount != 0 {
		t.Fatalf("no reminders: set=%v count=%v", s.ReminderSet, s.ReminderCount)
	}
	if s := res.Streams[1]; s.ReminderSet == nil || !*s.ReminderSet || *s.ReminderCount != 2 {
		t.Fatalf("with reminders: set=%v count=%v", s.ReminderSet, s.ReminderCount)
	}
	// Someone who set none sees the count and reminder_set false.
	res, _ = r.svc.DiscoverUpcoming(ctx, uuid.New(), DiscoverParams{})
	if s := res.Streams[1]; s.ReminderSet == nil || *s.ReminderSet || *s.ReminderCount != 2 {
		t.Fatalf("another viewer: set=%v count=%v", s.ReminderSet, s.ReminderCount)
	}
	// Signed out: the count, and no reminder_set at all.
	res, _ = r.svc.DiscoverUpcoming(ctx, uuid.Nil, DiscoverParams{})
	if s := res.Streams[1]; s.ReminderSet != nil || s.ReminderCount == nil || *s.ReminderCount != 2 {
		t.Fatalf("signed out: set=%v count=%v", s.ReminderSet, s.ReminderCount)
	}
	res, _ = r.svc.DiscoverUpcoming(ctx, viewer, DiscoverParams{Orientation: "portrait"})
	sameIDs(t, "orientation", res.Streams, later)
	res, _ = r.svc.DiscoverUpcoming(ctx, viewer, DiscoverParams{Category: "music"})
	sameIDs(t, "category", res.Streams, later)

	// Paging.
	res, _ = r.svc.DiscoverUpcoming(ctx, viewer, DiscoverParams{Limit: 1})
	sameIDs(t, "page 1", res.Streams, soon)
	if res.NextCursor == "" {
		t.Fatal("no cursor after a full page")
	}
	res, _ = r.svc.DiscoverUpcoming(ctx, viewer, DiscoverParams{Limit: 1, Cursor: res.NextCursor})
	sameIDs(t, "page 2", res.Streams, later)
}

func TestLiveCategories(t *testing.T) {
	r := newSurfRig()
	r.live(uuid.New(), 10, "landscape", "music")
	r.live(uuid.New(), 5, "portrait", "music")
	r.live(uuid.New(), 40, "landscape", "gaming")
	r.live(uuid.New(), 0, "landscape", "retired-slug") // not in the taxonomy any more
	r.live(uuid.New(), 500, "landscape", "")           // no category
	sched := r.scheduled(uuid.New(), time.Hour)
	r.store.Streams[sched.ID].Category = "science-tech"

	got, err := r.svc.LiveCategories(ctx, uuid.Nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []LiveCategory{
		{Slug: "gaming", Label: "Gaming", LiveCount: 1, ViewerCount: 40},
		{Slug: "music", Label: "Music", LiveCount: 2, ViewerCount: 15},
		{Slug: "retired-slug", Label: "retired-slug", LiveCount: 1, ViewerCount: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("categories:\n got %+v\nwant %+v", got, want)
	}
	// ?orientation= counts only that orientation.
	tall, err := r.svc.LiveCategories(ctx, uuid.Nil, "portrait")
	if err != nil || !reflect.DeepEqual(tall, []LiveCategory{{Slug: "music", Label: "Music", LiveCount: 1, ViewerCount: 5}}) {
		t.Fatalf("portrait categories: %+v %v", tall, err)
	}
	wide, _ := r.svc.LiveCategories(ctx, uuid.Nil, " Landscape ")
	if len(wide) != 3 || wide[0].Slug != "gaming" || wide[1] != (LiveCategory{Slug: "music", Label: "Music", LiveCount: 1, ViewerCount: 10}) {
		t.Fatalf("landscape categories: %+v", wide)
	}
	if _, err := r.svc.LiveCategories(ctx, uuid.Nil, "square"); !errors.Is(err, ErrInvalidOrientation) {
		t.Fatalf("unknown orientation: %v", err)
	}
	// Nothing on air: an empty list, not null.
	empty := newSurfRig()
	got, err = empty.svc.LiveCategories(ctx, uuid.Nil, "")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nothing live: %#v %v", got, err)
	}
}

func TestLiveCreators(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	r := newSurfRig()
	r.profiles.m[a] = Profile{Name: "A", Handle: "a"}
	aSmall := r.live(a, 5, "landscape", "")
	aBig := r.live(a, 80, "portrait", "")
	bOnly := r.live(b, 20, "landscape", "")
	_ = aSmall
	got, err := r.svc.LiveCreators(ctx, uuid.Nil, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("one row per creator: %+v", got)
	}
	if got[0].StreamID != aBig.ID || got[0].ViewerCount != 80 || got[0].Orientation != "portrait" || got[0].Creator == nil || got[0].Creator.Handle != "a" {
		t.Fatalf("first: %+v %+v", got[0], got[0].Creator)
	}
	if got[1].StreamID != bOnly.ID || got[1].Creator == nil || got[1].Creator.UserID != b || got[1].Creator.Name != "" {
		t.Fatalf("second: %+v %+v", got[1], got[1].Creator)
	}
	// ?orientation=: the creator's most-watched stream OF that orientation.
	wide, err := r.svc.LiveCreators(ctx, uuid.Nil, 0, "landscape")
	if err != nil || len(wide) != 2 || wide[0].StreamID != bOnly.ID || wide[1].StreamID != aSmall.ID {
		t.Fatalf("landscape creators: %+v %v", wide, err)
	}
	if _, err := r.svc.LiveCreators(ctx, uuid.Nil, 0, "square"); !errors.Is(err, ErrInvalidOrientation) {
		t.Fatalf("unknown orientation: %v", err)
	}
	// limit: default 10, at most 50.
	for i := 0; i < 60; i++ {
		r.live(uuid.New(), 1, "landscape", "")
	}
	for limit, want := range map[int]int{0: 10, -3: 10, 1: 1, 25: 25, 50: 50, 500: 50} {
		got, _ := r.svc.LiveCreators(ctx, uuid.Nil, limit, "")
		if len(got) != want {
			t.Fatalf("limit %d: %d rows, want %d", limit, len(got), want)
		}
	}
}

func TestUserStreams(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()
	r := newSurfRig(host)
	liveNow := r.live(host, 3, "landscape", "")
	r.live(uuid.New(), 9, "landscape", "") // someone else's
	soon := r.scheduled(host, time.Hour)
	overdue := r.scheduled(host, -time.Hour)
	draft := r.store.AddStreamStatus(host, stScheduled)  // no time at all
	draft2 := r.store.AddStreamStatus(host, stScheduled) // another: the keyset must walk among them
	if draft2.ID.String() < draft.ID.String() {
		draft, draft2 = draft2, draft
	}

	// Past: two that were live (older first created), one that never went
	// live, one that failed.
	endAfter := func(d time.Duration) *postgres.LiveStream {
		st := r.live(host, 0, "landscape", "")
		r.clock.Advance(d)
		if _, err := r.svc.EndStream(ctx, st.ID, host); err != nil {
			t.Fatal(err)
		}
		return r.store.Stream(st.ID)
	}
	older := endAfter(time.Minute)
	newer := endAfter(time.Minute)
	neverLive := r.store.AddStreamStatus(host, stScheduled)
	if _, err := r.svc.EndStream(ctx, neverLive.ID, host); err != nil {
		t.Fatal(err)
	}
	failed := r.store.AddStreamStatus(host, stFailed)
	_ = failed

	get := func(v uuid.UUID, status string) []*postgres.LiveStream {
		t.Helper()
		res, err := r.svc.UserStreams(ctx, v, host, status, 20, "")
		if err != nil {
			t.Fatalf("%s: %v", status, err)
		}
		return res.Streams
	}
	sameIDs(t, "live", get(viewer, "live"), liveNow)
	sameIDs(t, "default is live", get(viewer, ""), liveNow)
	sameIDs(t, "upcoming for a viewer", get(viewer, "upcoming"), soon)
	// The creator gets every unstarted stream: overdue first (soonest
	// first), the timeless ones last. Nobody else does.
	sameIDs(t, "upcoming for the creator", get(host, "upcoming"), overdue, soon, draft, draft2)
	sameIDs(t, "upcoming for a signed-out caller", get(uuid.Nil, "upcoming"), soon)
	var walked []*postgres.LiveStream
	cur := ""
	for i := 0; i < 8; i++ {
		page, err := r.svc.UserStreams(ctx, host, host, "upcoming", 1, cur)
		if err != nil {
			t.Fatal(err)
		}
		walked = append(walked, page.Streams...)
		if cur = page.NextCursor; cur == "" {
			break
		}
	}
	sameIDs(t, "the creator's unstarted list, paged by one", walked, overdue, soon, draft, draft2)
	sameIDs(t, "past", get(viewer, "past"), newer, older)
	for _, st := range get(viewer, "past") {
		if st.Status != stEnded || st.StartedAt == nil {
			t.Fatalf("a past row that was never live: %+v", st)
		}
		if st.RecordingPostID != nil {
			t.Fatalf("recording_post_id has no writer yet: %v", st.RecordingPostID)
		}
	}
	if up := get(viewer, "upcoming"); up[0].ReminderCount == nil || up[0].ReminderSet == nil {
		t.Fatal("a creator's upcoming rows carry no reminder fields")
	}
	if _, err := r.svc.UserStreams(ctx, viewer, host, "scheduled", 20, ""); !errors.Is(err, ErrInvalidUserStreamsStatus) {
		t.Fatalf("unknown status: %v", err)
	}
	// Paging the past list.
	res, _ := r.svc.UserStreams(ctx, viewer, host, "past", 1, "")
	sameIDs(t, "past page 1", res.Streams, newer)
	res, _ = r.svc.UserStreams(ctx, viewer, host, "past", 1, res.NextCursor)
	sameIDs(t, "past page 2", res.Streams, older)
}

// --- reminders ---

func TestReminderRules(t *testing.T) {
	host, viewer, other := uuid.New(), uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.scheduled(host, time.Hour)

	res, err := r.svc.SetReminder(ctx, st.ID, viewer, true)
	if err != nil || !res.ReminderSet || res.ReminderCount != 1 {
		t.Fatalf("set: %+v %v", res, err)
	}
	// Idempotent.
	res, err = r.svc.SetReminder(ctx, st.ID, viewer, true)
	if err != nil || !res.ReminderSet || res.ReminderCount != 1 {
		t.Fatalf("set twice: %+v %v", res, err)
	}
	res, _ = r.svc.SetReminder(ctx, st.ID, other, true)
	if res.ReminderCount != 2 {
		t.Fatalf("second viewer: %+v", res)
	}
	res, err = r.svc.SetReminder(ctx, st.ID, viewer, false)
	if err != nil || res.ReminderSet || res.ReminderCount != 1 {
		t.Fatalf("delete: %+v %v", res, err)
	}
	res, err = r.svc.SetReminder(ctx, st.ID, viewer, false)
	if err != nil || res.ReminderSet || res.ReminderCount != 1 {
		t.Fatalf("delete twice: %+v %v", res, err)
	}
	// The waiting page's read carries the same.
	got, _ := r.svc.GetStream(ctx, st.ID, other)
	if got.ReminderSet == nil || !*got.ReminderSet || *got.ReminderCount != 1 {
		t.Fatalf("GET of a scheduled stream: set=%v count=%v", got.ReminderSet, got.ReminderCount)
	}

	if _, err := r.svc.SetReminder(ctx, uuid.New(), viewer, true); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("unknown stream: %v", err)
	}
	// Only while scheduled, both ways.
	for _, status := range []string{stStarting, stLive, stReconnecting, stEnded, stFailed} {
		s2 := r.store.AddStreamStatus(host, status)
		for _, set := range []bool{true, false} {
			if _, err := r.svc.SetReminder(ctx, s2.ID, viewer, set); !errors.Is(err, ErrStateConflict) {
				t.Fatalf("reminder(set=%v) on a %s stream: %v", set, status, err)
			}
		}
		if n := len(r.store.Reminders[s2.ID]); n != 0 {
			t.Fatalf("a %s stream holds %d reminders", status, n)
		}
	}
	// The detail row carries the reminder fields in every status: the count
	// for everyone, reminder_set for a signed-in caller only.
	started, _ := r.svc.StartStream(ctx, st.ID, host)
	_ = r.svc.HandleWebhook(ctx, hostEvent(started.Stream, "track_published"))
	for _, tc := range []struct {
		caller uuid.UUID
		set    *bool
	}{{other, boolPtr(true)}, {viewer, boolPtr(false)}, {uuid.Nil, nil}} {
		row, err := r.svc.GetStream(ctx, st.ID, tc.caller)
		if err != nil {
			t.Fatal(err)
		}
		if row.Status != stLive || row.ReminderCount == nil || *row.ReminderCount != 1 || !reflect.DeepEqual(row.ReminderSet, tc.set) {
			t.Fatalf("detail row of a live stream for %s: count=%v set=%v", tc.caller, row.ReminderCount, row.ReminderSet)
		}
	}
}

// TestReminderViewerCheck: a reminder needs a stream the viewer may watch.
func TestReminderViewerCheck(t *testing.T) {
	host, follower, stranger, blocked, banned := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.scheduled(host, time.Hour)
	r.store.Streams[st.ID].Visibility = visibilityFollowers
	r.graph.follows[follower.String()+":"+host.String()] = true
	r.graph.follows[banned.String()+":"+host.String()] = true
	r.graph.blocked[blocked.String()+":"+host.String()] = true
	_ = r.store.BanFromStream(ctx, st.ID, banned, host, "")

	if _, err := r.svc.SetReminder(ctx, st.ID, follower, true); err != nil {
		t.Fatalf("follower: %v", err)
	}
	if _, err := r.svc.SetReminder(ctx, st.ID, host, true); err != nil {
		t.Fatalf("the host on their own stream: %v", err)
	}
	if _, err := r.svc.SetReminder(ctx, st.ID, stranger, true); !errors.Is(err, ErrNotFollower) {
		t.Fatalf("non-follower: %v", err)
	}
	if _, err := r.svc.SetReminder(ctx, st.ID, blocked, true); !errors.Is(err, ErrViewerBlocked) {
		t.Fatalf("blocked: %v", err)
	}
	if _, err := r.svc.SetReminder(ctx, st.ID, banned, true); !errors.Is(err, ErrBannedFromStream) {
		t.Fatalf("banned from the stream: %v", err)
	}
	r.graph.err = errors.New("graph down")
	if _, err := r.svc.SetReminder(ctx, st.ID, uuid.New(), true); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("graph down: %v", err)
	}
	if n := len(r.store.Reminders[st.ID]); n != 2 {
		t.Fatalf("reminders stored: %d, want the follower's and the host's", n)
	}
}

func TestReminderUserIDsPaging(t *testing.T) {
	host := uuid.New()
	r := newSurfRig(host)
	st := r.scheduled(host, time.Hour)
	want := map[string]bool{}
	for i := 0; i < 5; i++ {
		u := uuid.New()
		want[u.String()] = true
		if _, err := r.svc.SetReminder(ctx, st.ID, u, true); err != nil {
			t.Fatal(err)
		}
	}
	// The reminders outlive the start: that is when they are read.
	started, _ := r.svc.StartStream(ctx, st.ID, host)
	_ = r.svc.HandleWebhook(ctx, hostEvent(started.Stream, "track_published"))

	seen := map[string]bool{}
	after, pages, last := uuid.Nil, 0, ""
	for {
		page, err := r.svc.ReminderUserIDs(ctx, st.ID, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, id := range page.UserIDs {
			if seen[id] || id <= last {
				t.Fatalf("page %d repeats or is out of order: %s after %s", pages, id, last)
			}
			seen[id], last = true, id
		}
		if page.HasMore != (len(page.UserIDs) == 2) {
			t.Fatalf("has_more=%v with %d ids on a page of 2", page.HasMore, len(page.UserIDs))
		}
		if len(page.UserIDs) > 0 && page.NextAfter != page.UserIDs[len(page.UserIDs)-1] {
			t.Fatalf("next_after %q is not the last id", page.NextAfter)
		}
		if !page.HasMore {
			break
		}
		after = uuid.MustParse(page.NextAfter)
	}
	if !reflect.DeepEqual(seen, want) || pages != 3 {
		t.Fatalf("paged %d ids in %d pages, want 5 in 3", len(seen), pages)
	}
	// No reminders: an empty page, not null.
	plain := r.store.AddStream(host)
	page, err := r.svc.ReminderUserIDs(ctx, plain.ID, uuid.Nil, 0)
	if err != nil || page.UserIDs == nil || len(page.UserIDs) != 0 || page.HasMore || page.NextAfter != "" {
		t.Fatalf("no reminders: %+v %v", page, err)
	}
	if _, err := r.svc.ReminderUserIDs(ctx, uuid.New(), uuid.Nil, 10); !errors.Is(err, ErrStreamNotFound) {
		t.Fatalf("unknown stream: %v", err)
	}
}

// --- live.stream.started ---

func TestStartedEventPayload(t *testing.T) {
	host := uuid.New()
	r := newSurfRig(host)
	st, _ := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "Portrait jam", Visibility: "followers", Orientation: "portrait", Category: "music"})
	for i := 0; i < 3; i++ {
		u := uuid.New()
		r.graph.follows[u.String()+":"+host.String()] = true
		if _, err := r.svc.SetReminder(ctx, st.ID, u, true); err != nil {
			t.Fatal(err)
		}
	}
	started, _ := r.svc.StartStream(ctx, st.ID, host)
	if err := r.svc.HandleWebhook(ctx, hostEvent(started.Stream, "track_published")); err != nil {
		t.Fatal(err)
	}
	if len(r.store.Outbox) != 1 || r.store.Outbox[0].EventType != sharedevents.LiveStreamStarted {
		t.Fatalf("outbox: %v", outboxTypes(r.rig))
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(r.store.Outbox[0].Payload, &env); err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(env.Payload, &raw); err != nil {
		t.Fatalf("payload is not an object: %v\n%s", err, env.Payload)
	}
	want := map[string]any{
		"stream_id": st.ID.String(), "creator_id": host.String(), "creator_user_id": host.String(),
		"title": "Portrait jam", "visibility": "followers", "orientation": "portrait", "category": "music",
	}
	for k, v := range want {
		if raw[k] != v {
			t.Fatalf("payload %s = %v, want %v\n%s", k, raw[k], v, env.Payload)
		}
	}
	if _, has := raw["reminder_user_ids"]; has {
		t.Fatal("the event carries reminder_user_ids; it must be paged from the internal route")
	}
	for k := range raw {
		if _, known := want[k]; !known && k != "started_at" {
			t.Fatalf("unexpected payload field %q", k)
		}
	}
	// The shared type decodes it (what notification-service does).
	var p sharedevents.LiveStreamStartedPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.Orientation != "portrait" || p.Category != "music" || p.CreatorUserID != host.String() || p.StartedAt.IsZero() {
		t.Fatalf("shared decode: %+v %v", p, err)
	}

	// A stream with neither: landscape, and no category key at all.
	plain, _ := r.svc.CreateStream(ctx, host, CreateStreamParams{Title: "Wide"})
	ps, _ := r.svc.StartStream(ctx, plain.ID, host)
	_ = r.svc.HandleWebhook(ctx, hostEvent(ps.Stream, "track_published"))
	_ = json.Unmarshal(r.store.Outbox[1].Payload, &env)
	raw = map[string]any{}
	_ = json.Unmarshal(env.Payload, &raw)
	if raw["orientation"] != "landscape" {
		t.Fatalf("default orientation in the event: %v", raw["orientation"])
	}
	if _, has := raw["category"]; has {
		t.Fatalf("an empty category is on the wire: %s", env.Payload)
	}
}

// TestVisibleBoolsLineUp guards the helper every listing leans on.
func TestVisibleBoolsLineUp(t *testing.T) {
	viewer, blockedHost := uuid.New(), uuid.New()
	r := newSurfRig()
	r.graph.blocked[viewer.String()+":"+blockedHost.String()] = true
	rows := []*postgres.LiveStream{
		{ID: uuid.New(), CreatorUserID: uuid.New(), Visibility: visibilityPublic},
		{ID: uuid.New(), CreatorUserID: blockedHost, Visibility: visibilityPublic},
		{ID: uuid.New(), CreatorUserID: uuid.New(), Visibility: visibilityFollowers},
		{ID: uuid.New(), CreatorUserID: viewer, Visibility: visibilityFollowers},
		{ID: uuid.New(), CreatorUserID: uuid.New(), Visibility: visibilityPaid},
	}
	want := []bool{true, false, false, true, false}
	for _, g := range []GraphClient{r.graph, &batchFakeGraph{fakeGraph: r.graph}} {
		r.svc.graph = g
		if got := r.svc.visible(ctx, viewer, rows); !reflect.DeepEqual(got, want) {
			t.Fatalf("%T: visible = %v, want %v", g, got, want)
		}
	}
	if got := r.svc.visible(ctx, uuid.Nil, rows); !reflect.DeepEqual(got, []bool{true, true, false, false, false}) {
		t.Fatalf("signed out: %v", got)
	}

}

func boolPtr(b bool) *bool { return &b }
