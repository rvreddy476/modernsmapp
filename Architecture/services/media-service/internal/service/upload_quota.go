// upload_quota.go: the per-user video budget and the video length cap on
// the upload path (lane media-upload, 2026-11-17; long-video audit lv-3 and
// lv-30). Limits come from limits.go; the durable ledger is
// media_upload_usage (migration 034, store/postgres/upload_quota.go).
//
//   - Init (simple and resumable): a declared length over the cap is
//     refused (VIDEO_TOO_LONG); the declared size, and the declared length
//     when given, must fit the account's rolling-24 h budget; a declared
//     long video is refused while the account has its cap of long videos
//     in flight. Nothing is charged at init.
//   - Confirm (both routes end there): the length is read from the MP4/MOV
//     header (processing.MP4Duration, the worker's own router probe). Over
//     the cap: the asset is rejected before its transcode is queued. Then
//     the bytes and the probed length are charged in one transaction under
//     a per-user lock, or refused; the asset stays pending_upload, so the
//     client may confirm again later (or orphan GC collects it).
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ErrVideoTooLong: a video longer than MEDIA_MAX_VIDEO_DURATION_SEC, refused
// at init (declared length) or confirm (probed length). 413 VIDEO_TOO_LONG
// with details.max_duration_seconds.
var ErrVideoTooLong = errors.New("video too long")

// ErrUploadQuotaExceeded: the account's rolling-24 h video budget (bytes or
// source seconds) would be passed. 429 UPLOAD_QUOTA_EXCEEDED.
var ErrUploadQuotaExceeded = errors.New("daily video upload limit reached")

// ErrLongVideoInFlight: the account already has its cap of long videos
// queued or transcoding. 429 LONG_VIDEO_IN_PROGRESS.
var ErrLongVideoInFlight = errors.New("too many long videos processing")

// Quota limit names (details.limit of a 429).
const (
	QuotaLimitDailyBytes   = "daily_video_bytes"
	QuotaLimitDailySeconds = "daily_video_seconds"
	QuotaLimitInflightLong = "inflight_long_videos"
)

// UploadQuotaError is a quota refusal with what the client may show.
type UploadQuotaError struct {
	Limit string // one of the QuotaLimit* names
	Max   int64  // the limit (bytes, seconds or videos)
	Used  int64  // already used in the window (same unit)
	Asked int64  // what this upload would add (same unit)
}

func (e *UploadQuotaError) Error() string {
	switch e.Limit {
	case QuotaLimitInflightLong:
		return fmt.Sprintf("%s: %d of %d long videos are still processing", ErrLongVideoInFlight, e.Used, e.Max)
	default:
		return fmt.Sprintf("%s: %s used %d of %d, this upload needs %d", ErrUploadQuotaExceeded, e.Limit, e.Used, e.Max, e.Asked)
	}
}

func (e *UploadQuotaError) Is(target error) bool {
	if e.Limit == QuotaLimitInflightLong {
		return target == ErrLongVideoInFlight
	}
	return target == ErrUploadQuotaExceeded
}

// Message is the text a client can show as is.
func (e *UploadQuotaError) Message() string {
	switch e.Limit {
	case QuotaLimitInflightLong:
		return fmt.Sprintf("You already have %d long videos processing. Upload this one when one of them is finished.", e.Used)
	case QuotaLimitDailySeconds:
		return fmt.Sprintf("You can upload up to %s of video a day. Try again later, or contact support to raise your limit.",
			formatSeconds(e.Max))
	default:
		return fmt.Sprintf("You can upload up to %s of video a day. Try again later, or contact support to raise your limit.",
			formatByteSize(e.Max))
	}
}

// VideoTooLongMessage is the text of a VIDEO_TOO_LONG refusal.
func VideoTooLongMessage(maxSeconds float64) string {
	return "This video is longer than " + formatSeconds(int64(maxSeconds)) +
		", the longest we can process right now. Trim it or split it into parts, then upload again."
}

func formatSeconds(s int64) string {
	h, m := s/3600, (s%3600)/60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%d h %d min", h, m)
	case h > 0:
		return fmt.Sprintf("%d hours", h)
	default:
		return fmt.Sprintf("%d minutes", m)
	}
}

// ── init options ──────────────────────────────────────────────────────

type initOptions struct {
	// declaredDurationMs: the client's local reading of the video's
	// length; 0 when not sent. Only ever used to refuse early.
	declaredDurationMs int64
}

// InitOption is an optional upload-init field.
type InitOption func(*initOptions)

// WithDeclaredDurationMs passes the length the client read locally.
func WithDeclaredDurationMs(ms int64) InitOption {
	return func(o *initOptions) {
		if ms > 0 {
			o.declaredDurationMs = ms
		}
	}
}

func collectInitOptions(opts []InitOption) initOptions {
	var o initOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return o
}

// ── the length cap (lv-30) ───────────────────────────────────────────

// validateVideoDurationMs refuses a length (ms; 0 = unknown, admitted) over
// the cap in force (0 = no cap).
func validateVideoDurationMs(ms int64) error {
	return checkVideoDuration(uploadLimits.VideoMaxDurationSeconds, float64(ms)/1000)
}

func checkVideoDuration(maxSeconds, seconds float64) error {
	if maxSeconds > 0 && seconds > maxSeconds {
		return fmt.Errorf("%w: %.0fs (max %.0fs)", ErrVideoTooLong, seconds, maxSeconds)
	}
	return nil
}

// ── the budget (lv-3) ─────────────────────────────────────────────────

// uploadQuotaStore is the ledger (*postgres.MediaAssetStore).
type uploadQuotaStore interface {
	UploadUsageFor(ctx context.Context, userID uuid.UUID, since time.Time, exclude uuid.UUID) (postgres.UploadUsage, *postgres.UploadQuotaOverride, error)
	AdmitVideoUpload(ctx context.Context, c postgres.VideoUploadCharge, since time.Time,
		decide func(postgres.UploadUsage, *postgres.UploadQuotaOverride) error) error
}

// quotaWindow is the rolling window of the daily budget.
const quotaWindow = 24 * time.Hour

// effectiveQuota is the deployment default with the account's override.
func effectiveQuota(l UploadLimits, o *postgres.UploadQuotaOverride) (bytes, seconds int64, inflight int) {
	bytes, seconds, inflight = l.DailyVideoBytes, l.DailyVideoSeconds, l.MaxInflightLong
	if o != nil {
		if o.DailyVideoBytes != nil {
			bytes = *o.DailyVideoBytes
		}
		if o.DailyVideoSeconds != nil {
			seconds = *o.DailyVideoSeconds
		}
		if o.MaxInflightLong != nil {
			inflight = *o.MaxInflightLong
		}
	}
	return bytes, seconds, inflight
}

// decideVideoBudget refuses an upload of `bytes` and `durationMs` (0 =
// unknown, not counted against the seconds budget) when it would pass the
// account's budget; checkInflight also applies the long-video cap to a long
// upload. A limit of 0 is no limit.
func decideVideoBudget(l UploadLimits, u postgres.UploadUsage, o *postgres.UploadQuotaOverride,
	bytes, durationMs int64, isLong, checkInflight bool) error {
	maxBytes, maxSeconds, maxInflight := effectiveQuota(l, o)
	if checkInflight && isLong && maxInflight > 0 && u.InflightLong >= maxInflight {
		return &UploadQuotaError{Limit: QuotaLimitInflightLong, Max: int64(maxInflight), Used: int64(u.InflightLong), Asked: 1}
	}
	if maxBytes > 0 && u.Bytes+bytes > maxBytes {
		return &UploadQuotaError{Limit: QuotaLimitDailyBytes, Max: maxBytes, Used: u.Bytes, Asked: bytes}
	}
	if maxSeconds > 0 && durationMs > 0 && u.DurationMs+durationMs > maxSeconds*1000 {
		return &UploadQuotaError{Limit: QuotaLimitDailySeconds, Max: maxSeconds,
			Used: u.DurationMs / 1000, Asked: (durationMs + 999) / 1000}
	}
	return nil
}

// checkVideoBudgetAtInit is the init-time check: read-only, nothing charged.
func (s *Service) checkVideoBudgetAtInit(ctx context.Context, userID uuid.UUID, bytes, declaredMs int64) error {
	return videoBudgetAtInit(ctx, s.pgStore, uploadLimits, time.Now(), userID, bytes, declaredMs)
}

func videoBudgetAtInit(ctx context.Context, store uploadQuotaStore, l UploadLimits, now time.Time,
	userID uuid.UUID, bytes, declaredMs int64) error {
	usage, override, err := store.UploadUsageFor(ctx, userID, now.Add(-quotaWindow), uuid.Nil)
	if err != nil {
		// The budget cannot be read: refuse, as the count limit does.
		return fmt.Errorf("%w: %v", ErrUploadLimiterUnavailable, err)
	}
	// Only a DECLARED long video meets the in-flight cap at init: an
	// undeclared one (most reels) is classified at confirm.
	declaredLong := declaredMs > 0 && isLongVideoSeconds(float64(declaredMs)/1000)
	return decideVideoBudget(l, usage, override, bytes, declaredMs, declaredLong, declaredLong)
}

// admitVideoAtConfirm runs at confirm for a video, before it is marked
// uploaded: the length cap on the probed length (reject = persist the
// refusal), then the charge. A media id already charged (a retried confirm)
// is admitted again.
func admitVideoAtConfirm(ctx context.Context, store uploadQuotaStore, l UploadLimits, now time.Time,
	media *postgres.MediaAsset, sizeBytes int64, probeSeconds func() (float64, error), reject func() error) error {
	seconds, err := probeSeconds()
	if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		// Not an MP4/MOV header the probe reads: the length is unknown. The
		// worker still applies the cap after its own ffprobe; here the
		// video is charged as long, with no seconds.
		seconds = 0
	}
	if err := checkVideoDuration(l.VideoMaxDurationSeconds, seconds); err != nil {
		if rejErr := reject(); rejErr != nil {
			return fmt.Errorf("reject over-long video and persist status: %w", rejErr)
		}
		return err
	}
	durationMs := int64(math.Round(seconds * 1000))
	isLong := isLongVideoSeconds(seconds)
	charge := postgres.VideoUploadCharge{
		MediaID: media.ID, UploaderID: media.UploaderID, Bytes: sizeBytes, DurationMs: durationMs, IsLong: isLong,
	}
	err = store.AdmitVideoUpload(ctx, charge, now.Add(-quotaWindow),
		func(u postgres.UploadUsage, o *postgres.UploadQuotaOverride) error {
			return decideVideoBudget(l, u, o, sizeBytes, durationMs, isLong, true)
		})
	if errors.Is(err, postgres.ErrUploadAlreadyCharged) {
		return nil
	}
	return err
}

// admitVideoUpload is admitVideoAtConfirm on the service's stores.
func (s *Service) admitVideoUpload(ctx context.Context, media *postgres.MediaAsset, sizeBytes int64) error {
	probe := func() (float64, error) {
		pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return processing.MP4Duration(func(off, n int64) ([]byte, error) {
			return s.blobStore.ReadObjectRange(pctx, media.StorageKey, off, off+n-1)
		}, sizeBytes)
	}
	reject := func() error {
		slog.InfoContext(ctx, "upload refused: video over the length cap", "media_id", media.ID)
		return s.pgStore.UpdateStatus(ctx, media.ID, "rejected")
	}
	err := admitVideoAtConfirm(ctx, s.pgStore, uploadLimits, time.Now(), media, sizeBytes, probe, reject)
	if err != nil && !errors.Is(err, ErrVideoTooLong) && !errors.Is(err, ErrUploadQuotaExceeded) && !errors.Is(err, ErrLongVideoInFlight) {
		// The ledger could not be read or written: refuse (fail closed).
		return fmt.Errorf("%w: %v", ErrUploadLimiterUnavailable, err)
	}
	return err
}
