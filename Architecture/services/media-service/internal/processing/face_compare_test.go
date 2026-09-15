package processing

import (
	"bytes"
	"context"
	"errors"
	"image/jpeg"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Lane D5 — face comparer matrix. A provider non-answer must never be
// reported as a verdict. The local mock's dev rule: unmarked media is the
// uploader's one face; explicit markers still force every failure.

func mockImg(marker string) []byte {
	return append([]byte("\xff\xd8\xff\xe0 jpeg-ish header "), []byte(marker+"\n trailing")...)
}

func TestMockFaceComparer_UnmarkedIsTheUploadersFace(t *testing.T) {
	m := NewMockFaceComparer()
	ctx := context.Background()
	res, err := m.CompareFaces(ctx, []byte("a real selfie"), []byte("a real photo"))
	if err != nil || res.Reason != "" || res.Similarity != 99 || res.FaceCountSource != 1 || res.FaceCountTarget != 1 {
		t.Fatalf("unmarked vs unmarked = %+v, %v; want one face each, similarity 99", res, err)
	}
	unmarked := []byte("unmarked")
	cases := []struct {
		name       string
		src, tgt   []byte
		wantReason string
		wantSim    float64
	}{
		{"different face marker vs unmarked", mockImg(MockFaceMarker + "faces=1:subject=someone-else"), unmarked, "", 10},
		{"unmarked vs different face marker", unmarked, mockImg(MockFaceMarker + "faces=1:subject=someone-else"), "", 10},
		{"no face marker", mockImg(MockFaceMarker + "faces=0"), unmarked, FaceReasonNoFace, 0},
		{"group photo marker", unmarked, mockImg(MockFaceMarker + "faces=2"), FaceReasonMultipleFaces, 0},
		{"marker without subject", mockImg(MockFaceMarker + "faces=1"), unmarked, "", 10},
		{"marker naming the uploader", mockImg(MockFaceMarker + "faces=1:subject=" + MockFaceUploaderSubject), unmarked, "", 99},
	}
	for _, tc := range cases {
		res, err := m.CompareFaces(ctx, tc.src, tc.tgt)
		if err != nil || res.Reason != tc.wantReason || res.Similarity != tc.wantSim {
			t.Fatalf("%s: got %+v, %v; want reason %q similarity %v", tc.name, res, err, tc.wantReason, tc.wantSim)
		}
	}
	for marker, want := range map[string]int{"": 1, "faces=0": 0, "faces=2": 2} {
		img := unmarked
		if marker != "" {
			img = mockImg(MockFaceMarker + marker)
		}
		if n, err := m.CountFaces(ctx, img); err != nil || n != want {
			t.Fatalf("count %q = %d, %v; want %d", marker, n, err, want)
		}
	}
	if _, err := m.CountFaces(ctx, nil); !errors.Is(err, ErrFaceCompareUnavailable) {
		t.Fatalf("count empty: %v; want unavailable", err)
	}
}

// Prepare re-encodes the photo and drops the marker; the mock's stamp puts a
// rebuilt marker back after EOI so the decision survives, and nothing else.
func TestMockFaceComparer_StampCarriesMarkerThroughPrepare(t *testing.T) {
	m := NewMockFaceComparer()
	ctx := context.Background()
	prepare := func(comment string) ([]byte, *DatingImage) {
		in := testJPEGWithEXIF(t, 1200, 600, 6, comment)
		out, err := PrepareDatingImage(in)
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		if bytes.Contains(out.Original.Bytes, []byte("ATPOST-")) {
			t.Fatal("re-encode kept the marker; the test would prove nothing")
		}
		return in, out
	}
	splitEOI := func(data []byte) ([]byte, string) {
		i := bytes.LastIndex(data, []byte{0xFF, 0xD9})
		if i < 0 {
			t.Fatal("no EOI")
		}
		return data[:i+2], string(data[i+2:])
	}

	selfie := mockImg(MockFaceMarker + "faces=1:subject=asha")
	for _, tc := range []struct {
		comment, wantTrailer, wantReason string
		wantSim                          float64
	}{
		{MockFaceMarker + "faces=1:subject=asha\n", "\n" + MockFaceMarker + "faces=1:subject=asha\n", "", 99},
		{MockFaceMarker + "faces=1:subject=someone-else\n", "\n" + MockFaceMarker + "faces=1:subject=someone-else\n", "", 10},
		{MockFaceMarker + "faces=0\n", "\n" + MockFaceMarker + "faces=0\n", FaceReasonNoFace, 0},
		// Only parsed, sanitised fields are written back.
		{MockFaceMarker + "faces=1:subject=a<GPS>/b:similarity=85\n", "\n" + MockFaceMarker + "faces=1:subject=aGPSb:similarity=85\n", "", 10},
	} {
		in, out := prepare(tc.comment)
		m.StampPreparedDatingImage(in, out)
		for _, r := range append([]RenderedImage{out.Original}, out.Variants...) {
			img, trailer := splitEOI(r.Bytes)
			if JPEGHasMetadata(r.Bytes) || bytes.Contains(r.Bytes, []byte(gpsSecret)) || bytes.Contains(r.Bytes, []byte("Exif")) ||
				bytes.Contains(img, []byte("ATPOST-")) {
				t.Fatalf("%q: stamped %s carries metadata, GPS or a marker inside the image", tc.comment, r.Name)
			}
			if trailer != tc.wantTrailer {
				t.Fatalf("%q: %s trailer = %q, want %q", tc.comment, r.Name, trailer, tc.wantTrailer)
			}
			if _, err := jpeg.Decode(bytes.NewReader(r.Bytes)); err != nil {
				t.Fatalf("%q: stamped %s does not decode: %v", tc.comment, r.Name, err)
			}
		}
		assertServable(t, DatingBlurVariant, out.Blurred)
		res, err := m.CompareFaces(ctx, selfie, out.Original.Bytes)
		if err != nil || res.Reason != tc.wantReason || res.Similarity != tc.wantSim {
			t.Fatalf("%q: selfie vs prepared = %+v, %v; want reason %q similarity %v", tc.comment, res, err, tc.wantReason, tc.wantSim)
		}
	}

	// No marker in the upload: the stamp changes nothing.
	in, out := prepare("")
	before := append([]byte(nil), out.Original.Bytes...)
	m.StampPreparedDatingImage(in, out)
	if !bytes.Equal(before, out.Original.Bytes) {
		t.Fatal("stamp changed an unmarked prepared image")
	}
	m.StampPreparedDatingImage(in, nil)
}

// Only the mock stamps: the production comparer is a FaceCounter, never a
// DatingImageStamper.
func TestRekognitionFaceComparer_IsNotAStamper(t *testing.T) {
	var rek FaceCounter = newRekognitionFaceComparerWithAPI(&fakeFaceAPI{})
	if _, ok := rek.(DatingImageStamper); ok {
		t.Fatal("RekognitionFaceComparer implements DatingImageStamper")
	}
	var mock FaceCounter = NewMockFaceComparer()
	if _, ok := mock.(DatingImageStamper); !ok {
		t.Fatal("MockFaceComparer no longer implements DatingImageStamper")
	}
}

func TestMockFaceComparer_Deterministic(t *testing.T) {
	m := NewMockFaceComparer()
	ctx := context.Background()
	cases := []struct {
		name       string
		src, tgt   string
		wantReason string
		wantSim    float64
	}{
		{"same subject", "faces=1:subject=asha", "faces=1:subject=asha", "", 99},
		{"different subject", "faces=1:subject=asha", "faces=1:subject=ravi", "", 10},
		{"similarity override", "faces=1:subject=asha:similarity=85", "faces=1:subject=asha", "", 85},
		{"override ignored across subjects", "faces=1:subject=asha:similarity=95", "faces=1:subject=ravi", "", 10},
		{"no source face", "faces=0:subject=asha", "faces=1:subject=asha", FaceReasonNoFace, 0},
		{"two source faces", "faces=2:subject=asha", "faces=1:subject=asha", FaceReasonMultipleFaces, 0},
		{"two target faces", "faces=1:subject=asha", "faces=3:subject=asha", FaceReasonMultipleFaces, 0},
	}
	for _, tc := range cases {
		res, err := m.CompareFaces(ctx, mockImg(MockFaceMarker+tc.src), mockImg(MockFaceMarker+tc.tgt))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if res.Reason != tc.wantReason || res.Similarity != tc.wantSim {
			t.Fatalf("%s: got %+v; want reason %q similarity %v", tc.name, res, tc.wantReason, tc.wantSim)
		}
	}
	if _, err := m.CompareFaces(ctx, nil, mockImg(MockFaceMarker+"faces=1:subject=a")); !errors.Is(err, ErrFaceCompareUnavailable) {
		t.Fatalf("empty source: err=%v, want unavailable", err)
	}
}

type fakeFaceAPI struct {
	detect      map[int]*rekognition.DetectFacesOutput // by call index
	detectErr   error
	compare     *rekognition.CompareFacesOutput
	compareErr  error
	detectCalls int
	compares    int
}

func (f *fakeFaceAPI) DetectFaces(context.Context, *rekognition.DetectFacesInput, ...func(*rekognition.Options)) (*rekognition.DetectFacesOutput, error) {
	i := f.detectCalls
	f.detectCalls++
	if f.detectErr != nil {
		return nil, f.detectErr
	}
	return f.detect[i], nil
}

func (f *fakeFaceAPI) CompareFaces(context.Context, *rekognition.CompareFacesInput, ...func(*rekognition.Options)) (*rekognition.CompareFacesOutput, error) {
	f.compares++
	return f.compare, f.compareErr
}

func faces(n int) *rekognition.DetectFacesOutput {
	return &rekognition.DetectFacesOutput{FaceDetails: make([]rektypes.FaceDetail, n)}
}

func TestRekognitionFaceComparer_FaceCounts(t *testing.T) {
	ctx := context.Background()
	img := []byte("image")

	// Source has no face: target counted with a second DetectFaces, no compare.
	api := &fakeFaceAPI{detect: map[int]*rekognition.DetectFacesOutput{0: faces(0), 1: faces(1)}}
	res, err := newRekognitionFaceComparerWithAPI(api).CompareFaces(ctx, img, img)
	if err != nil || res.Reason != FaceReasonNoFace || res.FaceCountSource != 0 || res.FaceCountTarget != 1 || api.compares != 0 {
		t.Fatalf("no source face: %+v err=%v compares=%d", res, err, api.compares)
	}
	// Source has two faces.
	api = &fakeFaceAPI{detect: map[int]*rekognition.DetectFacesOutput{0: faces(2), 1: faces(1)}}
	res, err = newRekognitionFaceComparerWithAPI(api).CompareFaces(ctx, img, img)
	if err != nil || res.Reason != FaceReasonMultipleFaces || res.FaceCountSource != 2 {
		t.Fatalf("two source faces: %+v err=%v", res, err)
	}
	// Target has two faces (one matched, one not).
	api = &fakeFaceAPI{
		detect: map[int]*rekognition.DetectFacesOutput{0: faces(1)},
		compare: &rekognition.CompareFacesOutput{
			FaceMatches:    []rektypes.CompareFacesMatch{{Similarity: aws.Float32(98)}},
			UnmatchedFaces: []rektypes.ComparedFace{{}},
		},
	}
	res, err = newRekognitionFaceComparerWithAPI(api).CompareFaces(ctx, img, img)
	if err != nil || res.Reason != FaceReasonMultipleFaces || res.FaceCountTarget != 2 || res.Similarity != 0 {
		t.Fatalf("two target faces: %+v err=%v", res, err)
	}
	// Target has no face.
	api = &fakeFaceAPI{detect: map[int]*rekognition.DetectFacesOutput{0: faces(1)}, compare: &rekognition.CompareFacesOutput{}}
	res, err = newRekognitionFaceComparerWithAPI(api).CompareFaces(ctx, img, img)
	if err != nil || res.Reason != FaceReasonNoFace || res.FaceCountTarget != 0 {
		t.Fatalf("no target face: %+v err=%v", res, err)
	}
	// One face each: similarity passes through.
	api = &fakeFaceAPI{
		detect:  map[int]*rekognition.DetectFacesOutput{0: faces(1)},
		compare: &rekognition.CompareFacesOutput{FaceMatches: []rektypes.CompareFacesMatch{{Similarity: aws.Float32(91.5)}}},
	}
	res, err = newRekognitionFaceComparerWithAPI(api).CompareFaces(ctx, img, img)
	if err != nil || res.Reason != "" || res.Similarity != 91.5 || res.FaceCountSource != 1 || res.FaceCountTarget != 1 {
		t.Fatalf("one face each: %+v err=%v", res, err)
	}
}

func TestRekognitionFaceComparer_ProviderFailuresAreUnavailable(t *testing.T) {
	ctx := context.Background()
	img := []byte("image")
	cases := map[string]*fakeFaceAPI{
		"detect throttled":  {detectErr: errors.New("ThrottlingException")},
		"detect nil":        {detect: map[int]*rekognition.DetectFacesOutput{}},
		"compare error":     {detect: map[int]*rekognition.DetectFacesOutput{0: faces(1)}, compareErr: errors.New("InvalidImageFormatException")},
		"compare nil":       {detect: map[int]*rekognition.DetectFacesOutput{0: faces(1)}},
		"target detect nil": {detect: map[int]*rekognition.DetectFacesOutput{0: faces(0)}},
	}
	for name, api := range cases {
		res, err := newRekognitionFaceComparerWithAPI(api).CompareFaces(ctx, img, img)
		if !errors.Is(err, ErrFaceCompareUnavailable) {
			t.Fatalf("%s: err=%v res=%+v; want ErrFaceCompareUnavailable", name, err, res)
		}
	}
	big := make([]byte, MaxFaceImageBytes+1)
	if _, err := newRekognitionFaceComparerWithAPI(&fakeFaceAPI{}).CompareFaces(ctx, big, img); !errors.Is(err, ErrFaceCompareUnavailable) {
		t.Fatalf("oversized image: err=%v", err)
	}
}
