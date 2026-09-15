// Package payments is dating-service's side of the payments integration: the
// server-side Premium catalogue, the service-token client for
// payments-service, and the payment-event consumer with its pure decision
// table. Copied from food-service's payments package.
//
// It never imports the store. The store imports it (for Decide and the event
// types) and the consumer reaches the store through the Applier interface.
package payments

// Premium is sold as one-off purchases only (founder decision 2026-09-16): a
// pass of 30, 90 or 365 days, or a single Boost token. No subscription, no
// auto-renew. Prices live here and nowhere else: a purchase stores the price
// read from this table, and a client-supplied amount is refused at the HTTP
// layer. The amounts are the ones the retired Razorpay plan catalogue used
// (monthly_399, quarterly_999, yearly_2499, boost_49).
//
// Play Billing: selling digital goods inside the Android app may fall under
// Google Play's payments policy. Checked before public launch; not built.

// Product ids.
const (
	ProductPass30d  = "pass_30d"
	ProductPass90d  = "pass_90d"
	ProductPass365d = "pass_365d"
	ProductBoost    = "boost"
)

// Product kinds.
const (
	KindPass  = "pass"
	KindBoost = "boost"
)

// Premium features a pass unlocks. Every pass unlocks all of them; the list is
// per feature so the app can render each one and a later catalogue can split
// them without changing the wire shape.
const (
	// FeatureMatchExtend: extend a match by 7 days (service.ExtendMatch).
	FeatureMatchExtend = "match_extend"
	// FeatureDailyBoost: one free Boost every 24 hours (service.RequestBoost).
	FeatureDailyBoost = "daily_boost"
)

// PassFeatures is every feature a pass unlocks, in display order.
var PassFeatures = []string{FeatureMatchExtend, FeatureDailyBoost}

// CurrencyINR is the only currency sold.
const CurrencyINR = "INR"

// Product is one catalogue entry.
type Product struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	// DurationDays is the pass length; 0 for Boost.
	DurationDays int      `json:"duration_days,omitempty"`
	Features     []string `json:"features,omitempty"`
}

var catalogue = []Product{
	{ID: ProductPass30d, Kind: KindPass, Name: "Premium pass, 30 days", AmountMinor: 39900, Currency: CurrencyINR, DurationDays: 30},
	{ID: ProductPass90d, Kind: KindPass, Name: "Premium pass, 90 days", AmountMinor: 99900, Currency: CurrencyINR, DurationDays: 90},
	{ID: ProductPass365d, Kind: KindPass, Name: "Premium pass, 365 days", AmountMinor: 249900, Currency: CurrencyINR, DurationDays: 365},
	{ID: ProductBoost, Kind: KindBoost, Name: "Boost", AmountMinor: 4900, Currency: CurrencyINR},
}

// Catalogue returns a copy of every product, in display order.
func Catalogue() []Product {
	out := make([]Product, len(catalogue))
	for i, p := range catalogue {
		out[i] = p
		if p.Kind == KindPass {
			out[i].Features = append([]string(nil), PassFeatures...)
		}
	}
	return out
}

// LookupProduct returns the catalogue entry for id.
func LookupProduct(id string) (Product, bool) {
	for _, p := range Catalogue() {
		if p.ID == id {
			return p, true
		}
	}
	return Product{}, false
}

// ProductIDs lists every product id, for error details.
func ProductIDs() []string {
	out := make([]string, len(catalogue))
	for i, p := range catalogue {
		out[i] = p.ID
	}
	return out
}

// PassSeconds is a pass's length in seconds (0 for Boost).
func (p Product) PassSeconds() int64 {
	return int64(p.DurationDays) * 86400
}
