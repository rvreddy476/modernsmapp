package http

// The try-on publish routes exist, are reachable through the launch fence,
// and require a caller.
//
// Proven against the server's own registration and fence rather than a copy:
// the table is read back from the engine RegisterRoutes built, and each path
// goes through FenceMiddleware. With no X-User-Id a registered, unfenced
// route answers 401; a fenced one would answer 404 before routing and an
// unregistered one 404 from gin, so the 401 is what proves the handler ran.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const tryOnProductID = "3c9a1f0b-0000-4000-8000-0000000000a1"

func TestTryOnRoutesAreRegistered(t *testing.T) {
	r := productionEngine(t)
	want := map[string]bool{
		"PUT /v1/commerce/products/:productId/try-on":    false,
		"DELETE /v1/commerce/products/:productId/try-on": false,
	}
	for _, ri := range r.Routes() {
		key := ri.Method + " " + ri.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for key, seen := range want {
		if !seen {
			t.Errorf("%s is not registered; the try-on publish flow calls it", key)
		}
	}
}

// The read has no route of its own on purpose — it rides product detail — so
// a GET must not quietly exist and return something else.
func TestTryOnHasNoReadRouteOfItsOwn(t *testing.T) {
	r := productionEngine(t)
	for _, ri := range r.Routes() {
		if ri.Method == http.MethodGet && strings.HasSuffix(ri.Path, "/try-on") {
			t.Errorf("GET %s is registered; the descriptor is served inside product detail", ri.Path)
		}
	}
}

// `/v1/commerce/products` is a live launch prefix and try-on is a new
// subresource under it. If a later fence entry ever claims it, the publish
// flow would 404 with no other symptom.
func TestTryOnRoutesAreNotFenced(t *testing.T) {
	r := productionEngine(t)
	path := "/v1/commerce/products/" + tryOnProductID + "/try-on"
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s answered 404; the route is fenced or unregistered", method, path)
		}
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s answered %d; an anonymous caller must get 401", method, path, w.Code)
		}
	}
}
