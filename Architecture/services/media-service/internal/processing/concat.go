package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Joining a live stream's recording parts (16 Nov 2026).
//
// A live stream whose encoder reconnected is recorded as several MP4 parts
// (live-service-v2 migration 008). The import copies them under the asset's
// prefix and the worker joins them, in part order, into the one original
// before the normal transcode. Two ways:
//
//   - JoinCopy: the ffmpeg concat DEMUXER with stream copy
//     (-f concat -safe 0 -c copy). Seconds of work, no quality loss — but
//     only valid when every part carries the same streams with the same
//     parameters (LiveKit's composite egress normally writes identical
//     parts).
//   - JoinReencode: the concat FILTER over one hardened `-i` per part, each
//     scaled/padded to the first part's frame, one frame rate, stereo 48 kHz
//     audio (silence for a part without audio). Chosen when ffprobe shows
//     the parts differ, and as the fallback when a stream copy fails.
//
// The concat demuxer opens a playlist, which the hardened runner refuses on
// purpose (tool.go). The list here is written by the worker itself, names
// only worker-downloaded files whose magic bytes were checked as MP4, and
// runs under -protocol_whitelist file; that call alone goes through
// joinConcatFFmpeg. Every ffprobe and the re-encode use the hardened runner.

// JoinMode is how parts are joined.
type JoinMode string

const (
	JoinCopy     JoinMode = "copy"
	JoinReencode JoinMode = "reencode"
)

// MaxJoinParts bounds one join (live-service caps a stream at 20 parts).
const MaxJoinParts = 32

// RecordingPartsDir is where a multi-part import's copies live under the
// asset's prefix; the worker joins what it finds there when the original
// is absent (cmd/worker/recording_join.go).
const RecordingPartsDir = "parts/"

// RecordingPartKey is the media-bucket key of the n-th (1-based) part copy
// of an asset whose prefix is "user/<owner>/<id>/". Zero-padded so the
// keys sort in part order.
func RecordingPartKey(assetPrefix string, n int) string {
	return fmt.Sprintf("%s%s%03d.mp4", assetPrefix, RecordingPartsDir, n)
}

// ErrNoJoinableParts: not one part had a video stream.
var ErrNoJoinableParts = errors.New("no recording part has a video stream")

// PartVideo is the video stream parameters that must match for a copy.
type PartVideo struct {
	Codec     string
	Profile   string
	Width     int
	Height    int
	PixFmt    string
	FrameRate string // r_frame_rate, e.g. "30/1"
	TimeBase  string
}

// PartAudio is the audio stream parameters that must match for a copy.
type PartAudio struct {
	Codec      string
	SampleRate string
	Channels   int
}

// PartStreams is one part as ffprobe saw it.
type PartStreams struct {
	Video       *PartVideo
	Audio       *PartAudio
	DurationSec float64
}

// JoinPart is one downloaded part.
type JoinPart struct {
	Path    string
	Streams PartStreams
}

// Exec seams: tests replace these; production runs ffprobe/ffmpeg.
var (
	// joinProbe runs the hardened ffprobe (stdout only).
	joinProbe = func(ctx context.Context, args ...string) ([]byte, error) { return runFFprobe(ctx, args...) }
	// joinFFmpeg runs the hardened ffmpeg (every -i guarded).
	joinFFmpeg = func(ctx context.Context, args ...string) ([]byte, error) { return runFFmpeg(ctx, args...) }
	// joinConcatFFmpeg runs ffmpeg over a worker-built concat list, with
	// the per-call deadline but without the per-input guard (a list has no
	// container magic). Its argv carries -protocol_whitelist file itself.
	joinConcatFFmpeg = func(ctx context.Context, args ...string) ([]byte, error) {
		callCtx, cancel := context.WithTimeout(ctx, toolTimeout(ctx, "ffmpeg"))
		defer cancel()
		out, err := exec.CommandContext(callCtx, "ffmpeg", args...).CombinedOutput() //nolint:gosec // G204: fixed binary, argv without a shell; the list and paths are worker-built temp files
		if err != nil && ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return out, fmt.Errorf("%w: ffmpeg concat after %s", ErrToolTimeout, toolTimeout(ctx, "ffmpeg"))
		}
		return out, err
	}
)

// ProbeJoinPart reads the streams of one part.
func ProbeJoinPart(ctx context.Context, path string) (PartStreams, error) {
	out, err := joinProbe(ctx,
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name,profile,width,height,pix_fmt,r_frame_rate,time_base,sample_rate,channels:format=duration",
		"-of", "json",
		path)
	if err != nil {
		return PartStreams{}, fmt.Errorf("ffprobe part: %w", err)
	}
	return ParsePartProbe(out)
}

// ParsePartProbe parses ProbeJoinPart's JSON: the first video and the
// first audio stream count. Pure.
func ParsePartProbe(out []byte) (PartStreams, error) {
	var doc struct {
		Streams []struct {
			CodecType  string `json:"codec_type"`
			CodecName  string `json:"codec_name"`
			Profile    string `json:"profile"`
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			PixFmt     string `json:"pix_fmt"`
			RFrameRate string `json:"r_frame_rate"`
			TimeBase   string `json:"time_base"`
			SampleRate string `json:"sample_rate"`
			Channels   int    `json:"channels"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return PartStreams{}, fmt.Errorf("parse ffprobe part: %w", err)
	}
	var ps PartStreams
	for _, s := range doc.Streams {
		switch s.CodecType {
		case "video":
			if ps.Video == nil {
				ps.Video = &PartVideo{Codec: s.CodecName, Profile: s.Profile, Width: s.Width, Height: s.Height,
					PixFmt: s.PixFmt, FrameRate: s.RFrameRate, TimeBase: s.TimeBase}
			}
		case "audio":
			if ps.Audio == nil {
				ps.Audio = &PartAudio{Codec: s.CodecName, SampleRate: s.SampleRate, Channels: s.Channels}
			}
		}
	}
	ps.DurationSec, _ = strconv.ParseFloat(strings.TrimSpace(doc.Format.Duration), 64)
	return ps, nil
}

// ChooseJoinMode is copy only when every part has a video stream with the
// same parameters and the same audio (present in all with the same
// parameters, or absent in all); anything else re-encodes. Pure.
func ChooseJoinMode(parts []PartStreams) JoinMode {
	if len(parts) < 2 {
		return JoinCopy
	}
	first := parts[0]
	if first.Video == nil {
		return JoinReencode
	}
	for _, p := range parts[1:] {
		if p.Video == nil || *p.Video != *first.Video {
			return JoinReencode
		}
		if (p.Audio == nil) != (first.Audio == nil) {
			return JoinReencode
		}
		if p.Audio != nil && *p.Audio != *first.Audio {
			return JoinReencode
		}
	}
	return JoinCopy
}

// BuildConcatList is the concat demuxer's list for paths, in order: one
// `file '<path>'` line each; a single quote in a path closes the quoting,
// is backslash-escaped and reopens it (the demuxer's own rule). A path with
// a control character is refused. Pure.
func BuildConcatList(paths []string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("concat list: no parts")
	}
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, p := range paths {
		if p == "" {
			return "", errors.New("concat list: empty path")
		}
		for _, r := range p {
			if r < 0x20 || r == 0x7f {
				return "", fmt.Errorf("concat list: control character in %q", p)
			}
		}
		b.WriteString("file '")
		b.WriteString(strings.ReplaceAll(p, "'", `'\''`))
		b.WriteString("'\n")
	}
	return b.String(), nil
}

// concatCopyArgs is the stream-copy join of listPath into outPath.
func concatCopyArgs(listPath, outPath string) []string {
	return []string{
		"-y",
		"-protocol_whitelist", "file",
		"-f", "concat", "-safe", "0",
		"-i", listPath,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c", "copy",
		"-movflags", "+faststart",
		"-f", "mp4",
		outPath,
	}
}

// even rounds n down to an even number, at least 2.
func even(n int) int {
	if n < 2 {
		return 2
	}
	return n &^ 1
}

// concatReencodeArgs is the concat-filter join of parts into outPath: the
// first video part's frame and frame rate, stereo 48 kHz audio when any
// part has audio (silence of the part's length for one without). Pure.
func concatReencodeArgs(parts []JoinPart, outPath string) ([]string, error) {
	var ref *PartVideo
	anyAudio := false
	for _, p := range parts {
		if p.Streams.Video != nil && ref == nil {
			ref = p.Streams.Video
		}
		if p.Streams.Audio != nil {
			anyAudio = true
		}
	}
	if ref == nil {
		return nil, ErrNoJoinableParts
	}
	w, h := even(ref.Width), even(ref.Height)
	fps := ref.FrameRate
	if fps == "" || fps == "0/0" {
		fps = "30"
	}
	args := []string{"-y"}
	for _, p := range parts {
		args = append(args, "-i", p.Path)
	}
	var graph, chain strings.Builder
	for i, p := range parts {
		fmt.Fprintf(&graph, "[%d:v:0]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=%s,format=yuv420p[v%d];",
			i, w, h, w, h, fps, i)
		fmt.Fprintf(&chain, "[v%d]", i)
		if !anyAudio {
			continue
		}
		if p.Streams.Audio != nil {
			fmt.Fprintf(&graph, "[%d:a:0]aresample=48000,aformat=sample_fmts=fltp:channel_layouts=stereo[a%d];", i, i)
		} else {
			d := p.Streams.DurationSec
			if d <= 0 {
				d = 0.04
			}
			fmt.Fprintf(&graph, "anullsrc=r=48000:cl=stereo,atrim=duration=%s,aformat=sample_fmts=fltp:channel_layouts=stereo[a%d];",
				strconv.FormatFloat(d, 'f', 3, 64), i)
		}
		fmt.Fprintf(&chain, "[a%d]", i)
	}
	a := 0
	if anyAudio {
		a = 1
	}
	fmt.Fprintf(&graph, "%sconcat=n=%d:v=1:a=%d", chain.String(), len(parts), a)
	if anyAudio {
		graph.WriteString("[v][a]")
	} else {
		graph.WriteString("[v]")
	}
	args = append(args, "-filter_complex", graph.String(), "-map", "[v]")
	if anyAudio {
		args = append(args, "-map", "[a]", "-c:a", "aac", "-b:a", "160k")
	}
	args = append(args,
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "20", "-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-f", "mp4",
		outPath)
	return args, nil
}

// JoinVideoParts joins parts, in order, into outPath and reports how. A
// part without a video stream is left out (it cannot be part of a video);
// none left is ErrNoJoinableParts. A failed stream copy falls back to the
// re-encode once. listDir holds the concat list (removed afterwards).
func JoinVideoParts(ctx context.Context, parts []JoinPart, listDir, outPath string) (JoinMode, error) {
	var usable []JoinPart
	for _, p := range parts {
		if p.Streams.Video != nil {
			usable = append(usable, p)
		}
	}
	if len(usable) == 0 {
		return "", ErrNoJoinableParts
	}
	if len(usable) > MaxJoinParts {
		return "", fmt.Errorf("join: %d parts, at most %d", len(usable), MaxJoinParts)
	}
	streams := make([]PartStreams, len(usable))
	var total float64
	for i, p := range usable {
		streams[i] = p.Streams
		total += p.Streams.DurationSec
	}
	ctx = WithToolTimeout(ctx, FFmpegCallTimeout(total))

	if ChooseJoinMode(streams) == JoinCopy {
		paths := make([]string, len(usable))
		for i, p := range usable {
			paths[i] = p.Path
		}
		list, err := BuildConcatList(paths)
		if err != nil {
			return "", err
		}
		f, err := os.CreateTemp(listDir, "concat-*.txt")
		if err != nil {
			return "", fmt.Errorf("concat list: %w", err)
		}
		listPath := f.Name()
		defer os.Remove(listPath)
		if _, err := f.WriteString(list); err != nil {
			_ = f.Close()
			return "", fmt.Errorf("concat list: %w", err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("concat list: %w", err)
		}
		out, err := joinConcatFFmpeg(ctx, concatCopyArgs(listPath, outPath)...)
		if err == nil {
			return JoinCopy, nil
		}
		if ctx.Err() != nil {
			return "", err
		}
		_ = os.Remove(outPath)
		// A copy can still fail on what ffprobe does not show (timestamps,
		// extradata); the re-encode below accepts anything decodable.
		log.Printf("media join: stream copy failed, re-encoding: %v: %s", err, lastLine(out))
	}
	args, err := concatReencodeArgs(usable, outPath)
	if err != nil {
		return "", err
	}
	if out, err := joinFFmpeg(ctx, args...); err != nil {
		return "", fmt.Errorf("ffmpeg concat re-encode: %w: %s", err, lastLine(out))
	}
	return JoinReencode, nil
}
