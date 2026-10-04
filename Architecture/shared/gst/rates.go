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

	// Doorstep (home services), supplied by a gig service professional
	// through the platform. Each family has two categories and the CALLER
	// picks one from the professional's registration, exactly as it picks
	// "specified premises" for food:
	//
	//   <FAMILY>_VIA_ECO     the professional has no GSTIN. Housekeeping-type
	//                        services are notified under s.9(5) except where the
	//                        supplier is liable for registration under s.22(1),
	//                        so through the platform the ECO is liable. Compute
	//                        refuses a _VIA_ECO line when the professional HAS a
	//                        GSTIN (ErrUnsupportedSupply): that line belongs on
	//                        _REGISTERED.
	//   <FAMILY>_REGISTERED  the professional is registered and liable; the line
	//                        needs the professional's GSTIN, else
	//                        ErrLiablePartyUnregistered. Through the platform it
	//                        carries the ECOCollectsTCS marker (s.52).
	//
	// Beauty / salon is NOT a s.9(5) service, so it has only _REGISTERED. An
	// unregistered beautician's supply cannot be computed by this package; how
	// it is invoiced is an open adviser question (docs/DOORSTEP-TAX-ADVISER-REVIEW.md).
	CategoryHomeCleaningViaECO           Category = "HOME_CLEANING_VIA_ECO"
	CategoryHomeCleaningRegistered       Category = "HOME_CLEANING_REGISTERED"
	CategoryPestControlViaECO            Category = "PEST_CONTROL_VIA_ECO"
	CategoryPestControlRegistered        Category = "PEST_CONTROL_REGISTERED"
	CategoryApplianceRepairViaECO        Category = "APPLIANCE_REPAIR_VIA_ECO"
	CategoryApplianceRepairRegistered    Category = "APPLIANCE_REPAIR_REGISTERED"
	CategoryInstallationRepairViaECO     Category = "INSTALLATION_REPAIR_VIA_ECO"
	CategoryInstallationRepairRegistered Category = "INSTALLATION_REPAIR_REGISTERED"
	CategoryPaintingViaECO               Category = "PAINTING_VIA_ECO"
	CategoryPaintingRegistered           Category = "PAINTING_REGISTERED"
	CategoryBeautySalonRegistered        Category = "BEAUTY_SALON_REGISTERED"

	// B1 families (4 Oct 2026). Only construction (small masonry and tiling
	// repairs, the same building trades as plumbing and carpentry) is read
	// as s.9(5) housekeeping and has a _VIA_ECO category. Car wash (a
	// vehicle, not the household), home staffing (domestic workers by the
	// hour or month: labour, possibly employment), packers and movers
	// (goods transport), photography and fitness/wellness (yoga) are NOT
	// read as notified under s.9(5), so, like beauty, they have _REGISTERED
	// only and an unregistered professional's supply cannot be computed
	// (docs/DOORSTEP-TAX-ADVISER-REVIEW.md, questions 23-31).
	CategoryCarCareRegistered         Category = "CAR_CARE_REGISTERED"
	CategoryHomeStaffingRegistered    Category = "HOME_STAFFING_REGISTERED"
	CategoryRelocationRegistered      Category = "RELOCATION_REGISTERED"
	CategoryPhotographyRegistered     Category = "PHOTOGRAPHY_REGISTERED"
	CategoryFitnessWellnessRegistered Category = "FITNESS_WELLNESS_REGISTERED"
	CategoryConstructionViaECO        Category = "CONSTRUCTION_VIA_ECO"
	CategoryConstructionRegistered    Category = "CONSTRUCTION_REGISTERED"
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
	// SupplierServiceProfessional is the Doorstep (home services) gig
	// professional — cleaner, technician, painter, beautician — supplying
	// through the platform. Unlike the delivery partner and the driver it is
	// modelled OUTSIDE s.9(5) too: a registered professional is liable for
	// its own supply (the _REGISTERED categories).
	SupplierServiceProfessional SupplierRole = "SERVICE_PROFESSIONAL"
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
	return s == SupplierRestaurant || s == SupplierPlatform || s == SupplierDeliveryPartner || s == SupplierDriver ||
		s == SupplierServiceProfessional
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
	rows = append(rows, doorstepRows(d)...)
	t, err := NewRateTable(rows)
	if err != nil {
		panic("gst: seeded rate table is invalid: " + err.Error())
	}
	return t
}

// doorstepRows are the Doorstep (home services) rows: one _VIA_ECO and one
// _REGISTERED row per family, plus beauty (registered only).
//
// SAC choices (the team's reading; the adviser confirms each):
//
//   - cleaning 998533 "general cleaning services" and pest control 998531
//     "disinfecting and exterminating services", both in heading 9985
//     (support services);
//   - appliance repair (AC, RO, washing machine, refrigerator) 998715
//     "maintenance and repair of electrical household appliances", heading
//     9987;
//   - installation and repair (electrician, plumber, carpenter) 995469 "repair
//     services related to installations", heading 9954. 9954 was chosen over
//     9987 because the subject of these jobs is a fixture of the building —
//     wiring, pipes, fittings, woodwork — which the SAC scheme classes as
//     installation and building-completion services, whereas 9987 covers
//     repair of goods (machinery, appliances). Narrower alternatives exist per
//     trade (995461 electrical, 995462 plumbing, 995476 carpentry); one
//     family-level code is used until the adviser says invoices need the
//     trade-level code;
//   - painting 995473 "painting services", heading 9954 (building
//     completion and finishing);
//   - beauty / salon at home 999722 "cosmetic treatment, manicuring and
//     pedicuring services", heading 9997; men's haircuts are strictly 999721
//     "hairdressing and barbers services".
//
// Rates: 18% (the residual services rate) for every family except beauty,
// which the 22 Sep 2025 rationalisation moved to 5% without ITC. _VIA_ECO rows
// carry ITCAvailable = false: the professional is unregistered and the ECO
// discharges a s.9(5) liability in cash.
func doorstepRows(d time.Time) []RateRow {
	const viaECO = " Through the platform the ECO is liable under s.9(5) (housekeeping services, Notification 17/2017-CT(Rate) as amended), only where the professional is NOT liable to register under s.22(1); a professional with a GSTIN must use the _REGISTERED category."
	const registered = " The registered professional is liable and must supply its GSTIN; through the platform the ECO collects TCS under s.52 (marker only)."
	type fam struct {
		via, reg Category
		sac      string
		what     string
	}
	fams := []fam{
		{CategoryHomeCleaningViaECO, CategoryHomeCleaningRegistered, "998533", "Home cleaning (bathroom, kitchen, full home, sofa/carpet), SAC 998533 general cleaning, 18%."},
		{CategoryPestControlViaECO, CategoryPestControlRegistered, "998531", "Pest control, SAC 998531 disinfecting and exterminating, 18%; whether pest control is 'housekeeping' under s.9(5) is for the adviser."},
		{CategoryApplianceRepairViaECO, CategoryApplianceRepairRegistered, "998715", "Appliance repair (AC, RO, washing machine), SAC 998715 repair of electrical household appliances, 18%; whether appliance repair is 'housekeeping' under s.9(5) is for the adviser; parts are not modelled."},
		{CategoryInstallationRepairViaECO, CategoryInstallationRepairRegistered, "995469", "Electrician, plumber, carpenter, SAC 995469 repair services related to installations (9954 chosen over 9987: the subject is a building fixture), 18%; parts are not modelled."},
		{CategoryPaintingViaECO, CategoryPaintingRegistered, "995473", "Painting, SAC 995473 painting services, 18%; paint supplied by the professional may make this a works contract, which is not modelled."},
	}
	rows := make([]RateRow, 0, 2*len(fams)+1)
	for _, f := range fams {
		rows = append(rows,
			RateRow{Category: f.via, Supplier: SupplierServiceProfessional, ECOSection95: true, RateBP: 1800, ITCAvailable: false, SAC: f.sac, EffectiveFrom: d, NeedsAdviserConfirmation: true,
				Note: f.what + viaECO + adviserNote},
			RateRow{Category: f.reg, Supplier: SupplierServiceProfessional, ECOSection95: false, RateBP: 1800, ITCAvailable: true, SAC: f.sac, EffectiveFrom: d, NeedsAdviserConfirmation: true,
				Note: f.what + registered + adviserNote},
		)
	}
	rows = append(rows, RateRow{Category: CategoryBeautySalonRegistered, Supplier: SupplierServiceProfessional, ECOSection95: false, RateBP: 500, ITCAvailable: false, SAC: "999722", EffectiveFrom: d, NeedsAdviserConfirmation: true,
		Note: "Salon at home (women and men), SAC 999722 cosmetic treatment (haircuts strictly 999721): beauty and physical well-being services at 5% without ITC from 22 Sep 2025; NOT notified under s.9(5), so only a registered professional's supply is computed." + registered + adviserNote})
	return append(rows, doorstepB1Rows(d)...)
}

// doorstepB1Rows are the families added with professionals' own prices (B1,
// 4 Oct 2026). Same effective date and adviser flag as every Doorstep row.
//
// s.9(5) reading (the adviser confirms each, questions 23-31):
//
//   - construction: masonry, tiling and small civil repairs at the customer's
//     home are building trades of the same kind as "plumbing, carpentering"
//     in the housekeeping entry, so both categories exist, as for painting
//     (which raises the same works-contract question when materials are
//     supplied);
//   - car care: a car wash at the customer's parking services a vehicle, not
//     the household: not read as housekeeping; registered only;
//   - home staffing: a cook, house help, nanny or driver engaged by the hour
//     or the month supplies labour (and may be the household's employee,
//     outside GST under Schedule III): not housekeeping as notified;
//     registered only;
//   - relocation: packers and movers supply transport of goods (a goods
//     transport agency when a consignment note is issued), not housekeeping;
//     registered only. 18% with ITC is the GTA forward-charge option from
//     22 Sep 2025; the exemption for a GTA's supply to an unregistered
//     individual and the 5% option are for the adviser;
//   - photography and fitness/wellness (yoga): personal services, not
//     housekeeping; registered only. Yoga is read as physical well-being,
//     moved to 5% without ITC on 22 Sep 2025 like beauty; if it is coaching
//     instead it is 18% under 9992.
//
// SAC: car wash 998714 (maintenance and repair of transport machinery and
// equipment, which includes washing and polishing of motor vehicles);
// domestic services 999800 (heading 9998, cooks, maids, nannies, chauffeurs
// working for households); GTA road transport 996791; event photography
// 998383 (portraits strictly 998381); physical well-being 999723; masonry
// 995457 (tiling strictly 995474).
func doorstepB1Rows(d time.Time) []RateRow {
	const viaECO = " Through the platform the ECO is liable under s.9(5) (housekeeping services, Notification 17/2017-CT(Rate) as amended), only where the professional is NOT liable to register under s.22(1); a professional with a GSTIN must use the _REGISTERED category."
	const registered = " The registered professional is liable and must supply its GSTIN; through the platform the ECO collects TCS under s.52 (marker only)."
	const notNotified = " Not read as notified under s.9(5), so only a registered professional's supply is computed; an unregistered professional below the threshold may be outside GST altogether (Notification 65/2017-CT)."
	row := func(c Category, eco bool, rate RateBP, itc bool, sac, note string) RateRow {
		return RateRow{Category: c, Supplier: SupplierServiceProfessional, ECOSection95: eco, RateBP: rate, ITCAvailable: itc, SAC: sac,
			EffectiveFrom: d, NeedsAdviserConfirmation: true, Note: note + adviserNote}
	}
	const construction = "Construction and masonry (masonry, tiling, small civil repairs at home), SAC 995457 masonry (tiling 995474), 18%; materials supplied by the professional may make this a works contract (s.2(119)), which is not modelled."
	return []RateRow{
		row(CategoryCarCareRegistered, false, 1800, true, "998714",
			"Car wash at the customer's parking, SAC 998714 maintenance and repair of motor vehicles (includes washing and polishing), 18%."+notNotified+registered),
		row(CategoryHomeStaffingRegistered, false, 1800, true, "999800",
			"Home staffing (cook, house help, nanny, driver; hourly or monthly), SAC 999800 domestic services, 18% residual rate; a monthly domestic worker may be the household's employee (Schedule III, outside GST)."+notNotified+registered),
		row(CategoryRelocationRegistered, false, 1800, true, "996791",
			"Packers and movers within the city, SAC 996791 goods transport agency (road), 18% with ITC (the GTA forward-charge option from 22 Sep 2025); a GTA's supply to an unregistered individual may be exempt and transport by a non-GTA is exempt, both for the adviser."+notNotified+registered),
		row(CategoryPhotographyRegistered, false, 1800, true, "998383",
			"Photography at home (events, portraits), SAC 998383 event photography (portraits 998381), 18%."+notNotified+registered),
		row(CategoryFitnessWellnessRegistered, false, 500, false, "999723",
			"Yoga trainer at home, SAC 999723 physical well-being services, read as within beauty and physical well-being at 5% without ITC from 22 Sep 2025 (18% if it is coaching under 9992)."+notNotified+registered),
		row(CategoryConstructionViaECO, true, 1800, false, "995457", construction+viaECO),
		row(CategoryConstructionRegistered, false, 1800, true, "995457", construction+registered),
	}
}
