package processing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// HLSVariant describes a single HLS quality level.
type HLSVariant struct {
	Quality      string // "360p", "720p", "1080p"
	Height       int
	VideoBitrate string // e.g. "800k"
	AudioBitrate string // e.g. "96k"
}

var defaultHLSVariants = []HLSVariant{
	{"360p", 360, "800k", "96k"},
	{"720p", 720, "2500k", "128k"},
	{"1080p", 1080, "5000k", "192k"},
}

// HLSPlan says what the HLS ladder is for, so it can be sized to the video
// instead of always encoding every rung.
type HLSPlan struct {
	// Reel is short-form (see ReelMaxDurationSeconds). Reels are watched on
	// phones and already cap their MP4 renditions at 720p; the HLS ladder
	// used to ignore that and spend a full encode on a 1080p rung nobody
	// would be served.
	Reel bool
	// SourceHeight in pixels, or 0 when unknown. A rung taller than the
	// source is an upscale: a full encode that adds no detail.
	SourceHeight int
	// Renditions maps a quality name ("360p", "720p", "1080p") to the MP4
	// rendition of that height the first stage already encoded (see
	// MP4Renditions). A rung with a rendition is REPACKAGED into HLS with a
	// stream copy instead of being encoded a second time from the original;
	// a rung without one, or whose copy fails, is encoded from the original
	// as before. Nil means "encode every rung from the original".
	Renditions map[string]string
}

// hlsVariantsFor picks the rungs of the ladder for a plan. Always at least
// the lowest rung, so every video has one variant to play.
func hlsVariantsFor(plan HLSPlan) []HLSVariant {
	var out []HLSVariant
	for _, v := range defaultHLSVariants {
		if plan.Reel && v.Height > 720 {
			continue
		}
		if plan.SourceHeight > 0 && v.Height > plan.SourceHeight && len(out) > 0 {
			continue
		}
		out = append(out, v)
	}
	return out
}

// hlsPreset trades a little bitrate efficiency for wall-clock on reels: a
// two-minute phone clip took three sequential "fast" encodes and the app's
// readiness window ran out (2026-09-04). Long-form keeps "fast".
func hlsPreset(plan HLSPlan) string {
	if plan.Reel {
		return "veryfast"
	}
	return "fast"
}

// GenerateHLSVariants transcodes the video at inputPath into HLS adaptive bitrate segments
// using the full ladder. Prefer GenerateHLSVariantsFor when the plan is known.
func GenerateHLSVariants(ctx context.Context, inputPath, outputDir string) (masterPlaylistPath string, variantPaths []string, err error) {
	return GenerateHLSVariantsFor(ctx, inputPath, outputDir, HLSPlan{})
}

// GenerateHLSVariantsFor produces the HLS ladder for the rungs the plan
// calls for: each rung is repackaged from its MP4 rendition when the plan
// has one (HLSPlan.Renditions) and encoded from inputPath otherwise. Returns
// the master playlist path and the paths of every variant playlist and
// segment (local temp paths). The pipeline lives in hls.go.
func GenerateHLSVariantsFor(ctx context.Context, inputPath, outputDir string, plan HLSPlan) (masterPlaylistPath string, variantPaths []string, err error) {
	masterPlaylistPath, variantPaths, _, err = generateHLS(ctx, inputPath, outputDir, plan, execFFmpeg)
	return masterPlaylistPath, variantPaths, err
}

// TranscodeOutput holds the result of a single transcode operation.
type TranscodeOutput struct {
	Name string
	// ObjectName is the file name under the asset prefix when it differs
	// from Name (the storyboard: variant storyboard_jpg, object
	// storyboard.jpg). Empty means Name, which is every rendition.
	ObjectName string
	FilePath   string
	Width      int
	Height     int
	Mime       string
}

// VideoMeta holds extracted video metadata.
//
// Width and Height are the DISPLAY dimensions: the coded frame with the
// container's rotation applied. A phone held upright records a landscape
// coded frame (1920x1080) plus a 90-degree display matrix; reporting the coded
// size called that clip landscape, so is_vertical was false and post-service
// filed it as a landscape long_video. Everything downstream (thumbnail, MP4
// renditions, HLS) already autorotates, so the stored size disagreed with the
// pixels it described (Tube thumbnail sideways, 2026-09-05).
type VideoMeta struct {
	Width  int
	Height int
	// CodedWidth/CodedHeight are the raw stream dimensions before rotation.
	CodedWidth  int
	CodedHeight int
	// Rotation is the display rotation in degrees counter-clockwise,
	// normalised to 0, 90, 180 or 270 (ffprobe's side-data convention).
	Rotation        int
	DurationMs      int     // internal, from ffprobe (milliseconds)
	DurationSeconds int     // for DB storage (seconds)
	DurationFloat   float64 // precise duration in seconds
	CodecVideo      string  // e.g. "h264", "hevc"
	CodecAudio      string  // e.g. "aac", "opus"
	FrameRate       float64 // e.g. 30.0, 59.94
}

// ExtractThumbnail generates a JPEG thumbnail from a video at the given timestamp.
// A fractional timestamp is required for short clips: rounding a one-second
// upload to second 1 seeks to EOF and produces no thumbnail.
func ExtractThumbnail(ctx context.Context, inputPath, outputPath string, atSecond float64, size int) error {
	args := []string{
		"-y", "-ss", fmt.Sprintf("%.3f", atSecond),
		"-i", inputPath,
		"-vframes", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", size, size, size, size),
		"-q:v", "5",
		outputPath,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func thumbnailTimestamp(durationSeconds float64) float64 {
	if durationSeconds <= 0 {
		return 0
	}
	// A quarter of the precise duration is always within the media, including
	// sub-second clips, and avoids an often-black first frame.
	return durationSeconds * 0.25
}

// mp4Profile is the x264 rate/speed trade-off for one kind of rendition.
type mp4Profile struct {
	Preset string
	CRF    string
}

// longFormMP4Profile encodes the long-video renditions. It was "medium";
// a 24-minute upload took ~10 hours on the dev worker (2026-09-29). Since
// the HLS ladder is now a stream copy of these files, this is also what
// viewers are served, where they used to get a separate "fast" encode at a
// fixed bitrate. Measured on a synthetic 1080p clip
// (TestTranscodePipelineTiming): "veryfast" encodes the renditions ~1.4-1.7x
// faster than "medium" at 720p SSIM 0.9933 vs 0.9943 and ~12% fewer bytes.
// CRF 21 is the knob if served quality should match the old fixed-bitrate
// ladder more closely (SSIM 0.9948, ~+33% bytes, about the same time).
var longFormMP4Profile = mp4Profile{Preset: "veryfast", CRF: "23"}

// reelMP4Profile encodes the short-form renditions, where the readiness
// window on the phone matters more than bytes. The reel HLS rungs are now
// copies of these; ultrafast is a bitrate-hungry encoder (720p ~1.6x the
// bytes of the old 2500k rung on the synthetic clip, at slightly higher
// SSIM) — "superfast"/CRF 26 matched the old rung's bytes for ~10% more
// encode time.
var reelMP4Profile = mp4Profile{Preset: "ultrafast", CRF: "28"}

// keyframeIntervalSeconds forces an IDR frame at every multiple of this many
// seconds of presentation time in every MP4 rendition. The HLS muxer can
// only cut a stream copy on a keyframe, so this is what makes the copied
// segments hlsSegmentSeconds long (and seekable): x264's default GOP of 250
// frames is 8-10 s, and a scene-cut-only stream can go longer. Expressed in
// time, not frames, so it holds for 24, 30, 60 fps and variable frame rate.
const keyframeIntervalSeconds = 2

// hlsSegmentSeconds is -hls_time for every rung. It must be a multiple of
// keyframeIntervalSeconds for a copied segment to land on it exactly.
const hlsSegmentSeconds = 6

func keyframeArgs() []string {
	return []string{"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", keyframeIntervalSeconds)}
}

// mp4TranscodeArgs builds the ffmpeg arguments for one MP4 rendition. No
// -threads: ffmpeg's default (0, auto) lets x264 use every CPU the container
// is given; pinning it would throw the worker's CPU limit away.
func mp4TranscodeArgs(inputPath, outputPath string, maxHeight int, p mp4Profile) []string {
	args := []string{
		"-y", "-i", inputPath,
		"-vf", fmt.Sprintf("scale=-2:%d", maxHeight),
		"-c:v", "libx264", "-preset", p.Preset, "-crf", p.CRF,
	}
	args = append(args, keyframeArgs()...)
	return append(args,
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		outputPath,
	)
}

// TranscodeToMP4 transcodes a long video to a specific resolution.
func TranscodeToMP4(ctx context.Context, inputPath, outputPath string, maxHeight int) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", mp4TranscodeArgs(inputPath, outputPath, maxHeight, longFormMP4Profile)...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ProbeVideo extracts width, height, and duration from a video file.
func ProbeVideo(ctx context.Context, inputPath string) (*VideoMeta, error) {
	// Get duration
	durArgs := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	}
	durOut, err := exec.CommandContext(ctx, "ffprobe", durArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe duration: %w", err)
	}
	durStr := strings.TrimSpace(string(durOut))
	durFloat, _ := strconv.ParseFloat(durStr, 64)

	// Get dimensions AND the display rotation. The coded frame alone is not
	// the picture: a phone recording carries a display matrix (ffprobe
	// side-data "rotation"; older muxers wrote a "rotate" stream tag) that
	// every player and ffmpeg's own autorotate honour. The stored size must
	// be the display size or it disagrees with every output we produce.
	dimArgs := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height:stream_side_data=rotation:stream_tags=rotate",
		"-of", "default=noprint_wrappers=1",
		inputPath,
	}
	dimOut, err := exec.CommandContext(ctx, "ffprobe", dimArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe dimensions: %w", err)
	}
	codedW, codedH, rotation := parseProbeDimensions(string(dimOut))
	w, h := displayDimensions(codedW, codedH, rotation)

	// Get codec and framerate info
	codecArgs := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,r_frame_rate",
		"-of", "default=noprint_wrappers=1",
		inputPath,
	}
	codecOut, _ := exec.CommandContext(ctx, "ffprobe", codecArgs...).Output()
	codecVideo := ""
	frameRate := 0.0
	for _, line := range strings.Split(string(codecOut), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "codec_name=") {
			codecVideo = strings.TrimPrefix(line, "codec_name=")
		}
		if strings.HasPrefix(line, "r_frame_rate=") {
			frStr := strings.TrimPrefix(line, "r_frame_rate=")
			frParts := strings.Split(frStr, "/")
			if len(frParts) == 2 {
				num, _ := strconv.ParseFloat(frParts[0], 64)
				den, _ := strconv.ParseFloat(frParts[1], 64)
				if den > 0 {
					frameRate = num / den
				}
			}
		}
	}

	audioArgs := []string{
		"-v", "error",
		"-select_streams", "a:0",
		"-show_entries", "stream=codec_name",
		"-of", "default=noprint_wrappers=1:nokey=1",
		inputPath,
	}
	audioOut, _ := exec.CommandContext(ctx, "ffprobe", audioArgs...).Output()
	codecAudio := strings.TrimSpace(string(audioOut))

	return &VideoMeta{
		Width:           w,
		Height:          h,
		CodedWidth:      codedW,
		CodedHeight:     codedH,
		Rotation:        rotation,
		DurationMs:      int(durFloat * 1000),
		DurationSeconds: int(durFloat),
		DurationFloat:   durFloat,
		CodecVideo:      codecVideo,
		CodecAudio:      codecAudio,
		FrameRate:       frameRate,
	}, nil
}

// ReelMaxDurationSeconds is the maximum duration (inclusive) for a video to be
// classified as a flick (short-form). Videos longer than this are considered long-form.
// 300s = 5 minutes (founder: shorts max 3–5 min; 5 chosen, 2026-09-05). Keep in
// sync with shared/postclassify.FlickMaxDurationSeconds in post-service.
const ReelMaxDurationSeconds = 300

// MinVideoDurationSeconds is the minimum accepted video duration.
const MinVideoDurationSeconds = 3

// MinVideoResolution is the minimum accepted video height (360p).
const MinVideoResolution = 360

// TranscodeToMP4Fast transcodes with ultrafast preset for reels where encode
// speed matters more than compression ratio.
func TranscodeToMP4Fast(ctx context.Context, inputPath, outputPath string, maxHeight int) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", mp4TranscodeArgs(inputPath, outputPath, maxHeight, reelMP4Profile)...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// TranscodeReel runs a reel-optimized pipeline: no 1080p/4K, faster preset, 720p cap.
func TranscodeReel(ctx context.Context, inputPath, tmpDir string) ([]TranscodeOutput, *VideoMeta, error) {
	meta, err := ProbeVideo(ctx, inputPath)
	if err != nil {
		return nil, nil, err
	}

	var outputs []TranscodeOutput

	// 1. Thumbnail at 25% of duration
	thumbAt := thumbnailTimestamp(meta.DurationFloat)
	thumbPath := filepath.Join(tmpDir, "thumb_150.jpg")
	if err := ExtractThumbnail(ctx, inputPath, thumbPath, thumbAt, 150); err == nil {
		outputs = append(outputs, TranscodeOutput{
			Name: "thumb_150", FilePath: thumbPath,
			Width: 150, Height: 150, Mime: "image/jpeg",
		})
	}

	// 2. 360p
	if meta.Height >= 360 {
		path360 := filepath.Join(tmpDir, "360p.mp4")
		if err := TranscodeToMP4Fast(ctx, inputPath, path360, 360); err == nil {
			outputs = append(outputs, TranscodeOutput{
				Name: "360p", FilePath: path360,
				Width: 0, Height: 360, Mime: "video/mp4",
			})
		}
	}

	// 3. 480p
	if meta.Height >= 480 {
		path480 := filepath.Join(tmpDir, "480p.mp4")
		if err := TranscodeToMP4Fast(ctx, inputPath, path480, 480); err == nil {
			outputs = append(outputs, TranscodeOutput{
				Name: "480p", FilePath: path480,
				Width: 0, Height: 480, Mime: "video/mp4",
			})
		}
	}

	// 4. 720p — cap for reels (no 1080p, no 4K)
	if meta.Height >= 720 {
		path720 := filepath.Join(tmpDir, "720p.mp4")
		if err := TranscodeToMP4Fast(ctx, inputPath, path720, 720); err == nil {
			outputs = append(outputs, TranscodeOutput{
				Name: "720p", FilePath: path720,
				Width: 0, Height: 720, Mime: "video/mp4",
			})
		}
	}

	return outputs, meta, nil
}

// TranscodeVideo runs the full transcoding pipeline for a video file.
// Returns output files that were created in tmpDir.
func TranscodeVideo(ctx context.Context, inputPath, tmpDir string) ([]TranscodeOutput, *VideoMeta, error) {
	meta, err := ProbeVideo(ctx, inputPath)
	if err != nil {
		return nil, nil, err
	}

	var outputs []TranscodeOutput

	// 1. Thumbnail at 25% of duration
	thumbAt := thumbnailTimestamp(meta.DurationFloat)
	thumbPath := filepath.Join(tmpDir, "thumb_150.jpg")
	if err := ExtractThumbnail(ctx, inputPath, thumbPath, thumbAt, 150); err == nil {
		outputs = append(outputs, TranscodeOutput{
			Name: "thumb_150", FilePath: thumbPath,
			Width: 150, Height: 150, Mime: "image/jpeg",
		})
	}

	// 2. 360p (if source is >= 360p)
	if meta.Height >= 360 {
		path360 := filepath.Join(tmpDir, "360p.mp4")
		if err := TranscodeToMP4(ctx, inputPath, path360, 360); err == nil {
			outputs = append(outputs, TranscodeOutput{
				Name: "360p", FilePath: path360,
				Width: 0, Height: 360, Mime: "video/mp4",
			})
		}
	}

	// 3. 480p (if source is >= 480p)
	if meta.Height >= 480 {
		path480 := filepath.Join(tmpDir, "480p.mp4")
		if err := TranscodeToMP4(ctx, inputPath, path480, 480); err == nil {
			outputs = append(outputs, TranscodeOutput{
				Name: "480p", FilePath: path480,
				Width: 0, Height: 480, Mime: "video/mp4",
			})
		}
	}

	// 4. 720p (if source is >= 720p)
	if meta.Height >= 720 {
		path720 := filepath.Join(tmpDir, "720p.mp4")
		if err := TranscodeToMP4(ctx, inputPath, path720, 720); err == nil {
			outputs = append(outputs, TranscodeOutput{
				Name: "720p", FilePath: path720,
				Width: 0, Height: 720, Mime: "video/mp4",
			})
		}
	}

	// 5. 1080p (if source is >= 1080p)
	if meta.Height >= 1080 {
		path1080 := filepath.Join(tmpDir, "1080p.mp4")
		if err := TranscodeToMP4(ctx, inputPath, path1080, 1080); err == nil {
			outputs = append(outputs, TranscodeOutput{
				Name: "1080p", FilePath: path1080,
				Width: 0, Height: 1080, Mime: "video/mp4",
			})
		}
	}

	// 6. 4k (if source is >= 2160p)
	if meta.Height >= 2160 {
		path4k := filepath.Join(tmpDir, "4k.mp4")
		if err := TranscodeToMP4(ctx, inputPath, path4k, 2160); err == nil {
			outputs = append(outputs, TranscodeOutput{
				Name: "4k", FilePath: path4k,
				Width: 0, Height: 2160, Mime: "video/mp4",
			})
		}
	}

	return outputs, meta, nil
}
