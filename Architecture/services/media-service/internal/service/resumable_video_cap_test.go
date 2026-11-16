package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// withVideoCap runs the test under a different cap (the env override), and
// restores the one read at start-up.
func withVideoCap(t *testing.T, capBytes int64) {
	t.Helper()
	old := videoOriginalCap
	videoOriginalCap = capBytes
	t.Cleanup(func() { videoOriginalCap = old })
}

// The default is the worker's real capability, read from the same
// constant and the same variable the worker reads.
func TestVideoUploadCapIsTheWorkersCap(t *testing.T) {
	unset := func(string) string { return "" }
	if got := processing.VideoOriginalCap(unset); got != 16<<30 {
		t.Fatalf("default cap %d, want 16 GiB (the worker's scratch-volume capability)", got)
	}
	set := func(k string) string {
		if k == processing.VideoOriginalCapEnv {
			return "6442450944"
		}
		return ""
	}
	if got := processing.VideoOriginalCap(set); got != 6<<30 {
		t.Fatalf("env override: cap %d, want 6 GiB", got)
	}
	if processing.VideoOriginalCapEnv != "MEDIA_WORKER_MAX_ORIGINAL_BYTES" {
		t.Fatalf("cap variable renamed to %s: deploy values set MEDIA_WORKER_MAX_ORIGINAL_BYTES", processing.VideoOriginalCapEnv)
	}
}

// A resumable session opened under a larger cap is refused at the next
// chunk and at complete, not after the transcode fails: by its declared
// size, and by the bytes received.
func TestResumableVideoBytesRefusedPastTheCap(t *testing.T) {
	withVideoCap(t, 100<<20)
	video := &postgres.ResumableUpload{MimeType: "video/mp4", TotalBytes: 90 << 20}

	if err := checkResumableVideoBytes(video, 90<<20); err != nil {
		t.Fatalf("a video under the cap was refused: %v", err)
	}
	// Bytes received past the cap (a session whose parts outgrew it).
	if err := checkResumableVideoBytes(video, 100<<20+1); !errors.Is(err, ErrVideoTooLarge) {
		t.Fatalf("received bytes past the cap: err = %v, want ErrVideoTooLarge", err)
	}
	// A session that declared more than today's cap (opened before it was
	// lowered) is refused at its first chunk.
	big := &postgres.ResumableUpload{MimeType: "VIDEO/QuickTime", TotalBytes: 200 << 20}
	if err := checkResumableVideoBytes(big, 5<<20); !errors.Is(err, ErrVideoTooLarge) {
		t.Fatalf("declared size past the cap: err = %v, want ErrVideoTooLarge", err)
	}
	// An image session is not the transcode worker's.
	img := &postgres.ResumableUpload{MimeType: "image/jpeg", TotalBytes: 200 << 20}
	if err := checkResumableVideoBytes(img, 200<<20); err != nil {
		t.Fatalf("an image session was held to the video cap: %v", err)
	}
	// 0 is "no cap", as the worker reads it.
	withVideoCap(t, 0)
	if err := checkResumableVideoBytes(big, 200<<20); err != nil {
		t.Fatalf("cap 0 refused a video: %v", err)
	}
}

// Init refuses a declared size over the cap before the limiter, the object
// store or the database are reached (all nil here).
func TestResumableInitRefusesVideoOverTheCap(t *testing.T) {
	withVideoCap(t, 100<<20)
	s := &Service{}
	_, err := s.InitResumableUpload(context.Background(), uuid.New(), "video", "video/mp4", 100<<20+1)
	if !errors.Is(err, ErrVideoTooLarge) {
		t.Fatalf("err = %v, want ErrVideoTooLarge", err)
	}
}

// Product rule: long videos have no length cap; the message must say the
// limit is a processing limit on the file, and name it.
func TestVideoTooLargeMessageIsAProcessingLimit(t *testing.T) {
	for capBytes, size := range map[int64]string{16 << 30: "16 GiB", 6 << 30: "6 GiB", 3 << 29: "1.5 GiB", 700 << 20: "700 MiB"} {
		msg := VideoTooLargeMessage(capBytes)
		for _, want := range []string{size, "processing", "no limit on video length"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("cap %d: message %q lacks %q", capBytes, msg, want)
			}
		}
	}
}
