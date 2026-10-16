package processing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
)

// AudioFileProbe is what ffprobe says about an uploaded audio file: whether
// it has an audio stream, whether it carries a real video stream (cover art
// on an MP3 or M4A, an attached picture, does not count), the first audio
// codec, and the longest duration either the stream or the container
// reports (a limit is checked against the longer of the two).
type AudioFileProbe struct {
	HasAudio   bool
	HasVideo   bool
	Codec      string
	DurationMs int
}

// ErrProbeUnreadable: ffprobe produced nothing usable.
var ErrProbeUnreadable = errors.New("ffprobe: unreadable output")

// ProbeAudioFile runs ffprobe over a local file.
func ProbeAudioFile(ctx context.Context, path string) (*AudioFileProbe, error) {
	out, err := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name,duration:stream_disposition=attached_pic:format=duration",
		"-of", "json",
		path,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe audio file: %w", err)
	}
	return ParseAudioFileProbe(out)
}

// ParseAudioFileProbe reads ffprobe's -of json answer (split out so the
// rules are testable without ffprobe).
func ParseAudioFileProbe(out []byte) (*AudioFileProbe, error) {
	var doc struct {
		Streams []struct {
			CodecType   string `json:"codec_type"`
			CodecName   string `json:"codec_name"`
			Duration    string `json:"duration"`
			Disposition struct {
				AttachedPic int `json:"attached_pic"`
			} `json:"disposition"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProbeUnreadable, err)
	}
	p := &AudioFileProbe{}
	longest := seconds(doc.Format.Duration)
	for _, s := range doc.Streams {
		switch s.CodecType {
		case "audio":
			if !p.HasAudio {
				p.Codec = s.CodecName
			}
			p.HasAudio = true
			if d := seconds(s.Duration); d > longest {
				longest = d
			}
		case "video":
			if s.Disposition.AttachedPic == 0 {
				p.HasVideo = true
			}
		}
	}
	p.DurationMs = int(longest * 1000)
	return p, nil
}

func seconds(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}
