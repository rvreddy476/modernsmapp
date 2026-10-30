package settlement

import "testing"

// Audit 2026-10-30 M2: an approved cheaper substitution is settled against
// the restaurant's supply, once, and its GST leaves with it.

func TestSubstitutionLowersNetSupplyAndCommission(t *testing.T) {
	base := pricedOrder(t, "RESTAURANT_STANDALONE", 1000, 0)
	sub := base
	sub.SubstitutionSupplyPaise, sub.SubstitutionTaxPaise = 10000, 500
	a := ComputeRestaurant([]Order{base}, DefaultRules())
	b := ComputeRestaurant([]Order{sub}, DefaultRules())
	if b.NetSupplyPaise != a.NetSupplyPaise-10000 {
		t.Fatalf("net supply %d, want %d", b.NetSupplyPaise, a.NetSupplyPaise-10000)
	}
	if b.CommissionPaise != Commission(a.NetSupplyPaise-10000, 1000) {
		t.Fatalf("commission %d not on the reduced supply", b.CommissionPaise)
	}
	if b.SubstitutionCreditPaise != 10000 || b.RefundSharePaise != 0 {
		t.Fatalf("credit %d refund share %d", b.SubstitutionCreditPaise, b.RefundSharePaise)
	}
	// s.9(5): the platform's GST shrinks by the substitution's GST; nothing
	// passes through to the restaurant.
	if b.ExcludedSection95GSTPaise != a.ExcludedSection95GSTPaise-500 || b.GSTPassthroughPaise != 0 {
		t.Fatalf("s.9(5) GST %d (was %d), passthrough %d", b.ExcludedSection95GSTPaise, a.ExcludedSection95GSTPaise, b.GSTPassthroughPaise)
	}
	if b.PayoutPaise >= a.PayoutPaise {
		t.Fatalf("payout %d did not fall from %d", b.PayoutPaise, a.PayoutPaise)
	}
}

func TestSubstitutionGSTLeavesTheSupplierPassthrough(t *testing.T) {
	base := pricedOrder(t, "RESTAURANT_SPECIFIED_PREMISES", 1500, 0)
	sub := base
	sub.SubstitutionSupplyPaise, sub.SubstitutionTaxPaise = 10000, 1800
	a := ComputeRestaurant([]Order{base}, DefaultRules())
	b := ComputeRestaurant([]Order{sub}, DefaultRules())
	if b.GSTPassthroughPaise != a.GSTPassthroughPaise-1800 {
		t.Fatalf("passthrough %d, want %d", b.GSTPassthroughPaise, a.GSTPassthroughPaise-1800)
	}
	if b.TCSBasePaise != a.TCSBasePaise-10000 {
		t.Fatalf("TCS base %d, want %d", b.TCSBasePaise, a.TCSBasePaise-10000)
	}
	want := b.NetSupplyPaise - b.CommissionPaise - b.CommissionGSTPaise + b.GSTPassthroughPaise - b.TCSPaise - b.RefundSharePaise
	if b.PayoutPaise != want {
		t.Fatalf("payout %d != formula %d", b.PayoutPaise, want)
	}
}

// A later refund of the rest of the order takes the restaurant's whole
// (reduced) share: the substitution refund is not counted twice.
func TestRefundAfterSubstitutionUsesTheReducedTotal(t *testing.T) {
	o := pricedOrder(t, "RESTAURANT_STANDALONE", 1000, 0)
	o.SubstitutionSupplyPaise, o.SubstitutionTaxPaise = 10000, 500
	o.ProcessedRefundPaise = o.FinalAmountPaise - 10500 // the remaining balance
	l := ComputeRestaurant([]Order{o}, DefaultRules())
	if l.RefundSharePaise != l.NetSupplyPaise {
		t.Fatalf("refund share %d, want the whole reduced supply %d", l.RefundSharePaise, l.NetSupplyPaise)
	}
}
