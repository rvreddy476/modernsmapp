package http

// The reviewer's document view headers (31 Oct 2026): a PDF is never inline.
// The full route runs in sellerdocs_integration_test.go (CI).

import (
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/media"
)

func TestAPDFDocumentIsAnAttachmentAndImagesStayInline(t *testing.T) {
	if got := documentDisposition(media.DocumentTypePDF); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("a PDF is served %q; opened in the console's origin it could render inline", got)
	}
	for _, ct := range []string{"image/jpeg", "image/png", "image/webp"} {
		if got := documentDisposition(ct); got != "inline" {
			t.Errorf("%s is served %q; the console draws images inline", ct, got)
		}
	}
}

func TestTheDocumentViewIsSandboxed(t *testing.T) {
	if DocumentViewCSP != "default-src 'none'; sandbox" {
		t.Fatalf("DocumentViewCSP = %q", DocumentViewCSP)
	}
}

func TestTheDocumentViewHeaders(t *testing.T) {
	for ct, disposition := range map[string]string{
		media.DocumentTypePDF: `attachment; filename="document.pdf"`,
		"image/jpeg":          "inline",
	} {
		hdr := http.Header{}
		setDocumentViewHeaders(hdr, ct, 42)
		for k, want := range map[string]string{
			"Content-Type":            ct,
			"Content-Length":          "42",
			"Content-Disposition":     disposition,
			"Cache-Control":           "no-store, private",
			"X-Content-Type-Options":  "nosniff",
			"Content-Security-Policy": "default-src 'none'; sandbox",
		} {
			if got := hdr.Get(k); got != want {
				t.Errorf("%s: %s = %q, want %q", ct, k, got, want)
			}
		}
	}
}
