package service

import (
	"context"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateAudioTrack creates a new audio track record.
func (s *Service) CreateAudioTrack(ctx context.Context, track *postgres.AudioTrack) (*postgres.AudioTrack, error) {
	if track.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if track.MediaID == uuid.Nil {
		return nil, fmt.Errorf("media_id is required")
	}
	if err := s.pgStore.CreateAudioTrack(ctx, track); err != nil {
		return nil, fmt.Errorf("create audio track: %w", err)
	}
	return track, nil
}

// GetAudioTrack retrieves an audio track by ID.
func (s *Service) GetAudioTrack(ctx context.Context, id uuid.UUID) (*postgres.AudioTrack, error) {
	return s.pgStore.GetAudioTrack(ctx, id)
}

// GetTrendingAudio returns trending audio tracks.
func (s *Service) GetTrendingAudio(ctx context.Context, limit int) ([]postgres.AudioTrack, error) {
	return s.pgStore.GetTrendingAudio(ctx, limit)
}

// SearchAudio searches audio tracks by title or artist.
func (s *Service) SearchAudio(ctx context.Context, query string, limit int) ([]postgres.AudioTrack, error) {
	return s.pgStore.SearchAudio(ctx, query, limit)
}

// ErrAudioTrackPrivate is returned when an actor tries to attach a
// private audio track they don't own. M10 — previously any actor
// could attach any audio_track to their own post, including ones a
// creator had explicitly marked private.
var ErrAudioTrackPrivate = fmt.Errorf("audio track is private to its creator")

// AttachAudioToPost associates an audio track with a post and
// increments the use count. actorID is the post's author — needed
// for the M10 ownership check against private tracks.
func (s *Service) AttachAudioToPost(ctx context.Context, actorID, postID, audioTrackID uuid.UUID) error {
	track, err := s.pgStore.GetAudioTrack(ctx, audioTrackID)
	if err != nil {
		return fmt.Errorf("audio track not found: %w", err)
	}
	// M10: private tracks can only be attached by the creator. Public
	// tracks stay reusable by anyone (TikTok/Reels default UX).
	if !track.IsPublic {
		if track.CreatorUserID == nil || *track.CreatorUserID != actorID {
			return ErrAudioTrackPrivate
		}
	}
	if err := s.pgStore.AttachAudioToPost(ctx, postID, audioTrackID); err != nil {
		return fmt.Errorf("attach audio to post: %w", err)
	}
	// Increment use count (best-effort)
	_ = s.pgStore.IncrementAudioUseCount(ctx, audioTrackID)
	return nil
}
