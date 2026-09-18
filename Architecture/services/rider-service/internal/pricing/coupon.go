package pricing

// Coupon discount types.
const (
	CouponFlat    = "flat"
	CouponPercent = "percent"
)

// Coupon is a validated coupon as the engine sees it. Validation (dates,
// limits, city, vehicle, first ride) is the service's job; this type only
// knows how much to take off a ride fare.
type Coupon struct {
	ID               string
	Code             string
	DiscountType     string
	ValuePaise       int64 // flat
	PercentBPS       int64 // percent
	MaxDiscountPaise int64 // 0 = no cap
	MinFarePaise     int64
}

// MeetsMinFare reports whether the ride fare (before discount) is large
// enough for the coupon.
func (c *Coupon) MeetsMinFare(rideFarePaise int64) bool {
	return c != nil && rideFarePaise >= c.MinFarePaise
}

// DiscountFor is the raw discount on a ride fare, before the engine clamps
// it so the fare never drops below the rule's minimum. It is 0 when the ride
// fare is under the coupon's minimum.
func (c *Coupon) DiscountFor(rideFarePaise int64) int64 {
	if c == nil || rideFarePaise <= 0 || !c.MeetsMinFare(rideFarePaise) {
		return 0
	}
	var d int64
	switch c.DiscountType {
	case CouponFlat:
		d = c.ValuePaise
	case CouponPercent:
		d = rideFarePaise * c.PercentBPS / 10000
	default:
		return 0
	}
	if c.MaxDiscountPaise > 0 && d > c.MaxDiscountPaise {
		d = c.MaxDiscountPaise
	}
	if d > rideFarePaise {
		d = rideFarePaise
	}
	if d < 0 {
		d = 0
	}
	return d
}
