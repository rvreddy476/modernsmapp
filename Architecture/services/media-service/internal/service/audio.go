package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ExtractAudioFromMedia extracts the audio track from a video media asset,
// uploads it to blob storage, and creates an audio_tracks record.
func (s *Service) ExtractAudioFromMedia(ctx context.Context, mediaID uuid.UUID, title, artist string) (*postgres.AudioTrack, error) {
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, fmt.Errorf("media not found: %w", err)
	}
	if media.FileType != "video" {
		return nil, fmt.Errorf("audio extraction only supported for video media")
	}

	// Check if audio track already exists for this media
	existing, err := s.pgStore.GetAudioTrackByMedia(ctx, mediaID)
	if err == nil && existing != nil {
		return existing, nil
	}

	// Download original video
	videoData, err := s.blobStore.DownloadObject(ctx, media.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("download video: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "audio-extract-"+mediaID.String())
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPath := tmpDir + "/input"
	if err := os.WriteFile(inputPath, videoData, 0644); err != nil {
		return nil, fmt.Errorf("write temp: %w", err)
	}

	// Extract audio
	audioPath, audioMeta, err := processing.ExtractAudio(ctx, inputPath, tmpDir)
	if err != nil {
		return nil, fmt.Errorf("extract audio: %w", err)
	}

	audioData, err := os.ReadFile(audioPath)
	if err != nil {
		return nil, fmt.Errorf("read extracted audio: %w", err)
	}

	// Upload audio to blob storage
	audioKey := fmt.Sprintf("audio/%s/%s/audio.m4a", media.UploaderID, mediaID)
	if err := s.blobStore.UploadObject(ctx, audioKey, audioData, "audio/mp4"); err != nil {
		return nil, fmt.Errorf("upload audio: %w", err)
	}

	// Generate waveform
	var waveformKey *string
	waveformPath, wfErr := processing.GenerateWaveform(ctx, audioPath, tmpDir, 200)
	if wfErr == nil {
		wfData, readErr := os.ReadFile(waveformPath)
		if readErr == nil {
			wfKey := fmt.Sprintf("audio/%s/%s/waveform.json", media.UploaderID, mediaID)
			if uploadErr := s.blobStore.UploadObject(ctx, wfKey, wfData, "application/json"); uploadErr == nil {
				waveformKey = &wfKey
			}
		}
	}

	durationMs := 0
	sampleRate := 0
	if audioMeta != nil {
		durationMs = audioMeta.DurationMs
		sampleRate = audioMeta.SampleRate
	}

	if title == "" {
		title = "Original Sound"
	}

	track := &postgres.AudioTrack{
		SourceMediaID: &mediaID,
		Title:         title,
		Artist:        artist,
		AudioKey:      audioKey,
		WaveformKey:   waveformKey,
		DurationMs:    durationMs,
		SampleRate:    &sampleRate,
		Status:        "ready",
		IsOriginal:    true,
		LicenseType:   "standard",
	}

	if err := s.pgStore.CreateAudioTrack(ctx, track); err != nil {
		return nil, fmt.Errorf("create audio track: %w", err)
	}

	slog.Info("audio track extracted", "media_id", mediaID, "audio_track_id", track.ID, "duration_ms", durationMs)
	return track, nil
}

// GetAudioTrack returns an audio track by ID.
func (s *Service) GetAudioTrack(ctx context.Context, id uuid.UUID) (*postgres.AudioTrack, error) {
	return s.pgStore.GetAudioTrack(ctx, id)
}

// GetTrendingAudio returns trending audio tracks.
func (s *Service) GetTrendingAudio(ctx context.Context, limit, offset int) ([]postgres.AudioTrack, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.pgStore.GetTrendingAudioTracks(ctx, limit, offset)
}

// SearchAudio searches audio tracks by title or artist.
func (s *Service) SearchAudio(ctx context.Context, query string, limit, offset int) ([]postgres.AudioTrack, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.pgStore.SearchAudioTracks(ctx, query, limit, offset)
}

// UseAudioTrack increments usage count (snapshot) for a track.
func (s *Service) UseAudioTrack(ctx context.Context, audioTrackID uuid.UUID) error {
	return s.pgStore.IncrementAudioUsageCount(ctx, audioTrackID)
}

// GetAudioTrackURL returns a presigned URL for the audio file.
func (s *Service) GetAudioTrackURL(ctx context.Context, id uuid.UUID) (string, error) {
	track, err := s.pgStore.GetAudioTrack(ctx, id)
	if err != nil {
		return "", err
	}
	u, err := s.blobStore.GeneratePresignedGetURL(ctx, track.AudioKey, defaultURLExpiry)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
