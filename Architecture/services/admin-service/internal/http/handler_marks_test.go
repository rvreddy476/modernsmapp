package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// marksAll is profile-service's console-family permissions (contract §1).
var marksAll = []string{permMarksRead, permMarksReview, permGovInvite}

// marksRig is a productsRig wired with only the Marks page: a profile stub
// (audience "profile") and media-service's document-bytes stub (audience "media"),
// plus /me and the approvals routes. h is kept for executor unit checks.
type marksRig struct {
	*productsRig
	h *Handler
}

func newMarksRig(t *testing.T) *marksRig {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	rg := &productsRig{
		rec: &fakeRecorder{}, perms: &fakePerms{byUser: map[string]adminauth.Permissions{}},
		holders: &fakeHolders{n: 1}, store: newMemStore(), respond: map[string]func(http.ResponseWriter, *http.Request){},
		respondHit:      map[string]func(http.ResponseWriter, *http.Request, productHit){},
		respondTemplate: map[string]func(http.ResponseWriter, *http.Request){},
	}
	stub := func(aud string, perms []string) string {
		v := servicetoken.NewVerifier(aud)
		if err := v.RegisterBase64("admin-service", "a1", pub, perms, nil); err != nil {
			t.Fatal(err)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			verified, verr := v.Verify(strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer "), "", "")
			hit := productHit{
				aud: aud, method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(b),
				requestID: r.Header.Get("X-Request-Id"), userHdr: r.Header.Get("X-User-Id"), keyHdr: r.Header.Get("X-Internal-Service-Key"),
				verified: verified, verifyErr: verr,
			}
			rg.mu.Lock()
			rg.hits = append(rg.hits, hit)
			custom := rg.respond[r.Method+" "+r.URL.Path]
			if custom == nil {
				for k, fn := range rg.respondTemplate {
					if m, tpl, _ := strings.Cut(k, " "); m == r.Method && pathMatches(tpl, r.URL.Path) {
						custom = fn
					}
				}
			}
			rg.mu.Unlock()
			if verr != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if custom != nil {
				custom(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	profileURL := stub("profile", marksAll)
	// The literal contract operation (section 6), not the constant under test.
	mediaURL := stub("media", []string{"media:document-bytes.read"})
	signer, err := servicetoken.NewSignerFromBase64("admin-service", "a1", priv)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	rg.r = gin.New()
	rg.r.Use(middleware.RequestID())
	rg.gate = NewGate(rg.perms, rg.rec, true)
	h := New(&stubAdminService{}, rg.gate, approvals.NewService(rg.store, rg.holders))
	h.WithMediaDocuments(service.NewMediaDocumentClient(mediaURL, marksTestInternalKey, signer)).WithProfile(service.NewProfileConsoleClient(profileURL, signer))
	h.RegisterMeRoute(rg.r)
	h.RegisterApprovalRoutes(rg.r)
	h.RegisterMarksRoutes(rg.r)
	if err := rg.gate.VerifyDeclared(rg.r); err != nil {
		t.Fatal(err)
	}
	if err := rg.gate.VerifyExecutors(h.approvals.HasExecutor); err != nil {
		t.Fatal(err)
	}
	return &marksRig{productsRig: rg, h: h}
}

func marksRoute(t *testing.T, op string) productRoute {
	t.Helper()
	for _, rt := range MarksRoutes {
		if rt.operation == op {
			return rt
		}
	}
	t.Fatalf("no marks route %s", op)
	return productRoute{}
}

// The whole route table registers in the real wiring (RegisterAllRoutes),
// declared under the platform app with the contract's permissions.
func TestMarks_RegisteredInTheFullTable(t *testing.T) {
	rg := newProductsRig(t, true)
	want := map[string]string{
		"GET /v1/admin/marks/stats":                                                  permMarksRead,
		"GET /v1/admin/marks/organisation/applications":                              permMarksRead,
		"GET /v1/admin/marks/organisation/applications/:applicationId":               permMarksRead,
		"GET /v1/admin/marks/organisation/applications/:applicationId/document/view": permMarksReview,
		"POST /v1/admin/marks/organisation/applications/:applicationId/review":       permMarksReview,
		"GET /v1/admin/marks/government/invitations":                                 permMarksRead,
		"POST /v1/admin/marks/government/invitations":                                permGovInvite,
		"POST /v1/admin/marks/government/invitations/:invitationId/revoke":           permGovInvite,
	}
	for k, perm := range want {
		m, p, _ := strings.Cut(k, " ")
		req, ok := rg.gate.Requirement(m, p)
		if !ok || req.Permission != perm || req.app() != "platform" {
			t.Fatalf("%s: declared=%v permission=%q app=%q", k, ok, req.Permission, req.app())
		}
		if !adminauth.KnownPermission(perm) {
			t.Fatalf("%s missing from the catalogue mirror", perm)
		}
	}
	create, _ := rg.gate.Requirement(http.MethodPost, "/v1/admin/marks/government/invitations")
	if !create.TwoPerson || !create.StepUp {
		t.Fatalf("invitation create must be step-up and two-person: %+v", create)
	}
	// Without the profile client wired, the family answers 503, never 2xx.
	a := uuid.NewString()
	rg.perms.grant(a, marksAll...)
	if w := rg.do(http.MethodGet, "/v1/admin/marks/stats", "", a, false); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired profile: %d %s", w.Code, w.Body.String())
	}
}

// Forwarded routes: refused without the permission (audited, profile never
// called), forwarded with exactly the permission as scope, act = the admin.
func TestMarks_ForwardedRouteTable(t *testing.T) {
	rg := newMarksRig(t)
	bodies := map[string]string{
		opMarksAppReview:    `{"decision":"approve","reason":"registry and domain match"}`,
		opMarksInviteRevoke: `{"reason":"sent to the wrong body"}`,
	}
	for _, rt := range MarksRoutes {
		if rt.operation == opMarksDocumentView || rt.operation == opMarksInviteCreate {
			continue
		}
		t.Run(rt.operation, func(t *testing.T) {
			routeTableCase(t, rg.productsRig, marksPrefix, service.ProfileAdminPrefix, "profile", "platform", marksAll, rt, bodies[rt.operation])
		})
	}
}

func TestMarks_StepUp(t *testing.T) {
	rg := newMarksRig(t)
	rg.holders.n = 1
	for _, rt := range MarksRoutes {
		body := `{"reason":"r","decision":"approve","user_id":"` + uuid.NewString() + `","official_name":"Ministry"}`
		stepUpCase(t, rg.productsRig, marksPrefix, marksAll, rt, body, rt.stepUp)
	}
}

func TestMarks_DetailIsAnAuditedUncachedRead(t *testing.T) {
	rg := newMarksRig(t)
	app := uuid.NewString()
	actor := uuid.NewString()
	rg.perms.grant(actor, permMarksRead)
	w := rg.do(http.MethodGet, marksPrefix+"/organisation/applications/"+app, "", actor, false)
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("detail: %d cache=%q", w.Code, w.Header().Get("Cache-Control"))
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Operation != opMarksAppRead || a[0].TargetType != marksTargetApplication || a[0].TargetID != app ||
		a[0].App != "platform" || a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].Actor != actor {
		t.Fatalf("detail audit %+v", a)
	}
	// Moderators and support hold none of the marks permissions.
	mod := uuid.NewString()
	rg.perms.grant(mod, "platform:users.read", "platform:users.search")
	if w := rg.do(http.MethodGet, marksPrefix+"/organisation/applications/"+app, "", mod, false); w.Code != http.StatusForbidden {
		t.Fatalf("moderator read the detail: %d", w.Code)
	}
	rg.takeHits()
}

func TestMarks_ReviewAuditsTheDecision(t *testing.T) {
	rg := newMarksRig(t)
	app := uuid.NewString()
	actor := uuid.NewString()
	rg.perms.grant(actor, permMarksReview)
	w := rg.do(http.MethodPost, marksPrefix+"/organisation/applications/"+app+"/review", `{"decision":"reject","reason":"domain unproven"}`, actor, true)
	if w.Code != http.StatusOK {
		t.Fatalf("review: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].body != `{"decision":"reject","reason":"domain unproven"}` {
		t.Fatalf("review body not forwarded as sent: %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) == 0 || a[len(a)-1].Payload["decision"] != "reject" || a[len(a)-1].Reason != "domain unproven" || a[len(a)-1].TargetID != app {
		t.Fatalf("review audit %+v", a)
	}
}

// marksTestInternalKey is a test-only stand-in for media's internal group key.
const marksTestInternalKey = "marks-test-internal-key"

// Bodies that start with their type's magic bytes.
var (
	marksPNG  = append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, []byte("registration-certificate")...)
	marksJPEG = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, []byte("registration-certificate")...)
	marksPDF  = []byte("%PDF-1.7 registration certificate")
)

// docCase stubs profile's detail and document routes and media's
// document-bytes route, which serves only when owner_id is the applicant
// (media-service's own binding, mirrored so a wrong owner is its 404).
type docCase struct {
	app, owner, media string
}

func marksDocumentBytesPath(media string) string {
	return "/v1/media/internal/" + media + "/document-bytes"
}

func newDocCase(rg *marksRig, ct string, body []byte) docCase {
	d := docCase{app: uuid.NewString(), owner: uuid.NewString(), media: uuid.NewString()}
	base := service.ProfileAdminPrefix + "/marks/organisation/applications/" + d.app
	rg.on(http.MethodGet, base, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"application_id":"` + d.app + `","user_id":"` + d.owner + `","has_document":true}}`))
	})
	rg.on(http.MethodGet, base+"/document", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"document_media_id":"` + d.media + `"}}`))
	})
	serve := serveImage(ct, body, true)
	rg.on(http.MethodGet, marksDocumentBytesPath(d.media), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("owner_id") != d.owner {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"MEDIA_NOT_FOUND","message":"Media not found"}}`))
			return
		}
		serve(w, r)
	})
	return d
}

func (d docCase) path() string {
	return marksPrefix + "/organisation/applications/" + d.app + "/document/view"
}

func reviewer(rg *marksRig) string {
	a := uuid.NewString()
	rg.perms.grant(a, permMarksRead, permMarksReview)
	return a
}

// lastAudit is the request's own row: other routes may add rows of their
// own, so the tests read the last one rather than counting.
func lastAudit(t *testing.T, a []postgres.AdminAuditEntry) postgres.AdminAuditEntry {
	t.Helper()
	if len(a) == 0 {
		t.Fatal("no audit row")
	}
	return a[len(a)-1]
}

// documentViewRows counts the rows that record a successful document view.
func documentViewRows(a []postgres.AdminAuditEntry) int {
	n := 0
	for _, e := range a {
		if e.Operation == opMarksDocumentView && e.Outcome == postgres.AuditOutcomeSuccess {
			n++
		}
	}
	return n
}

func TestMarks_DocumentViewStreamsWithTheKYCHeaders(t *testing.T) {
	rg := newMarksRig(t)
	d := newDocCase(rg, "image/png", marksPNG)
	actor := reviewer(rg)
	w := rg.do(http.MethodGet, d.path(), "", actor, true)
	if w.Code != http.StatusOK || w.Body.String() != string(marksPNG) {
		t.Fatalf("view: %d %q", w.Code, w.Body.String())
	}
	if w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Length") != strconv.Itoa(len(marksPNG)) {
		t.Fatalf("type/length %q %q", w.Header().Get("Content-Type"), w.Header().Get("Content-Length"))
	}
	for _, kv := range kycViewHeaders {
		if got := w.Header().Get(kv[0]); got != kv[1] {
			t.Fatalf("%s = %q, want %q", kv[0], got, kv[1])
		}
	}
	// The media id never reaches the console: not in the body, not in a header.
	if strings.Contains(w.Body.String(), d.media) {
		t.Fatal("the body carries the media id")
	}
	for k, vs := range w.Header() {
		for _, v := range vs {
			if strings.Contains(v, d.media) {
				t.Fatalf("header %s carries the media id", k)
			}
		}
	}
	hits := rg.takeHits()
	if len(hits) != 3 {
		t.Fatalf("hits %+v", hits)
	}
	wantScope := []string{"platform:official_marks.read", "platform:official_marks.review", "media:document-bytes.read"}
	wantAud := []string{"profile", "profile", "media"}
	for i, h := range hits {
		if h.verifyErr != nil || h.aud != wantAud[i] || h.verified.Actor != actor || len(h.verified.Scope) != 1 || h.verified.Scope[0] != wantScope[i] ||
			h.userHdr != "" {
			t.Fatalf("hit %d unsafe: aud=%s path=%s err=%v", i, h.aud, h.path, h.verifyErr)
		}
	}
	// Profile never gets the internal key; media's internal group does.
	if hits[0].keyHdr != "" || hits[1].keyHdr != "" {
		t.Fatal("the internal key was sent to profile")
	}
	if hits[2].keyHdr != marksTestInternalKey {
		t.Fatal("media's document-bytes request did not carry the internal key")
	}
	q, _ := url.ParseQuery(hits[2].query)
	if hits[2].path != marksDocumentBytesPath(d.media) || q.Get("owner_id") != d.owner || len(q) != 1 {
		t.Fatalf("media request %s ?%s", hits[2].path, hits[2].query)
	}
	a := rg.takeAudit()
	row := lastAudit(t, a)
	if row.Operation != opMarksDocumentView || row.Outcome != postgres.AuditOutcomeSuccess ||
		row.TargetType != marksTargetApplication || row.TargetID != d.app || row.Actor != actor || documentViewRows(a) != 1 {
		t.Fatalf("view audit %+v", a)
	}
	if raw, _ := json.Marshal(a); strings.Contains(string(raw), d.media) {
		t.Fatalf("audit rows carry the media id: %s", raw)
	}

	// Every view is one more row.
	rg.do(http.MethodGet, d.path(), "", actor, true)
	if a := rg.takeAudit(); documentViewRows(a) != 1 || lastAudit(t, a).Operation != opMarksDocumentView {
		t.Fatalf("second view audit %+v", a)
	}
	rg.takeHits()
}

func TestMarks_DocumentViewRefusals(t *testing.T) {
	rg := newMarksRig(t)
	d := newDocCase(rg, "image/jpeg", marksJPEG)

	// review without read: refused before profile is called.
	only := uuid.NewString()
	rg.perms.grant(only, permMarksReview)
	if w := rg.do(http.MethodGet, d.path(), "", only, true); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("review-only: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("review-only reached profile: %+v", hits)
	}
	if a := lastAudit(t, rg.takeAudit()); a.Outcome != postgres.AuditOutcomeDenied {
		t.Fatalf("review-only audit %+v", a)
	}

	// read without review: the gate refuses.
	reader := uuid.NewString()
	rg.perms.grant(reader, permMarksRead)
	if w := rg.do(http.MethodGet, d.path(), "", reader, true); w.Code != http.StatusForbidden {
		t.Fatalf("read-only viewed the document: %d", w.Code)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("read-only reached profile")
	}
	rg.takeAudit()

	actor := reviewer(rg)
	// No document: 404 DOCUMENT_NOT_FOUND, media never asked.
	rg.on(http.MethodGet, service.ProfileAdminPrefix+"/marks/organisation/applications/"+d.app+"/document", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"DOCUMENT_NOT_FOUND","message":"none"}}`))
	})
	if w := rg.do(http.MethodGet, d.path(), "", actor, true); w.Code != http.StatusNotFound || !hasCode(w, CodeDocumentNotFound) {
		t.Fatalf("no document: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 2 {
		t.Fatalf("no document hits %+v", hits)
	}
	if a := rg.takeAudit(); lastAudit(t, a).Outcome == postgres.AuditOutcomeSuccess || documentViewRows(a) != 0 {
		t.Fatalf("no document audit %+v", a)
	}
}

// The owner named to media is the applicant profile answered: a document
// media says belongs to someone else is a 404, never bytes, never a view row.
func TestMarks_DocumentViewOwnerIsTheApplicant(t *testing.T) {
	rg := newMarksRig(t)
	d := newDocCase(rg, "image/png", marksPNG)
	// Profile's detail now names a different applicant than the uploader.
	other := uuid.NewString()
	rg.on(http.MethodGet, service.ProfileAdminPrefix+"/marks/organisation/applications/"+d.app, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"application_id":"` + d.app + `","user_id":"` + other + `","has_document":true}}`))
	})
	w := rg.do(http.MethodGet, d.path(), "", reviewer(rg), true)
	if w.Code != http.StatusNotFound || !hasCode(w, CodeDocumentNotFound) || strings.Contains(w.Body.String(), "registration-certificate") {
		t.Fatalf("another owner: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 3 {
		t.Fatalf("hits %+v", hits)
	}
	if q, _ := url.ParseQuery(hits[2].query); q.Get("owner_id") != other {
		t.Fatalf("owner_id sent %q, want the applicant profile named", q.Get("owner_id"))
	}
	if a := rg.takeAudit(); documentViewRows(a) != 0 || lastAudit(t, a).Outcome == postgres.AuditOutcomeSuccess {
		t.Fatalf("a refused view was audited as a view: %+v", a)
	}
}

func TestMarks_DocumentViewWithoutMediaClientIs503(t *testing.T) {
	rg := newMarksRig(t)
	rg.h.WithMediaDocuments(nil)
	d := newDocCase(rg, "image/png", marksPNG)
	w := rg.do(http.MethodGet, d.path(), "", reviewer(rg), true)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "registration-certificate") {
		t.Fatalf("no media client: %d %s", w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); documentViewRows(a) != 0 {
		t.Fatalf("no media client audited a view: %+v", a)
	}
}

func TestMarks_DocumentViewTypesAndAudit(t *testing.T) {
	cases := []struct {
		name, ct string
		body     []byte
		code     int
	}{
		{"pdf", "application/pdf", marksPDF, http.StatusOK},
		{"png", "image/png", marksPNG, http.StatusOK},
		{"jpeg", "image/jpeg", marksJPEG, http.StatusOK},
		{"webp", "image/webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), http.StatusBadGateway},
		{"html", "text/html", []byte("<script>x</script>"), http.StatusBadGateway},
		{"svg", "image/svg+xml", []byte("<svg/>"), http.StatusBadGateway},
		{"fake pdf", "application/pdf", []byte("<html>not a pdf"), http.StatusBadGateway},
		{"fake png", "image/png", []byte("<html>not a png at all"), http.StatusBadGateway},
		{"fake jpeg", "image/jpeg", []byte("<html>not a jpeg at all"), http.StatusBadGateway},
		{"png labelled jpeg", "image/jpeg", marksPNG, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rg := newMarksRig(t)
			d := newDocCase(rg, tc.ct, tc.body)
			w := rg.do(http.MethodGet, d.path(), "", reviewer(rg), true)
			if w.Code != tc.code {
				t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body.String())
			}
			a := rg.takeAudit()
			if tc.code != http.StatusOK {
				if !hasCode(w, CodeDocumentUnsupported) {
					t.Fatalf("%s: %s, want %s", tc.name, w.Body.String(), CodeDocumentUnsupported)
				}
				if strings.Contains(w.Body.String(), string(tc.body)) {
					t.Fatal("refused document bytes were relayed")
				}
				if documentViewRows(a) != 0 {
					t.Fatalf("a refused document was audited as a view: %+v", a)
				}
				return
			}
			if w.Body.String() != string(tc.body) || documentViewRows(a) != 1 {
				t.Fatalf("%s: body or audit wrong: %d view rows", tc.name, documentViewRows(a))
			}
			if tc.ct == "application/pdf" && w.Header().Get("Content-Disposition") != kycPDFDisposition {
				t.Fatalf("pdf disposition %q", w.Header().Get("Content-Disposition"))
			}
		})
	}
	// Over the size cap: refused whole.
	rg := newMarksRig(t)
	big := append(append([]byte{}, marksPNG...), make([]byte, maxKYCDocumentBytes)...)
	d := newDocCase(rg, "image/png", big)
	if w := rg.do(http.MethodGet, d.path(), "", reviewer(rg), true); w.Code != http.StatusBadGateway || !hasCode(w, CodeDocumentTooLarge) {
		t.Fatalf("oversize: %d", w.Code)
	}
	// No audit row, no bytes.
	rg2 := newMarksRig(t)
	d2 := newDocCase(rg2, "image/png", marksPNG)
	rg2.rec.err = context.DeadlineExceeded
	if w := rg2.do(http.MethodGet, d2.path(), "", reviewer(rg2), true); w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "registration-certificate") {
		t.Fatalf("document escaped without an audit row: %d", w.Code)
	}
}

const testInviteCode = "GOV-INVITE-7F3K-9QXZ"

func inviteBody(user string) string {
	return `{"user_id":"` + user + `","official_name":"Ministry of Examples","reason":"verified by letter"}`
}

func TestMarks_InvitationIsTwoPersonAndTheCodeIsNotStored(t *testing.T) {
	rg := newMarksRig(t)
	rg.holders.n = 1
	requester, approver, invitee := uuid.NewString(), uuid.NewString(), uuid.NewString()
	rg.perms.grant(requester, permGovInvite)
	rg.perms.grant(approver, permGovInvite)
	rg.on(http.MethodPost, service.ProfileAdminPrefix+"/marks/government/invitations", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"invitation_id":"` + uuid.NewString() + `","invite_code":"` + testInviteCode + `","expires_at":"2026-11-23T00:00:00Z"}}`))
	})

	w := rg.do(http.MethodPost, marksPrefix+"/government/invitations", inviteBody(invitee), requester, true)
	if w.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	ap := decodeApproval(t, w)
	if ap.Operation != opMarksInviteCreate || ap.App != "platform" || ap.TargetID != invitee || ap.RequiredPermission != permGovInvite {
		t.Fatalf("approval %+v", ap)
	}
	if !strings.HasPrefix(ap.Summary, "Invite government account") {
		t.Fatalf("summary %q", ap.Summary)
	}
	var pl govInvitePayload
	if err := json.Unmarshal(ap.Payload, &pl); err != nil || pl.RequestedBy != requester || pl.UserID != invitee {
		t.Fatalf("payload %s", ap.Payload)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("the first admin's call reached profile: %+v", hits)
	}
	if a := rg.takeAudit(); len(a) == 0 || a[len(a)-1].Outcome != postgres.AuditOutcomePending || a[len(a)-1].TargetID != invitee {
		t.Fatalf("create audit %+v", a)
	}

	// The requester cannot approve their own request.
	if w := rg.do(http.MethodPost, "/v1/admin/approvals/"+ap.ID+"/approve", `{"reason":"self"}`, requester, true); w.Code != http.StatusForbidden {
		t.Fatalf("self approval: %d", w.Code)
	}
	rg.takeAudit()

	w = rg.do(http.MethodPost, "/v1/admin/approvals/"+ap.ID+"/approve", `{"reason":"second check done"}`, approver, true)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), testInviteCode) {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].aud != "profile" || hits[0].path != service.ProfileAdminPrefix+"/marks/government/invitations" {
		t.Fatalf("approve hits %+v", hits)
	}
	h := hits[0]
	if h.verifyErr != nil || h.verified.Actor != approver || len(h.verified.Scope) != 1 || h.verified.Scope[0] != permGovInvite {
		t.Fatalf("approve token %+v", h.verified)
	}
	var sent govInvitePayload
	if err := json.Unmarshal([]byte(h.body), &sent); err != nil || sent.RequestedBy != requester || sent.UserID != invitee ||
		sent.OfficialName != "Ministry of Examples" || sent.Reason != "verified by letter" {
		t.Fatalf("profile body %s", h.body)
	}

	// Nowhere stored: the approval row and every audit row lack the code.
	rg.store.mu.Lock()
	stored, _ := json.Marshal(rg.store.rows)
	rg.store.mu.Unlock()
	if strings.Contains(string(stored), testInviteCode) {
		t.Fatalf("invite code stored in admin.approvals: %s", stored)
	}
	audit := rg.takeAudit()
	if len(audit) == 0 {
		t.Fatal("approve wrote no audit")
	}
	if raw, _ := json.Marshal(audit); strings.Contains(string(raw), testInviteCode) {
		t.Fatalf("invite code in the audit log: %s", raw)
	}

	// Shown once: a replayed approval answers the stored outcome, no code.
	w = rg.do(http.MethodPost, "/v1/admin/approvals/"+ap.ID+"/approve", `{"reason":"again"}`, approver, true)
	if strings.Contains(w.Body.String(), testInviteCode) || len(rg.takeHits()) != 0 {
		t.Fatalf("replay showed the code again or re-ran: %d %s", w.Code, w.Body.String())
	}
}

func TestMarks_InvitationRefusesTheSoleHolderPath(t *testing.T) {
	rg := newMarksRig(t)
	rg.holders.n = 0
	alone := uuid.NewString()
	rg.perms.grant(alone, permGovInvite)
	w := rg.do(http.MethodPost, marksPrefix+"/government/invitations", inviteBody(uuid.NewString()), alone, true)
	if w.Code != http.StatusConflict || !hasCode(w, CodeSecondApproverRequired) {
		t.Fatalf("sole holder: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("sole holder reached profile: %+v", hits)
	}
	rg.store.mu.Lock()
	n := len(rg.store.rows)
	rg.store.mu.Unlock()
	if n != 0 {
		t.Fatal("an approval nobody could approve was stored")
	}
	a := rg.takeAudit()
	if len(a) == 0 || a[len(a)-1].Outcome != postgres.AuditOutcomeDenied || a[len(a)-1].Payload["code"] != CodeSecondApproverRequired {
		t.Fatalf("sole holder audit %+v", a)
	}
	if rg.holders.lastPerm != permGovInvite || rg.holders.lastExclude != alone {
		t.Fatalf("holders asked for %q excluding %q", rg.holders.lastPerm, rg.holders.lastExclude)
	}
}

// The executor never runs as the requester, even if called that way.
func TestMarks_InvitationExecutorRefusesTheRequester(t *testing.T) {
	rg := newMarksRig(t)
	requester := uuid.NewString()
	pl, _ := json.Marshal(govInvitePayload{UserID: uuid.NewString(), OfficialName: "X", Reason: "r", RequestedBy: requester})
	res := rg.h.marksInviteExecutor(context.Background(), requester, pl)
	if res.Status != http.StatusConflict || !strings.Contains(string(res.Data), CodeSecondApproverRequired) || res.Err != nil {
		t.Fatalf("executor as requester: %+v", res)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("executor as requester reached profile")
	}
}

func TestMarks_InvitationValidation(t *testing.T) {
	rg := newMarksRig(t)
	actor := uuid.NewString()
	rg.perms.grant(actor, permGovInvite)
	for name, tc := range map[string]struct {
		body string
		code string
		st   int
	}{
		"not json":    {`nope`, CodeInvalidBody, http.StatusBadRequest},
		"bad user":    {`{"user_id":"x","official_name":"M","reason":"r"}`, CodeInvalidBody, http.StatusBadRequest},
		"no name":     {`{"user_id":"` + uuid.NewString() + `","official_name":" ","reason":"r"}`, CodeInvalidOfficialName, http.StatusBadRequest},
		"no reason":   {`{"user_id":"` + uuid.NewString() + `","official_name":"M","reason":""}`, CodeReasonRequired, http.StatusBadRequest},
		"self invite": {inviteBody(actor), CodeApproverIsInvitee, http.StatusForbidden},
	} {
		w := rg.do(http.MethodPost, marksPrefix+"/government/invitations", tc.body, actor, true)
		if w.Code != tc.st || !hasCode(w, tc.code) {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("an invalid invitation reached profile")
	}
	if rg.holders.calls != 0 {
		t.Fatal("an invalid invitation reached the approval service")
	}
	// Holding the read and review permissions does not open the invitation.
	rv := reviewer(rg)
	if w := rg.do(http.MethodPost, marksPrefix+"/government/invitations", inviteBody(uuid.NewString()), rv, true); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("reviewer invited: %d", w.Code)
	}
}

func TestMarks_MeNavigation(t *testing.T) {
	rg := newMarksRig(t)
	for name, tc := range map[string]struct {
		perms []string
		want  bool
	}{
		"read":        {[]string{permMarksRead}, true},
		"kyc":         {[]string{permMarksRead, permMarksReview}, true},
		"invite only": {[]string{permGovInvite}, false},
		"moderator":   {[]string{"platform:users.read", "platform:users.search"}, false},
	} {
		actor := uuid.NewString()
		rg.perms.grant(actor, tc.perms...)
		w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
		if got := strings.Contains(w.Body.String(), `{"app":"marks","label":"Official marks"}`); got != tc.want {
			t.Fatalf("%s: marks in navigation = %v (%s)", name, got, w.Body.String())
		}
	}
}

func TestMarks_CatalogueMirror(t *testing.T) {
	cat := adminauth.Catalogue()
	has := func(role, perm string) bool {
		for _, p := range cat.RolePermissions[role]["platform"] {
			if p == perm {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		role string
		want map[string]bool
	}{
		{adminauth.RoleSuperadmin, map[string]bool{permMarksRead: true, permMarksReview: true, permGovInvite: true}},
		{adminauth.RoleAdmin, map[string]bool{permMarksRead: true, permMarksReview: true, permGovInvite: true}},
		{adminauth.RoleKYCReviewer, map[string]bool{permMarksRead: true, permMarksReview: true}},
		{adminauth.RoleAuditor, map[string]bool{permMarksRead: true}},
		{adminauth.RoleModerator, map[string]bool{}},
		{adminauth.RoleSupport, map[string]bool{}},
		{adminauth.RoleFinance, map[string]bool{}},
	} {
		for _, perm := range marksAll {
			if has(tc.role, perm) != tc.want[perm] {
				t.Fatalf("%s holds %s = %v, want %v", tc.role, perm, !tc.want[perm], tc.want[perm])
			}
		}
	}
	// Always two-person is pinned on the route itself
	// (TestMarks_RegisteredInTheFullTable).
}
