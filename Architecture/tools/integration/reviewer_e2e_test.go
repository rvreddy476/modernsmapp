//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Reviewer_OptInAndKYCGate guards the reviewer onboarding + KYC gate:
//
//	opt-in → /me reflects it → pulling work without verified KYC is refused (403)
//
// The KYC gate (no review work until identity-verified) is the core compliance
// control of the reviewer module; this asserts it fires for a fresh reviewer.
func TestE2E_Reviewer_OptInAndKYCGate(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	rv := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	rv.MustOK(t, ctx, "POST", "/v1/reviewer/opt-in", map[string]any{
		"languages": []string{"en"},
		"region":    "IN",
	})
	rv.MustOK(t, ctx, "GET", "/v1/reviewer/me", nil)

	// No verified KYC → must not be handed review work.
	env := rv.MustDo(t, ctx, "GET", "/v1/reviewer/assignments/next", nil)
	if env.Status != 403 {
		t.Errorf("reviewer without verified KYC should be gated on next: want 403, got %d", env.Status)
	}
}

// TestE2E_Reviewer_AdminEnqueue guards the edge boundary for the reviewer
// intake: /internal/ routes are service-only, so the gateway refuses them from
// the edge with 404 whatever the token carries, admin included. Enqueue is
// reached in-cluster by the services that produce review work.
func TestE2E_Reviewer_AdminEnqueue(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	admin := NewHTTPClient(urls.APIGateway, uuid.New()).WithAdminRole()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if env := admin.MustDo(t, ctx, "POST", "/v1/reviewer/internal/enqueue", map[string]any{
		"content_id":      uuid.NewString(),
		"creator_id":      uuid.NewString(),
		"content_type":    "flick",
		"languages":       []string{"en"},
		"content_seconds": 30,
	}); env.Status != 404 {
		t.Errorf("admin on /internal/ enqueue from the edge: want 404, got %d", env.Status)
	}

	// A plain user is refused the same way.
	normal := NewHTTPClient(urls.APIGateway, uuid.New())
	env := normal.MustDo(t, ctx, "POST", "/v1/reviewer/internal/enqueue", map[string]any{
		"content_id": uuid.NewString(), "creator_id": uuid.NewString(),
	})
	if env.Status != 404 {
		t.Errorf("non-admin on /internal/ enqueue from the edge: want 404, got %d", env.Status)
	}
}
