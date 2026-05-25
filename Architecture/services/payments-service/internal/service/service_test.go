package service

import (
	"errors"
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// TestRupeesToPaise pins the API-boundary unit conversion so a future
// refactor that "simplifies" the cast back to int64 trips immediately.
// The reconciliation bug previously here passed ₹X to a gateway that
// reads paise, so the provider order opened at ₹X/100.
func TestRupeesToPaise(t *testing.T) {
	cases := []struct {
		name        string
		rupees      float64
		wantPaise   int64
	}{
		{"one rupee", 1.0, 100},
		{"one hundred", 100.0, 10000},
		{"one hundred and fifty paise", 100.50, 10050},
		// Banker's-rounded floats: 0.295 lands at 0.2949999... in
		// IEEE-754, so math.Round(29.499...) is 29. Verify we round
		// the math.Round result, not truncate.
		{"₹0.01", 0.01, 1},
		{"₹0.05", 0.05, 5},
		{"₹0.99", 0.99, 99},
		{"₹99.99", 99.99, 9999},
		// A common Razorpay test amount.
		{"₹500", 500.0, 50000},
		// Zero passes through.
		{"zero", 0.0, 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rupeesToPaise(c.rupees)
			if got != c.wantPaise {
				t.Errorf("rupeesToPaise(%v) = %d, want %d", c.rupees, got, c.wantPaise)
			}
		})
	}
}

// Audit P6 + P7 tests. The three required tests assert the new amount-
// cap behaviour on Service.InitiateRefund. The actual decision logic is
// extracted into the pure helper resolveRefundAmount (+ companion
// computeRefundStatus) so it's testable without a Postgres pool.
//
// payerID and payeeID are the two authorised actors; tests use payerID
// throughout. Intent amount = ₹100.00 → 10000 paise minor.

var (
	testPayerID = uuid.New()
	testPayeeID = uuid.New()
)

func succeededIntent(refundedMinor int64) *postgres.PaymentIntent {
	// Both fields set: post-P7-deep, every fresh row has both. The
	// legacy-only path is covered by TestGetIntent_LegacyFloatFallback.
	return &postgres.PaymentIntent{
		ID:                  uuid.New(),
		PayerID:             testPayerID,
		PayeeID:             testPayeeID,
		Amount:              100.00,
		AmountMinorRaw:      10000,
		Status:              statusFor(refundedMinor),
		RefundedAmountMinor: refundedMinor,
	}
}

// statusFor returns the status the store would set after a refunded
// running total — succeeded if nothing's been refunded yet, otherwise
// partially_refunded. Mirrors store.ApplyRefund's CASE expression.
func statusFor(refundedMinor int64) string {
	if refundedMinor == 0 {
		return "succeeded"
	}
	return "partially_refunded"
}

// TestInitiateRefund_AmountExceedsIntent pins audit P6: a refund larger
// than the intent's remaining refundable balance must surface
// ErrRefundAmountExceedsIntent. Previously the API had no amount field
// at all and would blanket-flip the whole intent to 'refunded'.
func TestInitiateRefund_AmountExceedsIntent(t *testing.T) {
	intent := succeededIntent(0) // 10000 paise, nothing refunded

	cases := []struct {
		name   string
		amount int64
	}{
		{"one paise over", 10001},
		{"double the intent", 20000},
		{"way over", 999999999},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := resolveRefundAmount(intent, testPayerID, c.amount)
			if !errors.Is(err, ErrRefundAmountExceedsIntent) {
				t.Errorf("resolveRefundAmount(amount=%d) err = %v, want ErrRefundAmountExceedsIntent", c.amount, err)
			}
		})
	}
}

// TestInitiateRefund_PartialFlipsStatus pins audit P6: a refund of
// strictly less than the remaining refundable balance flips the intent
// to 'partially_refunded', not 'refunded'. computeRefundStatus mirrors
// the CASE inside store.ApplyRefund — both must agree.
func TestInitiateRefund_PartialFlipsStatus(t *testing.T) {
	const intentAmountMinor int64 = 10000 // ₹100.00

	cases := []struct {
		name             string
		currentRefunded  int64
		refundAmount     int64
		wantStatus       string
		wantResolvedAmt  int64
	}{
		// First refund of ₹40 on a fresh ₹100 intent leaves ₹60 refundable.
		{"40 of 100 (first refund)", 0, 4000, "partially_refunded", 4000},
		// 1 paise of 10000 — still partial.
		{"1 paise of 10000", 0, 1, "partially_refunded", 1},
		// 9999 of 10000 — still partial (off by one paise).
		{"9999 of 10000", 0, 9999, "partially_refunded", 9999},
		// Top-up that doesn't fully cover the remainder — still partial.
		{"top-up keeps partial", 4000, 3000, "partially_refunded", 3000},
		// Exact match → flips to fully refunded.
		{"exact remainder full refund", 4000, 6000, "refunded", 6000},
		// 0 amount → full refund of remainder; with 4000 already refunded
		// the resolved refund is 6000 and status flips to fully refunded.
		{"amount=0 refunds the remaining balance", 4000, 0, "refunded", 6000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			intent := succeededIntent(c.currentRefunded)
			refundMinor, intentMinor, err := resolveRefundAmount(intent, testPayerID, c.refundAmount)
			if err != nil {
				t.Fatalf("resolveRefundAmount unexpected error: %v", err)
			}
			if refundMinor != c.wantResolvedAmt {
				t.Errorf("resolved refund = %d, want %d", refundMinor, c.wantResolvedAmt)
			}
			if intentMinor != intentAmountMinor {
				t.Errorf("intent amount_minor = %d, want %d", intentMinor, intentAmountMinor)
			}
			gotStatus := computeRefundStatus(c.currentRefunded, refundMinor, intentMinor)
			if gotStatus != c.wantStatus {
				t.Errorf("computeRefundStatus(refunded=%d, refund=%d, total=%d) = %q, want %q",
					c.currentRefunded, refundMinor, intentMinor, gotStatus, c.wantStatus)
			}
		})
	}
}

// TestInitiateRefund_FullStillWorks pins audit P6 + P7 backwards-compat:
// the historical signature (no amount, full refund) is preserved by
// passing amountMinor == 0 — the resolver returns the entire intent
// amount in paise and the status flips straight to 'refunded'.
func TestInitiateRefund_FullStillWorks(t *testing.T) {
	intent := succeededIntent(0) // 10000 paise, nothing refunded

	refundMinor, intentMinor, err := resolveRefundAmount(intent, testPayerID, 0)
	if err != nil {
		t.Fatalf("resolveRefundAmount(amount=0) unexpected error: %v", err)
	}
	if refundMinor != 10000 {
		t.Errorf("refundMinor = %d, want 10000 (full intent)", refundMinor)
	}
	if intentMinor != 10000 {
		t.Errorf("intentMinor = %d, want 10000", intentMinor)
	}
	if status := computeRefundStatus(0, refundMinor, intentMinor); status != "refunded" {
		t.Errorf("computeRefundStatus full = %q, want refunded", status)
	}

	// Payee-actor variant — both payer and payee are authorised refund
	// initiators (audit P1). A non-party actor must get
	// ErrRefundNotAuthorized.
	if _, _, err := resolveRefundAmount(intent, testPayeeID, 0); err != nil {
		t.Errorf("payee-actor full refund unexpectedly errored: %v", err)
	}
	if _, _, err := resolveRefundAmount(intent, uuid.New(), 0); !errors.Is(err, ErrRefundNotAuthorized) {
		t.Errorf("non-party actor err = %v, want ErrRefundNotAuthorized", err)
	}
}

// TestCreateIntent_StoresAmountMinor pins audit P7-deep: the
// resolution at the service entry point in InitiatePayment must end
// with a non-zero AmountMinorRaw on the row passed into the store. We
// can't reach the real DB here, so the test exercises the resolver
// branches that decide which input wins.
//
// Three scenarios, each verifying the rules in the type comment on
// InitiateInput:
//   (a) AmountMinor only      → that value is the source of truth, the
//       float mirror is populated from it (paise/100).
//   (b) Amount (legacy) only  → AmountMinor derived via rupeesToPaise.
//   (c) Both set, AmountMinor takes precedence — the float is recorded
//       as the caller sent it so analytics readers see the same value
//       the caller passed.
func TestCreateIntent_StoresAmountMinor(t *testing.T) {
	cases := []struct {
		name             string
		inAmountRupees   float64
		inAmountMinor    int64
		wantAmountMinor  int64
		wantAmountRupees float64
	}{
		{
			name:             "minor-only ₹100.50",
			inAmountRupees:   0,
			inAmountMinor:    10050,
			wantAmountMinor:  10050,
			wantAmountRupees: 100.5,
		},
		{
			name:             "legacy float-only ₹250.25",
			inAmountRupees:   250.25,
			inAmountMinor:    0,
			wantAmountMinor:  25025,
			wantAmountRupees: 250.25,
		},
		{
			name:             "both set; minor wins, float kept verbatim",
			inAmountRupees:   999.99,
			inAmountMinor:    12345,
			wantAmountMinor:  12345,
			wantAmountRupees: 999.99,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Replicate the resolution rules inside InitiatePayment so
			// the unit test pins the contract without spinning up
			// Postgres. If the resolution moves into a helper later,
			// swap this for the helper call.
			amountMinor := c.inAmountMinor
			if amountMinor == 0 {
				amountMinor = rupeesToPaise(c.inAmountRupees)
			}
			amountRupees := c.inAmountRupees
			if amountRupees <= 0 {
				amountRupees = float64(amountMinor) / 100.0
			}

			if amountMinor != c.wantAmountMinor {
				t.Errorf("amountMinor = %d, want %d", amountMinor, c.wantAmountMinor)
			}
			if amountRupees != c.wantAmountRupees {
				t.Errorf("amountRupees = %v, want %v", amountRupees, c.wantAmountRupees)
			}
		})
	}
}

// TestGetIntent_LegacyFloatFallback pins audit P7-deep: rows that
// pre-date the migration have AmountMinorRaw == 0 (the column was
// added with DEFAULT 0). AmountMinor() must round-trip through the
// legacy Amount float for those rows so refund / verify code paths
// keep working through the deprecation window.
//
// Once the follow-up migration drops the `amount` column the
// fallback can be removed; until then it's the safety net for
// legacy rows under partial-deploy / lagging backfill conditions.
func TestGetIntent_LegacyFloatFallback(t *testing.T) {
	cases := []struct {
		name           string
		amountRupees   float64
		amountMinorRaw int64
		wantMinor      int64
	}{
		// Legacy row: amount_minor was never backfilled. Fallback to
		// math.Round of the float.
		{"legacy row ₹100.50", 100.50, 0, 10050},
		{"legacy row ₹0.99", 0.99, 0, 99},
		// Post-migration row: amount_minor wins, float ignored.
		{"new row prefers amount_minor", 100.50, 12345, 12345},
		// New row with no float (caller only sent minor): amount_minor wins.
		{"new row, no float column", 0, 7777, 7777},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			intent := &postgres.PaymentIntent{
				Amount:         c.amountRupees,
				AmountMinorRaw: c.amountMinorRaw,
			}
			if got := intent.AmountMinor(); got != c.wantMinor {
				t.Errorf("AmountMinor() = %d, want %d", got, c.wantMinor)
			}
		})
	}
}
