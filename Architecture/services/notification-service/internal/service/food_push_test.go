package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

type sentFoodPush struct {
	token string
	app   string
	title string
	body  string
	data  map[string]string
}

// fakeFoodTransports records what runFoodPush does. Its device lookup returns
// EVERY device of the user whatever app is asked for, so the app guard in
// runFoodPush — not the fake — is what the app tests exercise.
type fakeFoodTransports struct {
	suppressed    bool
	claimed       map[uuid.UUID]bool
	claimErr      error
	decision      DeliveryDecision
	decisionCalls int
	inbox         []FoodPush
	devices       []postgres.UserDevice
	sendErr       map[string]error
	sends         []sentFoodPush
	retired       []string
}

func newFakeFoodTransports(devices ...postgres.UserDevice) *fakeFoodTransports {
	return &fakeFoodTransports{
		claimed:  map[uuid.UUID]bool{},
		decision: DeliveryDecision{CreateInbox: true, SendWebSocket: true, SendPush: true},
		devices:  devices,
		sendErr:  map[string]error{},
	}
}

func (f *fakeFoodTransports) recipientSuppressed(context.Context, uuid.UUID, string) bool {
	return f.suppressed
}

func (f *fakeFoodTransports) claimFoodPush(_ context.Context, id uuid.UUID) (bool, error) {
	if f.claimErr != nil {
		return false, f.claimErr
	}
	if f.claimed[id] {
		return false, nil
	}
	f.claimed[id] = true
	return true, nil
}

func (f *fakeFoodTransports) customerDecision(context.Context, uuid.UUID) DeliveryDecision {
	f.decisionCalls++
	return f.decision
}

func (f *fakeFoodTransports) writeFoodInbox(_ context.Context, p FoodPush) error {
	f.inbox = append(f.inbox, p)
	return nil
}

func (f *fakeFoodTransports) foodDevices(context.Context, uuid.UUID, string) ([]postgres.UserDevice, error) {
	return f.devices, nil
}

func (f *fakeFoodTransports) sendFoodPush(_ context.Context, d postgres.UserDevice, title, body string, data map[string]string) error {
	if err := f.sendErr[d.PushToken]; err != nil {
		return err
	}
	f.sends = append(f.sends, sentFoodPush{token: d.PushToken, app: d.App, title: title, body: body, data: data})
	return nil
}

func (f *fakeFoodTransports) retireFoodDevice(_ context.Context, _ uuid.UUID, token string) error {
	f.retired = append(f.retired, token)
	return nil
}

var (
	foodTestUser  = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	foodTestOrder = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	foodTestOffer = uuid.MustParse("33333333-3333-4333-8333-333333333333")
)

func foodDevice(token, app string) postgres.UserDevice {
	return postgres.UserDevice{UserID: foodTestUser, Platform: "android", PushToken: token, App: app, IsActive: true}
}

// One person with all three apps installed.
func allAppDevices() []postgres.UserDevice {
	return []postgres.UserDevice{
		foodDevice("momentum-phone", AppMomentum),
		foodDevice("kitchen-tablet", AppFeastKitchen),
		foodDevice("kitchen-phone", AppFeastKitchen),
		foodDevice("rider-phone", AppFeastRider),
	}
}

func kitchenPush() FoodPush {
	return FoodPush{
		DedupKey: "food_order_new:" + foodTestOrder.String(), RecipientID: foodTestUser,
		App: AppFeastKitchen, Type: FoodTypeOrderNew, AndroidChannel: FoodChannelKitchenNewOrder,
		Title: "New order", Body: "Tap to review", DeepLink: "/kitchen/orders/" + foodTestOrder.String(),
		OrderID: foodTestOrder, CreatedAt: time.Now(),
	}
}

func riderPush() FoodPush {
	return FoodPush{
		DedupKey: "food_delivery_offer:" + foodTestOffer.String(), RecipientID: foodTestUser,
		App: AppFeastRider, Type: FoodTypeDeliveryOffer, AndroidChannel: FoodChannelRiderJobOffer,
		Title: "New delivery job", Body: "Accept before the offer expires.",
		DeepLink: "/rider/offers/" + foodTestOffer.String(), OrderID: foodTestOrder, OfferID: foodTestOffer,
		OfferExpiresAt: "2026-09-13T10:00:30Z", CreatedAt: time.Now(),
	}
}

func customerPush() FoodPush {
	return FoodPush{
		DedupKey: "event:e1:food.delivery.picked_up", RecipientID: foodTestUser,
		App: AppMomentum, Type: FoodTypeOrderStatus, AndroidChannel: FoodChannelOrders,
		Title: "Out for delivery", Body: "On its way.", DeepLink: "/feast/orders/" + foodTestOrder.String(),
		OrderID: foodTestOrder, CreatedAt: time.Now(),
	}
}

// A push reaches only the install it is meant for, even when the device
// lookup hands back every app's tokens.
func TestFoodPush_SendsOnlyToDevicesOfItsApp(t *testing.T) {
	cases := []struct {
		push FoodPush
		want []string
	}{
		{kitchenPush(), []string{"kitchen-tablet", "kitchen-phone"}},
		{riderPush(), []string{"rider-phone"}},
		{customerPush(), []string{"momentum-phone"}},
	}
	for _, tc := range cases {
		t.Run(tc.push.Type, func(t *testing.T) {
			f := newFakeFoodTransports(allAppDevices()...)
			outcome, err := runFoodPush(context.Background(), f, tc.push)
			if err != nil || outcome != FoodPushSent {
				t.Fatalf("outcome=%s err=%v", outcome, err)
			}
			var got []string
			for _, s := range f.sends {
				if s.app != tc.push.App {
					t.Errorf("%s push sent to the %s install (%s)", tc.push.Type, s.app, s.token)
				}
				got = append(got, s.token)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("sent to %v, want %v", got, tc.want)
			}
		})
	}
}

// A redelivered event must not ring the kitchen twice.
func TestFoodPush_DuplicateDedupKeySendsOnce(t *testing.T) {
	f := newFakeFoodTransports(foodDevice("kitchen-tablet", AppFeastKitchen))
	p := kitchenPush()
	if outcome, err := runFoodPush(context.Background(), f, p); err != nil || outcome != FoodPushSent {
		t.Fatalf("first delivery: outcome=%s err=%v", outcome, err)
	}
	outcome, err := runFoodPush(context.Background(), f, p)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != FoodPushDuplicate {
		t.Fatalf("second delivery outcome = %s, want duplicate", outcome)
	}
	if len(f.sends) != 1 {
		t.Fatalf("duplicate event sent %d pushes, want 1", len(f.sends))
	}

	// The same order announced to a second staff member is a different push.
	other := p
	other.RecipientID = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	if outcome, _ := runFoodPush(context.Background(), f, other); outcome != FoodPushSent {
		t.Fatalf("second recipient outcome = %s, want sent", outcome)
	}
}

// A dedup ledger outage must not cost a restaurant its order.
func TestFoodPush_ClaimFailureStillSends(t *testing.T) {
	f := newFakeFoodTransports(foodDevice("kitchen-tablet", AppFeastKitchen))
	f.claimErr = errors.New("postgres down")
	if outcome, err := runFoodPush(context.Background(), f, kitchenPush()); err != nil || outcome != FoodPushSent {
		t.Fatalf("outcome=%s err=%v", outcome, err)
	}
}

func TestFoodPush_CustomerFollowsMomentumPreferences(t *testing.T) {
	f := newFakeFoodTransports(foodDevice("momentum-phone", AppMomentum))
	f.decision = DeliveryDecision{CreateInbox: true, SendWebSocket: true, SendPush: false}
	outcome, err := runFoodPush(context.Background(), f, customerPush())
	if err != nil || outcome != FoodPushMuted {
		t.Fatalf("outcome=%s err=%v, want muted", outcome, err)
	}
	if len(f.sends) != 0 {
		t.Fatal("customer with food_orders push off still got a push")
	}
	if len(f.inbox) != 1 {
		t.Fatalf("inbox rows = %d, want 1 (in-app still on)", len(f.inbox))
	}
}

func TestFoodPush_OperationalPushesIgnoreMomentumPreferences(t *testing.T) {
	for _, p := range []FoodPush{kitchenPush(), riderPush()} {
		f := newFakeFoodTransports(allAppDevices()...)
		f.decision = DeliveryDecision{}
		outcome, err := runFoodPush(context.Background(), f, p)
		if err != nil || outcome != FoodPushSent {
			t.Fatalf("%s: outcome=%s err=%v", p.Type, outcome, err)
		}
		if f.decisionCalls != 0 || len(f.inbox) != 0 {
			t.Fatalf("%s consulted Momentum preferences (%d) or wrote an inbox row (%d)", p.Type, f.decisionCalls, len(f.inbox))
		}
	}
}

func TestFoodPush_SuppressedAccountGetsNothing(t *testing.T) {
	f := newFakeFoodTransports(allAppDevices()...)
	f.suppressed = true
	outcome, err := runFoodPush(context.Background(), f, kitchenPush())
	if err != nil || outcome != FoodPushSuppressed || len(f.sends) != 0 || len(f.claimed) != 0 {
		t.Fatalf("outcome=%s err=%v sends=%d claims=%d", outcome, err, len(f.sends), len(f.claimed))
	}
}

func TestFoodPush_RejectedDeviceIsRetiredAndOthersStillSend(t *testing.T) {
	f := newFakeFoodTransports(foodDevice("dead", AppFeastKitchen), foodDevice("alive", AppFeastKitchen))
	f.sendErr["dead"] = fmt.Errorf("unregistered: %w", push.ErrDeviceRejected)
	outcome, err := runFoodPush(context.Background(), f, kitchenPush())
	if err != nil || outcome != FoodPushSent {
		t.Fatalf("outcome=%s err=%v", outcome, err)
	}
	if len(f.retired) != 1 || f.retired[0] != "dead" {
		t.Fatalf("retired = %v", f.retired)
	}
}

func TestFoodPush_TypeMustMatchItsApp(t *testing.T) {
	p := kitchenPush()
	p.App = AppMomentum
	f := newFakeFoodTransports(allAppDevices()...)
	if _, err := runFoodPush(context.Background(), f, p); err == nil {
		t.Fatal("a kitchen new-order push addressed to Momentum was accepted")
	}
	if len(f.sends) != 0 {
		t.Fatal("invalid push was sent")
	}
}

func TestFoodPushData_CarriesOnlyAllowlistedKeys(t *testing.T) {
	for _, p := range []FoodPush{kitchenPush(), riderPush(), customerPush()} {
		data := FoodPushData(p)
		for k := range data {
			if !FoodPushDataKeys[k] {
				t.Errorf("%s: data key %q is not allowlisted", p.Type, k)
			}
		}
		for _, k := range []string{"type", "order_id", "deep_link", "title", "body"} {
			if data[k] == "" {
				t.Errorf("%s: required data key %q empty", p.Type, k)
			}
		}
		if data[push.AndroidChannelDataKey] != p.AndroidChannel {
			t.Errorf("%s: channel %q", p.Type, data[push.AndroidChannelDataKey])
		}
	}
	offer := FoodPushData(riderPush())
	if offer["expires_at"] != "2026-09-13T10:00:30Z" || offer["offer_id"] != foodTestOffer.String() {
		t.Fatalf("offer data lacks expiry/offer id: %v", offer)
	}
}

func TestFoodOrdersPreferenceCategory(t *testing.T) {
	if categoryForEvent(FoodTypeOrderStatus) != catFoodOrders {
		t.Fatal("food_order_status is not in the food_orders category")
	}
	p := postgres.DefaultNotificationPreferences("u1")
	if !p.PushFoodOrders || !p.InappFoodOrders {
		t.Fatal("food_orders must default on for both halves")
	}
	if d := resolveDecision(FoodTypeOrderStatus, p, false); !d.SendPush || !d.CreateInbox {
		t.Fatalf("default decision = %+v", d)
	}
	p.PushFoodOrders = false
	if d := resolveDecision(FoodTypeOrderStatus, p, false); d.SendPush || !d.CreateInbox {
		t.Fatalf("push_food_orders=false decision = %+v", d)
	}
	p.PushFoodOrders, p.InappFoodOrders = true, false
	if d := resolveDecision(FoodTypeOrderStatus, p, false); !d.SendPush || d.CreateInbox {
		t.Fatalf("inapp_food_orders=false decision = %+v", d)
	}
	// Operational types are never category-gated.
	p.PushFoodOrders = false
	for _, typ := range []string{FoodTypeOrderNew, FoodTypeDeliveryOffer} {
		if !pushCategoryAllowed(p, typ) {
			t.Fatalf("%s gated by the food_orders toggle", typ)
		}
	}
}
