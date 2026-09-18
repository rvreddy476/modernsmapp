package http

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
)

// The ride row never serialises its OTP (json:"-"); only the customer view
// adds it, and only when the service filled it in.
func TestCustomerRideView_EmitsOTPOnlyWhenRevealed(t *testing.T) {
	otp := "4321"
	r := &store.Ride{ID: uuid.New(), Status: "arrived", OTPCode: &otp}
	raw, err := json.Marshal(customerRide(r))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"otp":"4321"`) {
		t.Fatalf("customer view must carry the OTP: %s", raw)
	}
	plain, _ := json.Marshal(r)
	if strings.Contains(string(plain), "4321") {
		t.Fatalf("the bare ride row must never serialise the OTP: %s", plain)
	}
	r.OTPCode = nil
	raw, _ = json.Marshal(customerRide(r))
	if strings.Contains(string(raw), `"otp"`) {
		t.Fatalf("no otp field when the service did not reveal one: %s", raw)
	}
}
