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

// VideoTooLargeMessage is the text of a VIDEO_TOO_LARGE refusal for a cap
// of capBytes. Product rule (2026-11-16): a long video has NO length cap;
// the cap is the transcode worker's processing limit on one FILE (its
// scratch volume holds the original plus about 2x for the ladder), so the
// message says that, and says how to get under it, instead of reading like
// a limit on how long a video may be.
func VideoTooLargeMessage(capBytes int64) string {
	size := formatByteSize(capBytes)
	return "This video file is larger than " + size + ", the most our video processing can take in one file. " +
		"There is no limit on video length: export the same video at a lower bitrate or resolution so the file is under " +
		size + ", then upload it again."
}

// formatByteSize writes a byte count as GiB (or MiB below 1 GiB), with one
// decimal only when it is not whole: 16 GiB, 6 GiB, 1.5 GiB, 700 MiB.
func formatByteSize(n int64) string {
	const kib, mib, gib = int64(1) << 10, int64(1) << 20, int64(1) << 30
	unit, name := gib, "GiB"
	if n < gib {
		unit, name = mib, "MiB"
	}
	if n < mib {
		unit, name = kib, "KiB"
	}
	if n%unit == 0 {
		return fmt.Sprintf("%d %s", n/unit, name)
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(unit), name)
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
		return fmt.Errorf("%w: voice duration could not be determined", ErrUploadUnacceptable)
	}
	if durationSec < MinVoiceDurationSec {
		return fmt.Errorf("%w: voice recording too short: %.1fs (min %ds)", ErrUploadUnacceptable, durationSec, MinVoiceDurationSec)
	}
	if durationSec > MaxVoiceDurationSec {
		return fmt.Errorf("%w: voice recording too long: %.1fs (max %ds)", ErrUploadUnacceptable, durationSec, MaxVoiceDurationSec)
	}
	return nil
}
