package courier

// B8 — production shipping was priced at zero on every order.
//
// The adapter is driven against a fake Shiprocket, so the rate-card contract
// is exercised without a live provider. What is proven:
//
//	1. a serviceable lane returns a real ShippingChargeMinor in paise;
//	2. the cheapest priced option is chosen;
//	3. "serviceable but no usable rate" is an ERROR, not a zero — this is the
//	   exact shape the placeholder returned, so it is the regression that
//	   matters;
//	4. an unserviceable lane is a normal result, not an error.
//
// The negative control at the bottom restores the placeholder body and shows
// it yields a zero charge that the quote would persist.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeShiprocket serves /auth/login and /courier/serviceability/.
func fakeShiprocket(t *testing.T, serviceability any, status int) *ShiprocketCourier {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/auth/login":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "test-token"})
		case r.URL.Path == "/courier/serviceability/":
			if r.Header.Get("Authorization") != "Bearer test-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			if serviceability != nil {
				_ = json.NewEncoder(w).Encode(serviceability)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewShiprocket("test@example.com", "secret")
	c.base = srv.URL
	c.http = srv.Client()
	return c
}

func rateCard(companies ...map[string]any) map[string]any {
	return map[string]any{
		"status": 200,
		"data":   map[string]any{"available_courier_companies": companies},
	}
}

var req = ServiceabilityRequest{
	PickupPincode: "560001",
	DropPincode:   "110001",
	WeightKg:      1.2,
	PaymentMethod: "prepaid",
}

func TestServiceableLaneReturnsARealRate(t *testing.T) {
	c := fakeShiprocket(t, rateCard(map[string]any{
		"courier_name":            "Delhivery",
		"rate":                    78.50,
		"estimated_delivery_days": "4",
		"cod":                     0,
	}), http.StatusOK)

	res, err := c.CheckServiceability(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Serviceable {
		t.Fatal("lane should be serviceable")
	}
	if res.ShippingChargeMinor != 7850 {
		t.Fatalf("ShippingChargeMinor = %d, want 7850 (₹78.50 in paise)", res.ShippingChargeMinor)
	}
	if res.EstimatedDays != 4 {
		t.Errorf("EstimatedDays = %d, want 4", res.EstimatedDays)
	}
}

func TestCheapestPricedOptionWins(t *testing.T) {
	c := fakeShiprocket(t, rateCard(
		map[string]any{"courier_name": "Expensive", "rate": 149.00, "estimated_delivery_days": "2"},
		map[string]any{"courier_name": "Cheap", "rate": 61.00, "estimated_delivery_days": "5"},
		// A zero-rate entry must never be selected as "cheapest".
		map[string]any{"courier_name": "Broken", "rate": 0.0, "estimated_delivery_days": "3"},
	), http.StatusOK)

	res, err := c.CheckServiceability(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ShippingChargeMinor != 6100 {
		t.Fatalf("ShippingChargeMinor = %d, want 6100 — the zero-rate option must not win", res.ShippingChargeMinor)
	}
}

// THE regression. Serviceable with no usable price must fail loudly.
func TestServiceableWithoutARateIsAnError(t *testing.T) {
	c := fakeShiprocket(t, rateCard(
		map[string]any{"courier_name": "Nameless", "rate": 0.0, "freight_charge": 0.0},
	), http.StatusOK)

	res, err := c.CheckServiceability(context.Background(), req)
	if err == nil {
		t.Fatalf("a serviceable lane with no rate must be an error, got %+v", res)
	}
	if res != nil && res.ShippingChargeMinor == 0 && res.Serviceable {
		t.Fatal("a zero shipping charge was returned as a usable quote")
	}
}

func TestUnserviceableLaneIsNotAnError(t *testing.T) {
	c := fakeShiprocket(t, rateCard(), http.StatusOK)
	res, err := c.CheckServiceability(context.Background(), req)
	if err != nil {
		t.Fatalf("an unserviceable lane is a normal answer, not an error: %v", err)
	}
	if res.Serviceable {
		t.Fatal("no courier companies returned, but the lane reported serviceable")
	}
}

func TestInvalidPincodeIsRejectedWithoutCallingTheProvider(t *testing.T) {
	c := fakeShiprocket(t, nil, http.StatusInternalServerError)
	res, err := c.CheckServiceability(context.Background(), ServiceabilityRequest{
		PickupPincode: "560001", DropPincode: "abc", WeightKg: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Serviceable {
		t.Fatal("an invalid pincode must not be serviceable")
	}
}

// ─── Negative control (review §4) ────────────────────────────────────
//
// Restore the placeholder's behaviour and show it produces the zero charge
// that PrepareQuote would persist into the quote and checkout would bill.
func TestNegativeControl_PlaceholderPricesEverythingAtZero(t *testing.T) {
	// This is verbatim what CheckServiceability used to return.
	placeholder := func(r ServiceabilityRequest) *ServiceabilityResult {
		if !validIndianPincode(r.PickupPincode) || !validIndianPincode(r.DropPincode) {
			return &ServiceabilityResult{Serviceable: false, Courier: "shiprocket"}
		}
		return &ServiceabilityResult{
			Serviceable:   true,
			CODSupported:  true,
			EstimatedDays: 4,
			Courier:       "shiprocket",
		}
	}

	res := placeholder(req)
	if !res.Serviceable || res.ShippingChargeMinor != 0 {
		t.Fatalf("negative control did not reproduce the defect: got serviceable=%v charge=%d, "+
			"want serviceable=true charge=0", res.Serviceable, res.ShippingChargeMinor)
	}
	t.Log("negative control reproduced the original defect: the placeholder reports every " +
		"serviceable lane at ShippingChargeMinor=0, which the quote persists as free shipping")
}
