// Feast (food) push consumer — lane B5b, 2026-09-13.
//
// WIRE FORMAT. food-service publishes through shared/outbox, which writes the
// RAW event payload as the Kafka value, the event type in the `event_type`
// header, and the partition key (a realtime topic name) as the Kafka key.
// There is no EventEnvelope and no event id. The previous handler ran inside
// the generic Consumer, decoded an EventEnvelope, found an empty event type
// and dropped every food event — no Feast push was ever sent. A value that
// IS a shared EventEnvelope is still accepted, and its event_id then keys
// idempotency.
//
// IDEMPOTENCY. Keyed on the envelope event_id when there is one, otherwise on
// a SHA-256 of (event type, key, value): a Kafka redelivery or an outbox
// re-publish of the same row carries identical bytes. "New order" is keyed per
// order instead, because several events can announce the same order.
//
// RECIPIENTS come from payload fields only; this service never reads
// food-service's database. An event without the field is counted
// (missing_recipient) and logged with the exact field food-service must add.
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
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/segmentio/kafka-go"
)

// Food event types not declared in shared/events.
const (
	foodEventDeliveryOffered = "food.delivery.offered"
)

// foodOrderStatusConfirmed is food-service orderstate.Confirmed.
const foodOrderStatusConfirmed = "CONFIRMED"

type foodOutcome string

const (
	foodOutcomePlanned          foodOutcome = "planned"
	foodOutcomeIgnored          foodOutcome = "ignored"   // known, deliberately not pushed
	foodOutcomeUnknown          foodOutcome = "unknown"   // an event type this build does not know
	foodOutcomeMalformed        foodOutcome = "malformed" // no event type, bad JSON, or no order id
	foodOutcomeMissingRecipient foodOutcome = "missing_recipient"
	foodOutcomeSecretBlocked    foodOutcome = "secret_blocked"
	foodOutcomeDeliveryError    foodOutcome = "delivery_error"
	foodOutcomePanic            foodOutcome = "panic"
)

var (
	foodEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost",
		Subsystem: "notification_service",
		Name:      "food_events_total",
		Help:      "Feast food events by handling outcome. Unknown event types are counted here and ignored.",
	}, []string{"outcome"})
	foodPushesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "atpost",
		Subsystem: "notification_service",
		Name:      "food_pushes_total",
		Help:      "Feast pushes by type, app and delivery outcome.",
	}, []string{"type", "app", "outcome"})
)

// foodPushDeliverer is the service seam; *service.Service satisfies it.
type foodPushDeliverer interface {
	DeliverFoodPush(ctx context.Context, p service.FoodPush) (service.FoodPushOutcome, error)
}

// FoodConsumer turns food-events into Feast pushes.
type FoodConsumer struct {
	reader    *kafka.Reader
	deliverer foodPushDeliverer
	now       func() time.Time

	mu     sync.Mutex
	counts map[foodOutcome]int64
}

func NewFoodConsumerWithDialer(brokers []string, groupID, topic string, svc foodPushDeliverer, dialer *kafka.Dialer) *FoodConsumer {
	c := newFoodConsumer(svc)
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

func newFoodConsumer(d foodPushDeliverer) *FoodConsumer {
	return &FoodConsumer{deliverer: d, now: time.Now, counts: map[foodOutcome]int64{}}
}

// Start consumes sequentially so one order's status pushes keep their order.
func (c *FoodConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("food consumer shutting down\n")
			} else {
				log.Printf("Food consumer error: %v\n", err)
			}
			break
		}
		c.processMessage(ctx, m)
	}
}

func (c *FoodConsumer) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}

func (c *FoodConsumer) count(o foodOutcome) {
	foodEventsTotal.WithLabelValues(string(o)).Inc()
	c.mu.Lock()
	c.counts[o]++
	c.mu.Unlock()
}

func (c *FoodConsumer) countOf(o foodOutcome) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[o]
}

// processMessage never returns an error and never panics: every outcome is
// counted and logged, and the offset moves on.
func (c *FoodConsumer) processMessage(ctx context.Context, m kafka.Message) {
	defer func() {
		if r := recover(); r != nil {
			c.count(foodOutcomePanic)
			slog.Error("food consumer: recovered panic", "panic", fmt.Sprint(r))
		}
	}()

	eventType, eventID, payload := decodeFoodMessage(m)
	if eventType == "" {
		c.count(foodOutcomeMalformed)
		slog.Warn("food consumer: message without an event type", "partition", m.Partition, "offset", m.Offset)
		return
	}

	plan := planFoodPushes(eventType, payload, foodDedupBase(eventType, eventID, m), c.now().UTC())
	switch plan.outcome {
	case foodOutcomeUnknown, foodOutcomeIgnored:
		c.count(plan.outcome)
		return
	case foodOutcomeMalformed:
		c.count(plan.outcome)
		slog.Warn("food consumer: malformed event", "event", eventType, "detail", plan.detail)
		return
	}
	for _, field := range plan.missing {
		c.count(foodOutcomeMissingRecipient)
		slog.Warn("food consumer: event lacks its push recipient; food-service must add the field",
			"event", eventType, "field", field)
	}
	if len(plan.pushes) == 0 {
		return
	}
	c.count(foodOutcomePlanned)

	secrets := foodSecretValues(payload)
	for _, p := range plan.pushes {
		if foodPushLeaksSecret(p, secrets) {
			c.count(foodOutcomeSecretBlocked)
			slog.Error("food consumer: push dropped — it would have carried a one-time code",
				"event", eventType, "type", p.Type)
			continue
		}
		outcome, err := c.deliverer.DeliverFoodPush(ctx, p)
		if err != nil {
			c.count(foodOutcomeDeliveryError)
			slog.Warn("food consumer: delivery failed", "event", eventType, "type", p.Type, "app", p.App, "error", err)
			continue
		}
		foodPushesTotal.WithLabelValues(p.Type, p.App, string(outcome)).Inc()
	}
}

// decodeFoodMessage extracts the event type, optional event id and payload.
func decodeFoodMessage(m kafka.Message) (eventType, eventID string, payload json.RawMessage) {
	for _, h := range m.Headers {
		if h.Key == "event_type" {
			eventType = string(h.Value)
		}
	}
	payload = m.Value
	var env events.EventEnvelope
	if err := json.Unmarshal(m.Value, &env); err == nil && env.EventType != "" && len(env.Payload) > 0 {
		if eventType == "" || eventType == env.EventType {
			return env.EventType, env.EventID, env.Payload
		}
	}
	return eventType, "", payload
}

// foodDedupBase is the per-event idempotency key.
func foodDedupBase(eventType, eventID string, m kafka.Message) string {
	if eventID != "" {
		return "event:" + eventID
	}
	h := sha256.New()
	h.Write([]byte(eventType))
	h.Write([]byte{0})
	h.Write(m.Key)
	h.Write([]byte{0})
	h.Write(m.Value)
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// foodPushPlan is what one event should push.
type foodPushPlan struct {
	outcome foodOutcome
	detail  string
	pushes  []service.FoodPush
	// missing lists payload fields that would have named a recipient.
	missing []string
}

type foodCopy struct{ title, body string }

// foodCustomerStatusCopy is every customer-visible order change and its copy.
// No amounts, no codes, nothing about the rider or the kitchen staff.
var foodCustomerStatusCopy = map[string]foodCopy{
	events.EventFoodOrderPaymentSucceeded:   {"Order confirmed", "Payment received. Your order is with the restaurant."},
	events.EventFoodOrderConfirmed:          {"Order confirmed", "Your order is with the restaurant."},
	events.EventFoodOrderPaymentFailed:      {"Payment failed", "Your payment didn't go through. Tap to try again."},
	events.EventFoodOrderRestaurantAccepted: {"Order accepted", "The restaurant accepted your order."},
	events.EventFoodOrderRestaurantRejected: {"Order not accepted", "The restaurant couldn't take your order. Any payment will be refunded."},
	events.EventFoodOrderPreparing:          {"Being prepared", "The restaurant is preparing your order."},
	events.EventFoodOrderReadyForPickup:     {"Order ready", "Your order is ready and waiting for a delivery partner."},
	events.EventFoodDeliveryAssigned:        {"Delivery partner assigned", "A delivery partner is heading to the restaurant."},
	events.EventFoodDeliveryPickedUp:        {"Out for delivery", "Your order has been picked up and is on its way."},
	events.EventFoodDeliveryDelivered:       {"Delivered", "Your order has been delivered. Enjoy your meal!"},
	events.EventFoodOrderCancelled:          {"Order cancelled", "Your order was cancelled."},
	events.EventFoodOrderRefundRequested:    {"Refund started", "We've started your refund."},
	events.EventFoodOrderRefunded:           {"Refund processed", "Your refund has been processed."},
}

// foodKitchenNewOrderEvents announce a new order to the kitchen. placed counts
// only when the order is already CONFIRMED (no online payment step);
// otherwise payment_succeeded announces it.
var foodKitchenNewOrderEvents = map[string]bool{
	events.EventFoodOrderPlaced:           true,
	events.EventFoodOrderPaymentSucceeded: true,
	events.EventFoodOrderConfirmed:        true,
}

// foodIgnoredEvents are known food events with no Feast push.
var foodIgnoredEvents = map[string]bool{
	events.EventFoodRestaurantCreated:       true,
	events.EventFoodRestaurantApproved:      true,
	events.EventFoodRestaurantRejected:      true,
	events.EventFoodDeliveryPartnerCreated:  true,
	events.EventFoodDeliveryPartnerApproved: true,
	events.EventFoodRatingCreated:           true,
	events.EventFoodSettlementGenerated:     true,
	events.EventFoodSettlementPaid:          true,
	"food.restaurant.fssai_expired":         true,
	"food.order.substitution_proposed":      true,
	"food.order.substitution_approved":      true,
	"food.order.substitution_declined":      true,
	"food.order.substitution_cancelled":     true,
	"food.order.message":                    true,
	"food.refund.approved":                  true,
	"food.refund.rejected":                  true,
}

func planFoodPushes(eventType string, payload json.RawMessage, base string, now time.Time) foodPushPlan {
	switch {
	case eventType == foodEventDeliveryOffered:
		return planRiderOffer(payload, now)
	case foodKitchenNewOrderEvents[eventType] || foodCustomerStatusCopy[eventType] != (foodCopy{}):
		return planOrderEvent(eventType, payload, base, now)
	case foodIgnoredEvents[eventType]:
		return foodPushPlan{outcome: foodOutcomeIgnored}
	default:
		return foodPushPlan{outcome: foodOutcomeUnknown}
	}
}

// foodOrderEventPayload covers every order-shaped food event: the full Order
// (placed, payment_*, cancelled, refunded), the small maps (picked_up,
// delivered, SLA restaurant_rejected), delivery.assigned in both shapes, and
// refund requests. Only fields a push may use are decoded.
type foodOrderEventPayload struct {
	ID          string `json:"id"`
	OrderID     string `json:"order_id"`
	OrderNumber string `json:"order_number"`
	UserID      string `json:"user_id"`
	CustomerID  string `json:"customer_id"`
	Status      string `json:"status"`

	RestaurantOwnerUserID  string   `json:"restaurant_owner_user_id"`
	RestaurantStaffUserIDs []string `json:"restaurant_staff_user_ids"`

	Offer *struct {
		OrderID string `json:"order_id"`
	} `json:"offer"`
}

// orderID prefers order_id: on refund payloads `id` is the refund's id.
func (p foodOrderEventPayload) orderID() string {
	switch {
	case p.OrderID != "":
		return p.OrderID
	case p.Offer != nil && p.Offer.OrderID != "":
		return p.Offer.OrderID
	default:
		return p.ID
	}
}

func parseRecipient(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

func orderRef(orderNumber string) string {
	if orderNumber == "" {
		return ""
	}
	return " #" + orderNumber
}

func planOrderEvent(eventType string, raw json.RawMessage, base string, now time.Time) foodPushPlan {
	var p foodOrderEventPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return foodPushPlan{outcome: foodOutcomeMalformed, detail: "payload is not a JSON object"}
	}
	orderID, ok := parseRecipient(p.orderID())
	if !ok {
		return foodPushPlan{outcome: foodOutcomeMalformed, detail: "no order id"}
	}
	plan := foodPushPlan{outcome: foodOutcomePlanned}

	if foodKitchenNewOrderEvents[eventType] &&
		(eventType != events.EventFoodOrderPlaced || p.Status == foodOrderStatusConfirmed) {
		kitchen := map[uuid.UUID]bool{}
		var recipients []uuid.UUID
		for _, raw := range append([]string{p.RestaurantOwnerUserID}, p.RestaurantStaffUserIDs...) {
			if id, ok := parseRecipient(raw); ok && !kitchen[id] {
				kitchen[id] = true
				recipients = append(recipients, id)
			}
		}
		if len(recipients) == 0 {
			plan.missing = append(plan.missing, "restaurant_owner_user_id")
		}
		for _, uid := range recipients {
			plan.pushes = append(plan.pushes, service.FoodPush{
				// Per order, not per event: payment_succeeded and confirmed
				// can both announce the same order.
				DedupKey:       "food_order_new:" + orderID.String(),
				RecipientID:    uid,
				App:            service.AppFeastKitchen,
				Type:           service.FoodTypeOrderNew,
				AndroidChannel: service.FoodChannelKitchenNewOrder,
				Title:          "New order" + orderRef(p.OrderNumber),
				Body:           "Tap to review and accept it before the timer runs out.",
				DeepLink:       "/kitchen/orders/" + orderID.String(),
				OrderID:        orderID,
				CreatedAt:      now,
			})
		}
	}

	if copy, ok := foodCustomerStatusCopy[eventType]; ok {
		customer, ok := parseRecipient(p.UserID)
		if !ok {
			customer, ok = parseRecipient(p.CustomerID)
		}
		if !ok {
			plan.missing = append(plan.missing, "user_id")
		} else {
			dedup := base + ":" + eventType
			if eventType == events.EventFoodOrderPaymentSucceeded || eventType == events.EventFoodOrderConfirmed {
				// One "Order confirmed" per order, whichever event says it.
				dedup = "food_order_confirmed:" + orderID.String()
			}
			plan.pushes = append(plan.pushes, service.FoodPush{
				DedupKey:       dedup,
				RecipientID:    customer,
				App:            service.AppMomentum,
				Type:           service.FoodTypeOrderStatus,
				AndroidChannel: service.FoodChannelOrders,
				Title:          copy.title + orderRef(p.OrderNumber),
				Body:           copy.body,
				DeepLink:       "/feast/orders/" + orderID.String(),
				OrderID:        orderID,
				CreatedAt:      now,
			})
		}
	}

	if len(plan.pushes) == 0 && len(plan.missing) == 0 {
		// e.g. placed while payment is still pending.
		plan.outcome = foodOutcomeIgnored
	}
	return plan
}

type foodOfferFields struct {
	ID                    string `json:"id"`
	OrderID               string `json:"order_id"`
	ExpiresAt             string `json:"expires_at"`
	DeliveryPartnerUserID string `json:"delivery_partner_user_id"`
}

// foodOfferPayload covers both food.delivery.offered shapes: a bare
// DeliveryOffer, and {offer, batch, is_batch} for batched orders.
type foodOfferPayload struct {
	foodOfferFields
	Offer *foodOfferFields `json:"offer"`
	Batch *struct {
		Members []json.RawMessage `json:"members"`
	} `json:"batch"`
}

func planRiderOffer(raw json.RawMessage, now time.Time) foodPushPlan {
	var p foodOfferPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return foodPushPlan{outcome: foodOutcomeMalformed, detail: "payload is not a JSON object"}
	}
	f := p.foodOfferFields
	if p.Offer != nil {
		if f.ID == "" {
			f.ID = p.Offer.ID
		}
		if f.OrderID == "" {
			f.OrderID = p.Offer.OrderID
		}
		if f.ExpiresAt == "" {
			f.ExpiresAt = p.Offer.ExpiresAt
		}
		if f.DeliveryPartnerUserID == "" {
			f.DeliveryPartnerUserID = p.Offer.DeliveryPartnerUserID
		}
	}
	offerID, ok := parseRecipient(f.ID)
	if !ok {
		return foodPushPlan{outcome: foodOutcomeMalformed, detail: "no offer id"}
	}
	orderID, ok := parseRecipient(f.OrderID)
	if !ok {
		return foodPushPlan{outcome: foodOutcomeMalformed, detail: "no order id"}
	}
	partner, ok := parseRecipient(f.DeliveryPartnerUserID)
	if !ok {
		return foodPushPlan{outcome: foodOutcomePlanned, missing: []string{"delivery_partner_user_id"}}
	}
	expires := ""
	if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(f.ExpiresAt)); err == nil {
		expires = t.UTC().Format(time.RFC3339)
	}
	body := "Accept before the offer expires."
	if p.Batch != nil && len(p.Batch.Members) > 1 {
		body = fmt.Sprintf("%d orders from one restaurant. Accept before the offer expires.", len(p.Batch.Members))
	}
	return foodPushPlan{outcome: foodOutcomePlanned, pushes: []service.FoodPush{{
		DedupKey:       "food_delivery_offer:" + offerID.String(),
		RecipientID:    partner,
		App:            service.AppFeastRider,
		Type:           service.FoodTypeDeliveryOffer,
		AndroidChannel: service.FoodChannelRiderJobOffer,
		Title:          "New delivery job",
		Body:           body,
		DeepLink:       "/rider/offers/" + offerID.String(),
		OrderID:        orderID,
		OfferID:        offerID,
		OfferExpiresAt: expires,
		CreatedAt:      now,
	}}}
}

// foodSecretKeyRe names payload keys that hold one-time codes (pickup_code,
// delivery_code, otp, pin, …). Also applied to push data keys.
var foodSecretKeyRe = regexp.MustCompile(`(?i)(^|_)(otp|code|pin|passcode)($|_)`)

func foodTokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// foodSecretValues collects every token (3+ characters) held under a
// code-like key anywhere in the payload.
func foodSecretValues(raw json.RawMessage) map[string]bool {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	out := map[string]bool{}
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
			if !foodSecretKeyRe.MatchString(key) {
				return
			}
			for _, tok := range foodTokens(fmt.Sprint(t)) {
				if len(tok) >= 3 {
					out[tok] = true
				}
			}
		}
	}
	walk("", v)
	return out
}

// foodPushLeaksSecret is the last line of defence against a one-time code in
// a push: a code-like data key, or any whole token of the title, body, deep
// link or data equal to a code carried by the event.
func foodPushLeaksSecret(p service.FoodPush, secrets map[string]bool) bool {
	data := service.FoodPushData(p)
	fields := []string{p.Title, p.Body, p.DeepLink}
	for k, v := range data {
		if foodSecretKeyRe.MatchString(k) {
			return true
		}
		fields = append(fields, v)
	}
	for _, f := range fields {
		for _, tok := range foodTokens(f) {
			if secrets[tok] {
				return true
			}
		}
	}
	return false
}
