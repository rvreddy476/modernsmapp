// limits.go holds the upload path's per-user and per-file limits that come
// from the environment (lane media-upload, 2026-11-17):
//
//   - the video length cap, ONE variable shared with the transcode worker
//     (MEDIA_MAX_VIDEO_DURATION_SEC; the worker reads it in
//     cmd/worker/transcode_guards.go loadWorkerConfig), checked here at init
//     from the declared length and at confirm from the probed one (lv-30);
//   - the per-user daily video budget (bytes and source seconds) and the cap
//     on in-flight long transcodes (lv-3);
//   - the resumable session's sliding idle expiry and its absolute ceiling
//     (lv-16).
//
// Every value is read once at start, like videoOriginalCap. A per-account
// raise is a row in media_upload_quota_overrides (migration 034), not an env
// change: see that migration's header.
package service

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/processing"
)

const (
	// VideoDurationCapEnv is the worker's variable (transcode_guards.go);
	// the upload path reads the same one so the two cannot disagree.
	VideoDurationCapEnv = "MEDIA_MAX_VIDEO_DURATION_SEC"

	// UploadDailyVideoBytesEnv: the bytes of video one account may confirm
	// in a rolling 24 h. Default 32 GiB: two files at the 16 GiB cap.
	UploadDailyVideoBytesEnv = "MEDIA_UPLOAD_DAILY_VIDEO_BYTES"
	// UploadDailyVideoSecondsEnv: the source seconds of video one account
	// may confirm in a rolling 24 h. Default 8 h: two at the 4 h cap.
	UploadDailyVideoSecondsEnv = "MEDIA_UPLOAD_DAILY_VIDEO_SECONDS"
	// UploadMaxInflightLongEnv: how many long (over the reel length, or of
	// unknown length) videos of one account may be queued or transcoding
	// at once. Default 2.
	UploadMaxInflightLongEnv = "MEDIA_UPLOAD_MAX_INFLIGHT_LONG"

	// ResumableIdleTTLEnv: how long a resumable session lives after its
	// last accepted part (or its init). Default 24h. Go duration syntax.
	ResumableIdleTTLEnv = "MEDIA_RESUMABLE_IDLE_TTL"
	// ResumableMaxLifetimeEnv: the absolute ceiling on a resumable
	// session, counted from init, whatever its activity. Default 168h
	// (7 days), below the bucket's incomplete-multipart abort rule.
	ResumableMaxLifetimeEnv = "MEDIA_RESUMABLE_MAX_LIFETIME"

	// UploadRateLimitOptionalEnv: "true" lets upload init run with no Redis
	// (local runs without one). Unset, a missing or failing Redis refuses
	// upload init (503 UPLOAD_LIMITER_UNAVAILABLE): the count limit fails
	// closed. Never set it in a deployed environment.
	UploadRateLimitOptionalEnv = "MEDIA_UPLOAD_RATE_LIMIT_OPTIONAL"

	defaultDailyVideoBytes   int64 = 32 << 30
	defaultDailyVideoSeconds int64 = 8 * 60 * 60
	defaultMaxInflightLong         = 2
	defaultResumableIdleTTL        = 24 * time.Hour
	defaultResumableMaxLife        = 7 * 24 * time.Hour
)

// UploadLimits is the configuration of the upload path's env-driven limits.
type UploadLimits struct {
	// VideoMaxDurationSeconds: 0 means no length cap (the worker's meaning).
	VideoMaxDurationSeconds float64
	// DailyVideoBytes / DailyVideoSeconds: 0 means no budget.
	DailyVideoBytes   int64
	DailyVideoSeconds int64
	// MaxInflightLong: 0 means no cap.
	MaxInflightLong int
	// ResumableIdleTTL / ResumableMaxLifetime: see the env constants.
	ResumableIdleTTL     time.Duration
	ResumableMaxLifetime time.Duration
	// RateLimitOptional: see UploadRateLimitOptionalEnv.
	RateLimitOptional bool
}

// uploadLimits is the configuration in force, read once.
var uploadLimits = loadUploadLimits(os.Getenv)

// CurrentUploadLimits is the configuration in force (for handlers' details).
func CurrentUploadLimits() UploadLimits { return uploadLimits }

func loadUploadLimits(getenv func(string) string) UploadLimits {
	l := UploadLimits{
		VideoMaxDurationSeconds: envNonNegFloat(getenv, VideoDurationCapEnv, processing.DefaultMaxVideoDurationSeconds),
		DailyVideoBytes:         envNonNegInt64(getenv, UploadDailyVideoBytesEnv, defaultDailyVideoBytes),
		DailyVideoSeconds:       envNonNegInt64(getenv, UploadDailyVideoSecondsEnv, defaultDailyVideoSeconds),
		MaxInflightLong:         int(envNonNegInt64(getenv, UploadMaxInflightLongEnv, defaultMaxInflightLong)),
		ResumableIdleTTL:        envPositiveDuration(getenv, ResumableIdleTTLEnv, defaultResumableIdleTTL),
		ResumableMaxLifetime:    envPositiveDuration(getenv, ResumableMaxLifetimeEnv, defaultResumableMaxLife),
		RateLimitOptional:       strings.EqualFold(strings.TrimSpace(getenv(UploadRateLimitOptionalEnv)), "true"),
	}
	if l.ResumableMaxLifetime < l.ResumableIdleTTL {
		slog.Warn("resumable max lifetime is shorter than the idle TTL; using the idle TTL",
			"max_lifetime", l.ResumableMaxLifetime, "idle_ttl", l.ResumableIdleTTL)
		l.ResumableMaxLifetime = l.ResumableIdleTTL
	}
	return l
}

// envNonNegFloat parses like the worker's envFloat: a non-negative number,
// else the default.
func envNonNegFloat(getenv func(string) string, key string, def float64) float64 {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			return f
		}
		slog.Warn("upload limit is not a non-negative number; using the default", "key", key, "value", v, "default", def)
	}
	return def
}

func envNonNegInt64(getenv func(string) string, key string, def int64) int64 {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
		slog.Warn("upload limit is not a non-negative integer; using the default", "key", key, "value", v, "default", def)
	}
	return def
}

func envPositiveDuration(getenv func(string) string, key string, def time.Duration) time.Duration {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		slog.Warn("upload limit is not a positive duration; using the default", "key", key, "value", v, "default", def)
	}
	return def
}

// isLongVideoSeconds: the worker's classForDuration — only a video proven
// no longer than a reel is short; an unknown length (0) is long.
func isLongVideoSeconds(seconds float64) bool {
	return !(seconds > 0 && seconds <= processing.ReelMaxDurationSeconds)
}
