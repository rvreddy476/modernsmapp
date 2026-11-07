package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/google/uuid"
)

// The upload routes refuse exactly what the worker refuses (2026-11-07):
// one cap, processing.MaxVideoOriginalBytes, from the same reader.
func TestVideoOriginalCapIsTheWorkersCap(t *testing.T) {
	if VideoOriginalCap() != processing.VideoOriginalCap(func(string) string { return "" }) {
		t.Fatalf("upload cap %d, worker default %d", VideoOriginalCap(), processing.MaxVideoOriginalBytes)
	}
	if processing.MaxVideoOriginalBytes != 16<<30 {
		t.Fatalf("MaxVideoOriginalBytes = %d, want 16 GiB", processing.MaxVideoOriginalBytes)
	}
}

func TestResumableInitRefusesAVideoOverTheCap(t *testing.T) {
	errPastSizeGate := errors.New("past the size gate")
	s := New(nil, nil)
	s.uploadLimit = func(context.Context, uuid.UUID) error { return errPastSizeGate }
	limit := VideoOriginalCap()

	_, err := s.InitResumableUpload(context.Background(), uuid.New(), "video", "video/mp4", limit+1)
	if !errors.Is(err, ErrVideoTooLarge) {
		t.Fatalf("cap + 1: err = %v, want ErrVideoTooLarge", err)
	}
	// At the cap the init goes on (to the upload limit, here a sentinel).
	if _, err := s.InitResumableUpload(context.Background(), uuid.New(), "video", "video/mp4", limit); !errors.Is(err, errPastSizeGate) {
		t.Fatalf("at the cap: err = %v, want it past the size gate", err)
	}
	// Images keep their own cap; the video cap does not apply to them.
	if _, err := s.InitResumableUpload(context.Background(), uuid.New(), "image", "image/jpeg", limit+1); errors.Is(err, ErrVideoTooLarge) {
		t.Fatalf("image refused with the video cap: %v", err)
	}
}

func TestSimpleInitRefusesAVideoOverTheCap(t *testing.T) {
	limit := VideoOriginalCap()
	if err := ValidateUploadSize("video", limit+1); !errors.Is(err, ErrVideoTooLarge) {
		t.Fatalf("cap + 1: err = %v, want ErrVideoTooLarge", err)
	}
	// Under the cap but over one PUT: switch routes, not a refusal.
	if err := ValidateUploadSize("video", limit); !errors.Is(err, ErrUseResumableUpload) {
		t.Fatalf("at the cap: err = %v, want ErrUseResumableUpload", err)
	}
	if err := ValidateUploadSize("video", MaxSimpleVideoUploadBytes); err != nil {
		t.Fatalf("5 GiB video refused: %v", err)
	}
}
