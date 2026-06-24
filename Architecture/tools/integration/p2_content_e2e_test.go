//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// P2 breadth — content domains. Each asserts an authenticated list/discover read
// returns 200 through the gateway (guards routing + read path for the long tail
// of services). Full create→join→moderate journeys are domain-specific
// follow-ups; these establish that the service is reachable and serving.

func get200(t *testing.T, path string) {
	t.Helper()
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	c := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if env := c.MustDo(t, ctx, "GET", path, nil); env.Status != 200 {
		t.Errorf("GET %s: want 200, got %d", path, env.Status)
	}
}

func TestE2E_Group_Discover(t *testing.T)     { get200(t, "/v1/groups/discover") }
func TestE2E_Channel_Discover(t *testing.T)   { get200(t, "/v1/broadcast-channels/discover") }
func TestE2E_Community_Discover(t *testing.T) { get200(t, "/v1/communities/discover") }
func TestE2E_QA_Drafts(t *testing.T)          { get200(t, "/v1/qa/drafts/questions") }
func TestE2E_Memories_Collections(t *testing.T) {
	get200(t, "/v1/memories/collections")
}
