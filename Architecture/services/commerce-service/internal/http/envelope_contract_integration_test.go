//go:build integration

package http

// Every commerce response is wrapped in the shared API envelope.
//
// ─── WHY THIS EXISTS ────────────────────────────────────────────────────
//
// The Android client types every call as `Response<ApiEnvelope<T>>` and reads
// `body.data`. A handler that answers with `c.JSON(http.StatusOK, payload)`
// emits the payload RAW — no `data` key — so `body.data` is null and the
// repository turns a perfectly good 200 into a failure.
//
// Six seller routes shipped that way and none of the Android tests caught it,
// because every one of them fakes `CommerceApi` and hands back an envelope the
// server never sent. That is the same shape as the cart defect and the ₹0
// price defect before it: **a test fixture supplying exactly the thing
// production drops.**
//
// So this asserts the WIRE, on the real route table, for every seller-facing
// read. It is deliberately not a unit test of a helper — the defect is not in
// `api.JSON`, it is in which of two functions a handler happened to call.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/http/... -v

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// assertEnveloped fails unless the body is `{"data": ...}` with a non-null
// payload — exactly what the client's ApiEnvelope requires.
func assertEnveloped(t *testing.T, path string, body []byte) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Errorf("%s: body is not a JSON object: %v\n%s", path, err, body)
		return
	}
	raw, ok := envelope["data"]
	if !ok {
		t.Errorf("%s: no `data` key — the client reads body.data, so this 200 arrives as a "+
			"failure with nothing rendered\n%s", path, body)
		return
	}
	if string(raw) == "null" {
		t.Errorf("%s: `data` is null\n%s", path, body)
	}
}

// Every seller-facing read, through the real registered routes.
func TestEverySellerReadIsEnveloped(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 5)

	cases := []struct {
		name string
		path string
	}{
		{"the seller's own catalogue", "/v1/commerce/seller/products"},
		{"a variant's stock", "/v1/commerce/seller/variants/" + s.variantID.String() + "/stock"},
		{"a variant", "/v1/commerce/seller/variants/" + s.variantID.String()},
		{"the GST rate table", "/v1/commerce/tax-classes"},
		{"the onboarding checklist", "/v1/commerce/onboarding/readiness"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := call(t, r, http.MethodGet, tc.path, s.sellerUserID, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			assertEnveloped(t, tc.path, w.Body.Bytes())
		})
	}
}

// The writes too. A stock adjustment returns the resulting level, and the app
// renders it.
func TestASellerWriteIsEnveloped(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 5)

	path := "/v1/commerce/seller/variants/" + s.variantID.String() + "/stock"
	w := call(t, r, http.MethodPatch, path, s.sellerUserID,
		map[string]any{"delta": 3, "reason": "purchase"})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	assertEnveloped(t, path, w.Body.Bytes())
}

// The buyer half, which was already correct — asserted so a future edit
// cannot quietly switch one of these to the raw shape.
func TestTheBuyerReadsAreEnveloped(t *testing.T) {
	r := journeyEngine(t, 4000)
	f := seedJourney(t, 5, 129900)

	for _, path := range []string{
		"/v1/commerce/cart",
		"/v1/commerce/addresses",
		"/v1/commerce/orders",
	} {
		w := call(t, r, http.MethodGet, path, f.userID, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, w.Code, w.Body.String())
		}
		assertEnveloped(t, path, w.Body.Bytes())
	}
}

// An error is enveloped too, under `error` rather than `data` — that is how
// the repository maps a stable code onto a typed CommerceError.
func TestARefusalCarriesACode(t *testing.T) {
	r := journeyEngine(t, 4000)
	s := seedSellerSurface(t, 5)

	w := call(t, r, http.MethodPatch,
		"/v1/commerce/seller/variants/"+s.otherVariant.String()+"/stock",
		s.sellerUserID, map[string]any{"delta": 1, "reason": "purchase"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403\n%s", w.Code, w.Body.String())
	}
	var body struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if body.Error == nil || body.Error.Code == "" {
		t.Fatalf("a refusal with no error code; the client can only show a generic "+
			"banner\n%s", w.Body.String())
	}
	_ = uuid.Nil
}
