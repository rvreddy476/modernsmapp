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

// AttachAudioToPost associates an audio track with a post and increments the use count.
func (s *Service) AttachAudioToPost(ctx context.Context, postID, audioTrackID uuid.UUID) error {
	// Verify the audio track exists
	if _, err := s.pgStore.GetAudioTrack(ctx, audioTrackID); err != nil {
		return fmt.Errorf("audio track not found: %w", err)
	}
	if err := s.pgStore.AttachAudioToPost(ctx, postID, audioTrackID); err != nil {
		return fmt.Errorf("attach audio to post: %w", err)
	}
	// Increment use count (best-effort)
	_ = s.pgStore.IncrementAudioUseCount(ctx, audioTrackID)
	return nil
}
