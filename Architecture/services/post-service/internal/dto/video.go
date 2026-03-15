package dto

import (
	"github.com/atpost/post-service/internal/store/postgres"
)

// VideoFeedItem is the DTO for video content in feed responses.
// It uses atpost engagement vocabulary: Spark (like), Echo (share), Stash (bookmark).
type VideoFeedItem struct {
	ID                       string  `json:"id"`
	Title                    string  `json:"title"`
	DurationSeconds          float64 `json:"durationSeconds"`
	EffectiveDurationSeconds float64 `json:"effectiveDurationSeconds"`
	FinalCategory            string  `json:"finalCategory"`
	ThumbnailURL             string  `json:"thumbnailUrl"`
	PlaybackURL              string  `json:"playbackUrl"`
	CreatorName              string  `json:"creatorName"`
	CreatorAvatarURL         string  `json:"creatorAvatarUrl"`
	PublishedAt              string  `json:"publishedAt"`
	ViewCount                int64   `json:"viewCount"`
	SparkCount               int64   `json:"sparkCount"`
	EchoCount                int64   `json:"echoCount"`
	StashCount               int64   `json:"stashCount"`
	TrimStartMs              int     `json:"trimStartMs"`
	TrimEndMs                *int    `json:"trimEndMs,omitempty"`
}

// EffectiveDuration calculates the playback duration after trim.
func EffectiveDuration(durationSec float64, trimStartMs int, trimEndMs *int) float64 {
	endMs := durationSec * 1000
	if trimEndMs != nil {
		endMs = float64(*trimEndMs)
	}
	result := (endMs - float64(trimStartMs)) / 1000
	if result < 0 {
		return 0
	}
	return result
}

// EngagementMapping maps internal engagement field names to atpost vocabulary.
type EngagementMapping struct {
	SparkCount int64 `json:"sparkCount"` // from like_count
	EchoCount  int64 `json:"echoCount"`  // from share_count
	StashCount int64 `json:"stashCount"` // from bookmark_count
}

// MapEngagement converts internal engagement counts to atpost vocabulary.
func MapEngagement(likeCount, shareCount, bookmarkCount int64) EngagementMapping {
	return EngagementMapping{
		SparkCount: likeCount,
		EchoCount:  shareCount,
		StashCount: bookmarkCount,
	}
}

// VideoMetadataDTO is the DTO shape included in post responses.
type VideoMetadataDTO struct {
	DurationSeconds          float64 `json:"duration_seconds"`
	EffectiveDurationSeconds float64 `json:"effective_duration_seconds"`
	Width                    *int    `json:"width,omitempty"`
	Height                   *int    `json:"height,omitempty"`
	Orientation              string  `json:"orientation"`
	ComputedCategory         string  `json:"computed_category"`
	FinalCategory            string  `json:"final_category"`
	UploadStatus             string  `json:"upload_status"`
	ThumbnailURL             *string `json:"thumbnail_url,omitempty"`
	PlaybackURL              *string `json:"playback_url,omitempty"`
	TrimStartMs              int     `json:"trim_start_ms"`
	TrimEndMs                *int    `json:"trim_end_ms,omitempty"`
}

// VideoMetadataToDTO converts a store model to DTO.
func VideoMetadataToDTO(vm *postgres.VideoMetadata) *VideoMetadataDTO {
	if vm == nil {
		return nil
	}
	return &VideoMetadataDTO{
		DurationSeconds:          vm.DurationSeconds,
		EffectiveDurationSeconds: EffectiveDuration(vm.DurationSeconds, vm.TrimStartMs, vm.TrimEndMs),
		Width:                    vm.Width,
		Height:                   vm.Height,
		Orientation:              vm.Orientation,
		ComputedCategory:         vm.ComputedCategory,
		FinalCategory:            vm.FinalCategory,
		UploadStatus:             vm.UploadStatus,
		ThumbnailURL:             vm.ThumbnailURL,
		PlaybackURL:              vm.PlaybackURL,
		TrimStartMs:              vm.TrimStartMs,
		TrimEndMs:                vm.TrimEndMs,
	}
}
