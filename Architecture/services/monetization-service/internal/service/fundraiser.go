package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/monetization-service/internal/donation"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/google/uuid"
)

// CreateFundraiser creates a new fundraising campaign. The goal is integer
// paise.
func (s *Service) CreateFundraiser(ctx context.Context, creatorID uuid.UUID, fType, title, description string, goalPaise int64, endsAt *time.Time) (*postgres.Fundraiser, error) {
	if goalPaise <= 0 {
		return nil, fmt.Errorf("INVALID_GOAL: goal_paise must be greater than zero")
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
		GoalPaise:   goalPaise,
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

// PendingDonation is what Donate returns: a donation the donor has asked
// to make, and what payments-service must be told to collect for it. It
// counts for nothing until the signed payment.succeeded event for exactly
// this reference, amount, currency and payer is applied.
type PendingDonation struct {
	Donation *postgres.Donation `json:"donation"`
	// The payments reference the intent must carry.
	ApplicationID string    `json:"application_id"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   uuid.UUID `json:"reference_id"`
	AmountMinor   int64     `json:"amount_minor"`
	Currency      string    `json:"currency"`
}

// Donate records a pending donation of amountPaise (integer paise) from
// donorID. It does not touch the fundraiser's total: a donation counts only
// on payments-service's signed event (ApplyDonationPaymentEvent). The
// fundraiser must be active.
func (s *Service) Donate(ctx context.Context, fundraiserID, donorID uuid.UUID, amountPaise int64, isAnonymous bool, message *string) (*PendingDonation, error) {
	if amountPaise <= 0 {
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

	d, err := s.store.CreatePendingDonation(ctx, &postgres.Donation{
		FundraiserID: fundraiserID,
		DonorID:      donorID,
		AmountPaise:  amountPaise,
		Currency:     f.Currency,
		IsAnonymous:  isAnonymous,
		Message:      message,
	})
	if err != nil {
		return nil, err
	}
	return &PendingDonation{
		Donation:      d,
		ApplicationID: donation.ApplicationID,
		ReferenceType: donation.RefType,
		ReferenceID:   d.ID,
		AmountMinor:   d.AmountPaise,
		Currency:      d.Currency,
	}, nil
}

// ApplyDonationPaymentEvent applies one payments-service event to the
// donation it references. The ONLY path by which a donation counts.
func (s *Service) ApplyDonationPaymentEvent(ctx context.Context, ev donation.Event) (donation.Applied, error) {
	applied, err := s.store.ApplyDonationPaymentEvent(ctx, ev)
	if err != nil {
		return applied, err
	}
	if applied.Decision.Outcome == donation.OutcomeMismatch {
		slog.Error("fundraisers: DONATION PAYMENT MISMATCH — nothing counted",
			"donation_id", ev.DonationID, "event_id", ev.EventID, "event_type", ev.EventType,
			"event_amount_minor", ev.AmountMinor, "detail", applied.Decision.Detail)
	}
	return applied, nil
}

// PublicDonation is a donation as a viewer sees it. DonorID is nil for an
// anonymous donation unless the viewer is that donor.
type PublicDonation struct {
	ID            uuid.UUID  `json:"id"`
	FundraiserID  uuid.UUID  `json:"fundraiser_id"`
	DonorID       *uuid.UUID `json:"donor_id,omitempty"`
	AmountPaise   int64      `json:"amount_paise"`
	RefundedPaise int64      `json:"refunded_paise"`
	Currency      string     `json:"currency"`
	Status        string     `json:"status"`
	IsAnonymous   bool       `json:"is_anonymous"`
	Message       *string    `json:"message,omitempty"`
	ReceiptURL    *string    `json:"receipt_url,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// redactDonations is the privacy gate on the donor list: an anonymous
// donor's id is shown to that donor only. Not to the public, not to the
// fundraiser's owner. viewer is uuid.Nil for a signed-out caller.
func redactDonations(in []postgres.Donation, viewer uuid.UUID) []PublicDonation {
	out := make([]PublicDonation, 0, len(in))
	for _, d := range in {
		p := PublicDonation{
			ID: d.ID, FundraiserID: d.FundraiserID, AmountPaise: d.AmountPaise, RefundedPaise: d.RefundedPaise,
			Currency: d.Currency, Status: d.Status, IsAnonymous: d.IsAnonymous,
			Message: d.Message, ReceiptURL: d.ReceiptURL, CreatedAt: d.CreatedAt,
		}
		if !d.IsAnonymous || (viewer != uuid.Nil && viewer == d.DonorID) {
			id := d.DonorID
			p.DonorID = &id
		}
		out = append(out, p)
	}
	return out
}

// GetDonationsByFundraiser returns the paginated donations that count, as
// viewer may see them.
func (s *Service) GetDonationsByFundraiser(ctx context.Context, viewer, fundraiserID uuid.UUID, limit, offset int) ([]PublicDonation, error) {
	rows, err := s.store.GetDonationsByFundraiser(ctx, fundraiserID, limit, offset)
	if err != nil {
		return nil, err
	}
	return redactDonations(rows, viewer), nil
}
