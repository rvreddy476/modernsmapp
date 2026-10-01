package postgres

// The coupon arithmetic and validation, without a database.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

func TestCouponDiscountMinor(t *testing.T) {
	bps := func(v int) *int { return &v }
	p := func(v int64) *int64 { return &v }
	for _, tc := range []struct {
		name     string
		typ      string
		bps      *int
		val, max *int64
		base     money.Paise
		want     money.Paise
		wantErr  bool
	}{
		{"10% of ₹1,000", "percentage", bps(1000), nil, nil, 100000, 10000, false},
		{"12.5% floors", "percentage", bps(1250), nil, nil, 1001, 125, false},
		{"percentage capped", "percentage", bps(2000), nil, p(15000), 200000, 15000, false},
		{"cap above the value is no cap", "percentage", bps(1000), nil, p(99999), 100000, 10000, false},
		{"100%", "percentage", bps(10000), nil, nil, 4321, 4321, false},
		{"flat", "flat", nil, p(5000), nil, 100000, 5000, false},
		{"flat larger than the bag stops at the bag", "flat", nil, p(99900), nil, 500, 500, false},
		{"zero base", "percentage", bps(1000), nil, nil, 0, 0, false},
		{"percentage without bps", "percentage", nil, nil, nil, 1000, 0, true},
		{"percentage over 100%", "percentage", bps(10001), nil, nil, 1000, 0, true},
		{"percentage of zero bps", "percentage", bps(0), nil, nil, 1000, 0, true},
		{"flat without value", "flat", nil, nil, nil, 1000, 0, true},
		{"flat of zero", "flat", nil, p(0), nil, 1000, 0, true},
		{"free shipping is not priced", "free_shipping", nil, p(100), nil, 1000, 0, true},
	} {
		got, err := CouponDiscountMinor(tc.typ, tc.bps, tc.val, tc.max, tc.base)
		if (err != nil) != tc.wantErr || (tc.wantErr && !errors.Is(err, ErrCouponInvalid)) {
			t.Fatalf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
		if got != tc.want {
			t.Fatalf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestCouponCodeFormat(t *testing.T) {
	for in, want := range map[string]string{
		"diwali10": "DIWALI10", " DIWALI 10 ": "DIWALI10", "a\tb c d": "ABCD",
	} {
		if got := NormalizeCouponCode(in); got != want {
			t.Fatalf("NormalizeCouponCode(%q) = %q, want %q", in, got, want)
		}
	}
	for code, ok := range map[string]bool{
		"ABCD": true, "A1B2C3D4E5F6G7H8I9J0": true, "ABC": false, "A1B2C3D4E5F6G7H8I9J0K": false,
		"AB-CD": false, "ab12": false, "ÄBCD": false, "": false, "AB CD": false,
	} {
		if ValidCouponCode(code) != ok {
			t.Fatalf("ValidCouponCode(%q) = %v", code, !ok)
		}
	}
}

func TestValidateCouponCreate(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	seller := uuid.New()
	ok := func() CouponCreate {
		return CouponCreate{SellerID: &seller, Code: "save 10", DiscountType: "percentage", DiscountValue: 1000,
			MaxUsesPerUser: 1, ApplicableTo: "all"}
	}
	in := ok()
	if err := ValidateCouponCreate(&in, now); err != nil || in.Code != "SAVE10" {
		t.Fatalf("valid coupon: %v (code %q)", err, in.Code)
	}
	i64 := func(v int64) *int64 { return &v }
	ip := func(v int) *int { return &v }
	tp := func(v time.Time) *time.Time { return &v }
	for name, mut := range map[string]func(*CouponCreate){
		"short code":               func(c *CouponCreate) { c.Code = "AB1" },
		"symbol in code":           func(c *CouponCreate) { c.Code = "SAVE-10" },
		"unknown type":             func(c *CouponCreate) { c.DiscountType = "free_shipping" },
		"percentage 0":             func(c *CouponCreate) { c.DiscountValue = 0 },
		"percentage over 100%":     func(c *CouponCreate) { c.DiscountValue = 10001 },
		"flat with a cap":          func(c *CouponCreate) { c.DiscountType, c.DiscountValue, c.MaxDiscountMinor = "flat", 100, i64(50) },
		"flat over the mirror":     func(c *CouponCreate) { c.DiscountType, c.DiscountValue = "flat", MaxCouponAmountMinor+1 },
		"zero cap":                 func(c *CouponCreate) { c.MaxDiscountMinor = i64(0) },
		"negative minimum":         func(c *CouponCreate) { c.MinOrderMinor = -1 },
		"zero max uses":            func(c *CouponCreate) { c.MaxUses = ip(0) },
		"zero per buyer":           func(c *CouponCreate) { c.MaxUsesPerUser = 0 },
		"per buyer above total":    func(c *CouponCreate) { c.MaxUses, c.MaxUsesPerUser = ip(2), 3 },
		"seller category scope":    func(c *CouponCreate) { c.ApplicableTo, c.ApplicableIDs = "category", []uuid.UUID{uuid.New()} },
		"product scope with no id": func(c *CouponCreate) { c.ApplicableTo = "product" },
		"all scope with ids":       func(c *CouponCreate) { c.ApplicableIDs = []uuid.UUID{uuid.New()} },
		"expires before start":     func(c *CouponCreate) { c.StartsAt, c.ExpiresAt = tp(now.Add(48*time.Hour)), tp(now.Add(24*time.Hour)) },
		"already expired":          func(c *CouponCreate) { c.ExpiresAt = tp(now.Add(-time.Second)) },
		"long description": func(c *CouponCreate) {
			c.Description = fmt.Sprintf("%0201d", 0)
		},
	} {
		c := ok()
		mut(&c)
		if err := ValidateCouponCreate(&c, now); !errors.Is(err, ErrInvalidCoupon) {
			t.Fatalf("%s: got %v, want ErrInvalidCoupon", name, err)
		}
	}
	// A platform coupon may scope to categories and sellers; ids dedupe.
	plat := ok()
	plat.SellerID = nil
	id := uuid.New()
	plat.ApplicableTo, plat.ApplicableIDs = "category", []uuid.UUID{id, id, uuid.Nil}
	if err := ValidateCouponCreate(&plat, now); err != nil || len(plat.ApplicableIDs) != 1 {
		t.Fatalf("platform category coupon: %v (ids %v)", err, plat.ApplicableIDs)
	}
}

func TestApplyCouponPatch_ThreeStates(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	desc := "Ten off"
	capV := int64(20000)
	uses := 5
	exp := now.Add(72 * time.Hour)
	base := CouponRecord{DiscountType: "percentage", Description: &desc, MaxDiscountMinor: &capV, MinOrderMinor: 500,
		MaxUses: &uses, MaxUsesPerUser: 1, StartsAt: now.Add(-time.Hour), ExpiresAt: &exp, IsActive: true, IsPublic: true}

	// Absent: no change.
	got, changed, err := ApplyCouponPatch(base, CouponPatch{}, now)
	if err != nil || changed || got.MaxUses == nil || got.Description == nil {
		t.Fatalf("empty: %+v %v %v", got, changed, err)
	}
	// Null clears exactly the named fields.
	got, changed, err = ApplyCouponPatch(base, CouponPatch{
		MaxUses: Opt[int]{Set: true, Null: true}, ExpiresAt: Opt[time.Time]{Set: true, Null: true},
	}, now)
	if err != nil || !changed || got.MaxUses != nil || got.ExpiresAt != nil ||
		got.MaxDiscountMinor == nil || got.Description == nil || got.MinOrderMinor != 500 {
		t.Fatalf("null: %+v %v %v", got, changed, err)
	}
	got, _, _ = ApplyCouponPatch(base, CouponPatch{
		Description: Opt[string]{Set: true, Null: true}, MaxDiscountMinor: Opt[int64]{Set: true, Null: true},
		MinOrderMinor: Opt[int64]{Set: true, Null: true},
	}, now)
	if got.Description != nil || got.MaxDiscountMinor != nil || got.MinOrderMinor != 0 || got.MaxUses == nil {
		t.Fatalf("null 2: %+v", got)
	}
	// The input is never mutated.
	if base.MaxUses == nil || *base.MaxUses != 5 || base.Description == nil {
		t.Fatal("ApplyCouponPatch mutated its input")
	}
	// A value sets; setting the same value is no change.
	got, changed, _ = ApplyCouponPatch(base, CouponPatch{IsActive: Opt[bool]{Set: true, Value: false}}, now)
	if !changed || got.IsActive {
		t.Fatalf("deactivate: %+v %v", got, changed)
	}
	_, changed, _ = ApplyCouponPatch(base, CouponPatch{MinOrderMinor: Opt[int64]{Set: true, Value: 500}}, now)
	if changed {
		t.Fatal("setting the same value reported a change")
	}
	for name, p := range map[string]CouponPatch{
		"null per buyer":        {MaxUsesPerUser: Opt[int]{Set: true, Null: true}},
		"null active":           {IsActive: Opt[bool]{Set: true, Null: true}},
		"null public":           {IsPublic: Opt[bool]{Set: true, Null: true}},
		"null starts":           {StartsAt: Opt[time.Time]{Set: true, Null: true}},
		"expiry in the past":    {ExpiresAt: Opt[time.Time]{Set: true, Value: now.Add(-time.Minute)}},
		"zero cap":              {MaxDiscountMinor: Opt[int64]{Set: true, Value: 0}},
		"negative minimum":      {MinOrderMinor: Opt[int64]{Set: true, Value: -1}},
		"per buyer above total": {MaxUsesPerUser: Opt[int]{Set: true, Value: 6}},
		"start after expiry":    {StartsAt: Opt[time.Time]{Set: true, Value: exp.Add(time.Hour)}},
	} {
		if _, _, err := ApplyCouponPatch(base, p, now); !errors.Is(err, ErrInvalidCoupon) {
			t.Fatalf("%s: got %v, want ErrInvalidCoupon", name, err)
		}
	}
	used := base
	used.UsesCount = 4
	if _, _, err := ApplyCouponPatch(used, CouponPatch{MaxUses: Opt[int]{Set: true, Value: 3}}, now); !errors.Is(err, ErrInvalidCoupon) {
		t.Fatalf("max_uses below uses: %v", err)
	}
	flat := base
	flat.DiscountType, flat.MaxDiscountMinor = "flat", nil
	if _, _, err := ApplyCouponPatch(flat, CouponPatch{MaxDiscountMinor: Opt[int64]{Set: true, Value: 100}}, now); !errors.Is(err, ErrInvalidCoupon) {
		t.Fatalf("cap on a flat coupon: %v", err)
	}
}

func TestCouponRefusalCode(t *testing.T) {
	for err, want := range map[error]string{
		ErrCouponInvalid:       "COUPON_INVALID",
		ErrCouponExpired:       "COUPON_EXPIRED",
		ErrCouponExhausted:     "COUPON_USED_UP",
		ErrCouponNotAvailable:  "COUPON_NOT_AVAILABLE",
		ErrCouponNotApplicable: "COUPON_NOT_APPLICABLE",
		fmt.Errorf("wrapped: %w", ErrCouponNotApplicable): "COUPON_NOT_APPLICABLE",
		&CouponMinOrderError{MinOrderMinor: 100}:          "COUPON_MIN_ORDER",
		&CouponNotStartedError{StartsAt: time.Now()}:      "COUPON_INVALID",
	} {
		if got, ok := CouponRefusalCode(err); !ok || got != want {
			t.Fatalf("%v: got %q %v, want %q", err, got, ok, want)
		}
	}
	for _, err := range []error{nil, ErrCouponNotFound, ErrInvalidCoupon, errors.New("db down")} {
		if _, ok := CouponRefusalCode(err); ok {
			t.Fatalf("%v treated as a coupon refusal", err)
		}
	}
}

func TestCouponTitleAndRupees(t *testing.T) {
	bps := func(v int) *int { return &v }
	p := func(v int64) *int64 { return &v }
	desc := "  Festive 10  "
	for _, tc := range []struct {
		got, want string
	}{
		{CouponTitle(&desc, "percentage", bps(1000), nil, nil), "Festive 10"},
		{CouponTitle(nil, "percentage", bps(1000), nil, nil), "10% off"},
		{CouponTitle(nil, "percentage", bps(1250), nil, p(20000)), "12.5% off up to ₹200"},
		{CouponTitle(nil, "percentage", bps(1205), nil, nil), "12.05% off"},
		{CouponTitle(nil, "flat", nil, p(5050), nil), "₹50.50 off"},
		{RupeeText(12345678900), "₹12,34,56,789"},
		{RupeeText(100000), "₹1,000"},
		{RupeeText(99), "₹0.99"},
	} {
		if tc.got != tc.want {
			t.Fatalf("got %q, want %q", tc.got, tc.want)
		}
	}
}

func TestPaymentOfferConsistency(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		o    *PaymentOffer
		want bool
	}{
		{&PaymentOffer{OfferID: id, DiscountMinor: 1000, CapturedMinor: 9000}, true},
		{&PaymentOffer{OfferID: id, DiscountMinor: 1000, CapturedMinor: 9001}, false},
		{&PaymentOffer{OfferID: id, DiscountMinor: 0, CapturedMinor: 10000}, false},
		{&PaymentOffer{OfferID: id, DiscountMinor: 10000, CapturedMinor: 0}, false},
		{&PaymentOffer{OfferID: "offer_RZP123", DiscountMinor: 1000, CapturedMinor: 9000}, false},
		{nil, false},
	} {
		if got := tc.o.consistentWith(10000); got != tc.want {
			t.Fatalf("%+v: got %v", tc.o, got)
		}
	}
}
