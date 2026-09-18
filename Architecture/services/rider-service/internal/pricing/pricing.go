// Package pricing is the Mopedu fare engine: pure functions over integer
// paise and basis points, with no database or clock of their own.
//
// The service layer gathers the inputs (fare rule in paise, route, the
// applicable fare window, the demand ratio, a validated coupon, waiting time,
// outstanding fees) and calls Compute; the result is the itemised Breakdown
// that is stored on the quote snapshot and, at completion, on the ride.
//
// Arithmetic rules:
//   - money is int64 paise, rates are basis points (10000 = 100%);
//   - every product is divided last, truncating toward zero, so a component
//     never rounds up against the customer;
//   - tax is computed by a TaxComputer (shared/gst in production, a flat
//     table in tests) and split into lines with largest-remainder allocation
//     (gst.Allocate), so lines always sum to the total.
package pricing

import (
	"errors"
	"fmt"
	"time"
)

// FarePolicyVersion is stamped on every breakdown so a stored quote can be
// told apart from one priced by an older engine.
const FarePolicyVersion = 2

// TaxNote is carried on every breakdown, like Feast's carts: the rates are
// the engineering team's reading of the law until a tax adviser confirms.
const TaxNote = "adviser confirmation pending"

// Surge reasons recorded on a quote.
const (
	SurgeNone       = "none"
	SurgePeakHours  = "peak_hours"
	SurgeHighDemand = "high_demand"
)

// Tax line refs, stable because the Android receipt keys on them.
const (
	RefRideFare        = "ride_fare"
	RefPlatformFee     = "platform_fee"
	RefWaitingCharge   = "waiting_charge"
	RefCancellationFee = "cancellation_fee"
	RefOutstanding     = "previous_cancellation_fee"
)

// Errors a caller can test for.
var (
	ErrInvalidInput = errors.New("pricing: invalid input")
	ErrNoTax        = errors.New("pricing: no tax computer")
)

// FareRule is the integer view of one rider_fare_rules row.
type FareRule struct {
	BasePaise             int64
	PerKMPaise            int64
	PerMinutePaise        int64
	MinimumPaise          int64
	PlatformFeePaise      int64
	CancellationFeePaise  int64
	WaitingFreeMinutes    int
	WaitingPerMinutePaise int64
	CancelFreeSeconds     int
}

// Input is one fare computation.
type Input struct {
	Rule            FareRule
	DistanceMeters  int
	DurationSeconds int

	// SurgeBPS is the effective extra over the base (2500 = x1.25); the
	// reason and window name are recorded as-is.
	SurgeBPS    int64
	SurgeReason string
	WindowName  string

	// Coupon is a validated coupon, or nil. Its discount reduces the taxable
	// ride fare (never the platform fee) and never takes the ride fare below
	// the rule's minimum.
	Coupon *Coupon

	// WaitingSeconds is the time the partner waited at pickup (arrived_at to
	// started_at). Only the minutes beyond the rule's free window are charged.
	WaitingSeconds int

	// OutstandingPaise is the sum of the customer's pending cancellation fees
	// from earlier rides, charged on this ride as a separate taxed line.
	OutstandingPaise int64
	OutstandingIDs   []string

	// RideFareCapPaise, when > 0, caps the ride fare after surge (the final
	// fare at completion is never more than twice the quoted ride fare).
	RideFareCapPaise int64

	Tax         TaxComputer
	InvoiceDate time.Time
}

// TaxLineResult is one taxed line of a breakdown.
type TaxLineResult struct {
	Ref          string `json:"ref"`
	Category     string `json:"category"`
	SAC          string `json:"sac,omitempty"`
	RateBPS      int64  `json:"rate_bps"`
	TaxablePaise int64  `json:"taxable_paise"`
	TaxPaise     int64  `json:"tax_paise"`
	GrossPaise   int64  `json:"gross_paise"`
}

// Breakdown is the itemised fare. The legacy field names (base_paise,
// distance_paise, time_paise, platform_fee_paise, tax_paise, toll_paise,
// surge_basis_points) are kept so older clients keep reading the quote.
type Breakdown struct {
	BasePaise         int64 `json:"base_paise"`
	DistancePaise     int64 `json:"distance_paise"`
	TimePaise         int64 `json:"time_paise"`
	SurgePaise        int64 `json:"surge_paise"`
	MinimumTopUpPaise int64 `json:"minimum_top_up_paise"`
	// RideFarePaise = base + distance + time + surge (+ minimum top-up),
	// before the coupon; after the cap when one applies.
	RideFarePaise int64 `json:"ride_fare_paise"`

	DiscountPaise int64  `json:"discount_paise"`
	CouponCode    string `json:"coupon_code,omitempty"`
	CouponID      string `json:"coupon_id,omitempty"`
	// TaxableRideFarePaise = ride fare - discount.
	TaxableRideFarePaise int64 `json:"taxable_ride_fare_paise"`

	PlatformFeePaise   int64    `json:"platform_fee_paise"`
	WaitingMinutes     int      `json:"waiting_minutes"`
	WaitingChargePaise int64    `json:"waiting_charge_paise"`
	OutstandingPaise   int64    `json:"outstanding_paise"`
	OutstandingIDs     []string `json:"outstanding_ids,omitempty"`
	TollPaise          int64    `json:"toll_paise"`

	TaxPaise int64           `json:"tax_paise"`
	TaxLines []TaxLineResult `json:"tax_lines"`
	TaxNote  string          `json:"tax_note"`

	SurgeBasisPoints int64  `json:"surge_basis_points"`
	SurgeReason      string `json:"surge_reason"`
	WindowName       string `json:"window_name,omitempty"`

	// DistanceMeters / DurationSeconds are what was priced: the route at quote
	// time, or the tracked route when completion recomputed the fare.
	DistanceMeters  int `json:"distance_meters"`
	DurationSeconds int `json:"duration_seconds"`

	TotalPaise int64 `json:"total_paise"`

	RecomputedFromTrack bool `json:"recomputed_from_track,omitempty"`
	CapApplied          bool `json:"cap_applied,omitempty"`
	FarePolicyVersion   int  `json:"fare_policy_version"`
}

// Compute prices one ride. It never returns a negative component.
func Compute(in Input) (Breakdown, error) {
	if in.DistanceMeters < 0 || in.DurationSeconds < 0 || in.SurgeBPS < 0 || in.WaitingSeconds < 0 || in.OutstandingPaise < 0 {
		return Breakdown{}, fmt.Errorf("%w: negative distance, duration, surge, waiting or outstanding", ErrInvalidInput)
	}
	r := in.Rule
	if r.BasePaise < 0 || r.PerKMPaise < 0 || r.PerMinutePaise < 0 || r.MinimumPaise < 0 || r.PlatformFeePaise < 0 {
		return Breakdown{}, fmt.Errorf("%w: negative fare rule component", ErrInvalidInput)
	}
	if in.Tax == nil {
		return Breakdown{}, ErrNoTax
	}

	b := Breakdown{
		BasePaise:         r.BasePaise,
		DistancePaise:     int64(in.DistanceMeters) * r.PerKMPaise / 1000,
		TimePaise:         int64(in.DurationSeconds) * r.PerMinutePaise / 60,
		PlatformFeePaise:  r.PlatformFeePaise,
		SurgeBasisPoints:  in.SurgeBPS,
		SurgeReason:       in.SurgeReason,
		WindowName:        in.WindowName,
		DistanceMeters:    in.DistanceMeters,
		DurationSeconds:   in.DurationSeconds,
		OutstandingPaise:  in.OutstandingPaise,
		OutstandingIDs:    append([]string(nil), in.OutstandingIDs...),
		TaxNote:           TaxNote,
		FarePolicyVersion: FarePolicyVersion,
	}
	if b.SurgeReason == "" {
		b.SurgeReason = SurgeNone
	}
	pre := b.BasePaise + b.DistancePaise + b.TimePaise
	b.SurgePaise = pre * in.SurgeBPS / 10000
	b.RideFarePaise = pre + b.SurgePaise
	if b.RideFarePaise < r.MinimumPaise {
		b.MinimumTopUpPaise = r.MinimumPaise - b.RideFarePaise
		b.RideFarePaise = r.MinimumPaise
	}
	if in.RideFareCapPaise > 0 && b.RideFarePaise > in.RideFareCapPaise {
		b.RideFarePaise = in.RideFareCapPaise
		b.CapApplied = true
	}

	if in.Coupon != nil {
		d := in.Coupon.DiscountFor(b.RideFarePaise)
		if room := b.RideFarePaise - r.MinimumPaise; d > room {
			d = room
		}
		if d < 0 {
			d = 0
		}
		b.DiscountPaise = d
		b.CouponCode = in.Coupon.Code
		b.CouponID = in.Coupon.ID
	}
	b.TaxableRideFarePaise = b.RideFarePaise - b.DiscountPaise

	b.WaitingMinutes, b.WaitingChargePaise = WaitingCharge(r, in.WaitingSeconds)

	lines := []TaxLine{
		{Ref: RefRideFare, Kind: KindPassengerTransport, AmountPaise: b.TaxableRideFarePaise},
	}
	if b.PlatformFeePaise > 0 {
		lines = append(lines, TaxLine{Ref: RefPlatformFee, Kind: KindPlatformFee, AmountPaise: b.PlatformFeePaise})
	}
	if b.WaitingChargePaise > 0 {
		lines = append(lines, TaxLine{Ref: RefWaitingCharge, Kind: KindPassengerTransport, AmountPaise: b.WaitingChargePaise})
	}
	if b.OutstandingPaise > 0 {
		lines = append(lines, TaxLine{Ref: RefOutstanding, Kind: KindPassengerTransport, AmountPaise: b.OutstandingPaise})
	}
	taxed, err := in.Tax.ComputeTax(lines, in.InvoiceDate)
	if err != nil {
		return Breakdown{}, err
	}
	b.TaxLines = taxed.Lines
	b.TaxPaise = taxed.TotalTaxPaise
	b.TotalPaise = b.TaxableRideFarePaise + b.PlatformFeePaise + b.WaitingChargePaise + b.OutstandingPaise + b.TaxPaise
	return b, nil
}

// CancellationBreakdown prices a cancellation fee on its own (one taxed line)
// so the cancelled ride's receipt itemises it like any other charge.
func CancellationBreakdown(feePaise int64, tax TaxComputer, on time.Time) (Breakdown, error) {
	if feePaise < 0 {
		return Breakdown{}, fmt.Errorf("%w: negative cancellation fee", ErrInvalidInput)
	}
	b := Breakdown{TaxNote: TaxNote, SurgeReason: SurgeNone, FarePolicyVersion: FarePolicyVersion}
	if feePaise == 0 || tax == nil {
		return b, nil
	}
	taxed, err := tax.ComputeTax([]TaxLine{{Ref: RefCancellationFee, Kind: KindPassengerTransport, AmountPaise: feePaise}}, on)
	if err != nil {
		return Breakdown{}, err
	}
	b.TaxLines = taxed.Lines
	b.TaxPaise = taxed.TotalTaxPaise
	b.TotalPaise = feePaise + taxed.TotalTaxPaise
	return b, nil
}
