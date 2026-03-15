package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/shop-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Storefronts ─────────────────────────────────────────────────────────────

func (s *Service) CreateStorefront(ctx context.Context, sellerID uuid.UUID, handle, displayName, tagline, about string) (*postgres.Storefront, error) {
	if handle == "" {
		return nil, fmt.Errorf("handle is required")
	}
	if displayName == "" {
		return nil, fmt.Errorf("display_name is required")
	}
	sf := &postgres.Storefront{
		SellerID:    sellerID,
		Handle:      handle,
		DisplayName: displayName,
		Tagline:     tagline,
		About:       about,
	}
	return s.store.CreateStorefront(ctx, sf)
}

func (s *Service) GetStorefrontByHandle(ctx context.Context, handle string) (*postgres.Storefront, error) {
	return s.store.GetStorefrontByHandle(ctx, handle)
}

func (s *Service) GetStorefrontBySeller(ctx context.Context, sellerID uuid.UUID) (*postgres.Storefront, error) {
	return s.store.GetStorefrontBySeller(ctx, sellerID)
}

func (s *Service) UpdateStorefront(ctx context.Context, storefrontID, callerID uuid.UUID, displayName, tagline, about string) error {
	sf, err := s.store.GetStorefrontBySeller(ctx, callerID)
	if err != nil {
		return fmt.Errorf("storefront not found for caller")
	}
	if sf.ID != storefrontID {
		return fmt.Errorf("not the storefront owner")
	}
	sf.DisplayName = displayName
	sf.Tagline = tagline
	sf.About = about
	return s.store.UpdateStorefront(ctx, storefrontID, sf)
}

func (s *Service) SetFeaturedListings(ctx context.Context, storefrontID uuid.UUID, listingIDs []uuid.UUID) error {
	return s.store.SetFeaturedListings(ctx, storefrontID, listingIDs)
}

func (s *Service) GetFeaturedListings(ctx context.Context, storefrontID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.GetFeaturedListings(ctx, storefrontID)
}

func (s *Service) CreateCollection(ctx context.Context, storefrontID uuid.UUID, name string, sortOrder int) (*postgres.StorefrontCollection, error) {
	if name == "" {
		return nil, fmt.Errorf("collection name is required")
	}
	c := &postgres.StorefrontCollection{
		StorefrontID: storefrontID,
		Name:         name,
		SortOrder:    sortOrder,
	}
	return s.store.CreateCollection(ctx, c)
}

func (s *Service) GetCollections(ctx context.Context, storefrontID uuid.UUID) ([]postgres.StorefrontCollection, error) {
	return s.store.GetCollections(ctx, storefrontID)
}

func (s *Service) AddListingToCollection(ctx context.Context, collectionID, listingID uuid.UUID, position int) error {
	return s.store.AddListingToCollection(ctx, collectionID, listingID, position)
}

func (s *Service) GetCollectionListings(ctx context.Context, collectionID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.GetCollectionListings(ctx, collectionID)
}

// ─── Product Tags ─────────────────────────────────────────────────────────────

func (s *Service) UpsertPostProductTags(ctx context.Context, postID uuid.UUID, tags []postgres.PostProductTag) error {
	return s.store.UpsertPostProductTags(ctx, postID, tags)
}

func (s *Service) GetPostProductTags(ctx context.Context, postID uuid.UUID) ([]postgres.PostProductTag, error) {
	return s.store.GetPostProductTags(ctx, postID)
}

func (s *Service) IncrementTagClickCount(ctx context.Context, tagID uuid.UUID) error {
	return s.store.IncrementTagClickCount(ctx, tagID)
}

// ─── Wishlist ─────────────────────────────────────────────────────────────────

func (s *Service) GetWishlist(ctx context.Context, userID uuid.UUID) (*postgres.Wishlist, []postgres.WishlistItem, error) {
	wishlist, err := s.store.GetOrCreateDefaultWishlist(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	items, err := s.store.GetWishlistItems(ctx, wishlist.ID, 100, 0)
	if err != nil {
		return wishlist, nil, err
	}
	return wishlist, items, nil
}

func (s *Service) AddToWishlist(ctx context.Context, userID, listingID uuid.UUID) error {
	wishlist, err := s.store.GetOrCreateDefaultWishlist(ctx, userID)
	if err != nil {
		return err
	}
	return s.store.AddToWishlist(ctx, wishlist.ID, listingID)
}

func (s *Service) RemoveFromWishlist(ctx context.Context, userID, listingID uuid.UUID) error {
	wishlist, err := s.store.GetOrCreateDefaultWishlist(ctx, userID)
	if err != nil {
		return err
	}
	return s.store.RemoveFromWishlist(ctx, wishlist.ID, listingID)
}

func (s *Service) CreateStockAlert(ctx context.Context, userID, listingID uuid.UUID) error {
	return s.store.CreateStockAlert(ctx, userID, listingID)
}

func (s *Service) RemoveStockAlert(ctx context.Context, userID, listingID uuid.UUID) error {
	return s.store.RemoveStockAlert(ctx, userID, listingID)
}

// ─── Group Buy ────────────────────────────────────────────────────────────────

func (s *Service) CreateGroupBuy(ctx context.Context, listingID, initiatorID uuid.UUID, targetQty int, discountedPrice, originalPrice float64, expiresAt time.Time) (*postgres.GroupBuy, error) {
	if targetQty <= 1 {
		return nil, fmt.Errorf("target_qty must be greater than 1")
	}
	if discountedPrice <= 0 || originalPrice <= 0 {
		return nil, fmt.Errorf("prices must be positive")
	}
	if discountedPrice >= originalPrice {
		return nil, fmt.Errorf("discounted_price must be less than original_price")
	}
	if expiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("expires_at must be in the future")
	}

	g := &postgres.GroupBuy{
		ListingID:       listingID,
		InitiatorID:     initiatorID,
		TargetQty:       targetQty,
		DiscountedPrice: discountedPrice,
		OriginalPrice:   originalPrice,
		ExpiresAt:       expiresAt,
	}
	return s.store.CreateGroupBuy(ctx, g)
}

func (s *Service) JoinGroupBuy(ctx context.Context, groupBuyID, userID uuid.UUID, paymentIntentID *uuid.UUID) error {
	gb, err := s.store.GetGroupBuy(ctx, groupBuyID)
	if err != nil {
		return fmt.Errorf("group buy not found")
	}
	if gb.Status != "open" {
		return fmt.Errorf("group buy is not open (status: %s)", gb.Status)
	}
	if gb.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("group buy has expired")
	}
	return s.store.JoinGroupBuy(ctx, groupBuyID, userID, paymentIntentID)
}

func (s *Service) GetGroupBuy(ctx context.Context, id uuid.UUID) (*postgres.GroupBuy, error) {
	return s.store.GetGroupBuy(ctx, id)
}

func (s *Service) ListActiveGroupBuys(ctx context.Context, listingID uuid.UUID) ([]postgres.GroupBuy, error) {
	return s.store.ListActiveGroupBuys(ctx, listingID)
}

func (s *Service) GetGroupBuyParticipants(ctx context.Context, groupBuyID uuid.UUID) ([]postgres.GroupBuyParticipant, error) {
	return s.store.GetGroupBuyParticipants(ctx, groupBuyID)
}

// ─── Ads ──────────────────────────────────────────────────────────────────────

func (s *Service) CreateAdCampaign(ctx context.Context, advertiserID uuid.UUID, name, objective, budgetType string, budgetAmount float64, startsAt time.Time) (*postgres.AdCampaign, error) {
	if name == "" {
		return nil, fmt.Errorf("campaign name is required")
	}
	c := &postgres.AdCampaign{
		AdvertiserID:          advertiserID,
		Name:                  name,
		Objective:             objective,
		BudgetType:            budgetType,
		BudgetAmount:          budgetAmount,
		Currency:              "INR",
		StartsAt:              startsAt,
		AttributionWindowDays: 7,
	}
	return s.store.CreateAdCampaign(ctx, c)
}

func (s *Service) GetAdCampaign(ctx context.Context, id uuid.UUID) (*postgres.AdCampaign, error) {
	return s.store.GetAdCampaign(ctx, id)
}

func (s *Service) ListAdCampaigns(ctx context.Context, advertiserID uuid.UUID, status string, limit, offset int) ([]postgres.AdCampaign, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.ListAdCampaigns(ctx, advertiserID, status, limit, offset)
}

func (s *Service) UpdateAdCampaignStatus(ctx context.Context, campaignID uuid.UUID, status string) error {
	validStatuses := map[string]bool{"draft": true, "review": true, "active": true, "paused": true, "completed": true, "rejected": true}
	if !validStatuses[status] {
		return fmt.Errorf("invalid status: %s", status)
	}
	return s.store.UpdateAdCampaignStatus(ctx, campaignID, status)
}

func (s *Service) CreateAdSet(ctx context.Context, as *postgres.AdSet) (*postgres.AdSet, error) {
	return s.store.CreateAdSet(ctx, as)
}

func (s *Service) CreateAdCreative(ctx context.Context, c *postgres.AdCreative) (*postgres.AdCreative, error) {
	return s.store.CreateAdCreative(ctx, c)
}

func (s *Service) UpsertAdPerformance(ctx context.Context, p *postgres.AdPerformance) error {
	return s.store.UpsertAdPerformance(ctx, p)
}

func (s *Service) GetAdPerformance(ctx context.Context, campaignID uuid.UUID, startDate, endDate time.Time) ([]postgres.AdPerformance, error) {
	return s.store.GetAdPerformance(ctx, campaignID, startDate, endDate)
}

func (s *Service) SetAdFrequencyCap(ctx context.Context, campaignID uuid.UUID, perDay, perWeek int) error {
	return s.store.SetAdFrequencyCap(ctx, campaignID, perDay, perWeek)
}

// CheckAdFrequency checks Redis for ad frequency cap compliance.
// Returns true if the user is under the cap (ad may be shown).
func (s *Service) CheckAdFrequency(ctx context.Context, campaignID, userID uuid.UUID) bool {
	if s.rdb == nil {
		return true
	}

	today := time.Now().UTC().Format("2006-01-02")
	key := fmt.Sprintf("ad_freq:%s:%s:%s", campaignID, userID, today)

	count, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		return true
	}
	// Set TTL on first increment
	if count == 1 {
		s.rdb.Expire(ctx, key, 24*time.Hour)
	}

	// Default cap = 3 per day; a real implementation would fetch from ad_frequency_caps
	const defaultDailyCap = 3
	return count <= defaultDailyCap
}
