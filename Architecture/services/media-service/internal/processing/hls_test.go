package processing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeFFmpeg stands in for ffmpeg in the HLS rung decisions: it writes a
// media playlist and segment files where the real muxer would, and records
// every invocation.
type fakeFFmpeg struct {
	copyErr       error
	copyDurations []float64 // segment durations a stream copy produces
	encDurations  []float64 // segment durations an encode produces
	segmentBytes  int
	calls         [][]string
}

func (f *fakeFFmpeg) run(_ context.Context, args []string) ([]byte, error) {
	f.calls = append(f.calls, args)
	isCopy := slices.Contains(args, "copy")
	if isCopy && f.copyErr != nil {
		// A failed copy that still left debris behind.
		writeFakeRung(args, []float64{6, 6, 6, 6, 6}, 10)
		return []byte("conversion failed"), f.copyErr
	}
	durs := f.encDurations
	if isCopy {
		durs = f.copyDurations
	}
	if durs == nil {
		durs = []float64{6, 6, 3.5}
	}
	size := f.segmentBytes
	if size == 0 {
		size = 1000
	}
	writeFakeRung(args, durs, size)
	return nil, nil
}

func writeFakeRung(args []string, durs []float64, size int) {
	var m3u8, pattern string
	for i, a := range args {
		if strings.HasSuffix(a, ".m3u8") {
			m3u8 = a
		}
		if a == "-hls_segment_filename" && i+1 < len(args) {
			pattern = args[i+1]
		}
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXT-X-PLAYLIST-TYPE:VOD\n")
	for i, d := range durs {
		seg := fmt.Sprintf(pattern, i)
		_ = os.WriteFile(seg, make([]byte, size), 0o644)
		fmt.Fprintf(&b, "#EXTINF:%f,\n%s\n", d, filepath.Base(seg))
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	_ = os.WriteFile(m3u8, []byte(b.String()), 0o644)
}

func (f *fakeFFmpeg) inputs() []string {
	var out []string
	for _, c := range f.calls {
		for i, a := range c {
			if a == "-i" {
				out = append(out, c[i+1])
			}
		}
	}
	return out
}

func writeRendition(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name+".mp4")
	if err := os.WriteFile(p, []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func methods(rs []hlsRungResult) map[string]hlsRungMethod {
	out := map[string]hlsRungMethod{}
	for _, r := range rs {
		out[r.Quality] = r.Method
	}
	return out
}

// Each rung with an MP4 rendition is a stream copy OF THAT RENDITION; the
// original is only opened for a rung that has none (here 1080p).
func TestHLSRungIsCopiedFromItsRendition(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	r360 := writeRendition(t, src, "360p")
	r720 := writeRendition(t, src, "720p")
	f := &fakeFFmpeg{}
	_, paths, results, err := generateHLS(context.Background(), "/in/original", out, HLSPlan{
		SourceHeight: 1080,
		Renditions:   map[string]string{"360p": r360, "480p": writeRendition(t, src, "480p"), "720p": r720},
	}, f.run)
	if err != nil {
		t.Fatal(err)
	}
	got := methods(results)
	if got["360p"] != hlsRungCopied || got["720p"] != hlsRungCopied || got["1080p"] != hlsRungEncoded {
		t.Fatalf("rung methods = %v, want 360p/720p copied and 1080p encoded", got)
	}
	if in := f.inputs(); !slices.Equal(in, []string{r360, r720, "/in/original"}) {
		t.Fatalf("ffmpeg inputs = %v, want the 360p and 720p renditions then the original", in)
	}
	for _, r := range results {
		if r.Quality == "1080p" && r.FallbackReason == "" {
			t.Fatalf("1080p encoded without a recorded reason")
		}
	}
	// Every rung's playlist and segments are returned for upload.
	for _, q := range []string{"360p", "720p", "1080p"} {
		if !slices.Contains(paths, filepath.Join(out, q+".m3u8")) || !slices.Contains(paths, filepath.Join(out, q+"_000.ts")) {
			t.Fatalf("paths %v miss %s playlist or segments", paths, q)
		}
	}
}

// A plan without renditions is today's behaviour: every rung encoded from
// the original, with no fallback noise.
func TestHLSWithoutRenditionsEncodesEveryRung(t *testing.T) {
	f := &fakeFFmpeg{}
	_, _, results, err := generateHLS(context.Background(), "/in/original", t.TempDir(), HLSPlan{SourceHeight: 720}, f.run)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Method != hlsRungEncoded || r.FallbackReason != "" {
			t.Fatalf("rung %+v, want encoded without a fallback reason", r)
		}
	}
	if len(f.calls) != 2 {
		t.Fatalf("%d ffmpeg calls, want 2", len(f.calls))
	}
}

// A copy that fails falls back to the encode, and the debris of the failed
// copy (here five segments, the encode writes three) is not uploaded.
func TestHLSCopyFailureFallsBackToEncode(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	f := &fakeFFmpeg{copyErr: errors.New("exit status 1")}
	_, paths, results, err := generateHLS(context.Background(), "/in/original", out, HLSPlan{
		Reel: true, SourceHeight: 720,
		Renditions: map[string]string{"360p": writeRendition(t, src, "360p"), "720p": writeRendition(t, src, "720p")},
	}, f.run)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Method != hlsRungEncoded || !strings.Contains(r.FallbackReason, "ffmpeg") {
			t.Fatalf("rung %+v, want encoded after a failed copy", r)
		}
	}
	if len(f.calls) != 4 {
		t.Fatalf("%d ffmpeg calls, want copy+encode per rung (4)", len(f.calls))
	}
	if slices.Contains(paths, filepath.Join(out, "360p_004.ts")) {
		t.Fatalf("stale segment from the failed copy is in the upload list: %v", paths)
	}
	if _, err := os.Stat(filepath.Join(out, "360p_004.ts")); !os.IsNotExist(err) {
		t.Fatalf("stale segment from the failed copy left on disk: %v", err)
	}
}

// A copy whose segments come out long means the rendition's keyframes were
// not on the 2 s grid; that rung is encoded instead of served unseekable.
func TestHLSCopyWithUnalignedKeyframesFallsBack(t *testing.T) {
	src := t.TempDir()
	f := &fakeFFmpeg{copyDurations: []float64{6, 10.4, 2}}
	_, _, results, err := generateHLS(context.Background(), "/in/original", t.TempDir(), HLSPlan{
		Reel: true, SourceHeight: 360,
		Renditions: map[string]string{"360p": writeRendition(t, src, "360p")},
	}, f.run)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Method != hlsRungEncoded || !strings.Contains(results[0].FallbackReason, "keyframes") {
		t.Fatalf("results = %+v, want the 360p rung encoded for unaligned keyframes", results)
	}
}

// Exactly one keyframe interval of slack is still a good copy.
func TestHLSCopyAtTheSegmentBoundIsKept(t *testing.T) {
	src := t.TempDir()
	f := &fakeFFmpeg{copyDurations: []float64{6, hlsMaxCopiedSegmentSeconds, 1}}
	_, _, results, err := generateHLS(context.Background(), "/in/original", t.TempDir(), HLSPlan{
		Reel: true, SourceHeight: 360,
		Renditions: map[string]string{"360p": writeRendition(t, src, "360p")},
	}, f.run)
	if err != nil || results[0].Method != hlsRungCopied {
		t.Fatalf("results = %+v, %v; want copied", results, err)
	}
}

func TestHLSCopyWithNoSegmentsFallsBack(t *testing.T) {
	src := t.TempDir()
	f := &fakeFFmpeg{copyDurations: []float64{}}
	_, _, results, err := generateHLS(context.Background(), "/in/original", t.TempDir(), HLSPlan{
		Reel: true, SourceHeight: 360,
		Renditions: map[string]string{"360p": writeRendition(t, src, "360p")},
	}, f.run)
	if err != nil || results[0].Method != hlsRungEncoded {
		t.Fatalf("results = %+v, %v; want encoded", results, err)
	}
}

// A rendition path that is missing or empty (the first stage ignores a
// failed rendition encode) is not copied.
func TestHLSRenditionMustBeANonEmptyFile(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "360p.mp4")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	v := defaultHLSVariants[0]
	for name, p := range map[string]string{"missing": filepath.Join(dir, "nope.mp4"), "empty": empty, "directory": dir} {
		if _, ok := hlsRenditionFor(HLSPlan{Renditions: map[string]string{"360p": p}}, v); ok {
			t.Fatalf("%s rendition accepted", name)
		}
	}
	good := writeRendition(t, dir, "good")
	if got, ok := hlsRenditionFor(HLSPlan{Renditions: map[string]string{"360p": good}}, v); !ok || got != good {
		t.Fatalf("good rendition = %q, %v", got, ok)
	}
}

// A copy failure caused by cancellation is the cancellation, not a reason
// to start a full encode.
func TestHLSCopyCancelledDoesNotEncode(t *testing.T) {
	src := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	f := &fakeFFmpeg{}
	run := func(c context.Context, args []string) ([]byte, error) {
		cancel()
		f.calls = append(f.calls, args)
		return nil, errors.New("signal: killed")
	}
	_, _, _, err := generateHLS(ctx, "/in/original", t.TempDir(), HLSPlan{
		Reel: true, SourceHeight: 360,
		Renditions: map[string]string{"360p": writeRendition(t, src, "360p")},
	}, run)
	if !errors.Is(err, context.Canceled) || len(f.calls) != 1 {
		t.Fatalf("err = %v after %d calls, want context.Canceled after the copy only", err, len(f.calls))
	}
}

func TestHLSCopyArgsAreAStreamCopyWithTheSameNames(t *testing.T) {
	args := hlsCopyArgs("/t/720p.mp4", "/h/720p.m3u8", "/h/720p_%03d.ts")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"-i /t/720p.mp4", "-c copy", "-f hls", "-hls_time 6", "-hls_playlist_type vod",
		"-hls_segment_filename /h/720p_%03d.ts",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("copy args %q miss %q", joined, want)
		}
	}
	if args[len(args)-1] != "/h/720p.m3u8" {
		t.Fatalf("copy args end with %q, want the playlist", args[len(args)-1])
	}
	for _, banned := range []string{"libx264", "-vf", "-b:v", "aac"} {
		if slices.Contains(args, banned) {
			t.Fatalf("copy args re-encode: %q", joined)
		}
	}
}

// The fallback is the pre-2026-09-29 encode, unchanged.
func TestHLSEncodeArgsAreTheOriginalLadderEncode(t *testing.T) {
	got := strings.Join(hlsEncodeArgs("/in/o", "/h/720p.m3u8", "/h/720p_%03d.ts", defaultHLSVariants[1], "fast"), " ")
	want := "-i /in/o -vf scale=-2:720 -c:v libx264 -preset fast -b:v 2500k -c:a aac -b:a 128k " +
		"-hls_time 6 -hls_playlist_type vod -hls_segment_filename /h/720p_%03d.ts /h/720p.m3u8 -y"
	if got != want {
		t.Fatalf("encode args\n got %s\nwant %s", got, want)
	}
}

func TestMP4RenditionsForceKeyframesOnTheSegmentGrid(t *testing.T) {
	if hlsSegmentSeconds%keyframeIntervalSeconds != 0 {
		t.Fatalf("hls_time %d is not a multiple of the keyframe interval %d", hlsSegmentSeconds, keyframeIntervalSeconds)
	}
	for _, p := range []mp4Profile{longFormMP4Profile, reelMP4Profile} {
		args := mp4TranscodeArgs("/in/o", "/t/720p.mp4", 720, p)
		joined := strings.Join(args, " ")
		for _, want := range []string{
			"-force_key_frames expr:gte(t,n_forced*2)",
			"-vf scale=-2:720", "-c:v libx264 -preset " + p.Preset + " -crf " + p.CRF,
			"-c:a aac -b:a 128k", "-movflags +faststart",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("mp4 args %q miss %q", joined, want)
			}
		}
		if args[len(args)-1] != "/t/720p.mp4" {
			t.Fatalf("mp4 args end with %q, want the output", args[len(args)-1])
		}
		if slices.Contains(args, "-threads") {
			t.Fatalf("mp4 args pin the thread count: %q", joined)
		}
	}
}

func TestLongFormRenditionsUseVeryfast(t *testing.T) {
	if longFormMP4Profile.Preset != "veryfast" || longFormMP4Profile.CRF != "23" {
		t.Fatalf("long-form profile = %+v", longFormMP4Profile)
	}
}

func TestMP4RenditionsPicksOnlyVideoOutputs(t *testing.T) {
	got := MP4Renditions([]TranscodeOutput{
		{Name: "thumb_150", FilePath: "/t/thumb.jpg", Mime: "image/jpeg"},
		{Name: "360p", FilePath: "/t/360p.mp4", Mime: "video/mp4"},
		{Name: "720p", FilePath: "/t/720p.mp4", Mime: "video/mp4"},
		{Name: StoryboardVTTVariant, FilePath: "/t/sb.vtt", Mime: "text/vtt"},
	})
	if len(got) != 2 || got["360p"] != "/t/360p.mp4" || got["720p"] != "/t/720p.mp4" {
		t.Fatalf("renditions = %v", got)
	}
}

// BANDWIDTH is the measured peak, not the nominal ladder number; the short
// tail segment does not set it.
func TestHLSMasterBandwidthIsTheMeasuredPeak(t *testing.T) {
	dir := t.TempDir()
	pl := filepath.Join(dir, "720p.m3u8")
	seg := func(name string, size int) {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seg("720p_000.ts", 3_000_000) // 4 Mbit/s over 6 s
	seg("720p_001.ts", 1_500_000) // 2 Mbit/s
	seg("720p_002.ts", 200_000)   // 0.2 s tail: 8 Mbit/s, ignored
	os.WriteFile(pl, []byte("#EXTM3U\n#EXTINF:6.000000,\n720p_000.ts\n#EXTINF:6.0,\n720p_001.ts\n#EXTINF:0.2,\n720p_002.ts\n#EXT-X-ENDLIST\n"), 0o644)
	got, ok := peakSegmentBandwidth(pl)
	if !ok || got != 4_000_000 {
		t.Fatalf("peak = %d, %v; want 4000000", got, ok)
	}

	// Only a short segment: it is all there is, so it counts.
	pl2 := filepath.Join(dir, "360p.m3u8")
	seg("360p_000.ts", 50_000)
	os.WriteFile(pl2, []byte("#EXTM3U\n#EXTINF:0.5,\n360p_000.ts\n#EXT-X-ENDLIST\n"), 0o644)
	if got, ok := peakSegmentBandwidth(pl2); !ok || got != 800_000 {
		t.Fatalf("short-only peak = %d, %v; want 800000", got, ok)
	}

	master := hlsMasterPlaylist([]hlsRungResult{{Quality: "360p", BandwidthBPS: 812345}, {Quality: "720p", BandwidthBPS: 4_000_000}})
	want := "#EXTM3U\n#EXT-X-VERSION:3\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=812345,RESOLUTION=640x360\n360p.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=4000000,RESOLUTION=1280x720\n720p.m3u8\n"
	if master != want {
		t.Fatalf("master\n%s\nwant\n%s", master, want)
	}
}

// The master the pipeline writes carries each rung's measured peak.
func TestHLSMasterWrittenWithMeasuredBandwidth(t *testing.T) {
	src, out := t.TempDir(), t.TempDir()
	f := &fakeFFmpeg{copyDurations: []float64{6, 6, 2}, segmentBytes: 750_000} // 1 Mbit/s over 6 s, 3 over 2 s
	master, _, _, err := generateHLS(context.Background(), "/in/o", out, HLSPlan{
		Reel: true, SourceHeight: 360,
		Renditions: map[string]string{"360p": writeRendition(t, src, "360p")},
	}, f.run)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(master)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "BANDWIDTH=3000000,RESOLUTION=640x360\n360p.m3u8") {
		t.Fatalf("master = %q, want the measured 3000000 peak", b)
	}
}

// An unreadable playlist keeps the old nominal number rather than 0.
func TestHLSMasterFallsBackToNominalBandwidth(t *testing.T) {
	run := func(context.Context, []string) ([]byte, error) { return nil, nil } // writes nothing
	_, _, results, err := generateHLS(context.Background(), "/in/o", t.TempDir(), HLSPlan{Reel: true, SourceHeight: 360}, run)
	if err != nil || len(results) != 1 || results[0].BandwidthBPS != 800000 {
		t.Fatalf("results = %+v, %v; want nominal 800000", results, err)
	}
}
