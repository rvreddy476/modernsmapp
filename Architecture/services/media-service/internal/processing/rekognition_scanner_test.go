package processing

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Module 4 M4-P0-3 — the scanner failure matrix.
//
// THE PROPERTY UNDER TEST IS A NEGATIVE ONE: no path that failed to produce a
// verdict may report the content safe. That is the whole reason this component
// exists, and it is exactly what the old StubScanner violated by construction.
//
// Detection quality is not tested here — it belongs to AWS. What is tested is
// the boundary this codebase owns: how a non-answer is represented.

type fakeRekognition struct {
	out *rekognition.DetectModerationLabelsOutput
	err error
}

func (f fakeRekognition) DetectModerationLabels(context.Context,
	*rekognition.DetectModerationLabelsInput,
	...func(*rekognition.Options)) (*rekognition.DetectModerationLabelsOutput, error) {
	return f.out, f.err
}

func scannerWith(api rekognitionAPI) *RekognitionScanner {
	return newRekognitionScannerWithAPI(api, RekognitionConfig{})
}

// Every provider failure shape must be unavailable, and never safe.
func TestEveryProviderFailureIsUnavailableNotSafe(t *testing.T) {
	cases := map[string]rekognitionAPI{
		"throttled":         fakeRekognition{err: errors.New("ThrottlingException: rate exceeded")},
		"timeout":           fakeRekognition{err: context.DeadlineExceeded},
		"access denied":     fakeRekognition{err: errors.New("AccessDeniedException")},
		"unsupported":       fakeRekognition{err: errors.New("InvalidImageFormatException")},
		"transport error":   fakeRekognition{err: errors.New("dial tcp: lookup failed")},
		"nil result no err": fakeRekognition{out: nil},
	}
	for name, api := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := scannerWith(api).ScanImage(context.Background(), []byte("frame"))
			if err == nil {
				t.Fatalf("provider failure produced no error (result %+v). A scan that did "+
					"not happen must never be reported as a verdict.", res)
			}
			if !errors.Is(err, ErrScanUnavailable) {
				t.Errorf("error is not ErrScanUnavailable: %v — the caller cannot tell it "+
					"apart from a real unsafe verdict and may take the wrong action", err)
			}
			if res.IsSafe {
				t.Error("a failed scan returned IsSafe=true")
			}
		})
	}
}

// An empty frame is not evidence of safety.
func TestEmptyImageIsUnavailable(t *testing.T) {
	res, err := scannerWith(fakeRekognition{}).ScanImage(context.Background(), nil)
	if !errors.Is(err, ErrScanUnavailable) || res.IsSafe {
		t.Fatalf("empty image: err=%v result=%+v; want unavailable and not safe", err, res)
	}
}

// A nil scanner or unconfigured API must not pass content.
func TestUnconfiguredScannerIsUnavailable(t *testing.T) {
	var s *RekognitionScanner
	if _, err := s.ScanImage(context.Background(), []byte("x")); !errors.Is(err, ErrScanUnavailable) {
		t.Fatalf("nil scanner: %v, want ErrScanUnavailable", err)
	}
	if _, err := (&RekognitionScanner{}).ScanImage(context.Background(), []byte("x")); !errors.Is(err, ErrScanUnavailable) {
		t.Fatalf("scanner with no api: %v, want ErrScanUnavailable", err)
	}
}

// A successful call with no findings IS a verdict — this is the one path that
// may return safe, and it must still work or nothing would ever publish.
func TestCleanImageIsSafe(t *testing.T) {
	api := fakeRekognition{out: &rekognition.DetectModerationLabelsOutput{}}
	res, err := scannerWith(api).ScanImage(context.Background(), []byte("frame"))
	if err != nil {
		t.Fatalf("clean image errored: %v", err)
	}
	if !res.IsSafe {
		t.Fatal("a clean image was not reported safe; nothing would ever be publishable")
	}
}

func TestBlockedLabelFailsTheImage(t *testing.T) {
	api := fakeRekognition{out: &rekognition.DetectModerationLabelsOutput{
		ModerationLabels: []rektypes.ModerationLabel{{
			Name:       aws.String("Graphic Violence"),
			ParentName: aws.String("Violence"),
			Confidence: aws.Float32(96.5),
		}},
	}}
	res, err := scannerWith(api).ScanImage(context.Background(), []byte("frame"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsSafe {
		t.Fatal("an image labelled Violence was reported safe")
	}
	if res.Reason != "violence" {
		t.Errorf("reason = %q, want violence", res.Reason)
	}
	if res.Score < 0.9 {
		t.Errorf("score = %v, want the provider confidence carried through", res.Score)
	}
}

// A category outside the blocked set must not fail the asset. Blocking legal
// content produces false positives that operators learn to ignore, which
// degrades the whole gate.
func TestUnblockedCategoryDoesNotFailTheImage(t *testing.T) {
	api := fakeRekognition{out: &rekognition.DetectModerationLabelsOutput{
		ModerationLabels: []rektypes.ModerationLabel{{
			Name:       aws.String("Beer"),
			ParentName: aws.String("Alcohol"),
			Confidence: aws.Float32(99),
		}},
	}}
	res, err := scannerWith(api).ScanImage(context.Background(), []byte("frame"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsSafe {
		t.Fatal("an Alcohol label failed the image; only the blocked categories should")
	}
}

// The worst blocked finding wins, so a high-confidence violation is not masked
// by a lower-confidence one appearing first.
func TestWorstBlockedFindingWins(t *testing.T) {
	api := fakeRekognition{out: &rekognition.DetectModerationLabelsOutput{
		ModerationLabels: []rektypes.ModerationLabel{
			{ParentName: aws.String("Violence"), Confidence: aws.Float32(81)},
			{ParentName: aws.String("Explicit Nudity"), Confidence: aws.Float32(99)},
		},
	}}
	res, err := scannerWith(api).ScanImage(context.Background(), []byte("frame"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Reason != "explicit_nudity" {
		t.Errorf("reason = %q, want the highest-confidence blocked label", res.Reason)
	}
}
