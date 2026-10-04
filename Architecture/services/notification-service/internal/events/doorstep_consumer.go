// Doorstep (home services) push consumer — lane L-E, 2026-10-04.
//
// WIRE FORMAT (contracts/doorstep/asyncapi.yaml). doorstep-service publishes
// through its schema-local outbox and shared/outbox: the Kafka value is the
// envelope {event_id, event_type, version, occurred_at, booking_id?,
// customer_user_id?, pro_user_id?, data}, the event type is mirrored in the
// `event_type` header, and the key is the booking id (or the professional's
// user id for doorstep.pro.*). A value that is a bare payload with the type
// only in the header is accepted too.
//
// ROUTING is the contract's push registry (`x-push-types`), held here as
// doorstepRules and pinned to the asyncapi file by
// TestDoorstepRegistryMatchesContract: customer types go to the
// customer_user_id's Momentum install, `doorstep.pro.*` types to the
// pro_user_id's doorstep_pro install. Recipients come from the event only;
// this service never reads doorstep-service's database. An event without its
// recipient is counted (missing_recipient) and logged with the field
// doorstep-service must add.
//
// IDEMPOTENCY. Every push is claimed in notify_meta.event_dedup under (event
// id, recipient, app, type) before it is sent (service.DeliverDoorstepPush),
// so a Kafka redelivery or an outbox re-publish sends nothing twice. An
// event without an event id is keyed on a SHA-256 of (type, key, value).
//
// SAFETY. doorstep.incident.raised pages the configured, staff-verified
// responders (the dating panic mechanism: ResponderDirectory +
// CreateSafetyAlertNotification), writes an ops alert and emails ops. No push
// ever carries a customer address, phone number, OTP or document number: the
// events carry none (locality at most), the copy names none, and a last-line
// guard drops any push whose text or data would repeat such a value from the
// event.
//
// Unknown event types are counted and ignored — never an error, never a
// crash. A panic anywhere in handling is recovered and counted.
package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/segmentio/kafka-go"
)

// DoorstepTopic is the Kafka topic doorstep-service publishes to.
const DoorstepTopic = "doorstep.events"

// Doorstep event types (asyncapi channel doorstep.events).
const (
	doorstepEventBookingCreated          = "doorstep.booking.created"
	doorstepEventBookingConfirmed        = "doorstep.booking.confirmed"
	doorstepEventBookingExpired          = "doorstep.booking.expired"
	doorstepEventBookingAssigned         = "doorstep.booking.assigned"
	doorstepEventBookingReassigned       = "doorstep.booking.reassigned"
	doorstepEventBookingUnassignedAlert  = "doorstep.booking.unassigned_alert"
	doorstepEventBookingEnRoute          = "doorstep.booking.en_route"
	doorstepEventBookingArrived          = "doorstep.booking.arrived"
	doorstepEventBookingStarted          = "doorstep.booking.started"
	doorstepEventBookingExtrasProposed   = "doorstep.booking.extras_proposed"
	doorstepEventBookingExtrasDecided    = "doorstep.booking.extras_decided"
	doorstepEventBookingExtrasPaymentDue = "doorstep.booking.extras_payment_due"
	doorstepEventBookingExtrasPaid       = "doorstep.booking.extras_paid"
	doorstepEventBookingOutstanding      = "doorstep.booking.outstanding_created"
	doorstepEventBookingCompleted        = "doorstep.booking.completed"
	doorstepEventBookingCancelled        = "doorstep.booking.cancelled"
	doorstepEventBookingRescheduled      = "doorstep.booking.rescheduled"
	doorstepEventBookingNoShow           = "doorstep.booking.no_show"
	doorstepEventBookingRefunded         = "doorstep.booking.refunded"
	doorstepEventBookingRated            = "doorstep.booking.rated"
	doorstepEventBookingReworkRequested  = "doorstep.booking.rework_requested"
	doorstepEventBookingMessageSent      = "doorstep.booking.message_sent"
	doorstepEventBookingReminder         = "doorstep.booking.reminder"
	doorstepEventIncidentRaised          = "doorstep.incident.raised"
	doorstepEventProApplied              = "doorstep.pro.applied"
	doorstepEventProStatusChanged        = "doorstep.pro.status_changed"
	doorstepEventProDocumentReviewed     = "doorstep.pro.document_reviewed"
	doorstepEventProBgCheckExpiring      = "doorstep.pro.background_check_expiring"
	doorstepEventProOfferCreated         = "doorstep.pro.offer_created"
	doorstepEventProOfferClosed          = "doorstep.pro.offer_closed"
	doorstepEventProSettlementComputed   = "doorstep.pro.settlement_computed"

	// A3/A4 events the push registry does not cover.
	doorstepEventBookingProLate          = "doorstep.booking.pro_late"
	doorstepEventBookingRefundFailed     = "doorstep.booking.refund_failed"
	doorstepEventBookingPaymentAttention = "doorstep.booking.payment_attention"

	// B1 (4 Oct 2026): professionals' prices and the pick-a-professional flow.
	doorstepEventProPriceSubmitted     = "doorstep.pro.price_submitted"
	doorstepEventProPriceReviewed      = "doorstep.pro.price_reviewed"
	doorstepEventBookingProUnavailable = "doorstep.booking.pro_unavailable"
	doorstepEventBookingProChanged     = "doorstep.booking.pro_changed"
)

// doorstepSilentEvents are known events with no push: a booking awaiting
// payment and a fresh application are nobody's news yet.
//
// doorstep.booking.pro_late has no push type in the contract's x-push-types
// (the asyncapi says so); the customer learns it from the booking's realtime
// frame (doorstep.booking.pro_late, free_cancel) while the app is open. A
// customer push needs a registry entry first; until then it is known and
// silent rather than counted unknown.
var doorstepSilentEvents = map[string]bool{
	doorstepEventBookingCreated: true,
	doorstepEventProApplied:     true,
	doorstepEventBookingProLate: true,
}

// doorstepOpsEvents become ops alert rows (the admin console's live feed),
// never a push: the price review queue and payment trouble.
var doorstepOpsEvents = map[string]bool{
	doorstepEventBookingUnassignedAlert:  true,
	doorstepEventProPriceSubmitted:       true,
	doorstepEventBookingRefundFailed:     true,
	doorstepEventBookingPaymentAttention: true,
}

// Safety notification type and ops alert kinds.
const (
	NotifDoorstepIncidentResponder = "doorstep.safety.incident.responder"

	OpsAlertDoorstepIncidentPaged       = "doorstep_incident_paged"
	OpsAlertDoorstepIncidentNoResponder = "doorstep_incident_no_responder"
	OpsAlertDoorstepIncidentRaised      = "doorstep_incident_raised"
	OpsAlertDoorstepBookingUnassigned   = "doorstep_booking_unassigned"
	OpsAlertDoorstepPriceSubmitted      = "doorstep_pro_price_submitted"
	OpsAlertDoorstepRefundFailed        = "doorstep_refund_failed"
	OpsAlertDoorstepPaymentAttention    = "doorstep_payment_attention"
)

type doorstepOutcome string

const (
	doorstepOutcomePlanned          doorstepOutcome = "planned"
	doorstepOutcomeIgnored          doorstepOutcome = "ignored"   // known, deliberately not pushed
	doorstepOutcomeUnknown          doorstepOutcome = "unknown"   // an event type this build does not know
	doorstepOutcomeMalformed        doorstepOutcome = "malformed" // no event type, bad JSON, or a missing id
	doorstepOutcomeMissingRecipient doorstepOutcome = "missing_recipient"
	doorstepOutcomeOfferExpired     doorstepOutcome = "offer_expired" // offer (or B1 choice window) already lapsed: no push
	doorstepOutcomePIIBlocked       doorstepOutcome = "pii_blocked"
	doorstepOutcomeDeliveryError    doorstepOutcome = "delivery_error"
	doorstepOutcomeIncident         doorstepOutcome = "incident"
	doorstepOutcomeOpsAlert         doorstepOutcome = "ops_alert"
	doorstepOutcomePanic            doorstepOutcome = "panic"
)

var (
	doorstepEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost",
		Subsystem: "notification_service",
		Name:      "doorstep_events_total",
		Help:      "Doorstep events by handling outcome. Unknown event types are counted here and ignored.",
	}, []string{"outcome"})
	doorstepPushesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost",
		Subsystem: "notification_service",
		Name:      "doorstep_pushes_total",
		Help:      "Doorstep pushes by type, app and delivery outcome.",
	}, []string{"type", "app", "outcome"})
)

// doorstepPushDeliverer is the service seam; *service.Service satisfies it.
type doorstepPushDeliverer interface {
	DeliverDoorstepPush(ctx context.Context, p service.DoorstepPush) (service.DoorstepPushOutcome, error)
}

// doorstepSafetyDeps is what incident paging and ops alerts need;
// DoorstepSafetyAdapter is the production implementation.
type doorstepSafetyDeps interface {
	ClaimEventDedup(ctx context.Context, id uuid.UUID) (bool, error)
	Responders(ctx context.Context) []uuid.UUID
	SendSafetyAlert(ctx context.Context, a service.SafetyAlert) error
	RecordOpsAlert(ctx context.Context, a postgres.OpsAlert) error
	EmailOps(ctx context.Context, subject, body string) error
}

// DoorstepConsumer turns doorstep.events into pushes, inbox rows and pages.
type DoorstepConsumer struct {
	reader    *kafka.Reader
	deliverer doorstepPushDeliverer
	safety    doorstepSafetyDeps
	now       func() time.Time

	mu     sync.Mutex
	counts map[doorstepOutcome]int64
}

func NewDoorstepConsumerWithDialer(brokers []string, groupID, topic string, svc doorstepPushDeliverer, dialer *kafka.Dialer) *DoorstepConsumer {
	c := newDoorstepConsumer(svc)
	c.reader = kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return c
}

func newDoorstepConsumer(d doorstepPushDeliverer) *DoorstepConsumer {
	return &DoorstepConsumer{deliverer: d, now: time.Now, counts: map[doorstepOutcome]int64{}}
}

// WithSafety wires incident paging and ops alerts. Without it an incident is
// logged at ERROR and nobody is paged.
func (c *DoorstepConsumer) WithSafety(d doorstepSafetyDeps) *DoorstepConsumer {
	c.safety = d
	return c
}

// Start consumes sequentially so one booking's pushes keep their order.
func (c *DoorstepConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("doorstep consumer shutting down\n")
			} else {
				log.Printf("Doorstep consumer error: %v\n", err)
			}
			break
		}
		c.processMessage(ctx, m)
	}
}

func (c *DoorstepConsumer) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

func (c *DoorstepConsumer) count(o doorstepOutcome) {
	doorstepEventsTotal.WithLabelValues(string(o)).Inc()
	c.mu.Lock()
	c.counts[o]++
	c.mu.Unlock()
}

func (c *DoorstepConsumer) countOf(o doorstepOutcome) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[o]
}

// doorstepFields is every payload field a push, page or alert may use. Only
// these are decoded; nothing else on an event can reach a push.
type doorstepFields struct {
	BookingID      string `json:"booking_id"`
	CustomerUserID string `json:"customer_user_id"`
	ProUserID      string `json:"pro_user_id"`
	CityCode       string `json:"city_code"`
	CategorySlug   string `json:"category_slug"`
	SlotStart      string `json:"slot_start"`

	ProFirstName string `json:"pro_first_name"`
	EtaMinutes   *int64 `json:"eta_minutes"`
	ExtraID      string `json:"extra_id"`
	TotalPaise   *int64 `json:"total_paise"`
	BillID       string `json:"bill_id"`
	AmountPaise  *int64 `json:"amount_paise"`
	RefundPaise  *int64 `json:"refund_paise"`
	Decision     string `json:"decision"`
	Party        string `json:"party"`
	SenderKind   string `json:"sender_kind"`
	MessageID    string `json:"message_id"`
	ReworkID     string `json:"rework_id"`
	RaterKind    string `json:"rater_kind"`
	Stars        *int64 `json:"stars"`
	CancelledBy  string `json:"cancelled_by"`

	OfferID      string `json:"offer_id"`
	ExpiresAt    string `json:"expires_at"`
	Outcome      string `json:"outcome"`
	ProID        string `json:"pro_id"`
	FromStatus   string `json:"from_status"`
	ToStatus     string `json:"to_status"`
	DocumentID   string `json:"document_id"`
	ValidUntil   string `json:"valid_until"`
	SettlementID string `json:"settlement_id"`
	NetPaise     *int64 `json:"net_paise"`

	IncidentID       string `json:"incident_id"`
	Kind             string `json:"kind"`
	Severity         string `json:"severity"`
	RaisedByKind     string `json:"raised_by_kind"`
	ProAutoSuspended bool   `json:"pro_auto_suspended"`
	MinutesToSlot    *int64 `json:"minutes_to_slot"`

	// B1. new_pro_user_id only gates the pro_changed push (the change went
	// through); nothing is ever sent to it from here. previous_pro_user_id is
	// not decoded: the contract has no doorstep_pro push type for a
	// professional who lost a job.
	Cause              string `json:"cause"`
	ChoiceDeadline     string `json:"choice_deadline"`
	DifferencePaise    *int64 `json:"difference_paise"`
	NewProUserID       string `json:"new_pro_user_id"`
	PriceID            string `json:"price_id"`
	ServiceID          string `json:"service_id"`
	ItemKind           string `json:"item_kind"`
	Unit               string `json:"unit"`
	PricePaise         *int64 `json:"price_paise"`
	PreviousPricePaise *int64 `json:"previous_price_paise"`

	// Payment trouble (ops only).
	RefundID         string `json:"refund_id"`
	PaymentEventType string `json:"payment_event_type"`
}

// doorstepEvent is one decoded doorstep.events message.
type doorstepEvent struct {
	Type       string
	ID         string
	OccurredAt time.Time
	Fields     doorstepFields
}

type doorstepEnvelope struct {
	EventID        string          `json:"event_id"`
	EventType      string          `json:"event_type"`
	OccurredAt     string          `json:"occurred_at"`
	BookingID      *string         `json:"booking_id"`
	CustomerUserID *string         `json:"customer_user_id"`
	ProUserID      *string         `json:"pro_user_id"`
	Data           json.RawMessage `json:"data"`
}

// decodeDoorstepMessage extracts the event. detail is set when the message is
// malformed.
func decodeDoorstepMessage(m kafka.Message) (ev doorstepEvent, detail string) {
	header := ""
	for _, h := range m.Headers {
		if h.Key == "event_type" {
			header = strings.TrimSpace(string(h.Value))
		}
	}
	var env doorstepEnvelope
	if err := json.Unmarshal(m.Value, &env); err != nil {
		return ev, "value is not a JSON object"
	}
	data := json.RawMessage(m.Value)
	if d := strings.TrimSpace(string(env.Data)); strings.HasPrefix(d, "{") {
		data = env.Data
	}
	ev.Type = strings.TrimSpace(env.EventType)
	if ev.Type == "" {
		ev.Type = header
	}
	if ev.Type == "" {
		return ev, "no event type"
	}
	if header != "" && header != ev.Type {
		return ev, fmt.Sprintf("header event_type %q disagrees with the envelope's %q", header, ev.Type)
	}
	ev.ID = strings.TrimSpace(env.EventID)
	if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(env.OccurredAt)); err == nil {
		ev.OccurredAt = t.UTC()
	}
	if err := json.Unmarshal(data, &ev.Fields); err != nil {
		return ev, "data is not a JSON object of the contract's shape"
	}
	// The envelope is the contract's routing source: it wins where set.
	if env.BookingID != nil && strings.TrimSpace(*env.BookingID) != "" {
		ev.Fields.BookingID = *env.BookingID
	}
	if env.CustomerUserID != nil && strings.TrimSpace(*env.CustomerUserID) != "" {
		ev.Fields.CustomerUserID = *env.CustomerUserID
	}
	if env.ProUserID != nil && strings.TrimSpace(*env.ProUserID) != "" {
		ev.Fields.ProUserID = *env.ProUserID
	}
	return ev, ""
}

// doorstepDedupBase is the per-event idempotency key.
func doorstepDedupBase(ev doorstepEvent, m kafka.Message) string {
	if ev.ID != "" {
		return "event:" + ev.ID
	}
	h := sha256.New()
	h.Write([]byte(ev.Type))
	h.Write([]byte{0})
	h.Write(m.Key)
	h.Write([]byte{0})
	h.Write(m.Value)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// processMessage never returns an error and never panics: every outcome is
// counted and logged, and the offset moves on.
func (c *DoorstepConsumer) processMessage(ctx context.Context, m kafka.Message) {
	defer func() {
		if r := recover(); r != nil {
			c.count(doorstepOutcomePanic)
			slog.Error("doorstep consumer: recovered panic", "panic", fmt.Sprint(r))
		}
	}()

	ev, detail := decodeDoorstepMessage(m)
	if detail != "" {
		c.count(doorstepOutcomeMalformed)
		slog.Warn("doorstep consumer: malformed message", "event", ev.Type, "detail", detail,
			"partition", m.Partition, "offset", m.Offset)
		return
	}
	now := c.now().UTC()

	switch ev.Type {
	case doorstepEventIncidentRaised:
		c.count(doorstepOutcomeIncident)
		if err := processDoorstepIncident(ctx, c.safety, ev, now); err != nil {
			c.count(doorstepOutcomeMalformed)
			slog.Error("doorstep consumer: incident not paged", "error", err)
		}
		return
	case doorstepEventBookingUnassignedAlert:
		c.count(doorstepOutcomeOpsAlert)
		recordDoorstepUnassigned(ctx, c.safety, ev)
		return
	case doorstepEventProPriceSubmitted, doorstepEventBookingRefundFailed, doorstepEventBookingPaymentAttention:
		c.count(doorstepOutcomeOpsAlert)
		recordDoorstepOpsEvent(ctx, c.safety, ev)
		return
	}

	plan := planDoorstepPushes(ev, doorstepDedupBase(ev, m), now)
	switch plan.outcome {
	case doorstepOutcomeUnknown, doorstepOutcomeIgnored:
		c.count(plan.outcome)
		return
	case doorstepOutcomeMalformed:
		c.count(plan.outcome)
		slog.Warn("doorstep consumer: malformed event", "event", ev.Type, "detail", plan.detail)
		return
	}
	for _, field := range plan.missing {
		c.count(doorstepOutcomeMissingRecipient)
		slog.Warn("doorstep consumer: event lacks its push recipient; doorstep-service must add the field",
			"event", ev.Type, "field", field)
	}
	for range plan.expired {
		c.count(doorstepOutcomeOfferExpired)
	}
	if len(plan.pushes) == 0 {
		return
	}
	c.count(doorstepOutcomePlanned)

	sensitive := doorstepSensitiveValues(m.Value)
	for _, p := range plan.pushes {
		if doorstepPushLeaks(p, sensitive) {
			c.count(doorstepOutcomePIIBlocked)
			slog.Error("doorstep consumer: push dropped — it would have carried an address, phone, code or document value",
				"event", ev.Type, "type", p.Type)
			continue
		}
		if c.deliverer == nil {
			c.count(doorstepOutcomeDeliveryError)
			continue
		}
		outcome, err := c.deliverer.DeliverDoorstepPush(ctx, p)
		if err != nil {
			c.count(doorstepOutcomeDeliveryError)
			slog.Warn("doorstep consumer: delivery failed", "event", ev.Type, "type", p.Type, "app", p.App, "error", err)
			continue
		}
		doorstepPushesTotal.WithLabelValues(p.Type, p.App, string(outcome)).Inc()
	}
}

// ---------------------------------------------------------------------------
// The push registry (contract x-push-types).

type doorstepAudience int

const (
	doorstepToCustomer doorstepAudience = iota
	doorstepToPro
)

// doorstepRule is one x-push-types entry: which event produces it, under
// which condition, for whom, with which words.
type doorstepRule struct {
	pushType  string
	fromEvent string
	audience  doorstepAudience
	// deeplink is the contract's template, verbatim. Momentum links are
	// delivered as the path (the app routes paths, like every other
	// Momentum push); doorstep-pro links keep their scheme.
	deeplink string
	// entityKey names the field holding the push's entity id.
	entityKey string
	// collapse prefixes the per-entity collapse key.
	collapse string
	when     func(f *doorstepFields) bool
	copy     func(f *doorstepFields) (title, body string)
}

const doorstepMomentumScheme = "momentum:/"

func proHas(f *doorstepFields) bool { return strings.TrimSpace(f.ProUserID) != "" }

var doorstepRules = []doorstepRule{
	// ---- Momentum (customer) ------------------------------------------
	{pushType: service.DoorstepTypeBookingConfirmed, fromEvent: doorstepEventBookingConfirmed,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(*doorstepFields) (string, string) {
			return "Booking confirmed", "Payment received. We're assigning a professional for your visit."
		}},
	{pushType: service.DoorstepTypeBookingAssigned, fromEvent: doorstepEventBookingAssigned,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(f *doorstepFields) (string, string) {
			if name := doorstepFirstName(f.ProFirstName); name != "" {
				return "Professional assigned", name + " will take care of your booking."
			}
			return "Professional assigned", "A professional has accepted your booking."
		}},
	// B1: reassigned is emitted only when the customer moved the slot to a
	// time the accepted professional cannot take (cause rescheduled). A
	// professional who drops out is doorstep.booking.pro_unavailable: the
	// customer chooses, nobody is reassigned silently.
	{pushType: service.DoorstepTypeBookingReassigned, fromEvent: doorstepEventBookingReassigned,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		when: func(f *doorstepFields) bool { return f.Cause == "rescheduled" },
		copy: func(f *doorstepFields) (string, string) {
			if at := doorstepDayClock(f.SlotStart); at != "" {
				return "Booking moved", "Your professional can't make " + at + ", so we're offering the new time to another professional."
			}
			return "Booking moved", "Your professional can't make the new time, so we're offering it to another professional."
		}},
	{pushType: service.DoorstepTypeBookingProUnavailable, fromEvent: doorstepEventBookingProUnavailable,
		deeplink: "momentum://doorstep/bookings/{booking_id}/professionals", entityKey: "booking_id", collapse: "doorstep_booking",
		when: func(f *doorstepFields) bool { return doorstepValidTime(f.ChoiceDeadline) },
		copy: func(f *doorstepFields) (string, string) {
			what := "Your professional can't take this booking."
			switch f.Cause {
			case "declined", "offer_expired":
				what = "Your professional couldn't accept this booking."
			case "pro_no_show":
				what = "Your professional didn't arrive."
			case "no_professional":
				what = "No professional is free for this booking."
			}
			if at := doorstepClock(f.ChoiceDeadline); at != "" {
				return "Choose another professional", what + " Pick another or cancel for a full refund by " + at + "."
			}
			return "Choose another professional", what + " Pick another or cancel for a full refund."
		}},
	{pushType: service.DoorstepTypeBookingProChanged, fromEvent: doorstepEventBookingProChanged,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		when: func(f *doorstepFields) bool { return strings.TrimSpace(f.NewProUserID) != "" },
		copy: func(f *doorstepFields) (string, string) {
			if f.DifferencePaise != nil && *f.DifferencePaise < 0 {
				return "New professional confirmed", "Your booking is confirmed with your new professional. " +
					doorstepRupees(-*f.DifferencePaise) + " will be refunded to you."
			}
			return "New professional confirmed", "Your booking is confirmed with your new professional."
		}},
	{pushType: service.DoorstepTypeBookingProEnRoute, fromEvent: doorstepEventBookingEnRoute,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(f *doorstepFields) (string, string) {
			if f.EtaMinutes != nil && *f.EtaMinutes > 0 {
				return "Professional on the way", fmt.Sprintf("Arriving in about %d min.", *f.EtaMinutes)
			}
			return "Professional on the way", "Your professional is heading to you."
		}},
	{pushType: service.DoorstepTypeBookingProArrived, fromEvent: doorstepEventBookingArrived,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(*doorstepFields) (string, string) {
			return "Professional has arrived", "Share the start code from the app when you're ready."
		}},
	{pushType: service.DoorstepTypeBookingStarted, fromEvent: doorstepEventBookingStarted,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(*doorstepFields) (string, string) {
			return "Service started", "Your service is in progress."
		}},
	{pushType: service.DoorstepTypeBookingExtrasProposed, fromEvent: doorstepEventBookingExtrasProposed,
		deeplink: "momentum://doorstep/bookings/{booking_id}/extras", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(f *doorstepFields) (string, string) {
			if f.TotalPaise != nil {
				return "Extra work proposed", "Your professional proposed extra work for " + doorstepRupees(*f.TotalPaise) + ". Review it in the app."
			}
			return "Extra work proposed", "Your professional proposed extra work. Review it in the app."
		}},
	{pushType: service.DoorstepTypeBookingExtrasPaymentDue, fromEvent: doorstepEventBookingExtrasPaymentDue,
		deeplink: "momentum://doorstep/bookings/{booking_id}/extras", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(f *doorstepFields) (string, string) {
			if f.AmountPaise != nil {
				return "Payment due for extras", "Pay " + doorstepRupees(*f.AmountPaise) + " for the extra work to finish your booking."
			}
			return "Payment due for extras", "Pay for the extra work to finish your booking."
		}},
	{pushType: service.DoorstepTypeBookingCompleted, fromEvent: doorstepEventBookingCompleted,
		deeplink: "momentum://doorstep/bookings/{booking_id}/rate", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(*doorstepFields) (string, string) {
			return "Service completed", "How did it go? Rate your professional."
		}},
	{pushType: service.DoorstepTypeBookingCancelled, fromEvent: doorstepEventBookingCancelled,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(f *doorstepFields) (string, string) {
			if f.RefundPaise != nil && *f.RefundPaise > 0 {
				return "Booking cancelled", "Your booking was cancelled. " + doorstepRupees(*f.RefundPaise) + " will be refunded to you."
			}
			return "Booking cancelled", "Your booking was cancelled."
		}},
	{pushType: service.DoorstepTypeBookingExpired, fromEvent: doorstepEventBookingExpired,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(*doorstepFields) (string, string) {
			return "Booking not completed", "Payment didn't finish in time, so the slot was released. Anything charged will be refunded."
		}},
	{pushType: service.DoorstepTypeBookingRefundIssued, fromEvent: doorstepEventBookingRefunded,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(f *doorstepFields) (string, string) {
			if f.AmountPaise != nil {
				return "Refund issued", doorstepRupees(*f.AmountPaise) + " is on its way back to you."
			}
			return "Refund issued", "Your refund is on its way back to you."
		}},
	{pushType: service.DoorstepTypeBookingReminder, fromEvent: doorstepEventBookingReminder,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(f *doorstepFields) (string, string) {
			if at := doorstepClock(f.SlotStart); at != "" {
				return "Visit in about an hour", "Your professional is due at " + at + "."
			}
			return "Visit in about an hour", "Your professional is due soon."
		}},
	{pushType: service.DoorstepTypeBookingProNoShow, fromEvent: doorstepEventBookingNoShow,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		when: func(f *doorstepFields) bool { return f.Party == "pro" },
		copy: func(*doorstepFields) (string, string) {
			return "Professional didn't arrive", "Sorry about that. We're finding someone else or refunding you in full."
		}},
	{pushType: service.DoorstepTypeOutstandingDue, fromEvent: doorstepEventBookingOutstanding,
		deeplink: "momentum://doorstep/outstanding", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(f *doorstepFields) (string, string) {
			if f.AmountPaise != nil {
				return "Payment pending", doorstepRupees(*f.AmountPaise) + " for extra work is unpaid. Pay it to book again."
			}
			return "Payment pending", "Extra work from your last visit is unpaid. Pay it to book again."
		}},
	{pushType: service.DoorstepTypeMessageNew, fromEvent: doorstepEventBookingMessageSent,
		deeplink: "momentum://doorstep/bookings/{booking_id}/chat", entityKey: "booking_id", collapse: "doorstep_chat",
		when: func(f *doorstepFields) bool { return f.SenderKind == "pro" },
		copy: func(*doorstepFields) (string, string) {
			return "New message from your professional", "Tap to read and reply."
		}},
	{pushType: service.DoorstepTypeReworkUpdated, fromEvent: doorstepEventBookingReworkRequested,
		deeplink: "momentum://doorstep/bookings/{booking_id}", entityKey: "booking_id", collapse: "doorstep_booking",
		copy: func(*doorstepFields) (string, string) {
			return "Rework request received", "We've got your rework request and will arrange a visit."
		}},

	// ---- doorstep_pro (professional) ----------------------------------
	{pushType: service.DoorstepTypeProOfferNew, fromEvent: doorstepEventProOfferCreated, audience: doorstepToPro,
		deeplink: "doorstep-pro://offers/{offer_id}", entityKey: "offer_id", collapse: "doorstep_offer",
		copy: func(*doorstepFields) (string, string) {
			return "New job offer", "Accept it in the app before the offer expires."
		}},
	{pushType: service.DoorstepTypeProOfferExpired, fromEvent: doorstepEventProOfferClosed, audience: doorstepToPro,
		deeplink: "doorstep-pro://offers", entityKey: "offer_id", collapse: "doorstep_offer",
		when: func(f *doorstepFields) bool { return f.Outcome == "expired" },
		copy: func(*doorstepFields) (string, string) {
			return "Offer expired", "The job offer has expired."
		}},
	{pushType: service.DoorstepTypeProJobCancelled, fromEvent: doorstepEventBookingCancelled, audience: doorstepToPro,
		deeplink: "doorstep-pro://jobs/{booking_id}", entityKey: "booking_id", collapse: "doorstep_job",
		when: proHas,
		copy: func(*doorstepFields) (string, string) {
			return "Job cancelled", "A job on your schedule was cancelled. That slot is free again."
		}},
	{pushType: service.DoorstepTypeProJobRescheduled, fromEvent: doorstepEventBookingRescheduled, audience: doorstepToPro,
		deeplink: "doorstep-pro://jobs/{booking_id}", entityKey: "booking_id", collapse: "doorstep_job",
		when: proHas,
		copy: func(f *doorstepFields) (string, string) {
			if at := doorstepDayClock(f.SlotStart); at != "" {
				return "Job rescheduled", "A job moved to " + at + ". Check your schedule."
			}
			return "Job rescheduled", "A job on your schedule moved. Check your schedule."
		}},
	{pushType: service.DoorstepTypeProJobReminder, fromEvent: doorstepEventBookingReminder, audience: doorstepToPro,
		deeplink: "doorstep-pro://jobs/{booking_id}", entityKey: "booking_id", collapse: "doorstep_job",
		when: proHas,
		copy: func(f *doorstepFields) (string, string) {
			if at := doorstepClock(f.SlotStart); at != "" {
				return "Job in about an hour", "Your next job starts at " + at + ". Leave in good time."
			}
			return "Job in about an hour", "Your next job starts soon. Leave in good time."
		}},
	{pushType: service.DoorstepTypeProExtrasApproved, fromEvent: doorstepEventBookingExtrasDecided, audience: doorstepToPro,
		deeplink: "doorstep-pro://jobs/{booking_id}", entityKey: "booking_id", collapse: "doorstep_job",
		when: func(f *doorstepFields) bool { return f.Decision == "approved" },
		copy: func(*doorstepFields) (string, string) {
			return "Extras approved", "The customer approved the extra work."
		}},
	{pushType: service.DoorstepTypeProExtrasDeclined, fromEvent: doorstepEventBookingExtrasDecided, audience: doorstepToPro,
		deeplink: "doorstep-pro://jobs/{booking_id}", entityKey: "booking_id", collapse: "doorstep_job",
		when: func(f *doorstepFields) bool { return f.Decision == "declined" },
		copy: func(*doorstepFields) (string, string) {
			return "Extras declined", "The customer declined the extra work. Carry on with the booked service."
		}},
	{pushType: service.DoorstepTypeProExtrasPaid, fromEvent: doorstepEventBookingExtrasPaid, audience: doorstepToPro,
		deeplink: "doorstep-pro://jobs/{booking_id}", entityKey: "booking_id", collapse: "doorstep_job",
		copy: func(*doorstepFields) (string, string) {
			return "Extras paid", "The customer paid for the extra work."
		}},
	{pushType: service.DoorstepTypeProMessageNew, fromEvent: doorstepEventBookingMessageSent, audience: doorstepToPro,
		deeplink: "doorstep-pro://jobs/{booking_id}/chat", entityKey: "booking_id", collapse: "doorstep_job_chat",
		when: func(f *doorstepFields) bool { return f.SenderKind == "customer" },
		copy: func(*doorstepFields) (string, string) {
			return "New message from the customer", "Tap to read and reply."
		}},
	{pushType: service.DoorstepTypeProApplicationApproved, fromEvent: doorstepEventProStatusChanged, audience: doorstepToPro,
		deeplink: "doorstep-pro://home", entityKey: "pro_id", collapse: "doorstep_pro_account",
		when: func(f *doorstepFields) bool {
			return f.ToStatus == "approved" && f.FromStatus == "pending_verification"
		},
		copy: func(*doorstepFields) (string, string) {
			return "You're approved", "Your Doorstep profile is approved. Go on duty to start getting jobs."
		}},
	{pushType: service.DoorstepTypeProApplicationRejected, fromEvent: doorstepEventProStatusChanged, audience: doorstepToPro,
		deeplink: "doorstep-pro://onboarding", entityKey: "pro_id", collapse: "doorstep_pro_account",
		when: func(f *doorstepFields) bool { return f.ToStatus == "rejected" },
		copy: func(*doorstepFields) (string, string) {
			return "Application not approved", "Your application wasn't approved. Open the app for details."
		}},
	{pushType: service.DoorstepTypeProAccountSuspended, fromEvent: doorstepEventProStatusChanged, audience: doorstepToPro,
		deeplink: "doorstep-pro://account", entityKey: "pro_id", collapse: "doorstep_pro_account",
		when: func(f *doorstepFields) bool { return f.ToStatus == "suspended" },
		copy: func(*doorstepFields) (string, string) {
			return "Account paused", "Your account is paused and you won't get new jobs. Open the app for details."
		}},
	{pushType: service.DoorstepTypeProAccountReinstated, fromEvent: doorstepEventProStatusChanged, audience: doorstepToPro,
		deeplink: "doorstep-pro://home", entityKey: "pro_id", collapse: "doorstep_pro_account",
		when: func(f *doorstepFields) bool { return f.ToStatus == "approved" && f.FromStatus == "suspended" },
		copy: func(*doorstepFields) (string, string) {
			return "Account active again", "You can go on duty and get jobs again."
		}},
	{pushType: service.DoorstepTypeProDocumentReviewed, fromEvent: doorstepEventProDocumentReviewed, audience: doorstepToPro,
		deeplink: "doorstep-pro://onboarding", entityKey: "document_id", collapse: "doorstep_pro_document",
		copy: func(f *doorstepFields) (string, string) {
			if f.Decision == "rejected" {
				return "Document needs attention", "A document you uploaded wasn't accepted. Upload it again in the app."
			}
			return "Document approved", "A document you uploaded was approved."
		}},
	{pushType: service.DoorstepTypeProBackgroundCheckExpiring, fromEvent: doorstepEventProBgCheckExpiring, audience: doorstepToPro,
		deeplink: "doorstep-pro://onboarding/police-certificate", entityKey: "pro_id", collapse: "doorstep_pro_bgc",
		copy: func(f *doorstepFields) (string, string) {
			if d := doorstepDate(f.ValidUntil); d != "" {
				return "Police certificate expiring", "Your police certificate expires on " + d + ". Upload a new one to keep getting jobs."
			}
			return "Police certificate expiring", "Your police certificate expires soon. Upload a new one to keep getting jobs."
		}},
	{pushType: service.DoorstepTypeProRatingReceived, fromEvent: doorstepEventBookingRated, audience: doorstepToPro,
		deeplink: "doorstep-pro://jobs/{booking_id}", entityKey: "booking_id", collapse: "doorstep_job",
		when: func(f *doorstepFields) bool { return f.RaterKind == "customer" },
		copy: func(f *doorstepFields) (string, string) {
			if f.Stars != nil && *f.Stars >= 1 && *f.Stars <= 5 {
				noun := "stars"
				if *f.Stars == 1 {
					noun = "star"
				}
				return "New rating", fmt.Sprintf("A customer rated you %d %s.", *f.Stars, noun)
			}
			return "New rating", "A customer rated your work."
		}},
	{pushType: service.DoorstepTypeProSettlementComputed, fromEvent: doorstepEventProSettlementComputed, audience: doorstepToPro,
		deeplink: "doorstep-pro://earnings", entityKey: "settlement_id", collapse: "doorstep_settlement",
		copy: func(f *doorstepFields) (string, string) {
			if f.NetPaise != nil {
				return "Earnings statement ready", "Your statement for this period is ready: " + doorstepRupees(*f.NetPaise) + "."
			}
			return "Earnings statement ready", "Your statement for this period is ready."
		}},
	{pushType: service.DoorstepTypeProPriceReviewed, fromEvent: doorstepEventProPriceReviewed, audience: doorstepToPro,
		deeplink: "doorstep-pro://prices", entityKey: "price_id", collapse: "doorstep_pro_price",
		when: func(f *doorstepFields) bool { return f.Decision == "approved" || f.Decision == "rejected" },
		copy: func(f *doorstepFields) (string, string) {
			if f.Decision == "rejected" {
				return "Price not approved", "A price you submitted wasn't approved. Open your prices to see why."
			}
			return "Price approved", "A price you submitted is approved. Customers can book you at it now."
		}},
}

// doorstepRulesByEvent indexes the rules by source event.
var doorstepRulesByEvent = func() map[string][]doorstepRule {
	out := map[string][]doorstepRule{}
	for _, r := range doorstepRules {
		out[r.fromEvent] = append(out[r.fromEvent], r)
	}
	return out
}()

// doorstepKnownEvents is every event of the contract this build handles.
var doorstepKnownEvents = func() map[string]bool {
	out := map[string]bool{
		doorstepEventIncidentRaised: true,
	}
	for e := range doorstepOpsEvents {
		out[e] = true
	}
	for e := range doorstepSilentEvents {
		out[e] = true
	}
	for _, r := range doorstepRules {
		out[r.fromEvent] = true
	}
	return out
}()

// doorstepValue returns the string form of one payload field by its contract
// name, and whether it is set.
func doorstepValue(f *doorstepFields, key string) (string, bool) {
	str := func(s string) (string, bool) {
		s = strings.TrimSpace(s)
		return s, s != ""
	}
	num := func(n *int64) (string, bool) {
		if n == nil {
			return "", false
		}
		return strconv.FormatInt(*n, 10), true
	}
	switch key {
	case "booking_id":
		return str(f.BookingID)
	case "pro_first_name":
		name := doorstepFirstName(f.ProFirstName)
		return name, name != ""
	case "eta_minutes":
		return num(f.EtaMinutes)
	case "extra_id":
		return str(f.ExtraID)
	case "total_paise":
		return num(f.TotalPaise)
	case "bill_id":
		return str(f.BillID)
	case "amount_paise":
		return num(f.AmountPaise)
	case "refund_paise":
		return num(f.RefundPaise)
	case "slot_start":
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(f.SlotStart)); err == nil {
			return t.UTC().Format(time.RFC3339), true
		}
		return "", false
	case "message_id":
		return str(f.MessageID)
	case "rework_id":
		return str(f.ReworkID)
	case "offer_id":
		return str(f.OfferID)
	case "expires_at":
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(f.ExpiresAt)); err == nil {
			return t.UTC().Format(time.RFC3339), true
		}
		return "", false
	case "pro_id":
		return str(f.ProID)
	case "document_id":
		return str(f.DocumentID)
	case "decision":
		return str(f.Decision)
	case "valid_until":
		return str(f.ValidUntil)
	case "stars":
		return num(f.Stars)
	case "settlement_id":
		return str(f.SettlementID)
	case "net_paise":
		return num(f.NetPaise)
	case "cause":
		return str(f.Cause)
	case "choice_deadline":
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(f.ChoiceDeadline)); err == nil {
			return t.UTC().Format(time.RFC3339), true
		}
		return "", false
	case "difference_paise":
		return num(f.DifferencePaise)
	case "price_id":
		return str(f.PriceID)
	case "service_id":
		return str(f.ServiceID)
	}
	return "", false
}

// doorstepValidTime reports whether s is an RFC 3339 time.
func doorstepValidTime(s string) bool {
	_, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s))
	return err == nil
}

// renderDoorstepDeepLink fills the contract template. Momentum links lose
// the scheme (the app routes paths); an unfilled placeholder is an error.
func renderDoorstepDeepLink(template string, f *doorstepFields) (string, error) {
	out := template
	for {
		open := strings.IndexByte(out, '{')
		if open < 0 {
			break
		}
		end := strings.IndexByte(out[open:], '}')
		if end < 0 {
			return "", fmt.Errorf("deep link template %q is unbalanced", template)
		}
		key := out[open+1 : open+end]
		v, ok := doorstepValue(f, key)
		if !ok {
			return "", fmt.Errorf("deep link needs %s", key)
		}
		if _, err := uuid.Parse(v); err != nil {
			return "", fmt.Errorf("deep link %s %q is not a uuid", key, v)
		}
		out = out[:open] + v + out[open+end+1:]
	}
	if strings.HasPrefix(out, doorstepMomentumScheme) {
		out = strings.TrimPrefix(out, doorstepMomentumScheme)
	}
	return out, nil
}

// doorstepPushPlan is what one event should push.
type doorstepPushPlan struct {
	outcome doorstepOutcome
	detail  string
	pushes  []service.DoorstepPush
	// missing lists payload fields that would have named a recipient.
	missing []string
	// expired lists offer push types dropped because the offer has lapsed.
	expired []string
}

func planDoorstepPushes(ev doorstepEvent, base string, now time.Time) doorstepPushPlan {
	if doorstepSilentEvents[ev.Type] {
		return doorstepPushPlan{outcome: doorstepOutcomeIgnored}
	}
	rules, known := doorstepRulesByEvent[ev.Type]
	if !known {
		if doorstepKnownEvents[ev.Type] {
			return doorstepPushPlan{outcome: doorstepOutcomeIgnored}
		}
		return doorstepPushPlan{outcome: doorstepOutcomeUnknown}
	}
	f := ev.Fields
	createdAt := now
	if !ev.OccurredAt.IsZero() {
		createdAt = ev.OccurredAt
	}
	plan := doorstepPushPlan{outcome: doorstepOutcomePlanned}
	for _, r := range rules {
		if r.when != nil && !r.when(&f) {
			continue
		}
		spec, ok := service.DoorstepPushSpecFor(r.pushType)
		if !ok {
			return doorstepPushPlan{outcome: doorstepOutcomeMalformed, detail: "rule without a registry entry: " + r.pushType}
		}
		recipientField, recipientRaw := "customer_user_id", f.CustomerUserID
		if r.audience == doorstepToPro {
			recipientField, recipientRaw = "pro_user_id", f.ProUserID
		}
		recipient, ok := parseRecipient(recipientRaw)
		if !ok {
			plan.missing = append(plan.missing, recipientField)
			continue
		}
		entityRaw, _ := doorstepValue(&f, r.entityKey)
		entity, err := uuid.Parse(entityRaw)
		if err != nil || entity == uuid.Nil {
			return doorstepPushPlan{outcome: doorstepOutcomeMalformed, detail: r.pushType + ": no valid " + r.entityKey}
		}
		link, err := renderDoorstepDeepLink(r.deeplink, &f)
		if err != nil {
			return doorstepPushPlan{outcome: doorstepOutcomeMalformed, detail: r.pushType + ": " + err.Error()}
		}
		data := map[string]string{}
		for _, k := range spec.DataKeys {
			if v, ok := doorstepValue(&f, k); ok {
				data[k] = v
			}
		}
		title, body := r.copy(&f)
		p := service.DoorstepPush{
			DedupKey:    base,
			RecipientID: recipient,
			App:         spec.App,
			Type:        r.pushType,
			Title:       title,
			Body:        body,
			DeepLink:    link,
			EntityID:    entity,
			CollapseKey: r.collapse + ":" + entity.String(),
			Data:        data,
			CreatedAt:   createdAt,
		}
		if r.pushType == service.DoorstepTypeProOfferNew {
			expires, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(f.ExpiresAt))
			if err != nil {
				return doorstepPushPlan{outcome: doorstepOutcomeMalformed, detail: "offer without a valid expires_at"}
			}
			ttl := expires.Sub(now)
			if ttl <= 0 {
				// The matcher has moved on; waking a professional for a
				// dead offer wastes a tap.
				plan.expired = append(plan.expired, r.pushType)
				continue
			}
			p.TTL = ttl
		}
		if r.pushType == service.DoorstepTypeBookingProUnavailable {
			// The choice window: after choice_deadline the booking is
			// cancelled with a full refund (its own push), so this one is
			// worthless and must not arrive late.
			deadline, _ := time.Parse(time.RFC3339Nano, strings.TrimSpace(f.ChoiceDeadline))
			ttl := deadline.Sub(now)
			if ttl <= 0 {
				plan.expired = append(plan.expired, r.pushType)
				continue
			}
			p.TTL = ttl
		}
		plan.pushes = append(plan.pushes, p)
	}
	if len(plan.pushes) == 0 && len(plan.missing) == 0 && len(plan.expired) == 0 {
		// Every rule's condition was false (e.g. a customer no-show, a
		// declined offer, a professional rating a customer).
		plan.outcome = doorstepOutcomeIgnored
	}
	return plan
}

// ---------------------------------------------------------------------------
// Copy helpers. Times are shown in India Standard Time (the pilot city is
// Hyderabad; India has no daylight saving, and a fixed zone needs no tzdata
// in the Alpine image).

var doorstepIST = time.FixedZone("IST", 5*3600+30*60)

func doorstepClock(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(rfc3339))
	if err != nil {
		return ""
	}
	return t.In(doorstepIST).Format("3:04 PM")
}

func doorstepDayClock(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(rfc3339))
	if err != nil {
		return ""
	}
	return t.In(doorstepIST).Format("Mon 2 Jan, 3:04 PM")
}

func doorstepDate(isoDate string) string {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(isoDate))
	if err != nil {
		return ""
	}
	return t.Format("2 Jan 2006")
}

// doorstepFirstName keeps one plain word: a first name, never anything that
// could carry contact details.
func doorstepFirstName(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	name := fields[0]
	if len([]rune(name)) > 40 {
		return ""
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && r != '-' && r != '\'' && r != '.' {
			return ""
		}
	}
	return name
}

// doorstepRupees renders integer paise as rupees with Indian digit grouping:
// 123456789 → ₹12,34,567.89; whole rupees drop the paise.
func doorstepRupees(paise int64) string {
	neg := paise < 0
	if neg {
		paise = -paise
	}
	rupees, rem := paise/100, paise%100
	digits := strconv.FormatInt(rupees, 10)
	if len(digits) > 3 {
		head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
		var groups []string
		for len(head) > 2 {
			groups = append([]string{head[len(head)-2:]}, groups...)
			head = head[:len(head)-2]
		}
		if head != "" {
			groups = append([]string{head}, groups...)
		}
		digits = strings.Join(groups, ",") + "," + tail
	}
	out := "₹" + digits
	if rem != 0 {
		out += fmt.Sprintf(".%02d", rem)
	}
	if neg {
		out = "-" + out
	}
	return out
}

// ---------------------------------------------------------------------------
// The last-line PII guard.

// doorstepSensitiveKey names payload keys whose values must never reach a
// push: codes, contact details, addresses, coordinates, document numbers.
func doorstepSensitiveKey(key string) (sensitive, codeLike bool) {
	k := strings.ToLower(key)
	if k == "city_code" {
		return false, false
	}
	if strings.Contains(k, "line1") || strings.Contains(k, "line2") || strings.HasSuffix(k, "_number") {
		return true, false
	}
	for _, tok := range strings.Split(k, "_") {
		switch tok {
		case "otp", "code", "pin", "passcode":
			return true, true
		case "phone", "mobile", "email", "address", "landmark", "street", "house", "flat",
			"lat", "lng", "latitude", "longitude", "pincode", "postcode", "aadhaar", "ifsc":
			return true, false
		}
	}
	return false, false
}

// doorstepSensitive is what one event must never leak.
type doorstepSensitive struct {
	tokens  map[string]bool // whole tokens of code-like values (3+ chars)
	phrases []string        // normalised address/contact values (5+ chars)
	digits  []string        // digit runs of contact values (6+ digits)
}

func doorstepNormalise(s string) string {
	return strings.Join(foodTokens(s), " ")
}

func doorstepDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// doorstepSensitiveValues collects every sensitive value anywhere in the
// Kafka value (envelope and data alike).
func doorstepSensitiveValues(raw []byte) doorstepSensitive {
	out := doorstepSensitive{tokens: map[string]bool{}}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return out
	}
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch t := v.(type) {
		case map[string]any:
			for k, child := range t {
				walk(k, child)
			}
		case []any:
			for _, child := range t {
				walk(key, child)
			}
		case string, json.Number:
			sensitive, codeLike := doorstepSensitiveKey(key)
			if !sensitive {
				return
			}
			s := fmt.Sprint(t)
			if codeLike {
				for _, tok := range foodTokens(s) {
					if len(tok) >= 3 {
						out.tokens[tok] = true
					}
				}
				return
			}
			if n := doorstepNormalise(s); len(n) >= 5 {
				out.phrases = append(out.phrases, n)
			}
			if d := doorstepDigits(s); len(d) >= 6 {
				out.digits = append(out.digits, d)
				if len(d) > 10 {
					// A phone number with its country code: the national
					// ten digits alone are just as identifying.
					out.digits = append(out.digits, d[len(d)-10:])
				}
			}
		}
	}
	walk("", v)
	return out
}

// doorstepUUIDRe matches ids. Ids are minted by our services, never secrets,
// and are blanked before the scan so a hex segment can never be mistaken for
// a code or a digit run.
var doorstepUUIDRe = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// doorstepPushLeaks reports whether p would carry a sensitive value from its
// event, or a sensitive data key at all.
func doorstepPushLeaks(p service.DoorstepPush, s doorstepSensitive) bool {
	data := service.DoorstepPushData(p)
	fields := []string{p.Title, p.Body, p.DeepLink}
	for k, v := range data {
		if sensitive, _ := doorstepSensitiveKey(k); sensitive {
			return true
		}
		fields = append(fields, v)
	}
	for _, f := range fields {
		f = doorstepUUIDRe.ReplaceAllString(f, " ")
		for _, tok := range foodTokens(f) {
			if s.tokens[tok] {
				return true
			}
		}
		norm := doorstepNormalise(f)
		for _, ph := range s.phrases {
			if strings.Contains(norm, ph) {
				return true
			}
		}
		if d := doorstepDigits(f); len(d) >= 6 {
			for _, sd := range s.digits {
				if strings.Contains(d, sd) {
					return true
				}
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Safety: incident paging and ops alerts.

// doorstepIncidentDedupNamespace scopes the event-dedup id of an incident page.
var doorstepIncidentDedupNamespace = uuid.MustParse("a2c94e17-5b3d-4f8a-8e61-7d0b9c3f2e54")

// doorstepIncidentPages reports whether an incident pages responders: every
// personal-safety kind, and anything raised as high or critical.
func doorstepIncidentPages(kind, severity string) bool {
	switch kind {
	case "sos", "safety", "harassment", "unsafe_exit":
		return true
	}
	return severity == "high" || severity == "critical"
}

func doorstepIncidentCopy(f *doorstepFields) (title, body string) {
	who := map[string]string{
		"customer": "A customer", "pro": "A professional", "system": "The system", "admin": "An admin",
	}[f.RaisedByKind]
	if who == "" {
		who = "Someone"
	}
	what := map[string]string{
		"sos": "an SOS", "safety": "a safety concern", "harassment": "a harassment report",
		"unsafe_exit": "an unsafe-situation exit", "damage": "a damage report", "theft": "a theft report",
	}[f.Kind]
	if what == "" {
		what = "an incident"
	}
	title = "Doorstep incident"
	switch f.Kind {
	case "sos", "safety", "harassment", "unsafe_exit":
		title = "Doorstep safety alert"
	}
	severity := ""
	if f.Severity != "" {
		severity = " (" + f.Severity + ")"
	}
	return title, who + " raised " + what + severity + " on a Doorstep booking. Open the incident now."
}

// processDoorstepIncident pages for one incident. It returns an error only
// for an undecodable event; delivery failures are logged and recorded as
// ops alerts so a partial failure never blocks the partition.
func processDoorstepIncident(ctx context.Context, deps doorstepSafetyDeps, ev doorstepEvent, now time.Time) error {
	f := ev.Fields
	incident, err := uuid.Parse(strings.TrimSpace(f.IncidentID))
	if err != nil || incident == uuid.Nil {
		return fmt.Errorf("doorstep.incident.raised: invalid incident_id %q", f.IncidentID)
	}
	if deps == nil {
		slog.Error("doorstep.incident.raised received but safety paging is not wired: NOBODY IS PAGED",
			"incident_id", incident, "kind", f.Kind, "severity", f.Severity)
		return nil
	}
	createdAt := ev.OccurredAt
	if createdAt.IsZero() {
		createdAt = now
	}

	first, derr := deps.ClaimEventDedup(ctx, uuid.NewSHA1(doorstepIncidentDedupNamespace, []byte(doorstepEventIncidentRaised+":"+incident.String())))
	switch {
	case derr != nil:
		// A duplicate page is better than a missed one; the per-recipient
		// identities still stop double delivery.
		slog.Error("doorstep incident dedup claim failed; paging anyway", "incident_id", incident, "error", derr)
	case !first:
		slog.Info("doorstep incident already handled; duplicate event ignored", "incident_id", incident)
		return nil
	}

	pages := doorstepIncidentPages(f.Kind, f.Severity)
	paged := 0
	if pages {
		parties := map[uuid.UUID]bool{}
		if id, ok := parseRecipient(f.CustomerUserID); ok {
			parties[id] = true
		}
		if id, ok := parseRecipient(f.ProUserID); ok {
			parties[id] = true
		}
		title, body := doorstepIncidentCopy(&f)
		for _, r := range deps.Responders(ctx) {
			if parties[r] {
				// A responder who is a party to the incident is not its responder.
				continue
			}
			err := deps.SendSafetyAlert(ctx, service.SafetyAlert{
				Recipient:  r,
				NotifType:  NotifDoorstepIncidentResponder,
				EntityType: "doorstep_incident",
				EntityID:   incident,
				DeepLink:   "/admin/doorstep/incidents/" + incident.String(),
				Title:      title,
				Body:       body,
				CreatedAt:  createdAt,
				Identity:   "doorstep_incident:" + incident.String() + ":responder:" + r.String(),
			})
			if err != nil {
				slog.Error("doorstep incident responder page failed", "incident_id", incident, "responder_id", r, "error", err)
				continue
			}
			paged++
		}
	}

	kind, severity := OpsAlertDoorstepIncidentRaised, "warning"
	switch {
	case pages && paged == 0:
		kind, severity = OpsAlertDoorstepIncidentNoResponder, "critical"
		slog.Error("DOORSTEP INCIDENT REACHED NO RESPONDER: set DOORSTEP_SAFETY_RESPONDER_USER_IDS to staff user ids",
			"incident_id", incident, "kind", f.Kind, "severity", f.Severity)
	case pages:
		kind, severity = OpsAlertDoorstepIncidentPaged, "critical"
		slog.Error("doorstep incident paged", "incident_id", incident, "kind", f.Kind,
			"severity", f.Severity, "responders_paged", paged)
	default:
		slog.Warn("doorstep incident raised (no page for this kind and severity)", "incident_id", incident,
			"kind", f.Kind, "severity", f.Severity)
	}
	if err := deps.RecordOpsAlert(ctx, postgres.OpsAlert{
		Source: "doorstep-service", Kind: kind, Severity: severity,
		SubjectID: incident, DedupeKey: "doorstep_incident:" + incident.String() + ":" + kind,
		Detail: map[string]any{
			"incident_kind":      f.Kind,
			"incident_severity":  f.Severity,
			"raised_by_kind":     f.RaisedByKind,
			"pro_auto_suspended": f.ProAutoSuspended,
			"responders_paged":   paged,
		},
	}); err != nil {
		slog.Error("ops alert not recorded", "kind", kind, "subject_id", incident, "error", err)
	}
	if pages {
		subject := "Doorstep safety alert: incident " + incident.String()
		if paged == 0 {
			subject = "UNPAGED Doorstep safety alert: incident " + incident.String()
		}
		if err := deps.EmailOps(ctx, subject, fmt.Sprintf(
			"Doorstep incident %s (%s, %s, raised by %s). Responders paged: %d. Open /admin/doorstep/incidents/%s.",
			incident, f.Kind, f.Severity, f.RaisedByKind, paged, incident)); err != nil {
			slog.Error("doorstep incident ops email failed", "incident_id", incident, "error", err)
		}
	}
	return nil
}

// recordDoorstepUnassigned turns the T-2 h "no professional yet" alert into
// an ops alert row (the admin console's live feed shows it too).
func recordDoorstepUnassigned(ctx context.Context, deps doorstepSafetyDeps, ev doorstepEvent) {
	f := ev.Fields
	booking, err := uuid.Parse(strings.TrimSpace(f.BookingID))
	if err != nil || booking == uuid.Nil {
		slog.Warn("doorstep unassigned alert without a valid booking_id")
		return
	}
	if deps == nil {
		slog.Error("doorstep booking unassigned near its slot but ops alerts are not wired", "booking_id", booking)
		return
	}
	detail := map[string]any{"city_code": f.CityCode, "category_slug": f.CategorySlug}
	if f.MinutesToSlot != nil {
		detail["minutes_to_slot"] = *f.MinutesToSlot
	}
	if err := deps.RecordOpsAlert(ctx, postgres.OpsAlert{
		Source: "doorstep-service", Kind: OpsAlertDoorstepBookingUnassigned, Severity: "warning",
		SubjectID: booking, DedupeKey: "doorstep_unassigned:" + booking.String(),
		Detail: detail,
	}); err != nil {
		slog.Error("ops alert not recorded", "kind", OpsAlertDoorstepBookingUnassigned, "subject_id", booking, "error", err)
	}
}

// recordDoorstepOpsEvent turns the ops-only events into ops alert rows:
//
//	doorstep.pro.price_submitted        info      the price review queue has a new entry
//	doorstep.booking.refund_failed      critical  a customer's refund did not go through
//	doorstep.booking.payment_attention  warning   a payment event did not match its booking
//
// The detail carries ids, amounts and enums only: never a user id, a name,
// an address or free text (refund_failed's reason and payment_attention's
// detail stay in doorstep-service, where the console reads them).
func recordDoorstepOpsEvent(ctx context.Context, deps doorstepSafetyDeps, ev doorstepEvent) {
	f := ev.Fields
	var (
		kind, severity, subjectRaw, dedupe string
		detail                             = map[string]any{}
	)
	switch ev.Type {
	case doorstepEventProPriceSubmitted:
		kind, severity, subjectRaw = OpsAlertDoorstepPriceSubmitted, "info", f.PriceID
		dedupe = "doorstep_price_submitted:" + strings.TrimSpace(f.PriceID)
		for k, v := range map[string]string{"pro_id": f.ProID, "service_id": f.ServiceID, "item_kind": f.ItemKind, "unit": f.Unit} {
			if v = strings.TrimSpace(v); v != "" {
				detail[k] = v
			}
		}
		if f.PricePaise != nil {
			detail["price_paise"] = *f.PricePaise
		}
		if f.PreviousPricePaise != nil {
			detail["previous_price_paise"] = *f.PreviousPricePaise
		}
	case doorstepEventBookingRefundFailed:
		kind, severity, subjectRaw = OpsAlertDoorstepRefundFailed, "critical", f.BookingID
		key := strings.TrimSpace(f.RefundID)
		if key == "" {
			key = strings.TrimSpace(f.BookingID)
		}
		dedupe = "doorstep_refund_failed:" + key
		detail["city_code"], detail["category_slug"] = f.CityCode, f.CategorySlug
		if id := strings.TrimSpace(f.RefundID); id != "" {
			detail["refund_id"] = id
		}
		if f.AmountPaise != nil {
			detail["amount_paise"] = *f.AmountPaise
		}
		if c := strings.TrimSpace(f.Cause); c != "" {
			detail["cause"] = c
		}
	case doorstepEventBookingPaymentAttention:
		kind, severity, subjectRaw = OpsAlertDoorstepPaymentAttention, "warning", f.BookingID
		// One row per event: one booking can mismatch more than once.
		key := ev.ID
		if key == "" {
			key = strings.TrimSpace(f.BookingID) + ":" + strings.TrimSpace(f.PaymentEventType)
		}
		dedupe = "doorstep_payment_attention:" + key
		detail["city_code"], detail["category_slug"] = f.CityCode, f.CategorySlug
		if t := strings.TrimSpace(f.PaymentEventType); t != "" {
			detail["payment_event_type"] = t
		}
	default:
		return
	}
	subject, err := uuid.Parse(strings.TrimSpace(subjectRaw))
	if err != nil || subject == uuid.Nil {
		slog.Warn("doorstep ops event without a valid subject id", "event", ev.Type)
		return
	}
	if deps == nil {
		slog.Error("doorstep ops event received but ops alerts are not wired", "event", ev.Type, "subject_id", subject)
		return
	}
	if err := deps.RecordOpsAlert(ctx, postgres.OpsAlert{
		Source: "doorstep-service", Kind: kind, Severity: severity,
		SubjectID: subject, DedupeKey: dedupe, Detail: detail,
	}); err != nil {
		slog.Error("ops alert not recorded", "kind", kind, "subject_id", subject, "error", err)
	}
}

// DoorstepSafetyAdapter is the production doorstepSafetyDeps: the dating
// panic mechanism (staff-verified ResponderDirectory, safety alerts that
// ignore preferences and quiet hours, ops alert rows, ops email).
type DoorstepSafetyAdapter struct {
	svc        *service.Service
	responders *ResponderDirectory
	opsEmail   string
}

// NewDoorstepSafetyAdapter builds the adapter. responders may be nil (nobody
// is paged; every paging incident raises a critical ops alert). opsEmail may
// be empty.
func NewDoorstepSafetyAdapter(svc *service.Service, responders *ResponderDirectory, opsEmail string) *DoorstepSafetyAdapter {
	return &DoorstepSafetyAdapter{svc: svc, responders: responders, opsEmail: strings.TrimSpace(opsEmail)}
}

func (a *DoorstepSafetyAdapter) ClaimEventDedup(ctx context.Context, id uuid.UUID) (bool, error) {
	return a.svc.ClaimEventDedup(ctx, id)
}

func (a *DoorstepSafetyAdapter) Responders(ctx context.Context) []uuid.UUID {
	if a.responders == nil {
		return nil
	}
	return a.responders.Responders(ctx)
}

func (a *DoorstepSafetyAdapter) SendSafetyAlert(ctx context.Context, alert service.SafetyAlert) error {
	return a.svc.CreateSafetyAlertNotification(ctx, alert)
}

func (a *DoorstepSafetyAdapter) RecordOpsAlert(ctx context.Context, alert postgres.OpsAlert) error {
	return a.svc.RecordOpsAlert(ctx, alert)
}

func (a *DoorstepSafetyAdapter) EmailOps(ctx context.Context, subject, body string) error {
	if a.opsEmail == "" {
		return nil
	}
	return a.svc.SendEmail(ctx, a.opsEmail, opsEmailTemplate, map[string]string{"Subject": subject, "Body": body})
}
