// Package courier provides a pluggable adapter over shipping carriers.
//
// Implementations: StubCourier (dev), ShiprocketCourier (prod).
// Selected via env COURIER_PROVIDER (default: "stub").
package courier

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"time"
)

// ShipmentRequest carries the inputs needed to book a shipment.
type ShipmentRequest struct {
	OrderID       string
	OrderNumber   string
	PickupAddress Address
	DropAddress   Address
	Weight        float64 // kg
	// Parcel dimensions in centimetres. Shiprocket refuses an order that
	// carries none, so the caller substitutes a small-parcel default when
	// the product does not declare its own.
	LengthCm      float64
	BreadthCm     float64
	HeightCm      float64
	PackageValue  float64 // INR
	PaymentMethod string  // "prepaid" | "cod"
	CODAmount     float64 // 0 for prepaid
	Items         []Item
}

type Item struct {
	Name     string
	SKU      string
	Quantity int
	Price    float64
	HSN      string
}

type Address struct {
	Name    string
	Phone   string
	Email   string
	Line1   string
	Line2   string
	City    string
	State   string
	Postal  string
	Country string
}

// ShipmentResponse is the result of a booking.
type ShipmentResponse struct {
	CourierOrderID string
	AWBNumber      string
	LabelURL       string
	TrackingURL    string
	EstimatedETA   time.Time
	RawResponse    []byte
}

// TrackingUpdate is a parsed status event from a carrier webhook.
type TrackingUpdate struct {
	TrackingNumber string
	Status         string // booked | picked_up | in_transit | out_for_delivery | delivered | rto_initiated | rto_delivered | lost
	Location       string
	Remark         string
	OccurredAt     time.Time
}

// ServiceabilityRequest is the input to CheckServiceability — Phase 1.3.
// PickupPincode is the seller's pickup address; DropPincode the customer's
// delivery pincode; WeightKg from the product; PaymentMethod gates COD.
type ServiceabilityRequest struct {
	PickupPincode string
	DropPincode   string
	WeightKg      float64
	PaymentMethod string // "prepaid" | "cod"
}

// ServiceabilityResult tells the customer whether their pincode is
// reachable, whether COD is available, and a delivery ETA — replaces the
// mobile pincode heuristic with a courier-backed answer.
type ServiceabilityResult struct {
	Serviceable   bool      `json:"serviceable"`
	CODSupported  bool      `json:"cod_supported"`
	EstimatedDays int       `json:"estimated_days"`
	EstimatedETA  time.Time `json:"estimated_eta"`
	Courier       string    `json:"courier"`
	Reason        string    `json:"reason,omitempty"`

	// ShippingChargeMinor is the delivery price in paise.
	//
	// Commerce P0 LB-20 / D7. Pricing used to hardcode ₹40, free over ₹499,
	// platform-wide — while this adapter, which actually knows what a
	// courier charges for this weight to this pincode, sat unused. The rate
	// now comes from here, is persisted as a bound quote (A4), and is
	// consumed inside the checkout transaction so no network call happens
	// before the commit.
	//
	// Paise, not rupees: no float touches a money path (LB-19).
	ShippingChargeMinor int64 `json:"shipping_charge_minor"`
}

// Provider is the courier adapter.
type Provider interface {
	Name() string
	CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResponse, error)
	CancelShipment(ctx context.Context, trackingNumber string) error
	ParseWebhook(ctx context.Context, payload []byte) ([]TrackingUpdate, error)
	// VerifyWebhook authenticates an incoming webhook before parsing it.
	// Return nil if authentic; non-nil to reject.
	VerifyWebhook(headers map[string]string, body []byte) error
	// CheckServiceability is the real implementation of the pincode +
	// weight + COD check that drives the customer-facing "deliver to"
	// chip on product pages and the checkout COD gate. Phase 1.3.
	CheckServiceability(ctx context.Context, req ServiceabilityRequest) (*ServiceabilityResult, error)
}

// New picks a provider based on env. Fallback: stub.
func New() Provider {
	switch os.Getenv("COURIER_PROVIDER") {
	case "shiprocket":
		sr := NewShiprocket(os.Getenv("SHIPROCKET_EMAIL"), os.Getenv("SHIPROCKET_PASSWORD")).
			WithPickupLocation(os.Getenv("SHIPROCKET_PICKUP_LOCATION"))
		sr.WithWebhookSecrets(os.Getenv("SHIPROCKET_WEBHOOK_TOKEN"), os.Getenv("SHIPROCKET_WEBHOOK_HMAC"))
		return sr
	default:
		return &StubCourier{}
	}
}

// ── StubCourier ────────────────────────────────────────────────────────────
// StubCourier auto-generates AWBs and never fails. Use in dev.
type StubCourier struct{}

func (StubCourier) Name() string { return "stub" }

func (StubCourier) CreateShipment(_ context.Context, req ShipmentRequest) (*ShipmentResponse, error) {
	awb := randHex(6)
	return &ShipmentResponse{
		CourierOrderID: "stub-" + awb,
		AWBNumber:      "STUB" + awb,
		LabelURL:       fmt.Sprintf("https://example.com/labels/%s.pdf", awb),
		TrackingURL:    fmt.Sprintf("https://postbook.app/track/%s", awb),
		EstimatedETA:   time.Now().Add(72 * time.Hour),
	}, nil
}

func (StubCourier) CancelShipment(_ context.Context, _ string) error { return nil }

func (StubCourier) ParseWebhook(_ context.Context, _ []byte) ([]TrackingUpdate, error) {
	return nil, nil
}

// VerifyWebhook accepts anything in dev.
func (StubCourier) VerifyWebhook(_ map[string]string, _ []byte) error { return nil }

// CheckServiceability for the stub courier returns a permissive default
// (3-day ETA, COD allowed) so dev environments can exercise the full
// checkout flow without a real courier. Pincodes are validated for shape
// only; anything else is left to the production Shiprocket adapter.
func (StubCourier) CheckServiceability(_ context.Context, req ServiceabilityRequest) (*ServiceabilityResult, error) {
	if !validIndianPincode(req.PickupPincode) || !validIndianPincode(req.DropPincode) {
		return &ServiceabilityResult{
			Serviceable: false,
			Courier:     "stub",
			Reason:      "invalid pincode",
		}, nil
	}
	return &ServiceabilityResult{
		Serviceable: true,
		// A5: prepaid-only launch. Even the stub must not offer COD, or a
		// dev environment would exercise a path production refuses.
		CODSupported:  false,
		EstimatedDays: 3,
		EstimatedETA:  time.Now().AddDate(0, 0, 3),
		Courier:       "stub",
		// A visible, obviously-fake rate. M-10: production refuses to load
		// this adapter at all, so a real order can never be quoted from it.
		ShippingChargeMinor: 4900,
	}, nil
}

// validIndianPincode checks for a 6-digit numeric string. India Post
// rejects pincodes beginning with 0; we keep the looser shape-only check
// and let the carrier API return the canonical "not serviceable" if the
// digits are non-allocated.
func validIndianPincode(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
