package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBuildUPIIntent_HasRequiredFields(t *testing.T) {
	id := uuid.New()
	intent := buildUPIIntent(29900, id)
	if !strings.HasPrefix(intent, "upi://pay?") {
		t.Fatalf("expected upi:// prefix; got %s", intent)
	}
	for _, want := range []string{"pa=", "pn=", "am=299.00", "cu=INR", "tr=rider-" + id.String()} {
		if !strings.Contains(intent, want) {
			t.Errorf("intent missing %q: %s", want, intent)
		}
	}
}

// Launch safety: the legacy Subscribe route takes the wallet only. "manual"
// (a proof an admin verified) is gone, and upi / card go through the
// payments-service checkout.
func TestAllowedPaymentMethods_Coverage(t *testing.T) {
	if !allowedPaymentMethods["wallet"] {
		t.Errorf("expected wallet to be allowed")
	}
	for _, m := range []string{"manual", "upi", "card", "cheque"} {
		if allowedPaymentMethods[m] {
			t.Errorf("%q must not be accepted on the legacy subscribe route", m)
		}
	}
}

func TestAllowedPartnerTypes_Coverage(t *testing.T) {
	for _, m := range []string{"individual_driver", "owner_driver", "fleet_owner", "fleet_driver"} {
		if !allowedPartnerTypes[m] {
			t.Errorf("expected partner type %q to be allowed", m)
		}
	}
}

func TestAllowedVehicleTypes_Coverage(t *testing.T) {
	for _, m := range []string{"bike", "auto", "mini_cab", "sedan", "suv", "premium", "ev_bike", "ev_car"} {
		if !allowedVehicleTypes[m] {
			t.Errorf("expected vehicle type %q to be allowed", m)
		}
	}
}

func TestAllowedDocumentTypes_Coverage(t *testing.T) {
	required := []string{"aadhaar", "pan", "driving_license", "vehicle_rc", "vehicle_insurance"}
	for _, m := range required {
		if !allowedDocumentTypes[m] {
			t.Errorf("expected document type %q to be allowed", m)
		}
	}
}
