package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

type fakePulseNotifier struct {
	alerts   []service.SafetyAlert
	checkins []service.DatingCheckinNotification
}

func (f *fakePulseNotifier) CreateSafetyAlertNotification(_ context.Context, a service.SafetyAlert) error {
	f.alerts = append(f.alerts, a)
	return nil
}

func (f *fakePulseNotifier) CreateDatingCheckinNotification(_ context.Context, n service.DatingCheckinNotification) error {
	f.checkins = append(f.checkins, n)
	return nil
}

func pulseEnvelope(t *testing.T, eventType string, payload any) events.EventEnvelope {
	t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return events.EventEnvelope{EventType: eventType, Payload: b}
}

func dispatchPulse(t *testing.T, f *fakePulseNotifier, env events.EventEnvelope) error {
	t.Helper()
	claimed, err := (&Consumer{datingNotify: f}).handleDatingEvent(context.Background(), env)
	if !claimed {
		t.Fatalf("%s was not claimed by the dating consumer", env.EventType)
	}
	return err
}

func scamPayload(recipient, match, name string) map[string]any {
	return map[string]any{
		"recipient_id": recipient, "match_id": match,
		"removed_first_name": name, "issued_at": "2026-10-02T10:00:00Z",
	}
}

func checkinPayload(recipient, match, meet, name string) map[string]any {
	return map[string]any{
		"recipient_id": recipient, "match_id": match, "meet_id": meet,
		"with_first_name": name, "due_at": "2026-10-02T21:00:00Z",
	}
}

const scamWarning = "Never send money, gift cards or codes to anyone you met here."

func TestDatingScamAlertIsAnActorlessSafetyAlert(t *testing.T) {
	recipient, match := uuid.New(), uuid.New()
	f := &fakePulseNotifier{}
	if err := dispatchPulse(t, f, pulseEnvelope(t, events.EventDatingScamAlert,
		scamPayload(recipient.String(), match.String(), "Ravi"))); err != nil {
		t.Fatalf("scam alert: %v", err)
	}
	if len(f.alerts) != 1 || len(f.checkins) != 0 {
		t.Fatalf("alerts=%d checkins=%d, want exactly one safety alert", len(f.alerts), len(f.checkins))
	}
	a := f.alerts[0]
	if a.NotifType != "dating_scam_alert" || a.EntityType != "dating_match" || a.EntityID != match {
		t.Fatalf("notification = %s %s/%s", a.NotifType, a.EntityType, a.EntityID)
	}
	if a.Recipient != recipient {
		t.Fatalf("recipient = %s, want %s", a.Recipient, recipient)
	}
	if a.Actor != uuid.Nil {
		t.Fatalf("actor = %s, the removed person must never be the actor", a.Actor)
	}
	if a.DeepLink != "/dating/safety" {
		t.Fatalf("deep link = %q", a.DeepLink)
	}
	if a.Title != "Safety notice" {
		t.Fatalf("title = %q", a.Title)
	}
	if want := "Ravi, who you matched with on Pulse, was removed for scam behaviour. " + scamWarning; a.Body != want {
		t.Fatalf("body = %q\nwant %q", a.Body, want)
	}
	if want := "dating_scam_alert:" + match.String() + ":" + recipient.String(); a.Identity != want {
		t.Fatalf("identity = %q, want %q", a.Identity, want)
	}
	if want := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC); !a.CreatedAt.Equal(want) {
		t.Fatalf("created at = %s, want issued_at", a.CreatedAt)
	}
}

func TestDatingScamAlertCopyWithoutAName(t *testing.T) {
	want := "Someone you matched with on Pulse was removed for scam behaviour. " + scamWarning
	for _, name := range []string{"", "   ", "\t\n"} {
		f := &fakePulseNotifier{}
		if err := dispatchPulse(t, f, pulseEnvelope(t, events.EventDatingScamAlert,
			scamPayload(uuid.NewString(), uuid.NewString(), name))); err != nil {
			t.Fatalf("name %q: %v", name, err)
		}
		if len(f.alerts) != 1 || f.alerts[0].Body != want || f.alerts[0].Title != "Safety notice" {
			t.Fatalf("name %q: got %+v", name, f.alerts)
		}
	}
	// The field may be missing altogether.
	f := &fakePulseNotifier{}
	raw := `{"recipient_id":"` + uuid.NewString() + `","match_id":"` + uuid.NewString() + `"}`
	if err := dispatchPulse(t, f, events.EventEnvelope{EventType: events.EventDatingScamAlert, Payload: json.RawMessage(raw)}); err != nil {
		t.Fatalf("absent name: %v", err)
	}
	if f.alerts[0].Body != want || f.alerts[0].CreatedAt.IsZero() {
		t.Fatalf("absent name: %+v", f.alerts[0])
	}
}

func TestDatingPulseNamesAreTrimmedAndCapped(t *testing.T) {
	long := strings.Repeat("é", 60)
	capped := strings.Repeat("é", 40)

	f := &fakePulseNotifier{}
	if err := dispatchPulse(t, f, pulseEnvelope(t, events.EventDatingScamAlert,
		scamPayload(uuid.NewString(), uuid.NewString(), "  Ravi  "))); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.alerts[0].Body, "Ravi, who") {
		t.Fatalf("untrimmed name: %q", f.alerts[0].Body)
	}
	if err := dispatchPulse(t, f, pulseEnvelope(t, events.EventDatingScamAlert,
		scamPayload(uuid.NewString(), uuid.NewString(), long))); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.alerts[1].Body, capped+", who") {
		t.Fatalf("name not capped at 40 runes: %q", f.alerts[1].Body)
	}

	if err := dispatchPulse(t, f, pulseEnvelope(t, events.EventDatingDateCheckinDue,
		checkinPayload(uuid.NewString(), uuid.NewString(), "", "  "+long+"  "))); err != nil {
		t.Fatal(err)
	}
	if got := f.checkins[0].FirstName; got != capped {
		t.Fatalf("check-in name = %q, want 40 runes", got)
	}
}

func TestDatingDateCheckinNotification(t *testing.T) {
	recipient, match, meet := uuid.New(), uuid.New(), uuid.New()
	f := &fakePulseNotifier{}
	if err := dispatchPulse(t, f, pulseEnvelope(t, events.EventDatingDateCheckinDue,
		checkinPayload(recipient.String(), match.String(), meet.String(), "Asha"))); err != nil {
		t.Fatalf("check-in: %v", err)
	}
	if len(f.checkins) != 1 || len(f.alerts) != 0 {
		t.Fatalf("checkins=%d alerts=%d; a check-in is not a safety alert", len(f.checkins), len(f.alerts))
	}
	n := f.checkins[0]
	if n.RecipientID != recipient || n.MatchID != match {
		t.Fatalf("ids = %s/%s", n.RecipientID, n.MatchID)
	}
	if want := "/dating/matches/" + match.String() + "?checkin=1"; n.DeepLink != want {
		t.Fatalf("deep link = %q, want %q", n.DeepLink, want)
	}
	if n.FirstName != "Asha" {
		t.Fatalf("first name = %q", n.FirstName)
	}
	if want := "dating_date_checkin:" + match.String() + ":" + meet.String() + ":" + recipient.String(); n.Identity != want {
		t.Fatalf("identity = %q, want %q", n.Identity, want)
	}
	if want := time.Date(2026, 10, 2, 21, 0, 0, 0, time.UTC); !n.CreatedAt.Equal(want) {
		t.Fatalf("created at = %s, want due_at", n.CreatedAt)
	}
	r := service.RenderDatingCheckin(n)
	if r.Title != "How did it go?" || r.Body != "Tell us how your date with Asha went. It takes a moment." {
		t.Fatalf("copy = %q / %q", r.Title, r.Body)
	}
}

func TestDatingDateCheckinWithoutNameOrMeet(t *testing.T) {
	recipient, match := uuid.New(), uuid.New()
	f := &fakePulseNotifier{}
	if err := dispatchPulse(t, f, pulseEnvelope(t, events.EventDatingDateCheckinDue,
		checkinPayload(recipient.String(), match.String(), "", ""))); err != nil {
		t.Fatalf("check-in: %v", err)
	}
	n := f.checkins[0]
	if n.FirstName != "" {
		t.Fatalf("first name = %q", n.FirstName)
	}
	r := service.RenderDatingCheckin(n)
	if r.Title != "How did it go?" || r.Body != "Tell us how your date went. It takes a moment." {
		t.Fatalf("copy = %q / %q", r.Title, r.Body)
	}
	// Without a meet the due time keys the identity, so a second date with
	// the same match is asked about too, and a redelivery is not.
	if want := "dating_date_checkin:" + match.String() + ":2026-10-02T21:00:00Z:" + recipient.String(); n.Identity != want {
		t.Fatalf("identity = %q, want %q", n.Identity, want)
	}
	// A garbage meet id is ignored, not fatal.
	if err := dispatchPulse(t, f, pulseEnvelope(t, events.EventDatingDateCheckinDue,
		checkinPayload(recipient.String(), match.String(), "not-a-uuid", ""))); err != nil {
		t.Fatalf("bad meet id: %v", err)
	}
	if f.checkins[1].Identity != n.Identity {
		t.Fatalf("bad meet id changed the identity: %q", f.checkins[1].Identity)
	}
}

// A payload without a usable recipient or match is a poison message: an
// error for the worker to log and skip, never a delivery, never a panic.
func TestDatingPulseMalformedPayloads(t *testing.T) {
	good := uuid.NewString()
	nilID := uuid.Nil.String()
	cases := []struct {
		name, recipient, match string
	}{
		{"missing recipient", "", good},
		{"invalid recipient", "nope", good},
		{"nil recipient", nilID, good},
		{"missing match", good, ""},
		{"invalid match", good, "nope"},
		{"nil match", good, nilID},
	}
	for _, tc := range cases {
		for _, env := range []events.EventEnvelope{
			pulseEnvelope(t, events.EventDatingScamAlert, scamPayload(tc.recipient, tc.match, "Ravi")),
			pulseEnvelope(t, events.EventDatingDateCheckinDue, checkinPayload(tc.recipient, tc.match, "", "Asha")),
		} {
			f := &fakePulseNotifier{}
			err := dispatchPulse(t, f, env)
			if err == nil {
				t.Fatalf("%s / %s: no error", tc.name, env.EventType)
			}
			if strings.Contains(err.Error(), "Ravi") || strings.Contains(err.Error(), "Asha") {
				t.Fatalf("%s: error carries the first name: %v", tc.name, err)
			}
			if len(f.alerts)+len(f.checkins) != 0 {
				t.Fatalf("%s / %s: delivered a notification", tc.name, env.EventType)
			}
		}
	}
	for _, et := range []string{events.EventDatingScamAlert, events.EventDatingDateCheckinDue} {
		f := &fakePulseNotifier{}
		if err := dispatchPulse(t, f, events.EventEnvelope{EventType: et, Payload: json.RawMessage(`[1,2]`)}); err == nil {
			t.Fatalf("%s: an undecodable payload was accepted", et)
		}
		if len(f.alerts)+len(f.checkins) != 0 {
			t.Fatalf("%s: undecodable payload delivered", et)
		}
	}
}

// Without the notifier wired the consumer logs and claims; it never panics.
func TestDatingPulseWithoutNotifier(t *testing.T) {
	c := &Consumer{}
	for _, env := range []events.EventEnvelope{
		pulseEnvelope(t, events.EventDatingScamAlert, scamPayload(uuid.NewString(), uuid.NewString(), "Ravi")),
		pulseEnvelope(t, events.EventDatingDateCheckinDue, checkinPayload(uuid.NewString(), uuid.NewString(), "", "")),
	} {
		claimed, err := c.handleDatingEvent(context.Background(), env)
		if !claimed || err != nil {
			t.Fatalf("%s: claimed=%v err=%v", env.EventType, claimed, err)
		}
	}
}
