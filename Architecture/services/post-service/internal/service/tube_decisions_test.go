package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The 31 Oct 2026 founder decisions for long video, their unit-level pins:
// unlisted videos are never listed to anyone but their author
// (unlisted_listing.go); series and playlist covers are media-claimed
// (tube_cover_claims.go); a members-only video's end screens, cards and
// product tags are the members' (tube_members_extras.go). The SQL halves are
// pinned against Postgres in tube_decisions_integration_test.go (CI only).

// ── 1. unlisted in listings ────────────────────────────────────────────────

func TestListedToKeepsUnlistedForItsAuthorAlone(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	for _, c := range []struct {
		vis    string
		viewer *uuid.UUID
		want   bool
	}{
		{"unlisted", &author, true},
		{"unlisted", &stranger, false},
		{"unlisted", nil, false},
		{" UNLISTED ", &stranger, false},
		{"public", &stranger, true},
		{"", nil, true},
		{"followers", &stranger, true}, // the audience rule is the caller's; this is only the listing rule
	} {
		if got := listedTo(c.vis, author, c.viewer); got != c.want {
			t.Errorf("listedTo(%q, viewer=%v) = %v, want %v", c.vis, c.viewer, got, c.want)
		}
	}
	// A row with no author (a unit rig's gate) is nobody's to see unlisted.
	if listedTo("unlisted", uuid.Nil, &uuid.Nil) {
		t.Fatal("an unlisted row with no author was listed")
	}
}

func TestProfileGridListsUnlistedOnlyToTheAuthor(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	pub, unl := uuid.New(), uuid.New()
	rows := []postgres.Post{
		{ID: pub, AuthorID: author, Visibility: "public"},
		{ID: unl, AuthorID: author, Visibility: "unlisted"},
	}
	never := func() bool { t.Error("asked the graph for an open post"); return false }
	ids := func(ps []postgres.Post) []uuid.UUID {
		out := make([]uuid.UUID, len(ps))
		for i, p := range ps {
			out[i] = p.ID
		}
		return out
	}
	for name, viewer := range map[string]*uuid.UUID{"stranger": &stranger, "signed out": nil} {
		if got := ids(authorGridRows(rows, false, viewer, never, never)); len(got) != 1 || got[0] != pub {
			t.Errorf("%s: grid = %v, want only the public video", name, got)
		}
	}
	if got := authorGridRows(rows, false, &author, never, never); len(got) != 2 {
		t.Fatalf("the author's own grid = %v, want both videos", ids(got))
	}
}

// pageRecorder is the playlist store, recording the page each read asked for.
type pageRecorder struct {
	fakeAuthoringStore
	pages []postgres.PlaylistItemsPage
}

func (p *pageRecorder) GetPlaylistItems(ctx context.Context, id uuid.UUID, page postgres.PlaylistItemsPage) ([]postgres.PlaylistItem, error) {
	p.pages = append(p.pages, page)
	return p.fakeAuthoringStore.GetPlaylistItems(ctx, id, page)
}

func TestPlaylistItemsHideUnlistedFromEveryoneButTheOwner(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	pl := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Visibility: "public", Kind: postgres.PlaylistKindUser}
	store := &pageRecorder{fakeAuthoringStore: fakeAuthoringStore{playlist: pl}}
	s := newAuthoringService(store)
	ctx := context.Background()
	for name, caller := range map[string]*uuid.UUID{"viewer": &viewer, "signed out": nil} {
		store.pages = nil
		if _, err := s.GetPlaylistItems(ctx, pl.ID, caller); err != nil {
			t.Fatal(err)
		}
		if len(store.pages) != 1 || !store.pages[0].HideUnlisted || store.pages[0].Viewer != caller {
			t.Fatalf("%s: page %+v; want unlisted hidden except for the viewer's own", name, store.pages)
		}
	}
	store.pages = nil
	if _, err := s.GetPlaylistItems(ctx, pl.ID, &owner); err != nil {
		t.Fatal(err)
	}
	if len(store.pages) != 1 || store.pages[0].HideUnlisted {
		t.Fatalf("the owner's own playlist hid unlisted items: %+v", store.pages)
	}
}

func TestHydratedPlaylistDropsAnUnlistedRowTheStoreLetThrough(t *testing.T) {
	owner, viewer, author := uuid.New(), uuid.New(), uuid.New()
	pl := &postgres.Playlist{ID: uuid.New(), CreatorID: owner, Visibility: "public", Kind: postgres.PlaylistKindUser}
	open, unl := uuid.New(), uuid.New()
	store := &fakeAuthoringStore{playlist: pl, items: []postgres.PlaylistItem{
		{PlaylistID: pl.ID, PostID: open, Position: 0}, {PlaylistID: pl.ID, PostID: unl, Position: 1}}}
	s := newAuthoringService(store)
	s.batchPostsSeam = func(context.Context, []uuid.UUID, *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
		// Made unlisted between the items read and the batch read.
		return map[uuid.UUID]*PostDetail{
			open: {Post: &postgres.Post{ID: open, AuthorID: author, Visibility: "public"}},
			unl:  {Post: &postgres.Post{ID: unl, AuthorID: author, Visibility: "unlisted"}},
		}, nil
	}
	ctx := context.Background()
	got, _, err := s.GetPlaylistItemsHydrated(ctx, pl.ID, &viewer, postgres.PlaylistItemsPage{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PostID != open {
		t.Fatalf("a viewer's page = %+v, want the public video only", got)
	}
	for name, caller := range map[string]*uuid.UUID{"playlist owner": &owner, "video author": &author} {
		got, _, err := s.GetPlaylistItemsHydrated(ctx, pl.ID, caller, postgres.PlaylistItemsPage{})
		if err != nil || len(got) != 2 {
			t.Fatalf("%s: %d rows (err %v), want both", name, len(got), err)
		}
	}
}

func TestSeriesEpisodesListUnlistedOnlyToTheCreator(t *testing.T) {
	owner, stranger, id := uuid.New(), uuid.New(), uuid.New()
	ep1, ep2, ep3 := uuid.New(), uuid.New(), uuid.New()
	store := seriesFixture(id, owner, true)
	store.episodes = []postgres.VideoSeriesEpisode{
		{SeriesID: id, PostID: ep1, EpisodeNum: 1}, {SeriesID: id, PostID: ep2, EpisodeNum: 2}, {SeriesID: id, PostID: ep3, EpisodeNum: 3},
	}
	store.memberships = []postgres.VideoSeriesEpisode{{SeriesID: id, PostID: ep2, EpisodeNum: 2}}
	s := newSeriesService(store)
	s.readGate = nil
	s.viewablePostsSeam = func(context.Context, []uuid.UUID, *uuid.UUID) (map[uuid.UUID]viewablePost, error) {
		return map[uuid.UUID]viewablePost{
			ep1: {AuthorID: owner, Visibility: "public"},
			ep2: {AuthorID: owner, Visibility: "unlisted"},
			ep3: {AuthorID: owner, Visibility: "public"},
		}, nil
	}
	ctx := context.Background()
	for name, viewer := range map[string]*uuid.UUID{"stranger": &stranger, "signed out": nil} {
		eps, err := s.GetVideoSeriesEpisodes(ctx, id, viewer)
		if err != nil {
			t.Fatal(err)
		}
		if len(eps) != 2 || eps[0].PostID != ep1 || eps[1].PostID != ep3 {
			t.Fatalf("%s: episodes %+v, want 1 and 3 (2 is unlisted)", name, eps)
		}
	}
	if eps, err := s.GetVideoSeriesEpisodes(ctx, id, &owner); err != nil || len(eps) != 3 {
		t.Fatalf("the creator's list: %d (err %v), want all three", len(eps), err)
	}
	// The watch page of the unlisted episode itself, opened by its link:
	// the panel shows, with that episode in it, and its neighbours.
	view, err := s.GetPostSeries(ctx, ep2, &stranger)
	if err != nil {
		t.Fatalf("the panel of an unlisted episode opened by link: %v", err)
	}
	if view.Current.EpisodeNum != 2 || view.Prev == nil || view.Prev.PostID != ep1 || view.Next == nil || view.Next.PostID != ep3 {
		t.Fatalf("panel %+v", view)
	}
	// Watching episode 1, the unlisted episode 2 is never the Next button.
	store.memberships = []postgres.VideoSeriesEpisode{{SeriesID: id, PostID: ep1, EpisodeNum: 1}}
	view, err = s.GetPostSeries(ctx, ep1, &stranger)
	if err != nil {
		t.Fatal(err)
	}
	if view.Next == nil || view.Next.PostID != ep3 || len(view.Episodes) != 2 {
		t.Fatalf("watching episode 1: next %+v, %d episodes; want next = 3 and the unlisted one left out", view.Next, len(view.Episodes))
	}
}

type flickFake struct {
	series *postgres.FlickSeries
	items  []postgres.FlickSeriesItem
}

func (f *flickFake) GetFlickSeries(context.Context, uuid.UUID) (*postgres.FlickSeries, error) {
	return f.series, nil
}
func (f *flickFake) GetSeriesEpisodes(context.Context, uuid.UUID) ([]postgres.FlickSeriesItem, error) {
	return f.items, nil
}

func TestReelsSeriesListsUnlistedOnlyToTheCreator(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	pub, unl := uuid.New(), uuid.New()
	s := &Service{}
	s.flickSeries = &flickFake{series: &postgres.FlickSeries{ID: uuid.New(), CreatorID: owner},
		items: []postgres.FlickSeriesItem{{PostID: pub}, {PostID: unl}}}
	s.viewablePostsSeam = func(context.Context, []uuid.UUID, *uuid.UUID) (map[uuid.UUID]viewablePost, error) {
		return map[uuid.UUID]viewablePost{pub: {AuthorID: owner, Visibility: "public"}, unl: {AuthorID: owner, Visibility: "unlisted"}}, nil
	}
	got, err := s.GetSeriesEpisodes(context.Background(), uuid.New(), &stranger)
	if err != nil || len(got) != 1 || got[0].PostID != pub {
		t.Fatalf("a stranger's reels series: %+v (err %v), want the public reel only", got, err)
	}
	if got, err := s.GetSeriesEpisodes(context.Background(), uuid.New(), &owner); err != nil || len(got) != 2 {
		t.Fatalf("the creator's own: %d (err %v)", len(got), err)
	}
}

func TestRepostsTabListsAnUnlistedOriginalOnlyToItsAuthorAndTheReposter(t *testing.T) {
	r := newRepostsRig()
	s := r.svc()
	s.batchPostsSeam = func(_ context.Context, ids []uuid.UUID, _ *uuid.UUID) (map[uuid.UUID]*PostDetail, error) {
		out := map[uuid.UUID]*PostDetail{}
		for _, id := range ids {
			if id == r.openPost {
				out[id] = &PostDetail{Post: &postgres.Post{ID: id, AuthorID: r.author, Visibility: "unlisted"}}
			}
		}
		return out, nil
	}
	r.edges = followEdges{follows: true}
	got, err := s.ListUserReposts(context.Background(), r.reposter, &r.viewer, 20, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("a viewer saw %d reposts of an unlisted video", len(got.Items))
	}
	for name, viewer := range map[string]uuid.UUID{"reposter": r.reposter, "original author": r.author} {
		got, err := s.ListUserReposts(context.Background(), r.reposter, &viewer, 20, "")
		if err != nil || len(got.Items) == 0 {
			t.Fatalf("%s: %d items (err %v), want the unlisted original listed", name, len(got.Items), err)
		}
	}
}

// ── 2. series and playlist covers are media-claimed ────────────────────────

// fakeTubeCovers is the series create and the playlist rows, mirroring each
// write into the holder fake as MediaHeldBy would read it.
type fakeTubeCovers struct {
	holders   *fakeHolders
	claims    *fakeClaims
	playlists map[uuid.UUID]*postgres.Playlist
	series    []uuid.UUID
	// claimedAtWrite is what media-service held for the row when its write
	// ran: a cover must be claimed BEFORE the row names it.
	claimedAtWrite []uuid.UUID
	writeErr       error
	writes         int
}

func newFakeTubeCovers(h *fakeHolders, c *fakeClaims) *fakeTubeCovers {
	return &fakeTubeCovers{holders: h, claims: c, playlists: map[uuid.UUID]*postgres.Playlist{}}
}

func (f *fakeTubeCovers) CreateVideoSeries(_ context.Context, vs *postgres.VideoSeries) error {
	f.writes++
	f.claimedAtWrite = f.claims.held(postgres.HolderVideoSeriesCover, vs.ID)
	if f.writeErr != nil {
		return f.writeErr
	}
	if vs.ID == uuid.Nil {
		vs.ID = uuid.New()
	}
	f.series = append(f.series, vs.ID)
	f.holders.set(postgres.HolderVideoSeriesCover, vs.ID, true, optionalID(vs.CoverMediaID)...)
	return nil
}

func (f *fakeTubeCovers) CreatePlaylist(_ context.Context, p *postgres.Playlist) error {
	f.writes++
	f.claimedAtWrite = f.claims.held(postgres.HolderPlaylistCover, p.ID)
	if f.writeErr != nil {
		return f.writeErr
	}
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	cp := *p
	f.playlists[p.ID] = &cp
	f.holders.set(postgres.HolderPlaylistCover, p.ID, true, optionalID(postgres.ParsePlaylistCoverID(p.CoverURL))...)
	return nil
}

func (f *fakeTubeCovers) UpdatePlaylist(_ context.Context, id uuid.UUID, patch postgres.PlaylistPatch) (*postgres.Playlist, error) {
	f.writes++
	f.claimedAtWrite = f.claims.held(postgres.HolderPlaylistCover, id)
	if f.writeErr != nil {
		return nil, f.writeErr
	}
	p := f.playlists[id]
	if p == nil {
		return nil, nil
	}
	if patch.CoverURL != nil {
		v := *patch.CoverURL
		p.CoverURL = &v
	}
	if patch.Title != nil {
		p.Title = *patch.Title
	}
	f.holders.set(postgres.HolderPlaylistCover, id, true, optionalID(postgres.ParsePlaylistCoverID(p.CoverURL))...)
	cp := *p
	return &cp, nil
}

func (f *fakeTubeCovers) DeletePlaylist(_ context.Context, id uuid.UUID) error {
	delete(f.playlists, id)
	delete(f.holders.rows, claimKey{postgres.HolderPlaylistCover, id})
	return nil
}

// seriesCoverStore is the series store with its cover mirrored into the
// holder fake.
type seriesCoverStore struct {
	*fakeVideoSeriesStore
	holders   *fakeHolders
	claims    *fakeClaims
	atWrite   []uuid.UUID
	updateErr error
}

func (f *seriesCoverStore) UpdateVideoSeries(ctx context.Context, id uuid.UUID, patch postgres.VideoSeriesPatch) (*postgres.VideoSeries, error) {
	f.atWrite = f.claims.held(postgres.HolderVideoSeriesCover, id)
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	vs, err := f.fakeVideoSeriesStore.UpdateVideoSeries(ctx, id, patch)
	if vs != nil && patch.CoverMediaID != nil {
		vs.CoverMediaID = patch.CoverMediaID
		f.holders.set(postgres.HolderVideoSeriesCover, id, true, *patch.CoverMediaID)
	}
	return vs, err
}

func (f *seriesCoverStore) DeleteVideoSeries(ctx context.Context, id uuid.UUID) error {
	delete(f.holders.rows, claimKey{postgres.HolderVideoSeriesCover, id})
	return f.fakeVideoSeriesStore.DeleteVideoSeries(ctx, id)
}

type coverRig struct {
	s       *Service
	claims  *fakeClaims
	holders *fakeHolders
	rows    *fakeTubeCovers
	series  *seriesCoverStore
	own     fakeOwnership
	owner   uuid.UUID
	id      uuid.UUID
}

func newCoverRig() *coverRig {
	s, claims, holders := claimRig()
	r := &coverRig{s: s, claims: claims, holders: holders, own: fakeOwnership{}, owner: uuid.New(), id: uuid.New()}
	r.rows = newFakeTubeCovers(holders, claims)
	r.series = &seriesCoverStore{fakeVideoSeriesStore: seriesFixture(r.id, r.owner, true), holders: holders, claims: claims}
	s.ownMedia = r.own
	s.videoSeries = r.series
	s.tubeCoverRowsSeam = r.rows
	return r
}

func TestHolderTubeCoverKindsMatchTheClaimKinds(t *testing.T) {
	if postgres.HolderVideoSeriesCover != "video_series_cover" || postgres.HolderPlaylistCover != "playlist_cover" {
		t.Fatalf("kinds = %q, %q; media-service registers video_series_cover and playlist_cover",
			postgres.HolderVideoSeriesCover, postgres.HolderPlaylistCover)
	}
}

func TestCreateVideoSeriesClaimsTheCoverBeforeTheRowNamesIt(t *testing.T) {
	r := newCoverRig()
	cover := ownImage(r.own, r.owner)
	vs := &postgres.VideoSeries{CreatorID: r.owner, Title: "S", CoverMediaID: &cover}
	if err := r.s.CreateVideoSeries(context.Background(), vs); err != nil {
		t.Fatal(err)
	}
	if vs.ID == uuid.Nil {
		t.Fatal("the series id was not minted before the claim")
	}
	sameIDs(t, "claimed when the row was written", r.rows.claimedAtWrite, cover)
	sameIDs(t, "series cover claims", r.claims.held(postgres.HolderVideoSeriesCover, vs.ID), cover)
	// No cover: nothing claimed, no lock taken.
	r2 := newCoverRig()
	if err := r2.s.CreateVideoSeries(context.Background(), &postgres.VideoSeries{CreatorID: r2.owner, Title: "S"}); err != nil {
		t.Fatal(err)
	}
	if len(*r2.claims.log) != 0 {
		t.Fatalf("a series without a cover touched claims: %v", *r2.claims.log)
	}
}

func TestCreateVideoSeriesFailsClosedWithoutClaims(t *testing.T) {
	r := newCoverRig()
	r.s.claims = nil
	cover := ownImage(r.own, r.owner)
	err := r.s.CreateVideoSeries(context.Background(), &postgres.VideoSeries{CreatorID: r.owner, Title: "S", CoverMediaID: &cover})
	if !errors.Is(err, ErrMediaClaimsUnavailable) || r.rows.writes != 0 {
		t.Fatalf("err=%v writes=%d; want 503 MEDIA_CLAIMS_UNAVAILABLE and no row", err, r.rows.writes)
	}
	// A cover deleted between the check and the claim is a refusal of the
	// cover, and nothing is written.
	r = newCoverRig()
	cover = ownImage(r.own, r.owner)
	r.claims.missing[cover] = true
	err = r.s.CreateVideoSeries(context.Background(), &postgres.VideoSeries{CreatorID: r.owner, Title: "S", CoverMediaID: &cover})
	var ce *CoverMediaError
	if !errors.As(err, &ce) || !errors.Is(err, ErrMediaNotFound) || r.rows.writes != 0 {
		t.Fatalf("err=%v writes=%d; want a CoverMediaError(MEDIA_NOT_FOUND) and no row", err, r.rows.writes)
	}
	// A failed insert releases what it claimed.
	r = newCoverRig()
	cover = ownImage(r.own, r.owner)
	r.rows.writeErr = errors.New("insert failed")
	vs := &postgres.VideoSeries{CreatorID: r.owner, Title: "S", CoverMediaID: &cover}
	if err := r.s.CreateVideoSeries(context.Background(), vs); err == nil {
		t.Fatal("a failed insert reported success")
	}
	if got := r.claims.held(postgres.HolderVideoSeriesCover, vs.ID); len(got) != 0 {
		t.Fatalf("a failed insert kept its claim: %v", got)
	}
}

func TestUpdateVideoSeriesCoverClaimsTheNewAndReleasesTheOld(t *testing.T) {
	r := newCoverRig()
	ctx := context.Background()
	old, next := ownImage(r.own, r.owner), ownImage(r.own, r.owner)
	r.holders.set(postgres.HolderVideoSeriesCover, r.id, true, old)
	_ = r.claims.Claim(ctx, postgres.HolderVideoSeriesCover, r.id, []uuid.UUID{old})
	if _, err := r.s.UpdateVideoSeries(ctx, r.owner, r.id, postgres.VideoSeriesPatch{CoverMediaID: &next}); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "claimed when the row was written", r.series.atWrite, next, old)
	sameIDs(t, "series cover claims after", r.claims.held(postgres.HolderVideoSeriesCover, r.id), next)

	// A patch that does not touch the cover changes no claim.
	*r.claims.log = nil
	title := "renamed"
	if _, err := r.s.UpdateVideoSeries(ctx, r.owner, r.id, postgres.VideoSeriesPatch{Title: &title}); err != nil {
		t.Fatal(err)
	}
	if len(*r.claims.log) != 0 {
		t.Fatalf("a title edit touched claims: %v", *r.claims.log)
	}
	sameIDs(t, "series cover claims after a title edit", r.claims.held(postgres.HolderVideoSeriesCover, r.id), next)

	// A failed update keeps the old claim and drops the new one.
	third := ownImage(r.own, r.owner)
	r.series.updateErr = errors.New("update failed")
	if _, err := r.s.UpdateVideoSeries(ctx, r.owner, r.id, postgres.VideoSeriesPatch{CoverMediaID: &third}); err == nil {
		t.Fatal("a failed update reported success")
	}
	sameIDs(t, "series cover claims after a failed update", r.claims.held(postgres.HolderVideoSeriesCover, r.id), next)
}

func TestDeleteVideoSeriesReleasesItsCover(t *testing.T) {
	r := newCoverRig()
	ctx := context.Background()
	cover := ownImage(r.own, r.owner)
	r.holders.set(postgres.HolderVideoSeriesCover, r.id, true, cover)
	_ = r.claims.Claim(ctx, postgres.HolderVideoSeriesCover, r.id, []uuid.UUID{cover})
	if err := r.s.DeleteVideoSeries(ctx, r.owner, r.id); err != nil {
		t.Fatal(err)
	}
	if got := r.claims.held(postgres.HolderVideoSeriesCover, r.id); len(got) != 0 {
		t.Fatalf("a deleted series kept its cover claim: %v", got)
	}
	// Someone else's delete is refused before anything is released.
	r = newCoverRig()
	r.holders.set(postgres.HolderVideoSeriesCover, r.id, true, cover)
	_ = r.claims.Claim(ctx, postgres.HolderVideoSeriesCover, r.id, []uuid.UUID{cover})
	if err := r.s.DeleteVideoSeries(ctx, uuid.New(), r.id); !errors.Is(err, ErrNotVideoSeriesOwner) {
		t.Fatalf("a stranger's delete: %v", err)
	}
	sameIDs(t, "claims after a refused delete", r.claims.held(postgres.HolderVideoSeriesCover, r.id), cover)
}

func servePath(id uuid.UUID) *string {
	v := "/v1/media/" + id.String() + "/serve"
	return &v
}

func TestPlaylistCoverClaimLifecycle(t *testing.T) {
	r := newCoverRig()
	ctx := context.Background()
	cover := ownImage(r.own, r.owner)
	p := &postgres.Playlist{CreatorID: r.owner, Title: "P", CoverURL: strptr(cover.String())}
	if err := r.s.CreatePlaylist(ctx, p); err != nil {
		t.Fatal(err)
	}
	if p.ID == uuid.Nil || *p.CoverURL != *servePath(cover) {
		t.Fatalf("created %+v", p)
	}
	sameIDs(t, "claimed when the row was written", r.rows.claimedAtWrite, cover)
	sameIDs(t, "playlist cover claims", r.claims.held(postgres.HolderPlaylistCover, p.ID), cover)

	// The owner's edits go through the ownership read.
	auth := &fakeAuthoringStore{playlist: &postgres.Playlist{ID: p.ID, CreatorID: r.owner, Kind: postgres.PlaylistKindUser}}
	r.s.authoringOwners = auth

	next := ownImage(r.own, r.owner)
	if _, err := r.s.UpdatePlaylist(ctx, r.owner, p.ID, postgres.PlaylistPatch{CoverURL: strptr(next.String())}); err != nil {
		t.Fatal(err)
	}
	sameIDs(t, "claimed when the update was written", r.rows.claimedAtWrite, cover, next)
	sameIDs(t, "playlist cover claims after the change", r.claims.held(postgres.HolderPlaylistCover, p.ID), next)

	// "" clears the cover: the old one is released, nothing is claimed.
	if _, err := r.s.UpdatePlaylist(ctx, r.owner, p.ID, postgres.PlaylistPatch{CoverURL: strptr("")}); err != nil {
		t.Fatal(err)
	}
	if got := r.claims.held(postgres.HolderPlaylistCover, p.ID); len(got) != 0 {
		t.Fatalf("a cleared cover kept its claim: %v", got)
	}

	// Set again, then delete: released.
	if _, err := r.s.UpdatePlaylist(ctx, r.owner, p.ID, postgres.PlaylistPatch{CoverURL: strptr(next.String())}); err != nil {
		t.Fatal(err)
	}
	if err := r.s.DeletePlaylist(ctx, r.owner, p.ID); err != nil {
		t.Fatal(err)
	}
	if got := r.claims.held(postgres.HolderPlaylistCover, p.ID); len(got) != 0 {
		t.Fatalf("a deleted playlist kept its cover claim: %v", got)
	}

	// No claim client: an edit that adds a cover is refused, unwritten.
	r.s.claims = nil
	r.rows.playlists[p.ID] = &postgres.Playlist{ID: p.ID, CreatorID: r.owner}
	writes := r.rows.writes
	if _, err := r.s.UpdatePlaylist(ctx, r.owner, p.ID, postgres.PlaylistPatch{CoverURL: strptr(next.String())}); !errors.Is(err, ErrMediaClaimsUnavailable) || r.rows.writes != writes {
		t.Fatalf("err=%v writes %d→%d; want 503 and no write", err, writes, r.rows.writes)
	}
}

func TestCreatePlaylistWithoutACoverClaimsNothing(t *testing.T) {
	r := newCoverRig()
	for _, cover := range []*string{nil, strptr("")} {
		if err := r.s.CreatePlaylist(context.Background(), &postgres.Playlist{CreatorID: r.owner, Title: "P", CoverURL: cover}); err != nil {
			t.Fatal(err)
		}
	}
	if len(*r.claims.log) != 0 {
		t.Fatalf("a playlist without a cover touched claims: %v", *r.claims.log)
	}
}

// ── 3. members-only extras ─────────────────────────────────────────────────

func TestProductTagsOfAMembersOnlyVideoAreTheMembers(t *testing.T) {
	author, member, stranger, tier := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f := &membersFake{member: map[uuid.UUID]map[uuid.UUID]bool{member: {tier: true}}}
	s := f.svc()
	gate := &tier
	s.postGateSeam = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) { return author, gate, nil }
	tags := []*postgres.PostProductTag{{ID: uuid.New()}}
	reads := 0
	s.productTagListSeam = func(context.Context, uuid.UUID) ([]*postgres.PostProductTag, error) {
		reads++
		return tags, nil
	}
	ctx := context.Background()
	for name, viewer := range map[string]*uuid.UUID{"stranger": &stranger, "signed out": nil} {
		got, err := s.ListProductTagsForViewer(ctx, uuid.New(), viewer)
		if err != nil || got == nil || len(got) != 0 {
			t.Fatalf("%s: %v (err %v), want an empty list", name, got, err)
		}
	}
	if reads != 0 {
		t.Fatalf("a non-member's read reached the tags (%d reads)", reads)
	}
	for name, viewer := range map[string]*uuid.UUID{"member": &member, "author": &author} {
		if got, err := s.ListProductTagsForViewer(ctx, uuid.New(), viewer); err != nil || len(got) != 1 {
			t.Fatalf("%s: %d tags (err %v), want the video's", name, len(got), err)
		}
	}
	gate = nil // an open video's tags are everyone's
	if got, err := s.ListProductTagsForViewer(ctx, uuid.New(), nil); err != nil || len(got) != 1 {
		t.Fatalf("an open video: %d tags (err %v)", len(got), err)
	}
	s.postGateSeam = func(context.Context, uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
		return uuid.Nil, nil, errors.New("db down")
	}
	if got, err := s.ListProductTagsForViewer(ctx, uuid.New(), &member); err == nil || got != nil {
		t.Fatalf("a failed gate lookup served %d tags", len(got))
	}
}
