package consumers

import (
	"os"
	"strings"
	"testing"
)

// The by-media lookup behind the media byte gate also answers for a post's
// COVER since 2026-09-29 (store/postgres/posts.go). The review-gate release
// must not: a safety verdict on an asset speaks for the posts that play it,
// never for a post that only names it as its cover, whose own attachments
// may still be under review. This consumer drives a concrete store, so the
// rule is held by source: it reads PostIDsAttachingMedia and nothing wider.
func TestReviewGateReleaseReadsAttachmentsOnly(t *testing.T) {
	src, err := os.ReadFile("media.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	if !strings.Contains(code, "c.store.PostIDsAttachingMedia(") {
		t.Fatal("the voice-safety release no longer reads PostIDsAttachingMedia")
	}
	for _, wide := range []string{".PostIDsByMediaID(", ".PostIDsByMediaIDs("} {
		if strings.Contains(code, wide) {
			t.Fatalf("media.go calls %s: that lookup includes covers and must not release a review gate", wide)
		}
	}
}
