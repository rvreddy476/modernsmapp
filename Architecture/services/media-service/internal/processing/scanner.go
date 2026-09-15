package processing

import (
	"context"
	"fmt"
	"os"
)

// Scanner is the interface for content safety scanning.
// In production, replace StubScanner with a real implementation
// (e.g., Google SafeSearch API, PhotoDNA, or AWS Rekognition).
type Scanner interface {
	ScanImage(ctx context.Context, data []byte) (ScanResult, error)
}

// ScanResult holds the outcome of a content scan.
type ScanResult struct {
	IsSafe bool
	Reason string  // "csam", "violence", "nsfw", or "" when safe
	Score  float64 // 0.0 (safe) to 1.0 (unsafe)
	// Labels is every finding the provider returned, whether or not it
	// failed the asset (dating lane D6 sends borderline ones to review).
	Labels []ModerationLabel
}

// ModerationLabel is one provider finding. Parent is the top-level category
// when the provider reports one ("Explicit Nudity" for "Nudity").
type ModerationLabel struct {
	Name       string  `json:"name"`
	Parent     string  `json:"parent,omitempty"`
	Confidence float64 `json:"confidence"`
}

// NamedScanner is implemented by scanners that report a provider name,
// recorded next to the labels (media_assets.moderation_scanner).
type NamedScanner interface {
	Name() string
}

// ScannerName returns the provider name recorded with a scan.
func ScannerName(s Scanner) string {
	if n, ok := s.(NamedScanner); ok {
		return n.Name()
	}
	return "unknown"
}

// StubScanner is a no-op implementation that always returns safe.
// Replace this with a real scanner before enabling ScannerEnabled in production.
type StubScanner struct{}

// Name implements NamedScanner.
func (s *StubScanner) Name() string { return "stub" }

func (s *StubScanner) ScanImage(_ context.Context, _ []byte) (ScanResult, error) {
	return ScanResult{IsSafe: true, Reason: "", Score: 0.0}, nil
}

// ScanVideoFrames runs each extracted frame through the scanner and
// returns the first unsafe verdict — a single unsafe frame fails the
// whole video. With StubScanner every frame is safe; a real Scanner
// implementation (PhotoDNA / Rekognition / SafeSearch) makes this a
// genuine content-safety gate.
func ScanVideoFrames(ctx context.Context, scanner Scanner, framePaths []string) (ScanResult, error) {
	for _, p := range framePaths {
		data, err := os.ReadFile(p)
		if err != nil {
			return ScanResult{}, fmt.Errorf("read frame %s: %w", p, err)
		}
		res, err := scanner.ScanImage(ctx, data)
		if err != nil {
			return ScanResult{}, err
		}
		if !res.IsSafe {
			return res, nil
		}
	}
	return ScanResult{IsSafe: true}, nil
}
