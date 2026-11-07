package service

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/atpost/media-service/internal/processing"
)

const (
	// MaxUploadSizeBytes is the absolute maximum file size a simple
	// (single presigned PUT) upload accepts for anything that is not a
	// video (500 MB). Images, GIFs and audio have tighter caps of their own
	// in ValidateUpload.
	MaxUploadSizeBytes int64 = 500 * 1024 * 1024

	// MaxSimpleVideoUploadBytes is the largest video a simple upload takes:
	// 5 GiB, S3's single-PUT maximum (founder, 2026-10-21, lifting the old
	// 500 MB limit). A larger video is not refused — it must use the
	// resumable (multipart) upload; the error is ErrUseResumableUpload so
	// clients can switch. Either route refuses a video over the cap the
	// worker can transcode (videoOriginalCap, ErrVideoTooLarge).
	MaxSimpleVideoUploadBytes int64 = 5 * 1024 * 1024 * 1024

	// Voice post constraints (Module 1 P0-6). The duration ceiling is
	// enforced server-side from the ffprobe result at confirm time — a
	// client-declared duration is never trusted.
	MaxVoiceDurationSec = 180
	MinVoiceDurationSec = 1
	// MaxVoiceSizeBytes: 25 MB is generous for 180 s of speech-grade
	// audio (~1.2 Mbps) while blocking oversized payloads early.
	MaxVoiceSizeBytes int64 = 25 * 1024 * 1024

	// Per-user upload rate limits.
	MaxUploadsPerHour = 10
	MaxUploadsPerDay  = 30

	// Maximum number of draft uploads a user may keep.
	MaxDraftsPerUser = 50
)

var allowedVideoMIME = map[string]bool{
	"video/mp4":        true,
	"video/quicktime":  true,
	"video/webm":       true,
	"video/x-msvideo":  true,
	"video/x-matroska": true,
}

var allowedImageMIME = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
	"image/heic": true,
}

// allowedAudioMIME is the explicit allow-list for voice/audio uploads
// (Module 1 P0-6). Anything not listed is rejected at init — the client
// cannot widen it by declaring a different type.
var allowedAudioMIME = map[string]bool{
	"audio/mp4":   true, // .m4a — iOS/Android recorder default
	"audio/m4a":   true,
	"audio/aac":   true,
	"audio/mpeg":  true, // .mp3
	"audio/ogg":   true,
	"audio/opus":  true,
	"audio/wav":   true,
	"audio/x-wav": true,
	"audio/webm":  true,
	"audio/flac":  true,
	"audio/amr":   true, // low-end Android voice recorders
}

// ValidateUploadMIME checks that contentType is in the allow-list for the given
// mediaType ("video", "image", "audio" or "document"). Unknown mediaTypes are passed through.
func ValidateUploadMIME(contentType string, mediaType string) error {
	ct := strings.ToLower(strings.Split(contentType, ";")[0])
	switch mediaType {
	case "video":
		if !allowedVideoMIME[ct] {
			return fmt.Errorf("invalid video type: %s", ct)
		}
	case "image":
		if !allowedImageMIME[ct] {
			return fmt.Errorf("invalid image type: %s", ct)
		}
	case "audio":
		if !allowedAudioMIME[ct] {
			return fmt.Errorf("invalid audio type: %s", ct)
		}
	case FileTypeDocument:
		if !allowedDocumentMIME[ct] {
			return fmt.Errorf("invalid document type: %s", ct)
		}
	}
	return nil
}

// ErrUseResumableUpload: a video too large for one presigned PUT. The HTTP
// layer answers 413 USE_RESUMABLE_UPLOAD; the client retries the same file
// through POST /v1/media/upload/resumable/init.
var ErrUseResumableUpload = errors.New("video too large for a simple upload: use the resumable upload")

// ErrVideoTooLarge: a video original over the cap the transcode worker
// takes (processing.MaxVideoOriginalBytes, or its environment override).
// Refused at init on both upload routes, before a byte is uploaded; the HTTP
// layer answers 413 VIDEO_TOO_LARGE with details.max_bytes.
var ErrVideoTooLarge = errors.New("video too large")

// videoOriginalCap is the cap in force, read once like the worker's
// (processing.VideoOriginalCap: the same constant and the same variable).
var videoOriginalCap = processing.VideoOriginalCap(os.Getenv)

// VideoOriginalCap is the largest video original the upload routes accept;
// 0 means no cap.
func VideoOriginalCap() int64 { return videoOriginalCap }

// validateVideoOriginalSize refuses a video the worker would refuse.
func validateVideoOriginalSize(size int64) error {
	if videoOriginalCap > 0 && size > videoOriginalCap {
		return fmt.Errorf("%w: %d bytes (max %d)", ErrVideoTooLarge, size, videoOriginalCap)
	}
	return nil
}

// ValidateUploadSize is the simple-upload size gate. A video may be up to
// MaxSimpleVideoUploadBytes (beyond that: ErrUseResumableUpload, unless it
// is over the worker's cap: ErrVideoTooLarge); anything else up to
// MaxUploadSizeBytes.
func ValidateUploadSize(fileType string, size int64) error {
	if fileType == "video" {
		if err := validateVideoOriginalSize(size); err != nil {
			return err
		}
		if size > MaxSimpleVideoUploadBytes {
			return fmt.Errorf("%w: %d bytes (simple upload max %d)", ErrUseResumableUpload, size, MaxSimpleVideoUploadBytes)
		}
		return nil
	}
	if size > MaxUploadSizeBytes {
		return fmt.Errorf("file too large: %d bytes (max %d)", size, MaxUploadSizeBytes)
	}
	return nil
}

// ValidateVoiceDuration enforces the voice-post duration window against a
// server-measured duration (P0-6). durationSec of 0 means "not yet
// measured" and is rejected — a voice post never publishes on an
// unverified duration.
func ValidateVoiceDuration(durationSec float64) error {
	if durationSec <= 0 {
		return fmt.Errorf("voice duration could not be determined")
	}
	if durationSec < MinVoiceDurationSec {
		return fmt.Errorf("voice recording too short: %.1fs (min %ds)", durationSec, MinVoiceDurationSec)
	}
	if durationSec > MaxVoiceDurationSec {
		return fmt.Errorf("voice recording too long: %.1fs (max %ds)", durationSec, MaxVoiceDurationSec)
	}
	return nil
}
