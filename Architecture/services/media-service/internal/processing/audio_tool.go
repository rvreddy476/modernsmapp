package processing

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Hardened ffmpeg / ffprobe for the audio paths (reels-16, was REELS-B-3).
//
// Creator-uploaded audio (alternate audio tracks, voice posts) and the sound
// extracted from a video used to reach ffprobe/ffmpeg through bare
// exec.CommandContext: no protocol whitelist, no forced demuxer and no
// deadline of their own. runAudioTool gives them what runTool (tool.go) gives
// the video path, reusing its pieces: the sniffed header (sniffFile), the
// refusal and timeout errors, ProbeTimeout and the WithToolTimeout budget.
// It differs only in the allowed containers, which for audio include the
// audio-only formats ValidateAudioMagicBytes admits at upload; a playlist,
// a concat list or anything unrecognised is refused before the tool runs.

// AudioFFmpegTimeout bounds one audio ffmpeg call that carries no budget of
// its own (WithToolTimeout). Re-encoding audio runs far faster than real
// time, so this is generous even for a long video's track.
const AudioFFmpegTimeout = 30 * time.Minute

// audioCommand builds the process; a variable so tests can stand in for the
// binary without ffmpeg installed.
var audioCommand = exec.CommandContext

// audioDemuxer maps the first bytes of an audio (or audio-bearing video)
// file to the ffmpeg demuxer that may open it. "" means not allowed.
func audioDemuxer(head []byte) string {
	if d := ffmpegDemuxer(head); d != "" {
		return d // mov (mp4/m4a), matroska (webm), avi
	}
	if len(head) < 12 {
		return ""
	}
	switch {
	case string(head[0:4]) == "OggS":
		return "ogg"
	case string(head[0:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return "wav"
	case string(head[0:4]) == "fLaC":
		return "flac"
	case string(head[0:6]) == "#!AMR\n":
		return "amr"
	case string(head[0:3]) == "ID3":
		return "mp3"
	case head[0] == 0xFF && head[1]&0xF6 == 0xF0:
		// ADTS AAC: frame sync with layer bits 00.
		return "aac"
	case head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return "mp3"
	}
	return ""
}

func audioInputGuard(path string) ([]string, error) {
	head, err := sniffFile(path)
	if err != nil {
		return nil, fmt.Errorf("read input header: %w", err)
	}
	demuxer := audioDemuxer(head)
	if demuxer == "" {
		return nil, fmt.Errorf("%w: %s", ErrInputFormatRefused, describeHead(head))
	}
	return []string{"-protocol_whitelist", "file", "-f", demuxer}, nil
}

// hardenAudioArgs is hardenArgs (tool.go) with the audio allow-list.
func hardenAudioArgs(tool string, args []string) ([]string, error) {
	if tool == "ffprobe" {
		if len(args) == 0 {
			return nil, fmt.Errorf("ffprobe: no input")
		}
		guard, err := audioInputGuard(args[len(args)-1])
		if err != nil {
			return nil, err
		}
		return append(guard, args...), nil
	}
	out := make([]string, 0, len(args)+8)
	inputs := 0
	for i := 0; i < len(args); i++ {
		if args[i] == "-i" && i+1 < len(args) {
			guard, err := audioInputGuard(args[i+1])
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

// audioToolTimeout: ffprobe gets ProbeTimeout; ffmpeg the attached budget,
// else AudioFFmpegTimeout.
func audioToolTimeout(ctx context.Context, tool string) time.Duration {
	if tool == "ffprobe" {
		return ProbeTimeout
	}
	if d, ok := ctx.Value(toolTimeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return AudioFFmpegTimeout
}

// runAudioTool runs ffmpeg or ffprobe over local audio files with a hardened
// argv and a deadline. combined selects stdout+stderr over stdout alone.
func runAudioTool(ctx context.Context, tool string, args []string, combined bool) ([]byte, error) {
	hardened, err := hardenAudioArgs(tool, args)
	if err != nil {
		return nil, err
	}
	limit := audioToolTimeout(ctx, tool)
	callCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	cmd := audioCommand(callCtx, tool, hardened...) //nolint:gosec // G204: fixed binary, argv without a shell; paths are service-built temp files
	var out []byte
	if combined {
		out, err = cmd.CombinedOutput()
	} else {
		out, err = cmd.Output()
	}
	if err != nil && ctx.Err() == nil && errors.Is(callCtx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%w: %s after %s", ErrToolTimeout, tool, limit)
	}
	return out, err
}
