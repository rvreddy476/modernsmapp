//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_TrustSafety_FileReport exercises the abuse-reporting intake through
// the gateway: a user files a report against a post, and it then appears in the
// reports listing. This is the entry point of the moderation pipeline.
func TestE2E_TrustSafety_FileReport(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	reporter := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	targetPost := uuid.NewString()
	created := reporter.MustOK(t, ctx, "POST", "/v1/reports", map[string]any{
		"entity_type": "post",
		"entity_id":   targetPost,
		"reason":      "spam",
		"details":     "e2e trust-safety report",
	})
	var rep struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(created, &rep)

	// The report is retrievable in the caller's listing (sync DB write).
	Eventually(t, 10*time.Second, "filed report appears in listing", func() bool {
		env, err := reporter.Do(ctx, "GET", "/v1/reports", nil)
		if err != nil || env.Status != 200 {
			return false
		}
		body := string(env.Data)
		// Match by report id when returned, else by the target entity id.
		return (rep.ID != "" && strings.Contains(body, rep.ID)) || strings.Contains(body, targetPost)
	})
}
