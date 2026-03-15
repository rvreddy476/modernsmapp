package service

import (
	"context"
	"fmt"

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

// GetAffiliateLinkByCode looks up an affiliate link by its short code and increments the click count.
func (s *Service) GetAffiliateLinkByCode(ctx context.Context, code string) (*postgres.AffiliateLink, error) {
	link, err := s.store.GetAffiliateLinkByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, nil
	}

	// Increment click count asynchronously — a failure here is non-fatal.
	_ = s.store.IncrementAffiliateLinkClick(ctx, code)

	return link, nil
}

// ListAffiliateLinks returns paginated active affiliate links for a creator.
func (s *Service) ListAffiliateLinks(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]postgres.AffiliateLink, error) {
	return s.store.GetAffiliateLinksByCreator(ctx, creatorID, limit, offset)
}

// RecordAffiliateConversion records a conversion for the affiliate link identified by linkCode.
// The commission amount is computed from the link's commission_pct applied to the order total,
// falling back to commission_flat if set and pct is zero.
func (s *Service) RecordAffiliateConversion(ctx context.Context, orderID, buyerID uuid.UUID, linkCode string, orderTotal float64) error {
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

	// Compute commission amount.
	var commissionAmt float64
	if link.CommissionPct > 0 {
		commissionAmt = orderTotal * float64(link.CommissionPct) / 100.0
	} else if link.CommissionFlat != nil {
		commissionAmt = *link.CommissionFlat
	}

	conv := &postgres.AffiliateConversion{
		AffiliateID:   link.ID,
		OrderID:       orderID,
		BuyerID:       buyerID,
		CommissionAmt: commissionAmt,
	}
	_, err = s.store.RecordAffiliateConversion(ctx, conv)
	return err
}

// ListAffiliateConversions returns paginated conversions for an affiliate link.
func (s *Service) ListAffiliateConversions(ctx context.Context, affiliateID uuid.UUID, limit, offset int) ([]postgres.AffiliateConversion, error) {
	return s.store.GetAffiliateConversions(ctx, affiliateID, limit, offset)
}
