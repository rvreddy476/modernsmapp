package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/captions"
	"github.com/atpost/media-service/internal/config"
	mediaEvents "github.com/atpost/media-service/internal/events"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Size limits per subtype/file_type
const (
	MaxImageSize  int64 = 20 * 1024 * 1024  // 20 MB
	MaxVideoSize  int64 = 2 * 1024 * 1024 * 1024 // 2 GB
	MaxAvatarSize int64 = 10 * 1024 * 1024  // 10 MB
	MaxCoverSize  int64 = 10 * 1024 * 1024  // 10 MB
	MaxGIFSize    int64 = 15 * 1024 * 1024  // 15 MB

	defaultURLExpiry = 15 * time.Minute
)

type Service struct {
	pgStore   *postgres.MediaAssetStore
	blobStore *blob.Store
	producer  *mediaEvents.Producer // optional, nil = skip Kafka
	cfg       *config.Config
	scanner   processing.Scanner
	rdb       *redis.Client // optional, nil = skip rate limiting
	captions  captions.Backend
}

func New(pg *postgres.MediaAssetStore, blobStore *blob.Store) *Service {
	return &Service{
		pgStore:   pg,
		blobStore: blobStore,
		cfg:       config.Load(),
		scanner:   &processing.StubScanner{},
		captions:  captions.SelectBackend(),
	}
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
	return &Service{
		pgStore:   pg,
		blobStore: blobStore,
		cfg:       cfg,
		scanner:   scanner,
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
	default:
		return fmt.Errorf("unknown file type: %s", fileType)
	}
	return nil
}

func (s *Service) InitUpload(ctx context.Context, userID uuid.UUID, fileType, mediaSubtype, mimeType string, fileSizeBytes int64, altText string) (*InitUploadResponse, error) {
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
		CreatedAt:        time.Now(),
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
		return nil, fmt.Errorf("forbidden: you do not own this media")
	}

	// Magic-bytes validation: download first 16 bytes and verify file signature
	headerData, err := s.blobStore.DownloadObject(ctx, media.StorageKey)
	if err == nil && len(headerData) >= 16 {
		switch media.FileType {
		case "video":
			if _, valid := processing.ValidateVideoMagicBytes(headerData[:min(len(headerData), 64)]); !valid {
				_ = s.pgStore.UpdateStatus(ctx, mediaID, "rejected")
				return nil, fmt.Errorf("invalid video file: magic bytes do not match declared MIME type")
			}
		case "image":
			if _, valid := processing.ValidateImageMagicBytes(headerData[:min(len(headerData), 64)]); !valid {
				_ = s.pgStore.UpdateStatus(ctx, mediaID, "rejected")
				return nil, fmt.Errorf("invalid image file: magic bytes do not match declared MIME type")
			}
		}
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

	case media.FileType == "video":
		// Set to processing — video transcoding is async
		if err := s.pgStore.UpdateStatus(ctx, mediaID, "processing"); err != nil {
			return nil, err
		}
		media.ProcessingStatus = "processing"
		// Emit MediaTranscodeRequested to Kafka for the worker to pick up
		if s.producer != nil {
			if err := s.producer.PublishTranscodeRequested(ctx, media.ID, media.UploaderID, media.StorageKey, media.MimeType); err != nil {
				slog.Warn("Failed to publish transcode event", "media_id", media.ID, "error", err)
			}
		}
	}

	return media, nil
}

// processImage handles synchronous image processing (resize + upload variants).
func (s *Service) processImage(ctx context.Context, media *postgres.MediaAsset) error {
	// Content safety scan for images
	if s.cfg.ScannerEnabled && isImage(media.MimeType) {
		imageData, err := s.blobStore.DownloadObject(ctx, media.StorageKey)
		if err != nil {
			slog.Warn("media: failed to download image for scanning, skipping scan",
				"media_id", media.ID, "error", err)
		} else {
			result, err := s.scanner.ScanImage(ctx, imageData)
			if err != nil {
				slog.Warn("media: scanner error, skipping scan", "media_id", media.ID, "error", err)
			} else if !result.IsSafe {
				slog.Warn("media: content rejected by scanner",
					"media_id", media.ID, "reason", result.Reason, "score", result.Score)
				_ = s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
				return fmt.Errorf("media rejected: %s", result.Reason)
			}
		}
	}

	outputs, meta, err := processing.ProcessImage(
		ctx, s.blobStore, media.StorageKey,
		media.ID.String(), media.UploaderID.String(),
	)
	if err != nil {
		return fmt.Errorf("process image: %w", err)
	}

	// Update media metadata (including blurhash)
	if err := s.pgStore.UpdateMediaMeta(ctx, media.ID, meta.Width, meta.Height, meta.Blurhash, nil); err != nil {
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
	MediaID  uuid.UUID         `json:"media_id"`
	FileType string            `json:"kind"`
	Status   string            `json:"status"`
	Width    *int              `json:"width,omitempty"`
	Height   *int              `json:"height,omitempty"`
	Blurhash *string           `json:"blurhash,omitempty"`
	Variants map[string]string `json:"variants"`
	HLSURL   string            `json:"hls_url,omitempty"`
}

// GetMediaURL generates presigned GET URLs for a media item and all its variants.
func (s *Service) GetMediaURL(ctx context.Context, mediaID uuid.UUID) (*MediaURLResponse, error) {
	media, err := s.pgStore.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		return nil, err
	}

	expiry := 15 * time.Minute
	variants := make(map[string]string)

	// Original
	origURL, err := s.blobStore.GeneratePresignedGetURL(ctx, media.StorageKey, expiry)
	if err == nil {
		variants["original"] = origURL.String()
	}

	// Each variant
	for _, v := range media.Variants {
		vURL, err := s.blobStore.GeneratePresignedGetURL(ctx, v.ObjectKey, expiry)
		if err == nil {
			variants[v.Name] = vURL.String()
		}
	}

	response := &MediaURLResponse{
		MediaID:  media.ID,
		FileType: media.FileType,
		Status:   media.ProcessingStatus,
		Width:    media.Width,
		Height:   media.Height,
		Blurhash: media.Blurhash,
		Variants: variants,
	}

	// Include HLS URL when available
	if media.HLSMasterKey != "" {
		hlsURL, err := s.blobStore.GeneratePresignedGetURL(ctx, media.HLSMasterKey, expiry)
		if err == nil {
			response.HLSURL = hlsURL.String()
		}
	}

	return response, nil
}

// GetMediaVariantURL generates a presigned GET URL for a specific variant.
func (s *Service) GetMediaVariantURL(ctx context.Context, mediaID uuid.UUID, variant string) (string, error) {
	if variant == "original" {
		media, err := s.pgStore.GetMedia(ctx, mediaID)
		if err != nil {
			return "", err
		}
		u, err := s.blobStore.GeneratePresignedGetURL(ctx, media.StorageKey, 15*time.Minute)
		if err != nil {
			return "", err
		}
		return u.String(), nil
	}

	variants, err := s.pgStore.GetVariants(ctx, mediaID)
	if err != nil {
		return "", err
	}
	for _, v := range variants {
		if v.Name == variant {
			u, err := s.blobStore.GeneratePresignedGetURL(ctx, v.ObjectKey, 15*time.Minute)
			if err != nil {
				return "", err
			}
			return u.String(), nil
		}
	}
	return "", fmt.Errorf("variant %q not found", variant)
}

// BatchMediaURLs returns presigned URLs for multiple media items.
func (s *Service) BatchMediaURLs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*MediaURLResponse, error) {
	if len(ids) > 50 {
		return nil, fmt.Errorf("batch limit is 50 media items")
	}

	medias, err := s.pgStore.GetMediaBatch(ctx, ids)
	if err != nil {
		return nil, err
	}

	expiry := 15 * time.Minute
	result := make(map[uuid.UUID]*MediaURLResponse, len(medias))

	for _, m := range medias {
		variants := make(map[string]string)
		origURL, err := s.blobStore.GeneratePresignedGetURL(ctx, m.StorageKey, expiry)
		if err == nil {
			variants["original"] = origURL.String()
		}
		for _, v := range m.Variants {
			vURL, err := s.blobStore.GeneratePresignedGetURL(ctx, v.ObjectKey, expiry)
			if err == nil {
				variants[v.Name] = vURL.String()
			}
		}
		resp := &MediaURLResponse{
			MediaID:  m.ID,
			FileType: m.FileType,
			Status:   m.ProcessingStatus,
			Width:    m.Width,
			Height:   m.Height,
			Blurhash: m.Blurhash,
			Variants: variants,
		}
		if m.HLSMasterKey != "" {
			hlsURL, err := s.blobStore.GeneratePresignedGetURL(ctx, m.HLSMasterKey, expiry)
			if err == nil {
				resp.HLSURL = hlsURL.String()
			}
		}
		result[m.ID] = resp
	}

	return result, nil
}

// ─── Delete ────────────────────────────────────────────────────────

// DeleteMedia verifies ownership, removes blobs from storage, then deletes the DB record.
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
type MediaStatusResponse struct {
	MediaID          uuid.UUID                 `json:"media_id"`
	ProcessingStatus string                    `json:"processing_status"`
	FileType         string                    `json:"file_type"`
	Width            *int                      `json:"width,omitempty"`
	Height           *int                      `json:"height,omitempty"`
	DurationSeconds  *int                      `json:"duration_seconds,omitempty"`
	TranscodingJobs  []postgres.TranscodingJob `json:"transcoding_jobs,omitempty"`
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
		FileType:         media.FileType,
		Width:            media.Width,
		Height:           media.Height,
		DurationSeconds:  media.DurationSeconds,
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
