package events

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/segmentio/kafka-go"
)

type recordingFoodDeliverer struct {
	pushes  []service.FoodPush
	err     error
	panicky bool
}

func (r *recordingFoodDeliverer) DeliverFoodPush(_ context.Context, p service.FoodPush) (service.FoodPushOutcome, error) {
	if r.panicky {
		panic("deliverer exploded")
	}
	r.pushes = append(r.pushes, p)
	return service.FoodPushSent, r.err
}

const (
	fxCustomer   = "a1111111-1111-4111-8111-111111111111"
	fxOwner      = "b2222222-2222-4222-8222-222222222222"
	fxStaff      = "c3333333-3333-4333-8333-333333333333"
	fxRider      = "d4444444-4444-4444-8444-444444444444"
	fxOrder      = "e5555555-5555-4555-8555-555555555555"
	fxOffer      = "f6666666-6666-4666-8666-666666666666"
	fxRestaurant = "07777777-7777-4777-8777-777777777777"
	fxPartner    = "08888888-8888-4888-8888-888888888888"
	fxRefund     = "09999999-9999-4999-8999-999999999999"
)

// foodMsg builds a message exactly as shared/outbox publishes it: raw payload
// value, event type header, realtime topic as key.
func foodMsg(eventType string, payload any) kafka.Message {
	b, _ := json.Marshal(payload)
	return kafka.Message{
		Key:     []byte("food.order." + fxOrder),
		Value:   b,
		Headers: []kafka.Header{{Key: "event_type", Value: []byte(eventType)}},
	}
}

// fxOrderPayload mirrors food-service postgres.Order, plus the recipient
// fields this lane asks food-service to add.
func fxOrderPayload(status string, extra map[string]any) map[string]any {
	p := map[string]any{
		"id":              fxOrder,
		"order_number":    "FG1001",
		"user_id":         fxCustomer,
		"restaurant_id":   fxRestaurant,
		"restaurant_name": "Spice Route",
		"status":          status,
		"payment_status":  "PAID",
		"payment_method":  "online",
		"totals":          map[string]any{"final_amount": 612.5},
		"placed_at":       "2026-09-13T09:00:00Z",
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func pushesByApp(ps []service.FoodPush) map[string][]service.FoodPush {
	out := map[string][]service.FoodPush{}
	for _, p := range ps {
		out[p.App] = append(out[p.App], p)
	}
	return out
}

func TestFoodConsumer_PaidOrderRingsTheKitchenAndTellsTheCustomer(t *testing.T) {
	d := &recordingFoodDeliverer{}
	c := newFoodConsumer(d)
	c.processMessage(context.Background(), foodMsg(events.EventFoodOrderPaymentSucceeded, fxOrderPayload("CONFIRMED", map[string]any{
		"restaurant_owner_user_id":  fxOwner,
		"restaurant_staff_user_ids": []string{fxStaff, fxOwner},
	})))

	byApp := pushesByApp(d.pushes)
	kitchen := byApp[service.AppFeastKitchen]
	if len(kitchen) != 2 {
		t.Fatalf("kitchen pushes = %d, want owner + staff (owner listed twice counts once): %+v", len(kitchen), kitchen)
	}
	for _, p := range kitchen {
		if p.Type != service.FoodTypeOrderNew || p.AndroidChannel != service.FoodChannelKitchenNewOrder {
			t.Errorf("kitchen push shape: %+v", p)
		}
		if p.DeepLink != "/kitchen/orders/"+fxOrder || p.OrderID.String() != fxOrder {
			t.Errorf("kitchen push link: %+v", p)
		}
	}
	if kitchen[0].RecipientID.String() != fxOwner || kitchen[1].RecipientID.String() != fxStaff {
		t.Errorf("kitchen recipients: %s, %s", kitchen[0].RecipientID, kitchen[1].RecipientID)
	}

	customer := byApp[service.AppMomentum]
	if len(customer) != 1 {
		t.Fatalf("customer pushes = %d, want 1", len(customer))
	}
	if p := customer[0]; p.RecipientID.String() != fxCustomer || p.Type != service.FoodTypeOrderStatus ||
		p.AndroidChannel != service.FoodChannelOrders || p.DeepLink != "/feast/orders/"+fxOrder {
		t.Errorf("customer push: %+v", p)
	}
	if len(byApp[service.AppFeastRider]) != 0 {
		t.Error("rider app received an order push")
	}

	// No amounts in any push.
	for _, p := range d.pushes {
		for k, v := range service.FoodPushData(p) {
			if strings.Contains(v, "612") {
				t.Errorf("%s data %q carries the order amount: %q", p.Type, k, v)
			}
		}
	}
}

func TestFoodConsumer_PlacedIsAnnouncedOnlyOnceConfirmed(t *testing.T) {
	owner := map[string]any{"restaurant_owner_user_id": fxOwner}

	d := &recordingFoodDeliverer{}
	c := newFoodConsumer(d)
	c.processMessage(context.Background(), foodMsg(events.EventFoodOrderPlaced, fxOrderPayload("PAYMENT_PENDING", owner)))
	if len(d.pushes) != 0 || c.countOf(foodOutcomeIgnored) != 1 {
		t.Fatalf("an unpaid placed order pushed %d (ignored=%d)", len(d.pushes), c.countOf(foodOutcomeIgnored))
	}

	c.processMessage(context.Background(), foodMsg(events.EventFoodOrderPlaced, fxOrderPayload("CONFIRMED", owner)))
	if len(d.pushes) != 1 || d.pushes[0].App != service.AppFeastKitchen {
		t.Fatalf("a confirmed placed order must ring only the kitchen: %+v", d.pushes)
	}
}

// Several events can announce the same order; the dedup keys make it one push.
func TestFoodConsumer_NewOrderAndConfirmationDedupPerOrder(t *testing.T) {
	now := time.Now()
	keys := map[string]map[string]bool{}
	for _, ev := range []string{events.EventFoodOrderPlaced, events.EventFoodOrderPaymentSucceeded, events.EventFoodOrderConfirmed} {
		m := foodMsg(ev, fxOrderPayload("CONFIRMED", map[string]any{"restaurant_owner_user_id": fxOwner, "emitted_by": ev}))
		plan := planFoodPushes(ev, m.Value, foodDedupBase(ev, "", m), now)
		for _, p := range plan.pushes {
			if keys[p.Type] == nil {
				keys[p.Type] = map[string]bool{}
			}
			keys[p.Type][p.DedupKey] = true
		}
	}
	if len(keys[service.FoodTypeOrderNew]) != 1 {
		t.Fatalf("new-order dedup keys differ across announcing events: %v", keys[service.FoodTypeOrderNew])
	}
	if len(keys[service.FoodTypeOrderStatus]) != 1 {
		t.Fatalf("order-confirmed dedup keys differ across payment_succeeded/confirmed: %v", keys[service.FoodTypeOrderStatus])
	}
}

func TestFoodConsumer_DeliveryOfferGoesToTheRiderApp(t *testing.T) {
	single := map[string]any{
		"id": fxOffer, "order_id": fxOrder, "delivery_partner_id": fxPartner,
		"delivery_partner_user_id": fxRider, "status": "PENDING",
		"distance_km": 1.4, "expires_at": "2026-09-13T15:30:30+05:30", "created_at": "2026-09-13T10:00:00Z",
	}
	batch := map[string]any{
		"delivery_partner_user_id": fxRider,
		"offer": map[string]any{
			"id": fxOffer, "order_id": fxOrder, "delivery_partner_id": fxPartner,
			"status": "PENDING", "expires_at": "2026-09-13T10:00:30Z",
		},
		"batch": map[string]any{
			"id": fxRefund, "restaurant_id": fxRestaurant,
			"members": []map[string]any{{"order_id": fxOrder, "sequence": 1}, {"order_id": fxCustomer, "sequence": 2}},
		},
		"is_batch": true,
	}
	for name, payload := range map[string]any{"single": single, "batch": batch} {
		t.Run(name, func(t *testing.T) {
			d := &recordingFoodDeliverer{}
			newFoodConsumer(d).processMessage(context.Background(), foodMsg(foodEventDeliveryOffered, payload))
			if len(d.pushes) != 1 {
				t.Fatalf("pushes = %d, want 1", len(d.pushes))
			}
			p := d.pushes[0]
			if p.App != service.AppFeastRider || p.Type != service.FoodTypeDeliveryOffer ||
				p.AndroidChannel != service.FoodChannelRiderJobOffer || p.RecipientID.String() != fxRider {
				t.Fatalf("offer push: %+v", p)
			}
			if p.OfferID.String() != fxOffer || p.OrderID.String() != fxOrder || p.DeepLink != "/rider/offers/"+fxOffer {
				t.Fatalf("offer ids/link: %+v", p)
			}
			if p.OfferExpiresAt != "2026-09-13T10:00:30Z" {
				t.Fatalf("expiry not normalised to UTC RFC3339: %q", p.OfferExpiresAt)
			}
			if name == "batch" && !strings.HasPrefix(p.Body, "2 orders") {
				t.Fatalf("batch body: %q", p.Body)
			}
		})
	}
}

func TestFoodConsumer_CustomerStatusEventsGoToMomentum(t *testing.T) {
	for ev := range foodCustomerStatusCopy {
		t.Run(ev, func(t *testing.T) {
			d := &recordingFoodDeliverer{}
			newFoodConsumer(d).processMessage(context.Background(), foodMsg(ev, map[string]any{
				"order_id": fxOrder, "user_id": fxCustomer,
			}))
			var customer []service.FoodPush
			for _, p := range d.pushes {
				if p.App == service.AppMomentum {
					customer = append(customer, p)
				}
			}
			if len(customer) != 1 {
				t.Fatalf("customer pushes = %d, want 1", len(customer))
			}
			p := customer[0]
			if p.Type != service.FoodTypeOrderStatus || p.AndroidChannel != service.FoodChannelOrders ||
				p.RecipientID.String() != fxCustomer || p.Title == "" || p.Body == "" {
				t.Fatalf("customer push: %+v", p)
			}
		})
	}
}

// Every payload shape food-service emits TODAY, and the exact field it must
// add before the push can be addressed. These events push nothing now.
func TestFoodConsumer_CurrentFoodServicePayloadsNameTheMissingRecipientField(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		payload any
		field   string
	}{
		{"placed (Order, COD confirmed)", events.EventFoodOrderPlaced, fxOrderPayload("CONFIRMED", nil), "restaurant_owner_user_id"},
		{"payment_succeeded (Order)", events.EventFoodOrderPaymentSucceeded, fxOrderPayload("CONFIRMED", nil), "restaurant_owner_user_id"},
		{"offered (DeliveryOffer)", foodEventDeliveryOffered, map[string]any{
			"id": fxOffer, "order_id": fxOrder, "delivery_partner_id": fxPartner, "status": "PENDING", "expires_at": "2026-09-13T10:00:30Z",
		}, "delivery_partner_user_id"},
		{"offered (batch)", foodEventDeliveryOffered, map[string]any{
			"offer": map[string]any{"id": fxOffer, "order_id": fxOrder, "delivery_partner_id": fxPartner},
			"batch": map[string]any{"id": fxRefund}, "is_batch": true,
		}, "delivery_partner_user_id"},
		{"assigned (batch member map)", events.EventFoodDeliveryAssigned, map[string]any{
			"order_id": fxOrder, "partner_id": fxPartner, "pickup_code": "482913", "delivery_code": "730541",
			"batch_id": fxRefund, "batch_sequence": 1, "batch_size": 2,
		}, "user_id"},
		{"assigned (single offer map)", events.EventFoodDeliveryAssigned, map[string]any{
			"offer":       map[string]any{"id": fxOffer, "order_id": fxOrder, "delivery_partner_id": fxPartner},
			"pickup_code": "482913", "delivery_code": "730541",
		}, "user_id"},
		{"picked_up", events.EventFoodDeliveryPickedUp, map[string]any{"order_id": fxOrder}, "user_id"},
		{"delivered", events.EventFoodDeliveryDelivered, map[string]any{"order_id": fxOrder}, "user_id"},
		{"restaurant_rejected (SLA)", events.EventFoodOrderRestaurantRejected, map[string]any{"id": fxOrder, "reason": "sla_breach"}, "user_id"},
		{"refund_requested (RefundPlan body)", events.EventFoodOrderRefundRequested, map[string]any{
			"id": fxRefund, "order_id": fxOrder, "amount": 612.5, "amount_minor": 61250, "status": "REQUESTED",
		}, "user_id"},
		{"payment_failed (fallback map)", events.EventFoodOrderPaymentFailed, map[string]any{"id": fxOrder, "outcome": "failed"}, "user_id"},
		{"refunded (fallback map)", events.EventFoodOrderRefunded, map[string]any{"id": fxOrder, "outcome": "refunded"}, "user_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := foodMsg(tc.event, tc.payload)
			plan := planFoodPushes(tc.event, m.Value, foodDedupBase(tc.event, "", m), time.Now())
			found := false
			for _, f := range plan.missing {
				found = found || f == tc.field
			}
			if !found {
				t.Fatalf("missing = %v, want %q", plan.missing, tc.field)
			}

			d := &recordingFoodDeliverer{}
			c := newFoodConsumer(d)
			c.processMessage(context.Background(), m)
			for _, p := range d.pushes {
				if (tc.field == "user_id" && p.App == service.AppMomentum) ||
					(tc.field == "restaurant_owner_user_id" && p.App == service.AppFeastKitchen) ||
					(tc.field == "delivery_partner_user_id" && p.App == service.AppFeastRider) {
					t.Fatalf("pushed without a recipient field: %+v", p)
				}
			}
			if c.countOf(foodOutcomeMissingRecipient) == 0 {
				t.Fatal("missing recipient not counted")
			}
		})
	}
}

// Pickup and delivery codes never ride a push, whatever the event carries.
func TestFoodConsumer_NeverForwardsDeliveryCodes(t *testing.T) {
	d := &recordingFoodDeliverer{}
	c := newFoodConsumer(d)
	c.processMessage(context.Background(), foodMsg(events.EventFoodDeliveryAssigned, map[string]any{
		"order_id": fxOrder, "user_id": fxCustomer, "partner_id": fxPartner,
		"pickup_code": "482913", "delivery_code": "730541",
		"verification": map[string]any{"otp": "915377"},
	}))
	if len(d.pushes) != 1 {
		t.Fatalf("pushes = %d, want 1", len(d.pushes))
	}
	p := d.pushes[0]
	all := []string{p.Title, p.Body, p.DeepLink}
	for k, v := range service.FoodPushData(p) {
		if !service.FoodPushDataKeys[k] {
			t.Errorf("data key %q is not allowlisted", k)
		}
		all = append(all, k, v)
	}
	for _, s := range all {
		for _, code := range []string{"482913", "730541", "915377"} {
			if strings.Contains(s, code) {
				t.Fatalf("push carries one-time code %s in %q", code, s)
			}
		}
	}
	if c.countOf(foodOutcomeSecretBlocked) != 0 {
		t.Fatal("clean push was blocked by the code guard")
	}
}

// The code guard itself: a push whose copy or link contains a code the event
// carried is flagged; an order number merely containing the digits is not.
func TestFoodSecretGuard_FlagsAPushCarryingACode(t *testing.T) {
	secrets := foodSecretValues(json.RawMessage(`{"order_id":"x","pickup_code":"482913","nested":{"delivery_otp":730541}}`))
	base := service.FoodPush{
		Type: service.FoodTypeOrderStatus, App: service.AppMomentum,
		Title: "Out for delivery", Body: "On its way.", DeepLink: "/feast/orders/" + fxOrder,
	}
	if foodPushLeaksSecret(base, secrets) {
		t.Fatal("clean push flagged")
	}
	leaks := []func(p *service.FoodPush){
		func(p *service.FoodPush) { p.Body = "Share code 482913 with your rider" },
		func(p *service.FoodPush) { p.Title = "Delivery OTP: 730541" },
		func(p *service.FoodPush) { p.DeepLink = "/feast/orders/x?code=482913" },
	}
	for i, mutate := range leaks {
		p := base
		mutate(&p)
		if !foodPushLeaksSecret(p, secrets) {
			t.Fatalf("leak %d not flagged: %+v", i, p)
		}
	}
	p := base
	p.Title = "Order #FG482913X confirmed"
	if foodPushLeaksSecret(p, secrets) {
		t.Fatal("order number containing the digits was flagged")
	}
}

func TestFoodConsumer_UnknownAndBrokenEventsAreCountedNeverFatal(t *testing.T) {
	d := &recordingFoodDeliverer{}
	c := newFoodConsumer(d)
	ctx := context.Background()

	c.processMessage(ctx, foodMsg("food.order.teleported", map[string]any{"id": fxOrder}))
	c.processMessage(ctx, foodMsg("", map[string]any{"id": fxOrder}))
	c.processMessage(ctx, kafka.Message{Value: []byte(`not json`)})
	c.processMessage(ctx, kafka.Message{Value: []byte(`{}`), Headers: []kafka.Header{{Key: "event_type", Value: []byte(events.EventFoodDeliveryPickedUp)}}})
	c.processMessage(ctx, kafka.Message{Value: []byte(`[1,2`), Headers: []kafka.Header{{Key: "event_type", Value: []byte(events.EventFoodOrderCancelled)}}})
	c.processMessage(ctx, foodMsg(events.EventFoodSettlementPaid, map[string]any{"id": fxOrder}))

	if got := c.countOf(foodOutcomeUnknown); got != 1 {
		t.Errorf("unknown = %d, want 1", got)
	}
	if got := c.countOf(foodOutcomeIgnored); got != 1 {
		t.Errorf("ignored = %d, want 1", got)
	}
	if got := c.countOf(foodOutcomeMalformed); got != 4 {
		t.Errorf("malformed = %d, want 4", got)
	}
	if got := c.countOf(foodOutcomePanic); got != 0 {
		t.Errorf("panics = %d, want 0", got)
	}
	if len(d.pushes) != 0 {
		t.Errorf("pushed for unknown/broken events: %+v", d.pushes)
	}

	statusMsg := foodMsg(events.EventFoodDeliveryPickedUp, map[string]any{"order_id": fxOrder, "user_id": fxCustomer})
	d.err = errors.New("fcm down")
	c.processMessage(ctx, statusMsg)
	if c.countOf(foodOutcomeDeliveryError) != 1 {
		t.Error("delivery error not counted")
	}
	d.err, d.panicky = nil, true
	c.processMessage(ctx, statusMsg)
	if c.countOf(foodOutcomePanic) != 1 {
		t.Error("panic not recovered and counted")
	}
}

func TestFoodConsumer_DedupBaseIsStableAcrossRedelivery(t *testing.T) {
	a := foodMsg(events.EventFoodDeliveryPickedUp, map[string]any{"order_id": fxOrder, "user_id": fxCustomer})
	redelivered := a
	redelivered.Offset, redelivered.Partition = a.Offset+40, a.Partition+1
	if foodDedupBase(events.EventFoodDeliveryPickedUp, "", a) != foodDedupBase(events.EventFoodDeliveryPickedUp, "", redelivered) {
		t.Fatal("redelivered bytes produced a different dedup base")
	}
	other := foodMsg(events.EventFoodDeliveryPickedUp, map[string]any{"order_id": fxOffer, "user_id": fxCustomer})
	if foodDedupBase(events.EventFoodDeliveryPickedUp, "", a) == foodDedupBase(events.EventFoodDeliveryPickedUp, "", other) {
		t.Fatal("different events share a dedup base")
	}
}

// A producer that wraps the shared envelope keys idempotency on its event id.
func TestFoodConsumer_EnvelopeValueUsesEventID(t *testing.T) {
	inner, _ := json.Marshal(map[string]any{"order_id": fxOrder, "user_id": fxCustomer})
	value, _ := json.Marshal(events.EventEnvelope{EventID: "evt-42", EventType: events.EventFoodDeliveryPickedUp, Payload: inner})
	d := &recordingFoodDeliverer{}
	newFoodConsumer(d).processMessage(context.Background(), kafka.Message{Value: value})
	if len(d.pushes) != 1 || !strings.HasPrefix(d.pushes[0].DedupKey, "event:evt-42") {
		t.Fatalf("pushes: %+v", d.pushes)
	}
}

// The channel travels as a transport key that the FCM builder lifts out.
func TestFoodConsumer_PushesNameAnAndroidChannel(t *testing.T) {
	d := &recordingFoodDeliverer{}
	newFoodConsumer(d).processMessage(context.Background(), foodMsg(events.EventFoodDeliveryPickedUp, map[string]any{"order_id": fxOrder, "user_id": fxCustomer}))
	msg := push.BuildFCMMessage("tok", d.pushes[0].Title, d.pushes[0].Body, service.FoodPushData(d.pushes[0]))
	android, _ := msg["android"].(map[string]interface{})
	notification, _ := android["notification"].(map[string]string)
	if notification["channel_id"] != service.FoodChannelOrders {
		t.Fatalf("android config = %v", android)
	}
}
