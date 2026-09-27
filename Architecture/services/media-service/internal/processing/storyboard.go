package processing

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Storyboard (MTube seek preview, 2026-09-27).
//
// For a video longer than StoryboardMinDurationSeconds the transcode worker
// produces one JPEG sprite sheet of 160x90 tiles, ten per row, one frame
// every duration/100 seconds (so always 100 frames in a 10x10 grid), and a
// WebVTT file whose cues map each time range to a tile with the
// `#xywh=x,y,w,h` media-fragment syntax players understand.
//
// Both are recorded as media_variants rows (storyboard_jpg, storyboard_vtt)
// so GET /v1/media/:id/serve/storyboard_vtt and /serve/storyboard_jpg
// deliver them through the ordinary gate. The VTT references the sprite by
// its bare object name (`storyboard.jpg#xywh=…`), which is what the worker
// can know; a player that fetches the VTT through the signed /serve
// redirect resolves that relative to the signed URL, whose signature does
// not cover the sibling object — so the web player fetches the VTT text
// and rewrites the image reference to /serve/storyboard_jpg.

const (
	// StoryboardMinDurationSeconds: shorter videos get no storyboard; a
	// hover preview on a 30-second clip is the clip.
	StoryboardMinDurationSeconds = 60.0
	StoryboardTileWidth          = 160
	StoryboardTileHeight         = 90
	StoryboardColumns            = 10
	StoryboardMaxFrames          = 100
	// StoryboardJPEGQuality is ffmpeg's -q:v (2 best … 31 worst).
	StoryboardJPEGQuality = 5

	StoryboardJPGVariant = "storyboard_jpg"
	StoryboardVTTVariant = "storyboard_vtt"
	StoryboardJPGObject  = "storyboard.jpg"
	StoryboardVTTObject  = "storyboard.vtt"
)

// StoryboardPlan is the pure description of one storyboard.
type StoryboardPlan struct {
	DurationSeconds float64
	// IntervalSeconds between frames: DurationSeconds / StoryboardMaxFrames.
	IntervalSeconds float64
	Frames          int
	Columns         int
	Rows            int
}

// PlanStoryboard decides whether a video gets a storyboard and, if so, its
// grid. ok is false for videos of StoryboardMinDurationSeconds or less, or
// with no known duration.
func PlanStoryboard(durationSeconds float64) (plan StoryboardPlan, ok bool) {
	if durationSeconds <= StoryboardMinDurationSeconds || math.IsNaN(durationSeconds) || math.IsInf(durationSeconds, 0) {
		return StoryboardPlan{}, false
	}
	frames := StoryboardMaxFrames
	return StoryboardPlan{
		DurationSeconds: durationSeconds,
		IntervalSeconds: durationSeconds / float64(frames),
		Frames:          frames,
		Columns:         StoryboardColumns,
		Rows:            (frames + StoryboardColumns - 1) / StoryboardColumns,
	}, true
}

// SheetWidth / SheetHeight are the sprite's pixel size.
func (p StoryboardPlan) SheetWidth() int  { return p.Columns * StoryboardTileWidth }
func (p StoryboardPlan) SheetHeight() int { return p.Rows * StoryboardTileHeight }

// StoryboardFFmpegArgs is the ffmpeg argv that renders the sprite sheet:
// sample at 1/interval fps, scale each frame to a tile, tile them
// columns x rows into ONE output image at JPEG quality q.
//
// `-frames:v 1` stops after the first (only) sheet; the tile filter pads
// an incomplete final sheet with black, so a source that yields 99 frames
// through rounding still produces a full-size image.
func StoryboardFFmpegArgs(inputPath, outputPath string, plan StoryboardPlan) []string {
	fps := 1 / plan.IntervalSeconds
	return []string{
		"-y",
		"-i", inputPath,
		"-vf", fmt.Sprintf("fps=%s,scale=%d:%d,tile=%dx%d",
			formatFloat(fps), StoryboardTileWidth, StoryboardTileHeight, plan.Columns, plan.Rows),
		"-frames:v", "1",
		"-q:v", fmt.Sprintf("%d", StoryboardJPEGQuality),
		outputPath,
	}
}

// StoryboardVTT renders the cue file. imageRef is what each cue points at
// before the `#xywh=` fragment — the sprite's object name.
func StoryboardVTT(plan StoryboardPlan, imageRef string) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n")
	for i := 0; i < plan.Frames; i++ {
		start := float64(i) * plan.IntervalSeconds
		end := float64(i+1) * plan.IntervalSeconds
		if i == plan.Frames-1 || end > plan.DurationSeconds {
			end = plan.DurationSeconds
		}
		if end <= start {
			break
		}
		col := i % plan.Columns
		row := i / plan.Columns
		fmt.Fprintf(&b, "\n%s --> %s\n%s#xywh=%d,%d,%d,%d\n",
			vttTimestamp(start), vttTimestamp(end), imageRef,
			col*StoryboardTileWidth, row*StoryboardTileHeight, StoryboardTileWidth, StoryboardTileHeight)
	}
	return b.String()
}

// vttTimestamp formats seconds as HH:MM:SS.mmm.
func vttTimestamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	ms := int64(math.Round(seconds * 1000))
	h := ms / 3_600_000
	ms -= h * 3_600_000
	m := ms / 60_000
	ms -= m * 60_000
	s := ms / 1000
	ms -= s * 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

// formatFloat prints a rate for a filter argument without exponent
// notation or a trailing run of zeros.
func formatFloat(f float64) string {
	s := fmt.Sprintf("%.6f", f)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// GenerateStoryboard renders the sprite and writes the VTT next to it. The
// two outputs carry the variant names the rows use and the object names
// the files are stored under.
func GenerateStoryboard(ctx context.Context, inputPath, tmpDir string, plan StoryboardPlan) ([]TranscodeOutput, error) {
	jpgPath := filepath.Join(tmpDir, StoryboardJPGObject)
	cmd := exec.CommandContext(ctx, "ffmpeg", StoryboardFFmpegArgs(inputPath, jpgPath, plan)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ffmpeg storyboard failed: %w\n%s", err, out)
	}
	vttPath := filepath.Join(tmpDir, StoryboardVTTObject)
	if err := os.WriteFile(vttPath, []byte(StoryboardVTT(plan, StoryboardJPGObject)), 0o644); err != nil {
		return nil, fmt.Errorf("write storyboard vtt: %w", err)
	}
	return []TranscodeOutput{
		{
			Name: StoryboardJPGVariant, ObjectName: StoryboardJPGObject, FilePath: jpgPath,
			Width: plan.SheetWidth(), Height: plan.SheetHeight(), Mime: "image/jpeg",
		},
		{
			Name: StoryboardVTTVariant, ObjectName: StoryboardVTTObject, FilePath: vttPath,
			Width: 0, Height: 0, Mime: "text/vtt",
		},
	}, nil
}
