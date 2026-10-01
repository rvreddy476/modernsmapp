package service

// MStore coupons and bank offers — the service half.
//
//	coupons      seller coupons (approved sellers only), platform coupons
//	             (admin console; applied only while
//	             COMMERCE_PLATFORM_COUPONS_ENABLED), the bag's coupon list,
//	             best_coupon on product reads
//	bank offers  the buyer-facing list of payments-service's registered
//	             Razorpay offers, with what each would save on an amount
//
// The money rules themselves live in the store (store/postgres/coupons.go)
// and, for bank offers, in payments-service (gateway.AllowedOfferDiscount);
// EstimatedOfferDiscount below is a display copy of that formula, pinned to
// payments' own table by a test.

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/atpost/commerce-service/internal/payments"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── The platform-coupon switch ─────────────────────────────────────────

// WithPlatformCoupons is COMMERCE_PLATFORM_COUPONS_ENABLED. Off (the default,
// and every environment until the tax adviser confirms GST on a
// platform-funded discount) refuses a platform code at quote and checkout
// with COUPON_NOT_AVAILABLE and lists none in the bag.
func (s *Service) WithPlatformCoupons(enabled bool) *Service {
	s.platformCoupons = enabled
	return s
}

// PlatformCouponsEnabled reports the switch (the admin list's meta field).
func (s *Service) PlatformCouponsEnabled() bool { return s.platformCoupons }

// ─── Seller coupons ─────────────────────────────────────────────────────

// approvedSeller resolves the caller's shop, which must be approved: a shop
// that cannot sell has nothing to discount.
func (s *Service) approvedSeller(ctx context.Context, userID uuid.UUID) (*postgres.Seller, error) {
	seller, err := s.GetSellerProfile(ctx, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNoSellerRow) {
			return nil, ErrNoSellerProfile
		}
		return nil, err
	}
	if seller == nil {
		return nil, ErrNoSellerProfile
	}
	if seller.Status != "approved" {
		return nil, postgres.ErrSellerNotApproved
	}
	return seller, nil
}

// SellerCoupons lists the caller's shop's coupons.
func (s *Service) SellerCoupons(ctx context.Context, userID uuid.UUID) ([]*postgres.CouponRecord, error) {
	seller, err := s.approvedSeller(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.store.ListSellerCoupons(ctx, seller.ID)
}

// CreateSellerCoupon creates a coupon funded by the caller's shop. The seller
// is the caller's, never the body's.
func (s *Service) CreateSellerCoupon(ctx context.Context, userID uuid.UUID, in postgres.CouponCreate) (*postgres.CouponRecord, error) {
	seller, err := s.approvedSeller(ctx, userID)
	if err != nil {
		return nil, err
	}
	in.SellerID = &seller.ID
	in.CreatedBy = userID
	in.Admin = false
	return s.store.CreateCoupon(ctx, in)
}

// PatchSellerCoupon edits one of the caller's shop's coupons.
func (s *Service) PatchSellerCoupon(ctx context.Context, userID, couponID uuid.UUID, p postgres.CouponPatch) (*postgres.CouponRecord, error) {
	seller, err := s.approvedSeller(ctx, userID)
	if err != nil {
		return nil, err
	}
	out, _, err := s.store.PatchCoupon(ctx, couponID, postgres.CouponPatchScope{SellerID: &seller.ID, Actor: userID}, p)
	return out, err
}

// ─── Platform coupons (admin console) ───────────────────────────────────

// AdminCoupons is the console's list.
func (s *Service) AdminCoupons(ctx context.Context, f postgres.CouponListFilter) ([]*postgres.CouponRecord, int, error) {
	return s.store.ListCoupons(ctx, f)
}

// CreatePlatformCoupon creates a platform-funded coupon. Allowed while the
// switch is off — the console prepares them; buyers cannot apply them.
func (s *Service) CreatePlatformCoupon(ctx context.Context, actor uuid.UUID, in postgres.CouponCreate) (*postgres.CouponRecord, error) {
	in.SellerID = nil
	in.CreatedBy = actor
	in.Admin = true
	return s.store.CreateCoupon(ctx, in)
}

// PatchPlatformCoupon edits a platform coupon; seller coupons are read-only
// to the console.
func (s *Service) PatchPlatformCoupon(ctx context.Context, actor, couponID uuid.UUID, p postgres.CouponPatch, reason *string) (*postgres.CouponRecord, error) {
	out, _, err := s.store.PatchCoupon(ctx, couponID,
		postgres.CouponPatchScope{Admin: true, Actor: actor, Reason: reason}, p)
	return out, err
}

// ─── The buyer side ─────────────────────────────────────────────────────

// CartCouponsResult is GET /cart/coupons.
type CartCouponsResult struct {
	SubtotalMinor int64                 `json:"subtotal_minor"`
	Items         []postgres.CartCoupon `json:"items"`
}

// CartCoupons lists what the public coupons would do to the caller's bag.
func (s *Service) CartCoupons(ctx context.Context, userID uuid.UUID) (*CartCouponsResult, error) {
	items, subtotal, err := s.store.CartCoupons(ctx, userID, s.platformCoupons)
	if err != nil {
		return nil, err
	}
	return &CartCouponsResult{SubtotalMinor: subtotal.Int64(), Items: items}, nil
}

// HydrateBestCoupons fills best_coupon on a page of buyer-facing products in
// one query. Fails SOFT, like image hydration: a product grid must not 500
// because the coupon badge could not be read — the field is then absent.
func (s *Service) HydrateBestCoupons(ctx context.Context, products []*postgres.Product) {
	if s.store == nil || len(products) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(products))
	for _, p := range products {
		if p != nil {
			ids = append(ids, p.ID)
		}
	}
	best, err := s.store.BestCouponsForProducts(ctx, ids)
	if err != nil {
		slog.WarnContext(ctx, "commerce: best coupon lookup failed; products served without it", "error", err)
		return
	}
	for _, p := range products {
		if p != nil {
			p.SetBestCoupon(best[p.ID])
		}
	}
}

// ─── Bank offers ────────────────────────────────────────────────────────

// offersReader is the slice of *payments.Client the offer list needs.
type offersReader interface {
	ListOffers(ctx context.Context, amountMinor int64) ([]payments.Offer, error)
}

// paymentOffersTTL is how long the registry's answer is reused. Offers change
// when an operator edits them in the console, not per request.
const paymentOffersTTL = 5 * time.Minute

// offerCache holds payments' UNFILTERED list (amount 0); each request filters
// it by its own amount and the clock, so one entry serves every product page.
type offerCache struct {
	mu    sync.Mutex
	at    time.Time
	items []payments.Offer
	ok    bool
}

// PaymentOfferView is one bank offer as a buyer sees it.
type PaymentOfferView struct {
	ID                     string  `json:"id"`
	Title                  string  `json:"title"`
	Description            string  `json:"description"`
	PaymentMethod          string  `json:"payment_method"`
	DiscountType           string  `json:"discount_type"`
	DiscountValue          int64   `json:"discount_value"`
	MaxDiscountMinor       *int64  `json:"max_discount_minor"`
	MinAmountMinor         int64   `json:"min_amount_minor"`
	EndsAt                 *string `json:"ends_at"`
	EstimatedDiscountMinor int64   `json:"estimated_discount_minor"`
}

// offerBasisPoints is 100% (payments' gateway.OfferBasisPoints).
const offerBasisPoints = 10000

// EstimatedOfferDiscount is payments-service's gateway.AllowedOfferDiscount,
// reproduced for display: the most the offer may take off amountMinor — the
// flat value, or floor(amount × bps / 10000) — capped by max_discount_minor.
// Integer arithmetic only; an unknown type, a percentage above 100% or an
// amount that would overflow allows nothing. It decides nothing about money:
// payments applies its own copy at capture. TestEstimatedOfferDiscountMatchesPayments
// pins the two to payments' own table.
func EstimatedOfferDiscount(amountMinor int64, o payments.Offer) int64 {
	if amountMinor <= 0 || o.DiscountValue <= 0 {
		return 0
	}
	var allowed int64
	switch o.DiscountType {
	case "flat":
		allowed = o.DiscountValue
	case "percentage":
		if o.DiscountValue > offerBasisPoints {
			return 0
		}
		if amountMinor > math.MaxInt64/offerBasisPoints {
			return 0
		}
		allowed = amountMinor * o.DiscountValue / offerBasisPoints
	default:
		return 0
	}
	if o.MaxDiscountMinor != nil && *o.MaxDiscountMinor >= 0 && allowed > *o.MaxDiscountMinor {
		allowed = *o.MaxDiscountMinor
	}
	return allowed
}

// PaymentOffers lists the bank offers applicable to amountMinor, largest
// saving first. Fails SOFT to an empty list (logged) when payments cannot be
// asked and nothing is cached: a product page without its offers is better
// than a product page that will not load.
func (s *Service) PaymentOffers(ctx context.Context, amountMinor int64) []PaymentOfferView {
	out := []PaymentOfferView{}
	all, ok := s.cachedOffers(ctx)
	if !ok {
		return out
	}
	now := s.clockNow()
	for _, o := range all {
		if o.MinAmountMinor > amountMinor {
			continue
		}
		if o.EndsAt != nil {
			if t, err := time.Parse(time.RFC3339, *o.EndsAt); err == nil && !now.Before(t) {
				continue
			}
		}
		out = append(out, PaymentOfferView{
			ID: o.ID, Title: o.Title, Description: o.Description, PaymentMethod: o.PaymentMethod,
			DiscountType: o.DiscountType, DiscountValue: o.DiscountValue,
			MaxDiscountMinor: o.MaxDiscountMinor, MinAmountMinor: o.MinAmountMinor, EndsAt: o.EndsAt,
			EstimatedDiscountMinor: EstimatedOfferDiscount(amountMinor, o),
		})
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].EstimatedDiscountMinor != out[b].EstimatedDiscountMinor {
			return out[a].EstimatedDiscountMinor > out[b].EstimatedDiscountMinor
		}
		return out[a].ID < out[b].ID
	})
	return out
}

func (s *Service) clockNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// cachedOffers returns payments' unfiltered list, refreshed at most every
// paymentOffersTTL. A failed refresh serves the previous answer if there is
// one.
func (s *Service) cachedOffers(ctx context.Context) ([]payments.Offer, bool) {
	if s.offers == nil {
		return nil, false
	}
	s.offerCache.mu.Lock()
	defer s.offerCache.mu.Unlock()
	if s.offerCache.ok && time.Since(s.offerCache.at) < paymentOffersTTL {
		return s.offerCache.items, true
	}
	items, err := s.offers.ListOffers(ctx, 0)
	if err != nil {
		slog.WarnContext(ctx, "commerce: bank offers could not be read from payments", "error", err)
		return s.offerCache.items, s.offerCache.ok
	}
	s.offerCache.items, s.offerCache.at, s.offerCache.ok = items, time.Now(), true
	return items, true
}

// withOffersReader swaps the offers source (tests only).
func (s *Service) withOffersReader(r offersReader) *Service {
	s.offers = r
	return s
}

// ─── Order detail: the payment offer ────────────────────────────────────

// OrderPaymentOfferView is order detail's payment_offer.
type OrderPaymentOfferView struct {
	Title         string `json:"title"`
	DiscountMinor int64  `json:"discount_minor"`
}

// paymentOfferView is order detail's payment_offer and amount_paid_minor.
// Paid means money was captured: paid, or any refund state after it.
func paymentOfferView(order *postgres.Order, o *postgres.OrderPaymentOffer) (*OrderPaymentOfferView, *int64) {
	var view *OrderPaymentOfferView
	if o != nil && o.DiscountMinor != nil && *o.DiscountMinor > 0 {
		title := ""
		if o.Title != nil {
			title = *o.Title
		}
		view = &OrderPaymentOfferView{Title: title, DiscountMinor: *o.DiscountMinor}
	}
	if o != nil && o.CapturedMinor != nil {
		v := *o.CapturedMinor
		return view, &v
	}
	switch order.PaymentStatus {
	case "paid", "refund_pending", "refunded", "partially_refunded":
		v := order.TotalMinor()
		return view, &v
	}
	return view, nil
}

// invoiceBankOfferNote is the invoice's note line for a bank offer, or "".
// A NOTE, not a tax line: the invoice is on the full order value, and the
// bank offer is a payment-side instrument (adviser to confirm).
func invoiceBankOfferNote(o *postgres.OrderPaymentOffer) string {
	if o == nil || o.DiscountMinor == nil || *o.DiscountMinor <= 0 {
		return ""
	}
	return "Bank offer applied at payment: −" + postgres.RupeeText(*o.DiscountMinor)
}
