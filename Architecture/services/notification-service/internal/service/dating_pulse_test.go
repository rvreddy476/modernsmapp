package service

import (
	"strings"
	"testing"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestDatingPulseCopy(t *testing.T) {
	const warning = "Never send money, gift cards or codes to anyone you met here."
	cases := []struct {
		name, title, body string
		copy              func(string) (string, string)
	}{
		{"Ravi", "Safety notice", "Ravi, who you matched with on Pulse, was removed for scam behaviour. " + warning, DatingScamAlertCopy},
		{"", "Safety notice", "Someone you matched with on Pulse was removed for scam behaviour. " + warning, DatingScamAlertCopy},
		{"Asha", "How did it go?", "Tell us how your date with Asha went. It takes a moment.", DatingDateCheckinCopy},
		{"", "How did it go?", "Tell us how your date went. It takes a moment.", DatingDateCheckinCopy},
	}
	for _, tc := range cases {
		title, body := tc.copy(tc.name)
		if title != tc.title || body != tc.body {
			t.Errorf("name %q: %q / %q\nwant %q / %q", tc.name, title, body, tc.title, tc.body)
		}
	}
}

func TestCleanDatingFirstName(t *testing.T) {
	cases := map[string]string{
		"  Ravi  ":                       "Ravi",
		"\tAsha\n":                       "Asha",
		"":                               "",
		"   ":                            "",
		strings.Repeat("अ", 41):          strings.Repeat("अ", 40),
		strings.Repeat("a", 39) + " bcd": strings.Repeat("a", 39), // cut lands on the space: trimmed
		"Ra\xffvi":                       "Ravi",
	}
	for in, want := range cases {
		if got := CleanDatingFirstName(in); got != want {
			t.Errorf("CleanDatingFirstName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The generic push copy (no render override) is the anonymous copy, never
// "New Notification".
func TestDatingPulseFallbackPushCopy(t *testing.T) {
	if title, body := notifTitleBody(DatingScamAlertType); title != "Safety notice" ||
		!strings.HasPrefix(body, "Someone you matched with on Pulse") {
		t.Fatalf("scam alert fallback = %q / %q", title, body)
	}
	if title, body := notifTitleBody(DatingDateCheckinType); title != "How did it go?" ||
		body != "Tell us how your date went. It takes a moment." {
		t.Fatalf("check-in fallback = %q / %q", title, body)
	}
}

func everythingOff() *postgres.NotificationPreferences {
	always := "00:00" // start == end: every minute is quiet
	return &postgres.NotificationPreferences{
		UserID: "u", PushEnabled: false, QuietHoursEnabled: true,
		QuietHoursStart: &always, QuietHoursEnd: &always,
	}
}

// A scam alert is safety class: push-eligible, and no master toggle, quiet
// hours, category toggle or mute can hold it back.
func TestDatingScamAlertIsAlwaysPushed(t *testing.T) {
	tpl, ok := Templates[DatingScamAlertType]
	if !ok || !tpl.PushEligible || tpl.Priority != "critical" {
		t.Fatalf("scam alert template = %+v (registered=%v)", tpl, ok)
	}
	if categoryForEvent(DatingScamAlertType) != catAlwaysOn {
		t.Fatal("scam alert must not sit behind a category toggle")
	}
	for _, muted := range []bool{false, true} {
		d := resolveDecision(DatingScamAlertType, everythingOff(), muted)
		if !d.SendPush || !d.CreateInbox || !d.SendWebSocket || d.DeferPush {
			t.Fatalf("muted=%v: %+v, want every channel", muted, d)
		}
	}
}

// A date check-in is ordinary: on by default, silenced by the master push
// toggle and deferred by quiet hours, like other dating engagement pushes.
func TestDatingDateCheckinRespectsPreferences(t *testing.T) {
	if d := resolveDecision(DatingDateCheckinType, postgres.DefaultNotificationPreferences("u"), false); !d.SendPush || !d.CreateInbox {
		t.Fatalf("defaults: %+v", d)
	}
	off := postgres.DefaultNotificationPreferences("u")
	off.PushEnabled = false
	if d := resolveDecision(DatingDateCheckinType, off, false); d.SendPush || !d.CreateInbox {
		t.Fatalf("push off: %+v, want inbox only", d)
	}
	quiet := postgres.DefaultNotificationPreferences("u")
	always := "00:00"
	quiet.QuietHoursEnabled, quiet.QuietHoursStart, quiet.QuietHoursEnd = true, &always, &always
	if d := resolveDecision(DatingDateCheckinType, quiet, false); d.SendPush || !d.DeferPush {
		t.Fatalf("quiet hours: %+v, want deferred", d)
	}
	if Templates[DatingDateCheckinType].Priority == "critical" || Templates[DatingDateCheckinType].OverridePrefs {
		t.Fatal("a date check-in must not override preferences")
	}
}

func TestRenderDatingCheckinPushData(t *testing.T) {
	recipient, match := uuid.New(), uuid.New()
	n := DatingCheckinNotification{RecipientID: recipient, MatchID: match, FirstName: "Asha",
		DeepLink: "/dating/matches/" + match.String() + "?checkin=1"}
	title, body, data := buildPushData(recipient, DatingDateCheckinType, DatingMatchEntityType, match, n.DeepLink, RenderDatingCheckin(n))
	if title != "How did it go?" || body != "Tell us how your date with Asha went. It takes a moment." {
		t.Fatalf("copy = %q / %q", title, body)
	}
	if data["type"] != "dating_date_checkin" || data["entity_type"] != "dating_match" ||
		data["entity_id"] != match.String() || data["deep_link"] != n.DeepLink {
		t.Fatalf("push data = %v", data)
	}
	if want := "dating_date_checkin:" + match.String() + ":" + recipient.String(); data["collapse_key"] != want {
		t.Fatalf("collapse key = %q, want %q", data["collapse_key"], want)
	}
}
