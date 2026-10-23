package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Paid memberships (2026-10-23): the six types are registered, sit in the
// memberships category and the system tab, render the contract copy, and are
// delivered once per (event, recipient) with the renewal kept quiet.

var (
	msSub     = uuid.MustParse("00000000-0000-4000-8000-0000000ab001")
	msMember  = uuid.MustParse("00000000-0000-4000-8000-0000000ab002")
	msCreator = uuid.MustParse("00000000-0000-4000-8000-0000000ab003")
	msEnd     = time.Date(2026, 11, 22, 18, 30, 0, 0, time.UTC) // 23 Nov 00:00 IST
)

func msNotice(typ string) MembershipNotification {
	n := MembershipNotification{
		EventID: "evt-1", RecipientID: msMember, ActorID: msCreator, Type: typ,
		SubscriptionID: msSub, TierName: "Gold", PeriodEnd: msEnd, DaysLeft: MembershipDaysLeftUnknown,
		DeepLink: MembershipDeepLink(msSub), CreatedAt: time.Date(2026, 10, 23, 9, 30, 0, 0, time.UTC),
	}
	if typ == MembershipTypeNewMember {
		n.RecipientID, n.ActorID, n.DeepLink = msCreator, msMember, MembershipCreatorDeepLink
	}
	return n
}

func TestMembershipTypesAreRegisteredInTheMembershipsCategory(t *testing.T) {
	if len(MembershipNotificationTypes) != 6 {
		t.Fatalf("membership types = %v", MembershipNotificationTypes)
	}
	for _, ty := range MembershipNotificationTypes {
		tpl, ok := Templates[ty]
		if !ok {
			t.Errorf("%s not in Templates", ty)
			continue
		}
		if tpl.TitleTemplate == "" || tpl.BodyTemplate == "" || tpl.CanAggregate || tpl.Priority == "critical" {
			t.Errorf("%s template incomplete: %+v", ty, tpl)
		}
		if wantPush := ty != MembershipTypeRenewed; tpl.PushEligible != wantPush {
			t.Errorf("%s PushEligible = %v, want %v", ty, tpl.PushEligible, wantPush)
		}
		if categoryForEvent(ty) != catMemberships {
			t.Errorf("%s is not in the memberships category", ty)
		}
		if inboxTabForType(ty) != InboxTabSystem {
			t.Errorf("%s not in the system tab", ty)
		}
		for _, name := range []string{"Asha Rao", ""} {
			if title, icon := InboxCopy(ty, name); title == inboxGenericTitle || icon != InboxIconAccount {
				t.Errorf("%s inbox copy(%q) = %q / %q", ty, name, title, icon)
			}
		}
	}
	cases := []struct{ ty, name, want string }{
		{MembershipTypeNewMember, "Asha Rao", "Asha Rao joined your membership"},
		{MembershipTypeNewMember, "", "Someone joined your membership"},
		{MembershipTypeJoined, "Asha Rao", "You're a member of Asha Rao"},
		{MembershipTypeJoined, " ", "You're a member of a creator"},
		{MembershipTypeRenewed, "Asha Rao", "Your Asha Rao membership was renewed"},
		{MembershipTypeRenewed, "", "Your membership with a creator was renewed"},
		{MembershipTypeRenewalDue, "Asha Rao", "Your Asha Rao membership ends soon — renew to keep access"},
		{MembershipTypeRenewalDue, "", "Your membership with a creator ends soon — renew to keep access"},
		{MembershipTypePaymentFailed, "Asha Rao", "Your Asha Rao membership payment didn't go through"},
		{MembershipTypePaymentFailed, "", "Your membership payment to a creator didn't go through"},
		{MembershipTypeEnded, "Asha Rao", "Your Asha Rao membership has ended"},
		{MembershipTypeEnded, "", "Your membership with a creator has ended"},
	}
	for _, c := range cases {
		if got, _ := InboxCopy(c.ty, c.name); got != c.want {
			t.Errorf("InboxCopy(%s, %q) = %q, want %q", c.ty, c.name, got, c.want)
		}
	}
}

func TestMembershipsPreferencesGateMembershipNotices(t *testing.T) {
	prefs := postgres.DefaultNotificationPreferences("u")
	if !prefs.PushMemberships || !prefs.InappMemberships {
		t.Fatal("memberships toggles must default on")
	}
	for _, ty := range MembershipNotificationTypes {
		wantPush := ty != MembershipTypeRenewed
		if d := resolveDecision(ty, prefs, false); !d.CreateInbox || d.SendPush != wantPush {
			t.Errorf("%s with defaults: %+v", ty, d)
		}
		off := postgres.DefaultNotificationPreferences("u")
		off.PushMemberships = false
		if d := resolveDecision(ty, off, false); d.SendPush || !d.CreateInbox {
			t.Errorf("%s push_memberships=false: %+v, want inbox only", ty, d)
		}
		off = postgres.DefaultNotificationPreferences("u")
		off.InappMemberships = false
		if d := resolveDecision(ty, off, false); d.CreateInbox || d.SendWebSocket || d.SendPush != wantPush {
			t.Errorf("%s inapp_memberships=false: %+v", ty, d)
		}
	}
	// The system toggles do not silence memberships, and memberships do not
	// silence system notices.
	s := postgres.DefaultNotificationPreferences("u")
	s.PushSystem, s.InappSystem = false, false
	if d := resolveDecision(MembershipTypeRenewalDue, s, false); !d.SendPush || !d.CreateInbox {
		t.Errorf("system toggles silenced a renewal reminder: %+v", d)
	}
	m := postgres.DefaultNotificationPreferences("u")
	m.PushMemberships, m.InappMemberships = false, false
	if d := resolveDecision("system.verification", m, false); !d.SendPush || !d.CreateInbox {
		t.Errorf("memberships toggles silenced a system notice: %+v", d)
	}
}

func TestRenderMembershipNotification(t *testing.T) {
	cases := []struct {
		ty, name, tier string
		daysLeft       int
		title, body    string
	}{
		{MembershipTypeNewMember, "Ravi", "Gold", -1, "Ravi joined your Gold membership", "Tap to see your members"},
		{MembershipTypeNewMember, "", "Gold", -1, "Someone joined your Gold membership", "Tap to see your members"},
		{MembershipTypeNewMember, "Ravi", "", -1, "Ravi joined your membership", "Tap to see your members"},
		{MembershipTypeJoined, "Blue Hour Films", "Gold", -1, "You're a member of Blue Hour Films", "Your Gold membership is active. Members-only posts are unlocked."},
		{MembershipTypeJoined, "", "", -1, "You're a member of a creator", "Your membership is active. Members-only posts are unlocked."},
		{MembershipTypeRenewed, "Blue Hour Films", "Gold", -1, "Your Blue Hour Films membership was renewed", "Access continues until 23 Nov 2026"},
		{MembershipTypeRenewalDue, "Blue Hour Films", "Gold", 3, "Your Blue Hour Films membership ends in 3 days — renew to keep access", "Tap to renew"},
		{MembershipTypeRenewalDue, "Blue Hour Films", "Gold", 0, "Your Blue Hour Films membership ends today — renew to keep access", "Tap to renew"},
		{MembershipTypeRenewalDue, "Blue Hour Films", "Gold", 1, "Your Blue Hour Films membership ends tomorrow — renew to keep access", "Tap to renew"},
		{MembershipTypeRenewalDue, "", "Gold", 3, "Your membership with a creator ends in 3 days — renew to keep access", "Tap to renew"},
		{MembershipTypeRenewalDue, "Blue Hour Films", "Gold", -1, "Your Blue Hour Films membership ends soon — renew to keep access", "Tap to renew"},
		{MembershipTypePaymentFailed, "Blue Hour Films", "Gold", -1, "Your Blue Hour Films membership payment didn't go through", "Tap to try again"},
		{MembershipTypePaymentFailed, "", "Gold", -1, "Your membership payment to a creator didn't go through", "Tap to try again"},
		{MembershipTypeEnded, "Blue Hour Films", "Gold", -1, "Your Blue Hour Films membership has ended", "Join again any time to unlock members-only posts"},
		{MembershipTypeEnded, "", "Gold", -1, "Your membership with a creator has ended", "Join again any time to unlock members-only posts"},
	}
	for _, c := range cases {
		n := msNotice(c.ty)
		n.ActorName, n.TierName, n.DaysLeft = c.name, c.tier, c.daysLeft
		r := RenderMembershipNotification(n)
		if r.Title != c.title || r.Body != c.body {
			t.Errorf("%s(%q, %q, %d) = %q / %q, want %q / %q", c.ty, c.name, c.tier, c.daysLeft, r.Title, r.Body, c.title, c.body)
		}
		if r.CollapseKey != "membership:"+msSub.String()+":"+n.RecipientID.String() {
			t.Errorf("%s collapse key %q", c.ty, r.CollapseKey)
		}
		if r.Data[push.AndroidChannelDataKey] != "account" || r.Data["subscription_id"] != msSub.String() {
			t.Errorf("%s data %v", c.ty, r.Data)
		}
	}
	n := msNotice(MembershipTypeRenewed)
	n.PeriodEnd = time.Time{}
	if r := RenderMembershipNotification(n); r.Body != "Access continues until your next renewal" {
		t.Errorf("renewed body without period_end = %q", r.Body)
	}
}

func TestMembershipIdentityIsPerEventRecipientAndType(t *testing.T) {
	a := msNotice(MembershipTypeJoined)
	if a.Identity() != "evt-1:"+msMember.String()+":membership_joined" {
		t.Fatalf("identity %q", a.Identity())
	}
	b := msNotice(MembershipTypeNewMember)
	c := a
	c.EventID = "evt-2"
	if a.Identity() == b.Identity() || a.Identity() == c.Identity() {
		t.Fatal("identities collide across recipients or events")
	}
}

func TestBuildPushDataCarriesTheMembershipCopy(t *testing.T) {
	n := msNotice(MembershipTypeRenewalDue)
	n.ActorName, n.DaysLeft = "Blue Hour Films", 0
	title, body, data := buildPushData(n.RecipientID, n.Type, MembershipEntityType, msSub, n.DeepLink, RenderMembershipNotification(n))
	if title != "Your Blue Hour Films membership ends today — renew to keep access" || body != "Tap to renew" {
		t.Fatalf("push copy %q / %q", title, body)
	}
	if data["deep_link"] != "/memberships/"+msSub.String() || data["entity_type"] != "membership" || data["entity_id"] != msSub.String() ||
		data["type"] != MembershipTypeRenewalDue || data[push.AndroidChannelDataKey] != "account" ||
		data["collapse_key"] != "membership:"+msSub.String()+":"+msMember.String() {
		t.Fatalf("push data %v", data)
	}
	msg := push.BuildFCMMessage("tok", title, body, data)
	android, _ := msg["android"].(map[string]interface{})
	if nb, _ := android["notification"].(map[string]string); nb["channel_id"] != "account" {
		t.Fatalf("fcm android block %v", android)
	}
}

// fakeMembershipTransports stands in for the stores. deliver mimics the
// idempotent inbox write: the first delivery of an identity creates the row
// (and, per the decision, the push); a repeat is a no-op.
type fakeMembershipTransports struct {
	suppressed bool
	prefs      *postgres.NotificationPreferences // nil: no preference store (every channel on)
	names      map[uuid.UUID]string
	lookups    int
	deliverErr error

	rows   map[string]MembershipNotification
	pushes []RenderOverride
	calls  []DeliveryDecision
}

func (f *fakeMembershipTransports) recipientSuppressed(context.Context, uuid.UUID, string) bool {
	return f.suppressed
}

func (f *fakeMembershipTransports) resolveGeneralDelivery(_ context.Context, _ uuid.UUID, ty string) DeliveryDecision {
	if f.prefs == nil {
		return DeliveryDecision{CreateInbox: true, SendWebSocket: true, SendPush: true}
	}
	return resolveDecision(ty, f.prefs, false)
}

func (f *fakeMembershipTransports) membershipActorName(_ context.Context, id uuid.UUID) string {
	f.lookups++
	return f.names[id]
}

func (f *fakeMembershipTransports) deliverWithDecision(_ context.Context, d DeliveryDecision, userID, actorID uuid.UUID, ty, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string, render RenderOverride) error {
	f.calls = append(f.calls, d)
	if f.deliverErr != nil {
		return f.deliverErr
	}
	if f.rows == nil {
		f.rows = map[string]MembershipNotification{}
	}
	if _, seen := f.rows[identity]; seen {
		return nil
	}
	if entityType != MembershipEntityType {
		return errors.New("wrong entity type " + entityType)
	}
	f.rows[identity] = MembershipNotification{RecipientID: userID, ActorID: actorID, Type: ty, SubscriptionID: entityID, DeepLink: deepLink, CreatedAt: createdAt}
	if d.SendPush {
		f.pushes = append(f.pushes, render)
	}
	return nil
}

func TestRunMembershipNoticeDeliversWithNamedPush(t *testing.T) {
	f := &fakeMembershipTransports{names: map[uuid.UUID]string{msCreator: "Blue Hour Films", msMember: "Ravi"}}
	n := msNotice(MembershipTypeNewMember)
	if err := runMembershipNotice(context.Background(), f, n); err != nil {
		t.Fatal(err)
	}
	row, ok := f.rows[n.Identity()]
	if !ok || row.RecipientID != msCreator || row.ActorID != msMember || row.DeepLink != "/monetization/tiers" || row.SubscriptionID != msSub {
		t.Fatalf("row %+v ok=%v", row, ok)
	}
	if len(f.pushes) != 1 || f.pushes[0].Title != "Ravi joined your Gold membership" {
		t.Fatalf("pushes %+v", f.pushes)
	}
}

// The renewal stays in-app even where no preference store answers (which
// turns every channel on), and needs no name lookup.
func TestRunMembershipNoticeRenewalIsQuiet(t *testing.T) {
	f := &fakeMembershipTransports{names: map[uuid.UUID]string{msCreator: "Blue Hour Films"}}
	if err := runMembershipNotice(context.Background(), f, msNotice(MembershipTypeRenewed)); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 1 || len(f.pushes) != 0 || f.lookups != 0 {
		t.Fatalf("rows %d pushes %d lookups %d", len(f.rows), len(f.pushes), f.lookups)
	}
	if d := f.calls[0]; d.SendPush || d.DeferPush || !d.CreateInbox {
		t.Fatalf("decision %+v", d)
	}
	// Every other type pushes.
	for _, ty := range MembershipNotificationTypes {
		if ty == MembershipTypeRenewed {
			continue
		}
		g := &fakeMembershipTransports{}
		if err := runMembershipNotice(context.Background(), g, msNotice(ty)); err != nil {
			t.Fatal(err)
		}
		if len(g.pushes) != 1 {
			t.Errorf("%s pushed %d times", ty, len(g.pushes))
		}
	}
}

func TestRunMembershipNoticePreferenceOffSuppresses(t *testing.T) {
	both := postgres.DefaultNotificationPreferences("u")
	both.PushMemberships, both.InappMemberships = false, false
	f := &fakeMembershipTransports{prefs: both}
	if err := runMembershipNotice(context.Background(), f, msNotice(MembershipTypeRenewalDue)); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 || f.lookups != 0 {
		t.Fatalf("both toggles off still delivered: calls %d lookups %d", len(f.calls), f.lookups)
	}

	pushOff := postgres.DefaultNotificationPreferences("u")
	pushOff.PushMemberships = false
	f = &fakeMembershipTransports{prefs: pushOff}
	if err := runMembershipNotice(context.Background(), f, msNotice(MembershipTypePaymentFailed)); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 1 || len(f.pushes) != 0 || f.lookups != 0 {
		t.Fatalf("push off: rows %d pushes %d lookups %d", len(f.rows), len(f.pushes), f.lookups)
	}

	master := postgres.DefaultNotificationPreferences("u")
	master.PushEnabled = false
	f = &fakeMembershipTransports{prefs: master}
	if err := runMembershipNotice(context.Background(), f, msNotice(MembershipTypeEnded)); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 1 || len(f.pushes) != 0 {
		t.Fatalf("master push off: rows %d pushes %d", len(f.rows), len(f.pushes))
	}

	f = &fakeMembershipTransports{suppressed: true}
	if err := runMembershipNotice(context.Background(), f, msNotice(MembershipTypeJoined)); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 0 {
		t.Fatal("a suppressed (deactivated) recipient was notified")
	}
}

func TestRunMembershipNoticeReplayIsIdempotent(t *testing.T) {
	f := &fakeMembershipTransports{}
	for i := 0; i < 3; i++ {
		for _, ty := range []string{MembershipTypeNewMember, MembershipTypeJoined} {
			if err := runMembershipNotice(context.Background(), f, msNotice(ty)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(f.rows) != 2 || len(f.pushes) != 2 {
		t.Fatalf("replayed event: rows %d pushes %d, want 2/2", len(f.rows), len(f.pushes))
	}
	// A new event for the same membership is a new notice.
	n := msNotice(MembershipTypeJoined)
	n.EventID = "evt-2"
	if err := runMembershipNotice(context.Background(), f, n); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 3 {
		t.Fatalf("second event: rows %d", len(f.rows))
	}
}

func TestRunMembershipNoticeDropsUndeliverable(t *testing.T) {
	f := &fakeMembershipTransports{}
	self := msNotice(MembershipTypeJoined)
	self.ActorID = self.RecipientID
	noRecipient := msNotice(MembershipTypeJoined)
	noRecipient.RecipientID = uuid.Nil
	noEvent := msNotice(MembershipTypeJoined)
	noEvent.EventID = ""
	noSub := msNotice(MembershipTypeJoined)
	noSub.SubscriptionID = uuid.Nil
	for _, n := range []MembershipNotification{self, noRecipient, noEvent, noSub} {
		if err := runMembershipNotice(context.Background(), f, n); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("undeliverable notices reached delivery: %d", len(f.calls))
	}
	// A store error is returned so the consumer retries.
	g := &fakeMembershipTransports{deliverErr: errors.New("scylla down")}
	if err := runMembershipNotice(context.Background(), g, msNotice(MembershipTypeEnded)); err == nil {
		t.Fatal("delivery error swallowed")
	}
}
