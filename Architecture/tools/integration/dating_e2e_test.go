//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Dating_ProfileAndMatches is a dating read smoke: a fresh user can hit
// the profile and matches endpoints through the gateway without a server error
// (no profile yet → 200 empty or 404). Guards routing + the read path.
//
// The full profile → discovery → mutual-like → match → chat-handoff journey
// needs two coordinated actors + the like/swipe write shapes; tracked as a
// deeper dating follow-up (it also exercises the anti-abuse velocity checks).
func TestE2E_Dating_ProfileAndMatches(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	u := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if env := u.MustDo(t, ctx, "GET", "/v1/dating/profile", nil); env.Status >= 500 {
		t.Errorf("GET /v1/dating/profile: server error %d (want 2xx/4xx)", env.Status)
	}
	if env := u.MustDo(t, ctx, "GET", "/v1/dating/matches", nil); env.Status >= 500 {
		t.Errorf("GET /v1/dating/matches: server error %d (want 2xx/4xx)", env.Status)
	}
}
