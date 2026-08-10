package service

import (
	"fmt"
	"strings"
)

const (
	// MaxUploadSizeBytes is the absolute maximum file size accepted (500 MB).
	MaxUploadSizeBytes int64 = 500 * 1024 * 1024

	// Reel duration constraints (seconds).
	MaxReelDurationSec = 90
	MinReelDurationSec = 3

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
	"video/mp4":          true,
	"video/quicktime":    true,
	"video/webm":         true,
	"video/x-msvideo":    true,
	"video/x-matroska":   true,
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
	"audio/mp4":    true, // .m4a — iOS/Android recorder default
	"audio/m4a":    true,
	"audio/aac":    true,
	"audio/mpeg":   true, // .mp3
	"audio/ogg":    true,
	"audio/opus":   true,
	"audio/wav":    true,
	"audio/x-wav":  true,
	"audio/webm":   true,
	"audio/flac":   true,
	"audio/amr":    true, // low-end Android voice recorders
}

// ValidateUploadMIME checks that contentType is in the allow-list for the given
// mediaType ("video", "image", or "audio"). Unknown mediaTypes are passed through.
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
	}
	return nil
}

// ValidateUploadSize rejects files that exceed MaxUploadSizeBytes.
func ValidateUploadSize(size int64) error {
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
