//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Monetization_PayoutGated is a money-safety negative: a fresh user with
// no verified payout method, no KYC, and no balance must NOT be able to withdraw
// funds. We send a well-formed payout request (so the rejection is the gate
// firing, not a malformed-body 400) and assert it is refused.
//
// The happy-path money flows (tips, membership gating, creator-fund settlement,
// subscribe) are covered by tips_test.go / membership_test.go / fund_settlement_test.go.
func TestE2E_Monetization_PayoutGated(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.Monetization)

	user := NewHTTPClient(urls.Monetization, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	env := user.MustDo(t, ctx, "POST", "/v1/monetization/payouts", map[string]any{
		"amount_paise":     100000, // ₹1000
		"payout_method_id": uuid.NewString(),
	})
	if env.Status >= 200 && env.Status < 300 {
		t.Errorf("payout for an unverified user with no balance/method should be refused; got %d", env.Status)
	}
}
