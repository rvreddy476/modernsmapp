package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/dubbing"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Alternate audio tracks for short videos (2026-09-27).
//
// A creator uploads a language-labelled audio file for a video they own, or
// asks for one to be generated; a DB-polled worker muxes it into every video
// rung as a media_variants row named dub_<lang>_<rung>, stored under the
// video's own key prefix. Viewers list the tracks and open
// /v1/media/<id>/serve/dub_<lang>_<rung> — the existing serve route and its
// gate, unchanged.

// Errors the handler maps onto the contract's status codes.
var (
	ErrAudioTrackMediaNotReady = errors.New("video is not ready")                      // 409 MEDIA_NOT_READY
	ErrAudioDurationMismatch   = errors.New("audio duration does not match the video") // 422 AUDIO_DURATION_MISMATCH
	ErrAudioTrackLimit         = errors.New("audio track limit reached")               // 422 AUDIO_TRACK_LIMIT
	ErrDubbingUnavailable      = errors.New("no dubbing backend is configured")        // 503 DUBBING_UNAVAILABLE
	ErrAudioTrackInvalid       = errors.New("invalid audio track")                     // 400 INVALID_AUDIO_TRACK
	ErrAudioTrackMediaNotFound = errors.New("media not found")                         // 404 NOT_FOUND
)

const (
	// MaxAudioTracksPerAsset caps language tracks on one video.
	MaxAudioTracksPerAsset = 10
	// MaxAudioTrackUploadBytes bounds one uploaded track.
	MaxAudioTrackUploadBytes int64 = 50 * 1024 * 1024
	// maxAudioTrackLabel bounds the display label.
	maxAudioTrackLabel = 60

	audioTrackWorkerTick     = 10 * time.Second
	audioTrackWorkerBatch    = 3
	audioTrackMaxAttempts    = 4
	audioTrackClaimStaleTime = 30 * time.Minute
)

// audioTrackMimeAllowed is the voice-upload set plus the common container
// spellings browsers and editors emit.
var audioTrackMimeAllowed = map[string]bool{
	"audio/mp4": true, "audio/m4a": true, "audio/x-m4a": true, "audio/aac": true,
	"audio/mpeg": true, "audio/mp3": true,
	"audio/ogg": true, "audio/opus": true,
	"audio/wav": true, "audio/x-wav": true, "audio/wave": true, "audio/vnd.wave": true,
	"audio/webm": true,
	"audio/flac": true, "audio/x-flac": true,
	"audio/amr": true,
}

// AudioTrackStore is the store slice the feature uses. *postgres.MediaAssetStore
// implements it; the handler tests substitute a fake.
type AudioTrackStore interface {
	GetMediaWithVariants(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
	InsertVariants(ctx context.Context, variants []postgres.MediaVariant) error
	ReplaceMediaAudioTrack(ctx context.Context, t *postgres.MediaAudioTrack) ([]string, error)
	ListMediaAudioTracks(ctx context.Context, mediaID uuid.UUID) ([]postgres.MediaAudioTrack, error)
	CountMediaAudioTracks(ctx context.Context, mediaID uuid.UUID) (int, error)
	GetMediaAudioTrack(ctx context.Context, mediaID, trackID uuid.UUID) (*postgres.MediaAudioTrack, error)
	DeleteMediaAudioTrack(ctx context.Context, mediaID, trackID uuid.UUID) ([]string, error)
	ClaimMediaAudioTracks(ctx context.Context, staleAfter time.Duration, limit int) ([]postgres.MediaAudioTrack, error)
	SetMediaAudioTrackSourceKey(ctx context.Context, trackID uuid.UUID, key string, token *string) error
	CompleteMediaAudioTrack(ctx context.Context, trackID uuid.UUID, rungs []string, token *string) error
	FailMediaAudioTrack(ctx context.Context, trackID uuid.UUID, reason string, token *string) error
	ReleaseMediaAudioTrack(ctx context.Context, trackID uuid.UUID, reason string, token *string) error
	RequeueMediaAudioTrack(ctx context.Context, trackID uuid.UUID) error
}

// AudioTrackBlobs is the object-store slice. *blob.Store implements it.
type AudioTrackBlobs interface {
	DownloadObject(ctx context.Context, objectKey string) ([]byte, error)
	UploadObject(ctx context.Context, objectKey string, data []byte, contentType string) error
	DeleteObject(ctx context.Context, objectKey string) error
}

// AudioTracks is the feature. Every ffmpeg step is a field so it can be
// replaced in tests (the test machines have no ffmpeg).
type AudioTracks struct {
	store         AudioTrackStore
	blobs         AudioTrackBlobs
	authorizeRead func(ctx context.Context, viewerID, mediaID uuid.UUID) error
	dubber        dubbing.Dubber

	probeAudio   func(ctx context.Context, path string) (*processing.AudioMeta, error)
	mux          func(ctx context.Context, videoPath, audioPath, outPath string) error
	extractAudio func(ctx context.Context, videoPath, outDir string) (string, error)
	log          *slog.Logger
}

// NewAudioTracks wires the feature. authorizeRead is the same gate the serve
// routes apply (Service.AuthorizeMediaRead); dubber may be nil.
func NewAudioTracks(store AudioTrackStore, blobs AudioTrackBlobs,
	authorizeRead func(ctx context.Context, viewerID, mediaID uuid.UUID) error, dubber dubbing.Dubber) *AudioTracks {
	return &AudioTracks{
		store:         store,
		blobs:         blobs,
		authorizeRead: authorizeRead,
		dubber:        dubber,
		probeAudio:    processing.ProbeAudio,
		mux:           processing.MuxAudio,
		extractAudio: func(ctx context.Context, videoPath, outDir string) (string, error) {
			path, _, err := processing.ExtractAudio(ctx, videoPath, outDir)
			return path, err
		},
		log: slog.Default(),
	}
}

// WithDubber sets (or clears) the generation backend.
func (a *AudioTracks) WithDubber(d dubbing.Dubber) *AudioTracks { a.dubber = d; return a }

// WithAudioProbe replaces the ffprobe step (tests).
func (a *AudioTracks) WithAudioProbe(fn func(ctx context.Context, path string) (*processing.AudioMeta, error)) *AudioTracks {
	a.probeAudio = fn
	return a
}

// WithMux replaces the ffmpeg mux step (tests).
func (a *AudioTracks) WithMux(fn func(ctx context.Context, videoPath, audioPath, outPath string) error) *AudioTracks {
	a.mux = fn
	return a
}

// WithAudioExtractor replaces the ffmpeg audio extraction step (tests).
func (a *AudioTracks) WithAudioExtractor(fn func(ctx context.Context, videoPath, outDir string) (string, error)) *AudioTracks {
	a.extractAudio = fn
	return a
}

// DubbingConfigured reports whether generate requests can be served.
func (a *AudioTracks) DubbingConfigured() bool { return a != nil && a.dubber != nil }

// AudioTrackView is the client-facing track.
type AudioTrackView struct {
	ID       string `json:"id"`
	Language string `json:"language"`
	Label    string `json:"label"`
	Source   string `json:"source"` // original | uploaded | generated
	Status   string `json:"status"` // pending | processing | ready | failed
	// PlaybackURL is the best muxed rung's serve path; ready tracks only.
	PlaybackURL string   `json:"playback_url,omitempty"`
	Rungs       []string `json:"rungs,omitempty"`
	Error       string   `json:"error,omitempty"`
	CreatedAt   *string  `json:"created_at,omitempty"`
}

// AudioTrackUpload is one uploaded file.
type AudioTrackUpload struct {
	Data     []byte
	Mime     string
	Language string
	Label    string
}

// ── keys and names ──────────────────────────────────────────────────────

// audioTrackSourceKey: user/<uid>/<mid>/dub/<lang>/source
func audioTrackSourceKey(uploaderID, mediaID uuid.UUID, language string) string {
	return fmt.Sprintf("user/%s/%s/dub/%s/source", uploaderID, mediaID, language)
}

// audioTrackRungKey: user/<uid>/<mid>/dub/<lang>/<rung>
func audioTrackRungKey(uploaderID, mediaID uuid.UUID, language, rung string) string {
	return fmt.Sprintf("user/%s/%s/dub/%s/%s", uploaderID, mediaID, language, rung)
}

// DubVariantName: dub_<lang>_<rung>, e.g. dub_pt-br_720p.
func DubVariantName(language, rung string) string {
	return postgres.DubVariantPrefix(language) + rung
}

// normalizeAudioLanguage validates and lowercases a tag.
func normalizeAudioLanguage(tag string) (string, error) {
	tag = strings.TrimSpace(tag)
	if !validLanguageTag(tag) {
		return "", fmt.Errorf("%w: language must be a BCP-47-like tag (e.g. en, hi, pt-BR)", ErrAudioTrackInvalid)
	}
	return strings.ToLower(tag), nil
}

// languageDisplayNames backs the default label.
var languageDisplayNames = map[string]string{
	"en": "English", "hi": "Hindi", "ta": "Tamil", "te": "Telugu", "kn": "Kannada", "ml": "Malayalam",
	"mr": "Marathi", "bn": "Bengali", "gu": "Gujarati", "pa": "Punjabi", "ur": "Urdu", "or": "Odia",
	"as": "Assamese", "ne": "Nepali", "si": "Sinhala",
	"es": "Spanish", "fr": "French", "de": "German", "it": "Italian", "pt": "Portuguese", "pt-br": "Portuguese (Brazil)",
	"ru": "Russian", "ja": "Japanese", "ko": "Korean", "zh": "Chinese", "zh-cn": "Chinese (Simplified)",
	"zh-tw": "Chinese (Traditional)", "ar": "Arabic", "tr": "Turkish", "id": "Indonesian", "ms": "Malay",
	"th": "Thai", "vi": "Vietnamese", "nl": "Dutch", "sv": "Swedish", "pl": "Polish", "uk": "Ukrainian",
	"fa": "Persian", "sw": "Swahili", "fil": "Filipino", "en-in": "English (India)", "en-us": "English (US)",
	"en-gb": "English (UK)",
}

// LanguageLabel is the default label for a tag: its display name if known,
// else the tag itself.
func LanguageLabel(tag string) string {
	if name, ok := languageDisplayNames[strings.ToLower(tag)]; ok {
		return name
	}
	return tag
}

func normalizeAudioLabel(label, language string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return LanguageLabel(language), nil
	}
	if len([]rune(label)) > maxAudioTrackLabel {
		return "", fmt.Errorf("%w: label exceeds %d characters", ErrAudioTrackInvalid, maxAudioTrackLabel)
	}
	return label, nil
}

// videoRungPattern matches the transcode ladder's variant names (360p …).
var videoRungPattern = regexp.MustCompile(`^\d{3,4}p$`)

// rungRank orders rungs best-first: numeric height descending, original last.
func rungRank(r string) int {
	if r == "original" {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(r, "p"))
	return n
}

func sortRungsBestFirst(r []string) {
	sort.SliceStable(r, func(i, j int) bool { return rungRank(r[i]) > rungRank(r[j]) })
}

// ── owner-gated loads ───────────────────────────────────────────────────

// loadOwnedVideo is the write-side gate: the caller must be the uploader.
// A missing asset is ErrAudioTrackMediaNotFound; the wrong caller is
// ErrNotMediaOwner. Neither reveals more than the other endpoints do.
func (a *AudioTracks) loadOwnedVideo(ctx context.Context, callerID, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	if callerID == uuid.Nil {
		return nil, ErrNotMediaOwner
	}
	media, err := a.store.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAudioTrackMediaNotFound
		}
		return nil, err
	}
	if media == nil {
		return nil, ErrAudioTrackMediaNotFound
	}
	if media.UploaderID != callerID {
		return nil, ErrNotMediaOwner
	}
	return media, nil
}

// requireReadyVideo: tracks only attach to a transcoded video.
func requireReadyVideo(media *postgres.MediaAsset) error {
	if media.FileType != "video" {
		return fmt.Errorf("%w: audio tracks are only available for videos", ErrAudioTrackInvalid)
	}
	if media.ProcessingStatus != "ready" {
		return ErrAudioTrackMediaNotReady
	}
	return nil
}

// enforceTrackLimit: at most MaxAudioTracksPerAsset languages; a replacement
// of an existing language does not count as a new one.
func (a *AudioTracks) enforceTrackLimit(ctx context.Context, mediaID uuid.UUID, language string) error {
	existing, err := a.store.ListMediaAudioTracks(ctx, mediaID)
	if err != nil {
		return err
	}
	n := 0
	for _, t := range existing {
		if t.Language != language {
			n++
		}
	}
	if n >= MaxAudioTracksPerAsset {
		return ErrAudioTrackLimit
	}
	return nil
}

// ── read ────────────────────────────────────────────────────────────────

// List returns the original entry followed by every track, oldest first.
// Same gate as /serve: a viewer who may not have the video gets the same
// not-found answer the serve route gives.
func (a *AudioTracks) List(ctx context.Context, viewerID, mediaID uuid.UUID) ([]AudioTrackView, error) {
	if err := a.authorizeRead(ctx, viewerID, mediaID); err != nil {
		return nil, err
	}
	media, err := a.store.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAudioTrackMediaNotFound
		}
		return nil, err
	}
	tracks, err := a.store.ListMediaAudioTracks(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	out := make([]AudioTrackView, 0, len(tracks)+1)
	out = append(out, AudioTrackView{
		ID: "original", Language: "und", Label: "Original", Source: "original", Status: "ready",
	})
	for i := range tracks {
		out = append(out, a.view(ctx, media, &tracks[i]))
	}
	return out, nil
}

// view renders one track. Rungs are read from the LIVE media_variants rows
// rather than the stored column: a transcode re-run prunes every variant it
// did not produce, dub ones included, and a "ready" track with no outputs
// is re-queued rather than reported as playable.
func (a *AudioTracks) view(ctx context.Context, media *postgres.MediaAsset, t *postgres.MediaAudioTrack) AudioTrackView {
	v := AudioTrackView{
		ID:       t.ID.String(),
		Language: t.Language,
		Label:    t.Label,
		Source:   t.Source,
		Status:   t.Status,
	}
	created := t.CreatedAt.UTC().Format(time.RFC3339)
	v.CreatedAt = &created
	switch t.Status {
	case "ready":
		rungs := liveDubRungs(media, t.Language)
		if len(rungs) == 0 {
			if err := a.store.RequeueMediaAudioTrack(ctx, t.ID); err != nil {
				a.log.Warn("audio tracks: requeue after variant prune failed", "track_id", t.ID, "error", err)
			}
			v.Status = "pending"
			return v
		}
		v.Rungs = rungs
		v.PlaybackURL = fmt.Sprintf("/v1/media/%s/serve/%s", media.ID, DubVariantName(t.Language, rungs[0]))
	case "failed":
		if t.LastError != nil {
			v.Error = *t.LastError
		}
	}
	return v
}

// liveDubRungs lists the rungs whose dub_<lang>_<rung> variant row exists,
// best first.
func liveDubRungs(media *postgres.MediaAsset, language string) []string {
	prefix := postgres.DubVariantPrefix(language)
	var rungs []string
	for _, v := range media.Variants {
		if strings.HasPrefix(v.Name, prefix) {
			rungs = append(rungs, strings.TrimPrefix(v.Name, prefix))
		}
	}
	sortRungsBestFirst(rungs)
	return rungs
}

// ── write ───────────────────────────────────────────────────────────────

// Upload validates and stores a creator-supplied track, replacing any
// previous track for the same language, and queues the mux.
func (a *AudioTracks) Upload(ctx context.Context, callerID, mediaID uuid.UUID, in AudioTrackUpload) (*AudioTrackView, error) {
	media, err := a.loadOwnedVideo(ctx, callerID, mediaID)
	if err != nil {
		return nil, err
	}
	if err := requireReadyVideo(media); err != nil {
		return nil, err
	}
	language, err := normalizeAudioLanguage(in.Language)
	if err != nil {
		return nil, err
	}
	label, err := normalizeAudioLabel(in.Label, language)
	if err != nil {
		return nil, err
	}
	if len(in.Data) == 0 {
		return nil, fmt.Errorf("%w: audio file is empty", ErrAudioTrackInvalid)
	}
	if int64(len(in.Data)) > MaxAudioTrackUploadBytes {
		return nil, fmt.Errorf("%w: audio file exceeds %d bytes", ErrAudioTrackInvalid, MaxAudioTrackUploadBytes)
	}
	mime := strings.ToLower(strings.TrimSpace(strings.Split(in.Mime, ";")[0]))
	if !audioTrackMimeAllowed[mime] {
		return nil, fmt.Errorf("%w: unsupported audio type %q", ErrAudioTrackInvalid, in.Mime)
	}
	if err := a.enforceTrackLimit(ctx, mediaID, language); err != nil {
		return nil, err
	}

	// ffprobe: an audio stream must exist and its length must match the
	// video's within max(3 s, 5 %).
	tmp, err := os.MkdirTemp("", "audio-track-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	local := filepath.Join(tmp, "source"+audioExt(mime))
	if err := os.WriteFile(local, in.Data, 0o600); err != nil {
		return nil, err
	}
	meta, err := a.probeAudio(ctx, local)
	if err != nil || meta == nil || (meta.Codec == "" && meta.DurationMs == 0) {
		return nil, fmt.Errorf("%w: file has no decodable audio stream", ErrAudioTrackInvalid)
	}
	if err := checkAudioDuration(meta.DurationMs, media.DurationMsValue()); err != nil {
		return nil, err
	}

	sourceKey := audioTrackSourceKey(media.UploaderID, media.ID, language)
	if err := a.blobs.UploadObject(ctx, sourceKey, in.Data, mime); err != nil {
		return nil, fmt.Errorf("store audio: %w", err)
	}
	track := &postgres.MediaAudioTrack{
		MediaAssetID: media.ID, Language: language, Label: label,
		Source: "uploaded", SourceKey: &sourceKey, Status: "pending",
	}
	stale, err := a.store.ReplaceMediaAudioTrack(ctx, track)
	if err != nil {
		_ = a.blobs.DeleteObject(ctx, sourceKey)
		return nil, err
	}
	// The previous track's source lived at the same deterministic key the
	// new one was just written to; everything else it owned goes.
	a.deleteObjects(ctx, stale, sourceKey)
	v := a.view(ctx, media, track)
	return &v, nil
}

// checkAudioDuration: within max(3 s, 5 %) of the video. A video with no
// recorded duration (rows older than migration 016 that were never
// re-probed) cannot be compared and is admitted; the mux's -shortest bounds
// the output either way.
func checkAudioDuration(audioMs, videoMs int) error {
	if videoMs <= 0 {
		return nil
	}
	tolerance := videoMs / 20
	if tolerance < 3000 {
		tolerance = 3000
	}
	diff := audioMs - videoMs
	if diff < 0 {
		diff = -diff
	}
	if diff > tolerance {
		return fmt.Errorf("%w: audio is %.1fs, video is %.1fs (allowed difference %.1fs)",
			ErrAudioDurationMismatch, float64(audioMs)/1000, float64(videoMs)/1000, float64(tolerance)/1000)
	}
	return nil
}

// Generate queues an AI-dubbed track. The dubber runs in the worker; here
// only the request is validated and recorded.
func (a *AudioTracks) Generate(ctx context.Context, callerID, mediaID uuid.UUID, language, sourceLanguage string) (*AudioTrackView, error) {
	media, err := a.loadOwnedVideo(ctx, callerID, mediaID)
	if err != nil {
		return nil, err
	}
	if err := requireReadyVideo(media); err != nil {
		return nil, err
	}
	lang, err := normalizeAudioLanguage(language)
	if err != nil {
		return nil, err
	}
	var srcLang *string
	if strings.TrimSpace(sourceLanguage) != "" {
		s, err := normalizeAudioLanguage(sourceLanguage)
		if err != nil {
			return nil, fmt.Errorf("%w: source_language must be a BCP-47-like tag", ErrAudioTrackInvalid)
		}
		srcLang = &s
	}
	if !a.DubbingConfigured() {
		return nil, ErrDubbingUnavailable
	}
	if err := a.enforceTrackLimit(ctx, mediaID, lang); err != nil {
		return nil, err
	}
	track := &postgres.MediaAudioTrack{
		MediaAssetID: media.ID, Language: lang, Label: LanguageLabel(lang),
		Source: "generated", SourceLanguage: srcLang, Status: "pending",
	}
	stale, err := a.store.ReplaceMediaAudioTrack(ctx, track)
	if err != nil {
		return nil, err
	}
	a.deleteObjects(ctx, stale, "")
	v := a.view(ctx, media, track)
	return &v, nil
}

// Delete removes a track, its source and its muxed variants.
func (a *AudioTracks) Delete(ctx context.Context, callerID, mediaID, trackID uuid.UUID) error {
	if _, err := a.loadOwnedVideo(ctx, callerID, mediaID); err != nil {
		return err
	}
	keys, err := a.store.DeleteMediaAudioTrack(ctx, mediaID, trackID)
	if err != nil {
		return err
	}
	a.deleteObjects(ctx, keys, "")
	return nil
}

// deleteObjects removes blobs best-effort, skipping `keep` (the key a
// replacement was just written to).
func (a *AudioTracks) deleteObjects(ctx context.Context, keys []string, keep string) {
	seen := map[string]bool{}
	for _, k := range keys {
		if k == "" || k == keep || seen[k] {
			continue
		}
		seen[k] = true
		if err := a.blobs.DeleteObject(ctx, k); err != nil {
			a.log.Warn("audio tracks: stale object not deleted", "key", k, "error", err)
		}
	}
}

// ── worker ──────────────────────────────────────────────────────────────

// StartWorker drains pending tracks. Safe in every replica: tracks are
// claimed with FOR UPDATE SKIP LOCKED and every terminal write is fenced by
// the claim token.
func (a *AudioTracks) StartWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(audioTrackWorkerTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				tracks, err := a.store.ClaimMediaAudioTracks(ctx, audioTrackClaimStaleTime, audioTrackWorkerBatch)
				if err != nil {
					a.log.Error("audio tracks: claim failed", "error", err)
					continue
				}
				for i := range tracks {
					a.runTrack(ctx, &tracks[i])
				}
			}
		}
	}()
}

// runTrack processes one claimed track end to end.
func (a *AudioTracks) runTrack(ctx context.Context, t *postgres.MediaAudioTrack) {
	rungs, err := a.process(ctx, t)
	switch {
	case err == nil:
		if err := a.store.CompleteMediaAudioTrack(ctx, t.ID, rungs, t.ClaimToken); err != nil {
			a.log.Error("audio tracks: completion write failed", "track_id", t.ID, "error", err)
			return
		}
		a.log.Info("audio tracks: ready", "track_id", t.ID, "media_id", t.MediaAssetID, "language", t.Language, "rungs", rungs)
	case t.Attempts >= audioTrackMaxAttempts:
		_ = a.store.FailMediaAudioTrack(ctx, t.ID, trimError(err), t.ClaimToken)
		a.log.Error("audio tracks: giving up", "track_id", t.ID, "attempts", t.Attempts, "error", err)
	default:
		_ = a.store.ReleaseMediaAudioTrack(ctx, t.ID, trimError(err), t.ClaimToken)
		a.log.Warn("audio tracks: retrying", "track_id", t.ID, "attempt", t.Attempts, "error", err)
	}
}

func trimError(err error) string {
	s := err.Error()
	if len(s) > 1000 {
		s = s[:1000]
	}
	return s
}

// videoRung is one video file the track is muxed into.
type videoRung struct {
	Name      string
	ObjectKey string
	Width     *int
	Height    *int
}

// videoRungs picks the transcode ladder (720p, 480p, 360p …) from the
// asset's variants, or the original when there is no ladder.
func videoRungs(media *postgres.MediaAsset) []videoRung {
	var out []videoRung
	for _, v := range media.Variants {
		if videoRungPattern.MatchString(v.Name) && (v.Mime == "" || strings.HasPrefix(v.Mime, "video/")) {
			out = append(out, videoRung{Name: v.Name, ObjectKey: v.ObjectKey, Width: v.Width, Height: v.Height})
		}
	}
	if len(out) == 0 {
		out = append(out, videoRung{Name: "original", ObjectKey: media.StorageKey, Width: media.Width, Height: media.Height})
	}
	sort.SliceStable(out, func(i, j int) bool { return rungRank(out[i].Name) > rungRank(out[j].Name) })
	return out
}

// process does the work for one track and returns the rungs it produced.
func (a *AudioTracks) process(ctx context.Context, t *postgres.MediaAudioTrack) ([]string, error) {
	media, err := a.store.GetMediaWithVariants(ctx, t.MediaAssetID)
	if err != nil {
		return nil, fmt.Errorf("load video: %w", err)
	}
	rungs := videoRungs(media)

	tmp, err := os.MkdirTemp("", "dub-"+t.ID.String())
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)

	// Video files, downloaded once each.
	videoPaths := map[string]string{}
	fetchVideo := func(r videoRung) (string, error) {
		if p, ok := videoPaths[r.Name]; ok {
			return p, nil
		}
		data, err := a.blobs.DownloadObject(ctx, r.ObjectKey)
		if err != nil {
			return "", fmt.Errorf("download %s: %w", r.Name, err)
		}
		p := filepath.Join(tmp, "video-"+r.Name+".mp4")
		if err := os.WriteFile(p, data, 0o600); err != nil {
			return "", err
		}
		videoPaths[r.Name] = p
		return p, nil
	}

	// 1. A generated track without a source yet: run the dubber.
	sourcePath := filepath.Join(tmp, "source.audio")
	if t.Source == "generated" && (t.SourceKey == nil || *t.SourceKey == "") {
		if a.dubber == nil {
			return nil, ErrDubbingUnavailable
		}
		best, err := fetchVideo(rungs[0])
		if err != nil {
			return nil, err
		}
		extracted, err := a.extractAudio(ctx, best, tmp)
		if err != nil {
			return nil, fmt.Errorf("extract source audio: %w", err)
		}
		srcLang := ""
		if t.SourceLanguage != nil {
			srcLang = *t.SourceLanguage
		}
		dubbed, err := a.dubber.Dub(ctx, dubbing.DubInput{
			VideoPath:       best,
			SourceAudioPath: extracted,
			SourceLanguage:  srcLang,
			TargetLanguage:  t.Language,
			DurationMs:      media.DurationMsValue(),
			WorkDir:         filepath.Join(tmp, "dub"),
		})
		if err != nil {
			return nil, fmt.Errorf("dubbing (%s): %w", a.dubber.Name(), err)
		}
		data, err := os.ReadFile(dubbed)
		if err != nil {
			return nil, err
		}
		key := audioTrackSourceKey(media.UploaderID, media.ID, t.Language)
		if err := a.blobs.UploadObject(ctx, key, data, "audio/mp4"); err != nil {
			return nil, fmt.Errorf("store generated audio: %w", err)
		}
		if err := a.store.SetMediaAudioTrackSourceKey(ctx, t.ID, key, t.ClaimToken); err != nil {
			return nil, err
		}
		t.SourceKey = &key
		sourcePath = dubbed
	} else {
		data, err := a.blobs.DownloadObject(ctx, *t.SourceKey)
		if err != nil {
			return nil, fmt.Errorf("download source audio: %w", err)
		}
		if err := os.WriteFile(sourcePath, data, 0o600); err != nil {
			return nil, err
		}
	}

	// 2. Mux into every rung and record the variants.
	produced := make([]string, 0, len(rungs))
	for _, r := range rungs {
		videoPath, err := fetchVideo(r)
		if err != nil {
			return nil, err
		}
		outPath := filepath.Join(tmp, "dub-"+r.Name+".mp4")
		if err := a.mux(ctx, videoPath, sourcePath, outPath); err != nil {
			return nil, fmt.Errorf("mux %s: %w", r.Name, err)
		}
		out, err := os.ReadFile(outPath)
		if err != nil {
			return nil, err
		}
		key := audioTrackRungKey(media.UploaderID, media.ID, t.Language, r.Name)
		if err := a.blobs.UploadObject(ctx, key, out, "video/mp4"); err != nil {
			return nil, fmt.Errorf("upload %s: %w", r.Name, err)
		}
		size := int64(len(out))
		if err := a.store.InsertVariants(ctx, []postgres.MediaVariant{{
			MediaAssetID: media.ID,
			Name:         DubVariantName(t.Language, r.Name),
			Width:        r.Width,
			Height:       r.Height,
			SizeBytes:    &size,
			Mime:         "video/mp4",
			ObjectKey:    key,
		}}); err != nil {
			return nil, fmt.Errorf("record variant %s: %w", r.Name, err)
		}
		produced = append(produced, r.Name)
	}
	return produced, nil
}
