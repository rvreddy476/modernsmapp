package processing

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Real-ffmpeg checks of the rendition + HLS pipeline (2026-09-29). Skipped
// when ffmpeg/ffprobe are not on PATH, and in -short.
//
// TestHLSRepackagedFromRenditionsIsPlayable runs the production path on a
// synthetic 1080p clip and asserts the HLS it serves is sound.
//
// TestTranscodePipelineTiming is the before/after measurement; it only runs
// with MEDIA_PIPELINE_BENCH=1:
//
//	MEDIA_PIPELINE_BENCH=1 MEDIA_PIPELINE_BENCH_SECONDS=90 \
//	MEDIA_PIPELINE_BENCH_VARIANTS=legacy,repack-medium,repack \
//	go test ./internal/processing -run TestTranscodePipelineTiming -v -timeout 60m
//
// Variants: "legacy" is the code before this change (medium renditions with
// x264's default GOP, then every HLS rung encoded again from the original);
// "repack-medium" is the repackaged ladder with the old medium preset;
// "repack" is the production path (veryfast renditions, repackaged ladder);
// "repack@<preset>:<crf>" repackages renditions encoded with that profile.
// MEDIA_PIPELINE_BENCH_REEL=1 benchmarks the reel pipeline instead.

func requireFFmpegOrSkip(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("real ffmpeg pipeline skipped in -short")
	}
	if err := RequireFFmpeg(); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
}

// synthSource writes a lavfi clip: testsrc2 (moving, detailed) plus a sine
// tone, H.264/AAC in MP4 like a phone upload.
func synthSource(t *testing.T, dir string, seconds, height int) string {
	t.Helper()
	p := filepath.Join(dir, "original")
	w := height * 16 / 9
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%dx%d:rate=30:duration=%d", w, height, seconds),
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=440:sample_rate=48000:duration=%d", seconds),
		"-c:v", "libx264", "-preset", "ultrafast", "-crf", "18", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-shortest", "-f", "mp4", p)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, out)
	}
	return p
}

func TestHLSRepackagedFromRenditionsIsPlayable(t *testing.T) {
	requireFFmpegOrSkip(t)
	ctx := context.Background()
	work := t.TempDir()
	src := synthSource(t, work, 20, 1080)
	meta, err := ProbeVideo(ctx, src)
	if err != nil {
		t.Fatal(err)
	}

	outputs, _, err := TranscodeVideo(ctx, src, work)
	if err != nil {
		t.Fatal(err)
	}
	renditions := MP4Renditions(outputs)
	for _, q := range []string{"360p", "480p", "720p", "1080p"} {
		if renditions[q] == "" {
			t.Fatalf("no %s rendition: %v", q, renditions)
		}
	}
	assertFaststart(t, renditions["720p"])
	assertKeyframesEvery(t, renditions["360p"], keyframeIntervalSeconds, meta.DurationFloat)

	hlsDir := filepath.Join(work, "hls")
	if err := os.Mkdir(hlsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, results, err := generateHLS(ctx, src, hlsDir, HLSPlan{SourceHeight: meta.Height, Renditions: renditions}, execFFmpeg)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Method != hlsRungCopied {
			t.Fatalf("rung %s was %s (%s), want a stream copy", r.Quality, r.Method, r.FallbackReason)
		}
	}
	assertPlayableHLS(t, hlsDir, []string{"360p", "720p", "1080p"}, meta.DurationFloat, 7)
}

// A reel's ladder stops at 720p and is copied from the reel renditions.
func TestHLSRepackagedReelIsPlayable(t *testing.T) {
	requireFFmpegOrSkip(t)
	ctx := context.Background()
	work := t.TempDir()
	src := synthSource(t, work, 13, 1080)
	meta, err := ProbeVideo(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	outputs, _, err := TranscodeReel(ctx, src, work)
	if err != nil {
		t.Fatal(err)
	}
	hlsDir := filepath.Join(work, "hls")
	_ = os.Mkdir(hlsDir, 0o755)
	_, _, results, err := generateHLS(ctx, src, hlsDir, HLSPlan{Reel: true, SourceHeight: meta.Height, Renditions: MP4Renditions(outputs)}, execFFmpeg)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Method != hlsRungCopied {
			t.Fatalf("rung %s was %s (%s), want a stream copy", r.Quality, r.Method, r.FallbackReason)
		}
	}
	assertPlayableHLS(t, hlsDir, []string{"360p", "720p"}, meta.DurationFloat, 7)
}

// assertPlayableHLS: the master lists exactly the expected rungs; every
// variant has ~6 s segments (none over 7 s), adds up to the source
// duration within a second, and ffprobe reads its first segment (H.264 +
// AAC) and the playlist as a whole.
func assertPlayableHLS(t *testing.T, hlsDir string, rungs []string, sourceSeconds, maxSegment float64) map[string][]float64 {
	t.Helper()
	master, err := os.ReadFile(filepath.Join(hlsDir, "master.m3u8"))
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, line := range strings.Split(string(master), "\n") {
		if strings.HasSuffix(line, ".m3u8") {
			listed = append(listed, strings.TrimSuffix(line, ".m3u8"))
		}
	}
	if !slices.Equal(listed, rungs) {
		t.Fatalf("master lists %v, want %v\n%s", listed, rungs, master)
	}
	durations := map[string][]float64{}
	for _, q := range rungs {
		segs, err := readHLSPlaylist(filepath.Join(hlsDir, q+".m3u8"))
		if err != nil || len(segs) == 0 {
			t.Fatalf("%s playlist: %v (%d segments)", q, err, len(segs))
		}
		total := 0.0
		for i, s := range segs {
			durations[q] = append(durations[q], s.Duration)
			total += s.Duration
			if s.Duration > maxSegment {
				t.Fatalf("%s segment %s is %.3fs, want <= %.1fs", q, s.URI, s.Duration, maxSegment)
			}
			if i < len(segs)-1 && s.Duration < 5.0 {
				t.Fatalf("%s segment %s is %.3fs mid-stream, want ~6s", q, s.URI, s.Duration)
			}
		}
		if math.Abs(total-sourceSeconds) > 1.0 {
			t.Fatalf("%s totals %.3fs, source is %.3fs", q, total, sourceSeconds)
		}
		streams := ffprobeOut(t, "-v", "error", "-show_entries", "stream=codec_type,codec_name", "-of", "csv=p=0",
			filepath.Join(hlsDir, segs[0].URI))
		if !strings.Contains(streams, "h264,video") || !strings.Contains(streams, "aac,audio") {
			t.Fatalf("%s first segment streams = %q, want h264 video and aac audio", q, streams)
		}
		dur := ffprobeOut(t, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1",
			filepath.Join(hlsDir, q+".m3u8"))
		if d, err := strconv.ParseFloat(strings.TrimSpace(dur), 64); err != nil || math.Abs(d-sourceSeconds) > 1.0 {
			t.Fatalf("ffprobe %s playlist duration = %q, source %.3fs", q, dur, sourceSeconds)
		}
	}
	return durations
}

func ffprobeOut(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("ffprobe", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// assertFaststart: the moov atom precedes mdat, so the MP4 plays before it
// has fully downloaded.
func assertFaststart(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	moov, mdat := bytes.Index(b, []byte("moov")), bytes.Index(b, []byte("mdat"))
	if moov < 0 || mdat < 0 || moov > mdat {
		t.Fatalf("%s: moov at %d, mdat at %d; want moov first (+faststart)", filepath.Base(path), moov, mdat)
	}
}

// assertKeyframesEvery: a keyframe within a frame of every multiple of
// interval seconds.
func assertKeyframesEvery(t *testing.T, path string, interval int, duration float64) {
	t.Helper()
	out := ffprobeOut(t, "-v", "error", "-select_streams", "v:0", "-skip_frame", "nokey",
		"-show_entries", "frame=pts_time", "-of", "csv=p=0", path)
	var keys []float64
	for _, l := range strings.Fields(out) {
		if v, err := strconv.ParseFloat(strings.Trim(l, ","), 64); err == nil {
			keys = append(keys, v)
		}
	}
	for want := 0.0; want < duration-0.1; want += float64(interval) {
		found := false
		for _, k := range keys {
			if math.Abs(k-want) < 0.05 {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s: no keyframe at %.0fs; keyframes %v", filepath.Base(path), want, keys)
		}
	}
}

// ---- timing harness ----

func legacyMP4Args(in, out string, h int, p mp4Profile) []string {
	return []string{"-y", "-i", in, "-vf", fmt.Sprintf("scale=-2:%d", h),
		"-c:v", "libx264", "-preset", p.Preset, "-crf", p.CRF,
		"-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", out}
}

type pipelineRun struct {
	Renditions time.Duration
	HLS        time.Duration
	Durations  map[string][]float64
	HLSBytes   map[string]int64
	SSIM720    string
}

func runPipeline(t *testing.T, variant, src, work string, reel bool, meta *VideoMeta) pipelineRun {
	t.Helper()
	ctx := context.Background()
	heights := []int{360, 480, 720, 1080}
	profile := longFormMP4Profile
	if reel {
		heights = []int{360, 480, 720}
		profile = reelMP4Profile
	}
	argsFor := func(in, out string, h int) []string { return mp4TranscodeArgs(in, out, h, profile) }
	switch variant {
	case "legacy":
		if !reel {
			profile = mp4Profile{Preset: "medium", CRF: "23"}
		}
		argsFor = func(in, out string, h int) []string { return legacyMP4Args(in, out, h, profile) }
	case "repack-medium":
		argsFor = func(in, out string, h int) []string {
			return mp4TranscodeArgs(in, out, h, mp4Profile{Preset: "medium", CRF: "23"})
		}
	case "repack":
	default:
		// "repack@<preset>:<crf>" tries another rendition profile.
		spec, ok := strings.CutPrefix(variant, "repack@")
		preset, crf, ok2 := strings.Cut(spec, ":")
		if !ok || !ok2 {
			t.Fatalf("unknown variant %q", variant)
		}
		argsFor = func(in, out string, h int) []string {
			return mp4TranscodeArgs(in, out, h, mp4Profile{Preset: preset, CRF: crf})
		}
	}
	dir := filepath.Join(work, strings.NewReplacer("@", "_", ":", "_").Replace(variant))
	hlsDir := filepath.Join(dir, "hls")
	if err := os.MkdirAll(hlsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var run pipelineRun
	start := time.Now()
	renditions := map[string]string{}
	for _, h := range heights {
		if meta.Height < h {
			continue
		}
		out := filepath.Join(dir, fmt.Sprintf("%dp.mp4", h))
		if b, err := exec.Command("ffmpeg", argsFor(src, out, h)...).CombinedOutput(); err != nil {
			t.Fatalf("%s %dp: %v\n%s", variant, h, err, b)
		}
		renditions[fmt.Sprintf("%dp", h)] = out
	}
	run.Renditions = time.Since(start)

	plan := HLSPlan{Reel: reel, SourceHeight: meta.Height}
	if variant != "legacy" {
		plan.Renditions = renditions
	}
	start = time.Now()
	_, _, results, err := generateHLS(ctx, src, hlsDir, plan, execFFmpeg)
	if err != nil {
		t.Fatal(err)
	}
	run.HLS = time.Since(start)
	t.Logf("%-14s renditions %7.1fs  hls %7.1fs  total %7.1fs", variant,
		run.Renditions.Seconds(), run.HLS.Seconds(), (run.Renditions + run.HLS).Seconds())
	var rungs []string
	for _, r := range results {
		rungs = append(rungs, r.Quality)
		if variant != "legacy" && r.Method != hlsRungCopied {
			t.Fatalf("%s: rung %s was %s (%s)", variant, r.Quality, r.Method, r.FallbackReason)
		}
	}
	// The legacy ladder cut wherever x264's default GOP put a keyframe
	// (8.3 s at 30 fps); it is measured, not held to the new bound.
	maxSegment := 7.0
	if variant == "legacy" {
		maxSegment = 30
	}
	run.Durations = assertPlayableHLS(t, hlsDir, rungs, meta.DurationFloat, maxSegment)
	run.HLSBytes = map[string]int64{}
	for _, q := range rungs {
		files, _ := filepath.Glob(filepath.Join(hlsDir, q+"_*.ts"))
		for _, f := range files {
			if st, err := os.Stat(f); err == nil {
				run.HLSBytes[q] += st.Size()
			}
		}
	}
	run.SSIM720 = ssimAgainstSource(t, filepath.Join(hlsDir, "720p.m3u8"), src)
	return run
}

var ssimAll = regexp.MustCompile(`All:([0-9.]+) \(([0-9.inf]+)\)`)

// ssimAgainstSource scores what a viewer of the 720p rung sees against the
// source scaled to the same size.
func ssimAgainstSource(t *testing.T, playlist, src string) string {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-v", "info", "-i", playlist, "-i", src, "-lavfi",
		"[0:v]setpts=PTS-STARTPTS[d];[1:v]scale=-2:720,setpts=PTS-STARTPTS[r];[d][r]ssim", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("ssim: %v\n%s", err, out)
	}
	m := ssimAll.FindStringSubmatch(string(out))
	if m == nil {
		return "n/a"
	}
	return m[1] + " (" + m[2] + " dB)"
}

func TestTranscodePipelineTiming(t *testing.T) {
	if os.Getenv("MEDIA_PIPELINE_BENCH") != "1" {
		t.Skip("set MEDIA_PIPELINE_BENCH=1 to run the transcode timing harness")
	}
	requireFFmpegOrSkip(t)
	seconds := 90
	if v, err := strconv.Atoi(os.Getenv("MEDIA_PIPELINE_BENCH_SECONDS")); err == nil && v > 0 {
		seconds = v
	}
	variants := []string{"legacy", "repack"}
	if v := os.Getenv("MEDIA_PIPELINE_BENCH_VARIANTS"); v != "" {
		variants = strings.Split(v, ",")
	}
	reel := os.Getenv("MEDIA_PIPELINE_BENCH_REEL") == "1"

	work := t.TempDir()
	src := synthSource(t, work, seconds, 1080)
	meta, err := ProbeVideo(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("source: %.2fs %dx%d, reel=%v", meta.DurationFloat, meta.Width, meta.Height, reel)
	for _, v := range variants {
		r := runPipeline(t, v, src, work, reel, meta)
		total := r.Renditions + r.HLS
		t.Logf("%-14s renditions %7.1fs  hls %7.1fs  total %7.1fs  (%.2fx realtime)  720p SSIM %s",
			v, r.Renditions.Seconds(), r.HLS.Seconds(), total.Seconds(), total.Seconds()/meta.DurationFloat, r.SSIM720)
		for _, q := range []string{"360p", "720p", "1080p"} {
			d, ok := r.Durations[q]
			if !ok {
				continue
			}
			t.Logf("%-14s   %s: %d segments %v, %.0f kbit/s avg", v, q, len(d), d,
				float64(r.HLSBytes[q])*8/1000/meta.DurationFloat)
		}
	}
}
