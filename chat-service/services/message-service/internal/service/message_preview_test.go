package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// The inbox preview is denormalized from the newest message. Deleting that
// message used to leave its text on every member's conversation list: gone
// from history, still readable on the inbox. These tests pin the repair and,
// just as importantly, pin that it does NOT fire for a message that was not
// the preview source.

type previewConvStore struct {
	*governanceFake
	replaceCalls  int
	replaceErr    error
	lastDeletedTs time.Time
	lastPreview   string
	lastSender    *uuid.UUID
	lastTs        *time.Time
	// currentLastMessageAt models the SQL guard window.
	currentLastMessageAt time.Time
	applied              bool
	appliedCount         int

	// The durable preview-repair obligations (MP-LB-1).
	obligations         map[uuid.UUID]postgres.PreviewRepairObligation
	createObligationErr error
	deferCalls          int
	// journal records write ordering across both fakes.
	journal *[]string
}

func newPreviewConvStore(f *governanceFake) *previewConvStore {
	return &previewConvStore{
		governanceFake: f,
		obligations:    map[uuid.UUID]postgres.PreviewRepairObligation{},
	}
}

func (p *previewConvStore) ReplaceLastMessage(
	_ context.Context,
	_ uuid.UUID,
	deletedTs time.Time,
	preview string,
	senderID *uuid.UUID,
	ts *time.Time,
) error {
	if p.replaceErr != nil {
		return p.replaceErr
	}
	p.replaceCalls++
	p.lastDeletedTs = deletedTs
	p.lastPreview = preview
	p.lastSender = senderID
	p.lastTs = ts
	// Models the store's ONE-MILLISECOND WINDOW guard, not equality: the
	// stored timestamp carries microseconds while the one read back from
	// Scylla is truncated to milliseconds.
	if !p.currentLastMessageAt.Before(deletedTs) &&
		p.currentLastMessageAt.Before(deletedTs.Add(time.Millisecond)) {
		p.applied = true
		p.appliedCount++
	}
	return nil
}

func (p *previewConvStore) CreatePreviewRepairObligation(_ context.Context, conversationID, messageID uuid.UUID, bucket string, deletedTs time.Time) error {
	if p.journal != nil {
		*p.journal = append(*p.journal, "obligation")
	}
	if p.createObligationErr != nil {
		return p.createObligationErr
	}
	// Upsert, like the real store: replay re-arms rather than duplicates.
	o := p.obligations[messageID]
	o.MessageID = messageID
	o.ConversationID = conversationID
	o.Bucket = bucket
	o.DeletedTs = deletedTs
	if o.CreatedAt.IsZero() {
		o.CreatedAt = time.Now()
	}
	p.obligations[messageID] = o
	return nil
}

func (p *previewConvStore) ClaimDuePreviewRepairObligations(_ context.Context, limit int, _ time.Duration) ([]postgres.PreviewRepairObligation, error) {
	var out []postgres.PreviewRepairObligation
	for id, o := range p.obligations {
		o.AttemptCount++
		p.obligations[id] = o
		out = append(out, o)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (p *previewConvStore) CompletePreviewRepairObligation(_ context.Context, messageID uuid.UUID) error {
	delete(p.obligations, messageID)
	return nil
}

func (p *previewConvStore) DeferPreviewRepairObligation(_ context.Context, _ uuid.UUID, _ time.Duration, _ string) error {
	p.deferCalls++
	return nil
}

type previewMsgStore struct {
	target        *scylla.Message
	surviving     []scylla.Message
	deletes       int
	lastReadLim   int
	getMessageErr error
	getPageErr    error
	softDeleteErr error
	journal       *[]string
}

func (m *previewMsgStore) CreateMessage(context.Context, *scylla.Message) error { return nil }
func (m *previewMsgStore) UpsertInbox(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, time.Time) error {
	return nil
}
func (m *previewMsgStore) AddReaction(context.Context, uuid.UUID, string, time.Time, uuid.UUID, string, uuid.UUID) error {
	return nil
}
func (m *previewMsgStore) RemoveReaction(context.Context, uuid.UUID, string, time.Time, uuid.UUID, string, uuid.UUID) error {
	return nil
}
func (m *previewMsgStore) HasReaction(context.Context, uuid.UUID, string, time.Time, uuid.UUID, string, uuid.UUID) (bool, error) {
	return false, nil
}
func (m *previewMsgStore) GetReactionsForMessages(context.Context, uuid.UUID, string, []scylla.MsgKey) (map[uuid.UUID][]scylla.Reaction, error) {
	return nil, nil
}

func (m *previewMsgStore) GetMessage(context.Context, uuid.UUID, string, time.Time, uuid.UUID) (*scylla.Message, error) {
	if m.getMessageErr != nil {
		return nil, m.getMessageErr
	}
	return m.target, nil
}

func (m *previewMsgStore) GetMessages(_ context.Context, _ uuid.UUID, _ *scylla.MessageCursor, limit int) ([]scylla.Message, *scylla.MessageCursor, error) {
	m.lastReadLim = limit
	if m.getPageErr != nil {
		return nil, nil, m.getPageErr
	}
	return m.surviving, nil, nil
}

func (m *previewMsgStore) SoftDeleteMessage(context.Context, uuid.UUID, string, time.Time, uuid.UUID) error {
	if m.journal != nil {
		*m.journal = append(*m.journal, "scylla-delete")
	}
	if m.softDeleteErr != nil {
		return m.softDeleteErr
	}
	m.deletes++
	// Like the real store: the row stays, flagged deleted.
	if m.target != nil {
		m.target.IsDeleted = true
	}
	return nil
}

// A client that cannot connect: DeleteMessage's cache invalidation ignores the
// result, and a refused port fails immediately rather than hanging.
func offlineRedis() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		MaxRetries:  -1, // fail on the first refused dial, don't back off
		DialTimeout: 50 * time.Millisecond,
	})
}

func newPreviewService(conv *previewConvStore, msgs *previewMsgStore) *Service {
	return &Service{convStore: conv, msgStore: msgs, rdb: offlineRedis(), log: slog.Default()}
}

func TestDeletingTheNewestMessageRewritesTheInboxPreview(t *testing.T) {
	sender := uuid.New()
	convID := uuid.New()
	deletedTs := time.Now().UTC()
	survivorTs := deletedTs.Add(-time.Minute)
	survivorSender := uuid.New()

	f := newGovernanceFake()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	f.roles[convID] = map[uuid.UUID]string{sender: "member"}
	conv := newPreviewConvStore(f)
	conv.currentLastMessageAt = deletedTs

	msgs := &previewMsgStore{
		target: &scylla.Message{
			ConversationID: convID, SenderID: sender, Ts: deletedTs, Text: "delete me",
		},
		surviving: []scylla.Message{{
			ConversationID: convID, SenderID: survivorSender, Ts: survivorTs, Text: "the one before",
		}},
	}

	if err := newPreviewService(conv, msgs).DeleteMessage(
		context.Background(), sender, convID, uuid.New(), "202608", deletedTs,
	); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if msgs.deletes != 1 {
		t.Fatalf("expected the message to be soft-deleted once, got %d", msgs.deletes)
	}
	if conv.replaceCalls != 1 {
		t.Fatalf("expected one preview rewrite, got %d", conv.replaceCalls)
	}
	if !conv.applied {
		t.Fatal("the rewrite was not guarded onto the deleted message's timestamp")
	}
	if conv.lastPreview != "the one before" {
		t.Fatalf("preview not recomputed from the newest survivor: %q", conv.lastPreview)
	}
	// Scylla applies LIMIT before filtering soft-deleted rows, so a scan of 1
	// returns only the message just deleted and finds no replacement — which
	// blanked the preview live. The repair must read a window.
	if msgs.lastReadLim < 2 {
		t.Fatalf("survivor lookup asked for %d rows; a deleted tail would blank the preview", msgs.lastReadLim)
	}
	if conv.lastSender == nil || *conv.lastSender != survivorSender {
		t.Fatalf("preview sender not recomputed: %v", conv.lastSender)
	}
	if conv.lastTs == nil || !conv.lastTs.Equal(survivorTs) {
		t.Fatalf("preview timestamp not recomputed: %v", conv.lastTs)
	}
	if !conv.lastDeletedTs.Equal(deletedTs) {
		t.Fatalf("guard timestamp wrong: %v", conv.lastDeletedTs)
	}
}

// Regression, caught live: Postgres stores last_message_at at MICROSECOND
// precision (written from the delivery intent) while the same instant read
// back from Scylla is truncated to MILLISECONDS. An equality guard therefore
// never matched, the rewrite silently no-op'd, and the deleted text stayed on
// every member's inbox.
func TestPreviewRewriteSurvivesScyllaMillisecondTruncation(t *testing.T) {
	sender := uuid.New()
	convID := uuid.New()
	storedTs := time.Date(2026, 8, 26, 20, 38, 45, 971899000, time.UTC) // µs, as Postgres holds it
	truncatedTs := storedTs.Truncate(time.Millisecond)                  // as Scylla returns it
	if storedTs.Equal(truncatedTs) {
		t.Fatal("fixture must differ below the millisecond to exercise the guard")
	}

	f := newGovernanceFake()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	f.roles[convID] = map[uuid.UUID]string{sender: "member"}
	conv := newPreviewConvStore(f)
	conv.currentLastMessageAt = storedTs

	msgs := &previewMsgStore{
		target: &scylla.Message{
			ConversationID: convID, SenderID: sender, Ts: truncatedTs, Text: "delete me",
		},
		surviving: []scylla.Message{{
			ConversationID: convID, SenderID: sender,
			Ts: truncatedTs.Add(-time.Minute), Text: "the one before",
		}},
	}

	if err := newPreviewService(conv, msgs).DeleteMessage(
		context.Background(), sender, convID, uuid.New(), "202608", truncatedTs,
	); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if !conv.applied {
		t.Fatal("millisecond-truncated timestamp missed the guard — the deleted text would stay on the inbox")
	}
	if conv.lastPreview != "the one before" {
		t.Fatalf("preview not recomputed: %q", conv.lastPreview)
	}
}

func TestDeletingTheOnlyMessageClearsTheInboxPreview(t *testing.T) {
	sender := uuid.New()
	convID := uuid.New()
	deletedTs := time.Now().UTC()

	f := newGovernanceFake()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	f.roles[convID] = map[uuid.UUID]string{sender: "member"}
	conv := newPreviewConvStore(f)
	conv.currentLastMessageAt = deletedTs

	msgs := &previewMsgStore{
		target: &scylla.Message{
			ConversationID: convID, SenderID: sender, Ts: deletedTs, Text: "the only one",
		},
		surviving: nil, // nothing left
	}

	if err := newPreviewService(conv, msgs).DeleteMessage(
		context.Background(), sender, convID, uuid.New(), "202608", deletedTs,
	); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if conv.lastPreview != "" {
		t.Fatalf("emptied conversation kept a preview: %q", conv.lastPreview)
	}
	if conv.lastSender != nil || conv.lastTs != nil {
		t.Fatalf("emptied conversation kept last-message metadata: sender=%v ts=%v",
			conv.lastSender, conv.lastTs)
	}
}

// A delete of an OLDER message must not disturb the current preview. The
// service always calls through; the store guard is what makes it a no-op, so
// this pins the guard argument rather than the call count.
func TestDeletingAnOlderMessageLeavesTheInboxPreviewAlone(t *testing.T) {
	sender := uuid.New()
	convID := uuid.New()
	newestTs := time.Now().UTC()
	olderTs := newestTs.Add(-time.Hour)

	f := newGovernanceFake()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	f.roles[convID] = map[uuid.UUID]string{sender: "member"}
	// The inbox preview points at the NEWEST message, not the deleted one.
	conv := newPreviewConvStore(f)
	conv.currentLastMessageAt = newestTs

	msgs := &previewMsgStore{
		target: &scylla.Message{
			ConversationID: convID, SenderID: sender, Ts: olderTs, Text: "an old aside",
		},
		surviving: []scylla.Message{{
			ConversationID: convID, SenderID: sender, Ts: newestTs, Text: "the newest",
		}},
	}

	if err := newPreviewService(conv, msgs).DeleteMessage(
		context.Background(), sender, convID, uuid.New(), "202607", olderTs,
	); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	if conv.applied {
		t.Fatal("deleting an older message rewrote the current inbox preview")
	}
	if !conv.lastDeletedTs.Equal(olderTs) {
		t.Fatalf("guard was not the deleted message's timestamp: %v", conv.lastDeletedTs)
	}
}
