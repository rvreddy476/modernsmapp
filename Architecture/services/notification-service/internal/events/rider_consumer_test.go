package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
)

// Mopedu pushes (2026-09-18): the customer's ride lifecycle and payment reach
// the Momentum app as its exact ride.* types; the captain's offer and payment
// notice reach the Mopedu Captain app.

type recordingRideDeliverer struct {
	pushes []service.RidePush
}

func (r *recordingRideDeliverer) DeliverRidePush(_ context.Context, p service.RidePush) (service.RidePushOutcome, error) {
	r.pushes = append(r.pushes, p)
	return service.RidePushSent, nil
}

const (
	rxRide        = "a1a1a1a1-1111-4111-8111-a1a1a1a1a1a1"
	rxCustomer    = "b2b2b2b2-2222-4222-8222-b2b2b2b2b2b2"
	rxPartner     = "c3c3c3c3-3333-4333-8333-c3c3c3c3c3c3"
	rxPartnerUser = "d4d4d4d4-4444-4444-8444-d4d4d4d4d4d4"
	rxOffer       = "e5e5e5e5-5555-4555-8555-e5e5e5e5e5e5"
)

func rideEnvelope(eventType string, payload any) events.EventEnvelope {
	b, _ := json.Marshal(payload)
	return events.EventEnvelope{EventID: "evt-" + eventType, EventType: eventType, OccurredAt: time.Now(), Payload: b}
}

func rideConsumer() (*Consumer, *recordingRideDeliverer) {
	d := &recordingRideDeliverer{}
	return &Consumer{ridePush: d}, d
}

// Every lifecycle event becomes exactly one customer push with the app's
// type string, the ride as entity_id, the ride_updates channel and no OTP.
func TestRiderConsumer_LifecycleEventsBecomeTheAppsRideTypes(t *testing.T) {
	cases := []struct {
		event, pushType string
		payload         map[string]any
	}{
		{events.EventRiderRideAssigned, "ride.assigned", map[string]any{"ride_id": rxRide, "customer_user_id": rxCustomer, "partner_id": rxPartner, "offer_id": rxOffer, "assigned_at": "2026-09-18T10:00:00Z"}},
		{events.EventRiderRideArriving, "ride.arriving", map[string]any{"ride_id": rxRide, "customer_user_id": rxCustomer, "partner_id": rxPartner, "occurred_at": "2026-09-18T10:01:00Z"}},
		{events.EventRiderRideArrived, "ride.arrived", map[string]any{"ride_id": rxRide, "customer_user_id": rxCustomer, "partner_id": rxPartner, "occurred_at": "2026-09-18T10:02:00Z", "otp": "4821"}},
		{events.EventRiderRideStarted, "ride.started", map[string]any{"ride_id": rxRide, "customer_user_id": rxCustomer, "partner_id": rxPartner, "occurred_at": "2026-09-18T10:03:00Z"}},
		{events.EventRiderRideCompleted, "ride.completed", map[string]any{"ride_id": rxRide, "customer_user_id": rxCustomer, "partner_id": rxPartner, "final_fare_paise": 12350, "completed_at": "2026-09-18T10:20:00Z"}},
		{events.EventRiderRideCancelled, "ride.cancelled", map[string]any{"ride_id": rxRide, "customer_user_id": rxCustomer, "cancelled_by_kind": "partner", "cancelled_at": "2026-09-18T10:05:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.event, func(t *testing.T) {
			c, d := rideConsumer()
			handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(tc.event, tc.payload))
			if !handled || err != nil {
				t.Fatalf("handled=%v err=%v", handled, err)
			}
			if len(d.pushes) != 1 {
				t.Fatalf("pushes = %d, want 1: %+v", len(d.pushes), d.pushes)
			}
			p := d.pushes[0]
			if p.Type != tc.pushType || p.App != service.AppMomentum || p.AndroidChannel != service.RideChannelUpdates {
				t.Fatalf("push routing = %s/%s/%s", p.Type, p.App, p.AndroidChannel)
			}
			if p.RecipientID.String() != rxCustomer || p.EntityID.String() != rxRide || p.RideID.String() != rxRide {
				t.Fatalf("push ids = recipient %s entity %s ride %s", p.RecipientID, p.EntityID, p.RideID)
			}
			if p.DeepLink != "/mopedu/booking/"+rxRide {
				t.Fatalf("deep link = %q", p.DeepLink)
			}
			if p.Title == "" || p.Body == "" {
				t.Fatalf("empty copy: %+v", p)
			}
			if strings.Contains(p.Body, "4821") || strings.Contains(p.Title, "4821") || strings.Contains(p.Body, "123") {
				t.Fatalf("push leaks the OTP or the fare: %q / %q", p.Title, p.Body)
			}
			if p.DedupKey != "event:evt-"+tc.event+":"+tc.event {
				t.Fatalf("dedup key = %q", p.DedupKey)
			}
			if p.CreatedAt.IsZero() || p.CreatedAt.Year() != 2026 {
				t.Fatalf("created_at = %v, want the event's timestamp", p.CreatedAt)
			}
			if tc.event == events.EventRiderRideArrived && !strings.Contains(p.Body, "OTP from the app") {
				t.Fatalf("arrived copy must tell the customer to share the OTP from the app: %q", p.Body)
			}
			if tc.event == events.EventRiderRideCancelled && !strings.Contains(p.Body, "captain cancelled") {
				t.Fatalf("partner-cancelled copy = %q", p.Body)
			}
			data := service.RidePushData(p)
			for _, key := range []string{"type", "entity_id", "deep_link", "title", "body"} {
				if data[key] == "" {
					t.Errorf("data lacks %q: %v", key, data)
				}
			}
		})
	}
}

// A ride the customer cancelled themselves is not announced to them.
func TestRiderConsumer_CustomerCancellationIsNotPushed(t *testing.T) {
	c, d := rideConsumer()
	handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRideCancelled, map[string]any{
		"ride_id": rxRide, "customer_user_id": rxCustomer, "cancelled_by_kind": "customer", "cancelled_by_user_id": rxCustomer,
	}))
	if !handled || err != nil || len(d.pushes) != 0 {
		t.Fatalf("handled=%v err=%v pushes=%d", handled, err, len(d.pushes))
	}
	// A system cancellation uses the generic copy.
	handled, err = c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRideCancelled, map[string]any{
		"ride_id": rxRide, "customer_user_id": rxCustomer, "cancelled_by_kind": "system",
	}))
	if !handled || err != nil || len(d.pushes) != 1 || d.pushes[0].Body != "Your ride was cancelled. Tap for details." {
		t.Fatalf("system cancellation: handled=%v err=%v pushes=%+v", handled, err, d.pushes)
	}
}

// completed/cancelled from a producer that has not yet added
// customer_user_id: claimed, counted, no push, no error (no DLQ).
func TestRiderConsumer_LifecycleWithoutCustomerIsClaimedNotPushed(t *testing.T) {
	for _, event := range []string{events.EventRiderRideCompleted, events.EventRiderRideCancelled, events.EventRiderRideAssigned} {
		c, d := rideConsumer()
		handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(event, map[string]any{
			"ride_id": rxRide, "partner_id": rxPartner, "cancelled_by_kind": "partner",
		}))
		if !handled || err != nil {
			t.Fatalf("%s: handled=%v err=%v", event, handled, err)
		}
		if len(d.pushes) != 0 {
			t.Fatalf("%s: pushed without a recipient: %+v", event, d.pushes)
		}
	}
	// A malformed ride id IS an error: the event is unusable, not merely unenriched.
	c, _ := rideConsumer()
	if _, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRideStarted, map[string]any{
		"ride_id": "not-a-uuid", "customer_user_id": rxCustomer,
	})); err == nil {
		t.Fatal("malformed ride_id accepted")
	}
}

// ride.offered → captain.offer to the partner's captain devices, keyed per
// offer, entity_id = offer id, with a TTL that ends when the offer does.
func TestRiderConsumer_RideOfferedBecomesCaptainOffer(t *testing.T) {
	now := time.Now().UTC()
	c, d := rideConsumer()
	handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRideOffered, map[string]any{
		"ride_id": rxRide, "offer_id": rxOffer, "partner_id": rxPartner, "partner_user_id": rxPartnerUser,
		"score": 0.91, "expires_at": now.Add(18 * time.Second).Format(time.RFC3339Nano), "offered_at": now.Format(time.RFC3339Nano),
	}))
	if !handled || err != nil || len(d.pushes) != 1 {
		t.Fatalf("handled=%v err=%v pushes=%d", handled, err, len(d.pushes))
	}
	p := d.pushes[0]
	if p.Type != "captain.offer" || p.App != service.AppMopeduCaptain || p.AndroidChannel != "captain_offer" {
		t.Fatalf("routing = %s/%s/%s", p.Type, p.App, p.AndroidChannel)
	}
	if p.RecipientID.String() != rxPartnerUser {
		t.Fatalf("recipient = %s, want the partner's USER id %s (not partner_id)", p.RecipientID, rxPartnerUser)
	}
	if p.EntityID.String() != rxOffer || p.RideID.String() != rxRide || p.DeepLink != "/captain/offers/"+rxOffer {
		t.Fatalf("ids/link = entity %s ride %s link %s", p.EntityID, p.RideID, p.DeepLink)
	}
	if p.DedupKey != "ride_offer:"+rxOffer {
		t.Fatalf("dedup key = %q", p.DedupKey)
	}
	if p.TTL < 15*time.Second || p.TTL > 20*time.Second {
		t.Fatalf("ttl = %v, want the ~18 s left on the offer", p.TTL)
	}
	if strings.Contains(p.Body, "0.91") || strings.Contains(p.Body, rxRide) {
		t.Fatalf("offer body leaks matcher internals: %q", p.Body)
	}
	data := service.RidePushData(p)
	if data["entity_id"] != rxOffer || data["android_ttl"] == "" {
		t.Fatalf("offer data = %v", data)
	}

	// Without partner_user_id the consumer falls back to partner_id (warned).
	c, d = rideConsumer()
	if _, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRideOffered, map[string]any{
		"ride_id": rxRide, "offer_id": rxOffer, "partner_id": rxPartner,
	})); err != nil || len(d.pushes) != 1 || d.pushes[0].RecipientID.String() != rxPartner {
		t.Fatalf("partner_id fallback: err=%v pushes=%+v", err, d.pushes)
	}
	if d.pushes[0].TTL != rideOfferDefaultTTL {
		t.Fatalf("ttl without expires_at = %v, want %v", d.pushes[0].TTL, rideOfferDefaultTTL)
	}

	// An offer that has already expired is not pushed at all.
	c, d = rideConsumer()
	if _, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRideOffered, map[string]any{
		"ride_id": rxRide, "offer_id": rxOffer, "partner_user_id": rxPartnerUser,
		"expires_at": now.Add(-time.Second).Format(time.RFC3339Nano),
	})); err != nil || len(d.pushes) != 0 {
		t.Fatalf("expired offer: err=%v pushes=%d", err, len(d.pushes))
	}

	// No recipient at all: claimed, not pushed, not an error.
	c, d = rideConsumer()
	if handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRideOffered, map[string]any{
		"ride_id": rxRide, "offer_id": rxOffer,
	})); !handled || err != nil || len(d.pushes) != 0 {
		t.Fatalf("no recipient: handled=%v err=%v pushes=%d", handled, err, len(d.pushes))
	}
}

func TestRideOfferTTLBounds(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		in      time.Duration
		want    time.Duration
		expired bool
	}{
		{18 * time.Second, 18 * time.Second, false},
		{2 * time.Second, rideOfferMinTTL, false},
		{5 * time.Minute, rideOfferMaxTTL, false},
		{0, 0, true},
		{-time.Minute, 0, true},
	}
	for _, tc := range cases {
		got, expired := rideOfferTTL(now.Add(tc.in), now)
		if got != tc.want || expired != tc.expired {
			t.Errorf("rideOfferTTL(+%v) = %v,%v want %v,%v", tc.in, got, expired, tc.want, tc.expired)
		}
	}
	if got, expired := rideOfferTTL(time.Time{}, now); got != rideOfferDefaultTTL || expired {
		t.Errorf("unset expires_at = %v,%v", got, expired)
	}
}

// ride.payment_paid → ride.payment.paid to the customer AND "Payment
// received ₹x" to the captain, each on its own app.
func TestRiderConsumer_PaymentPaidTellsCustomerAndCaptain(t *testing.T) {
	c, d := rideConsumer()
	handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRidePaymentPaid, events.RiderRidePaymentPaidPayload{
		RideID: rxRide, CustomerUserID: rxCustomer, PartnerID: rxPartner, PartnerUserID: rxPartnerUser,
		AmountPaise: 123450, Method: "upi", PaidAt: time.Date(2026, 9, 18, 10, 30, 0, 0, time.UTC),
	}))
	if !handled || err != nil || len(d.pushes) != 2 {
		t.Fatalf("handled=%v err=%v pushes=%d", handled, err, len(d.pushes))
	}
	byApp := map[string]service.RidePush{}
	for _, p := range d.pushes {
		byApp[p.App] = p
	}
	customer, ok := byApp[service.AppMomentum]
	if !ok || customer.Type != "ride.payment.paid" || customer.RecipientID.String() != rxCustomer ||
		customer.EntityID.String() != rxRide || customer.AndroidChannel != "ride_updates" ||
		customer.DeepLink != "/mopedu/booking/"+rxRide {
		t.Fatalf("customer push = %+v", customer)
	}
	if !strings.Contains(customer.Body, "₹1,234.50") || !strings.Contains(customer.Body, "UPI") {
		t.Fatalf("customer body = %q", customer.Body)
	}
	captain, ok := byApp[service.AppMopeduCaptain]
	if !ok || captain.Type != service.CaptainTypePaymentReceived || captain.RecipientID.String() != rxPartnerUser ||
		captain.EntityID.String() != rxRide || captain.AndroidChannel != "captain_earnings" {
		t.Fatalf("captain push = %+v", captain)
	}
	if captain.Title != "Payment received ₹1,234.50" {
		t.Fatalf("captain title = %q", captain.Title)
	}
	if customer.CreatedAt != captain.CreatedAt || customer.CreatedAt.Hour() != 10 {
		t.Fatalf("created_at = %v / %v, want paid_at", customer.CreatedAt, captain.CreatedAt)
	}

	// Each recipient is independent: a payload with only the customer still
	// tells the customer; one with only the partner still tells the captain.
	c, d = rideConsumer()
	if _, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRidePaymentPaid, map[string]any{
		"ride_id": rxRide, "customer_user_id": rxCustomer, "amount_paise": 9000, "method": "cash",
	})); err != nil || len(d.pushes) != 1 || d.pushes[0].App != service.AppMomentum || !strings.Contains(d.pushes[0].Body, "₹90 paid in cash") {
		t.Fatalf("customer only: err=%v pushes=%+v", err, d.pushes)
	}
	c, d = rideConsumer()
	if _, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRidePaymentPaid, map[string]any{
		"ride_id": rxRide, "partner_id": rxPartner, "amount_paise": 9000, "method": "cash",
	})); err != nil || len(d.pushes) != 1 || d.pushes[0].App != service.AppMopeduCaptain || d.pushes[0].RecipientID.String() != rxPartner {
		t.Fatalf("captain only (partner_id fallback): err=%v pushes=%+v", err, d.pushes)
	}
}

func TestRupees(t *testing.T) {
	cases := map[int64]string{0: "₹0", 5: "₹0.05", 9000: "₹90", 12350: "₹123.50", 123450: "₹1,234.50",
		10000000: "₹1,00,000", 123456789: "₹12,34,567.89", -250: "-₹2.50"}
	for paise, want := range cases {
		if got := rupees(paise); got != want {
			t.Errorf("rupees(%d) = %q, want %q", paise, got, want)
		}
	}
}

// A consumer built without the ride push seam claims the event and sends
// nothing, rather than panicking on a nil interface.
func TestRiderConsumer_UnwiredRidePushIsClaimedNotPanicked(t *testing.T) {
	c := &Consumer{}
	handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(events.EventRiderRideArrived, map[string]any{
		"ride_id": rxRide, "customer_user_id": rxCustomer,
	}))
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	// The constructor wires the service only when there is one.
	if NewConsumerWithDialer([]string{"127.0.0.1:1"}, "g", "t", nil, nil).ridePush != nil {
		t.Fatal("a nil *Service was stored as a non-nil ridePushDeliverer")
	}
}

// Other rider.* events (offer rejected, ride rated, …) stay claimed and
// silent, and a non-rider event is not claimed.
func TestRiderConsumer_OtherRiderEventsStayClaimedAndSilent(t *testing.T) {
	c, d := rideConsumer()
	for _, event := range []string{events.EventRiderRideOfferRejected, events.EventRiderRideOfferExpired, events.EventRiderRideRated, events.EventRiderRideExpired, events.EventRiderRideRequested} {
		handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(event, map[string]any{"ride_id": rxRide}))
		if !handled || err != nil {
			t.Fatalf("%s: handled=%v err=%v", event, handled, err)
		}
	}
	if len(d.pushes) != 0 {
		t.Fatalf("silent events pushed: %+v", d.pushes)
	}
	if handled, _ := c.handleRiderEvent(context.Background(), rideEnvelope("food.order.placed", map[string]any{})); handled {
		t.Fatal("a food event was claimed by the rider handler")
	}
}
