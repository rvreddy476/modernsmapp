package dubbing

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Segment is one synthesised utterance and the slot it must land in.
type Segment struct {
	// StartMs / EndMs is where the source speech sat in the video.
	StartMs int
	EndMs   int
	// AudioMs is the length of the synthesised clip for this segment.
	AudioMs int
	// Path is the clip's file (one ffmpeg input each, in order).
	Path string
}

// Time-stretch bounds. ffmpeg's atempo accepts 0.5–2.0 per instance, so a
// larger factor is a chain; past maxTempo speech is no longer intelligible,
// so the clip is left to overlap the next slot instead (amix sums them).
const (
	minTempo = 0.5
	maxTempo = 2.0
	// maxTotalTempo caps the chained factor.
	maxTotalTempo = 4.0
	// minSlotMs is the smallest slot a clip is stretched into.
	minSlotMs = 200
)

// AssemblyFilter builds the ffmpeg -filter_complex graph that lays every
// segment at its start time and yields one track exactly durationMs long.
//
// Input i of the ffmpeg command is segments[i].Path. Each clip is normalised
// to 44.1 kHz stereo, sped up with a chained atempo when it is longer than
// the room it has (its own slot, extended to the next segment's start), and
// delayed to its start. The clips are mixed without attenuation, padded with
// silence to the video's length and trimmed to it. The returned label is the
// output pad to -map.
//
// Pure: no ffmpeg needed, so the command shape is unit-tested.
func AssemblyFilter(segments []Segment, durationMs int) (filter, outLabel string, err error) {
	if len(segments) == 0 {
		return "", "", errors.New("dubbing: no segments to assemble")
	}
	if durationMs <= 0 {
		return "", "", errors.New("dubbing: duration must be positive")
	}
	var parts []string
	var labels []string
	for i, seg := range segments {
		room := seg.EndMs - seg.StartMs
		// The slot runs to the next segment's start (or the video's end)
		// when that is later than the transcribed end: silence between
		// utterances is room the dub may use.
		next := durationMs
		if i+1 < len(segments) {
			next = segments[i+1].StartMs
		}
		if next-seg.StartMs > room {
			room = next - seg.StartMs
		}
		if room < minSlotMs {
			room = minSlotMs
		}
		chain := []string{"aformat=sample_rates=44100:channel_layouts=stereo"}
		if seg.AudioMs > room {
			factor := float64(seg.AudioMs) / float64(room)
			chain = append(chain, tempoChain(factor)...)
		}
		start := seg.StartMs
		if start < 0 {
			start = 0
		}
		chain = append(chain, fmt.Sprintf("adelay=delays=%d:all=1", start))
		label := fmt.Sprintf("s%d", i)
		parts = append(parts, fmt.Sprintf("[%d:a]%s[%s]", i, strings.Join(chain, ","), label))
		labels = append(labels, "["+label+"]")
	}
	tail := fmt.Sprintf("apad=whole_dur=%dms,atrim=end=%s", durationMs, msToSeconds(durationMs))
	if len(labels) == 1 {
		parts = append(parts, fmt.Sprintf("%s%s[out]", labels[0], tail))
	} else {
		parts = append(parts, fmt.Sprintf("%samix=inputs=%d:normalize=0:dropout_transition=0,%s[out]",
			strings.Join(labels, ""), len(labels), tail))
	}
	return strings.Join(parts, ";"), "[out]", nil
}

// tempoChain splits a speed-up factor into atempo stages within [0.5, 2.0],
// capped at maxTotalTempo.
func tempoChain(factor float64) []string {
	if factor > maxTotalTempo {
		factor = maxTotalTempo
	}
	if factor < minTempo {
		factor = minTempo
	}
	var out []string
	for factor > maxTempo {
		out = append(out, "atempo=2.0")
		factor /= maxTempo
	}
	if factor > 1.005 || factor < 0.995 {
		out = append(out, "atempo="+strconv.FormatFloat(factor, 'f', 4, 64))
	}
	return out
}

func msToSeconds(ms int) string {
	return strconv.FormatFloat(float64(ms)/1000.0, 'f', 3, 64)
}

// AssembleArgs is the full ffmpeg argv for AssemblyFilter: one -i per
// segment, the graph, the output pad, and a hard -t at the video's length.
func AssembleArgs(segments []Segment, durationMs int, outPath string) ([]string, error) {
	filter, label, err := AssemblyFilter(segments, durationMs)
	if err != nil {
		return nil, err
	}
	args := []string{"-y"}
	for _, seg := range segments {
		args = append(args, "-i", seg.Path)
	}
	args = append(args,
		"-filter_complex", filter,
		"-map", label,
		"-t", msToSeconds(durationMs),
		"-c:a", "aac", "-b:a", "128k",
		"-f", "mp4",
		outPath,
	)
	return args, nil
}

// AssembleWithFFmpeg runs AssembleArgs.
func AssembleWithFFmpeg(ctx context.Context, segments []Segment, durationMs int, outPath string) error {
	args, err := AssembleArgs(segments, durationMs, outPath)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		tail := string(out)
		if len(tail) > 2048 {
			tail = "…" + tail[len(tail)-2048:]
		}
		return fmt.Errorf("ffmpeg dub assembly failed: %w\n%s", err, tail)
	}
	return nil
}

// ProbeDurationMs reads a media file's duration with ffprobe.
func ProbeDurationMs(ctx context.Context, path string) (int, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration: %w", err)
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration parse: %w", err)
	}
	return int(sec*1000 + 0.5), nil
}
