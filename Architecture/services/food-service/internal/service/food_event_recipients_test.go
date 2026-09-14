package service

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/foodevents"
	"github.com/atpost/food-service/internal/orderstate"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Mirrors of what notification-service decodes from food-events. Field names
// and JSON tags are copied from
// services/notification-service/internal/events/food_consumer.go (1718ffe8):
//
//	foodOrderEventPayload  food_consumer.go:312
//	foodOfferFields        food_consumer.go:432
//	foodOfferPayload       food_consumer.go:441
//
// Recipient rules: planOrderEvent (food_consumer.go:355) pushes the kitchen on
// placed-while-CONFIRMED, payment_succeeded and confirmed from
// restaurant_owner_user_id (+ restaurant_staff_user_ids), and the customer on
// every status event from user_id (customer_id fallback). planRiderOffer
// (food_consumer.go:449) pushes the rider from delivery_partner_user_id, top
// level first, then offer.delivery_partner_user_id.
type consumerOrderPayload struct {
	ID                     string   `json:"id"`
	OrderID                string   `json:"order_id"`
	OrderNumber            string   `json:"order_number"`
	UserID                 string   `json:"user_id"`
	CustomerID             string   `json:"customer_id"`
	Status                 string   `json:"status"`
	RestaurantOwnerUserID  string   `json:"restaurant_owner_user_id"`
	RestaurantStaffUserIDs []string `json:"restaurant_staff_user_ids"`
}

type consumerOfferFields struct {
	ID                    string `json:"id"`
	OrderID               string `json:"order_id"`
	ExpiresAt             string `json:"expires_at"`
	DeliveryPartnerUserID string `json:"delivery_partner_user_id"`
}

type consumerOfferPayload struct {
	consumerOfferFields
	Offer *consumerOfferFields `json:"offer"`
	Batch *struct {
		Members []json.RawMessage `json:"members"`
	} `json:"batch"`
}

// Every event notification-service pushes on, built exactly as food-service
// builds it (foodevents.NewOrderEvent is what enqueueOrderEventTx writes; the
// offer builders are what the dispatch worker emits), decoded with the
// consumer's own field names.
func TestFoodEventsCarryTheRecipientNotificationServiceReads(t *testing.T) {
	header := foodevents.OrderHeader{
		OrderID: uuid.New(), OrderNumber: "FG1000000000003", UserID: uuid.New(),
		RestaurantID: uuid.New(), RestaurantOwnerUserID: uuid.New(),
	}
	orderCases := []struct {
		event, status, previous string
		kitchen, customer       bool
	}{
		{foodevents.OrderPlaced, orderstate.Confirmed, "", true, false},
		{foodevents.OrderPaymentSucceeded, orderstate.Confirmed, orderstate.PaymentPending, true, true},
		{foodevents.OrderConfirmed, orderstate.Confirmed, orderstate.Placed, true, true},
		{foodevents.OrderPaymentFailed, orderstate.PaymentFailed, orderstate.PaymentPending, false, true},
		{foodevents.OrderRestaurantAccepted, orderstate.Preparing, orderstate.Confirmed, false, true},
		{foodevents.OrderRestaurantRejected, orderstate.RestaurantRejected, orderstate.Confirmed, false, true},
		{foodevents.OrderReadyForPickup, orderstate.ReadyForPickup, orderstate.Preparing, false, true},
		{foodevents.DeliveryAssigned, orderstate.DeliveryAssigned, orderstate.DeliveryAssigning, false, true},
		{foodevents.DeliveryPickedUp, orderstate.PickedUp, orderstate.DeliveryAssigned, false, true},
		{foodevents.DeliveryDelivered, orderstate.Delivered, orderstate.OutForDelivery, false, true},
		{foodevents.OrderCancelled, orderstate.CancelledByAdmin, orderstate.DeliveryAssigned, false, true},
		{foodevents.OrderRefundRequested, orderstate.RefundPending, orderstate.RestaurantRejected, false, true},
		{foodevents.OrderRefunded, orderstate.Refunded, orderstate.RefundPending, false, true},
	}
	for _, tc := range orderCases {
		t.Run(tc.event, func(t *testing.T) {
			h := header
			h.Status = tc.status
			raw, err := foodevents.Marshal(tc.event, foodevents.NewOrderEvent(h, tc.previous))
			if err != nil {
				t.Fatal(err)
			}
			var p consumerOrderPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if p.OrderID != header.OrderID.String() || p.ID != header.OrderID.String() {
				t.Fatalf("order id: id=%q order_id=%q in %s", p.ID, p.OrderID, raw)
			}
			if tc.kitchen && p.RestaurantOwnerUserID != header.RestaurantOwnerUserID.String() {
				t.Fatalf("kitchen recipient restaurant_owner_user_id = %q in %s", p.RestaurantOwnerUserID, raw)
			}
			if tc.customer && p.UserID != header.UserID.String() {
				t.Fatalf("customer recipient user_id = %q in %s", p.UserID, raw)
			}
			if tc.event == foodevents.OrderPlaced && p.Status != orderstate.Confirmed {
				t.Fatalf("placed status %q: the consumer pushes the kitchen only on CONFIRMED", p.Status)
			}
			if strings.Contains(string(raw), "code") || strings.Contains(string(raw), "amount") {
				t.Fatalf("payload carries a code or an amount: %s", raw)
			}
		})
	}

	rider, partnerRow := uuid.New(), uuid.New()
	offer := postgres.DeliveryOffer{ID: uuid.New(), OrderID: header.OrderID, DeliveryPartnerID: partnerRow,
		Status: "pending", ExpiresAt: "2026-09-13T06:30:25Z", CreatedAt: "2026-09-13T06:30:00Z"}
	offerCtx := rnOfferContext()
	view := postgres.BuildDeliveryOfferView(offer, &offerCtx, time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC), 2*time.Minute)
	batch := &postgres.DeliveryBatch{ID: uuid.New(), RestaurantID: header.RestaurantID, Status: "pending",
		Members: []postgres.BatchMember{{OrderID: header.OrderID, Sequence: 1}, {OrderID: uuid.New(), Sequence: 2}}}
	offerCases := []struct {
		name string
		data any
	}{
		{"food.delivery.offered single", newDeliveryOfferedEvent(view, rider)},
		{"food.delivery.offered batch", newBatchDeliveryOfferedEvent(view, batch, rider)},
	}
	for _, tc := range offerCases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := foodevents.Marshal(foodevents.DeliveryOffered, tc.data)
			if err != nil {
				t.Fatal(err)
			}
			var p consumerOfferPayload
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			if p.DeliveryPartnerUserID != rider.String() {
				t.Fatalf("top-level delivery_partner_user_id = %q (partner row %s) in %s", p.DeliveryPartnerUserID, partnerRow, raw)
			}
			f := p.consumerOfferFields
			if p.Offer != nil {
				f = *p.Offer
			}
			if f.ID != offer.ID.String() || f.OrderID != offer.OrderID.String() || f.ExpiresAt == "" {
				t.Fatalf("offer fields = %+v in %s", f, raw)
			}
			if strings.Contains(string(raw), "code") {
				t.Fatalf("payload carries a code: %s", raw)
			}
			// Additive offer detail; never the exact drop-off (rider-nav lane).
			rnAssertOfferPrivate(t, raw)
		})
	}
}

// Every order event notification-service pushes on has a writer: an edge the
// state machine allows (transitionOrderTx writes foodevents.ForTransition) or
// one of the non-transition writers named below, all inside their
// transaction. food.order.preparing is deliberately unannounced: the only edge
// into PREPARING is the restaurant's accept, announced as
// restaurant_accepted.
func TestEveryPushedOrderEventHasAWriter(t *testing.T) {
	statuses := []string{
		orderstate.Draft, orderstate.Placed, orderstate.PaymentPending, orderstate.PaymentFailed,
		orderstate.Confirmed, orderstate.RestaurantRejected, orderstate.Preparing, orderstate.ReadyForPickup,
		orderstate.DeliveryAssigning, orderstate.DeliveryAssigned, orderstate.PickedUp, orderstate.OutForDelivery,
		orderstate.Delivered, orderstate.CancelledByCustomer, orderstate.CancelledByRestaurant,
		orderstate.CancelledByAdmin, orderstate.RefundPending, orderstate.Refunded, orderstate.Failed,
	}
	written := map[string]bool{
		foodevents.OrderPlaced:           true, // store.PlaceOrder
		foodevents.OrderPaymentSucceeded: true, // store.confirmOrderPaidTx (captured)
		foodevents.OrderRefunded:         true, // store.settleRefundTx (partial)
		foodevents.OrderRefundRequested:  true, // store.AdminRequestRefund (partial)
	}
	for _, from := range statuses {
		for _, to := range statuses {
			if orderstate.EdgeExists(from, to) {
				if ev := foodevents.ForTransition(from, to); ev != "" {
					written[ev] = true
				}
			}
		}
	}
	got := make([]string, 0, len(written))
	for ev := range written {
		got = append(got, ev)
	}
	sort.Strings(got)
	want := []string{
		foodevents.DeliveryAssigned, foodevents.DeliveryDelivered, foodevents.DeliveryPickedUp,
		foodevents.OrderCancelled, foodevents.OrderConfirmed, foodevents.OrderPaymentFailed,
		foodevents.OrderPaymentSucceeded, foodevents.OrderPlaced, foodevents.OrderReadyForPickup,
		foodevents.OrderRefundRequested, foodevents.OrderRefunded, foodevents.OrderRestaurantAccepted,
		foodevents.OrderRestaurantRejected,
	}
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("written order events = %v\nwant %v", got, want)
	}
}
