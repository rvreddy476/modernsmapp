package service

import (
	"context"
	"errors"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Series and playlist covers under the claim protocol (media_claims.go),
// founder decision of 31 Oct 2026: they are media-claimed like every other
// cover.
//
//	video_series.cover_media_id  kind video_series_cover, ref = series id
//	playlists.cover_url          kind playlist_cover,     ref = playlist id
//
// The series cover's foreign key became ON DELETE SET NULL in migration 081,
// so the owner's delete used to clear a series cover from under it; a
// playlist cover is a TEXT serve path (/v1/media/<id>/serve) no key can
// guard. Each writer below claims the cover before the row names it and
// releases the one the row stopped naming:
//
//	create   mint the id, claim the cover, insert
//	update   (only when the patch sets a cover) claim the new one, write,
//	         release the old; an empty playlist cover releases the old
//	delete   delete, then release whatever the gone row held
//
// Each runs under the row's holder lock (LockMediaHolder, behind the
// in-process gate) and takes exactly one: never two at once. Authority comes
// first and is unchanged: the cover is the creator's own finished image
// (verifySeriesCover, normalizePlaylistCover). The backfill for covers saved
// before this is cmd/media-claims, which pages both kinds.

// tubeCoverRows is the series create and the playlist writes the claimed
// writers make (*postgres.Store). A series update or delete goes through
// videoSeriesStore, which the ownership check already reads.
type tubeCoverRows interface {
	CreateVideoSeries(ctx context.Context, vs *postgres.VideoSeries) error
	CreatePlaylist(ctx context.Context, p *postgres.Playlist) error
	UpdatePlaylist(ctx context.Context, id uuid.UUID, patch postgres.PlaylistPatch) (*postgres.Playlist, error)
	DeletePlaylist(ctx context.Context, id uuid.UUID) error
}

// tubeDecisionSeams are the 31 Oct founder decisions' test seams; all nil in
// production.
type tubeDecisionSeams struct {
	// tubeCoverRowsSeam stands in for the series / playlist writes.
	tubeCoverRowsSeam tubeCoverRows
	// productTagListSeam stands in for the product-tag read.
	productTagListSeam func(ctx context.Context, postID uuid.UUID) ([]*postgres.PostProductTag, error)
}

func (s *Service) tubeCoverWrites() tubeCoverRows {
	if s.tubeCoverRowsSeam != nil {
		return s.tubeCoverRowsSeam
	}
	return s.pgStore
}

// coverClaimError makes a cover the claim found gone a refusal of the
// COVER, as the post cover's (setPostCoverClaimed).
func coverClaimError(err error) error {
	var gone *ClaimMediaGoneError
	if errors.As(err, &gone) {
		return &CoverMediaError{Err: err}
	}
	return err
}

// createVideoSeriesClaimed writes a new series with its cover claimed first.
// A series without a cover claims nothing and takes no lock.
func (s *Service) createVideoSeriesClaimed(ctx context.Context, vs *postgres.VideoSeries) error {
	rows := s.tubeCoverWrites()
	write := func() error { return rows.CreateVideoSeries(ctx, vs) }
	want := optionalID(vs.CoverMediaID)
	if len(want) == 0 {
		return write()
	}
	if vs.ID == uuid.Nil {
		vs.ID = uuid.New()
	}
	return coverClaimError(s.holderWrite(ctx, postgres.HolderVideoSeriesCover, vs.ID,
		func(postgres.MediaHolding) ([]uuid.UUID, error) { return want, nil },
		write))
}

// updateVideoSeriesClaimed applies patch. Only a patch that sets the cover
// changes a claim (a series cover can be replaced, not cleared: the patch
// has no "set to null"); any other patch is written as it always was.
func (s *Service) updateVideoSeriesClaimed(ctx context.Context, seriesID uuid.UUID, patch postgres.VideoSeriesPatch) (*postgres.VideoSeries, error) {
	rows := s.videoSeries
	if rows == nil {
		return nil, ErrVideoSeriesStoreUnavailable
	}
	var out *postgres.VideoSeries
	write := func() error {
		vs, err := rows.UpdateVideoSeries(ctx, seriesID, patch)
		if err != nil {
			return err
		}
		if vs == nil {
			// Deleted between the ownership read and the update.
			return ErrVideoSeriesNotFound
		}
		out = vs
		return nil
	}
	if patch.CoverMediaID == nil {
		return out, write()
	}
	// A series deleted since the ownership read: the write finds no row and
	// fails, and holderWrite releases the claim it made.
	want := optionalID(patch.CoverMediaID)
	err := s.holderWrite(ctx, postgres.HolderVideoSeriesCover, seriesID,
		func(postgres.MediaHolding) ([]uuid.UUID, error) { return want, nil },
		write)
	if err != nil {
		return nil, coverClaimError(err)
	}
	return out, nil
}

// deleteVideoSeriesClaimed deletes the series and releases its cover claim
// (best effort: media-claims repairs a lost release, and its orphan pass
// finds the gone row).
func (s *Service) deleteVideoSeriesClaimed(ctx context.Context, seriesID uuid.UUID) error {
	rows := s.videoSeries
	if rows == nil {
		return ErrVideoSeriesStoreUnavailable
	}
	if err := rows.DeleteVideoSeries(ctx, seriesID); err != nil {
		return err
	}
	s.settleMediaHolderBestEffort(ctx, postgres.HolderVideoSeriesCover, seriesID)
	return nil
}

// createPlaylistClaimed writes a new playlist with its cover claimed first.
// p.CoverURL is already normalized (normalizePlaylistCover): a serve path,
// "" or nil.
func (s *Service) createPlaylistClaimed(ctx context.Context, p *postgres.Playlist) error {
	rows := s.tubeCoverWrites()
	write := func() error { return rows.CreatePlaylist(ctx, p) }
	want := optionalID(postgres.ParsePlaylistCoverID(p.CoverURL))
	if len(want) == 0 {
		return write()
	}
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return coverClaimError(s.holderWrite(ctx, postgres.HolderPlaylistCover, p.ID,
		func(postgres.MediaHolding) ([]uuid.UUID, error) { return want, nil },
		write))
}

// updatePlaylistClaimed applies patch. A patch that sets cover_url claims
// the new cover and releases the old one; "" (no cover) releases the old
// one. Any other patch changes no claim.
func (s *Service) updatePlaylistClaimed(ctx context.Context, playlistID uuid.UUID, patch postgres.PlaylistPatch) (*postgres.Playlist, error) {
	rows := s.tubeCoverWrites()
	var out *postgres.Playlist
	write := func() error {
		p, err := rows.UpdatePlaylist(ctx, playlistID, patch)
		if err != nil {
			return err
		}
		if p == nil {
			// Deleted between the ownership read and the update.
			return ErrPlaylistNotFound
		}
		out = p
		return nil
	}
	if patch.CoverURL == nil {
		return out, write()
	}
	want := optionalID(postgres.ParsePlaylistCoverID(patch.CoverURL))
	err := s.holderWrite(ctx, postgres.HolderPlaylistCover, playlistID,
		func(postgres.MediaHolding) ([]uuid.UUID, error) { return want, nil },
		write)
	if err != nil {
		return nil, coverClaimError(err)
	}
	return out, nil
}

// deletePlaylistClaimed deletes the playlist and releases its cover claim
// (best effort, as deleteVideoSeriesClaimed).
func (s *Service) deletePlaylistClaimed(ctx context.Context, playlistID uuid.UUID) error {
	rows := s.tubeCoverWrites()
	if err := rows.DeletePlaylist(ctx, playlistID); err != nil {
		return err
	}
	s.settleMediaHolderBestEffort(ctx, postgres.HolderPlaylistCover, playlistID)
	return nil
}
