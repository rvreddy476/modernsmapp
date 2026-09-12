package service

// Who wins when a seller sends a courier and tracking number with a
// shipment: the seller under a provider that accepts manual booking, the
// adapter under one that does not. Pure, no store: the decision is a
// function of the provider and the body.

import (
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/store/postgres"
)

// fakeCarrier is a carrier-backed adapter: it does NOT opt in to manual
// booking, like Shiprocket.
type fakeCarrier struct{ courier.StubCourier }

func (fakeCarrier) Name() string { return "carrier" }

// The embedded stub opts in; a carrier must say no in its own voice.
func (fakeCarrier) AcceptsManualBooking() bool { return false }

// fakeManual opts in, like the stub.
type fakeManual struct{ fakeCarrier }

func (fakeManual) AcceptsManualBooking() bool { return true }

func adapterBooking() *postgres.Shipment {
	awb, co, label, track := "AWB-FROM-ADAPTER", "co_1", "https://carrier/label", "https://carrier/track"
	return &postgres.Shipment{
		Courier: "carrier", TrackingNumber: &awb, CourierOrderID: &co,
		LabelURL: &label, TrackingURL: &track, Status: "booked",
	}
}

func TestSellerValuesAreStoredUnderAProviderThatAcceptsManualBooking(t *testing.T) {
	sh := adapterBooking()
	used := applyManualBooking(fakeManual{}, ShipmentBooking{Courier: " Delhivery ", TrackingNumber: " DL123 "}, sh)
	if !used {
		t.Fatal("applyManualBooking = false under a provider that accepts manual booking")
	}
	if sh.Courier != "Delhivery" || sh.TrackingNumber == nil || *sh.TrackingNumber != "DL123" {
		t.Errorf("courier/tracking = %q/%v, want Delhivery/DL123 (trimmed)", sh.Courier, sh.TrackingNumber)
	}
	// The invented AWB's label and tracking page pointed at a number that
	// no longer exists on this shipment; they go with it.
	if sh.LabelURL != nil || sh.TrackingURL != nil || sh.CourierOrderID != nil {
		t.Errorf("adapter urls survived a manual booking: label=%v track=%v co=%v", sh.LabelURL, sh.TrackingURL, sh.CourierOrderID)
	}
	if r := bookingRemark(sh, true); !strings.Contains(r, "Delhivery") || !strings.Contains(r, "DL123") {
		t.Errorf("first event remark %q does not name the seller's courier and number", r)
	}
}

func TestSellerValuesAreIgnoredUnderACarrierBackedProvider(t *testing.T) {
	sh := adapterBooking()
	used := applyManualBooking(fakeCarrier{}, ShipmentBooking{Courier: "Delhivery", TrackingNumber: "DL123"}, sh)
	if used {
		t.Fatal("applyManualBooking = true under a carrier-backed provider; the carrier's AWB is the one its webhooks will name")
	}
	if sh.Courier != "carrier" || *sh.TrackingNumber != "AWB-FROM-ADAPTER" || sh.LabelURL == nil {
		t.Errorf("adapter booking was altered: %+v", sh)
	}
	if r := bookingRemark(sh, false); !strings.Contains(r, "AWB-FROM-ADAPTER") {
		t.Errorf("first event remark %q does not name the adapter's number", r)
	}
}

func TestAnEmptyBodyLeavesTheAdapterBookingAlone(t *testing.T) {
	sh := adapterBooking()
	if applyManualBooking(fakeManual{}, ShipmentBooking{}, sh) {
		t.Fatal("an empty body counted as a manual booking")
	}
	if *sh.TrackingNumber != "AWB-FROM-ADAPTER" || sh.LabelURL == nil {
		t.Errorf("adapter booking was altered by an empty body: %+v", sh)
	}
	if applyManualBooking(nil, ShipmentBooking{Courier: "x"}, sh) {
		t.Fatal("a nil provider accepted a manual booking")
	}
}

// The real stub opts in; the real Shiprocket adapter does not.
func TestTheStubAcceptsManualBookingAndShiprocketDoesNot(t *testing.T) {
	if !courier.AcceptsManualBooking(&courier.StubCourier{}) {
		t.Error("StubCourier must accept manual booking: its AWB is invented")
	}
	if courier.AcceptsManualBooking(courier.NewShiprocket("", "")) {
		t.Error("Shiprocket must not accept manual booking: its AWB is the webhook key")
	}
}
