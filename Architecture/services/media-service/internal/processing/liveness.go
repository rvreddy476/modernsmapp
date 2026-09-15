package processing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Dating plan lane D5 — blink liveness.
//
// The user records a short video (≤ MaxDurationMs) blinking twice. The
// server samples frames at a fixed rate, asks the provider for the face and
// eye state in every sampled frame, and requires:
//
//   - exactly one face in every analysed frame (NO_FACE / MULTIPLE_FACES);
//   - the same face throughout: no bounding-box jump between consecutive
//     frames, and the first and last eyes-open frames compare as the same
//     person (FACE_CHANGED);
//   - at least MinConfidentFrames frames whose eye state is reported with
//     confidence ≥ EyesOpenConfidence (LOW_QUALITY) — frames below that
//     confidence are ignored, never counted as open or closed;
//   - RequiredBlinks blinks, a blink being eyes-open → eyes-closed for at
//     least MinClosedFrames confident frames → eyes-open (NOT_ENOUGH_BLINKS).
//
// Then the sharpest eyes-open frame is compared with the reference (the
// approved primary photo) for the similarity.
//
// No frame, embedding or byte leaves this package in a result or a log;
// extracted frames live in a temporary directory removed before return.

// Liveness reason codes (NO_FACE and MULTIPLE_FACES are shared with compare).
const (
	LivenessReasonFaceChanged     = "FACE_CHANGED"
	LivenessReasonNotEnoughBlinks = "NOT_ENOUGH_BLINKS"
	LivenessReasonVideoTooLong    = "VIDEO_TOO_LONG"
	LivenessReasonLowQuality      = "LOW_QUALITY"
)

// ErrLivenessVideoUnreadable means the upload is not a decodable video.
var ErrLivenessVideoUnreadable = errors.New("liveness: video cannot be decoded")

// LivenessConfig holds sampling and decision parameters (MEDIA_LIVENESS_*).
type LivenessConfig struct {
	SampleFPS           int     // frames sampled per second of video
	MaxFrames           int     // hard cap on analysed frames (= provider calls)
	MaxDurationMs       int     // longest accepted recording
	DurationToleranceMs int     // container/encoder slack on top of MaxDurationMs
	EyesOpenConfidence  float64 // 0-100; eye states below this are ignored
	MinClosedFrames     int     // confident closed frames that make a blink
	RequiredBlinks      int     // blinks needed
	MinConfidentFrames  int     // confident frames needed for any verdict
	FaceConsistency     float64 // 0-100; first vs last open frame similarity
	MaxCentreJump       float64 // 0-1; bounding-box centre move between frames
	MinSharpness        float64 // 0-100; best frame sharpness floor
}

// DefaultLivenessConfig: 8 fps, ≤40 frames, ≤4 s (+0.5 s), eye confidence
// 80, a 1-frame closed run, 2 blinks, 8 confident frames, face consistency
// 90, centre jump 0.25, no sharpness floor.
func DefaultLivenessConfig() LivenessConfig {
	return LivenessConfig{
		SampleFPS:           8,
		MaxFrames:           40,
		MaxDurationMs:       4000,
		DurationToleranceMs: 500,
		EyesOpenConfidence:  80,
		MinClosedFrames:     1,
		RequiredBlinks:      2,
		MinConfidentFrames:  8,
		FaceConsistency:     90,
		MaxCentreJump:       0.25,
		MinSharpness:        0,
	}
}

func (c LivenessConfig) normalized() LivenessConfig {
	d := DefaultLivenessConfig()
	if c.SampleFPS <= 0 {
		c.SampleFPS = d.SampleFPS
	}
	if c.MaxFrames <= 0 {
		c.MaxFrames = d.MaxFrames
	}
	if c.MaxDurationMs <= 0 {
		c.MaxDurationMs = d.MaxDurationMs
	}
	if c.DurationToleranceMs < 0 {
		c.DurationToleranceMs = d.DurationToleranceMs
	}
	if c.EyesOpenConfidence <= 0 {
		c.EyesOpenConfidence = d.EyesOpenConfidence
	}
	if c.MinClosedFrames <= 0 {
		c.MinClosedFrames = d.MinClosedFrames
	}
	if c.RequiredBlinks <= 0 {
		c.RequiredBlinks = d.RequiredBlinks
	}
	if c.MinConfidentFrames <= 0 {
		c.MinConfidentFrames = d.MinConfidentFrames
	}
	if c.FaceConsistency <= 0 {
		c.FaceConsistency = d.FaceConsistency
	}
	if c.MaxCentreJump <= 0 {
		c.MaxCentreJump = d.MaxCentreJump
	}
	return c
}

// LongestAcceptedMs is MaxDurationMs plus the tolerance.
func (c LivenessConfig) LongestAcceptedMs() int {
	c = c.normalized()
	return c.MaxDurationMs + c.DurationToleranceMs
}

// LivenessResult is a liveness verdict. Reason empty means every liveness
// check held; Similarity is then the best frame against the reference.
type LivenessResult struct {
	BlinksDetected       int
	FramesAnalysed       int
	DurationMs           int
	SingleFace           bool
	SameFaceAcrossFrames bool
	Similarity           float64
	Reason               string
}

// LivenessAnalyzer runs blink liveness on a video and compares its face with
// a reference image.
type LivenessAnalyzer interface {
	AnalyzeLiveness(ctx context.Context, video, reference []byte, cfg LivenessConfig) (LivenessResult, error)
	Name() string
}

// ── Frame sampling ──────────────────────────────────────────────────────────

// SampledVideo is the probed duration and the sampled JPEG frames, in order.
// Frames is empty when the video is longer than the caller allowed.
type SampledVideo struct {
	DurationMs int
	Frames     [][]byte
}

// FrameSampler extracts frames at a fixed rate.
type FrameSampler interface {
	SampleFrames(ctx context.Context, video []byte, fps, maxFrames, longestMs int) (*SampledVideo, error)
}

// FFmpegFrameSampler samples with ffprobe + ffmpeg (the tools the worker's
// transcode already uses). The upload and every extracted frame are written
// to a private temporary directory that is removed before returning.
type FFmpegFrameSampler struct{}

// RequireFFmpeg reports whether ffmpeg and ffprobe are on PATH.
func RequireFFmpeg() error {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			return fmt.Errorf("liveness needs %s on PATH (install ffmpeg in the media-service image): %w", bin, err)
		}
	}
	return nil
}

// SampleFrames implements FrameSampler.
func (FFmpegFrameSampler) SampleFrames(ctx context.Context, video []byte, fps, maxFrames, longestMs int) (*SampledVideo, error) {
	if len(video) == 0 {
		return nil, ErrLivenessVideoUnreadable
	}
	dir, err := os.MkdirTemp("", "liveness-")
	if err != nil {
		return nil, fmt.Errorf("%w: temp dir: %v", ErrFaceCompareUnavailable, err)
	}
	defer os.RemoveAll(dir)

	input := filepath.Join(dir, "input")
	if err := os.WriteFile(input, video, 0o600); err != nil {
		return nil, fmt.Errorf("%w: write temp: %v", ErrFaceCompareUnavailable, err)
	}
	meta, err := ProbeVideo(ctx, input)
	if err != nil || meta == nil || meta.DurationFloat <= 0 {
		return nil, ErrLivenessVideoUnreadable
	}
	out := &SampledVideo{DurationMs: int(math.Round(meta.DurationFloat * 1000))}
	if longestMs > 0 && out.DurationMs > longestMs {
		return out, nil
	}
	args := []string{
		"-v", "error", "-y", "-i", input,
		"-vf", fmt.Sprintf("fps=%d,scale=w=-2:h='min(720,ih)'", fps),
		"-frames:v", strconv.Itoa(maxFrames),
		"-q:v", "3",
		filepath.Join(dir, "frame_%03d.jpg"),
	}
	if msg, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %v", ErrFaceCompareUnavailable, ctx.Err())
		}
		_ = msg // never echoed: ffmpeg output can quote container metadata
		return nil, ErrLivenessVideoUnreadable
	}
	paths, err := filepath.Glob(filepath.Join(dir, "frame_*.jpg"))
	if err != nil {
		return nil, fmt.Errorf("%w: list frames: %v", ErrFaceCompareUnavailable, err)
	}
	sort.Strings(paths)
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("%w: read frame: %v", ErrFaceCompareUnavailable, err)
		}
		out.Frames = append(out.Frames, b)
	}
	return out, nil
}

// ── Decision (provider-independent) ─────────────────────────────────────────

// FrameObservation is what the provider reported for one frame.
type FrameObservation struct {
	FaceCount      int
	HasEyes        bool
	EyesOpen       bool
	EyesConfidence float64
	Sharpness      float64
	HasBox         bool
	CentreX        float64
	CentreY        float64
}

// countBlinks counts open → closed (≥ minClosed) → open cycles in a sequence
// of confident eye states. A sequence that starts closed does not count its
// first reopening, and a closure never reopened is not a blink.
func countBlinks(open []bool, minClosed int) int {
	blinks, closedRun, sawOpen := 0, 0, false
	for _, o := range open {
		if o {
			if sawOpen && closedRun >= minClosed {
				blinks++
			}
			sawOpen, closedRun = true, 0
			continue
		}
		if sawOpen {
			closedRun++
		}
	}
	return blinks
}

// compareFunc returns the similarity of the single face in source to the
// best face in target, and how many faces target holds.
type compareFunc func(ctx context.Context, source, target []byte) (similarity float64, targetFaces int, err error)

// evaluateLiveness applies the liveness rules to per-frame observations.
func evaluateLiveness(ctx context.Context, frames [][]byte, obs []FrameObservation, reference []byte,
	cfg LivenessConfig, cmp compareFunc) (LivenessResult, error) {
	res := LivenessResult{FramesAnalysed: len(obs)}
	if len(obs) == 0 {
		res.Reason = LivenessReasonLowQuality
		return res, nil
	}
	noFace := false
	for _, o := range obs {
		switch {
		case o.FaceCount > 1:
			res.Reason = FaceReasonMultipleFaces
			return res, nil
		case o.FaceCount == 0:
			noFace = true
		}
	}
	if noFace {
		res.Reason = FaceReasonNoFace
		return res, nil
	}
	res.SingleFace = true

	for i := 1; i < len(obs); i++ {
		a, b := obs[i-1], obs[i]
		if a.HasBox && b.HasBox && math.Hypot(a.CentreX-b.CentreX, a.CentreY-b.CentreY) > cfg.MaxCentreJump {
			res.Reason = LivenessReasonFaceChanged
			return res, nil
		}
	}

	var states []bool
	var openIdx []int
	for i, o := range obs {
		if !o.HasEyes || o.EyesConfidence < cfg.EyesOpenConfidence {
			continue
		}
		states = append(states, o.EyesOpen)
		if o.EyesOpen {
			openIdx = append(openIdx, i)
		}
	}
	if len(states) < cfg.MinConfidentFrames || len(openIdx) == 0 {
		res.Reason = LivenessReasonLowQuality
		return res, nil
	}
	res.BlinksDetected = countBlinks(states, cfg.MinClosedFrames)

	first, last := openIdx[0], openIdx[len(openIdx)-1]
	if first != last {
		sim, faces, err := cmp(ctx, frames[first], frames[last])
		if err != nil {
			return LivenessResult{}, err
		}
		if faces != 1 || sim < cfg.FaceConsistency {
			res.Reason = LivenessReasonFaceChanged
			return res, nil
		}
	}
	res.SameFaceAcrossFrames = true

	if res.BlinksDetected < cfg.RequiredBlinks {
		res.Reason = LivenessReasonNotEnoughBlinks
		return res, nil
	}

	best := openIdx[0]
	for _, i := range openIdx[1:] {
		if obs[i].Sharpness > obs[best].Sharpness {
			best = i
		}
	}
	if obs[best].Sharpness < cfg.MinSharpness {
		res.Reason = LivenessReasonLowQuality
		return res, nil
	}
	sim, faces, err := cmp(ctx, frames[best], reference)
	if err != nil {
		return LivenessResult{}, err
	}
	if r := faceCountReason(faces); r != "" {
		res.Reason = r
		return res, nil
	}
	res.Similarity = math.Max(0, math.Min(100, sim))
	return res, nil
}

// ── AWS Rekognition ─────────────────────────────────────────────────────────

// livenessDetectConcurrency bounds parallel DetectFaces calls per check.
const livenessDetectConcurrency = 4

// RekognitionLivenessAnalyzer: one DetectFaces (Attributes ALL) per sampled
// frame, one CompareFaces for face consistency and one against the
// reference — at most MaxFrames + 2 calls per verification.
type RekognitionLivenessAnalyzer struct {
	api     rekognitionFaceAPI
	sampler FrameSampler
}

// NewRekognitionLivenessAnalyzer builds the analyzer on the shared client.
func NewRekognitionLivenessAnalyzer(client *rekognition.Client, sampler FrameSampler) *RekognitionLivenessAnalyzer {
	return &RekognitionLivenessAnalyzer{api: client, sampler: sampler}
}

func newRekognitionLivenessAnalyzerWithAPI(api rekognitionFaceAPI, sampler FrameSampler) *RekognitionLivenessAnalyzer {
	return &RekognitionLivenessAnalyzer{api: api, sampler: sampler}
}

// Name implements LivenessAnalyzer.
func (*RekognitionLivenessAnalyzer) Name() string { return "rekognition" }

// AnalyzeLiveness implements LivenessAnalyzer.
func (a *RekognitionLivenessAnalyzer) AnalyzeLiveness(ctx context.Context, video, reference []byte, cfg LivenessConfig) (LivenessResult, error) {
	if a == nil || a.api == nil || a.sampler == nil {
		return LivenessResult{}, fmt.Errorf("%w: liveness analyzer not configured", ErrFaceCompareUnavailable)
	}
	if len(video) == 0 {
		return LivenessResult{}, ErrLivenessVideoUnreadable
	}
	if len(reference) == 0 || len(reference) > MaxFaceImageBytes {
		return LivenessResult{}, fmt.Errorf("%w: reference image unusable", ErrFaceCompareUnavailable)
	}
	cfg = cfg.normalized()
	sampled, err := a.sampler.SampleFrames(ctx, video, cfg.SampleFPS, cfg.MaxFrames, cfg.LongestAcceptedMs())
	if err != nil {
		return LivenessResult{}, err
	}
	if sampled.DurationMs > cfg.LongestAcceptedMs() {
		return LivenessResult{DurationMs: sampled.DurationMs, Reason: LivenessReasonVideoTooLong}, nil
	}
	frames := sampled.Frames
	if len(frames) > cfg.MaxFrames {
		frames = frames[:cfg.MaxFrames]
	}
	// DetectFaces per frame, livenessDetectConcurrency at a time; results
	// keep frame order.
	obs := make([]FrameObservation, len(frames))
	errs := make([]error, len(frames))
	sem := make(chan struct{}, livenessDetectConcurrency)
	var wg sync.WaitGroup
	for i, f := range frames {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, f []byte) {
			defer wg.Done()
			defer func() { <-sem }()
			out, err := a.api.DetectFaces(ctx, &rekognition.DetectFacesInput{
				Image:      &rektypes.Image{Bytes: f},
				Attributes: []rektypes.Attribute{rektypes.AttributeAll},
			})
			switch {
			case err != nil:
				errs[i] = fmt.Errorf("%w: detect faces: %v", ErrFaceCompareUnavailable, err)
			case out == nil:
				errs[i] = fmt.Errorf("%w: detect faces returned no result", ErrFaceCompareUnavailable)
			default:
				obs[i] = observeFrame(out.FaceDetails)
			}
		}(i, f)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return LivenessResult{}, err
		}
	}
	res, err := evaluateLiveness(ctx, frames, obs, reference, cfg, a.compare)
	res.DurationMs = sampled.DurationMs
	return res, err
}

func observeFrame(details []rektypes.FaceDetail) FrameObservation {
	o := FrameObservation{FaceCount: len(details)}
	if len(details) != 1 {
		return o
	}
	d := details[0]
	if d.EyesOpen != nil && d.EyesOpen.Confidence != nil {
		o.HasEyes, o.EyesOpen, o.EyesConfidence = true, d.EyesOpen.Value, float64(*d.EyesOpen.Confidence)
	}
	if d.Quality != nil && d.Quality.Sharpness != nil {
		o.Sharpness = float64(*d.Quality.Sharpness)
	}
	if b := d.BoundingBox; b != nil && b.Left != nil && b.Top != nil && b.Width != nil && b.Height != nil {
		o.HasBox = true
		o.CentreX = float64(*b.Left + *b.Width/2)
		o.CentreY = float64(*b.Top + *b.Height/2)
	}
	return o
}

// compare calls CompareFaces directly: the source frame is already known to
// hold exactly one face, so no extra DetectFaces is spent on it.
func (a *RekognitionLivenessAnalyzer) compare(ctx context.Context, source, target []byte) (float64, int, error) {
	out, err := a.api.CompareFaces(ctx, &rekognition.CompareFacesInput{
		SourceImage:         &rektypes.Image{Bytes: source},
		TargetImage:         &rektypes.Image{Bytes: target},
		SimilarityThreshold: aws.Float32(0),
	})
	if err != nil {
		return 0, 0, fmt.Errorf("%w: compare faces: %v", ErrFaceCompareUnavailable, err)
	}
	if out == nil {
		return 0, 0, fmt.Errorf("%w: compare faces returned no result", ErrFaceCompareUnavailable)
	}
	best := 0.0
	for _, m := range out.FaceMatches {
		if m.Similarity != nil && float64(*m.Similarity) > best {
			best = float64(*m.Similarity)
		}
	}
	return best, len(out.FaceMatches) + len(out.UnmatchedFaces), nil
}

// ── Mock (local/dev and tests only) ─────────────────────────────────────────

// MockLivenessMarker is the prefix of the test marker the mock reads from the
// video bytes. Format (ASCII, anywhere in the file):
//
//	ATPOST-LIVENESS-TEST:v1:blinks=<n>:faces=<n>:subject=<id>[:duration_ms=<n>][:changed=1][:similarity=<0-100>]
//
// The reference image follows the face-compare rule (MockFaceMarker). The
// mock is deterministic. Its dev rule: a video WITHOUT the marker is one face
// of MockFaceUploaderSubject blinking exactly RequiredBlinks times for 3 s,
// so a real phone recording passes against the same account's unmarked (or
// subject=uploader) photo. Negative tests use an explicit marker: blinks=1
// (NOT_ENOUGH_BLINKS), faces=0 / faces=2 (NO_FACE / MULTIPLE_FACES),
// changed=1 (FACE_CHANGED), duration_ms over the limit (VIDEO_TOO_LONG), or a
// subject different from the reference's (similarity 10). The reference must
// hold one face with the same subject or similarity is 10. Refused outside
// ENV local/dev.
const MockLivenessMarker = "ATPOST-LIVENESS-TEST:v1:"

// MockLivenessAnalyzer is the local/dev LivenessAnalyzer.
type MockLivenessAnalyzer struct{}

// NewMockLivenessAnalyzer returns the deterministic local/dev analyzer.
func NewMockLivenessAnalyzer() *MockLivenessAnalyzer { return &MockLivenessAnalyzer{} }

// Name implements LivenessAnalyzer.
func (*MockLivenessAnalyzer) Name() string { return "mock" }

func parseMarker(data []byte, prefix string) (map[string]string, bool) {
	i := bytes.Index(data, []byte(prefix))
	if i < 0 {
		return nil, false
	}
	rest := data[i+len(prefix):]
	if end := bytes.IndexAny(rest, "\x00\r\n \t"); end >= 0 {
		rest = rest[:end]
	}
	out := map[string]string{}
	for _, part := range strings.Split(string(rest), ":") {
		if k, v, ok := strings.Cut(part, "="); ok {
			out[k] = v
		}
	}
	return out, true
}

func markerInt(m map[string]string, key string, def int) int {
	if v, err := strconv.Atoi(m[key]); err == nil && v >= 0 {
		return v
	}
	return def
}

// AnalyzeLiveness implements LivenessAnalyzer.
func (*MockLivenessAnalyzer) AnalyzeLiveness(ctx context.Context, video, reference []byte, cfg LivenessConfig) (LivenessResult, error) {
	if err := ctx.Err(); err != nil {
		return LivenessResult{}, fmt.Errorf("%w: %v", ErrFaceCompareUnavailable, err)
	}
	if len(video) == 0 {
		return LivenessResult{}, ErrLivenessVideoUnreadable
	}
	if len(reference) == 0 {
		return LivenessResult{}, fmt.Errorf("%w: empty reference", ErrFaceCompareUnavailable)
	}
	cfg = cfg.normalized()
	m, ok := parseMarker(video, MockLivenessMarker)
	if !ok {
		m = map[string]string{
			"blinks":  strconv.Itoa(cfg.RequiredBlinks),
			"faces":   "1",
			"subject": MockFaceUploaderSubject,
		}
	}
	res := LivenessResult{DurationMs: markerInt(m, "duration_ms", 3000)}
	if res.DurationMs > cfg.LongestAcceptedMs() {
		res.Reason = LivenessReasonVideoTooLong
		return res, nil
	}
	res.FramesAnalysed = res.DurationMs * cfg.SampleFPS / 1000
	if res.FramesAnalysed > cfg.MaxFrames {
		res.FramesAnalysed = cfg.MaxFrames
	}
	if r := faceCountReason(markerInt(m, "faces", 0)); r != "" {
		res.Reason = r
		return res, nil
	}
	res.SingleFace = true
	if m["changed"] == "1" {
		res.Reason = LivenessReasonFaceChanged
		return res, nil
	}
	res.SameFaceAcrossFrames = true
	res.BlinksDetected = markerInt(m, "blinks", 0)
	if res.BlinksDetected < cfg.RequiredBlinks {
		res.Reason = LivenessReasonNotEnoughBlinks
		return res, nil
	}
	ref := parseMockFace(reference)
	if r := faceCountReason(ref.faces); r != "" {
		res.Reason = r
		return res, nil
	}
	switch {
	case m["subject"] == "" || m["subject"] != ref.subject:
		res.Similarity = 10
	default:
		res.Similarity = 99
		if f, err := strconv.ParseFloat(m["similarity"], 64); err == nil && f >= 0 && f <= 100 {
			res.Similarity = f
		}
	}
	return res, nil
}
