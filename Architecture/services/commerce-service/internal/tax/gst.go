// Package tax computes Indian GST for an order, in integer paise, under
// GST-INCLUSIVE catalogue semantics.
//
// Decision D1: `product_variants.selling_price` is the price the buyer pays.
// It already contains GST. The Legal Metrology (Packaged Commodities) Rules
// require the declared retail sale price on pre-packaged goods to be
// inclusive of all taxes, and Indian consumer marketplaces display inclusive
// prices. So tax is EXTRACTED from the line, never ADDED to it. Adding 18%
// to a ₹1,180 listing and charging ₹1,392.40 is the defect this package
// exists to make impossible.
//
// Amendment A4 requires more than "per line, half-up". The order-level
// discount and the shipping charge are both order-level amounts that must be
// spread across lines before tax can be extracted, because each line may sit
// on a different slab. Spreading money by proportion always leaves a
// remainder, and dropping it breaks the one invariant that matters:
//
//	sum(line taxable) + sum(line tax) == amount captured
//
// So allocation uses the largest-remainder method with a deterministic
// tie-break, and every split is exact by construction: the residual is
// distributed, never truncated away.
//
// Shipping is allocated across lines and taxed at each line's own rate
// because delivery is ancillary to the goods — under composite-supply
// treatment it attracts the rate of the principal supply, not a rate of its
// own.
package tax

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/atpost/commerce-service/internal/money"
)

// RateBP is a GST rate in basis points: 18% is 1800. Basis points keep the
// rate integral, so a 2.5% CGST half never introduces a fraction.
type RateBP int32

// The five statutory slabs, seeded in `tax_classes`.
const (
	Rate0  RateBP = 0
	Rate5  RateBP = 500
	Rate12 RateBP = 1200
	Rate18 RateBP = 1800
	Rate28 RateBP = 2800
)

// Line is one order line as it enters the tax computation.
//
// GrossInclusive is unit price × quantity, GST included. LineDiscount is a
// line-scoped discount already applied by the seller (also inclusive); it is
// subtracted before order-level allocation so a line-level markdown does not
// attract a share of the order-level coupon.
type Line struct {
	// Ref identifies the line for the caller. Any stable string; used only
	// to key results and to make the tie-break deterministic.
	Ref            string
	GrossInclusive money.Paise
	LineDiscount   money.Paise
	Rate           RateBP
}

// LineTax is the computed, storable tax breakdown for one line. Every field
// is persisted on `order_items` so a later refund reuses these numbers
// instead of recomputing them against a catalogue that may have moved.
type LineTax struct {
	Ref string

	// NetInclusive is what this line contributes to the amount charged:
	// gross - line discount - allocated order discount + allocated shipping.
	NetInclusive money.Paise

	// AllocatedDiscount and AllocatedShipping are this line's share of the
	// order-level amounts, stored so the allocation is auditable and a
	// refund can reverse exactly what was charged.
	AllocatedDiscount money.Paise
	AllocatedShipping money.Paise

	// Taxable is the value excluding GST; Tax is the GST component.
	// Taxable + Tax == NetInclusive, exactly, always.
	Taxable money.Paise
	Tax     money.Paise

	// Exactly one of (CGST+SGST) or IGST is non-zero. CGST+SGST == Tax for
	// an intrastate supply; IGST == Tax for an interstate one.
	CGST money.Paise
	SGST money.Paise
	IGST money.Paise

	Rate RateBP
}

// Order is the computed result for a whole order.
type Order struct {
	Lines []LineTax

	// GrossInclusive is the sum of line gross before any order-level amount.
	GrossInclusive money.Paise
	// LineDiscounts is the sum of seller line-level discounts.
	LineDiscounts money.Paise
	// OrderDiscount is the order-level (coupon) discount actually applied.
	OrderDiscount money.Paise
	// Shipping is the delivery charge, GST-inclusive.
	Shipping money.Paise

	// TotalTaxable, TotalTax and Total are the order aggregates.
	// TotalTaxable + TotalTax == Total, exactly.
	Total        money.Paise
	TotalTaxable money.Paise
	TotalTax     money.Paise
	TotalCGST    money.Paise
	TotalSGST    money.Paise
	TotalIGST    money.Paise

	// Interstate records which split was applied, so the invoice can label
	// it and a later refund reproduces it.
	Interstate bool
}

// Input is the whole-order tax request.
type Input struct {
	Lines []Line

	// OrderDiscount is the coupon amount, GST-inclusive, order-level.
	OrderDiscount money.Paise
	// Shipping is the delivery charge, GST-inclusive.
	Shipping money.Paise

	// Interstate selects IGST over CGST+SGST. The caller derives it by
	// comparing the seller's place of supply with the delivery address
	// state; this package does not guess.
	Interstate bool
}

// Compute extracts GST for an order under inclusive semantics.
//
// It is total-preserving by construction: the returned Total equals
// sum(gross) - sum(line discounts) - order discount + shipping, and
// TotalTaxable + TotalTax equals that same number. The proof suite asserts
// both identities across randomised inputs, not just the golden vectors.
func Compute(in Input) (*Order, error) {
	if len(in.Lines) == 0 {
		return nil, fmt.Errorf("tax: no lines")
	}
	if err := money.MustNonNegative("order_discount", in.OrderDiscount); err != nil {
		return nil, err
	}
	if err := money.MustNonNegative("shipping", in.Shipping); err != nil {
		return nil, err
	}

	// Net-of-line-discount gross is the weight basis for both allocations.
	// A line discounted to zero draws no share of the coupon and no share
	// of shipping, which is the intuitive reading and keeps weights >= 0.
	weights := make([]money.Paise, len(in.Lines))
	var totalWeight, grossSum, lineDiscSum money.Paise
	for i, l := range in.Lines {
		if err := money.MustNonNegative("line_gross", l.GrossInclusive); err != nil {
			return nil, err
		}
		if err := money.MustNonNegative("line_discount", l.LineDiscount); err != nil {
			return nil, err
		}
		if l.LineDiscount > l.GrossInclusive {
			return nil, fmt.Errorf("tax: line %s discount %s exceeds gross %s", l.Ref, l.LineDiscount, l.GrossInclusive)
		}
		if l.Rate < 0 {
			return nil, fmt.Errorf("tax: line %s has negative rate", l.Ref)
		}
		w := l.GrossInclusive.Sub(l.LineDiscount)
		weights[i] = w
		totalWeight = totalWeight.Add(w)
		grossSum = grossSum.Add(l.GrossInclusive)
		lineDiscSum = lineDiscSum.Add(l.LineDiscount)
	}

	if in.OrderDiscount > totalWeight {
		return nil, fmt.Errorf("tax: order discount %s exceeds eligible subtotal %s", in.OrderDiscount, totalWeight)
	}

	discAlloc, err := Allocate(in.OrderDiscount, weights)
	if err != nil {
		return nil, fmt.Errorf("tax: allocate discount: %w", err)
	}
	shipAlloc, err := Allocate(in.Shipping, weights)
	if err != nil {
		return nil, fmt.Errorf("tax: allocate shipping: %w", err)
	}

	out := &Order{
		Lines:          make([]LineTax, len(in.Lines)),
		GrossInclusive: grossSum,
		LineDiscounts:  lineDiscSum,
		OrderDiscount:  in.OrderDiscount,
		Shipping:       in.Shipping,
		Interstate:     in.Interstate,
	}

	for i, l := range in.Lines {
		net := weights[i].Sub(discAlloc[i]).Add(shipAlloc[i])
		if net.IsNegative() {
			// Unreachable given the discount<=weight guard above, but a
			// negative net would silently invert a tax sign, so refuse.
			return nil, fmt.Errorf("tax: line %s net is negative (%s)", l.Ref, net)
		}
		taxable, tx := extract(net, l.Rate)

		lt := LineTax{
			Ref:               l.Ref,
			NetInclusive:      net,
			AllocatedDiscount: discAlloc[i],
			AllocatedShipping: shipAlloc[i],
			Taxable:           taxable,
			Tax:               tx,
			Rate:              l.Rate,
		}
		if in.Interstate {
			lt.IGST = tx
		} else {
			// Half to CGST (floor), remainder to SGST, so the two always
			// sum to Tax with no paise lost on an odd amount.
			lt.CGST = tx / 2
			lt.SGST = tx.Sub(lt.CGST)
		}

		out.Lines[i] = lt
		out.Total = out.Total.Add(net)
		out.TotalTaxable = out.TotalTaxable.Add(taxable)
		out.TotalTax = out.TotalTax.Add(tx)
		out.TotalCGST = out.TotalCGST.Add(lt.CGST)
		out.TotalSGST = out.TotalSGST.Add(lt.SGST)
		out.TotalIGST = out.TotalIGST.Add(lt.IGST)
	}

	// Belt and braces: these identities are what the whole package is for,
	// so assert rather than trust. A violation is a programming error, not
	// a user error, and must never reach a payment.
	if out.TotalTaxable.Add(out.TotalTax) != out.Total {
		return nil, fmt.Errorf("tax: internal invariant broken: taxable %s + tax %s != total %s",
			out.TotalTaxable, out.TotalTax, out.Total)
	}
	expected := grossSum.Sub(lineDiscSum).Sub(in.OrderDiscount).Add(in.Shipping)
	if out.Total != expected {
		return nil, fmt.Errorf("tax: internal invariant broken: total %s != expected %s", out.Total, expected)
	}
	if out.TotalCGST.Add(out.TotalSGST).Add(out.TotalIGST) != out.TotalTax {
		return nil, fmt.Errorf("tax: internal invariant broken: component split != total tax")
	}
	return out, nil
}

// extract splits a GST-inclusive amount into taxable value and tax.
//
//	taxable = floor(net * 10000 / (10000 + rateBP))
//	tax     = net - taxable
//
// Deriving tax by subtraction rather than by its own rounded formula is what
// guarantees taxable + tax == net for every input, including the awkward
// ones. big.Int carries the intermediate product so a large order cannot
// overflow int64 on the multiply.
func extract(net money.Paise, rate RateBP) (taxable, tx money.Paise) {
	if rate == 0 || net == 0 {
		return net, 0
	}
	num := new(big.Int).Mul(big.NewInt(net.Int64()), big.NewInt(10000))
	den := big.NewInt(int64(10000 + rate))
	q := new(big.Int).Quo(num, den) // truncation; net >= 0 so this is floor
	taxable = money.Paise(q.Int64())
	tx = net.Sub(taxable)
	return taxable, tx
}

// Allocate spreads `amount` across `weights` using the largest-remainder
// method, so the parts sum to `amount` exactly.
//
// Each part gets floor(amount * wᵢ / W). That leaves a shortfall of at most
// len(weights)-1 paise, which is handed out one paise at a time to the
// entries with the largest fractional remainder. Ties break by lower index,
// which makes the result a pure function of the input ordering — the caller
// orders lines deterministically (by variant id), so the same cart always
// produces the same allocation and a refund can reproduce it.
//
// When every weight is zero (a fully discounted order that still carries
// shipping) proportion is undefined, so the amount goes to the first entry
// rather than being silently dropped.
func Allocate(amount money.Paise, weights []money.Paise) ([]money.Paise, error) {
	out := make([]money.Paise, len(weights))
	if len(weights) == 0 {
		if amount != 0 {
			return nil, fmt.Errorf("tax: cannot allocate %s across zero lines", amount)
		}
		return out, nil
	}
	if amount == 0 {
		return out, nil
	}
	if amount.IsNegative() {
		return nil, fmt.Errorf("tax: cannot allocate a negative amount (%s)", amount)
	}

	var total money.Paise
	for _, w := range weights {
		if w.IsNegative() {
			return nil, fmt.Errorf("tax: negative weight")
		}
		total = total.Add(w)
	}
	if total == 0 {
		out[0] = amount
		return out, nil
	}

	type rem struct {
		idx int
		r   *big.Int
	}
	rems := make([]rem, 0, len(weights))

	amt := big.NewInt(amount.Int64())
	tot := big.NewInt(total.Int64())
	var assigned money.Paise

	for i, w := range weights {
		num := new(big.Int).Mul(amt, big.NewInt(w.Int64()))
		q, r := new(big.Int).QuoRem(num, tot, new(big.Int))
		out[i] = money.Paise(q.Int64())
		assigned = assigned.Add(out[i])
		rems = append(rems, rem{idx: i, r: r})
	}

	short := amount.Sub(assigned)
	if short < 0 {
		return nil, fmt.Errorf("tax: allocation overshot by %s", -short)
	}
	if short == 0 {
		return out, nil
	}

	sort.SliceStable(rems, func(a, b int) bool {
		c := rems[a].r.Cmp(rems[b].r)
		if c != 0 {
			return c > 0 // larger remainder first
		}
		return rems[a].idx < rems[b].idx // deterministic tie-break
	})
	for i := 0; i < int(short); i++ {
		out[rems[i%len(rems)].idx]++
	}
	return out, nil
}

// RefundComponents apportions a partial refund across the stored line tax
// components of the original order.
//
// A4 requires a refund to reuse what was stored rather than recompute
// against a catalogue that has since moved. The refund is allocated over the
// lines' NetInclusive using the same largest-remainder rule, then each
// line's share is split back into taxable/tax at that line's stored rate —
// so a refunded rupee carries the same GST it was charged with, and a full
// refund reverses the order exactly.
func RefundComponents(refund money.Paise, lines []LineTax, interstate bool) ([]LineTax, error) {
	if err := money.MustNonNegative("refund", refund); err != nil {
		return nil, err
	}
	weights := make([]money.Paise, len(lines))
	var total money.Paise
	for i, l := range lines {
		weights[i] = l.NetInclusive
		total = total.Add(l.NetInclusive)
	}
	if refund > total {
		return nil, fmt.Errorf("tax: refund %s exceeds order net %s", refund, total)
	}
	alloc, err := Allocate(refund, weights)
	if err != nil {
		return nil, err
	}
	out := make([]LineTax, len(lines))
	for i, l := range lines {
		net := alloc[i]
		taxable, tx := extract(net, l.Rate)
		r := LineTax{
			Ref:          l.Ref,
			NetInclusive: net,
			Taxable:      taxable,
			Tax:          tx,
			Rate:         l.Rate,
		}
		if interstate {
			r.IGST = tx
		} else {
			r.CGST = tx / 2
			r.SGST = tx.Sub(r.CGST)
		}
		out[i] = r
	}
	return out, nil
}

// RateFromPercent converts a NUMERIC(5,2) percentage as stored in
// `tax_classes` into basis points, rejecting anything that is not a whole
// number of basis points. A rate of 18.005% is a data error, not a rounding
// opportunity.
func RateFromPercent(pct float64) (RateBP, error) {
	scaled := pct * 100
	r := int64(scaled + 0.5)
	if scaled < 0 {
		r = int64(scaled - 0.5)
	}
	if diff := scaled - float64(r); diff > 1e-6 || diff < -1e-6 {
		return 0, fmt.Errorf("tax: rate %.4f%% is not a whole basis point", pct)
	}
	if r < 0 || r > 10000 {
		return 0, fmt.Errorf("tax: rate %.2f%% out of range", pct)
	}
	return RateBP(r), nil
}
