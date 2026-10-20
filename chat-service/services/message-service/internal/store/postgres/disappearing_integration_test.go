//go:build integration

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Disappearing messages against a real Postgres (a *_test database only):
// the due filter and batch limit of the claim, replay-safe scheduling, the
// conversation timer and its CHECK, the once-only Postgres side of the
// expiry, and conversation-scoped unpin.

func disappearingStore(t *testing.T) (*ConversationStore, *pgxpool.Pool) {
	t.Helper()
	u, err := url.Parse(os.Getenv("CHAT_POSTGRES_DSN"))
	if err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatal("CHAT_POSTGRES_DSN must name a database whose name ends in _test")
	}
	return testStore(t)
}

func seedDirect(t *testing.T, pool *pgxpool.Pool, a, b uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	convID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by) VALUES ($1, 'direct', $2)`, convID, a); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, role) VALUES ($1, $2, 'member'), ($1, $3, 'member')`, convID, a, b); err != nil {
		t.Fatal(err)
	}
	return convID
}

func TestDisappearClaimTakesOnlyDueRowsUpToTheLimit(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM chat.message_expiries`); err != nil {
		t.Fatal(err)
	}
	a, b := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, a, b)
	now := time.Now()
	sched := func(expires time.Time) uuid.UUID {
		id := uuid.New()
		if err := store.ScheduleMessageExpiry(ctx, MessageExpiry{MessageID: id, ConversationID: convID, Bucket: now.UTC().Format("200601"),
			MessageTS: expires.Add(-24 * time.Hour), SenderID: a, ExpiresAt: expires}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	due := map[uuid.UUID]bool{sched(now.Add(-3 * time.Minute)): true, sched(now.Add(-2 * time.Minute)): true, sched(now.Add(-time.Minute)): true}
	future := sched(now.Add(time.Hour))

	first, err := store.ClaimDueMessageExpiries(ctx, 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("claimed %d, want the limit 2", len(first))
	}
	for _, e := range first {
		if !due[e.MessageID] || e.AttemptCount != 1 {
			t.Fatalf("claimed a row that is not due (or attempt not counted): %+v", e)
		}
	}
	second, err := store.ClaimDueMessageExpiries(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || !due[second[0].MessageID] {
		t.Fatalf("second claim = %+v, want only the remaining due row (leased rows and the future row excluded)", second)
	}
	for _, e := range append(first, second...) {
		if e.MessageID == future {
			t.Fatal("a future expiry was claimed")
		}
	}
	// Replaying the schedule never moves an expiry.
	if err := store.ScheduleMessageExpiry(ctx, MessageExpiry{MessageID: future, ConversationID: convID, Bucket: "x",
		MessageTS: now, SenderID: a, ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if again, _ := store.ClaimDueMessageExpiries(ctx, 10, time.Minute); len(again) != 0 {
		t.Fatalf("a replayed schedule made the future row due: %+v", again)
	}
}

func TestDisappearTimerIsConversationWideAndChecked(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, a, b)

	day := int64(86400000)
	if err := store.SetDisappearTimer(ctx, convID, &day, a); err != nil {
		t.Fatal(err)
	}
	timer, err := store.GetDisappearTimer(ctx, convID)
	if err != nil || timer.AfterMs == nil || *timer.AfterMs != day || timer.UpdatedBy == nil || *timer.UpdatedBy != a {
		t.Fatalf("timer = %+v (%v)", timer, err)
	}
	meta, err := store.GetConversationMeta(ctx, convID)
	if err != nil || meta.DisappearAfterMs == nil || *meta.DisappearAfterMs != day {
		t.Fatalf("send-path meta must carry the timer: %+v %v", meta, err)
	}
	for _, viewer := range []uuid.UUID{a, b} {
		im, err := store.GetInboxMeta(ctx, viewer, []uuid.UUID{convID})
		if err != nil || im[convID].DisappearAfterMs != day {
			t.Fatalf("every member's inbox row shows the same timer: %+v %v", im[convID], err)
		}
	}
	hour := int64(3600000)
	if err := store.SetDisappearTimer(ctx, convID, &hour, a); err == nil {
		t.Fatal("the CHECK constraint must refuse a value outside 24h/7d/90d")
	}
	if err := store.SetDisappearTimer(ctx, convID, nil, b); err != nil {
		t.Fatal(err)
	}
	if timer, _ := store.GetDisappearTimer(ctx, convID); timer.AfterMs != nil {
		t.Fatal("nil must turn the timer off")
	}
}

func TestExpireMessageRecordsRunsOnceAndWipesThePlaintext(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	sender, reader := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, sender, reader)
	msgID, mediaID := uuid.New(), uuid.New()
	ts := time.Now().Add(-25 * time.Hour).UTC().Truncate(time.Microsecond)
	key := "it-" + msgID.String()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO chat.message_delivery_intents (idempotency_key, request_hash, conversation_id, sender_id, message_id, bucket,
		message_ts, message_type, message_text, media_id, member_ids, extra, completed_at)
		VALUES ($1, 'h', $2, $3, $4, $5, $6, 'media', 'secret text', $7, $8, '{"expires_at":"2026-01-01T00:00:00Z"}', NOW())`,
		key, convID, sender, msgID, ts.Format("200601"), ts, mediaID, []uuid.UUID{sender, reader})
	exec(`INSERT INTO chat.idempotency_keys (key, request_hash, response) VALUES ($1, 'h', '{"text":"secret text"}')`, key)
	exec(`INSERT INTO chat.message_media_references (message_id, media_id, conversation_id) VALUES ($1, $2, $3)`, msgID, mediaID, convID)
	exec(`INSERT INTO chat.conversation_shared_items (conversation_id, message_id, kind, media_id, sender_id, sent_at) VALUES ($1, $2, 'media', $3, $4, $5)`,
		convID, msgID, mediaID, sender, ts)
	exec(`INSERT INTO chat.starred_messages (user_id, conversation_id, message_id, message_preview) VALUES ($1, $2, $3, 'secret text')`, reader, convID, msgID)
	exec(`INSERT INTO chat.outbox_events (dedupe_key, event_type, payload, published_at) VALUES ($1, 'MessageCreated', '{"text":"secret text","preview":"secret text","message_id":"x"}', NOW())`,
		"message-created:"+msgID.String())
	exec(`UPDATE chat.conversation_members SET unread_count = 1 WHERE conversation_id = $1 AND user_id = $2`, convID, reader)
	e := MessageExpiry{MessageID: msgID, ConversationID: convID, Bucket: ts.Format("200601"), MessageTS: ts, SenderID: sender, ExpiresAt: ts.Add(24 * time.Hour)}
	if err := store.ScheduleMessageExpiry(ctx, e); err != nil {
		t.Fatal(err)
	}

	payload := map[string]any{"message_id": msgID.String(), "conversation_id": convID.String(), "reason": "expired"}
	members, err := store.ExpireMessageRecords(ctx, e, false, "MessageDeleted", payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("members at send = %v", members)
	}
	scalar := func(q string, args ...any) any {
		t.Helper()
		var v any
		if err := pool.QueryRow(ctx, q, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}
	if n := scalar(`SELECT unread_count FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2`, convID, reader); n != int32(0) {
		t.Fatalf("reader unread = %v, want 0", n)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM chat.message_media_references WHERE message_id = $1`,
		`SELECT COUNT(*) FROM chat.conversation_shared_items WHERE message_id = $1`,
		`SELECT COUNT(*) FROM chat.starred_messages WHERE message_id = $1`,
	} {
		if n := scalar(q, msgID); n != int64(0) {
			t.Fatalf("%s = %v, want 0", q, n)
		}
	}
	if txt := scalar(`SELECT message_text || COALESCE(extra::text, '') FROM chat.message_delivery_intents WHERE message_id = $1`, msgID); txt != "" {
		t.Fatalf("intent still holds plaintext: %v", txt)
	}
	if resp := scalar(`SELECT COALESCE(response::text, '') FROM chat.idempotency_keys WHERE key = $1`, key); resp != "" {
		t.Fatalf("cached response still holds plaintext: %v", resp)
	}
	if p := scalar(`SELECT payload::text FROM chat.outbox_events WHERE dedupe_key = $1`, "message-created:"+msgID.String()); strings.Contains(p.(string), "secret") {
		t.Fatalf("published MessageCreated still holds plaintext: %v", p)
	}
	raw := scalar(`SELECT payload::text FROM chat.outbox_events WHERE dedupe_key = $1 AND event_type = 'MessageDeleted'`, "message-expired:"+msgID.String())
	var ev map[string]any
	_ = json.Unmarshal([]byte(raw.(string)), &ev)
	if ev["reason"] != "expired" || ev["message_id"] != msgID.String() {
		t.Fatalf("MessageDeleted outbox payload = %v", raw)
	}
	if d := scalar(`SELECT deleted_at IS NOT NULL FROM chat.message_expiries WHERE message_id = $1`, msgID); d != true {
		t.Fatal("deleted_at must be set")
	}

	// A retry does not release unread again or queue a second event.
	exec(`UPDATE chat.conversation_members SET unread_count = 1 WHERE conversation_id = $1 AND user_id = $2`, convID, reader)
	if _, err := store.ExpireMessageRecords(ctx, e, false, "MessageDeleted", payload); err != nil {
		t.Fatal(err)
	}
	if n := scalar(`SELECT unread_count FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2`, convID, reader); n != int32(1) {
		t.Fatalf("a retry released unread twice: %v", n)
	}
	if n := scalar(`SELECT COUNT(*) FROM chat.outbox_events WHERE dedupe_key = $1`, "message-expired:"+msgID.String()); n != int64(1) {
		t.Fatalf("MessageDeleted queued %v times", n)
	}

	// A message whose delivery has not completed is refused untouched.
	pendingID := uuid.New()
	exec(`INSERT INTO chat.message_delivery_intents (idempotency_key, request_hash, conversation_id, sender_id, message_id, bucket,
		message_ts, message_type, message_text, member_ids) VALUES ($1, 'h', $2, $3, $4, $5, $6, 'text', 'pending text', $7)`,
		"it-"+pendingID.String(), convID, sender, pendingID, ts.Format("200601"), ts, []uuid.UUID{sender, reader})
	pe := e
	pe.MessageID = pendingID
	if err := store.ScheduleMessageExpiry(ctx, pe); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExpireMessageRecords(ctx, pe, false, "MessageDeleted", payload); !errors.Is(err, ErrExpiryDeliveryPending) {
		t.Fatalf("pending delivery = %v, want ErrExpiryDeliveryPending", err)
	}
	if d := scalar(`SELECT deleted_at IS NULL FROM chat.message_expiries WHERE message_id = $1`, pendingID); d != true {
		t.Fatal("a refused expiry must not be marked deleted")
	}
}

func TestUnpinOnlyRemovesAPinOfTheNamedConversation(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	convA, convB := seedDirect(t, pool, a, b), seedDirect(t, pool, a, b)
	pin, err := store.PinMessage(ctx, convA, uuid.New(), a)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.UnpinMessage(ctx, convB, pin.ID); err != nil || got != nil {
		t.Fatalf("a pin of another conversation must not be removed: %+v %v", got, err)
	}
	if pins, _ := store.GetConversationPins(ctx, convA); len(pins) != 1 {
		t.Fatal("the pin must survive a wrong-conversation unpin")
	}
	if got, err := store.UnpinMessage(ctx, convA, pin.ID); err != nil || got == nil || got.ID != pin.ID {
		t.Fatalf("unpin in its own conversation: %+v %v", got, err)
	}
	if pins, _ := store.GetConversationPins(ctx, convA); len(pins) != 0 {
		t.Fatal("the pin must be gone")
	}
}
