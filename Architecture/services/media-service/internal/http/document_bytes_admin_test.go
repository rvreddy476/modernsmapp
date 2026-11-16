package http

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// admin-service on document-bytes (16 Nov 2026): the Official-mark
// registration document. owner_id is required for this issuer and must be the
// document's uploader; every miss is the one 404 MEDIA_NOT_FOUND.

const docAdminActor = "6c1f3e2a-9b8d-4c7e-a1f0-2b3c4d5e6f70"

func (f *documentBytesFixture) withAdminCaller(t *testing.T, ops ...string) *servicetoken.Signer {
	t.Helper()
	signer, pub := mustSigner(t, IssuerAdminService, "a1")
	if len(ops) == 0 {
		ops = []string{OpDocumentBytesRead}
	}
	if err := f.verifier.RegisterBase64(IssuerAdminService, "a1", pub, ops, nil); err != nil {
		t.Fatal(err)
	}
	return signer
}

func mintAdminDoc(t *testing.T, s *servicetoken.Signer, actor string, scope ...string) string {
	t.Helper()
	var opts []servicetoken.MintOption
	if actor != "" {
		opts = append(opts, servicetoken.WithActor(actor))
	}
	tok, err := s.Mint(AudienceMedia, "marks-document-view", scope, nil, time.Minute, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func documentBytesPathOwner(id uuid.UUID, owner string) string {
	p := documentBytesPath(id)
	if owner == "" {
		return p
	}
	return p + "?owner_id=" + url.QueryEscape(owner)
}

func (f *documentBytesFixture) uploader(id uuid.UUID) string {
	return f.records[id].UploaderID.String()
}

func TestDocumentBytes_AdminServiceWithTheUploaderGetsTheBytes(t *testing.T) {
	f := newDocumentBytesFixture(t)
	admin := f.withAdminCaller(t)
	r := f.router(t, false, nil)
	tok := mintAdminDoc(t, admin, docAdminActor, OpDocumentBytesRead)
	for _, tc := range []struct {
		id   uuid.UUID
		body []byte
		ct   string
	}{{f.pdf, docPDFBody, "application/pdf"}, {f.png, docPNGBody, "image/png"}} {
		w := getImageBytes(r, documentBytesPathOwner(tc.id, f.uploader(tc.id)), docKey, tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200: %s", tc.ct, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != tc.ct {
			t.Errorf("Content-Type = %q, want %q", got, tc.ct)
		}
		if got := w.Header().Get("Content-Security-Policy"); got != DocumentCSP {
			t.Errorf("CSP = %q", got)
		}
		if !bytes.Equal(w.Body.Bytes(), tc.body) {
			t.Fatalf("%s: bytes not served unchanged", tc.ct)
		}
	}
}

func TestDocumentBytes_AdminServiceOwnerBinding(t *testing.T) {
	f := newDocumentBytesFixture(t)
	admin := f.withAdminCaller(t)
	r := f.router(t, false, nil)
	tok := mintAdminDoc(t, admin, docAdminActor, OpDocumentBytesRead)
	cases := map[string]string{
		"no owner_id":        "",
		"another account":    uuid.NewString(),
		"the png's uploader": f.uploader(f.png),
		"nil uuid":           uuid.Nil.String(),
		"not a uuid":         "applicant",
	}
	for name, owner := range cases {
		w := getImageBytes(r, documentBytesPathOwner(f.pdf, owner), docKey, tok, nil)
		if w.Code != http.StatusNotFound || imageBytesErrorCode(t, w) != CodeMediaNotFound {
			t.Errorf("%s: %d %s, want 404 MEDIA_NOT_FOUND", name, w.Code, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte("%PDF")) {
			t.Fatalf("%s: a refusal carried document bytes", name)
		}
		assertDocumentHeadersOnEveryAnswer(t, name, w)
	}
}

func TestDocumentBytes_AdminServiceNeedsAnActingPerson(t *testing.T) {
	f := newDocumentBytesFixture(t)
	admin := f.withAdminCaller(t)
	r := f.router(t, false, nil)
	for name, actor := range map[string]string{"no act": "", "not a uuid": "someone"} {
		tok := mintAdminDoc(t, admin, actor, OpDocumentBytesRead)
		w := getImageBytes(r, documentBytesPathOwner(f.pdf, f.uploader(f.pdf)), docKey, tok, nil)
		if w.Code != http.StatusForbidden || imageBytesErrorCode(t, w) != CodeServiceTokenRejected {
			t.Errorf("%s: %d %s, want 403 SERVICE_TOKEN_REJECTED", name, w.Code, w.Body.String())
		}
	}
}

func TestDocumentBytes_AdminServiceWrongOperationRefused(t *testing.T) {
	f := newDocumentBytesFixture(t)
	// Registered for both, so only the operation in the token can refuse it.
	admin := f.withAdminCaller(t, OpDocumentBytesRead, PartnerEvidenceOperation)
	r := f.router(t, false, nil)
	for name, tok := range map[string]string{
		"partner-evidence operation": mintAdminDoc(t, admin, docAdminActor, PartnerEvidenceOperation),
		"image-bytes operation":      mintAdminDoc(t, admin, docAdminActor, OpImageBytesRead),
	} {
		w := getImageBytes(r, documentBytesPathOwner(f.pdf, f.uploader(f.pdf)), docKey, tok, nil)
		if w.Code != http.StatusForbidden || imageBytesErrorCode(t, w) != CodeServiceTokenRejected {
			t.Errorf("%s: %d %s, want 403 SERVICE_TOKEN_REJECTED", name, w.Code, w.Body.String())
		}
	}
}

func TestDocumentBytes_AdminServiceNotRegisteredIsRefused(t *testing.T) {
	f := newDocumentBytesFixture(t)
	signer, _ := mustSigner(t, IssuerAdminService, "a1") // never registered
	r := f.router(t, false, nil)
	tok := mintAdminDoc(t, signer, docAdminActor, OpDocumentBytesRead)
	w := getImageBytes(r, documentBytesPathOwner(f.pdf, f.uploader(f.pdf)), docKey, tok, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unregistered admin-service: %d, want 403", w.Code)
	}
}

func TestDocumentBytes_CommerceUnchangedByOwnerID(t *testing.T) {
	f := newDocumentBytesFixture(t)
	f.withAdminCaller(t)
	r := f.router(t, false, nil)
	// commerce names no owner and is served; an owner_id it might send is
	// not consulted (its reads resolve the media id from its own records).
	for name, owner := range map[string]string{"none": "", "unrelated": uuid.NewString()} {
		w := getImageBytes(r, documentBytesPathOwner(f.pdf, owner), docKey, f.commerceToken(t), nil)
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), docPDFBody) {
			t.Errorf("commerce, owner %s: %d, want 200 with the bytes", name, w.Code)
		}
	}
}

func TestDocumentBytes_AdminServiceCannotUseImageBytes(t *testing.T) {
	f := newDocumentBytesFixture(t)
	admin := f.withAdminCaller(t, OpDocumentBytesRead, OpImageBytesRead)
	r := f.router(t, false, nil)
	tok := mintAdminDoc(t, admin, docAdminActor, OpImageBytesRead)
	w := getImageBytes(r, "/v1/media/internal/"+f.image.String()+"/image-bytes", docKey, tok, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("admin-service on image-bytes: %d, want 403", w.Code)
	}
}

// countingDocumentBytes records whether the reader was asked at all.
type countingDocumentBytes struct {
	calls *int
	inner documentBytesService
}

func (c countingDocumentBytes) DocumentBytes(ctx context.Context, id uuid.UUID) (*service.DocumentBytes, error) {
	*c.calls++
	return c.inner.DocumentBytes(ctx, id)
}

func TestDocumentBytes_AdminServiceWithoutOwnerNeverReadsTheDocument(t *testing.T) {
	f := newDocumentBytesFixture(t)
	admin := f.withAdminCaller(t)
	calls := 0
	r := f.router(t, false, countingDocumentBytes{calls: &calls, inner: service.NewDocumentReader(f.records, f.objects)})
	tok := mintAdminDoc(t, admin, docAdminActor, OpDocumentBytesRead)
	for _, owner := range []string{"", "applicant", uuid.Nil.String()} {
		w := getImageBytes(r, documentBytesPathOwner(f.pdf, owner), docKey, tok, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("owner %q: %d, want 404", owner, w.Code)
		}
	}
	if calls != 0 {
		t.Fatalf("the document was read %d times for a request naming no owner", calls)
	}
}
