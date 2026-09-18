package pricing

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
)

// LineKind names what a taxed line is; the TaxComputer maps it to a rate.
type LineKind string

const (
	// KindPassengerTransport: the ride fare, waiting and cancellation charges.
	// Passenger transport by motorcycle / auto / cab through the platform:
	// s.9(5), 5% without ITC, SAC 996412 (gst.CategoryPassengerTransportViaECO).
	KindPassengerTransport LineKind = "PASSENGER_TRANSPORT"
	// KindPlatformFee: the platform's own convenience fee, 18% (gst.CategoryPlatformFee).
	KindPlatformFee LineKind = "PLATFORM_FEE"
)

// TaxLine is one amount to tax, GST-exclusive.
type TaxLine struct {
	Ref         string
	Kind        LineKind
	AmountPaise int64
}

// TaxResult is the taxed set of lines.
type TaxResult struct {
	Lines         []TaxLineResult
	TotalTaxPaise int64
}

// TaxComputer turns exclusive lines into taxed lines. Two implementations
// exist: GSTComputer (shared/gst, production) and FlatRateTax (tests and the
// local stack when no platform GSTIN is configured).
type TaxComputer interface {
	ComputeTax(lines []TaxLine, on time.Time) (TaxResult, error)
}

// ErrPlatformGSTIN is returned when the platform GSTIN is missing or invalid.
var ErrPlatformGSTIN = errors.New("pricing: platform GSTIN")

// GSTComputer is the production TaxComputer: shared/gst's rate table with
// the platform as the electronic commerce operator liable under s.9(5) for
// the ride fare and as the supplier of its own platform fee.
type GSTComputer struct {
	table              *gst.RateTable
	platformGSTIN      string
	placeOfSupplyState string
}

// StateCodeForName returns the two-digit GST state code for a state name as
// rider_cities.state stores it ("Karnataka" -> "29"); a code passed in is
// returned as-is when valid. ok is false for anything unknown.
func StateCodeForName(name string) (code string, ok bool) {
	s := strings.TrimSpace(name)
	if s == "" {
		return "", false
	}
	if len(s) == 2 && kyc.IsValidGSTStateCode(s) {
		return s, true
	}
	for i := 1; i <= 99; i++ {
		c := fmt.Sprintf("%02d", i)
		if n, found := kyc.GSTStateName(c); found && strings.EqualFold(n, s) {
			return c, true
		}
	}
	return "", false
}

// NewGSTComputer builds the computer. platformGSTIN is validated; the place
// of supply is the two-digit GST state code of the city (StateCodeForName
// derives it from a state name), defaulting to the GSTIN's state.
func NewGSTComputer(table *gst.RateTable, platformGSTIN, placeOfSupplyState string) (*GSTComputer, error) {
	if table == nil {
		table = gst.DefaultRateTable()
	}
	g, err := kyc.ValidateGSTIN(platformGSTIN)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPlatformGSTIN, err)
	}
	pos := strings.TrimSpace(placeOfSupplyState)
	if pos == "" {
		pos = g.StateCode
	}
	if !kyc.IsValidGSTStateCode(pos) {
		return nil, fmt.Errorf("%w: place of supply %q is not a GST state code", gst.ErrInvalidPlaceOfSupply, placeOfSupplyState)
	}
	return &GSTComputer{table: table, platformGSTIN: g.Normalized, placeOfSupplyState: pos}, nil
}

// WithPlaceOfSupply returns a copy for another state (a quote in a city
// outside the platform's home state).
func (g *GSTComputer) WithPlaceOfSupply(state string) (*GSTComputer, error) {
	if !kyc.IsValidGSTStateCode(strings.TrimSpace(state)) {
		return nil, fmt.Errorf("%w: %q", gst.ErrInvalidPlaceOfSupply, state)
	}
	cp := *g
	cp.placeOfSupplyState = strings.TrimSpace(state)
	return &cp, nil
}

func categoryFor(k LineKind) (gst.Category, error) {
	switch k {
	case KindPassengerTransport:
		return gst.CategoryPassengerTransportViaECO, nil
	case KindPlatformFee:
		return gst.CategoryPlatformFee, nil
	}
	return "", fmt.Errorf("%w: unknown line kind %q", ErrInvalidInput, k)
}

// ComputeTax implements TaxComputer.
func (g *GSTComputer) ComputeTax(lines []TaxLine, on time.Time) (TaxResult, error) {
	if len(lines) == 0 {
		return TaxResult{}, nil
	}
	in := gst.Input{
		Mode:               gst.ModeExclusive,
		InvoiceDate:        on,
		ThroughECO:         true,
		Platform:           gst.Party{GSTIN: g.platformGSTIN},
		PlaceOfSupplyState: g.placeOfSupplyState,
	}
	for _, l := range lines {
		cat, err := categoryFor(l.Kind)
		if err != nil {
			return TaxResult{}, err
		}
		in.Lines = append(in.Lines, gst.Line{Ref: l.Ref, Category: cat, Amount: gst.Paise(l.AmountPaise)})
	}
	res, err := gst.Compute(g.table, in)
	if err != nil {
		return TaxResult{}, fmt.Errorf("pricing: gst: %w", err)
	}
	out := TaxResult{TotalTaxPaise: int64(res.TotalTax)}
	for _, lr := range res.Lines {
		out.Lines = append(out.Lines, TaxLineResult{
			Ref: lr.Ref, Category: string(lr.Category), SAC: lr.SAC, RateBPS: int64(lr.RateBP),
			TaxablePaise: int64(lr.Taxable), TaxPaise: int64(lr.Tax), GrossPaise: int64(lr.Gross),
		})
	}
	return out, nil
}

// FlatRateTax is the rider-local fallback table: 5% on passenger transport,
// 18% on the platform fee, rounded half up per line like shared/gst. It
// carries no GSTIN or place of supply and is for tests and the local stack.
type FlatRateTax struct {
	PassengerBPS   int64
	PlatformFeeBPS int64
}

// DefaultFlatRateTax mirrors the seeded gst rows (500 / 1800 bps).
func DefaultFlatRateTax() FlatRateTax { return FlatRateTax{PassengerBPS: 500, PlatformFeeBPS: 1800} }

// ComputeTax implements TaxComputer.
func (f FlatRateTax) ComputeTax(lines []TaxLine, _ time.Time) (TaxResult, error) {
	var out TaxResult
	for _, l := range lines {
		if l.AmountPaise < 0 {
			return TaxResult{}, fmt.Errorf("%w: negative tax line", ErrInvalidInput)
		}
		var rate int64
		var cat gst.Category
		switch l.Kind {
		case KindPassengerTransport:
			rate, cat = f.PassengerBPS, gst.CategoryPassengerTransportViaECO
		case KindPlatformFee:
			rate, cat = f.PlatformFeeBPS, gst.CategoryPlatformFee
		default:
			return TaxResult{}, fmt.Errorf("%w: unknown line kind %q", ErrInvalidInput, l.Kind)
		}
		tax := (l.AmountPaise*rate + 5000) / 10000
		out.Lines = append(out.Lines, TaxLineResult{
			Ref: l.Ref, Category: string(cat), RateBPS: rate,
			TaxablePaise: l.AmountPaise, TaxPaise: tax, GrossPaise: l.AmountPaise + tax,
		})
		out.TotalTaxPaise += tax
	}
	return out, nil
}
