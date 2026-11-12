package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/monetization-service/internal/service/premiumtest"
	"github.com/atpost/shared/gst"
	"github.com/google/uuid"
)

// Prices exclude GST (founder, 13 Nov 2026): the pass, the intent and the
// signed event all carry the GST-inclusive total.

func TestPremiumCheckoutChargesTheGSTInclusiveTotal(t *testing.T) {
	r := newPremiumRig(t, priced())
	for _, c := range []struct {
		plan                string
		taxable, tax, total int64
	}{{premium.Plan30d, 14900, 2682, 17582}, {premium.Plan365d, 149900, 26982, 176882}} {
		co := r.checkout(t, c.plan)
		if co.AmountPaise != c.total || *co.TotalPaise != c.total || *co.TaxablePaise != c.taxable || *co.GSTPaise != c.tax ||
			*co.CGSTPaise+*co.SGSTPaise+*co.IGSTPaise != c.tax || !co.PricesExcludeGST || *co.SAC != "998439" {
			t.Fatalf("%s checkout: %+v", c.plan, co)
		}
		if got := r.pay.IntentFor(co.PassID); got == nil || got.AmountMinor != c.total {
			t.Fatalf("%s intent must be for the total: %+v", c.plan, got)
		}
		stored := r.store.Periods[co.PassID]
		if stored.AmountPaise != c.total || stored.Tax == nil || stored.Tax.TaxablePaise != c.taxable || stored.Tax.TotalPaise != c.total {
			t.Fatalf("%s stored pass: %+v", c.plan, stored)
		}
		pass, err := r.svc.Pass(r.ctx, r.buyer, co.PassID)
		if err != nil || pass.AmountPaise != c.total || *pass.TotalPaise != c.total || *pass.TaxablePaise != c.taxable || !pass.PricesExcludeGST {
			t.Fatalf("%s pass view: %+v %v", c.plan, pass, err)
		}
	}
}

// A signed capture for the pre-tax price is a mismatch: nothing is granted.
func TestPremiumSignedEventAtThePreTaxAmountIsRefused(t *testing.T) {
	r := newPremiumRig(t, priced())
	co := r.checkout(t, premium.Plan30d)
	ev := r.event(t, co, "payment.succeeded")
	ev.AmountMinor = 14900
	if out := r.apply(t, ev); out != premium.OutcomeMismatch {
		t.Fatalf("pre-tax capture: %s", out)
	}
	if s := r.status(t); s.Active || len(r.roles.Snapshot()) != 0 {
		t.Fatalf("pre-tax capture granted something: %+v", s)
	}
	if out := r.apply(t, r.event(t, co, "payment.succeeded")); out != premium.OutcomeActivated {
		t.Fatalf("capture of the total: %s", out)
	}
}

// When GST cannot be computed nothing is opened or charged.
func TestPremiumCheckoutFailsClosedWithoutTax(t *testing.T) {
	only, err := gst.NewRateTable([]gst.RateRow{{Category: gst.CategoryPlatformFee, Supplier: gst.SupplierPlatform, RateBP: 1800,
		SAC: "998599", EffectiveFrom: time.Date(2025, 9, 22, 0, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := premium.NewTax(only, "")
	if err != nil {
		t.Fatal(err)
	}
	r := newPremiumRig(t, priced())
	r.svc.WithTax(tx)
	if _, err := r.svc.Checkout(r.ctx, r.buyer, premium.Plan30d); !errors.Is(err, premium.ErrTaxUnavailable) {
		t.Fatalf("checkout without tax: %v", err)
	}
	if len(r.store.Periods) != 0 || len(r.pay.Calls) != 0 {
		t.Fatal("nothing may be opened without the tax")
	}
	plans, _ := r.svc.Plans(r.ctx)
	for _, p := range plans {
		if p.Available {
			t.Fatalf("untaxable plan offered: %+v", p)
		}
	}
}

// tamperingStore returns a pass whose amount is not its GST-inclusive total.
type tamperingStore struct {
	*premiumtest.Store
	mutate func(*premium.Period)
}

func (s tamperingStore) OpenPremiumCheckout(ctx context.Context, u uuid.UUID, plan premium.PlanRow, tax premium.Breakdown, now time.Time) (*premium.Period, error) {
	p, err := s.Store.OpenPremiumCheckout(ctx, u, plan, tax, now)
	if err == nil {
		s.mutate(p)
	}
	return p, err
}

func TestPremiumNeverChargesAPassThatIsNotItsTotal(t *testing.T) {
	cases := map[string]func(*premium.Period){
		"pre-tax amount": func(p *premium.Period) { p.AmountPaise = p.Tax.TaxablePaise },
		"no split":       func(p *premium.Period) { p.Tax = nil },
		"other split": func(p *premium.Period) {
			b := *p.Tax
			b.TotalPaise, b.TaxPaise = b.TotalPaise+1, b.TaxPaise+1
			p.Tax, p.AmountPaise = &b, b.TotalPaise
		},
		"stored split disagrees with the amount": func(p *premium.Period) {
			b := *p.Tax
			b.TotalPaise++
			p.Tax = &b
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			pay := premiumtest.NewPayments()
			svc := NewPremium(tamperingStore{premiumtest.New(), mutate}, priced()).WithPayments(pay, false)
			if _, err := svc.Checkout(context.Background(), uuid.New(), premium.Plan30d); !errors.Is(err, ErrPremiumAmountMismatch) {
				t.Fatalf("%v", err)
			}
			if len(pay.Calls) != 0 {
				t.Fatal("no intent may be opened")
			}
		})
	}
}

// A retry reuses the open checkout only at the same split: a change of SAC
// (same total) opens a new pass, so a pass never carries a stale split.
func TestPremiumCheckoutReopensWhenTheSplitChanges(t *testing.T) {
	r := newPremiumRig(t, priced())
	a := r.checkout(t, premium.Plan30d)
	other, err := gst.NewRateTable([]gst.RateRow{{Category: premium.TaxCategory, Supplier: gst.SupplierPlatform, RateBP: 1800,
		ITCAvailable: true, SAC: "998431", EffectiveFrom: time.Date(2025, 9, 22, 0, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := premium.NewTax(other, "")
	if err != nil {
		t.Fatal(err)
	}
	r.svc.WithTax(tx)
	b := r.checkout(t, premium.Plan30d)
	if b.PassID == a.PassID || *b.SAC != "998431" || b.AmountPaise != a.AmountPaise {
		t.Fatalf("a changed split must open a new pass: %+v", b)
	}
	if r.store.Periods[a.PassID].Status != premium.StatusAbandoned {
		t.Fatal("the old checkout must be abandoned")
	}
}
