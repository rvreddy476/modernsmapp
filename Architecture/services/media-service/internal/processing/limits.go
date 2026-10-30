package processing

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// Video limits, enforced on the probed file before any encode (reels audit
// 2026-10-30, finding 3).
//
// The upload path checks bytes, and bytes say little about cost: a few-MB
// HEVC file can hold hours of static 8K frames or a 16384x16384 stream, and
// the worker then spends days of CPU on six sequential encodes while every
// reel behind it on the partition waits. ProbeVideo already read the
// numbers; nothing compared them with anything.

// ErrVideoRefused: the probed video is outside what the platform
// transcodes. Terminal: the same file is refused every time.
var ErrVideoRefused = errors.New("video refused")

// VideoRefusal says which limit a video broke. It unwraps to
// ErrVideoRefused; Reason is a stable token for logs and metrics.
type VideoRefusal struct {
	Reason string // duration_too_long | resolution_too_low | resolution_too_high | frame_rate_too_high | no_duration | no_video_stream
	Detail string
}

func (e *VideoRefusal) Error() string {
	return fmt.Sprintf("%s: %s (%s)", ErrVideoRefused, e.Reason, e.Detail)
}

func (e *VideoRefusal) Unwrap() error { return ErrVideoRefused }

// VideoLimits are the bounds CheckVideoLimits enforces.
type VideoLimits struct {
	// MaxDurationSeconds: longer videos are refused.
	MaxDurationSeconds float64
	// MinShortSide: a video whose shorter display side is below this is
	// refused (MinVideoResolution, "360p").
	MinShortSide int
	// MaxLongSide / MaxPixels bound the frame: either over is refused.
	MaxLongSide int
	MaxPixels   int
	// MaxFrameRate: frames per second, measured as avg_frame_rate (r_frame_rate
	// when ffprobe gave no average).
	MaxFrameRate float64
}

const (
	// DefaultMaxVideoDurationSeconds is the longest video the worker
	// transcodes: 4 hours. Long videos had no length limit (founder,
	// 2026-10-21); a bound is still needed so one upload cannot hold a
	// worker for days. MEDIA_MAX_VIDEO_DURATION_SEC overrides it.
	DefaultMaxVideoDurationSeconds = 4 * 60 * 60
	// DefaultMaxVideoLongSide / DefaultMaxVideoPixels admit every 4K
	// recording (3840x2160 and DCI 4096x2160, either orientation) and
	// refuse 8K and synthetic giant frames.
	DefaultMaxVideoLongSide = 4096
	DefaultMaxVideoPixels   = 4096 * 2304
	// DefaultMaxVideoFrameRate admits phone slow-motion (240 fps) and
	// refuses the absurd rates a hostile file declares to multiply encode
	// cost.
	DefaultMaxVideoFrameRate = 240
)

// DefaultVideoLimits is the worker's default policy.
func DefaultVideoLimits() VideoLimits {
	return VideoLimits{
		MaxDurationSeconds: DefaultMaxVideoDurationSeconds,
		MinShortSide:       MinVideoResolution,
		MaxLongSide:        DefaultMaxVideoLongSide,
		MaxPixels:          DefaultMaxVideoPixels,
		MaxFrameRate:       DefaultMaxVideoFrameRate,
	}
}

// CheckVideoLimits refuses a probed video outside lim with a *VideoRefusal,
// and returns nil for one inside.
func CheckVideoLimits(meta *VideoMeta, lim VideoLimits) error {
	if meta == nil {
		return &VideoRefusal{Reason: "no_video_stream", Detail: "nothing probed"}
	}
	if meta.Width <= 0 || meta.Height <= 0 {
		return &VideoRefusal{Reason: "no_video_stream", Detail: fmt.Sprintf("%dx%d", meta.Width, meta.Height)}
	}
	d := meta.DurationFloat
	if d <= 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return &VideoRefusal{Reason: "no_duration", Detail: fmt.Sprintf("duration %v", d)}
	}
	if lim.MaxDurationSeconds > 0 && d > lim.MaxDurationSeconds {
		return &VideoRefusal{Reason: "duration_too_long",
			Detail: fmt.Sprintf("%.0fs over %.0fs", d, lim.MaxDurationSeconds)}
	}
	short, long := meta.Width, meta.Height
	if short > long {
		short, long = long, short
	}
	if lim.MinShortSide > 0 && short < lim.MinShortSide {
		return &VideoRefusal{Reason: "resolution_too_low",
			Detail: fmt.Sprintf("%dx%d, shorter side under %d", meta.Width, meta.Height, lim.MinShortSide)}
	}
	if lim.MaxLongSide > 0 && long > lim.MaxLongSide {
		return &VideoRefusal{Reason: "resolution_too_high",
			Detail: fmt.Sprintf("%dx%d, longer side over %d", meta.Width, meta.Height, lim.MaxLongSide)}
	}
	if lim.MaxPixels > 0 && meta.Width*meta.Height > lim.MaxPixels {
		return &VideoRefusal{Reason: "resolution_too_high",
			Detail: fmt.Sprintf("%dx%d over %d pixels", meta.Width, meta.Height, lim.MaxPixels)}
	}
	fps := meta.AvgFrameRate
	if fps <= 0 {
		fps = meta.FrameRate
	}
	if lim.MaxFrameRate > 0 && fps > lim.MaxFrameRate {
		return &VideoRefusal{Reason: "frame_rate_too_high",
			Detail: fmt.Sprintf("%.2f fps over %.0f", fps, lim.MaxFrameRate)}
	}
	return nil
}

// FFmpegCallTimeout is the deadline for ONE ffmpeg call over a video of
// durationSec: a fixed allowance for startup plus a multiple of real time.
// The dev worker (1 CPU) has measured ~15x real time for a long-form
// "veryfast" rendition, so 20x keeps a healthy job far from the edge while
// still ending a runaway one. Capped so a lying duration cannot buy days.
func FFmpegCallTimeout(durationSec float64) time.Duration {
	const (
		base      = 10 * time.Minute
		realtimeX = 20
		capped    = 24 * time.Hour
	)
	if durationSec <= 0 || math.IsNaN(durationSec) || math.IsInf(durationSec, 0) {
		return base
	}
	// Compared in seconds first: a lying duration overflows a Duration.
	if base.Seconds()+durationSec*realtimeX >= capped.Seconds() {
		return capped
	}
	return base + time.Duration(durationSec*realtimeX*float64(time.Second))
}

// TranscodeJobBudget is the whole job's wall-clock budget: every ffmpeg
// stage of the pipeline run back to back (up to six MP4 rungs, three HLS
// rungs, storyboard, frames), each at FFmpegCallTimeout, capped at
// MaxTranscodeJobBudget. A job past it is not making progress worth
// waiting for, and the heartbeat stops renewing its lease (cmd/worker).
func TranscodeJobBudget(durationSec float64) time.Duration {
	if d := 12 * FFmpegCallTimeout(durationSec); d < MaxTranscodeJobBudget {
		return d
	}
	return MaxTranscodeJobBudget
}

// MaxTranscodeJobBudget bounds one transcode job, whatever the video.
const MaxTranscodeJobBudget = 48 * time.Hour

// Content-scan frame sampling (reels audit 2026-10-30, finding 10): the
// scan looked at 5 frames whatever the length, so a 10-minute reel was
// judged on five instants about 100 s apart.
const (
	// ScanFrameIntervalSeconds: one frame per this many seconds of video.
	ScanFrameIntervalSeconds = 4
	// MinScanFrames / MaxScanFrames bound the sample. The cap bounds the
	// provider cost and the scan time of a long video.
	MinScanFrames = 5
	MaxScanFrames = 60
)

// ScanFrameCount is how many frames the content scan samples from a video
// of durationSec: one every ScanFrameIntervalSeconds, at least
// MinScanFrames, at most MaxScanFrames.
func ScanFrameCount(durationSec float64) int {
	if durationSec <= 0 || math.IsNaN(durationSec) || math.IsInf(durationSec, 0) {
		return MinScanFrames
	}
	n := int(math.Ceil(durationSec / ScanFrameIntervalSeconds))
	if n < MinScanFrames {
		return MinScanFrames
	}
	if n > MaxScanFrames {
		return MaxScanFrames
	}
	return n
}

// Container families: what an upload's magic bytes say it is, against what
// it was declared as (reels audit 2026-10-30, finding 7). The confirm path
// threw the sniffed type away, so a "video/mp4" carrying AVI bytes passed.
const (
	ContainerMOV      = "mov"      // MP4 / QuickTime (ftyp)
	ContainerMatroska = "matroska" // WebM / Matroska (EBML)
	ContainerAVI      = "avi"      // RIFF AVI
)

// DeclaredVideoContainer is the container family a declared video MIME
// must hold, or "" for a MIME this service does not accept.
func DeclaredVideoContainer(mime string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])) {
	case "video/mp4", "video/quicktime":
		return ContainerMOV
	case "video/webm", "video/x-matroska":
		return ContainerMatroska
	case "video/x-msvideo", "video/avi":
		return ContainerAVI
	}
	return ""
}

// SniffVideoContainer is the container family of a file's first bytes, or
// "" for anything else.
func SniffVideoContainer(head []byte) string {
	return ffmpegDemuxer(head)
}

// VideoContainerMatches reports whether head holds the container the
// declared MIME promises.
func VideoContainerMatches(declaredMIME string, head []byte) bool {
	want := DeclaredVideoContainer(declaredMIME)
	return want != "" && SniffVideoContainer(head) == want
}

// SniffVideoFile is SniffVideoContainer over a local file.
func SniffVideoFile(path string) (string, error) {
	head, err := sniffFile(path)
	if err != nil {
		return "", err
	}
	return SniffVideoContainer(head), nil
}
