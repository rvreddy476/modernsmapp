package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The stranger tests the long-video gate (line 3) asked for: on every
// owner-only creator tool a non-owner is refused and nothing changes; the
// owner succeeds. Plus lv-28 (cover-frame thumbnail) and lv-74 (reels series).

type fakeVideoTools struct {
	authors  map[uuid.UUID]uuid.UUID
	vm       map[uuid.UUID]*postgres.VideoMetadata
	writes   []string
	coverSet []*uuid.UUID
}

func newFakeVideoTools(post, author uuid.UUID) *fakeVideoTools {
	return &fakeVideoTools{
		authors: map[uuid.UUID]uuid.UUID{post: author},
		vm: map[uuid.UUID]*postgres.VideoMetadata{post: {PostID: post, DurationSeconds: 120, UploadStatus: "ready",
			ComputedCategory: "long_video", FinalCategory: "long_video", Orientation: "landscape"}},
	}
}

func (f *fakeVideoTools) GetPostAuthorID(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	a, ok := f.authors[id]
	if !ok {
		return uuid.Nil, errors.New("no rows")
	}
	return a, nil
}
func (f *fakeVideoTools) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	a, ok := f.authors[id]
	if !ok {
		return nil, nil
	}
	return &postgres.Post{ID: id, AuthorID: a, ContentType: "long_video", Visibility: "public"}, nil
}
func (f *fakeVideoTools) GetVideoMetadata(_ context.Context, id uuid.UUID) (*postgres.VideoMetadata, error) {
	vm, ok := f.vm[id]
	if !ok {
		return nil, errors.New("no rows")
	}
	cp := *vm
	return &cp, nil
}
func (f *fakeVideoTools) UpdateTrim(context.Context, uuid.UUID, int, *int) error {
	f.writes = append(f.writes, "trim")
	return nil
}
func (f *fakeVideoTools) UpdateFinalCategory(context.Context, uuid.UUID, string) error {
	f.writes = append(f.writes, "category")
	return nil
}
func (f *fakeVideoTools) UpdateVideoMetadata(_ context.Context, vm *postgres.VideoMetadata) error {
	f.writes = append(f.writes, "metadata")
	f.vm[vm.PostID] = vm
	return nil
}
func (f *fakeVideoTools) UpdatePostCoverMedia(_ context.Context, _ uuid.UUID, id *uuid.UUID) error {
	f.writes = append(f.writes, "cover")
	f.coverSet = append(f.coverSet, id)
	return nil
}
func (f *fakeVideoTools) UpdateDistribution(_ context.Context, _, authorID uuid.UUID, _ json.RawMessage,
	build func(rev int64) (string, interface{})) (int64, error) {
	f.writes = append(f.writes, "distribution")
	build(1)
	return 1, nil
}

type videoToolRig struct {
	svc      *Service
	store    *fakeVideoTools
	post     uuid.UUID
	owner    uuid.UUID
	stranger uuid.UUID
	publish  int
}

func newVideoToolRig() *videoToolRig {
	r := &videoToolRig{post: uuid.New(), owner: uuid.New(), stranger: uuid.New()}
	r.store = newFakeVideoTools(r.post, r.owner)
	r.svc = &Service{}
	r.svc.videoToolsSeam = r.store
	r.svc.publishScheduledSeam = func(_ context.Context, _ uuid.UUID, _ *uuid.UUID, _ bool) (bool, error) {
		r.publish++
		return true, nil
	}
	return r
}

func TestOwnerOnlyVideoToolsRefuseAStrangerAndLetTheOwner(t *testing.T) {
	ctx := context.Background()
	end := 60000
	validPolicy := json.RawMessage(`{"version":1}`)
	tools := []struct {
		name string
		call func(r *videoToolRig, caller uuid.UUID) error
		did  func(r *videoToolRig) bool
	}{
		{"trim", func(r *videoToolRig, c uuid.UUID) error { return r.svc.UpdateVideoTrim(ctx, r.post, c, 1000, &end) },
			func(r *videoToolRig) bool { return len(r.store.writes) > 0 }},
		{"category override", func(r *videoToolRig, c uuid.UUID) error {
			return r.svc.OverrideCategory(ctx, r.post, c, "long_video")
		}, func(r *videoToolRig) bool { return len(r.store.writes) > 0 }},
		{"publish now", func(r *videoToolRig, c uuid.UUID) error { return r.svc.PublishVideo(ctx, r.post, c) },
			func(r *videoToolRig) bool { return r.publish > 0 }},
		{"distribution", func(r *videoToolRig, c uuid.UUID) error {
			_, err := r.svc.UpdateDistribution(ctx, r.post, c, validPolicy)
			return err
		}, func(r *videoToolRig) bool { return len(r.store.writes) > 0 }},
	}
	for _, tc := range tools {
		t.Run(tc.name, func(t *testing.T) {
			r := newVideoToolRig()
			err := tc.call(r, r.stranger)
			if err == nil || !(strings.Contains(err.Error(), "unauthorized") || errors.Is(err, ErrNotPostAuthor)) {
				t.Fatalf("stranger: err = %v; want the owner refusal", err)
			}
			if tc.did(r) {
				t.Fatalf("stranger: something changed (writes %v, publishes %d)", r.store.writes, r.publish)
			}
			if err := tc.call(r, r.owner); err != nil {
				t.Fatalf("owner: %v", err)
			}
			if !tc.did(r) {
				t.Fatal("owner: nothing was written")
			}
		})
	}
}

// lv-28: cover-frame's thumbnail_url is the owner's own image by media id,
// stored as its serve path; an off-platform URL or a stranger's image is
// refused before ANY write (the cover included).
func TestCoverFrameThumbnailIsTheOwnersOwnPlatformImage(t *testing.T) {
	ctx := context.Background()
	r := newVideoToolRig()
	mine, theirs := uuid.New(), uuid.New()
	r.svc.ownMedia = fakeOwnMedia{mine: tubeOwnImage(r.owner), theirs: tubeOwnImage(r.stranger)}

	for _, bad := range []string{"https://attacker.example/p.gif", "https://cdn.example/v1/media/" + mine.String() + "/serve"} {
		if err := r.svc.SetCoverFrame(ctx, r.post, r.owner, nil, &bad); !errors.Is(err, ErrInvalidCoverThumbnail) {
			t.Fatalf("%q: err = %v; want ErrInvalidCoverThumbnail", bad, err)
		}
	}
	ref := theirs.String()
	if err := r.svc.SetCoverFrame(ctx, r.post, r.owner, nil, &ref); !errors.Is(err, ErrMediaNotOwned) {
		t.Fatalf("a stranger's image: err = %v; want ErrMediaNotOwned", err)
	}
	if len(r.store.writes) != 0 {
		t.Fatalf("a refused thumbnail wrote %v", r.store.writes)
	}
	// A stranger cannot set even their own image on someone else's video.
	own := theirs.String()
	if err := r.svc.SetCoverFrame(ctx, r.post, r.stranger, nil, &own); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("stranger cover-frame: %v", err)
	}
	ref = mine.String()
	if err := r.svc.SetCoverFrame(ctx, r.post, r.owner, nil, &ref); err != nil {
		t.Fatalf("own image: %v", err)
	}
	got := r.store.vm[r.post].ThumbnailURL
	if got == nil || *got != "/v1/media/"+mine.String()+"/serve" {
		t.Fatalf("stored thumbnail = %v; want the serve path", got)
	}
}

// ── playlist delete ───────────────────────────────────────────────────────

func TestDeletePlaylistRefusesAStranger(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	pl := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Visibility: "public", Kind: postgres.PlaylistKindUser}
	s := newAuthoringService(&fakeAuthoringStore{playlist: pl})
	if err := s.DeletePlaylist(context.Background(), stranger, pl.ID); !errors.Is(err, ErrNotPlaylistOwner) {
		t.Fatalf("stranger delete: err = %v; want ErrNotPlaylistOwner", err)
	}
}

// ── video-series episodes: a non-owner caller on someone's series ───────

func TestVideoSeriesEpisodeAddAndDeleteRefuseANonOwnerCaller(t *testing.T) {
	ctx := context.Background()
	owner, stranger, id := uuid.New(), uuid.New(), uuid.New()
	strangersPost, ownersPost := uuid.New(), uuid.New()
	store := seriesFixture(id, owner, true)
	store.postAuthors = map[uuid.UUID]uuid.UUID{strangersPost: stranger, ownersPost: owner}
	store.episodes = []postgres.VideoSeriesEpisode{{SeriesID: id, PostID: ownersPost, EpisodeNum: 1}}
	s := newSeriesService(store)

	if _, err := s.AddEpisodeToVideoSeries(ctx, stranger, id, strangersPost, 2, nil); !errors.Is(err, ErrNotVideoSeriesOwner) {
		t.Fatalf("stranger add: err = %v; want ErrNotVideoSeriesOwner", err)
	}
	if store.addCalls != 0 {
		t.Fatal("a stranger's add reached the store")
	}
	if err := s.DeleteVideoSeriesEpisodeByPost(ctx, stranger, id, ownersPost); !errors.Is(err, ErrNotVideoSeriesOwner) {
		t.Fatalf("stranger delete by post: err = %v; want ErrNotVideoSeriesOwner", err)
	}
	if len(store.episodes) != 1 {
		t.Fatal("a stranger removed an episode")
	}
	if err := s.DeleteVideoSeriesEpisodeByPost(ctx, owner, id, ownersPost); err != nil || len(store.episodes) != 0 {
		t.Fatalf("owner delete by post: %v (left %d)", err, len(store.episodes))
	}
}

// ── lv-74: reels (flick) series ───────────────────────────────────────────

type fakeFlickSeries struct {
	series  map[uuid.UUID]*postgres.FlickSeries
	authors map[uuid.UUID]uuid.UUID
	items   map[int]uuid.UUID
	adds    int
}

func (f *fakeFlickSeries) GetFlickSeries(_ context.Context, id uuid.UUID) (*postgres.FlickSeries, error) {
	return f.series[id], nil
}
func (f *fakeFlickSeries) GetSeriesEpisodes(context.Context, uuid.UUID) ([]postgres.FlickSeriesItem, error) {
	return nil, nil
}
func (f *fakeFlickSeries) GetPostAuthorID(_ context.Context, id uuid.UUID) (uuid.UUID, error) {
	a, ok := f.authors[id]
	if !ok {
		return uuid.Nil, errors.New("no rows")
	}
	return a, nil
}

// AddEpisodeToSeries mirrors the store: an occupied number held by another
// post is refused.
func (f *fakeFlickSeries) AddEpisodeToSeries(_ context.Context, seriesID, postID uuid.UUID, n int) (*postgres.FlickSeriesItem, error) {
	f.adds++
	if held, ok := f.items[n]; ok && held != postID {
		return nil, postgres.ErrFlickEpisodeNumberTaken
	}
	f.items[n] = postID
	return &postgres.FlickSeriesItem{SeriesID: seriesID, PostID: postID, EpisodeNum: n}, nil
}

func TestFlickSeriesEpisodeAddIsTheOwnersOwnPostAtAFreeNumber(t *testing.T) {
	ctx := context.Background()
	owner, stranger, id := uuid.New(), uuid.New(), uuid.New()
	ownReel, otherReel, strangersReel := uuid.New(), uuid.New(), uuid.New()
	f := &fakeFlickSeries{
		series:  map[uuid.UUID]*postgres.FlickSeries{id: {ID: id, CreatorID: owner}},
		authors: map[uuid.UUID]uuid.UUID{ownReel: owner, otherReel: owner, strangersReel: stranger},
		items:   map[int]uuid.UUID{},
	}
	s := &Service{flickSeries: f}
	s.flickSeriesWrites = f

	if _, err := s.AddEpisodeToSeries(ctx, stranger, id, strangersReel, 1); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("a non-owner caller: %v", err)
	}
	if _, err := s.AddEpisodeToSeries(ctx, owner, id, strangersReel, 1); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("the owner adding a stranger's reel: %v", err)
	}
	if f.adds != 0 {
		t.Fatal("a refused add reached the store")
	}
	if _, err := s.AddEpisodeToSeries(ctx, owner, id, ownReel, 1); err != nil {
		t.Fatalf("own reel: %v", err)
	}
	if _, err := s.AddEpisodeToSeries(ctx, owner, id, otherReel, 1); !errors.Is(err, ErrFlickEpisodeNumberTaken) {
		t.Fatalf("an occupied number: %v; want ErrFlickEpisodeNumberTaken", err)
	}
	if f.items[1] != ownReel {
		t.Fatal("episode 1 was overwritten")
	}
}
