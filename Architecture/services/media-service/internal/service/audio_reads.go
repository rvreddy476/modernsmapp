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
// answer for it and is refused, except a platform library sound
// (library_id, 2026-10-15), which is public and heard by everyone while it
// is ready. A list leaves out what the viewer may not
// hear; it never says that something was left out.
//
// A RETIRED sound (2026-11-16: its source video was deleted by its author,
// migration 032) has no source either. It answers only through a reel that
// already plays it (?post_id=<reel> on GET /v1/audio/:id, /url and /serve):
// to the audience of that reel's video. Never in a list, never for a use.
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
// A retired sound is refused here: it is heard only through a reel that
// carries it (AudioTrackForViewerVia).
func (r *RecordReads) AudioTrackForViewer(ctx context.Context, viewerID, audioID uuid.UUID) (*postgres.AudioTrack, error) {
	return r.AudioTrackForViewerVia(ctx, viewerID, audioID, uuid.Nil)
}

// AudioTrackForViewerVia is AudioTrackForViewer with the reel the viewer is
// playing (viaPost, ?post_id=; uuid.Nil when the request names none). The
// reel matters only for a retired sound (admitToTrackVia).
func (r *RecordReads) AudioTrackForViewerVia(ctx context.Context, viewerID, audioID, viaPost uuid.UUID) (*postgres.AudioTrack, error) {
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
	if err := r.admitToTrackVia(ctx, viewerID, track, viaPost); err != nil {
		return nil, err
	}
	return track, nil
}

// admitToTrackVia is admitToTrack, except for a RETIRED sound (its source
// video deleted by its author, migration 032): that one is heard by the
// viewers of a reel that already plays it, and by nobody else. viaPost must
// be a live post that plays this sound, and the viewer must be admitted to
// that post's video (the same content-authority question as its bytes).
// Without a carrying post the answer is the missing one: a retired sound is
// never found for a new attachment, a search or its own page.
func (r *RecordReads) admitToTrackVia(ctx context.Context, viewerID uuid.UUID, track *postgres.AudioTrack, viaPost uuid.UUID) error {
	if track == nil {
		return delivery.ErrDeliveryDenied
	}
	if track.Status != postgres.SoundStatusRetired || track.IsLibrarySound() {
		return r.admitToTrack(ctx, viewerID, track)
	}
	if viaPost == uuid.Nil || track.AudioKey == "" {
		return delivery.ErrDeliveryDenied
	}
	carrier, err := r.store.RetiredSoundCarrier(ctx, track.ID, viaPost)
	if err != nil {
		return fmt.Errorf("%w: retired sound carrier: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if carrier == nil || *carrier == uuid.Nil {
		return delivery.ErrDeliveryDenied
	}
	_, err = r.MediaForViewer(ctx, viewerID, *carrier)
	return err
}

// admitToTrack is the audience decision for one sound.
//
// A platform library sound (library_id, library_sounds.go) has no source
// video: it is public and heard by everyone, the signed-out viewer included,
// while it is 'ready'. Withdrawing it (any other status) silences it.
func (r *RecordReads) admitToTrack(ctx context.Context, viewerID uuid.UUID, track *postgres.AudioTrack) error {
	if track.IsLibrarySound() {
		if track.Status != "ready" {
			return delivery.ErrDeliveryDenied
		}
		return nil
	}
	if track.Status == postgres.SoundStatusRetired {
		// Heard only through a reel that carries it (admitToTrackVia):
		// lists, trending and the sound page never answer it.
		return delivery.ErrDeliveryDenied
	}
	if track.SourceMediaID == nil || *track.SourceMediaID == uuid.Nil {
		return delivery.ErrDeliveryDenied
	}
	_, err := r.MediaForViewer(ctx, viewerID, *track.SourceMediaID)
	return err
}

// GET /v1/audio/trending is the stored ranking since 2026-10-24
// (trending_sounds.go); it asks admitToTrack for every row it serves.

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
	// Search is how a creator finds a sound to put on a new post, so a
	// withdrawn, switched-off or licence-expired library sound is left out
	// (reels audit 2026-10-30, 13). Its own page (AudioTrackForViewer)
	// still answers, with available_for_new_posts false.
	usable := tracks[:0]
	for _, t := range tracks {
		if t.ReuseBlock == "" {
			usable = append(usable, t)
		}
	}
	return r.audibleTo(ctx, viewerID, usable)
}

// audibleTo keeps the sounds viewerID may hear, in order. Several sounds of
// one source ask once. Never nil, so the list is `[]` on the wire.
func (r *RecordReads) audibleTo(ctx context.Context, viewerID uuid.UUID, tracks []postgres.AudioTrack) ([]postgres.AudioTrack, error) {
	out := make([]postgres.AudioTrack, 0, len(tracks))
	decided := make(map[uuid.UUID]bool, len(tracks))
	for i := range tracks {
		track := &tracks[i]
		if track.IsLibrarySound() {
			// The platform's: no source video to ask.
			if r.admitToTrack(ctx, viewerID, track) == nil {
				out = append(out, *track)
			}
			continue
		}
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

// ErrSoundUnavailableForNewPosts: the caller may hear the sound, but it may
// not go on a new post: a library sound that was withdrawn or switched off,
// or whose licence has expired (reels audit 2026-10-30, 13). Posts that
// already carry it keep playing it. HTTP: 410 SOUND_UNAVAILABLE.
var ErrSoundUnavailableForNewPosts = errors.New("sound is not available for new posts")

// UseAudioTrackAsViewer is POST /v1/audio/:audioId/use: the use is counted
// only for a sound the caller may hear, and a sound they may not is the same
// answer as one that does not exist. A sound they may hear but that may not
// go on a new post is ErrSoundUnavailableForNewPosts, and nothing is counted.
func (r *RecordReads) UseAudioTrackAsViewer(ctx context.Context, viewerID, audioID uuid.UUID) error {
	if viewerID == uuid.Nil {
		return delivery.ErrDeliveryDenied
	}
	track, err := r.AudioTrackForViewer(ctx, viewerID, audioID)
	if err != nil {
		return err
	}
	if !track.AvailableForNewPosts() {
		return fmt.Errorf("%w: %s", ErrSoundUnavailableForNewPosts, track.ReuseBlock)
	}
	if err := r.store.IncrementAudioUsageCount(ctx, audioID); err != nil {
		return fmt.Errorf("count audio use: %w", err)
	}
	return nil
}
