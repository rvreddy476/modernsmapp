package processing

import (
	"context"
	"testing"
)

// TestStubScannerImplementsInterface is a compile-time assertion that *StubScanner
// satisfies the Scanner interface.
var _ Scanner = (*StubScanner)(nil)

func TestStubScannerImplementsInterface(t *testing.T) {
	// The var-level compile-time check above is the real guard.
	// This test body makes it appear in go test -v output.
	var s Scanner = &StubScanner{}
	if s == nil {
		t.Fatal("StubScanner does not satisfy Scanner interface")
	}
}

// TestStubScannerAlwaysSafe verifies that ScanImage always returns
// IsSafe=true, Score=0.0, and an empty Reason regardless of the input data.
func TestStubScannerAlwaysSafe(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"empty data", []byte{}},
		{"small data", []byte{0x00, 0x01, 0x02}},
		{"text data", []byte("not actually an image")},
		{"large data", make([]byte, 1024*1024)}, // 1 MiB of zeros
	}

	s := &StubScanner{}
	ctx := context.Background()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := s.ScanImage(ctx, tc.data)
			if err != nil {
				t.Fatalf("ScanImage returned unexpected error: %v", err)
			}
			if !result.IsSafe {
				t.Errorf("expected IsSafe=true, got false")
			}
			if result.Score != 0.0 {
				t.Errorf("expected Score=0.0, got %f", result.Score)
			}
			if result.Reason != "" {
				t.Errorf("expected empty Reason, got %q", result.Reason)
			}
		})
	}
}

// TestStubScannerNilData verifies that ScanImage does not panic when passed nil data.
func TestStubScannerNilData(t *testing.T) {
	s := &StubScanner{}
	ctx := context.Background()

	result, err := s.ScanImage(ctx, nil)
	if err != nil {
		t.Fatalf("ScanImage(ctx, nil) returned unexpected error: %v", err)
	}
	if !result.IsSafe {
		t.Errorf("expected IsSafe=true for nil data, got false")
	}
	if result.Score != 0.0 {
		t.Errorf("expected Score=0.0 for nil data, got %f", result.Score)
	}
	if result.Reason != "" {
		t.Errorf("expected empty Reason for nil data, got %q", result.Reason)
	}
}
