//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// The creator fund (monetization-service creator_fund_handler.go). In beta
// the RULES of payment and the creator's own ESTIMATES are open reads; the
// one creator-facing write, apply, is behind the launch boundary. The
// switches are described in monetization_payout_e2e_test.go.

// TestCreatorFundEndpointsWired guards the creator-fund reads that are open
// in beta and stay open once money launches: each answers with its shape for
// a creator who has no qualifying analytics rows.
//
// It does not assert earnings arithmetic: that needs synthetic
// analytics.content_daily_summary rows, an active rate and a worker run (see
// fund_settlement_test.go).
//
//   - status: the decision envelope and the platform fee
//   - rates: the seeded long_video and flick rows
//   - earnings: while payouts are off the figure is labelled an estimate and
//     none of it is withdrawable (estimateFlags)
func TestCreatorFundEndpointsWired(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	creator := NewHTTPClient(urls.Monetization, uuid.New())

	// 1. Status: the decision envelope. A new creator is not eligible.
	var status struct {
		Decision struct {
			Status string `json:"status"`
		} `json:"decision"`
		PlatformFeeBps int `json:"platform_fee_bps"`
	}
	env := creator.MustDo(t, ctx, "GET", "/v1/monetization/creator-fund/status", nil)
	if wantData(t, env, "GET creator-fund/status", http.StatusOK, &status) {
		if status.Decision.Status == "" {
			t.Errorf("decision.status missing: the endpoint shape changed")
		}
		if status.Decision.Status == "eligible" {
			t.Errorf("a creator with no views is eligible for the fund")
		}
		if status.PlatformFeeBps == 0 {
			t.Errorf("platform_fee_bps is zero: the fund config is not loaded")
		}
	}

	// 2. Rates: the seeded long_video and flick rows.
	var rates []json.RawMessage
	env = creator.MustDo(t, ctx, "GET", "/v1/monetization/creator-fund/rates", nil)
	if wantData(t, env, "GET creator-fund/rates", http.StatusOK, &rates) && len(rates) < 2 {
		t.Errorf("rates list: want >= 2 (long_video and flick are seeded), got %d", len(rates))
	}

	// 3. Earnings: labelled an estimate while payouts are off. No flow in
	// this suite runs a stack with MONETIZATION_PAYOUTS_ENABLED.
	var earnings struct {
		Estimate     *bool `json:"estimate"`
		Withdrawable *bool `json:"withdrawable"`
	}
	env = creator.MustDo(t, ctx, "GET", "/v1/monetization/creator-fund/earnings?days=30", nil)
	if wantData(t, env, "GET creator-fund/earnings", http.StatusOK, &earnings) {
		if earnings.Estimate == nil || !*earnings.Estimate {
			t.Errorf("earnings: want estimate=true while payouts are off")
		}
		if earnings.Withdrawable == nil || *earnings.Withdrawable {
			t.Errorf("earnings: want withdrawable=false while payouts are off")
		}
	}

	// The per-caller reads need a caller: with the internal key but no user
	// the service answers 401, it does not fall back to anyone's row.
	nobody := NewHTTPClient(urls.Monetization, uuid.Nil)
	env = nobody.MustDo(t, ctx, "GET", "/v1/monetization/creator-fund/status", nil)
	wantRefusal(t, env, "creator-fund/status with no user", http.StatusUnauthorized, "UNAUTHORIZED")
}

// TestCreatorFundApplyClosedInBeta guards the beta line: applying to the fund
// is a money action and answers 503 MONETIZATION_NOT_LAUNCHED, signed in or
// not, at the service and through the gateway, and it leaves the creator's
// status as it was.
func TestCreatorFundApplyClosedInBeta(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	if monetizationWrites() {
		t.Skipf("%s=1: this stack runs with money writes on", monetizationWritesEnv)
	}
	creatorID := uuid.New()

	wantMoneyRefusal(t, ctx, urls, creatorID, "POST", "/v1/monetization/creator-fund/apply", nil,
		http.StatusServiceUnavailable, moneyClosedCode)
	// A write on an open read's path is still a write.
	wantMoneyRefusal(t, ctx, urls, creatorID, "POST", "/v1/monetization/creator-fund/earnings", nil,
		http.StatusServiceUnavailable, moneyClosedCode)
}

// TestCreatorFundApplyOpen is the on-path for a stack that runs with money
// writes on (ATPOST_E2E_MONETIZATION_WRITES=1): apply re-evaluates the caller
// and answers 200 with their eligibility row. A creator with no views is not
// made eligible by asking.
func TestCreatorFundApplyOpen(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	if !monetizationWrites() {
		t.Skipf("%s is not 1: apply is closed on this stack (TestCreatorFundApplyClosedInBeta covers that)", monetizationWritesEnv)
	}
	creatorID := uuid.New()
	creator := NewHTTPClient(urls.Monetization, creatorID)

	var row struct {
		CreatorID uuid.UUID `json:"creator_id"`
		Status    string    `json:"status"`
	}
	env := creator.MustDo(t, ctx, "POST", "/v1/monetization/creator-fund/apply", nil)
	if !wantData(t, env, "POST creator-fund/apply", http.StatusOK, &row) {
		return
	}
	if row.CreatorID != creatorID {
		t.Errorf("apply evaluated %s, the caller is %s", row.CreatorID, creatorID)
	}
	if row.Status == "" || row.Status == "eligible" {
		t.Errorf("apply for a creator with no views: want a non-eligible status, got %q", row.Status)
	}
}
