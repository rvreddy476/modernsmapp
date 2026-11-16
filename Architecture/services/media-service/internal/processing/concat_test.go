package processing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Recording-part joins with the exec seams faked: no ffmpeg needed.

func TestBuildConcatList(t *testing.T) {
	got, err := BuildConcatList([]string{"/tmp/t/parts/001.mp4", "/tmp/t/parts/002.mp4", "/tmp/it's/003.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	want := "ffconcat version 1.0\n" +
		"file '/tmp/t/parts/001.mp4'\n" +
		"file '/tmp/t/parts/002.mp4'\n" +
		`file '/tmp/it'\''s/003.mp4'` + "\n"
	if got != want {
		t.Fatalf("list =\n%s\nwant\n%s", got, want)
	}
	for name, paths := range map[string][]string{
		"none":     nil,
		"empty":    {"/a.mp4", ""},
		"newline":  {"/a.mp4\nfile '/etc/passwd'"},
		"carriage": {"/a\r.mp4"},
	} {
		if _, err := BuildConcatList(paths); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func vid(w, h int, codec string) *PartVideo {
	return &PartVideo{Codec: codec, Profile: "High", Width: w, Height: h, PixFmt: "yuv420p", FrameRate: "30/1", TimeBase: "1/15360"}
}

func aud() *PartAudio { return &PartAudio{Codec: "aac", SampleRate: "48000", Channels: 2} }

func TestChooseJoinMode(t *testing.T) {
	same := PartStreams{Video: vid(1920, 1080, "h264"), Audio: aud()}
	cases := []struct {
		name  string
		parts []PartStreams
		want  JoinMode
	}{
		{"one part", []PartStreams{same}, JoinCopy},
		{"identical", []PartStreams{same, same, same}, JoinCopy},
		{"identical, no audio", []PartStreams{{Video: vid(1280, 720, "h264")}, {Video: vid(1280, 720, "h264")}}, JoinCopy},
		{"resolution differs", []PartStreams{same, {Video: vid(1280, 720, "h264"), Audio: aud()}}, JoinReencode},
		{"codec differs", []PartStreams{same, {Video: vid(1920, 1080, "vp8"), Audio: aud()}}, JoinReencode},
		{"frame rate differs", []PartStreams{same, {Video: func() *PartVideo { v := vid(1920, 1080, "h264"); v.FrameRate = "25/1"; return v }(), Audio: aud()}}, JoinReencode},
		{"audio missing in one", []PartStreams{same, {Video: vid(1920, 1080, "h264")}}, JoinReencode},
		{"sample rate differs", []PartStreams{same, {Video: vid(1920, 1080, "h264"), Audio: &PartAudio{Codec: "aac", SampleRate: "44100", Channels: 2}}}, JoinReencode},
		{"no video", []PartStreams{{Audio: aud()}, same}, JoinReencode},
	}
	for _, c := range cases {
		if got := ChooseJoinMode(c.parts); got != c.want {
			t.Errorf("%s: mode = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestParsePartProbe(t *testing.T) {
	out := []byte(`{"streams":[
		{"codec_type":"video","codec_name":"h264","profile":"High","width":1280,"height":720,"pix_fmt":"yuv420p","r_frame_rate":"30/1","time_base":"1/15360"},
		{"codec_type":"audio","codec_name":"aac","sample_rate":"48000","channels":2},
		{"codec_type":"video","codec_name":"mjpeg","width":10,"height":10}],
		"format":{"duration":"61.500000"}}`)
	ps, err := ParsePartProbe(out)
	if err != nil {
		t.Fatal(err)
	}
	if ps.Video == nil || *ps.Video != *vid(1280, 720, "h264") || ps.Audio == nil || *ps.Audio != *aud() || ps.DurationSec != 61.5 {
		t.Fatalf("probe = %+v video=%+v audio=%+v", ps, ps.Video, ps.Audio)
	}
	if _, err := ParsePartProbe([]byte("not json")); err == nil {
		t.Fatal("parsed garbage")
	}
}

// fakeJoinExec replaces the ffmpeg seams for one test.
type fakeJoinExec struct {
	concatArgs [][]string
	concatList []string
	encodeArgs [][]string
	concatErr  error
}

func installFakeJoinExec(t *testing.T) *fakeJoinExec {
	t.Helper()
	f := &fakeJoinExec{}
	prevConcat, prevFF := joinConcatFFmpeg, joinFFmpeg
	t.Cleanup(func() { joinConcatFFmpeg, joinFFmpeg = prevConcat, prevFF })
	joinConcatFFmpeg = func(_ context.Context, args ...string) ([]byte, error) {
		f.concatArgs = append(f.concatArgs, args)
		for i, a := range args {
			if a == "-i" && i+1 < len(args) {
				b, err := os.ReadFile(args[i+1])
				if err != nil {
					t.Fatalf("concat list not readable: %v", err)
				}
				f.concatList = append(f.concatList, string(b))
			}
		}
		return []byte("Non-monotonic DTS"), f.concatErr
	}
	joinFFmpeg = func(_ context.Context, args ...string) ([]byte, error) {
		f.encodeArgs = append(f.encodeArgs, args)
		return nil, nil
	}
	return f
}

func joinParts(dir string, streams ...PartStreams) []JoinPart {
	var out []JoinPart
	for i, s := range streams {
		out = append(out, JoinPart{Path: filepath.Join(dir, "parts", string(rune('1'+i))+".mp4"), Streams: s})
	}
	return out
}

func hasArgs(args []string, want ...string) bool {
	return strings.Contains(" "+strings.Join(args, " ")+" ", " "+strings.Join(want, " ")+" ")
}

func TestJoinVideoParts_CopyWhenIdentical(t *testing.T) {
	f := installFakeJoinExec(t)
	dir := t.TempDir()
	same := PartStreams{Video: vid(1920, 1080, "h264"), Audio: aud(), DurationSec: 60}
	parts := joinParts(dir, same, same)
	out := filepath.Join(dir, "original")
	mode, err := JoinVideoParts(context.Background(), parts, dir, out)
	if err != nil || mode != JoinCopy {
		t.Fatalf("mode=%s err=%v", mode, err)
	}
	if len(f.concatArgs) != 1 || len(f.encodeArgs) != 0 {
		t.Fatalf("concat=%d encode=%d", len(f.concatArgs), len(f.encodeArgs))
	}
	args := f.concatArgs[0]
	if !hasArgs(args, "-protocol_whitelist", "file", "-f", "concat", "-safe", "0") || !hasArgs(args, "-c", "copy") || args[len(args)-1] != out {
		t.Fatalf("args = %v", args)
	}
	want, _ := BuildConcatList([]string{parts[0].Path, parts[1].Path})
	if f.concatList[0] != want {
		t.Fatalf("list = %q, want %q", f.concatList[0], want)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "concat-*.txt")); len(left) != 0 {
		t.Fatalf("concat list left behind: %v", left)
	}
}

func TestJoinVideoParts_ReencodeWhenParametersDiffer(t *testing.T) {
	f := installFakeJoinExec(t)
	dir := t.TempDir()
	parts := joinParts(dir,
		PartStreams{Video: vid(1920, 1080, "h264"), Audio: aud(), DurationSec: 60},
		PartStreams{Video: vid(1281, 721, "h264"), DurationSec: 12.5}, // odd frame, no audio
	)
	mode, err := JoinVideoParts(context.Background(), parts, dir, filepath.Join(dir, "original"))
	if err != nil || mode != JoinReencode {
		t.Fatalf("mode=%s err=%v", mode, err)
	}
	if len(f.concatArgs) != 0 || len(f.encodeArgs) != 1 {
		t.Fatalf("concat=%d encode=%d", len(f.concatArgs), len(f.encodeArgs))
	}
	args := f.encodeArgs[0]
	if !hasArgs(args, "-i", parts[0].Path, "-i", parts[1].Path) || !hasArgs(args, "-c:v", "libx264") || !hasArgs(args, "-map", "[a]") {
		t.Fatalf("args = %v", args)
	}
	graph := args[indexOf(args, "-filter_complex")+1]
	for _, want := range []string{
		"[1:v:0]scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080",
		"anullsrc=r=48000:cl=stereo,atrim=duration=12.500",
		"[v0][a0][v1][a1]concat=n=2:v=1:a=1[v][a]",
	} {
		if !strings.Contains(graph, want) {
			t.Fatalf("graph %q lacks %q", graph, want)
		}
	}
}

func TestJoinVideoParts_CopyFailureFallsBackToReencode(t *testing.T) {
	f := installFakeJoinExec(t)
	f.concatErr = errors.New("exit status 1")
	dir := t.TempDir()
	same := PartStreams{Video: vid(1280, 720, "h264"), DurationSec: 30}
	mode, err := JoinVideoParts(context.Background(), joinParts(dir, same, same), dir, filepath.Join(dir, "original"))
	if err != nil || mode != JoinReencode {
		t.Fatalf("mode=%s err=%v", mode, err)
	}
	if len(f.concatArgs) != 1 || len(f.encodeArgs) != 1 {
		t.Fatalf("concat=%d encode=%d", len(f.concatArgs), len(f.encodeArgs))
	}
	if hasArgs(f.encodeArgs[0], "-map", "[a]") {
		t.Fatalf("audio mapped for parts without audio: %v", f.encodeArgs[0])
	}
}

func TestJoinVideoParts_NoVideo(t *testing.T) {
	installFakeJoinExec(t)
	dir := t.TempDir()
	_, err := JoinVideoParts(context.Background(), joinParts(dir, PartStreams{Audio: aud()}), dir, filepath.Join(dir, "o"))
	if !errors.Is(err, ErrNoJoinableParts) {
		t.Fatalf("err = %v", err)
	}
}

func TestRecordingPartKeySortsInPartOrder(t *testing.T) {
	a, b := RecordingPartKey("user/u/m/", 2), RecordingPartKey("user/u/m/", 10)
	if a != "user/u/m/parts/002.mp4" || b != "user/u/m/parts/010.mp4" || !(a < b) {
		t.Fatalf("keys = %q %q", a, b)
	}
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}
