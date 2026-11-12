package main

import (
	"testing"

	"github.com/atpost/api-gateway/pkg/internalroutes"
)

// Echo (docs/designs/echo-contract.md): /v1/notes goes to post-service,
// stamped like every post-service prefix; /v1/feed/notes stays with
// feed-service through /v1/feed.
func TestNotesPrefixRoutesToPostService(t *testing.T) {
	t.Setenv("POST_SERVICE_URL", "")
	t.Setenv("FEED_SERVICE_URL", "")
	targets := map[string]string{}
	for _, rd := range routeDefinitions() {
		targets[rd.prefix] = rd.target
	}
	if got := targets["/v1/notes"]; got != "http://post-service:8084" {
		t.Fatalf("/v1/notes -> %q, want post-service", got)
	}
	if got := targets["/v1/feed"]; got != "http://feed-service:8086" {
		t.Fatalf("/v1/feed -> %q, want feed-service", got)
	}
	if _, ok := targets["/v1/feed/notes"]; ok {
		t.Fatal("/v1/feed/notes must ride /v1/feed, not its own prefix")
	}
	if stamp, ok := internalroutes.StampPolicy["/v1/notes"]; !ok || !stamp {
		t.Fatalf("/v1/notes stamp policy = %v (declared %v), want stamped", stamp, ok)
	}
}
