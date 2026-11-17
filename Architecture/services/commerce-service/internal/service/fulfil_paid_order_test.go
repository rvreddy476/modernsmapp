package service

// com-1: the fulfilment job that runs about a second after payment booked the
// courier and moved the order to `shipped` before the seller had packed it.
// It now issues the invoice only; booking is the seller's Ship.
//
// The Service has a courier and no store. The old job reached the store to
// read the order before booking, so it panicked here; the new one touches
// neither the store nor the courier.

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/google/uuid"
)

// bookingCountCourier counts bookings and answers nothing else of interest.
type bookingCountCourier struct{ bookings *int32 }

func (bookingCountCourier) Name() string { return "counting" }
func (c bookingCountCourier) CreateShipment(context.Context, courier.ShipmentRequest) (*courier.ShipmentResponse, error) {
	atomic.AddInt32(c.bookings, 1)
	return &courier.ShipmentResponse{AWBNumber: "AWB-TEST"}, nil
}
func (bookingCountCourier) CancelShipment(context.Context, string) error { return nil }
func (bookingCountCourier) ParseWebhook(context.Context, []byte) ([]courier.TrackingUpdate, error) {
	return nil, nil
}
func (bookingCountCourier) VerifyWebhook(map[string]string, []byte) error { return nil }
func (bookingCountCourier) CheckServiceability(context.Context, courier.ServiceabilityRequest) (*courier.ServiceabilityResult, error) {
	return &courier.ServiceabilityResult{Serviceable: true}, nil
}

func TestFulfillPaidOrderJobDoesNotBookOrShip(t *testing.T) {
	var bookings int32
	s := &Service{courier: bookingCountCourier{bookings: &bookings}}
	orderID := uuid.New()

	for run := 1; run <= 2; run++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("run %d: the job reached the order store to book a shipment (panic: %v); "+
						"a paid order must stay confirmed until its seller ships it", run, r)
				}
			}()
			if err := s.FulfillPaidOrderJob(context.Background(), orderID); err != nil {
				t.Fatalf("run %d: %v", run, err)
			}
		}()
	}
	if n := atomic.LoadInt32(&bookings); n != 0 {
		t.Fatalf("the courier was booked %d time(s) by the payment job, want 0", n)
	}
}
