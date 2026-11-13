package events

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type fakeNoteNotifier struct{ got []service.NoteNotification }

func (f *fakeNoteNotifier) CreateNoteNotification(_ context.Context, n service.NoteNotification) error {
	f.got = append(f.got, n)
	return nil
}

type noteBlockCall struct{ user, other uuid.UUID }

type fakeNoteBlocks struct {
	blocked bool
	err     error
	calls   []noteBlockCall
}

func (f *fakeNoteBlocks) EitherBlocked(_ context.Context, user, other uuid.UUID) (bool, error) {
	f.calls = append(f.calls, noteBlockCall{user, other})
	return f.blocked, f.err
}

var (
	ncActor     = "bbbbbbbb-0000-4000-8000-000000000001"
	ncRecipient = "bbbbbbbb-0000-4000-8000-000000000002"
	ncNote      = "bbbbbbbb-0000-4000-8000-000000000003"
	ncParent    = "bbbbbbbb-0000-4000-8000-000000000004"
	ncConvo     = "bbbbbbbb-0000-4000-8000-000000000005"
	ncAt        = time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
)

func noteMessage(t *testing.T, eventType string, payload any) kafka.Message {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(sharedevents.EventEnvelope{
		EventID: "evt-note-1", EventType: eventType, OccurredAt: ncAt,
		ActorUserID: &ncActor, Payload: json.RawMessage(raw),
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Key: []byte(ncActor), Value: value}
}

func repliedMessage(t *testing.T, actor, recipient string) kafka.Message {
	return noteMessage(t, sharedevents.EventNoteReplied, sharedevents.NoteRepliedPayload{
		NoteID: ncNote, ConversationID: ncConvo, ReplyToID: ncParent,
		ActorID: actor, RecipientID: recipient, CreatedAt: ncAt,
	})
}

func quotedMessage(t *testing.T, actor, recipient string) kafka.Message {
	return noteMessage(t, sharedevents.EventNoteQuoted, sharedevents.NoteQuotedPayload{
		NoteID: ncNote, QuoteOfID: ncParent, ActorID: actor, RecipientID: recipient, CreatedAt: ncAt,
	})
}

func TestNoteEvents_BecomeOneNoticeToTheParentAuthor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		msg      func(*testing.T, string, string) kafka.Message
		wantType string
	}{
		{"note.replied", repliedMessage, service.NoteReplyType},
		{"note.quoted", quotedMessage, service.NoteQuoteType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			notify, blocks := &fakeNoteNotifier{}, &fakeNoteBlocks{}
			c := &Consumer{noteNotify: notify, noteBlocks: blocks}
			if err := c.processMessage(context.Background(), tc.msg(t, ncActor, ncRecipient)); err != nil {
				t.Fatal(err)
			}
			if len(notify.got) != 1 {
				t.Fatalf("notices = %d, want 1", len(notify.got))
			}
			want := service.NoteNotification{
				EventID: "evt-note-1", Type: tc.wantType,
				RecipientID: uuid.MustParse(ncRecipient), ActorID: uuid.MustParse(ncActor),
				NoteID: uuid.MustParse(ncNote), ParentID: uuid.MustParse(ncParent), CreatedAt: ncAt,
			}
			if got := notify.got[0]; got != want {
				t.Fatalf("notice = %+v\nwant     %+v", got, want)
			}
			if len(blocks.calls) != 1 || blocks.calls[0] != (noteBlockCall{uuid.MustParse(ncRecipient), uuid.MustParse(ncActor)}) {
				t.Fatalf("block checks = %+v, want one between recipient and actor", blocks.calls)
			}
		})
	}
}

// Defence in depth: post-service emits nothing for a self-reply, but if one
// arrives it notifies nobody.
func TestNoteEvents_SelfReplyAndSelfQuoteNotifyNobody(t *testing.T) {
	for _, msg := range []func(*testing.T, string, string) kafka.Message{repliedMessage, quotedMessage} {
		notify, blocks := &fakeNoteNotifier{}, &fakeNoteBlocks{}
		c := &Consumer{noteNotify: notify, noteBlocks: blocks}
		if err := c.processMessage(context.Background(), msg(t, ncActor, ncActor)); err != nil {
			t.Fatal(err)
		}
		if len(notify.got) != 0 {
			t.Fatalf("self notice delivered: %+v", notify.got)
		}
	}
}

// A block in either direction (graph answers for both) stops the notice; so
// does a block check that cannot be answered, or no checker at all.
func TestNoteEvents_BlocksFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		blocks noteBlockChecker
	}{
		{"blocked", &fakeNoteBlocks{blocked: true}},
		{"graph unavailable", &fakeNoteBlocks{err: errors.New("graph down")}},
		{"no block checker", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, msg := range []func(*testing.T, string, string) kafka.Message{repliedMessage, quotedMessage} {
				notify := &fakeNoteNotifier{}
				c := &Consumer{noteNotify: notify, noteBlocks: tc.blocks}
				if err := c.processMessage(context.Background(), msg(t, ncActor, ncRecipient)); err != nil {
					t.Fatal(err)
				}
				if len(notify.got) != 0 {
					t.Fatalf("notice delivered: %+v", notify.got)
				}
			}
		})
	}
}

// Malformed ids notify nobody and are not retried.
func TestNoteEvents_MalformedIDsDropped(t *testing.T) {
	notify := &fakeNoteNotifier{}
	c := &Consumer{noteNotify: notify, noteBlocks: &fakeNoteBlocks{}}
	for _, msg := range []kafka.Message{
		repliedMessage(t, "not-a-uuid", ncRecipient),
		repliedMessage(t, ncActor, ""),
		noteMessage(t, sharedevents.EventNoteQuoted, sharedevents.NoteQuotedPayload{ActorID: ncActor, RecipientID: ncRecipient}),
	} {
		if err := c.processMessage(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
	}
	if len(notify.got) != 0 {
		t.Fatalf("delivered %+v", notify.got)
	}
}

// Echo: a note is not an upload. A public note on a channel, with the
// creator's notify switch on, never becomes a subscriber "uploaded" job —
// not even when it carries the video-ish fields an upload would.
func TestPostCreated_NoteNeverFansOutToSubscribers(t *testing.T) {
	f := &fakeFanout{channel: uuid.New()}
	c := &Consumer{fanout: f}
	mainFeed := false
	msg := noteMessage(t, sharedevents.PostCreated, sharedevents.PostCreatedPayload{
		PostID: ncNote, AuthorID: ncActor, ContentType: "note", Visibility: "public",
		ChannelID: uuid.NewString(), ChannelName: "Ana", Title: "hello",
		NotifySubscribers: boolp(true), MainFeed: &mainFeed,
		ConversationID: ncNote, CreatedAt: ncAt,
	})
	if err := c.processMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(f.enqueued) != 0 {
		t.Fatalf("note enqueued a subscriber fan-out: %+v", f.enqueued)
	}
	if got := service.UploadNotifType("note"); got != "" {
		t.Fatalf("UploadNotifType(note) = %q, want none", got)
	}
}

type inboxDelete struct{ user, entity uuid.UUID }

type fakeInboxDeleter struct {
	calls []inboxDelete
	err   error
}

func (f *fakeInboxDeleter) DeleteNotificationsForEntity(_ context.Context, user, entity uuid.UUID) (int, error) {
	f.calls = append(f.calls, inboxDelete{user, entity})
	return 1, f.err
}

func postDeletedMessage(t *testing.T, p sharedevents.PostDeletedPayload) kafka.Message {
	return noteMessage(t, sharedevents.PostDeleted, p)
}

// A deleted reply or quote takes its notice out of the target author's
// inbox (the row's entity is the deleted note), after the author's own.
func TestPostDeleted_NoteRemovesTheTargetAuthorsNotice(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload sharedevents.PostDeletedPayload
	}{
		{"reply", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "note", ReplyToID: ncParent, TargetAuthorID: ncRecipient}},
		{"quote", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "note", QuoteOfID: ncParent, TargetAuthorID: ncRecipient}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			del := &fakeInboxDeleter{}
			c := &Consumer{inboxDeleter: del}
			if err := c.processMessage(context.Background(), postDeletedMessage(t, tc.payload)); err != nil {
				t.Fatal(err)
			}
			want := []inboxDelete{
				{uuid.MustParse(ncActor), uuid.MustParse(ncNote)},
				{uuid.MustParse(ncRecipient), uuid.MustParse(ncNote)},
			}
			if len(del.calls) != 2 || del.calls[0] != want[0] || del.calls[1] != want[1] {
				t.Fatalf("deletes %+v, want %+v", del.calls, want)
			}
		})
	}
}

// Only the author's inbox for anything else: a post, a top-level note, a
// self-reply, an older payload without a target author, a non-note that
// somehow carries one.
func TestPostDeleted_OnlyTheAuthorsInboxOtherwise(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload sharedevents.PostDeletedPayload
	}{
		{"post", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "post"}},
		{"post carrying a target", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "post", ReplyToID: ncParent, TargetAuthorID: ncRecipient}},
		{"top-level note", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "note"}},
		{"top-level note naming a target", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "note", TargetAuthorID: ncRecipient}},
		{"self reply", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "note", ReplyToID: ncParent, TargetAuthorID: ncActor}},
		{"older producer", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "note", ReplyToID: ncParent}},
		{"bad target", sharedevents.PostDeletedPayload{PostID: ncNote, AuthorID: ncActor, ContentType: "note", ReplyToID: ncParent, TargetAuthorID: "nope"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			del := &fakeInboxDeleter{}
			c := &Consumer{inboxDeleter: del}
			if err := c.processMessage(context.Background(), postDeletedMessage(t, tc.payload)); err != nil {
				t.Fatal(err)
			}
			if len(del.calls) != 1 || del.calls[0] != (inboxDelete{uuid.MustParse(ncActor), uuid.MustParse(ncNote)}) {
				t.Fatalf("deletes %+v, want only the author's inbox", del.calls)
			}
		})
	}
}

// A failed delete is retried (both deletes are idempotent).
func TestPostDeleted_InboxErrorIsRetried(t *testing.T) {
	c := &Consumer{inboxDeleter: &fakeInboxDeleter{err: errors.New("scylla down")}}
	err := c.processMessage(context.Background(), postDeletedMessage(t, sharedevents.PostDeletedPayload{
		PostID: ncNote, AuthorID: ncActor, ContentType: "note", ReplyToID: ncParent, TargetAuthorID: ncRecipient,
	}))
	if err == nil {
		t.Fatal("inbox delete error swallowed")
	}
}

// A restore re-creates nothing.
func TestPostRestored_NoteNoticeIsNotRecreated(t *testing.T) {
	notify, del := &fakeNoteNotifier{}, &fakeInboxDeleter{}
	c := &Consumer{noteNotify: notify, noteBlocks: &fakeNoteBlocks{}, inboxDeleter: del}
	if err := c.processMessage(context.Background(), noteMessage(t, sharedevents.PostRestored, sharedevents.PostRestoredPayload{
		PostID: ncNote, AuthorID: ncActor, ContentType: "note", Visibility: "public", CreatedAt: ncAt, RestoredAt: ncAt,
	})); err != nil {
		t.Fatal(err)
	}
	if len(notify.got) != 0 || len(del.calls) != 0 {
		t.Fatalf("restore: notices %+v deletes %+v", notify.got, del.calls)
	}
}
