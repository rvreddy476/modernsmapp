package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// reels-16 (gate line 6): POST /v1/audio/voiceover. The size cap and the
// content check run before any temp file or tool, and a file ffmpeg cannot
// decode is refused rather than stored raw. None of these paths reaches the
// stores, so a zero Service is enough.

func TestVoiceoverOverTheCapIsRefusedBeforeTheTool(t *testing.T) {
	data := make([]byte, MaxVoiceoverUploadBytes+1)
	copy(data, "OggS")
	if _, err := (&Service{}).RecordVoiceover(context.Background(), uuid.New(), data, "audio/ogg"); !errors.Is(err, ErrVoiceoverTooLarge) {
		t.Fatalf("err %v, want ErrVoiceoverTooLarge", err)
	}
}

func TestVoiceoverThatIsNotAudioIsRefusedBeforeTheTool(t *testing.T) {
	for name, data := range map[string][]byte{
		"hls playlist": []byte("#EXTM3U\n#EXT-X-VERSION:3\nhttp://169.254.169.254/latest\n"),
		"concat list":  []byte("ffconcat version 1.0\nfile 'subfile:,start,0,end,0,,:/etc/passwd'\n"),
		"text":         []byte("hello, this is not audio at all"),
		"short":        []byte("ID3"),
	} {
		// Declared as audio: the client's MIME is not believed.
		if _, err := (&Service{}).RecordVoiceover(context.Background(), uuid.New(), data, "audio/webm"); !errors.Is(err, ErrVoiceoverUnsupported) {
			t.Fatalf("%s: err %v, want ErrVoiceoverUnsupported", name, err)
		}
	}
}

// An allowed container whose body is garbage is refused (and so never
// stored as the user's asset). It used to fall back to the raw upload.
func TestVoiceoverThatCannotBeDecodedIsRefusedNotStoredRaw(t *testing.T) {
	data := append([]byte("OggS"), bytes.Repeat([]byte{0x5A}, 4096)...)
	_, err := (&Service{}).RecordVoiceover(context.Background(), uuid.New(), data, "audio/ogg")
	if !errors.Is(err, ErrVoiceoverUnreadable) {
		t.Fatalf("err %v, want ErrVoiceoverUnreadable", err)
	}
}

// The voiceover path must not regain a bare ffmpeg: every tool call on user
// audio goes through processing's hardened runner.
func TestVoiceoverRunsNoBareTool(t *testing.T) {
	src, err := os.ReadFile("audio.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"os/exec", "exec.Command"} {
		if strings.Contains(string(src), banned) {
			t.Fatalf("service/audio.go uses %s: route voiceover audio through processing.ExtractAudio", banned)
		}
	}
}
