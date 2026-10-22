package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Field encryption at rest (22 Oct 2026), service side: completed intents
// are never replayed (their content was dropped on completion), unsend
// removes the Postgres copies before the Scylla row, published outbox rows
// are pruned, and nothing writes the old unencrypted Redis message list.

type countingMsgStore struct {
	*previewMsgStore
	creates int
}

func (c *countingMsgStore) CreateMessage(context.Context, *scylla.Message) error {
	c.creates++
	return errors.New("scylla unavailable")
}

func TestACompletedIntentIsNeverReplayedIntoScylla(t *testing.T) {
	msgs := &countingMsgStore{previewMsgStore: &previewMsgStore{}}
	svc := &Service{msgStore: msgs, log: slog.Default()}
	done := time.Now()
	// Completion blanked the stored copy of the content.
	intent := &postgres.MessageDeliveryIntent{MessageID: uuid.New(), ConversationID: uuid.New(), CompletedAt: &done}
	reply := &ReplyRef{ID: uuid.New(), Preview: "the quote"}
	if err := svc.settleDelivery(context.Background(), intent, "the text", reply, []byte(`{"k":1}`)); err != nil {
		t.Fatalf("settling a completed intent failed: %v", err)
	}
	if msgs.creates != 0 {
		t.Fatal("a completed (blanked) intent was replayed into Scylla — it would overwrite the message with an empty one")
	}
	if intent.MessageText != "the text" || intent.ReplyToPreview != "the quote" || string(intent.Extra) != `{"k":1}` {
		t.Fatalf("the response is not built from the request: %+v", intent)
	}

	// An incomplete intent is delivered as before.
	pending := &postgres.MessageDeliveryIntent{MessageID: uuid.New(), ConversationID: uuid.New(), MessageText: "x"}
	if err := svc.settleDelivery(context.Background(), pending, "x", nil, nil); err == nil || msgs.creates != 1 {
		t.Fatalf("an incomplete intent was not delivered: creates=%d err=%v", msgs.creates, err)
	}
}

type redactingConvStore struct {
	*previewConvStore
	journal *[]string
	err     error
	calls   int
}

func (r *redactingConvStore) RedactUnsentMessageRecords(context.Context, uuid.UUID, uuid.UUID) error {
	r.calls++
	*r.journal = append(*r.journal, "pg-redact")
	return r.err
}

func TestUnsendRedactsPostgresCopiesBeforeTheScyllaRow(t *testing.T) {
	fx := newPreviewFixture(t)
	var journal []string
	fx.conv.journal = &journal
	fx.msgs.journal = &journal
	rc := &redactingConvStore{previewConvStore: fx.conv, journal: &journal}
	svc := &Service{convStore: rc, msgStore: fx.msgs, rdb: offlineRedis(), log: slog.Default()}

	if err := svc.DeleteMessage(context.Background(), fx.sender, fx.convID, fx.msgID, "202608", fx.ts); err != nil {
		t.Fatal(err)
	}
	if rc.calls != 1 || len(journal) < 3 || journal[0] != "obligation" || journal[1] != "pg-redact" || journal[2] != "scylla-delete" {
		t.Fatalf("unsend order = %v, want obligation, pg-redact, scylla-delete", journal)
	}

	// A failed redaction deletes nothing, so the client's retry can still
	// reach every copy.
	fx2 := newPreviewFixture(t)
	var journal2 []string
	fx2.conv.journal = &journal2
	fx2.msgs.journal = &journal2
	rc2 := &redactingConvStore{previewConvStore: fx2.conv, journal: &journal2, err: errors.New("postgres down")}
	svc2 := &Service{convStore: rc2, msgStore: fx2.msgs, rdb: offlineRedis(), log: slog.Default()}
	if err := svc2.DeleteMessage(context.Background(), fx2.sender, fx2.convID, fx2.msgID, "202608", fx2.ts); err == nil {
		t.Fatal("unsend reported success although the Postgres copies were not redacted")
	}
	for _, step := range journal2 {
		if step == "scylla-delete" {
			t.Fatal("the Scylla row was deleted although the Postgres redaction failed")
		}
	}
}

type fakePruner struct {
	batches []int64
	cutoffs []time.Time
	limits  []int
}

func (f *fakePruner) PruneOutboxEvents(_ context.Context, before time.Time, limit int) (int64, error) {
	f.cutoffs = append(f.cutoffs, before)
	f.limits = append(f.limits, limit)
	if len(f.batches) == 0 {
		return 0, nil
	}
	n := f.batches[0]
	f.batches = f.batches[1:]
	return n, nil
}

func TestOutboxRetentionDeletesPublishedRowsOlderThanSevenDays(t *testing.T) {
	svc := &Service{log: slog.Default()}
	p := &fakePruner{batches: []int64{1000, 1000, 3}}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	n, err := svc.pruneOutbox(context.Background(), p, OutboxRetention{}.withDefaults(), now)
	if err != nil || n != 2003 {
		t.Fatalf("pruned %d, %v; want 2003", n, err)
	}
	if len(p.cutoffs) != 3 || !p.cutoffs[0].Equal(now.Add(-7*24*time.Hour)) || p.limits[0] != 1000 {
		t.Fatalf("passes = %d, cutoff = %v, limit = %v", len(p.cutoffs), p.cutoffs, p.limits)
	}
}

// The Redis list chat_messages:{conversation} was an unencrypted, never-read
// copy of every message. Nothing may write it again.
func TestNoServiceCodeWritesTheLegacyRedisMessageList(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") {
				continue
			}
			if strings.Contains(code, "LPush(") || strings.Contains(code, "RPush(") {
				t.Fatalf("%s:%d pushes onto a Redis list: %s", f, i+1, code)
			}
		}
	}
}
