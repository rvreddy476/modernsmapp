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
	OrderID        string
	OrderNumber    string
	PickupAddress  Address
	DropAddress    Address
	Weight         float64 // kg
	PackageValue   float64 // INR
	PaymentMethod  string  // "prepaid" | "cod"
	CODAmount      float64 // 0 for prepaid
	Items          []Item
}

type Item struct {
	Name     string
	SKU      string
	Quantity int
	Price    float64
	HSN      string
}

type Address struct {
	Name     string
	Phone    string
	Email    string
	Line1    string
	Line2    string
	City     string
	State    string
	Postal   string
	Country  string
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

// Provider is the courier adapter.
type Provider interface {
	Name() string
	CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResponse, error)
	CancelShipment(ctx context.Context, trackingNumber string) error
	ParseWebhook(ctx context.Context, payload []byte) ([]TrackingUpdate, error)
	// VerifyWebhook authenticates an incoming webhook before parsing it.
	// Return nil if authentic; non-nil to reject.
	VerifyWebhook(headers map[string]string, body []byte) error
}

// New picks a provider based on env. Fallback: stub.
func New() Provider {
	switch os.Getenv("COURIER_PROVIDER") {
	case "shiprocket":
		sr := NewShiprocket(os.Getenv("SHIPROCKET_EMAIL"), os.Getenv("SHIPROCKET_PASSWORD"))
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

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
