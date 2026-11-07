package donation

import (
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func fixture() (Donation, Fundraiser) {
	f := Fundraiser{ID: uuid.New(), GoalPaise: 100_000, RaisedPaise: 10_000, Status: "active"}
	d := Donation{ID: uuid.New(), FundraiserID: f.ID, DonorID: uuid.New(), AmountPaise: 25_000, Currency: "INR", Status: StatusPending}
	return d, f
}

func captured(d Donation) Event {
	return Event{EventID: "evt-1", EventType: events.EventPaymentSucceeded, IntentID: "pi_1",
		DonationID: d.ID, PayerID: d.DonorID, AmountMinor: d.AmountPaise, Currency: "INR"}
}

func TestOnlyTheDonorsOwnCaptureCounts(t *testing.T) {
	d, f := fixture()

	dec := Decide(d, f, captured(d))
	if dec.Outcome != OutcomeCounted || dec.RaisedDeltaPaise != 25_000 || dec.DonorDelta != 1 || dec.Complete {
		t.Fatalf("own capture = %+v", dec)
	}
	if dec.Donation == nil || dec.Donation.Status != StatusSucceeded || dec.Donation.IntentID != "pi_1" {
		t.Fatalf("the donation must become succeeded on the event's intent: %+v", dec.Donation)
	}

	for name, mutate := range map[string]func(*Event){
		"another payer":      func(e *Event) { e.PayerID = uuid.New() },
		"no payer stated":    func(e *Event) { e.PayerID = uuid.Nil },
		"one paisa short":    func(e *Event) { e.AmountMinor-- },
		"one paisa over":     func(e *Event) { e.AmountMinor++ },
		"another currency":   func(e *Event) { e.Currency = "USD" },
		"no currency stated": func(e *Event) { e.Currency = "" },
	} {
		ev := captured(d)
		mutate(&ev)
		got := Decide(d, f, ev)
		if got.Outcome != OutcomeMismatch || got.RaisedDeltaPaise != 0 || got.DonorDelta != 0 || got.Donation != nil {
			t.Errorf("%s: %+v, want a mismatch that moves nothing", name, got)
		}
	}
}

func TestACaptureReachingTheGoalCompletesTheFundraiser(t *testing.T) {
	d, f := fixture()
	f.RaisedPaise = 75_000
	if dec := Decide(d, f, captured(d)); !dec.Complete {
		t.Fatalf("75,000 + 25,000 reaches a 100,000 goal: %+v", dec)
	}
	f.Status = "paused"
	if dec := Decide(d, f, captured(d)); dec.Complete || dec.Outcome != OutcomeCounted {
		t.Fatalf("a paused fundraiser counts the paid donation but is not completed by it: %+v", dec)
	}
}

func TestACountedDonationIsNotCountedTwice(t *testing.T) {
	d, f := fixture()
	d.Status, d.IntentID = StatusSucceeded, "pi_1"
	if dec := Decide(d, f, captured(d)); dec.Outcome != OutcomeAlreadyCounted || dec.RaisedDeltaPaise != 0 {
		t.Fatalf("redelivered capture = %+v", dec)
	}
	ev := captured(d)
	ev.IntentID = "pi_other"
	if dec := Decide(d, f, ev); dec.Outcome != OutcomeMismatch || dec.RaisedDeltaPaise != 0 {
		t.Fatalf("a second payment for a counted donation must be flagged, not counted: %+v", dec)
	}
}

func TestLegacyRowsAreNotTouchedByEvents(t *testing.T) {
	d, f := fixture()
	d.Status = StatusLegacy
	if dec := Decide(d, f, captured(d)); dec.Outcome != OutcomeIgnored || dec.RaisedDeltaPaise != 0 {
		t.Fatalf("legacy row = %+v", dec)
	}
}

func TestFailureMarksOnlyAPendingDonation(t *testing.T) {
	d, f := fixture()
	ev := captured(d)
	ev.EventType = events.EventPaymentFailed
	if dec := Decide(d, f, ev); dec.Outcome != OutcomeMarkedFailed || dec.Donation.Status != StatusFailed || dec.RaisedDeltaPaise != 0 {
		t.Fatalf("failed = %+v", dec)
	}
	d.Status = StatusSucceeded
	if dec := Decide(d, f, ev); dec.Outcome != OutcomeFailedIgnored || dec.Donation != nil {
		t.Fatalf("a failure after success must not undo it: %+v", dec)
	}
}

func TestRefundsUncountExactlyWhatWasRefunded(t *testing.T) {
	d, f := fixture()
	d.Status, d.IntentID = StatusSucceeded, "pi_1"
	refund := Event{EventID: "evt-r", EventType: events.EventPaymentRefunded, IntentID: "pi_1", DonationID: d.ID, AmountMinor: 10_000}

	part := Decide(d, f, refund)
	if part.Outcome != OutcomePartiallyRefunded || part.RaisedDeltaPaise != -10_000 || part.DonorDelta != 0 ||
		part.Donation.RefundedPaise != 10_000 || part.Donation.Status != StatusPartiallyRefunded {
		t.Fatalf("partial refund = %+v", part)
	}
	rest := *part.Donation
	refund.AmountMinor = 15_000
	full := Decide(rest, f, refund)
	if full.Outcome != OutcomeRefunded || full.RaisedDeltaPaise != -15_000 || full.DonorDelta != -1 || full.Donation.Status != StatusRefunded {
		t.Fatalf("remaining refund = %+v", full)
	}

	refund.AmountMinor = 15_001
	if over := Decide(rest, f, refund); over.Outcome != OutcomeMismatch || over.RaisedDeltaPaise != 0 {
		t.Fatalf("a refund over what is left must not be applied: %+v", over)
	}
	pending, _ := fixture()
	if ign := Decide(pending, f, refund); ign.Outcome != OutcomeRefundIgnored || ign.RaisedDeltaPaise != 0 {
		t.Fatalf("a refund of an uncounted donation = %+v", ign)
	}
}

func TestCounted(t *testing.T) {
	for status, want := range map[string]bool{
		StatusLegacy: true, StatusSucceeded: true, StatusPartiallyRefunded: true,
		StatusPending: false, StatusFailed: false, StatusRefunded: false, "": false,
	} {
		if Counted(status) != want {
			t.Errorf("Counted(%q) = %v, want %v", status, !want, want)
		}
	}
}
