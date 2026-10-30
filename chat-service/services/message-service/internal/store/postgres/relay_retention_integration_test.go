//go:build integration

package postgres

// Chat backend audit (30 Oct 2026) against a real Postgres (a *_test
// database only): outbox claims and dead letters (H3/H4), idempotency
// takeover and purge (M1), the scheduled-message cap and fair claim (M2),
// delivery intent compaction (M8) and the online index build (L9).
//
// Run: CHAT_POSTGRES_DSN=postgres://.../chat_it_test go test ./internal/store/postgres -tags integration -run 'Relay|Idem|Scheduled|Compact|Online'

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func rrExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func TestRelayClaimsNeverOverlapAndDeadLettersAreSkipped(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	rrExec(t, pool, `DELETE FROM chat.outbox_events`)
	for i := 0; i < 40; i++ {
		if err := store.InsertOutboxEvent(ctx, "MemberAdded", map[string]string{"conversation_id": "c"}); err != nil {
			t.Fatal(err)
		}
	}

	// Two replicas claim at once: disjoint sets covering every row.
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := store.FetchUnpublishedOutboxEvents(ctx, 25)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, e := range got {
				seen[e.ID]++
			}
		}()
	}
	wg.Wait()
	if len(seen) != 40 {
		t.Fatalf("claimed %d distinct rows of 40", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("row %d claimed %d times", id, n)
		}
	}
	// Leased rows are not claimed again.
	if again, _ := store.FetchUnpublishedOutboxEvents(ctx, 100); len(again) != 0 {
		t.Fatalf("re-claimed %d leased rows", len(again))
	}

	// Expire the leases; publish all but two, dead-letter one, fail one transiently.
	rrExec(t, pool, `UPDATE chat.outbox_events SET claimed_until = NOW() - INTERVAL '1 second'`)
	rows, err := store.FetchUnpublishedOutboxEvents(ctx, 100)
	if err != nil || len(rows) != 40 {
		t.Fatalf("got %d, %v", len(rows), err)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].ID <= rows[i-1].ID {
			t.Fatal("claimed rows are not in id order")
		}
	}
	poison, flaky := rows[0].ID, rows[1].ID
	var ids []int64
	for _, e := range rows[2:] {
		ids = append(ids, e.ID)
	}
	if err := store.MarkOutboxEventsPublished(ctx, ids); err != nil {
		t.Fatal(err)
	}
	if dead, err := store.RecordOutboxEventFailure(ctx, poison, OutboxFailure{Reason: "too large", Permanent: true}); err != nil || !dead {
		t.Fatalf("dead=%v err=%v", dead, err)
	}
	if dead, err := store.RecordOutboxEventFailure(ctx, flaky, OutboxFailure{Reason: "connection refused"}); err != nil || dead {
		t.Fatalf("dead=%v err=%v", dead, err)
	}
	next, err := store.FetchUnpublishedOutboxEvents(ctx, 100)
	if err != nil || len(next) != 1 || next[0].ID != flaky {
		t.Fatalf("after one dead letter and one transient failure the relay claimed %+v, %v", next, err)
	}
	var attempts int
	var lastErr string
	var failedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT attempts, last_error, failed_at FROM chat.outbox_events WHERE id = $1`, poison).Scan(&attempts, &lastErr, &failedAt); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || lastErr != "too large" || failedAt == nil {
		t.Fatalf("dead letter: attempts=%d last_error=%q failed_at=%v", attempts, lastErr, failedAt)
	}

	// Counted failures back off and dead-letter at OutboxMaxAttempts.
	for i := 0; i < OutboxMaxAttempts; i++ {
		rrExec(t, pool, `UPDATE chat.outbox_events SET claimed_until = NULL WHERE id = $1`, flaky)
		if _, err := store.RecordOutboxEventFailure(ctx, flaky, OutboxFailure{Reason: "broker refused", Counted: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT failed_at FROM chat.outbox_events WHERE id = $1`, flaky).Scan(&failedAt); err != nil || failedAt == nil {
		t.Fatalf("counted failures did not dead-letter: %v %v", failedAt, err)
	}
}

func TestRelayLockAdmitsOneReplica(t *testing.T) {
	store, _ := disappearingStore(t)
	ctx := context.Background()
	release, ok, err := store.TryOutboxRelayLock(ctx)
	if err != nil || !ok {
		t.Fatalf("first lock ok=%v err=%v", ok, err)
	}
	_, ok2, err := store.TryOutboxRelayLock(ctx)
	if err != nil || ok2 {
		t.Fatalf("second replica got the lock too (ok=%v err=%v)", ok2, err)
	}
	release()
	release3, ok3, err := store.TryOutboxRelayLock(ctx)
	if err != nil || !ok3 {
		t.Fatalf("lock not released (ok=%v err=%v)", ok3, err)
	}
	release3()
}

func TestIdempotencyKeysAreTakenOverAndPurged(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	key := func() string { return "it-idem-" + uuid.NewString() }

	// Expired: any request takes it over.
	expired := key()
	if ok, err := store.CreateIdempotencyKey(ctx, expired, "h1"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := store.SaveIdempotencyResponse(ctx, expired, "h1", map[string]string{"ok": "1"}); err != nil {
		t.Fatal(err)
	}
	rrExec(t, pool, `UPDATE chat.idempotency_keys SET expires_at = NOW() - INTERVAL '1 minute' WHERE key = $1`, expired)
	if ok, err := store.CreateIdempotencyKey(ctx, expired, "h2"); err != nil || !ok {
		t.Fatalf("expired key not taken over: %v %v", ok, err)
	}

	// In progress: blocked inside the lease, taken over after it, same hash only.
	stuck := key()
	if ok, _ := store.CreateIdempotencyKey(ctx, stuck, "h"); !ok {
		t.Fatal("fresh key refused")
	}
	if ok, _ := store.CreateIdempotencyKey(ctx, stuck, "h"); ok {
		t.Fatal("an in-progress key inside its lease was taken over")
	}
	rrExec(t, pool, `UPDATE chat.idempotency_keys SET created_at = NOW() - INTERVAL '3 minutes' WHERE key = $1`, stuck)
	if ok, _ := store.CreateIdempotencyKey(ctx, stuck, "other"); ok {
		t.Fatal("an abandoned key was taken over by a different request")
	}
	if ok, _ := store.CreateIdempotencyKey(ctx, stuck, "h"); !ok {
		t.Fatal("an abandoned in-progress key was not taken over after the lease")
	}

	// Tombstone: an unsent send (response nulled, completed intent).
	a, b := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, a, b)
	tomb := key()
	if ok, _ := store.CreateIdempotencyKey(ctx, tomb, "h"); !ok {
		t.Fatal("fresh key refused")
	}
	now := time.Now().UTC()
	if _, err := store.ReserveMessageDeliveryIntent(ctx, MessageDeliveryIntent{IdempotencyKey: tomb, RequestHash: "h", ConversationID: convID,
		SenderID: a, MessageID: uuid.New(), Bucket: now.Format("200601"), MessageTS: now, MessageType: "text", MemberIDs: []uuid.UUID{a, b}}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteMessageDeliveryIntent(ctx, tomb); err != nil {
		t.Fatal(err)
	}
	rrExec(t, pool, `UPDATE chat.idempotency_keys SET created_at = NOW() - INTERVAL '2 days', expires_at = NOW() - INTERVAL '1 day' WHERE key = $1`, tomb)
	if ok, _ := store.CreateIdempotencyKey(ctx, tomb, "h"); ok {
		t.Fatal("a tombstone was taken over: a retry would re-broadcast an unsent message")
	}
	if got, err := store.CheckIdempotencyKey(ctx, tomb); err != nil || got == nil || len(got.Response) != 0 {
		t.Fatalf("expired tombstone must still read as in progress: %+v %v", got, err)
	}

	// Purge: expired non-tombstones go, the tombstone stays (moved out of range).
	gone := key()
	_, _ = store.CreateIdempotencyKey(ctx, gone, "h")
	rrExec(t, pool, `UPDATE chat.idempotency_keys SET expires_at = NOW() - INTERVAL '1 hour' WHERE key = $1`, gone)
	if _, err := store.PurgeExpiredIdempotencyKeys(ctx, time.Now(), 10000); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM chat.idempotency_keys WHERE key = $1`, gone).Scan(&n)
	if n != 0 {
		t.Fatal("an expired key was not purged")
	}
	var infinite bool
	if err := pool.QueryRow(ctx, `SELECT expires_at = 'infinity' FROM chat.idempotency_keys WHERE key = $1`, tomb).Scan(&infinite); err != nil || !infinite {
		t.Fatalf("tombstone purged or left in the expiry range: %v %v", infinite, err)
	}
}

func TestScheduledCapAndFairClaim(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	rrExec(t, pool, `DELETE FROM chat.scheduled_messages`)
	heavy, light := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, heavy, light)
	text := "later"
	past := time.Now().Add(-time.Hour)
	for i := 0; i < MaxPendingScheduledPerUser; i++ {
		if _, err := store.CreateScheduledMessage(ctx, &ScheduledMessage{ConversationID: convID, SenderID: heavy, Type: "text", Content: &text,
			SendAt: past.Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatalf("schedule %d: %v", i, err)
		}
	}
	if _, err := store.CreateScheduledMessage(ctx, &ScheduledMessage{ConversationID: convID, SenderID: heavy, Type: "text", Content: &text, SendAt: past}); !errors.Is(err, ErrScheduledMessageLimit) {
		t.Fatalf("schedule past the cap: %v", err)
	}
	if _, err := store.CreateScheduledMessage(ctx, &ScheduledMessage{ConversationID: convID, SenderID: light, Type: "text", Content: &text,
		SendAt: past.Add(10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetPendingScheduledMessages(ctx, time.Now(), 20)
	if err != nil {
		t.Fatal(err)
	}
	perSender := map[uuid.UUID]int{}
	for _, m := range got {
		perSender[m.SenderID]++
	}
	if perSender[heavy] != ScheduledPerSenderPerClaim || perSender[light] != 1 {
		t.Fatalf("claim per sender = %v; the light sender was starved", perSender)
	}
	// A second replica gets different rows.
	other, err := store.GetPendingScheduledMessages(ctx, time.Now(), 20)
	if err != nil {
		t.Fatal(err)
	}
	firstIDs := map[uuid.UUID]bool{}
	for _, m := range got {
		firstIDs[m.ID] = true
	}
	for _, m := range other {
		if firstIDs[m.ID] {
			t.Fatalf("row %s claimed twice", m.ID)
		}
	}
}

func TestCompactionKeepsPendingDisappearingIntents(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	// Compaction waits for its partial index (migration 020).
	if err := BuildConcurrentIndexes(ctx, pool, database.ConcurrentIndexes, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	a, b := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, a, b)
	old := time.Now().Add(-30 * 24 * time.Hour).UTC()
	mk := func() MessageDeliveryIntent {
		in := MessageDeliveryIntent{IdempotencyKey: "it-compact-" + uuid.NewString(), RequestHash: "h", ConversationID: convID, SenderID: a,
			MessageID: uuid.New(), Bucket: old.Format("200601"), MessageTS: old, MessageType: "text", MemberIDs: []uuid.UUID{a, b}}
		if _, err := store.ReserveMessageDeliveryIntent(ctx, in); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteMessageDeliveryIntent(ctx, in.IdempotencyKey); err != nil {
			t.Fatal(err)
		}
		rrExec(t, pool, `UPDATE chat.message_delivery_intents SET completed_at = $2 WHERE idempotency_key = $1`, in.IdempotencyKey, old)
		return in
	}
	plain, disappearing := mk(), mk()
	if err := store.ScheduleMessageExpiry(ctx, MessageExpiry{MessageID: disappearing.MessageID, ConversationID: convID, Bucket: disappearing.Bucket,
		MessageTS: old, SenderID: a, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompactCompletedDeliveryIntents(ctx, time.Now().Add(-7*24*time.Hour), 100000); err != nil {
		t.Fatal(err)
	}
	members := func(key string) int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT cardinality(member_ids) FROM chat.message_delivery_intents WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if members(plain.IdempotencyKey) != 0 {
		t.Fatal("an old completed intent kept its member list")
	}
	if members(disappearing.IdempotencyKey) != 2 {
		t.Fatal("a pending disappearing message lost the members its expiry needs")
	}
	// The intent row itself stays (unsend and tombstones key on it).
	if got, err := store.GetMessageDeliveryIntent(ctx, plain.IdempotencyKey); err != nil || got == nil || got.CompletedAt == nil {
		t.Fatalf("compacted intent: %+v %v", got, err)
	}
}

func TestOnlineIndexBuildIsValidAndIdempotent(t *testing.T) {
	_, pool := disappearingStore(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, ix := range database.ConcurrentIndexes {
		rrExec(t, pool, `DROP INDEX IF EXISTS chat.`+ix.Name)
	}
	for run := 0; run < 2; run++ {
		if err := BuildConcurrentIndexes(ctx, pool, database.ConcurrentIndexes, quiet); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
	}
	for _, ix := range database.ConcurrentIndexes {
		var valid bool
		if err := pool.QueryRow(ctx, `
			SELECT i.indisvalid FROM pg_index i
			JOIN pg_class c ON c.oid = i.indexrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'chat' AND c.relname = $1`, ix.Name).Scan(&valid); err != nil || !valid {
			t.Fatalf("%s: valid=%v err=%v", ix.Name, valid, err)
		}
	}
	// A second replica while the lock is held skips without error.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, concurrentIndexLockKey); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, concurrentIndexLockKey) }()
	if err := BuildConcurrentIndexes(ctx, pool, database.ConcurrentIndexes, quiet); err != nil {
		t.Fatalf("locked-out replica: %v", err)
	}
}

func TestBootstrapIsRepeatableWithMigration019(t *testing.T) {
	_, pool := disappearingStore(t)
	for i := 0; i < 2; i++ {
		if err := BootstrapSchema(context.Background(), pool, database.SetupSQL); err != nil {
			t.Fatalf("bootstrap %d: %v", i, err)
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM information_schema.columns
		WHERE table_schema = 'chat' AND (
		    (table_name = 'outbox_events' AND column_name IN ('claimed_until','attempts','last_error','failed_at'))
		 OR (table_name = 'scheduled_messages' AND column_name = 'claimed_until'))`).Scan(&n); err != nil || n != 5 {
		t.Fatalf("migration 019 columns: %d %v", n, err)
	}
}
