package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Follow requests to private accounts (2026-10-24): both types are
// registered, ride the follows toggles, land in the followers tab, and
// deliver once per event and recipient with a named push.

var (
	frRequester = uuid.MustParse("00000000-0000-4000-8000-0000000fa001")
	frPrivate   = uuid.MustParse("00000000-0000-4000-8000-0000000fa002")
	frAt        = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
)

func TestFollowRequestTypesAreRegisteredInTheFollowsCategory(t *testing.T) {
	for _, ty := range FollowRequestNotificationTypes {
		tpl, ok := Templates[ty]
		if !ok {
			t.Errorf("%s not in Templates", ty)
			continue
		}
		if !tpl.PushEligible || tpl.TitleTemplate == "" || tpl.BodyTemplate == "" || tpl.CanAggregate {
			t.Errorf("%s template incomplete: %+v", ty, tpl)
		}
		if categoryForEvent(ty) != catFollows {
			t.Errorf("%s is not in the follows category", ty)
		}
		if title, icon := InboxCopy(ty, "Asha Rao"); title == inboxGenericTitle || icon != InboxIconSocial {
			t.Errorf("%s inbox copy = %q / %q", ty, title, icon)
		}
		if inboxTabForType(ty) != InboxTabFollowers {
			t.Errorf("%s not in the followers tab", ty)
		}
		if title, _ := notifTitleBody(ty); title == "New Notification" {
			t.Errorf("%s has no push fallback copy", ty)
		}
	}
	if got, _ := InboxCopy(FollowRequestType, "Asha Rao"); got != "Asha Rao requested to follow you" {
		t.Errorf("request inbox copy = %q", got)
	}
	if got, _ := InboxCopy(FollowRequestAcceptedType, ""); got != "Someone accepted your follow request" {
		t.Errorf("accepted inbox copy = %q", got)
	}
}

func TestFollowsPreferencesGateFollowRequestNotices(t *testing.T) {
	for _, ty := range FollowRequestNotificationTypes {
		on := postgres.DefaultNotificationPreferences("u")
		if d := resolveDecision(ty, on, false); !d.CreateInbox || !d.SendPush {
			t.Errorf("%s with defaults: %+v", ty, d)
		}
		off := postgres.DefaultNotificationPreferences("u")
		off.PushFollows = false
		if d := resolveDecision(ty, off, false); d.SendPush || !d.CreateInbox {
			t.Errorf("%s push_follows=false: %+v, want inbox only", ty, d)
		}
		off = postgres.DefaultNotificationPreferences("u")
		off.InappFollows = false
		if d := resolveDecision(ty, off, false); d.CreateInbox || !d.SendPush {
			t.Errorf("%s inapp_follows=false: %+v, want push only", ty, d)
		}
	}
}

type fakeFollowRequestTransports struct {
	suppressed bool
	prefs      *postgres.NotificationPreferences
	names      map[uuid.UUID]string
	lookups    int

	rows   map[string]FollowRequestNotification
	pushes []RenderOverride
}

func (f *fakeFollowRequestTransports) recipientSuppressed(context.Context, uuid.UUID, string) bool {
	return f.suppressed
}

func (f *fakeFollowRequestTransports) resolveGeneralDelivery(_ context.Context, _ uuid.UUID, ty string) DeliveryDecision {
	if f.prefs == nil {
		return DeliveryDecision{CreateInbox: true, SendWebSocket: true, SendPush: true}
	}
	return resolveDecision(ty, f.prefs, false)
}

func (f *fakeFollowRequestTransports) followRequestActorName(_ context.Context, id uuid.UUID) string {
	f.lookups++
	return f.names[id]
}

// deliverWithDecision mimics the idempotent inbox write: the first delivery
// of an identity creates the row (and the push); a repeat is a no-op.
func (f *fakeFollowRequestTransports) deliverWithDecision(_ context.Context, d DeliveryDecision, userID, actorID uuid.UUID, ty, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string, render RenderOverride) error {
	if f.rows == nil {
		f.rows = map[string]FollowRequestNotification{}
	}
	if _, seen := f.rows[identity]; seen {
		return nil
	}
	if entityType != FollowRequestEntityType || entityID != actorID {
		panic("follow-request notice must point at the actor's profile")
	}
	f.rows[identity] = FollowRequestNotification{RecipientID: userID, ActorID: actorID, Type: ty, DeepLink: deepLink, CreatedAt: createdAt}
	if d.SendPush {
		f.pushes = append(f.pushes, render)
	}
	return nil
}

func frRequestNotice() FollowRequestNotification {
	return FollowRequestNotification{
		EventID: "evt-1", RecipientID: frPrivate, ActorID: frRequester, Type: FollowRequestType,
		DeepLink: FollowRequestsDeepLink, CreatedAt: frAt,
	}
}

func TestRunFollowRequestNoticeDeliversOncePerEventWithNamedPush(t *testing.T) {
	f := &fakeFollowRequestTransports{names: map[uuid.UUID]string{frRequester: "Ravi"}}
	n := frRequestNotice()
	for i := 0; i < 3; i++ { // Kafka redelivery
		if err := runFollowRequestNotice(context.Background(), f, n); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.rows) != 1 || len(f.pushes) != 1 {
		t.Fatalf("redelivered event made %d rows / %d pushes, want 1/1", len(f.rows), len(f.pushes))
	}
	row := f.rows[n.Identity()]
	if row.RecipientID != frPrivate || row.ActorID != frRequester || row.DeepLink != "/notifications?tab=requests" {
		t.Fatalf("row %+v", row)
	}
	if f.pushes[0].Title != "Ravi requested to follow you" {
		t.Fatalf("push title %q", f.pushes[0].Title)
	}

	// The same request re-sent after a decline is a new event: a new notice.
	again := n
	again.EventID = "evt-2"
	if err := runFollowRequestNotice(context.Background(), f, again); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 2 {
		t.Fatalf("a new event id must notify again: %d rows", len(f.rows))
	}
}

func TestRunFollowRequestNoticeAcceptedNamesTheAccount(t *testing.T) {
	f := &fakeFollowRequestTransports{names: map[uuid.UUID]string{frPrivate: "Asha Rao"}}
	n := FollowRequestNotification{
		EventID: "evt-3", RecipientID: frRequester, ActorID: frPrivate, Type: FollowRequestAcceptedType,
		DeepLink: FollowRequestAcceptedDeepLink(frPrivate), CreatedAt: frAt,
	}
	if err := runFollowRequestNotice(context.Background(), f, n); err != nil {
		t.Fatal(err)
	}
	row, ok := f.rows[n.Identity()]
	if !ok || row.DeepLink != "/u/"+frPrivate.String() || row.RecipientID != frRequester {
		t.Fatalf("row %+v ok=%v", row, ok)
	}
	if len(f.pushes) != 1 || f.pushes[0].Title != "Asha Rao accepted your follow request" {
		t.Fatalf("pushes %+v", f.pushes)
	}
}

func TestRunFollowRequestNoticeDrops(t *testing.T) {
	cases := map[string]func(*FollowRequestNotification){
		"no event id":  func(n *FollowRequestNotification) { n.EventID = " " },
		"no recipient": func(n *FollowRequestNotification) { n.RecipientID = uuid.Nil },
		"no actor":     func(n *FollowRequestNotification) { n.ActorID = uuid.Nil },
		"to oneself":   func(n *FollowRequestNotification) { n.ActorID = n.RecipientID },
		"unknown type": func(n *FollowRequestNotification) { n.Type = "follow" },
	}
	for name, mutate := range cases {
		f := &fakeFollowRequestTransports{}
		n := frRequestNotice()
		mutate(&n)
		if err := runFollowRequestNotice(context.Background(), f, n); err != nil {
			t.Fatal(err)
		}
		if len(f.rows) != 0 {
			t.Errorf("%s: delivered %+v", name, f.rows)
		}
	}
	f := &fakeFollowRequestTransports{suppressed: true}
	if err := runFollowRequestNotice(context.Background(), f, frRequestNotice()); err != nil || len(f.rows) != 0 {
		t.Fatalf("suppressed recipient got %d rows (err %v)", len(f.rows), err)
	}
}

func TestRunFollowRequestNoticeFollowsTogglesOff(t *testing.T) {
	off := postgres.DefaultNotificationPreferences("u")
	off.PushFollows, off.InappFollows = false, false
	f := &fakeFollowRequestTransports{prefs: off, names: map[uuid.UUID]string{frRequester: "Ravi"}}
	if err := runFollowRequestNotice(context.Background(), f, frRequestNotice()); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 0 || f.lookups != 0 {
		t.Fatalf("follows toggles off still delivered: rows=%d lookups=%d", len(f.rows), f.lookups)
	}
}

func TestRenderFollowRequestNotificationFallsBackToSomeone(t *testing.T) {
	r := RenderFollowRequestNotification(frRequestNotice())
	if r.Title != "Someone requested to follow you" || r.Body != "Tap to accept or decline" || r.CollapseKey == "" {
		t.Fatalf("render %+v", r)
	}
}
