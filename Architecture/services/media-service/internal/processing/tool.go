package processing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// Hardened ffmpeg / ffprobe execution for the video path (reels audit
// 2026-10-30, findings 3 and 7).
//
// Every ffmpeg and ffprobe the transcode worker runs over an uploaded file
// goes through runTool. It adds two things the bare exec.CommandContext
// calls did not have:
//
//  1. A DEADLINE. The calls used to run under the worker's process-lifetime
//     context, so a few-MB file holding hours of 8K frames could hold the
//     consumer (and every reel queued behind it on the partition) for days.
//     ffprobe gets ProbeTimeout. ffmpeg gets the per-call budget the worker
//     attached with WithToolTimeout (scaled to the probed duration), or
//     DefaultFFmpegTimeout when none was attached.
//
//  2. A RESTRICTED INPUT. ffmpeg probes an extension-less file by content and
//     will happily open an HLS or concat playlist, follow file:, http: or
//     subfile: references inside it, and run every demuxer it was built with.
//     Each input now gets `-protocol_whitelist file` and an explicit `-f`
//     demuxer chosen from the file's own magic bytes; a file whose magic is
//     not one of the allowed containers (MP4/MOV, Matroska/WebM, AVI) is
//     refused before ffmpeg sees it (ErrInputFormatRefused).

const (
	// ProbeTimeout bounds one ffprobe. Reading a container header is
	// seconds of work even for a long file; a probe that takes longer is
	// stuck on something hostile.
	ProbeTimeout = 2 * time.Minute
	// DefaultFFmpegTimeout bounds an ffmpeg call that carries no budget of
	// its own (WithToolTimeout). Generous, because the callers that rely on
	// it have not measured the input, but finite.
	DefaultFFmpegTimeout = 6 * time.Hour
)

// ErrInputFormatRefused: an ffmpeg/ffprobe input whose container is not in
// the allowed set. Deterministic: the same bytes are refused every time.
var ErrInputFormatRefused = errors.New("media container not allowed")

// ErrToolTimeout: an ffmpeg/ffprobe call ran past its deadline and was
// killed. Deterministic for a given input and budget, so the worker treats
// it as terminal (the parent context, if it ended, is reported separately).
var ErrToolTimeout = errors.New("media tool exceeded its time budget")

type toolTimeoutKey struct{}

// WithToolTimeout attaches the deadline every ffmpeg call under ctx gets
// (one call, not the whole job). ffprobe keeps ProbeTimeout.
func WithToolTimeout(ctx context.Context, d time.Duration) context.Context {
	if d <= 0 {
		return ctx
	}
	return context.WithValue(ctx, toolTimeoutKey{}, d)
}

// toolTimeout is the deadline for one call of tool under ctx.
func toolTimeout(ctx context.Context, tool string) time.Duration {
	if tool == "ffprobe" {
		return ProbeTimeout
	}
	if d, ok := ctx.Value(toolTimeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return DefaultFFmpegTimeout
}

// ffmpegDemuxer maps a container sniffed from the first bytes of a file to
// the ffmpeg demuxer that may open it. "" means not allowed.
func ffmpegDemuxer(head []byte) string {
	if len(head) < 12 {
		return ""
	}
	switch {
	case string(head[4:8]) == "ftyp":
		// mov,mp4,m4a,3gp,3g2,mj2 share one demuxer.
		return "mov"
	case head[0] == 0x1A && head[1] == 0x45 && head[2] == 0xDF && head[3] == 0xA3:
		return "matroska" // matroska,webm
	case string(head[0:4]) == "RIFF" && string(head[8:12]) == "AVI ":
		return "avi"
	}
	return ""
}

// sniffFile reads the first bytes of path.
func sniffFile(path string) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // G304: a worker-built temp path
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 64)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return head[:n], nil
}

// inputGuardArgs is what goes in front of one input: the protocol
// whitelist and the demuxer its magic bytes call for.
func inputGuardArgs(path string) ([]string, error) {
	head, err := sniffFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input header: %w", err)
	}
	demuxer := ffmpegDemuxer(head)
	if demuxer == "" {
		return nil, fmt.Errorf("%w: %s", ErrInputFormatRefused, describeHead(head))
	}
	return []string{"-protocol_whitelist", "file", "-f", demuxer}, nil
}

func describeHead(head []byte) string {
	if len(head) > 12 {
		head = head[:12]
	}
	return fmt.Sprintf("header %q", head)
}

// hardenArgs returns args with every input restricted. For ffmpeg each
// `-i <path>` is preceded by the guard; for ffprobe the input is the last
// argument and the guard goes first.
func hardenArgs(tool string, args []string) ([]string, error) {
	if tool == "ffprobe" {
		if len(args) == 0 {
			return nil, fmt.Errorf("ffprobe: no input")
		}
		guard, err := inputGuardArgs(args[len(args)-1])
		if err != nil {
			return nil, err
		}
		return append(guard, args...), nil
	}
	out := make([]string, 0, len(args)+8)
	inputs := 0
	for i := 0; i < len(args); i++ {
		if args[i] == "-i" && i+1 < len(args) {
			guard, err := inputGuardArgs(args[i+1])
			if err != nil {
				return nil, err
			}
			out = append(out, guard...)
			out = append(out, args[i], args[i+1])
			i++
			inputs++
			continue
		}
		out = append(out, args[i])
	}
	if inputs == 0 {
		return nil, fmt.Errorf("ffmpeg: no input")
	}
	return out, nil
}

// runTool runs ffmpeg or ffprobe over worker files with a hardened argv and
// a deadline. combined selects stdout+stderr (ffmpeg diagnostics) over
// stdout alone (ffprobe output).
func runTool(ctx context.Context, tool string, args []string, combined bool) ([]byte, error) {
	hardened, err := hardenArgs(tool, args)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, toolTimeout(ctx, tool))
	defer cancel()
	cmd := exec.CommandContext(callCtx, tool, hardened...) //nolint:gosec // G204: fixed binary, argv without a shell; paths are worker-built temp files
	var out []byte
	if combined {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output()
	}
	if err != nil && ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%w: %s after %s", ErrToolTimeout, tool, toolTimeout(ctx, tool))
	}
	return out, err
}

// runFFmpeg / runFFprobe are runTool for the two binaries.
func runFFmpeg(ctx context.Context, args ...string) ([]byte, error) {
	return runTool(ctx, "ffmpeg", args, true)
}

func runFFprobe(ctx context.Context, args ...string) ([]byte, error) {
	return runTool(ctx, "ffprobe", args, false)
}
