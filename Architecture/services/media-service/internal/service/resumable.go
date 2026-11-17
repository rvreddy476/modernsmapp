package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	mib = 1024 * 1024

	// minChunkSize is the S3/MinIO minimum part size — every part except
	// the last must be at least this large. Small uploads keep 5 MiB chunks.
	minChunkSize = 5 * mib
	// maxChunkSize bounds one chunk request. Every chunk crosses Cloudflare,
	// which refuses request bodies past ~100 MB, so 64 MiB leaves headroom.
	maxChunkSize = 64 * mib
	// maxUploadParts is S3's ceiling on parts in one multipart upload.
	maxUploadParts = 10_000
	// chunkTargetParts is the part count the chunk size aims for: the
	// chunk grows with the file so a long upload stays under 9,000 parts,
	// leaving 1,000 parts of headroom below maxUploadParts.
	chunkTargetParts = 9_000

	// MaxResumableUploadBytes is the largest file the resumable path can
	// take: 10,000 parts of 64 MiB = 625 GiB. This is only the arithmetic
	// of S3 parts and Cloudflare's request cap; a video is refused well
	// below it, at the worker's cap (ErrVideoTooLarge).
	MaxResumableUploadBytes int64 = maxChunkSize * maxUploadParts
)

// A session's life (lv-16): it expires uploadLimits.ResumableIdleTTL after
// its last accepted part (or its init), and never later than
// uploadLimits.ResumableMaxLifetime after init. expires_at in
// resumable_uploads is the one rule: UploadPart and complete slide it
// (TouchResumableUpload), and the expiry sweep (ExpireResumableUploads) and
// orphan GC (OpenResumableMediaIDs) read it.

var (
	// ErrResumableUploadTooLarge is returned when a file would need more
	// than maxUploadParts chunks even at maxChunkSize.
	ErrResumableUploadTooLarge = errors.New("file too large for a resumable upload")
	// ErrResumableNotFound: no session with this id for this caller
	// (someone else's answers the same). 404 NOT_FOUND.
	ErrResumableNotFound = errors.New("upload not found")
	// ErrResumableExpired: the session passed its expiry (idle too long,
	// or its absolute ceiling). 410 UPLOAD_EXPIRED; start a new upload.
	ErrResumableExpired = errors.New("upload expired")
	// ErrResumableBadPart: a part number out of range, or a part of the
	// wrong size. 400 BAD_REQUEST.
	ErrResumableBadPart = errors.New("invalid part")
	// ErrResumableIncomplete: complete before every part landed.
	// 409 UPLOAD_INCOMPLETE; status lists the parts held.
	ErrResumableIncomplete = errors.New("upload incomplete")
	// ErrResumableCompleted: a part sent to a completed session.
	ErrResumableCompleted = errors.New("upload already completed")
)

// resumableChunkPlan sizes a resumable upload's chunks:
//
//	chunk = max(5 MiB, ceil(total / 9000)), rounded up to a whole MiB,
//	        at most 64 MiB
//	parts = ceil(total / chunk)
//
// and refuses a file whose parts would exceed 10,000 at 64 MiB (≈ 625 GiB).
// Clients must upload in the returned chunk size, not assume 5 MiB.
func resumableChunkPlan(totalBytes int64) (chunkSize, totalParts int, err error) {
	if totalBytes <= 0 {
		return 0, 0, fmt.Errorf("total_bytes must be positive")
	}
	chunk := (totalBytes + chunkTargetParts - 1) / chunkTargetParts // ceil(total / 9000)
	chunk = (chunk + mib - 1) / mib * mib                           // up to a whole MiB
	if chunk < minChunkSize {
		chunk = minChunkSize
	}
	if chunk > maxChunkSize {
		chunk = maxChunkSize
	}
	parts := (totalBytes + chunk - 1) / chunk
	if parts > maxUploadParts {
		return 0, 0, fmt.Errorf("%w: %d bytes needs %d parts of %d MiB (max %d parts, %d bytes)",
			ErrResumableUploadTooLarge, totalBytes, parts, chunk/mib, maxUploadParts, MaxResumableUploadBytes)
	}
	return int(chunk), int(parts), nil
}

// checkResumableVideoBytes is the worker's cap (validateVideoOriginalSize)
// applied to a video session after init: to the size it declared and to
// the bytes received so far (receivedBytes). Init already refused a
// declared size over the cap; this catches a session opened under a larger
// cap, so the creator learns it at the next chunk, not after the last one
// and a failed transcode. A non-video session is not the worker's.
func checkResumableVideoBytes(upload *postgres.ResumableUpload, receivedBytes int64) error {
	if upload == nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(upload.MimeType)), "video/") {
		return nil
	}
	if err := validateVideoOriginalSize(upload.TotalBytes); err != nil {
		return err
	}
	return validateVideoOriginalSize(receivedBytes)
}

// resumableDB is the session state the resumable flow needs
// (*postgres.MediaAssetStore in production; a fake in tests).
type resumableDB interface {
	CreateMedia(ctx context.Context, m *postgres.MediaAsset) error
	CreateResumableUpload(ctx context.Context, u *postgres.ResumableUpload) error
	GetResumableUpload(ctx context.Context, uploadID uuid.UUID) (*postgres.ResumableUpload, error)
	ListUploadParts(ctx context.Context, uploadID uuid.UUID) ([]postgres.UploadPartRecord, error)
	RecordUploadPart(ctx context.Context, uploadID uuid.UUID, partNumber int, etag string, sizeBytes int64) error
	UpdateResumableUploadProgress(ctx context.Context, uploadID uuid.UUID, uploadedBytes int64, status string) error
	CompleteResumableUpload(ctx context.Context, uploadID uuid.UUID) error
	TouchResumableUpload(ctx context.Context, uploadID uuid.UUID, idleTTL, maxLifetime time.Duration) (time.Time, error)
}

// multipartBlobs is the object store's multipart API (*blob.Store).
type multipartBlobs interface {
	Bucket() string
	InitMultipartUpload(ctx context.Context, objectKey, contentType string) (string, error)
	AbortMultipartUpload(ctx context.Context, objectKey, storageUploadID string) error
	UploadPart(ctx context.Context, objectKey, storageUploadID string, partNumber int, data io.Reader, size int64) (blob.MultipartPart, error)
	CompleteMultipartUpload(ctx context.Context, objectKey, storageUploadID string, parts []blob.MultipartPart) error
	StatObject(ctx context.Context, objectKey string) (blob.ObjectInfo, error)
}

// resumableFlow is the resumable upload logic over its two stores, so the
// server half can be tested without PostgreSQL or S3 (reels-13).
type resumableFlow struct {
	db      resumableDB
	blobs   multipartBlobs
	limits  UploadLimits
	now     func() time.Time
	confirm func(ctx context.Context, mediaID, userID uuid.UUID) (*postgres.MediaAsset, error)
	// admit runs the init-time checks that need the caller's account
	// (upload count, daily budget); nil admits.
	admit func(ctx context.Context, userID uuid.UUID, fileType string, totalBytes int64, o initOptions) error
}

func (s *Service) resumable() *resumableFlow {
	return &resumableFlow{
		db:      s.pgStore,
		blobs:   s.blobStore,
		limits:  uploadLimits,
		now:     time.Now,
		confirm: s.ConfirmUpload,
		admit: func(ctx context.Context, userID uuid.UUID, fileType string, totalBytes int64, o initOptions) error {
			// Security sweep #20: a resumable init counts against the same
			// per-user hourly/daily upload limits as InitUpload.
			if err := s.checkUploadLimit(ctx, userID); err != nil {
				return err
			}
			if fileType == "video" {
				return s.checkVideoBudgetAtInit(ctx, userID, totalBytes, o.declaredDurationMs)
			}
			return nil
		},
	}
}

// InitResumableUploadResponse is returned when initiating a multipart upload.
type InitResumableUploadResponse struct {
	UploadID   uuid.UUID `json:"upload_id"`
	MediaID    uuid.UUID `json:"media_id"`
	ChunkSize  int       `json:"chunk_size"`
	TotalParts int       `json:"total_parts"`
	// ExpiresAt slides: each accepted part moves it to at least
	// now + MEDIA_RESUMABLE_IDLE_TTL, up to MEDIA_RESUMABLE_MAX_LIFETIME
	// after init. Clients should keep the latest value from a chunk or
	// status answer, not this first one.
	ExpiresAt time.Time `json:"expires_at"`
}

// InitResumableUpload creates a media asset record, opens an object-store
// multipart upload, and records a resumable upload session.
func (s *Service) InitResumableUpload(ctx context.Context, userID uuid.UUID, fileType, mimeType string, totalBytes int64, opts ...InitOption) (*InitResumableUploadResponse, error) {
	return s.resumable().init(ctx, userID, fileType, mimeType, totalBytes, collectInitOptions(opts))
}

func (f *resumableFlow) init(ctx context.Context, userID uuid.UUID, fileType, mimeType string, totalBytes int64, o initOptions) (*InitResumableUploadResponse, error) {
	// A video is capped at what the worker transcodes (ErrVideoTooLarge,
	// processing.MaxVideoOriginalBytes): the chunk plan alone would take
	// 625 GiB that the worker then refuses after the whole upload.
	// ValidateUpload still caps images.
	//
	// A document is a simple upload only: its budget, its confirm path and its
	// size cap are the init route's (document.go). The handler binds
	// image|video; this is the backstop.
	if fileType == FileTypeDocument {
		return nil, fmt.Errorf("a document cannot use the resumable upload")
	}
	if fileType == "video" {
		if err := validateVideoOriginalSize(totalBytes); err != nil {
			return nil, err
		}
		// lv-30: a declared length over the worker's cap is refused before
		// a byte is sent; confirm checks the probed length again.
		if err := validateVideoDurationMs(o.declaredDurationMs); err != nil {
			return nil, err
		}
	}
	if err := ValidateUpload(fileType, "general", mimeType, totalBytes); err != nil {
		return nil, err
	}
	if f.admit != nil {
		if err := f.admit(ctx, userID, fileType, totalBytes, o); err != nil {
			return nil, err
		}
	}
	chunkSize, totalParts, err := resumableChunkPlan(totalBytes)
	if err != nil {
		return nil, err
	}

	mediaID := uuid.New()
	objectKey := fmt.Sprintf("user/%s/%s/original", userID, mediaID)

	// Open the multipart upload on the object store first — if this fails
	// we never create the half-built media / session rows.
	storageUploadID, err := f.blobs.InitMultipartUpload(ctx, objectKey, mimeType)
	if err != nil {
		return nil, fmt.Errorf("open multipart upload: %w", err)
	}

	now := f.now()
	media := &postgres.MediaAsset{
		ID:               mediaID,
		UploaderID:       userID,
		FileType:         fileType,
		MediaSubtype:     "general",
		MimeType:         mimeType,
		FileSizeBytes:    totalBytes,
		StorageBucket:    f.blobs.Bucket(),
		StorageKey:       objectKey,
		ProcessingStatus: "pending_upload",
		CreatedAt:        now,
	}
	if err := f.db.CreateMedia(ctx, media); err != nil {
		_ = f.blobs.AbortMultipartUpload(ctx, objectKey, storageUploadID)
		return nil, fmt.Errorf("create media record: %w", err)
	}

	expiresAt := now.Add(f.limits.ResumableIdleTTL)
	upload := &postgres.ResumableUpload{
		MediaID:         mediaID,
		UploaderID:      userID,
		TotalBytes:      totalBytes,
		ChunkSize:       chunkSize,
		TotalParts:      totalParts,
		Status:          "initiated",
		MimeType:        mimeType,
		ObjectKey:       objectKey,
		StorageUploadID: storageUploadID,
		ExpiresAt:       expiresAt,
	}
	if err := f.db.CreateResumableUpload(ctx, upload); err != nil {
		_ = f.blobs.AbortMultipartUpload(ctx, objectKey, storageUploadID)
		return nil, fmt.Errorf("create resumable upload: %w", err)
	}

	return &InitResumableUploadResponse{
		UploadID:   upload.UploadID,
		MediaID:    mediaID,
		ChunkSize:  chunkSize,
		TotalParts: totalParts,
		ExpiresAt:  expiresAt,
	}, nil
}

// ResumableUploadStatus is the GET-status payload: the session plus the
// part numbers already stored, so a resuming client can skip them.
type ResumableUploadStatus struct {
	UploadID      uuid.UUID `json:"upload_id"`
	MediaID       uuid.UUID `json:"media_id"`
	TotalBytes    int64     `json:"total_bytes"`
	UploadedBytes int64     `json:"uploaded_bytes"`
	ChunkSize     int       `json:"chunk_size"`
	TotalParts    int       `json:"total_parts"`
	Status        string    `json:"status"`
	MimeType      string    `json:"mime_type"`
	ExpiresAt     time.Time `json:"expires_at"`
	UploadedParts []int     `json:"uploaded_parts"`
}

// GetResumableUploadStatus returns the current state of a resumable upload
// session, including which parts have already landed.
func (s *Service) GetResumableUploadStatus(ctx context.Context, uploadID, userID uuid.UUID) (*ResumableUploadStatus, error) {
	return s.resumable().status(ctx, uploadID, userID)
}

// owned reads a session and refuses one that is missing or someone else's
// with the same ErrResumableNotFound.
func (f *resumableFlow) owned(ctx context.Context, uploadID, userID uuid.UUID) (*postgres.ResumableUpload, error) {
	upload, err := f.db.GetResumableUpload(ctx, uploadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrResumableNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	if upload.UploaderID != userID {
		return nil, ErrResumableNotFound
	}
	return upload, nil
}

func (f *resumableFlow) status(ctx context.Context, uploadID, userID uuid.UUID) (*ResumableUploadStatus, error) {
	upload, err := f.owned(ctx, uploadID, userID)
	if err != nil {
		return nil, err
	}
	parts, err := f.db.ListUploadParts(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list parts: %w", err)
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		nums[i] = p.PartNumber
	}
	status := upload.Status
	// Past its expiry but not yet swept: say expired, as the next part
	// would be refused.
	if (status == "initiated" || status == "uploading") && !f.now().Before(upload.ExpiresAt) {
		status = "expired"
	}
	return &ResumableUploadStatus{
		UploadID: upload.UploadID, MediaID: upload.MediaID,
		TotalBytes: upload.TotalBytes, UploadedBytes: upload.UploadedBytes,
		ChunkSize: upload.ChunkSize, TotalParts: upload.TotalParts,
		Status: status, MimeType: upload.MimeType,
		ExpiresAt: upload.ExpiresAt, UploadedParts: nums,
	}, nil
}

// UploadChunkResponse is returned after a part is uploaded.
type UploadChunkResponse struct {
	UploadID      uuid.UUID `json:"upload_id"`
	PartNumber    int       `json:"part_number"`
	UploadedBytes int64     `json:"uploaded_bytes"`
	TotalBytes    int64     `json:"total_bytes"`
	AllPartsIn    bool      `json:"all_parts_in"`
	// ExpiresAt is the session's expiry after this part (it slides).
	ExpiresAt time.Time `json:"expires_at"`
}

// UploadPart streams one part's bytes to the object store and records its
// ETag. Re-uploading a part number is safe — the ETag is overwritten and
// progress recomputed from the recorded parts.
func (s *Service) UploadPart(ctx context.Context, uploadID, userID uuid.UUID, partNumber int, data io.Reader, size int64) (*UploadChunkResponse, error) {
	return s.resumable().uploadPart(ctx, uploadID, userID, partNumber, data, size)
}

// touch slides an open session's expiry, or reports why it cannot.
func (f *resumableFlow) touch(ctx context.Context, upload *postgres.ResumableUpload) (time.Time, error) {
	exp, err := f.db.TouchResumableUpload(ctx, upload.UploadID, f.limits.ResumableIdleTTL, f.limits.ResumableMaxLifetime)
	if errors.Is(err, pgx.ErrNoRows) {
		// Not open any more: completed by a racing complete, or expired.
		cur, readErr := f.db.GetResumableUpload(ctx, upload.UploadID)
		if readErr == nil && cur.Status == "completed" {
			return time.Time{}, ErrResumableCompleted
		}
		return time.Time{}, ErrResumableExpired
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("extend upload: %w", err)
	}
	return exp, nil
}

func (f *resumableFlow) uploadPart(ctx context.Context, uploadID, userID uuid.UUID, partNumber int, data io.Reader, size int64) (*UploadChunkResponse, error) {
	upload, err := f.owned(ctx, uploadID, userID)
	if err != nil {
		return nil, err
	}
	if upload.Status == "completed" {
		return nil, ErrResumableCompleted
	}
	if upload.Status == "expired" || upload.Status == "aborted" || !f.now().Before(upload.ExpiresAt) {
		return nil, ErrResumableExpired
	}
	if partNumber < 1 || partNumber > upload.TotalParts {
		return nil, fmt.Errorf("%w: part_number %d out of range (1..%d)", ErrResumableBadPart, partNumber, upload.TotalParts)
	}
	if size <= 0 {
		return nil, fmt.Errorf("%w: empty part", ErrResumableBadPart)
	}
	expectedSize := int64(upload.ChunkSize)
	if partNumber == upload.TotalParts {
		expectedSize = upload.TotalBytes - int64(upload.ChunkSize)*int64(upload.TotalParts-1)
	}
	if size != expectedSize {
		return nil, fmt.Errorf("%w: part %d has %d bytes, expected %d", ErrResumableBadPart, partNumber, size, expectedSize)
	}
	// The cap again, on the bytes this part would bring the file to: a
	// session opened before the cap was lowered (or before it existed)
	// stops here, not after the last part.
	if err := checkResumableVideoBytes(upload, int64(upload.ChunkSize)*int64(partNumber-1)+size); err != nil {
		return nil, err
	}
	// lv-16: an accepted part is activity. Slide the expiry BEFORE the
	// bytes move, so the expiry sweep cannot abort the multipart upload
	// under a part that is being written.
	expiresAt, err := f.touch(ctx, upload)
	if err != nil {
		return nil, err
	}

	part, err := f.blobs.UploadPart(ctx, upload.ObjectKey, upload.StorageUploadID, partNumber, data, size)
	if err != nil {
		return nil, err
	}
	if err := f.db.RecordUploadPart(ctx, uploadID, partNumber, part.ETag, part.Size); err != nil {
		return nil, fmt.Errorf("record part: %w", err)
	}

	// Recompute progress from the recorded parts so a re-uploaded part
	// does not double-count.
	parts, err := f.db.ListUploadParts(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list parts: %w", err)
	}
	var uploaded int64
	for _, p := range parts {
		uploaded += p.SizeBytes
	}
	if err := f.db.UpdateResumableUploadProgress(ctx, uploadID, uploaded, "uploading"); err != nil {
		return nil, fmt.Errorf("update progress: %w", err)
	}

	return &UploadChunkResponse{
		UploadID:      uploadID,
		PartNumber:    partNumber,
		UploadedBytes: uploaded,
		TotalBytes:    upload.TotalBytes,
		AllPartsIn:    len(parts) >= upload.TotalParts,
		ExpiresAt:     expiresAt,
	}, nil
}

// CompleteResumableUpload assembles the uploaded parts into the final
// object, then runs the same confirm / processing flow as a simple upload.
func (s *Service) CompleteResumableUpload(ctx context.Context, uploadID, userID uuid.UUID) (*postgres.MediaAsset, error) {
	return s.resumable().complete(ctx, uploadID, userID)
}

func (f *resumableFlow) complete(ctx context.Context, uploadID, userID uuid.UUID) (*postgres.MediaAsset, error) {
	upload, err := f.owned(ctx, uploadID, userID)
	if err != nil {
		return nil, err
	}
	if upload.Status == "completed" {
		// Idempotent — a retried complete just re-confirms.
		return f.confirm(ctx, upload.MediaID, userID)
	}
	if upload.Status == "expired" || upload.Status == "aborted" {
		return nil, ErrResumableExpired
	}

	parts, err := f.db.ListUploadParts(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list parts: %w", err)
	}
	if len(parts) < upload.TotalParts {
		return nil, fmt.Errorf("%w: %d of %d parts uploaded", ErrResumableIncomplete, len(parts), upload.TotalParts)
	}
	var received int64
	for _, p := range parts {
		received += p.SizeBytes
	}
	if received != upload.TotalBytes {
		return nil, fmt.Errorf("%w: %d of %d bytes received", ErrResumableIncomplete, received, upload.TotalBytes)
	}
	if err := checkResumableVideoBytes(upload, received); err != nil {
		return nil, err
	}
	// Hold the session open while the object store assembles it: the
	// expiry sweep only takes a session past its expiry, and this moves it
	// on. A session that already expired (or was swept a moment ago) is
	// refused here, before the assembly, so complete and the sweep's abort
	// never both act on one multipart upload. A racing complete that won
	// leaves it 'completed': answer as the idempotent path does.
	if _, err := f.touch(ctx, upload); err != nil {
		if errors.Is(err, ErrResumableCompleted) {
			return f.confirm(ctx, upload.MediaID, userID)
		}
		return nil, err
	}

	blobParts := make([]blob.MultipartPart, len(parts))
	for i, p := range parts {
		blobParts[i] = blob.MultipartPart{PartNumber: p.PartNumber, ETag: p.ETag, Size: p.SizeBytes}
	}
	// Timing: one CompleteMultipartUpload call for up to 10,000 parts. S3
	// usually answers in seconds but documents that a large assembly "could
	// take several minutes", which is past Cloudflare's ~100 s origin
	// timeout; the server's own write timeout is 10 min (cmd/server). If the
	// edge gives up first, S3 still finishes, and the client's retry lands
	// here with the upload already assembled: S3 then answers NoSuchUpload.
	// The object at this key can only have come from this upload (the key
	// embeds the media id), so an object of exactly the declared size means
	// an earlier complete succeeded — carry on to confirm, which re-checks
	// size and magic bytes.
	if err := f.blobs.CompleteMultipartUpload(ctx, upload.ObjectKey, upload.StorageUploadID, blobParts); err != nil {
		info, statErr := f.blobs.StatObject(ctx, upload.ObjectKey)
		if statErr != nil || info.Size != upload.TotalBytes {
			return nil, err
		}
	}

	if err := f.db.CompleteResumableUpload(ctx, uploadID); err != nil {
		return nil, fmt.Errorf("mark complete: %w", err)
	}

	// Same confirm flow as simple uploads — validates the object exists
	// and triggers image processing / video transcode.
	return f.confirm(ctx, upload.MediaID, userID)
}
