package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Video series: the watch page's view, the caps and editing (2026-09-12).
//
// The watch page needs "which series is this video in, and what are the
// previous and next episodes" from a POST id, not a series id. Before this
// the client fetched every series of the creator and scanned the episode
// lists, which the private-series gate then broke. Same fake store, same
// no-database rule as video_series_visibility_test.go.

func watchFixture(t *testing.T, owner uuid.UUID, public bool, nums ...int) (*fakeVideoSeriesStore, uuid.UUID, map[int]uuid.UUID) {
	t.Helper()
	id := uuid.New()
	store := seriesFixture(id, owner, public)
	posts := map[int]uuid.UUID{}
	for _, n := range nums {
		p := uuid.New()
		posts[n] = p
		ep := postgres.VideoSeriesEpisode{SeriesID: id, PostID: p, EpisodeNum: n}
		store.episodes = append(store.episodes, ep)
		store.memberships = append(store.memberships, ep)
	}
	return store, id, posts
}

// ── the private gate answers "no such thing", not "not yours" ───────────────

func TestGetPostSeriesHidesPrivateSeriesFromStrangers(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	store, _, posts := watchFixture(t, owner, false, 1, 2)

	for name, caller := range map[string]*uuid.UUID{"stranger": &stranger, "anonymous": nil} {
		t.Run(name, func(t *testing.T) {
			store.episodesFetched = 0
			view, err := newSeriesService(store).GetPostSeries(context.Background(), posts[1], caller)
			if !errors.Is(err, ErrPostNotInSeries) {
				t.Fatalf("%s reading a post's private series: err=%v want ErrPostNotInSeries (a 404, never a 403: "+
					"a 403 would confirm through a post id that a private series exists)", name, err)
			}
			if view != nil {
				t.Fatalf("%s got the view of a private series", name)
			}
			if store.episodesFetched != 0 {
				t.Fatal("the episode rows were read before the visibility decision; refuse first")
			}
		})
	}

	t.Run("owner", func(t *testing.T) {
		view, err := newSeriesService(store).GetPostSeries(context.Background(), posts[1], &owner)
		if err != nil || view == nil {
			t.Fatalf("owner: view=%v err=%v; the creator must still see their own series", view, err)
		}
		if seen := store.includeUnpublishedSeen; len(seen) == 0 || !seen[len(seen)-1] {
			t.Fatal("the owner's episode read must include unpublished (scheduled, deleted) episodes")
		}
	})
}

func TestGetPostSeriesAnswersNotFoundForAPostInNoSeries(t *testing.T) {
	store := seriesFixture(uuid.New(), uuid.New(), true)
	_, err := newSeriesService(store).GetPostSeries(context.Background(), uuid.New(), nil)
	if !errors.Is(err, ErrPostNotInSeries) {
		t.Fatalf("post in no series: err=%v want ErrPostNotInSeries", err)
	}
}

// A stranger's read must ask the store for the filtered list; the owner's
// for the full one.
func TestGetPostSeriesStrangerGetsPublishedEpisodesOnly(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	store, _, posts := watchFixture(t, owner, true, 1, 2)
	if _, err := newSeriesService(store).GetPostSeries(context.Background(), posts[1], &stranger); err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if seen := store.includeUnpublishedSeen; len(seen) != 1 || seen[0] {
		t.Fatalf("stranger's episode read carried includeUnpublished=%v; want [false]", seen)
	}
}

// ── next / previous step over gaps ──────────────────────────────────────────

func TestNeighboursStepOverGaps(t *testing.T) {
	p1, p2, p4 := uuid.New(), uuid.New(), uuid.New()
	eps := []postgres.VideoSeriesEpisode{
		{PostID: p1, EpisodeNum: 1},
		{PostID: p2, EpisodeNum: 2},
		{PostID: p4, EpisodeNum: 4},
	}

	t.Run("middle, with a gap after", func(t *testing.T) {
		prev, next := neighbours(eps, p2)
		if prev == nil || prev.EpisodeNum != 1 {
			t.Fatalf("prev of episode 2 = %v; want episode 1", prev)
		}
		if next == nil || next.EpisodeNum != 4 {
			t.Fatalf("next of episode 2 = %v; want episode 4 (3 was removed and left a gap, DECISION 1)", next)
		}
	})

	t.Run("last", func(t *testing.T) {
		prev, next := neighbours(eps, p4)
		if next != nil {
			t.Fatalf("next of the last episode = %v; want nil", next)
		}
		if prev == nil || prev.EpisodeNum != 2 {
			t.Fatalf("prev of episode 4 = %v; want episode 2", prev)
		}
	})

	t.Run("first", func(t *testing.T) {
		prev, next := neighbours(eps, p1)
		if prev != nil {
			t.Fatalf("prev of the first episode = %v; want nil", prev)
		}
		if next == nil || next.EpisodeNum != 2 {
			t.Fatalf("next of episode 1 = %v; want episode 2", next)
		}
	})

	// The store orders by episode_num, but the helper must not depend on it:
	// a caller that hands over rows in another order gets the same answer.
	t.Run("unsorted input", func(t *testing.T) {
		shuffled := []postgres.VideoSeriesEpisode{eps[2], eps[0], eps[1]}
		prev, next := neighbours(shuffled, p2)
		if prev == nil || prev.EpisodeNum != 1 || next == nil || next.EpisodeNum != 4 {
			t.Fatalf("unsorted: prev=%v next=%v; want 1 and 4", prev, next)
		}
	})

	t.Run("post not in the list", func(t *testing.T) {
		prev, next := neighbours(eps, uuid.New())
		if prev != nil || next != nil {
			t.Fatalf("a post that is not an episode has no neighbours; got prev=%v next=%v", prev, next)
		}
	})
}

func TestGetPostSeriesViewCarriesCurrentAndNeighbours(t *testing.T) {
	owner := uuid.New()
	store, id, posts := watchFixture(t, owner, true, 1, 2, 4)
	view, err := newSeriesService(store).GetPostSeries(context.Background(), posts[2], nil)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if view.Series == nil || view.Series.ID != id {
		t.Fatalf("view.Series = %v; want series %s", view.Series, id)
	}
	if view.Current.EpisodeNum != 2 {
		t.Fatalf("current episode = %d; want 2", view.Current.EpisodeNum)
	}
	if view.Prev == nil || view.Prev.PostID != posts[1] || view.Next == nil || view.Next.PostID != posts[4] {
		t.Fatalf("prev=%v next=%v; want posts 1 and 4", view.Prev, view.Next)
	}
	if len(view.Episodes) != 3 {
		t.Fatalf("view carries %d episodes; want 3", len(view.Episodes))
	}
}

// ── a post in two series: the newest membership wins ────────────────────────

func TestGetPostSeriesPrefersNewestMembership(t *testing.T) {
	owner, post := uuid.New(), uuid.New()
	older, newer := uuid.New(), uuid.New()
	store := &fakeVideoSeriesStore{series: map[uuid.UUID]*postgres.VideoSeries{
		older: {ID: older, CreatorID: owner, Title: "older", IsPublic: true},
		newer: {ID: newer, CreatorID: owner, Title: "newer", IsPublic: true},
	}}
	now := time.Now()
	// The store hands memberships back newest first; the fake is fed in
	// that order to match.
	store.memberships = []postgres.VideoSeriesEpisode{
		{SeriesID: newer, PostID: post, EpisodeNum: 3, AddedAt: now},
		{SeriesID: older, PostID: post, EpisodeNum: 1, AddedAt: now.Add(-time.Hour)},
	}
	store.episodes = []postgres.VideoSeriesEpisode{{SeriesID: newer, PostID: post, EpisodeNum: 3}}

	view, err := newSeriesService(store).GetPostSeries(context.Background(), post, nil)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if view.Series.ID != newer {
		t.Fatalf("a post in two series resolved to %q; want the newest membership (%q)", view.Series.Title, "newer")
	}
	if view.Current.EpisodeNum != 3 {
		t.Fatalf("current episode = %d; want 3 (the number in the newest series)", view.Current.EpisodeNum)
	}
}

// When the newest membership is a private series the stranger cannot see,
// the read falls through to the next visible one rather than 404ing on a
// public series the post is also in. The private series is still never
// confirmed to exist.
func TestGetPostSeriesFallsThroughAPrivateNewestMembership(t *testing.T) {
	owner, stranger, post := uuid.New(), uuid.New(), uuid.New()
	pub, prv := uuid.New(), uuid.New()
	store := &fakeVideoSeriesStore{series: map[uuid.UUID]*postgres.VideoSeries{
		pub: {ID: pub, CreatorID: owner, Title: "public", IsPublic: true},
		prv: {ID: prv, CreatorID: owner, Title: "private", IsPublic: false},
	}}
	store.memberships = []postgres.VideoSeriesEpisode{
		{SeriesID: prv, PostID: post, EpisodeNum: 1},
		{SeriesID: pub, PostID: post, EpisodeNum: 5},
	}
	store.episodes = []postgres.VideoSeriesEpisode{{SeriesID: pub, PostID: post, EpisodeNum: 5}}

	view, err := newSeriesService(store).GetPostSeries(context.Background(), post, &stranger)
	if err != nil {
		t.Fatalf("unexpected err %v", err)
	}
	if view.Series.ID != pub {
		t.Fatalf("resolved to %q; want the public series", view.Series.Title)
	}
}

// ── the 50-episode server bound ─────────────────────────────────────────────

func TestAddEpisodeRefusesTheFiftyFirstEpisode(t *testing.T) {
	owner, id := uuid.New(), uuid.New()
	store := seriesFixture(id, owner, true)
	for n := 1; n <= postgres.MaxSeriesEpisodes; n++ {
		store.episodes = append(store.episodes, postgres.VideoSeriesEpisode{SeriesID: id, PostID: uuid.New(), EpisodeNum: n})
	}
	svc := newSeriesService(store)

	_, err := svc.AddEpisodeToVideoSeries(context.Background(), owner, id, uuid.New(), postgres.MaxSeriesEpisodes+1, nil)
	if !errors.Is(err, ErrVideoSeriesFull) {
		t.Fatalf("episode %d of a full series: err=%v want ErrVideoSeriesFull", postgres.MaxSeriesEpisodes+1, err)
	}
	if len(store.episodes) != postgres.MaxSeriesEpisodes {
		t.Fatalf("series grew to %d episodes past the cap", len(store.episodes))
	}

	// Overwriting an occupied number is not growth, so a full series can
	// still be edited.
	title := "recut"
	if _, err := svc.AddEpisodeToVideoSeries(context.Background(), owner, id, uuid.New(), 7, &title); err != nil {
		t.Fatalf("replacing episode 7 of a full series: err=%v; want it accepted (an overwrite is not growth)", err)
	}
	if len(store.episodes) != postgres.MaxSeriesEpisodes {
		t.Fatalf("an overwrite changed the episode count to %d", len(store.episodes))
	}
}

// ── editing is the owner's alone ────────────────────────────────────────────

func TestUpdateVideoSeriesRequiresOwner(t *testing.T) {
	owner, attacker, id := uuid.New(), uuid.New(), uuid.New()
	title := "renamed"

	t.Run("stranger", func(t *testing.T) {
		store := seriesFixture(id, owner, true)
		_, err := newSeriesService(store).UpdateVideoSeries(context.Background(), attacker, id, postgres.VideoSeriesPatch{Title: &title})
		if !errors.Is(err, ErrNotVideoSeriesOwner) {
			t.Fatalf("stranger editing a series: err=%v want ErrNotVideoSeriesOwner", err)
		}
		if len(store.patches) != 0 {
			t.Fatal("a stranger's edit reached the store")
		}
	})

	t.Run("missing series", func(t *testing.T) {
		store := &fakeVideoSeriesStore{series: map[uuid.UUID]*postgres.VideoSeries{}}
		_, err := newSeriesService(store).UpdateVideoSeries(context.Background(), owner, id, postgres.VideoSeriesPatch{Title: &title})
		if !errors.Is(err, ErrVideoSeriesNotFound) {
			t.Fatalf("editing a series that is not there: err=%v want ErrVideoSeriesNotFound", err)
		}
	})

	t.Run("owner", func(t *testing.T) {
		store := seriesFixture(id, owner, true)
		got, err := newSeriesService(store).UpdateVideoSeries(context.Background(), owner, id, postgres.VideoSeriesPatch{Title: &title})
		if err != nil || got == nil || got.Title != "renamed" {
			t.Fatalf("owner's edit: got=%v err=%v; want the renamed series", got, err)
		}
	})

	t.Run("empty title", func(t *testing.T) {
		store := seriesFixture(id, owner, true)
		empty := ""
		_, err := newSeriesService(store).UpdateVideoSeries(context.Background(), owner, id, postgres.VideoSeriesPatch{Title: &empty})
		if !errors.Is(err, ErrVideoSeriesTitleRequired) {
			t.Fatalf("blanking the title: err=%v want ErrVideoSeriesTitleRequired (create requires one; a patch must not remove it)", err)
		}
	})
}
