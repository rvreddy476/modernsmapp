//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// P2 breadth — discovery / event / platform domains. Read smokes (200) plus the
// ai moderation endpoint as a POST smoke. Event-driven cross-service assertions
// (e.g. "post → Eventually appears in search index / notifications") are a
// deeper follow-up; these guard reachability + the read path.

func TestE2E_Search_Query(t *testing.T)      { get200(t, "/v1/search?q=test") }
func TestE2E_Notification_List(t *testing.T) { get200(t, "/v1/notifications") }
func TestE2E_Analytics_Overview(t *testing.T) {
	get200(t, "/v1/analytics/overview")
}
func TestE2E_Suggestion_List(t *testing.T) { get200(t, "/v1/suggestions") }
func TestE2E_FeatureFlag_Me(t *testing.T)  { get200(t, "/v1/flags/me") }

// TestE2E_AI_Moderation hits the (stub) moderation classifier — POST returns a
// verdict for arbitrary text.
func TestE2E_AI_Moderation(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	c := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if env := c.MustDo(t, ctx, "POST", "/v1/ai/moderation/check", map[string]any{
		"text": "e2e benign sample text",
	}); env.Status != 200 {
		t.Errorf("POST /v1/ai/moderation/check: want 200, got %d", env.Status)
	}
}
