package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

const (
	kycListTpl  = service.CommerceAdminPrefix + "/sellers/:sellerId/documents"
	kycImageTpl = service.CommerceAdminPrefix + "/sellers/:sellerId/documents/:documentId/image"
)

// kycStubAnyDocument answers every seller's list with no documents, for the
// shared route-table cases.
func kycStubAnyDocument(rg *productsRig) {
	rg.onTemplate(http.MethodGet, kycListTpl, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
}

// kycDoc is one commerce list row as commerce sends it.
func kycListBody(rows ...map[string]any) []byte {
	b, _ := json.Marshal(map[string]any{"data": rows})
	return b
}

func kycRow(id, docType string, viewable bool) map[string]any {
	return map[string]any{"id": id, "document_type": docType, "verification_status": "pending",
		"uploaded_at": "2026-09-30T10:00:00Z", "viewable": viewable}
}

// kycImage is a stand-in document: every byte value, in an order a copy that
// dropped, reordered or re-encoded anything would not reproduce.
func kycImage(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*31 + i/251) % 256)
	}
	return b
}

type kycCase struct {
	seller, doc, docType string
	path                 string
}

func newKYCCase() kycCase {
	k := kycCase{seller: uuid.NewString(), doc: uuid.NewString(), docType: "pan_card"}
	k.path = commercePrefix + "/sellers/" + k.seller + "/documents/" + k.doc + "/view"
	return k
}

// stub installs commerce's list (with this document) and its image answer.
func (k kycCase) stub(rg *productsRig, viewable bool, image func(w http.ResponseWriter, r *http.Request)) {
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+k.seller+"/documents", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(kycListBody(kycRow(uuid.NewString(), "gst_certificate", true), kycRow(k.doc, k.docType, viewable)))
	})
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+k.seller+"/documents/"+k.doc+"/image", image)
}

func serveImage(ct string, body []byte, declareLength bool) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "no-store, private")
		if declareLength {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}
		// Chunked: flush in pieces so no length is known up front.
		f := w.(http.Flusher)
		for i := 0; i < len(body); i += 64 << 10 {
			end := min(i+64<<10, len(body))
			_, _ = w.Write(body[i:end])
			f.Flush()
		}
	}
}

func kycAdmin(rg *productsRig) string {
	a := uuid.NewString()
	rg.perms.grant(a, permKYCVerify)
	return a
}

// --- permission and step-up -------------------------------------------------

func TestKYCDocumentView_RequiresKYCVerify(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	k.stub(rg, true, serveImage("image/png", kycImage(1000), true))
	other := uuid.NewString()
	for _, p := range commerceAll {
		if p != permKYCVerify {
			rg.perms.grant(other, p)
		}
	}
	w := rg.do(http.MethodGet, k.path, "", other, true)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("without %s: %d %s", permKYCVerify, w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("a refused view reached commerce: %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Operation != opSellerKYCDocumentView || a[0].Actor != other {
		t.Fatalf("refusal audit = %+v", a)
	}
	// The list needs it too.
	w = rg.do(http.MethodGet, commercePrefix+"/sellers/"+k.seller+"/documents", "", other, true)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("list without %s: %d %s", permKYCVerify, w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("a refused list reached commerce: %+v", hits)
	}
}

func TestKYCDocumentView_RequiresStepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	k.stub(rg, true, serveImage("image/png", kycImage(1000), true))
	actor := kycAdmin(rg)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"missing": rg.do(http.MethodGet, k.path, "", actor, false),
		"stale":   rg.do(http.MethodGet, k.path, "", actor, false, stepUpAgo(adminauth.StepUpWindow+time.Minute)),
	} {
		if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
			t.Fatalf("%s step-up: %d %s", name, w.Code, w.Body.String())
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("a view without step-up reached commerce: %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) != 2 || a[0].Outcome != postgres.AuditOutcomeDenied || a[1].Outcome != postgres.AuditOutcomeDenied {
		t.Fatalf("step-up refusals audit = %+v", a)
	}
	// The list is an ordinary read: no step-up.
	w := rg.do(http.MethodGet, commercePrefix+"/sellers/"+k.seller+"/documents", "", actor, false)
	if w.Code != http.StatusOK {
		t.Fatalf("list without step-up: %d %s", w.Code, w.Body.String())
	}
}

// --- the view ---------------------------------------------------------------

func TestKYCDocumentView_StreamsBytesUnchangedWithPinnedHeaders(t *testing.T) {
	for _, declared := range []bool{true, false} {
		t.Run("content-length="+strconv.FormatBool(declared), func(t *testing.T) {
			rg := newProductsRig(t, true)
			k := newKYCCase()
			img := kycImage(300_000)
			k.stub(rg, true, serveImage("image/jpeg; charset=binary", img, declared))
			actor := kycAdmin(rg)

			w := rg.do(http.MethodGet, k.path, "", actor, true)
			if w.Code != http.StatusOK {
				t.Fatalf("view: %d %s", w.Code, w.Body.String())
			}
			if !bytes.Equal(w.Body.Bytes(), img) {
				t.Fatalf("bytes changed: got %d bytes, want %d", w.Body.Len(), len(img))
			}
			want := map[string]string{
				"Content-Type":                 "image/jpeg",
				"Content-Length":               strconv.Itoa(len(img)),
				"Content-Disposition":          "inline",
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
			for _, h := range []string{"Location", "Set-Cookie", "Content-Encoding"} {
				if w.Header().Get(h) != "" {
					t.Fatalf("unexpected %s header %q", h, w.Header().Get(h))
				}
			}

			hits := rg.takeHits()
			if len(hits) != 2 ||
				hits[0].path != service.CommerceAdminPrefix+"/sellers/"+k.seller+"/documents" ||
				hits[1].path != service.CommerceAdminPrefix+"/sellers/"+k.seller+"/documents/"+k.doc+"/image" {
				t.Fatalf("commerce hits = %+v", hits)
			}
			for _, h := range hits {
				if h.verifyErr != nil || h.aud != "commerce" || len(h.verified.Scope) != 1 || h.verified.Scope[0] != permKYCVerify ||
					h.verified.Actor != actor || h.userHdr != "" || h.keyHdr != "" {
					t.Fatalf("commerce call %s: aud=%s err=%v token=%+v", h.path, h.aud, h.verifyErr, h.verified)
				}
			}
		})
	}
}

func TestKYCDocumentView_WritesOneAuditRow(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	k.stub(rg, true, serveImage("image/png", kycImage(5000), true))
	actor := kycAdmin(rg)
	w := rg.do(http.MethodGet, k.path, "", actor, true)
	if w.Code != http.StatusOK {
		t.Fatalf("view: %d %s", w.Code, w.Body.String())
	}
	a := rg.takeAudit()
	if len(a) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1: %+v", len(a), a)
	}
	e := a[0]
	if e.Operation != "seller.kyc_document_view" || e.App != "commerce" || e.Actor != actor ||
		e.TargetType != "seller" || e.TargetID != k.seller || e.Outcome != postgres.AuditOutcomeSuccess ||
		e.StatusCode != http.StatusOK || e.RequestID != "req-product-1" {
		t.Fatalf("audit row = %+v", e)
	}
	if want := map[string]any{"document_id": k.doc, "document_type": "pan_card"}; !reflect.DeepEqual(e.Payload, want) {
		t.Fatalf("audit detail = %v, want %v", e.Payload, want)
	}
}

// firstByteProbe runs check when the response first starts (status or body).
type firstByteProbe struct {
	*httptest.ResponseRecorder
	check func()
	fired bool
}

func (p *firstByteProbe) fire() {
	if !p.fired {
		p.fired = true
		p.check()
	}
}
func (p *firstByteProbe) WriteHeader(code int) { p.fire(); p.ResponseRecorder.WriteHeader(code) }
func (p *firstByteProbe) Write(b []byte) (int, error) {
	p.fire()
	return p.ResponseRecorder.Write(b)
}

func TestKYCDocumentView_AuditRowExistsBeforeTheFirstByte(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	k.stub(rg, true, serveImage("image/webp", kycImage(4000), true))
	actor := kycAdmin(rg)

	req := httptest.NewRequest(http.MethodGet, k.path, nil)
	req.Header.Set("X-Request-Id", "req-product-1")
	req.Header.Set("X-User-Id", actor)
	req.Header.Set("X-Admin-MFA", "true")
	req.Header.Set("X-Step-Up-At", strconv.FormatInt(time.Now().Unix(), 10))
	rowsAtFirstByte := -1
	probe := &firstByteProbe{ResponseRecorder: httptest.NewRecorder(), check: func() {
		rg.rec.mu.Lock()
		rowsAtFirstByte = len(rg.rec.entries)
		rg.rec.mu.Unlock()
	}}
	rg.r.ServeHTTP(probe, req)
	if probe.Code != http.StatusOK {
		t.Fatalf("view: %d %s", probe.Code, probe.Body.String())
	}
	if rowsAtFirstByte != 1 {
		t.Fatalf("audit rows when the first byte left = %d, want 1 (audit before streaming)", rowsAtFirstByte)
	}
	if a := rg.takeAudit(); len(a) != 1 {
		t.Fatalf("audit rows after the view = %d, want exactly 1", len(a))
	}
}

func TestKYCDocumentView_BrokenStreamStillOneRow(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	img := kycImage(200_000)
	k.stub(rg, true, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(img)))
		_, _ = w.Write(img[:len(img)/2])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // commerce drops the connection part-way
	})
	actor := kycAdmin(rg)
	w := rg.do(http.MethodGet, k.path, "", actor, true)
	if w.Body.Len() >= len(img) || w.Header().Get("Content-Length") != strconv.Itoa(len(img)) {
		t.Fatalf("broken stream: sent %d of declared %s", w.Body.Len(), w.Header().Get("Content-Length"))
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Operation != opSellerKYCDocumentView || a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("broken stream audit = %+v, want exactly one view row", a)
	}
}

func TestKYCDocumentView_AuditFailureWithholdsTheDocument(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newKYCCase()
	img := kycImage(5000)
	k.stub(rg, true, serveImage("image/png", img, true))
	actor := kycAdmin(rg)
	rg.rec.mu.Lock()
	rg.rec.err = errors.New("audit store down")
	rg.rec.mu.Unlock()
	w := rg.do(http.MethodGet, k.path, "", actor, true)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeAuditUnavailable) {
		t.Fatalf("view without an audit row: %d %s", w.Code, w.Body.String())
	}
	if bytes.Contains(w.Body.Bytes(), img[:64]) || strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
		t.Fatal("the document was released without an audit row")
	}
}

func TestKYCDocumentView_NotFound(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := kycAdmin(rg)
	img := serveImage("image/png", kycImage(1000), true)

	// commerce's image route 404s (the document is not this seller's).
	k := newKYCCase()
	k.stub(rg, true, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"DOCUMENT_NOT_FOUND"}}`))
	})
	w := rg.do(http.MethodGet, k.path, "", actor, true)
	if w.Code != http.StatusNotFound || !hasCode(w, CodeDocumentNotFound) {
		t.Fatalf("image 404: %d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome == postgres.AuditOutcomeSuccess || a[0].StatusCode != http.StatusNotFound {
		t.Fatalf("image 404 audit = %+v", a)
	}
	rg.takeHits()

	// The document is not in the seller's list: commerce's image route is never called.
	k2 := newKYCCase()
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+k2.seller+"/documents", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(kycListBody(kycRow(uuid.NewString(), "pan_card", true)))
	})
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+k2.seller+"/documents/"+k2.doc+"/image", img)
	w = rg.do(http.MethodGet, k2.path, "", actor, true)
	if w.Code != http.StatusNotFound || !hasCode(w, CodeDocumentNotFound) {
		t.Fatalf("not listed: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 1 {
		t.Fatalf("not listed: commerce hits %+v, want the list only", hits)
	}
	rg.takeAudit()

	// Listed but not viewable (no media).
	k3 := newKYCCase()
	k3.stub(rg, false, img)
	w = rg.do(http.MethodGet, k3.path, "", actor, true)
	if w.Code != http.StatusNotFound || !hasCode(w, CodeDocumentNotFound) {
		t.Fatalf("not viewable: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 1 {
		t.Fatalf("not viewable: commerce hits %+v", hits)
	}
	rg.takeAudit()

	// Unknown seller: commerce's list 404s.
	k4 := newKYCCase()
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+k4.seller+"/documents", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"SELLER_NOT_FOUND"}}`))
	})
	if w = rg.do(http.MethodGet, k4.path, "", actor, true); w.Code != http.StatusNotFound {
		t.Fatalf("unknown seller: %d %s", w.Code, w.Body.String())
	}
	rg.takeHits()
	rg.takeAudit()

	// Ids that are not uuids never reach commerce.
	for _, p := range []string{
		commercePrefix + "/sellers/s-1/documents/" + uuid.NewString() + "/view",
		commercePrefix + "/sellers/" + uuid.NewString() + "/documents/..%2f/view",
		commercePrefix + "/sellers/" + uuid.NewString() + "/documents/x/view",
	} {
		if w := rg.do(http.MethodGet, p, "", actor, true); w.Code == http.StatusOK {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("bad ids reached commerce: %+v", hits)
	}
}

func TestKYCDocumentView_OversizeRefused(t *testing.T) {
	for _, declared := range []bool{true, false} {
		t.Run("content-length="+strconv.FormatBool(declared), func(t *testing.T) {
			rg := newProductsRig(t, true)
			k := newKYCCase()
			big := kycImage(maxKYCDocumentBytes + 1)
			k.stub(rg, true, serveImage("image/png", big, declared))
			actor := kycAdmin(rg)
			w := rg.do(http.MethodGet, k.path, "", actor, true)
			if w.Code != http.StatusBadGateway || !hasCode(w, CodeDocumentTooLarge) {
				t.Fatalf("oversize: %d %.200s", w.Code, w.Body.String())
			}
			if w.Body.Len() > 1024 || strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
				t.Fatalf("oversize released %d bytes as %s", w.Body.Len(), w.Header().Get("Content-Type"))
			}
			if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure {
				t.Fatalf("oversize audit = %+v", a)
			}
		})
	}
	// Exactly the cap is allowed.
	rg := newProductsRig(t, true)
	k := newKYCCase()
	k.stub(rg, true, serveImage("image/png", kycImage(maxKYCDocumentBytes), false))
	if w := rg.do(http.MethodGet, k.path, "", kycAdmin(rg), true); w.Code != http.StatusOK || w.Body.Len() != maxKYCDocumentBytes {
		t.Fatalf("at the cap: %d, %d bytes", w.Code, w.Body.Len())
	}
}

func TestKYCDocumentView_NeverRedirects(t *testing.T) {
	rg := newProductsRig(t, true)
	// Where a followed redirect would land: a perfectly good image.
	elsewhere := httptest.NewServer(http.HandlerFunc(serveImage("image/png", kycImage(1000), true)))
	t.Cleanup(elsewhere.Close)
	k := newKYCCase()
	k.stub(rg, true, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/signed?X-Amz-Signature=abc", http.StatusFound)
	})
	w := rg.do(http.MethodGet, k.path, "", kycAdmin(rg), true)
	if w.Code != http.StatusBadGateway || w.Header().Get("Location") != "" ||
		strings.Contains(w.Body.String(), elsewhere.URL) || strings.Contains(w.Body.String(), "download_url") {
		t.Fatalf("redirect: %d loc=%q %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
}

func TestKYCDocumentView_RefusesWhatIsNotAnImage(t *testing.T) {
	for _, ct := range []string{"image/svg+xml", "text/html", "application/json", ""} {
		rg := newProductsRig(t, true)
		k := newKYCCase()
		k.stub(rg, true, func(w http.ResponseWriter, _ *http.Request) {
			w.Header()["Content-Type"] = []string{ct}
			_, _ = w.Write([]byte(`<svg onload="alert(1)"/>`))
		})
		w := rg.do(http.MethodGet, k.path, "", kycAdmin(rg), true)
		if w.Code != http.StatusBadGateway || !hasCode(w, CodeDocumentUnsupported) || strings.Contains(w.Body.String(), "onload") {
			t.Fatalf("%q: %d %s", ct, w.Code, w.Body.String())
		}
	}
}

// --- the list ---------------------------------------------------------------

func TestKYCDocumentList_ReturnsOnlyThePinnedFields(t *testing.T) {
	rg := newProductsRig(t, true)
	seller, doc := uuid.NewString(), uuid.NewString()
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+seller+"/documents", func(w http.ResponseWriter, _ *http.Request) {
		row := kycRow(doc, "aadhaar", true)
		row["media_id"] = uuid.NewString()
		row["document_number"] = "XXXX-1234"
		row["url"] = "https://minio.example/kyc/1.jpg?sig=1"
		_, _ = w.Write(kycListBody(row, kycRow("not-a-uuid", "other", true)))
	})
	actor := kycAdmin(rg)
	w := rg.do(http.MethodGet, commercePrefix+"/sellers/"+seller+"/documents", "", actor, false)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || len(env.Data) != 1 {
		t.Fatalf("list body %s (%v)", w.Body.String(), err)
	}
	want := map[string]any{"id": doc, "document_type": "aadhaar", "verification_status": "pending",
		"uploaded_at": "2026-09-30T10:00:00Z", "viewable": true}
	if !reflect.DeepEqual(env.Data[0], want) {
		t.Fatalf("list row = %v, want %v", env.Data[0], want)
	}
	for _, leak := range []string{"media_id", "document_number", "minio", "XXXX"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("list leaked %q: %s", leak, w.Body.String())
		}
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store, private, max-age=0" {
		t.Fatalf("list Cache-Control %q", got)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Operation != opSellerKYCDocuments || a[0].TargetType != "seller" || a[0].TargetID != seller ||
		a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("list audit = %+v", a)
	}

	// Commerce's 404 for an unknown seller reaches the console as a 404.
	other := uuid.NewString()
	rg.on(http.MethodGet, service.CommerceAdminPrefix+"/sellers/"+other+"/documents", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"SELLER_NOT_FOUND","message":"Seller not found"}}`))
	})
	if w := rg.do(http.MethodGet, commercePrefix+"/sellers/"+other+"/documents", "", actor, false); w.Code != http.StatusNotFound {
		t.Fatalf("list 404: %d %s", w.Code, w.Body.String())
	}
}
