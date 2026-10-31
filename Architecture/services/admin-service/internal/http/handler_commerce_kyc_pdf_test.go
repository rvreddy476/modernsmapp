package http

// A seller KYC document uploaded as a private PDF (31 Oct 2026): relayed
// byte for byte, as an attachment, under the same no-store / nosniff /
// sandbox headers as an image — and only if its bytes are a PDF. Doorstep's
// worker documents stay images.

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

func kycPDF(n int) []byte {
	return append([]byte("%PDF-1.7\n"), kycImage(n)...)
}

func TestKYCDocumentView_RelaysAPDFAsAnAttachment(t *testing.T) {
	for _, declared := range []bool{true, false} {
		t.Run("content-length="+strconv.FormatBool(declared), func(t *testing.T) {
			rg := newProductsRig(t, true)
			k := newKYCCase()
			pdf := kycPDF(200_000)
			k.stub(rg, true, serveImage("application/pdf", pdf, declared))
			w := rg.do(http.MethodGet, k.path, "", kycAdmin(rg), true)
			if w.Code != http.StatusOK {
				t.Fatalf("view: %d %s", w.Code, w.Body.String())
			}
			if !bytes.Equal(w.Body.Bytes(), pdf) {
				t.Fatalf("bytes changed: got %d, want %d", w.Body.Len(), len(pdf))
			}
			want := map[string]string{
				"Content-Type":                 "application/pdf",
				"Content-Length":               strconv.Itoa(len(pdf)),
				"Content-Disposition":          `attachment; filename="document.pdf"`,
				"Cache-Control":                "no-store, private, max-age=0",
				"Pragma":                       "no-cache",
				"X-Content-Type-Options":       "nosniff",
				"Content-Security-Policy":      "default-src 'none'; sandbox",
				"Cross-Origin-Resource-Policy": "same-origin",
			}
			for h, v := range want {
				if got := w.Header().Values(h); len(got) != 1 || got[0] != v {
					t.Fatalf("header %s = %q, want exactly %q", h, got, v)
				}
			}
			a := rg.takeAudit()
			if len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].Operation != opSellerKYCDocumentView {
				t.Fatalf("audit = %+v, want one successful view row", a)
			}
		})
	}
}

func TestKYCDocumentView_ImagesStayInline(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	k.stub(rg, true, serveImage("image/png", kycImage(1000), true))
	w := rg.do(http.MethodGet, k.path, "", kycAdmin(rg), true)
	if w.Code != http.StatusOK || w.Header().Get("Content-Disposition") != "inline" {
		t.Fatalf("image view: %d disposition %q", w.Code, w.Header().Get("Content-Disposition"))
	}
}

func TestKYCDocumentView_RefusesAPDFThatIsNotOne(t *testing.T) {
	for name, body := range map[string][]byte{
		"html":           []byte(`<html><script>alert(1)</script></html>`),
		"png":            append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, kycImage(64)...),
		"leading space":  append([]byte(" "), kycPDF(64)...),
		"truncated head": []byte("%PDF"),
	} {
		for _, declared := range []bool{true, false} {
			rg := newProductsRig(t, true)
			k := newKYCCase()
			k.stub(rg, true, serveImage("application/pdf", body, declared))
			w := rg.do(http.MethodGet, k.path, "", kycAdmin(rg), true)
			if w.Code != http.StatusBadGateway || !hasCode(w, CodeDocumentUnsupported) ||
				strings.Contains(w.Body.String(), "<script>") || w.Header().Get("Content-Type") == "application/pdf" {
				t.Fatalf("%s (declared=%v): %d %s", name, declared, w.Code, w.Body.String())
			}
			for _, row := range rg.takeAudit() {
				if row.Outcome == postgres.AuditOutcomeSuccess {
					t.Fatalf("%s: a refused document was audited as viewed", name)
				}
			}
		}
	}
}

func TestDeliveryDocumentView_StillRefusesAPDF(t *testing.T) {
	rg := newProductsRig(t, true)
	profile, doc := uuid.NewString(), uuid.NewString()
	path := "/delivery/profiles/" + profile + "/documents/" + doc
	rg.on(http.MethodGet, service.CommerceAdminPrefix+path+"/image", serveImage("application/pdf", kycPDF(100), true))
	w := rg.do(http.MethodGet, commercePrefix+path+"/view", "", kycAdmin(rg), true)
	if w.Code != http.StatusBadGateway || !hasCode(w, CodeDocumentUnsupported) {
		t.Fatalf("a PDF on the delivery route: %d %s, want 502 DOCUMENT_UNSUPPORTED", w.Code, w.Body.String())
	}
}
