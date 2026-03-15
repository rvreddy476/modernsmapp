package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateFundraiser creates a new fundraising campaign.
func (s *Service) CreateFundraiser(ctx context.Context, creatorID uuid.UUID, fType, title, description string, goalAmount float64, endsAt *time.Time) (*postgres.Fundraiser, error) {
	if goalAmount <= 0 {
		return nil, fmt.Errorf("INVALID_GOAL: goal_amount must be greater than zero")
	}
	validTypes := map[string]bool{"personal": true, "community": true, "ngo": true, "emergency": true}
	if !validTypes[fType] {
		return nil, fmt.Errorf("INVALID_TYPE: must be one of personal, community, ngo, emergency")
	}

	f := &postgres.Fundraiser{
		CreatorID:   creatorID,
		Type:        fType,
		Title:       title,
		Description: description,
		GoalAmount:  goalAmount,
		Currency:    "INR",
		EndsAt:      endsAt,
	}
	return s.store.CreateFundraiser(ctx, f)
}

// GetFundraiser returns a fundraiser by ID.
func (s *Service) GetFundraiser(ctx context.Context, id uuid.UUID) (*postgres.Fundraiser, error) {
	return s.store.GetFundraiser(ctx, id)
}

// ListActiveFundraisers returns paginated active fundraisers.
func (s *Service) ListActiveFundraisers(ctx context.Context, limit, offset int) ([]postgres.Fundraiser, error) {
	return s.store.ListActiveFundraisers(ctx, limit, offset)
}

// ListMyFundraisers returns all fundraisers created by the given creator.
func (s *Service) ListMyFundraisers(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]postgres.Fundraiser, error) {
	return s.store.ListFundraisersByCreator(ctx, creatorID, limit, offset)
}

// PauseFundraiser pauses an active fundraiser. Only the creator can pause it.
func (s *Service) PauseFundraiser(ctx context.Context, creatorID, id uuid.UUID) error {
	f, err := s.store.GetFundraiser(ctx, id)
	if err != nil {
		return err
	}
	if f == nil {
		return fmt.Errorf("FUNDRAISER_NOT_FOUND")
	}
	if f.CreatorID != creatorID {
		return fmt.Errorf("FORBIDDEN: not the fundraiser owner")
	}
	if f.Status != "active" {
		return fmt.Errorf("FUNDRAISER_NOT_ACTIVE: current status is %s", f.Status)
	}
	return s.store.UpdateFundraiserStatus(ctx, id, "paused")
}

// Donate creates a donation for a fundraiser.
// The fundraiser must be active. If the donation causes raised_amount >= goal_amount
// the store will automatically mark it as 'completed'.
func (s *Service) Donate(ctx context.Context, fundraiserID, donorID, paymentIntentID uuid.UUID, amount float64, isAnonymous bool, message *string) (*postgres.Donation, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("INVALID_AMOUNT: donation must be greater than zero")
	}

	f, err := s.store.GetFundraiser(ctx, fundraiserID)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return nil, fmt.Errorf("FUNDRAISER_NOT_FOUND")
	}
	if f.Status != "active" {
		return nil, fmt.Errorf("FUNDRAISER_NOT_ACTIVE: cannot donate to a %s fundraiser", f.Status)
	}

	d := &postgres.Donation{
		FundraiserID:    fundraiserID,
		DonorID:         donorID,
		Amount:          amount,
		Currency:        f.Currency,
		PaymentIntentID: paymentIntentID,
		IsAnonymous:     isAnonymous,
		Message:         message,
	}
	return s.store.CreateDonation(ctx, d)
}

// GetDonationsByFundraiser returns paginated donations for a fundraiser.
func (s *Service) GetDonationsByFundraiser(ctx context.Context, fundraiserID uuid.UUID, limit, offset int) ([]postgres.Donation, error) {
	return s.store.GetDonationsByFundraiser(ctx, fundraiserID, limit, offset)
}
