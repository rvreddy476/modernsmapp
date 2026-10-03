package events

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// The contract (contracts/doorstep/asyncapi.yaml).

type doorstepContractPush struct {
	FromEvent string   `yaml:"from_event"`
	Data      []string `yaml:"data"`
	Deeplink  string   `yaml:"deeplink"`
	When      string   `yaml:"when"`
	Priority  string   `yaml:"priority"`
}

type doorstepContract struct {
	Channels map[string]struct {
		Publish struct {
			Message struct {
				OneOf []struct {
					Ref string `yaml:"$ref"`
				} `yaml:"oneOf"`
			} `yaml:"message"`
		} `yaml:"publish"`
	} `yaml:"channels"`
	XPush struct {
		Momentum map[string]doorstepContractPush `yaml:"momentum"`
		Pro      map[string]doorstepContractPush `yaml:"doorstep_pro"`
	} `yaml:"x-push-types"`
	Components struct {
		Messages map[string]struct {
			Name string `yaml:"name"`
		} `yaml:"messages"`
	} `yaml:"components"`
}

func loadDoorstepContract(t *testing.T) doorstepContract {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		path := filepath.Join(dir, "contracts", "doorstep", "asyncapi.yaml")
		if raw, err := os.ReadFile(path); err == nil {
			var c doorstepContract
			if err := yaml.Unmarshal(raw, &c); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			return c
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("contracts/doorstep/asyncapi.yaml not found above the test directory")
		}
		dir = parent
	}
}

// contractEvents lists the event names on the doorstep.events channel.
func (c doorstepContract) contractEvents(t *testing.T) []string {
	t.Helper()
	ch, ok := c.Channels[DoorstepTopic]
	if !ok {
		t.Fatalf("contract has no %s channel", DoorstepTopic)
	}
	var out []string
	for _, ref := range ch.Publish.Message.OneOf {
		key := strings.TrimPrefix(ref.Ref, "#/components/messages/")
		msg, ok := c.Components.Messages[key]
		if !ok || msg.Name == "" {
			t.Fatalf("contract message %q has no name", ref.Ref)
		}
		out = append(out, msg.Name)
	}
	sort.Strings(out)
	return out
}

func (c doorstepContract) pushTypes() map[string]doorstepContractPush {
	out := map[string]doorstepContractPush{}
	for k, v := range c.XPush.Momentum {
		out[k] = v
	}
	for k, v := range c.XPush.Pro {
		out[k] = v
	}
	return out
}

// The registry is the contract, in both directions: the same push types,
// each from the same event, to the same app, with the same data keys, deep
// link template, condition presence and priority; and every contract event
// is handled (pushed, paged, alerted or deliberately silent).
func TestDoorstepRegistryMatchesContract(t *testing.T) {
	c := loadDoorstepContract(t)
	if len(c.XPush.Momentum) == 0 || len(c.XPush.Pro) == 0 {
		t.Fatal("contract x-push-types not parsed")
	}

	rules := map[string]doorstepRule{}
	for _, r := range doorstepRules {
		if _, dup := rules[r.pushType]; dup {
			t.Fatalf("two rules for %s", r.pushType)
		}
		rules[r.pushType] = r
	}
	contract := c.pushTypes()
	var contractTypes []string
	for k := range contract {
		contractTypes = append(contractTypes, k)
	}
	sort.Strings(contractTypes)
	if got := service.DoorstepPushTypes(); !reflect.DeepEqual(got, contractTypes) {
		t.Fatalf("service registry types\n got %v\nwant %v", got, contractTypes)
	}
	if len(rules) != len(contract) {
		t.Fatalf("%d rules for %d contract push types", len(rules), len(contract))
	}

	for typ, want := range contract {
		r, ok := rules[typ]
		if !ok {
			t.Errorf("%s: no consumer rule", typ)
			continue
		}
		spec, _ := service.DoorstepPushSpecFor(typ)
		wantApp, wantAudience := service.AppMomentum, doorstepToCustomer
		if _, pro := c.XPush.Pro[typ]; pro {
			wantApp, wantAudience = service.AppDoorstepPro, doorstepToPro
		}
		if spec.App != wantApp || r.audience != wantAudience {
			t.Errorf("%s: app %q audience %v, want %q %v", typ, spec.App, r.audience, wantApp, wantAudience)
		}
		if r.fromEvent != want.FromEvent {
			t.Errorf("%s: from %q, contract says %q", typ, r.fromEvent, want.FromEvent)
		}
		if !reflect.DeepEqual(spec.DataKeys, want.Data) {
			t.Errorf("%s: data keys %v, contract says %v", typ, spec.DataKeys, want.Data)
		}
		if r.deeplink != want.Deeplink {
			t.Errorf("%s: deep link %q, contract says %q", typ, r.deeplink, want.Deeplink)
		}
		if (r.when != nil) != (want.When != "") {
			t.Errorf("%s: condition present=%v, contract when=%q", typ, r.when != nil, want.When)
		}
		msg := push.BuildFCMMessage("tok", "T", "B", map[string]string{"type": typ})
		android, _ := msg["android"].(map[string]interface{})
		if high := android["priority"] == "high"; high != (want.Priority == "high") {
			t.Errorf("%s: FCM high priority %v, contract priority %q", typ, high, want.Priority)
		}
	}

	events := c.contractEvents(t)
	if len(events) != 31 {
		t.Fatalf("contract lists %d events; this test was written against 31 — re-check the registry", len(events))
	}
	for _, e := range events {
		if !doorstepKnownEvents[e] {
			t.Errorf("contract event %s is not handled", e)
		}
	}
	for e := range doorstepKnownEvents {
		found := false
		for _, ce := range events {
			found = found || ce == e
		}
		if !found {
			t.Errorf("handled event %s is not in the contract", e)
		}
	}
}

// ---------------------------------------------------------------------------
// Fixtures.

type fakeDoorstepDeliverer struct {
	pushes []service.DoorstepPush
	err    error
}

func (f *fakeDoorstepDeliverer) DeliverDoorstepPush(_ context.Context, p service.DoorstepPush) (service.DoorstepPushOutcome, error) {
	if f.err != nil {
		return "", f.err
	}
	f.pushes = append(f.pushes, p)
	return service.DoorstepPushSent, nil
}

var (
	dsBooking    = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	dsCustomer   = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	dsPro        = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	dsOffer      = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	dsProRecord  = uuid.MustParse("55555555-5555-4555-8555-555555555555")
	dsDocument   = uuid.MustParse("66666666-6666-4666-8666-666666666666")
	dsSettlement = uuid.MustParse("77777777-7777-4777-8777-777777777777")
	dsIncident   = uuid.MustParse("88888888-8888-4888-8888-888888888888")
	dsResponder  = uuid.MustParse("99999999-9999-4999-8999-999999999999")
	dsNow        = time.Date(2026, 10, 4, 6, 30, 0, 0, time.UTC)
)

// Values that must never appear in a push. The contract says events carry
// none of them; the fixtures carry them anyway so the copy and the guard are
// both exercised.
var dsPII = map[string]any{
	"address_line1":   "Flat 302, Sunrise Apartments",
	"customer_phone":  "+91 98765 43210",
	"start_otp":       "4821",
	"document_number": "ABCD1234567",
	"landmark":        "Opposite Cyber Towers",
	"latitude":        17.4483,
	"longitude":       78.3915,
}

// dsData is a payload carrying every field any Doorstep push may use.
func dsData() map[string]any {
	d := map[string]any{
		"booking_id":          dsBooking.String(),
		"customer_user_id":    dsCustomer.String(),
		"pro_user_id":         dsPro.String(),
		"status":              "confirmed",
		"city_code":           "HYD",
		"category_slug":       "home-cleaning",
		"service_id":          "a0000000-0000-4000-8000-00000000000a",
		"slot_start":          "2026-10-04T10:00:00Z",
		"slot_end":            "2026-10-04T12:00:00Z",
		"previous_slot_start": "2026-10-04T09:00:00Z",
		"pro_first_name":      "Ravi",
		"assignment_id":       "a0000000-0000-4000-8000-00000000000b",
		"eta_minutes":         12,
		"extra_id":            "a0000000-0000-4000-8000-00000000000c",
		"name":                "Tap replacement",
		"total_paise":         45000,
		"bill_id":             "a0000000-0000-4000-8000-00000000000d",
		"amount_paise":        123456,
		"due_at":              "2026-10-04T12:15:00Z",
		"refund_paise":        30000,
		"fee_paise":           0,
		"cancelled_by":        "customer",
		"decision":            "approved",
		"reason":              "plans changed",
		"message_id":          "a0000000-0000-4000-8000-00000000000e",
		"rework_id":           "a0000000-0000-4000-8000-00000000000f",
		"stars":               5,
		"offer_id":            dsOffer.String(),
		"expires_at":          dsNow.Add(5 * time.Minute).Format(time.RFC3339),
		"locality":            "Madhapur",
		"pro_id":              dsProRecord.String(),
		"document_id":         dsDocument.String(),
		"kind":                "police_certificate",
		"valid_until":         "2027-01-02",
		"settlement_id":       dsSettlement.String(),
		"net_paise":           250000,
		"period_start":        "2026-09-28",
		"period_end":          "2026-10-04",
	}
	for k, v := range dsPII {
		d[k] = v
	}
	return d
}

func dsMessage(t *testing.T, eventType, eventID string, data map[string]any, envelopeIDs bool) kafka.Message {
	t.Helper()
	env := map[string]any{
		"event_id":    eventID,
		"event_type":  eventType,
		"version":     1,
		"occurred_at": dsNow.Format(time.RFC3339),
		"data":        data,
	}
	if envelopeIDs {
		for _, k := range []string{"booking_id", "customer_user_id", "pro_user_id"} {
			if v, ok := data[k]; ok {
				env[k] = v
			}
		}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{
		Key:     []byte(dsBooking.String()),
		Value:   raw,
		Headers: []kafka.Header{{Key: "event_type", Value: []byte(eventType)}},
	}
}

func newTestDoorstepConsumer() (*DoorstepConsumer, *fakeDoorstepDeliverer) {
	d := &fakeDoorstepDeliverer{}
	c := newDoorstepConsumer(d)
	c.now = func() time.Time { return dsNow }
	return c, d
}

// dsConditionOverrides makes each conditional push type's `when` true.
var dsConditionOverrides = map[string]map[string]any{
	service.DoorstepTypeBookingProNoShow:       {"party": "pro"},
	service.DoorstepTypeMessageNew:             {"sender_kind": "pro"},
	service.DoorstepTypeProMessageNew:          {"sender_kind": "customer"},
	service.DoorstepTypeProOfferExpired:        {"outcome": "expired"},
	service.DoorstepTypeProJobCancelled:        {},
	service.DoorstepTypeProJobRescheduled:      {},
	service.DoorstepTypeProJobReminder:         {},
	service.DoorstepTypeProExtrasApproved:      {"decision": "approved"},
	service.DoorstepTypeProExtrasDeclined:      {"decision": "declined"},
	service.DoorstepTypeProApplicationApproved: {"from_status": "pending_verification", "to_status": "approved"},
	service.DoorstepTypeProApplicationRejected: {"from_status": "pending_verification", "to_status": "rejected"},
	service.DoorstepTypeProAccountSuspended:    {"from_status": "approved", "to_status": "suspended"},
	service.DoorstepTypeProAccountReinstated:   {"from_status": "suspended", "to_status": "approved"},
	service.DoorstepTypeProRatingReceived:      {"rater_kind": "customer"},
}

func dsRender(template string) string {
	r := strings.NewReplacer("{booking_id}", dsBooking.String(), "{offer_id}", dsOffer.String())
	return strings.TrimPrefix(r.Replace(template), "momentum:/")
}

func dsLeaksPII(t *testing.T, p service.DoorstepPush) {
	t.Helper()
	all := []string{p.Title, p.Body, p.DeepLink}
	for _, v := range service.DoorstepPushData(p) {
		all = append(all, v)
	}
	text := strings.ToLower(strings.Join(all, " | "))
	for _, needle := range []string{"sunrise", "302", "98765", "43210", "4821", "abcd1234567", "cyber towers", "17.44", "78.39", "madhapur"} {
		if strings.Contains(text, needle) {
			t.Errorf("%s carries %q: %s", p.Type, needle, text)
		}
	}
}

// Every push type in the contract registry is produced from its source
// event, for the right person, on the right app, with the contract's data
// keys and deep link, plain copy and no PII.
func TestDoorstepEveryRegistryPushTypeIsDelivered(t *testing.T) {
	c := loadDoorstepContract(t)
	for typ, want := range c.pushTypes() {
		t.Run(typ, func(t *testing.T) {
			override, has := dsConditionOverrides[typ]
			if want.When != "" && !has {
				t.Fatalf("contract condition %q has no fixture override", want.When)
			}
			data := dsData()
			for k, v := range override {
				data[k] = v
			}
			cons, d := newTestDoorstepConsumer()
			cons.processMessage(context.Background(), dsMessage(t, want.FromEvent, uuid.NewString(), data, true))

			var got *service.DoorstepPush
			for i := range d.pushes {
				if d.pushes[i].Type == typ {
					got = &d.pushes[i]
				}
			}
			if got == nil {
				t.Fatalf("no %s push from %s (pushes: %+v)", typ, want.FromEvent, d.pushes)
			}
			wantRecipient, wantApp := dsCustomer, service.AppMomentum
			if _, pro := c.XPush.Pro[typ]; pro {
				wantRecipient, wantApp = dsPro, service.AppDoorstepPro
			}
			if got.RecipientID != wantRecipient || got.App != wantApp {
				t.Fatalf("%s → %s on %s, want %s on %s", typ, got.RecipientID, got.App, wantRecipient, wantApp)
			}
			if got.DeepLink != dsRender(want.Deeplink) {
				t.Fatalf("deep link %q, want %q", got.DeepLink, dsRender(want.Deeplink))
			}
			if wantApp == service.AppMomentum && !strings.HasPrefix(got.DeepLink, "/doorstep/") {
				t.Fatalf("Momentum deep link %q is not an app path", got.DeepLink)
			}
			for _, k := range want.Data {
				if got.Data[k] == "" {
					t.Errorf("data key %s missing: %v", k, got.Data)
				}
			}
			allowed := service.DoorstepPushAllowedDataKeys(typ)
			for k := range service.DoorstepPushData(*got) {
				if !allowed[k] {
					t.Errorf("data key %s is not allowed for %s", k, typ)
				}
			}
			if got.Title == "" || got.Body == "" || got.CollapseKey == "" || got.EntityID == uuid.Nil {
				t.Fatalf("incomplete push %+v", got)
			}
			if !strings.HasPrefix(got.DedupKey, "event:") {
				t.Fatalf("dedup key %q is not the event id", got.DedupKey)
			}
			if n := cons.countOf(doorstepOutcomePIIBlocked); n != 0 {
				t.Fatalf("%d pushes blocked by the PII guard", n)
			}
			for _, p := range d.pushes {
				dsLeaksPII(t, p)
			}
		})
	}
}

// Routing: customer-facing types reach the customer's Momentum install and
// pro types the professional's doorstep_pro install — never the other way.
func TestDoorstepRoutingCustomerVersusPro(t *testing.T) {
	type sent struct {
		typ, app string
		to       uuid.UUID
	}
	cases := []struct {
		name      string
		event     string
		overrides map[string]any
		drop      []string
		want      []sent
	}{
		{"pro writes, customer hears", doorstepEventBookingMessageSent, map[string]any{"sender_kind": "pro"}, nil,
			[]sent{{service.DoorstepTypeMessageNew, service.AppMomentum, dsCustomer}}},
		{"customer writes, pro hears", doorstepEventBookingMessageSent, map[string]any{"sender_kind": "customer"}, nil,
			[]sent{{service.DoorstepTypeProMessageNew, service.AppDoorstepPro, dsPro}}},
		{"reminder reaches both sides", doorstepEventBookingReminder, nil, nil,
			[]sent{{service.DoorstepTypeBookingReminder, service.AppMomentum, dsCustomer}, {service.DoorstepTypeProJobReminder, service.AppDoorstepPro, dsPro}}},
		{"cancelled with a pro reaches both", doorstepEventBookingCancelled, nil, nil,
			[]sent{{service.DoorstepTypeBookingCancelled, service.AppMomentum, dsCustomer}, {service.DoorstepTypeProJobCancelled, service.AppDoorstepPro, dsPro}}},
		{"cancelled before assignment reaches the customer only", doorstepEventBookingCancelled, nil, []string{"pro_user_id"},
			[]sent{{service.DoorstepTypeBookingCancelled, service.AppMomentum, dsCustomer}}},
		{"rescheduled reaches the pro only", doorstepEventBookingRescheduled, nil, nil,
			[]sent{{service.DoorstepTypeProJobRescheduled, service.AppDoorstepPro, dsPro}}},
		{"assigned reaches the customer only", doorstepEventBookingAssigned, nil, nil,
			[]sent{{service.DoorstepTypeBookingAssigned, service.AppMomentum, dsCustomer}}},
		{"offer reaches the pro only", doorstepEventProOfferCreated, nil, nil,
			[]sent{{service.DoorstepTypeProOfferNew, service.AppDoorstepPro, dsPro}}},
		{"approval reaches the pro", doorstepEventProStatusChanged, map[string]any{"from_status": "pending_verification", "to_status": "approved"}, nil,
			[]sent{{service.DoorstepTypeProApplicationApproved, service.AppDoorstepPro, dsPro}}},
		// Conditions that are false push nothing.
		{"customer no-show is not pushed", doorstepEventBookingNoShow, map[string]any{"party": "customer"}, nil, nil},
		{"accepted offer is not pushed", doorstepEventProOfferClosed, map[string]any{"outcome": "accepted"}, nil, nil},
		{"pro rating a customer is not pushed", doorstepEventBookingRated, map[string]any{"rater_kind": "pro"}, nil, nil},
		{"draft to pending is not pushed", doorstepEventProStatusChanged, map[string]any{"from_status": "draft", "to_status": "pending_verification"}, nil, nil},
		{"blocked is not pushed", doorstepEventProStatusChanged, map[string]any{"from_status": "approved", "to_status": "blocked"}, nil, nil},
		{"created is silent", doorstepEventBookingCreated, nil, nil, nil},
		{"applied is silent", doorstepEventProApplied, nil, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := dsData()
			for k, v := range tc.overrides {
				data[k] = v
			}
			for _, k := range tc.drop {
				delete(data, k)
			}
			cons, d := newTestDoorstepConsumer()
			cons.processMessage(context.Background(), dsMessage(t, tc.event, uuid.NewString(), data, true))
			var got []sent
			for _, p := range d.pushes {
				got = append(got, sent{p.Type, p.App, p.RecipientID})
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("pushes\n got %+v\nwant %+v", got, tc.want)
			}
			if cons.countOf(doorstepOutcomeMissingRecipient) != 0 {
				t.Fatal("a skipped condition was counted as a missing recipient")
			}
		})
	}
}

// The envelope ids are the routing source: a payload whose data lacks the
// ids still routes on the envelope.
func TestDoorstepEnvelopeIDsRoute(t *testing.T) {
	data := dsData()
	delete(data, "customer_user_id")
	delete(data, "pro_user_id")
	msg := dsMessage(t, doorstepEventBookingReminder, uuid.NewString(), data, false)
	var env map[string]any
	_ = json.Unmarshal(msg.Value, &env)
	env["customer_user_id"] = dsCustomer.String()
	env["pro_user_id"] = dsPro.String()
	msg.Value, _ = json.Marshal(env)

	cons, d := newTestDoorstepConsumer()
	cons.processMessage(context.Background(), msg)
	if len(d.pushes) != 2 || d.pushes[0].RecipientID != dsCustomer || d.pushes[1].RecipientID != dsPro {
		t.Fatalf("pushes = %+v", d.pushes)
	}
}

func TestDoorstepMissingRecipientIsCountedNotSent(t *testing.T) {
	data := dsData()
	delete(data, "customer_user_id")
	cons, d := newTestDoorstepConsumer()
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventBookingConfirmed, uuid.NewString(), data, true))
	if len(d.pushes) != 0 {
		t.Fatalf("pushed without a recipient: %+v", d.pushes)
	}
	if cons.countOf(doorstepOutcomeMissingRecipient) != 1 {
		t.Fatalf("missing_recipient = %d", cons.countOf(doorstepOutcomeMissingRecipient))
	}
}

// Idempotency is keyed on the event id: a redelivered message plans pushes
// under the same dedup key (the service claims it once), a different event
// under a different one, and an event without an id hashes its bytes.
func TestDoorstepDedupKeyIsTheEventID(t *testing.T) {
	id := uuid.NewString()
	msg := dsMessage(t, doorstepEventBookingConfirmed, id, dsData(), true)
	cons, d := newTestDoorstepConsumer()
	cons.processMessage(context.Background(), msg)
	cons.processMessage(context.Background(), msg)
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventBookingConfirmed, uuid.NewString(), dsData(), true))
	if len(d.pushes) != 3 {
		t.Fatalf("pushes = %d", len(d.pushes))
	}
	if d.pushes[0].DedupKey != "event:"+id || d.pushes[1].DedupKey != d.pushes[0].DedupKey {
		t.Fatalf("redelivery keys %q / %q", d.pushes[0].DedupKey, d.pushes[1].DedupKey)
	}
	if d.pushes[2].DedupKey == d.pushes[0].DedupKey {
		t.Fatal("two events share a dedup key")
	}
	if service.DoorstepDedupID(d.pushes[2]) == service.DoorstepDedupID(d.pushes[0]) {
		t.Fatal("two events map to one dedup claim")
	}
	if service.DoorstepDedupID(d.pushes[0]) != service.DoorstepDedupID(d.pushes[1]) {
		t.Fatal("a redelivery maps to a different dedup claim")
	}

	noID := dsMessage(t, doorstepEventBookingConfirmed, "", dsData(), true)
	ev, _ := decodeDoorstepMessage(noID)
	a, b := doorstepDedupBase(ev, noID), doorstepDedupBase(ev, noID)
	if !strings.HasPrefix(a, "sha256:") || a != b {
		t.Fatalf("id-less dedup base %q / %q", a, b)
	}
}

// A redelivered message reaches the real delivery path twice and is sent
// once: the duplicate is refused by the dedup claim (service test
// TestDoorstepPush_DuplicateIsSentOnce pins the claim itself).
func TestDoorstepDuplicateEventIgnoredEndToEnd(t *testing.T) {
	claimed := map[uuid.UUID]bool{}
	sends := 0
	deliverer := doorstepDelivererFunc(func(p service.DoorstepPush) service.DoorstepPushOutcome {
		id := service.DoorstepDedupID(p)
		if claimed[id] {
			return service.DoorstepPushDuplicate
		}
		claimed[id] = true
		sends++
		return service.DoorstepPushSent
	})
	cons := newDoorstepConsumer(deliverer)
	cons.now = func() time.Time { return dsNow }
	msg := dsMessage(t, doorstepEventBookingReminder, uuid.NewString(), dsData(), true)
	cons.processMessage(context.Background(), msg)
	cons.processMessage(context.Background(), msg)
	if sends != 2 { // customer + pro, once each
		t.Fatalf("sends = %d, want 2 (one per side, the redelivery sends nothing)", sends)
	}
}

type doorstepDelivererFunc func(p service.DoorstepPush) service.DoorstepPushOutcome

func (f doorstepDelivererFunc) DeliverDoorstepPush(_ context.Context, p service.DoorstepPush) (service.DoorstepPushOutcome, error) {
	return f(p), nil
}

func TestDoorstepOfferTTLAndExpiry(t *testing.T) {
	cons, d := newTestDoorstepConsumer()
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventProOfferCreated, uuid.NewString(), dsData(), true))
	if len(d.pushes) != 1 || d.pushes[0].TTL != 5*time.Minute {
		t.Fatalf("offer push = %+v", d.pushes)
	}
	if got := service.DoorstepPushData(d.pushes[0])[push.AndroidTTLDataKey]; got != "300s" {
		t.Fatalf("ttl data = %q", got)
	}

	data := dsData()
	data["expires_at"] = dsNow.Add(-time.Second).Format(time.RFC3339)
	cons, d = newTestDoorstepConsumer()
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventProOfferCreated, uuid.NewString(), data, true))
	if len(d.pushes) != 0 || cons.countOf(doorstepOutcomeOfferExpired) != 1 {
		t.Fatalf("lapsed offer pushed: %+v", d.pushes)
	}
}

// The last-line guard drops a push whose words repeat a sensitive value of
// its event, even if the copy was never meant to.
func TestDoorstepPIIGuardBlocksALeak(t *testing.T) {
	data := dsData()
	// A landmark that happens to equal the professional's first name: the
	// assigned copy would print it, so the push must be dropped.
	data["pro_first_name"] = "Ravinder"
	data["landmark"] = "Ravinder"
	cons, d := newTestDoorstepConsumer()
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventBookingAssigned, uuid.NewString(), data, true))
	if len(d.pushes) != 0 || cons.countOf(doorstepOutcomePIIBlocked) != 1 {
		t.Fatalf("leaking push delivered: %+v (blocked %d)", d.pushes, cons.countOf(doorstepOutcomePIIBlocked))
	}

	raw, _ := json.Marshal(map[string]any{"data": map[string]any{"customer_phone": "+91 98765 43210", "start_otp": "4821"}})
	s := doorstepSensitiveValues(raw)
	base := service.DoorstepPush{Type: service.DoorstepTypeBookingStarted, Title: "Service started", Body: "In progress.", DeepLink: "/doorstep/bookings/x"}
	if doorstepPushLeaks(base, s) {
		t.Fatal("clean push flagged")
	}
	for _, body := range []string{"Call 9876543210", "Your code is 4821", "+91-98765-43210"} {
		p := base
		p.Body = body
		if !doorstepPushLeaks(p, s) {
			t.Errorf("leak not caught: %q", body)
		}
	}
	p := base
	p.Data = map[string]string{"customer_phone": "x"}
	if !doorstepPushLeaks(p, s) {
		t.Error("sensitive data key not caught")
	}
}

func TestDoorstepMalformedAndUnknownAreCounted(t *testing.T) {
	cons, d := newTestDoorstepConsumer()
	ctx := context.Background()
	cons.processMessage(ctx, kafka.Message{Value: []byte("not json")})
	cons.processMessage(ctx, kafka.Message{Value: []byte(`{"data":{}}`)})
	mismatch := dsMessage(t, doorstepEventBookingConfirmed, uuid.NewString(), dsData(), true)
	mismatch.Headers = []kafka.Header{{Key: "event_type", Value: []byte(doorstepEventBookingCompleted)}}
	cons.processMessage(ctx, mismatch)
	bad := dsData()
	bad["booking_id"] = "not-a-uuid"
	cons.processMessage(ctx, dsMessage(t, doorstepEventBookingConfirmed, uuid.NewString(), bad, false))
	if n := cons.countOf(doorstepOutcomeMalformed); n != 4 {
		t.Fatalf("malformed = %d, want 4", n)
	}
	cons.processMessage(ctx, dsMessage(t, "doorstep.booking.teleported", uuid.NewString(), dsData(), true))
	if cons.countOf(doorstepOutcomeUnknown) != 1 {
		t.Fatal("unknown event not counted")
	}
	if len(d.pushes) != 0 {
		t.Fatalf("pushed from a bad message: %+v", d.pushes)
	}

	// A bare payload with the type only in the header is accepted.
	raw, _ := json.Marshal(dsData())
	cons.processMessage(ctx, kafka.Message{Value: raw, Headers: []kafka.Header{{Key: "event_type", Value: []byte(doorstepEventBookingStarted)}}})
	if len(d.pushes) != 1 || d.pushes[0].Type != service.DoorstepTypeBookingStarted || !strings.HasPrefix(d.pushes[0].DedupKey, "sha256:") {
		t.Fatalf("bare payload pushes = %+v", d.pushes)
	}
}

// ---------------------------------------------------------------------------
// Incidents.

type fakeDoorstepSafety struct {
	claimed    map[uuid.UUID]bool
	claimErr   error
	responders []uuid.UUID
	alerts     []service.SafetyAlert
	ops        []postgres.OpsAlert
	emails     []string
}

func newFakeDoorstepSafety(responders ...uuid.UUID) *fakeDoorstepSafety {
	return &fakeDoorstepSafety{claimed: map[uuid.UUID]bool{}, responders: responders}
}

func (f *fakeDoorstepSafety) ClaimEventDedup(_ context.Context, id uuid.UUID) (bool, error) {
	if f.claimErr != nil {
		return false, f.claimErr
	}
	if f.claimed[id] {
		return false, nil
	}
	f.claimed[id] = true
	return true, nil
}
func (f *fakeDoorstepSafety) Responders(context.Context) []uuid.UUID { return f.responders }
func (f *fakeDoorstepSafety) SendSafetyAlert(_ context.Context, a service.SafetyAlert) error {
	f.alerts = append(f.alerts, a)
	return nil
}
func (f *fakeDoorstepSafety) RecordOpsAlert(_ context.Context, a postgres.OpsAlert) error {
	f.ops = append(f.ops, a)
	return nil
}
func (f *fakeDoorstepSafety) EmailOps(_ context.Context, subject, body string) error {
	f.emails = append(f.emails, subject+"\n"+body)
	return nil
}

func dsIncidentData(kind, severity, raisedBy string) map[string]any {
	d := map[string]any{
		"incident_id":        dsIncident.String(),
		"booking_id":         dsBooking.String(),
		"customer_user_id":   dsCustomer.String(),
		"pro_user_id":        dsPro.String(),
		"kind":               kind,
		"severity":           severity,
		"raised_by_kind":     raisedBy,
		"pro_auto_suspended": true,
	}
	for k, v := range dsPII {
		d[k] = v
	}
	return d
}

func TestDoorstepIncidentPagesResponders(t *testing.T) {
	safety := newFakeDoorstepSafety(dsResponder, dsCustomer) // a party is never its own responder
	cons, d := newTestDoorstepConsumer()
	cons.WithSafety(safety)
	msg := dsMessage(t, doorstepEventIncidentRaised, uuid.NewString(), dsIncidentData("sos", "critical", "customer"), true)
	cons.processMessage(context.Background(), msg)

	if len(safety.alerts) != 1 {
		t.Fatalf("alerts = %+v", safety.alerts)
	}
	a := safety.alerts[0]
	if a.Recipient != dsResponder || a.NotifType != NotifDoorstepIncidentResponder || a.EntityID != dsIncident ||
		a.DeepLink != "/admin/doorstep/incidents/"+dsIncident.String() ||
		a.Identity != "doorstep_incident:"+dsIncident.String()+":responder:"+dsResponder.String() {
		t.Fatalf("page = %+v", a)
	}
	if a.Title != "Doorstep safety alert" || !strings.Contains(a.Body, "A customer raised an SOS (critical)") {
		t.Fatalf("copy = %q / %q", a.Title, a.Body)
	}
	dsLeaksPII(t, service.DoorstepPush{Type: service.DoorstepTypeBookingStarted, Title: a.Title, Body: a.Body, DeepLink: a.DeepLink})
	if len(safety.ops) != 1 || safety.ops[0].Kind != OpsAlertDoorstepIncidentPaged || safety.ops[0].Severity != "critical" ||
		safety.ops[0].SubjectID != dsIncident {
		t.Fatalf("ops = %+v", safety.ops)
	}
	for k := range safety.ops[0].Detail {
		if strings.Contains(k, "user") || strings.Contains(k, "address") || strings.Contains(k, "phone") {
			t.Fatalf("ops alert detail carries %s", k)
		}
	}
	if len(safety.emails) != 1 || strings.Contains(safety.emails[0], "Sunrise") || strings.Contains(safety.emails[0], "98765") {
		t.Fatalf("emails = %v", safety.emails)
	}
	if len(d.pushes) != 0 {
		t.Fatalf("an incident produced customer/pro pushes: %+v", d.pushes)
	}

	// Redelivery pages nobody again.
	cons.processMessage(context.Background(), msg)
	if len(safety.alerts) != 1 || len(safety.ops) != 1 || len(safety.emails) != 1 {
		t.Fatalf("duplicate incident handled again: alerts %d ops %d emails %d", len(safety.alerts), len(safety.ops), len(safety.emails))
	}
}

func TestDoorstepIncidentWithoutRespondersRaisesCriticalAlert(t *testing.T) {
	safety := newFakeDoorstepSafety()
	cons, _ := newTestDoorstepConsumer()
	cons.WithSafety(safety)
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventIncidentRaised, uuid.NewString(), dsIncidentData("unsafe_exit", "high", "pro"), true))
	if len(safety.ops) != 1 || safety.ops[0].Kind != OpsAlertDoorstepIncidentNoResponder || safety.ops[0].Severity != "critical" {
		t.Fatalf("ops = %+v", safety.ops)
	}
	if len(safety.emails) != 1 || !strings.HasPrefix(safety.emails[0], "UNPAGED") {
		t.Fatalf("emails = %v", safety.emails)
	}
}

func TestDoorstepLowSeverityIncidentIsRecordedNotPaged(t *testing.T) {
	safety := newFakeDoorstepSafety(dsResponder)
	cons, _ := newTestDoorstepConsumer()
	cons.WithSafety(safety)
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventIncidentRaised, uuid.NewString(), dsIncidentData("damage", "low", "customer"), true))
	if len(safety.alerts) != 0 || len(safety.emails) != 0 {
		t.Fatalf("low damage report paged: %+v", safety.alerts)
	}
	if len(safety.ops) != 1 || safety.ops[0].Kind != OpsAlertDoorstepIncidentRaised || safety.ops[0].Severity != "warning" {
		t.Fatalf("ops = %+v", safety.ops)
	}
}

func TestDoorstepIncidentWithoutSafetyWiringDoesNotPanic(t *testing.T) {
	cons, _ := newTestDoorstepConsumer()
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventIncidentRaised, uuid.NewString(), dsIncidentData("sos", "critical", "pro"), true))
	if cons.countOf(doorstepOutcomePanic) != 0 || cons.countOf(doorstepOutcomeIncident) != 1 {
		t.Fatal("incident without wiring mishandled")
	}
}

func TestDoorstepUnassignedAlertBecomesOpsAlert(t *testing.T) {
	safety := newFakeDoorstepSafety()
	cons, d := newTestDoorstepConsumer()
	cons.WithSafety(safety)
	data := dsData()
	data["minutes_to_slot"] = 120
	cons.processMessage(context.Background(), dsMessage(t, doorstepEventBookingUnassignedAlert, uuid.NewString(), data, true))
	if len(safety.ops) != 1 || safety.ops[0].Kind != OpsAlertDoorstepBookingUnassigned || safety.ops[0].SubjectID != dsBooking ||
		safety.ops[0].DedupeKey != "doorstep_unassigned:"+dsBooking.String() {
		t.Fatalf("ops = %+v", safety.ops)
	}
	if len(d.pushes) != 0 {
		t.Fatalf("unassigned alert pushed: %+v", d.pushes)
	}
}

// ---------------------------------------------------------------------------
// Copy helpers.

func TestDoorstepCopyHelpers(t *testing.T) {
	for paise, want := range map[int64]string{
		0: "₹0", 100: "₹1", 45000: "₹450", 123456: "₹1,234.56", 9876500: "₹98,765",
		12345678900: "₹12,34,56,789", 5: "₹0.05",
	} {
		if got := doorstepRupees(paise); got != want {
			t.Errorf("rupees(%d) = %q, want %q", paise, got, want)
		}
	}
	if got := doorstepClock("2026-10-04T10:00:00Z"); got != "3:30 PM" {
		t.Errorf("clock = %q (IST)", got)
	}
	if got := doorstepDayClock("2026-10-04T10:00:00Z"); got != "Sun 4 Oct, 3:30 PM" {
		t.Errorf("day clock = %q", got)
	}
	if got := doorstepDate("2027-01-02"); got != "2 Jan 2027" {
		t.Errorf("date = %q", got)
	}
	for raw, want := range map[string]string{"Ravi": "Ravi", "Ravi Kumar": "Ravi", "9876543210": "", "": "", "a@b.c": ""} {
		if got := doorstepFirstName(raw); got != want {
			t.Errorf("first name %q = %q, want %q", raw, got, want)
		}
	}
}
