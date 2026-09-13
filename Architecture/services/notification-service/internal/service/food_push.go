package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Feast pushes (lane B5b, 2026-09-13).
//
// Three audiences, three installed apps:
//
//	food_order_new       → restaurant owner/staff  → feast_kitchen  (channel kitchen_new_order)
//	food_delivery_offer  → delivery partner        → feast_rider    (channel rider_job_offer)
//	food_order_status    → customer                → momentum       (channel food_orders)
//
// A push reaches ONLY devices registered for its app (user_devices.app,
// migration 007). The customer copy follows the user's Momentum preferences
// (the food_orders category, master toggle, quiet hours) and also writes an
// inbox row. Kitchen and rider pushes are operational — a restaurant that
// muted Momentum likes must still hear a new order — so no Momentum
// preference gates them; Android channel settings in those apps are the
// user's control. Account suppression applies to every audience.
//
// Delivery is AT MOST ONCE per (dedup key, recipient, app, type): the claim
// in notify_meta.event_dedup happens before any send, matching the general
// notification path. If the claim itself cannot be recorded (database blip)
// the push is sent anyway — a lost new-order push costs a restaurant an
// order, and the per-order collapse key bounds a duplicate to a device-side
// replace.

// Push types, Android channels and apps for Feast.
const (
	FoodTypeOrderNew      = "food_order_new"
	FoodTypeDeliveryOffer = "food_delivery_offer"
	FoodTypeOrderStatus   = "food_order_status"

	FoodChannelKitchenNewOrder = "kitchen_new_order"
	FoodChannelRiderJobOffer   = "rider_job_offer"
	FoodChannelOrders          = "food_orders"

	AppMomentum     = postgres.AppMomentum
	AppFeastKitchen = postgres.AppFeastKitchen
	AppFeastRider   = postgres.AppFeastRider
)

// FoodPushDataKeys is the COMPLETE set of data keys a Feast push may carry.
// The data map is built from typed fields only (FoodPushData), never copied
// from an event, so nothing food-service puts on an event — a pickup or
// delivery code, an address, a phone number — can ride along.
var FoodPushDataKeys = map[string]bool{
	"type":                     true,
	"order_id":                 true,
	"deep_link":                true,
	"title":                    true,
	"body":                     true,
	"offer_id":                 true, // offers only
	"expires_at":               true, // offers only
	"collapse_key":             true, // transport: lifted into android.collapse_key
	push.AndroidChannelDataKey: true, // transport: lifted into android.notification.channel_id
}

// FoodPush is one rendered push for one recipient.
type FoodPush struct {
	// DedupKey is stable for the logical delivery (event id, or a per-order
	// key for "new order" which several events can announce).
	DedupKey    string
	RecipientID uuid.UUID
	App         string
	Type        string

	AndroidChannel string
	Title          string
	Body           string
	DeepLink       string

	OrderID uuid.UUID
	// Offers only.
	OfferID        uuid.UUID
	OfferExpiresAt string // RFC3339, UTC

	CreatedAt time.Time
}

// FoodPushOutcome says what happened to one push.
type FoodPushOutcome string

const (
	FoodPushSent       FoodPushOutcome = "sent"
	FoodPushDuplicate  FoodPushOutcome = "duplicate"
	FoodPushSuppressed FoodPushOutcome = "suppressed" // account deactivated / deletion scheduled
	FoodPushMuted      FoodPushOutcome = "muted"      // customer preferences: no push
	FoodPushNoDevices  FoodPushOutcome = "no_devices" // nothing registered for the app, or every send failed
)

// foodDedupNamespace scopes UUIDv5 dedup ids for Feast pushes.
var foodDedupNamespace = uuid.MustParse("5b0f7a52-3c1e-4c55-9d64-2f1f6a9e0b71")

// foodDedupID is the notify_meta.event_dedup key for one push.
func foodDedupID(p FoodPush) uuid.UUID {
	return uuid.NewSHA1(foodDedupNamespace,
		[]byte(p.DedupKey+"|"+p.RecipientID.String()+"|"+p.App+"|"+p.Type))
}

func (p FoodPush) customerFacing() bool { return p.App == AppMomentum }

func (p FoodPush) validate() error {
	if p.RecipientID == uuid.Nil {
		return errors.New("food push: no recipient")
	}
	if p.DedupKey == "" {
		return errors.New("food push: no dedup key")
	}
	if p.OrderID == uuid.Nil {
		return errors.New("food push: no order id")
	}
	want := map[string]string{
		FoodTypeOrderNew:      AppFeastKitchen,
		FoodTypeDeliveryOffer: AppFeastRider,
		FoodTypeOrderStatus:   AppMomentum,
	}
	app, known := want[p.Type]
	if !known {
		return fmt.Errorf("food push: unknown type %q", p.Type)
	}
	if p.App != app {
		return fmt.Errorf("food push: %s belongs to app %s, not %q", p.Type, app, p.App)
	}
	if p.Type == FoodTypeDeliveryOffer && p.OfferID == uuid.Nil {
		return errors.New("food push: offer without offer id")
	}
	return nil
}

// FoodPushData renders the complete FCM data payload for p.
func FoodPushData(p FoodPush) map[string]string {
	data := map[string]string{
		"type":      p.Type,
		"order_id":  p.OrderID.String(),
		"deep_link": p.DeepLink,
		"title":     p.Title,
		"body":      p.Body,
	}
	collapse := "food_order:" + p.OrderID.String()
	switch p.Type {
	case FoodTypeDeliveryOffer:
		data["offer_id"] = p.OfferID.String()
		if p.OfferExpiresAt != "" {
			data["expires_at"] = p.OfferExpiresAt
		}
		collapse = "food_offer:" + p.OfferID.String()
	case FoodTypeOrderNew:
		collapse = "food_order_new:" + p.OrderID.String()
	}
	data["collapse_key"] = collapse
	if p.AndroidChannel != "" {
		data[push.AndroidChannelDataKey] = p.AndroidChannel
	}
	return data
}

// foodPushTransports is the dependency set one Feast push rides on. *Service
// implements it over PostgreSQL, Scylla and FCM/APNs; runFoodPush owns the
// rules so they are testable without live stores.
type foodPushTransports interface {
	recipientSuppressed(ctx context.Context, userID uuid.UUID, notifType string) bool
	claimFoodPush(ctx context.Context, id uuid.UUID) (bool, error)
	customerDecision(ctx context.Context, userID uuid.UUID) DeliveryDecision
	writeFoodInbox(ctx context.Context, p FoodPush) error
	foodDevices(ctx context.Context, userID uuid.UUID, app string) ([]postgres.UserDevice, error)
	sendFoodPush(ctx context.Context, d postgres.UserDevice, title, body string, data map[string]string) error
	retireFoodDevice(ctx context.Context, userID uuid.UUID, token string) error
}

// DeliverFoodPush delivers one Feast push. See the notes at the top of file.
func (s *Service) DeliverFoodPush(ctx context.Context, p FoodPush) (FoodPushOutcome, error) {
	return runFoodPush(ctx, s, p)
}

func runFoodPush(ctx context.Context, t foodPushTransports, p FoodPush) (FoodPushOutcome, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	if t.recipientSuppressed(ctx, p.RecipientID, p.Type) {
		return FoodPushSuppressed, nil
	}

	first, err := t.claimFoodPush(ctx, foodDedupID(p))
	switch {
	case err != nil:
		slog.Warn("food push: dedup claim failed; sending anyway (collapse key bounds a duplicate)",
			"type", p.Type, "app", p.App, "error", err)
	case !first:
		return FoodPushDuplicate, nil
	}

	if p.customerFacing() {
		decision := t.customerDecision(ctx, p.RecipientID)
		if decision.CreateInbox {
			if err := t.writeFoodInbox(ctx, p); err != nil {
				slog.Warn("food push: inbox row failed", "type", p.Type, "error", err)
			}
		}
		if !decision.SendPush {
			return FoodPushMuted, nil
		}
	}

	devices, err := t.foodDevices(ctx, p.RecipientID, p.App)
	if err != nil {
		return "", fmt.Errorf("food push: devices: %w", err)
	}
	data := FoodPushData(p)
	sent := 0
	for _, d := range devices {
		// THE app guard: a token registered for another app never receives
		// this push, whatever the device lookup returned.
		if d.App != p.App {
			continue
		}
		err := t.sendFoodPush(ctx, d, p.Title, p.Body, data)
		switch {
		case err == nil:
			sent++
		case errors.Is(err, push.ErrDeviceRejected):
			if rerr := t.retireFoodDevice(ctx, p.RecipientID, d.PushToken); rerr != nil {
				slog.Warn("food push: retire rejected device failed", "platform", d.Platform, "error", rerr)
			}
		default:
			slog.Warn("food push: send failed", "type", p.Type, "app", p.App, "platform", d.Platform, "error", err)
		}
	}
	if sent == 0 {
		return FoodPushNoDevices, nil
	}
	return FoodPushSent, nil
}

func (s *Service) claimFoodPush(ctx context.Context, id uuid.UUID) (bool, error) {
	if s.pgStore == nil {
		return true, nil
	}
	return s.pgStore.ClaimEventDedup(ctx, id)
}

func (s *Service) customerDecision(ctx context.Context, userID uuid.UUID) DeliveryDecision {
	return s.resolveGeneralDelivery(ctx, userID, FoodTypeOrderStatus)
}

// writeFoodInbox records the customer's inbox row (and realtime frame) under
// the push's deterministic identity, without a second push.
func (s *Service) writeFoodInbox(ctx context.Context, p FoodPush) error {
	if s.scyllaStore == nil {
		return nil
	}
	decision := DeliveryDecision{CreateInbox: true, SendWebSocket: true}
	return s.deliverWithDecision(ctx, decision, p.RecipientID, p.RecipientID, p.Type,
		"food_order", p.OrderID, p.DeepLink, p.CreatedAt, foodDedupID(p).String(), RenderOverride{})
}

func (s *Service) foodDevices(ctx context.Context, userID uuid.UUID, app string) ([]postgres.UserDevice, error) {
	if s.pgStore == nil {
		return nil, nil
	}
	return s.pgStore.GetUserDevicesForApp(ctx, userID, app)
}

func (s *Service) sendFoodPush(ctx context.Context, d postgres.UserDevice, title, body string, data map[string]string) error {
	if s.pusher == nil {
		return fmt.Errorf("food push: %w", push.ErrProviderNotConfigured)
	}
	return s.pusher.Send(ctx, d.PushToken, d.Platform, title, body, data)
}

func (s *Service) retireFoodDevice(ctx context.Context, userID uuid.UUID, token string) error {
	if s.pgStore == nil {
		return nil
	}
	return s.pgStore.RetireDeviceToken(ctx, userID, token)
}
