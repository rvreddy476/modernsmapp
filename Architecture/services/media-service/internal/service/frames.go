package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// FrameExtractionResponse contains presigned URLs for extracted frames.
type FrameExtractionResponse struct {
	MediaID uuid.UUID     `json:"media_id"`
	Frames  []FrameResult `json:"frames"`
}

// FrameResult represents a single extracted frame with its URL.
type FrameResult struct {
	Index     int    `json:"index"`
	ObjectKey string `json:"object_key"`
	URL       string `json:"url"`
}

// MaxFrameSourceBytes is the largest original POST /v1/media/:id/frames
// will copy into the API pod (2 GiB). Larger videos pick a cover in the
// client (the web hub captures frames in the browser) or from the worker's
// thumbnails; the route answers 413 FRAME_SOURCE_TOO_LARGE.
const MaxFrameSourceBytes int64 = 2 << 30

// frameToolTimeout bounds one frame-extraction ffmpeg in the API pod. The
// seek is an input seek (-ss before -i), so one frame is seconds of work.
const frameToolTimeout = time.Minute

// ErrFrameSourceTooLarge: the original is over MaxFrameSourceBytes (413).
var ErrFrameSourceTooLarge = errors.New("video is too large for server-side frame extraction")

// downloadFrameSource copies at most MaxFrameSourceBytes of the object to
// path, and refuses an object larger than that without finishing the copy.
func (s *Service) downloadFrameSource(ctx context.Context, key, path string) error {
	rc, info, err := s.blobStore.OpenObject(ctx, key)
	if err != nil {
		return fmt.Errorf("download video: %w", err)
	}
	defer rc.Close()
	return copyBounded(rc, info.Size, path, MaxFrameSourceBytes)
}

// copyBounded writes src to path, refusing (ErrFrameSourceTooLarge) a
// declared size or a stream over limit. A refused copy leaves no file.
func copyBounded(src io.Reader, declared int64, path string, limit int64) error {
	if declared > limit {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrFrameSourceTooLarge, declared, limit)
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // G304: a service-built temp path
	if err != nil {
		return fmt.Errorf("create temp input: %w", err)
	}
	n, copyErr := io.Copy(out, io.LimitReader(src, limit+1))
	if closeErr := out.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr == nil && n > limit {
		copyErr = fmt.Errorf("%w: more than %d bytes", ErrFrameSourceTooLarge, limit)
	}
	if copyErr != nil {
		_ = os.Remove(path)
		if errors.Is(copyErr, ErrFrameSourceTooLarge) {
			return copyErr
		}
		return fmt.Errorf("download video: %w", copyErr)
	}
	return nil
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

	// Gate line 6 sweep: this runs in the API pod, not the worker. A long
	// video's original (up to MEDIA_WORKER_MAX_ORIGINAL_BYTES, 16 GiB) used
	// to be copied whole onto the pod's disk on any owner's request. The
	// recorded size is checked first, and the copy itself is bounded, so a
	// row that understates the object cannot get past the cap either.
	if media.FileSizeBytes > MaxFrameSourceBytes {
		return nil, fmt.Errorf("%w: %d bytes (max %d)", ErrFrameSourceTooLarge, media.FileSizeBytes, MaxFrameSourceBytes)
	}

	tmpDir, err := os.MkdirTemp("", "frames-"+mediaID.String())
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Stream the original to disk, never into the API process's memory.
	inputPath := filepath.Join(tmpDir, "input")
	if err := s.downloadFrameSource(ctx, media.StorageKey, inputPath); err != nil {
		return nil, err
	}

	// ffprobe keeps ProbeTimeout; each frame's ffmpeg gets frameToolTimeout
	// instead of the worker's six-hour default. The tool is the hardened
	// video runner (processing/tool.go): file protocol, demuxer from bytes.
	toolCtx := processing.WithToolTimeout(ctx, frameToolTimeout)
	framePaths, err := processing.ExtractFrames(toolCtx, inputPath, tmpDir, numFrames)
	if err != nil {
		return nil, fmt.Errorf("extract frames: %w", err)
	}

	// The owner's own frames (ownership is checked above), but still a
	// presigned URL outside the delivery gate, so it carries the gate's cap
	// (P-9): the cover picker loads these immediately and re-asks if it
	// needs them again.
	expiry := delivery.MaxProtectedTTL
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
