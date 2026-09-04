package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/captions"
	"github.com/atpost/media-service/internal/config"
	"github.com/atpost/media-service/internal/delivery"
	mediaEvents "github.com/atpost/media-service/internal/events"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Size limits per subtype/file_type
const (
	MaxImageSize  int64 = 20 * 1024 * 1024       // 20 MB
	MaxVideoSize  int64 = 2 * 1024 * 1024 * 1024 // 2 GB
	MaxAvatarSize int64 = 10 * 1024 * 1024       // 10 MB
	MaxCoverSize  int64 = 10 * 1024 * 1024       // 10 MB
	MaxGIFSize    int64 = 15 * 1024 * 1024       // 15 MB

	defaultURLExpiry = 15 * time.Minute
)

var (
	ErrUploadForbidden    = errors.New("upload confirmation forbidden")
	ErrUploadObjectAbsent = errors.New("upload object is absent")
	ErrUploadSizeMismatch = errors.New("upload object size does not match the declared size")
	ErrUploadState        = errors.New("upload cannot be confirmed in its current state")
	ErrUploadIntegrity    = errors.New("upload metadata integrity check failed")
)

type Service struct {
	pgStore   *postgres.MediaAssetStore
	blobStore *blob.Store
	producer  *mediaEvents.Producer // optional, nil = skip Kafka
	cfg       *config.Config
	scanner   processing.Scanner
	rdb       *redis.Client // optional, nil = skip rate limiting
	captions  captions.Backend
	// audioSafety evaluates voice recordings. Defaults to an evaluator
	// that always reports "unavailable", so a missing configuration fails
	// CLOSED to manual review rather than approving (fixes-v2 / P0-2).
	audioSafety processing.AudioSafetyEvaluator
	// gate authorizes and signs every byte-delivery URL (M4-P0-5).
	//
	// Nil is NOT permissive: URLFor on a nil gate returns an unresolved error,
	// so an unwired deployment denies protected media instead of falling back
	// to the stable public URL this item exists to remove.
	gate *delivery.Gate
}

// WithDeliveryGate wires byte-delivery authorization. Called from main.go.
func (s *Service) WithDeliveryGate(g *delivery.Gate) *Service {
	s.gate = g
	return s
}

// deliveryURL resolves one object key for one viewer.
//
// Every media read path goes through here. Previously each one called
// blobStore.ObjectURL directly, which returned a stable unauthenticated CDN URL
// for any key — so authorization existed only on the JSON and never on the
// bytes.
func (s *Service) deliveryURL(ctx context.Context, viewerID, mediaID uuid.UUID, objectKey string) (string, error) {
	if s.gate == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	return s.gate.URLFor(ctx, viewerID.String(), mediaID.String(), objectKey)
}

func New(pg *postgres.MediaAssetStore, blobStore *blob.Store) *Service {
	cfg := config.Load()
	s := &Service{
		pgStore:     pg,
		blobStore:   blobStore,
		cfg:         cfg,
		scanner:     &processing.StubScanner{},
		captions:    captions.SelectBackend(),
		audioSafety: selectAudioSafety(cfg),
	}
	s.logScannerPolicy()
	return s
}

// selectAudioSafety picks the voice safety evaluator. With no blocklist
// configured it returns UnavailableEvaluator, which never returns a safe
// verdict — the deliberate opposite of the permissive image StubScanner.
func selectAudioSafety(cfg *config.Config) processing.AudioSafetyEvaluator {
	if cfg != nil && cfg.VoiceSafetyBlocklist != "" {
		if e := processing.NewKeywordTranscriptEvaluator(cfg.VoiceSafetyBlocklist); e != nil {
			slog.Info("voice safety: transcript evaluator configured", "evaluator", e.Name())
			return e
		}
	}
	slog.Warn("voice safety: no evaluator configured — every voice post will be held for manual review")
	return processing.UnavailableEvaluator{}
}

// WithAudioSafety overrides the evaluator (tests / alternate providers).
func (s *Service) WithAudioSafety(e processing.AudioSafetyEvaluator) *Service {
	s.audioSafety = e
	return s
}

// WithCaptionsBackend overrides the auto-selected captions backend.
// Used in tests + by main.go when a non-default backend (e.g. a
// self-hosted whisper.cpp endpoint) is configured.
func (s *Service) WithCaptionsBackend(b captions.Backend) *Service {
	s.captions = b
	return s
}

// NewWithConfig creates a Service with an explicit config and scanner.
func NewWithConfig(pg *postgres.MediaAssetStore, blobStore *blob.Store, cfg *config.Config, scanner processing.Scanner) *Service {
	if scanner == nil {
		scanner = &processing.StubScanner{}
	}
	s := &Service{
		pgStore:     pg,
		blobStore:   blobStore,
		cfg:         cfg,
		scanner:     scanner,
		captions:    captions.SelectBackend(),
		audioSafety: selectAudioSafety(cfg),
	}
	s.logScannerPolicy()
	return s
}

// logScannerPolicy emits a loud startup log describing the active
// content-safety scanning policy. Audit H8: leaving the stub wired
// while ScannerEnabled=true is the same as leaving the gate off —
// except it looks compliant. Make the choice visible at boot.
func (s *Service) logScannerPolicy() {
	if s.cfg == nil {
		return
	}
	_, isStub := s.scanner.(*processing.StubScanner)
	switch {
	case !s.cfg.ScannerEnabled:
		slog.Info("media scanner: disabled (no content safety gate)")
	case isStub && s.cfg.ScannerAllowStub:
		slog.Warn("media scanner: STUB SCANNER ACTIVE — every image will pass. " +
			"Set MEDIA_SCANNER_ALLOW_STUB=false in production and wire a real scanner.")
	case isStub:
		slog.Error("media scanner: enabled but only StubScanner is wired and MEDIA_SCANNER_ALLOW_STUB=false. " +
			"All image uploads will be REJECTED until a real scanner is configured.")
	default:
		slog.Info("media scanner: enabled with real backend (fail-closed on scanner errors)")
	}
}

// SetProducer sets the Kafka producer for async video transcoding events.
func (s *Service) SetProducer(p *mediaEvents.Producer) {
	s.producer = p
}

// SetRedis sets the Redis client used for upload rate limiting.
func (s *Service) SetRedis(rdb *redis.Client) {
	s.rdb = rdb
}

type InitUploadResponse struct {
	MediaID   uuid.UUID `json:"media_id"`
	UploadURL string    `json:"upload_url"`
	ObjectKey string    `json:"object_key"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ValidateUpload checks size and mime constraints for the given file type and subtype.
func ValidateUpload(fileType, mediaSubtype, mimeType string, fileSizeBytes int64) error {
	// Check subtype-specific limits first
	switch mediaSubtype {
	case "avatar":
		if fileSizeBytes > MaxAvatarSize {
			return fmt.Errorf("avatar size exceeds %d MB limit", MaxAvatarSize/(1024*1024))
		}
		if !strings.HasPrefix(mimeType, "image/") {
			return fmt.Errorf("avatar must be an image, got: %s", mimeType)
		}
		return nil
	case "cover":
		if fileSizeBytes > MaxCoverSize {
			return fmt.Errorf("cover size exceeds %d MB limit", MaxCoverSize/(1024*1024))
		}
		if !strings.HasPrefix(mimeType, "image/") {
			return fmt.Errorf("cover must be an image, got: %s", mimeType)
		}
		return nil
	case "gif":
		if fileSizeBytes > MaxGIFSize {
			return fmt.Errorf("gif size exceeds %d MB limit", MaxGIFSize/(1024*1024))
		}
		if mimeType != "image/gif" {
			return fmt.Errorf("invalid mime type for gif: %s", mimeType)
		}
		return nil
	}

	// Fall through to file_type checks for general subtype
	switch fileType {
	case "image":
		if fileSizeBytes > MaxImageSize {
			return fmt.Errorf("image size exceeds %d MB limit", MaxImageSize/(1024*1024))
		}
		if !strings.HasPrefix(mimeType, "image/") {
			return fmt.Errorf("invalid mime type for image: %s", mimeType)
		}
	case "video":
		if fileSizeBytes > MaxVideoSize {
			return fmt.Errorf("video size exceeds %d MB limit", MaxVideoSize/(1024*1024))
		}
		validMimes := map[string]bool{"video/mp4": true, "video/webm": true, "video/quicktime": true}
		if !validMimes[mimeType] {
			return fmt.Errorf("invalid mime type for video: %s", mimeType)
		}
	case "audio":
		// Voice posts (Module 1 P0-6). MIME is already checked against
		// allowedAudioMIME by ValidateUploadMIME; this bounds the size.
		// Duration is enforced server-side at confirm from ffprobe.
		if fileSizeBytes > MaxVoiceSizeBytes {
			return fmt.Errorf("audio size exceeds %d MB limit", MaxVoiceSizeBytes/(1024*1024))
		}
	default:
		return fmt.Errorf("unknown file type: %s", fileType)
	}
	return nil
}

func (s *Service) InitUpload(ctx context.Context, userID uuid.UUID, fileType, mediaSubtype, mimeType string, fileSizeBytes int64, altText string, decorative bool, uploadPurpose string) (*InitUploadResponse, error) {
	// Absolute size cap (applies to all file types)
	if err := ValidateUploadSize(fileSizeBytes); err != nil {
		return nil, err
	}

	// MIME allow-list check
	if err := ValidateUploadMIME(mimeType, fileType); err != nil {
		return nil, err
	}

	// Per-user upload rate limit (Redis sliding window)
	if err := s.CheckUploadRateLimit(ctx, userID); err != nil {
		return nil, err
	}

	// Subtype/file-type-specific size + MIME validation (existing)
	if err := ValidateUpload(fileType, mediaSubtype, mimeType, fileSizeBytes); err != nil {
		return nil, err
	}

	mediaID := uuid.New()
	storageKey := fmt.Sprintf("user/%s/%s/original", userID, mediaID)
	expiry := 15 * time.Minute

	url, err := s.blobStore.GeneratePresignedPutURL(ctx, storageKey, expiry)
	if err != nil {
		return nil, err
	}

	// P1-7: decorative and a description are mutually exclusive.
	if decorative {
		altText = ""
	}
	media := &postgres.MediaAsset{
		ID:               mediaID,
		UploaderID:       userID,
		FileType:         fileType,
		MediaSubtype:     mediaSubtype,
		MimeType:         mimeType,
		FileSizeBytes:    fileSizeBytes,
		StorageBucket:    s.blobStore.Bucket(),
		StorageKey:       storageKey,
		ProcessingStatus: "pending_upload",
		AltText:          altText,
		AltDecorative:    decorative,
		// Only the value the caller named. An unknown or absent purpose stays
		// NULL, and a NULL lease is never a confirmed-reclamation candidate.
		UploadPurpose: normaliseUploadPurpose(uploadPurpose),
		CreatedAt:     time.Now(),
	}

	if err := s.pgStore.CreateMedia(ctx, media); err != nil {
		return nil, err
	}

	return &InitUploadResponse{
		MediaID:   mediaID,
		UploadURL: url.String(),
		ObjectKey: storageKey,
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

func (s *Service) ConfirmUpload(ctx context.Context, mediaID uuid.UUID, userID uuid.UUID) (*postgres.MediaAsset, error) {
	// 1. Fetch the record and verify ownership
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if media.UploaderID != userID {
		return nil, fmt.Errorf("%w: you do not own this media", ErrUploadForbidden)
	}
	// Confirmation is idempotent after work has been accepted. It must not
	// enqueue duplicate transcodes or repeat synchronous image processing.
	switch media.ProcessingStatus {
	case "processing", "ready":
		return media, nil
	case "failed", "rejected":
		return nil, fmt.Errorf("%w: media is %s", ErrUploadState, media.ProcessingStatus)
	case "pending_upload", "uploaded":
		// continue; uploaded is recoverable after a crash between the status
		// update and the durable work-intent transaction.
	default:
		return nil, fmt.Errorf("%w: unknown status %q", ErrUploadState, media.ProcessingStatus)
	}

	expectedKey := fmt.Sprintf("user/%s/%s/original", media.UploaderID, media.ID)
	if media.StorageKey != expectedKey {
		return nil, fmt.Errorf("%w: unexpected storage key", ErrUploadIntegrity)
	}
	info, err := s.blobStore.StatObject(ctx, media.StorageKey)
	if errors.Is(err, blob.ErrObjectNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrUploadObjectAbsent, media.StorageKey)
	}
	if err != nil {
		return nil, fmt.Errorf("verify uploaded object: %w", err)
	}
	if info.Size != media.FileSizeBytes {
		return nil, fmt.Errorf("%w: declared=%d actual=%d", ErrUploadSizeMismatch, media.FileSizeBytes, info.Size)
	}

	// Magic-byte validation reads at most 64 bytes. A storage read failure is
	// not an approval; confirmation is retried with no state change.
	headerEnd := int64(63)
	if info.Size-1 < headerEnd {
		headerEnd = info.Size - 1
	}
	if headerEnd < 0 {
		return nil, fmt.Errorf("%w: uploaded object is empty", ErrUploadSizeMismatch)
	}
	headerData, err := s.blobStore.ReadObjectRange(ctx, media.StorageKey, 0, headerEnd)
	if err != nil {
		return nil, fmt.Errorf("read uploaded object header: %w", err)
	}
	valid := false
	switch media.FileType {
	case "video":
		_, valid = processing.ValidateVideoMagicBytes(headerData)
	case "image":
		_, valid = processing.ValidateImageMagicBytes(headerData)
	case "audio":
		_, valid = processing.ValidateAudioMagicBytes(headerData)
	}
	if !valid {
		if updateErr := s.pgStore.UpdateStatus(ctx, mediaID, "rejected"); updateErr != nil {
			return nil, fmt.Errorf("reject invalid %s and persist status: %w", media.FileType, updateErr)
		}
		return nil, fmt.Errorf("invalid %s file: magic bytes do not match declared MIME type", media.FileType)
	}

	// 2. Update status to 'uploaded'
	if err := s.pgStore.UpdateStatus(ctx, mediaID, "uploaded"); err != nil {
		return nil, err
	}

	// 3. Process based on file_type + media_subtype
	switch {
	case media.FileType == "image" && (media.MediaSubtype == "general" || media.MediaSubtype == "avatar" || media.MediaSubtype == "cover"):
		if err := s.processImage(ctx, media); err != nil {
			_ = s.pgStore.UpdateStatus(ctx, mediaID, "failed")
			media.ProcessingStatus = "failed"
			return media, nil
		}
		media.ProcessingStatus = "ready"

	case media.MediaSubtype == "gif":
		if err := s.processImage(ctx, media); err != nil {
			_ = s.pgStore.UpdateStatus(ctx, mediaID, "failed")
			media.ProcessingStatus = "failed"
			return media, nil
		}
		media.ProcessingStatus = "ready"

	case media.FileType == "audio":
		// Voice posts (Module 1 P0-6): enforce the server-measured
		// duration cap and build the waveform synchronously, then hold
		// distribution until baseline safety completes.
		if err := s.processVoice(ctx, media); err != nil {
			media.ProcessingStatus = "rejected"
			return nil, err
		}
		if err := s.pgStore.UpdateStatus(ctx, mediaID, "ready"); err != nil {
			return nil, err
		}
		media.ProcessingStatus = "ready"

		// Safety gate: 'pending' keeps the voice post out of public
		// surfaces until the transcript/audio scan lands. With the gate
		// disabled (dev) the asset is approved immediately.
		safety := VoiceSafetyPending
		if !s.cfg.VoiceSafetyRequired {
			safety = VoiceSafetyApproved
		}
		if err := s.pgStore.SetMediaModerationStatus(ctx, mediaID, safety); err != nil {
			slog.Warn("voice: failed to set moderation status", "media_id", mediaID, "error", err)
		}
		media.ModerationStatus = safety

		// Durable caption request — persisted, then drained by the caption
		// worker (no untracked goroutine). With no backend configured the
		// asset is routed to manual review; it is never auto-approved.
		// fixes-v2 / P1-4: an enqueue failure is now surfaced so the
		// confirm fails and the client can retry, instead of silently
		// leaving the media pending with no durable job.
		if err := s.enqueueCaptionsForVoice(ctx, mediaID, media.UploaderID, ""); err != nil {
			return nil, err
		}

	case media.FileType == "video":
		// State and work intent are one PostgreSQL commit. Kafka is drained
		// from the outbox, so confirm never succeeds after merely hoping a
		// direct publish worked.
		if err := s.pgStore.QueueTranscode(ctx, media); err != nil {
			return nil, err
		}
		media.ProcessingStatus = "processing"
	}

	return media, nil
}

// processImage handles synchronous image processing (resize + upload variants).
func (s *Service) processImage(ctx context.Context, media *postgres.MediaAsset) error {
	moderationStatus := "passed"
	// Content safety scan for images.
	//
	// Audit H8: previously this block "skipped" the scan on download
	// failure or scanner error and continued to admit the image — so
	// any transient MinIO blip or scanner outage created a silent
	// bypass. Policy now is fail-closed:
	//
	//   - Stub scanner + AllowStub=false → reject every image upload
	//     (refuses to silently pretend it's scanning).
	//   - Download error                 → reject (can't scan, can't admit).
	//   - Scanner error                  → reject (same reasoning).
	//   - IsSafe=false                   → reject.
	//
	// When the scanner is disabled entirely the block is skipped and
	// the image is admitted unscanned (dev / behind-perimeter mode).
	if s.cfg.ScannerEnabled && isImage(media.MimeType) {
		moderationStatus = "manual_review"
		if _, isStub := s.scanner.(*processing.StubScanner); isStub && !s.cfg.ScannerAllowStub {
			slog.Error("media: rejecting upload — scanner enabled but only StubScanner configured",
				"media_id", media.ID)
			_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
			return fmt.Errorf("media rejected: content safety scanner not configured")
		}

		imageData, err := s.blobStore.DownloadObject(ctx, media.StorageKey)
		if err != nil {
			slog.Error("media: rejecting upload — failed to download image for scanning",
				"media_id", media.ID, "error", err)
			_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
			return fmt.Errorf("media rejected: cannot fetch for scan: %w", err)
		}

		result, err := s.scanner.ScanImage(ctx, imageData)
		if err != nil {
			slog.Error("media: rejecting upload — scanner error",
				"media_id", media.ID, "error", err)
			_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
			return fmt.Errorf("media rejected: scanner error: %w", err)
		}
		if !result.IsSafe {
			slog.Warn("media: content rejected by scanner",
				"media_id", media.ID, "reason", result.Reason, "score", result.Score)
			_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
			return fmt.Errorf("media rejected: %s", result.Reason)
		}
		moderationStatus = "passed"
	}

	outputs, meta, err := processing.ProcessImage(
		ctx, s.blobStore, media.StorageKey,
		media.ID.String(), media.UploaderID.String(),
	)
	if err != nil {
		return fmt.Errorf("process image: %w", err)
	}

	// Update media metadata (including blurhash)
	if err := s.pgStore.UpdateMediaMeta(ctx, media.ID, meta.Width, meta.Height, meta.Blurhash, nil, nil); err != nil {
		return fmt.Errorf("update media meta: %w", err)
	}

	// Insert variant records
	var variants []postgres.MediaVariant
	for _, out := range outputs {
		w := out.Width
		h := out.Height
		sz := out.SizeBytes
		variants = append(variants, postgres.MediaVariant{
			MediaAssetID: media.ID,
			Name:         out.Name,
			Width:        &w,
			Height:       &h,
			SizeBytes:    &sz,
			Mime:         out.Mime,
			ObjectKey:    out.ObjectKey,
		})
	}

	if err := s.pgStore.InsertVariants(ctx, variants); err != nil {
		return fmt.Errorf("insert variants: %w", err)
	}
	if err := s.pgStore.UpdateMediaModerationStatus(ctx, media.ID, moderationStatus); err != nil {
		return fmt.Errorf("persist image moderation verdict: %w", err)
	}
	media.ModerationStatus = moderationStatus

	// Populate URL fields
	s.populateMediaURLs(ctx, media, variants)

	// Mark as ready
	if err := s.pgStore.UpdateStatus(ctx, media.ID, "ready"); err != nil {
		return err
	}

	// Activate any pending slots referencing this media asset
	if err := s.ActivatePendingSlots(ctx, media.ID); err != nil {
		slog.Warn("failed to activate pending slots after image processing",
			"media_id", media.ID, "error", err)
	}

	return nil
}

// isImage returns true when contentType is an image MIME type.
func isImage(contentType string) bool {
	return strings.HasPrefix(contentType, "image/")
}

func (s *Service) GetMedia(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	return s.pgStore.GetMediaWithVariants(ctx, mediaID)
}

// MediaURLResponse is the response for serving media URLs.
type MediaURLResponse struct {
	MediaID  uuid.UUID `json:"media_id"`
	FileType string    `json:"kind"`
	Status   string    `json:"status"`
	Width    *int      `json:"width,omitempty"`
	Height   *int      `json:"height,omitempty"`
	Blurhash *string   `json:"blurhash,omitempty"`
	// DurationMs is the ffprobe duration in milliseconds for video and
	// audio; omitted (0) while unknown and for images (Tube, 2026-09-05).
	DurationMs int               `json:"duration_ms,omitempty"`
	Variants   map[string]string `json:"variants,omitempty"`
	HLSURL     string            `json:"hls_url,omitempty"`
	// ExpiresAt tells native clients when the bounded delivery capability
	// must be refreshed. Public variants may be stable, but using the same
	// refresh boundary keeps one DTO valid when visibility later becomes
	// protected and covers the signed HLS child/segment URLs.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// PlaybackURL / PlaybackKind (instant publish, 2026-09-04): the ONE URL
	// a video player should open and what it is.
	//
	//   - "hls": the authorized master playlist (gateway-relative, same
	//     value as hls_url) — once the transcode has produced a ladder.
	//   - "original": the signed URL of the object the phone uploaded, a
	//     progressive MP4 — while the transcode is still running (or if it
	//     produced no HLS). A reel is playable the moment its upload
	//     finishes; the ladder is a quality upgrade that lands later.
	//
	// Absent for images and audio. The poster/thumbnail may still be
	// missing while the kind is "original"; the worker generates it.
	PlaybackURL  string `json:"playback_url,omitempty"`
	PlaybackKind string `json:"playback_kind,omitempty"`
}

// Playback kinds. See MediaURLResponse.PlaybackKind.
const (
	PlaybackKindHLS      = "hls"
	PlaybackKindOriginal = "original"
)

// choosePlayback picks the playback URL for a video asset. Pure, so it is
// the one place the fallback rule lives and it is unit-tested without a
// blob store. `urls` is the signed map from the delivery gate; it always
// carries "original" once the asset is authorized, whatever its processing
// state, because the original object exists from the moment the upload is
// confirmed.
func choosePlayback(fileType, hlsURL string, urls map[string]string) (url, kind string) {
	if fileType != "video" {
		return "", ""
	}
	if hlsURL != "" {
		return hlsURL, PlaybackKindHLS
	}
	if orig := urls["original"]; orig != "" {
		return orig, PlaybackKindOriginal
	}
	return "", ""
}

// GetMediaURL returns authorized delivery URLs for a media item and all its
// variants.
//
// M4-P0-5 — this takes a viewer now. It previously took only a media id and
// handed back stable CDN URLs for every key, so the caller's identity never
// entered the decision at all.
//
// Signing errors are RETURNED rather than skipped. The old loop used
// `if err == nil { variants[name] = url }`, which meant a misconfiguration
// produced a 200 with an empty variant map — indistinguishable from media that
// had not finished processing, and impossible to alert on.
func (s *Service) GetMediaURL(ctx context.Context, viewerID, mediaID uuid.UUID) (*MediaURLResponse, error) {
	media, err := s.pgStore.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if s.gate == nil {
		return nil, fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}

	keys := map[string]string{"original": media.StorageKey}
	for _, v := range media.Variants {
		keys[v.Name] = v.ObjectKey
	}
	if media.HLSMasterKey != "" {
		keys["hls"] = media.HLSMasterKey
	}

	// One authorization for the asset, then every key signed.
	urls, err := s.gate.URLsForAsset(ctx, viewerID.String(), mediaID.String(), keys)
	if err != nil {
		return nil, err
	}

	if media.ProcessingStatus != "ready" {
		slog.InfoContext(ctx, "media single: asset permitted but not ready",
			"media_id", media.ID,
			"viewer_id", viewerID,
			"status", media.ProcessingStatus)
		// Instant publish: no variants yet, but a confirmed video is
		// playable as the original progressive MP4 right now. The URL is
		// signed, so it carries the same refresh boundary as a ready asset.
		playbackURL, playbackKind := choosePlayback(media.FileType, "", urls)
		res := &MediaURLResponse{
			MediaID:      media.ID,
			FileType:     media.FileType,
			Status:       media.ProcessingStatus,
			Width:        media.Width,
			Height:       media.Height,
			Blurhash:     media.Blurhash,
			DurationMs:   media.DurationMsValue(),
			Variants:     nil,
			HLSURL:       "",
			PlaybackURL:  playbackURL,
			PlaybackKind: playbackKind,
		}
		if playbackURL != "" {
			expires := time.Now().UTC().Add(delivery.MaxProtectedTTL)
			res.ExpiresAt = &expires
		}
		return res, nil
	}

	hlsURL := ""
	if media.HLSMasterKey != "" {
		// A signature on the storage master does not propagate to its relative
		// child playlists and segments. Return the authorized API playlist path;
		// that path rewrites children and signs the byte-bearing segments.
		hlsURL = hlsPlaylistURL(media.ID, "master.m3u8")
	}
	delete(urls, "hls")
	expires := time.Now().UTC().Add(delivery.MaxProtectedTTL)
	playbackURL, playbackKind := choosePlayback(media.FileType, hlsURL, urls)

	return &MediaURLResponse{
		MediaID:      media.ID,
		FileType:     media.FileType,
		Status:       media.ProcessingStatus,
		Width:        media.Width,
		Height:       media.Height,
		Blurhash:     media.Blurhash,
		DurationMs:   media.DurationMsValue(),
		Variants:     urls,
		HLSURL:       hlsURL,
		ExpiresAt:    &expires,
		PlaybackURL:  playbackURL,
		PlaybackKind: playbackKind,
	}, nil
}

func hlsPlaylistURL(mediaID uuid.UUID, playlist string) string {
	return fmt.Sprintf("/v1/media/%s/hls/%s", mediaID, playlist)
}

// GetHLSPlaylist authorizes an asset and rewrites its playlist into a graph a
// native player can follow. Only generated basename playlists are accepted;
// callers cannot turn this endpoint into an arbitrary bucket reader.
func (s *Service) GetHLSPlaylist(ctx context.Context, viewerID, mediaID uuid.UUID, playlist string) ([]byte, error) {
	if s.gate == nil {
		return nil, fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if media.HLSMasterKey == "" {
		return nil, fmt.Errorf("HLS is not available for media %s", mediaID)
	}
	playlist = strings.TrimSpace(playlist)
	if playlist == "" || playlist == "hls" {
		playlist = "master.m3u8"
	}
	if strings.ContainsAny(playlist, `/\\`) || playlist == "." || playlist == ".." || !strings.HasSuffix(playlist, ".m3u8") {
		return nil, fmt.Errorf("invalid HLS playlist %q", playlist)
	}

	baseKey := strings.TrimSuffix(media.HLSMasterKey, "master.m3u8")
	playlistKey := baseKey + playlist
	raw, err := s.blobStore.DownloadObject(ctx, playlistKey)
	if err != nil {
		return nil, fmt.Errorf("load HLS playlist: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	keys := map[string]string{"playlist": playlistKey}
	if playlist != "master.m3u8" {
		for _, line := range lines {
			name := strings.TrimSpace(line)
			if name == "" || strings.HasPrefix(name, "#") {
				continue
			}
			if strings.ContainsAny(name, `/\\`) || name == "." || name == ".." {
				return nil, fmt.Errorf("invalid HLS segment reference %q", name)
			}
			keys[name] = baseKey + name
		}
	}

	// URLsForAsset is the authorization choke point. Including the playlist
	// key makes even an empty playlist require a resolved permission decision;
	// child segment URLs are then signed after that same single decision.
	urls, err := s.gate.URLsForAsset(ctx, viewerID.String(), mediaID.String(), keys)
	if err != nil {
		return nil, err
	}
	for i, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if playlist == "master.m3u8" {
			if !strings.HasSuffix(name, ".m3u8") || strings.ContainsAny(name, `/\\`) {
				return nil, fmt.Errorf("invalid HLS child playlist reference %q", name)
			}
			lines[i] = hlsPlaylistURL(mediaID, name)
			continue
		}
		signed, ok := urls[name]
		if !ok || signed == "" {
			return nil, fmt.Errorf("missing signed URL for HLS segment %q", name)
		}
		lines[i] = signed
	}
	return []byte(strings.Join(lines, "\n")), nil
}

// GetMediaVariantURL returns an authorized delivery URL for one variant.
func (s *Service) GetMediaVariantURL(ctx context.Context, viewerID, mediaID uuid.UUID, variant string) (string, error) {
	if variant == "original" {
		media, err := s.pgStore.GetMedia(ctx, mediaID)
		if err != nil {
			return "", err
		}
		return s.deliveryURL(ctx, viewerID, mediaID, media.StorageKey)
	}

	variants, err := s.pgStore.GetVariants(ctx, mediaID)
	if err != nil {
		return "", err
	}
	for _, v := range variants {
		if v.Name == variant {
			return s.deliveryURL(ctx, viewerID, mediaID, v.ObjectKey)
		}
	}
	return "", fmt.Errorf("variant %q not found", variant)
}

// BatchMediaURLs returns authorized delivery URLs for multiple media items.
//
// M4-P0-5 — a batch is where an authorization bypass is most valuable to an
// attacker: one request, fifty ids, and before this every one came back with a
// stable URL regardless of who asked.
//
// A media item this viewer may not have is OMITTED from the result rather than
// failing the whole batch. That is deliberate and it is not error-swallowing:
// the batch is a rendering convenience over a mixed set (a feed page contains
// other people's media), so one denied item must not blank the page. The
// omission is indistinguishable from "no such media", which keeps the batch
// from being used to enumerate what exists.
//
// An UNRESOLVED item fails the whole call, because that is an outage rather
// than an answer, and silently dropping it would render a feed with holes and
// no indication anything went wrong.
func (s *Service) BatchMediaURLs(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*MediaURLResponse, error) {
	if len(ids) > 50 {
		return nil, fmt.Errorf("batch limit is 50 media items")
	}
	if s.gate == nil {
		return nil, fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}

	medias, err := s.pgStore.GetMediaBatch(ctx, ids)
	if err != nil {
		return nil, err
	}

	foundMap := make(map[uuid.UUID]bool, len(medias))
	for _, m := range medias {
		foundMap[m.ID] = true
	}
	for _, id := range ids {
		if !foundMap[id] {
			slog.WarnContext(ctx, "media batch: requested asset absent from database",
				"viewer_id", viewerID,
				"media_id", id)
		}
	}

	assetKeys := make(map[string]map[string]string, len(medias))
	for _, m := range medias {
		keys := map[string]string{"original": m.StorageKey}
		for _, v := range m.Variants {
			keys[v.Name] = v.ObjectKey
		}
		if m.HLSMasterKey != "" {
			keys["hls"] = m.HLSMasterKey
		}

		assetKeys[m.ID.String()] = keys
	}
	urlsByMedia, err := s.gate.URLsForAssets(ctx, viewerID.String(), assetKeys)
	if err != nil {
		return nil, err
	}

	result := make(map[uuid.UUID]*MediaURLResponse, len(medias))
	for _, m := range medias {
		urls, ok := urlsByMedia[m.ID.String()]
		if !ok {
			slog.InfoContext(ctx, "media batch: asset denied for viewer",
				"viewer_id", viewerID,
				"media_id", m.ID,
				"status", m.ProcessingStatus)
			continue // resolved denial — omit without revealing existence
		}

		if m.ProcessingStatus != "ready" {
			// Permitted content, but asset processing is not yet ready for URL delivery.
			slog.InfoContext(ctx, "media batch: asset permitted but not ready",
				"viewer_id", viewerID,
				"media_id", m.ID,
				"status", m.ProcessingStatus)
			// Instant publish: a confirmed video plays as the original
			// progressive MP4 until the ladder lands (feed hydration reads
			// this DTO, so the reels surface gets it too).
			playbackURL, playbackKind := choosePlayback(m.FileType, "", urls)
			res := &MediaURLResponse{
				MediaID:      m.ID,
				FileType:     m.FileType,
				Status:       m.ProcessingStatus,
				Width:        m.Width,
				Height:       m.Height,
				Blurhash:     m.Blurhash,
				DurationMs:   m.DurationMsValue(),
				Variants:     nil,
				HLSURL:       "",
				PlaybackURL:  playbackURL,
				PlaybackKind: playbackKind,
			}
			if playbackURL != "" {
				expires := time.Now().UTC().Add(delivery.MaxProtectedTTL)
				res.ExpiresAt = &expires
			}
			result[m.ID] = res
			continue
		}

		hlsURL := ""
		if m.HLSMasterKey != "" {
			hlsURL = hlsPlaylistURL(m.ID, "master.m3u8")
		}
		delete(urls, "hls")
		expires := time.Now().UTC().Add(delivery.MaxProtectedTTL)
		playbackURL, playbackKind := choosePlayback(m.FileType, hlsURL, urls)
		result[m.ID] = &MediaURLResponse{
			MediaID:      m.ID,
			FileType:     m.FileType,
			Status:       m.ProcessingStatus,
			Width:        m.Width,
			Height:       m.Height,
			Blurhash:     m.Blurhash,
			DurationMs:   m.DurationMsValue(),
			Variants:     urls,
			HLSURL:       hlsURL,
			ExpiresAt:    &expires,
			PlaybackURL:  playbackURL,
			PlaybackKind: playbackKind,
		}
	}

	return result, nil
}

// ─── Delete ────────────────────────────────────────────────────────

// DeleteMedia verifies ownership, removes blobs from storage, then deletes the DB record.
// ErrMediaNotOrphaned is returned when an internal orphan-delete request
// targets media that is still in use or too recent to reclaim.
var ErrMediaNotOrphaned = errors.New("media is not orphaned")

// orphanMinAge is the minimum age before an unreferenced asset may be
// reclaimed. Protects an upload that is mid-flight between /confirm and
// the post create that will attach it.
const orphanMinAge = 24 * time.Hour

// DeleteOrphanMedia deletes an asset on behalf of an internal caller
// (fixes-v2 / Codex P1-5), re-verifying eligibility here rather than
// trusting the caller:
//
//   - the asset must exist,
//   - it must be older than orphanMinAge,
//   - it must not be attached to any post.
//
// media-service does not own the posts table, so the post-attachment
// check is delegated to the shared `post_media` table via the same
// database. When that table is unreachable the delete is refused —
// fail-closed, because deleting live media is unrecoverable.
func (s *Service) DeleteOrphanMedia(ctx context.Context, mediaID uuid.UUID) error {
	// Module 1 fixes-v3 / LB-1: the whole eligibility decision (age,
	// published references, surviving-draft references) and the row
	// deletion now happen in ONE transaction that locks the asset row,
	// so a concurrent attach cannot slip between check and delete. See
	// DeleteOrphanMediaAtomic for the locking rationale.
	objectKeys, err := s.pgStore.DeleteOrphanMediaAtomic(ctx, mediaID, orphanMinAge)
	switch {
	case errors.Is(err, postgres.ErrMediaNotFound):
		// Already reclaimed by an earlier attempt. Idempotent success:
		// the caller's goal (asset gone) is satisfied.
		slog.Info("orphan delete: media already absent", "media_id", mediaID)
		return nil
	case errors.Is(err, postgres.ErrMediaTooYoung):
		return fmt.Errorf("%w: asset is younger than the reclaim window", ErrMediaNotOrphaned)
	case errors.Is(err, postgres.ErrMediaConfirmed):
		// Slice C / C-CLB-1. Confirmed assets are not reclaimable while
		// non-FK live references exist, so this is a policy refusal, not a
		// fault: the sweeper logs it and moves on, exactly as it does for an
		// asset that gained a reference mid-scan.
		return fmt.Errorf("%w: confirmed assets are not reclaimable", ErrMediaNotOrphaned)
	case errors.Is(err, postgres.ErrMediaStillReferenced):
		return fmt.Errorf("%w: still attached to a post or surviving draft", ErrMediaNotOrphaned)
	case err != nil:
		return err
	}

	// Rows are gone and the object keys are durably recorded in
	// media_blob_reclaim, so a failure below is retried by the sweeper
	// rather than leaking the object forever.
	s.reclaimBlobs(ctx, objectKeys)
	slog.Info("orphan media reclaimed", "media_id", mediaID, "objects", len(objectKeys))
	return nil
}

// reclaimBlobs deletes objects and clears their reclaim rows. Any failure
// leaves the row in place for the sweeper to retry.
func (s *Service) reclaimBlobs(ctx context.Context, objectKeys []string) {
	for _, key := range objectKeys {
		if key == "" {
			continue
		}
		if err := s.blobStore.DeleteObject(ctx, key); err != nil {
			slog.Warn("orphan delete: blob removal failed (will retry)", "key", key, "error", err)
			_ = s.pgStore.RecordBlobReclaimFailure(ctx, key, err.Error())
			continue
		}
		if err := s.pgStore.ClearBlobReclaim(ctx, key); err != nil {
			slog.Warn("orphan delete: reclaim row cleanup failed", "key", key, "error", err)
		}
	}
}

// StartBlobReclaimWorker drains media_blob_reclaim. Without it a transient
// S3/MinIO failure during orphan deletion would leak the object with no
// record left to retry from (LB-1 requirement 7).
func (s *Service) StartBlobReclaimWorker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				keys, err := s.pgStore.PendingBlobReclaims(ctx, 200)
				if err != nil {
					slog.Error("blob reclaim: list failed", "error", err)
					continue
				}
				if len(keys) == 0 {
					continue
				}
				s.reclaimBlobs(ctx, keys)
				slog.Info("blob reclaim sweep", "attempted", len(keys))
			}
		}
	}()
}

func (s *Service) DeleteMedia(ctx context.Context, mediaID uuid.UUID, userID uuid.UUID) error {
	// 1. Fetch and verify ownership
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return fmt.Errorf("media not found")
	}
	if media.UploaderID != userID {
		return fmt.Errorf("forbidden: you do not own this media")
	}

	// 2. Delete from DB (returns all object keys)
	objectKeys, err := s.pgStore.DeleteMedia(ctx, mediaID)
	if err != nil {
		return fmt.Errorf("delete media record: %w", err)
	}

	// 3. Delete blobs from storage (best-effort, don't fail the request)
	for _, key := range objectKeys {
		if err := s.blobStore.DeleteObject(ctx, key); err != nil {
			slog.Warn("Failed to delete blob", "key", key, "error", err)
		}
	}

	return nil
}

// ─── Status ────────────────────────────────────────────────────────

// MediaStatusResponse is the response for the status endpoint.
// MediaStatusResponse is what a CLIENT polls while it waits to attach an asset.
//
// ModerationStatus is part of the answer, not an extra.
//
// An asset is attachable only at EXACT `processing_status=ready` AND
// `moderation_status=passed` — post-service enforces both. This response
// carried only the processing half, so a client polling here could never learn
// the safety verdict: it either waited forever for a field that is never sent,
// or attached on `ready` alone and had the create refused with an error the
// person could not act on. Found by the C-LB-8 live run; every unit test passed
// because their fakes supplied the field this endpoint does not.
type MediaStatusResponse struct {
	MediaID          uuid.UUID                 `json:"media_id"`
	ProcessingStatus string                    `json:"processing_status"`
	ModerationStatus string                    `json:"moderation_status"`
	FileType         string                    `json:"file_type"`
	Width            *int                      `json:"width,omitempty"`
	Height           *int                      `json:"height,omitempty"`
	DurationSeconds  *int                      `json:"duration_seconds,omitempty"`
	DurationMs       int                       `json:"duration_ms,omitempty"`
	TranscodingJobs  []postgres.TranscodingJob `json:"transcoding_jobs,omitempty"`
}

// ProfileMediaAuthority is the server-to-server answer used before an
// identity profile is allowed to reference an asset.  A client-side upload
// check is not an authority: a caller can bypass the Android application and
// submit any UUID to profile-service.  Keeping this decision in media-service
// means ownership, processing and moderation are evaluated where the asset
// record lives.
type ProfileMediaAuthority struct {
	MediaID    uuid.UUID `json:"media_id"`
	OwnerID    uuid.UUID `json:"owner_id"`
	Subtype    string    `json:"media_subtype"`
	Attachable bool      `json:"attachable"`
}

// CheckProfileMediaAuthority returns an attachable verdict for an avatar or
// cover.  Unknown subtypes fail closed and no storage coordinates are exposed.
func (s *Service) CheckProfileMediaAuthority(ctx context.Context, mediaID, ownerID uuid.UUID, subtype string) (*ProfileMediaAuthority, error) {
	if subtype != "avatar" && subtype != "cover" {
		return nil, fmt.Errorf("unsupported profile media subtype %q", subtype)
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	return &ProfileMediaAuthority{
		MediaID: media.ID,
		OwnerID: media.UploaderID,
		Subtype: media.MediaSubtype,
		Attachable: media.UploaderID == ownerID &&
			media.FileType == "image" &&
			media.MediaSubtype == subtype &&
			media.ProcessingStatus == "ready" &&
			media.ModerationStatus == "passed",
	}, nil
}

// GetMediaStatus returns the processing status and transcoding job details.
func (s *Service) GetMediaStatus(ctx context.Context, mediaID uuid.UUID) (*MediaStatusResponse, error) {
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}

	resp := &MediaStatusResponse{
		MediaID:          media.ID,
		ProcessingStatus: media.ProcessingStatus,
		ModerationStatus: media.ModerationStatus,
		FileType:         media.FileType,
		Width:            media.Width,
		Height:           media.Height,
		DurationSeconds:  media.DurationSeconds,
		DurationMs:       media.DurationMsValue(),
	}

	// Include transcoding jobs for videos
	if media.FileType == "video" {
		jobs, err := s.pgStore.GetTranscodingJobs(ctx, mediaID)
		if err != nil {
			slog.Warn("Failed to fetch transcoding jobs", "media_id", mediaID, "error", err)
		} else {
			resp.TranscodingJobs = jobs
		}
	}

	return resp, nil
}

// ─── URL Population ────────────────────────────────────────────────

// populateMediaURLs generates and stores the URL references for a processed media item.
func (s *Service) populateMediaURLs(ctx context.Context, media *postgres.MediaAsset, variants []postgres.MediaVariant) {
	originalURL := media.StorageKey
	cdnURL := fmt.Sprintf("/%s/%s", media.StorageBucket, media.StorageKey)

	var thumbnailURL *string
	for _, v := range variants {
		if v.Name == "thumb_150" {
			key := v.ObjectKey
			thumbnailURL = &key
			break
		}
	}

	if err := s.pgStore.UpdateMediaURLs(ctx, media.ID, &originalURL, &cdnURL, thumbnailURL); err != nil {
		slog.Warn("Failed to update media URLs", "media_id", media.ID, "error", err)
	}
}

// ─── Alt Text ──────────────────────────────────────────────────────

// UpdateAltText updates the alt_text field of a media asset owned by userID.
// Returns an error if the asset does not exist or is not owned by userID.
func (s *Service) UpdateAltText(ctx context.Context, mediaID uuid.UUID, userID uuid.UUID, altText string) error {
	return s.pgStore.UpdateAltText(ctx, mediaID, userID, altText)
}

// UpdateAltTextWithDecorative sets the description and decorative marker
// together, owner-only (Codex P1-7).
func (s *Service) UpdateAltTextWithDecorative(ctx context.Context, mediaID, userID uuid.UUID, altText string, decorative bool) error {
	return s.pgStore.UpdateAltTextWithDecorative(ctx, mediaID, userID, altText, decorative)
}

// ─── Presigned Upload ──────────────────────────────────────────────

// PresignedUploadResponse is returned by GetPresignedUploadURL.
type PresignedUploadResponse struct {
	UploadURL string    `json:"upload_url"`
	MediaID   uuid.UUID `json:"media_id"`
	ObjectKey string    `json:"object_key"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GetPresignedUploadURL creates a new media asset record in pending_upload state and
// returns a presigned PUT URL that the client can use to upload the file directly to
// object storage.
func (s *Service) GetPresignedUploadURL(ctx context.Context, userID uuid.UUID, filename, contentType string) (*PresignedUploadResponse, error) {
	mediaID := uuid.New()
	expiry := 15 * time.Minute

	// Derive file_type from content_type
	fileType := "image"
	if strings.HasPrefix(contentType, "video/") {
		fileType = "video"
	}

	objectKey := fmt.Sprintf("user/%s/%s/original/%s", userID, mediaID, filename)

	presignedURL, err := s.blobStore.GeneratePresignedPutURL(ctx, objectKey, expiry)
	if err != nil {
		return nil, fmt.Errorf("generate presigned put url: %w", err)
	}

	media := &postgres.MediaAsset{
		ID:               mediaID,
		UploaderID:       userID,
		FileType:         fileType,
		MediaSubtype:     "general",
		MimeType:         contentType,
		FileSizeBytes:    0, // unknown at presign time
		StorageBucket:    s.blobStore.Bucket(),
		StorageKey:       objectKey,
		ProcessingStatus: "pending_upload",
		CreatedAt:        time.Now(),
	}

	if err := s.pgStore.CreateMedia(ctx, media); err != nil {
		return nil, fmt.Errorf("create media record: %w", err)
	}

	return &PresignedUploadResponse{
		UploadURL: presignedURL.String(),
		MediaID:   mediaID,
		ObjectKey: objectKey,
		ExpiresAt: time.Now().Add(expiry),
	}, nil
}

// normaliseUploadPurpose accepts only leases this service recognises.
//
// An allowlist, not passthrough: `upload_purpose` is a client-supplied string
// that decides whether an asset can later be reclaimed, so an arbitrary value
// must not become a lease. Anything unrecognised is stored as empty, which the
// insert maps to NULL — the permanently-protected state.
func normaliseUploadPurpose(purpose string) string {
	if purpose == postgres.UploadPurposeComposer {
		return purpose
	}
	return ""
}
