//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Wallet_BalanceAndKYC is a money-domain read smoke: a fresh user can
// read their wallet balance and KYC status through the gateway (the wallet is
// auto-provisioned / returns a sane zero+unverified state). Guards routing +
// the read path.
//
// The full top-up → balance → ledger journey needs the payment-gateway mock
// (top-up creates an intent confirmed out-of-band) — tracked as a deeper
// money-flow follow-up, not asserted here.
func TestE2E_Wallet_BalanceAndKYC(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	u := NewHTTPClient(urls.APIGateway, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if env := u.MustDo(t, ctx, "GET", "/v1/wallet/balance", nil); env.Status != 200 {
		t.Errorf("GET /v1/wallet/balance: want 200, got %d", env.Status)
	}
	if env := u.MustDo(t, ctx, "GET", "/v1/wallet/kyc", nil); env.Status != 200 {
		t.Errorf("GET /v1/wallet/kyc: want 200, got %d", env.Status)
	}
}
