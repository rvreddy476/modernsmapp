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

// TranscodeOutput holds the result of a single transcode operation.
type TranscodeOutput struct {
	Name      string
	FilePath  string
	Width     int
	Height    int
	Mime      string
}

// VideoMeta holds extracted video metadata.
type VideoMeta struct {
	Width           int
	Height          int
	DurationMs      int // internal, from ffprobe (milliseconds)
	DurationSeconds int // for DB storage (seconds)
}

// ExtractThumbnail generates a JPEG thumbnail from a video at the given timestamp.
func ExtractThumbnail(ctx context.Context, inputPath, outputPath string, atSecond int, size int) error {
	args := []string{
		"-y", "-ss", fmt.Sprintf("%d", atSecond),
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

	return &VideoMeta{
		Width:           w,
		Height:          h,
		DurationMs:      int(durFloat * 1000),
		DurationSeconds: int(durFloat),
	}, nil
}

// ReelMaxDurationSeconds is the maximum duration (inclusive) for a video to be
// classified as a reel. Videos longer than this are considered long-form.
const ReelMaxDurationSeconds = 90

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

	// 1. Thumbnail
	thumbPath := filepath.Join(tmpDir, "thumb_150.jpg")
	if err := ExtractThumbnail(ctx, inputPath, thumbPath, 1, 150); err == nil {
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

	// 1. Thumbnail
	thumbPath := filepath.Join(tmpDir, "thumb_150.jpg")
	if err := ExtractThumbnail(ctx, inputPath, thumbPath, 1, 150); err == nil {
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
