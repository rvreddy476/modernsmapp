package membership

import (
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

var d0 = time.Date(2026, 10, 23, 10, 0, 0, 0, time.UTC)

func paidJoin() (Payment, Sub) {
	start, end := d0.Add(-10*24*time.Hour), d0.Add(-10*24*time.Hour).AddDate(0, 1, 0)
	tier := uuid.New()
	p := Payment{ID: uuid.New(), SubscriptionID: uuid.New(), PayerID: uuid.New(), CreatorID: uuid.New(), Kind: KindJoin,
		TierID: tier, TierName: "Fan", TierPricePaise: 4_900, AmountPaise: 4_900, CreatorSharePaise: 4_900, Currency: "INR",
		PeriodStart: start, PeriodEnd: end, GrantedStart: &start, GrantedEnd: &end, IntentID: uuid.NewString(), Status: PayPaid}
	s := Sub{ID: p.SubscriptionID, SubscriberID: p.PayerID, CreatorID: p.CreatorID, TierID: tier, TierName: "Fan", PricePaise: 4_900,
		Status: StatusActive, CurrentPeriodStart: start, CurrentPeriodEnd: end, Counted: true, ActivatedAt: &start}
	return p, s
}

func TestPartialRefundKeepsThePeriodAndReversesItsShare(t *testing.T) {
	p, s := paidJoin()
	d := Decide(p, s, false, Event{EventID: "r", EventType: events.EventPaymentRefunded, IntentID: p.IntentID, AmountMinor: 1_000, Status: "partially_refunded"}, d0)
	if d.Outcome != OutcomePartiallyRefunded || d.Sub != nil || d.ReverseCreatorPaise != 1_000 || d.ReverseEarningRow || d.Payment.RefundedPaise != 1_000 {
		t.Fatalf("%+v", d)
	}
	// The rest, as a second partial that completes it: full.
	p.RefundedPaise = 1_000
	d = Decide(p, s, false, Event{EventID: "r2", EventType: events.EventPaymentRefunded, IntentID: p.IntentID, AmountMinor: 3_900, Status: "partially_refunded"}, d0)
	if d.Outcome != OutcomeRefunded || d.Sub == nil || d.Sub.Status != StatusExpired || !d.ReverseEarningRow {
		t.Fatalf("%+v", d)
	}
	// More than is left is a mismatch.
	d = Decide(p, s, false, Event{EventID: "r3", EventType: events.EventPaymentRefunded, IntentID: p.IntentID, AmountMinor: 4_900, Status: "refunded"}, d0)
	if d.Outcome != OutcomeMismatch {
		t.Fatalf("over-refund: %+v", d)
	}
}

func TestRefundOfAnUpgradeRevertsTheTier(t *testing.T) {
	_, s := paidJoin()
	from := s.TierID
	gold := uuid.New()
	s.TierID, s.TierName, s.PricePaise = gold, "Gold", 19_900
	p := Payment{ID: uuid.New(), SubscriptionID: s.ID, PayerID: s.SubscriberID, Kind: KindUpgrade, TierID: gold, TierName: "Gold",
		TierPricePaise: 19_900, FromTierID: &from, FromTierName: "Fan", FromPricePaise: 4_900, AmountPaise: 7_000, CreatorSharePaise: 7_000,
		IntentID: uuid.NewString(), Status: PayPaid}
	d := Decide(p, s, false, Event{EventID: "r", EventType: events.EventPaymentRefunded, IntentID: p.IntentID, AmountMinor: 7_000, Status: "refunded"}, d0)
	if d.Sub == nil || d.Sub.TierID != from || d.Sub.PricePaise != 4_900 || d.Entitlement != EntitlementDowngraded ||
		len(d.Tiers) != 2 || d.Tiers[0] != (TierDelta{gold, -1}) || d.Tiers[1] != (TierDelta{from, +1}) {
		t.Fatalf("%+v", d)
	}
}

func TestRefundFailedIsRecordedOnlyWhileARefundIsInFlight(t *testing.T) {
	p, s := paidJoin()
	ev := Event{EventID: "f", EventType: events.EventPaymentRefundFailed, IntentID: p.IntentID, ReasonCode: "provider_error"}
	if d := Decide(p, s, false, ev, d0); d.Outcome != OutcomeRefundIgnored {
		t.Fatalf("no refund in flight: %+v", d)
	}
	p.RefundStatus = RefundPending
	d := Decide(p, s, false, ev, d0)
	if d.Outcome != OutcomeRefundFailed || d.Payment.RefundStatus != RefundFailed || d.Payment.RefundError != "provider_error" {
		t.Fatalf("%+v", d)
	}
}

func TestFailedEventForAnotherIntentChangesNothing(t *testing.T) {
	p, s := paidJoin()
	p.Status = PayCreated
	d := Decide(p, s, false, Event{EventID: "x", EventType: events.EventPaymentFailed, IntentID: uuid.NewString()}, d0)
	if d.Outcome != OutcomeMismatch || d.Payment != nil {
		t.Fatalf("%+v", d)
	}
	if d := Decide(p, s, false, Event{EventID: "x", EventType: events.EventPaymentFailed, IntentID: p.IntentID}, d0); d.Outcome != OutcomeMarkedFailed {
		t.Fatalf("%+v", d)
	}
	p.Status = PayPaid
	if d := Decide(p, s, false, Event{EventID: "x", EventType: events.EventPaymentFailed, IntentID: p.IntentID}, d0); d.Outcome != OutcomeFailedIgnored {
		t.Fatalf("failed after paid must not unpay: %+v", d)
	}
}

func TestJoinCaptureWhileAnotherLiveSubscriptionExistsIsLate(t *testing.T) {
	p, s := paidJoin()
	p.Status, s.Status, s.Counted, s.ActivatedAt = PayCreated, StatusPendingPayment, false, nil
	ev := Event{EventID: "s", EventType: events.EventPaymentSucceeded, IntentID: p.IntentID, PayerID: p.PayerID, AmountMinor: 4_900, Currency: "inr"}
	d := Decide(p, s, true, ev, d0)
	if d.Outcome != OutcomeLateCapture || d.Sub != nil || d.CreditCreatorPaise != 0 || d.Refund == nil || d.Refund.Key != LateRefundKey(p.ID) {
		t.Fatalf("%+v", d)
	}
	// Currency is compared case-insensitively.
	if d := Decide(p, s, false, ev, d0); d.Outcome != OutcomeActivated {
		t.Fatalf("%+v", d)
	}
}

func TestPeriodsAndKeys(t *testing.T) {
	jan31 := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	if !RenewalStart(jan31, jan31.Add(-time.Hour)).Equal(jan31) || !RenewalStart(jan31, jan31.Add(time.Hour)).Equal(jan31.Add(time.Hour)) {
		t.Fatal("renewal start")
	}
	if CanRenew(StatusActive, false, jan31, jan31) || !CanRenew(StatusExpired, true, jan31, jan31.Add(29*24*time.Hour)) ||
		CanRenew(StatusExpired, true, jan31, jan31.Add(30*24*time.Hour)) || CanRenew(StatusCancelled, true, jan31, jan31) || CanRenew(StatusPaused, true, jan31, jan31) {
		t.Fatal("CanRenew")
	}
	id := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	if IntentKey(id) != "creator_membership:"+id.String() || LateRefundKey(id) != "membership-late:"+id.String() || SupportRefundKey(id) != "membership-refund:"+id.String() {
		t.Fatal("keys")
	}
	if fee, share := Split(4_900); fee != 0 || share != 4_900 {
		t.Fatal("fee must be zero for now")
	}
	if IsLive(StatusPendingPayment) || IsLive(StatusExpired) || !IsLive(StatusGrace) {
		t.Fatal("IsLive")
	}
}

// A refund event for a payment that never took money (still open, or failed)
// changes nothing: no reversal of earnings that were never recorded.
func TestRefundOfAnUnpaidPaymentIsIgnored(t *testing.T) {
	for _, st := range []string{PayCreated, PayConfirming, PayFailed} {
		p, s := paidJoin()
		p.Status, p.GrantedStart, p.GrantedEnd = st, nil, nil
		d := Decide(p, s, false, Event{EventID: "r", EventType: events.EventPaymentRefunded, IntentID: p.IntentID, AmountMinor: 4_900, Status: "refunded"}, d0)
		if d.Outcome != OutcomeRefundIgnored || d.Payment != nil || d.Sub != nil || d.ReverseCreatorPaise != 0 || d.ReverseEarningRow {
			t.Fatalf("%s: %+v", st, d)
		}
	}
}

// A renewal captured after the subscription stopped being renewable (the
// member cancelled it outright; its period end is unchanged) is refunded,
// never applied.
func TestRenewalCapturedAfterCancelIsLate(t *testing.T) {
	_, s := paidJoin()
	p := Payment{ID: uuid.New(), SubscriptionID: s.ID, PayerID: s.SubscriberID, Kind: KindRenewal, TierID: s.TierID, TierName: "Fan",
		TierPricePaise: 4_900, AmountPaise: 4_900, CreatorSharePaise: 4_900, Currency: "INR", PeriodStart: s.CurrentPeriodEnd,
		IntentID: uuid.NewString(), Status: PayCreated}
	s.Status = StatusCancelled
	d := Decide(p, s, false, Event{EventID: "s", EventType: events.EventPaymentSucceeded, IntentID: p.IntentID, PayerID: p.PayerID, AmountMinor: 4_900, Currency: "INR"}, d0)
	if d.Outcome != OutcomeLateCapture || d.Sub != nil || d.CreditCreatorPaise != 0 || d.Refund == nil {
		t.Fatalf("%+v", d)
	}
}
