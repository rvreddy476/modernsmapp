package blob

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Module 4 M4-P0-5 — ObjectURL must never hand back a stable URL for protected
// bytes.
//
// This test exists because a negative control found it missing. Removing the
// protected-key refusal from ObjectURL did not fail anything: it only broke the
// build on an unused import, which is an accident of Go, not a guarantee. A
// future change that removed the refusal AND used the import elsewhere would
// have restored the original hole silently.
//
// The Store is built as a struct literal rather than through New() on purpose:
// New() dials MinIO and creates a bucket, and the property under test is a pure
// decision about a key that needs no object store at all.

func TestObjectURLRefusesProtectedKeys(t *testing.T) {
	s := &Store{bucket: "media", cdnBaseURL: "https://cdn.example.test"}

	for _, key := range []string{
		"protected/stories/abc.jpg",
		"stories/abc.jpg", // unknown prefix — protected by default
		"",
		"posts/restricted/v.mp4",
	} {
		t.Run(key, func(t *testing.T) {
			got, err := s.ObjectURL(context.Background(), key, time.Minute)
			if err == nil {
				t.Fatalf("ObjectURL(%q) returned %q with no error.\n"+
					"A stable, unauthenticated URL for protected bytes is the exact defect "+
					"M4-P0-5 closes: it survives blocks, takedowns and deletion.", key, got)
			}
			if got != "" {
				t.Errorf("ObjectURL(%q) returned a URL alongside its error: %q", key, got)
			}
		})
	}
}

func TestObjectURLStillServesPublicKeys(t *testing.T) {
	s := &Store{bucket: "media", cdnBaseURL: "https://cdn.example.test"}
	got, err := s.ObjectURL(context.Background(), "public/avatars/a.png", time.Minute)
	if err != nil {
		t.Fatalf("public key was refused: %v", err)
	}
	if !strings.HasPrefix(got, "https://cdn.example.test/") {
		t.Fatalf("public key did not get a CDN URL: %q", got)
	}
}
