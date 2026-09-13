package service

import (
	"context"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/gst"
	"github.com/google/uuid"
)

// Lane B8: the partner read-backs, readiness, single reads and menu extras
// the Feast Kitchen app needs. Ownership is enforced by the store; a foreign
// restaurant, order, item, group, variant or add-on is pgx.ErrNoRows.

// WithMediaBaseURL sets the base the dish-photo URL is built on (default:
// FOOD_MEDIA_PUBLIC_BASE_URL; empty keeps the gateway-relative path).
func (s *Service) WithMediaBaseURL(base string) *Service {
	s.mediaBaseURL = base
	return s
}

// resolveMenuImage turns an uploaded dish photo's media id into the URL the
// item stores. Without a media id, image_url is used as given.
func (s *Service) resolveMenuImage(in *postgres.MenuItemInput) {
	if in.ImageMediaID != nil {
		in.ImageURL = onboarding.MediaServeURL(s.mediaBaseURL, *in.ImageMediaID)
	}
}

func (s *Service) GetRestaurantReadiness(ctx context.Context, ownerID, restaurantID uuid.UUID) (*postgres.RestaurantReadiness, error) {
	return s.store.GetRestaurantReadiness(ctx, ownerID, restaurantID)
}

// GetRestaurantCompliance returns the PUT's shape: the stored, masked record
// plus who owes GST for the saved category. No key is needed: nothing sealed
// is read.
func (s *Service) GetRestaurantCompliance(ctx context.Context, ownerID, restaurantID uuid.UUID) (*ComplianceView, error) {
	rec, err := s.store.GetRestaurantCompliance(ctx, ownerID, restaurantID)
	if err != nil {
		return nil, err
	}
	view := &ComplianceView{RestaurantCompliance: *rec}
	if row, err := onboardingRateTable.Lookup(gst.Category(rec.TaxCategory), s.onboardingClock()); err == nil {
		view.GSTLiability = string(onboarding.LiabilityOf(row))
		view.GSTINRequired = !row.ECOSection95
	}
	return view, nil
}

func (s *Service) GetRestaurantLocation(ctx context.Context, ownerID, restaurantID uuid.UUID) (*postgres.RestaurantLocation, error) {
	return s.store.GetRestaurantLocation(ctx, ownerID, restaurantID)
}

func (s *Service) GetOperatingHours(ctx context.Context, ownerID, restaurantID uuid.UUID) (*postgres.OperatingHours, error) {
	return s.store.GetOperatingHours(ctx, ownerID, restaurantID)
}

func (s *Service) GetRestaurantFSSAI(ctx context.Context, ownerID, restaurantID uuid.UUID) (*postgres.RestaurantFSSAIView, error) {
	return s.store.GetRestaurantFSSAI(ctx, ownerID, restaurantID)
}

func (s *Service) GetPartnerOrder(ctx context.Context, ownerID, orderID uuid.UUID) (*postgres.Order, error) {
	return s.store.GetPartnerOrder(ctx, ownerID, orderID)
}

func (s *Service) GetPartnerMenuItem(ctx context.Context, ownerID, itemID uuid.UUID) (*postgres.PartnerMenuItem, error) {
	return s.store.GetPartnerMenuItem(ctx, ownerID, itemID)
}

func (s *Service) ListMenuVariants(ctx context.Context, ownerID, itemID uuid.UUID) ([]postgres.MenuVariant, error) {
	return s.store.ListMenuVariants(ctx, ownerID, itemID)
}

func (s *Service) CreateMenuVariant(ctx context.Context, ownerID, itemID uuid.UUID, in postgres.MenuPriceInput) (*postgres.MenuVariant, error) {
	return s.store.CreateMenuVariant(ctx, ownerID, itemID, in)
}

func (s *Service) UpdateMenuVariant(ctx context.Context, ownerID, itemID, variantID uuid.UUID, in postgres.MenuPriceInput) (*postgres.MenuVariant, error) {
	return s.store.UpdateMenuVariant(ctx, ownerID, itemID, variantID, in)
}

func (s *Service) DeleteMenuVariant(ctx context.Context, ownerID, itemID, variantID uuid.UUID) error {
	return s.store.DeleteMenuVariant(ctx, ownerID, itemID, variantID)
}

func (s *Service) ListAddonGroups(ctx context.Context, ownerID, itemID uuid.UUID) ([]postgres.MenuAddonGroup, error) {
	return s.store.ListAddonGroups(ctx, ownerID, itemID)
}

func (s *Service) CreateAddonGroup(ctx context.Context, ownerID, itemID uuid.UUID, in postgres.MenuAddonGroupInput) (*postgres.MenuAddonGroup, error) {
	return s.store.CreateAddonGroup(ctx, ownerID, itemID, in)
}

func (s *Service) UpdateAddonGroup(ctx context.Context, ownerID, itemID, groupID uuid.UUID, in postgres.MenuAddonGroupInput) (*postgres.MenuAddonGroup, error) {
	return s.store.UpdateAddonGroup(ctx, ownerID, itemID, groupID, in)
}

func (s *Service) DeleteAddonGroup(ctx context.Context, ownerID, itemID, groupID uuid.UUID) error {
	return s.store.DeleteAddonGroup(ctx, ownerID, itemID, groupID)
}

func (s *Service) CreateAddon(ctx context.Context, ownerID, itemID, groupID uuid.UUID, in postgres.MenuPriceInput) (*postgres.MenuAddon, error) {
	return s.store.CreateAddon(ctx, ownerID, itemID, groupID, in)
}

func (s *Service) UpdateAddon(ctx context.Context, ownerID, itemID, groupID, addonID uuid.UUID, in postgres.MenuPriceInput) (*postgres.MenuAddon, error) {
	return s.store.UpdateAddon(ctx, ownerID, itemID, groupID, addonID, in)
}

func (s *Service) DeleteAddon(ctx context.Context, ownerID, itemID, groupID, addonID uuid.UUID) error {
	return s.store.DeleteAddon(ctx, ownerID, itemID, groupID, addonID)
}
