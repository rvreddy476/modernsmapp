package tax

// Line.CouponExcluded: an order discount scoped to some lines lowers THEIR
// taxable value only, and every identity still holds.

import (
	"testing"

	"github.com/atpost/commerce-service/internal/money"
)

func TestCouponExcludedLinesTakeNoDiscount(t *testing.T) {
	in := Input{
		Lines: []Line{
			{Ref: "a", GrossInclusive: 118000, Rate: Rate18},
			{Ref: "b", GrossInclusive: 80000, Rate: Rate12, CouponExcluded: true},
			{Ref: "c", GrossInclusive: 52500, Rate: Rate5},
		},
		OrderDiscount: 17051,
		Shipping:      4900,
	}
	out, err := Compute(in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Lines[1].AllocatedDiscount != 0 {
		t.Fatalf("excluded line took %s of the discount", out.Lines[1].AllocatedDiscount)
	}
	if out.Lines[0].AllocatedDiscount+out.Lines[2].AllocatedDiscount != 17051 {
		t.Fatalf("eligible lines took %s + %s, want 17051", out.Lines[0].AllocatedDiscount, out.Lines[2].AllocatedDiscount)
	}
	// Proportional to the eligible lines only: 118000 : 52500.
	if out.Lines[0].AllocatedDiscount <= out.Lines[2].AllocatedDiscount {
		t.Fatalf("allocation not proportional: %+v", out.Lines)
	}
	// Shipping still spreads over every line.
	if out.Lines[1].AllocatedShipping == 0 {
		t.Fatal("an excluded line must still carry its share of shipping")
	}
	var total money.Paise
	for _, l := range out.Lines {
		taxable, tx := extract(l.NetInclusive, l.Rate)
		if l.Taxable != taxable || l.Tax != tx {
			t.Fatalf("line %s tax not on its discounted net", l.Ref)
		}
		total = total.Add(l.NetInclusive)
	}
	want := money.Paise(118000 + 80000 + 52500 - 17051 + 4900)
	if out.Total != want || total != want || out.TotalTaxable+out.TotalTax != want {
		t.Fatalf("total %s / lines %s / taxable+tax %s, want %s", out.Total, total, out.TotalTaxable+out.TotalTax, want)
	}
	// The unscoped line's tax is exactly what it would be with no coupon.
	plain, _ := Compute(Input{Lines: []Line{in.Lines[0], {Ref: "b", GrossInclusive: 80000, Rate: Rate12}, in.Lines[2]}, Shipping: 4900})
	if out.Lines[1].Tax != plain.Lines[1].Tax {
		t.Fatalf("the excluded line's GST moved from %s to %s", plain.Lines[1].Tax, out.Lines[1].Tax)
	}
}

func TestCouponDiscountAboveTheEligibleLinesIsRefused(t *testing.T) {
	_, err := Compute(Input{
		Lines: []Line{
			{Ref: "a", GrossInclusive: 1000, Rate: Rate18},
			{Ref: "b", GrossInclusive: 100000, Rate: Rate18, CouponExcluded: true},
		},
		OrderDiscount: 1001,
	})
	if err == nil {
		t.Fatal("a discount larger than the lines it applies to was accepted")
	}
}

func TestCouponEveryLineExcludedTakesNoDiscount(t *testing.T) {
	out, err := Compute(Input{
		Lines:         []Line{{Ref: "a", GrossInclusive: 1000, Rate: Rate18, CouponExcluded: true}},
		OrderDiscount: 0, Shipping: 100,
	})
	if err != nil || out.Total != 1100 {
		t.Fatalf("total %v err %v", out, err)
	}
}
