// upload.go is the simple (single PUT) upload path: ValidateUpload's size
// and MIME rules, InitUpload's presigned PUT, ConfirmUpload's integrity
// checks and routing of each file type to its processing path, and the
// synchronous image pipeline (processImage, populateMediaURLs). The
// resumable path is resumable.go; both share checkUploadLimit. Source-scan
// tests read the ConfirmUpload..processImage span from this file
// (confirm_processing_coverage_test.go, transcode_outbox_guard_test.go).

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	ErrUploadForbidden    = errors.New("upload confirmation forbidden")
	ErrUploadObjectAbsent = errors.New("upload object is absent")
	ErrUploadSizeMismatch = errors.New("upload object size does not match the declared size")
	ErrUploadState        = errors.New("upload cannot be confirmed in its current state")
	ErrUploadIntegrity    = errors.New("upload metadata integrity check failed")
)

// checkUploadLimit is what InitUpload and InitResumableUpload call.
func (s *Service) checkUploadLimit(ctx context.Context, userID uuid.UUID) error {
	if s.uploadLimit != nil {
		return s.uploadLimit(ctx, userID)
	}
	return s.CheckUploadRateLimit(ctx, userID)
}

type InitUploadResponse struct {
	MediaID   uuid.UUID `json:"media_id"`
	UploadURL string    `json:"upload_url"`
	ObjectKey string    `json:"object_key"`
	ExpiresAt time.Time `json:"expires_at"`
	// UploadHeaders are the headers the PUT must carry, exactly: they are
	// signed into upload_url (Content-Type = the declared MIME,
	// Content-Length = the declared size; reels audit 2026-10-30, finding
	// 5). A PUT with any other value is refused by the object store.
	UploadHeaders map[string]string `json:"upload_headers"`
}

// presignedPutExpiry is how long a simple-upload PUT URL is valid. It is
// also the window in which the original can still be replaced by its
// uploader after confirm (originalUnchanged).
const presignedPutExpiry = 15 * time.Minute

// ValidateUpload checks size and mime constraints for the given file type and subtype.
func ValidateUpload(fileType, mediaSubtype, mimeType string, fileSizeBytes int64) error {
	// A document is checked whole, before the subtype rules below: a
	// "document" declaring subtype avatar must not take the avatar branch,
	// which only asks for an image/ prefix.
	if fileType == FileTypeDocument {
		return validateDocumentUpload(mediaSubtype, mimeType, fileSizeBytes)
	}
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
		// The size caps are not here: the simple path is bounded by
		// ValidateUploadSize (5 GiB single PUT), and both paths by the
		// worker's cap (validateVideoOriginalSize, ErrVideoTooLarge).
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
	if err := ValidateUploadSize(fileType, fileSizeBytes); err != nil {
		return nil, err
	}

	// MIME allow-list check
	if err := ValidateUploadMIME(mimeType, fileType); err != nil {
		return nil, err
	}

	// Per-user upload rate limit (Redis sliding window). A document is
	// validated whole first, so a refused document spends nothing, and then
	// counts against its own budget only (document.go).
	if fileType == FileTypeDocument {
		if err := ValidateUpload(fileType, mediaSubtype, mimeType, fileSizeBytes); err != nil {
			return nil, err
		}
		if err := s.checkDocumentUploadLimit(ctx, userID); err != nil {
			return nil, err
		}
		mediaSubtype = "general"
		mimeType = normaliseMIME(mimeType)
		// A document carries no description, and no composer lease: it is
		// never a reclamation candidate once confirmed.
		altText, decorative, uploadPurpose = "", false, ""
	} else if err := s.checkUploadLimit(ctx, userID); err != nil {
		return nil, err
	}

	// Subtype/file-type-specific size + MIME validation (existing)
	if err := ValidateUpload(fileType, mediaSubtype, mimeType, fileSizeBytes); err != nil {
		return nil, err
	}

	mediaID := uuid.New()
	storageKey := fmt.Sprintf("user/%s/%s/original", userID, mediaID)
	expiry := presignedPutExpiry

	// Content-Type and Content-Length are signed into the URL (finding 5):
	// the object store refuses a PUT of any other type or length, so the
	// holder cannot store text/html on the media origin.
	url, err := s.blobStore.GeneratePresignedPutURLFor(ctx, storageKey, expiry, mimeType, fileSizeBytes)
	if err != nil {
		return nil, err
	}
	signedHeaders, err := blob.PresignedPutHeaders(mimeType, fileSizeBytes)
	if err != nil {
		return nil, err
	}
	uploadHeaders := make(map[string]string, len(signedHeaders))
	for k := range signedHeaders {
		uploadHeaders[k] = signedHeaders.Get(k)
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
		MediaID:       mediaID,
		UploadURL:     url.String(),
		ObjectKey:     storageKey,
		ExpiresAt:     time.Now().Add(expiry),
		UploadHeaders: uploadHeaders,
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
	// The stored Content-Type must be the declared one (finding 5). The PUT
	// URL signs it now; this also catches an object written another way.
	if !uploadedTypeMatches(media, info) {
		if updateErr := s.pgStore.UpdateStatus(ctx, mediaID, "rejected"); updateErr != nil {
			return nil, fmt.Errorf("reject mistyped upload and persist status: %w", updateErr)
		}
		return nil, fmt.Errorf("%w: stored content type %q is not the declared %q",
			ErrUploadIntegrity, info.ContentType, media.MimeType)
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
	if !uploadHeaderValid(media.FileType, media.MimeType, headerData) {
		if updateErr := s.pgStore.UpdateStatus(ctx, mediaID, "rejected"); updateErr != nil {
			return nil, fmt.Errorf("reject invalid %s and persist status: %w", media.FileType, updateErr)
		}
		return nil, fmt.Errorf("invalid %s file: magic bytes do not match declared MIME type", media.FileType)
	}

	// 2. Update status to 'uploaded' and pin the upload-time identity
	// (P-14): upload_confirmed_at is set once, here, on both the single
	// PUT and the resumable path (CompleteResumableUpload ends here), with
	// the ETag the object store reported for the original.
	if err := s.pgStore.MarkUploadConfirmed(ctx, mediaID, info.ETag); err != nil {
		if errors.Is(err, postgres.ErrUploadAlreadyConfirmed) {
			// Another confirm moved the asset on between our read and this
			// write (finding 12). Answer what it is now, as the early
			// return above would have.
			return s.confirmedAnswer(ctx, mediaID)
		}
		return nil, err
	}

	// 3. Process based on file_type + media_subtype
	switch {
	// EVERY image takes the same synchronous pipeline: scan, then variants.
	//
	// This case used to name the subtypes it would process — general, avatar
	// and cover, with gif repeating the identical body in a case of its own —
	// and an image with any other subtype fell through the whole switch.
	// Nothing failed and nothing was logged: confirm answered 200 with the
	// asset left at 'uploaded'/'pending'.
	//
	// That is why chat attachments were refused on send. The reservation
	// requires ready+passed and correctly denied an asset that had never been
	// processed; the user saw only that the send failed. `post_image` uploads
	// were stuck the same way. The subtype says where an asset is USED; it has
	// never been a reason to skip scanning it.
	case media.FileType == "image":
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

	case media.FileType == FileTypeDocument:
		// No scan, no renditions, no transcode: the verified original is the
		// whole document (document.go). The ETag pinned above is what the
		// document-bytes route serves against, so a confirm without one is
		// not a confirm.
		if info.ETag == "" {
			return nil, fmt.Errorf("%w: the object store reported no ETag for a document", ErrUploadIntegrity)
		}
		if err := s.pgStore.MarkDocumentReady(ctx, mediaID); err != nil {
			if errors.Is(err, postgres.ErrDocumentNotConfirmable) {
				// A concurrent confirm finished it first: answer what it is.
				return s.confirmedAnswer(ctx, mediaID)
			}
			return nil, err
		}
		media.ProcessingStatus = "ready"
		media.ModerationStatus = "passed"

	case media.FileType == "video":
		// State and work intent are one PostgreSQL commit. Kafka is drained
		// from the outbox, so confirm never succeeds after merely hoping a
		// direct publish worked.
		if err := s.pgStore.QueueTranscode(ctx, media); err != nil {
			return nil, err
		}
		media.ProcessingStatus = "processing"

	default:
		// Unreachable while file_type is validated to image/video/audio/
		// document and each has a case above. If another type is added without one,
		// say so instead of returning 200 over an asset left at 'uploaded'
		// that every downstream gate will then refuse.
		return nil, fmt.Errorf("%w: no processing path for file_type=%q media_subtype=%q",
			ErrUploadState, media.FileType, media.MediaSubtype)
	}

	return media, nil
}

// confirmedAnswer is the idempotent answer to a confirm that lost the race
// to another one: the asset as it stands, or the state error.
func (s *Service) confirmedAnswer(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	switch media.ProcessingStatus {
	case "processing", "ready":
		return media, nil
	default:
		return nil, fmt.Errorf("%w: media is %s", ErrUploadState, media.ProcessingStatus)
	}
}

// uploadedTypeMatches: the stored object's Content-Type is the declared
// MIME (finding 5).
func uploadedTypeMatches(media *postgres.MediaAsset, info blob.ObjectInfo) bool {
	return sameMIME(info.ContentType, media.MimeType)
}

// uploadHeaderValid checks an upload's first bytes against its file type
// and, for a video, against the container its declared MIME names
// (finding 7): "video/mp4" carrying AVI or Matroska bytes passed the magic
// check before, and ffmpeg then probed whatever it was.
func uploadHeaderValid(fileType, declaredMIME string, head []byte) bool {
	valid := false
	switch fileType {
	case "video":
		_, valid = processing.ValidateVideoMagicBytes(head)
		valid = valid && processing.VideoContainerMatches(declaredMIME, head)
	case "image":
		_, valid = processing.ValidateImageMagicBytes(head)
	case "audio":
		_, valid = processing.ValidateAudioMagicBytes(head)
	case FileTypeDocument:
		valid = DocumentMagicMatches(declaredMIME, head)
	}
	return valid
}

// sameMIME compares two Content-Type values by media type alone,
// case-insensitively, ignoring parameters.
func sameMIME(a, b string) bool {
	norm := func(v string) string {
		return strings.ToLower(strings.TrimSpace(strings.Split(v, ";")[0]))
	}
	return norm(a) != "" && norm(a) == norm(b)
}

// processImage handles synchronous image processing (resize + upload variants).
func (s *Service) processImage(ctx context.Context, media *postgres.MediaAsset) error {
	moderationStatus := "passed"
	// Lane D6: the scanner's full label list is kept with the verdict, so
	// dating-service can review borderline photos the verdict let through.
	var scanLabels []processing.ModerationLabel
	scanned := false
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
		scanLabels, scanned = result.Labels, true
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
	// The worker replaced the original with its metadata-free encode
	// (security sweep #4); HEAD answers Content-Length from this row.
	if meta.OriginalSizeBytes > 0 && meta.OriginalSizeBytes != media.FileSizeBytes {
		if err := s.pgStore.UpdateMediaFileSize(ctx, media.ID, meta.OriginalSizeBytes); err != nil {
			return fmt.Errorf("update original size: %w", err)
		}
		media.FileSizeBytes = meta.OriginalSizeBytes
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
	if scanned {
		if scanLabels == nil {
			scanLabels = []processing.ModerationLabel{}
		}
		rawLabels, err := json.Marshal(scanLabels)
		if err != nil {
			return fmt.Errorf("encode image moderation labels: %w", err)
		}
		if err := s.pgStore.UpdateMediaModerationScan(ctx, media.ID, rawLabels, processing.ScannerName(s.scanner)); err != nil {
			return fmt.Errorf("persist image moderation labels: %w", err)
		}
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
