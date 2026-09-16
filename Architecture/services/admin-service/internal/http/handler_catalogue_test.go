package http

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/atpost/admin-service/internal/adminauth"
)

// The catalogue editor's routes now reach commerce's token family: no
// internal key, a token scoped to commerce:catalogue.edit, and only the
// routes listed in CatalogueRoutes.

const (
	catalogueModerator = "44444444-4444-4444-8444-444444444444"
	catalogueEditor    = "55555555-5555-4555-8555-555555555555"
)

func catalogueRig(t *testing.T) *rig {
	t.Helper()
	rg := newRig(t, rigOpts{})
	rg.perms.grant(catalogueModerator, permProductsModerate)
	rg.perms.grant(catalogueEditor, permProductsModerate, permCatalogueEdit)
	return rg
}

func TestTheConsoleReachesCatalogueRoutesWithAScopedToken(t *testing.T) {
	rg := catalogueRig(t)
	w := rg.do(http.MethodGet, "/v1/admin/commerce/catalogue/attribute-definitions?limit=50&active=true", "", catalogueEditor)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	hits, actor, paths, _ := rg.seen.snapshot()
	if hits != 1 || paths[0] != "/v1/commerce/internal/admin/attribute-definitions" || actor != catalogueEditor {
		t.Fatalf("hits=%d paths=%v actor=%q", hits, paths, actor)
	}
	if len(rg.seen.scope) != 1 || rg.seen.scope[0] != permCatalogueEdit || rg.seen.keyHdr != "" || rg.seen.userHdr != "" {
		t.Fatalf("scope=%v key sent=%v X-User-Id=%q", rg.seen.scope, rg.seen.keyHdr != "", rg.seen.userHdr)
	}
}

func TestEveryConsoleCataloguePathIsDeclared(t *testing.T) {
	// The paths apps/admin/src/hooks/useCatalogue.ts calls.
	rg := catalogueRig(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "attribute-schema"},
		{http.MethodGet, "attribute-definitions"},
		{http.MethodGet, "attribute-definitions/d1/enum-values"},
		{http.MethodGet, "attribute-definitions/d1/impact"},
		{http.MethodGet, "categories/c1/attributes"},
		{http.MethodPost, "categories"},
		{http.MethodPatch, "categories/c1"},
		{http.MethodPut, "categories/c1/attributes"},
		{http.MethodPost, "attribute-definitions"},
		{http.MethodPatch, "attribute-definitions/d1"},
		{http.MethodPost, "attribute-definitions/d1/enum-values"},
		{http.MethodPatch, "attribute-definitions/d1/enum-values/v1"},
		{http.MethodPut, "attribute-definitions/d1/enum-values/order"},
		{http.MethodPost, "attribute-schema/publish"},
	} {
		w := rg.do(c.method, "/v1/admin/commerce/catalogue/"+c.path+"?ack_impact=4", `{"x":1}`, catalogueEditor)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: status %d %s", c.method, c.path, w.Code, w.Body.String())
		}
		_, _, paths, bodies := rg.seen.snapshot()
		last := len(paths) - 1
		if paths[last] != "/v1/commerce/internal/admin/"+c.path {
			t.Fatalf("%s %s went to %q", c.method, c.path, paths[last])
		}
		if c.method != http.MethodGet && bodies[last] != `{"x":1}` {
			t.Fatalf("%s %s body %q", c.method, c.path, bodies[last])
		}
	}
}

func TestOnlyTheCatalogueIsReachable(t *testing.T) {
	for _, path := range []string{
		"/v1/admin/commerce/catalogue/sellers/queue",
		"/v1/admin/commerce/catalogue/payouts",
		"/v1/admin/commerce/catalogue/products/123/approve",
		"/v1/admin/commerce/catalogue/categories/..%2f..%2fsellers/queue",
		"/v1/admin/commerce/catalogue/categories/../../sellers/queue/attributes",
	} {
		rg := catalogueRig(t)
		w := rg.do(http.MethodGet, path, "", catalogueEditor)
		if w.Code == http.StatusOK {
			t.Fatalf("%s: status %d", path, w.Code)
		}
		if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
			t.Fatalf("%s reached commerce", path)
		}
	}
}

func TestCatalogueNeedsCatalogueEditAndPublishNeedsStepUp(t *testing.T) {
	rg := catalogueRig(t)
	// Commerce admits catalogue reads only with catalogue.edit, so a products
	// moderator is refused here rather than by commerce.
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		w := rg.do(method, "/v1/admin/commerce/catalogue/attribute-definitions", "{}", catalogueModerator)
		if w.Code != http.StatusForbidden || !hasCode(w, CodePermissionDenied) {
			t.Fatalf("moderator %s: %d %s", method, w.Code, w.Body.String())
		}
	}
	w := rg.do(http.MethodPost, "/v1/admin/commerce/catalogue/attribute-schema/publish", "{}", catalogueEditor, stepUpAgo(10*time.Minute))
	if w.Code != http.StatusForbidden || !hasCode(w, adminauth.CodeStepUpRequired) {
		t.Fatalf("publish without step-up: %d %s", w.Code, w.Body.String())
	}
	if w := rg.do(http.MethodPost, "/v1/admin/commerce/catalogue/attribute-definitions", "{}", catalogueEditor, noStepUp); w.Code != http.StatusOK {
		t.Fatalf("authoring asked for step-up: %d", w.Code)
	}
	if hits, _, paths, _ := rg.seen.snapshot(); hits != 1 || !strings.HasSuffix(paths[0], "/attribute-definitions") {
		t.Fatalf("hits=%d paths=%v", hits, paths)
	}
}

func TestACatalogueBodyMustBeJSON(t *testing.T) {
	rg := catalogueRig(t)
	w := rg.do(http.MethodPost, "/v1/admin/commerce/catalogue/categories", "not json", catalogueEditor)
	if w.Code != http.StatusBadRequest || !hasCode(w, CodeInvalidBody) {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if hits, _, _, _ := rg.seen.snapshot(); hits != 0 {
		t.Fatal("a non-JSON body was forwarded")
	}
}
