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
