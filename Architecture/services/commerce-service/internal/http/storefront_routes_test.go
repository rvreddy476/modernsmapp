package http

// The storefront's routes exist, and registering them does not panic.
//
// ─── WHY THIS TEST IS WORTH ITS LINES ───────────────────────────────────
//
// Two of the new registrations are the shape gin's router is fussiest about:
//
//	PUT    /products/:productId/media/order      a STATIC child…
//	DELETE /products/:productId/media/:mediaId   …beside a PARAM child
//
// gin resolves that correctly, but it panics at REGISTRATION time on a
// genuine conflict — and registration happens in cmd/server's init path, so
// the failure mode is a service that will not boot at all rather than one
// route misbehaving. `RegisterRoutes` is called here for exactly that reason:
// a compile-time-clean but router-invalid route table is otherwise only
// discovered by deploying it.
//
// It also asserts the route table itself, because every defect this change
// set exists to fix was a route that was missing or a field that never
// reached the wire — never a bug in a handler's body.

import (
	"testing"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
)

func TestStorefrontRoutesRegisterWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// A nil pool is fine: nothing here executes a query. What is under test
	// is the router, which is built entirely from the registration calls.
	h := New(service.New(postgres.New(nil), nil, "")).WithInternalKey("test-key")
	h.RegisterRoutes(r) // panics on a conflicting route table

	registered := map[string]bool{}
	for _, info := range r.Routes() {
		registered[info.Method+" "+info.Path] = true
	}

	for _, want := range []string{
		// The landing page the app had no endpoint to open on.
		"GET /v1/commerce/home",
		// The gallery writes `product_media` had none of, which is why it
		// held zero rows.
		"POST /v1/commerce/products/:productId/media",
		"GET /v1/commerce/products/:productId/media",
		"PUT /v1/commerce/products/:productId/media/order",
		"DELETE /v1/commerce/products/:productId/media/:mediaId",
		// The heart beside the bag.
		"GET /v1/commerce/favourites",
		"POST /v1/commerce/favourites",
		"DELETE /v1/commerce/favourites/:productId",
		// The category strip.
		"GET /v1/commerce/categories",
		// Merchandising, behind the internal key.
		"GET /v1/commerce/internal/banners",
		"POST /v1/commerce/internal/banners",
		"PUT /v1/commerce/internal/banners/:bannerId",
		"DELETE /v1/commerce/internal/banners/:bannerId",
	} {
		if !registered[want] {
			t.Errorf("%s is not registered; the client would get a 404 for a route it is built against", want)
		}
	}
}
