package model

// ContentType constants for video analytics.
const (
	ContentTypeReel      = "reel"       // <= 90 seconds
	ContentTypeLongVideo = "long_video" // > 90 seconds
)

// Event name constants — the 13 video analytics event types.
const (
	EventImpression        = "impression"
	EventPlayStart         = "play_start"
	EventWatchHeartbeat    = "watch_heartbeat"
	EventMilestone         = "milestone"
	EventPlayEnd           = "play_end"
	EventLike              = "like"
	EventCommentCreate     = "comment_create"
	EventShare             = "share"
	EventSave              = "save"
	EventFollowFromContent = "follow_from_content"
	EventNotInterested     = "not_interested"
	EventReport            = "report"
	EventBlockCreator      = "block_creator"
)

// VideoEventNames is the set of all recognised video analytics event types.
var VideoEventNames = map[string]bool{
	EventImpression:        true,
	EventPlayStart:         true,
	EventWatchHeartbeat:    true,
	EventMilestone:         true,
	EventPlayEnd:           true,
	EventLike:              true,
	EventCommentCreate:     true,
	EventShare:             true,
	EventSave:              true,
	EventFollowFromContent: true,
	EventNotInterested:     true,
	EventReport:            true,
	EventBlockCreator:      true,
}

// VideoEventCommon contains fields shared across all 13 video analytics events
// as defined in PRD Section 5.1.
type VideoEventCommon struct {
	EventID      string `json:"event_id"`
	EventName    string `json:"event_name"`
	TimestampMS  int64  `json:"timestamp_ms"`
	ContentID    string `json:"content_id"`
	CreatorID    string `json:"creator_id"`
	ViewerUserID string `json:"viewer_user_id"`
	SessionID    string `json:"session_id"`
	Surface      string `json:"surface"`       // reels_feed, home_feed, search_results, profile, share_link, recommended_next
	Position     int    `json:"position"`       // rank position in feed
	Country      string `json:"country"`        // ISO 3166-1 alpha-2
	Language     string `json:"language"`        // BCP-47
	DeviceIDHash string `json:"device_id_hash"` // privacy-safe hash
	AppVersion   string `json:"app_version"`
	OS           string `json:"os"`
	NetworkType  string `json:"network_type"` // wifi, 4g, 5g, ethernet, unknown
	IsAutoplay   bool   `json:"is_autoplay"`
}

// ImpressionEvent — content became visible in viewport.
// Reels: visible >= 300ms, Long card: visible >= 500ms.
type ImpressionEvent struct {
	VideoEventCommon
	VisibleMS int64 `json:"visible_ms"`
}

// PlayStartEvent — video playback began.
type PlayStartEvent struct {
	VideoEventCommon
	ContentDurationMS   int64  `json:"content_duration_ms"`
	ContentType         string `json:"content_type"` // "reel" or "long_video"
	StartMethod         string `json:"start_method"` // autoplay, tap, resume
	IsMuted             bool   `json:"is_muted"`
	TimeToFirstFrameMS  int64  `json:"time_to_first_frame_ms"`
	InitialBufferMS     int64  `json:"initial_buffer_ms"`
}

// WatchHeartbeatEvent — periodic progress update.
// Reels: every 2s, Long Video: every 5s.
type WatchHeartbeatEvent struct {
	VideoEventCommon
	WatchedMSIncrement   int64   `json:"watched_ms_increment"`
	WatchedMSTotal       int64   `json:"watched_ms_total"`
	PlayheadPositionMS   int64   `json:"playhead_position_ms"`
	BufferingMSIncrement int64   `json:"buffering_ms_increment"`
	SeekCountIncrement   int     `json:"seek_count_increment"`
	PlaybackSpeed        float64 `json:"playback_speed"`
}

// MilestoneEvent — viewer crossed a time or percent threshold.
type MilestoneEvent struct {
	VideoEventCommon
	MilestoneType string `json:"milestone_type"` // e.g. VIEW_1S, VIEW_3S, PCT_25, PCT_50
	WatchedMS     int64  `json:"watched_ms"`
}

// PlayEndEvent — playback ended.
type PlayEndEvent struct {
	VideoEventCommon
	EndReason            string  `json:"end_reason"` // swipe_next, back, ended, background, error
	WatchedMSTotal       int64   `json:"watched_ms_total"`
	MaxContinuousWatchMS int64   `json:"max_continuous_watch_ms"`
	ContentDurationMS    int64   `json:"content_duration_ms"`
	ContentType          string  `json:"content_type"`
	PercentViewed        float64 `json:"percent_viewed"`
	LoopCount            int     `json:"loop_count"`
}

// ReelMilestones — time and percent thresholds for reels (<= 90s).
var ReelMilestones = struct {
	Time    []string // milestone_type values
	Percent []string
}{
	Time:    []string{"VIEW_1S", "VIEW_3S", "VIEW_10S"},
	Percent: []string{"PCT_25", "PCT_50", "PCT_75", "PCT_95"},
}

// LongVideoMilestones — time and percent thresholds for long video (> 90s).
var LongVideoMilestones = struct {
	Time    []string
	Percent []string
}{
	Time:    []string{"VIEW_10S", "VIEW_30S", "VIEW_60S", "VIEW_120S"},
	Percent: []string{"PCT_25", "PCT_50", "PCT_75", "PCT_95"},
}

// MilestoneToViewBucket maps a milestone_type to the corresponding view counter bucket name.
var MilestoneToViewBucket = map[string]string{
	"VIEW_1S":   "views_1s",
	"VIEW_3S":   "views_3s",
	"VIEW_10S":  "views_10s",
	"VIEW_30S":  "views_30s",
	"VIEW_60S":  "views_60s",
	"VIEW_120S": "views_120s",
}
