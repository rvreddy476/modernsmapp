package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Video series: visibility, duplicate episodes and removal (2026-09-10).
//
// Three defects are pinned here, all reproduced against the running stack
// before the fix:
//
//   - a series created with is_public:false answered its full body to another
//     logged-in account AND to a caller with no token at all, and its episode
//     list had no gate either;
//   - ListCreatorVideoSeries handed a stranger the creator's private series;
//   - the same post id was accepted at episode 1 and episode 3 of one series
//     (201 both times), so the watch page's next-episode control offered a
//     loop back to the video already playing.
//
// The fake below stands in for the Postgres store (videoSeriesStore), the
// same way video_authoring_authz_test.go fakes videoAuthoringStore: these are
// assertions about the decision and must not need a live database.

type fakeVideoSeriesStore struct {
	series   map[uuid.UUID]*postgres.VideoSeries
	list     []postgres.VideoSeries
	episodes []postgres.VideoSeriesEpisode

	getErr error

	deletedSeries   []uuid.UUID
	deletedByNum    []int
	deletedByPost   []uuid.UUID
	episodesFetched int
	addCalls        int

	// memberships is what FindSeriesMembershipsByPost answers, already in
	// the store's order (newest added_at first).
	memberships []postgres.VideoSeriesEpisode
	// includeUnpublishedSeen records the flag each episode read carried, so
	// a test can prove a stranger's read asked for the filtered list.
	includeUnpublishedSeen []bool
	// patches records every UpdateVideoSeries that reached the store.
	patches []postgres.VideoSeriesPatch
}

func (f *fakeVideoSeriesStore) GetVideoSeries(_ context.Context, id uuid.UUID) (*postgres.VideoSeries, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.series[id], nil
}

func (f *fakeVideoSeriesStore) ListVideoSeriesByCreator(_ context.Context, _ uuid.UUID, _, _ int) ([]postgres.VideoSeries, error) {
	return f.list, nil
}

func (f *fakeVideoSeriesStore) GetVideoSeriesEpisodes(_ context.Context, _ uuid.UUID, includeUnpublished bool) ([]postgres.VideoSeriesEpisode, error) {
	f.episodesFetched++
	f.includeUnpublishedSeen = append(f.includeUnpublishedSeen, includeUnpublished)
	return f.episodes, nil
}

func (f *fakeVideoSeriesStore) FindSeriesMembershipsByPost(_ context.Context, postID uuid.UUID) ([]postgres.VideoSeriesEpisode, error) {
	var out []postgres.VideoSeriesEpisode
	for _, m := range f.memberships {
		if m.PostID == postID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeVideoSeriesStore) UpdateVideoSeries(_ context.Context, id uuid.UUID, patch postgres.VideoSeriesPatch) (*postgres.VideoSeries, error) {
	f.patches = append(f.patches, patch)
	vs := f.series[id]
	if vs == nil {
		return nil, nil
	}
	if patch.Title != nil {
		vs.Title = *patch.Title
	}
	if patch.IsPublic != nil {
		vs.IsPublic = *patch.IsPublic
	}
	return vs, nil
}

func (f *fakeVideoSeriesStore) AddEpisodeToVideoSeries(_ context.Context, seriesID, postID uuid.UUID, episodeNum int, title *string) (*postgres.VideoSeriesEpisode, error) {
	f.addCalls++
	// Mirrors the store's in-transaction guard so the service test sees the
	// same refusal a real insert would produce.
	for _, ep := range f.episodes {
		if ep.PostID == postID && ep.EpisodeNum != episodeNum {
			return nil, postgres.ErrEpisodePostAlreadyInSeries
		}
	}
	ep := postgres.VideoSeriesEpisode{SeriesID: seriesID, PostID: postID, EpisodeNum: episodeNum, Title: title}
	for i := range f.episodes {
		if f.episodes[i].EpisodeNum == episodeNum {
			// ON CONFLICT (series_id, episode_num) DO UPDATE: an overwrite,
			// so the cap below does not apply.
			f.episodes[i] = ep
			return &ep, nil
		}
	}
	if len(f.episodes) >= postgres.MaxSeriesEpisodes {
		return nil, postgres.ErrVideoSeriesFull
	}
	f.episodes = append(f.episodes, ep)
	return &ep, nil
}

func (f *fakeVideoSeriesStore) FindVideoSeriesEpisodeByPost(_ context.Context, _ uuid.UUID, postID uuid.UUID) (*postgres.VideoSeriesEpisode, error) {
	for i := range f.episodes {
		if f.episodes[i].PostID == postID {
			return &f.episodes[i], nil
		}
	}
	return nil, nil
}

func (f *fakeVideoSeriesStore) DeleteVideoSeries(_ context.Context, id uuid.UUID) error {
	f.deletedSeries = append(f.deletedSeries, id)
	return nil
}

func (f *fakeVideoSeriesStore) DeleteVideoSeriesEpisodeByNum(_ context.Context, _ uuid.UUID, episodeNum int) (bool, error) {
	for i, ep := range f.episodes {
		if ep.EpisodeNum == episodeNum {
			f.episodes = append(f.episodes[:i], f.episodes[i+1:]...)
			f.deletedByNum = append(f.deletedByNum, episodeNum)
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeVideoSeriesStore) DeleteVideoSeriesEpisodeByPost(_ context.Context, _ uuid.UUID, postID uuid.UUID) (bool, error) {
	for i, ep := range f.episodes {
		if ep.PostID == postID {
			f.episodes = append(f.episodes[:i], f.episodes[i+1:]...)
			f.deletedByPost = append(f.deletedByPost, postID)
			return true, nil
		}
	}
	return false, nil
}

// newSeriesService wires ONLY the series store. pgStore stays nil, so a
// refusal that stopped refusing would panic on the nil store rather than pass
// quietly.
func newSeriesService(store videoSeriesStore) *Service {
	s := &Service{}
	s.videoSeries = store
	return s
}

func seriesFixture(id, creator uuid.UUID, public bool) *fakeVideoSeriesStore {
	vs := postgres.VideoSeries{ID: id, CreatorID: creator, Title: "s", IsPublic: public}
	return &fakeVideoSeriesStore{
		series: map[uuid.UUID]*postgres.VideoSeries{id: &vs},
		list:   []postgres.VideoSeries{vs},
	}
}

// ── is_public on the single-series read ─────────────────────────────────────

func TestGetVideoSeriesHidesPrivateSeries(t *testing.T) {
	owner, stranger, id := uuid.New(), uuid.New(), uuid.New()

	t.Run("stranger", func(t *testing.T) {
		got, err := newSeriesService(seriesFixture(id, owner, false)).
			GetVideoSeries(context.Background(), id, &stranger)
		if !errors.Is(err, ErrVideoSeriesPrivate) {
			t.Fatalf("stranger reading a private series: err=%v want ErrVideoSeriesPrivate", err)
		}
		if got != nil {
			t.Fatalf("stranger got the body of a private series (%q)", got.Title)
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		got, err := newSeriesService(seriesFixture(id, owner, false)).
			GetVideoSeries(context.Background(), id, nil)
		if !errors.Is(err, ErrVideoSeriesPrivate) {
			t.Fatalf("anonymous reading a private series: err=%v want ErrVideoSeriesPrivate", err)
		}
		if got != nil {
			t.Fatalf("a caller with no token got the body of a private series (%q)", got.Title)
		}
	})

	t.Run("owner still reads it", func(t *testing.T) {
		got, err := newSeriesService(seriesFixture(id, owner, false)).
			GetVideoSeries(context.Background(), id, &owner)
		if err != nil || got == nil {
			t.Fatalf("owner: got=%v err=%v; the creator must still see their own series", got, err)
		}
	})
}

// A public series stays public — the gate must not lock viewers out.
func TestGetVideoSeriesAllowsPublicSeriesAnonymously(t *testing.T) {
	id := uuid.New()
	got, err := newSeriesService(seriesFixture(id, uuid.New(), true)).
		GetVideoSeries(context.Background(), id, nil)
	if err != nil || got == nil {
		t.Fatalf("public series anonymously: got=%v err=%v; want the series, no error", got, err)
	}
}

// The episode list is the series' contents; it must answer the same way.
func TestGetVideoSeriesEpisodesHidesPrivateSeries(t *testing.T) {
	owner, stranger, id := uuid.New(), uuid.New(), uuid.New()
	store := seriesFixture(id, owner, false)
	store.episodes = []postgres.VideoSeriesEpisode{{SeriesID: id, PostID: uuid.New(), EpisodeNum: 1}}

	eps, err := newSeriesService(store).GetVideoSeriesEpisodes(context.Background(), id, &stranger)
	if !errors.Is(err, ErrVideoSeriesPrivate) {
		t.Fatalf("stranger listing a private series' episodes: err=%v want ErrVideoSeriesPrivate", err)
	}
	if len(eps) != 0 {
		t.Fatalf("stranger got %d episodes of a private series; want none", len(eps))
	}
	if store.episodesFetched != 0 {
		t.Fatal("the episode rows were read before the visibility decision; refuse first")
	}
}

// ── DECISION 2: the creator listing omits private series for strangers ──────

func TestListCreatorVideoSeriesOmitsPrivateFromStrangers(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	pub := postgres.VideoSeries{ID: uuid.New(), CreatorID: owner, Title: "public one", IsPublic: true}
	prv := postgres.VideoSeries{ID: uuid.New(), CreatorID: owner, Title: "private one", IsPublic: false}
	store := &fakeVideoSeriesStore{list: []postgres.VideoSeries{pub, prv}}
	svc := newSeriesService(store)

	t.Run("stranger", func(t *testing.T) {
		got, err := svc.ListVideoSeriesByCreator(context.Background(), owner, &stranger, 20, 0)
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		for _, vs := range got {
			if !vs.IsPublic {
				t.Fatalf("a stranger's listing contains the private series %q", vs.Title)
			}
		}
		if len(got) != 1 {
			t.Fatalf("stranger got %d series, want 1 (the public one)", len(got))
		}
	})

	t.Run("anonymous", func(t *testing.T) {
		got, err := svc.ListVideoSeriesByCreator(context.Background(), owner, nil, 20, 0)
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("anonymous got %d series, want 1 (the public one)", len(got))
		}
	})

	// The other half of the decision: it is a filter, not a refusal, and the
	// creator's own listing is complete.
	t.Run("creator sees both", func(t *testing.T) {
		got, err := svc.ListVideoSeriesByCreator(context.Background(), owner, &owner, 20, 0)
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("the creator got %d of their own series, want 2 — a private series must "+
				"still be listed to the person who made it", len(got))
		}
	})
}

// ── one post, at most one episode ───────────────────────────────────────────

func TestAddEpisodeRefusesPostAlreadyInSeries(t *testing.T) {
	owner, id, postID := uuid.New(), uuid.New(), uuid.New()
	store := seriesFixture(id, owner, true)
	svc := newSeriesService(store)

	if _, err := svc.AddEpisodeToVideoSeries(context.Background(), owner, id, postID, 1, nil); err != nil {
		t.Fatalf("first add: unexpected err %v", err)
	}
	_, err := svc.AddEpisodeToVideoSeries(context.Background(), owner, id, postID, 3, nil)
	if !errors.Is(err, ErrEpisodePostDuplicate) {
		t.Fatalf("the same post accepted at episode 1 and episode 3: err=%v want ErrEpisodePostDuplicate.\n"+
			"A series that lists one video twice offers a next-episode link back to the video already playing.", err)
	}
	if len(store.episodes) != 1 {
		t.Fatalf("series holds %d episodes after the refused add; want 1", len(store.episodes))
	}
}

// Re-adding a post at the number it already holds is an UPDATE (a retitle),
// not a conflict. The guard must not break that.
func TestAddEpisodeAllowsSamePostAtSameEpisodeNumber(t *testing.T) {
	owner, id, postID := uuid.New(), uuid.New(), uuid.New()
	svc := newSeriesService(seriesFixture(id, owner, true))
	if _, err := svc.AddEpisodeToVideoSeries(context.Background(), owner, id, postID, 2, nil); err != nil {
		t.Fatalf("first add: unexpected err %v", err)
	}
	title := "renamed"
	if _, err := svc.AddEpisodeToVideoSeries(context.Background(), owner, id, postID, 2, &title); err != nil {
		t.Fatalf("re-adding the same post at the same episode number: err=%v; want it accepted", err)
	}
}

// ── removal ─────────────────────────────────────────────────────────────────

func TestDeleteVideoSeriesRequiresOwnership(t *testing.T) {
	owner, attacker, id := uuid.New(), uuid.New(), uuid.New()

	t.Run("stranger", func(t *testing.T) {
		store := seriesFixture(id, owner, true)
		if err := newSeriesService(store).DeleteVideoSeries(context.Background(), attacker, id); !errors.Is(err, ErrNotVideoSeriesOwner) {
			t.Fatalf("stranger deleting a series: err=%v want ErrNotVideoSeriesOwner", err)
		}
		if len(store.deletedSeries) != 0 {
			t.Fatal("the series was deleted for a caller who does not own it")
		}
	})

	t.Run("missing series", func(t *testing.T) {
		store := &fakeVideoSeriesStore{series: map[uuid.UUID]*postgres.VideoSeries{}}
		if err := newSeriesService(store).DeleteVideoSeries(context.Background(), owner, id); !errors.Is(err, ErrVideoSeriesNotFound) {
			t.Fatalf("deleting a series that is not there: err=%v want ErrVideoSeriesNotFound", err)
		}
	})

	t.Run("owner", func(t *testing.T) {
		store := seriesFixture(id, owner, true)
		if err := newSeriesService(store).DeleteVideoSeries(context.Background(), owner, id); err != nil {
			t.Fatalf("owner deleting their own series: unexpected err %v", err)
		}
		if len(store.deletedSeries) != 1 || store.deletedSeries[0] != id {
			t.Fatalf("owner's delete did not reach the store: %v", store.deletedSeries)
		}
	})
}

func TestDeleteVideoSeriesEpisode(t *testing.T) {
	owner, attacker, id := uuid.New(), uuid.New(), uuid.New()
	postA, postB := uuid.New(), uuid.New()

	fixture := func() *fakeVideoSeriesStore {
		store := seriesFixture(id, owner, true)
		store.episodes = []postgres.VideoSeriesEpisode{
			{SeriesID: id, PostID: postA, EpisodeNum: 1},
			{SeriesID: id, PostID: postB, EpisodeNum: 2},
		}
		return store
	}

	t.Run("by episode number", func(t *testing.T) {
		store := fixture()
		if err := newSeriesService(store).DeleteVideoSeriesEpisodeByNum(context.Background(), owner, id, 2); err != nil {
			t.Fatalf("owner removing episode 2: unexpected err %v", err)
		}
		if len(store.episodes) != 1 || store.episodes[0].EpisodeNum != 1 {
			t.Fatalf("after removing episode 2 the series holds %v", store.episodes)
		}
	})

	t.Run("by post id", func(t *testing.T) {
		store := fixture()
		if err := newSeriesService(store).DeleteVideoSeriesEpisodeByPost(context.Background(), owner, id, postA); err != nil {
			t.Fatalf("owner removing an episode by post id: unexpected err %v", err)
		}
		if len(store.episodes) != 1 || store.episodes[0].PostID != postB {
			t.Fatalf("after removing post A the series holds %v", store.episodes)
		}
	})

	t.Run("stranger", func(t *testing.T) {
		store := fixture()
		if err := newSeriesService(store).DeleteVideoSeriesEpisodeByNum(context.Background(), attacker, id, 1); !errors.Is(err, ErrNotVideoSeriesOwner) {
			t.Fatalf("stranger removing an episode: err=%v want ErrNotVideoSeriesOwner", err)
		}
		if len(store.episodes) != 2 {
			t.Fatal("a stranger removed an episode from someone else's series")
		}
	})

	t.Run("episode that is not there", func(t *testing.T) {
		store := fixture()
		if err := newSeriesService(store).DeleteVideoSeriesEpisodeByNum(context.Background(), owner, id, 9); !errors.Is(err, ErrVideoSeriesEpisodeNotFound) {
			t.Fatalf("removing an episode that does not exist: err=%v want ErrVideoSeriesEpisodeNotFound", err)
		}
	})
}

// DECISION 1, pinned: removing episode 2 of 3 leaves 1 and 3. If someone
// later decides renumbering is better, this test is the place that argument
// has to be had.
func TestRemovingAnEpisodeLeavesAGapRatherThanRenumbering(t *testing.T) {
	owner, id := uuid.New(), uuid.New()
	store := seriesFixture(id, owner, true)
	store.episodes = []postgres.VideoSeriesEpisode{
		{SeriesID: id, PostID: uuid.New(), EpisodeNum: 1},
		{SeriesID: id, PostID: uuid.New(), EpisodeNum: 2},
		{SeriesID: id, PostID: uuid.New(), EpisodeNum: 3},
	}
	if err := newSeriesService(store).DeleteVideoSeriesEpisodeByNum(context.Background(), owner, id, 2); err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	var nums []int
	for _, ep := range store.episodes {
		nums = append(nums, ep.EpisodeNum)
	}
	if len(nums) != 2 || nums[0] != 1 || nums[1] != 3 {
		t.Fatalf("episode numbers after removing 2 of 3: %v; want [1 3] — the numbers are labels "+
			"the audience has already seen and are not shifted down", nums)
	}
}

// ── fail closed ─────────────────────────────────────────────────────────────

func TestVideoSeriesFlowsFailClosedWithoutStore(t *testing.T) {
	s := &Service{}
	if _, err := s.GetVideoSeries(context.Background(), uuid.New(), nil); !errors.Is(err, ErrVideoSeriesStoreUnavailable) {
		t.Fatalf("read with no store: err=%v want ErrVideoSeriesStoreUnavailable", err)
	}
	if err := s.DeleteVideoSeries(context.Background(), uuid.New(), uuid.New()); !errors.Is(err, ErrVideoSeriesStoreUnavailable) {
		t.Fatalf("delete with no store: err=%v want ErrVideoSeriesStoreUnavailable", err)
	}
	if _, err := s.ListVideoSeriesByCreator(context.Background(), uuid.New(), nil, 20, 0); !errors.Is(err, ErrVideoSeriesStoreUnavailable) {
		t.Fatalf("listing with no store: err=%v want ErrVideoSeriesStoreUnavailable", err)
	}
}
