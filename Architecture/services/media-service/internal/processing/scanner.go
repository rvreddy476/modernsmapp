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
}

// StubScanner is a no-op implementation that always returns safe.
// Replace this with a real scanner before enabling ScannerEnabled in production.
type StubScanner struct{}

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
