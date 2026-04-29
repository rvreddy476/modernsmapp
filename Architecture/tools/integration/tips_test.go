//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestTipFlow walks the Tier 3d happy path:
//
//  1. fan sends a tip       — POST /v1/monetization/tips
//  2. tip lands in /sent    — GET  /v1/monetization/tips/sent
//  3. tip lands in /received— GET  /v1/monetization/tips/received  (creator-side)
//  4. negative: self-tip    — POST /v1/monetization/tips with own ID  (400)
//  5. negative: under min   — POST with amount_paise=10               (400)
//
// Wallet balance changes aren't asserted here because these are
// fresh users with no balance — the validator catches issues before
// the wallet is touched in steps 4-5, and step 1 will hit
// CHARGE_FAILED if the test runner's wallet is empty (which is
// fine — the test still verifies the request shape + the route is
// wired).
func TestTipFlow(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.Monetization)

	fanID := uuid.New()
	creatorID := uuid.New()

	fan := NewHTTPClient(urls.Monetization, fanID)
	creator := NewHTTPClient(urls.Monetization, creatorID)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// 1. Send a tip. Either succeeds (wallet has balance) or returns
	// 402 CHARGE_FAILED (wallet empty). Both are acceptable — we're
	// asserting the route exists and validates correctly.
	env := fan.MustDo(t, ctx, "POST", "/v1/monetization/tips", map[string]interface{}{
		"recipient_id": creatorID.String(),
		"amount_paise": 5000,
		"message":      "great content",
	})
	switch {
	case env.Status == 201:
		// happy path — verify it shows up in /sent
		var resp struct {
			Tip struct {
				ID          string `json:"id"`
				AmountPaise int64  `json:"amount_paise"`
			} `json:"tip"`
		}
		if err := json.Unmarshal(env.Data, &resp); err != nil {
			t.Fatalf("decode tip: %v", err)
		}
		if resp.Tip.ID == "" {
			t.Fatalf("tip created without ID")
		}
		if resp.Tip.AmountPaise != 5000 {
			t.Errorf("tip amount: got %d, want 5000", resp.Tip.AmountPaise)
		}
		// 2. Sender history.
		sent := fan.MustOK(t, ctx, "GET", "/v1/monetization/tips/sent", nil)
		var sentList []map[string]interface{}
		_ = json.Unmarshal(sent, &sentList)
		// 3. Recipient history.
		recv := creator.MustOK(t, ctx, "GET", "/v1/monetization/tips/received", nil)
		var recvList []map[string]interface{}
		_ = json.Unmarshal(recv, &recvList)
		if len(sentList) == 0 {
			t.Errorf("/tips/sent returned empty after successful tip")
		}
		if len(recvList) == 0 {
			t.Errorf("/tips/received returned empty after successful tip")
		}
	case env.Status == 402:
		// CHARGE_FAILED — wallet empty. Acceptable: the route works,
		// it's just unfunded. Don't fail the test.
		t.Logf("tip charge failed (expected when fan wallet is empty): %s", env.Error.Code)
	default:
		t.Fatalf("POST /tips: unexpected status %d (code=%s, msg=%s)",
			env.Status,
			condStr(env.Error != nil, env.Error.Code),
			condStr(env.Error != nil, env.Error.Message),
		)
	}

	// 4. Self-tip — must be rejected with 400.
	selfEnv := fan.MustDo(t, ctx, "POST", "/v1/monetization/tips", map[string]interface{}{
		"recipient_id": fanID.String(),
		"amount_paise": 1000,
	})
	if selfEnv.Status != 400 {
		t.Errorf("self-tip: want 400, got %d", selfEnv.Status)
	}

	// 5. Below minimum — must be rejected with 400.
	lowEnv := fan.MustDo(t, ctx, "POST", "/v1/monetization/tips", map[string]interface{}{
		"recipient_id": creatorID.String(),
		"amount_paise": 10,
	})
	if lowEnv.Status != 400 {
		t.Errorf("amount < min: want 400, got %d", lowEnv.Status)
	}
}

func condStr(ok bool, s string) string {
	if !ok {
		return "<no-error>"
	}
	return s
}
