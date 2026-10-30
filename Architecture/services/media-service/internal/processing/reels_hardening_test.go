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

	"github.com/aws/aws-sdk-go-v2/aws"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Reels audit 2026-10-30: limits, timeouts, input restriction, metadata
// strip, frame sampling and scanner classification.

func writeHead(t *testing.T, head []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(p, head, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var (
	mp4Head  = []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2")
	mkvHead  = []byte("\x1a\x45\xdf\xa3\x9f\x42\x86\x81\x01\x42\xf7\x81")
	aviHead  = []byte("RIFF\x00\x00\x00\x00AVI LIST")
	m3u8Head = []byte("#EXTM3U\n#EXTINF:1,\nfile:/etc/passwd\n")
)

func TestFFmpegInputsAreRestrictedToFileProtocolAndSniffedDemuxer(t *testing.T) {
	in := writeHead(t, mp4Head)
	got, err := hardenArgs("ffmpeg", []string{"-y", "-ss", "1", "-i", in, "-vframes", "1", "/out.jpg"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-y", "-ss", "1", "-protocol_whitelist", "file", "-f", "mov", "-i", in, "-vframes", "1", "/out.jpg"}
	if !slices.Equal(got, want) {
		t.Fatalf("hardened args\n got %q\nwant %q", got, want)
	}
}

func TestFFprobeInputIsRestricted(t *testing.T) {
	in := writeHead(t, mkvHead)
	got, err := hardenArgs("ffprobe", []string{"-v", "error", in})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-protocol_whitelist", "file", "-f", "matroska", "-v", "error", in}
	if !slices.Equal(got, want) {
		t.Fatalf("hardened args\n got %q\nwant %q", got, want)
	}
}

func TestEveryFFmpegInputIsGuarded(t *testing.T) {
	a, b := writeHead(t, mp4Head), writeHead(t, aviHead)
	got, err := hardenArgs("ffmpeg", []string{"-i", a, "-i", b, "/o.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-protocol_whitelist file -f mov -i "+a) ||
		!strings.Contains(joined, "-protocol_whitelist file -f avi -i "+b) {
		t.Fatalf("second input not guarded: %q", joined)
	}
}

func TestPlaylistOrUnknownInputIsRefusedBeforeFFmpeg(t *testing.T) {
	for name, head := range map[string][]byte{"hls playlist": m3u8Head, "empty": {}, "jpeg": {0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0}} {
		in := writeHead(t, head)
		if _, err := hardenArgs("ffmpeg", []string{"-i", in, "/o.mp4"}); !errors.Is(err, ErrInputFormatRefused) {
			t.Fatalf("%s: err = %v, want ErrInputFormatRefused", name, err)
		}
		if _, err := hardenArgs("ffprobe", []string{in}); !errors.Is(err, ErrInputFormatRefused) {
			t.Fatalf("%s (ffprobe): err = %v, want ErrInputFormatRefused", name, err)
		}
	}
	if _, err := runTool(context.Background(), "ffmpeg", []string{"-i", writeHead(t, m3u8Head), "/dev/null"}, true); !errors.Is(err, ErrInputFormatRefused) {
		t.Fatalf("runTool ran a refused input: %v", err)
	}
}

func TestToolTimeoutComesFromTheContextBudget(t *testing.T) {
	ctx := context.Background()
	if got := toolTimeout(ctx, "ffmpeg"); got != DefaultFFmpegTimeout {
		t.Fatalf("no budget: %s", got)
	}
	if got := toolTimeout(WithToolTimeout(ctx, 3*time.Minute), "ffmpeg"); got != 3*time.Minute {
		t.Fatalf("budget not used: %s", got)
	}
	if got := toolTimeout(WithToolTimeout(ctx, 3*time.Hour), "ffprobe"); got != ProbeTimeout {
		t.Fatalf("ffprobe took the ffmpeg budget: %s", got)
	}
}

// A real ffmpeg is killed at its deadline and reported as ErrToolTimeout.
// Tiny synthetic input; skipped where ffmpeg is not installed.
func TestFFmpegIsKilledAtItsDeadline(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	in := writeHead(t, mp4Head) // passes the guard; ffmpeg itself is what runs
	ctx := WithToolTimeout(context.Background(), time.Nanosecond)
	_, err := runTool(ctx, "ffmpeg", []string{"-i", in, "-f", "null", "-"}, true)
	if !errors.Is(err, ErrToolTimeout) {
		t.Fatalf("err = %v, want ErrToolTimeout", err)
	}
}

func TestCallTimeoutScalesWithDurationAndIsCapped(t *testing.T) {
	short, long := FFmpegCallTimeout(30), FFmpegCallTimeout(1800)
	if short <= 10*time.Minute || long <= short {
		t.Fatalf("timeouts do not scale: 30s=%s 1800s=%s", short, long)
	}
	if got := FFmpegCallTimeout(1e9); got != 24*time.Hour {
		t.Fatalf("absurd duration buys %s", got)
	}
	if FFmpegCallTimeout(0) != 10*time.Minute {
		t.Fatal("unknown duration has no base timeout")
	}
	if TranscodeJobBudget(60) <= FFmpegCallTimeout(60) {
		t.Fatal("job budget is not larger than one call")
	}
	if got := TranscodeJobBudget(4 * 3600); got != MaxTranscodeJobBudget {
		t.Fatalf("a 4-hour video's job budget is %s, want the %s cap", got, MaxTranscodeJobBudget)
	}
}

func meta(w, h int, dur, fps float64) *VideoMeta {
	return &VideoMeta{Width: w, Height: h, DurationFloat: dur, AvgFrameRate: fps, FrameRate: fps}
}

func TestVideoLimits(t *testing.T) {
	lim := DefaultVideoLimits()
	ok := []*VideoMeta{
		meta(1080, 1920, 59, 30),     // phone reel
		meta(3840, 2160, 600, 60),    // 4K landscape
		meta(2160, 3840, 30, 30),     // 4K portrait
		meta(640, 360, 3600, 25),     // 360p hour
		meta(1080, 1920, 10, 240),    // slow motion
		meta(1920, 1080, 4*3600, 30), // exactly the duration cap
	}
	for _, m := range ok {
		if err := CheckVideoLimits(m, lim); err != nil {
			t.Fatalf("%+v refused: %v", *m, err)
		}
	}
	refused := map[string]*VideoMeta{
		"duration_too_long":   meta(1080, 1920, 4*3600+1, 30),
		"resolution_too_low":  meta(320, 240, 10, 30),
		"resolution_too_high": meta(7680, 4320, 10, 30),
		"frame_rate_too_high": meta(1080, 1920, 10, 1000),
		"no_duration":         meta(1080, 1920, 0, 30),
		"no_video_stream":     meta(0, 0, 10, 0),
	}
	for reason, m := range refused {
		err := CheckVideoLimits(m, lim)
		var r *VideoRefusal
		if !errors.As(err, &r) || r.Reason != reason || !errors.Is(err, ErrVideoRefused) {
			t.Fatalf("%s: err = %v", reason, err)
		}
	}
	// Over the long side, inside the pixel budget.
	if err := CheckVideoLimits(meta(4200, 1000, 10, 30), lim); err == nil {
		t.Fatal("a 4200-wide frame passed the long-side limit")
	}
	// Square giant inside the long side but over the pixel budget.
	if err := CheckVideoLimits(meta(4096, 4096, 10, 30), lim); err == nil {
		t.Fatal("4096x4096 passed the pixel budget")
	}
	// A VFR file reports r_frame_rate 1000/1 with a sane average: admitted.
	vfr := &VideoMeta{Width: 1280, Height: 720, DurationFloat: 10, FrameRate: 1000, AvgFrameRate: 30}
	if err := CheckVideoLimits(vfr, lim); err != nil {
		t.Fatalf("VFR WebM refused on r_frame_rate: %v", err)
	}
}

func TestParseProbeCodec(t *testing.T) {
	codec, r, avg := parseProbeCodec("codec_name=h264\nr_frame_rate=1000/1\navg_frame_rate=30000/1001\n")
	if codec != "h264" || r != 1000 || avg < 29.9 || avg > 30 {
		t.Fatalf("parsed %q %v %v", codec, r, avg)
	}
	if _, _, avg := parseProbeCodec("avg_frame_rate=0/0"); avg != 0 {
		t.Fatalf("0/0 = %v", avg)
	}
}

func TestScanFrameCountIsProportionalAndCapped(t *testing.T) {
	cases := map[float64]int{0: MinScanFrames, 5: MinScanFrames, 60: 15, 120: 30, 600: MaxScanFrames, 36000: MaxScanFrames}
	for dur, want := range cases {
		if got := ScanFrameCount(dur); got != want {
			t.Fatalf("ScanFrameCount(%v) = %d, want %d", dur, got, want)
		}
	}
}

func TestDeclaredContainerMustMatchTheBytes(t *testing.T) {
	cases := []struct {
		mime string
		head []byte
		want bool
	}{
		{"video/mp4", mp4Head, true},
		{"video/quicktime", mp4Head, true},
		{"video/webm", mkvHead, true},
		{"video/mp4", aviHead, false},
		{"video/mp4", mkvHead, false},
		{"video/webm", mp4Head, false},
		{"video/ogg", mp4Head, false},
	}
	for _, c := range cases {
		if got := VideoContainerMatches(c.mime, c.head); got != c.want {
			t.Fatalf("%s with %q = %v, want %v", c.mime, c.head[:8], got, c.want)
		}
	}
}

// Every rendition and HLS rung is written without the source's tags, so
// what viewers are served carries no capture location (finding 4).
func TestRenditionsDropEveryMetadataTag(t *testing.T) {
	joined := strings.Join(mp4TranscodeArgs("/t/original", "/t/720p.mp4", 720, reelMP4Profile), " ")
	for _, want := range []string{"-map_metadata -1", "-map_metadata:s -1", "-map_chapters -1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("rendition args %q miss %q", joined, want)
		}
	}
}

// End to end on a 1-second synthetic clip carrying a location and a title:
// the 360p rendition has neither. Skipped without ffmpeg.
func TestRenditionCarriesNoLocation(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in"), filepath.Join(dir, "360p.mp4")
	gen := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=10",
		"-t", "1", "-c:v", "libx264", "-preset", "ultrafast",
		"-metadata", "location=+37.7749-122.4194/", "-metadata", "title=secret",
		"-movflags", "+use_metadata_tags", "-f", "mp4", in)
	if b, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot synthesise a clip: %v %s", err, b)
	}
	if err := TranscodeToMP4Fast(context.Background(), in, out, 360); err != nil {
		t.Fatal(err)
	}
	probe, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format_tags:stream_tags", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"location", "37.7749", "secret"} {
		if strings.Contains(string(probe), leak) {
			t.Fatalf("rendition still carries %q:\n%s", leak, probe)
		}
	}
}

func TestScanFramesAreDownscaled(t *testing.T) {
	if !strings.Contains(scanFrameScale, "min(1280,iw)") || !strings.Contains(scanFrameScale, "min(1280,ih)") {
		t.Fatalf("frame scale %q does not bound the long side", scanFrameScale)
	}
	joined := strings.Join(scanFrameArgs("/t/original", "/t/frame_01.jpg", 4), " ")
	if !strings.Contains(joined, "-vf "+scanFrameScale) {
		t.Fatalf("frame args %q do not downscale", joined)
	}
}

func TestRekognitionInputRejectionsArePermanent(t *testing.T) {
	for name, perr := range map[string]error{
		"too large":  &rektypes.ImageTooLargeException{Message: aws.String("too big")},
		"bad format": &rektypes.InvalidImageFormatException{Message: aws.String("format")},
		"bad param":  &rektypes.InvalidParameterException{Message: aws.String("min dims")},
	} {
		_, err := scannerWith(fakeRekognition{err: perr}).ScanImage(context.Background(), []byte{1})
		if !errors.Is(err, ErrScanUnavailable) || !errors.Is(err, ErrScanInputRejected) {
			t.Fatalf("%s: err = %v, want unavailable AND input rejected", name, err)
		}
	}
	_, err := scannerWith(fakeRekognition{err: &rektypes.ThrottlingException{Message: aws.String("slow")}}).
		ScanImage(context.Background(), []byte{1})
	if !errors.Is(err, ErrScanUnavailable) || errors.Is(err, ErrScanInputRejected) {
		t.Fatalf("throttling: err = %v, want unavailable and retryable", err)
	}
}
