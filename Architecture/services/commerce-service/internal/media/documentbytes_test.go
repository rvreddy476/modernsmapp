package media

// The KYC document read, against a stand-in media-service: the pinned route,
// the token it must carry, the types and bytes that are never passed on, and
// the size cap. Plus VerifyOwnedOneOf, which onboarding uses to require the
// document kind for new KYC uploads.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

var (
	pdfDoc  = []byte("%PDF-1.7\n1 0 obj << >> endobj\n%%EOF\n")
	pngDoc  = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, bytes.Repeat([]byte{1}, 20)...)
	jpegDoc = append([]byte{0xFF, 0xD8, 0xFF, 0xE1}, bytes.Repeat([]byte{2}, 20)...)
)

// documentClient is a client with a commerce signer, and the verifier
// media-service would hold for it.
func documentClient(t *testing.T, url string) (*Client, *servicetoken.Verifier) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ServiceTokenSigner("c1", priv)
	if err != nil || signer == nil {
		t.Fatalf("ServiceTokenSigner: %v %v", signer, err)
	}
	v := servicetoken.NewVerifier(ImageBytesAudience)
	if err := v.RegisterBase64("commerce-service", "c1", pub, []string{DocumentBytesOperation}, nil); err != nil {
		t.Fatal(err)
	}
	return New(url, "the-key").WithServiceToken(signer), v
}

func docServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestFetchDocumentBytesCallsThePinnedRouteWithBothCredentials(t *testing.T) {
	id := uuid.New()
	var gotPath, gotKey, gotOp string
	var userHeaders []string
	var verr error
	var v *servicetoken.Verifier
	s := docServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("X-Internal-Service-Key")
		raw := strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer ")
		var verified *servicetoken.Verified
		verified, verr = v.Verify(raw, DocumentBytesOperation, "")
		if verified != nil {
			gotOp = verified.Issuer
		}
		for _, h := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
			if _, present := r.Header[http.CanonicalHeaderKey(h)]; present {
				userHeaders = append(userHeaders, h)
			}
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfDoc)
	})
	var c *Client
	c, v = documentClient(t, s.URL)
	got, ct, err := c.FetchDocumentBytes(WithViewer(context.Background(), uuid.New()), id)
	if err != nil {
		t.Fatalf("FetchDocumentBytes: %v", err)
	}
	if want := "/v1/media/internal/" + id.String() + "/document-bytes"; gotPath != want {
		t.Fatalf("called %s, want %s", gotPath, want)
	}
	if gotKey != "the-key" {
		t.Fatalf("X-Internal-Service-Key = %q", gotKey)
	}
	if verr != nil || gotOp != "commerce-service" {
		t.Fatalf("media-service could not verify a commerce document token: %v (issuer %q)", verr, gotOp)
	}
	if len(userHeaders) != 0 {
		t.Fatalf("sent user headers %v", userHeaders)
	}
	if ct != DocumentTypePDF || !bytes.Equal(got, pdfDoc) {
		t.Fatalf("got %q / %d bytes", ct, len(got))
	}
}

func TestFetchDocumentBytesNeverCallsWithoutAToken(t *testing.T) {
	called := false
	s := docServer(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	_, _, err := New(s.URL, "k").FetchDocumentBytes(context.Background(), uuid.New())
	if !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("err = %v, want ErrMediaUnavailable", err)
	}
	if called {
		t.Fatal("a client with no signer called the document route")
	}
}

func TestFetchDocumentBytesAcceptsTheThreeTypes(t *testing.T) {
	for ct, body := range map[string][]byte{"application/pdf": pdfDoc, "image/png": pngDoc, "IMAGE/JPEG; q=1": jpegDoc} {
		s := docServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			_, _ = w.Write(body)
		})
		c, _ := documentClient(t, s.URL)
		got, typ, err := c.FetchDocumentBytes(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("%s refused: %v", ct, err)
		}
		if typ != strings.ToLower(strings.Split(ct, ";")[0]) || !bytes.Equal(got, body) {
			t.Fatalf("%s came back as %q", ct, typ)
		}
	}
}

func TestFetchDocumentBytesRefusals(t *testing.T) {
	html := []byte("<html><script>alert(1)</script></html>")
	cases := []struct {
		name   string
		status int
		ctype  string
		body   []byte
		want   error
	}{
		{"not found", http.StatusNotFound, "application/json", []byte(`{}`), ErrMediaNotFound},
		{"401 is unavailability", http.StatusUnauthorized, "application/json", []byte(`{}`), ErrMediaUnavailable},
		{"403 is unavailability", http.StatusForbidden, "application/json", []byte(`{}`), ErrMediaUnavailable},
		{"500 is unavailability", http.StatusInternalServerError, "application/json", []byte(`{}`), ErrMediaUnavailable},
		{"HTML is not a document", http.StatusOK, "text/html", html, ErrNotADocument},
		{"SVG is not a document", http.StatusOK, "image/svg+xml", []byte("<svg/>"), ErrNotADocument},
		{"WebP is not a document", http.StatusOK, "image/webp", []byte("RIFF....WEBP"), ErrNotADocument},
		{"no type", http.StatusOK, "", pdfDoc, ErrNotADocument},
		{"HTML labelled PDF", http.StatusOK, "application/pdf", html, ErrNotADocument},
		{"PNG labelled PDF", http.StatusOK, "application/pdf", pngDoc, ErrNotADocument},
		{"PDF labelled JPEG", http.StatusOK, "image/jpeg", pdfDoc, ErrNotADocument},
		{"PDF marker past offset 0", http.StatusOK, "application/pdf", append([]byte("<html>"), pdfDoc...), ErrNotADocument},
		{"empty", http.StatusOK, "application/pdf", nil, ErrNotADocument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := docServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header()["Content-Type"] = []string{tc.ctype}
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			})
			c, _ := documentClient(t, s.URL)
			got, _, err := c.FetchDocumentBytes(context.Background(), uuid.New())
			if !errors.Is(err, tc.want) || got != nil {
				t.Fatalf("(%d bytes, %v), want %v", len(got), err, tc.want)
			}
		})
	}
}

func TestFetchDocumentBytesCapsTheSize(t *testing.T) {
	big := append(append([]byte{}, pdfDoc...), bytes.Repeat([]byte{'a'}, MaxDocumentBytes)...)
	for name, declare := range map[string]bool{"declared": true, "undeclared": false} {
		s := docServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			if declare {
				w.Header().Set("Content-Length", strconv.Itoa(len(big)))
			}
			_, _ = w.Write(big)
		})
		c, _ := documentClient(t, s.URL)
		if _, _, err := c.FetchDocumentBytes(context.Background(), uuid.New()); !errors.Is(err, ErrDocumentTooLarge) {
			t.Fatalf("%s oversize: %v, want ErrDocumentTooLarge", name, err)
		}
	}
	exact := append(append([]byte{}, pdfDoc...), bytes.Repeat([]byte{'a'}, MaxDocumentBytes-len(pdfDoc))...)
	s := docServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(exact)
	})
	c, _ := documentClient(t, s.URL)
	if got, _, err := c.FetchDocumentBytes(context.Background(), uuid.New()); err != nil || len(got) != MaxDocumentBytes {
		t.Fatalf("a document of exactly the cap: %d bytes, %v", len(got), err)
	}
}

func TestVerifyOwnedOneOfAcceptsOnlyTheNamedKinds(t *testing.T) {
	owner := uuid.New()
	ctx := context.Background()
	for _, tc := range []struct {
		fileType string
		kinds    []Kind
		ok       bool
	}{
		{"document", []Kind{KindDocument}, true},
		{"image", []Kind{KindDocument}, false},
		{"video", []Kind{KindDocument}, false},
		{"image", []Kind{KindDocument, KindImage}, true},
		{"document", []Kind{KindDocument, KindImage}, true},
		{"video", []Kind{KindDocument, KindImage}, false},
		{"video", []Kind{KindAny}, true},
		{"video", nil, true},
	} {
		a := readyImage(owner)
		a.FileType = tc.fileType
		err := New(stubMedia(t, a, http.StatusOK).URL, "k").VerifyOwnedOneOf(ctx, a.ID, owner, tc.kinds...)
		if tc.ok && err != nil {
			t.Errorf("%s with %v refused: %v", tc.fileType, tc.kinds, err)
		}
		if !tc.ok && !errors.Is(err, ErrMediaWrongKind) {
			t.Errorf("%s with %v: %v, want ErrMediaWrongKind", tc.fileType, tc.kinds, err)
		}
	}
	// The other conditions still hold.
	stolen := readyImage(uuid.New())
	stolen.FileType = "document"
	if err := New(stubMedia(t, stolen, http.StatusOK).URL, "k").
		VerifyOwnedOneOf(ctx, stolen.ID, owner, KindDocument, KindImage); !errors.Is(err, ErrNotYourMedia) {
		t.Fatalf("someone else's document: %v, want ErrNotYourMedia", err)
	}
}
