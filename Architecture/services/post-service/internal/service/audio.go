package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
// THE RULE for attaching: a post may play a sound its author may hear, and
// may reuse.
//
//  1. The sound exists and is ready.
//  2. A private sound (is_public = false) is its creator's alone (M10).
//  3. A post does not add its OWN video's sound: it already plays it. That
//     is not a refusal; nothing is linked and nothing is counted.
//  4. The author is admitted to the sound's media by this service's own
//     media-access decision, the one that gates the bytes: their own upload,
//     or a video whose audience includes them. A sound with no media has
//     nobody to answer for it and is refused.
//  5. Creator consent: no reel the sound was taken from has turned reuse
//     off (remix_setting = 'disallow'). Its own author always may.
//
// The link stores where in the sound playback starts (audio_start_ms); a
// start outside the sound is 0.
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
	// ErrSoundReuseNotAllowed: a reel the sound comes from has turned reuse
	// off, and the actor is not its author.
	ErrSoundReuseNotAllowed = errors.New("the creator has turned off reuse of this sound")
)

// remixDisallow is the remix_setting that withdraws a reel's audio from
// reuse; 'allow' and 'allow_audio_only' both permit it.
const remixDisallow = "disallow"

// audioTrackStore is the store slice attaching a sound needs. An interface
// so the rule is testable without a database.
type audioTrackStore interface {
	GetAudioTrack(ctx context.Context, id uuid.UUID) (*postgres.AudioTrack, error)
	AttachAudioToPost(ctx context.Context, postID, audioTrackID uuid.UUID, startMs int) (newToPost bool, err error)
	IncrementAudioUseCount(ctx context.Context, id uuid.UUID) error
	SoundSourcePosts(ctx context.Context, sourcePostID, mediaID *uuid.UUID) ([]postgres.SoundSourcePost, error)
	PrimaryVideoMediaID(ctx context.Context, postID uuid.UUID) (*uuid.UUID, error)
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

// SoundLink is the link AttachAudioToPost made: the sound, and where in it
// playback starts.
type SoundLink struct {
	AudioTrackID uuid.UUID
	StartMs      int
}

// clampSoundStart is the stored start: startMs when it lies inside the
// sound, 0 otherwise (negative, past the end, or a sound of unknown length).
func clampSoundStart(startMs, durationMs int) int {
	if startMs < 0 || startMs > durationMs {
		return 0
	}
	return startMs
}

// soundReuseAllowed is creator consent: false when any post the sound comes
// from has turned reuse off and actorID is not that post's author.
func soundReuseAllowed(sources []postgres.SoundSourcePost, actorID uuid.UUID) bool {
	for _, src := range sources {
		if strings.EqualFold(strings.TrimSpace(src.RemixSetting), remixDisallow) && src.AuthorID != actorID {
			return false
		}
	}
	return true
}

// AttachAudioToPost links a sound to a post and counts the use. actorID is
// the post's author; startMs is where in the sound playback starts. It
// returns the link it made, or nil with no error when the sound is the
// post's own video's and nothing was linked.
func (s *Service) AttachAudioToPost(ctx context.Context, actorID, postID, audioTrackID uuid.UUID, startMs int) (*SoundLink, error) {
	track, err := s.audioStore().GetAudioTrack(ctx, audioTrackID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAudioTrackNotFound
		}
		return nil, fmt.Errorf("load audio track: %w", err)
	}
	if track == nil {
		return nil, ErrAudioTrackNotFound
	}
	if track.Status != audioTrackReady {
		return nil, fmt.Errorf("%w: status %q", ErrAudioTrackNotReady, track.Status)
	}
	// M10: private tracks can only be attached by the creator.
	if !track.IsPublic {
		if track.CreatorUserID == nil || *track.CreatorUserID != actorID {
			return nil, ErrAudioTrackPrivate
		}
	}
	if track.MediaID == nil || *track.MediaID == uuid.Nil {
		return nil, ErrAudioTrackNotFound
	}
	// The post's own video's sound is the audio it already plays.
	own, err := s.isOwnSound(ctx, postID, track)
	if err != nil {
		return nil, fmt.Errorf("audio track source: %w", err)
	}
	if own {
		return nil, nil
	}
	allowed, err := s.authorMayHear(ctx, actorID, *track.MediaID)
	if err != nil {
		return nil, fmt.Errorf("audio track audience: %w", err)
	}
	if !allowed {
		return nil, ErrAudioTrackNotFound
	}
	sources, err := s.audioStore().SoundSourcePosts(ctx, track.SourcePostID, track.MediaID)
	if err != nil {
		return nil, fmt.Errorf("audio track consent: %w", err)
	}
	if !soundReuseAllowed(sources, actorID) {
		return nil, ErrSoundReuseNotAllowed
	}
	link := &SoundLink{AudioTrackID: audioTrackID, StartMs: clampSoundStart(startMs, track.DurationMs)}
	newToPost, err := s.audioStore().AttachAudioToPost(ctx, postID, audioTrackID, link.StartMs)
	if err != nil {
		return nil, fmt.Errorf("attach audio to post: %w", err)
	}
	// The cached body (post_cache.go) was written without the link.
	s.InvalidatePostBodyCache(ctx, postID)
	if !newToPost {
		// The post already played this sound: a retry, not another use.
		return link, nil
	}
	// Increment use count (best-effort). Routes through the sharded
	// counter when Redis is configured; falls back to the per-event PG
	// UPDATE otherwise. Counter-sharding rollout — replaces the previous
	// direct s.pgStore.IncrementAudioUseCount call so concurrent attach
	// load on a trending audio row no longer pins audio_tracks under
	// row-level lock contention.
	_ = s.adjustAudioUseCount(ctx, audioTrackID)
	return link, nil
}

// isOwnSound reports whether the sound was taken from this very post: the
// sound names the post as its source, or its media is the post's primary
// video.
func (s *Service) isOwnSound(ctx context.Context, postID uuid.UUID, track *postgres.AudioTrack) (bool, error) {
	if track.SourcePostID != nil && *track.SourcePostID == postID {
		return true, nil
	}
	primary, err := s.audioStore().PrimaryVideoMediaID(ctx, postID)
	if err != nil {
		return false, err
	}
	return primary != nil && track.MediaID != nil && *primary == *track.MediaID, nil
}

// attachDraftSound links the sound a draft chose to the post published from
// it. audioTrackID is the draft's text column; anything that is not a sound
// id is no sound. Best-effort like every attach after a create: the post
// exists, so the caller logs a refusal or a fault and the post goes out
// without the sound.
func (s *Service) attachDraftSound(ctx context.Context, authorID, postID uuid.UUID, audioTrackID *string, startMs int) (*SoundLink, error) {
	if audioTrackID == nil {
		return nil, nil
	}
	// The empty string a draft stores for "no sound" does not parse either.
	audioID, err := uuid.Parse(strings.TrimSpace(*audioTrackID))
	if err != nil {
		return nil, nil
	}
	return s.AttachAudioToPost(ctx, authorID, postID, audioID, startMs)
}

// ApplySoundLink writes a link made after the create onto the post body the
// caller is about to return, so the answer matches the row.
func ApplySoundLink(p *postgres.Post, link *SoundLink) {
	if p == nil || link == nil {
		return
	}
	id, start := link.AudioTrackID, link.StartMs
	p.AudioTrackID, p.AudioStartMs = &id, &start
}

const audioTrackReady = "ready"
