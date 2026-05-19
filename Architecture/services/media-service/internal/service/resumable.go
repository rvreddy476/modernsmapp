package service

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

const (
	// defaultChunkSize is also the S3/MinIO minimum part size — every part
	// except the last must be at least this large.
	defaultChunkSize   = 5 * 1024 * 1024 // 5 MB
	resumableUploadTTL = 24 * time.Hour
)

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
	if err := ValidateUpload(fileType, "general", mimeType, totalBytes); err != nil {
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

	chunkSize := defaultChunkSize
	totalParts := int(totalBytes / int64(chunkSize))
	if totalBytes%int64(chunkSize) != 0 {
		totalParts++
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
	*postgres.ResumableUpload
	UploadedParts []int `json:"uploaded_parts"`
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
	return &ResumableUploadStatus{ResumableUpload: upload, UploadedParts: nums}, nil
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
	if err := s.blobStore.CompleteMultipartUpload(ctx, upload.ObjectKey, upload.StorageUploadID, blobParts); err != nil {
		return nil, err
	}

	if err := s.pgStore.CompleteResumableUpload(ctx, uploadID); err != nil {
		return nil, fmt.Errorf("mark complete: %w", err)
	}

	// Same confirm flow as simple uploads — validates the object exists
	// and triggers image processing / video transcode.
	return s.ConfirmUpload(ctx, upload.MediaID, userID)
}
