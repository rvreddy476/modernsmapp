// Package tax splits Doorstep's GST-inclusive customer prices into taxable
// value and tax for a quote, on shared/gst.
//
// At quote time the professional is not known, so a quote uses the
// `<FAMILY>_VIA_ECO` category (an unregistered professional; the platform is
// liable under s.9(5)). The invoice split is recomputed at completion with the
// actual professional (A5): a professional with a GSTIN moves the lines to
// `<FAMILY>_REGISTERED`.
//
// Two paths, both from the shared rate table:
//
//   - computed: gst.Compute in inclusive mode, through the ECO, with the
//     platform GSTIN as the liable party. Used for every family with a
//     _VIA_ECO category when DOORSTEP_PLATFORM_GSTIN is configured (required
//     in production).
//   - estimated: the rate row's rate and SAC, the same floor split as
//     shared/gst (taxable = floor(gross*10000/(10000+rate)), tax = gross -
//     taxable), per line. Used for beauty/salon (registered only in shared/gst:
//     an unknown professional cannot be computed; adviser item) and, outside
//     production, when no platform GSTIN is configured.
//
// Every Doorstep rate row is adviser-flagged, so Result.Provisional is true
// until the adviser signs the rows off (docs/DOORSTEP-TAX-ADVISER-REVIEW.md).
package tax

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
)

// Note is shown with every quote.
const Note = "Prices include GST. The tax split is an estimate until the invoice is issued at completion."

// Families (doorstep.categories.family).
const (
	FamilyHomeCleaning       = "HOME_CLEANING"
	FamilyPestControl        = "PEST_CONTROL"
	FamilyApplianceRepair    = "APPLIANCE_REPAIR"
	FamilyInstallationRepair = "INSTALLATION_REPAIR"
	FamilyPainting           = "PAINTING"
	FamilyBeautySalon        = "BEAUTY_SALON"

	// B1 families (4 Oct 2026). shared/gst has no category for them yet:
	// the tax lane maps them (adviser-flagged). Until then they are not
	// Supported: their services are hidden from customers and cannot be
	// quoted (professionals may still declare the skills and submit prices).
	FamilyCarCare         = "CAR_CARE"
	FamilyHomeStaffing    = "HOME_STAFFING"
	FamilyRelocation      = "RELOCATION"
	FamilyPhotography     = "PHOTOGRAPHY"
	FamilyFitnessWellness = "FITNESS_WELLNESS"
	FamilyConstruction    = "CONSTRUCTION"
)

// Families is every doorstep.categories.family value (migration 005).
var Families = []string{FamilyHomeCleaning, FamilyPestControl, FamilyApplianceRepair, FamilyInstallationRepair,
	FamilyPainting, FamilyBeautySalon, FamilyCarCare, FamilyHomeStaffing, FamilyRelocation, FamilyPhotography,
	FamilyFitnessWellness, FamilyConstruction}

// Supported reports whether quotes of a family can be taxed (shared/gst has
// its category). An unsupported family's services are not offered to
// customers: fail closed rather than invent a rate.
func Supported(family string) bool {
	_, err := QuoteCategory(family)
	return err == nil
}

// RegisteredOnly keeps families without an adviser-approved unregistered
// treatment unavailable until the professional's GSTIN is admin-verified.
func RegisteredOnly(family string) bool {
	category, err := QuoteCategory(family)
	return err == nil && strings.HasSuffix(string(category), "_REGISTERED")
}

// ErrUnknownFamily is returned for a family with no GST category.
var ErrUnknownFamily = errors.New("tax: unknown family")

// ErrPlatformGSTIN is returned when DOORSTEP_PLATFORM_GSTIN is invalid.
var ErrPlatformGSTIN = errors.New("tax: platform GSTIN")

// Line is one GST-inclusive charge.
type Line struct {
	Ref        string
	GrossPaise int64
}

// Input is one quote's tax request.
type Input struct {
	ProfessionalGSTIN  string // completion only; the actual supplier
	Family             string
	PlaceOfSupplyState string // two-digit GST state code of the city (HYD: 36)
	At                 time.Time
	Lines              []Line
}

// LineResult is the split of one line; Taxable + Tax == Gross.
type LineResult struct {
	Ref          string
	Category     string
	SAC          string
	RateBPS      int
	TaxablePaise int64
	TaxPaise     int64
	GrossPaise   int64
}

// Result is the split of a quote.
type Result struct {
	Lines       []LineResult
	Provisional bool
	Note        string
}

// Computer splits GST-inclusive lines.
type Computer interface {
	SplitInclusive(in Input) (Result, error)
}

// QuoteCategory is the shared/gst category a quote line of family uses when
// the professional is not yet known.
func QuoteCategory(family string) (gst.Category, error) {
	switch family {
	case FamilyHomeCleaning:
		return gst.CategoryHomeCleaningViaECO, nil
	case FamilyPestControl:
		return gst.CategoryPestControlViaECO, nil
	case FamilyApplianceRepair:
		return gst.CategoryApplianceRepairViaECO, nil
	case FamilyInstallationRepair:
		return gst.CategoryInstallationRepairViaECO, nil
	case FamilyPainting:
		return gst.CategoryPaintingViaECO, nil
	case FamilyBeautySalon:
		// Not under s.9(5): only the registered category exists.
		return gst.CategoryBeautySalonRegistered, nil
	case FamilyCarCare:
		return gst.CategoryCarCareRegistered, nil
	case FamilyHomeStaffing:
		return gst.CategoryHomeStaffingRegistered, nil
	case FamilyRelocation:
		return gst.CategoryRelocationRegistered, nil
	case FamilyPhotography:
		return gst.CategoryPhotographyRegistered, nil
	case FamilyFitnessWellness:
		return gst.CategoryFitnessWellnessRegistered, nil
	case FamilyConstruction:
		return gst.CategoryConstructionViaECO, nil
	}
	return "", fmt.Errorf("%w: %q", ErrUnknownFamily, family)
}

// GST is the shared/gst-backed Computer.
type GST struct {
	table         *gst.RateTable
	platformGSTIN string // normalised; "" = estimate only (outside production)
}

// NewGST builds the computer. An empty platformGSTIN selects the estimate
// path (callers refuse that in production); a non-empty one must be valid.
func NewGST(table *gst.RateTable, platformGSTIN string) (*GST, error) {
	if table == nil {
		table = gst.DefaultRateTable()
	}
	g := &GST{table: table}
	if raw := strings.TrimSpace(platformGSTIN); raw != "" {
		v, err := kyc.ValidateGSTIN(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPlatformGSTIN, err)
		}
		g.platformGSTIN = v.Normalized
	}
	return g, nil
}

// Computed reports whether the gst.Compute path is available.
func (g *GST) Computed() bool { return g.platformGSTIN != "" }

// SplitInclusive implements Computer.
func (g *GST) SplitInclusive(in Input) (Result, error) {
	cat, err := QuoteCategory(in.Family)
	if err != nil {
		return Result{}, err
	}
	if in.ProfessionalGSTIN != "" {
		valid, e := kyc.ValidateGSTIN(in.ProfessionalGSTIN)
		if e != nil {
			return Result{}, e
		}
		cat = gst.Category(in.Family + "_REGISTERED")
		gin := gst.Input{Mode: gst.ModeInclusive, InvoiceDate: in.At, ThroughECO: true, Platform: gst.Party{GSTIN: g.platformGSTIN}, ServiceProfessional: gst.Party{GSTIN: valid.Normalized}, PlaceOfSupplyState: in.PlaceOfSupplyState}
		for _, l := range in.Lines {
			gin.Lines = append(gin.Lines, gst.Line{Ref: l.Ref, Category: cat, Amount: gst.Paise(l.GrossPaise)})
		}
		res, e := gst.Compute(g.table, gin)
		if e != nil {
			return Result{}, e
		}
		out := Result{Provisional: res.NeedsAdviserConfirmation, Note: Note, Lines: make([]LineResult, len(res.Lines))}
		for i, l := range res.Lines {
			out.Lines[i] = LineResult{Ref: l.Ref, Category: string(l.Category), SAC: l.SAC, RateBPS: int(l.RateBP), GrossPaise: int64(l.Gross), TaxablePaise: int64(l.Taxable), TaxPaise: int64(l.Tax)}
		}
		return out, nil
	}
	if len(in.Lines) == 0 {
		return Result{Note: Note, Provisional: true}, nil
	}
	if g.platformGSTIN != "" && strings.HasSuffix(string(cat), "_VIA_ECO") {
		return g.compute(cat, in)
	}
	return g.estimate(cat, in)
}

func (g *GST) compute(cat gst.Category, in Input) (Result, error) {
	gin := gst.Input{
		Mode:               gst.ModeInclusive,
		InvoiceDate:        in.At,
		ThroughECO:         true,
		Platform:           gst.Party{GSTIN: g.platformGSTIN},
		PlaceOfSupplyState: in.PlaceOfSupplyState,
		// ServiceProfessional left empty: unregistered at quote time.
	}
	for _, l := range in.Lines {
		gin.Lines = append(gin.Lines, gst.Line{Ref: l.Ref, Category: cat, Amount: gst.Paise(l.GrossPaise)})
	}
	res, err := gst.Compute(g.table, gin)
	if err != nil {
		return Result{}, err
	}
	out := Result{Lines: make([]LineResult, len(res.Lines)), Provisional: res.NeedsAdviserConfirmation, Note: Note}
	for i, lr := range res.Lines {
		out.Lines[i] = LineResult{
			Ref: lr.Ref, Category: string(lr.Category), SAC: lr.SAC, RateBPS: int(lr.RateBP),
			TaxablePaise: int64(lr.Taxable), TaxPaise: int64(lr.Tax), GrossPaise: int64(lr.Gross),
		}
	}
	return out, nil
}

func (g *GST) estimate(cat gst.Category, in Input) (Result, error) {
	if !kyc.IsValidGSTStateCode(strings.TrimSpace(in.PlaceOfSupplyState)) {
		return Result{}, gst.ErrInvalidPlaceOfSupply
	}
	row, err := g.table.Lookup(cat, in.At)
	if err != nil {
		return Result{}, err
	}
	out := Result{Lines: make([]LineResult, len(in.Lines)), Provisional: true, Note: Note}
	for i, l := range in.Lines {
		if l.GrossPaise < 0 {
			return Result{}, gst.ErrNegativeAmount
		}
		taxable, tax := ExtractInclusive(l.GrossPaise, int(row.RateBP))
		out.Lines[i] = LineResult{
			Ref: l.Ref, Category: string(cat), SAC: row.SAC, RateBPS: int(row.RateBP),
			TaxablePaise: taxable, TaxPaise: tax, GrossPaise: l.GrossPaise,
		}
	}
	return out, nil
}

// ExtractInclusive is shared/gst's inclusive split:
// taxable = floor(gross*10000/(10000+rate)), tax = gross - taxable.
func ExtractInclusive(gross int64, rateBPS int) (taxable, tax int64) {
	if rateBPS == 0 || gross == 0 {
		return gross, 0
	}
	num := new(big.Int).Mul(big.NewInt(gross), big.NewInt(10000))
	q := new(big.Int).Quo(num, big.NewInt(int64(10000+rateBPS)))
	taxable = q.Int64()
	return taxable, gross - taxable
}
