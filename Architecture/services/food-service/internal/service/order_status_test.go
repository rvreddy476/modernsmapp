package service

import "testing"

func TestValidateOrderTransition(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr bool
	}{
		{name: "cod placed to confirmed", from: OrderStatusPlaced, to: OrderStatusConfirmed},
		{name: "confirmed to preparing", from: OrderStatusConfirmed, to: OrderStatusPreparing},
		{name: "delivery completes", from: OrderStatusOutForDelivery, to: OrderStatusDelivered},
		{name: "delivered cannot cancel", from: OrderStatusDelivered, to: OrderStatusCancelledByCustomer, wantErr: true},
		{name: "preparing cannot jump to delivered", from: OrderStatusPreparing, to: OrderStatusDelivered, wantErr: true},
		{name: "cancelled paid order can refund", from: OrderStatusCancelledByAdmin, to: OrderStatusRefundPending},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOrderTransition(tt.from, tt.to)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
