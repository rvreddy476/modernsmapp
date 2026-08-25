package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This structural guard prevents the exact regression where ConfirmUpload
// marks processing and then performs a best-effort direct Kafka publish.
func TestVideoConfirmUsesTransactionalOutboxOnly(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(here), "media.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	start := strings.Index(src, "func (s *Service) ConfirmUpload")
	if start < 0 {
		t.Fatal("could not inventory ConfirmUpload source")
	}
	end := strings.Index(src[start:], "func (s *Service) processImage")
	if end < 0 {
		t.Fatal("could not find end of ConfirmUpload source")
	}
	body := src[start : start+end]
	for _, forbidden := range []string{"PublishTranscodeRequested", "s.producer", `UpdateStatus(ctx, mediaID, "processing")`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("ConfirmUpload restored a lossy direct transcode path: %q", forbidden)
		}
	}
	if !strings.Contains(body, "QueueTranscode(ctx, media)") {
		t.Fatal("ConfirmUpload no longer uses the transactional transcode outbox")
	}
}
