package processing

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
)

// One rung at a time (long-video audit lv-5 / lv-13 / lv-15, 2026-11-17).
//
// The worker used to encode every rendition into one temp directory, keep
// them all until the job ended, and only then upload anything; a rung that
// failed was dropped without a word. It now asks for the plan (RungPlan),
// encodes one rung (EncodeRung), uploads it and its HLS copy, records it as
// done, and deletes the local files before the next rung starts. A failed
// rung is returned as an error for the worker to log and record.

// RungKind is what a rung produces.
type RungKind string

const (
	// RungThumb is the 150 px JPEG poster.
	RungThumb RungKind = "thumb"
	// RungMP4 is an H.264/AAC MP4 rendition of one height.
	RungMP4 RungKind = "mp4"
)

// Rung is one rendition of the ladder.
type Rung struct {
	Name   string // variant name: "thumb_150", "360p", ... "4k"
	Height int    // output height; 0 for the thumbnail
	Kind   RungKind
}

// fullRungLadder is every rung a video may get, in encode order.
var fullRungLadder = []Rung{
	{"thumb_150", 0, RungThumb},
	{"360p", 360, RungMP4},
	{"480p", 480, RungMP4},
	{"720p", 720, RungMP4},
	{"1080p", 1080, RungMP4},
	{"4k", 2160, RungMP4},
}

// reelMaxRungHeight caps reels at 720p: no 1080p or 4K.
const reelMaxRungHeight = 720

// RungPlan is the ladder for a probed video: the thumbnail always, every MP4
// rung no taller than the source, and nothing above 720p for a reel.
func RungPlan(meta *VideoMeta, reel bool) []Rung {
	var out []Rung
	for _, r := range fullRungLadder {
		if r.Kind == RungMP4 {
			if meta == nil || meta.Height < r.Height {
				continue
			}
			if reel && r.Height > reelMaxRungHeight {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}

// RungRecipe names everything that decides a rung's bytes apart from the
// source: a stored rung is reused only when the recipe it was made with is
// the one the code would use now. Bump the trailing version when an encode
// argument changes in a way the profile does not name.
func RungRecipe(r Rung, reel bool) string {
	if r.Kind == RungThumb {
		return "thumb/150/q5/v1"
	}
	p := longFormMP4Profile
	if reel {
		p = reelMP4Profile
	}
	return fmt.Sprintf("mp4/%d/%s/crf%s/kf%d/hls%d/v1", r.Height, p.Preset, p.CRF, keyframeIntervalSeconds, hlsSegmentSeconds)
}

// EncodeRung produces one rung from the original into dir. The file name is
// the one the old all-at-once pipeline used.
func EncodeRung(ctx context.Context, inputPath, dir string, r Rung, meta *VideoMeta, reel bool) (TranscodeOutput, error) {
	return encodeRung(ctx, inputPath, dir, r, meta, reel, runFFmpegArgs)
}

func runFFmpegArgs(ctx context.Context, args []string) ([]byte, error) {
	return runFFmpeg(ctx, args...)
}

func encodeRung(ctx context.Context, inputPath, dir string, r Rung, meta *VideoMeta, reel bool, run ffmpegRunner) (TranscodeOutput, error) {
	switch r.Kind {
	case RungThumb:
		p := filepath.Join(dir, "thumb_150.jpg")
		dur := 0.0
		if meta != nil {
			dur = meta.DurationFloat
		}
		if err := extractThumbnailWith(ctx, run, inputPath, p, thumbnailTimestamp(dur), 150); err != nil {
			return TranscodeOutput{}, err
		}
		return TranscodeOutput{Name: r.Name, FilePath: p, Width: 150, Height: 150, Mime: "image/jpeg"}, nil
	case RungMP4:
		p := filepath.Join(dir, r.Name+".mp4")
		prof := longFormMP4Profile
		if reel {
			prof = reelMP4Profile
		}
		if out, err := run(ctx, mp4TranscodeArgs(inputPath, p, r.Height, prof)); err != nil {
			return TranscodeOutput{}, fmt.Errorf("ffmpeg %dp: %w: %s", r.Height, err, lastLine(out))
		}
		return TranscodeOutput{Name: r.Name, FilePath: p, Width: 0, Height: r.Height, Mime: "video/mp4"}, nil
	}
	return TranscodeOutput{}, fmt.Errorf("unknown rung kind %q", r.Kind)
}

// transcodeLadder encodes every rung of the plan into tmpDir. A failed rung
// is LOGGED and left out (lv-15: it used to vanish without a line); the
// caller keeps "ready with fewer rungs".
func transcodeLadder(ctx context.Context, inputPath, tmpDir string, meta *VideoMeta, reel bool, run ffmpegRunner) []TranscodeOutput {
	var outputs []TranscodeOutput
	for _, r := range RungPlan(meta, reel) {
		out, err := encodeRung(ctx, inputPath, tmpDir, r, meta, reel, run)
		if err != nil {
			log.Printf("Warning: rung %s not produced: %v", r.Name, err)
			continue
		}
		outputs = append(outputs, out)
	}
	return outputs
}
