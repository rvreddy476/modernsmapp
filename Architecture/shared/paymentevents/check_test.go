package paymentevents

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCheckCapture(t *testing.T) {
	payer := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	want := Expected{AmountMinor: 25000, Currency: "INR", PayerID: payer, IntentID: "i-1"}
	exact := Observed{AmountMinor: 25000, Currency: "INR", PayerID: payer, IntentID: "i-1"}

	for _, policy := range []Policy{RequireStated, AllowUnstated} {
		if err := CheckCapture(want, exact, policy); err != nil {
			t.Fatalf("policy %d: exact match refused: %v", policy, err)
		}
		lower := exact
		lower.Currency = "inr"
		if err := CheckCapture(want, lower, policy); err != nil {
			t.Fatalf("policy %d: currency case refused: %v", policy, err)
		}
		noBound := want
		noBound.IntentID = ""
		other := exact
		other.IntentID = "i-9"
		if err := CheckCapture(noBound, other, policy); err != nil {
			t.Fatalf("policy %d: an order with no bound intent compared the intent: %v", policy, err)
		}
	}

	// A STATED difference is a mismatch under both policies, field by field,
	// in order, with the detail food records.
	for _, tc := range []struct {
		name   string
		mutate func(*Observed)
		field  string
		detail string
	}{
		{"amount", func(o *Observed) { o.AmountMinor = 1 }, FieldAmount, "amount_minor 1 != order 25000"},
		{"currency", func(o *Observed) { o.Currency = "USD" }, FieldCurrency, `currency "USD" != order "INR"`},
		{"payer", func(o *Observed) { o.PayerID = uuid.MustParse("dddddddd-0000-0000-0000-000000000004") }, FieldPayer,
			"payer dddddddd-0000-0000-0000-000000000004 is not the order's customer"},
		{"intent", func(o *Observed) { o.IntentID = "i-2" }, FieldIntent, "intent i-2 is not the order's intent"},
	} {
		for _, policy := range []Policy{RequireStated, AllowUnstated} {
			got := exact
			tc.mutate(&got)
			err := CheckCapture(want, got, policy)
			var me *MismatchError
			if !errors.As(err, &me) || !errors.Is(err, ErrMismatch) || me.Field != tc.field || me.Error() != tc.detail {
				t.Fatalf("%s policy %d: err = %v, want %s %q", tc.name, policy, err, tc.field, tc.detail)
			}
		}
	}

	// The amount is checked first: a wrong amount AND a wrong payer reports the amount.
	both := exact
	both.AmountMinor, both.PayerID = 1, uuid.New()
	if err := CheckCapture(want, both, AllowUnstated); err.(*MismatchError).Field != FieldAmount {
		t.Fatalf("first failure = %v", err)
	}

	// An UNSTATED value: food refuses it, commerce does not compare it.
	for _, tc := range []struct {
		name   string
		mutate func(*Observed)
		field  string
	}{
		{"blank currency", func(o *Observed) { o.Currency = "" }, FieldCurrency},
		{"nil payer", func(o *Observed) { o.PayerID = uuid.Nil }, FieldPayer},
		{"blank intent", func(o *Observed) { o.IntentID = "" }, FieldIntent},
	} {
		got := exact
		tc.mutate(&got)
		if err := CheckCapture(want, got, AllowUnstated); err != nil {
			t.Fatalf("%s: AllowUnstated refused: %v", tc.name, err)
		}
		var me *MismatchError
		if err := CheckCapture(want, got, RequireStated); !errors.As(err, &me) || me.Field != tc.field {
			t.Fatalf("%s: RequireStated err = %v, want %s", tc.name, err, tc.field)
		}
	}
}

func TestCheckRefund(t *testing.T) {
	want := Expected{AmountMinor: 25000, IntentID: "i-1"}
	for _, ok := range []Observed{{AmountMinor: 1, IntentID: "i-1"}, {AmountMinor: 25000, IntentID: "i-1"}} {
		if err := CheckRefund(want, ok); err != nil {
			t.Fatalf("%+v refused: %v", ok, err)
		}
	}
	if err := CheckRefund(Expected{AmountMinor: 25000}, Observed{AmountMinor: 5, IntentID: "anything"}); err != nil {
		t.Fatalf("no bound intent compared the intent: %v", err)
	}
	for _, tc := range []struct {
		got    Observed
		detail string
	}{
		{Observed{AmountMinor: 0, IntentID: "i-1"}, "refund amount_minor 0 outside order 25000"},
		{Observed{AmountMinor: 25001, IntentID: "i-1"}, "refund amount_minor 25001 outside order 25000"},
		{Observed{AmountMinor: 100, IntentID: "i-2"}, "refund intent i-2 is not the order's intent"},
		{Observed{AmountMinor: 100, IntentID: ""}, "refund intent  is not the order's intent"},
	} {
		if err := CheckRefund(want, tc.got); !errors.Is(err, ErrMismatch) || err.Error() != tc.detail {
			t.Fatalf("%+v: err = %v, want %q", tc.got, err, tc.detail)
		}
	}
}
