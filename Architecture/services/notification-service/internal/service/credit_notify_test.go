package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Video credits (2026-10-23): both types are registered, sit in the credits
// category, and render the contract copy.

func TestCreditTypesAreRegisteredInTheCreditsCategory(t *testing.T) {
	for _, ty := range CreditNotificationTypes {
		tpl, ok := Templates[ty]
		if !ok {
			t.Errorf("%s not in Templates", ty)
			continue
		}
		if !tpl.PushEligible || tpl.TitleTemplate == "" || tpl.BodyTemplate == "" || tpl.CanAggregate {
			t.Errorf("%s template incomplete: %+v", ty, tpl)
		}
		if categoryForEvent(ty) != catCredits {
			t.Errorf("%s is not in the credits category", ty)
		}
		if title, icon := InboxCopy(ty, "Asha Rao"); title == inboxGenericTitle || icon != InboxIconSocial {
			t.Errorf("%s inbox copy = %q / %q", ty, title, icon)
		}
		if inboxTabForType(ty) != InboxTabMentions {
			t.Errorf("%s not in the mentions tab", ty)
		}
	}
	if got, _ := InboxCopy(CreditTypeRequest, "Asha Rao"); got != "Asha Rao credited you in a video" {
		t.Errorf("request inbox copy = %q", got)
	}
	if got, _ := InboxCopy(CreditTypeAccepted, ""); got != "Someone accepted your credit" {
		t.Errorf("accepted inbox copy = %q", got)
	}
}

func TestCreditsPreferencesGateCreditNotices(t *testing.T) {
	prefs := postgres.DefaultNotificationPreferences("u")
	if !prefs.PushCredits || !prefs.InappCredits {
		t.Fatal("credits toggles must default on")
	}
	for _, ty := range CreditNotificationTypes {
		if d := resolveDecision(ty, prefs, false); !d.CreateInbox || !d.SendPush {
			t.Errorf("%s with defaults: %+v", ty, d)
		}
		off := postgres.DefaultNotificationPreferences("u")
		off.PushCredits = false
		if d := resolveDecision(ty, off, false); d.SendPush || !d.CreateInbox {
			t.Errorf("%s push_credits=false: %+v, want inbox only", ty, d)
		}
		off = postgres.DefaultNotificationPreferences("u")
		off.InappCredits = false
		if d := resolveDecision(ty, off, false); d.CreateInbox || !d.SendPush {
			t.Errorf("%s inapp_credits=false: %+v, want push only", ty, d)
		}
	}
	// The mentions toggles do not silence credits, and credits do not
	// silence mentions.
	m := postgres.DefaultNotificationPreferences("u")
	m.PushMentions, m.InappMentions = false, false
	if d := resolveDecision(CreditTypeRequest, m, false); !d.SendPush || !d.CreateInbox {
		t.Errorf("mentions toggles silenced a credit request: %+v", d)
	}
	c := postgres.DefaultNotificationPreferences("u")
	c.PushCredits, c.InappCredits = false, false
	if d := resolveDecision("mention", c, false); !d.SendPush || !d.CreateInbox {
		t.Errorf("credits toggles silenced a mention: %+v", d)
	}
}

func TestRenderCreditNotification(t *testing.T) {
	credit, tagged, uploader, post := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	n := CreditNotification{RecipientID: tagged, ActorID: uploader, Type: CreditTypeRequest, CreditID: credit,
		PostID: post, UploaderName: "Blue Hour Films", Name: "Asha Rao", Role: "Director",
		PostTitle: "Monsoon Letters", CreatedAt: time.Now()}
	r := RenderCreditNotification(n)
	if r.Title != "Blue Hour Films credited you in a video" || r.Body != "Director · Monsoon Letters" {
		t.Fatalf("request render = %+v", r)
	}
	if r.CollapseKey != "credit:"+credit.String()+":"+tagged.String() {
		t.Fatalf("collapse key %q", r.CollapseKey)
	}
	n.UploaderName, n.PostTitle = "  ", ""
	if r := RenderCreditNotification(n); r.Title != "Someone credited you in a video" || r.Body != "Director" {
		t.Fatalf("request render without name/title = %+v", r)
	}
	a := CreditNotification{RecipientID: uploader, ActorID: tagged, Type: CreditTypeAccepted, CreditID: credit,
		PostID: post, Name: "Asha Rao", Role: "Director", PostTitle: "Monsoon Letters"}
	if r := RenderCreditNotification(a); r.Title != "Asha Rao accepted your credit" || r.Body != "Director · Monsoon Letters" {
		t.Fatalf("accepted render = %+v", r)
	}
	a.PostTitle = ""
	if r := RenderCreditNotification(a); r.Body != "Director" {
		t.Fatalf("accepted body without title = %q", r.Body)
	}
	if n.Identity() != credit.String()+":"+tagged.String()+":"+CreditTypeRequest {
		t.Fatalf("identity %q", n.Identity())
	}
	if a.Identity() == n.Identity() {
		t.Fatal("request and acceptance of one credit share an identity")
	}
}

// A notice to oneself is dropped before any store is touched (a zero
// Service has none, so reaching delivery would panic).
func TestCreateCreditNotificationDropsSelfAndNil(t *testing.T) {
	s := &Service{}
	me := uuid.New()
	for _, n := range []CreditNotification{
		{RecipientID: me, ActorID: me, Type: CreditTypeRequest, CreditID: uuid.New(), PostID: uuid.New()},
		{RecipientID: uuid.Nil, ActorID: me, Type: CreditTypeAccepted, CreditID: uuid.New(), PostID: uuid.New()},
	} {
		if err := s.CreateCreditNotification(context.Background(), n); err != nil {
			t.Fatalf("%+v: %v", n, err)
		}
	}
}

func TestBuildPushDataCarriesTheCreditCopy(t *testing.T) {
	user, post, credit := uuid.New(), uuid.New(), uuid.New()
	n := CreditNotification{RecipientID: user, ActorID: uuid.New(), Type: CreditTypeRequest, CreditID: credit, PostID: post,
		UploaderName: "Ravi", Role: "Editor", PostTitle: "Night Bus", DeepLink: CreditRequestsDeepLink}
	title, body, data := buildPushData(user, n.Type, CreditEntityType, post, n.DeepLink, RenderCreditNotification(n))
	if title != "Ravi credited you in a video" || body != "Editor · Night Bus" {
		t.Fatalf("push copy %q / %q", title, body)
	}
	if data["deep_link"] != "/credits/requests" || data["entity_type"] != "post" || data["entity_id"] != post.String() ||
		data["type"] != CreditTypeRequest || data["collapse_key"] != "credit:"+credit.String()+":"+user.String() {
		t.Fatalf("push data %v", data)
	}
}
