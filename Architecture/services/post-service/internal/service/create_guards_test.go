package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Slice C create guards — C-LB-1.3 and C-LB-3.5.
//
// These are server-side invariants. Every one is reachable by a hostile or
// simply older client that never learned the rule, so proving them here is
// proving the platform holds them regardless of what Android does.

func TestValidatePostContentRejectsEmpty(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		mediaCount int
		wantErr    error
	}{
		{"nothing at all", "", 0, ErrEmptyPost},
		{"spaces only", "   ", 0, ErrEmptyPost},
		{"tabs and newlines only", "\t\n  \r\n", 0, ErrEmptyPost},
		// An image with no caption is a legitimate post; a caption with no
		// image obviously is too. Only the empty intersection is refused.
		{"image with no text", "", 1, nil},
		{"text with no image", "hello", 0, nil},
		{"both", "hello", 1, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePostContent(tc.text, tc.mediaCount)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// The ceiling is CODE POINTS, not bytes and not UTF-16 units.
//
// A byte limit would accept 5,000 Latin characters and reject roughly 1,600
// Devanagari ones — a limit that discriminates by script, which for an
// India-first product is a product bug, not a technicality. Android counts the
// same unit so the two agree on the boundary.
func TestValidatePostContentCountsCodePoints(t *testing.T) {
	if MaxPostTextRunes != 5000 {
		t.Fatalf("ceiling changed to %d; Android's MAX_TEXT_CODE_POINTS must move with it",
			MaxPostTextRunes)
	}

	if err := ValidatePostContent(strings.Repeat("x", MaxPostTextRunes), 0); err != nil {
		t.Fatalf("exactly at the limit must be accepted: %v", err)
	}
	if err := ValidatePostContent(strings.Repeat("x", MaxPostTextRunes+1), 0); !errors.Is(err, ErrTextTooLong) {
		t.Fatalf("one over the limit must be refused, got %v", err)
	}

	// 5,000 Devanagari code points are ~15,000 bytes. A byte-based limit would
	// wrongly reject this.
	devanagari := strings.Repeat("क", MaxPostTextRunes)
	if len(devanagari) <= MaxPostTextRunes {
		t.Fatal("fixture is not multi-byte; the test proves nothing")
	}
	if err := ValidatePostContent(devanagari, 0); err != nil {
		t.Fatalf("5,000 Devanagari code points must be accepted: %v", err)
	}

	// 2,600 astral emoji are 2,600 code points but 5,200 UTF-16 units.
	emoji := strings.Repeat("😀", 2600)
	if err := ValidatePostContent(emoji, 0); err != nil {
		t.Fatalf("2,600 emoji are under the code-point limit: %v", err)
	}
}

// The fingerprint is what makes an edited retry a 409 instead of a silent
// replay of the older text.
func TestCreateFingerprintDistinguishesContent(t *testing.T) {
	media := []uuid.UUID{uuid.MustParse("11111111-2222-3333-4444-555555555555")}

	base := CreateFingerprint("hello", "public", "post", "text", "en", nil)

	if base == CreateFingerprint("hello, edited", "public", "post", "text", "en", nil) {
		t.Error("edited text must change the fingerprint, or an edited retry " +
			"silently replays the ORIGINAL post and the edit is lost")
	}
	if base == CreateFingerprint("hello", "followers", "post", "text", "en", nil) {
		t.Error("a changed audience must change the fingerprint")
	}
	if base == CreateFingerprint("hello", "public", "post", "image", "en", media) {
		t.Error("attaching media must change the fingerprint")
	}
	if base == CreateFingerprint("hello", "public", "post", "text", "hi", nil) {
		t.Error("a changed language must change the fingerprint")
	}
}

func TestCreateFingerprintIsStableForIdenticalIntent(t *testing.T) {
	a := CreateFingerprint("hello", "public", "post", "text", "en", nil)
	b := CreateFingerprint("hello", "public", "post", "text", "en", nil)
	if a != b {
		t.Fatal("identical intent must fingerprint identically, or every retry " +
			"is a spurious 409")
	}
}

// Order matters: [a,b] and [b,a] are different posts.
func TestCreateFingerprintRespectsMediaOrder(t *testing.T) {
	a := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	b := uuid.MustParse("66666666-7777-8888-9999-000000000000")

	if CreateFingerprint("t", "public", "post", "image", "en", []uuid.UUID{a, b}) ==
		CreateFingerprint("t", "public", "post", "image", "en", []uuid.UUID{b, a}) {
		t.Error("media order must be part of the fingerprint")
	}
}

// Length-prefixed, so text containing the delimiter cannot be arranged to
// collide with a different split of the same bytes.
func TestCreateFingerprintResistsFieldBoundaryCollision(t *testing.T) {
	// Naively joining with "|" would make these two identical.
	a := CreateFingerprint("a|public", "post", "text", "en", "", nil)
	b := CreateFingerprint("a", "public|post", "text", "en", "", nil)
	if a == b {
		t.Error("field boundaries must be unambiguous, or one intent can be " +
			"made to fingerprint as another")
	}
}

// Strict media/content compatibility — C-P0-1, C-LB-4.1.
//
// `Kind` used to be read and never used, so a video could back an image post
// and an image could back a Flick. Both produce a post whose own reader
// resolves the wrong renderer.
func TestCheckMediaCompatibility(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		postType    string
		kind        string
		wantErr     bool
	}{
		{"image post with an image", "post", "image", "image", false},
		{"image post with a video", "post", "image", "video", true},
		{"image post with audio", "post", "image", "audio", true},
		{"text post with an image", "post", "text", "image", false},
		// A video silently published as a text post is how PostTube content
		// leaks into the social feed with no player.
		{"text post with a video", "post", "text", "video", true},
		{"flick with a video", "flick", "text", "video", false},
		{"flick with an image", "flick", "text", "image", true},
		{"long video with a video", "long_video", "text", "video", false},
		{"long video with an image", "long_video", "text", "image", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkMediaCompatibility(tc.contentType, tc.postType, tc.kind)
			if tc.wantErr && !errors.Is(err, ErrMediaTypeMismatch) {
				t.Fatalf("expected a type mismatch, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected acceptance, got %v", err)
			}
		})
	}
}
