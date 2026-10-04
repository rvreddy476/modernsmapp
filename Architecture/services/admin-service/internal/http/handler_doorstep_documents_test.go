package http

import (
	"bytes"
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

// Doorstep professional documents, view-only (4 Oct 2026):
// GET /v1/admin/doorstep/documents/:id/view relays doorstep-service's
// GET /v1/doorstep/internal/admin/documents/:id/view the way the commerce KYC
// view relays commerce's image: doorstep:documents.review + step-up, one
// audit row per view written before the first byte, bytes unchanged, the
// pinned no-store headers.

type doorstepDocCase struct {
	doc, path, upstream string
}

func newDoorstepDocCase() doorstepDocCase {
	d := uuid.NewString()
	return doorstepDocCase{
		doc:      d,
		path:     doorstepPrefix + "/documents/" + d + "/view",
		upstream: service.DoorstepAdminPrefix + "/documents/" + d + "/view",
	}
}

func (k doorstepDocCase) stub(rg *productsRig, fn func(w http.ResponseWriter, r *http.Request)) {
	rg.on(http.MethodGet, k.upstream, fn)
}

func doorstepReviewer(rg *productsRig) string {
	a := uuid.NewString()
	rg.perms.grant(a, permDoorstepDocumentsReview)
	return a
}

func TestDoorstepDocumentView_RequiresDocumentsReview(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newDoorstepDocCase()
	k.stub(rg, serveImage("image/png", kycImage(1000), true))
	other := uuid.NewString()
	for _, p := range doorstepAll {
		if p != permDoorstepDocumentsReview {
			rg.perms.grant(other, p)
		}
	}
	w := rg.do(http.MethodGet, k.path, "", other, true)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("without %s: %d %s", permDoorstepDocumentsReview, w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("a refused view reached doorstep: %+v", hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Operation != opDoorstepDocumentView || a[0].Actor != other {
		t.Fatalf("refusal audit = %+v", a)
	}
}

func TestDoorstepDocumentView_RequiresStepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newDoorstepDocCase()
	k.stub(rg, serveImage("image/png", kycImage(1000), true))
	actor := doorstepReviewer(rg)
	for name, w := range map[string]*httptest.ResponseRecorder{
		"missing": rg.do(http.MethodGet, k.path, "", actor, false),
		"stale":   rg.do(http.MethodGet, k.path, "", actor, false, stepUpAgo(adminauth.StepUpWindow+time.Minute)),
	} {
		if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
			t.Fatalf("%s step-up: %d %s", name, w.Code, w.Body.String())
		}
		if strings.HasPrefix(w.Header().Get("Content-Type"), "image/") {
			t.Fatalf("%s step-up: an image was released", name)
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("a view without step-up reached doorstep: %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) != 2 || a[0].Outcome != postgres.AuditOutcomeDenied || a[1].Outcome != postgres.AuditOutcomeDenied ||
		a[0].Actor != actor || a[0].Operation != opDoorstepDocumentView {
		t.Fatalf("step-up refusals audit = %+v", a)
	}
	// With a fresh step-up the same admin sees it.
	if w := rg.do(http.MethodGet, k.path, "", actor, true); w.Code != http.StatusOK {
		t.Fatalf("with step-up: %d %s", w.Code, w.Body.String())
	}
}

func TestDoorstepDocumentView_StreamsBytesUnchangedWithPinnedHeaders(t *testing.T) {
	for _, tc := range []struct {
		ct, want string
		declared bool
	}{
		{"image/jpeg", "image/jpeg", true},
		{"image/jpeg; charset=binary", "image/jpeg", false},
		{"image/avif", "image/avif", true},
		{"image/gif", "image/gif", false},
	} {
		t.Run(tc.ct+" content-length="+strconv.FormatBool(tc.declared), func(t *testing.T) {
			rg := newProductsRig(t, true)
			k := newDoorstepDocCase()
			img := kycImage(300_000)
			k.stub(rg, serveImage(tc.ct, img, tc.declared))
			actor := doorstepReviewer(rg)

			w := rg.do(http.MethodGet, k.path, "", actor, true)
			if w.Code != http.StatusOK {
				t.Fatalf("view: %d %s", w.Code, w.Body.String())
			}
			if !bytes.Equal(w.Body.Bytes(), img) {
				t.Fatalf("bytes changed: got %d bytes, want %d", w.Body.Len(), len(img))
			}
			want := map[string]string{
				"Content-Type":                 tc.want,
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
			if len(hits) != 1 || hits[0].method != http.MethodGet || hits[0].path != k.upstream {
				t.Fatalf("doorstep hits = %+v, want one GET %s", hits, k.upstream)
			}
			h := hits[0]
			if h.verifyErr != nil || h.aud != "doorstep" || len(h.verified.Scope) != 1 || h.verified.Scope[0] != permDoorstepDocumentsReview ||
				h.verified.Actor != actor || h.userHdr != "" || h.keyHdr != "" {
				t.Fatalf("doorstep call: aud=%s err=%v token=%+v user=%q key=%v", h.aud, h.verifyErr, h.verified, h.userHdr, h.keyHdr != "")
			}
		})
	}
}

func TestDoorstepDocumentView_WritesOneAuditRow(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newDoorstepDocCase()
	k.stub(rg, serveImage("image/png", kycImage(5000), true))
	actor := doorstepReviewer(rg)
	for i := 0; i < 2; i++ { // one row per view, every view
		if w := rg.do(http.MethodGet, k.path, "", actor, true); w.Code != http.StatusOK {
			t.Fatalf("view %d: %d %s", i, w.Code, w.Body.String())
		}
		a := rg.takeAudit()
		if len(a) != 1 {
			t.Fatalf("view %d: audit rows = %d, want exactly 1: %+v", i, len(a), a)
		}
		e := a[0]
		if e.Operation != opDoorstepDocumentView || e.App != doorstepAuditApp || e.Actor != actor ||
			e.TargetType != "doorstep_document" || e.TargetID != k.doc || e.Outcome != postgres.AuditOutcomeSuccess ||
			e.StatusCode != http.StatusOK || e.RequestID != "req-product-1" {
			t.Fatalf("view %d: audit row = %+v", i, e)
		}
		if len(e.Payload) != 0 && !reflect.DeepEqual(e.Payload, map[string]any{}) {
			t.Fatalf("view %d: audit detail = %v, want none (no media id, no URL)", i, e.Payload)
		}
	}
}

func TestDoorstepDocumentView_AuditRowExistsBeforeTheFirstByte(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newDoorstepDocCase()
	k.stub(rg, serveImage("image/webp", kycImage(4000), true))
	actor := doorstepReviewer(rg)

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

func TestDoorstepDocumentView_AuditFailureWithholdsTheDocument(t *testing.T) {
	rg := newProductsRig(t, true)
	k := newDoorstepDocCase()
	img := kycImage(5000)
	k.stub(rg, serveImage("image/png", img, true))
	actor := doorstepReviewer(rg)
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

func TestDoorstepDocumentView_UpstreamRefusals(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := doorstepReviewer(rg)
	jsonErr := func(status int, code string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"code":"` + code + `","message":"x"}}`))
		}
	}
	for _, tc := range []struct {
		name     string
		fn       func(http.ResponseWriter, *http.Request)
		status   int
		code     string
		upstream int
	}{
		{"not found", jsonErr(http.StatusNotFound, "DOORSTEP_NOT_FOUND"), http.StatusNotFound, CodeDocumentNotFound, http.StatusNotFound},
		{"scope refused", jsonErr(http.StatusForbidden, "ADMIN_PERMISSION_NOT_IN_TOKEN"), http.StatusBadGateway, CodeUpstreamError, http.StatusForbidden},
		{"media down", jsonErr(http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE"), http.StatusBadGateway, CodeUpstreamError, http.StatusServiceUnavailable},
	} {
		k := newDoorstepDocCase()
		k.stub(rg, tc.fn)
		w := rg.do(http.MethodGet, k.path, "", actor, true)
		if w.Code != tc.status || !hasCode(w, tc.code) {
			t.Fatalf("%s: %d %s, want %d %s", tc.name, w.Code, w.Body.String(), tc.status, tc.code)
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome == postgres.AuditOutcomeSuccess ||
			a[0].Payload["upstream_status"] != tc.upstream || a[0].TargetID != k.doc {
			t.Fatalf("%s: audit = %+v", tc.name, a)
		}
		rg.takeHits()
	}

	// Ids that are not uuids never reach doorstep.
	for _, p := range []string{
		doorstepPrefix + "/documents/d-1/view",
		doorstepPrefix + "/documents/..%2f/view",
	} {
		if w := rg.do(http.MethodGet, p, "", actor, true); w.Code == http.StatusOK {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("bad ids reached doorstep: %+v", hits)
	}
}

func TestDoorstepDocumentView_OversizeRefused(t *testing.T) {
	for _, declared := range []bool{true, false} {
		t.Run("content-length="+strconv.FormatBool(declared), func(t *testing.T) {
			rg := newProductsRig(t, true)
			k := newDoorstepDocCase()
			k.stub(rg, serveImage("image/png", kycImage(maxKYCDocumentBytes+1), declared))
			w := rg.do(http.MethodGet, k.path, "", doorstepReviewer(rg), true)
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
}

func TestDoorstepDocumentView_NeverRedirects(t *testing.T) {
	rg := newProductsRig(t, true)
	elsewhere := httptest.NewServer(http.HandlerFunc(serveImage("image/png", kycImage(1000), true)))
	t.Cleanup(elsewhere.Close)
	k := newDoorstepDocCase()
	k.stub(rg, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/signed?X-Amz-Signature=abc", http.StatusFound)
	})
	w := rg.do(http.MethodGet, k.path, "", doorstepReviewer(rg), true)
	if w.Code != http.StatusBadGateway || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), elsewhere.URL) {
		t.Fatalf("redirect: %d loc=%q %s", w.Code, w.Header().Get("Location"), w.Body.String())
	}
}

func TestDoorstepDocumentView_RefusesWhatIsNotAnImage(t *testing.T) {
	for _, ct := range []string{"image/svg+xml", "text/html", "application/json", ""} {
		rg := newProductsRig(t, true)
		k := newDoorstepDocCase()
		k.stub(rg, func(w http.ResponseWriter, _ *http.Request) {
			w.Header()["Content-Type"] = []string{ct}
			_, _ = w.Write([]byte(`<svg onload="alert(1)"/>`))
		})
		w := rg.do(http.MethodGet, k.path, "", doorstepReviewer(rg), true)
		if w.Code != http.StatusBadGateway || !hasCode(w, CodeDocumentUnsupported) || strings.Contains(w.Body.String(), "onload") {
			t.Fatalf("%q: %d %s", ct, w.Code, w.Body.String())
		}
	}
}
