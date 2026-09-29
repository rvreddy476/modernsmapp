package fingerprint

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The ffmpeg-backed harness (plan 12.5 in miniature, T15-6, T10-3, T10-6).
// Skipped without ffmpeg on PATH and in -short. It runs the real
// extractor over lavfi sources: a reference clip and its transforms must
// come out full_or_near_full through the whole pipeline (anchors → probes
// → hits → vote → align → classify); a flipped or re-timed copy need not;
// black, a static card and different sources must not.
//
// Run it in a throwaway container from the worker image (ffmpeg 6.1):
//
//	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c -o fp.test ./internal/fingerprint
//	docker run --rm --name fp-harness -v "$PWD:/t" atpost_stack-media-worker:latest /t/fp.test -test.run Harness -test.v

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("ffmpeg harness skipped in -short")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
}

// lavfi renders a source graph with an optional video filter into an MP4.
func lavfi(t *testing.T, dir, name, source, vf string, extra ...string) string {
	t.Helper()
	p := filepath.Join(dir, name+".mp4")
	args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", source}
	args = append(args, extra...)
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-crf", "20", "-pix_fmt", "yuv420p", "-an", p)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %s: %v\n%s", name, err, out)
	}
	return p
}

// transcode re-encodes an existing file with a filter (and optional
// input-side args such as -ss).
func transcode(t *testing.T, dir, name, src, vf string, crf string, inputArgs ...string) string {
	t.Helper()
	p := filepath.Join(dir, name+".mp4")
	args := []string{"-v", "error", "-y"}
	args = append(args, inputArgs...)
	args = append(args, "-i", src)
	if vf != "" {
		args = append(args, "-vf", vf)
	}
	args = append(args, "-c:v", "libx264", "-preset", "ultrafast", "-crf", crf, "-pix_fmt", "yuv420p", "-an", p)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %s: %v\n%s", name, err, out)
	}
	return p
}

func hashFile(t *testing.T, path string) *Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	res, err := Extractor{}.FromFile(ctx, path)
	if err != nil {
		t.Fatalf("extract %s: %v", filepath.Base(path), err)
	}
	return res
}

// diagnose reports, for a copy that failed to match, the best raw
// alignment there is: over offsets on the 2 fps grid within ±5 s, the one
// with the most frames under τ, and its median Hamming. It separates "the
// hashes disagree" from "the candidate stage never voted".
func diagnose(t *testing.T, name string, copyF, refF []Frame) {
	t.Helper()
	refIdx := map[int32]uint64{}
	for _, f := range refF {
		refIdx[f.TMs] = f.Hash
	}
	bestOff, bestN, bestMed, bestMedNb := int32(0), -1, 64, 64
	for off := int32(-5000); off <= 5000; off += FrameMs {
		var ds, dsNb []int
		for _, c := range copyF {
			h, ok := refIdx[c.TMs+off]
			if !ok {
				continue
			}
			d := Hamming(c.Hash, h)
			ds = append(ds, d)
			// Neighbour-tolerant: the nearest of the frame and its ±1
			// grid neighbours (a ±0.5 s phase allowance).
			nb := d
			for _, o := range []int32{-FrameMs, FrameMs} {
				if h2, ok := refIdx[c.TMs+off+o]; ok {
					if d2 := Hamming(c.Hash, h2); d2 < nb {
						nb = d2
					}
				}
			}
			dsNb = append(dsNb, nb)
		}
		if len(ds) == 0 {
			continue
		}
		n := 0
		for _, d := range ds {
			if d <= AlignHamming {
				n++
			}
		}
		sortInts(ds)
		sortInts(dsNb)
		if n > bestN || (n == bestN && ds[len(ds)/2] < bestMed) {
			bestOff, bestN, bestMed, bestMedNb = off, n, ds[len(ds)/2], dsNb[len(dsNb)/2]
		}
	}
	t.Logf("%s: best grid offset %d ms: %d/%d frames ≤ τ, median Hamming %d (neighbour-tolerant median %d)",
		name, bestOff, bestN, len(copyF), bestMed, bestMedNb)
}

func sortInts(v []int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j-1] > v[j]; j-- {
			v[j-1], v[j] = v[j], v[j-1]
		}
	}
}

// pipeline runs copy against ref exactly as the worker does.
func pipeline(copyF, refF []Frame) (Scores, Class) {
	spacing := AnchorSpacingS(InformativeMs(Weights(refF)))
	var postings []Posting
	for _, a := range Anchors(refF, spacing) {
		postings = append(postings, Posting{AnchorMs: a.TMs, Hash: a.Hash})
	}
	q := QueryFrames(copyF)
	hits := Hits(q, postings)
	offsets := Vote(hits)
	a := Align(copyF, refF, offsets)
	s := Score(copyF, refF, a, false)
	return s, Classify(s)
}

func TestHarnessTransformsAndNegatives(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	const seconds = 24
	// The reference is a mandelbrot zoom: natural-like spectra and motion
	// that a real recording could have (source_survey_test.go: a 0.2 s
	// phase shift moves its hashes by ~2 bits, against ~20 for testsrc2,
	// whose every 30 fps frame is a different picture).
	src := "mandelbrot=size=640x360:rate=30"
	ref := lavfi(t, dir, "ref", src, "", "-t", fmt.Sprint(seconds))
	refRes := hashFile(t, ref)
	if got := refRes.MeasuredMs; !DurationConsistent(got, seconds*1000) {
		t.Fatalf("reference measured %d ms", got)
	}
	if inf := InformativeMs(Weights(refRes.Frames)); inf < 20_000 {
		t.Fatalf("reference informative %d ms; the zoom should be nearly all informative", inf)
	}
	t.Logf("reference: %d frames, crop %+v", len(refRes.Frames), refRes.Crop)

	mustMatch := map[string]string{
		"reencode_crf32": transcode(t, dir, "reencode", ref, "", "32"),
		"scale_240p":     transcode(t, dir, "scale240", ref, "scale=426:240", "23"),
		"letterbox":      transcode(t, dir, "letterbox", ref, "scale=640:240,pad=640:360:0:60:black", "23"),
		"pillarbox":      transcode(t, dir, "pillarbox", ref, "scale=480:270,pad=640:270:80:0:black", "23"),
		"brightness":     transcode(t, dir, "bright", ref, "eq=brightness=0.12:contrast=1.15:saturation=1.3", "23"),
		"logo":           transcode(t, dir, "logo", ref, "drawbox=x=16:y=16:w=120:h=68:color=white@1:t=fill", "23"),
		"trim_5pct":      transcode(t, dir, "trim", ref, "", "23", "-ss", "1.2"),
	}
	for name, path := range mustMatch {
		res := hashFile(t, path)
		s, c := pipeline(res.Frames, refRes.Frames)
		if c != ClassFullOrNearFull {
			t.Errorf("%s: class %s, want full_or_near_full: %+v (crop %+v)", name, c, s, res.Crop)
			diagnose(t, name, res.Frames, refRes.Frames)
		}
	}

	// Out of pass scope, and diagnostics that separate phase from overlay
	// sensitivity: reported, not asserted either way.
	for name, path := range map[string]string{
		"hflip":            transcode(t, dir, "hflip", ref, "hflip", "23"),
		"speed_1_1":        transcode(t, dir, "speed", ref, "setpts=PTS/1.1", "23"),
		"trim_on_grid_1s":  transcode(t, dir, "trim1", ref, "", "23", "-ss", "1.0"),
		"trim_off_grid_04": transcode(t, dir, "trim04", ref, "", "23", "-ss", "0.4"),
		"logo_10pct_white": transcode(t, dir, "logo10", ref, "drawbox=x=16:y=16:w=200:h=115:color=white@1:t=fill", "23"),
		"testsrc2_fast_vs_its_1_2s_trim": func() string {
			// The adversarial source: every 30 fps frame a new picture.
			fast := lavfi(t, dir, "fast", fmt.Sprintf("testsrc2=size=640x360:rate=30:duration=%d", seconds), "")
			fastTrim := transcode(t, dir, "fasttrim", fast, "", "23", "-ss", "1.2")
			fr := hashFile(t, fast)
			s, c := pipeline(hashFile(t, fastTrim).Frames, fr.Frames)
			t.Logf("testsrc2 (fast, adversarial) 1.2 s trim: class %s copy %.2f ref %.2f", c, s.CopyCoverage, s.RefCoverage)
			return fastTrim
		}(),
	} {
		res := hashFile(t, path)
		s, c := pipeline(res.Frames, refRes.Frames)
		t.Logf("%s (diagnostic): class %s, copy %.2f ref %.2f", name, c, s.CopyCoverage, s.RefCoverage)
		diagnose(t, name, res.Frames, refRes.Frames)
	}

	mustNot := map[string]string{
		"black":       lavfi(t, dir, "black", fmt.Sprintf("color=black:size=640x360:rate=30:duration=%d", seconds), ""),
		"static_card": lavfi(t, dir, "smpte", fmt.Sprintf("smptebars=size=640x360:rate=30:duration=%d", seconds), ""),
		"testsrc":     lavfi(t, dir, "testsrc", fmt.Sprintf("testsrc=size=640x360:rate=30:duration=%d", seconds), ""),
		"testsrc2":    lavfi(t, dir, "testsrc2", fmt.Sprintf("testsrc2=size=640x360:rate=30:duration=%d", seconds), ""),
		"life":        lavfi(t, dir, "life", "life=size=640x360:rate=30:mold=10:ratio=0.3", "", "-t", fmt.Sprint(seconds)),
		"other_zoom":  lavfi(t, dir, "otherzoom", "mandelbrot=size=640x360:rate=30:start_x=-0.75:start_y=0.1:end_scale=0.01", "", "-t", fmt.Sprint(seconds)),
	}
	for name, path := range mustNot {
		res := hashFile(t, path)
		s, c := pipeline(res.Frames, refRes.Frames)
		if c == ClassFullOrNearFull || c == ClassContains || c == ClassPartial {
			t.Errorf("%s: class %s must not match: %+v", name, c, s)
		}
	}
	// Black is all flat; the static card weighs at most 2 s.
	black := hashFile(t, mustNot["black"])
	if inf := InformativeMs(Weights(black.Frames)); inf != 0 {
		t.Errorf("black informative %d ms, want 0", inf)
	}
	card := hashFile(t, mustNot["static_card"])
	if inf := InformativeMs(Weights(card.Frames)); inf > StaticRunCapMs {
		t.Errorf("static card informative %d ms, want ≤ %d", inf, StaticRunCapMs)
	}
}

// The same bytes through the stream path (what the HLS rung uses) hash the
// same as through the file path.
func TestHarnessStreamPathMatchesFilePath(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	ref := lavfi(t, dir, "ref", "testsrc2=size=640x360:rate=30:duration=10", "")
	ts := filepath.Join(dir, "ref.ts")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-y", "-i", ref, "-c", "copy", "-f", "mpegts", ts).CombinedOutput(); err != nil {
		t.Fatalf("remux: %v\n%s", err, out)
	}
	fileRes := hashFile(t, ref)
	f, err := os.Open(ts)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	streamRes, err := Extractor{}.FromStream(ctx, "mpegts", f)
	if err != nil {
		t.Fatalf("stream extract: %v", err)
	}
	if len(streamRes.Frames) != len(fileRes.Frames) {
		t.Fatalf("stream %d frames, file %d", len(streamRes.Frames), len(fileRes.Frames))
	}
	for i := range fileRes.Frames {
		if d := Hamming(fileRes.Frames[i].Hash, streamRes.Frames[i].Hash); d > 2 {
			t.Fatalf("frame %d: stream vs file Hamming %d", i, d)
		}
	}
}

// A deadline kills ffmpeg and returns the context error, even while ffmpeg
// is blocked waiting for input (a stalled segment stream).
func TestHarnessDeadlineStopsFFmpeg(t *testing.T) {
	requireFFmpeg(t)
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Extractor{}.FromStream(ctx, "mpegts", pr)
	if err == nil {
		t.Fatal("expected a deadline error")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("deadline took %s to stop ffmpeg", time.Since(start))
	}
}
