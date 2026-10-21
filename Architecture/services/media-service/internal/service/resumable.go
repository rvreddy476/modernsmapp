package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
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
	// take: 10,000 parts of 64 MiB = 625 GiB. Long videos have no size
	// limit of their own (founder, 2026-10-21); this is only the arithmetic
	// of S3 parts and Cloudflare's request cap.
	MaxResumableUploadBytes int64 = maxChunkSize * maxUploadParts

	resumableUploadTTL = 24 * time.Hour
)

// ErrResumableUploadTooLarge is returned when a file would need more than
// maxUploadParts chunks even at maxChunkSize.
var ErrResumableUploadTooLarge = errors.New("file too large for a resumable upload")

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

// InitResumableUploadResponse is returned when initiating a multipart upload.
type InitResumableUploadResponse struct {
	UploadID   uuid.UUID `json:"upload_id"`
	MediaID    uuid.UUID `json:"media_id"`
	ChunkSize  int       `json:"chunk_size"`
	TotalParts int       `json:"total_parts"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// InitResumableUpload creates a media asset record, opens an object-store
// multipart upload, and records a resumable upload session.
func (s *Service) InitResumableUpload(ctx context.Context, userID uuid.UUID, fileType, mimeType string, totalBytes int64) (*InitResumableUploadResponse, error) {
	// No total size cap for video here (founder, 2026-10-21); ValidateUpload
	// still caps images. The chunk plan refuses only what S3's part limit
	// cannot hold.
	//
	// NOTE: unlike InitUpload this path does not call CheckUploadRateLimit,
	// so resumable inits are not counted against the per-user hourly/daily
	// upload limits. Left as it was on purpose (2026-10-21); worth closing.
	if err := ValidateUpload(fileType, "general", mimeType, totalBytes); err != nil {
		return nil, err
	}
	chunkSize, totalParts, err := resumableChunkPlan(totalBytes)
	if err != nil {
		return nil, err
	}

	mediaID := uuid.New()
	objectKey := fmt.Sprintf("user/%s/%s/original", userID, mediaID)

	// Open the multipart upload on the object store first — if this fails
	// we never create the half-built media / session rows.
	storageUploadID, err := s.blobStore.InitMultipartUpload(ctx, objectKey, mimeType)
	if err != nil {
		return nil, fmt.Errorf("open multipart upload: %w", err)
	}

	media := &postgres.MediaAsset{
		ID:               mediaID,
		UploaderID:       userID,
		FileType:         fileType,
		MediaSubtype:     "general",
		MimeType:         mimeType,
		FileSizeBytes:    totalBytes,
		StorageBucket:    s.blobStore.Bucket(),
		StorageKey:       objectKey,
		ProcessingStatus: "pending_upload",
		CreatedAt:        time.Now(),
	}
	if err := s.pgStore.CreateMedia(ctx, media); err != nil {
		_ = s.blobStore.AbortMultipartUpload(ctx, objectKey, storageUploadID)
		return nil, fmt.Errorf("create media record: %w", err)
	}

	expiresAt := time.Now().Add(resumableUploadTTL)
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
	if err := s.pgStore.CreateResumableUpload(ctx, upload); err != nil {
		_ = s.blobStore.AbortMultipartUpload(ctx, objectKey, storageUploadID)
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
	upload, err := s.pgStore.GetResumableUpload(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("upload not found: %w", err)
	}
	if upload.UploaderID != userID {
		return nil, fmt.Errorf("forbidden: you do not own this upload")
	}
	parts, err := s.pgStore.ListUploadParts(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list parts: %w", err)
	}
	nums := make([]int, len(parts))
	for i, p := range parts {
		nums[i] = p.PartNumber
	}
	return &ResumableUploadStatus{
		UploadID: upload.UploadID, MediaID: upload.MediaID,
		TotalBytes: upload.TotalBytes, UploadedBytes: upload.UploadedBytes,
		ChunkSize: upload.ChunkSize, TotalParts: upload.TotalParts,
		Status: upload.Status, MimeType: upload.MimeType,
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
}

// UploadPart streams one part's bytes to the object store and records its
// ETag. Re-uploading a part number is safe — the ETag is overwritten and
// progress recomputed from the recorded parts.
func (s *Service) UploadPart(ctx context.Context, uploadID, userID uuid.UUID, partNumber int, data io.Reader, size int64) (*UploadChunkResponse, error) {
	upload, err := s.pgStore.GetResumableUpload(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("upload not found: %w", err)
	}
	if upload.UploaderID != userID {
		return nil, fmt.Errorf("forbidden")
	}
	if upload.Status == "completed" {
		return nil, fmt.Errorf("upload already completed")
	}
	if upload.Status == "expired" || time.Now().After(upload.ExpiresAt) {
		return nil, fmt.Errorf("upload expired")
	}
	if partNumber < 1 || partNumber > upload.TotalParts {
		return nil, fmt.Errorf("part_number %d out of range (1..%d)", partNumber, upload.TotalParts)
	}
	if size <= 0 {
		return nil, fmt.Errorf("empty part")
	}
	expectedSize := int64(upload.ChunkSize)
	if partNumber == upload.TotalParts {
		expectedSize = upload.TotalBytes - int64(upload.ChunkSize)*int64(upload.TotalParts-1)
	}
	if size != expectedSize {
		return nil, fmt.Errorf("part %d has %d bytes, expected %d", partNumber, size, expectedSize)
	}

	part, err := s.blobStore.UploadPart(ctx, upload.ObjectKey, upload.StorageUploadID, partNumber, data, size)
	if err != nil {
		return nil, err
	}
	if err := s.pgStore.RecordUploadPart(ctx, uploadID, partNumber, part.ETag, part.Size); err != nil {
		return nil, fmt.Errorf("record part: %w", err)
	}

	// Recompute progress from the recorded parts so a re-uploaded part
	// does not double-count.
	parts, err := s.pgStore.ListUploadParts(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list parts: %w", err)
	}
	var uploaded int64
	for _, p := range parts {
		uploaded += p.SizeBytes
	}
	if err := s.pgStore.UpdateResumableUploadProgress(ctx, uploadID, uploaded, "uploading"); err != nil {
		return nil, fmt.Errorf("update progress: %w", err)
	}

	return &UploadChunkResponse{
		UploadID:      uploadID,
		PartNumber:    partNumber,
		UploadedBytes: uploaded,
		TotalBytes:    upload.TotalBytes,
		AllPartsIn:    len(parts) >= upload.TotalParts,
	}, nil
}

// CompleteResumableUpload assembles the uploaded parts into the final
// object, then runs the same confirm / processing flow as a simple upload.
func (s *Service) CompleteResumableUpload(ctx context.Context, uploadID, userID uuid.UUID) (*postgres.MediaAsset, error) {
	upload, err := s.pgStore.GetResumableUpload(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("upload not found: %w", err)
	}
	if upload.UploaderID != userID {
		return nil, fmt.Errorf("forbidden")
	}
	if upload.Status == "completed" {
		// Idempotent — a retried complete just re-confirms.
		return s.ConfirmUpload(ctx, upload.MediaID, userID)
	}

	parts, err := s.pgStore.ListUploadParts(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("list parts: %w", err)
	}
	if len(parts) < upload.TotalParts {
		return nil, fmt.Errorf("incomplete: %d of %d parts uploaded", len(parts), upload.TotalParts)
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
	if err := s.blobStore.CompleteMultipartUpload(ctx, upload.ObjectKey, upload.StorageUploadID, blobParts); err != nil {
		info, statErr := s.blobStore.StatObject(ctx, upload.ObjectKey)
		if statErr != nil || info.Size != upload.TotalBytes {
			return nil, err
		}
	}

	if err := s.pgStore.CompleteResumableUpload(ctx, uploadID); err != nil {
		return nil, fmt.Errorf("mark complete: %w", err)
	}

	// Same confirm flow as simple uploads — validates the object exists
	// and triggers image processing / video transcode.
	return s.ConfirmUpload(ctx, upload.MediaID, userID)
}
