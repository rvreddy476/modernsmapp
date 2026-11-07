package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
)

// pagingMsgStore answers GetMessages from a script and records each read.
type pagingMsgStore struct {
	*previewMsgStore
	pages []pagingAnswer
	reads []pagingRead
}

type pagingAnswer struct {
	msgs []scylla.Message
	next *scylla.MessageCursor
}

type pagingRead struct {
	cursor *scylla.MessageCursor
	oldest string
}

func (m *pagingMsgStore) GetMessages(_ context.Context, _ uuid.UUID, cursor *scylla.MessageCursor, _ int, oldest string) ([]scylla.Message, *scylla.MessageCursor, error) {
	m.reads = append(m.reads, pagingRead{cursor, oldest})
	if len(m.pages) == 0 {
		return nil, nil, nil
	}
	a := m.pages[0]
	m.pages = m.pages[1:]
	return a.msgs, a.next, nil
}

// M3: history paging is bounded by the conversation's start, not a fixed 12
// months. The service passes that start to every read and keeps reading
// from a bucket-only resume point, so a long quiet run is not the end of
// the chat.
func TestHistoryPagingFollowsResumePointsToTheConversationStart(t *testing.T) {
	ctx := context.Background()
	f := newGovernanceFake()
	viewer, convID := uuid.New(), uuid.New()
	f.addGroup(convID, viewer, nil)
	f.conversations[convID].CreatedAt = time.Date(2024, time.March, 31, 23, 0, 0, 0, time.UTC)
	old := scylla.Message{ConversationID: convID, Bucket: "202405", Ts: time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC),
		MsgID: uuid.New(), SenderID: uuid.New(), Type: "text", Text: "long ago"}
	msgs := &pagingMsgStore{previewMsgStore: &previewMsgStore{}, pages: []pagingAnswer{
		{nil, &scylla.MessageCursor{Bucket: "202506"}}, // twelve empty months
		{[]scylla.Message{old}, nil},
	}}
	svc := &Service{convStore: f, msgStore: msgs, log: slog.Default()}

	out, next, err := svc.GetMessages(ctx, viewer, convID, nil, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].MsgID != old.MsgID || next != nil {
		t.Fatalf("page = %d messages, cursor %+v; want the old message and the end of history", len(out), next)
	}
	if len(msgs.reads) != 2 || msgs.reads[1].cursor == nil || msgs.reads[1].cursor.Bucket != "202506" {
		t.Fatalf("reads = %+v, want a second read from the resume point", msgs.reads)
	}
	for _, r := range msgs.reads {
		if r.oldest != "202402" {
			t.Fatalf("read bounded by %q, want the month before the conversation began (202402)", r.oldest)
		}
	}
}

func TestHistoryOldestBucket(t *testing.T) {
	for created, want := range map[time.Time]string{
		time.Date(2026, time.March, 31, 12, 0, 0, 0, time.UTC):                  "202602", // no day-of-month overflow
		time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC):                  "202512",
		time.Date(2026, time.July, 1, 3, 0, 0, 0, time.FixedZone("IST", 19800)): "202605", // 30 Jun UTC
	} {
		if got := historyOldestBucket(&postgres.Conversation{CreatedAt: created}); got != want {
			t.Errorf("created %v: oldest bucket %q, want %q", created, got, want)
		}
	}
	if got := historyOldestBucket(&postgres.Conversation{}); got != "" {
		t.Errorf("unknown start: %q, want \"\"", got)
	}
}

// cutoffConv answers the two parity reads applyHistoryCutoff makes.
type cutoffConv struct {
	ConversationStore
	parityStore
	cut time.Time
}

func (c cutoffConv) GetMemberCutoff(context.Context, uuid.UUID, uuid.UUID) (*time.Time, error) {
	return &c.cut, nil
}
func (c cutoffConv) HiddenMessageIDs(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) (map[uuid.UUID]bool, error) {
	return nil, nil
}

// A resume point in a month before the member's cleared-history cut-off
// leads only to history they cleared: paging ends instead.
func TestHistoryResumePointBeforeTheCutoffEndsPaging(t *testing.T) {
	ctx := context.Background()
	svc := &Service{convStore: cutoffConv{cut: time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)}, log: slog.Default()}
	for bucket, wantNext := range map[string]bool{"202602": false, "202603": true, "202604": true} {
		_, next, err := svc.applyHistoryCutoff(ctx, uuid.New(), uuid.New(), nil, &scylla.MessageCursor{Bucket: bucket})
		if err != nil {
			t.Fatal(err)
		}
		if (next != nil) != wantNext {
			t.Errorf("resume at %s: cursor %+v, want kept=%v", bucket, next, wantNext)
		}
	}
}
