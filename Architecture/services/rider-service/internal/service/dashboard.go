package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// PartnerDashboard is the response shape for GET /partners/me/dashboard.
type PartnerDashboard struct {
	Partner          *store.Partner             `json:"partner"`
	Subscription     *store.PartnerSubscription `json:"subscription,omitempty"`
	Plan             *store.SubscriptionPlan    `json:"plan,omitempty"`
	LeadsUsed        int                        `json:"leads_used"`
	LeadAllotment    *int                       `json:"lead_allotment,omitempty"`
	Today            store.PartnerEarningsSummary `json:"today"`
	Rating           float64                    `json:"rating"`
	AcceptanceRate   float64                    `json:"acceptance_rate"`
	CancellationRate float64                    `json:"cancellation_rate"`
	IsOnline         bool                       `json:"is_online"`
}

// GetPartnerDashboard composes the partner-mobile home screen response.
func (s *Service) GetPartnerDashboard(ctx context.Context, partnerUserID uuid.UUID) (*PartnerDashboard, error) {
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: partner")
		}
		return nil, err
	}
	out := &PartnerDashboard{
		Partner:          partner,
		Rating:           partner.Rating,
		AcceptanceRate:   partner.AcceptanceRate,
		CancellationRate: partner.CancellationRate,
		IsOnline:         partner.IsOnline,
	}
	if sub, err := s.store.GetActiveSubscription(ctx, partner.ID); err == nil {
		out.Subscription = sub
		out.LeadsUsed = sub.LeadsUsed
		if plan, err := s.store.GetPlan(ctx, sub.PlanID); err == nil {
			out.Plan = plan
			out.LeadAllotment = plan.LeadLimit
		}
	}
	since := time.Now().UTC().Truncate(24 * time.Hour)
	if today, err := s.store.PartnerEarnings(ctx, partner.ID, since); err == nil {
		out.Today = *today
	}
	return out, nil
}

// PartnerEarningsResult is the response for GET /partners/me/earnings?period=…
type PartnerEarningsResult struct {
	Period       string       `json:"period"`
	Since        time.Time    `json:"since"`
	RideCount    int          `json:"ride_count"`
	EarningPaise int64        `json:"earning_paise"`
	Rides        []store.Ride `json:"rides"`
}

// GetPartnerEarnings returns the partner's completed-ride count + sum of
// final fares for the given period (today | week | month).
func (s *Service) GetPartnerEarnings(ctx context.Context, partnerUserID uuid.UUID, period string) (*PartnerEarningsResult, error) {
	partner, err := s.store.GetPartnerByUserID(ctx, partnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrPartnerNotFound) {
			return nil, fmt.Errorf("not_found: partner")
		}
		return nil, err
	}
	since := earningsSince(period)
	summary, err := s.store.PartnerEarnings(ctx, partner.ID, since)
	if err != nil {
		return nil, err
	}
	rides, err := s.store.ListRidesByPartner(ctx, partner.ID, since, 200)
	if err != nil {
		return nil, err
	}
	return &PartnerEarningsResult{
		Period:       period,
		Since:        since,
		RideCount:    summary.RideCount,
		EarningPaise: summary.EarningPaise,
		Rides:        rides,
	}, nil
}

func earningsSince(period string) time.Time {
	now := time.Now().UTC()
	switch period {
	case "week":
		return now.Add(-7 * 24 * time.Hour)
	case "month":
		return now.AddDate(0, -1, 0)
	default:
		return now.Truncate(24 * time.Hour)
	}
}
