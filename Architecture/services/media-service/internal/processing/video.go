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

// GenerateHLSVariantsFor transcodes the video at inputPath into HLS adaptive bitrate segments
// for the rungs the plan calls for. Returns paths to the generated files (local temp paths)
// and the master playlist path.
func GenerateHLSVariantsFor(ctx context.Context, inputPath, outputDir string, plan HLSPlan) (masterPlaylistPath string, variantPaths []string, err error) {
	variants := hlsVariantsFor(plan)
	preset := hlsPreset(plan)
	for _, v := range variants {
		outputM3U8 := filepath.Join(outputDir, v.Quality+".m3u8")
		segmentPattern := filepath.Join(outputDir, v.Quality+"_%03d.ts")

		args := []string{
			"-i", inputPath,
			"-vf", fmt.Sprintf("scale=-2:%d", v.Height),
			"-c:v", "libx264", "-preset", preset,
			"-b:v", v.VideoBitrate,
			"-c:a", "aac", "-b:a", v.AudioBitrate,
			"-hls_time", "6",
			"-hls_playlist_type", "vod",
			"-hls_segment_filename", segmentPattern,
			outputM3U8,
			"-y",
		}

		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		out, cmdErr := cmd.CombinedOutput()
		if cmdErr != nil {
			return "", nil, fmt.Errorf("ffmpeg HLS %s failed: %w\n%s", v.Quality, cmdErr, out)
		}
		variantPaths = append(variantPaths, outputM3U8)
		// Also collect .ts segment paths
		tsFiles, _ := filepath.Glob(filepath.Join(outputDir, v.Quality+"_*.ts"))
		variantPaths = append(variantPaths, tsFiles...)
	}

	// Generate master playlist
	masterPlaylistPath = filepath.Join(outputDir, "master.m3u8")
	master := "#EXTM3U\n#EXT-X-VERSION:3\n"
	bandwidths := map[string]int{"360p": 800000, "720p": 2500000, "1080p": 5000000}
	resolutions := map[string]string{"360p": "640x360", "720p": "1280x720", "1080p": "1920x1080"}
	// Only the rungs that were encoded: a master listing a variant that does
	// not exist sends the player to a 404 mid-stream.
	for _, v := range variants {
		master += fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s\n%s.m3u8\n",
			bandwidths[v.Quality], resolutions[v.Quality], v.Quality)
	}
	os.WriteFile(masterPlaylistPath, []byte(master), 0644) //nolint:errcheck

	return masterPlaylistPath, variantPaths, nil
}

// TranscodeOutput holds the result of a single transcode operation.
type TranscodeOutput struct {
	Name     string
	FilePath string
	Width    int
	Height   int
	Mime     string
}

// VideoMeta holds extracted video metadata.
type VideoMeta struct {
	Width           int
	Height          int
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

// TranscodeToMP4 transcodes a video to a specific resolution.
func TranscodeToMP4(ctx context.Context, inputPath, outputPath string, maxHeight int) error {
	vf := fmt.Sprintf("scale=-2:%d", maxHeight)
	args := []string{
		"-y", "-i", inputPath,
		"-vf", vf,
		"-c:v", "libx264", "-preset", "medium", "-crf", "23",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		outputPath,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
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

	// Get dimensions
	dimArgs := []string{
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-of", "csv=s=x:p=0",
		inputPath,
	}
	dimOut, err := exec.CommandContext(ctx, "ffprobe", dimArgs...).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe dimensions: %w", err)
	}
	dimStr := strings.TrimSpace(string(dimOut))
	parts := strings.Split(dimStr, "x")
	w, _ := strconv.Atoi(parts[0])
	h := 0
	if len(parts) > 1 {
		h, _ = strconv.Atoi(parts[1])
	}

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
	vf := fmt.Sprintf("scale=-2:%d", maxHeight)
	args := []string{
		"-y", "-i", inputPath,
		"-vf", vf,
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "28",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		outputPath,
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
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
