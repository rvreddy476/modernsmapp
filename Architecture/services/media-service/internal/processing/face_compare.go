package processing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Dating plan lane D5 — server-side face comparison for selfie verification.
//
// A FaceComparer answers one question about two images the caller already
// owns: does each contain exactly one face, and how similar are those faces
// (0-100)? It never returns, stores or logs a face embedding or image bytes.
//
// THE ONE INVARIANT
//
// ErrFaceCompareUnavailable is returned for every path that is not a real
// answer from the provider (throttling, timeout, transport failure, an empty
// image, a malformed response). It is never reported as "no match" and never
// as "match": the caller refuses the attempt and asks the user to retry.

// Reason codes for a comparison that ran but cannot be a match.
const (
	FaceReasonNoFace        = "NO_FACE"
	FaceReasonMultipleFaces = "MULTIPLE_FACES"
)

// MaxFaceImageBytes is Rekognition's limit for images passed as bytes.
const MaxFaceImageBytes = 5 * 1024 * 1024

// ErrFaceCompareUnavailable means no comparison result could be produced.
var ErrFaceCompareUnavailable = errors.New("face comparison unavailable")

// FaceCompareResult is a provider answer. Similarity is 0-100 and is 0 when
// Reason is set. Face counts are always both evaluated.
type FaceCompareResult struct {
	Similarity      float64
	FaceCountSource int
	FaceCountTarget int
	Reason          string
}

// FaceComparer compares the single face in source (the selfie) with the
// single face in target (the primary profile photo).
type FaceComparer interface {
	CompareFaces(ctx context.Context, source, target []byte) (FaceCompareResult, error)
	// Name is the provider label recorded with the verification outcome.
	Name() string
}

// faceCountReason returns the reason code for a face count that is not 1.
func faceCountReason(n int) string {
	switch {
	case n == 0:
		return FaceReasonNoFace
	case n > 1:
		return FaceReasonMultipleFaces
	}
	return ""
}

// ── Mock (local/dev and tests only) ─────────────────────────────────────────

// MockFaceMarker is the prefix of the test marker the mock looks for inside
// image bytes. Format (ASCII, anywhere in the file):
//
//	ATPOST-FACE-TEST:v1:faces=<n>:subject=<id>[:similarity=<0-100>]
//
// The mock is deterministic. Its dev rule:
//   - an image WITHOUT the marker holds exactly one face whose subject is
//     MockFaceUploaderSubject. Every caller compares media owned by one
//     requester (FaceCompareService checks ownership first), so that constant
//     is in effect the uploader's own stable identity: a phone photo and a
//     phone selfie video from the same account match, with no marker;
//   - faces=0 / faces>1 produce NO_FACE / MULTIPLE_FACES (the "no face" and
//     "group photo" test markers);
//   - two single-face images match (similarity 99, or the source marker's
//     similarity=) only when their subject ids are equal; different
//     subjects score 10 (the "different face" test marker is any other
//     subject=, e.g. subject=someone-else). A marker without subject= never
//     matches.
//
// Dating photo prepare re-encodes the image, which drops the marker; the
// mock carries it onto the prepared renditions (StampPreparedDatingImage).
//
// It is refused outside ENV local/dev/development (ResolveFaceCompareSettings).
const MockFaceMarker = "ATPOST-FACE-TEST:v1:"

// MockFaceUploaderSubject is the subject of an image or video that carries no
// test marker. A marker may name it to match unmarked media.
const MockFaceUploaderSubject = "uploader"

// MockFaceComparer is the local/dev FaceComparer. See MockFaceMarker.
type MockFaceComparer struct{}

// NewMockFaceComparer returns the deterministic local/dev comparer.
func NewMockFaceComparer() *MockFaceComparer { return &MockFaceComparer{} }

// Name implements FaceComparer.
func (*MockFaceComparer) Name() string { return "mock" }

type mockFace struct {
	faces      int
	subject    string
	similarity float64 // < 0 when absent
}

// parseMockFace applies the dev rule: no marker is the uploader's one face.
func parseMockFace(img []byte) mockFace {
	if f, ok := findMockFace(img); ok {
		return f
	}
	return mockFace{faces: 1, subject: MockFaceUploaderSubject, similarity: -1}
}

// findMockFace reads the explicit marker; ok is false when there is none.
func findMockFace(img []byte) (mockFace, bool) {
	out := mockFace{similarity: -1}
	i := bytes.Index(img, []byte(MockFaceMarker))
	if i < 0 {
		return out, false
	}
	rest := img[i+len(MockFaceMarker):]
	if end := bytes.IndexAny(rest, "\x00\r\n \t"); end >= 0 {
		rest = rest[:end]
	}
	for _, part := range strings.Split(string(rest), ":") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "faces":
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				out.faces = n
			}
		case "subject":
			out.subject = v
		case "similarity":
			if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 && f <= 100 {
				out.similarity = f
			}
		}
	}
	return out, true
}

// maxMockSubjectLen bounds a carried subject id.
const maxMockSubjectLen = 64

// line rebuilds a canonical marker from the parsed fields. Nothing is copied
// from the upload: the subject keeps only [A-Za-z0-9._@+-], so the line can
// never hold EXIF, GPS or other uploaded bytes.
func (f mockFace) line() []byte {
	var b strings.Builder
	b.WriteString("\n" + MockFaceMarker + "faces=" + strconv.Itoa(f.faces))
	if subject := sanitizeMockSubject(f.subject); subject != "" {
		b.WriteString(":subject=" + subject)
	}
	if f.similarity >= 0 {
		b.WriteString(":similarity=" + strconv.FormatFloat(f.similarity, 'f', -1, 64))
	}
	b.WriteString("\n")
	return []byte(b.String())
}

func sanitizeMockSubject(s string) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= maxMockSubjectLen {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '@', r == '+', r == '-':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// StampPreparedDatingImage implements DatingImageStamper, for the mock only.
//
// PrepareDatingImage re-encodes the upload, which removes an explicit face
// test marker; without it a "different face" or "no face" test photo would
// fall back to the uploader's face after prepare and the negative tests would
// pass by accident, while a seeded subject would stop matching its video.
// When the upload carried a marker, the canonical line (mockFace.line) is
// appended after the JPEG's end-of-image marker on the original and on every
// rendition the face routes can read. Bytes after EOI are not an APPn or COM
// segment: decoders ignore them, JPEGHasMetadata still reports false, and the
// line holds only the parsed fields. The blurred variant is never stamped. An
// upload without a marker is left byte-for-byte as prepared.
func (*MockFaceComparer) StampPreparedDatingImage(uploaded []byte, img *DatingImage) {
	if img == nil {
		return
	}
	f, ok := findMockFace(uploaded)
	if !ok {
		return
	}
	line := f.line()
	stamp := func(r *RenderedImage) {
		r.Bytes = append(append(make([]byte, 0, len(r.Bytes)+len(line)), r.Bytes...), line...)
	}
	stamp(&img.Original)
	for i := range img.Variants {
		stamp(&img.Variants[i])
	}
}

// CompareFaces implements FaceComparer.
func (*MockFaceComparer) CompareFaces(ctx context.Context, source, target []byte) (FaceCompareResult, error) {
	if err := ctx.Err(); err != nil {
		return FaceCompareResult{}, fmt.Errorf("%w: %v", ErrFaceCompareUnavailable, err)
	}
	if len(source) == 0 || len(target) == 0 {
		return FaceCompareResult{}, fmt.Errorf("%w: empty image", ErrFaceCompareUnavailable)
	}
	src, tgt := parseMockFace(source), parseMockFace(target)
	res := FaceCompareResult{FaceCountSource: src.faces, FaceCountTarget: tgt.faces}
	if r := faceCountReason(src.faces); r != "" {
		res.Reason = r
		return res, nil
	}
	if r := faceCountReason(tgt.faces); r != "" {
		res.Reason = r
		return res, nil
	}
	switch {
	case src.subject == "" || src.subject != tgt.subject:
		res.Similarity = 10
	case src.similarity >= 0:
		res.Similarity = src.similarity
	default:
		res.Similarity = 99
	}
	return res, nil
}

// ── AWS Rekognition ─────────────────────────────────────────────────────────

// rekognitionFaceAPI is the two calls the comparer needs, as an interface so
// the face-count matrix is testable without AWS.
type rekognitionFaceAPI interface {
	DetectFaces(ctx context.Context, in *rekognition.DetectFacesInput,
		optFns ...func(*rekognition.Options)) (*rekognition.DetectFacesOutput, error)
	CompareFaces(ctx context.Context, in *rekognition.CompareFacesInput,
		optFns ...func(*rekognition.Options)) (*rekognition.CompareFacesOutput, error)
}

// RekognitionFaceComparer implements FaceComparer with DetectFaces (source
// face count) and CompareFaces (target face count + similarity). Every
// comparison is exactly two Rekognition calls.
type RekognitionFaceComparer struct {
	api rekognitionFaceAPI
}

// NewRekognitionFaceComparer builds the comparer on a client from
// NewRekognitionClient (the same loader and IRSA credential chain the content
// scanner uses).
func NewRekognitionFaceComparer(client *rekognition.Client) *RekognitionFaceComparer {
	return &RekognitionFaceComparer{api: client}
}

func newRekognitionFaceComparerWithAPI(api rekognitionFaceAPI) *RekognitionFaceComparer {
	return &RekognitionFaceComparer{api: api}
}

// Name implements FaceComparer.
func (*RekognitionFaceComparer) Name() string { return "rekognition" }

// CompareFaces implements FaceComparer.
func (c *RekognitionFaceComparer) CompareFaces(ctx context.Context, source, target []byte) (FaceCompareResult, error) {
	if c == nil || c.api == nil {
		return FaceCompareResult{}, fmt.Errorf("%w: comparer not configured", ErrFaceCompareUnavailable)
	}
	if len(source) == 0 || len(target) == 0 {
		return FaceCompareResult{}, fmt.Errorf("%w: empty image", ErrFaceCompareUnavailable)
	}
	if len(source) > MaxFaceImageBytes || len(target) > MaxFaceImageBytes {
		return FaceCompareResult{}, fmt.Errorf("%w: image exceeds %d bytes", ErrFaceCompareUnavailable, MaxFaceImageBytes)
	}

	detected, err := c.api.DetectFaces(ctx, &rekognition.DetectFacesInput{
		Image:      &rektypes.Image{Bytes: source},
		Attributes: []rektypes.Attribute{rektypes.AttributeDefault},
	})
	if err != nil {
		return FaceCompareResult{}, fmt.Errorf("%w: detect faces: %v", ErrFaceCompareUnavailable, err)
	}
	if detected == nil {
		return FaceCompareResult{}, fmt.Errorf("%w: detect faces returned no result", ErrFaceCompareUnavailable)
	}
	res := FaceCompareResult{FaceCountSource: len(detected.FaceDetails)}

	if reason := faceCountReason(res.FaceCountSource); reason != "" {
		// CompareFaces needs a face in the source, so count the target on
		// its own. Two calls either way.
		tgt, err := c.api.DetectFaces(ctx, &rekognition.DetectFacesInput{
			Image:      &rektypes.Image{Bytes: target},
			Attributes: []rektypes.Attribute{rektypes.AttributeDefault},
		})
		if err != nil {
			return FaceCompareResult{}, fmt.Errorf("%w: detect target faces: %v", ErrFaceCompareUnavailable, err)
		}
		if tgt == nil {
			return FaceCompareResult{}, fmt.Errorf("%w: detect target faces returned no result", ErrFaceCompareUnavailable)
		}
		res.FaceCountTarget = len(tgt.FaceDetails)
		res.Reason = reason
		return res, nil
	}

	// SimilarityThreshold 0 returns every target face in FaceMatches with
	// its score, so "no face above the bar" cannot be mistaken for "no
	// face". The pass/review bars are the caller's decision.
	cmp, err := c.api.CompareFaces(ctx, &rekognition.CompareFacesInput{
		SourceImage:         &rektypes.Image{Bytes: source},
		TargetImage:         &rektypes.Image{Bytes: target},
		SimilarityThreshold: aws.Float32(0),
	})
	if err != nil {
		return FaceCompareResult{}, fmt.Errorf("%w: compare faces: %v", ErrFaceCompareUnavailable, err)
	}
	if cmp == nil {
		return FaceCompareResult{}, fmt.Errorf("%w: compare faces returned no result", ErrFaceCompareUnavailable)
	}
	res.FaceCountTarget = len(cmp.FaceMatches) + len(cmp.UnmatchedFaces)
	if reason := faceCountReason(res.FaceCountTarget); reason != "" {
		res.Reason = reason
		return res, nil
	}
	if len(cmp.FaceMatches) == 1 && cmp.FaceMatches[0].Similarity != nil {
		s := float64(*cmp.FaceMatches[0].Similarity)
		if s < 0 {
			s = 0
		}
		if s > 100 {
			s = 100
		}
		res.Similarity = s
	}
	return res, nil
}

// FaceCounter counts the faces in one image (lane D6: a primary dating photo
// with no face goes to review). Every non-answer is ErrFaceCompareUnavailable.
type FaceCounter interface {
	CountFaces(ctx context.Context, image []byte) (int, error)
}

// CountFaces implements FaceCounter with the mock's dev rule: an image
// without the test marker holds one face (the uploader's), so a real phone
// photo uploaded locally is not sent to review for "no face"; faces=0 in a
// marker still reports 0.
func (*MockFaceComparer) CountFaces(ctx context.Context, img []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, fmt.Errorf("%w: %v", ErrFaceCompareUnavailable, err)
	}
	if len(img) == 0 {
		return 0, fmt.Errorf("%w: empty image", ErrFaceCompareUnavailable)
	}
	return parseMockFace(img).faces, nil
}

// CountFaces implements FaceCounter with one DetectFaces call.
func (c *RekognitionFaceComparer) CountFaces(ctx context.Context, img []byte) (int, error) {
	if c == nil || c.api == nil {
		return 0, fmt.Errorf("%w: comparer not configured", ErrFaceCompareUnavailable)
	}
	if len(img) == 0 {
		return 0, fmt.Errorf("%w: empty image", ErrFaceCompareUnavailable)
	}
	if len(img) > MaxFaceImageBytes {
		return 0, fmt.Errorf("%w: image exceeds %d bytes", ErrFaceCompareUnavailable, MaxFaceImageBytes)
	}
	out, err := c.api.DetectFaces(ctx, &rekognition.DetectFacesInput{
		Image:      &rektypes.Image{Bytes: img},
		Attributes: []rektypes.Attribute{rektypes.AttributeDefault},
	})
	if err != nil {
		return 0, fmt.Errorf("%w: detect faces: %v", ErrFaceCompareUnavailable, err)
	}
	if out == nil {
		return 0, fmt.Errorf("%w: detect faces returned no result", ErrFaceCompareUnavailable)
	}
	return len(out.FaceDetails), nil
}
