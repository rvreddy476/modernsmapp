package http

// The attribute-schema route table exists, and registering it does not panic.
//
// ─── WHY THIS IS WORTH ITS LINES ────────────────────────────────────────
//
// Two of the new registrations are the shape gin's router is fussiest about:
//
//	PUT   /attribute-definitions/:defId/enum-values/order       a STATIC child…
//	PATCH /attribute-definitions/:defId/enum-values/:valueId    …beside a PARAM one
//
// and the whole admin block reuses `:categoryId`, a parameter name that also
// appears on the public route in a DIFFERENT group. gin panics at REGISTRATION
// on a genuine conflict — and registration happens in cmd/server's init path,
// so the failure mode is a service that will not boot at all rather than one
// route misbehaving.
//
// It also asserts the table itself. Every defect this change set exists to fix
// was a missing route or a field that never reached the wire, never a bug in a
// handler's body.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
)

func TestAttributeRoutesRegisterWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// A nil pool is fine: nothing here executes a query. What is under test is
	// the router, which is built entirely from the registration calls.
	h := New(service.New(postgres.New(nil), nil, "")).WithInternalKey("test-key")
	h.RegisterRoutes(r) // panics on a conflicting route table

	registered := map[string]bool{}
	for _, info := range r.Routes() {
		registered[info.Method+" "+info.Path] = true
	}

	for _, want := range []string{
		// The one public read: the form a category asks for.
		"GET /v1/commerce/categories/:categoryId/attribute-schema",
		// The flat category list is UNCHANGED and still registered — the tree
		// is a query parameter on it, not a second route.
		"GET /v1/commerce/categories",

		// Authoring, behind the internal key.
		"GET /v1/commerce/internal/attribute-definitions",
		"POST /v1/commerce/internal/attribute-definitions",
		"GET /v1/commerce/internal/attribute-definitions/:defId",
		"PATCH /v1/commerce/internal/attribute-definitions/:defId",
		"GET /v1/commerce/internal/attribute-definitions/:defId/impact",
		"GET /v1/commerce/internal/attribute-definitions/:defId/enum-values",
		"POST /v1/commerce/internal/attribute-definitions/:defId/enum-values",
		"PUT /v1/commerce/internal/attribute-definitions/:defId/enum-values/order",
		"PATCH /v1/commerce/internal/attribute-definitions/:defId/enum-values/:valueId",
		"GET /v1/commerce/internal/categories/:categoryId/attributes",
		"PUT /v1/commerce/internal/categories/:categoryId/attributes",
		"POST /v1/commerce/internal/categories",
		"PATCH /v1/commerce/internal/categories/:categoryId",
		"GET /v1/commerce/internal/attribute-schema",
		"POST /v1/commerce/internal/attribute-schema/publish",
	} {
		if !registered[want] {
			t.Errorf("route not registered: %s", want)
		}
	}

	// There is no DELETE anywhere in this family, on purpose. A definition, an
	// option and a category are each referenced by products that already
	// exist; deleting one makes those products' stored values unreadable, so
	// retirement is `is_active = false`. This is what stops a later edit
	// quietly adding one back.
	for _, forbidden := range []string{
		"DELETE /v1/commerce/internal/attribute-definitions/:defId",
		"DELETE /v1/commerce/internal/attribute-definitions/:defId/enum-values/:valueId",
		"DELETE /v1/commerce/internal/categories/:categoryId",
	} {
		if registered[forbidden] {
			t.Errorf("%s is registered; retirement is is_active=false, never a delete", forbidden)
		}
	}
}

// The admin block sits behind the same internal-key middleware the seller
// queue does. Without it, anyone who can reach commerce-service directly can
// rewrite the catalogue's questions.
func TestAttributeAdminRoutesRequireTheInternalKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(postgres.New(nil), nil, "")).WithInternalKey("test-key").RegisterRoutes(r)

	for _, path := range []string{
		"/v1/commerce/internal/attribute-definitions",
		"/v1/commerce/internal/attribute-schema",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Errorf("GET %s with no internal key answered %d; want the key check to refuse it "+
				"(it would otherwise have reached the handler and queried a nil pool)", path, w.Code)
		}
	}
}
