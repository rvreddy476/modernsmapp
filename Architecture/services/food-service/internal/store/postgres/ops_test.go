package postgres

import "testing"

func TestPartnerTransitionAllowed(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{name: "confirmed to preparing", from: "CONFIRMED", to: "PREPARING", want: true},
		{name: "confirmed to rejected", from: "CONFIRMED", to: "RESTAURANT_REJECTED", want: true},
		{name: "preparing to ready", from: "PREPARING", to: "READY_FOR_PICKUP", want: true},
		{name: "confirmed cannot jump ready", from: "CONFIRMED", to: "READY_FOR_PICKUP", want: false},
		{name: "delivered is terminal for partner", from: "DELIVERED", to: "PREPARING", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := partnerTransitionAllowed(tt.from, tt.to); got != tt.want {
				t.Fatalf("partnerTransitionAllowed(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestDeliveryTransitionAllowed(t *testing.T) {
	tests := []struct {
		name string
		from string
		to   string
		want bool
	}{
		{name: "created to accepted", from: "CREATED", to: "ACCEPTED", want: true},
		{name: "accepted to restaurant arrival", from: "ACCEPTED", to: "ARRIVED_AT_RESTAURANT", want: true},
		{name: "restaurant arrival to picked up", from: "ARRIVED_AT_RESTAURANT", to: "PICKED_UP", want: true},
		{name: "picked up to arrived customer", from: "PICKED_UP", to: "ARRIVED_AT_CUSTOMER", want: true},
		{name: "arrived customer to delivered", from: "ARRIVED_AT_CUSTOMER", to: "DELIVERED", want: true},
		{name: "created cannot jump delivered", from: "CREATED", to: "DELIVERED", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deliveryTransitionAllowed(tt.from, tt.to); got != tt.want {
				t.Fatalf("deliveryTransitionAllowed(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestValueHelpers(t *testing.T) {
	input := map[string]any{
		"name":      "  Koramangala  ",
		"count":     float64(4),
		"is_active": true,
	}

	if got := stringValue(input, "name", "fallback"); got != "Koramangala" {
		t.Fatalf("stringValue trimmed = %q", got)
	}
	if got := intValue(input, "count", 1); got != 4 {
		t.Fatalf("intValue = %d", got)
	}
	if got := boolValue(input, "is_active", false); !got {
		t.Fatalf("boolValue = false")
	}
	if got := numberPtr(input, "missing"); got != nil {
		t.Fatalf("numberPtr missing = %#v", got)
	}
}

func TestTrackingHelpers(t *testing.T) {
	raw := []byte(`{"address_line1":"Main Road","city":"Bengaluru","latitude":12.9716,"longitude":77.5946}`)
	location := locationFromJSON(raw)
	if location == nil {
		t.Fatalf("locationFromJSON returned nil")
	}
	if got := location["latitude"]; got != 12.9716 {
		t.Fatalf("latitude = %#v", got)
	}
	if got := orderStatusLabel("READY_FOR_PICKUP"); got != "Ready for pickup" {
		t.Fatalf("orderStatusLabel = %q", got)
	}
	if got := deliveryStatusLabel("ARRIVED_AT_CUSTOMER"); got != "Arrived at customer" {
		t.Fatalf("deliveryStatusLabel = %q", got)
	}
}
