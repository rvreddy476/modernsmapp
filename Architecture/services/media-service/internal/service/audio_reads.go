package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sound reads (2026-09-29): a sound is the audio of a source video, so every
// route that names one answers to that video's audience and to nobody else.
//
// THE HOLE THIS CLOSES
//
// The presigned URL (GET /v1/audio/:audioId/url) was put behind the delivery
// gate earlier the same day. The routes beside it were not:
//
//	GET  /v1/audio/:audioId   answered the whole row to anyone holding the id:
//	                          title, artist, the source video's id and the
//	                          storage key, whose path carries the uploader.
//	GET  /v1/audio/trending   listed every ready sound, a private video's
//	GET  /v1/audio/search     included, with the same fields.
//	POST /v1/audio/:id/use    counted a use for any signed-in caller on any
//	                          id, and told a real id from a made-up one.
//
// THE RULE
//
// One decision, the record's (record_read.go, MediaForViewer on the source
// asset): the uploader, then whoever the content authority admits, signed
// out included for a public video. A sound with no source has nobody to
// answer for it and is refused. A list leaves out what the viewer may not
// hear; it never says that something was left out.
//
// Fail-closed: an authority that cannot answer is ErrDeliveryUnresolved for
// the whole request. A list that silently dropped the sounds it could not
// check would look complete and be cached as such.

// maxAudioList is the page ceiling of the sound lists.
const maxAudioList = 50

func clampAudioPage(limit, offset int) (int, int) {
	if limit <= 0 || limit > maxAudioList {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// AudioTrackForViewer is GET /v1/audio/:audioId for viewerID (uuid.Nil is
// signed-out): the row, once the viewer is admitted to the source video.
func (r *RecordReads) AudioTrackForViewer(ctx context.Context, viewerID, audioID uuid.UUID) (*postgres.AudioTrack, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: record store not configured", delivery.ErrDeliveryUnresolved)
	}
	track, err := r.store.GetAudioTrack(ctx, audioID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, delivery.ErrDeliveryDenied
		}
		return nil, fmt.Errorf("%w: load audio track: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if track == nil {
		return nil, delivery.ErrDeliveryDenied
	}
	if err := r.admitToTrack(ctx, viewerID, track); err != nil {
		return nil, err
	}
	return track, nil
}

// admitToTrack is the audience decision for one sound.
func (r *RecordReads) admitToTrack(ctx context.Context, viewerID uuid.UUID, track *postgres.AudioTrack) error {
	if track.SourceMediaID == nil || *track.SourceMediaID == uuid.Nil {
		return delivery.ErrDeliveryDenied
	}
	_, err := r.MediaForViewer(ctx, viewerID, *track.SourceMediaID)
	return err
}

// TrendingAudioForViewer is GET /v1/audio/trending for viewerID.
func (r *RecordReads) TrendingAudioForViewer(ctx context.Context, viewerID uuid.UUID, limit, offset int) ([]postgres.AudioTrack, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: record store not configured", delivery.ErrDeliveryUnresolved)
	}
	limit, offset = clampAudioPage(limit, offset)
	tracks, err := r.store.GetTrendingAudioTracks(ctx, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list trending audio: %w", err)
	}
	return r.audibleTo(ctx, viewerID, tracks)
}

// SearchAudioForViewer is GET /v1/audio/search for viewerID.
func (r *RecordReads) SearchAudioForViewer(ctx context.Context, viewerID uuid.UUID, query string, limit, offset int) ([]postgres.AudioTrack, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: record store not configured", delivery.ErrDeliveryUnresolved)
	}
	limit, offset = clampAudioPage(limit, offset)
	tracks, err := r.store.SearchAudioTracks(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("search audio: %w", err)
	}
	return r.audibleTo(ctx, viewerID, tracks)
}

// audibleTo keeps the sounds viewerID may hear, in order. Several sounds of
// one source ask once. Never nil, so the list is `[]` on the wire.
func (r *RecordReads) audibleTo(ctx context.Context, viewerID uuid.UUID, tracks []postgres.AudioTrack) ([]postgres.AudioTrack, error) {
	out := make([]postgres.AudioTrack, 0, len(tracks))
	decided := make(map[uuid.UUID]bool, len(tracks))
	for i := range tracks {
		track := &tracks[i]
		if track.SourceMediaID == nil || *track.SourceMediaID == uuid.Nil {
			continue
		}
		source := *track.SourceMediaID
		admitted, known := decided[source]
		if !known {
			err := r.admitToTrack(ctx, viewerID, track)
			switch {
			case err == nil:
				admitted = true
			case errors.Is(err, delivery.ErrDeliveryDenied):
				admitted = false
			default:
				return nil, err
			}
			decided[source] = admitted
		}
		if admitted {
			out = append(out, *track)
		}
	}
	return out, nil
}

// UseAudioTrackAsViewer is POST /v1/audio/:audioId/use: the use is counted
// only for a sound the caller may hear, and a sound they may not is the same
// answer as one that does not exist.
func (r *RecordReads) UseAudioTrackAsViewer(ctx context.Context, viewerID, audioID uuid.UUID) error {
	if viewerID == uuid.Nil {
		return delivery.ErrDeliveryDenied
	}
	if _, err := r.AudioTrackForViewer(ctx, viewerID, audioID); err != nil {
		return err
	}
	if err := r.store.IncrementAudioUsageCount(ctx, audioID); err != nil {
		return fmt.Errorf("count audio use: %w", err)
	}
	return nil
}
