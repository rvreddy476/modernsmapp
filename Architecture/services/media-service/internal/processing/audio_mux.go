package processing

import (
	"context"
	"fmt"
	"os/exec"
)

// Alternate audio tracks (2026-09-27): replace a video variant's audio with a
// language track without touching the picture.

// MuxAudioArgs is the ffmpeg argv that lays audioPath over videoPath's first
// video stream: video copied as-is, audio re-encoded to AAC 128k, the output
// cut to the shorter of the two, faststart so the MP4 streams progressively.
// Pure so the command shape is unit-tested; MuxAudio runs it.
func MuxAudioArgs(videoPath, audioPath, outPath string) []string {
	return []string{
		"-y",
		"-i", videoPath,
		"-i", audioPath,
		"-map", "0:v:0",
		"-map", "1:a:0",
		"-c:v", "copy",
		"-c:a", "aac",
		"-b:a", "128k",
		"-shortest",
		"-movflags", "+faststart",
		"-f", "mp4",
		outPath,
	}
}

// MuxAudio runs MuxAudioArgs through ffmpeg.
func MuxAudio(ctx context.Context, videoPath, audioPath, outPath string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", MuxAudioArgs(videoPath, audioPath, outPath)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg audio mux failed: %w\n%s", err, truncateOutput(out, 2048))
	}
	return nil
}

func truncateOutput(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return "…" + string(b[len(b)-n:])
}
