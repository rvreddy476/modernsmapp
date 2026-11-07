package service

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Bytes that are not the declared type are a refusal of the client's upload:
// ErrUploadIntegrity, which confirm answers as 409 UPLOAD_INTEGRITY_ERROR
// (handoff-android-auth-2026-10-29.md, "Commerce lane addendum"). It was a
// bare error, answered 500 INTERNAL_ERROR, and the Android KYC flow treated a
// mistyped document as an outage.
func TestConfirmHeaderMismatchIsAnIntegrityRefusal(t *testing.T) {
	html := []byte("<html><script>alert(1)</script></html>")
	cases := []struct {
		name     string
		fileType string
		declared string
		head     []byte
		refused  bool
	}{
		{"document pdf", FileTypeDocument, "application/pdf", docPDF, false},
		{"document png", FileTypeDocument, "image/png", docPNG, false},
		{"document png declared as pdf", FileTypeDocument, "application/pdf", docPNG, true},
		{"document pdf declared as jpeg", FileTypeDocument, "image/jpeg", docPDF, true},
		{"document html declared as pdf", FileTypeDocument, "application/pdf", html, true},
		{"image jpeg", "image", "image/jpeg", docJPEG, false},
		{"image png", "image", "image/png", docPNG, false},
		{"image html", "image", "image/jpeg", html, true},
		{"image pdf", "image", "image/png", docPDF, true},
		{"video html", "video", "video/mp4", html, true},
		{"audio html", "audio", "audio/mpeg", html, true},
	}
	for _, c := range cases {
		err := uploadHeaderError(c.fileType, c.declared, c.head)
		if !c.refused {
			if err != nil {
				t.Errorf("%s: refused valid bytes: %v", c.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrUploadIntegrity) {
			t.Errorf("%s: want ErrUploadIntegrity, got %v", c.name, err)
			continue
		}
		if !strings.Contains(err.Error(), c.fileType) {
			t.Errorf("%s: message does not name the file type: %v", c.name, err)
		}
	}
}

// ConfirmUpload must hand the classified error back, not a fresh bare one.
func TestConfirmUploadReturnsTheHeaderVerdict(t *testing.T) {
	body := confirmUploadBody(t)
	if !strings.Contains(body, "uploadHeaderError(media.FileType, media.MimeType, headerData)") ||
		!strings.Contains(body, "return nil, headerErr") {
		t.Fatal("ConfirmUpload no longer returns uploadHeaderError's typed verdict")
	}
	if strings.Contains(body, `fmt.Errorf("invalid %s file`) {
		t.Fatal("ConfirmUpload builds an untyped magic-byte error again (answered as 500)")
	}
}

// An unknown media id is a 404, not a pgx.ErrNoRows passed through as 500.
func TestConfirmUploadClassifiesAnUnknownID(t *testing.T) {
	body := confirmUploadBody(t)
	if !strings.Contains(body, "errors.Is(err, pgx.ErrNoRows)") ||
		!strings.Contains(body, "ErrUploadNotFound") {
		t.Fatal("ConfirmUpload passes an unknown id's ErrNoRows through untyped")
	}
}

// A voice note that does not decode is bytes that are not the declared type:
// the same 409 integrity refusal. processVoice needs the object store and
// ffprobe, so this reads the source, as the other confirm guards here do.
func TestUndecodableVoiceIsAnIntegrityRefusal(t *testing.T) {
	_, here, _, _ := runtime.Caller(0)
	b, err := os.ReadFile(filepath.Join(filepath.Dir(here), "voice.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `audio could not be decoded", ErrUploadIntegrity)`) {
		t.Fatal("an undecodable voice upload is no longer ErrUploadIntegrity (answered 500)")
	}
}
