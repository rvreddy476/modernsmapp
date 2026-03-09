package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// FrameExtractionResponse contains presigned URLs for extracted frames.
type FrameExtractionResponse struct {
	MediaID uuid.UUID      `json:"media_id"`
	Frames  []FrameResult  `json:"frames"`
}

// FrameResult represents a single extracted frame with its URL.
type FrameResult struct {
	Index     int    `json:"index"`
	ObjectKey string `json:"object_key"`
	URL       string `json:"url"`
}

// ExtractFrames extracts cover frame candidates from a video and uploads them.
func (s *Service) ExtractFrames(ctx context.Context, mediaID uuid.UUID, userID uuid.UUID, numFrames int) (*FrameExtractionResponse, error) {
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, fmt.Errorf("media not found: %w", err)
	}
	if media.UploaderID != userID {
		return nil, fmt.Errorf("forbidden: you do not own this media")
	}
	if media.FileType != "video" {
		return nil, fmt.Errorf("frame extraction only supported for video media")
	}
	if numFrames <= 0 || numFrames > 10 {
		numFrames = 5
	}

	// Download original video
	videoData, err := s.blobStore.DownloadObject(ctx, media.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("download video: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "frames-"+mediaID.String())
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	inputPath := tmpDir + "/input"
	if err := os.WriteFile(inputPath, videoData, 0644); err != nil {
		return nil, fmt.Errorf("write temp: %w", err)
	}

	framePaths, err := processing.ExtractFrames(ctx, inputPath, tmpDir, numFrames)
	if err != nil {
		return nil, fmt.Errorf("extract frames: %w", err)
	}

	expiry := 15 * time.Minute
	var frames []FrameResult

	for i, fp := range framePaths {
		frameData, readErr := os.ReadFile(fp)
		if readErr != nil {
			continue
		}

		objectKey := fmt.Sprintf("user/%s/%s/frames/frame_%02d.jpg", media.UploaderID, mediaID, i+1)
		if uploadErr := s.blobStore.UploadObject(ctx, objectKey, frameData, "image/jpeg"); uploadErr != nil {
			continue
		}

		// Also create rendition records for frame thumbnails
		r := &postgres.MediaRendition{
			MediaID:       mediaID,
			RenditionType: "thumbnail",
			Quality:       fmt.Sprintf("frame_%02d", i+1),
			Status:        "completed",
			MaxRetries:    0,
		}
		w, h := 0, 0
		sz := int64(len(frameData))
		mime := "image/jpeg"
		_ = s.pgStore.CreateRendition(ctx, r)
		_ = s.pgStore.UpdateRenditionCompleted(ctx, r.ID, objectKey, mime, &w, &h, &sz, nil)

		url, urlErr := s.blobStore.GeneratePresignedGetURL(ctx, objectKey, expiry)
		urlStr := ""
		if urlErr == nil {
			urlStr = url.String()
		}

		frames = append(frames, FrameResult{
			Index:     i + 1,
			ObjectKey: objectKey,
			URL:       urlStr,
		})
	}

	return &FrameExtractionResponse{
		MediaID: mediaID,
		Frames:  frames,
	}, nil
}
