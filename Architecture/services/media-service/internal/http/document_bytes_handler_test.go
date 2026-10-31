package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Private documents over httptest: GET /v1/media/internal/:mediaId/document-
// bytes through the REAL RegisterRoutes, caller check and reader (fake record
// and object stores); POST /v1/media/init's document refusals; and the viewer
// routes refusing a document. No database, no object store.

const docKey = "document-bytes-internal-key"

var (
	docPDFBody = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << >>\n%%EOF\n")
	docPNGBody = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R'}, bytes.Repeat([]byte{7}, 32)...)
)

type docRecordStore map[uuid.UUID]*postgres.MediaAsset

func (s docRecordStore) GetMedia(_ context.Context, id uuid.UUID) (*postgres.MediaAsset, error) {
	m, ok := s[id]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	cp := *m
	return &cp, nil
}

type docObjectStore map[string][]byte

func (o docObjectStore) OpenObject(_ context.Context, key string) (io.ReadCloser, blob.ObjectInfo, error) {
	d, ok := o[key]
	if !ok {
		return nil, blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(d)), blob.ObjectInfo{Size: int64(len(d)), ETag: "etag-" + key}, nil
}

type documentBytesFixture struct {
	records docRecordStore
	objects docObjectStore

	commerceSigner, doorstepSigner, rogueSigner *servicetoken.Signer
	verifier                                    *servicetoken.Verifier

	pdf, png, image uuid.UUID
}

func newDocumentBytesFixture(t *testing.T) *documentBytesFixture {
	t.Helper()
	f := &documentBytesFixture{records: docRecordStore{}, objects: docObjectStore{}}
	add := func(fileType, mime string, body []byte) uuid.UUID {
		id, owner := uuid.New(), uuid.New()
		key := "user/" + owner.String() + "/" + id.String() + "/original"
		f.records[id] = &postgres.MediaAsset{ID: id, UploaderID: owner, FileType: fileType, MimeType: mime,
			FileSizeBytes: int64(len(body)), StorageKey: key, ProcessingStatus: "ready", ModerationStatus: "passed",
			OriginalETag: "etag-" + key}
		f.objects[key] = body
		return id
	}
	f.pdf = add(service.FileTypeDocument, "application/pdf", docPDFBody)
	f.png = add(service.FileTypeDocument, "image/png", docPNGBody)
	// An image whose bytes are a perfectly good PNG: only the kind refuses it.
	f.image = add("image", "image/png", docPNGBody)

	var commercePub, doorstepPub string
	f.commerceSigner, commercePub = mustSigner(t, IssuerCommerceService, "c1")
	f.doorstepSigner, doorstepPub = mustSigner(t, IssuerDoorstepService, "doorstep-1")
	f.rogueSigner, _ = mustSigner(t, IssuerCommerceService, "c1")
	f.verifier = servicetoken.NewVerifier(AudienceMedia)
	if err := f.verifier.RegisterBase64(IssuerCommerceService, "c1", commercePub, []string{OpImageBytesRead, OpDocumentBytesRead}, nil); err != nil {
		t.Fatal(err)
	}
	// doorstep is registered for the document operation too, so only the
	// issuer allow-list can refuse it.
	if err := f.verifier.RegisterBase64(IssuerDoorstepService, "doorstep-1", doorstepPub, []string{OpImageBytesRead, OpDocumentBytesRead}, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *documentBytesFixture) router(t *testing.T, legacyKey bool, svc documentBytesService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(postgres.New(nil), nil)).WithInternalKey(docKey).WithImageBytesAuth(f.verifier, legacyKey)
	if svc == nil {
		svc = service.NewDocumentReader(f.records, f.objects)
	}
	h.documentBytes = svc
	h.RegisterRoutes(r, passthrough, passthrough)
	return r
}

func documentBytesPath(id uuid.UUID) string {
	return "/v1/media/internal/" + id.String() + "/document-bytes"
}

func (f *documentBytesFixture) commerceToken(t *testing.T) string {
	return mint(t, f.commerceSigner, AudienceMedia, OpDocumentBytesRead)
}

func assertDocumentHeadersOnEveryAnswer(t *testing.T, what string, w *httptest.ResponseRecorder) {
	t.Helper()
	if got := w.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Errorf("%s: Cache-Control = %q, want no-store, private", what, got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("%s: X-Content-Type-Options = %q, want nosniff", what, got)
	}
}

func TestDocumentBytes_RouteRegisteredOnlyInInternalGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	with := gin.New()
	New(service.New(postgres.New(nil), nil)).WithInternalKey("k").RegisterRoutes(with, passthrough, passthrough)
	found := false
	for _, ri := range with.Routes() {
		if ri.Method == http.MethodGet && ri.Path == "/v1/media/internal/:mediaId/document-bytes" {
			found = strings.Contains(ri.Handler, ".GetDocumentBytes")
		}
	}
	if !found {
		t.Fatal("GET /v1/media/internal/:mediaId/document-bytes is not registered to GetDocumentBytes")
	}
	without := gin.New()
	New(service.New(postgres.New(nil), nil)).RegisterRoutes(without, passthrough, passthrough)
	for _, ri := range without.Routes() {
		if strings.HasSuffix(ri.Path, "/document-bytes") {
			t.Fatalf("document-bytes must not exist without an internal key, found %s %s", ri.Method, ri.Path)
		}
	}
}

func TestDocumentBytes_ServesPDFAsAnAttachmentWithSafeHeaders(t *testing.T) {
	f := newDocumentBytesFixture(t)
	r := f.router(t, false, nil)
	w := getImageBytes(r, documentBytesPath(f.pdf), docKey, f.commerceToken(t), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	want := map[string]string{
		"Content-Type":                 "application/pdf",
		"Content-Disposition":          `attachment; filename="document.pdf"`,
		"Content-Security-Policy":      "default-src 'none'; sandbox",
		"Cache-Control":                "no-store, private",
		"X-Content-Type-Options":       "nosniff",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Content-Length":               strconv.Itoa(len(docPDFBody)),
	}
	for k, v := range want {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if w.Header().Get("Location") != "" {
		t.Error("document-bytes redirected; it must stream")
	}
	if !bytes.Equal(w.Body.Bytes(), docPDFBody) {
		t.Fatal("the PDF was not served byte for byte")
	}
}

func TestDocumentBytes_ImageDocumentTypeIsTheVerifiedOne(t *testing.T) {
	f := newDocumentBytesFixture(t)
	r := f.router(t, false, nil)
	w := getImageBytes(r, documentBytesPath(f.png), docKey, f.commerceToken(t), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="document.png"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
}

func TestDocumentBytes_RefusesAnImageAsNotFound(t *testing.T) {
	f := newDocumentBytesFixture(t)
	r := f.router(t, false, nil)
	w := getImageBytes(r, documentBytesPath(f.image), docKey, f.commerceToken(t), nil)
	if w.Code != http.StatusNotFound || imageBytesErrorCode(t, w) != CodeMediaNotFound {
		t.Fatalf("an image asset: %d %s, want 404 MEDIA_NOT_FOUND", w.Code, w.Body.String())
	}
	assertDocumentHeadersOnEveryAnswer(t, "404", w)
	if w.Header().Get("Content-Disposition") != "" || w.Header().Get("Content-Type") == "image/png" {
		t.Fatal("a refusal carried document headers")
	}
}

func TestDocumentBytes_RequiresAServiceTokenEvenWhereImageBytesTakesTheKey(t *testing.T) {
	f := newDocumentBytesFixture(t)
	for _, legacy := range []bool{false, true} {
		r := f.router(t, legacy, nil)
		w := getImageBytes(r, documentBytesPath(f.pdf), docKey, "", nil)
		if w.Code != http.StatusUnauthorized || imageBytesErrorCode(t, w) != CodeServiceTokenRequired {
			t.Fatalf("legacy=%v, no token: %d %s, want 401 SERVICE_TOKEN_REQUIRED", legacy, w.Code, w.Body.String())
		}
		assertDocumentHeadersOnEveryAnswer(t, "401", w)
		if bytes.Contains(w.Body.Bytes(), []byte("%PDF")) {
			t.Fatal("a refused request carried document bytes")
		}
	}
}

func TestDocumentBytes_RequiresTheInternalKey(t *testing.T) {
	f := newDocumentBytesFixture(t)
	r := f.router(t, false, nil)
	for _, key := range []string{"", "wrong-key"} {
		w := getImageBytes(r, documentBytesPath(f.pdf), key, f.commerceToken(t), nil)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("internal key %q: %d, want 401", key, w.Code)
		}
	}
}

func TestDocumentBytes_RejectsTheWrongToken(t *testing.T) {
	f := newDocumentBytesFixture(t)
	r := f.router(t, false, nil)
	cases := map[string]string{
		"image-bytes operation only":    mint(t, f.commerceSigner, AudienceMedia, OpImageBytesRead),
		"doorstep-service":              mint(t, f.doorstepSigner, AudienceMedia, OpDocumentBytesRead),
		"unregistered key, same issuer": mint(t, f.rogueSigner, AudienceMedia, OpDocumentBytesRead),
		"another audience":              mint(t, f.commerceSigner, "payments", OpDocumentBytesRead),
		"garbage":                       "not-a-token",
	}
	for name, tok := range cases {
		w := getImageBytes(r, documentBytesPath(f.pdf), docKey, tok, nil)
		if w.Code != http.StatusForbidden || imageBytesErrorCode(t, w) != CodeServiceTokenRejected {
			t.Errorf("%s: %d %s, want 403 SERVICE_TOKEN_REJECTED", name, w.Code, w.Body.String())
		}
	}
}

func TestDocumentBytes_NoVerifierRejectsEveryToken(t *testing.T) {
	f := newDocumentBytesFixture(t)
	f.verifier = nil
	r := f.router(t, true, nil)
	w := getImageBytes(r, documentBytesPath(f.pdf), docKey, f.commerceToken(t), nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("no configured callers: %d, want 403", w.Code)
	}
}

func TestDocumentBytes_RefusesAUserIdentity(t *testing.T) {
	f := newDocumentBytesFixture(t)
	r := f.router(t, false, nil)
	w := getImageBytes(r, documentBytesPath(f.pdf), docKey, f.commerceToken(t), map[string]string{"X-User-Id": uuid.NewString()})
	if w.Code != http.StatusForbidden || imageBytesErrorCode(t, w) != CodeUserCallerRefused {
		t.Fatalf("a request carrying X-User-Id: %d %s, want 403 USER_CALLER_REFUSED", w.Code, w.Body.String())
	}
}

// fakeDocumentBytes answers a fixed result, to drive the handler's own
// backstop and the outage path.
type fakeDocumentBytes struct {
	doc *service.DocumentBytes
	err error
}

func (f fakeDocumentBytes) DocumentBytes(context.Context, uuid.UUID) (*service.DocumentBytes, error) {
	return f.doc, f.err
}

func TestDocumentBytes_HandlerNeverServesUnverifiedBytes(t *testing.T) {
	f := newDocumentBytesFixture(t)
	cases := map[string]*service.DocumentBytes{
		"html labelled pdf":  {Bytes: []byte("<html><script>alert(1)</script></html>"), ContentType: "application/pdf"},
		"png labelled pdf":   {Bytes: docPNGBody, ContentType: "application/pdf"},
		"pdf labelled html":  {Bytes: docPDFBody, ContentType: "text/html"},
		"empty":              {Bytes: nil, ContentType: "application/pdf"},
		"over the size cap":  {Bytes: append(append([]byte{}, docPDFBody...), make([]byte, service.MaxDocumentSizeBytes)...), ContentType: "application/pdf"},
		"nil with nil error": nil,
	}
	for name, doc := range cases {
		r := f.router(t, false, fakeDocumentBytes{doc: doc})
		w := getImageBytes(r, documentBytesPath(f.pdf), docKey, f.commerceToken(t), nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, w.Code)
		}
	}
}

func TestDocumentBytes_StoreOutageIs503(t *testing.T) {
	f := newDocumentBytesFixture(t)
	r := f.router(t, false, fakeDocumentBytes{err: errors.New("blob: connection reset")})
	w := getImageBytes(r, documentBytesPath(f.pdf), docKey, f.commerceToken(t), nil)
	if w.Code != http.StatusServiceUnavailable || imageBytesErrorCode(t, w) != CodeMediaUnavailable {
		t.Fatalf("outage: %d %s, want 503 MEDIA_UNAVAILABLE", w.Code, w.Body.String())
	}
	assertDocumentHeadersOnEveryAnswer(t, "503", w)
}

// ── POST /v1/media/init for a document ───────────────────────────────────

func initDocument(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(postgres.New(nil), nil)).RegisterRoutes(r, passthrough, passthrough)
	req := httptest.NewRequest(http.MethodPost, "/v1/media/init", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestInitDocument_RefusesATypeOutsideTheAllowList(t *testing.T) {
	for _, mime := range []string{"text/html", "image/svg+xml", "image/webp", "application/octet-stream"} {
		w := initDocument(t, `{"file_type":"document","mime_type":"`+mime+`","file_size_bytes":1024}`)
		if w.Code != http.StatusUnsupportedMediaType || imageBytesErrorCode(t, w) != "UNSUPPORTED_MEDIA_TYPE" {
			t.Errorf("%s: %d %s, want 415 UNSUPPORTED_MEDIA_TYPE", mime, w.Code, w.Body.String())
		}
	}
}

func TestInitDocument_RefusesOverTheSizeCap(t *testing.T) {
	w := initDocument(t, `{"file_type":"document","mime_type":"application/pdf","file_size_bytes":`+
		strconv.FormatInt(service.MaxDocumentSizeBytes+1, 10)+`}`)
	if w.Code != http.StatusRequestEntityTooLarge || imageBytesErrorCode(t, w) != "FILE_TOO_LARGE" {
		t.Fatalf("%d %s, want 413 FILE_TOO_LARGE", w.Code, w.Body.String())
	}
}

func TestInitRefusesAnUnknownFileType(t *testing.T) {
	w := initDocument(t, `{"file_type":"pdf","mime_type":"application/pdf","file_size_bytes":1024}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("%d, want 400 for a file_type outside image|video|audio|document", w.Code)
	}
}

// ── no viewer route serves a document ────────────────────────────────────

// allowEveryone is a content authority that admits every viewer, signed-out
// included: the document rule is the only thing left that can refuse.
type allowEveryone struct{ asked int }

func (a *allowEveryone) Authorize(context.Context, string, string) error { a.asked++; return nil }

func TestViewerRoutesRefuseADocument(t *testing.T) {
	owner := uuid.New()
	id := uuid.New()
	doc := &postgres.MediaAsset{ID: id, UploaderID: owner, FileType: service.FileTypeDocument, MimeType: "application/pdf",
		FileSizeBytes: int64(len(docPDFBody)), StorageKey: "user/" + owner.String() + "/" + id.String() + "/original",
		ProcessingStatus: "ready", ModerationStatus: "passed"}
	authz := &allowEveryone{}
	signer := &headSigner{}
	gate := delivery.NewGate(signer, authz)
	heads := &headStore{media: map[uuid.UUID]*postgres.MediaAsset{id: doc}}
	records := &fakeRecordStore{media: map[uuid.UUID]*postgres.MediaAsset{id: doc}}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{
		heads:   service.NewServeHeads(heads, gate),
		records: service.NewRecordReads(records, &fakeRecordBlobs{}, gate),
	}
	h.RegisterRoutes(r, passthrough, passthrough)
	do := func(method, path string, viewer uuid.UUID) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if viewer != uuid.Nil {
			req.Header.Set("X-User-Id", viewer.String())
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	for _, viewer := range []uuid.UUID{owner, uuid.New(), uuid.Nil} {
		if w := do(http.MethodHead, servePath(id, ""), viewer); w.Code != http.StatusNotFound {
			t.Errorf("HEAD serve for viewer %s: %d, want 404", viewer, w.Code)
		}
		if w := do(http.MethodHead, servePath(id, "original"), viewer); w.Code != http.StatusNotFound {
			t.Errorf("HEAD serve/original for viewer %s: %d, want 404", viewer, w.Code)
		}
	}
	// The record: the uploader polls their own status; nobody else sees it.
	if w := do(http.MethodGet, "/v1/media/"+id.String(), owner); w.Code != http.StatusOK {
		t.Errorf("the uploader's own document record: %d %s, want 200", w.Code, w.Body.String())
	}
	for _, viewer := range []uuid.UUID{uuid.New(), uuid.Nil} {
		if w := do(http.MethodGet, "/v1/media/"+id.String(), viewer); w.Code != http.StatusNotFound {
			t.Errorf("a document's record for viewer %s: %d, want 404", viewer, w.Code)
		}
	}
	if len(signer.signed) != 0 || len(signer.public) != 0 {
		t.Fatalf("a document key was signed for a viewer: %v %v", signer.signed, signer.public)
	}
}
