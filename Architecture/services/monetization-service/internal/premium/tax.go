package premium

// GST on Premium (founder, 13 Nov 2026): prices EXCLUDE GST. The configured
// price (PREMIUM_PRICE_*_PAISE) is the taxable value; GST at the rate of
// shared/gst's CategoryPlatformDigitalSubscription row (18%) is added on top,
// and the buyer pays the total. The total is the payments intent amount and
// the amount the signed event must carry.
//
// Two paths, both from the shared rate table, never floats:
//
//   - computed: gst.Compute in ModeExclusive with the platform as the
//     supplier and liable party (PREMIUM_PLATFORM_GSTIN). Required in
//     production: boot refuses Premium without it.
//   - estimated: outside production with no GSTIN configured, the same row's
//     rate and SAC and shared/gst's exclusive rounding (tax = floor((taxable
//     * rate + 5000) / 10000), round half up to the paise), so dev can run.
//
// Both are Provisional: the rate row is adviser-flagged.
//
// Place of supply (IGST Act s.12(2)(b), supply to an unregistered person):
// the buyer's state when the platform has an address on record for them,
// else the location of the supplier. The platform keeps NO buyer address
// today, so every caller passes "" and the place of supply is the
// supplier's state (CGST + SGST). ADVISER ITEM: confirm that a consumer
// subscription with no address on record may be invoiced at the supplier's
// location, and whether a billing state must be collected at checkout.

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
)

// EnvPlatformGSTIN is the platform's own GSTIN, the supplier of Premium.
const EnvPlatformGSTIN = "PREMIUM_PLATFORM_GSTIN"

// TaxCategory is the shared/gst category of a Premium pass.
const TaxCategory = gst.CategoryPlatformDigitalSubscription

var (
	// ErrPlatformGSTINRequired: production with Premium on and no GSTIN.
	ErrPlatformGSTINRequired = errors.New("PREMIUM_PLATFORM_GSTIN is required in production: the platform supplies Premium and is liable for its GST")
	// ErrPlatformGSTINInvalid: the configured GSTIN does not validate.
	ErrPlatformGSTINInvalid = errors.New("PREMIUM_PLATFORM_GSTIN is not a valid GSTIN")
	// ErrTaxUnavailable: the tax of a price could not be computed; nothing is sold.
	ErrTaxUnavailable = errors.New("PREMIUM_TAX_UNAVAILABLE")
)

// Breakdown is the GST on one pass. TaxablePaise + TaxPaise == TotalPaise
// and CGST + SGST + IGST == TaxPaise.
type Breakdown struct {
	TaxablePaise int64
	CGSTPaise    int64
	SGSTPaise    int64
	IGSTPaise    int64
	TaxPaise     int64
	TotalPaise   int64
	RateBP       int
	SAC          string
	// Provisional: the rate row awaits the adviser, or the split is the
	// dev-only estimate.
	Provisional bool
}

// Valid checks the identities a stored breakdown must satisfy.
func (b Breakdown) Valid() bool {
	return b.TaxablePaise > 0 && b.TaxPaise >= 0 && b.CGSTPaise >= 0 && b.SGSTPaise >= 0 && b.IGSTPaise >= 0 &&
		b.CGSTPaise+b.SGSTPaise+b.IGSTPaise == b.TaxPaise && b.TaxablePaise+b.TaxPaise == b.TotalPaise
}

// Tax computes the GST on a Premium price.
type Tax struct {
	table *gst.RateTable
	// gstin is the normalised platform GSTIN; "" selects the estimate.
	gstin string
	state string
}

// NewTax builds the computer. An empty gstin selects the dev-only estimate;
// a non-empty one must validate.
func NewTax(table *gst.RateTable, gstin string) (*Tax, error) {
	if table == nil {
		table = gst.DefaultRateTable()
	}
	t := &Tax{table: table}
	if raw := strings.TrimSpace(gstin); raw != "" {
		v, err := kyc.ValidateGSTIN(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPlatformGSTINInvalid, err)
		}
		t.gstin, t.state = v.Normalized, v.StateCode
	}
	return t, nil
}

// EstimatedTax is the dev-only computer (no GSTIN).
func EstimatedTax() *Tax {
	t, _ := NewTax(nil, "")
	return t
}

// IsProduction reports whether ENV names production.
func IsProduction(environment string) bool {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case "prod", "production":
		return true
	}
	return false
}

// TaxFromEnv reads PREMIUM_PLATFORM_GSTIN. Production refuses to start
// without it; anywhere, a GSTIN that does not validate refuses to start.
func TaxFromEnv(getenv func(string) string, environment string) (*Tax, error) {
	raw := strings.TrimSpace(getenv(EnvPlatformGSTIN))
	if raw == "" && IsProduction(environment) {
		return nil, ErrPlatformGSTINRequired
	}
	return NewTax(nil, raw)
}

// Computed reports whether gst.Compute (a configured GSTIN) is used.
func (t *Tax) Computed() bool { return t != nil && t.gstin != "" }

// Compute answers the GST on a GST-exclusive price at `at`. buyerState is
// the buyer's two-digit GST state code when the platform has their address
// on record, else "" (the place of supply is then the supplier's state).
func (t *Tax) Compute(taxablePaise int64, buyerState string, at time.Time) (Breakdown, error) {
	if t == nil {
		return Breakdown{}, fmt.Errorf("%w: no tax computer", ErrTaxUnavailable)
	}
	if taxablePaise <= 0 {
		return Breakdown{}, fmt.Errorf("%w: price %d", ErrTaxUnavailable, taxablePaise)
	}
	buyerState = strings.TrimSpace(buyerState)
	if buyerState != "" && !kyc.IsValidGSTStateCode(buyerState) {
		return Breakdown{}, fmt.Errorf("%w: %v", ErrTaxUnavailable, gst.ErrInvalidPlaceOfSupply)
	}
	var b Breakdown
	var err error
	if t.gstin != "" {
		b, err = t.compute(taxablePaise, buyerState, at)
	} else {
		b, err = t.estimate(taxablePaise, buyerState, at)
	}
	if err != nil {
		return Breakdown{}, fmt.Errorf("%w: %v", ErrTaxUnavailable, err)
	}
	if !b.Valid() || b.TaxablePaise != taxablePaise {
		return Breakdown{}, fmt.Errorf("%w: breakdown does not add up", ErrTaxUnavailable)
	}
	return b, nil
}

func (t *Tax) compute(taxable int64, buyerState string, at time.Time) (Breakdown, error) {
	pos := t.state
	if buyerState != "" {
		pos = buyerState
	}
	res, err := gst.Compute(t.table, gst.Input{
		Mode:               gst.ModeExclusive,
		InvoiceDate:        at,
		Platform:           gst.Party{GSTIN: t.gstin},
		PlaceOfSupplyState: pos,
		Lines:              []gst.Line{{Ref: "premium", Category: TaxCategory, Amount: gst.Paise(taxable)}},
	})
	if err != nil {
		return Breakdown{}, err
	}
	l := res.Lines[0]
	return Breakdown{
		TaxablePaise: int64(l.Taxable), CGSTPaise: int64(l.CGST), SGSTPaise: int64(l.SGST), IGSTPaise: int64(l.IGST),
		TaxPaise: int64(l.Tax), TotalPaise: int64(l.Gross), RateBP: int(l.RateBP), SAC: l.SAC,
		Provisional: res.NeedsAdviserConfirmation,
	}, nil
}

// estimate is the dev-only path: no supplier GSTIN, so no supplier state.
// The split is intra-state unless a buyer state is known (it never is yet).
func (t *Tax) estimate(taxable int64, buyerState string, at time.Time) (Breakdown, error) {
	row, err := t.table.Lookup(TaxCategory, at)
	if err != nil {
		return Breakdown{}, err
	}
	tax := TaxOnExclusive(taxable, int(row.RateBP))
	b := Breakdown{TaxablePaise: taxable, TaxPaise: tax, TotalPaise: taxable + tax, RateBP: int(row.RateBP), SAC: row.SAC,
		Provisional: true}
	_ = buyerState // no supplier state to compare with: intra-state
	b.CGSTPaise = tax / 2
	b.SGSTPaise = tax - b.CGSTPaise
	return b, nil
}

// TaxOnExclusive is shared/gst's exclusive rounding: floor((taxable * rate +
// 5000) / 10000), half up to the paise.
func TaxOnExclusive(taxable int64, rateBP int) int64 {
	if rateBP == 0 || taxable == 0 {
		return 0
	}
	num := new(big.Int).Mul(big.NewInt(taxable), big.NewInt(int64(rateBP)))
	num.Add(num, big.NewInt(5000))
	return num.Quo(num, big.NewInt(10000)).Int64()
}
