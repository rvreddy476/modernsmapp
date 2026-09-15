package processing

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Lane D5 — face comparer matrix. The negative property matters most: an
// image the mock was not told about must never match, and a provider
// non-answer must never be reported as a verdict.

func mockImg(marker string) []byte {
	return append([]byte("\xff\xd8\xff\xe0 jpeg-ish header "), []byte(marker+"\n trailing")...)
}

func TestMockFaceComparer_ArbitraryImagesNeverMatch(t *testing.T) {
	m := NewMockFaceComparer()
	res, err := m.CompareFaces(context.Background(), []byte("a real photo"), []byte("another real photo"))
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if res.Reason != FaceReasonNoFace || res.Similarity != 0 || res.FaceCountSource != 0 {
		t.Fatalf("arbitrary images = %+v; want NO_FACE with similarity 0", res)
	}
	// A marked selfie against an unmarked photo still has no target face.
	res, _ = m.CompareFaces(context.Background(), mockImg(MockFaceMarker+"faces=1:subject=asha"), []byte("unmarked"))
	if res.Reason != FaceReasonNoFace || res.FaceCountSource != 1 || res.FaceCountTarget != 0 {
		t.Fatalf("marked vs unmarked = %+v; want NO_FACE on the target", res)
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
