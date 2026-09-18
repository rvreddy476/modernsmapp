package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

type sentRidePush struct {
	token string
	app   string
	title string
	body  string
	data  map[string]string
}

// fakeRideTransports records what runRidePush does. Its device lookup returns
// EVERY device of the user whatever app is asked for, so the app guard in
// runRidePush — not the fake — is what the app tests exercise.
type fakeRideTransports struct {
	suppressed  bool
	claimed     map[uuid.UUID]bool
	claimErr    error
	inboxWanted bool
	inboxAsked  []string
	inbox       []RidePush
	devices     []postgres.UserDevice
	sendErr     map[string]error
	sends       []sentRidePush
	retired     []string
}

func newFakeRideTransports(devices ...postgres.UserDevice) *fakeRideTransports {
	return &fakeRideTransports{
		claimed:     map[uuid.UUID]bool{},
		inboxWanted: true,
		devices:     devices,
		sendErr:     map[string]error{},
	}
}

func (f *fakeRideTransports) recipientSuppressed(context.Context, uuid.UUID, string) bool {
	return f.suppressed
}

func (f *fakeRideTransports) claimPush(_ context.Context, id uuid.UUID) (bool, error) {
	if f.claimErr != nil {
		return false, f.claimErr
	}
	if f.claimed[id] {
		return false, nil
	}
	f.claimed[id] = true
	return true, nil
}

func (f *fakeRideTransports) rideInboxWanted(_ context.Context, _ uuid.UUID, pushType string) bool {
	f.inboxAsked = append(f.inboxAsked, pushType)
	return f.inboxWanted
}

func (f *fakeRideTransports) writeRideInbox(_ context.Context, p RidePush) error {
	f.inbox = append(f.inbox, p)
	return nil
}

func (f *fakeRideTransports) appDevices(context.Context, uuid.UUID, string) ([]postgres.UserDevice, error) {
	return f.devices, nil
}

func (f *fakeRideTransports) sendAppPush(_ context.Context, d postgres.UserDevice, title, body string, data map[string]string) error {
	if err := f.sendErr[d.PushToken]; err != nil {
		return err
	}
	f.sends = append(f.sends, sentRidePush{token: d.PushToken, app: d.App, title: title, body: body, data: data})
	return nil
}

func (f *fakeRideTransports) retireAppDevice(_ context.Context, _ uuid.UUID, token string) error {
	f.retired = append(f.retired, token)
	return nil
}

var (
	rideTestUser  = uuid.MustParse("51111111-1111-4111-8111-111111111111")
	rideTestRide  = uuid.MustParse("52222222-2222-4222-8222-222222222222")
	rideTestOffer = uuid.MustParse("53333333-3333-4333-8333-333333333333")
)

func rideDevice(token, app string) postgres.UserDevice {
	return postgres.UserDevice{UserID: rideTestUser, Platform: "android", PushToken: token, App: app, IsActive: true}
}

// One person with every app installed — a captain who also rides as a
// customer, with a Feast side gig.
func everyAppDevices() []postgres.UserDevice {
	return []postgres.UserDevice{
		rideDevice("momentum-phone", AppMomentum),
		rideDevice("kitchen-tablet", AppFeastKitchen),
		rideDevice("rider-phone", AppFeastRider),
		rideDevice("captain-phone", AppMopeduCaptain),
		rideDevice("captain-spare", AppMopeduCaptain),
	}
}

func customerRidePush(typ string) RidePush {
	return RidePush{
		DedupKey: "event:e1:" + typ, RecipientID: rideTestUser,
		App: AppMomentum, Type: typ, AndroidChannel: RideChannelUpdates,
		Title: "Captain assigned", Body: "Your captain is on the way.",
		DeepLink: "/mopedu/booking/" + rideTestRide.String(),
		EntityID: rideTestRide, RideID: rideTestRide, CreatedAt: time.Now(),
	}
}

func captainOfferPush() RidePush {
	return RidePush{
		DedupKey: "ride_offer:" + rideTestOffer.String(), RecipientID: rideTestUser,
		App: AppMopeduCaptain, Type: CaptainTypeOffer, AndroidChannel: CaptainChannelOffer,
		Title: "New ride offer", Body: "Accept before the offer expires.",
		DeepLink: "/captain/offers/" + rideTestOffer.String(),
		EntityID: rideTestOffer, RideID: rideTestRide, TTL: 20 * time.Second, CreatedAt: time.Now(),
	}
}

func captainPaidPush() RidePush {
	return RidePush{
		DedupKey: "event:e2:captain.payment.received", RecipientID: rideTestUser,
		App: AppMopeduCaptain, Type: CaptainTypePaymentReceived, AndroidChannel: CaptainChannelOnDuty,
		Title: "Payment received", Body: "₹123.50 received for your last ride.",
		DeepLink: "/captain/rides/" + rideTestRide.String(),
		EntityID: rideTestRide, RideID: rideTestRide, CreatedAt: time.Now(),
	}
}

// A captain push goes only to mopedu_captain devices; a ride.* push only to
// Momentum devices — even when the device lookup hands back every app's
// tokens.
func TestRidePush_SendsOnlyToDevicesOfItsApp(t *testing.T) {
	cases := []struct {
		push RidePush
		want []string
	}{
		{captainOfferPush(), []string{"captain-phone", "captain-spare"}},
		{captainPaidPush(), []string{"captain-phone", "captain-spare"}},
	}
	for _, typ := range []string{RideTypeAssigned, RideTypeArriving, RideTypeArrived, RideTypeStarted,
		RideTypeCompleted, RideTypeCancelled, RideTypePaymentPaid} {
		cases = append(cases, struct {
			push RidePush
			want []string
		}{customerRidePush(typ), []string{"momentum-phone"}})
	}
	for _, tc := range cases {
		t.Run(tc.push.Type, func(t *testing.T) {
			f := newFakeRideTransports(everyAppDevices()...)
			outcome, err := runRidePush(context.Background(), f, tc.push)
			if err != nil || outcome != RidePushSent {
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

// The data payload is exactly the app contract plus transport keys, and the
// type strings are the ones the apps switch on.
func TestRidePush_DataIsTheAppContract(t *testing.T) {
	f := newFakeRideTransports(rideDevice("momentum-phone", AppMomentum))
	if _, err := runRidePush(context.Background(), f, customerRidePush(RideTypeArrived)); err != nil {
		t.Fatal(err)
	}
	data := f.sends[0].data
	for k := range data {
		if !RidePushDataKeys[k] {
			t.Errorf("unexpected data key %q", k)
		}
	}
	if data["type"] != "ride.arrived" || data["entity_id"] != rideTestRide.String() ||
		data["deep_link"] != "/mopedu/booking/"+rideTestRide.String() ||
		data["title"] != "Captain assigned" || data["body"] != "Your captain is on the way." {
		t.Fatalf("customer data = %v", data)
	}
	if data["collapse_key"] != "ride:"+rideTestRide.String() || data[push.AndroidChannelDataKey] != "ride_updates" {
		t.Fatalf("customer transport keys = %v", data)
	}
	if _, has := data[push.AndroidTTLDataKey]; has {
		t.Fatal("a customer push carries no ttl")
	}

	f = newFakeRideTransports(rideDevice("captain-phone", AppMopeduCaptain))
	if _, err := runRidePush(context.Background(), f, captainOfferPush()); err != nil {
		t.Fatal(err)
	}
	data = f.sends[0].data
	if data["type"] != "captain.offer" || data["entity_id"] != rideTestOffer.String() ||
		data["deep_link"] != "/captain/offers/"+rideTestOffer.String() {
		t.Fatalf("offer data = %v", data)
	}
	if data[push.AndroidTTLDataKey] != "20s" || data[push.AndroidChannelDataKey] != "captain_offer" ||
		data["collapse_key"] != "ride_offer:"+rideTestOffer.String() {
		t.Fatalf("offer transport keys = %v", data)
	}

	want := map[string]string{
		RideTypeAssigned: AppMomentum, RideTypeArriving: AppMomentum, RideTypeArrived: AppMomentum,
		RideTypeStarted: AppMomentum, RideTypeCompleted: AppMomentum, RideTypeCancelled: AppMomentum,
		RideTypePaymentPaid: AppMomentum, CaptainTypeOffer: AppMopeduCaptain, CaptainTypePaymentReceived: AppMopeduCaptain,
	}
	for typ, app := range want {
		if got, ok := RidePushAppFor(typ); !ok || got != app {
			t.Errorf("RidePushAppFor(%s) = %s,%v want %s", typ, got, ok, app)
		}
	}
	if _, ok := RidePushAppFor("food_order_new"); ok {
		t.Error("a Feast type routed as a Mopedu push")
	}
}

// The customer gets an inbox row (entity rider_ride, the ride id) when their
// in-app preferences allow one; the push is sent regardless. Captains never
// get an inbox row and their preferences are never consulted.
func TestRidePush_InboxFollowsInAppPreferenceOnly(t *testing.T) {
	f := newFakeRideTransports(rideDevice("momentum-phone", AppMomentum))
	if outcome, err := runRidePush(context.Background(), f, customerRidePush(RideTypeCompleted)); err != nil || outcome != RidePushSent {
		t.Fatalf("outcome=%s err=%v", outcome, err)
	}
	if len(f.inbox) != 1 || f.inbox[0].RideID != rideTestRide {
		t.Fatalf("inbox rows = %+v", f.inbox)
	}
	if RideInboxEntityType != "rider_ride" {
		t.Fatalf("inbox entity type = %q", RideInboxEntityType)
	}

	f = newFakeRideTransports(rideDevice("momentum-phone", AppMomentum))
	f.inboxWanted = false
	if outcome, _ := runRidePush(context.Background(), f, customerRidePush(RideTypeCancelled)); outcome != RidePushSent {
		t.Fatalf("in-app off: outcome = %s, the push itself is operational", outcome)
	}
	if len(f.inbox) != 0 || len(f.sends) != 1 {
		t.Fatalf("in-app off: inbox=%d sends=%d", len(f.inbox), len(f.sends))
	}

	f = newFakeRideTransports(rideDevice("captain-phone", AppMopeduCaptain))
	if _, err := runRidePush(context.Background(), f, captainOfferPush()); err != nil {
		t.Fatal(err)
	}
	if len(f.inbox) != 0 || len(f.inboxAsked) != 0 {
		t.Fatalf("captain push touched the inbox: rows=%d asked=%v", len(f.inbox), f.inboxAsked)
	}
}

// A redelivered event must not buzz the customer twice; a dedup ledger
// outage must not lose "your captain has arrived"; a suppressed account
// gets nothing.
func TestRidePush_DedupClaimFailureAndSuppression(t *testing.T) {
	f := newFakeRideTransports(rideDevice("momentum-phone", AppMomentum))
	p := customerRidePush(RideTypeArrived)
	if outcome, _ := runRidePush(context.Background(), f, p); outcome != RidePushSent {
		t.Fatalf("first: %s", outcome)
	}
	if outcome, _ := runRidePush(context.Background(), f, p); outcome != RidePushDuplicate || len(f.sends) != 1 {
		t.Fatalf("second: %s, sends=%d", outcome, len(f.sends))
	}

	f = newFakeRideTransports(rideDevice("captain-phone", AppMopeduCaptain))
	f.claimErr = errors.New("postgres down")
	if outcome, err := runRidePush(context.Background(), f, captainOfferPush()); err != nil || outcome != RidePushSent {
		t.Fatalf("claim outage: outcome=%s err=%v", outcome, err)
	}

	f = newFakeRideTransports(rideDevice("momentum-phone", AppMomentum))
	f.suppressed = true
	if outcome, _ := runRidePush(context.Background(), f, p); outcome != RidePushSuppressed || len(f.sends) != 0 || len(f.inbox) != 0 {
		t.Fatalf("suppressed: outcome=%s sends=%d inbox=%d", outcome, len(f.sends), len(f.inbox))
	}
}

// A permanently rejected token is retired; nothing registered means
// no_devices, not an error.
func TestRidePush_RejectedTokenRetiredAndNoDevices(t *testing.T) {
	f := newFakeRideTransports(rideDevice("captain-phone", AppMopeduCaptain), rideDevice("captain-spare", AppMopeduCaptain))
	f.sendErr["captain-phone"] = push.ErrDeviceRejected
	if outcome, _ := runRidePush(context.Background(), f, captainOfferPush()); outcome != RidePushSent {
		t.Fatalf("outcome = %s", outcome)
	}
	if strings.Join(f.retired, ",") != "captain-phone" {
		t.Fatalf("retired = %v", f.retired)
	}

	f = newFakeRideTransports(rideDevice("momentum-phone", AppMomentum))
	if outcome, err := runRidePush(context.Background(), f, captainOfferPush()); err != nil || outcome != RidePushNoDevices {
		t.Fatalf("captain without the app: outcome=%s err=%v", outcome, err)
	}
}

// The routing table is enforced, not advisory.
func TestRidePush_ValidateRejectsMisroutedPushes(t *testing.T) {
	bad := []RidePush{
		func() RidePush { p := customerRidePush(RideTypeAssigned); p.App = AppMopeduCaptain; return p }(),
		func() RidePush { p := captainOfferPush(); p.App = AppMomentum; return p }(),
		func() RidePush { p := captainOfferPush(); p.App = AppFeastRider; return p }(),
		func() RidePush { p := customerRidePush(RideTypeAssigned); p.Type = "food_order_status"; return p }(),
		func() RidePush { p := captainOfferPush(); p.EntityID = rideTestRide; return p }(),
		func() RidePush { p := customerRidePush(RideTypeAssigned); p.RecipientID = uuid.Nil; return p }(),
		func() RidePush { p := customerRidePush(RideTypeAssigned); p.DedupKey = ""; return p }(),
	}
	for i, p := range bad {
		f := newFakeRideTransports(everyAppDevices()...)
		if _, err := runRidePush(context.Background(), f, p); err == nil {
			t.Errorf("case %d (%s → %s) accepted", i, p.Type, p.App)
		}
		if len(f.sends) != 0 {
			t.Errorf("case %d sent %d pushes", i, len(f.sends))
		}
	}
}
