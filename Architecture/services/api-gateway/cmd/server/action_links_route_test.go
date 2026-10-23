package main

import (
	"testing"

	"github.com/atpost/api-gateway/pkg/internalroutes"
)

// The Action button's public redirect read (GET /v1/links/:id) is served by
// post-service; user-service never registered a /v1/links route. Stamped
// with the internal key like every post-service prefix (post-service
// requires it engine-wide), and the edge never refuses it for want of a
// token: the read is public.
func TestActionLinksPrefixRoutesToPostService(t *testing.T) {
	t.Setenv("POST_SERVICE_URL", "")
	t.Setenv("USER_SERVICE_URL", "")
	found := false
	for _, rd := range routeDefinitions() {
		if rd.prefix != "/v1/links" {
			continue
		}
		found = true
		if rd.target != "http://post-service:8084" {
			t.Fatalf("/v1/links -> %s, want post-service", rd.target)
		}
	}
	if !found {
		t.Fatal("no /v1/links route")
	}
	if stamp, ok := internalroutes.StampPolicy["/v1/links"]; !ok || !stamp {
		t.Fatalf("/v1/links stamp policy = %v (declared %v), want stamped", stamp, ok)
	}
}
