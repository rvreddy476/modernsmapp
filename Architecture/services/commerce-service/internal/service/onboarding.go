package service

import (
	"context"
	"fmt"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// ─── Onboarding wizard ───────────────────────────────────────────

type StartOnboardingInput struct {
	UserID         uuid.UUID
	BusinessPageID *uuid.UUID
	StoreName      string
	Email          string
	SellerType     string
	BusinessType   string
}

// StartOnboarding creates a draft seller record. Idempotent — returns existing if already started.
func (s *Service) StartOnboarding(ctx context.Context, in StartOnboardingInput) (*postgres.Seller, error) {
	// Return existing draft if present
	existing, err := s.store.GetSellerByUserID(ctx, in.UserID)
	if err == nil {
		return existing, nil
	}

	if in.StoreName == "" {
		return nil, fmt.Errorf("store_name is required")
	}
	slug := uniqueSlug(slugify(in.StoreName))
	sel := &postgres.Seller{
		UserID:         in.UserID,
		BusinessPageID: in.BusinessPageID,
		StoreName:      in.StoreName,
		Email:          in.Email,
		Slug:           slug,
		SellerType:     coalesceStr(in.SellerType, "individual"),
		BusinessType:   coalesceStr(in.BusinessType, "individual"),
	}
	if err := s.store.StartSellerOnboarding(ctx, sel); err != nil {
		return nil, fmt.Errorf("start onboarding: %w", err)
	}
	return sel, nil
}

// GetOnboardingStatus returns the current seller draft/status for a user.
func (s *Service) GetOnboardingStatus(ctx context.Context, userID uuid.UUID) (*postgres.Seller, error) {
	return s.store.GetSellerOnboardingStatus(ctx, userID)
}

// SaveBasicInfo saves step 3 fields.
func (s *Service) SaveBasicInfo(ctx context.Context, userID uuid.UUID, in postgres.OnboardingBasicInput) error {
	if in.StoreName == "" || in.Email == "" {
		return fmt.Errorf("store_name and email are required")
	}
	if in.SellerType == "" {
		in.SellerType = "individual"
	}
	if in.BusinessType == "" {
		in.BusinessType = "individual"
	}
	return s.store.SaveOnboardingBasic(ctx, userID, in)
}

// SaveStorefront saves step 4 fields.
func (s *Service) SaveStorefront(ctx context.Context, userID uuid.UUID, in postgres.OnboardingStorefrontInput) error {
	return s.store.SaveOnboardingStorefront(ctx, userID, in)
}

// SaveDocuments saves step 5 KYC documents.
func (s *Service) SaveDocuments(ctx context.Context, userID uuid.UUID, docs []postgres.SellerDocument) error {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("seller not found: %w", err)
	}
	return s.store.SaveOnboardingCompliance(ctx, sel.ID, docs)
}

// SaveFulfillment saves step 6 fields.
func (s *Service) SaveFulfillment(ctx context.Context, userID uuid.UUID, in postgres.OnboardingFulfillmentInput) error {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("seller not found: %w", err)
	}
	return s.store.SaveOnboardingFulfillment(ctx, sel.ID, in)
}

// SavePayout saves step 7 bank details.
func (s *Service) SavePayout(ctx context.Context, userID uuid.UUID, in postgres.OnboardingPayoutInput) error {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("seller not found: %w", err)
	}
	return s.store.SaveOnboardingPayout(ctx, sel.ID, in)
}

// SubmitApplication submits the seller application for review.
func (s *Service) SubmitApplication(ctx context.Context, userID uuid.UUID) error {
	if err := s.store.SubmitSellerApplication(ctx, userID); err != nil {
		return err
	}
	s.publish(ctx, events.EventSellerSubmitted, map[string]any{"user_id": userID})
	return nil
}

// GetDashboard returns seller dashboard stats.
func (s *Service) GetDashboard(ctx context.Context, userID uuid.UUID) (*postgres.DashboardStats, error) {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("seller not found")
	}
	return s.store.GetDashboardStats(ctx, sel.ID)
}

// SubmitProduct submits a product for admin review.
func (s *Service) SubmitProduct(ctx context.Context, productID, userID uuid.UUID) error {
	sel, err := s.store.GetSellerByUserID(ctx, userID)
	if err != nil {
		return fmt.Errorf("seller not found")
	}
	return s.store.SubmitProductForReview(ctx, productID, sel.ID)
}

// ─── Internal admin operations (called by admin-service) ─────────

func (s *Service) AdminListSellerQueue(ctx context.Context, limit, offset int) ([]*postgres.Seller, int, error) {
	return s.store.ListSellerQueue(ctx, limit, offset)
}

func (s *Service) AdminGetSeller(ctx context.Context, sellerID uuid.UUID) (*postgres.Seller, error) {
	return s.store.GetSellerByID(ctx, sellerID)
}

func (s *Service) AdminApproveSeller(ctx context.Context, sellerID, actorID uuid.UUID, notes string) error {
	if err := s.store.ApproveSellerByAdmin(ctx, sellerID, actorID, notes); err != nil {
		return err
	}
	// Include business_page_id so user-service can activate the page
	payload := map[string]any{"seller_id": sellerID, "actor_id": actorID}
	sel, err := s.store.GetSellerByID(ctx, sellerID)
	if err == nil && sel.BusinessPageID != nil {
		payload["business_page_id"] = *sel.BusinessPageID
	}
	s.publish(ctx, events.EventSellerApproved, payload)
	return nil
}

func (s *Service) AdminRejectSeller(ctx context.Context, sellerID, actorID uuid.UUID, reason, notes string) error {
	if err := s.store.RejectSellerByAdmin(ctx, sellerID, actorID, reason, notes); err != nil {
		return err
	}
	s.publish(ctx, events.EventSellerRejected, map[string]any{"seller_id": sellerID, "reason": reason})
	return nil
}

func (s *Service) AdminRequestSellerChanges(ctx context.Context, sellerID, actorID uuid.UUID, changes, notes string) error {
	return s.store.RequestSellerChanges(ctx, sellerID, actorID, changes, notes)
}

func (s *Service) AdminSuspendSeller(ctx context.Context, sellerID, actorID uuid.UUID, reason, notes string) error {
	if err := s.store.SuspendSellerByAdmin(ctx, sellerID, actorID, reason, notes); err != nil {
		return err
	}
	s.publish(ctx, events.EventSellerSuspended, map[string]any{"seller_id": sellerID, "reason": reason})
	return nil
}

func (s *Service) AdminListProductQueue(ctx context.Context, limit, offset int) ([]*postgres.Product, int, error) {
	return s.store.ListProductQueue(ctx, limit, offset)
}

func (s *Service) AdminApproveProduct(ctx context.Context, productID, actorID uuid.UUID, notes string) error {
	if err := s.store.ApproveProductByAdmin(ctx, productID, actorID, notes); err != nil {
		return err
	}
	s.publish(ctx, events.EventProductApproved, map[string]any{"product_id": productID})
	return nil
}

func (s *Service) AdminRejectProduct(ctx context.Context, productID, actorID uuid.UUID, reason string) error {
	return s.store.RejectProductByAdmin(ctx, productID, actorID, reason)
}
