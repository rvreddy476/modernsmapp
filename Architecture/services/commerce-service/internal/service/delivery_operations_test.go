package service

import (
	"context"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
	"testing"
	"time"
)

type decisionStoreFake struct {
	err   error
	calls int
}

func (s *decisionStoreFake) DeliveryOffer(context.Context, uuid.UUID, uuid.UUID) (postgres.DeliveryOffer, error) {
	return postgres.DeliveryOffer{ReservationID: uuid.New()}, nil
}
func (s *decisionStoreFake) DecideDeliveryOffer(context.Context, uuid.UUID, uuid.UUID, bool) (*postgres.DeliveryJob, error) {
	s.calls++
	return &postgres.DeliveryJob{Status: "assigned"}, s.err
}

type decisionWorkFake struct{ actions []string }

func (*decisionWorkFake) Read(context.Context, uuid.UUID) (partnerwork.State, error) {
	return partnerwork.State{}, nil
}
func (*decisionWorkFake) SetDuty(context.Context, uuid.UUID, *partnerwork.Category, int64) error {
	return nil
}
func (*decisionWorkFake) Offer(context.Context, uuid.UUID, partnerwork.Category, uuid.UUID, time.Time, time.Time) (partnerwork.Reservation, error) {
	return partnerwork.Reservation{}, nil
}
func (w *decisionWorkFake) Decide(_ context.Context, _ uuid.UUID, _ partnerwork.Category, _ uuid.UUID, action string) error {
	w.actions = append(w.actions, action)
	return nil
}

func TestDeliveryDecisionNeverReleasesWinningReservation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		accept    bool
		domainErr error
	}{
		{"stale decline after acceptance", false, postgres.ErrDeliveryWorkConflict},
		{"duplicate accept domain conflict", true, postgres.ErrDeliveryWorkConflict},
		{"duplicate accept idempotent", true, nil},
		{"decline queued for durable release", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, work := &decisionStoreFake{err: tc.domainErr}, &decisionWorkFake{}
			_, err := decideDeliveryOffer(context.Background(), store, work, uuid.New(), uuid.New(), tc.accept)
			if err != tc.domainErr || store.calls != 1 {
				t.Fatalf("decision %v calls %d", err, store.calls)
			}
			for _, action := range work.actions {
				if action == "release" {
					t.Fatal("released an unproven terminal reservation")
				}
			}
			if !tc.accept && len(work.actions) != 0 {
				t.Fatal("decline mutated coordinator before validated domain commit")
			}
		})
	}
}
