package http

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

// soundsAll is media-service's admin sound permissions.
var soundsAll = []string{permSoundsRead, permSoundsManage}

// soundCase gives each Sound library route a body media-service would accept.
func soundCase(rt productRoute) string {
	switch rt.operation {
	case opSoundsUpload:
		return `{"filename":"monsoon.m4a","content_type":"audio/mp4","size_bytes":1024}`
	case opSoundsCreate:
		return `{"storage_key":"audio-library/` + uuid.NewString() + `.m4a","title":"Monsoon","artist":"Momentum","genre":"pop","license_type":"licensed","rights_holder":"Momentum Records"}`
	case opSoundsUpdate:
		return `{"is_active":true,"reason":"licence cleared"}`
	case opSoundsWithdraw:
		return `{"scope":"new_uses","reason":"licence under review"}`
	}
	return ""
}

// soundUpstream is the pinned contract: method, media-service path under
// /v1/media/internal/admin/sounds/library, the permission, the audit target.
var soundUpstream = []struct{ method, path, perm, target string }{
	{http.MethodGet, "", "media:sounds.read", ""},
	{http.MethodGet, "/:soundId", "media:sounds.read", "library_sound"},
	{http.MethodGet, "/:soundId/preview", "media:sounds.read", "library_sound"},
	{http.MethodPost, "/uploads", "media:sounds.manage", ""},
	{http.MethodPost, "", "media:sounds.manage", "library_sound"},
	{http.MethodPatch, "/:soundId", "media:sounds.manage", "library_sound"},
	{http.MethodPost, "/:soundId/withdraw", "media:sounds.manage", "library_sound"},
}

func TestSoundRoutes_MatchTheContract(t *testing.T) {
	if len(SoundRoutes) != len(soundUpstream) {
		t.Fatalf("SoundRoutes has %d entries, want %d", len(SoundRoutes), len(soundUpstream))
	}
	for i, want := range soundUpstream {
		rt := SoundRoutes[i]
		up := rt.upstream
		if up == "" {
			up = rt.path
		}
		if rt.method != want.method || up != want.path || rt.permission != want.perm || rt.targetType != want.target {
			t.Fatalf("route %d: %s %s %s %s, want %+v", i, rt.method, up, rt.permission, rt.targetType, want)
		}
		if rt.twoPerson || rt.mayTwoPerson || rt.idempotent || rt.stepUp {
			t.Fatalf("%s declares two-person/idempotent/step-up; only withdraw everywhere steps up (decided)", rt.operation)
		}
	}
	if service.MediaAudience != "media" || service.MediaSoundsAdminPrefix != "/v1/media/internal/admin/sounds/library" {
		t.Fatalf("sounds client: %q %q", service.MediaAudience, service.MediaSoundsAdminPrefix)
	}
	if soundsPrefix != "/v1/admin/sounds/library" {
		t.Fatalf("console prefix %q", soundsPrefix)
	}
}

// Every route: refused without its own permission (manage does not imply
// read), forwarded with a token for "media" scoped to exactly that
// permission and acting as the admin, one audit row each.
func TestSoundRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range SoundRoutes {
		t.Run(rt.operation, func(t *testing.T) {
			if rt.path != "" {
				routeTableCase(t, rg, soundsPrefix, service.MediaSoundsAdminPrefix, "media", "media", soundsAll, rt, soundCase(rt))
				return
			}
			rootRouteCase(t, rg, rt, soundCase(rt))
		})
	}
}

// rootRouteCase is routeTableCase for the collection root (list, create),
// whose product path is the prefix itself (routeTableCase expects a
// segment after it).
func rootRouteCase(t *testing.T, rg *productsRig, rt productRoute, body string) {
	t.Helper()
	other := uuid.NewString()
	for _, p := range soundsAll {
		if p != rt.permission {
			rg.perms.grant(other, p)
		}
	}
	w := rg.do(rt.method, soundsPrefix, body, other, true)
	if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) || len(rg.takeHits()) != 0 {
		t.Fatalf("%s without %s: %d %s", rt.operation, rt.permission, w.Code, w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Actor != other || a[0].App != "media" {
		t.Fatalf("%s refusal audit %+v", rt.operation, a)
	}
	actor := uuid.NewString()
	rg.perms.grant(actor, rt.permission)
	w = rg.do(rt.method, soundsPrefix+"?status=inactive&limit=10", body, actor, true)
	if w.Code < 200 || w.Code > 299 {
		t.Fatalf("%s: %d %s", rt.operation, w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].aud != "media" || hits[0].verifyErr != nil || hits[0].path != service.MediaSoundsAdminPrefix ||
		hits[0].method != rt.method || hits[0].userHdr != "" || hits[0].keyHdr != "" || hits[0].requestID != "req-product-1" {
		t.Fatalf("%s hits %+v", rt.operation, hits)
	}
	v := hits[0].verified
	if v.Issuer != "admin-service" || len(v.Scope) != 1 || v.Scope[0] != rt.permission || v.Actor != actor {
		t.Fatalf("%s token %+v", rt.operation, v)
	}
	if rt.method == http.MethodGet && hits[0].query != "status=inactive&limit=10" {
		t.Fatalf("list query %q not forwarded", hits[0].query)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Actor != actor || a[0].Operation != rt.operation || a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("%s audit %+v", rt.operation, a)
	}
}

// The catalogue mirror holds both permissions, admin only.
func TestSoundPermissions_InTheMirror(t *testing.T) {
	cat := adminauth.Catalogue()
	got := map[string][]string{}
	for _, p := range cat.Permissions["media"] {
		got[p.Permission] = p.Roles
	}
	for _, perm := range soundsAll {
		roles := strings.Join(got[perm], ",")
		if roles != "superadmin,admin" {
			t.Fatalf("%s held by %q, want superadmin,admin", perm, roles)
		}
	}
	if len(got) != 2 {
		t.Fatalf("media permissions %v", got)
	}
}

func auditPayload(t *testing.T, a postgres.AdminAuditEntry) string {
	t.Helper()
	b, _ := json.Marshal(a.Payload)
	return string(b)
}

func TestSoundWithdraw_ReasonScopeAndStepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, soundsAll...)
	id := uuid.NewString()
	path := soundsPrefix + "/" + id + "/withdraw"

	for _, body := range []string{``, `not json`, `[1]`, `{}`, `{"scope":"everywhere"}`, `{"scope":"everywhere","reason":"  "}`,
		`{"scope":"everywhere","reason":"ab"}`, `{"scope":"everywhere","reason":"` + strings.Repeat("x", 501) + `"}`,
		`{"scope":"all","reason":"licence lapsed"}`, `{"reason":"licence lapsed"}`} {
		w := rg.do(http.MethodPost, path, body, actor, true)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%q: %d %s", body, w.Code, w.Body.String())
		}
		if len(rg.takeHits()) != 0 {
			t.Fatalf("%q reached media-service", body)
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeDenied || a[0].Actor != actor {
			t.Fatalf("%q audit %+v", body, a)
		}
	}

	// everywhere silences existing posts: a fresh step-up is required.
	w := rg.do(http.MethodPost, path, `{"scope":"everywhere","reason":"rights holder complaint"}`, actor, false)
	if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) || len(rg.takeHits()) != 0 {
		t.Fatalf("everywhere without step-up: %d %s", w.Code, w.Body.String())
	}
	rg.takeAudit()
	w = rg.do(http.MethodPost, path, `{"scope":"everywhere","reason":"rights holder complaint"}`, actor, true)
	if w.Code != http.StatusOK {
		t.Fatalf("everywhere: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].path != service.MediaSoundsAdminPrefix+"/"+id+"/withdraw" ||
		!strings.Contains(hits[0].body, `"everywhere"`) || hits[0].verified.Actor != actor {
		t.Fatalf("hits %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Operation != opSoundsWithdraw || a[0].App != "media" || a[0].Reason != "rights holder complaint" ||
		a[0].TargetType != "library_sound" || a[0].TargetID != id || !strings.Contains(auditPayload(t, a[0]), `"scope":"everywhere"`) ||
		a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("audit %+v", a)
	}

	// new_uses needs no step-up.
	if w := rg.do(http.MethodPost, path, `{"scope":"new_uses","reason":"licence under review"}`, actor, false); w.Code != http.StatusOK {
		t.Fatalf("new_uses: %d %s", w.Code, w.Body.String())
	}
	rg.takeHits()
	if a := rg.takeAudit(); len(a) != 1 || a[0].Reason != "licence under review" {
		t.Fatalf("new_uses audit %+v", a)
	}
}

func TestSoundCreate_AuditsTheNewSound(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permSoundsManage)
	newID := uuid.NewString()
	rg.on(http.MethodPost, service.MediaSoundsAdminPrefix, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"` + newID + `","status":"draft"}}`))
	})
	body := strings.TrimSuffix(soundCase(productRoute{operation: opSoundsCreate}), "}") + `,"reason":"new licensed track"}`
	w := rg.do(http.MethodPost, soundsPrefix, body, actor, false)
	if w.Code != http.StatusCreated || !strings.Contains(w.Body.String(), newID) {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	hits := rg.takeHits()
	if len(hits) != 1 || hits[0].path != service.MediaSoundsAdminPrefix || hits[0].method != http.MethodPost ||
		!strings.Contains(hits[0].body, `"storage_key"`) || hits[0].verified.Scope[0] != permSoundsManage {
		t.Fatalf("hits %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].TargetType != "library_sound" || a[0].TargetID != newID || a[0].Reason != "new licensed track" ||
		!strings.Contains(auditPayload(t, a[0]), `"license_type":"licensed"`) || a[0].Outcome != postgres.AuditOutcomeSuccess {
		t.Fatalf("audit %+v", a)
	}

	// A refusal from media-service (422 TOO_LONG) is relayed and audited as a failure.
	rg.on(http.MethodPost, service.MediaSoundsAdminPrefix, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":{"code":"TOO_LONG","message":"The sound is longer than 5 minutes (300 s)"}}`))
	})
	w = rg.do(http.MethodPost, soundsPrefix, soundCase(productRoute{operation: opSoundsCreate}), actor, false)
	if w.Code != http.StatusUnprocessableEntity || !hasCode(w, "TOO_LONG") {
		t.Fatalf("relay: %d %s", w.Code, w.Body.String())
	}
	rg.takeHits()
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || a[0].StatusCode != 422 {
		t.Fatalf("failure audit %+v", a)
	}
}

func TestSoundUpdate_AuditsTheFieldsAndReason(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permSoundsManage)
	id := uuid.NewString()
	w := rg.do(http.MethodPatch, soundsPrefix+"/"+id, `{"title":"Monsoon (Edit)","is_active":false,"reason":"duplicate"}`, actor, false)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 1 || hits[0].method != http.MethodPatch || !strings.Contains(hits[0].body, `"is_active":false`) {
		t.Fatalf("hits %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Reason != "duplicate" || a[0].TargetID != id ||
		!strings.Contains(auditPayload(t, a[0]), `"fields":["is_active","title"]`) || !strings.Contains(auditPayload(t, a[0]), `"is_active":false`) {
		t.Fatalf("audit %+v %s", a, auditPayload(t, a[0]))
	}
	for _, body := range []string{``, `nope`, `[]`} {
		if w := rg.do(http.MethodPatch, soundsPrefix+"/"+id, body, actor, false); w.Code != http.StatusBadRequest || len(rg.takeHits()) != 0 {
			t.Fatalf("%q: %d", body, w.Code)
		}
		rg.takeAudit()
	}
}

func TestSoundPreview_RedirectsWithoutRecordingTheURL(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permSoundsRead)
	id := uuid.NewString()
	signed := "https://minio.test/media/audio-library/x.m4a?X-Amz-Signature=abc"
	rg.on(http.MethodGet, service.MediaSoundsAdminPrefix+"/"+id+"/preview", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, signed, http.StatusTemporaryRedirect)
	})
	w := rg.do(http.MethodGet, soundsPrefix+"/"+id+"/preview", "", actor, false)
	if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != signed || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("preview: %d %v", w.Code, w.Header())
	}
	if hits := rg.takeHits(); len(hits) != 1 || hits[0].verified.Scope[0] != permSoundsRead {
		t.Fatalf("hits %+v", hits)
	}
	a := rg.takeAudit()
	if len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].TargetID != id || strings.Contains(auditPayload(t, a[0]), "Signature") ||
		!strings.Contains(auditPayload(t, a[0]), "link_issued") {
		t.Fatalf("audit %+v", a)
	}

	// An unusable redirect is not passed on.
	rg.on(http.MethodGet, service.MediaSoundsAdminPrefix+"/"+id+"/preview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "javascript:alert(1)")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	if w := rg.do(http.MethodGet, soundsPrefix+"/"+id+"/preview", "", actor, false); w.Code != http.StatusBadGateway || w.Header().Get("Location") != "" {
		t.Fatalf("bad redirect: %d %v", w.Code, w.Header())
	}
	rg.takeHits()
	rg.takeAudit()

	// A missing sound is relayed as media-service answered it.
	rg.on(http.MethodGet, service.MediaSoundsAdminPrefix+"/"+id+"/preview", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"Sound not found"}}`))
	})
	if w := rg.do(http.MethodGet, soundsPrefix+"/"+id+"/preview", "", actor, false); w.Code != http.StatusNotFound || !hasCode(w, "NOT_FOUND") {
		t.Fatalf("missing: %d %s", w.Code, w.Body.String())
	}
}

// Without a signing key every sound route answers 503 and calls nothing.
func TestSoundRoutes_NoKey(t *testing.T) {
	rg := newProductsRig(t, false)
	actor := uuid.NewString()
	rg.perms.grant(actor, soundsAll...)
	w := rg.do(http.MethodGet, soundsPrefix, "", actor, false)
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeProductUnavailable) || len(rg.takeHits()) != 0 {
		t.Fatalf("no key: %d %s", w.Code, w.Body.String())
	}
}

// The navigation shows the page as "Sound library", right after Live.
func TestSoundNavigation(t *testing.T) {
	for i, n := range navigationOrder {
		if n.app == "media" {
			if n.label != "Sound library" || i == 0 || navigationOrder[i-1].app != "live" {
				t.Fatalf("media nav %+v at %d", n, i)
			}
			return
		}
	}
	t.Fatal("media is not in the navigation")
}
