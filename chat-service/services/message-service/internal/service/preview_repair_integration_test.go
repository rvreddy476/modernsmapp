//go:build integration

package service

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Live-PostgreSQL proofs for the MP-LB-1 durable preview-repair obligations:
// the intent row actually persists, claiming leases through FOR UPDATE SKIP
// LOCKED, two workers cannot claim the same obligation, and the REAL
// ReplaceLastMessage statement (not the unit fake's model of it) refuses to
// overwrite a newer preview.
//
// Run with a DISPOSABLE database:
//
//	CHAT_POSTGRES_DSN=postgres://...:.../chat_preview_repair_it?sslmode=disable \
//	  go test -tags integration -run PreviewRepairPG ./internal/service/ -count=1
//
// The suite DROPS AND RECREATES the `chat` schema in that database.
func previewRepairTestStore(t *testing.T) (*pgxpool.Pool, *store.ConversationStore) {
	t.Helper()
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS chat CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatal(err)
	}
	return pool, store.New(pool)
}

func TestPreviewRepairPGObligationPersistsAndReplays(t *testing.T) {
	pool, conv := previewRepairTestStore(t)
	ctx := context.Background()
	conversationID, messageID := uuid.New(), uuid.New()
	// Production stores msg.Ts as read from Scylla, whose timestamps are
	// MILLISECOND precision; timestamptz holds microseconds, so that value
	// round-trips exactly. (A nanosecond-precision Go time would not — which
	// is why the obligation records the Scylla-read timestamp, never a
	// locally generated one.)
	deletedTs := time.Now().UTC().Truncate(time.Millisecond)

	if err := conv.CreatePreviewRepairObligation(ctx, conversationID, messageID, "202608", deletedTs); err != nil {
		t.Fatal(err)
	}
	// Replay of the same deletion: idempotent upsert, still ONE row.
	if err := conv.CreatePreviewRepairObligation(ctx, conversationID, messageID, "202608", deletedTs); err != nil {
		t.Fatal(err)
	}

	var count int
	var storedConv uuid.UUID
	var storedBucket string
	var storedTs time.Time
	if err := pool.QueryRow(ctx, `
		SELECT COUNT(*) OVER (), conversation_id, bucket, deleted_ts
		FROM chat.preview_repair_obligations WHERE message_id = $1
	`, messageID).Scan(&count, &storedConv, &storedBucket, &storedTs); err != nil {
		t.Fatal(err)
	}
	if count != 1 || storedConv != conversationID || storedBucket != "202608" {
		t.Fatalf("durable intent wrong: count=%d conv=%s bucket=%s", count, storedConv, storedBucket)
	}
	// The timestamp the guarded rewrite depends on must round-trip exactly.
	if !storedTs.Equal(deletedTs) {
		t.Fatalf("deleted_ts did not round-trip: stored=%v want=%v", storedTs, deletedTs)
	}

	if err := conv.CompletePreviewRepairObligation(ctx, messageID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat.preview_repair_obligations`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("completed obligation still present: %d", count)
	}
}

func TestPreviewRepairPGClaimLeasesAndBacksOff(t *testing.T) {
	pool, conv := previewRepairTestStore(t)
	ctx := context.Background()
	messageID := uuid.New()
	if err := conv.CreatePreviewRepairObligation(ctx, uuid.New(), messageID, "202608", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	// Make it due now (creation defers by the inline-repair grace).
	if _, err := pool.Exec(ctx, `UPDATE chat.preview_repair_obligations SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}

	claimed, err := conv.ClaimDuePreviewRepairObligations(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].MessageID != messageID || claimed[0].AttemptCount != 1 {
		t.Fatalf("claim wrong: %+v", claimed)
	}

	// The lease holds: a second pass sees nothing due.
	again, err := conv.ClaimDuePreviewRepairObligations(ctx, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("leased obligation was claimed again: %+v", again)
	}

	// Defer records the error and reschedules.
	if err := conv.DeferPreviewRepairObligation(ctx, messageID, time.Hour, "scylla unavailable"); err != nil {
		t.Fatal(err)
	}
	var lastErr string
	var next time.Time
	if err := pool.QueryRow(ctx, `
		SELECT last_error, next_attempt_at FROM chat.preview_repair_obligations WHERE message_id = $1
	`, messageID).Scan(&lastErr, &next); err != nil {
		t.Fatal(err)
	}
	if lastErr != "scylla unavailable" || time.Until(next) < 50*time.Minute {
		t.Fatalf("defer not recorded: last_error=%q next=%v", lastErr, next)
	}
}

// Two replicas racing the same due obligations: FOR UPDATE SKIP LOCKED must
// hand each row to exactly one claimer, with none lost.
func TestPreviewRepairPGTwoWorkersCannotClaimTheSameObligation(t *testing.T) {
	pool, conv := previewRepairTestStore(t)
	ctx := context.Background()
	const rows = 40
	for i := 0; i < rows; i++ {
		if err := conv.CreatePreviewRepairObligation(ctx, uuid.New(), uuid.New(), "202608", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE chat.preview_repair_obligations SET next_attempt_at = now()`); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	seen := map[uuid.UUID]int{}
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				claimed, err := conv.ClaimDuePreviewRepairObligations(ctx, 5, time.Minute)
				if err != nil {
					t.Error(err)
					return
				}
				if len(claimed) == 0 {
					return
				}
				mu.Lock()
				for _, o := range claimed {
					seen[o.MessageID]++
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != rows {
		t.Fatalf("obligations lost: claimed %d of %d", len(seen), rows)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("obligation %s claimed %d times", id, n)
		}
	}
}

// The REAL guarded rewrite: a newer preview outside the millisecond window
// must survive a repair for an older deletion, and a preview still pointing
// at the deleted timestamp must be rewritten. This pins the actual SQL, which
// the unit fake only models.
func TestPreviewRepairPGReplaceGuardAgainstConcurrentDelivery(t *testing.T) {
	pool, conv := previewRepairTestStore(t)
	ctx := context.Background()
	conversationID, creator := uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO chat.conversations (id, type, created_by) VALUES ($1, 'direct', $2)`,
		conversationID, creator); err != nil {
		t.Fatal(err)
	}

	deletedTs := time.Now().UTC().Truncate(time.Millisecond)
	survivorTs := deletedTs.Add(-time.Minute)
	survivor := uuid.New()

	// Case 1: the preview still points at the deleted message (with real
	// microsecond skew inside the window) — rewrite happens.
	if err := conv.SetLastMessage(ctx, conversationID, creator, "deleted plaintext", deletedTs.Add(400*time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if err := conv.ReplaceLastMessage(ctx, conversationID, deletedTs, "the survivor", &survivor, &survivorTs); err != nil {
		t.Fatal(err)
	}
	var preview string
	if err := pool.QueryRow(ctx,
		`SELECT last_message_preview FROM chat.conversations WHERE id = $1`, conversationID,
	).Scan(&preview); err != nil {
		t.Fatal(err)
	}
	if preview != "the survivor" {
		t.Fatalf("guarded rewrite did not repair the deleted preview: %q", preview)
	}

	// Case 2: concurrent delivery moved the preview to a NEWER message —
	// repair for the old deletion must be a no-op.
	newerTs := deletedTs.Add(2 * time.Second)
	if err := conv.SetLastMessage(ctx, conversationID, creator, "the newer message", newerTs); err != nil {
		t.Fatal(err)
	}
	if err := conv.ReplaceLastMessage(ctx, conversationID, deletedTs, "stale repair", &survivor, &survivorTs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT last_message_preview FROM chat.conversations WHERE id = $1`, conversationID,
	).Scan(&preview); err != nil {
		t.Fatal(err)
	}
	if preview != "the newer message" {
		t.Fatalf("repair overwrote a newer preview: %q", preview)
	}
}
