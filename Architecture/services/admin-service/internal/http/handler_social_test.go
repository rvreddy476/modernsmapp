package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
)

// contentCase gives each post-service route a body its declared permission
// is the one for (no takedown: approve, approved, visible).
func contentCase(rt productRoute) string {
	switch {
	case strings.HasSuffix(rt.operation, "."+opContentDecide):
		return `{"decision_id":"` + uuid.NewString() + `","action":"approve","reason":"fine"}`
	case strings.HasSuffix(rt.operation, "."+opContentReviewStatus):
		return `{"post_id":"` + uuid.NewString() + `","status":"approved","reason":"ok"}`
	case strings.HasSuffix(rt.operation, "."+opContentVisibility):
		return `{"post_id":"` + uuid.NewString() + `","visibility":"public","reason":"promote"}`
	case rt.operation == opSocialCommentDecid:
		return `{"status":"visible"}`
	case rt.operation == "social.report.review", rt.operation == "tube.report.review":
		return `{"status":"reviewed","review_note":"seen"}`
	}
	if rt.method == http.MethodGet {
		return ""
	}
	return `{"reason":"checked"}`
}

func TestSocialRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(SocialContentRoutes) != 21 || len(SocialPagesRoutes) != 10 {
		t.Fatalf("SocialContentRoutes=%d SocialPagesRoutes=%d, want 21 and 10", len(SocialContentRoutes), len(SocialPagesRoutes))
	}
	for _, rt := range SocialContentRoutes {
		if rt.operation == opSocialStats { // merged from two products: its own test
			continue
		}
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, socialPrefix, service.PostAdminPrefix, "post", "social", socialAll, rt, contentCase(rt))
		})
	}
	for _, rt := range SocialPagesRoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, socialPrefix, service.UserPagesAdminPrefix, "social", "social", socialAll, rt, contentCase(rt))
		})
	}
}

// A read admitted under moderate or remove is scoped to the one the admin
// holds, never to one they lack.
func TestSocialAlternativeReads_ScopeToTheHeldPermission(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range SocialContentRoutes {
		for _, alt := range rt.alternatives {
			actor := uuid.NewString()
			rg.perms.grant(actor, alt)
			path, _ := fill(rt.path)
			w := rg.do(rt.method, socialPrefix+path, "", actor, false)
			hits := rg.takeHits()
			if w.Code != http.StatusOK || len(hits) != 1 || len(hits[0].verified.Scope) != 1 || hits[0].verified.Scope[0] != alt {
				t.Fatalf("%s as %s: %d hits=%+v", rt.operation, alt, w.Code, hits)
			}
		}
	}
}

func TestSocialRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range SocialContentRoutes {
		if rt.operation == opSocialStats {
			continue
		}
		stepUpCase(t, rg, socialPrefix, socialAll, rt, contentCase(rt), rt.stepUp)
	}
	// Pages: suspend, disable, reinstate and every document route.
	want := map[string]bool{
		"social.page.suspend": true, "social.page.disable": true, "social.page.reinstate": true,
		"social.page.documents.list": true, "social.page.document.approve": true, "social.page.document.reject": true,
	}
	for _, rt := range SocialPagesRoutes {
		if rt.stepUp != want[rt.operation] {
			t.Fatalf("%s declares step-up=%v, want %v", rt.operation, rt.stepUp, want[rt.operation])
		}
		stepUpCase(t, rg, socialPrefix, socialAll, rt, contentCase(rt), rt.stepUp)
	}
}

// The kind in the console URL and the action in the body choose the scope:
// a takedown (reject / rejected) signs the kind's .remove and needs a step-up;
// anything else signs the kind's .moderate. Decided before post-service is
// called.
func TestContentTakedown_KindAndActionChooseTheScope(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, postAll...)
	decision := func(action string) string {
		return `{"decision_id":"` + uuid.NewString() + `","action":"` + action + `","reason":"policy"}`
	}
	review := func(status string) string {
		return `{"post_id":"` + uuid.NewString() + `","status":"` + status + `","reason":"policy"}`
	}
	for _, c := range []struct {
		prefix, path, body, scope string
		stepUp                    bool
	}{
		{socialPrefix, "/posts/" + uuid.NewString() + "/moderation", decision("reject"), permSocialPostsRemove, true},
		{socialPrefix, "/posts/" + uuid.NewString() + "/moderation", decision(" REJECT "), permSocialPostsRemove, true},
		{socialPrefix, "/posts/" + uuid.NewString() + "/moderation", decision("approve"), permSocialPostsModerate, false},
		{socialPrefix, "/posts/" + uuid.NewString() + "/moderation", decision("needs_changes"), permSocialPostsModerate, false},
		{socialPrefix, "/reels/" + uuid.NewString() + "/moderation", decision("reject"), permSocialReelsRemove, true},
		{socialPrefix, "/reels/" + uuid.NewString() + "/moderation", decision("approve"), permSocialReelsModerate, false},
		{tubePrefix, "/videos/" + uuid.NewString() + "/moderation", decision("reject"), permTubeVideosRemove, true},
		{tubePrefix, "/videos/" + uuid.NewString() + "/moderation", decision("approve"), permTubeVideosModerate, false},
		{socialPrefix, "/posts/review-status", review("rejected"), permSocialPostsRemove, true},
		{socialPrefix, "/posts/review-status", review("approved"), permSocialPostsModerate, false},
		{socialPrefix, "/reels/review-status", review("rejected"), permSocialReelsRemove, true},
		{tubePrefix, "/videos/review-status", review("rejected"), permTubeVideosRemove, true},
		{tubePrefix, "/videos/review-status", review("approved"), permTubeVideosModerate, false},
		{socialPrefix, "/posts/visibility", `{"post_id":"` + uuid.NewString() + `","visibility":"public"}`, permSocialPostsModerate, false},
		{tubePrefix, "/videos/visibility", `{"post_id":"` + uuid.NewString() + `","visibility":"public"}`, permTubeVideosModerate, false},
	} {
		w := rg.do(http.MethodPost, c.prefix+c.path, c.body, actor, false)
		hits := rg.takeHits()
		audit := rg.takeAudit()
		if c.stepUp {
			if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) || len(hits) != 0 {
				t.Fatalf("%s %s without step-up: %d hits=%d body=%s", c.path, c.body, w.Code, len(hits), w.Body.String())
			}
			if len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomeDenied {
				t.Fatalf("%s audit %+v", c.path, audit)
			}
			w = rg.do(http.MethodPost, c.prefix+c.path, c.body, actor, true)
			hits = rg.takeHits()
			audit = rg.takeAudit()
		}
		if w.Code != http.StatusOK || len(hits) != 1 || len(hits[0].verified.Scope) != 1 || hits[0].verified.Scope[0] != c.scope {
			t.Fatalf("%s %s: %d hits=%+v, want scope %s", c.path, c.body, w.Code, hits, c.scope)
		}
		if hits[0].body != c.body {
			t.Fatalf("%s: body forwarded %q, want the console's %q", c.path, hits[0].body, c.body)
		}
		if len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomeSuccess || audit[0].TargetID == "" || audit[0].TargetID == c.prefix+c.path {
			t.Fatalf("%s audit %+v, want one success row targeting the post", c.path, audit)
		}
	}

	// Holding only moderate: a takedown is refused before the call. Holding
	// only remove: approve is refused, reject (with step-up) goes through.
	moderator := uuid.NewString()
	rg.perms.grant(moderator, permSocialReelsModerate)
	path := socialPrefix + "/reels/" + uuid.NewString() + "/moderation"
	if w := rg.do(http.MethodPost, path, decision("reject"), moderator, true); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("reject with moderate only: %d %s", w.Code, w.Body.String())
	}
	remover := uuid.NewString()
	rg.perms.grant(remover, permSocialReelsRemove)
	if w := rg.do(http.MethodPost, path, decision("approve"), remover, true); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("approve with remove only: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("refused decisions reached post-service: %+v", hits)
	}
	rg.takeAudit()
	if w := rg.do(http.MethodPost, path, decision("reject"), remover, true); w.Code != http.StatusOK {
		t.Fatalf("reject with remove: %d %s", w.Code, w.Body.String())
	}
	rg.takeHits()
	rg.takeAudit()

	// Unknown or missing actions never reach post-service.
	for _, body := range []string{decision("delete"), `{}`, ``, `not json`, review("hidden"), `{"status":"rejected"}`} {
		for _, p := range []string{"/posts/" + uuid.NewString() + "/moderation", "/posts/review-status"} {
			if w := rg.do(http.MethodPost, socialPrefix+p, body, actor, true); w.Code != http.StatusBadRequest {
				t.Fatalf("%s %q: %d %s", p, body, w.Code, w.Body.String())
			}
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("invalid bodies reached post-service: %+v", hits)
	}
}

// The kind is a claim: post-service compares the token's scope with the
// post's STORED kind. A stub post-service that stores the post as a video
// refuses a takedown signed as a reel (social:reels.remove) and accepts the
// same takedown from the Tube route (tube:videos.remove). admin-service
// records the refusal on its audit row.
func TestContentKind_PostServiceReChecksTheStoredKind(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, postAll...)
	postID := uuid.NewString()
	storedKind := "video"
	rg.onHit(http.MethodPost, service.PostAdminPrefix+"/posts/"+postID+"/moderation", func(w http.ResponseWriter, _ *http.Request, hit productHit) {
		// post-service: a reject needs kindRemovePerm[storedKind] in the scope.
		want := map[string]string{"post": permSocialPostsRemove, "reel": permSocialReelsRemove, "video": permTubeVideosRemove}[storedKind]
		if len(hit.verified.Scope) != 1 || hit.verified.Scope[0] != want {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"ADMIN_PERMISSION_NOT_IN_TOKEN"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"status":"rejected"}}`))
	})
	body := `{"decision_id":"` + uuid.NewString() + `","action":"reject","reason":"policy"}`

	// Mislabelled as a reel: signed social:reels.remove, refused by post-service.
	w := rg.do(http.MethodPost, socialPrefix+"/reels/"+postID+"/moderation", body, actor, true)
	hits := rg.takeHits()
	audit := rg.takeAudit()
	if w.Code != http.StatusForbidden || len(hits) != 1 || hits[0].verified.Scope[0] != permSocialReelsRemove {
		t.Fatalf("reel-labelled video takedown: %d hits=%+v body=%s", w.Code, hits, w.Body.String())
	}
	if len(audit) != 1 || audit[0].Outcome != postgres.AuditOutcomeFailure || audit[0].StatusCode != http.StatusForbidden {
		t.Fatalf("audit %+v, want one failure row with the upstream 403", audit)
	}
	// Labelled as it is stored: accepted.
	w = rg.do(http.MethodPost, tubePrefix+"/videos/"+postID+"/moderation", body, actor, true)
	hits = rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].verified.Scope[0] != permTubeVideosRemove {
		t.Fatalf("video takedown: %d hits=%+v", w.Code, hits)
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].App != tubeAuditApp {
		t.Fatalf("audit %+v", a)
	}
}

func TestSocialReviewQueue_KindIsFixedByTheRoute(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, postAll...)
	for _, c := range []struct{ prefix, seg, kind string }{
		{socialPrefix, "posts", "post"}, {socialPrefix, "reels", "reel"}, {tubePrefix, "videos", "video"},
	} {
		w := rg.do(http.MethodGet, c.prefix+"/"+c.seg+"/review-queue?queue=staged&limit=10", "", actor, false)
		hits := rg.takeHits()
		if w.Code != http.StatusOK || len(hits) != 1 || hits[0].path != service.PostAdminPrefix+"/posts/review-queue" {
			t.Fatalf("%s: %d hits=%+v", c.seg, w.Code, hits)
		}
		if q := hits[0].query; !strings.Contains(q, "kind="+c.kind) || !strings.Contains(q, "queue=staged") || !strings.Contains(q, "limit=10") {
			t.Fatalf("%s: query %q", c.seg, q)
		}
		if w := rg.do(http.MethodGet, c.prefix+"/"+c.seg+"/review-queue?kind=video", "", actor, false); c.kind != "video" && w.Code != http.StatusBadRequest {
			t.Fatalf("%s with kind=video: %d", c.seg, w.Code)
		}
		rg.takeHits()
		rg.takeAudit()
	}
}

func TestSocialComment_StatusChoosesTheScope(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permSocialCommentsModerate, permSocialCommentsRemove)
	for _, c := range []struct {
		status, scope string
		stepUp        bool
	}{
		{"hidden", permSocialCommentsRemove, true}, {"removed", permSocialCommentsRemove, true},
		{"visible", permSocialCommentsModerate, false}, {"review", permSocialCommentsModerate, false},
	} {
		path := socialPrefix + "/comments/" + uuid.NewString() + "/moderation"
		body := `{"status":"` + c.status + `"}`
		w := rg.do(http.MethodPatch, path, body, actor, false)
		hits := rg.takeHits()
		rg.takeAudit()
		if c.stepUp {
			if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) || len(hits) != 0 {
				t.Fatalf("%s without step-up: %d hits=%d", c.status, w.Code, len(hits))
			}
			w = rg.do(http.MethodPatch, path, body, actor, true)
			hits = rg.takeHits()
			rg.takeAudit()
		}
		if w.Code != http.StatusOK || len(hits) != 1 || hits[0].verified.Scope[0] != c.scope {
			t.Fatalf("%s: %d hits=%+v", c.status, w.Code, hits)
		}
	}
	if w := rg.do(http.MethodPatch, socialPrefix+"/comments/"+uuid.NewString()+"/moderation", `{"status":"deleted"}`, actor, true); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown status: %d", w.Code)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("an unknown status reached post-service")
	}
}

// merged decodes a merged stats answer (200 data or 503 details).
func merged(t *testing.T, w *httptest.ResponseRecorder) MergedStats {
	t.Helper()
	var env struct {
		Data  *MergedStats `json:"data"`
		Error *struct {
			Code    string      `json:"code"`
			Details MergedStats `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("merged stats body %s: %v", w.Body.String(), err)
	}
	if env.Data != nil {
		return *env.Data
	}
	if env.Error == nil {
		t.Fatalf("no merged stats in %s", w.Body.String())
	}
	return env.Error.Details
}

// Social stats are post-service's and user-service's, side by side. A source
// that fails is shown as unavailable with the reason — never as zeros.
func TestSocialStats_MergedAndAFailedSourceIsUnavailableNotZero(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permSocialStatsRead)
	rg.on(http.MethodGet, service.PostAdminPrefix+"/stats", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"flagged_posts":3,"open_reports":1}}`))
	})
	rg.on(http.MethodGet, service.UserPagesAdminPrefix+"/stats", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"pages":{"pending":2}}}`))
	})
	w := rg.do(http.MethodGet, socialPrefix+"/stats", "", actor, false)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 2 {
		t.Fatalf("both up: %d hits=%d body=%s", w.Code, len(hits), w.Body.String())
	}
	auds := map[string]bool{}
	for _, h := range hits {
		if h.verifyErr != nil || len(h.verified.Scope) != 1 || h.verified.Scope[0] != permSocialStatsRead || h.verified.Actor != actor {
			t.Fatalf("stats hit %+v", h)
		}
		auds[h.aud] = true
	}
	if !auds["post"] || !auds["social"] {
		t.Fatalf("audiences %v, want post and social", auds)
	}
	m := merged(t, w)
	if !m.Complete || m.Parts["content"].Status != StatsOK || m.Parts["pages"].Status != StatsOK ||
		!strings.Contains(string(m.Parts["content"].Stats), `"flagged_posts":3`) || !strings.Contains(string(m.Parts["pages"].Stats), `"pending":2`) {
		t.Fatalf("merged %s", w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeSuccess || a[0].Payload["unavailable"] != nil {
		t.Fatalf("audit %+v", a)
	}

	// user-service down: pages unavailable with the upstream status, content
	// still shown, complete=false, the audit row names the missing source.
	rg.on(http.MethodGet, service.UserPagesAdminPrefix+"/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"USER_ADMIN_STATS_FAILED"}}`))
	})
	w = rg.do(http.MethodGet, socialPrefix+"/stats", "", actor, false)
	rg.takeHits()
	m = merged(t, w)
	pages := m.Parts["pages"]
	if w.Code != http.StatusOK || m.Complete || pages.Status != StatsUnavailable || pages.UpstreamStatus != 500 ||
		pages.Error != "answered 500" || len(pages.Stats) != 0 || m.Parts["content"].Status != StatsOK {
		t.Fatalf("pages down: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"pending":0`) {
		t.Fatalf("a failed source was shown as zeros: %s", w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || fmt.Sprint(a[0].Payload["unavailable"]) != "[pages]" {
		t.Fatalf("audit %+v, want unavailable=[pages]", a)
	}

	// A 200 without a data object is malformed, not zeros.
	rg.on(http.MethodGet, service.UserPagesAdminPrefix+"/stats", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	w = rg.do(http.MethodGet, socialPrefix+"/stats", "", actor, false)
	rg.takeHits()
	rg.takeAudit()
	if m = merged(t, w); m.Parts["pages"].Status != StatsUnavailable || m.Parts["pages"].Error != "malformed stats" {
		t.Fatalf("malformed: %s", w.Body.String())
	}

	// Neither answers: 503 STATS_UNAVAILABLE carrying both parts, audited as a failure.
	rg.on(http.MethodGet, service.PostAdminPrefix+"/stats", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	w = rg.do(http.MethodGet, socialPrefix+"/stats", "", actor, false)
	rg.takeHits()
	if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeStatsUnavailable) {
		t.Fatalf("both down: %d %s", w.Code, w.Body.String())
	}
	m = merged(t, w)
	if m.Complete || m.Parts["content"].Status != StatsUnavailable || m.Parts["content"].UpstreamStatus != 502 || m.Parts["pages"].Status != StatsUnavailable {
		t.Fatalf("both down parts: %s", w.Body.String())
	}
	if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure || fmt.Sprint(a[0].Payload["unavailable"]) != "[content pages]" {
		t.Fatalf("audit %+v", a)
	}

	// Without a permission: refused before either source is called.
	if w := rg.do(http.MethodGet, socialPrefix+"/stats", "", uuid.NewString(), false); w.Code != http.StatusForbidden {
		t.Fatalf("no permission: %d", w.Code)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("stats reached a product without a permission")
	}
}

func TestSocialStats_NoKeyIsUnavailableOnEveryPart(t *testing.T) {
	rg := newProductsRig(t, false)
	actor := uuid.NewString()
	rg.perms.grant(actor, permSocialStatsRead, permChatStatsRead)
	for _, path := range []string{socialPrefix + "/stats", chatPrefix + "/stats"} {
		w := rg.do(http.MethodGet, path, "", actor, false)
		if w.Code != http.StatusServiceUnavailable || !hasCode(w, CodeStatsUnavailable) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		for name, part := range merged(t, w).Parts {
			if part.Status != StatsUnavailable || part.Error != "service token key not configured" {
				t.Fatalf("%s %s: %+v", path, name, part)
			}
		}
		if a := rg.takeAudit(); len(a) != 1 || a[0].Outcome != postgres.AuditOutcomeFailure {
			t.Fatalf("%s audit %+v", path, a)
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatal("reached a product without a key")
	}
}

func TestMe_ContentNavigationOnlyWithAPermissionInThatApp(t *testing.T) {
	rg := newProductsRig(t, true)
	apps := []string{"social", "tube", "qa", "chat"}
	cases := map[string]struct {
		perm string
		want string
	}{
		"social posts": {permSocialPostsModerate, "social"},
		"social pages": {permSocialPagesModerate, "social"},
		"tube":         {permTubeVideosModerate, "tube"},
		"qa":           {permQAReportsAct, "qa"},
		"chat":         {permChatReportsAct, "chat"},
		"feast only":   {permFoodOrdersRead, ""},
	}
	for name, tc := range cases {
		actor := uuid.NewString()
		rg.perms.grant(actor, tc.perm)
		w := rg.do(http.MethodGet, "/v1/admin/me", "", actor, false)
		for _, app := range apps {
			if got := strings.Contains(w.Body.String(), `"app":"`+app+`"`); got != (app == tc.want) {
				t.Fatalf("%s: %s in navigation = %v (%s)", name, app, got, w.Body.String())
			}
		}
	}
}
