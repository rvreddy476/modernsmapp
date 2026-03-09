package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

const (
	defaultChunkSize     = 5 * 1024 * 1024 // 5 MB
	resumableUploadTTL   = 24 * time.Hour
)

// InitResumableUploadResponse is returned when initiating a multipart upload.
type InitResumableUploadResponse struct {
	UploadID  uuid.UUID `json:"upload_id"`
	MediaID   uuid.UUID `json:"media_id"`
	ChunkSize int       `json:"chunk_size"`
	ExpiresAt time.Time `json:"expires_at"`
}

// InitResumableUpload creates a media asset record and a resumable upload session.
func (s *Service) InitResumableUpload(ctx context.Context, userID uuid.UUID, fileType, mimeType string, totalBytes int64) (*InitResumableUploadResponse, error) {
	if err := ValidateUpload(fileType, "general", mimeType, totalBytes); err != nil {
		return nil, err
	}

	mediaID := uuid.New()
	objectKey := fmt.Sprintf("user/%s/%s/original", userID, mediaID)

	// Create the media asset record in pending_upload state
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
		return nil, fmt.Errorf("create media record: %w", err)
	}

	chunkSize := defaultChunkSize
	totalParts := int(totalBytes / int64(chunkSize))
	if totalBytes%int64(chunkSize) != 0 {
		totalParts++
	}

	expiresAt := time.Now().Add(resumableUploadTTL)

	upload := &postgres.ResumableUpload{
		MediaID:    mediaID,
		UploaderID: userID,
		TotalBytes: totalBytes,
		ChunkSize:  chunkSize,
		TotalParts: totalParts,
		Status:     "initiated",
		MimeType:   mimeType,
		ObjectKey:  objectKey,
		ExpiresAt:  expiresAt,
	}
	if err := s.pgStore.CreateResumableUpload(ctx, upload); err != nil {
		return nil, fmt.Errorf("create resumable upload: %w", err)
	}

	return &InitResumableUploadResponse{
		UploadID:  upload.UploadID,
		MediaID:   mediaID,
		ChunkSize: chunkSize,
		ExpiresAt: expiresAt,
	}, nil
}

// GetResumableUploadStatus returns the current state of a resumable upload session.
func (s *Service) GetResumableUploadStatus(ctx context.Context, uploadID uuid.UUID, userID uuid.UUID) (*postgres.ResumableUpload, error) {
	upload, err := s.pgStore.GetResumableUpload(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("upload not found: %w", err)
	}
	if upload.UploaderID != userID {
		return nil, fmt.Errorf("forbidden: you do not own this upload")
	}
	return upload, nil
}

// UploadChunkResponse is returned after a chunk is uploaded.
type UploadChunkResponse struct {
	UploadID      uuid.UUID `json:"upload_id"`
	UploadedBytes int64     `json:"uploaded_bytes"`
	TotalBytes    int64     `json:"total_bytes"`
	IsComplete    bool      `json:"is_complete"`
}

// UploadChunk records that a chunk has been uploaded. In a real implementation,
// the client uploads directly to S3/MinIO using presigned URLs for each part.
// This method just tracks progress.
func (s *Service) UploadChunk(ctx context.Context, uploadID uuid.UUID, userID uuid.UUID, chunkBytes int64) (*UploadChunkResponse, error) {
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
	if time.Now().After(upload.ExpiresAt) {
		return nil, fmt.Errorf("upload expired")
	}

	newTotal := upload.UploadedBytes + chunkBytes
	if newTotal > upload.TotalBytes {
		newTotal = upload.TotalBytes
	}

	status := "uploading"
	isComplete := newTotal >= upload.TotalBytes
	if isComplete {
		status = "completed"
	}

	if err := s.pgStore.UpdateResumableUploadProgress(ctx, uploadID, newTotal, status); err != nil {
		return nil, fmt.Errorf("update progress: %w", err)
	}

	return &UploadChunkResponse{
		UploadID:      uploadID,
		UploadedBytes: newTotal,
		TotalBytes:    upload.TotalBytes,
		IsComplete:    isComplete,
	}, nil
}

// CompleteResumableUpload finalizes a resumable upload and triggers processing.
func (s *Service) CompleteResumableUpload(ctx context.Context, uploadID uuid.UUID, userID uuid.UUID) (*postgres.MediaAsset, error) {
	upload, err := s.pgStore.GetResumableUpload(ctx, uploadID)
	if err != nil {
		return nil, fmt.Errorf("upload not found: %w", err)
	}
	if upload.UploaderID != userID {
		return nil, fmt.Errorf("forbidden")
	}

	if err := s.pgStore.CompleteResumableUpload(ctx, uploadID); err != nil {
		return nil, fmt.Errorf("complete upload record: %w", err)
	}

	// Trigger the same confirm flow as simple uploads
	return s.ConfirmUpload(ctx, upload.MediaID, userID)
}
