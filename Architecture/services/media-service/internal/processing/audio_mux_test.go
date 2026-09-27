package processing

import (
	"strings"
	"testing"
)

// The dub mux must never re-encode the picture and must always produce a
// progressive MP4 cut to the shorter stream.
func TestMuxAudioArgsShape(t *testing.T) {
	args := MuxAudioArgs("in.mp4", "track.m4a", "out.mp4")
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"-i in.mp4 -i track.m4a",
		"-map 0:v:0 -map 1:a:0",
		"-c:v copy",
		"-c:a aac -b:a 128k",
		"-shortest",
		"-movflags +faststart",
		"-f mp4 out.mp4",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv missing %q: %s", want, joined)
		}
	}
	if args[0] != "-y" {
		t.Errorf("must overwrite the scratch output (-y first), got %q", args[0])
	}
	if args[len(args)-1] != "out.mp4" {
		t.Errorf("output path must be last, got %q", args[len(args)-1])
	}
	if strings.Contains(joined, "-c:v libx264") || strings.Contains(joined, "-vf") {
		t.Errorf("mux must not re-encode or filter video: %s", joined)
	}
}
