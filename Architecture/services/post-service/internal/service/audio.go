package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Sounds on posts (2026-09-29).
//
// ONE OWNER. media-service owns the sound catalogue: it holds the audio, it
// extracts a sound from a video, and its routes (/v1/audio, which the
// gateway sends there) list and serve a sound to the source video's
// audience. post-service owns one thing, the link from a post to the sound
// it plays (posts.audio_track_id), and the rule for making that link.
//
// post-service used to carry a second catalogue under /v1/audio/tracks:
// create, trending, search and read over the same table. The gateway never
// routed it, so no client could reach it, and it must not become reachable
// as it was: the create took no caller and no proof that the caller owned
// the media, and the lists showed every row. It is removed. A sound is made
// with POST /v1/audio/extract/:mediaId and found with GET /v1/audio/trending
// and /search.
//
// THE RULE for attaching: a post may play a sound its author may hear.
//
//  1. The sound exists and is ready.
//  2. A private sound (is_public = false) is its creator's alone (M10).
//  3. The author is admitted to the sound's media by this service's own
//     media-access decision, the one that gates the bytes: their own upload,
//     or a video whose audience includes them. A sound with no media has
//     nobody to answer for it and is refused.
//
// Fail-closed: a decision that cannot be made refuses the attach. The post
// itself is already created by then; the callers log and carry on, so the
// post is published without the sound rather than with one its author was
// never allowed to use.

var (
	// ErrAudioTrackPrivate is returned when an actor tries to attach a
	// private audio track they don't own. M10 — previously any actor
	// could attach any audio_track to their own post, including ones a
	// creator had explicitly marked private.
	ErrAudioTrackPrivate = errors.New("audio track is private to its creator")
	// ErrAudioTrackNotFound: no such sound, or one the author may not hear.
	// One error for both, so an attach cannot be used to test an id.
	ErrAudioTrackNotFound = errors.New("audio track not found")
	// ErrAudioTrackNotReady: the sound is still processing, or was refused.
	ErrAudioTrackNotReady = errors.New("audio track is not ready")
)

// audioTrackStore is the store slice attaching a sound needs. An interface
// so the rule is testable without a database.
type audioTrackStore interface {
	GetAudioTrack(ctx context.Context, id uuid.UUID) (*postgres.AudioTrack, error)
	AttachAudioToPost(ctx context.Context, postID, audioTrackID uuid.UUID) error
	IncrementAudioUseCount(ctx context.Context, id uuid.UUID) error
}

func (s *Service) audioStore() audioTrackStore {
	if s.audioTracks != nil {
		return s.audioTracks
	}
	return s.pgStore
}

// authorMayHear is the media-access decision for the author on the sound's
// media; audioAudience replaces it in tests.
func (s *Service) authorMayHear(ctx context.Context, authorID, mediaID uuid.UUID) (bool, error) {
	if s.audioAudience != nil {
		return s.audioAudience(ctx, authorID, mediaID)
	}
	res, err := s.ViewerMayAccessMedia(ctx, authorID, mediaID)
	if err != nil {
		return false, err
	}
	return res.Allowed, nil
}

// AttachAudioToPost links a sound to a post and counts the use. actorID is
// the post's author.
func (s *Service) AttachAudioToPost(ctx context.Context, actorID, postID, audioTrackID uuid.UUID) error {
	track, err := s.audioStore().GetAudioTrack(ctx, audioTrackID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAudioTrackNotFound
		}
		return fmt.Errorf("load audio track: %w", err)
	}
	if track == nil {
		return ErrAudioTrackNotFound
	}
	if track.Status != audioTrackReady {
		return fmt.Errorf("%w: status %q", ErrAudioTrackNotReady, track.Status)
	}
	// M10: private tracks can only be attached by the creator.
	if !track.IsPublic {
		if track.CreatorUserID == nil || *track.CreatorUserID != actorID {
			return ErrAudioTrackPrivate
		}
	}
	if track.MediaID == nil || *track.MediaID == uuid.Nil {
		return ErrAudioTrackNotFound
	}
	allowed, err := s.authorMayHear(ctx, actorID, *track.MediaID)
	if err != nil {
		return fmt.Errorf("audio track audience: %w", err)
	}
	if !allowed {
		return ErrAudioTrackNotFound
	}
	if err := s.audioStore().AttachAudioToPost(ctx, postID, audioTrackID); err != nil {
		return fmt.Errorf("attach audio to post: %w", err)
	}
	// Increment use count (best-effort). Routes through the sharded
	// counter when Redis is configured; falls back to the per-event PG
	// UPDATE otherwise. Counter-sharding rollout — replaces the previous
	// direct s.pgStore.IncrementAudioUseCount call so concurrent attach
	// load on a trending audio row no longer pins audio_tracks under
	// row-level lock contention.
	_ = s.adjustAudioUseCount(ctx, audioTrackID)
	return nil
}

const audioTrackReady = "ready"
