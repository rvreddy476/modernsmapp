package processing

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rekognition"
	rektypes "github.com/aws/aws-sdk-go-v2/service/rekognition/types"
)

// Lane D6 — scanners keep every label, and the local mock is deterministic.

func TestMockModerationScanner_Deterministic(t *testing.T) {
	m := NewMockModerationScanner()
	ctx := context.Background()

	clean, err := m.ScanImage(ctx, []byte("plain photo bytes"))
	if err != nil || !clean.IsSafe || clean.Labels == nil || len(clean.Labels) != 0 {
		t.Fatalf("no marker = %+v, %v; want safe with an empty label list", clean, err)
	}

	borderline, err := m.ScanImage(ctx, []byte("x "+MockModerationMarker+"labels=Swimwear_or_Underwear@91.5;Suggestive>Revealing_Clothes@84\n"))
	if err != nil || !borderline.IsSafe || len(borderline.Labels) != 2 {
		t.Fatalf("borderline = %+v, %v; want safe with two labels", borderline, err)
	}
	if l := borderline.Labels[1]; l.Name != "Revealing Clothes" || l.Parent != "Suggestive" || l.Confidence != 84 {
		t.Fatalf("parsed label = %+v", l)
	}

	explicit, err := m.ScanImage(ctx, []byte(MockModerationMarker+"labels=Explicit_Nudity>Nudity@97\n"))
	if err != nil || explicit.IsSafe || explicit.Reason != "explicit_nudity" || len(explicit.Labels) != 1 {
		t.Fatalf("explicit = %+v, %v; want unsafe explicit_nudity", explicit, err)
	}

	if _, err := m.ScanImage(ctx, []byte(MockModerationMarker+"unavailable\n")); !errors.Is(err, ErrScanUnavailable) {
		t.Fatalf("unavailable marker: err=%v", err)
	}
	if ScannerName(m) != "mock" || ScannerName(&StubScanner{}) != "stub" {
		t.Fatalf("names = %q, %q", ScannerName(m), ScannerName(&StubScanner{}))
	}
}

func TestRekognitionScanner_KeepsUnblockedLabels(t *testing.T) {
	s := newRekognitionScannerWithAPI(fakeRekognition{out: &rekognition.DetectModerationLabelsOutput{
		ModerationLabels: []rektypes.ModerationLabel{
			{Name: aws.String("Swimwear or Underwear"), Confidence: aws.Float32(92)},
			{Name: aws.String("Revealing Clothes"), ParentName: aws.String("Suggestive"), Confidence: aws.Float32(85)},
		},
	}}, RekognitionConfig{})
	res, err := s.ScanImage(context.Background(), []byte("img"))
	if err != nil || !res.IsSafe {
		t.Fatalf("scan = %+v, %v; want safe", res, err)
	}
	if len(res.Labels) != 2 || res.Labels[0].Name != "Swimwear or Underwear" || res.Labels[1].Parent != "Suggestive" || res.Labels[1].Confidence != 85 {
		t.Fatalf("labels = %+v; want both findings kept", res.Labels)
	}
	if ScannerName(s) != "rekognition" {
		t.Fatalf("name = %q", ScannerName(s))
	}
}
