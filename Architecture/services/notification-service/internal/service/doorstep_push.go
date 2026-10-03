package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Doorstep pushes (home services, lane L-E, 2026-10-04).
//
// Two audiences, two installed apps, one registry: contracts/doorstep/
// asyncapi.yaml `x-push-types`.
//
//	doorstep.*      → customer      → momentum      (channel doorstep_updates)
//	doorstep.pro.*  → professional  → doorstep_pro  (channels doorstep_pro_offers,
//	                                                 doorstep_pro_jobs,
//	                                                 doorstep_pro_account,
//	                                                 doorstep_pro_earnings)
//
// A push reaches ONLY devices registered for its app (user_devices.app,
// migration 013 adds doorstep_pro).
//
// The customer's pushes follow the `doorstep` preference category (migration
// 013: push_doorstep / inapp_doorstep, both default on), the master push
// toggle and quiet hours, exactly like Feast's food_orders. Each also writes
// an inbox row (entity doorstep_booking) when inapp_doorstep allows it.
//
// The professional's pushes are operational — an offer expires in minutes, a
// cancelled job frees a slot — so no Momentum preference gates them; Android
// channel settings in the pro app are the professional's control. They keep
// an inbox row (job, account, document or settlement history), except offers,
// which are gone minutes later. Account suppression applies to everyone.
//
// A push NEVER carries an address, a phone number, an OTP or a document
// number: the data map is built from the registry's keys only
// (DoorstepPushData), and validate refuses any other key.
//
// Delivery is AT MOST ONCE per (dedup key, recipient, app, type), claimed
// before any send; the dedup key is the event id. On a dedup-store blip the
// push is sent anyway and the per-booking collapse key bounds a duplicate to
// a device-side replace.

// Device app for the Doorstep professional app.
const AppDoorstepPro = postgres.AppDoorstepPro

// Android notification channels. The Momentum channel and the pro app's four
// channels are created by the respective Android apps (lanes L-I and L-J).
const (
	DoorstepChannelUpdates     = "doorstep_updates"
	DoorstepChannelProOffers   = "doorstep_pro_offers"
	DoorstepChannelProJobs     = "doorstep_pro_jobs"
	DoorstepChannelProAccount  = "doorstep_pro_account"
	DoorstepChannelProEarnings = "doorstep_pro_earnings"
)

// Inbox entity types. The entity id is the booking for every customer push
// and the professional's job pushes, the offer for offers (no inbox row), the
// professional record for account pushes, the document and the settlement
// for theirs.
const (
	DoorstepEntityBooking    = "doorstep_booking"
	DoorstepEntityJob        = "doorstep_job"
	DoorstepEntityOffer      = "doorstep_offer"
	DoorstepEntityPro        = "doorstep_pro"
	DoorstepEntityDocument   = "doorstep_pro_document"
	DoorstepEntitySettlement = "doorstep_settlement"
)

// Push types — the x-push-types registry keys, verbatim.
const (
	DoorstepTypeBookingConfirmed        = "doorstep.booking.confirmed"
	DoorstepTypeBookingAssigned         = "doorstep.booking.assigned"
	DoorstepTypeBookingReassigned       = "doorstep.booking.reassigned"
	DoorstepTypeBookingProEnRoute       = "doorstep.booking.pro_en_route"
	DoorstepTypeBookingProArrived       = "doorstep.booking.pro_arrived"
	DoorstepTypeBookingStarted          = "doorstep.booking.started"
	DoorstepTypeBookingExtrasProposed   = "doorstep.booking.extras_proposed"
	DoorstepTypeBookingExtrasPaymentDue = "doorstep.booking.extras_payment_due"
	DoorstepTypeBookingCompleted        = "doorstep.booking.completed"
	DoorstepTypeBookingCancelled        = "doorstep.booking.cancelled"
	DoorstepTypeBookingExpired          = "doorstep.booking.expired"
	DoorstepTypeBookingRefundIssued     = "doorstep.booking.refund_issued"
	DoorstepTypeBookingReminder         = "doorstep.booking.reminder"
	DoorstepTypeBookingProNoShow        = "doorstep.booking.pro_no_show"
	DoorstepTypeOutstandingDue          = "doorstep.outstanding.due"
	DoorstepTypeMessageNew              = "doorstep.message.new"
	DoorstepTypeReworkUpdated           = "doorstep.rework.updated"

	DoorstepTypeProOfferNew                = "doorstep.pro.offer.new"
	DoorstepTypeProOfferExpired            = "doorstep.pro.offer.expired"
	DoorstepTypeProJobCancelled            = "doorstep.pro.job.cancelled"
	DoorstepTypeProJobRescheduled          = "doorstep.pro.job.rescheduled"
	DoorstepTypeProJobReminder             = "doorstep.pro.job.reminder"
	DoorstepTypeProExtrasApproved          = "doorstep.pro.extras.approved"
	DoorstepTypeProExtrasDeclined          = "doorstep.pro.extras.declined"
	DoorstepTypeProExtrasPaid              = "doorstep.pro.extras.paid"
	DoorstepTypeProMessageNew              = "doorstep.pro.message.new"
	DoorstepTypeProApplicationApproved     = "doorstep.pro.application.approved"
	DoorstepTypeProApplicationRejected     = "doorstep.pro.application.rejected"
	DoorstepTypeProAccountSuspended        = "doorstep.pro.account.suspended"
	DoorstepTypeProAccountReinstated       = "doorstep.pro.account.reinstated"
	DoorstepTypeProDocumentReviewed        = "doorstep.pro.document.reviewed"
	DoorstepTypeProBackgroundCheckExpiring = "doorstep.pro.background_check.expiring"
	DoorstepTypeProRatingReceived          = "doorstep.pro.rating.received"
	DoorstepTypeProSettlementComputed      = "doorstep.pro.settlement.computed"
)

// DoorstepPushSpec is one registry entry: where a push type goes and which
// contract data keys it may carry.
type DoorstepPushSpec struct {
	App        string
	Channel    string
	EntityType string
	// DataKeys are the contract's `data` keys for the type (x-push-types),
	// on top of the transport keys every Doorstep push carries.
	DataKeys []string
	// Inbox: the push also writes an inbox row.
	Inbox bool
}

func momentumSpec(keys ...string) DoorstepPushSpec {
	return DoorstepPushSpec{App: AppMomentum, Channel: DoorstepChannelUpdates, EntityType: DoorstepEntityBooking, DataKeys: keys, Inbox: true}
}

func proSpec(channel, entity string, inbox bool, keys ...string) DoorstepPushSpec {
	return DoorstepPushSpec{App: AppDoorstepPro, Channel: channel, EntityType: entity, DataKeys: keys, Inbox: inbox}
}

// doorstepPushSpecs is the registry. TestDoorstepRegistryMatchesContract
// (internal/events) pins it to the asyncapi file in both directions.
var doorstepPushSpecs = map[string]DoorstepPushSpec{
	DoorstepTypeBookingConfirmed:        momentumSpec("booking_id"),
	DoorstepTypeBookingAssigned:         momentumSpec("booking_id", "pro_first_name"),
	DoorstepTypeBookingReassigned:       momentumSpec("booking_id"),
	DoorstepTypeBookingProEnRoute:       momentumSpec("booking_id", "eta_minutes"),
	DoorstepTypeBookingProArrived:       momentumSpec("booking_id"),
	DoorstepTypeBookingStarted:          momentumSpec("booking_id"),
	DoorstepTypeBookingExtrasProposed:   momentumSpec("booking_id", "extra_id", "total_paise"),
	DoorstepTypeBookingExtrasPaymentDue: momentumSpec("booking_id", "bill_id", "amount_paise"),
	DoorstepTypeBookingCompleted:        momentumSpec("booking_id"),
	DoorstepTypeBookingCancelled:        momentumSpec("booking_id", "refund_paise"),
	DoorstepTypeBookingExpired:          momentumSpec("booking_id"),
	DoorstepTypeBookingRefundIssued:     momentumSpec("booking_id", "amount_paise"),
	DoorstepTypeBookingReminder:         momentumSpec("booking_id", "slot_start"),
	DoorstepTypeBookingProNoShow:        momentumSpec("booking_id"),
	DoorstepTypeOutstandingDue:          momentumSpec("booking_id", "bill_id", "amount_paise"),
	DoorstepTypeMessageNew:              momentumSpec("booking_id", "message_id"),
	DoorstepTypeReworkUpdated:           momentumSpec("booking_id", "rework_id"),

	DoorstepTypeProOfferNew:                proSpec(DoorstepChannelProOffers, DoorstepEntityOffer, false, "offer_id", "booking_id", "expires_at"),
	DoorstepTypeProOfferExpired:            proSpec(DoorstepChannelProOffers, DoorstepEntityOffer, false, "offer_id"),
	DoorstepTypeProJobCancelled:            proSpec(DoorstepChannelProJobs, DoorstepEntityJob, true, "booking_id"),
	DoorstepTypeProJobRescheduled:          proSpec(DoorstepChannelProJobs, DoorstepEntityJob, true, "booking_id", "slot_start"),
	DoorstepTypeProJobReminder:             proSpec(DoorstepChannelProJobs, DoorstepEntityJob, true, "booking_id", "slot_start"),
	DoorstepTypeProExtrasApproved:          proSpec(DoorstepChannelProJobs, DoorstepEntityJob, true, "booking_id", "extra_id"),
	DoorstepTypeProExtrasDeclined:          proSpec(DoorstepChannelProJobs, DoorstepEntityJob, true, "booking_id", "extra_id"),
	DoorstepTypeProExtrasPaid:              proSpec(DoorstepChannelProJobs, DoorstepEntityJob, true, "booking_id", "bill_id"),
	DoorstepTypeProMessageNew:              proSpec(DoorstepChannelProJobs, DoorstepEntityJob, true, "booking_id", "message_id"),
	DoorstepTypeProApplicationApproved:     proSpec(DoorstepChannelProAccount, DoorstepEntityPro, true, "pro_id"),
	DoorstepTypeProApplicationRejected:     proSpec(DoorstepChannelProAccount, DoorstepEntityPro, true, "pro_id"),
	DoorstepTypeProAccountSuspended:        proSpec(DoorstepChannelProAccount, DoorstepEntityPro, true, "pro_id"),
	DoorstepTypeProAccountReinstated:       proSpec(DoorstepChannelProAccount, DoorstepEntityPro, true, "pro_id"),
	DoorstepTypeProDocumentReviewed:        proSpec(DoorstepChannelProAccount, DoorstepEntityDocument, true, "document_id", "decision"),
	DoorstepTypeProBackgroundCheckExpiring: proSpec(DoorstepChannelProAccount, DoorstepEntityPro, true, "valid_until"),
	DoorstepTypeProRatingReceived:          proSpec(DoorstepChannelProJobs, DoorstepEntityJob, true, "booking_id", "stars"),
	DoorstepTypeProSettlementComputed:      proSpec(DoorstepChannelProEarnings, DoorstepEntitySettlement, true, "settlement_id", "net_paise"),
}

// DoorstepPushSpecFor returns the registry entry of a push type.
func DoorstepPushSpecFor(pushType string) (DoorstepPushSpec, bool) {
	s, ok := doorstepPushSpecs[pushType]
	return s, ok
}

// DoorstepPushTypes lists every registered push type, sorted.
func DoorstepPushTypes() []string {
	out := make([]string, 0, len(doorstepPushSpecs))
	for t := range doorstepPushSpecs {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// doorstepTransportKeys are the keys every Doorstep push carries besides the
// type's contract keys.
var doorstepTransportKeys = map[string]bool{
	"type":                     true,
	"entity_type":              true,
	"entity_id":                true,
	"deep_link":                true,
	"title":                    true,
	"body":                     true,
	"collapse_key":             true, // transport: lifted into android.collapse_key
	push.AndroidChannelDataKey: true, // transport: lifted into android.notification.channel_id
	push.AndroidTTLDataKey:     true, // transport: lifted into android.ttl (offers only)
}

// DoorstepPushAllowedDataKeys is the COMPLETE set of data keys a push of
// pushType may carry.
func DoorstepPushAllowedDataKeys(pushType string) map[string]bool {
	out := make(map[string]bool, len(doorstepTransportKeys)+4)
	for k := range doorstepTransportKeys {
		out[k] = true
	}
	if spec, ok := doorstepPushSpecs[pushType]; ok {
		for _, k := range spec.DataKeys {
			out[k] = true
		}
	}
	return out
}

// DoorstepPush is one rendered push for one recipient.
type DoorstepPush struct {
	// DedupKey is stable for the logical delivery: the event id (or a hash
	// of the Kafka message when an event carries none).
	DedupKey    string
	RecipientID uuid.UUID
	App         string
	Type        string

	Title    string
	Body     string
	DeepLink string

	// EntityID is the inbox row's entity (see the entity types above) and
	// the push's entity_id.
	EntityID uuid.UUID
	// CollapseKey is the device-side replace key: per booking for the
	// customer, per offer, per job, per account record.
	CollapseKey string
	// Data holds the type's contract data keys only.
	Data map[string]string
	// TTL, when set, is how long FCM may hold the push (offers: until the
	// offer expires).
	TTL time.Duration

	CreatedAt time.Time
}

// DoorstepPushOutcome says what happened to one push.
type DoorstepPushOutcome string

const (
	DoorstepPushSent       DoorstepPushOutcome = "sent"
	DoorstepPushDuplicate  DoorstepPushOutcome = "duplicate"
	DoorstepPushSuppressed DoorstepPushOutcome = "suppressed" // account deactivated / deletion scheduled
	DoorstepPushMuted      DoorstepPushOutcome = "muted"      // customer preferences: no push
	DoorstepPushNoDevices  DoorstepPushOutcome = "no_devices" // nothing registered for the app, or every send failed
)

// doorstepDedupNamespace scopes UUIDv5 dedup ids for Doorstep pushes.
var doorstepDedupNamespace = uuid.MustParse("3f6a1d2e-8b4c-4e7a-9d15-6c2b0e8f4a93")

// DoorstepDedupID is the notify_meta.event_dedup key for one push.
func DoorstepDedupID(p DoorstepPush) uuid.UUID {
	return uuid.NewSHA1(doorstepDedupNamespace,
		[]byte(p.DedupKey+"|"+p.RecipientID.String()+"|"+p.App+"|"+p.Type))
}

func (p DoorstepPush) customerFacing() bool { return p.App == AppMomentum }

func (p DoorstepPush) validate() error {
	if p.RecipientID == uuid.Nil {
		return errors.New("doorstep push: no recipient")
	}
	if p.DedupKey == "" {
		return errors.New("doorstep push: no dedup key")
	}
	spec, known := doorstepPushSpecs[p.Type]
	if !known {
		return fmt.Errorf("doorstep push: unknown type %q", p.Type)
	}
	if p.App != spec.App {
		return fmt.Errorf("doorstep push: %s belongs to app %s, not %q", p.Type, spec.App, p.App)
	}
	if p.EntityID == uuid.Nil {
		return errors.New("doorstep push: no entity id")
	}
	if p.CollapseKey == "" {
		return errors.New("doorstep push: no collapse key")
	}
	if p.Title == "" || p.Body == "" || p.DeepLink == "" {
		return fmt.Errorf("doorstep push: %s without title, body or deep link", p.Type)
	}
	allowed := map[string]bool{}
	for _, k := range spec.DataKeys {
		allowed[k] = true
	}
	for k := range p.Data {
		if !allowed[k] {
			return fmt.Errorf("doorstep push: %s may not carry data key %q", p.Type, k)
		}
	}
	return nil
}

// DoorstepPushData renders the complete FCM data payload for p: the
// transport keys plus the type's contract keys.
func DoorstepPushData(p DoorstepPush) map[string]string {
	spec := doorstepPushSpecs[p.Type]
	data := make(map[string]string, len(p.Data)+9)
	for k, v := range p.Data {
		data[k] = v
	}
	data["type"] = p.Type
	data["entity_type"] = spec.EntityType
	data["entity_id"] = p.EntityID.String()
	data["deep_link"] = p.DeepLink
	data["title"] = p.Title
	data["body"] = p.Body
	data["collapse_key"] = p.CollapseKey
	if spec.Channel != "" {
		data[push.AndroidChannelDataKey] = spec.Channel
	}
	if p.TTL > 0 {
		secs := int64(p.TTL.Round(time.Second) / time.Second)
		if secs < 1 {
			secs = 1
		}
		data[push.AndroidTTLDataKey] = fmt.Sprintf("%ds", secs)
	}
	return data
}

// doorstepPushTransports is the dependency set one Doorstep push rides on;
// *Service implements it and runDoorstepPush owns the rules so they are
// testable without live stores.
type doorstepPushTransports interface {
	recipientSuppressed(ctx context.Context, userID uuid.UUID, notifType string) bool
	claimPush(ctx context.Context, id uuid.UUID) (bool, error)
	doorstepCustomerDecision(ctx context.Context, userID uuid.UUID, pushType string) DeliveryDecision
	writeDoorstepInbox(ctx context.Context, p DoorstepPush) error
	appDevices(ctx context.Context, userID uuid.UUID, app string) ([]postgres.UserDevice, error)
	sendAppPush(ctx context.Context, d postgres.UserDevice, title, body string, data map[string]string) error
	retireAppDevice(ctx context.Context, userID uuid.UUID, token string) error
}

// DeliverDoorstepPush delivers one Doorstep push. See the notes at the top
// of the file.
func (s *Service) DeliverDoorstepPush(ctx context.Context, p DoorstepPush) (DoorstepPushOutcome, error) {
	return runDoorstepPush(ctx, s, p)
}

func runDoorstepPush(ctx context.Context, t doorstepPushTransports, p DoorstepPush) (DoorstepPushOutcome, error) {
	if err := p.validate(); err != nil {
		return "", err
	}
	if t.recipientSuppressed(ctx, p.RecipientID, p.Type) {
		return DoorstepPushSuppressed, nil
	}

	first, err := t.claimPush(ctx, DoorstepDedupID(p))
	switch {
	case err != nil:
		slog.Warn("doorstep push: dedup claim failed; sending anyway (collapse key bounds a duplicate)",
			"type", p.Type, "app", p.App, "error", err)
	case !first:
		return DoorstepPushDuplicate, nil
	}

	spec := doorstepPushSpecs[p.Type]
	if p.customerFacing() {
		// The customer's doorstep category: inapp_doorstep gates the row,
		// push_doorstep (plus the master toggle and quiet hours) the push.
		decision := t.doorstepCustomerDecision(ctx, p.RecipientID, p.Type)
		if decision.CreateInbox && spec.Inbox {
			if err := t.writeDoorstepInbox(ctx, p); err != nil {
				slog.Warn("doorstep push: inbox row failed", "type", p.Type, "error", err)
			}
		}
		if !decision.SendPush {
			return DoorstepPushMuted, nil
		}
	} else if spec.Inbox {
		// The professional's job and account history: operational, no
		// Momentum preference applies.
		if err := t.writeDoorstepInbox(ctx, p); err != nil {
			slog.Warn("doorstep push: inbox row failed", "type", p.Type, "error", err)
		}
	}

	devices, err := t.appDevices(ctx, p.RecipientID, p.App)
	if err != nil {
		return "", fmt.Errorf("doorstep push: devices: %w", err)
	}
	data := DoorstepPushData(p)
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
				slog.Warn("doorstep push: retire rejected device failed", "platform", d.Platform, "error", rerr)
			}
		default:
			slog.Warn("doorstep push: send failed", "type", p.Type, "app", p.App, "platform", d.Platform, "error", err)
		}
	}
	if sent == 0 {
		return DoorstepPushNoDevices, nil
	}
	return DoorstepPushSent, nil
}

// doorstepCustomerDecision resolves the customer's preferences for one push
// type; every customer type maps to the doorstep category.
func (s *Service) doorstepCustomerDecision(ctx context.Context, userID uuid.UUID, pushType string) DeliveryDecision {
	return s.resolveGeneralDelivery(ctx, userID, pushType)
}

// writeDoorstepInbox records the push's inbox row (and realtime frame) under
// the push's deterministic identity, without a second push.
func (s *Service) writeDoorstepInbox(ctx context.Context, p DoorstepPush) error {
	if s.scyllaStore == nil {
		return nil
	}
	spec := doorstepPushSpecs[p.Type]
	decision := DeliveryDecision{CreateInbox: true, SendWebSocket: true}
	return s.deliverWithDecision(ctx, decision, p.RecipientID, p.RecipientID, p.Type,
		spec.EntityType, p.EntityID, p.DeepLink, p.CreatedAt, DoorstepDedupID(p).String(),
		RenderOverride{Title: p.Title, Body: p.Body, CollapseKey: p.CollapseKey})
}
