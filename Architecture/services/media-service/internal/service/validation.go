package service

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// MaxUploadSizeBytes is the absolute maximum file size accepted (500 MB).
	MaxUploadSizeBytes int64 = 500 * 1024 * 1024

	// Reel duration constraints (seconds).
	MaxReelDurationSec = 90
	MinReelDurationSec = 3

	// Maximum number of draft uploads a user may keep.
	MaxDraftsPerUser = 50
)

// Per-user upload rate limits. Env-tunable because legitimate flows blow past
// the old hardcoded 10/hour — a single restaurant-partner onboarding uploads
// logo + outlet photos + PAN/FSSAI/GST/cheque + one photo per dish in one
// submit. Defaults stay conservative for production; dev compose raises them.
var (
	MaxUploadsPerHour = envInt("UPLOAD_RATE_MAX_PER_HOUR", 10)
	MaxUploadsPerDay  = envInt("UPLOAD_RATE_MAX_PER_DAY", 30)
)

func envInt(key string, fallback int) int {
	if raw := os.Getenv(key); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			return value
		}
	}
	return fallback
}

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

// ValidateUploadMIME checks that contentType is in the allow-list for the given
// mediaType ("video" or "image"). Unknown mediaTypes are passed through.
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
