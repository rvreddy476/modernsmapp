package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	noteRecipient = uuid.MustParse("aaaaaaaa-0000-4000-8000-000000000001")
	noteActor     = uuid.MustParse("aaaaaaaa-0000-4000-8000-000000000002")
	noteReplyID   = uuid.MustParse("aaaaaaaa-0000-4000-8000-000000000003")
	noteParentID  = uuid.MustParse("aaaaaaaa-0000-4000-8000-000000000004")
	noteAt        = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
)

type noteRow struct {
	userID, actorID uuid.UUID
	notifType       string
	entityType      string
	entityID        uuid.UUID
	deepLink        string
	identity        string
}

type fakeNoteTransports struct {
	suppressed bool
	prefs      *postgres.NotificationPreferences
	names      map[uuid.UUID]string

	rows   []noteRow
	pushes []RenderOverride
}

func (f *fakeNoteTransports) recipientSuppressed(context.Context, uuid.UUID, string) bool {
	return f.suppressed
}

func (f *fakeNoteTransports) resolveGeneralDelivery(_ context.Context, _ uuid.UUID, ty string) DeliveryDecision {
	if f.prefs == nil {
		return DeliveryDecision{CreateInbox: true, SendWebSocket: true, SendPush: true}
	}
	return resolveDecision(ty, f.prefs, false)
}

func (f *fakeNoteTransports) followRequestActorName(_ context.Context, id uuid.UUID) string {
	return f.names[id]
}

func (f *fakeNoteTransports) deliverWithDecision(_ context.Context, d DeliveryDecision, userID, actorID uuid.UUID, ty, entityType string, entityID uuid.UUID, deepLink string, _ time.Time, identity string, render RenderOverride) error {
	if !d.CreateInbox && !d.SendWebSocket && !d.SendPush {
		return nil
	}
	f.rows = append(f.rows, noteRow{userID, actorID, ty, entityType, entityID, deepLink, identity})
	if d.SendPush {
		f.pushes = append(f.pushes, render)
	}
	return nil
}

func replyNotice() NoteNotification {
	return NoteNotification{
		EventID: "evt-1", Type: NoteReplyType, RecipientID: noteRecipient, ActorID: noteActor,
		NoteID: noteReplyID, ParentID: noteParentID, CreatedAt: noteAt,
	}
}

// A reply becomes one inbox row on the reply (a post entity) with the web
// route, and a named push that opens the app route.
func TestRunNoteNotice_ReplyDeliversWebRowAndAppPush(t *testing.T) {
	f := &fakeNoteTransports{names: map[uuid.UUID]string{noteActor: "Ana"}}
	if err := runNoteNotice(context.Background(), f, replyNotice()); err != nil {
		t.Fatal(err)
	}
	if len(f.rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(f.rows))
	}
	want := noteRow{noteRecipient, noteActor, NoteReplyType, "post", noteReplyID,
		"/echo/note/" + noteReplyID.String(), "evt-1:" + noteRecipient.String() + ":note_reply"}
	if f.rows[0] != want {
		t.Fatalf("row = %+v\nwant  %+v", f.rows[0], want)
	}
	if len(f.pushes) != 1 {
		t.Fatalf("pushes = %d, want 1", len(f.pushes))
	}
	p := f.pushes[0]
	if p.Title != "Ana replied to your note" {
		t.Errorf("title = %q", p.Title)
	}
	if p.PushDeepLink != "momentum://echo/note/"+noteReplyID.String() {
		t.Errorf("push deep link = %q", p.PushDeepLink)
	}
	if p.CollapseKey != "note_reply:"+noteParentID.String()+":"+noteRecipient.String() {
		t.Errorf("collapse key = %q", p.CollapseKey)
	}
}

func TestRunNoteNotice_QuoteCopyAndUnknownActor(t *testing.T) {
	f := &fakeNoteTransports{}
	n := replyNotice()
	n.Type = NoteQuoteType
	if err := runNoteNotice(context.Background(), f, n); err != nil {
		t.Fatal(err)
	}
	if len(f.pushes) != 1 || f.pushes[0].Title != "Someone quoted your note" {
		t.Fatalf("pushes = %+v", f.pushes)
	}
	if f.rows[0].notifType != NoteQuoteType {
		t.Fatalf("type = %q", f.rows[0].notifType)
	}
}

// Suppressed recipients, unknown types and notices without an event id are
// dropped.
func TestRunNoteNotice_Drops(t *testing.T) {
	for _, tc := range []struct {
		name   string
		f      *fakeNoteTransports
		mutate func(*NoteNotification)
	}{
		{"suppressed recipient", &fakeNoteTransports{suppressed: true}, func(*NoteNotification) {}},
		{"unknown type", &fakeNoteTransports{}, func(n *NoteNotification) { n.Type = "post_reposted" }},
		{"no event id", &fakeNoteTransports{}, func(n *NoteNotification) { n.EventID = " " }},
		{"no note", &fakeNoteTransports{}, func(n *NoteNotification) { n.NoteID = uuid.Nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := replyNotice()
			tc.mutate(&n)
			if err := runNoteNotice(context.Background(), tc.f, n); err != nil {
				t.Fatal(err)
			}
			if len(tc.f.rows) != 0 {
				t.Fatalf("delivered %+v", tc.f.rows)
			}
		})
	}
}

// Both types ride the replies toggle pair, like comment replies, and land on
// the Comments tab.
func TestNoteTypes_RideTheRepliesPreference(t *testing.T) {
	for _, ty := range NoteNotificationTypes {
		if got := categoryForEvent(ty); got != catReplies {
			t.Errorf("%s category = %v, want replies", ty, got)
		}
		if got := inboxTabForType(ty); got != InboxTabComments {
			t.Errorf("%s tab = %q, want comments", ty, got)
		}
		off := postgres.DefaultNotificationPreferences("u")
		off.PushReplies = false
		f := &fakeNoteTransports{prefs: off}
		n := replyNotice()
		n.Type = ty
		if err := runNoteNotice(context.Background(), f, n); err != nil {
			t.Fatal(err)
		}
		if len(f.rows) != 1 || len(f.pushes) != 0 {
			t.Errorf("%s push_replies=false: rows %d pushes %d, want inbox only", ty, len(f.rows), len(f.pushes))
		}
		off = postgres.DefaultNotificationPreferences("u")
		off.InappReplies = false
		off.PushReplies = false
		f = &fakeNoteTransports{prefs: off}
		if err := runNoteNotice(context.Background(), f, n); err != nil {
			t.Fatal(err)
		}
		if len(f.rows) != 0 {
			t.Errorf("%s replies off: delivered %+v", ty, f.rows)
		}
	}
}

// The push carries the app route while the stored row keeps the web route.
func TestBuildPushData_PushDeepLinkOverridesOnlyThePush(t *testing.T) {
	render := RenderNoteNotification(replyNotice())
	_, _, data := buildPushData(noteRecipient, NoteReplyType, "post", noteReplyID, NoteWebDeepLink(noteReplyID), render)
	if data["deep_link"] != "momentum://echo/note/"+noteReplyID.String() {
		t.Fatalf("deep_link = %q", data["deep_link"])
	}
	_, _, plain := buildPushData(noteRecipient, "post_reposted", "post", noteReplyID, "/post/x", RenderOverride{})
	if plain["deep_link"] != "/post/x" {
		t.Fatalf("a push without an override changed its link: %q", plain["deep_link"])
	}
}

func TestNoteTypes_InboxCopyAndTemplates(t *testing.T) {
	if title, _ := InboxCopy(NoteReplyType, "Ana"); title != "Ana replied to your note" {
		t.Errorf("reply inbox title = %q", title)
	}
	if title, _ := InboxCopy(NoteQuoteType, "Ana"); title != "Ana quoted your note" {
		t.Errorf("quote inbox title = %q", title)
	}
	for _, ty := range NoteNotificationTypes {
		if tpl := GetTemplate(ty); tpl.EventType != ty || !tpl.PushEligible {
			t.Errorf("%s template = %+v", ty, tpl)
		}
	}
}
