package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// confirmUploadBody returns the source of ConfirmUpload, which is where an
// asset is routed to its processing path.
func confirmUploadBody(t *testing.T) string {
	t.Helper()
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
	return src[start : start+end]
}

// An image was routed to processing by its media_subtype: general, avatar and
// cover were processed, everything else fell through the switch untouched. The
// asset stayed at 'uploaded'/'pending' and confirm still answered 200, so the
// caller believed the upload had worked.
//
// Chat attachments were the visible casualty. The chat reservation requires
// ready+passed, so every send of an image attachment was denied, and
// `post_image` uploads were left in the same dead state.
//
// The subtype records where an asset is USED. It is not a reason to skip
// scanning one, and it must never again decide whether an image is processed.
func TestConfirmUploadProcessesEveryImageSubtype(t *testing.T) {
	body := confirmUploadBody(t)

	for _, gated := range []string{
		`media.MediaSubtype == "general"`,
		`media.MediaSubtype == "avatar"`,
		`media.MediaSubtype == "cover"`,
		`media.MediaSubtype == "gif"`,
	} {
		if strings.Contains(body, gated) {
			t.Fatalf("ConfirmUpload routes images by subtype again (%s): "+
				"an image with any other subtype falls through, stays "+
				"'uploaded'/'pending', and every downstream gate refuses it "+
				"while confirm answers 200", gated)
		}
	}

	if !strings.Contains(body, `case media.FileType == "image":`) {
		t.Fatal("ConfirmUpload no longer has an unconditional image case")
	}
}

// Every file_type accepted by InitUpload must reach a processing path, and a
// type that reaches none must fail loudly rather than answer 200 over an asset
// nothing will ever accept.
func TestConfirmUploadCoversEveryAcceptedFileType(t *testing.T) {
	body := confirmUploadBody(t)

	// Mirrors InitUploadRequest's `oneof=image video audio` binding.
	for _, fileType := range []string{"image", "video", "audio"} {
		if !strings.Contains(body, `case media.FileType == "`+fileType+`":`) {
			t.Fatalf("ConfirmUpload has no processing path for file_type %q", fileType)
		}
	}

	if !strings.Contains(body, "default:") {
		t.Fatal("ConfirmUpload has no default case: an unrouted asset would " +
			"be confirmed with a 200 and left unusable")
	}
}
