package processing

import "context"

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
