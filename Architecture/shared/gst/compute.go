package gst

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/atpost/shared/kyc"
)

// Mode says whether line amounts already contain GST.
type Mode string

const (
	// ModeExclusive: Amount is the taxable value; tax is added on top.
	ModeExclusive Mode = "EXCLUSIVE"
	// ModeInclusive: Amount already contains GST; tax is extracted from it.
	ModeInclusive Mode = "INCLUSIVE"
)

// Party is a participant's registration. When GSTIN is set its state code
// is authoritative; a StateCode that disagrees with it is refused.
type Party struct {
	GSTIN     string
	StateCode string
}

// Line is one charge on the order.
type Line struct {
	Ref      string
	Category Category
	// Amount is inclusive or exclusive of GST according to Input.Mode.
	Amount Paise
	// PlaceOfSupplyState overrides Input.PlaceOfSupplyState for this line.
	PlaceOfSupplyState string
}

// Input is one order's tax request.
type Input struct {
	Mode        Mode
	InvoiceDate time.Time
	// ThroughECO: the order is placed through the platform acting as an
	// electronic commerce operator.
	ThroughECO bool

	Restaurant      Party
	Platform        Party
	DeliveryPartner Party
	// Driver is the ride-hailing driver (Mopedu). Only read for
	// CategoryPassengerTransportViaECO lines.
	Driver Party

	// PlaceOfSupplyState is the two-digit GST state code the caller has
	// determined as the place of supply. The package does not guess it.
	PlaceOfSupplyState string

	// RestaurantDiscount is restaurant-funded, in the same mode as the
	// amounts, and is allocated over RESTAURANT lines only, by Amount.
	RestaurantDiscount Paise

	Lines []Line
}

// LineResult is the computed, storable breakdown for one line.
type LineResult struct {
	Ref      string
	Category Category
	SAC      string
	RateBP   RateBP

	ITCAvailable             bool
	NeedsAdviserConfirmation bool
	RateEffectiveFrom        time.Time

	Supplier  SupplierRole
	Liability Liability
	// LiableParty collects and deposits the tax; LiablePartyGSTIN is its
	// normalised GSTIN.
	LiableParty      SupplierRole
	LiablePartyGSTIN string
	// ECOCollectsTCS marks a supplier-liable, non-platform supply made
	// through the ECO, on which the ECO collects TCS under s.52. MARKER ONLY:
	// the TCS amount is not computed by this package.
	ECOCollectsTCS bool

	SupplierState      string
	PlaceOfSupplyState string
	Interstate         bool

	Amount            Paise
	AllocatedDiscount Paise
	// Taxable + Tax == Gross. Gross is what the customer pays for the line.
	Taxable Paise
	Tax     Paise
	CGST    Paise
	SGST    Paise
	IGST    Paise
	Gross   Paise
}

// PartyTotals aggregates the lines one party is liable for, under one
// liability.
type PartyTotals struct {
	LiableParty SupplierRole
	GSTIN       string
	Liability   Liability
	Taxable     Paise
	Tax         Paise
	CGST        Paise
	SGST        Paise
	IGST        Paise
	Gross       Paise
}

// Result is the computed order.
type Result struct {
	Lines         []LineResult
	ByLiableParty []PartyTotals

	Total        Paise
	TotalTaxable Paise
	TotalTax     Paise
	TotalCGST    Paise
	TotalSGST    Paise
	TotalIGST    Paise

	// NeedsAdviserConfirmation is true when ANY line used a rate row flagged
	// for adviser confirmation. Do not file or present these numbers as
	// settled while it is true.
	NeedsAdviserConfirmation bool
}

type resolvedParty struct {
	gstin string
	state string
}

func resolveParty(role SupplierRole, p Party) (resolvedParty, error) {
	var rp resolvedParty
	if strings.TrimSpace(p.GSTIN) != "" {
		g, err := kyc.ValidateGSTIN(p.GSTIN)
		if err != nil {
			return rp, fmt.Errorf("gst: %s GSTIN: %w", role, err)
		}
		rp.gstin = g.Normalized
		rp.state = g.StateCode
	}
	if sc := strings.TrimSpace(p.StateCode); sc != "" {
		if !kyc.IsValidGSTStateCode(sc) {
			return rp, fmt.Errorf("%w: %s state code is not an assigned GST state code", ErrPartyState, role)
		}
		if rp.gstin != "" && sc != rp.state {
			return rp, fmt.Errorf("%w: %s state code disagrees with its GSTIN", ErrPartyState, role)
		}
		if rp.gstin == "" {
			rp.state = sc
		}
	}
	return rp, nil
}

type groupKey struct {
	liable    SupplierRole
	liability Liability
	supplier  SupplierRole
	rate      RateBP
	sac       string
	pos       string
}

// Compute computes GST for one order.
func Compute(table *RateTable, in Input) (*Result, error) {
	if table == nil {
		return nil, ErrNoRateTable
	}
	if len(in.Lines) == 0 {
		return nil, ErrNoLines
	}
	if len(in.Lines) > MaxLines {
		return nil, fmt.Errorf("%w: more than %d lines", ErrAmountOutOfRange, MaxLines)
	}
	if in.Mode != ModeExclusive && in.Mode != ModeInclusive {
		return nil, ErrInvalidMode
	}
	if in.RestaurantDiscount < 0 {
		return nil, fmt.Errorf("%w: restaurant discount", ErrNegativeAmount)
	}
	pos := strings.TrimSpace(in.PlaceOfSupplyState)
	if !kyc.IsValidGSTStateCode(pos) {
		return nil, ErrInvalidPlaceOfSupply
	}

	parties := map[SupplierRole]resolvedParty{}
	for role, p := range map[SupplierRole]Party{
		SupplierRestaurant: in.Restaurant, SupplierPlatform: in.Platform, SupplierDeliveryPartner: in.DeliveryPartner,
		SupplierDriver: in.Driver,
	} {
		rp, err := resolveParty(role, p)
		if err != nil {
			return nil, err
		}
		parties[role] = rp
	}

	res := &Result{Lines: make([]LineResult, len(in.Lines))}
	var restIdx []int
	var restWeights []Paise
	var restSum Paise

	for i, l := range in.Lines {
		if l.Amount < 0 {
			return nil, fmt.Errorf("%w: line %d", ErrNegativeAmount, i)
		}
		if l.Amount > MaxAmount {
			return nil, fmt.Errorf("%w: line %d", ErrAmountOutOfRange, i)
		}
		row, err := table.Lookup(l.Category, in.InvoiceDate)
		if err != nil {
			return nil, fmt.Errorf("gst: line %d: %w", i, err)
		}
		if row.RateBP == 0 && !row.ExplicitZeroRate {
			return nil, fmt.Errorf("%w: line %d", ErrZeroRateNotExplicit, i)
		}

		liability := LiabilitySupplier
		if in.ThroughECO && row.ECOSection95 {
			liability = LiabilityECOSection95
		}
		if row.Supplier == SupplierDeliveryPartner && liability != LiabilityECOSection95 {
			return nil, fmt.Errorf("%w: line %d: delivery-partner supply outside s.9(5) is not modelled", ErrUnsupportedSupply, i)
		}
		if row.Supplier == SupplierDriver && liability != LiabilityECOSection95 {
			return nil, fmt.Errorf("%w: line %d: driver supply outside s.9(5) is not modelled", ErrUnsupportedSupply, i)
		}
		liable := row.Supplier
		if liability == LiabilityECOSection95 {
			liable = SupplierPlatform
		}
		lp := parties[liable]
		if lp.gstin == "" {
			return nil, fmt.Errorf("%w: line %d: %s has no GSTIN", ErrLiablePartyUnregistered, i, liable)
		}

		linePos := pos
		if o := strings.TrimSpace(l.PlaceOfSupplyState); o != "" {
			if !kyc.IsValidGSTStateCode(o) {
				return nil, fmt.Errorf("%w: line %d", ErrInvalidPlaceOfSupply, i)
			}
			linePos = o
		}

		res.Lines[i] = LineResult{
			Ref:                      l.Ref,
			Category:                 l.Category,
			SAC:                      row.SAC,
			RateBP:                   row.RateBP,
			ITCAvailable:             row.ITCAvailable,
			NeedsAdviserConfirmation: row.NeedsAdviserConfirmation,
			RateEffectiveFrom:        row.EffectiveFrom,
			Supplier:                 row.Supplier,
			Liability:                liability,
			LiableParty:              liable,
			LiablePartyGSTIN:         lp.gstin,
			ECOCollectsTCS:           in.ThroughECO && liability == LiabilitySupplier && row.Supplier != SupplierPlatform,
			SupplierState:            lp.state,
			PlaceOfSupplyState:       linePos,
			Interstate:               lp.state != linePos,
			Amount:                   l.Amount,
		}
		res.NeedsAdviserConfirmation = res.NeedsAdviserConfirmation || row.NeedsAdviserConfirmation
		if row.Supplier == SupplierRestaurant {
			restIdx = append(restIdx, i)
			restWeights = append(restWeights, l.Amount)
			restSum += l.Amount
		}
	}

	// Restaurant-funded discount, over restaurant lines by Amount.
	if in.RestaurantDiscount > restSum {
		return nil, ErrDiscountExceedsSubtotal
	}
	alloc, err := Allocate(in.RestaurantDiscount, restWeights)
	if err != nil {
		return nil, err
	}
	for k, i := range restIdx {
		res.Lines[i].AllocatedDiscount = alloc[k]
	}

	// Group lines, in order of first appearance, so tax is rounded once per
	// (liable party, liability, supplier, rate, SAC, place of supply) and then
	// allocated back to lines.
	var order []groupKey
	members := map[groupKey][]int{}
	for i := range res.Lines {
		lr := &res.Lines[i]
		k := groupKey{lr.LiableParty, lr.Liability, lr.Supplier, lr.RateBP, lr.SAC, lr.PlaceOfSupplyState}
		if _, ok := members[k]; !ok {
			order = append(order, k)
		}
		members[k] = append(members[k], i)
	}
	for _, k := range order {
		idx := members[k]
		nets := make([]Paise, len(idx))
		var groupNet Paise
		for j, i := range idx {
			net := res.Lines[i].Amount - res.Lines[i].AllocatedDiscount
			nets[j] = net
			groupNet += net
		}
		var groupTax Paise
		if in.Mode == ModeInclusive {
			_, groupTax = extract(groupNet, k.rate)
		} else {
			groupTax = taxOnExclusive(groupNet, k.rate)
		}
		taxes, err := Allocate(groupTax, nets)
		if err != nil {
			return nil, err
		}
		for j, i := range idx {
			lr := &res.Lines[i]
			lr.Tax = taxes[j]
			if in.Mode == ModeInclusive {
				lr.Gross = nets[j]
				lr.Taxable = nets[j] - taxes[j]
			} else {
				lr.Taxable = nets[j]
				lr.Gross = nets[j] + taxes[j]
			}
			if lr.Interstate {
				lr.IGST = lr.Tax
			} else {
				// commerce-service/internal/tax/gst.go:223-226: half to CGST
				// (floor), remainder to SGST, so no paise is lost on odd tax.
				lr.CGST = lr.Tax / 2
				lr.SGST = lr.Tax - lr.CGST
			}
		}
	}

	type partyKey struct {
		role      SupplierRole
		liability Liability
	}
	partyAt := map[partyKey]int{}
	for _, lr := range res.Lines {
		res.Total += lr.Gross
		res.TotalTaxable += lr.Taxable
		res.TotalTax += lr.Tax
		res.TotalCGST += lr.CGST
		res.TotalSGST += lr.SGST
		res.TotalIGST += lr.IGST

		pk := partyKey{lr.LiableParty, lr.Liability}
		at, ok := partyAt[pk]
		if !ok {
			at = len(res.ByLiableParty)
			partyAt[pk] = at
			res.ByLiableParty = append(res.ByLiableParty, PartyTotals{LiableParty: lr.LiableParty, GSTIN: lr.LiablePartyGSTIN, Liability: lr.Liability})
		}
		pt := &res.ByLiableParty[at]
		pt.Taxable += lr.Taxable
		pt.Tax += lr.Tax
		pt.CGST += lr.CGST
		pt.SGST += lr.SGST
		pt.IGST += lr.IGST
		pt.Gross += lr.Gross
	}
	sort.SliceStable(res.ByLiableParty, func(a, b int) bool {
		pa, pb := res.ByLiableParty[a], res.ByLiableParty[b]
		if roleRank(pa.LiableParty) != roleRank(pb.LiableParty) {
			return roleRank(pa.LiableParty) < roleRank(pb.LiableParty)
		}
		return liabilityRank(pa.Liability) < liabilityRank(pb.Liability)
	})

	if err := checkInvariants(in, res); err != nil {
		return nil, err
	}
	return res, nil
}

func roleRank(r SupplierRole) int {
	switch r {
	case SupplierRestaurant:
		return 0
	case SupplierPlatform:
		return 1
	}
	return 2
}

func liabilityRank(l Liability) int {
	if l == LiabilitySupplier {
		return 0
	}
	return 1
}

func invariantf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInternalInvariant}, args...)...)
}

// checkInvariants asserts the identities the package exists for, in the
// manner of commerce-service/internal/tax/gst.go:241-251. A failure is a
// programming error.
func checkInvariants(in Input, res *Result) error {
	var t PartyTotals
	var discount, netSum Paise
	for i, l := range res.Lines {
		net := l.Amount - l.AllocatedDiscount
		// I4: nothing negative, and tax never exceeds what the line costs.
		if net < 0 || l.Taxable < 0 || l.Tax < 0 || l.CGST < 0 || l.SGST < 0 || l.IGST < 0 || l.Tax > l.Gross {
			return invariantf("line %d has a negative component or tax above gross", i)
		}
		// I1
		if l.Taxable+l.Tax != l.Gross {
			return invariantf("line %d taxable + tax != gross", i)
		}
		// I2
		if l.CGST+l.SGST+l.IGST != l.Tax {
			return invariantf("line %d components != tax", i)
		}
		// I3
		if (l.Interstate && (l.CGST != 0 || l.SGST != 0)) || (!l.Interstate && l.IGST != 0) {
			return invariantf("line %d mixes IGST with CGST/SGST", i)
		}
		if (in.Mode == ModeInclusive && l.Gross != net) || (in.Mode == ModeExclusive && l.Taxable != net) {
			return invariantf("line %d does not match its net amount", i)
		}
		discount += l.AllocatedDiscount
		netSum += net
		t.Taxable += l.Taxable
		t.Tax += l.Tax
		t.CGST += l.CGST
		t.SGST += l.SGST
		t.IGST += l.IGST
		t.Gross += l.Gross
	}
	// I5
	if res.Total != t.Gross || res.TotalTaxable != t.Taxable || res.TotalTax != t.Tax ||
		res.TotalCGST != t.CGST || res.TotalSGST != t.SGST || res.TotalIGST != t.IGST {
		return invariantf("totals != sum of lines")
	}
	// I6
	var p PartyTotals
	for _, pt := range res.ByLiableParty {
		p.Taxable += pt.Taxable
		p.Tax += pt.Tax
		p.CGST += pt.CGST
		p.SGST += pt.SGST
		p.IGST += pt.IGST
		p.Gross += pt.Gross
	}
	if p != (PartyTotals{Taxable: t.Taxable, Tax: t.Tax, CGST: t.CGST, SGST: t.SGST, IGST: t.IGST, Gross: t.Gross}) {
		return invariantf("party totals != totals")
	}
	// I7
	want := netSum
	if in.Mode == ModeExclusive {
		want += res.TotalTax
	}
	if res.Total != want {
		return invariantf("total != net amounts (+ tax when exclusive)")
	}
	// I8
	if discount != in.RestaurantDiscount {
		return invariantf("allocated discount != restaurant discount")
	}
	return nil
}
