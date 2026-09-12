package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Groups and communities have no client on any platform, so their public
// prefixes are closed by default and open only by an explicit flag. The gate
// must answer 404, not 503, so the edge does not confirm the product exists.
func TestDormantProductsAreClosedByDefault(t *testing.T) {
	for _, path := range []string{"/v1/groups", "/v1/groups/abc/posts", "/v1/communities", "/v1/communities/x/join"} {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if !serveDormantProductGate(res, req, false) {
			t.Fatalf("%s was not gated", path)
		}
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", path, res.Code)
		}
	}
}

func TestDormantProductsGateDoesNotTouchNeighbours(t *testing.T) {
	for _, path := range []string{"/v1/groupsx", "/v1/broadcast-channels", "/v1/community-service", "/v1/chat/requests"} {
		res := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if serveDormantProductGate(res, req, false) {
			t.Fatalf("%s was gated but is not a dormant product", path)
		}
	}
}

func TestDormantProductsOpenWhenEnabled(t *testing.T) {
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/groups", nil)
	if serveDormantProductGate(res, req, true) {
		t.Fatal("enabled dormant product was intercepted")
	}
}
