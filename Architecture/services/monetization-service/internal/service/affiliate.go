package service

import (
	"context"
	"fmt"
	"math"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateAffiliateLink creates a new affiliate link for a creator pointing to a marketplace listing.
func (s *Service) CreateAffiliateLink(ctx context.Context, creatorID, listingID uuid.UUID, commissionPct float32, commissionFlat *float64) (*postgres.AffiliateLink, error) {
	if commissionPct < 0 || commissionPct > 100 {
		return nil, fmt.Errorf("INVALID_COMMISSION_PCT: must be between 0 and 100")
	}

	l := &postgres.AffiliateLink{
		CreatorID:      creatorID,
		ListingID:      listingID,
		CommissionPct:  commissionPct,
		CommissionFlat: commissionFlat,
	}
	return s.store.CreateAffiliateLink(ctx, l)
}

// GetAffiliateLinkByCode looks up an affiliate link by its short code. It is a
// pure read: it does not count a click (RecordAffiliateClick does).
func (s *Service) GetAffiliateLinkByCode(ctx context.Context, code string) (*postgres.AffiliateLink, error) {
	return s.store.GetAffiliateLinkByCode(ctx, code)
}

// RecordAffiliateClick counts one click on an active link and reports whether
// the code names one.
func (s *Service) RecordAffiliateClick(ctx context.Context, code string) (bool, error) {
	link, err := s.store.GetAffiliateLinkByCode(ctx, code)
	if err != nil || link == nil || !link.IsActive {
		return false, err
	}
	return true, s.store.IncrementAffiliateLinkClick(ctx, code)
}

// GetAffiliateLinkByID returns the affiliate link by its UUID without
// incrementing the click count. Used by post-service's product-tag
// validator — we don't want a tag-creation flow to inflate click
// stats.
func (s *Service) GetAffiliateLinkByID(ctx context.Context, id uuid.UUID) (*postgres.AffiliateLink, error) {
	return s.store.GetAffiliateLinkByID(ctx, id)
}

// ListAffiliateLinks returns paginated active affiliate links for a creator.
func (s *Service) ListAffiliateLinks(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]postgres.AffiliateLink, error) {
	return s.store.GetAffiliateLinksByCreator(ctx, creatorID, limit, offset)
}

// RecordAffiliateConversion records a conversion for the affiliate link
// identified by linkCode, for an order of orderTotalPaise (integer paise).
// The commission is AffiliateCommissionPaise of it.
//
// NOTE (review 2026-11-07): nothing calls this. No order flow reports an
// affiliate conversion yet, so no commission has ever been recorded by it;
// it is kept, in integer paise, for the flow that will.
func (s *Service) RecordAffiliateConversion(ctx context.Context, orderID, buyerID uuid.UUID, linkCode string, orderTotalPaise int64) error {
	if orderTotalPaise <= 0 {
		return fmt.Errorf("INVALID_ORDER_TOTAL: order_total_paise must be greater than zero")
	}
	link, err := s.store.GetAffiliateLinkByCode(ctx, linkCode)
	if err != nil {
		return err
	}
	if link == nil {
		return fmt.Errorf("AFFILIATE_LINK_NOT_FOUND")
	}
	if !link.IsActive {
		return fmt.Errorf("AFFILIATE_LINK_INACTIVE")
	}

	conv := &postgres.AffiliateConversion{
		AffiliateID:     link.ID,
		OrderID:         orderID,
		BuyerID:         buyerID,
		CommissionPaise: AffiliateCommissionPaise(orderTotalPaise, link.CommissionPct, link.CommissionFlat),
	}
	_, err = s.store.RecordAffiliateConversion(ctx, conv)
	return err
}

// AffiliateCommissionPaise is the commission on an order, in integer
// paise. The link stores its percentage as a REAL (5.0 = 5%) and its flat
// commission as NUMERIC(8,2) rupees; each is converted ONCE, rounded to
// the nearest basis point or paisa, and the money itself is integer
// arithmetic: total x bps / 10000, truncated (the platform never rounds a
// commission up). A percentage wins; a flat amount applies only when the
// percentage is zero.
func AffiliateCommissionPaise(orderTotalPaise int64, commissionPct float32, commissionFlat *float64) int64 {
	if orderTotalPaise <= 0 {
		return 0
	}
	if bps := int64(math.Round(float64(commissionPct) * 100)); bps > 0 {
		if bps > 10_000 {
			bps = 10_000
		}
		return orderTotalPaise/10_000*bps + orderTotalPaise%10_000*bps/10_000
	}
	if commissionFlat != nil && *commissionFlat > 0 {
		return int64(math.Round(*commissionFlat * 100))
	}
	return 0
}

// ListAffiliateConversions returns the conversions on the creator's own
// links (optionally one of them), paginated. Ownership is enforced in SQL.
func (s *Service) ListAffiliateConversions(ctx context.Context, creatorID uuid.UUID, linkID *uuid.UUID, limit, offset int) ([]postgres.AffiliateConversion, error) {
	return s.store.GetAffiliateConversionsForCreator(ctx, creatorID, linkID, limit, offset)
}
