// Package settlement computes what a restaurant is owed for a period (Wave 1
// B3). It is pure: every amount is integer paise, nothing is read or written,
// and nothing here moves money. Settlement rows stay PENDING until an admin
// marks them paid by hand.
//
//	net supply      = items + add-ons + packaging - restaurant-funded discount
//	commission      = net supply x commission % (per order, half up)
//	commission GST  = total commission x FOOD_COMMISSION_GST_BP (half up)   [adviser Q16]
//	GST passthrough = GST on SUPPLIER-liable restaurant lines only
//	TCS             = supplier-liable taxable value x FOOD_TCS_RATE_BP      [adviser Q10]
//	refund share    = the restaurant's share of processed refunds
//	payout          = net - commission - commission GST + passthrough - TCS - refund share
//
// The platform fee, the delivery fee and s.9(5) GST (which the platform pays)
// never reach the restaurant; they are reported as exclusions only.
package settlement

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/shared/gst"
)

const (
	EnvCommissionGSTBP = "FOOD_COMMISSION_GST_BP"
	EnvTCSRateBP       = "FOOD_TCS_RATE_BP"

	DefaultCommissionGSTBP int64 = 1800
	// DefaultTCSRateBP is 0.5%, the rate believed in force from 10 July 2024.
	DefaultTCSRateBP int64 = 50
)

// Rules are the two configurable rates. Both are adviser-flagged.
type Rules struct {
	CommissionGSTBP int64 `json:"commission_gst_bp"`
	TCSRateBP       int64 `json:"tcs_rate_bp"`
}

func DefaultRules() Rules {
	return Rules{CommissionGSTBP: DefaultCommissionGSTBP, TCSRateBP: DefaultTCSRateBP}
}

func envBP(getenv func(string) string, key string, def int64) (int64, error) {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 || v > 10000 {
		return 0, fmt.Errorf("%s must be whole basis points between 0 and 10000", key)
	}
	return v, nil
}

// RulesFromEnv reads FOOD_COMMISSION_GST_BP and FOOD_TCS_RATE_BP.
func RulesFromEnv(getenv func(string) string) (Rules, error) {
	r := DefaultRules()
	var err error
	if r.CommissionGSTBP, err = envBP(getenv, EnvCommissionGSTBP, DefaultCommissionGSTBP); err != nil {
		return Rules{}, err
	}
	if r.TCSRateBP, err = envBP(getenv, EnvTCSRateBP, DefaultTCSRateBP); err != nil {
		return Rules{}, err
	}
	return r, nil
}

// Order is one delivered order of the period.
type Order struct {
	OrderID                 string
	ItemSubtotalPaise       int64
	AddonTotalPaise         int64
	PackagingFeePaise       int64
	RestaurantDiscountPaise int64
	PlatformFeePaise        int64
	DeliveryFeePaise        int64
	FinalAmountPaise        int64
	// CommissionBP is the commission percentage snapshot in basis points.
	CommissionBP         int64
	ProcessedRefundPaise int64
	// Breakdown is nil for an order placed before B3; such an order passes no
	// GST through and has no TCS base.
	Breakdown *pricing.Breakdown
}

// OrderLine is the per-order working behind a Line.
type OrderLine struct {
	OrderID             string `json:"order_id"`
	NetSupplyPaise      int64  `json:"net_supply_paise"`
	CommissionPaise     int64  `json:"commission_paise"`
	GSTPassthroughPaise int64  `json:"gst_passthrough_paise"`
	TCSBasePaise        int64  `json:"tcs_base_paise"`
	RefundSharePaise    int64  `json:"refund_share_paise"`
	HasTaxBreakdown     bool   `json:"has_tax_breakdown"`
}

// Line is one restaurant's settlement for a period.
type Line struct {
	OrderCount          int   `json:"order_count"`
	NetSupplyPaise      int64 `json:"net_supply_paise"`
	CommissionPaise     int64 `json:"commission_paise"`
	CommissionGSTPaise  int64 `json:"commission_gst_paise"`
	GSTPassthroughPaise int64 `json:"gst_passthrough_paise"`
	TCSBasePaise        int64 `json:"tcs_base_paise"`
	TCSPaise            int64 `json:"tcs_paise"`
	RefundSharePaise    int64 `json:"refund_share_paise"`
	PayoutPaise         int64 `json:"payout_paise"`

	ExcludedPlatformFeePaise    int64 `json:"excluded_platform_fee_paise"`
	ExcludedDeliveryFeePaise    int64 `json:"excluded_delivery_fee_paise"`
	ExcludedSection95GSTPaise   int64 `json:"excluded_section_9_5_gst_paise"`
	ExcludedPlatformOwnGSTPaise int64 `json:"excluded_platform_own_gst_paise"`

	OrdersWithoutTaxBreakdown int   `json:"orders_without_tax_breakdown"`
	NeedsAdviserConfirmation  bool  `json:"needs_adviser_confirmation"`
	Rules                     Rules `json:"rules"`

	Orders []OrderLine `json:"orders,omitempty"`
}

// halfUp is amount x bp / 10000 rounded half up, in big integers.
func halfUp(amount, bp int64) int64 {
	if amount <= 0 || bp <= 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(amount), big.NewInt(bp))
	n.Add(n, big.NewInt(5000))
	return n.Quo(n, big.NewInt(10000)).Int64()
}

// Commission is the commission on one order's net supply, half up. PlaceOrder
// uses it for orders.commission_amount so the order and the settlement agree.
func Commission(netSupplyPaise, commissionBP int64) int64 {
	return halfUp(netSupplyPaise, commissionBP)
}

// refundShare is the restaurant's part of a processed refund: all of its share
// when the whole order was refunded, otherwise the refund split between the
// restaurant's share and the rest of the order by largest remainder.
func refundShare(refund, share, final int64) int64 {
	if refund <= 0 || share <= 0 {
		return 0
	}
	if refund >= final || share >= final {
		return share
	}
	parts, err := gst.Allocate(gst.Paise(refund), []gst.Paise{gst.Paise(share), gst.Paise(final - share)})
	if err != nil {
		return share
	}
	return int64(parts[0])
}

// ComputeRestaurant settles one restaurant's orders for a period.
func ComputeRestaurant(orders []Order, rules Rules) Line {
	line := Line{Rules: rules, NeedsAdviserConfirmation: true}
	for _, o := range orders {
		ol := OrderLine{OrderID: o.OrderID, HasTaxBreakdown: o.Breakdown != nil}
		ol.NetSupplyPaise = o.ItemSubtotalPaise + o.AddonTotalPaise + o.PackagingFeePaise - o.RestaurantDiscountPaise
		ol.CommissionPaise = Commission(ol.NetSupplyPaise, o.CommissionBP)
		if o.Breakdown == nil {
			line.OrdersWithoutTaxBreakdown++
		} else {
			for _, l := range o.Breakdown.Lines {
				switch {
				case l.Supplier == string(gst.SupplierRestaurant) && l.Liability == string(gst.LiabilitySupplier):
					ol.GSTPassthroughPaise += l.TaxPaise
					if l.ECOCollectsTCS {
						ol.TCSBasePaise += l.TaxablePaise
					}
				case l.Supplier == string(gst.SupplierRestaurant) && l.Liability == string(gst.LiabilityECOSection95):
					line.ExcludedSection95GSTPaise += l.TaxPaise
				case l.Supplier == string(gst.SupplierPlatform):
					line.ExcludedPlatformOwnGSTPaise += l.TaxPaise
				}
			}
		}
		ol.RefundSharePaise = refundShare(o.ProcessedRefundPaise, ol.NetSupplyPaise+ol.GSTPassthroughPaise, o.FinalAmountPaise)

		line.OrderCount++
		line.NetSupplyPaise += ol.NetSupplyPaise
		line.CommissionPaise += ol.CommissionPaise
		line.GSTPassthroughPaise += ol.GSTPassthroughPaise
		line.TCSBasePaise += ol.TCSBasePaise
		line.RefundSharePaise += ol.RefundSharePaise
		line.ExcludedPlatformFeePaise += o.PlatformFeePaise
		line.ExcludedDeliveryFeePaise += o.DeliveryFeePaise
		line.Orders = append(line.Orders, ol)
	}
	line.CommissionGSTPaise = halfUp(line.CommissionPaise, rules.CommissionGSTBP)
	line.TCSPaise = halfUp(line.TCSBasePaise, rules.TCSRateBP)
	line.PayoutPaise = line.NetSupplyPaise - line.CommissionPaise - line.CommissionGSTPaise +
		line.GSTPassthroughPaise - line.TCSPaise - line.RefundSharePaise
	return line
}

// DeliveryPartner is one delivery partner's period total: the sum of the
// stored rider payouts. No tax is computed on it here.
type DeliveryPartner struct {
	DeliveryCount     int   `json:"delivery_count"`
	GrossEarningPaise int64 `json:"gross_earning_paise"`
	PayoutPaise       int64 `json:"payout_paise"`
}

// ComputeDeliveryPartner sums stored rider payouts in paise.
func ComputeDeliveryPartner(payoutsPaise []int64) DeliveryPartner {
	d := DeliveryPartner{DeliveryCount: len(payoutsPaise)}
	for _, p := range payoutsPaise {
		d.GrossEarningPaise += p
	}
	d.PayoutPaise = d.GrossEarningPaise
	return d
}
