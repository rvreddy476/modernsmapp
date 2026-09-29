package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Original sounds on reels (2026-09-29): ONE sound per source video.
//
// A sound is the audio of a short video, extracted once and played by every
// reel that uses it. Two routes ensure it exists and both come through here:
//
//	POST /v1/media/internal/:mediaId/sound   post-service, "use this sound"
//	POST /v1/audio/extract/:mediaId          the uploader, for their own video
//
// WHAT WAS WRONG WITH THE PATH THIS REPLACES
//
// ExtractAudioFromMedia looked for a row and, finding none, extracted and
// inserted. Two callers could both find none: two rows for one video, and
// post-service linking reels to either. It read the ORIGINAL upload whole
// into memory (up to 2 GB) inside the server process, and it took a video of
// any length.
//
// THE RULES
//
//   - One row per source, decided by the database: insert ON CONFLICT DO
//     NOTHING against the unique index of migration 024, then re-read. A
//     caller that lost the race answers with the winner's row.
//   - Nothing is downloaded until the asset has been accepted: a video, ready,
//     not rejected, no longer than MaxSoundSourceMs, and not one of the
//     uploader-only scopes.
//   - The source is the SMALLEST mp4 rendition, streamed to a temp file; the
//     original is the fallback of an asset with no ladder.
//   - A row that exists is returned as it is. Its title, artist and creator
//     are never rewritten; a source post or creator it does not have yet is
//     filled in.

// MaxSoundSourceMs is the longest video a sound is taken from: short-form
// only (contract section 0).
const MaxSoundSourceMs = 300_000

// DefaultSoundTitle names a sound whose caller gave it no title.
const DefaultSoundTitle = "Original sound"

const (
	maxSoundTitleRunes  = 120
	maxSoundArtistRunes = 80
	// soundExtractTimeout bounds one extraction. The work is detached from
	// the request: a caller that gives up must not leave the next one to
	// start again from nothing.
	soundExtractTimeout = 2 * time.Minute
)

var (
	// ErrSoundNotAVideo: the asset is not a video.
	ErrSoundNotAVideo = errors.New("sound source is not a video")
	// ErrSoundTooLong: the video is longer than MaxSoundSourceMs.
	ErrSoundTooLong = errors.New("sound source is longer than 300 s")
	// ErrSoundNotReady: the video is not processed, is rejected, or its
	// existing sound is not playable.
	ErrSoundNotReady = errors.New("sound source is not ready")
	// ErrSoundNoAudio: the video carries no audio stream.
	ErrSoundNoAudio = errors.New("sound source has no audio")
	// ErrSoundUnavailable: storage, ffmpeg or the store failed. Retryable;
	// the cause is logged and never answered.
	ErrSoundUnavailable = errors.New("sound could not be produced")
)

// ExtractedSound is what an extraction yields.
type ExtractedSound struct {
	// Audio is AAC in an M4A container.
	Audio []byte
	// Waveform is the JSON peaks, nil when none could be drawn.
	Waveform   []byte
	DurationMs int
	SampleRate int
}

// SoundExtractor turns a video into its sound. An interface so the ensure
// path is tested without ffmpeg.
type SoundExtractor interface {
	ExtractSound(ctx context.Context, video io.Reader) (*ExtractedSound, error)
}

// soundStore is the store slice the ensure path needs.
type soundStore interface {
	GetMediaWithVariants(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
	GetAudioTrackByMedia(ctx context.Context, mediaID uuid.UUID) (*postgres.AudioTrack, error)
	InsertSoundIfAbsent(ctx context.Context, a *postgres.AudioTrack) (bool, error)
	FillSoundOrigin(ctx context.Context, id uuid.UUID, sourcePostID, creatorUserID *uuid.UUID) error
}

// soundBlobs is the object store slice: a streamed read and a write.
type soundBlobs interface {
	OpenObject(ctx context.Context, objectKey string) (io.ReadCloser, blob.ObjectInfo, error)
	UploadObject(ctx context.Context, objectKey string, data []byte, contentType string) error
}

// Sounds ensures the one sound of a video.
type Sounds struct {
	store     soundStore
	blobs     soundBlobs
	extractor SoundExtractor
}

// NewSounds builds the ensure path over explicit dependencies. The handler
// tests use it with fakes; production goes through Service.Sounds.
func NewSounds(store soundStore, blobs soundBlobs, extractor SoundExtractor) *Sounds {
	return &Sounds{store: store, blobs: blobs, extractor: extractor}
}

// Sounds exposes the ensure path over the service's own store and blob
// store, extracting with ffmpeg in this process.
func (s *Service) Sounds() *Sounds {
	return NewSounds(s.pgStore, s.blobStore, FFmpegSoundExtractor{})
}

// EnsureSoundInput names the video and what to call its sound.
type EnsureSoundInput struct {
	MediaID uuid.UUID
	Title   string
	Artist  string
	// SourcePostID is the reel the sound is taken from; nil when the caller
	// knows of none.
	SourcePostID *uuid.UUID
	// CreatorUserID is that reel's author; nil means the video's uploader.
	CreatorUserID *uuid.UUID
}

// Ensure is POST /v1/media/internal/:mediaId/sound: the sound of the video,
// extracted now if it has none. The caller holds the internal key and has
// made its own decision about who is asking.
func (s *Sounds) Ensure(ctx context.Context, in EnsureSoundInput) (*postgres.AudioTrack, error) {
	media, err := s.loadSource(ctx, in.MediaID)
	if err != nil {
		return nil, err
	}
	return s.ensure(ctx, media, in)
}

// EnsureForOwner is POST /v1/audio/extract/:mediaId: the same, for the
// video's uploader and for nobody else.
func (s *Sounds) EnsureForOwner(ctx context.Context, ownerID uuid.UUID, in EnsureSoundInput) (*postgres.AudioTrack, error) {
	media, err := s.loadSource(ctx, in.MediaID)
	if err != nil {
		return nil, err
	}
	if ownerID == uuid.Nil || media.UploaderID != ownerID {
		return nil, ErrNotMediaOwner
	}
	return s.ensure(ctx, media, in)
}

func (s *Sounds) loadSource(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	if s == nil || s.store == nil || s.blobs == nil || s.extractor == nil {
		return nil, fmt.Errorf("%w: sounds not configured", ErrSoundUnavailable)
	}
	media, err := s.store.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAssetNotFound
		}
		return nil, fmt.Errorf("%w: load media: %v", ErrSoundUnavailable, err)
	}
	if media == nil {
		return nil, ErrAssetNotFound
	}
	return media, nil
}

// soundSourceRefusal is every reason an asset is not a sound source, decided
// from the row alone. Nothing has been downloaded when it answers.
func soundSourceRefusal(media *postgres.MediaAsset) error {
	// A dating photo and an anonymous attachment are their uploader's alone,
	// and a sound names its creator: there is no sound of either.
	if media.AccessScope == postgres.AccessScopeDatingPhoto || media.AccessScope == postgres.AccessScopeAnonymous {
		return ErrAssetNotFound
	}
	if media.FileType != "video" {
		return ErrSoundNotAVideo
	}
	if media.ProcessingStatus != "ready" || media.ModerationStatus == "rejected" {
		return ErrSoundNotReady
	}
	duration := media.DurationMsValue()
	if duration <= 0 {
		// A ready video always has a measured duration. One without cannot
		// be shown to be short, so it is not taken on trust.
		return ErrSoundNotReady
	}
	if duration > MaxSoundSourceMs {
		return ErrSoundTooLong
	}
	return nil
}

// soundSourceKey is the object the sound is extracted from: the smallest
// rung of the mp4 ladder, or the original when the asset has no ladder. A
// dub (dub_<lang>_<rung>) carries another language's audio and is never it.
func soundSourceKey(media *postgres.MediaAsset) string {
	key, rank := "", 0
	for _, v := range media.Variants {
		if !videoRungPattern.MatchString(v.Name) || v.ObjectKey == "" {
			continue
		}
		if v.Mime != "" && v.Mime != "video/mp4" {
			continue
		}
		if r := rungRank(v.Name); key == "" || r < rank {
			key, rank = v.ObjectKey, r
		}
	}
	if key == "" {
		return media.StorageKey
	}
	return key
}

func clampRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return strings.TrimSpace(string(r[:max]))
	}
	return s
}

func (s *Sounds) ensure(ctx context.Context, media *postgres.MediaAsset, in EnsureSoundInput) (*postgres.AudioTrack, error) {
	if err := soundSourceRefusal(media); err != nil {
		return nil, err
	}
	creator := in.CreatorUserID
	if creator == nil || *creator == uuid.Nil {
		uploader := media.UploaderID
		creator = &uploader
	}
	sourcePost := in.SourcePostID
	if sourcePost != nil && *sourcePost == uuid.Nil {
		sourcePost = nil
	}

	existing, err := s.existing(ctx, media.ID, sourcePost, creator)
	if err != nil || existing != nil {
		return existing, err
	}

	// Detached from the request on purpose (soundExtractTimeout).
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), soundExtractTimeout)
	defer cancel()

	extracted, err := s.extract(work, media)
	if err != nil {
		return nil, err
	}

	audioKey := fmt.Sprintf("audio/%s/%s/audio.m4a", media.UploaderID, media.ID)
	if err := s.blobs.UploadObject(work, audioKey, extracted.Audio, "audio/mp4"); err != nil {
		return nil, fmt.Errorf("%w: upload audio: %v", ErrSoundUnavailable, err)
	}
	// The waveform is a decoration: a sound without one still plays.
	var waveformKey *string
	if len(extracted.Waveform) > 0 {
		key := fmt.Sprintf("audio/%s/%s/waveform.json", media.UploaderID, media.ID)
		if err := s.blobs.UploadObject(work, key, extracted.Waveform, "application/json"); err != nil {
			slog.Warn("sound: waveform not stored", "media_id", media.ID, "error", err)
		} else {
			waveformKey = &key
		}
	}

	title := clampRunes(in.Title, maxSoundTitleRunes)
	if title == "" {
		title = DefaultSoundTitle
	}
	var sampleRate *int
	if extracted.SampleRate > 0 {
		sampleRate = &extracted.SampleRate
	}
	durationMs := extracted.DurationMs
	if durationMs <= 0 {
		// The probe of the extracted file failed; the video's own measured
		// duration is the sound's.
		durationMs = media.DurationMsValue()
	}
	mediaID := media.ID
	track := &postgres.AudioTrack{
		SourceMediaID: &mediaID,
		SourceReelID:  sourcePost,
		CreatorUserID: creator,
		Title:         title,
		Artist:        clampRunes(in.Artist, maxSoundArtistRunes),
		AudioKey:      audioKey,
		WaveformKey:   waveformKey,
		DurationMs:    durationMs,
		SampleRate:    sampleRate,
		Status:        "ready",
		IsOriginal:    true,
		LicenseType:   "standard",
	}
	inserted, err := s.store.InsertSoundIfAbsent(work, track)
	if err != nil {
		return nil, fmt.Errorf("%w: insert sound: %v", ErrSoundUnavailable, err)
	}

	// Re-read whoever's row the index kept: this call's, or the row of a
	// caller that was extracting the same video at the same time.
	kept, err := s.existing(work, media.ID, sourcePost, creator)
	if err != nil {
		return nil, err
	}
	if kept == nil {
		return nil, fmt.Errorf("%w: the sound of %s is not readable after its insert", ErrSoundUnavailable, media.ID)
	}
	if inserted {
		slog.Info("sound extracted", "media_id", media.ID, "audio_track_id", kept.ID, "duration_ms", kept.DurationMs)
	}
	return kept, nil
}

// existing returns the sound the video already has, or (nil, nil) when it
// has none. The row is answered as it stands; only a source post or creator
// it lacks is filled in.
func (s *Sounds) existing(ctx context.Context, mediaID uuid.UUID, sourcePost, creator *uuid.UUID) (*postgres.AudioTrack, error) {
	track, err := s.store.GetAudioTrackByMedia(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: load sound: %v", ErrSoundUnavailable, err)
	}
	if track == nil {
		return nil, nil
	}
	// One row per source: a sound that was rejected or deleted, or never got
	// its audio, is not replaced by a second one.
	if track.Status != "ready" || track.AudioKey == "" {
		return nil, ErrSoundNotReady
	}
	fillPost := track.SourceReelID == nil && sourcePost != nil
	fillCreator := track.CreatorUserID == nil && creator != nil
	if fillPost || fillCreator {
		if err := s.store.FillSoundOrigin(ctx, track.ID, sourcePost, creator); err != nil {
			return nil, fmt.Errorf("%w: record sound origin: %v", ErrSoundUnavailable, err)
		}
		if fillPost {
			track.SourceReelID = sourcePost
		}
		if fillCreator {
			track.CreatorUserID = creator
		}
	}
	return track, nil
}

func (s *Sounds) extract(ctx context.Context, media *postgres.MediaAsset) (*ExtractedSound, error) {
	video, _, err := s.blobs.OpenObject(ctx, soundSourceKey(media))
	if err != nil {
		return nil, fmt.Errorf("%w: open source video: %v", ErrSoundUnavailable, err)
	}
	defer video.Close()

	extracted, err := s.extractor.ExtractSound(ctx, video)
	if err != nil {
		if errors.Is(err, ErrSoundNoAudio) {
			return nil, ErrSoundNoAudio
		}
		return nil, fmt.Errorf("%w: extract: %v", ErrSoundUnavailable, err)
	}
	if extracted == nil || len(extracted.Audio) == 0 {
		return nil, fmt.Errorf("%w: extraction produced no audio", ErrSoundUnavailable)
	}
	return extracted, nil
}

// FFmpegSoundExtractor extracts with the ffmpeg of this process's image. The
// video is streamed to a temp file, never held in memory; what is read back
// is the audio alone, at most MaxSoundSourceMs of 128 kbps AAC (about 5 MB).
type FFmpegSoundExtractor struct{}

func (FFmpegSoundExtractor) ExtractSound(ctx context.Context, video io.Reader) (*ExtractedSound, error) {
	tmpDir, err := os.MkdirTemp("", "sound-extract-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPath := filepath.Join(tmpDir, "input")
	input, err := os.Create(inputPath)
	if err != nil {
		return nil, fmt.Errorf("create temp input: %w", err)
	}
	_, copyErr := io.Copy(input, video)
	if closeErr := input.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return nil, fmt.Errorf("download video: %w", copyErr)
	}

	// A video with no audio stream is not a fault to retry: ffprobe answers
	// with no stream at all, and ffmpeg would only fail on it.
	if probed, probeErr := processing.ProbeAudio(ctx, inputPath); probeErr == nil && probed.Codec == "" {
		return nil, ErrSoundNoAudio
	}

	audioPath, meta, err := processing.ExtractAudio(ctx, inputPath, tmpDir)
	if err != nil {
		return nil, err
	}
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		return nil, fmt.Errorf("read extracted audio: %w", err)
	}
	out := &ExtractedSound{Audio: audio}
	if meta != nil {
		out.DurationMs, out.SampleRate = meta.DurationMs, meta.SampleRate
	}
	if waveformPath, wfErr := processing.GenerateWaveform(ctx, audioPath, tmpDir, 200); wfErr == nil {
		if waveform, readErr := os.ReadFile(waveformPath); readErr == nil {
			out.Waveform = waveform
		}
	}
	return out, nil
}
