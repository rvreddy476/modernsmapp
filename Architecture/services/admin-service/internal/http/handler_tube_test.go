package http

import (
	"net/http"
	"testing"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/google/uuid"
)

// tubeAll is every tube permission; the "other" admin in routeTableCase
// holds all but the route's.
var tubeAll = []string{permTubeStatsRead, permTubeVideosModerate, permTubeVideosRemove, permTubeChannelsModerate, permTubeReportsAct}

func TestTubeRoutes_EachRequiresItsPermission_AndSignsForIt(t *testing.T) {
	rg := newProductsRig(t, true)
	if len(TubeRoutes) != 17 {
		t.Fatalf("TubeRoutes has %d entries, want 17", len(TubeRoutes))
	}
	for _, rt := range TubeRoutes {
		t.Run(rt.operation+" "+rt.method+" "+rt.path, func(t *testing.T) {
			routeTableCase(t, rg, tubePrefix, service.PostAdminPrefix, "post", "tube", tubeAll, rt, contentCase(rt))
		})
	}
}

func TestTubeAlternativeReads_ScopeToTheHeldPermission(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range TubeRoutes {
		for _, alt := range rt.alternatives {
			actor := uuid.NewString()
			rg.perms.grant(actor, alt)
			path, _ := fill(rt.path)
			w := rg.do(rt.method, tubePrefix+path, "", actor, false)
			hits := rg.takeHits()
			if w.Code != http.StatusOK || len(hits) != 1 || len(hits[0].verified.Scope) != 1 || hits[0].verified.Scope[0] != alt {
				t.Fatalf("%s as %s: %d hits=%+v", rt.operation, alt, w.Code, hits)
			}
		}
	}
}

// No Tube route needs a step-up on its declaration; a video takedown is
// decided from the body (TestContentTakedown_KindAndActionChooseTheScope).
func TestTubeRoutes_StepUp(t *testing.T) {
	rg := newProductsRig(t, true)
	for _, rt := range TubeRoutes {
		if rt.stepUp {
			t.Fatalf("%s declares step-up", rt.operation)
		}
		stepUpCase(t, rg, tubePrefix, tubeAll, rt, contentCase(rt), false)
	}
}

// A Social permission never reaches a Tube route and the other way round:
// the same post-service, two apps.
func TestTubeAndSocial_PermissionsDoNotCross(t *testing.T) {
	rg := newProductsRig(t, true)
	social := uuid.NewString()
	rg.perms.grant(social, postAll[:9]...)
	tube := uuid.NewString()
	rg.perms.grant(tube, postAll[9:]...)
	for _, c := range []struct{ actor, path string }{
		{social, tubePrefix + "/videos/" + uuid.NewString()},
		{social, tubePrefix + "/stats"},
		{social, tubePrefix + "/reports"},
		{tube, socialPrefix + "/reels/" + uuid.NewString()},
		{tube, socialPrefix + "/reports"},
		{tube, socialPrefix + "/comments/moderation"},
	} {
		if w := rg.do(http.MethodGet, c.path, "", c.actor, false); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
			t.Fatalf("%s: %d %s", c.path, w.Code, w.Body.String())
		}
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("a cross-app call reached post-service: %+v", hits)
	}
}

// Copyright title review: a block is a takedown (tube:videos.remove and a
// fresh step-up), a clear needs tube:videos.moderate; an unknown decision
// never reaches post-service. The body forwarded is the console's own.
func TestTubeCopyrightDecision_BlockIsATakedown(t *testing.T) {
	rg := newProductsRig(t, true)
	actor := uuid.NewString()
	rg.perms.grant(actor, permTubeVideosModerate, permTubeVideosRemove)
	path := tubePrefix + "/copyright/reviews/" + uuid.NewString() + "/decision"
	block := `{"decision":"block","note":"pirated upload"}`

	if w := rg.do(http.MethodPost, path, block, actor, false); w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
		t.Fatalf("block without step-up: %d %s", w.Code, w.Body.String())
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("a refused block reached post-service: %+v", hits)
	}
	rg.takeAudit()
	w := rg.do(http.MethodPost, path, block, actor, true)
	hits := rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || len(hits[0].verified.Scope) != 1 || hits[0].verified.Scope[0] != permTubeVideosRemove || hits[0].body != block {
		t.Fatalf("block with step-up: %d hits=%+v", w.Code, hits)
	}
	rg.takeAudit()
	clear := `{"decision":"clear"}`
	w = rg.do(http.MethodPost, path, clear, actor, false)
	hits = rg.takeHits()
	if w.Code != http.StatusOK || len(hits) != 1 || hits[0].verified.Scope[0] != permTubeVideosModerate {
		t.Fatalf("clear: %d hits=%+v", w.Code, hits)
	}
	rg.takeAudit()
	moderator := uuid.NewString()
	rg.perms.grant(moderator, permTubeVideosModerate)
	if w := rg.do(http.MethodPost, path, block, moderator, true); w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
		t.Fatalf("block with moderate only: %d %s", w.Code, w.Body.String())
	}
	if w := rg.do(http.MethodPost, path, `{"decision":"approve"}`, actor, true); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown decision: %d", w.Code)
	}
	if hits := rg.takeHits(); len(hits) != 0 {
		t.Fatalf("refused decisions reached post-service: %+v", hits)
	}
}
