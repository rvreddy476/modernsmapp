package http

// The read-back route table, and the buyer-facing shape it must not touch.
//
// ─── WHY THE ROUTE TEST ─────────────────────────────────────────────────
//
// `/products/search-docs` is a STATIC segment registered beside the PARAM
// segment `/products/:productId`. gin resolves that correctly but panics at
// REGISTRATION on a genuine conflict — and registration happens in
// cmd/server's init path, so the failure mode is a service that will not
// boot at all rather than one route misbehaving.
//
// ─── AND WHY THE "UNCHANGED" TEST ───────────────────────────────────────
//
// This step adds routes; it must not move any existing one. The buyer-
// facing product surface is what the phone and the storefront read, and a
// path that quietly changed shape here would break them with nothing in
// this service failing.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
)

func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// A nil pool is fine: nothing here executes a query. What is under test
	// is the router, built entirely from the registration calls.
	New(service.New(postgres.New(nil), nil, "")).WithInternalKey("test-key").RegisterRoutes(r)

	out := map[string]bool{}
	for _, info := range r.Routes() {
		out[info.Method+" "+info.Path] = true
	}
	return out
}

func TestSearchDocRoutesRegisterWithoutConflict(t *testing.T) {
	registered := registeredRoutes(t)

	for _, want := range []string{
		// The read-back the search consumer calls after every visibility event.
		"GET /v1/commerce/internal/products/:productId/search-doc",
		// The keyset walk a reindex pages through.
		"GET /v1/commerce/internal/products/search-docs",
		// The filterable attribute definitions a facet rail is built from.
		"GET /v1/commerce/internal/search-facets",
		// The static sibling that was already there — proof the pattern is
		// one gin accepts rather than one this change got away with.
		"GET /v1/commerce/internal/products/queue",
	} {
		if !registered[want] {
			t.Errorf("route not registered: %s", want)
		}
	}
}

// The read-back is internal. The document carries a listing's lifecycle
// state — including for products no buyer may see — which is the seller's
// private business, and it is shaped for an indexer rather than a client.
func TestSearchDocRoutesRequireTheInternalKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(postgres.New(nil), nil, "")).WithInternalKey("test-key").RegisterRoutes(r)

	for _, path := range []string{
		"/v1/commerce/internal/products/search-docs",
		"/v1/commerce/internal/products/11111111-1111-1111-1111-111111111111/search-doc",
		"/v1/commerce/internal/search-facets",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Errorf("GET %s with no internal key answered %d; a seller's unapproved listings "+
				"must not be readable by anyone who can reach this service directly", path, w.Code)
		}
	}
}

// TestBuyerFacingProductRoutesAreUnchanged pins the paths this step is
// forbidden from touching.
func TestBuyerFacingProductRoutesAreUnchanged(t *testing.T) {
	registered := registeredRoutes(t)

	for _, want := range []string{
		"GET /v1/commerce/products",
		"GET /v1/commerce/products/:productId",
		"GET /v1/commerce/products/:productId/preview",
		"POST /v1/commerce/products",
		"PATCH /v1/commerce/products/:productId",
		"GET /v1/commerce/products/:productId/variants",
		"GET /v1/commerce/products/:productId/attributes",
		"GET /v1/commerce/products/:productId/reviews",
		"GET /v1/commerce/products/:productId/media",
		"GET /v1/commerce/categories",
		"GET /v1/commerce/categories/:categoryId/attribute-schema",
		"GET /v1/commerce/sellers/:sellerId/products",
	} {
		if !registered[want] {
			t.Errorf("buyer-facing route is missing: %s.\n"+
				"This step adds a read-back and a facet source; it must not move anything the "+
				"phone or the storefront already calls.", want)
		}
	}
}
