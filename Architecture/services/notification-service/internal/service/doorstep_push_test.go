package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

type sentDoorstepPush struct {
	token string
	app   string
	data  map[string]string
}

// fakeDoorstepTransports records what runDoorstepPush does. Its device
// lookup returns EVERY device of the user whatever app is asked for, so the
// app guard in runDoorstepPush — not the fake — is what the tests exercise.
type fakeDoorstepTransports struct {
	suppressed     bool
	claimed        map[uuid.UUID]bool
	claimErr       error
	decision       DeliveryDecision
	decisionsAsked []string
	inbox          []DoorstepPush
	devices        []postgres.UserDevice
	sends          []sentDoorstepPush
	retired        []string
	sendErr        map[string]error
}

func newFakeDoorstepTransports(devices ...postgres.UserDevice) *fakeDoorstepTransports {
	return &fakeDoorstepTransports{
		claimed:  map[uuid.UUID]bool{},
		decision: DeliveryDecision{CreateInbox: true, SendWebSocket: true, SendPush: true},
		devices:  devices,
		sendErr:  map[string]error{},
	}
}

func (f *fakeDoorstepTransports) recipientSuppressed(context.Context, uuid.UUID, string) bool {
	return f.suppressed
}

func (f *fakeDoorstepTransports) claimPush(_ context.Context, id uuid.UUID) (bool, error) {
	if f.claimErr != nil {
		return false, f.claimErr
	}
	if f.claimed[id] {
		return false, nil
	}
	f.claimed[id] = true
	return true, nil
}

func (f *fakeDoorstepTransports) doorstepCustomerDecision(_ context.Context, _ uuid.UUID, pushType string) DeliveryDecision {
	f.decisionsAsked = append(f.decisionsAsked, pushType)
	return f.decision
}

func (f *fakeDoorstepTransports) writeDoorstepInbox(_ context.Context, p DoorstepPush) error {
	f.inbox = append(f.inbox, p)
	return nil
}

func (f *fakeDoorstepTransports) appDevices(context.Context, uuid.UUID, string) ([]postgres.UserDevice, error) {
	return f.devices, nil
}

func (f *fakeDoorstepTransports) sendAppPush(_ context.Context, d postgres.UserDevice, _, _ string, data map[string]string) error {
	if err := f.sendErr[d.PushToken]; err != nil {
		return err
	}
	f.sends = append(f.sends, sentDoorstepPush{token: d.PushToken, app: d.App, data: data})
	return nil
}

func (f *fakeDoorstepTransports) retireAppDevice(_ context.Context, _ uuid.UUID, token string) error {
	f.retired = append(f.retired, token)
	return nil
}

var (
	dpUser    = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	dpBooking = uuid.MustParse("11111111-1111-4111-8111-111111111111")
)

func dpDevices() []postgres.UserDevice {
	return []postgres.UserDevice{
		{UserID: dpUser, Platform: "android", PushToken: "momentum-tok", App: AppMomentum},
		{UserID: dpUser, Platform: "android", PushToken: "pro-tok", App: AppDoorstepPro},
		{UserID: dpUser, Platform: "android", PushToken: "captain-tok", App: AppMopeduCaptain},
		{UserID: dpUser, Platform: "android", PushToken: "kitchen-tok", App: AppFeastKitchen},
	}
}

func dpCustomerPush() DoorstepPush {
	return DoorstepPush{
		DedupKey: "event:e1", RecipientID: dpUser, App: AppMomentum, Type: DoorstepTypeBookingAssigned,
		Title: "Professional assigned", Body: "Ravi will take care of your booking.",
		DeepLink: "/doorstep/bookings/" + dpBooking.String(), EntityID: dpBooking,
		CollapseKey: "doorstep_booking:" + dpBooking.String(),
		Data:        map[string]string{"booking_id": dpBooking.String(), "pro_first_name": "Ravi"},
		CreatedAt:   time.Date(2026, 10, 4, 6, 30, 0, 0, time.UTC),
	}
}

func dpProPush() DoorstepPush {
	return DoorstepPush{
		DedupKey: "event:e2", RecipientID: dpUser, App: AppDoorstepPro, Type: DoorstepTypeProJobCancelled,
		Title: "Job cancelled", Body: "A job on your schedule was cancelled.",
		DeepLink: "doorstep-pro://jobs/" + dpBooking.String(), EntityID: dpBooking,
		CollapseKey: "doorstep_job:" + dpBooking.String(),
		Data:        map[string]string{"booking_id": dpBooking.String()},
	}
}

func TestDoorstepPush_CustomerReachesOnlyMomentumAndWritesInbox(t *testing.T) {
	f := newFakeDoorstepTransports(dpDevices()...)
	out, err := runDoorstepPush(context.Background(), f, dpCustomerPush())
	if err != nil || out != DoorstepPushSent {
		t.Fatalf("outcome %v %v", out, err)
	}
	if len(f.sends) != 1 || f.sends[0].token != "momentum-tok" {
		t.Fatalf("sends = %+v", f.sends)
	}
	if len(f.inbox) != 1 || len(f.decisionsAsked) != 1 || f.decisionsAsked[0] != DoorstepTypeBookingAssigned {
		t.Fatalf("inbox %d decisions %v", len(f.inbox), f.decisionsAsked)
	}
	d := f.sends[0].data
	if d["type"] != DoorstepTypeBookingAssigned || d["entity_type"] != DoorstepEntityBooking || d["entity_id"] != dpBooking.String() ||
		d["collapse_key"] != "doorstep_booking:"+dpBooking.String() || d[push.AndroidChannelDataKey] != DoorstepChannelUpdates ||
		d["pro_first_name"] != "Ravi" {
		t.Fatalf("data = %v", d)
	}
	allowed := DoorstepPushAllowedDataKeys(DoorstepTypeBookingAssigned)
	for k := range d {
		if !allowed[k] {
			t.Fatalf("data key %q not allowed", k)
		}
	}
}

func TestDoorstepPush_ProReachesOnlyTheProAppAndIgnoresMomentumPreferences(t *testing.T) {
	f := newFakeDoorstepTransports(dpDevices()...)
	f.decision = DeliveryDecision{} // a Momentum user who silenced everything
	out, err := runDoorstepPush(context.Background(), f, dpProPush())
	if err != nil || out != DoorstepPushSent {
		t.Fatalf("outcome %v %v", out, err)
	}
	if len(f.sends) != 1 || f.sends[0].token != "pro-tok" {
		t.Fatalf("sends = %+v", f.sends)
	}
	if len(f.decisionsAsked) != 0 {
		t.Fatalf("a pro push consulted Momentum preferences: %v", f.decisionsAsked)
	}
	if len(f.inbox) != 1 {
		t.Fatalf("pro job push wrote %d inbox rows", len(f.inbox))
	}
	if f.sends[0].data[push.AndroidChannelDataKey] != DoorstepChannelProJobs {
		t.Fatalf("channel = %v", f.sends[0].data)
	}
}

func TestDoorstepPush_OfferWritesNoInboxAndCarriesTTL(t *testing.T) {
	offer := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	p := DoorstepPush{
		DedupKey: "event:e3", RecipientID: dpUser, App: AppDoorstepPro, Type: DoorstepTypeProOfferNew,
		Title: "New job offer", Body: "Accept it in the app before the offer expires.",
		DeepLink: "doorstep-pro://offers/" + offer.String(), EntityID: offer,
		CollapseKey: "doorstep_offer:" + offer.String(), TTL: 90 * time.Second,
		Data: map[string]string{"offer_id": offer.String(), "booking_id": dpBooking.String(), "expires_at": "2026-10-04T06:35:00Z"},
	}
	f := newFakeDoorstepTransports(dpDevices()...)
	if out, err := runDoorstepPush(context.Background(), f, p); err != nil || out != DoorstepPushSent {
		t.Fatalf("outcome %v %v", out, err)
	}
	if len(f.inbox) != 0 {
		t.Fatal("an offer wrote an inbox row")
	}
	if f.sends[0].data[push.AndroidTTLDataKey] != "90s" {
		t.Fatalf("ttl = %v", f.sends[0].data)
	}
}

// Preference off: the customer's push_doorstep off keeps the device quiet
// (the inbox row still lands); inapp_doorstep off drops the row.
func TestDoorstepPush_PreferenceOffSuppressesPush(t *testing.T) {
	f := newFakeDoorstepTransports(dpDevices()...)
	f.decision = DeliveryDecision{CreateInbox: true, SendWebSocket: true, SendPush: false}
	out, err := runDoorstepPush(context.Background(), f, dpCustomerPush())
	if err != nil || out != DoorstepPushMuted || len(f.sends) != 0 || len(f.inbox) != 1 {
		t.Fatalf("outcome %v %v sends %d inbox %d", out, err, len(f.sends), len(f.inbox))
	}

	f = newFakeDoorstepTransports(dpDevices()...)
	f.decision = DeliveryDecision{SendPush: true}
	if _, err := runDoorstepPush(context.Background(), f, dpCustomerPush()); err != nil {
		t.Fatal(err)
	}
	if len(f.inbox) != 0 || len(f.sends) != 1 {
		t.Fatalf("inapp off: inbox %d sends %d", len(f.inbox), len(f.sends))
	}
}

// The real preference resolution: every customer Doorstep type sits in the
// doorstep category (default on), and its toggles gate push and inbox.
func TestDoorstepPreferenceCategory(t *testing.T) {
	defaults := postgres.DefaultNotificationPreferences(dpUser.String())
	if !defaults.PushDoorstep || !defaults.InappDoorstep {
		t.Fatal("doorstep category must default on")
	}
	for _, typ := range DoorstepPushTypes() {
		spec, _ := DoorstepPushSpecFor(typ)
		if spec.App != AppMomentum {
			if categoryForEvent(typ) != catAlwaysOn {
				t.Errorf("%s: pro type in category %v", typ, categoryForEvent(typ))
			}
			continue
		}
		if categoryForEvent(typ) != catDoorstep {
			t.Errorf("%s: category %v, want doorstep", typ, categoryForEvent(typ))
			continue
		}
		on := resolveDecision(typ, postgres.DefaultNotificationPreferences(dpUser.String()), false)
		if !on.SendPush || !on.CreateInbox {
			t.Errorf("%s with defaults: %+v", typ, on)
		}
		pushOff := postgres.DefaultNotificationPreferences(dpUser.String())
		pushOff.PushDoorstep = false
		if d := resolveDecision(typ, pushOff, false); d.SendPush || !d.CreateInbox {
			t.Errorf("%s with push_doorstep off: %+v", typ, d)
		}
		inappOff := postgres.DefaultNotificationPreferences(dpUser.String())
		inappOff.InappDoorstep = false
		if d := resolveDecision(typ, inappOff, false); d.CreateInbox || !d.SendPush {
			t.Errorf("%s with inapp_doorstep off: %+v", typ, d)
		}
		master := postgres.DefaultNotificationPreferences(dpUser.String())
		master.PushEnabled = false
		if d := resolveDecision(typ, master, false); d.SendPush {
			t.Errorf("%s with push disabled: %+v", typ, d)
		}
	}
	// Turning doorstep off silences nothing else.
	off := postgres.DefaultNotificationPreferences(dpUser.String())
	off.PushDoorstep = false
	if d := resolveDecision(FoodTypeOrderStatus, off, false); !d.SendPush {
		t.Fatal("push_doorstep off silenced Feast")
	}
}

func TestDoorstepPush_DuplicateIsSentOnce(t *testing.T) {
	f := newFakeDoorstepTransports(dpDevices()...)
	p := dpCustomerPush()
	if out, _ := runDoorstepPush(context.Background(), f, p); out != DoorstepPushSent {
		t.Fatalf("first = %v", out)
	}
	if out, _ := runDoorstepPush(context.Background(), f, p); out != DoorstepPushDuplicate {
		t.Fatalf("second = %v", out)
	}
	if len(f.sends) != 1 || len(f.inbox) != 1 {
		t.Fatalf("sends %d inbox %d after a duplicate", len(f.sends), len(f.inbox))
	}
	// The same event for a different type or recipient is its own delivery.
	other := p
	other.Type = DoorstepTypeBookingStarted
	other.Data = map[string]string{"booking_id": dpBooking.String()}
	if DoorstepDedupID(other) == DoorstepDedupID(p) {
		t.Fatal("dedup id ignores the type")
	}
	// A dedup-store blip still sends.
	f = newFakeDoorstepTransports(dpDevices()...)
	f.claimErr = context.DeadlineExceeded
	if out, _ := runDoorstepPush(context.Background(), f, p); out != DoorstepPushSent {
		t.Fatalf("claim failure outcome = %v", out)
	}
}

func TestDoorstepPush_SuppressedAccountGetsNothing(t *testing.T) {
	f := newFakeDoorstepTransports(dpDevices()...)
	f.suppressed = true
	if out, _ := runDoorstepPush(context.Background(), f, dpProPush()); out != DoorstepPushSuppressed || len(f.sends) != 0 || len(f.inbox) != 0 {
		t.Fatalf("suppressed: %v sends %d inbox %d", out, len(f.sends), len(f.inbox))
	}
}

func TestDoorstepPush_RejectedTokenIsRetired(t *testing.T) {
	f := newFakeDoorstepTransports(dpDevices()...)
	f.sendErr["pro-tok"] = push.ErrDeviceRejected
	if out, _ := runDoorstepPush(context.Background(), f, dpProPush()); out != DoorstepPushNoDevices {
		t.Fatalf("outcome = %v", out)
	}
	if len(f.retired) != 1 || f.retired[0] != "pro-tok" {
		t.Fatalf("retired = %v", f.retired)
	}
}

func TestDoorstepPush_ValidateRefusesBadPushes(t *testing.T) {
	cases := map[string]func(p *DoorstepPush){
		"wrong app":         func(p *DoorstepPush) { p.App = AppDoorstepPro },
		"unknown type":      func(p *DoorstepPush) { p.Type = "doorstep.booking.teleported" },
		"foreign data key":  func(p *DoorstepPush) { p.Data["customer_phone"] = "x" },
		"key of other type": func(p *DoorstepPush) { p.Data["offer_id"] = "x" },
		"no recipient":      func(p *DoorstepPush) { p.RecipientID = uuid.Nil },
		"no dedup key":      func(p *DoorstepPush) { p.DedupKey = "" },
		"no entity":         func(p *DoorstepPush) { p.EntityID = uuid.Nil },
		"no collapse key":   func(p *DoorstepPush) { p.CollapseKey = "" },
		"no body":           func(p *DoorstepPush) { p.Body = "" },
	}
	for name, mutate := range cases {
		p := dpCustomerPush()
		mutate(&p)
		f := newFakeDoorstepTransports(dpDevices()...)
		if _, err := runDoorstepPush(context.Background(), f, p); err == nil || len(f.sends) != 0 {
			t.Errorf("%s: accepted (err %v, sends %d)", name, err, len(f.sends))
		}
	}
}

func TestDoorstepRegistryShape(t *testing.T) {
	types := DoorstepPushTypes()
	if len(types) != 34 {
		t.Fatalf("%d push types, the contract has 34", len(types))
	}
	for _, typ := range types {
		spec, _ := DoorstepPushSpecFor(typ)
		pro := strings.HasPrefix(typ, "doorstep.pro.")
		if pro != (spec.App == AppDoorstepPro) {
			t.Errorf("%s routed to %s", typ, spec.App)
		}
		if spec.Channel == "" || spec.EntityType == "" || len(spec.DataKeys) == 0 {
			t.Errorf("%s incomplete: %+v", typ, spec)
		}
		for _, k := range spec.DataKeys {
			if doorstepTransportKeys[k] {
				t.Errorf("%s: contract key %s collides with a transport key", typ, k)
			}
		}
	}
}
