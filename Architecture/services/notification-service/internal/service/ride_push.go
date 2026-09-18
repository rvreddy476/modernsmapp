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

// Mopedu pushes (2026-09-18).
//
// Two audiences, two installed apps:
//
//	ride.assigned / arriving / arrived / started / completed / cancelled /
//	ride.payment.paid        → customer  → momentum        (channel ride_updates)
//	captain.offer            → captain   → mopedu_captain  (channel captain_offer)
//	captain.payment.received → captain   → mopedu_captain  (channel captain_earnings)
//	captain.approved / under_review
//	captain.subscription.expiring / expired / renewed / payment_failed
//	                         → captain   → mopedu_captain  (channel captain_account)
//
// The type strings are the Momentum app's PushDestinations.RIDE_TYPES and the
// captain app's NotificationChannelSpec.forType — change one here and the
// client routes the tap to SOCIAL, which the captain app does not even
// register. A push reaches ONLY devices registered for its app.
//
// The customer's pushes are operational — a captain waiting at the kerb is
// not a "like" — so no Momentum category toggle, quiet hours or master push
// switch silences them; the ride_updates Android channel is the customer's
// control. The customer copy also writes an inbox row (entity rider_ride)
// when the user's in-app preferences allow one. Captain ride pushes (offer,
// payment notice) write no inbox row: the captain app has no ride inbox. The
// captain ACCOUNT pushes (approval, plan state) do keep an inbox row — entity
// rider_partner or rider_subscription — so the account history survives the
// notification tray; no Momentum preference is consulted for it, because a
// captain has no category toggle for their captain account. Account
// suppression applies to everyone.
//
// A push NEVER carries an OTP, a fare breakdown, a phone number or a
// location: the data map is built from typed fields only (RidePushData), and
// the copy tells the customer to share the OTP "from the app".
//
// Delivery is AT MOST ONCE per (dedup key, recipient, app, type), claimed
// before any send; on a dedup-store blip the push is sent anyway and the
// collapse key bounds the duplicate to a device-side replace.

// Push types, Android channels and apps for Mopedu.
const (
	RideTypeAssigned    = "ride.assigned"
	RideTypeArriving    = "ride.arriving"
	RideTypeArrived     = "ride.arrived"
	RideTypeStarted     = "ride.started"
	RideTypeCompleted   = "ride.completed"
	RideTypeCancelled   = "ride.cancelled"
	RideTypePaymentPaid = "ride.payment.paid"

	CaptainTypeOffer           = "captain.offer"
	CaptainTypePaymentReceived = "captain.payment.received"

	// Captain account pushes (2026-09-19): the partner's approval and plan
	// state. These carry no ride: entity_id is the partner or subscription.
	CaptainTypeApproved                  = "captain.approved"
	CaptainTypeUnderReview               = "captain.under_review"
	CaptainTypeSubscriptionExpiring      = "captain.subscription.expiring"
	CaptainTypeSubscriptionExpired       = "captain.subscription.expired"
	CaptainTypeSubscriptionRenewed       = "captain.subscription.renewed"
	CaptainTypeSubscriptionPaymentFailed = "captain.subscription.payment_failed"

	RideChannelUpdates     = "ride_updates"
	CaptainChannelOffer    = "captain_offer"
	CaptainChannelOnDuty   = "captain_on_duty"
	CaptainChannelEarnings = "captain_earnings"
	CaptainChannelAccount  = "captain_account"

	AppMopeduCaptain = postgres.AppMopeduCaptain
)

// RideInboxEntityType is the inbox row's entity type for a customer ride
// notification; the entity id is the ride id.
const RideInboxEntityType = "rider_ride"

// Inbox entity types for the captain account pushes; the entity id is the
// partner id or the subscription id.
const (
	CaptainPartnerInboxEntityType      = "rider_partner"
	CaptainSubscriptionInboxEntityType = "rider_subscription"
)

// captainAccountInbox pins each captain account type to its inbox entity
// type. A captain type absent here (offer, payment notice) writes no inbox
// row and is bound to a ride.
var captainAccountInbox = map[string]string{
	CaptainTypeApproved:                  CaptainPartnerInboxEntityType,
	CaptainTypeUnderReview:               CaptainPartnerInboxEntityType,
	CaptainTypeSubscriptionExpiring:      CaptainSubscriptionInboxEntityType,
	CaptainTypeSubscriptionExpired:       CaptainSubscriptionInboxEntityType,
	CaptainTypeSubscriptionRenewed:       CaptainSubscriptionInboxEntityType,
	CaptainTypeSubscriptionPaymentFailed: CaptainSubscriptionInboxEntityType,
}

// ridePushApps is the routing table: which installed app each type reaches.
// A type absent here is not a Mopedu push.
var ridePushApps = map[string]string{
	RideTypeAssigned:                     AppMomentum,
	RideTypeArriving:                     AppMomentum,
	RideTypeArrived:                      AppMomentum,
	RideTypeStarted:                      AppMomentum,
	RideTypeCompleted:                    AppMomentum,
	RideTypeCancelled:                    AppMomentum,
	RideTypePaymentPaid:                  AppMomentum,
	CaptainTypeOffer:                     AppMopeduCaptain,
	CaptainTypePaymentReceived:           AppMopeduCaptain,
	CaptainTypeApproved:                  AppMopeduCaptain,
	CaptainTypeUnderReview:               AppMopeduCaptain,
	CaptainTypeSubscriptionExpiring:      AppMopeduCaptain,
	CaptainTypeSubscriptionExpired:       AppMopeduCaptain,
	CaptainTypeSubscriptionRenewed:       AppMopeduCaptain,
	CaptainTypeSubscriptionPaymentFailed: AppMopeduCaptain,
}

// CaptainAccountInboxEntityFor returns the inbox entity type of a captain
// account push type, and false for every other type.
func CaptainAccountInboxEntityFor(pushType string) (string, bool) {
	entity, ok := captainAccountInbox[pushType]
	return entity, ok
}

// RidePushAppFor returns the app a Mopedu push type is delivered to.
func RidePushAppFor(pushType string) (string, bool) {
	app, ok := ridePushApps[pushType]
	return app, ok
}

// RidePushDataKeys is the COMPLETE set of data keys a Mopedu push may carry.
var RidePushDataKeys = map[string]bool{
	"type":                     true,
	"entity_id":                true, // ride id; the offer id for captain.offer
	"deep_link":                true,
	"title":                    true,
	"body":                     true,
	"collapse_key":             true, // transport: lifted into android.collapse_key
	push.AndroidChannelDataKey: true, // transport: lifted into android.notification.channel_id
	push.AndroidTTLDataKey:     true, // transport: lifted into android.ttl (offers only)
}

// RidePush is one rendered push for one recipient.
type RidePush struct {
	// DedupKey is stable for the logical delivery (the event id, or a
	// per-offer key).
	DedupKey    string
	RecipientID uuid.UUID
	App         string
	Type        string

	AndroidChannel string
	Title          string
	Body           string
	DeepLink       string

	// EntityID is the push's entity_id: the ride for every customer push and
	// the payment notice, the OFFER for captain.offer, the PARTNER for
	// captain.approved / under_review and the SUBSCRIPTION for the
	// captain.subscription.* types.
	EntityID uuid.UUID
	// RideID is the ride the push is about (the inbox row's entity, and the
	// collapse key for customer pushes). Nil — and only Nil — for the captain
	// account pushes, which are about no ride.
	RideID uuid.UUID
	// TTL, when set, is how long FCM may hold the push before dropping it
	// (offers: until the offer expires). Zero means the provider default.
	TTL time.Duration

	CreatedAt time.Time
}

// RidePushOutcome says what happened to one push.
type RidePushOutcome string

const (
	RidePushSent       RidePushOutcome = "sent"
	RidePushDuplicate  RidePushOutcome = "duplicate"
	RidePushSuppressed RidePushOutcome = "suppressed" // account deactivated / deletion scheduled
	RidePushNoDevices  RidePushOutcome = "no_devices" // nothing registered for the app, or every send failed
)

// rideDedupNamespace scopes UUIDv5 dedup ids for Mopedu pushes.
var rideDedupNamespace = uuid.MustParse("9c4e2b1a-7d63-4f0e-a5b8-3e1d0c9f7a26")

// rideDedupID is the notify_meta.event_dedup key for one push.
func rideDedupID(p RidePush) uuid.UUID {
	return uuid.NewSHA1(rideDedupNamespace,
		[]byte(p.DedupKey+"|"+p.RecipientID.String()+"|"+p.App+"|"+p.Type))
}

func (p RidePush) customerFacing() bool { return p.App == AppMomentum }

// captainAccount reports whether p is a captain account push (approval or
// plan state): bound to a partner or subscription, never to a ride.
func (p RidePush) captainAccount() bool {
	_, ok := captainAccountInbox[p.Type]
	return ok
}

// inboxEntity is the inbox row p writes, if any: the ride for a customer
// push, the partner or subscription for a captain account push. ok=false for
// the captain's ride pushes (offer, payment notice), which keep no row.
func (p RidePush) inboxEntity() (entityType string, entityID uuid.UUID, ok bool) {
	if p.customerFacing() {
		return RideInboxEntityType, p.RideID, true
	}
	if entity, isAccount := captainAccountInbox[p.Type]; isAccount {
		return entity, p.EntityID, true
	}
	return "", uuid.Nil, false
}

// collapseKey is the device-side replace key: one notification per ride for
// the customer, one per offer, one per partner or subscription for the
// account pushes ("expired" replaces "expiring").
func (p RidePush) collapseKey() string {
	switch {
	case p.Type == CaptainTypeOffer:
		return "ride_offer:" + p.EntityID.String()
	case p.captainAccount():
		return captainAccountInbox[p.Type] + ":" + p.EntityID.String()
	}
	return "ride:" + p.RideID.String()
}

func (p RidePush) validate() error {
	if p.RecipientID == uuid.Nil {
		return errors.New("ride push: no recipient")
	}
	if p.DedupKey == "" {
		return errors.New("ride push: no dedup key")
	}
	app, known := ridePushApps[p.Type]
	if !known {
		return fmt.Errorf("ride push: unknown type %q", p.Type)
	}
	if p.App != app {
		return fmt.Errorf("ride push: %s belongs to app %s, not %q", p.Type, app, p.App)
	}
	if p.EntityID == uuid.Nil {
		return errors.New("ride push: no entity id")
	}
	if p.captainAccount() {
		if p.RideID != uuid.Nil {
			return fmt.Errorf("ride push: %s is an account push and carries no ride", p.Type)
		}
		return nil
	}
	if p.RideID == uuid.Nil {
		return errors.New("ride push: no ride id")
	}
	if p.Type == CaptainTypeOffer && p.EntityID == p.RideID {
		return errors.New("ride push: captain.offer must carry the offer id, not the ride id")
	}
	return nil
}

// RidePushData renders the complete FCM data payload for p: exactly
// {type, entity_id, deep_link, title, body} plus the transport keys.
func RidePushData(p RidePush) map[string]string {
	data := map[string]string{
		"type":      p.Type,
		"entity_id": p.EntityID.String(),
		"deep_link": p.DeepLink,
		"title":     p.Title,
		"body":      p.Body,
	}
	// One notification per ride on the customer's device: "arriving"
	// replaces "assigned". Each offer stands alone. One per partner or
	// subscription for the account pushes.
	data["collapse_key"] = p.collapseKey()
	if p.AndroidChannel != "" {
		data[push.AndroidChannelDataKey] = p.AndroidChannel
	}
	if p.TTL > 0 {
		data[push.AndroidTTLDataKey] = fmt.Sprintf("%ds", int64(p.TTL.Round(time.Second)/time.Second))
	}
	return data
}

// ridePushTransports is the dependency set one Mopedu push rides on; *Service
// implements it and runRidePush owns the rules so they are testable without
// live stores.
type ridePushTransports interface {
	recipientSuppressed(ctx context.Context, userID uuid.UUID, notifType string) bool
	claimPush(ctx context.Context, id uuid.UUID) (bool, error)
	rideInboxWanted(ctx context.Context, userID uuid.UUID, pushType string) bool
	writeRideInbox(ctx context.Context, p RidePush) error
	appDevices(ctx context.Context, userID uuid.UUID, app string) ([]postgres.UserDevice, error)
	sendAppPush(ctx context.Context, d postgres.UserDevice, title, body string, data map[string]string) error
	retireAppDevice(ctx context.Context, userID uuid.UUID, token string) error
}

// DeliverRidePush delivers one Mopedu push. See the notes at the top of file.
func (s *Service) DeliverRidePush(ctx context.Context, p RidePush) (RidePushOutcome, error) {
	return runRidePush(ctx, s, p)
}

func runRidePush(ctx context.Context, t ridePushTransports, p RidePush) (RidePushOutcome, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	if t.recipientSuppressed(ctx, p.RecipientID, p.Type) {
		return RidePushSuppressed, nil
	}

	first, err := t.claimPush(ctx, rideDedupID(p))
	switch {
	case err != nil:
		slog.Warn("ride push: dedup claim failed; sending anyway (collapse key bounds a duplicate)",
			"type", p.Type, "app", p.App, "error", err)
	case !first:
		return RidePushDuplicate, nil
	}

	// The customer's row follows their in-app preference; the captain
	// account row is unconditional (no preference exists for it); the
	// captain's ride pushes keep no row.
	wantInbox := false
	if _, _, hasInbox := p.inboxEntity(); hasInbox {
		wantInbox = p.captainAccount() || t.rideInboxWanted(ctx, p.RecipientID, p.Type)
	}
	if wantInbox {
		if err := t.writeRideInbox(ctx, p); err != nil {
			slog.Warn("ride push: inbox row failed", "type", p.Type, "error", err)
		}
	}

	devices, err := t.appDevices(ctx, p.RecipientID, p.App)
	if err != nil {
		return "", fmt.Errorf("ride push: devices: %w", err)
	}
	data := RidePushData(p)
	sent := 0
	for _, d := range devices {
		// THE app guard: a token registered for another app never receives
		// this push, whatever the device lookup returned.
		if d.App != p.App {
			continue
		}
		err := t.sendAppPush(ctx, d, p.Title, p.Body, data)
		switch {
		case err == nil:
			sent++
		case errors.Is(err, push.ErrDeviceRejected):
			if rerr := t.retireAppDevice(ctx, p.RecipientID, d.PushToken); rerr != nil {
				slog.Warn("ride push: retire rejected device failed", "platform", d.Platform, "error", rerr)
			}
		default:
			slog.Warn("ride push: send failed", "type", p.Type, "app", p.App, "platform", d.Platform, "error", err)
		}
	}
	if sent == 0 {
		return RidePushNoDevices, nil
	}
	return RidePushSent, nil
}

// The transports below share their implementation with the Feast path
// (food_push.go): the dedup claim, per-app device lookup, provider send and
// token retirement are the same operations under app-neutral names.

func (s *Service) claimPush(ctx context.Context, id uuid.UUID) (bool, error) {
	return s.claimFoodPush(ctx, id)
}

// rideInboxWanted consults only the in-app half of the customer's
// preferences: the push itself is operational and never gated.
func (s *Service) rideInboxWanted(ctx context.Context, userID uuid.UUID, pushType string) bool {
	return s.resolveGeneralDelivery(ctx, userID, pushType).CreateInbox
}

// writeRideInbox records the push's inbox row (and realtime frame) — the
// customer's ride, or the captain's partner / subscription — under the push's
// deterministic identity, without a second push.
func (s *Service) writeRideInbox(ctx context.Context, p RidePush) error {
	if s.scyllaStore == nil {
		return nil
	}
	entityType, entityID, ok := p.inboxEntity()
	if !ok {
		return nil
	}
	decision := DeliveryDecision{CreateInbox: true, SendWebSocket: true}
	return s.deliverWithDecision(ctx, decision, p.RecipientID, p.RecipientID, p.Type,
		entityType, entityID, p.DeepLink, p.CreatedAt, rideDedupID(p).String(),
		RenderOverride{Title: p.Title, Body: p.Body})
}

func (s *Service) appDevices(ctx context.Context, userID uuid.UUID, app string) ([]postgres.UserDevice, error) {
	return s.foodDevices(ctx, userID, app)
}

func (s *Service) sendAppPush(ctx context.Context, d postgres.UserDevice, title, body string, data map[string]string) error {
	return s.sendFoodPush(ctx, d, title, body, data)
}

func (s *Service) retireAppDevice(ctx context.Context, userID uuid.UUID, token string) error {
	return s.retireFoodDevice(ctx, userID, token)
}
