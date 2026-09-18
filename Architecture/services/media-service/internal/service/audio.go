package service

import (
	"context"
	"encoding/json"
	"errors"
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

// SaveMediaClips replaces the clip sequence for a Flick post, owner-only.
//
// It previously took no actor at all: POST /v1/clips/:postId authenticated
// the caller and then replaced ANY post's clip sequence, because the only
// identifier in the request was the post id. The route is not routed at
// the gateway today, which is why nobody noticed — an unreachable loaded
// gun is still loaded.
//
// media-service does not know who owns a post, so it authorizes what it
// does own: every media asset involved. The actor must own each asset
// being written (AssertMediaOwner, the same check the caption writes use)
// AND every asset already in the sequence being replaced — otherwise
// owning one clip would be enough to delete somebody else's whole edit.
func (s *Service) SaveMediaClips(ctx context.Context, actorID, postID uuid.UUID, clips []postgres.MediaClip) error {
	if actorID == uuid.Nil {
		return ErrNotMediaOwner
	}
	existing, err := s.pgStore.GetMediaClips(ctx, postID)
	if err != nil {
		return err
	}
	if err := assertClipsOwned(ctx, s.AssertMediaOwner, actorID, existing, clips); err != nil {
		return err
	}
	return s.pgStore.SaveMediaClips(ctx, postID, clips)
}

// assertClipsOwned requires actorID to own every distinct media asset in
// every given clip set. assertOwner is AssertMediaOwner in production.
func assertClipsOwned(
	ctx context.Context,
	assertOwner func(ctx context.Context, mediaID, actorID uuid.UUID) error,
	actorID uuid.UUID,
	clipSets ...[]postgres.MediaClip,
) error {
	if actorID == uuid.Nil {
		return ErrNotMediaOwner
	}
	seen := map[uuid.UUID]bool{}
	for _, set := range clipSets {
		for _, clip := range set {
			if seen[clip.MediaAssetID] {
				continue
			}
			seen[clip.MediaAssetID] = true
			if err := assertOwner(ctx, clip.MediaAssetID, actorID); err != nil {
				if errors.Is(err, ErrNotMediaOwner) {
					return err
				}
				// A missing or unreadable asset is not an ownership
				// answer, but it is not a clip this actor may write
				// either. Fail closed.
				return fmt.Errorf("%w: clip media %s is not readable", ErrNotMediaOwner, clip.MediaAssetID)
			}
		}
	}
	return nil
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

// ErrNotMediaOwner is returned when an actor tries to write captions for
// media they do not own. Handlers map it to 403.
var ErrNotMediaOwner = errors.New("forbidden: you do not own this media")

// AssertMediaOwner is the single authorization gate for every caption
// write path (Module 1 fixes-v1 / Codex P0-3). The vulnerability was that
// `POST /v1/subtitles/:mediaId` and `/auto` authenticated the caller but
// never checked ownership, so any authenticated user could overwrite
// another creator's canonical caption track (the store upserts on
// (media_asset_id, language)). Enforcement lives HERE, at the service
// boundary, so no handler can forget it.
func (s *Service) AssertMediaOwner(ctx context.Context, mediaID, actorID uuid.UUID) error {
	if actorID == uuid.Nil {
		return ErrNotMediaOwner
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return err
	}
	if media == nil || media.UploaderID != actorID {
		return ErrNotMediaOwner
	}
	return nil
}

// CreateSubtitle upserts a subtitle track for a media asset. Owner-only.
//
// content_url is DERIVED here and whatever the caller put in it is
// discarded. It used to be stored verbatim from the request body, which
// made the column an arbitrary-URL sink: the value's whole purpose is to
// become a `<track src>`, so a stored `javascript:…` or a URL on a host
// this service does not serve would have been fetched, or executed, by
// every viewer's browser on the uploader's say-so. There is exactly one
// legitimate value — this service's own rendering of the stored cues —
// so the service writes it and no allowlist is needed.
func (s *Service) CreateSubtitle(ctx context.Context, actorID uuid.UUID, sub *postgres.MediaSubtitle) (*postgres.MediaSubtitle, error) {
	if err := s.AssertMediaOwner(ctx, sub.MediaAssetID, actorID); err != nil {
		return nil, err
	}
	if !validLanguageTag(sub.Language) {
		return nil, fmt.Errorf("%w: language must be a BCP-47-like tag (e.g. en, hi, en-IN)", ErrInvalidCaption)
	}
	if len(sub.Content) > maxCaptionContentBytes {
		return nil, fmt.Errorf("%w: transcript exceeds %d characters",
			ErrInvalidCaption, maxCaptionContentBytes)
	}
	// Normalize to the schema's enum. 'auto' is not an accepted value —
	// writing it violated the CHECK constraint and every configured
	// transcription failed at the database (Codex P0-2 evidence).
	sub.Source = normalizeSubtitleSource(sub.Source)
	sub.ContentURL = SubtitleTrackPath(sub.MediaAssetID, sub.Language)
	return s.pgStore.CreateSubtitle(ctx, sub)
}

// normalizeSubtitleSource maps caller/legacy spellings onto the
// media_subtitles CHECK set (auto_generated | manual | translated).
func normalizeSubtitleSource(source string) string {
	switch source {
	case "auto", "auto_generated", "":
		return "auto_generated"
	case "manual", "translated":
		return source
	default:
		// Unknown values would otherwise fail the constraint at insert
		// time; treat an unrecognized source as a manual upload.
		return "manual"
	}
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
// ProviderTranscript is PROVIDER-GENERATED transcription evidence.
//
// Module 1 fixes-v3 / LB-2 requirement 5: this is deliberately a distinct
// type from the stored display caption. The stored row can be overwritten
// by the owner (`edited_by_owner`), and `CreateSubtitle` suppresses the
// upsert in that case and returns the EXISTING owner-authored row. Feeding
// that row back to the safety evaluator would reintroduce exactly the
// bypass LB-2 closes. Safety therefore consumes this value, which never
// passes through the display-caption merge.
type ProviderTranscript struct {
	Text       string
	Language   string
	Confidence float64
	Backend    string
	// Placeholder marks a stub/no-op backend result — never real evidence.
	Placeholder bool
}

// GenerateAutoCaptionsWithEvidence runs the backend and returns both the
// stored display caption and the untouched provider transcript.
func (s *Service) GenerateAutoCaptionsWithEvidence(ctx context.Context, mediaID uuid.UUID, language string) (*postgres.MediaSubtitle, *ProviderTranscript, error) {
	return s.generateAutoCaptions(ctx, mediaID, language)
}

// GenerateAutoCaptions preserves the original signature for the HTTP
// caller, which only needs the display caption.
func (s *Service) GenerateAutoCaptions(ctx context.Context, mediaID uuid.UUID, language string) (*postgres.MediaSubtitle, error) {
	sub, _, err := s.generateAutoCaptions(ctx, mediaID, language)
	return sub, err
}

func (s *Service) generateAutoCaptions(ctx context.Context, mediaID uuid.UUID, language string) (*postgres.MediaSubtitle, *ProviderTranscript, error) {
	if s.captions == nil {
		return nil, nil, fmt.Errorf("CAPTIONS_BACKEND_UNCONFIGURED")
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, nil, fmt.Errorf("media not found: %w", err)
	}
	// Module 1 P0-6/P0-9: audio (voice posts) transcribes through the same
	// path as video — the backend consumes a presigned URL either way.
	if media.FileType != "video" && media.FileType != "audio" {
		return nil, nil, fmt.Errorf("auto-captions only supported for video and audio media")
	}

	// Use a presigned GET URL so the backend can fetch directly from
	// blob storage (Whisper needs the raw audio file). Short expiry —
	// transcription rarely takes more than a minute or two.
	signed, err := s.blobStore.GeneratePresignedGetURL(ctx, media.StorageKey, 30*time.Minute)
	if err != nil {
		return nil, nil, fmt.Errorf("sign audio url: %w", err)
	}

	res, err := s.captions.Transcribe(ctx, signed.String(), language)
	if err != nil {
		slog.Error("auto-captions: backend failed",
			"backend", s.captions.Name(), "media_id", mediaID, "error", err)
		return nil, nil, fmt.Errorf("transcribe: %w", err)
	}

	// Module 1 P0-9: a placeholder result means no real backend is wired.
	// Storing it as a subtitle row would present "captions unavailable"
	// as a finished caption. Return nils so callers report the honest
	// "unavailable" state instead.
	if res.IsPlaceholder {
		slog.Info("auto-captions: backend is a placeholder; no subtitle row stored",
			"backend", s.captions.Name(), "media_id", mediaID)
		return nil, nil, nil
	}

	// Module 1 fixes-v3 / LB-2: capture the provider transcript BEFORE it
	// is written to (and potentially suppressed by) the display-caption
	// row. This value is the only thing safety evaluation may consume.
	evidence := &ProviderTranscript{
		Text:        res.Text,
		Language:    res.Language,
		Confidence:  float64(res.Confidence),
		Backend:     s.captions.Name(),
		Placeholder: res.IsPlaceholder,
	}

	var wordsJSON []byte
	if len(res.Words) > 0 {
		wordsJSON, _ = json.Marshal(res.Words)
	}
	conf := res.Confidence
	sub := &postgres.MediaSubtitle{
		MediaAssetID: mediaID,
		Language:     res.Language,
		// Schema enum, not 'auto' — see normalizeSubtitleSource.
		Source: "auto_generated",
		Format: res.Format,
		// The service's own track route, rendered from these rows on
		// demand. Never a caller-supplied URL — see CreateSubtitle.
		ContentURL:    SubtitleTrackPath(mediaID, res.Language),
		Content:       res.Text,
		WordLevelJSON: wordsJSON,
		Confidence:    &conf,
	}
	saved, err := s.pgStore.CreateSubtitle(ctx, sub)
	if err != nil {
		return nil, nil, fmt.Errorf("save subtitle: %w", err)
	}
	slog.Info("auto-captions: stored",
		"backend", s.captions.Name(),
		"media_id", mediaID,
		"language", res.Language,
		"placeholder", res.IsPlaceholder,
		"word_count", len(res.Words))
	return saved, evidence, nil
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
