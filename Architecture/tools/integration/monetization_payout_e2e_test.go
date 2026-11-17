//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Money is closed for the beta. monetization-service decides it, on three
// boot flags (cmd/server/main.go, internal/runmode), all default false:
//
//	MONETIZATION_WRITES_ENABLED   the launch boundary (internal/http
//	                              handler.go, launchBoundary). Off: only the
//	                              beta reads answer (betaReadOnlyRules: the
//	                              creator ledger, transactions, the payout
//	                              list, the creator-fund rules, status and
//	                              estimates, and a creator's support read);
//	                              every other route under /v1/monetization is
//	                              503 MONETIZATION_NOT_LAUNCHED before its
//	                              handler runs.
//	MEMBERSHIPS_ENABLED           paid memberships, independent of the writes
//	                              flag (membership_payments_handler.go). Off:
//	                              the membership routes are 503
//	                              MEMBERSHIPS_NOT_ENABLED whatever the writes
//	                              flag says.
//	MONETIZATION_PAYOUTS_ENABLED  withdrawals; needs writes. Off with writes
//	                              on: 503 PAYOUTS_NOT_ENABLED.
//
// The gateway has no money switch: it proxies /v1/monetization, and the
// boundary answers before the handler looks for a user, so a signed-in
// caller and an anonymous one get the same refusal.
//
// The two variables below tell these flows which way the stack under test
// was started. The flows never guess: each asserts the answer of the mode it
// was told, the closed flows run on a default stack, and the open flows skip
// there.
const (
	// "1" when monetization-service runs with MONETIZATION_WRITES_ENABLED=true
	// (and MONETIZATION_PAYOUTS_ENABLED unset).
	monetizationWritesEnv = "ATPOST_E2E_MONETIZATION_WRITES"
	// "1" when monetization-service runs with MEMBERSHIPS_ENABLED=true.
	membershipsOpenEnv = "ATPOST_E2E_MEMBERSHIPS"
)

const (
	moneyClosedCode       = "MONETIZATION_NOT_LAUNCHED"
	membershipsClosedCode = "MEMBERSHIPS_NOT_ENABLED"
)

func monetizationWrites() bool { return os.Getenv(monetizationWritesEnv) == "1" }
func membershipsOpen() bool    { return os.Getenv(membershipsOpenEnv) == "1" }

// monetizationForFlow applies the usual gates (ATPOST_RUN_INTEGRATION, a
// monetization-service and a gateway that answer) and returns both base URLs
// with a context that ends with the test.
func monetizationForFlow(t *testing.T) (ServiceURLs, context.Context) {
	t.Helper()
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.Monetization, urls.APIGateway)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return urls, ctx
}

// wantMoneyRefusal sends one request three ways and requires the same exact
// refusal from each: as userID straight to monetization-service, as userID
// through the gateway, and anonymously through the gateway.
func wantMoneyRefusal(t *testing.T, ctx context.Context, urls ServiceURLs, userID uuid.UUID, method, path string, body any, status int, code string) {
	t.Helper()
	for _, tc := range []struct {
		who    string
		client *HTTPClient
	}{
		{"signed-in user at the service", NewHTTPClient(urls.Monetization, userID)},
		{"signed-in user through the gateway", NewHTTPClient(urls.APIGateway, userID)},
		{"anonymous caller through the gateway", NewHTTPClient(urls.APIGateway, uuid.Nil)},
	} {
		env := tc.client.MustDo(t, ctx, method, path, body)
		wantRefusal(t, env, tc.who+" "+method+" "+path, status, code)
	}
}

// TestE2E_Monetization_PayoutGated is the withdrawal money-safety negative. A
// well-formed payout request (so the refusal is the gate, not a 400 for the
// body) is refused with the exact answer of the stack's mode:
//
//   - beta (writes off): 503 MONETIZATION_NOT_LAUNCHED from the launch
//     boundary, signed in or not, at the service and through the gateway;
//   - writes on, payouts off: 503 PAYOUTS_NOT_ENABLED, the first gate of the
//     payout pipeline (internal/service payout.go).
//
// The payout LIST is a beta read and answers the caller either way.
func TestE2E_Monetization_PayoutGated(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	userID := uuid.New()
	user := NewHTTPClient(urls.Monetization, userID)
	payout := map[string]any{
		"amount_paise":     100000, // ₹1000
		"payout_method_id": uuid.NewString(),
	}

	if monetizationWrites() {
		for who, c := range map[string]*HTTPClient{
			"at the service":      user,
			"through the gateway": NewHTTPClient(urls.APIGateway, userID),
		} {
			env := c.MustDo(t, ctx, "POST", "/v1/monetization/payouts", payout)
			wantRefusal(t, env, "payout with payouts off, "+who, http.StatusServiceUnavailable, "PAYOUTS_NOT_ENABLED")
		}
	} else {
		wantMoneyRefusal(t, ctx, urls, userID, "POST", "/v1/monetization/payouts", payout,
			http.StatusServiceUnavailable, moneyClosedCode)
		// Adding a payout method is a write too.
		wantMoneyRefusal(t, ctx, urls, userID, "POST", "/v1/monetization/payout-methods",
			map[string]any{"type": "upi", "upi_id": "e2e@upi"},
			http.StatusServiceUnavailable, moneyClosedCode)
	}

	// Nothing was created: the caller's payout list is readable and empty.
	var payouts []json.RawMessage
	env := user.MustDo(t, ctx, "GET", "/v1/monetization/payouts", nil)
	if wantData(t, env, "GET /v1/monetization/payouts", http.StatusOK, &payouts) && len(payouts) != 0 {
		t.Errorf("a refused payout left %d payout row(s) behind", len(payouts))
	}
}

// TestE2E_Monetization_ServiceNeedsInternalKey guards the port itself: a
// caller that reaches monetization-service without the internal service key
// is 401 UNAUTHORIZED before the launch boundary, on an open beta read and on
// a closed write alike (shared/middleware RequireInternalKey). A stack
// started without a key has no such gate, so the flow skips unless the suite
// was given one (ATPOST_INTERNAL_KEY).
func TestE2E_Monetization_ServiceNeedsInternalKey(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	if internalKey() == "" {
		t.Skip("ATPOST_INTERNAL_KEY is unset: this stack is not known to run with an internal service key")
	}
	noKey := NewHTTPClient(urls.Monetization, uuid.New()).WithHeader("X-Internal-Service-Key", "")

	env := noKey.MustDo(t, ctx, "GET", "/v1/monetization/creator-fund/rates", nil)
	wantRefusal(t, env, "beta read without the internal key", http.StatusUnauthorized, "UNAUTHORIZED")
	env = noKey.MustDo(t, ctx, "POST", "/v1/monetization/payouts", map[string]any{
		"amount_paise": 100000, "payout_method_id": uuid.NewString(),
	})
	wantRefusal(t, env, "payout without the internal key", http.StatusUnauthorized, "UNAUTHORIZED")
}
