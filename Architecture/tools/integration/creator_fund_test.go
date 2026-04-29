//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestCreatorFundEndpointsWired is a wiring-only smoke for Tier 3a:
// every public endpoint responds with the expected shape, regardless
// of whether the caller is actually eligible.
//
// We don't assert numeric correctness of earnings here — that needs
// synthetic analytics.content_daily_summary rows + an active rpm rate
// + a worker run, all of which are out of scope for a fast suite.
// What this does verify is that:
//
//   - status endpoint returns the decision envelope shape
//   - rates endpoint returns a list (default seeded rates exist)
//   - apply endpoint accepts the POST without 404'ing
func TestCreatorFundEndpointsWired(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.Monetization)

	creatorID := uuid.New()
	creator := NewHTTPClient(urls.Monetization, creatorID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 1. Status — must return decision envelope. New creator → 'ineligible'
	// because they have no qualifying analytics rows.
	status := creator.MustOK(t, ctx, "GET", "/v1/monetization/creator-fund/status", nil)
	var statusEnv struct {
		Decision struct {
			Status string `json:"status"`
		} `json:"decision"`
		PlatformFeeBps int `json:"platform_fee_bps"`
	}
	if err := json.Unmarshal(status, &statusEnv); err != nil {
		t.Fatalf("decode status: %v (raw: %s)", err, string(status))
	}
	if statusEnv.Decision.Status == "" {
		t.Errorf("decision.status missing — endpoint shape changed?")
	}
	if statusEnv.PlatformFeeBps == 0 {
		t.Errorf("platform_fee_bps zero — config not loaded?")
	}

	// 2. Rates — must return seeded long_video + flick rows.
	rates := creator.MustOK(t, ctx, "GET", "/v1/monetization/creator-fund/rates", nil)
	var ratesList []map[string]interface{}
	if err := json.Unmarshal(rates, &ratesList); err != nil {
		t.Fatalf("decode rates: %v (raw: %s)", err, string(rates))
	}
	if len(ratesList) < 2 {
		t.Errorf("rates list: want >= 2 (long_video + flick seeded), got %d", len(ratesList))
	}

	// 3. Apply — accepts the POST. Idempotent, just re-evaluates.
	creator.MustOK(t, ctx, "POST", "/v1/monetization/creator-fund/apply", nil)
}
