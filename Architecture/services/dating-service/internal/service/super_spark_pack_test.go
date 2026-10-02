package service

import (
	"context"
	"strings"
	"testing"

	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Mechanic M3 — Super Spark packs. Like every premium product, a pack is
// granted only by the payments-service event and never by the purchase call.

func (r *premiumRig) superSparkBalance(t *testing.T, user uuid.UUID) int {
	t.Helper()
	ent, err := r.st.GetPremiumEntitlement(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return ent.SuperSparkBalance
}

func TestSuperSparkPack_GrantedOnlyByThePaymentEvent(t *testing.T) {
	r := newPremiumRig(t)
	r.svc.SetMechanicsConfig(MechanicsConfig{SuperSpark: true})
	user := uuid.New()

	p := r.buy(t, user, payments.ProductSuperSpark5)
	if p.AmountMinor != 9900 {
		t.Fatalf("pack price = %d, want 9900 from the catalogue", p.AmountMinor)
	}
	if got := r.superSparkBalance(t, user); got != 0 {
		t.Fatalf("balance = %d before any payment event, want 0", got)
	}

	// A capture for the wrong amount grants nothing.
	r.deliver(t, events.EventPaymentSucceeded, "evt-ss-short-"+p.ID.String(), p, func(m map[string]any) { m["amount_minor"] = 100 })
	if got := r.superSparkBalance(t, user); got != 0 {
		t.Fatalf("balance = %d after an underpaid capture, want 0", got)
	}

	paid := "evt-ss-paid-" + p.ID.String()
	r.deliver(t, events.EventPaymentSucceeded, paid, p, nil)
	if got := r.superSparkBalance(t, user); got != 5 {
		t.Fatalf("balance = %d after the capture, want 5", got)
	}
	// The same event again, and a second capture event, add nothing.
	r.deliver(t, events.EventPaymentSucceeded, paid, p, nil)
	r.deliver(t, events.EventPaymentSucceeded, "evt-ss-again-"+p.ID.String(), p, nil)
	if got := r.superSparkBalance(t, user); got != 5 {
		t.Fatalf("balance = %d after replayed captures, want 5", got)
	}
	me, err := r.svc.MyPremium(context.Background(), user)
	if err != nil || me.SuperSparkBalance != 5 || me.IsPremium {
		t.Fatalf("premium/me = %+v err=%v, want a balance of 5 and no pass", me, err)
	}
}

func TestSuperSparkPack_FullRefundTakesBackOnlyTheUnspent(t *testing.T) {
	r := newPremiumRig(t)
	r.svc.SetMechanicsConfig(MechanicsConfig{SuperSpark: true})
	user := uuid.New()

	p := r.buy(t, user, payments.ProductSuperSpark5)
	r.deliver(t, events.EventPaymentSucceeded, "evt-ss-paid-"+p.ID.String(), p, nil)
	other := r.buy(t, user, payments.ProductSuperSpark5)
	r.deliver(t, events.EventPaymentSucceeded, "evt-ss-paid-"+other.ID.String(), other, nil)
	// Eight of the ten are spent.
	d8Exec(t, r.st, `UPDATE dating_super_spark_balances SET balance = 2 WHERE user_id = $1`, user)

	refund := "evt-ss-refund-" + p.ID.String()
	r.deliver(t, events.EventPaymentRefunded, refund, r.purchase(t, p), nil)
	if got := r.superSparkBalance(t, user); got != 0 {
		t.Fatalf("balance = %d after the refund, want 0 (two unspent taken back, never negative)", got)
	}
	if got := r.purchase(t, p); got.Status != payments.StatusRefunded {
		t.Fatalf("purchase status = %s, want refunded", got.Status)
	}
	if outcome := r.inboxOutcome(t, refund); !strings.HasPrefix(outcome, string(payments.OutcomeRefunded)) || !strings.Contains(outcome, "already used") {
		t.Fatalf("inbox outcome = %q, want refunded with the already-used note", outcome)
	}

	// A refund never takes more than its own purchase gave: refunding the
	// second pack with a fresh balance of 9 takes back five.
	d8Exec(t, r.st, `UPDATE dating_super_spark_balances SET balance = 9 WHERE user_id = $1`, user)
	r.deliver(t, events.EventPaymentRefunded, "evt-ss-refund-"+other.ID.String(), r.purchase(t, other), nil)
	if got := r.superSparkBalance(t, user); got != 4 {
		t.Fatalf("balance = %d after refunding a pack of five from nine, want 4", got)
	}
}

func TestSuperSparkPack_NotSoldWhileTheFlagIsOff(t *testing.T) {
	r := newPremiumRig(t)
	for _, p := range r.svc.PremiumCatalogue() {
		if p.Kind == payments.KindSuperSpark {
			t.Fatalf("catalogue lists %s with the flag off", p.ID)
		}
	}
	_, _, err := r.svc.CreatePremiumPurchase(context.Background(), uuid.New(), PremiumPurchaseInput{Product: payments.ProductSuperSpark5, IdempotencyKey: "k-" + uuid.NewString()})
	if err != ErrPremiumProductUnknown {
		t.Fatalf("buying a pack with the flag off: err=%v, want ErrPremiumProductUnknown", err)
	}
}
