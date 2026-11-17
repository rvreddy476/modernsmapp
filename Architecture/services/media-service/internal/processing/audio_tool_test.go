package processing

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// reels-16: creator audio reaches ffprobe/ffmpeg only with a file-only
// protocol whitelist, a demuxer forced from its own bytes, and a deadline.

// stubAudioTool replaces the binary with this test executable re-run as a
// helper (TestAudioToolHelperProcess), recording each argv. mode "sleep"
// makes the helper outlive any short deadline; "ok" prints "{}".
func stubAudioTool(t *testing.T, mode string) *[][]string {
	t.Helper()
	calls := &[][]string{}
	prev := audioCommand
	audioCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		*calls = append(*calls, append([]string{name}, args...))
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAudioToolHelperProcess$")
		cmd.Env = append(os.Environ(), "MEDIA_AUDIO_TOOL_HELPER="+mode)
		return cmd
	}
	t.Cleanup(func() { audioCommand = prev })
	return calls
}

func TestAudioToolHelperProcess(t *testing.T) {
	switch os.Getenv("MEDIA_AUDIO_TOOL_HELPER") {
	case "":
		return
	case "sleep":
		time.Sleep(30 * time.Second)
	default:
		os.Stdout.WriteString("{}")
	}
	os.Exit(0)
}

func writeAudio(t *testing.T, head []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "upload")
	if err := os.WriteFile(p, head, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var (
	m4aHead  = []byte("\x00\x00\x00\x20ftypM4A \x00\x00\x00\x00")
	mp3Head  = []byte("ID3\x04\x00\x00\x00\x00\x00\x00\x00\x00")
	adtsHead = []byte{0xFF, 0xF1, 0x50, 0x80, 0, 0, 0, 0, 0, 0, 0, 0}
	wavHead  = []byte("RIFF\x00\x00\x00\x00WAVEfmt ")
	// A playlist that points ffmpeg at the network and a local file.
	hlsAudio    = []byte("#EXTM3U\n#EXTINF:1,\nhttp://169.254.169.254/latest/meta-data\nfile:/etc/passwd\n")
	concatAudio = []byte("ffconcat version 1.0\nfile 'http://evil/x.mp3'\n")
)

func TestAudioDemuxerAllowList(t *testing.T) {
	for head, want := range map[string]string{
		string(m4aHead): "mov", string(mp3Head): "mp3", string(adtsHead): "aac", string(wavHead): "wav",
		"OggS\x00\x02\x00\x00\x00\x00\x00\x00": "ogg", "fLaC\x00\x00\x00\x22\x00\x00\x00\x00": "flac",
		"#!AMR\n\x00\x00\x00\x00\x00\x00": "amr", string([]byte{0xFF, 0xFB, 0x90, 0x64, 0, 0, 0, 0, 0, 0, 0, 0}): "mp3",
		string(mkvHead): "matroska", string(hlsAudio): "", string(concatAudio): "", "short": "",
	} {
		if got := audioDemuxer([]byte(head)); got != want {
			t.Errorf("audioDemuxer(%q) = %q, want %q", head[:min(len(head), 12)], got, want)
		}
	}
}

// A playlist or concat body naming another protocol is refused before any
// tool runs, on every audio entry point.
func TestAudioPlaylistIsRefusedBeforeTheTool(t *testing.T) {
	calls := stubAudioTool(t, "ok")
	ctx := context.Background()
	for name, head := range map[string][]byte{"hls": hlsAudio, "concat": concatAudio} {
		in := writeAudio(t, head)
		if _, err := ProbeAudioFile(ctx, in); !errors.Is(err, ErrInputFormatRefused) {
			t.Errorf("%s ProbeAudioFile: err = %v, want ErrInputFormatRefused", name, err)
		}
		if _, err := ProbeAudio(ctx, in); !errors.Is(err, ErrInputFormatRefused) {
			t.Errorf("%s ProbeAudio: err = %v, want ErrInputFormatRefused", name, err)
		}
		if err := MuxAudio(ctx, writeAudio(t, mp4Head), in, filepath.Join(t.TempDir(), "o.mp4")); !errors.Is(err, ErrInputFormatRefused) {
			t.Errorf("%s MuxAudio: err = %v, want ErrInputFormatRefused", name, err)
		}
		if _, err := GenerateWaveform(ctx, in, t.TempDir(), 10); !errors.Is(err, ErrInputFormatRefused) {
			t.Errorf("%s GenerateWaveform: err = %v, want ErrInputFormatRefused", name, err)
		}
		if _, _, err := ExtractAudio(ctx, in, t.TempDir()); !errors.Is(err, ErrInputFormatRefused) {
			t.Errorf("%s ExtractAudio: err = %v, want ErrInputFormatRefused", name, err)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused input reached the tool: %q", *calls)
	}
}

// The argv the tools get: every input behind -protocol_whitelist file and
// its sniffed -f.
func TestAudioToolArgvIsHardened(t *testing.T) {
	calls := stubAudioTool(t, "ok")
	ctx := context.Background()
	track := writeAudio(t, mp3Head)
	if _, err := ProbeAudioFile(ctx, track); err != nil {
		t.Fatalf("ProbeAudioFile: %v", err)
	}
	video := writeAudio(t, mp4Head)
	_ = MuxAudio(ctx, video, track, filepath.Join(t.TempDir(), "o.mp4"))
	if len(*calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(*calls))
	}
	probe := (*calls)[0]
	if probe[0] != "ffprobe" || !slices.Equal(probe[1:5], []string{"-protocol_whitelist", "file", "-f", "mp3"}) {
		t.Errorf("ffprobe argv not hardened: %q", probe)
	}
	mux := strings.Join((*calls)[1], " ")
	if !strings.Contains(mux, "-protocol_whitelist file -f mov -i "+video) ||
		!strings.Contains(mux, "-protocol_whitelist file -f mp3 -i "+track) {
		t.Errorf("ffmpeg mux argv not hardened: %q", mux)
	}
}

// A tool that hangs is killed at its deadline and reported as ErrToolTimeout.
func TestAudioToolIsKilledAtItsDeadline(t *testing.T) {
	stubAudioTool(t, "sleep")
	ctx := WithToolTimeout(context.Background(), 200*time.Millisecond)
	start := time.Now()
	err := MuxAudio(ctx, writeAudio(t, mp4Head), writeAudio(t, m4aHead), filepath.Join(t.TempDir(), "o.mp4"))
	if !errors.Is(err, ErrToolTimeout) {
		t.Fatalf("err = %v, want ErrToolTimeout", err)
	}
	if time.Since(start) > 20*time.Second {
		t.Fatalf("the hung tool was not killed at its deadline (%s)", time.Since(start))
	}
}

func TestAudioToolTimeouts(t *testing.T) {
	ctx := context.Background()
	if got := audioToolTimeout(ctx, "ffprobe"); got != ProbeTimeout {
		t.Errorf("ffprobe: %s", got)
	}
	if got := audioToolTimeout(ctx, "ffmpeg"); got != AudioFFmpegTimeout {
		t.Errorf("ffmpeg default: %s", got)
	}
	if got := audioToolTimeout(WithToolTimeout(ctx, time.Minute), "ffmpeg"); got != time.Minute {
		t.Errorf("ffmpeg budget: %s", got)
	}
}
