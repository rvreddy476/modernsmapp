//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// Tips (monetization-service tips_handler.go, creator_support.go) sit behind
// the launch boundary: see monetization_payout_e2e_test.go for the switches.

// creatorSupport is GET /v1/monetization/creators/:creatorId/support, the one
// tip route that is open in beta: its whole job there is to say tips are off
// so a client hides the button instead of the button meeting a 503.
type creatorSupport struct {
	TipsEnabled     bool   `json:"tips_enabled"`
	MinTipPaise     int64  `json:"min_tip_paise"`
	Currency        string `json:"currency"`
	MembershipTiers int    `json:"membership_tiers"`
}

func tipBody(recipient uuid.UUID, amountPaise int64) map[string]any {
	return map[string]any{
		"recipient_id": recipient.String(),
		"amount_paise": amountPaise,
		"message":      "great content",
	}
}

// TestTipClosedInBeta guards the beta line for tips: nobody sends a tip or
// reads tip history, signed in or not, at the service or through the gateway.
// Every one is 503 MONETIZATION_NOT_LAUNCHED from the launch boundary, which
// runs before the handler (so a tip that validation would refuse with a 400
// gets the same 503), and the public support read says so up front.
func TestTipClosedInBeta(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	if monetizationWrites() {
		t.Skipf("%s=1: this stack runs with money writes on", monetizationWritesEnv)
	}
	fanID, creatorID := uuid.New(), uuid.New()

	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/v1/monetization/tips", tipBody(creatorID, 5000)},
		// A self-tip and a tip under the minimum are 400s once tips open.
		{"POST", "/v1/monetization/tips", tipBody(fanID, 1000)},
		{"POST", "/v1/monetization/tips", tipBody(creatorID, 10)},
		{"GET", "/v1/monetization/tips/sent", nil},
		{"GET", "/v1/monetization/tips/received", nil},
		{"GET", "/v1/monetization/tips/post/" + uuid.NewString(), nil},
	} {
		wantMoneyRefusal(t, ctx, urls, fanID, tc.method, tc.path, tc.body,
			http.StatusServiceUnavailable, moneyClosedCode)
	}

	// The support read answers a signed-in viewer and an anonymous one with
	// tips_enabled=false and the rules a tip will follow when it opens.
	path := "/v1/monetization/creators/" + creatorID.String() + "/support"
	for who, c := range map[string]*HTTPClient{
		"signed-in viewer at the service":      NewHTTPClient(urls.Monetization, fanID),
		"signed-in viewer through the gateway": NewHTTPClient(urls.APIGateway, fanID),
		"anonymous viewer through the gateway": NewHTTPClient(urls.APIGateway, uuid.Nil),
	} {
		var got creatorSupport
		env := c.MustDo(t, ctx, "GET", path, nil)
		if !wantData(t, env, who+" GET "+path, http.StatusOK, &got) {
			continue
		}
		if got.TipsEnabled {
			t.Errorf("%s: tips_enabled=true while POST /tips answers 503 %s", who, moneyClosedCode)
		}
		if got.MinTipPaise != 100 || got.Currency != "INR" {
			t.Errorf("%s: want min_tip_paise=100 currency=INR, got %d %q", who, got.MinTipPaise, got.Currency)
		}
	}
}

// TestTipFlow walks the tip path on a stack that runs with money writes on
// (ATPOST_E2E_MONETIZATION_WRITES=1, see monetization_payout_e2e_test.go):
//
//  1. the support read says tips are on for another viewer, off on the
//     creator's own page
//  2. self-tip                 — 400 INVALID_REQUEST
//  3. under the ₹1 minimum     — 400 INVALID_REQUEST
//  4. a fan with an empty wallet tips — 402 CHARGE_FAILED, and no tip row is
//     written on either side (the charge is atomic and comes first)
//
// A completed tip needs a funded wallet, and the only way to fund one is a
// real payment through payments-service; that leg is not covered here. Step
// 4 also needs graph-service: the tip asks it about blocks first and answers
// 503 TIP_UNAVAILABLE when it cannot.
func TestTipFlow(t *testing.T) {
	urls, ctx := monetizationForFlow(t)
	if !monetizationWrites() {
		t.Skipf("%s is not 1: tips are closed on this stack (TestTipClosedInBeta covers that)", monetizationWritesEnv)
	}
	SkipIfDown(t, urls.Graph)
	fanID, creatorID := uuid.New(), uuid.New()
	fan := NewHTTPClient(urls.Monetization, fanID)
	creator := NewHTTPClient(urls.Monetization, creatorID)

	// 1. Support read.
	path := "/v1/monetization/creators/" + creatorID.String() + "/support"
	var asFan, asCreator creatorSupport
	if wantData(t, fan.MustDo(t, ctx, "GET", path, nil), "fan GET "+path, http.StatusOK, &asFan) && !asFan.TipsEnabled {
		t.Errorf("tips_enabled=false for another viewer with money writes on")
	}
	if wantData(t, creator.MustDo(t, ctx, "GET", path, nil), "creator GET "+path, http.StatusOK, &asCreator) && asCreator.TipsEnabled {
		t.Errorf("tips_enabled=true on the creator's own page")
	}

	// 2 and 3. Validation refusals, before any wallet is touched.
	env := fan.MustDo(t, ctx, "POST", "/v1/monetization/tips", tipBody(fanID, 1000))
	wantRefusal(t, env, "self-tip", http.StatusBadRequest, "INVALID_REQUEST")
	env = fan.MustDo(t, ctx, "POST", "/v1/monetization/tips", tipBody(creatorID, 10))
	wantRefusal(t, env, "tip under the minimum", http.StatusBadRequest, "INVALID_REQUEST")

	// 4. An empty wallet cannot tip, and nothing is recorded.
	env = fan.MustDo(t, ctx, "POST", "/v1/monetization/tips", tipBody(creatorID, 5000))
	wantRefusal(t, env, "tip from an empty wallet", http.StatusPaymentRequired, "CHARGE_FAILED")
	for who, tc := range map[string]struct {
		client *HTTPClient
		path   string
	}{
		"fan":     {fan, "/v1/monetization/tips/sent"},
		"creator": {creator, "/v1/monetization/tips/received"},
	} {
		var tips []json.RawMessage
		env := tc.client.MustDo(t, ctx, "GET", tc.path, nil)
		if wantData(t, env, who+" GET "+tc.path, http.StatusOK, &tips) && len(tips) != 0 {
			t.Errorf("%s: a failed charge left %d tip row(s) in %s", who, len(tips), tc.path)
		}
	}
}
