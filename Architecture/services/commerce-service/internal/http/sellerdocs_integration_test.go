//go:build integration

package http

// The reviewer's view of a seller's KYC documents, over the real token routes
// and a real database, with media-service stood in by an httptest server that
// answers GET /v1/media/internal/:mediaId/image-bytes and …/document-bytes to
// the pinned shapes, and GET /v1/media/:id (the record commerce reads to pick
// between them).
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/http/ -run SellerDocsIT -v

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var docJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("pan-card"), 512)...)

// docPDF is a private KYC document as media-service's document-bytes serves it.
var docPDF = []byte("%PDF-1.7\n1 0 obj << /Type /Catalog >> endobj\ntrailer << >>\n%%EOF\n")

// docMedia is media-service's byte routes and its record route. calls records
// every byte read (image-bytes, document-bytes); records counts the record
// reads separately, so the byte-read assertions stay exact.
type docMedia struct {
	mu      sync.Mutex
	calls   []string
	headers []http.Header
	records int
	// per media id: "ok" (default), "404", "svg", "500"
	mode map[string]string
	// per media id: the file_type the record names; "image" by default.
	kind map[string]string
}

func (m *docMedia) handler(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	// v1 media <id>: the record, answered for every id as a ready, passed
	// asset of the configured kind.
	if len(parts) == 3 && parts[1] == "media" && r.Method == http.MethodGet {
		m.mu.Lock()
		m.records++
		kind := m.kind[parts[2]]
		m.mu.Unlock()
		if kind == "" {
			kind = "image"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"id": parts[2], "uploader_id": uuid.NewString(), "file_type": kind,
			"processing_status": "ready", "moderation_status": "passed",
		}})
		return
	}
	m.mu.Lock()
	m.calls = append(m.calls, r.URL.Path)
	m.headers = append(m.headers, r.Header.Clone())
	m.mu.Unlock()
	// v1 media internal <id> document-bytes
	if len(parts) == 5 && parts[2] == "internal" && parts[4] == "document-bytes" && r.Method == http.MethodGet {
		switch m.mode[parts[3]] {
		case "404":
			w.WriteHeader(http.StatusNotFound)
		case "html":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("<html><script>alert(1)</script></html>"))
		default:
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write(docPDF)
		}
		return
	}
	// v1 media internal <id> image-bytes
	if len(parts) != 5 || parts[2] != "internal" || parts[4] != "image-bytes" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	switch m.mode[parts[3]] {
	case "404":
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"MEDIA_NOT_FOUND"}}`))
	case "svg":
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(`<svg/>`))
	case "500":
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(docJPEG)
	}
}

type docRig struct {
	r      *gin.Engine
	signer *servicetoken.Signer
	media  *docMedia
}

func newDocRig(t *testing.T) *docRig {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                     "admin-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    joinPerms(),
	}
	v, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	dm := &docMedia{mode: map[string]string{}, kind: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(dm.handler))
	t.Cleanup(srv.Close)
	cipher, err := pii.New(devKeyProvider{}, []byte("seller-docs-slt!"))
	if err != nil {
		t.Fatal(err)
	}
	// commerce's own media token: document-bytes is never called without it.
	_, mediaPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	mediaSigner, err := media.ServiceTokenSigner("c1", mediaPriv)
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(postgres.New(edgePool), nil, "").WithPII(cipher).
		WithMedia(media.New(srv.URL, "media-internal-key").WithServiceToken(mediaSigner))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(FenceMiddleware())
	r.Use(RequireGatewayTrust(integrationInternalKey))
	h := New(svc).WithInternalKey(integrationInternalKey).WithServiceVerifier(v)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return &docRig{r: r, signer: signer, media: dm}
}

func (rg *docRig) get(t *testing.T, path, perm string) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := rg.signer.Mint(AudienceCommerce, "admin-console", []string{perm}, nil, time.Minute,
		servicetoken.WithActor(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	// A forged user header rides along; it must neither authorise anything
	// nor reach media-service.
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	rg.r.ServeHTTP(w, req)
	return w
}

type docSeed struct {
	seller, other                uuid.UUID
	pan, cheque, missing, theirs uuid.UUID // document ids
	panMedia, chequeMedia        uuid.UUID
}

func seedSellerDocs(t *testing.T) docSeed {
	t.Helper()
	ctx := context.Background()
	s := docSeed{
		seller: uuid.New(), other: uuid.New(),
		pan: uuid.New(), cheque: uuid.New(), missing: uuid.New(), theirs: uuid.New(),
		panMedia: uuid.New(), chequeMedia: uuid.New(),
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := edgePool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\nSQL: %s", err, sql)
		}
	}
	for _, id := range []uuid.UUID{s.seller, s.other} {
		exec(`INSERT INTO sellers (id,user_id,store_name,slug,email,state,status)
		      VALUES ($1,$2,'Docs Store',$3,'docs@example.test','KA','submitted')`, id, uuid.New(), "docs-"+id.String()[:8])
	}
	exec(`INSERT INTO seller_documents (id,seller_id,document_type,document_number,media_id,verification_status,uploaded_at)
	      VALUES ($1,$2,'pan_card','ABCDE1234F',$3,'pending','2026-09-30T10:00:00Z')`, s.pan, s.seller, s.panMedia)
	exec(`INSERT INTO seller_documents (id,seller_id,document_type,media_id,verification_status,uploaded_at)
	      VALUES ($1,$2,'cancelled_cheque',$3,'verified','2026-09-30T11:00:00Z')`, s.cheque, s.seller, s.chequeMedia)
	// media_id is NOT NULL in the schema; the all-zeroes id is how a row
	// with no usable upload looks, and it must read as not viewable.
	exec(`INSERT INTO seller_documents (id,seller_id,document_type,media_id,verification_status,uploaded_at)
	      VALUES ($1,$2,'address_proof',$3,'needs_correction','2026-09-30T12:00:00Z')`, s.missing, s.seller, uuid.Nil)
	exec(`INSERT INTO seller_documents (id,seller_id,document_type,media_id,verification_status,uploaded_at)
	      VALUES ($1,$2,'pan_card',$3,'pending','2026-09-30T10:00:00Z')`, s.theirs, s.other, uuid.New())
	return s
}

func TestSellerDocsIT_ListCarriesNoMediaIdNumberOrURL(t *testing.T) {
	rg := newDocRig(t)
	s := seedSellerDocs(t)
	w := rg.get(t, InternalAdminPrefix+"/sellers/"+s.seller.String()+"/documents", PermKYCVerify)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, leak := range []string{s.panMedia.String(), s.chequeMedia.String(), "ABCDE1234F", "http", "media_id", "document_number"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the list leaks %q: %s", leak, body)
		}
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 3 {
		t.Fatalf("got %d documents, want this seller's 3 (and not the other seller's): %s", len(env.Data), body)
	}
	want := map[string]struct {
		typ, status string
		viewable    bool
	}{
		s.pan.String():     {"pan_card", "pending", true},
		s.cheque.String():  {"cancelled_cheque", "verified", true},
		s.missing.String(): {"address_proof", "needs_correction", false},
	}
	for _, d := range env.Data {
		// back_viewable joined the wire shape in migration 045 (the back of a
		// voter ID or passport); none of these three documents has a back.
		if len(d) != 6 {
			t.Fatalf("row has fields %v, want exactly id, document_type, verification_status, uploaded_at, viewable, back_viewable", d)
		}
		if bv, ok := d["back_viewable"].(bool); !ok || bv {
			t.Fatalf("row %v: back_viewable must be false for a document with no back image", d)
		}
		w, ok := want[d["id"].(string)]
		if !ok {
			t.Fatalf("unexpected document %v", d["id"])
		}
		if d["document_type"] != w.typ || d["verification_status"] != w.status || d["viewable"] != w.viewable {
			t.Fatalf("row %v, want %+v", d, w)
		}
		if _, err := time.Parse(time.RFC3339, d["uploaded_at"].(string)); err != nil {
			t.Fatalf("uploaded_at %v is not RFC 3339", d["uploaded_at"])
		}
	}
	if len(rg.media.calls) != 0 {
		t.Fatalf("listing called media-service %d times; it must not", len(rg.media.calls))
	}
}

func TestSellerDocsIT_ListAnswersForMissingAndEmptySellers(t *testing.T) {
	rg := newDocRig(t)
	w := rg.get(t, InternalAdminPrefix+"/sellers/"+uuid.NewString()+"/documents", PermKYCVerify)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "SELLER_NOT_FOUND") {
		t.Fatalf("unknown seller: %d %s, want 404 SELLER_NOT_FOUND", w.Code, w.Body.String())
	}
	empty := uuid.New()
	if _, err := edgePool.Exec(context.Background(), `INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	      VALUES ($1,$2,'Empty Docs',$3,'e@example.test','KA')`, empty, uuid.New(), "edocs-"+empty.String()[:8]); err != nil {
		t.Fatal(err)
	}
	w = rg.get(t, InternalAdminPrefix+"/sellers/"+empty.String()+"/documents", PermKYCVerify)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("seller without documents: %d %s, want 200 with data []", w.Code, w.Body.String())
	}
}

func TestSellerDocsIT_ImageStreamsBytesInlineAndUncached(t *testing.T) {
	rg := newDocRig(t)
	s := seedSellerDocs(t)
	w := rg.get(t, InternalAdminPrefix+"/sellers/"+s.seller.String()+"/documents/"+s.pan.String()+"/image", PermKYCVerify)
	if w.Code != http.StatusOK {
		t.Fatalf("image: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), docJPEG) {
		t.Fatalf("got %d bytes, want the %d media-service sent", w.Body.Len(), len(docJPEG))
	}
	for k, want := range map[string]string{
		"Content-Type":           "image/jpeg",
		"Content-Disposition":    "inline",
		"Cache-Control":          "no-store, private",
		"X-Content-Type-Options": "nosniff",
		"Content-Length":         "4100",
	} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if len(rg.media.calls) != 1 || rg.media.calls[0] != "/v1/media/internal/"+s.panMedia.String()+"/image-bytes" {
		t.Fatalf("media calls = %v, want exactly the PAN card's image-bytes", rg.media.calls)
	}
	h := rg.media.headers[0]
	if h.Get("X-Internal-Service-Key") != "media-internal-key" {
		t.Error("media-service was not given the internal key")
	}
	for _, k := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		if _, present := h[http.CanonicalHeaderKey(k)]; present {
			t.Errorf("media-service received %s; it refuses the route with 403 USER_CALLER_REFUSED", k)
		}
	}
}

func TestSellerDocsIT_ImageRefusals(t *testing.T) {
	rg := newDocRig(t)
	s := seedSellerDocs(t)
	img := func(seller, doc uuid.UUID) *httptest.ResponseRecorder {
		return rg.get(t, InternalAdminPrefix+"/sellers/"+seller.String()+"/documents/"+doc.String()+"/image", PermKYCVerify)
	}
	expect := func(name string, w *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		if w.Code != status || !strings.Contains(w.Body.String(), `"code":"`+code+`"`) {
			t.Errorf("%s: %d %s, want %d %s", name, w.Code, w.Body.String(), status, code)
		}
		if ct := w.Header().Get("Content-Type"); strings.HasPrefix(ct, "image/") {
			t.Errorf("%s: an error answered with Content-Type %s", name, ct)
		}
	}

	expect("another seller's document", img(s.seller, s.theirs), http.StatusNotFound, "DOCUMENT_NOT_FOUND")
	expect("the other seller's id with this seller's document", img(s.other, s.pan), http.StatusNotFound, "DOCUMENT_NOT_FOUND")
	expect("an unknown document", img(s.seller, uuid.New()), http.StatusNotFound, "DOCUMENT_NOT_FOUND")
	expect("a row with no upload", img(s.seller, s.missing), http.StatusNotFound, "DOCUMENT_IMAGE_UNAVAILABLE")
	if len(rg.media.calls) != 0 {
		t.Fatalf("media-service was called %d times for documents commerce must refuse itself", len(rg.media.calls))
	}

	rg.media.mode[s.panMedia.String()] = "404"
	expect("media-service has no servable image", img(s.seller, s.pan), http.StatusNotFound, "DOCUMENT_IMAGE_UNAVAILABLE")
	rg.media.mode[s.panMedia.String()] = "svg"
	expect("media-service returns SVG", img(s.seller, s.pan), http.StatusBadGateway, "DOCUMENT_IMAGE_INVALID")
	rg.media.mode[s.panMedia.String()] = "500"
	expect("media-service is down", img(s.seller, s.pan), http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE")

	// Only kyc.verify opens it.
	w := rg.get(t, InternalAdminPrefix+"/sellers/"+s.seller.String()+"/documents/"+s.cheque.String()+"/image", PermSellersRead)
	if w.Code != http.StatusForbidden {
		t.Errorf("a sellers.read token opened a KYC image: %d", w.Code)
	}
}

// A private KYC document (31 Oct 2026): read through document-bytes with
// commerce's token, and handed on as an attachment under a sandboxing CSP,
// never inline.
func TestSellerDocsIT_PDFDocumentStreamsAsAnAttachment(t *testing.T) {
	rg := newDocRig(t)
	s := seedSellerDocs(t)
	rg.media.kind[s.panMedia.String()] = "document"
	w := rg.get(t, InternalAdminPrefix+"/sellers/"+s.seller.String()+"/documents/"+s.pan.String()+"/image", PermKYCVerify)
	if w.Code != http.StatusOK {
		t.Fatalf("document: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), docPDF) {
		t.Fatalf("got %d bytes, want the %d-byte PDF", w.Body.Len(), len(docPDF))
	}
	for k, want := range map[string]string{
		"Content-Type":            "application/pdf",
		"Content-Disposition":     `attachment; filename="document.pdf"`,
		"Cache-Control":           "no-store, private",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox",
	} {
		if got := w.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if len(rg.media.calls) != 1 || rg.media.calls[0] != "/v1/media/internal/"+s.panMedia.String()+"/document-bytes" {
		t.Fatalf("media byte reads = %v, want exactly the PAN card's document-bytes", rg.media.calls)
	}
	h := rg.media.headers[0]
	if h.Get("X-Internal-Service-Key") != "media-internal-key" || !strings.HasPrefix(h.Get("X-Service-Authorization"), "Bearer ") {
		t.Error("document-bytes was called without both the internal key and commerce's service token")
	}
	for _, k := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		if _, present := h[http.CanonicalHeaderKey(k)]; present {
			t.Errorf("media-service received %s", k)
		}
	}
}

func TestSellerDocsIT_PDFDocumentRefusals(t *testing.T) {
	rg := newDocRig(t)
	s := seedSellerDocs(t)
	open := func() *httptest.ResponseRecorder {
		return rg.get(t, InternalAdminPrefix+"/sellers/"+s.seller.String()+"/documents/"+s.pan.String()+"/image", PermKYCVerify)
	}
	rg.media.kind[s.panMedia.String()] = "document"

	rg.media.mode[s.panMedia.String()] = "html"
	if w := open(); w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "DOCUMENT_IMAGE_INVALID") ||
		strings.Contains(w.Body.String(), "<script>") {
		t.Errorf("HTML labelled PDF: %d %s, want 502 DOCUMENT_IMAGE_INVALID and no HTML passed on", w.Code, w.Body.String())
	}
	rg.media.mode[s.panMedia.String()] = "404"
	if w := open(); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "DOCUMENT_IMAGE_UNAVAILABLE") {
		t.Errorf("no servable document: %d %s, want 404 DOCUMENT_IMAGE_UNAVAILABLE", w.Code, w.Body.String())
	}
	// A kind a KYC row cannot hold is never opened.
	rg.media.mode[s.panMedia.String()] = ""
	rg.media.kind[s.panMedia.String()] = "video"
	before := len(rg.media.calls)
	if w := open(); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "DOCUMENT_IMAGE_UNAVAILABLE") {
		t.Errorf("a video: %d %s, want 404 DOCUMENT_IMAGE_UNAVAILABLE", w.Code, w.Body.String())
	}
	if len(rg.media.calls) != before {
		t.Errorf("a video asset was read through a byte route: %v", rg.media.calls[before:])
	}
}
