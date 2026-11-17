package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// lv-7 (long-video readiness audit): a series or playlist may name only its
// creator's own channel. A stranger's channel used to be stored, and the
// reference then blocked that stranger's account erasure for ever.

// channelRefRows records the series / playlist creates that reached the
// store, so a refusal can be seen to write nothing.
type channelRefRows struct {
	series    []postgres.VideoSeries
	playlists []postgres.Playlist
}

func (r *channelRefRows) CreateVideoSeries(_ context.Context, vs *postgres.VideoSeries) error {
	r.series = append(r.series, *vs)
	return nil
}

func (r *channelRefRows) CreatePlaylist(_ context.Context, p *postgres.Playlist) error {
	r.playlists = append(r.playlists, *p)
	return nil
}

func (r *channelRefRows) UpdatePlaylist(context.Context, uuid.UUID, postgres.PlaylistPatch) (*postgres.Playlist, error) {
	return nil, errors.New("not used")
}

func (r *channelRefRows) DeletePlaylist(context.Context, uuid.UUID) error {
	return errors.New("not used")
}

type channelRefRig struct {
	s         *Service
	rows      *channelRefRows
	series    *fakeVideoSeriesStore
	owner     uuid.UUID
	seriesID  uuid.UUID
	ownChan   uuid.UUID
	otherChan uuid.UUID
}

func newChannelRefRig() *channelRefRig {
	r := &channelRefRig{owner: uuid.New(), seriesID: uuid.New(), ownChan: uuid.New(), otherChan: uuid.New()}
	stranger := uuid.New()
	chans := newFakeChannelStore()
	chans.byUser[r.owner] = &postgres.Channel{ID: r.ownChan, UserID: r.owner, Name: "Mine", Handle: "mine"}
	chans.byUser[stranger] = &postgres.Channel{ID: r.otherChan, UserID: stranger, Name: "Theirs", Handle: "theirs"}
	r.series = seriesFixture(r.seriesID, r.owner, true)
	r.rows = &channelRefRows{}
	r.s = newSeriesService(r.series)
	r.s.channels = chans
	r.s.tubeCoverRowsSeam = r.rows
	return r
}

func TestCreateVideoSeriesRefusesAStrangersChannel(t *testing.T) {
	r := newChannelRefRig()
	err := r.s.CreateVideoSeries(context.Background(), &postgres.VideoSeries{CreatorID: r.owner, Title: "x", ChannelID: &r.otherChan})
	if !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("a stranger's channel: err=%v want ErrNotChannelOwner", err)
	}
	if len(r.rows.series) != 0 {
		t.Fatal("a series naming a stranger's channel reached the store")
	}
	// An unknown channel id is refused the same way.
	unknown := uuid.New()
	if err := r.s.CreateVideoSeries(context.Background(), &postgres.VideoSeries{CreatorID: r.owner, Title: "x", ChannelID: &unknown}); !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("an unknown channel: err=%v want ErrNotChannelOwner", err)
	}
	// The creator's own channel, and no channel at all, are stored.
	if err := r.s.CreateVideoSeries(context.Background(), &postgres.VideoSeries{CreatorID: r.owner, Title: "x", ChannelID: &r.ownChan}); err != nil {
		t.Fatalf("own channel: %v", err)
	}
	if err := r.s.CreateVideoSeries(context.Background(), &postgres.VideoSeries{CreatorID: r.owner, Title: "y"}); err != nil {
		t.Fatalf("no channel: %v", err)
	}
	if len(r.rows.series) != 2 || r.rows.series[0].ChannelID == nil || *r.rows.series[0].ChannelID != r.ownChan {
		t.Fatalf("stored series = %+v; want the own-channel one and the channel-less one", r.rows.series)
	}
}

func TestUpdateVideoSeriesRefusesAStrangersChannel(t *testing.T) {
	r := newChannelRefRig()
	if _, err := r.s.UpdateVideoSeries(context.Background(), r.owner, r.seriesID, postgres.VideoSeriesPatch{ChannelID: &r.otherChan}); !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("a stranger's channel on PATCH: err=%v want ErrNotChannelOwner", err)
	}
	if len(r.series.patches) != 0 {
		t.Fatal("a patch naming a stranger's channel reached the store")
	}
	if _, err := r.s.UpdateVideoSeries(context.Background(), r.owner, r.seriesID, postgres.VideoSeriesPatch{ChannelID: &r.ownChan}); err != nil {
		t.Fatalf("own channel on PATCH: %v", err)
	}
	if len(r.series.patches) != 1 || *r.series.patches[0].ChannelID != r.ownChan {
		t.Fatalf("patches = %+v; want the own-channel patch", r.series.patches)
	}
}

func TestCreatePlaylistRefusesAStrangersChannel(t *testing.T) {
	r := newChannelRefRig()
	err := r.s.CreatePlaylist(context.Background(), &postgres.Playlist{CreatorID: r.owner, Title: "x", ChannelID: &r.otherChan})
	if !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("a stranger's channel: err=%v want ErrNotChannelOwner", err)
	}
	if len(r.rows.playlists) != 0 {
		t.Fatal("a playlist naming a stranger's channel reached the store")
	}
	if err := r.s.CreatePlaylist(context.Background(), &postgres.Playlist{CreatorID: r.owner, Title: "x", ChannelID: &r.ownChan}); err != nil {
		t.Fatalf("own channel: %v", err)
	}
	if len(r.rows.playlists) != 1 || *r.rows.playlists[0].ChannelID != r.ownChan {
		t.Fatalf("stored playlists = %+v; want the own-channel one", r.rows.playlists)
	}
}

// Fail closed: a caller with no channel, a nil caller and an unwired
// channel store are all refused; a nil channel id is always allowed.
func TestRequireOwnChannelRefFailsClosed(t *testing.T) {
	r := newChannelRefRig()
	ctx := context.Background()
	if err := r.s.requireOwnChannelRef(ctx, uuid.New(), &r.ownChan); !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("caller without a channel: err=%v", err)
	}
	if err := r.s.requireOwnChannelRef(ctx, uuid.Nil, &r.ownChan); !errors.Is(err, ErrNotChannelOwner) {
		t.Fatalf("nil caller: err=%v", err)
	}
	if err := r.s.requireOwnChannelRef(ctx, r.owner, nil); err != nil {
		t.Fatalf("no channel named: %v", err)
	}
	r.s.channels = nil
	if err := r.s.requireOwnChannelRef(ctx, r.owner, &r.ownChan); err == nil {
		t.Fatal("an unwired channel store allowed the reference")
	}
}

// The trailer, like an episode, is the creator's own video: a stranger's
// post is refused on create and on PATCH and nothing is written; the
// creator's own is stored. (A missing post is requireOwnEpisodePost's 404,
// pinned with the episode rule.)
func TestSeriesTrailerMustBeTheCreatorsOwnVideo(t *testing.T) {
	r := newChannelRefRig()
	mine, theirs := uuid.New(), uuid.New()
	r.series.postAuthors = map[uuid.UUID]uuid.UUID{mine: r.owner, theirs: uuid.New()}
	ctx := context.Background()

	if err := r.s.CreateVideoSeries(ctx, &postgres.VideoSeries{CreatorID: r.owner, Title: "x", TrailerPostID: &theirs}); !errors.Is(err, ErrTrailerNotCreatorsVideo) {
		t.Fatalf("create with a stranger's trailer: err=%v", err)
	}
	if len(r.rows.series) != 0 {
		t.Fatal("a refused trailer reached the store")
	}
	if _, err := r.s.UpdateVideoSeries(ctx, r.owner, r.seriesID, postgres.VideoSeriesPatch{TrailerPostID: &theirs}); !errors.Is(err, ErrTrailerNotCreatorsVideo) {
		t.Fatalf("PATCH with a stranger's trailer: err=%v", err)
	}
	if len(r.series.patches) != 0 {
		t.Fatal("a refused trailer patch reached the store")
	}
	if err := r.s.CreateVideoSeries(ctx, &postgres.VideoSeries{CreatorID: r.owner, Title: "x", TrailerPostID: &mine}); err != nil {
		t.Fatalf("own trailer on create: %v", err)
	}
	if _, err := r.s.UpdateVideoSeries(ctx, r.owner, r.seriesID, postgres.VideoSeriesPatch{TrailerPostID: &mine}); err != nil {
		t.Fatalf("own trailer on PATCH: %v", err)
	}
}
