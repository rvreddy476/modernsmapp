package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Security sweep 2026-10-21 #28: the edit and delete author rules were
// enforced but had no negative test, so a refactor could drop them unnoticed.
// Edit is sender-only. Delete is sender, or a group admin/owner; a plain
// member — and anyone in a direct conversation who is not the sender — is
// refused before any store write.

type authorRuleMsgStore struct {
	previewMsgStore
	edits int
}

func (m *authorRuleMsgStore) UpdateMessageText(context.Context, uuid.UUID, string, time.Time, uuid.UUID, string, time.Time) error {
	m.edits++
	return nil
}

// authorRuleConvStore adds the inbox-preview refresh a successful edit makes.
type authorRuleConvStore struct {
	*previewConvStore
}

func (c *authorRuleConvStore) SetLastMessage(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error {
	return nil
}

func authorRuleFixture(convType string, sender uuid.UUID, members map[uuid.UUID]string) (*governanceFake, *authorRuleConvStore, *authorRuleMsgStore, *scylla.Message) {
	f := newGovernanceFake()
	convID := uuid.New()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: convType}
	f.roles[convID] = map[uuid.UUID]string{}
	for userID, role := range members {
		f.roles[convID][userID] = role
	}
	msg := &scylla.Message{
		MsgID: uuid.New(), ConversationID: convID, SenderID: sender,
		Bucket: "2026-10", Ts: time.Now().UTC().Add(-time.Minute), Type: "text", Text: "original",
	}
	conv := &authorRuleConvStore{previewConvStore: newPreviewConvStore(f)}
	msgs := &authorRuleMsgStore{previewMsgStore: previewMsgStore{target: msg}}
	return f, conv, msgs, msg
}

func newAuthorRuleService(conv *authorRuleConvStore, msgs *authorRuleMsgStore) *Service {
	svc := newPreviewService(conv.previewConvStore, &msgs.previewMsgStore)
	svc.convStore = conv
	svc.msgStore = msgs
	return svc
}

func TestEditMessageIsSenderOnly(t *testing.T) {
	sender, other := uuid.New(), uuid.New()
	_, conv, msgs, msg := authorRuleFixture("direct", sender, map[uuid.UUID]string{sender: "member", other: "member"})
	svc := newAuthorRuleService(conv, msgs)

	if _, err := svc.EditMessage(context.Background(), other, msg.ConversationID, msg.MsgID, msg.Bucket, msg.Ts, "rewritten"); err == nil {
		t.Fatal("a member who is not the sender edited the message")
	}
	if msgs.edits != 0 {
		t.Fatalf("store written on a refused edit: %d", msgs.edits)
	}

	// A group admin may delete, but never rewrite, someone else's words.
	admin := uuid.New()
	_, gconv, gmsgs, gmsg := authorRuleFixture("group", sender, map[uuid.UUID]string{sender: "member", admin: "admin"})
	gsvc := newAuthorRuleService(gconv, gmsgs)
	if _, err := gsvc.EditMessage(context.Background(), admin, gmsg.ConversationID, gmsg.MsgID, gmsg.Bucket, gmsg.Ts, "rewritten"); err == nil {
		t.Fatal("a group admin edited another member's message")
	}
	if gmsgs.edits != 0 {
		t.Fatalf("store written on a refused admin edit: %d", gmsgs.edits)
	}

	// The sender's own edit reaches the store.
	if _, err := gsvc.EditMessage(context.Background(), sender, gmsg.ConversationID, gmsg.MsgID, gmsg.Bucket, gmsg.Ts, "rewritten"); err != nil {
		t.Fatalf("sender edit refused: %v", err)
	}
	if gmsgs.edits != 1 {
		t.Fatalf("sender edit not written: %d", gmsgs.edits)
	}
}

func TestDeleteMessageIsSenderOrGroupAdmin(t *testing.T) {
	sender, other := uuid.New(), uuid.New()

	// Direct conversation: the other party is a member but not the sender.
	_, conv, msgs, msg := authorRuleFixture("direct", sender, map[uuid.UUID]string{sender: "member", other: "member"})
	svc := newAuthorRuleService(conv, msgs)
	if err := svc.DeleteMessage(context.Background(), other, msg.ConversationID, msg.MsgID, msg.Bucket, msg.Ts); err == nil {
		t.Fatal("the other party of a direct conversation deleted the sender's message")
	}
	if msgs.deletes != 0 || len(conv.obligations) != 0 || msg.IsDeleted {
		t.Fatalf("refused delete touched the stores: deletes=%d obligations=%d", msgs.deletes, len(conv.obligations))
	}

	// Group: a plain member is refused, an admin is allowed.
	member, admin := uuid.New(), uuid.New()
	_, gconv, gmsgs, gmsg := authorRuleFixture("group", sender, map[uuid.UUID]string{sender: "member", member: "member", admin: "admin"})
	gsvc := newAuthorRuleService(gconv, gmsgs)
	if err := gsvc.DeleteMessage(context.Background(), member, gmsg.ConversationID, gmsg.MsgID, gmsg.Bucket, gmsg.Ts); err == nil {
		t.Fatal("a plain group member deleted another member's message")
	}
	if gmsgs.deletes != 0 || len(gconv.obligations) != 0 {
		t.Fatalf("refused member delete touched the stores: deletes=%d obligations=%d", gmsgs.deletes, len(gconv.obligations))
	}
	if err := gsvc.DeleteMessage(context.Background(), admin, gmsg.ConversationID, gmsg.MsgID, gmsg.Bucket, gmsg.Ts); err != nil {
		t.Fatalf("group admin delete refused: %v", err)
	}
	if gmsgs.deletes != 1 || !gmsg.IsDeleted {
		t.Fatalf("admin delete not applied: deletes=%d", gmsgs.deletes)
	}

	// Non-members are refused before the message is even read.
	stranger := uuid.New()
	_, sconv, smsgs, smsg := authorRuleFixture("group", sender, map[uuid.UUID]string{sender: "member"})
	ssvc := newAuthorRuleService(sconv, smsgs)
	if err := ssvc.DeleteMessage(context.Background(), stranger, smsg.ConversationID, smsg.MsgID, smsg.Bucket, smsg.Ts); err == nil {
		t.Fatal("a non-member deleted a message")
	}
	if _, err := ssvc.EditMessage(context.Background(), stranger, smsg.ConversationID, smsg.MsgID, smsg.Bucket, smsg.Ts, "rewritten"); err == nil {
		t.Fatal("a non-member edited a message")
	}
}
