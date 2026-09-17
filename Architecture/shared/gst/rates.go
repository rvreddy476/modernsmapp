package gst

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Category is what is being supplied on a line.
type Category string

const (
	// Restaurant service other than in specified premises (dine-in, takeaway,
	// door delivery). Notified under s.9(5).
	CategoryRestaurantStandalone Category = "RESTAURANT_STANDALONE"
	// Restaurant service in specified premises (hotel premises meeting the
	// tariff test). Excluded from s.9(5); the restaurant stays liable.
	CategoryRestaurantSpecifiedPremises Category = "RESTAURANT_SPECIFIED_PREMISES"
	// Cloud kitchen / takeaway-only kitchen, treated as restaurant service.
	CategoryCloudKitchenTakeaway Category = "CLOUD_KITCHEN_TAKEAWAY"
	// Outdoor catering other than in specified premises.
	CategoryOutdoorCatering Category = "OUTDOOR_CATERING"
	// Outdoor catering in specified premises.
	CategoryOutdoorCateringSpecifiedPremises Category = "OUTDOOR_CATERING_SPECIFIED_PREMISES"
	// The platform's own platform / convenience fee.
	CategoryPlatformFee Category = "PLATFORM_FEE"
	// Delivery supplied by the platform itself.
	CategoryDeliveryFeePlatform Category = "DELIVERY_FEE_PLATFORM"
	// Local delivery by a delivery partner, supplied through the platform.
	CategoryDeliveryFeePartnerViaECO Category = "DELIVERY_FEE_PARTNER_VIA_ECO"
	// Passenger transport by motorcycle, auto-rickshaw or motor cab (Mopedu
	// ride fare), supplied by the driver through the platform. Notified under
	// s.9(5). The platform's own convenience fee on a ride is
	// CategoryPlatformFee, as for food.
	CategoryPassengerTransportViaECO Category = "PASSENGER_TRANSPORT_VIA_ECO"
)

// SupplierRole is who makes the supply (and who is liable, when not s.9(5)).
type SupplierRole string

const (
	SupplierRestaurant      SupplierRole = "RESTAURANT"
	SupplierPlatform        SupplierRole = "PLATFORM"
	SupplierDeliveryPartner SupplierRole = "DELIVERY_PARTNER"
	// SupplierDriver is the ride-hailing driver (motorcycle, auto or cab)
	// supplying passenger transport through the platform.
	SupplierDriver SupplierRole = "DRIVER"
)

// Liability is who pays the tax to the government.
type Liability string

const (
	// LiabilitySupplier: the supplier collects and deposits the tax.
	LiabilitySupplier Liability = "SUPPLIER"
	// LiabilityECOSection95: CGST Act s.9(5) makes the electronic commerce
	// operator liable as if it were the supplier.
	LiabilityECOSection95 Liability = "ECO_SECTION_9_5"
)

var (
	ErrUnknownCategory         = errors.New("GST_UNKNOWN_CATEGORY")
	ErrNoRateInEffect          = errors.New("GST_NO_RATE_IN_EFFECT")
	ErrInvalidRateRow          = errors.New("GST_INVALID_RATE_ROW")
	ErrZeroRateNotExplicit     = errors.New("GST_ZERO_RATE_NOT_EXPLICIT")
	ErrPartyState              = errors.New("GST_PARTY_STATE")
	ErrLiablePartyUnregistered = errors.New("GST_LIABLE_PARTY_UNREGISTERED")
	ErrUnsupportedSupply       = errors.New("GST_UNSUPPORTED_SUPPLY")
	ErrNegativeAmount          = errors.New("GST_NEGATIVE_AMOUNT")
	ErrDiscountExceedsSubtotal = errors.New("GST_DISCOUNT_EXCEEDS_SUBTOTAL")
	ErrNoLines                 = errors.New("GST_NO_LINES")

	// Added during build (not in the design's sentinel list).
	ErrNoRateTable          = errors.New("GST_NO_RATE_TABLE")
	ErrInvalidMode          = errors.New("GST_INVALID_MODE")
	ErrInvalidPlaceOfSupply = errors.New("GST_INVALID_PLACE_OF_SUPPLY")
	ErrAmountOutOfRange     = errors.New("GST_AMOUNT_OUT_OF_RANGE")
	// ErrInternalInvariant means a programming error: the computed numbers
	// failed an identity they must satisfy. It must never reach a payment.
	ErrInternalInvariant = errors.New("GST_INTERNAL_INVARIANT")
)

// ist is India Standard Time. India has no daylight saving, so a fixed zone
// is exact and needs no tzdata.
var ist = time.FixedZone("IST", 5*3600+30*60)

// istDay is the IST calendar date of t as yyyymmdd.
func istDay(t time.Time) int {
	t = t.In(ist)
	y, m, d := t.Date()
	return y*10000 + int(m)*100 + d
}

// RateRow is one effective-dated rate for a category.
type RateRow struct {
	Category Category
	Supplier SupplierRole
	// ECOSection95: the category is notified under s.9(5), so a supply made
	// through the ECO makes the ECO liable.
	ECOSection95 bool
	RateBP       RateBP
	ITCAvailable bool
	SAC          string
	// EffectiveFrom is read as an IST calendar date; the stored row is
	// normalised to IST midnight.
	EffectiveFrom time.Time
	// ExplicitZeroRate must be set for a 0 bp row. A zero rate is never a
	// default: an unset rate is a data error, not a tax position.
	ExplicitZeroRate         bool
	NeedsAdviserConfirmation bool
	Note                     string
}

type rateEntry struct {
	day int
	row RateRow
}

// RateTable is an immutable, validated set of rate rows.
type RateTable struct {
	byCategory map[Category][]rateEntry
	rows       []RateRow
}

func validSupplier(s SupplierRole) bool {
	return s == SupplierRestaurant || s == SupplierPlatform || s == SupplierDeliveryPartner || s == SupplierDriver
}

func sixDigits(s string) bool {
	if len(s) != 6 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// NewRateTable validates rows and returns a table holding a sorted copy.
func NewRateTable(rows []RateRow) (*RateTable, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: table has no rows", ErrInvalidRateRow)
	}
	t := &RateTable{byCategory: map[Category][]rateEntry{}}
	type key struct {
		c   Category
		day int
	}
	seen := map[key]bool{}
	for i, r := range rows {
		if strings.TrimSpace(string(r.Category)) == "" {
			return nil, fmt.Errorf("%w: row %d: empty category", ErrInvalidRateRow, i)
		}
		if !validSupplier(r.Supplier) {
			return nil, fmt.Errorf("%w: row %d: unknown supplier role", ErrInvalidRateRow, i)
		}
		if r.RateBP < 0 || r.RateBP > bpDenominator {
			return nil, fmt.Errorf("%w: row %d: rate outside 0..10000 bp", ErrInvalidRateRow, i)
		}
		if r.RateBP == 0 && !r.ExplicitZeroRate {
			return nil, fmt.Errorf("%w: row %d: 0 bp without ExplicitZeroRate", ErrZeroRateNotExplicit, i)
		}
		if r.ExplicitZeroRate && r.RateBP != 0 {
			return nil, fmt.Errorf("%w: row %d: ExplicitZeroRate on a non-zero rate", ErrInvalidRateRow, i)
		}
		if !sixDigits(r.SAC) {
			return nil, fmt.Errorf("%w: row %d: SAC must be 6 digits", ErrInvalidRateRow, i)
		}
		if r.EffectiveFrom.IsZero() {
			return nil, fmt.Errorf("%w: row %d: EffectiveFrom is zero", ErrInvalidRateRow, i)
		}
		if r.Supplier == SupplierPlatform && r.ECOSection95 {
			return nil, fmt.Errorf("%w: row %d: the platform's own supply cannot shift liability under s.9(5)", ErrInvalidRateRow, i)
		}
		day := istDay(r.EffectiveFrom)
		if seen[key{r.Category, day}] {
			return nil, fmt.Errorf("%w: row %d: duplicate category and effective date", ErrInvalidRateRow, i)
		}
		seen[key{r.Category, day}] = true
		r.EffectiveFrom = time.Date(day/10000, time.Month(day/100%100), day%100, 0, 0, 0, 0, ist)
		t.byCategory[r.Category] = append(t.byCategory[r.Category], rateEntry{day: day, row: r})
		t.rows = append(t.rows, r)
	}
	for c := range t.byCategory {
		es := t.byCategory[c]
		sort.Slice(es, func(a, b int) bool { return es[a].day < es[b].day })
	}
	sort.SliceStable(t.rows, func(a, b int) bool {
		if t.rows[a].Category != t.rows[b].Category {
			return t.rows[a].Category < t.rows[b].Category
		}
		return t.rows[a].EffectiveFrom.Before(t.rows[b].EffectiveFrom)
	})
	return t, nil
}

// Lookup returns the latest row for c whose EffectiveFrom IST date is on or
// before the IST date of on.
func (t *RateTable) Lookup(c Category, on time.Time) (RateRow, error) {
	if t == nil {
		return RateRow{}, ErrNoRateTable
	}
	entries, ok := t.byCategory[c]
	if !ok {
		return RateRow{}, fmt.Errorf("%w: %s", ErrUnknownCategory, c)
	}
	day := istDay(on)
	found := false
	var best RateRow
	for _, e := range entries {
		if e.day <= day {
			best = e.row
			found = true
		}
	}
	if !found {
		return RateRow{}, fmt.Errorf("%w: %s", ErrNoRateInEffect, c)
	}
	return best, nil
}

// Rows returns a copy of the table's rows, sorted by category then date.
func (t *RateTable) Rows() []RateRow {
	if t == nil {
		return nil
	}
	return append([]RateRow(nil), t.rows...)
}

// rateRationalisationDate is 22 September 2025 (IST), the date the GST rate
// rationalisation took effect. The seeded rows start here; an invoice dated
// earlier gets ErrNoRateInEffect by design rather than a guessed rate.
var rateRationalisationDate = time.Date(2025, time.September, 22, 0, 0, 0, 0, ist)

const adviserNote = " ADVISER TO CONFIRM before any invoice or return relies on this row."

// DefaultRateTable returns the seeded table. EVERY row carries
// NeedsAdviserConfirmation = true: these are the engineering team's reading
// of the law, not a tax adviser's opinion.
func DefaultRateTable() *RateTable {
	d := rateRationalisationDate
	rows := []RateRow{
		{Category: CategoryRestaurantStandalone, Supplier: SupplierRestaurant, ECOSection95: true, RateBP: 500, ITCAvailable: false, SAC: "996331", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Restaurant service outside specified premises: 5% without ITC; notified under s.9(5), so the ECO is liable when supplied through it." + adviserNote},
		{Category: CategoryRestaurantSpecifiedPremises, Supplier: SupplierRestaurant, ECOSection95: false, RateBP: 1800, ITCAvailable: true, SAC: "996331", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Restaurant service in specified premises: 18% with ITC; excluded from s.9(5), restaurant liable; through an ECO the ECO collects TCS under s.52 (amount not computed here)." + adviserNote},
		{Category: CategoryCloudKitchenTakeaway, Supplier: SupplierRestaurant, ECOSection95: true, RateBP: 500, ITCAvailable: false, SAC: "996331", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Cloud kitchen / takeaway treated as restaurant service: 5% without ITC, within s.9(5)." + adviserNote},
		{Category: CategoryOutdoorCatering, Supplier: SupplierRestaurant, ECOSection95: false, RateBP: 500, ITCAvailable: false, SAC: "996334", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Outdoor catering outside specified premises: 5% without ITC; treated as outside s.9(5), caterer liable." + adviserNote},
		{Category: CategoryOutdoorCateringSpecifiedPremises, Supplier: SupplierRestaurant, ECOSection95: false, RateBP: 1800, ITCAvailable: true, SAC: "996334", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Outdoor catering in specified premises: 18% with ITC; caterer liable." + adviserNote},
		{Category: CategoryPlatformFee, Supplier: SupplierPlatform, RateBP: 1800, ITCAvailable: true, SAC: "998599", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Platform / convenience fee: the platform's own service at 18%; SAC 998599 is the team's choice." + adviserNote},
		{Category: CategoryDeliveryFeePlatform, Supplier: SupplierPlatform, RateBP: 1800, ITCAvailable: true, SAC: "996813", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Delivery supplied by the platform itself: local delivery service at 18%." + adviserNote},
		{Category: CategoryDeliveryFeePartnerViaECO, Supplier: SupplierDeliveryPartner, ECOSection95: true, RateBP: 1800, ITCAvailable: false, SAC: "996813", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Local delivery by an unregistered delivery partner through the ECO: believed notified under s.9(5) at 18% from 22 Sep 2025; registered partners not modelled." + adviserNote},
		{Category: CategoryPassengerTransportViaECO, Supplier: SupplierDriver, ECOSection95: true, RateBP: 500, ITCAvailable: false, SAC: "996412", EffectiveFrom: d, NeedsAdviserConfirmation: true,
			Note: "Passenger transport by motorcycle, auto-rickshaw or motor cab through the ECO (Mopedu ride fare): 5% without ITC, notified under s.9(5), so the ECO is liable; the 12%-with-ITC option for cabs is not modelled." + adviserNote},
	}
	t, err := NewRateTable(rows)
	if err != nil {
		panic("gst: seeded rate table is invalid: " + err.Error())
	}
	return t
}
