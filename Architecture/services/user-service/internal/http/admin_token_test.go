// Admin-service token tests (admin console Wave 2 — Content: business pages).
// No database: a recording fake store proves admission and the actor and entry
// point handed to the store; refusals are proven by the auth layer's own
// status and code. Audit rows, the append-only trigger and stats counts are in
// admin_token_integration_test.go.
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/user-service/internal/pages"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const adminTestInternalKey = "user-admin-token-test-key"

// fakePageAdmin records every decision it receives.
type fakePageAdmin struct {
	pages        map[uuid.UUID]*store.BusinessPage
	docs         map[uuid.UUID]store.PageDocument
	decisions    []store.PageStatusDecision
	docDecisions []store.PageDocumentDecision
}

func newFakePageAdmin() *fakePageAdmin {
	return &fakePageAdmin{pages: map[uuid.UUID]*store.BusinessPage{}, docs: map[uuid.UUID]store.PageDocument{}}
}

func (f *fakePageAdmin) addPage(status string) *store.BusinessPage {
	p := &store.BusinessPage{ID: uuid.New(), PageHandle: "h" + uuid.NewString()[:8], Status: status, EntityType: "page"}
	f.pages[p.ID] = p
	return p
}

func (f *fakePageAdmin) addDoc(pageID uuid.UUID) store.PageDocument {
	d := store.PageDocument{ID: uuid.New(), PageID: pageID, DocumentType: "identity_proof", DocumentURL: "https://docs.example/secret", Status: "pending"}
	f.docs[d.ID] = d
	return d
}

func (f *fakePageAdmin) GetBusinessPageByID(_ context.Context, id uuid.UUID, _ *uuid.UUID) (*store.BusinessPage, error) {
	if p, ok := f.pages[id]; ok {
		cp := *p
		return &cp, nil
	}
	return nil, pgx.ErrNoRows
}

func (f *fakePageAdmin) GetBusinessPageByHandle(_ context.Context, handle string, _ *uuid.UUID) (*store.BusinessPage, error) {
	for _, p := range f.pages {
		if p.PageHandle == handle {
			cp := *p
			return &cp, nil
		}
	}
	return nil, pgx.ErrNoRows
}

func (f *fakePageAdmin) ListPageDocuments(_ context.Context, pageID uuid.UUID) ([]store.PageDocument, error) {
	var out []store.PageDocument
	for _, d := range f.docs {
		if d.PageID == pageID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *fakePageAdmin) AdminListPendingPages(context.Context, int, int) ([]store.BusinessPage, error) {
	var out []store.BusinessPage
	for _, p := range f.pages {
		if p.Status == pages.StatusPendingReview {
			out = append(out, *p)
		}
	}
	return out, nil
}

func (f *fakePageAdmin) AdminSetPageStatus(_ context.Context, d store.PageStatusDecision) error {
	p, ok := f.pages[d.PageID]
	if !ok {
		return store.ErrPageNotFound
	}
	if p.Status != d.From {
		return store.ErrPageStatusChanged
	}
	f.decisions = append(f.decisions, d)
	p.Status = d.To
	return nil
}

func (f *fakePageAdmin) AdminDecidePageDocument(_ context.Context, d store.PageDocumentDecision) error {
	doc, ok := f.docs[d.DocumentID]
	if !ok || (d.PageID != uuid.Nil && d.PageID != doc.PageID) {
		return store.ErrPageDocumentNotFound
	}
	f.docDecisions = append(f.docDecisions, d)
	return nil
}

func (f *fakePageAdmin) PageAdminStats(context.Context) (*store.PageAdminStats, error) {
	return &store.PageAdminStats{PendingReview: 3, Approved7d: 2, Rejected7d: 1, Suspended7d: 1, DocumentsPending: 4, WindowDays: 7}, nil
}

type pageTokenRig struct {
	r        *gin.Engine
	v        *servicetoken.Verifier
	st       *fakePageAdmin
	admin    *servicetoken.Signer // admin-service, registered for AdminPermissions
	notifier *servicetoken.Signer // a registered caller that is not admin-service
	rogue    *servicetoken.Signer // claims to be admin-service, unregistered key
	actor    uuid.UUID
	allowed  uuid.UUID // on PAGES_ADMIN_USER_IDS
}

func newPageTokenRig(t *testing.T) *pageTokenRig {
	t.Helper()
	aPub, aPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	nPub, nPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_, rPriv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"SERVICE_CALLERS":                            "admin-service,notification-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":           "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY":        aPub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":           strings.Join(AdminPermissions, ","),
		"SERVICE_CALLER_NOTIFICATION_SERVICE_KID":    "n1",
		"SERVICE_CALLER_NOTIFICATION_SERVICE_PUBKEY": nPub,
		// Deliberately over-granted: even with the ops registered, a caller
		// other than admin-service must not reach an admin route.
		"SERVICE_CALLER_NOTIFICATION_SERVICE_OPS": strings.Join(AdminPermissions, ","),
	}
	v, err := AdminServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	mk := func(iss, kid, priv string) *servicetoken.Signer {
		s, err := servicetoken.NewSignerFromBase64(iss, kid, priv)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	rg := &pageTokenRig{
		v:        v,
		st:       newFakePageAdmin(),
		admin:    mk(IssuerAdminService, "a1", aPriv),
		notifier: mk("notification-service", "n1", nPriv),
		rogue:    mk(IssuerAdminService, "a1", rPriv),
		actor:    uuid.New(),
		allowed:  uuid.New(),
	}
	rg.r = pageTokenRouter(rg.st, v, rg.allowed)
	return rg
}

// pageTokenRouter drives the REAL route table.
func pageTokenRouter(st pageAdminStore, v *servicetoken.Verifier, allowed uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{pageAdmins: map[string]bool{allowed.String(): true}, pageAdmin: st}
	h.WithInternalRoutes(adminTestInternalKey).WithAdminServiceAuth(v)
	r := gin.New()
	r.Use(gin.Recovery())
	h.RegisterRoutes(r)
	return r
}

func (rg *pageTokenRig) mint(t *testing.T, s *servicetoken.Signer, aud string, scope []string, actor string) string {
	t.Helper()
	var opts []servicetoken.MintOption
	if actor != "" {
		opts = append(opts, servicetoken.WithActor(actor))
	}
	tok, err := s.Mint(aud, "admin-console", scope, nil, time.Minute, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func adminServe(r *gin.Engine, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func bearer(tok string) map[string]string {
	return map[string]string{ServiceAuthHeader: "Bearer " + tok}
}

// forgedGateway is everything an edge or in-cluster forger could add: the
// internal key and an allowlisted identity.
func forgedGateway(user uuid.UUID) map[string]string {
	return map[string]string{
		internalServiceKeyHeader: adminTestInternalKey,
		"X-User-Id":              user.String(),
		"X-Scopes":               "superadmin admin moderator",
		"X-Admin-Id":             user.String(),
		"X-Actor-Id":             user.String(),
	}
}

func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	if env.Error == nil {
		return ""
	}
	return env.Error.Code
}

func uuidParams(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[i] = uuid.NewString()
		}
	}
	return strings.Join(parts, "/")
}

const reasonBody = `{"reason":"policy breach"}`

func TestAdminToken_RightScopeAdmittedActorIsAct(t *testing.T) {
	rg := newPageTokenRig(t)
	p := rg.st.addPage(pages.StatusApproved)
	tok := rg.mint(t, rg.admin, AudienceSocial, []string{PermPagesSuspend}, rg.actor.String())
	// The key and an ALLOWLISTED X-User-Id ride along; the actor is still act.
	hdr := forgedGateway(rg.allowed)
	hdr[ServiceAuthHeader] = "Bearer " + tok
	w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+p.ID.String()+"/suspend", reasonBody, hdr)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if len(rg.st.decisions) != 1 {
		t.Fatalf("decisions=%d, want 1", len(rg.st.decisions))
	}
	d := rg.st.decisions[0]
	if d.Actor != rg.actor || d.Via != store.ViaAdminService || d.Action != store.PageActionSuspend ||
		d.From != pages.StatusApproved || d.To != pages.StatusSuspended || d.Reason != "policy breach" {
		t.Fatalf("decision=%+v, want actor %s via admin_service suspend approved→suspended", d, rg.actor)
	}

	doc := rg.st.addDoc(p.ID)
	docTok := rg.mint(t, rg.admin, AudienceSocial, []string{PermDocumentsReview}, rg.actor.String())
	hdr[ServiceAuthHeader] = "Bearer " + docTok
	w = adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+p.ID.String()+"/documents/"+doc.ID.String()+"/approve", "", hdr)
	if w.Code != http.StatusOK || len(rg.st.docDecisions) != 1 {
		t.Fatalf("document approve: status=%d body=%s decisions=%d", w.Code, w.Body.String(), len(rg.st.docDecisions))
	}
	dd := rg.st.docDecisions[0]
	if dd.Actor != rg.actor || dd.Via != store.ViaAdminService || dd.PageID != p.ID || dd.Status != "approved" {
		t.Fatalf("document decision=%+v, want actor act, page-bound, approved", dd)
	}
}

func TestAdminToken_Refusals(t *testing.T) {
	rg := newPageTokenRig(t)
	actor := rg.actor.String()
	scope := []string{PermPagesSuspend}
	cases := []struct {
		name     string
		tok      string
		clock    time.Duration
		wantCode string
	}{
		{"wrong audience user-service", rg.mint(t, rg.admin, AudienceUserService, scope, actor), 0, CodeServiceTokenRejected},
		{"wrong audience food", rg.mint(t, rg.admin, "food", scope, actor), 0, CodeServiceTokenRejected},
		{"missing act", rg.mint(t, rg.admin, AudienceSocial, scope, ""), 0, CodeAdminActorRequired},
		{"malformed act", rg.mint(t, rg.admin, AudienceSocial, scope, "not-a-uuid"), 0, CodeAdminActorRequired},
		{"nil act", rg.mint(t, rg.admin, AudienceSocial, scope, uuid.Nil.String()), 0, CodeAdminActorRequired},
		{"wrong scope", rg.mint(t, rg.admin, AudienceSocial, []string{PermPagesModerate, PermStatsRead}, actor), 0, CodeAdminPermissionScope},
		{"expired", rg.mint(t, rg.admin, AudienceSocial, scope, actor), 2 * time.Minute, CodeServiceTokenRejected},
		{"unknown caller key", rg.mint(t, rg.rogue, AudienceSocial, scope, actor), 0, CodeServiceTokenRejected},
		{"registered non-admin caller", rg.mint(t, rg.notifier, AudienceSocial, scope, actor), 0, CodeServiceTokenRejected},
		{"garbage", "a.b.c", 0, CodeServiceTokenRejected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clock != 0 {
				rg.v.SetClock(func() time.Time { return time.Now().Add(tc.clock) })
				defer rg.v.SetClock(time.Now)
			}
			p := rg.st.addPage(pages.StatusApproved)
			hdr := forgedGateway(rg.allowed)
			hdr[ServiceAuthHeader] = "Bearer " + tc.tok
			w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+p.ID.String()+"/suspend", reasonBody, hdr)
			if w.Code != http.StatusForbidden || errorCode(t, w) != tc.wantCode {
				t.Fatalf("status=%d code=%q body=%s, want 403 %s", w.Code, errorCode(t, w), w.Body.String(), tc.wantCode)
			}
			if len(rg.st.decisions) != 0 {
				t.Fatalf("a refused request reached the store")
			}
		})
	}
}

// adminRoutePermissions is the route table under test: method + suffix → the
// permission the route's gate names first.
func adminRoutePermissions() map[string]string {
	return map[string]string{
		"GET /stats":                               PermStatsRead,
		"GET /pages/pending":                       PermPagesModerate,
		"GET /pages/:id":                           PermPagesModerate,
		"POST /pages/:id/approve":                  PermPagesModerate,
		"POST /pages/:id/reject":                   PermPagesModerate,
		"POST /pages/:id/suspend":                  PermPagesSuspend,
		"POST /pages/:id/disable":                  PermPagesDisable,
		"GET /pages/:id/documents":                 PermDocumentsReview,
		"POST /pages/:id/documents/:docId/approve": PermDocumentsReview,
		"POST /pages/:id/documents/:docId/reject":  PermDocumentsReview,
	}
}

// secondaryPermission is the narrower permission a mixed route also admits.
var secondaryPermission = map[string]string{
	"/pages/:id/approve": PermPagesSuspend,
}

// The edge cannot reach the token-only family with the internal key plus an
// allowlisted, forged actor: no token, no entry.
func TestAdminToken_InternalFamilyRefusesKeyAndForgedActor(t *testing.T) {
	rg := newPageTokenRig(t)
	var checked int
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		checked++
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), reasonBody, forgedGateway(rg.allowed))
		if w.Code != http.StatusUnauthorized || errorCode(t, w) != CodeAdminTokenRequired {
			t.Fatalf("%s %s: status=%d code=%q, want 401 %s", ri.Method, ri.Path, w.Code, errorCode(t, w), CodeAdminTokenRequired)
		}
	}
	if checked != len(adminRoutePermissions()) {
		t.Fatalf("checked %d internal admin routes, want %d", checked, len(adminRoutePermissions()))
	}
	if len(rg.st.decisions)+len(rg.st.docDecisions) != 0 {
		t.Fatalf("a request without a token reached the store")
	}
}

func TestAdminToken_EveryRouteNeedsItsPermission(t *testing.T) {
	rg := newPageTokenRig(t)
	want := adminRoutePermissions()
	seen := 0
	for _, ri := range rg.r.Routes() {
		if !strings.HasPrefix(ri.Path, InternalAdminPrefix+"/") {
			continue
		}
		suffix := strings.TrimPrefix(ri.Path, InternalAdminPrefix)
		perm, ok := want[ri.Method+" "+suffix]
		if !ok {
			t.Fatalf("undeclared admin route %s %s", ri.Method, ri.Path)
		}
		seen++
		var others []string
		for _, p := range AdminPermissions {
			if p != perm && p != secondaryPermission[suffix] {
				others = append(others, p)
			}
		}
		tok := rg.mint(t, rg.admin, AudienceSocial, others, rg.actor.String())
		hdr := forgedGateway(rg.allowed)
		hdr[ServiceAuthHeader] = "Bearer " + tok
		w := adminServe(rg.r, ri.Method, uuidParams(ri.Path), reasonBody, hdr)
		if w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
			t.Fatalf("%s %s without %s: status=%d code=%q, want 403 %s", ri.Method, ri.Path, perm, w.Code, errorCode(t, w), CodeAdminPermissionScope)
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d admin routes, want %d", seen, len(want))
	}
}

// Approving a submission needs moderate; reinstating a suspended page reverses
// a suspension and needs suspend.
func TestAdminToken_ApproveNarrowsOnReinstate(t *testing.T) {
	rg := newPageTokenRig(t)
	moderate := rg.mint(t, rg.admin, AudienceSocial, []string{PermPagesModerate}, rg.actor.String())
	suspend := rg.mint(t, rg.admin, AudienceSocial, []string{PermPagesSuspend}, rg.actor.String())
	approve := func(p *store.BusinessPage, tok string) *httptest.ResponseRecorder {
		return adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+p.ID.String()+"/approve", "", bearer(tok))
	}

	pending := rg.st.addPage(pages.StatusPendingReview)
	if w := approve(pending, suspend); w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
		t.Fatalf("approve submission with suspend only: status=%d code=%q, want 403", w.Code, errorCode(t, w))
	}
	if w := approve(pending, moderate); w.Code != http.StatusOK {
		t.Fatalf("approve submission with moderate: status=%d body=%s", w.Code, w.Body.String())
	}

	suspended := rg.st.addPage(pages.StatusSuspended)
	if w := approve(suspended, moderate); w.Code != http.StatusForbidden || errorCode(t, w) != CodeAdminPermissionScope {
		t.Fatalf("reinstate with moderate only: status=%d code=%q, want 403", w.Code, errorCode(t, w))
	}
	if w := approve(suspended, suspend); w.Code != http.StatusOK {
		t.Fatalf("reinstate with suspend: status=%d body=%s", w.Code, w.Body.String())
	}
	if len(rg.st.decisions) != 2 {
		t.Fatalf("decisions=%d, want 2", len(rg.st.decisions))
	}
	for _, d := range rg.st.decisions {
		if d.Actor != rg.actor {
			t.Fatalf("actor %s, want act %s", d.Actor, rg.actor)
		}
	}
}

func TestAdminToken_ReasonsAndConflicts(t *testing.T) {
	rg := newPageTokenRig(t)
	all := rg.mint(t, rg.admin, AudienceSocial, AdminPermissions, rg.actor.String())
	for _, tc := range []struct{ from, action string }{
		{pages.StatusPendingReview, "reject"},
		{pages.StatusApproved, "suspend"},
		{pages.StatusApproved, "disable"},
	} {
		p := rg.st.addPage(tc.from)
		path := InternalAdminPrefix + "/pages/" + p.ID.String() + "/" + tc.action
		if w := adminServe(rg.r, http.MethodPost, path, `{"reason":"  "}`, bearer(all)); w.Code != http.StatusBadRequest {
			t.Fatalf("%s without reason: status=%d, want 400", tc.action, w.Code)
		}
	}
	p := rg.st.addPage(pages.StatusPendingReview)
	doc := rg.st.addDoc(p.ID)
	docPath := InternalAdminPrefix + "/pages/" + p.ID.String() + "/documents/" + doc.ID.String() + "/reject"
	if w := adminServe(rg.r, http.MethodPost, docPath, `{}`, bearer(all)); w.Code != http.StatusBadRequest {
		t.Fatalf("document reject without reason: status=%d, want 400", w.Code)
	}
	// A document on another page is not found through this page.
	other := rg.st.addPage(pages.StatusPendingReview)
	wrongPage := InternalAdminPrefix + "/pages/" + other.ID.String() + "/documents/" + doc.ID.String() + "/approve"
	if w := adminServe(rg.r, http.MethodPost, wrongPage, "", bearer(all)); w.Code != http.StatusNotFound {
		t.Fatalf("document via the wrong page: status=%d, want 404", w.Code)
	}
	// Illegal transition: disabled is terminal.
	gone := rg.st.addPage(pages.StatusDisabled)
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+gone.ID.String()+"/approve", "", bearer(all)); w.Code != http.StatusConflict {
		t.Fatalf("approve disabled: status=%d, want 409", w.Code)
	}
	if w := adminServe(rg.r, http.MethodPost, InternalAdminPrefix+"/pages/"+p.PageHandle+"/approve", "", bearer(all)); w.Code != http.StatusBadRequest {
		t.Fatalf("handle on token path: status=%d, want 400", w.Code)
	}
	if len(rg.st.decisions)+len(rg.st.docDecisions) != 0 {
		t.Fatalf("a refused decision reached the store")
	}
}

// The page detail carries no document URL; the documents route does.
func TestAdminToken_DetailHidesDocumentURLs(t *testing.T) {
	rg := newPageTokenRig(t)
	p := rg.st.addPage(pages.StatusPendingReview)
	rg.st.addDoc(p.ID)
	moderate := rg.mint(t, rg.admin, AudienceSocial, []string{PermPagesModerate}, rg.actor.String())
	w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/pages/"+p.ID.String(), "", bearer(moderate))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "docs.example") || !strings.Contains(w.Body.String(), "identity_proof") {
		t.Fatalf("detail: status=%d body=%s, want 200 with document metadata and no URL", w.Code, w.Body.String())
	}
	review := rg.mint(t, rg.admin, AudienceSocial, []string{PermDocumentsReview}, rg.actor.String())
	w = adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/pages/"+p.ID.String()+"/documents", "", bearer(review))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "docs.example") {
		t.Fatalf("documents: status=%d body=%s, want 200 with URL", w.Code, w.Body.String())
	}
	w = adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/pages/pending", "", bearer(moderate))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), p.ID.String()) {
		t.Fatalf("pending: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAdminToken_StatsShape(t *testing.T) {
	rg := newPageTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceSocial, []string{PermStatsRead}, rg.actor.String())
	w := adminServe(rg.r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok))
	if w.Code != http.StatusOK {
		t.Fatalf("stats: status=%d body=%s", w.Code, w.Body.String())
	}
	for _, key := range []string{`"pending_review":3`, `"approved_7d":2`, `"rejected_7d":1`, `"suspended_7d":1`, `"documents_pending":4`, `"window_days":7`} {
		if !strings.Contains(w.Body.String(), key) {
			t.Fatalf("stats body %s lacks %s", w.Body.String(), key)
		}
	}
}

// A deployment without admin-service registered accepts no admin token.
func TestAdminToken_NoVerifierRefuses(t *testing.T) {
	rg := newPageTokenRig(t)
	tok := rg.mint(t, rg.admin, AudienceSocial, []string{PermStatsRead}, rg.actor.String())
	r := pageTokenRouter(newFakePageAdmin(), nil, rg.allowed)
	if w := adminServe(r, http.MethodGet, InternalAdminPrefix+"/stats", "", bearer(tok)); w.Code != http.StatusUnauthorized {
		t.Fatalf("no verifier: status=%d, want 401", w.Code)
	}
}

// The legacy PAGES_ADMIN_USER_IDS routes work as before, record the header
// actor via pages_allowlist, and never accept a token in place of the list.
func TestLegacyPagesAllowlist_StillWorks(t *testing.T) {
	rg := newPageTokenRig(t)
	p := rg.st.addPage(pages.StatusPendingReview)
	allowed := map[string]string{"X-User-Id": rg.allowed.String()}

	// By handle, as before.
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+p.PageHandle+"/approve", "", allowed); w.Code != http.StatusOK {
		t.Fatalf("legacy approve: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+p.ID.String()+"/suspend", `{}`, allowed); w.Code != http.StatusBadRequest {
		t.Fatalf("legacy suspend without reason: status=%d, want 400", w.Code)
	}
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+p.ID.String()+"/suspend", reasonBody, allowed); w.Code != http.StatusOK {
		t.Fatalf("legacy suspend: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+p.ID.String()+"/approve", "", allowed); w.Code != http.StatusOK {
		t.Fatalf("legacy reinstate: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+p.ID.String()+"/disable", "", allowed); w.Code != http.StatusOK {
		t.Fatalf("legacy disable without reason: status=%d body=%s, want 200", w.Code, w.Body.String())
	}
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+p.ID.String()+"/approve", "", allowed); w.Code != http.StatusConflict {
		t.Fatalf("legacy approve disabled: status=%d, want 409", w.Code)
	}
	if len(rg.st.decisions) != 4 {
		t.Fatalf("legacy decisions=%d, want 4", len(rg.st.decisions))
	}
	for _, d := range rg.st.decisions {
		if d.Actor != rg.allowed || d.Via != store.ViaPagesAllowlist {
			t.Fatalf("legacy decision %+v, want actor %s via pages_allowlist", d, rg.allowed)
		}
	}

	doc := rg.st.addDoc(p.ID)
	if w := adminServe(rg.r, http.MethodPost, "/v1/pages/"+p.PageHandle+"/documents/"+doc.ID.String()+"/reject", reasonBody, allowed); w.Code != http.StatusOK {
		t.Fatalf("legacy document reject: status=%d body=%s", w.Code, w.Body.String())
	}
	if len(rg.st.docDecisions) != 1 || rg.st.docDecisions[0].Actor != rg.allowed || rg.st.docDecisions[0].Via != store.ViaPagesAllowlist {
		t.Fatalf("legacy document decisions=%+v", rg.st.docDecisions)
	}

	// Not on the list → 403, even with a valid admin-service token attached.
	fresh := rg.st.addPage(pages.StatusPendingReview)
	tok := rg.mint(t, rg.admin, AudienceSocial, AdminPermissions, rg.actor.String())
	hdr := bearer(tok)
	hdr["X-User-Id"] = uuid.NewString()
	for _, path := range []string{"/v1/pages/" + fresh.ID.String() + "/approve", "/v1/pages/" + fresh.ID.String() + "/documents/" + doc.ID.String() + "/approve"} {
		if w := adminServe(rg.r, http.MethodPost, path, reasonBody, hdr); w.Code != http.StatusForbidden {
			t.Fatalf("%s off the allowlist with a token: status=%d, want 403", path, w.Code)
		}
	}
	if len(rg.st.decisions) != 4 || len(rg.st.docDecisions) != 1 {
		t.Fatalf("a refused legacy request reached the store")
	}
}

func TestAdminServiceCallersFromEnv(t *testing.T) {
	if v, err := AdminServiceCallersFromEnv(func(string) string { return "" }); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: v=%v err=%v, want nil, nil", v, err)
	}
	pub, priv, _ := servicetoken.GenerateKeypair()
	for name, env := range map[string]map[string]string{
		"missing key": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_OPS": PermStatsRead},
		"missing ops": {"SERVICE_CALLERS": "admin-service", "SERVICE_CALLER_ADMIN_SERVICE_KID": "a1", "SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub},
	} {
		if _, err := AdminServiceCallersFromEnv(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("%s: want a configuration error", name)
		}
	}
	// The two verifiers share the registry but not the audience.
	env := map[string]string{
		"SERVICE_CALLERS":                     "admin-service",
		"SERVICE_CALLER_ADMIN_SERVICE_KID":    "a1",
		"SERVICE_CALLER_ADMIN_SERVICE_PUBKEY": pub,
		"SERVICE_CALLER_ADMIN_SERVICE_OPS":    PermStatsRead + "," + OpReadPrivateProfile,
	}
	social, err := AdminServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	profile, err := ServiceCallersFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	s, _ := servicetoken.NewSignerFromBase64(IssuerAdminService, "a1", priv)
	socialTok, _ := s.Mint(AudienceSocial, "x", []string{OpReadPrivateProfile}, nil, time.Minute)
	if _, err := profile.Verify(socialTok, OpReadPrivateProfile, ""); err == nil {
		t.Fatalf("a social-audience token read private profiles")
	}
	userTok, _ := s.Mint(AudienceUserService, "x", []string{PermStatsRead}, nil, time.Minute)
	if _, err := social.Verify(userTok, PermStatsRead, ""); err == nil {
		t.Fatalf("a user-service-audience token passed the social verifier")
	}
}
