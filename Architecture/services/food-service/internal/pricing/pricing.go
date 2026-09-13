// Package pricing turns a cart into order totals in integer paise through
// shared/gst (Wave 1 B3). It is pure: no database, no clock, no network.
//
// Everything it computes rests on the seeded rate table, every row of which
// is flagged for adviser confirmation (docs/FEAST-TAX-ADVISER-REVIEW.md), so
// every Breakdown and every client view carries that flag.
//
// Assumptions encoded here, each flagged for the adviser:
//   - menu prices are EXCLUSIVE of GST; tax is added at checkout;
//   - menu items, add-ons and packaging are the restaurant's supply at the
//     restaurant's tax category;
//   - the platform fee is PLATFORM_FEE and the delivery fee is
//     DELIVERY_FEE_PLATFORM, both the platform's own supplies;
//   - the place of supply for EVERY line is the restaurant's state;
//   - a coupon discount, when coupons are switched on, is treated as
//     restaurant-funded (it reduces the restaurant's taxable value).
package pricing

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/shared/gst"
	"github.com/atpost/shared/kyc"
)

const (
	EnvPlatformFeePaise = "FOOD_PLATFORM_FEE_PAISE"
	EnvDeliveryFeePaise = "FOOD_DELIVERY_FEE_PAISE"
	EnvPlatformGSTIN    = "FOOD_PLATFORM_GSTIN"
	EnvCouponsEnabled   = "FOOD_COUPONS_ENABLED"

	DefaultPlatformFeePaise int64 = 500
	DefaultDeliveryFeePaise int64 = 2900

	// AdviserNotice is shown wherever a computed tax figure reaches a person.
	AdviserNotice = "Tax rates pending adviser confirmation"

	// BreakdownVersion is stored on every orders.tax_breakdown document.
	BreakdownVersion = 1
)

// Line kinds, stored on each breakdown line.
const (
	KindItem        = "ITEM"
	KindAddon       = "ADDON"
	KindPackaging   = "PACKAGING"
	KindPlatformFee = "PLATFORM_FEE"
	KindDeliveryFee = "DELIVERY_FEE"
)

// Fixed refs for the order-level lines.
const (
	RefPackaging   = "packaging"
	RefPlatformFee = "platform_fee"
	RefDeliveryFee = "delivery_fee"
)

var (
	// ErrRestaurantTaxCategoryMissing: 422 FOOD_RESTAURANT_TAX_CATEGORY_MISSING.
	ErrRestaurantTaxCategoryMissing = errors.New("restaurant has no GST tax category and cannot take orders")
	// ErrRestaurantStateUnknown: 422 FOOD_RESTAURANT_STATE_UNKNOWN.
	ErrRestaurantStateUnknown = errors.New("restaurant state is not a GST state, so the place of supply cannot be set")
	// ErrRestaurantGSTINMissing: 422 FOOD_RESTAURANT_GSTIN_MISSING.
	ErrRestaurantGSTINMissing = errors.New("restaurant is liable for GST on its tax category but has no GSTIN")
	// ErrPlatformGSTINNotConfigured: 503 FOOD_PLATFORM_GSTIN_NOT_CONFIGURED.
	ErrPlatformGSTINNotConfigured = errors.New("the platform GSTIN is not configured, so orders cannot be priced")
	// ErrCouponsDisabled: 422 FOOD_COUPONS_DISABLED. Adviser question 12.
	ErrCouponsDisabled = errors.New("coupons are switched off until the GST treatment of discounts is confirmed")
	// ErrPricingFailed: 422 FOOD_PRICING_FAILED.
	ErrPricingFailed = errors.New("the order could not be priced")
)

// Code is the stable machine-readable code for a pricing error, "" otherwise.
func Code(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrRestaurantTaxCategoryMissing):
		return "FOOD_RESTAURANT_TAX_CATEGORY_MISSING"
	case errors.Is(err, ErrRestaurantStateUnknown):
		return "FOOD_RESTAURANT_STATE_UNKNOWN"
	case errors.Is(err, ErrRestaurantGSTINMissing):
		return "FOOD_RESTAURANT_GSTIN_MISSING"
	case errors.Is(err, ErrPlatformGSTINNotConfigured):
		return "FOOD_PLATFORM_GSTIN_NOT_CONFIGURED"
	case errors.Is(err, ErrCouponsDisabled):
		return "FOOD_COUPONS_DISABLED"
	case errors.Is(err, ErrPricingFailed):
		return "FOOD_PRICING_FAILED"
	}
	return ""
}

var defaultTable = gst.DefaultRateTable()

// Config is the pricing configuration read once at boot.
type Config struct {
	PlatformFeePaise int64
	DeliveryFeePaise int64
	// PlatformGSTIN is normalised; empty means not configured (local/dev only).
	PlatformGSTIN  string
	CouponsEnabled bool
	// Table nil means gst.DefaultRateTable().
	Table *gst.RateTable
}

// DefaultConfig has the default fees, coupons off and no platform GSTIN.
func DefaultConfig() Config {
	return Config{PlatformFeePaise: DefaultPlatformFeePaise, DeliveryFeePaise: DefaultDeliveryFeePaise}
}

func (c Config) table() *gst.RateTable {
	if c.Table != nil {
		return c.Table
	}
	return defaultTable
}

// CheckCoupon refuses any coupon while the coupons flag is off.
func (c Config) CheckCoupon(code string) error {
	if strings.TrimSpace(code) != "" && !c.CouponsEnabled {
		return ErrCouponsDisabled
	}
	return nil
}

// isProduction matches foodpii.IsProduction and the payments client: every
// ENV except local, dev and development, a blank one included.
func isProduction(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return false
	}
	return true
}

func envPaise(getenv func(string) string, key string, def int64) (int64, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 || v > int64(gst.MaxAmount) {
		return 0, fmt.Errorf("%s must be a whole number of paise, 0 or more", key)
	}
	return v, nil
}

// ConfigFromEnv reads the fees, the coupons flag and the platform GSTIN. A
// GSTIN that is set must be valid in every environment; an absent one is
// refused outside local/dev. Errors name the variable, never its value.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := DefaultConfig()
	var err error
	if cfg.PlatformFeePaise, err = envPaise(getenv, EnvPlatformFeePaise, DefaultPlatformFeePaise); err != nil {
		return Config{}, err
	}
	if cfg.DeliveryFeePaise, err = envPaise(getenv, EnvDeliveryFeePaise, DefaultDeliveryFeePaise); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(getenv(EnvCouponsEnabled)); raw != "" {
		on, perr := strconv.ParseBool(raw)
		if perr != nil {
			return Config{}, fmt.Errorf("%s must be true or false", EnvCouponsEnabled)
		}
		cfg.CouponsEnabled = on
	}
	raw := strings.TrimSpace(getenv(EnvPlatformGSTIN))
	if raw == "" {
		if isProduction(getenv("ENV")) {
			return Config{}, fmt.Errorf("%s is required unless ENV is local, dev or development", EnvPlatformGSTIN)
		}
		return cfg, nil
	}
	g, err := kyc.ValidateGSTIN(raw)
	if err != nil {
		return Config{}, fmt.Errorf("%s is not a valid GSTIN", EnvPlatformGSTIN)
	}
	cfg.PlatformGSTIN = g.Normalized
	return cfg, nil
}

// Restaurant is the restaurant's tax identity as stored.
type Restaurant struct {
	TaxCategory    string
	GSTIN          string
	GSTINStateCode string
	// State is food.restaurants.state: a state name or a two-digit code.
	State string
}

var gstStateCodes = func() []string {
	var out []string
	for i := 1; i <= 99; i++ {
		code := fmt.Sprintf("%02d", i)
		if kyc.IsValidGSTStateCode(code) {
			out = append(out, code)
		}
	}
	return out
}()

// PlaceOfSupplyState is the restaurant's GST state: from its GSTIN, else its
// recorded GSTIN state code, else its state (a code or an exact state name).
func (r Restaurant) PlaceOfSupplyState() (string, error) {
	if raw := strings.TrimSpace(r.GSTIN); raw != "" {
		g, err := kyc.ValidateGSTIN(raw)
		if err != nil {
			return "", fmt.Errorf("%w: the restaurant GSTIN on record is not valid", ErrPricingFailed)
		}
		return g.StateCode, nil
	}
	if code := strings.TrimSpace(r.GSTINStateCode); kyc.IsValidGSTStateCode(code) {
		return code, nil
	}
	state := strings.TrimSpace(r.State)
	if kyc.IsValidGSTStateCode(state) {
		return state, nil
	}
	for _, code := range gstStateCodes {
		if name, _ := kyc.GSTStateName(code); state != "" && strings.EqualFold(name, state) {
			return code, nil
		}
	}
	return "", ErrRestaurantStateUnknown
}

// AddonLine is one add-on of a cart item. Quantity is the TOTAL units (add-on
// quantity x item quantity).
type AddonLine struct {
	Ref       string
	Name      string
	Quantity  int64
	UnitPaise int64
}

// ItemLine is one cart item.
type ItemLine struct {
	Ref       string
	Name      string
	Quantity  int64
	UnitPaise int64
	Addons    []AddonLine
}

// Cart is what Price prices.
type Cart struct {
	Items          []ItemLine
	PackagingPaise int64
	// DiscountPaise is allocated over the restaurant's lines (restaurant-funded).
	DiscountPaise int64
}

// Totals are the order money columns in paise.
type Totals struct {
	ItemSubtotalPaise  int64 `json:"item_subtotal_paise"`
	AddonTotalPaise    int64 `json:"addon_total_paise"`
	PackagingFeePaise  int64 `json:"packaging_fee_paise"`
	DeliveryFeePaise   int64 `json:"delivery_fee_paise"`
	PlatformFeePaise   int64 `json:"platform_fee_paise"`
	TaxTotalPaise      int64 `json:"tax_total_paise"`
	DiscountTotalPaise int64 `json:"discount_total_paise"`
	FinalAmountPaise   int64 `json:"final_amount_paise"`
}

// Sum recomputes the final amount from the components.
func (t Totals) Sum() int64 {
	return t.ItemSubtotalPaise + t.AddonTotalPaise + t.PackagingFeePaise + t.DeliveryFeePaise +
		t.PlatformFeePaise + t.TaxTotalPaise - t.DiscountTotalPaise
}

// BreakdownLine is one gst.LineResult, stored in snake_case.
type BreakdownLine struct {
	Ref                      string `json:"ref"`
	Kind                     string `json:"kind"`
	Category                 string `json:"category"`
	SAC                      string `json:"sac"`
	RateBP                   int32  `json:"rate_bp"`
	ITCAvailable             bool   `json:"itc_available"`
	NeedsAdviserConfirmation bool   `json:"needs_adviser_confirmation"`
	RateEffectiveFrom        string `json:"rate_effective_from"`
	Supplier                 string `json:"supplier"`
	Liability                string `json:"liability"`
	LiableParty              string `json:"liable_party"`
	LiablePartyGSTIN         string `json:"liable_party_gstin"`
	ECOCollectsTCS           bool   `json:"eco_collects_tcs"`
	SupplierState            string `json:"supplier_state"`
	PlaceOfSupplyState       string `json:"place_of_supply_state"`
	Interstate               bool   `json:"interstate"`
	AmountPaise              int64  `json:"amount_paise"`
	AllocatedDiscountPaise   int64  `json:"allocated_discount_paise"`
	TaxablePaise             int64  `json:"taxable_paise"`
	TaxPaise                 int64  `json:"tax_paise"`
	CGSTPaise                int64  `json:"cgst_paise"`
	SGSTPaise                int64  `json:"sgst_paise"`
	IGSTPaise                int64  `json:"igst_paise"`
	GrossPaise               int64  `json:"gross_paise"`
}

// PartyTotals is one gst.PartyTotals, stored in snake_case.
type PartyTotals struct {
	LiableParty  string `json:"liable_party"`
	GSTIN        string `json:"gstin"`
	Liability    string `json:"liability"`
	TaxablePaise int64  `json:"taxable_paise"`
	TaxPaise     int64  `json:"tax_paise"`
	CGSTPaise    int64  `json:"cgst_paise"`
	SGSTPaise    int64  `json:"sgst_paise"`
	IGSTPaise    int64  `json:"igst_paise"`
	GrossPaise   int64  `json:"gross_paise"`
}

// Breakdown is the gst.Result stored on orders.tax_breakdown. It is food's
// own shape so the stored document does not change when shared/gst renames a
// Go field.
type Breakdown struct {
	Version                      int             `json:"version"`
	Mode                         string          `json:"mode"`
	MenuPricesTreatedAsExclusive bool            `json:"menu_prices_treated_as_exclusive"`
	ThroughECO                   bool            `json:"through_eco"`
	InvoiceDate                  string          `json:"invoice_date"`
	PlaceOfSupplyState           string          `json:"place_of_supply_state"`
	RestaurantTaxCategory        string          `json:"restaurant_tax_category"`
	Lines                        []BreakdownLine `json:"lines"`
	ByLiableParty                []PartyTotals   `json:"by_liable_party"`
	TotalPaise                   int64           `json:"total_paise"`
	TotalTaxablePaise            int64           `json:"total_taxable_paise"`
	TotalTaxPaise                int64           `json:"total_tax_paise"`
	TotalCGSTPaise               int64           `json:"total_cgst_paise"`
	TotalSGSTPaise               int64           `json:"total_sgst_paise"`
	TotalIGSTPaise               int64           `json:"total_igst_paise"`
	NeedsAdviserConfirmation     bool            `json:"needs_adviser_confirmation"`
}

// Quote is a priced cart.
type Quote struct {
	Totals    Totals
	Breakdown Breakdown
	// itemOf maps an add-on ref to its item ref.
	itemOf map[string]string
}

// Line returns the breakdown line for ref.
func (q *Quote) Line(ref string) (BreakdownLine, bool) {
	for _, l := range q.Breakdown.Lines {
		if l.Ref == ref {
			return l, true
		}
	}
	return BreakdownLine{}, false
}

// TaxByItem is the tax on each item line plus its add-on lines, keyed by the
// item ref. Packaging and fee tax is not in it.
func (q *Quote) TaxByItem() map[string]int64 {
	out := map[string]int64{}
	for _, l := range q.Breakdown.Lines {
		switch l.Kind {
		case KindItem:
			out[l.Ref] += l.TaxPaise
		case KindAddon:
			out[q.itemOf[l.Ref]] += l.TaxPaise
		}
	}
	return out
}

func mul(a, b int64) (int64, bool) {
	p := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if !p.IsInt64() || p.Int64() > int64(gst.MaxAmount) {
		return 0, false
	}
	return p.Int64(), true
}

// Price computes the order through gst.Compute: EXCLUSIVE mode, through the
// ECO, place of supply the restaurant's state, zero-amount lines omitted.
func Price(cfg Config, r Restaurant, c Cart, at time.Time) (*Quote, error) {
	category := gst.Category(strings.ToUpper(strings.TrimSpace(r.TaxCategory)))
	if category == "" {
		return nil, ErrRestaurantTaxCategoryMissing
	}
	table := cfg.table()
	row, err := table.Lookup(category, at)
	if err != nil || row.Supplier != gst.SupplierRestaurant {
		return nil, fmt.Errorf("%w: the restaurant tax category has no restaurant rate in effect", ErrPricingFailed)
	}
	pos, err := r.PlaceOfSupplyState()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.PlatformGSTIN) == "" {
		return nil, ErrPlatformGSTINNotConfigured
	}
	if !row.ECOSection95 && strings.TrimSpace(r.GSTIN) == "" {
		return nil, ErrRestaurantGSTINMissing
	}
	if c.PackagingPaise < 0 || c.DiscountPaise < 0 || cfg.PlatformFeePaise < 0 || cfg.DeliveryFeePaise < 0 {
		return nil, fmt.Errorf("%w: negative amount", ErrPricingFailed)
	}

	q := &Quote{itemOf: map[string]string{}}
	var lines []gst.Line
	var kinds []string
	add := func(ref, kind string, cat gst.Category, amount int64) {
		if amount == 0 {
			return
		}
		lines = append(lines, gst.Line{Ref: ref, Category: cat, Amount: gst.Paise(amount)})
		kinds = append(kinds, kind)
	}
	for _, it := range c.Items {
		if it.Quantity <= 0 || it.UnitPaise < 0 {
			return nil, fmt.Errorf("%w: item quantity or price out of range", ErrPricingFailed)
		}
		amount, ok := mul(it.Quantity, it.UnitPaise)
		if !ok {
			return nil, fmt.Errorf("%w: item amount out of range", ErrPricingFailed)
		}
		q.Totals.ItemSubtotalPaise += amount
		add(it.Ref, KindItem, category, amount)
		for _, a := range it.Addons {
			if a.Quantity <= 0 || a.UnitPaise < 0 {
				return nil, fmt.Errorf("%w: add-on quantity or price out of range", ErrPricingFailed)
			}
			addonAmount, ok := mul(a.Quantity, a.UnitPaise)
			if !ok {
				return nil, fmt.Errorf("%w: add-on amount out of range", ErrPricingFailed)
			}
			q.Totals.AddonTotalPaise += addonAmount
			q.itemOf[a.Ref] = it.Ref
			add(a.Ref, KindAddon, category, addonAmount)
		}
	}
	q.Totals.PackagingFeePaise = c.PackagingPaise
	add(RefPackaging, KindPackaging, category, c.PackagingPaise)
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: nothing to price", ErrPricingFailed)
	}
	q.Totals.PlatformFeePaise = cfg.PlatformFeePaise
	add(RefPlatformFee, KindPlatformFee, gst.CategoryPlatformFee, cfg.PlatformFeePaise)
	q.Totals.DeliveryFeePaise = cfg.DeliveryFeePaise
	add(RefDeliveryFee, KindDeliveryFee, gst.CategoryDeliveryFeePlatform, cfg.DeliveryFeePaise)

	restaurantParty := gst.Party{StateCode: pos}
	if strings.TrimSpace(r.GSTIN) != "" {
		restaurantParty = gst.Party{GSTIN: r.GSTIN}
	}
	res, err := gst.Compute(table, gst.Input{
		Mode:               gst.ModeExclusive,
		InvoiceDate:        at,
		ThroughECO:         true,
		Restaurant:         restaurantParty,
		Platform:           gst.Party{GSTIN: cfg.PlatformGSTIN},
		PlaceOfSupplyState: pos,
		RestaurantDiscount: gst.Paise(c.DiscountPaise),
		Lines:              lines,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPricingFailed, err)
	}
	q.Totals.TaxTotalPaise = int64(res.TotalTax)
	q.Totals.DiscountTotalPaise = c.DiscountPaise
	q.Totals.FinalAmountPaise = int64(res.Total)
	q.Breakdown = breakdownFrom(res, kinds, at, pos, string(category))
	if q.Totals.Sum() != q.Totals.FinalAmountPaise {
		return nil, fmt.Errorf("%w: %v", ErrPricingFailed, gst.ErrInternalInvariant)
	}
	return q, nil
}

func breakdownFrom(res *gst.Result, kinds []string, at time.Time, pos, category string) Breakdown {
	b := Breakdown{
		Version:                      BreakdownVersion,
		Mode:                         string(gst.ModeExclusive),
		MenuPricesTreatedAsExclusive: true,
		ThroughECO:                   true,
		InvoiceDate:                  at.UTC().Format(time.RFC3339),
		PlaceOfSupplyState:           pos,
		RestaurantTaxCategory:        category,
		TotalPaise:                   int64(res.Total),
		TotalTaxablePaise:            int64(res.TotalTaxable),
		TotalTaxPaise:                int64(res.TotalTax),
		TotalCGSTPaise:               int64(res.TotalCGST),
		TotalSGSTPaise:               int64(res.TotalSGST),
		TotalIGSTPaise:               int64(res.TotalIGST),
		NeedsAdviserConfirmation:     res.NeedsAdviserConfirmation,
		Lines:                        make([]BreakdownLine, len(res.Lines)),
		ByLiableParty:                make([]PartyTotals, len(res.ByLiableParty)),
	}
	for i, l := range res.Lines {
		b.Lines[i] = BreakdownLine{
			Ref: l.Ref, Kind: kinds[i], Category: string(l.Category), SAC: l.SAC, RateBP: int32(l.RateBP),
			ITCAvailable: l.ITCAvailable, NeedsAdviserConfirmation: l.NeedsAdviserConfirmation,
			RateEffectiveFrom: l.RateEffectiveFrom.Format("2006-01-02"),
			Supplier:          string(l.Supplier), Liability: string(l.Liability), LiableParty: string(l.LiableParty),
			LiablePartyGSTIN: l.LiablePartyGSTIN, ECOCollectsTCS: l.ECOCollectsTCS,
			SupplierState: l.SupplierState, PlaceOfSupplyState: l.PlaceOfSupplyState, Interstate: l.Interstate,
			AmountPaise: int64(l.Amount), AllocatedDiscountPaise: int64(l.AllocatedDiscount),
			TaxablePaise: int64(l.Taxable), TaxPaise: int64(l.Tax), CGSTPaise: int64(l.CGST),
			SGSTPaise: int64(l.SGST), IGSTPaise: int64(l.IGST), GrossPaise: int64(l.Gross),
		}
	}
	for i, p := range res.ByLiableParty {
		b.ByLiableParty[i] = PartyTotals{
			LiableParty: string(p.LiableParty), GSTIN: p.GSTIN, Liability: string(p.Liability),
			TaxablePaise: int64(p.Taxable), TaxPaise: int64(p.Tax), CGSTPaise: int64(p.CGST),
			SGSTPaise: int64(p.SGST), IGSTPaise: int64(p.IGST), GrossPaise: int64(p.Gross),
		}
	}
	return b
}

// ─── Client view ────────────────────────────────────────────────────────────

// Charge is a pre-tax charge the customer pays on top of the food.
type Charge struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	AmountPaise int64  `json:"amount_paise"`
}

// RateLine is one rate inside a TaxGroup.
type RateLine struct {
	RateBP                   int32  `json:"rate_bp"`
	RatePercent              string `json:"rate_percent"`
	TaxablePaise             int64  `json:"taxable_paise"`
	CGSTPaise                int64  `json:"cgst_paise"`
	SGSTPaise                int64  `json:"sgst_paise"`
	IGSTPaise                int64  `json:"igst_paise"`
	TaxPaise                 int64  `json:"tax_paise"`
	NeedsAdviserConfirmation bool   `json:"needs_adviser_confirmation"`
}

// TaxGroup is the tax one party is liable for, under one liability.
type TaxGroup struct {
	LiableParty  string     `json:"liable_party"`
	Liability    string     `json:"liability"`
	Label        string     `json:"label"`
	TaxablePaise int64      `json:"taxable_paise"`
	TaxPaise     int64      `json:"tax_paise"`
	Rates        []RateLine `json:"rates"`
}

// TaxesAndCharges is what a client renders under "Taxes & charges" without
// doing any tax arithmetic. Every figure is already in the order total.
type TaxesAndCharges struct {
	Charges                      []Charge   `json:"charges"`
	Taxes                        []TaxGroup `json:"taxes"`
	TotalChargesPaise            int64      `json:"total_charges_paise"`
	TotalTaxPaise                int64      `json:"total_tax_paise"`
	TotalTaxesAndChargesPaise    int64      `json:"total_taxes_and_charges_paise"`
	MenuPricesTreatedAsExclusive bool       `json:"menu_prices_treated_as_exclusive"`
	NeedsAdviserConfirmation     bool       `json:"needs_adviser_confirmation"`
	AdviserNotice                string     `json:"adviser_notice,omitempty"`
}

func percent(bp int32) string {
	return fmt.Sprintf("%d.%02d", bp/100, bp%100)
}

func groupLabel(party, liability string) string {
	switch {
	case party == string(gst.SupplierRestaurant):
		return "GST on food, charged by the restaurant"
	case liability == string(gst.LiabilityECOSection95):
		return "GST on food, paid by the platform under section 9(5)"
	default:
		return "GST on platform and delivery fees"
	}
}

func partyRank(party string) int {
	switch party {
	case string(gst.SupplierRestaurant):
		return 0
	case string(gst.SupplierPlatform):
		return 1
	}
	return 2
}

// TaxesAndChargesFrom builds the client view from a stored breakdown.
func TaxesAndChargesFrom(b Breakdown, t Totals) TaxesAndCharges {
	view := TaxesAndCharges{
		Charges:                      []Charge{},
		Taxes:                        []TaxGroup{},
		MenuPricesTreatedAsExclusive: b.MenuPricesTreatedAsExclusive,
		NeedsAdviserConfirmation:     b.NeedsAdviserConfirmation,
	}
	for _, ch := range []Charge{
		{Kind: KindPackaging, Label: "Packaging charges", AmountPaise: t.PackagingFeePaise},
		{Kind: KindPlatformFee, Label: "Platform fee", AmountPaise: t.PlatformFeePaise},
		{Kind: KindDeliveryFee, Label: "Delivery fee", AmountPaise: t.DeliveryFeePaise},
	} {
		if ch.AmountPaise != 0 {
			view.Charges = append(view.Charges, ch)
			view.TotalChargesPaise += ch.AmountPaise
		}
	}
	type gkey struct{ party, liability string }
	groups := map[gkey]*TaxGroup{}
	rates := map[gkey]map[int32]*RateLine{}
	var order []gkey
	for _, l := range b.Lines {
		k := gkey{l.LiableParty, l.Liability}
		g, ok := groups[k]
		if !ok {
			g = &TaxGroup{LiableParty: l.LiableParty, Liability: l.Liability, Label: groupLabel(l.LiableParty, l.Liability)}
			groups[k] = g
			rates[k] = map[int32]*RateLine{}
			order = append(order, k)
		}
		g.TaxablePaise += l.TaxablePaise
		g.TaxPaise += l.TaxPaise
		rl, ok := rates[k][l.RateBP]
		if !ok {
			rl = &RateLine{RateBP: l.RateBP, RatePercent: percent(l.RateBP)}
			rates[k][l.RateBP] = rl
		}
		rl.TaxablePaise += l.TaxablePaise
		rl.CGSTPaise += l.CGSTPaise
		rl.SGSTPaise += l.SGSTPaise
		rl.IGSTPaise += l.IGSTPaise
		rl.TaxPaise += l.TaxPaise
		rl.NeedsAdviserConfirmation = rl.NeedsAdviserConfirmation || l.NeedsAdviserConfirmation
	}
	sort.SliceStable(order, func(i, j int) bool {
		if partyRank(order[i].party) != partyRank(order[j].party) {
			return partyRank(order[i].party) < partyRank(order[j].party)
		}
		return order[i].liability > order[j].liability // SUPPLIER before ECO_SECTION_9_5
	})
	for _, k := range order {
		g := groups[k]
		var bps []int32
		for bp := range rates[k] {
			bps = append(bps, bp)
		}
		sort.Slice(bps, func(i, j int) bool { return bps[i] < bps[j] })
		g.Rates = []RateLine{}
		for _, bp := range bps {
			g.Rates = append(g.Rates, *rates[k][bp])
		}
		view.Taxes = append(view.Taxes, *g)
		view.TotalTaxPaise += g.TaxPaise
	}
	view.TotalTaxesAndChargesPaise = view.TotalChargesPaise + view.TotalTaxPaise
	if view.NeedsAdviserConfirmation {
		view.AdviserNotice = AdviserNotice
	}
	return view
}

// PaiseToRupees is for float JSON fields that predate B3. Exact for every
// amount below 2^53 paise: the shortest decimal of p/100 is its 2dp form.
func PaiseToRupees(p int64) float64 {
	return float64(p) / 100
}
