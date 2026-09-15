package processing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Lane D5 — blink liveness decision matrix, driven by a fake sampler and a
// fake Rekognition that returns scripted per-frame EyesOpen sequences.
//
// Script letters, one per sampled frame:
//
//	O  one face, eyes open,   confidence 95
//	C  one face, eyes closed, confidence 95
//	o  one face, eyes open,   confidence 50 (below the bar: ignored)
//	c  one face, eyes closed, confidence 50 (below the bar: ignored)
//	2  two faces
//	0  no face
//	J  one face, eyes open, bounding box jumped across the frame

type fakeSampler struct {
	durationMs int
	frames     int
	err        error
	calls      int
}

func (f *fakeSampler) SampleFrames(_ context.Context, _ []byte, _, maxFrames, longestMs int) (*SampledVideo, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	out := &SampledVideo{DurationMs: f.durationMs}
	if longestMs > 0 && f.durationMs > longestMs {
		return out, nil
	}
	n := f.frames
	if n > maxFrames {
		n = maxFrames
	}
	for i := 0; i < n; i++ {
		out.Frames = append(out.Frames, []byte(fmt.Sprintf("frame-%03d", i)))
	}
	return out, nil
}

type fakeLivenessAPI struct {
	mu             sync.Mutex
	script         string
	consistency    float32 // frame vs frame
	reference      float32 // frame vs reference
	referenceFaces int
	detectErr      error
	detectCalls    int
	compareCalls   int
	sawAll         bool
}

func (f *fakeLivenessAPI) DetectFaces(_ context.Context, in *rekognition.DetectFacesInput, _ ...func(*rekognition.Options)) (*rekognition.DetectFacesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detectCalls++
	if f.detectErr != nil {
		return nil, f.detectErr
	}
	for _, a := range in.Attributes {
		if a == rektypes.AttributeAll {
			f.sawAll = true
		}
	}
	var idx int
	if _, err := fmt.Sscanf(string(in.Image.Bytes), "frame-%03d", &idx); err != nil || idx >= len(f.script) {
		return &rekognition.DetectFacesOutput{}, nil
	}
	face := func(open bool, conf, left float32) rektypes.FaceDetail {
		return rektypes.FaceDetail{
			EyesOpen:    &rektypes.EyeOpen{Value: open, Confidence: aws.Float32(conf)},
			Quality:     &rektypes.ImageQuality{Sharpness: aws.Float32(float32(40 + idx))},
			BoundingBox: &rektypes.BoundingBox{Left: aws.Float32(left), Top: aws.Float32(0.3), Width: aws.Float32(0.3), Height: aws.Float32(0.4)},
		}
	}
	switch f.script[idx] {
	case 'O':
		return &rekognition.DetectFacesOutput{FaceDetails: []rektypes.FaceDetail{face(true, 95, 0.35)}}, nil
	case 'C':
		return &rekognition.DetectFacesOutput{FaceDetails: []rektypes.FaceDetail{face(false, 95, 0.35)}}, nil
	case 'o':
		return &rekognition.DetectFacesOutput{FaceDetails: []rektypes.FaceDetail{face(true, 50, 0.35)}}, nil
	case 'c':
		return &rekognition.DetectFacesOutput{FaceDetails: []rektypes.FaceDetail{face(false, 50, 0.35)}}, nil
	case 'J':
		return &rekognition.DetectFacesOutput{FaceDetails: []rektypes.FaceDetail{face(true, 95, 0.9)}}, nil
	case '2':
		return &rekognition.DetectFacesOutput{FaceDetails: []rektypes.FaceDetail{face(true, 95, 0.1), face(true, 95, 0.6)}}, nil
	}
	return &rekognition.DetectFacesOutput{}, nil
}

func (f *fakeLivenessAPI) CompareFaces(_ context.Context, in *rekognition.CompareFacesInput, _ ...func(*rekognition.Options)) (*rekognition.CompareFacesOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.compareCalls++
	if bytes.HasPrefix(in.TargetImage.Bytes, []byte("frame-")) {
		return &rekognition.CompareFacesOutput{FaceMatches: []rektypes.CompareFacesMatch{{Similarity: aws.Float32(f.consistency)}}}, nil
	}
	out := &rekognition.CompareFacesOutput{FaceMatches: []rektypes.CompareFacesMatch{{Similarity: aws.Float32(f.reference)}}}
	for i := 1; i < f.referenceFaces; i++ {
		out.UnmatchedFaces = append(out.UnmatchedFaces, rektypes.ComparedFace{})
	}
	return out, nil
}

func runLiveness(t *testing.T, script string, mutate func(*fakeLivenessAPI, *fakeSampler)) (LivenessResult, *fakeLivenessAPI, *fakeSampler, error) {
	t.Helper()
	api := &fakeLivenessAPI{script: script, consistency: 97, reference: 95, referenceFaces: 1}
	sampler := &fakeSampler{durationMs: 3000, frames: len(script)}
	if mutate != nil {
		mutate(api, sampler)
	}
	res, err := newRekognitionLivenessAnalyzerWithAPI(api, sampler).
		AnalyzeLiveness(context.Background(), []byte("video"), []byte("reference"), DefaultLivenessConfig())
	return res, api, sampler, err
}

func TestLiveness_TwoCleanBlinksPass(t *testing.T) {
	res, api, _, err := runLiveness(t, "OOOCOOOCOOOO", nil)
	if err != nil || res.Reason != "" || res.BlinksDetected != 2 || !res.SingleFace || !res.SameFaceAcrossFrames ||
		res.Similarity != 95 || res.FramesAnalysed != 12 || res.DurationMs != 3000 {
		t.Fatalf("two blinks = %+v, %v", res, err)
	}
	if api.detectCalls != 12 || api.compareCalls != 2 || !api.sawAll {
		t.Fatalf("provider calls detect=%d compare=%d attributesALL=%v; want 12 / 2 / true", api.detectCalls, api.compareCalls, api.sawAll)
	}
	// A two-frame closure is one blink, not two.
	if res, _, _, _ := runLiveness(t, "OOCCOOOCCOOO", nil); res.BlinksDetected != 2 || res.Reason != "" {
		t.Fatalf("two-frame closures = %+v", res)
	}
}

func TestLiveness_OneBlinkIsNotEnough(t *testing.T) {
	res, api, _, err := runLiveness(t, "OOOCOOOOOOOO", nil)
	if err != nil || res.Reason != LivenessReasonNotEnoughBlinks || res.BlinksDetected != 1 || res.Similarity != 0 {
		t.Fatalf("one blink = %+v, %v", res, err)
	}
	if api.compareCalls != 1 {
		t.Fatalf("compare calls = %d; the reference is not compared without enough blinks", api.compareCalls)
	}
}

// A still photo held up to the camera: eyes never close.
func TestLiveness_EyesNeverClosedFails(t *testing.T) {
	res, _, _, err := runLiveness(t, "OOOOOOOOOOOO", nil)
	if err != nil || res.Reason != LivenessReasonNotEnoughBlinks || res.BlinksDetected != 0 {
		t.Fatalf("eyes never closed = %+v, %v", res, err)
	}
}

func TestLiveness_IncompleteCyclesAreNotBlinks(t *testing.T) {
	for _, script := range []string{
		"CCCOOOOOOOOO", // closed then open: no open before the closure
		"OOOOOOOOCCCC", // open then closed: never reopened
		"CCOOOOOOOCCC", // both ends incomplete
	} {
		res, _, _, err := runLiveness(t, script, nil)
		if err != nil || res.BlinksDetected != 0 || res.Reason != LivenessReasonNotEnoughBlinks {
			t.Fatalf("%s = %+v, %v; want 0 blinks", script, res, err)
		}
	}
}

func TestLiveness_SecondFaceMidVideo(t *testing.T) {
	res, _, _, err := runLiveness(t, "OOOCO2OOCOOO", nil)
	if err != nil || res.Reason != FaceReasonMultipleFaces || res.SingleFace {
		t.Fatalf("second face = %+v, %v", res, err)
	}
	if res, _, _, _ := runLiveness(t, "OOOCOO0OCOOO", nil); res.Reason != FaceReasonNoFace || res.SingleFace {
		t.Fatalf("face missing from a frame = %+v", res)
	}
}

func TestLiveness_FaceChanged(t *testing.T) {
	// Same single-face frames throughout, but the first and last open frames
	// are a different person (a swapped face or a second person).
	res, _, _, err := runLiveness(t, "OOOCOOOCOOOO", func(a *fakeLivenessAPI, _ *fakeSampler) { a.consistency = 40 })
	if err != nil || res.Reason != LivenessReasonFaceChanged || res.SameFaceAcrossFrames || !res.SingleFace {
		t.Fatalf("different person = %+v, %v", res, err)
	}
	// The face jumps across the frame between consecutive samples.
	res, _, _, err = runLiveness(t, "OOOCOJOOCOOO", nil)
	if err != nil || res.Reason != LivenessReasonFaceChanged {
		t.Fatalf("bounding box jump = %+v, %v", res, err)
	}
}

// Eye states below the confidence bar are ignored: they neither make nor
// break a blink.
func TestLiveness_LowConfidenceFramesIgnored(t *testing.T) {
	res, _, _, err := runLiveness(t, "OOOOcOOOOcOOOO", nil)
	if err != nil || res.BlinksDetected != 0 || res.Reason != LivenessReasonNotEnoughBlinks {
		t.Fatalf("low-confidence closures = %+v, %v; want them ignored", res, err)
	}
	// A low-confidence open frame inside a closure does not split it.
	res, _, _, err = runLiveness(t, "OOOCoCOOOCOOOO", nil)
	if err != nil || res.BlinksDetected != 2 || res.Reason != "" {
		t.Fatalf("low-confidence open inside a closure = %+v, %v; want 2 blinks", res, err)
	}
	// Too few confident frames is no verdict on blinks at all.
	res, _, _, err = runLiveness(t, "OOcococococo", nil)
	if err != nil || res.Reason != LivenessReasonLowQuality {
		t.Fatalf("mostly low confidence = %+v, %v; want LOW_QUALITY", res, err)
	}
}

func TestLiveness_VideoTooLongRefusedBeforeAnalysis(t *testing.T) {
	res, api, _, err := runLiveness(t, "OOOCOOOCOOOO", func(_ *fakeLivenessAPI, s *fakeSampler) { s.durationMs = 6000 })
	if err != nil || res.Reason != LivenessReasonVideoTooLong || res.DurationMs != 6000 || api.detectCalls != 0 {
		t.Fatalf("long video = %+v, %v, detect calls %d", res, err, api.detectCalls)
	}
}

func TestLiveness_FramesBoundedAndReferenceFaces(t *testing.T) {
	long := strings.Repeat("OOOC", 15) + "OOOO" // 64 frames
	res, api, _, err := runLiveness(t, long, nil)
	if err != nil || res.FramesAnalysed != DefaultLivenessConfig().MaxFrames || api.detectCalls != DefaultLivenessConfig().MaxFrames {
		t.Fatalf("bounded frames = %+v, %v, detect calls %d", res, err, api.detectCalls)
	}
	res, _, _, _ = runLiveness(t, "OOOCOOOCOOOO", func(a *fakeLivenessAPI, _ *fakeSampler) { a.referenceFaces = 2 })
	if res.Reason != FaceReasonMultipleFaces {
		t.Fatalf("reference with two faces = %+v", res)
	}
	res, _, _, _ = runLiveness(t, "OOOCOOOCOOOO", func(a *fakeLivenessAPI, _ *fakeSampler) { a.reference = 62 })
	if res.Reason != "" || res.Similarity != 62 {
		t.Fatalf("low similarity is reported, not decided here: %+v", res)
	}
}

func TestLiveness_ProviderFailuresAreUnavailable(t *testing.T) {
	_, _, _, err := runLiveness(t, "OOOCOOOCOOOO", func(a *fakeLivenessAPI, _ *fakeSampler) { a.detectErr = errors.New("ThrottlingException") })
	if !errors.Is(err, ErrFaceCompareUnavailable) {
		t.Fatalf("detect error: %v", err)
	}
	_, _, _, err = runLiveness(t, "OOOCOOOCOOOO", func(_ *fakeLivenessAPI, s *fakeSampler) { s.err = ErrLivenessVideoUnreadable })
	if !errors.Is(err, ErrLivenessVideoUnreadable) {
		t.Fatalf("sampler error: %v", err)
	}
	a := newRekognitionLivenessAnalyzerWithAPI(&fakeLivenessAPI{}, &fakeSampler{})
	if _, err := a.AnalyzeLiveness(context.Background(), []byte("v"), nil, DefaultLivenessConfig()); !errors.Is(err, ErrFaceCompareUnavailable) {
		t.Fatalf("empty reference: %v", err)
	}
}

func TestCountBlinks(t *testing.T) {
	o, c := true, false
	cases := []struct {
		seq  []bool
		want int
	}{
		{[]bool{o, c, o, c, o}, 2},
		{[]bool{o, c, c, c, o}, 1},
		{[]bool{c, o, c, o}, 1},
		{[]bool{c, c, o, o}, 0},
		{[]bool{o, o, c, c}, 0},
		{nil, 0},
	}
	for _, tc := range cases {
		if got := countBlinks(tc.seq, 1); got != tc.want {
			t.Fatalf("countBlinks(%v) = %d, want %d", tc.seq, got, tc.want)
		}
	}
	if got := countBlinks([]bool{o, c, o, c, c, o}, 2); got != 1 {
		t.Fatalf("min closed run 2 = %d, want 1", got)
	}
}

func TestMockLivenessAnalyzer_NeverPassesArbitraryUploads(t *testing.T) {
	m := NewMockLivenessAnalyzer()
	ctx := context.Background()
	cfg := DefaultLivenessConfig()
	ref := []byte(MockFaceMarker + "faces=1:subject=asha")
	res, err := m.AnalyzeLiveness(ctx, []byte("a real selfie video"), ref, cfg)
	if err != nil || res.Reason != FaceReasonNoFace || res.BlinksDetected != 0 {
		t.Fatalf("arbitrary video = %+v, %v", res, err)
	}
	cases := []struct {
		marker     string
		wantReason string
		wantBlinks int
		wantSim    float64
	}{
		{"blinks=2:faces=1:subject=asha", "", 2, 99},
		{"blinks=3:faces=1:subject=asha:similarity=85", "", 3, 85},
		{"blinks=1:faces=1:subject=asha", LivenessReasonNotEnoughBlinks, 1, 0},
		{"blinks=2:faces=1:subject=ravi", "", 2, 10},
		{"blinks=2:faces=2:subject=asha", FaceReasonMultipleFaces, 0, 0},
		{"blinks=2:faces=1:subject=asha:changed=1", LivenessReasonFaceChanged, 0, 0},
		{"blinks=2:faces=1:subject=asha:duration_ms=6000", LivenessReasonVideoTooLong, 0, 0},
	}
	for _, tc := range cases {
		res, err := m.AnalyzeLiveness(ctx, []byte("mp4 "+MockLivenessMarker+tc.marker+"\n"), ref, cfg)
		if err != nil || res.Reason != tc.wantReason || res.BlinksDetected != tc.wantBlinks || res.Similarity != tc.wantSim {
			t.Fatalf("%s = %+v, %v", tc.marker, res, err)
		}
	}
}

// Real ffmpeg, when installed on the test host: a generated 2 s clip samples
// at 8 fps; a 6 s clip is refused without extracting frames.
func TestFFmpegFrameSampler_Real(t *testing.T) {
	if err := RequireFFmpeg(); err != nil {
		t.Skipf("ffmpeg not installed: %v", err)
	}
	dir := t.TempDir()
	gen := func(seconds int) []byte {
		p := filepath.Join(dir, fmt.Sprintf("clip_%d.mp4", seconds))
		cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=30",
			"-t", fmt.Sprint(seconds), "-pix_fmt", "yuv420p", p)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("generate clip: %v %s", err, out)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read clip: %v", err)
		}
		return b
	}
	s := FFmpegFrameSampler{}
	got, err := s.SampleFrames(context.Background(), gen(2), 8, 40, 4500)
	if err != nil || got.DurationMs < 1900 || got.DurationMs > 2100 || len(got.Frames) < 15 || len(got.Frames) > 17 {
		t.Fatalf("2 s clip: duration=%v frames=%d err=%v", got, len(got.Frames), err)
	}
	got, err = s.SampleFrames(context.Background(), gen(6), 8, 40, 4500)
	if err != nil || len(got.Frames) != 0 || got.DurationMs < 5900 {
		t.Fatalf("6 s clip: %+v, %v", got, err)
	}
	if _, err := s.SampleFrames(context.Background(), []byte("not a video"), 8, 40, 4500); !errors.Is(err, ErrLivenessVideoUnreadable) {
		t.Fatalf("garbage: %v", err)
	}
}
