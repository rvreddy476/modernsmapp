package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

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

// ─── Audio Library ──────────────────────────────────────────────────

// GetTrendingAudioLibrary returns library tracks ordered by usage_count.
func (s *Service) GetTrendingAudioLibrary(ctx context.Context, genre *string, limit, offset int) ([]postgres.AudioLibraryTrack, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.pgStore.GetTrendingAudioLibrary(ctx, genre, limit, offset)
}

// GetAudioLibraryTrackByID returns a single audio_library track.
func (s *Service) GetAudioLibraryTrackByID(ctx context.Context, id uuid.UUID) (*postgres.AudioLibraryTrack, error) {
	return s.pgStore.GetAudioLibraryTrack(ctx, id)
}

// ─── Multi-clip ─────────────────────────────────────────────────────

// SaveMediaClips replaces the clip sequence for a Flick post.
func (s *Service) SaveMediaClips(ctx context.Context, postID uuid.UUID, clips []postgres.MediaClip) error {
	return s.pgStore.SaveMediaClips(ctx, postID, clips)
}

// GetMediaClips returns the ordered clip sequence for a Flick post.
func (s *Service) GetMediaClips(ctx context.Context, postID uuid.UUID) ([]postgres.MediaClip, error) {
	return s.pgStore.GetMediaClips(ctx, postID)
}

// ─── Subtitles ───────────────────────────────────────────────────────

// GetSubtitles returns all subtitle tracks for a media asset.
func (s *Service) GetSubtitles(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaSubtitle, error) {
	return s.pgStore.GetSubtitles(ctx, mediaAssetID)
}

// CreateSubtitle upserts a subtitle track for a media asset.
func (s *Service) CreateSubtitle(ctx context.Context, sub *postgres.MediaSubtitle) (*postgres.MediaSubtitle, error) {
	return s.pgStore.CreateSubtitle(ctx, sub)
}

// GenerateAutoCaptions runs the configured speech-to-text backend
// against the audio of a video media asset and persists the result
// as a media_subtitles row with source="auto". Idempotent — calling
// twice for the same (media, language) replaces the previous row
// (CreateSubtitle's upsert semantics).
//
// Behaviour:
//   - When OPENAI_API_KEY is set, the WhisperBackend ships the audio
//     to OpenAI and returns a real transcript with word-level timing.
//   - When not set, StubBackend returns a placeholder marked
//     IsPlaceholder=true so the studio can render "captions
//     pending — wire a backend".
//
// language="" asks the backend to auto-detect.
func (s *Service) GenerateAutoCaptions(ctx context.Context, mediaID uuid.UUID, language string) (*postgres.MediaSubtitle, error) {
	if s.captions == nil {
		return nil, fmt.Errorf("CAPTIONS_BACKEND_UNCONFIGURED")
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, fmt.Errorf("media not found: %w", err)
	}
	if media.FileType != "video" {
		return nil, fmt.Errorf("auto-captions only supported for video media")
	}

	// Use a presigned GET URL so the backend can fetch directly from
	// blob storage (Whisper needs the raw audio file). Short expiry —
	// transcription rarely takes more than a minute or two.
	signed, err := s.blobStore.GeneratePresignedGetURL(ctx, media.StorageKey, 30*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("sign audio url: %w", err)
	}

	res, err := s.captions.Transcribe(ctx, signed.String(), language)
	if err != nil {
		slog.Error("auto-captions: backend failed",
			"backend", s.captions.Name(), "media_id", mediaID, "error", err)
		return nil, fmt.Errorf("transcribe: %w", err)
	}

	var wordsJSON []byte
	if len(res.Words) > 0 {
		wordsJSON, _ = json.Marshal(res.Words)
	}
	conf := res.Confidence
	sub := &postgres.MediaSubtitle{
		MediaAssetID:  mediaID,
		Language:      res.Language,
		Source:        "auto",
		Format:        res.Format,
		ContentURL:    "", // inline transcript only — no .vtt file rendered yet
		WordLevelJSON: wordsJSON,
		Confidence:    &conf,
	}
	saved, err := s.pgStore.CreateSubtitle(ctx, sub)
	if err != nil {
		return nil, fmt.Errorf("save subtitle: %w", err)
	}
	slog.Info("auto-captions: stored",
		"backend", s.captions.Name(),
		"media_id", mediaID,
		"language", res.Language,
		"placeholder", res.IsPlaceholder,
		"word_count", len(res.Words))
	return saved, nil
}


// ─── Voiceover ───────────────────────────────────────────────────────

// RecordVoiceover transcodes raw audio to AAC, uploads it to blob storage,
// and creates a media_assets record so it can be attached as a Flick overlay.
func (s *Service) RecordVoiceover(ctx context.Context, uploaderID uuid.UUID, audioData []byte, mimeType string) (*postgres.MediaAsset, error) {
	tmpDir, err := os.MkdirTemp("", "voiceover-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	ext := ".webm"
	switch mimeType {
	case "audio/mp4", "audio/aac":
		ext = ".m4a"
	case "audio/ogg":
		ext = ".ogg"
	}
	inputPath := tmpDir + "/input" + ext
	if err := os.WriteFile(inputPath, audioData, 0644); err != nil {
		return nil, fmt.Errorf("write input: %w", err)
	}

	outputPath := tmpDir + "/output.m4a"
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", inputPath,
		"-c:a", "aac", "-b:a", "128k", outputPath)
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		slog.Warn("ffmpeg voiceover transcode failed, using raw audio",
			"err", cmdErr, "output", string(out))
		outputPath = inputPath
	}

	outData, err := os.ReadFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("read output: %w", err)
	}

	storageKey := fmt.Sprintf("voiceovers/%s/%s.m4a", uploaderID, uuid.New())
	if err := s.blobStore.UploadObject(ctx, storageKey, outData, "audio/mp4"); err != nil {
		return nil, fmt.Errorf("upload voiceover: %w", err)
	}

	presignURL, err := s.blobStore.GeneratePresignedGetURL(ctx, storageKey, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("presign voiceover URL: %w", err)
	}

	now := time.Now()
	asset := &postgres.MediaAsset{
		ID:               uuid.New(),
		UploaderID:       uploaderID,
		FileType:         "audio",
		MediaSubtype:     "voiceover",
		MimeType:         "audio/mp4",
		FileSizeBytes:    int64(len(outData)),
		StorageBucket:    s.blobStore.Bucket(),
		StorageKey:       storageKey,
		ProcessingStatus: "ready",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	originalURL := presignURL.String()
	asset.OriginalURL = &originalURL

	if err := s.pgStore.CreateMedia(ctx, asset); err != nil {
		return nil, fmt.Errorf("create media asset: %w", err)
	}

	slog.Info("voiceover uploaded", "uploader_id", uploaderID, "media_id", asset.ID, "size_bytes", len(outData))
	return asset, nil
}
