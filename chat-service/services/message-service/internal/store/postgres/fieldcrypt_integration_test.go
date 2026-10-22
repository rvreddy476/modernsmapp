//go:build integration

package postgres

// Field encryption at rest against a real Postgres (a *_test database only):
// columns hold ciphertext, reads return plaintext, legacy rows still read,
// the backfill seals legacy rows once (JSONB included, legacy outbox rows get
// a dedupe key), the KMS key-ring table refuses a second first key, and the
// outbox retention deletes only old published rows.
//
// Run: CHAT_POSTGRES_DSN=postgres://.../chat_it_test go test ./internal/store/postgres -tags integration -run FieldCrypt

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/fieldbackfill"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/google/uuid"
)

func TestFieldCryptColumnsHoldCiphertextAndReadBack(t *testing.T) {
	store, pool := disappearingStore(t)
	store.SetFieldCipher(testCipher(t))
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, a, b)

	if err := store.SetLastMessage(ctx, convID, a, secret, time.Now()); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := pool.QueryRow(ctx, `SELECT last_message_preview FROM chat.conversations WHERE id = $1`, convID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !fieldcrypt.IsSealed(raw) || strings.Contains(raw, "banyan") {
		t.Fatalf("the column holds %q", raw)
	}
	got, err := store.GetConversation(ctx, convID)
	if err != nil || got.LastMessagePreview != secret {
		t.Fatalf("read back %q, %v", got.LastMessagePreview, err)
	}

	// A row written before encryption reads unchanged.
	if _, err := pool.Exec(ctx, `UPDATE chat.conversations SET last_message_preview = 'old plaintext' WHERE id = $1`, convID); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.GetConversation(ctx, convID); got.LastMessagePreview != "old plaintext" {
		t.Fatalf("legacy row read as %q", got.LastMessagePreview)
	}

	// JSONB: the cached response is a JSON string holding the sealed value.
	key := "fc-" + uuid.NewString()
	if _, err := store.CreateIdempotencyKey(ctx, key, "h"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveIdempotencyResponse(ctx, key, "h", map[string]string{"text": secret}); err != nil {
		t.Fatal(err)
	}
	var rawResp string
	_ = pool.QueryRow(ctx, `SELECT response::text FROM chat.idempotency_keys WHERE key = $1`, key).Scan(&rawResp)
	if !fieldcrypt.IsSealedJSON([]byte(rawResp)) {
		t.Fatalf("response column = %.40q", rawResp)
	}
	res, err := store.CheckIdempotencyKey(ctx, key)
	if err != nil || !strings.Contains(string(res.Response), "banyan") {
		t.Fatalf("response read back as %s, %v", res.Response, err)
	}
}

func TestFieldCryptBackfillSealsLegacyRowsOnce(t *testing.T) {
	store, pool := disappearingStore(t)
	c := testCipher(t)
	store.SetFieldCipher(c)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, a, b)
	if _, err := pool.Exec(ctx, `UPDATE chat.conversations SET last_message_preview = $2 WHERE id = $1`, convID, secret); err != nil {
		t.Fatal(err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx, `INSERT INTO chat.outbox_events (event_type, payload) VALUES ('MessageCreated', $1) RETURNING id`,
		[]byte(`{"text":"`+secret+`"}`)).Scan(&outboxID); err != nil {
		t.Fatal(err)
	}
	if err := store.ResetProgress(ctx); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := fieldbackfill.Run(ctx, c, store.BackfillTargets(), store, fieldbackfill.Options{Batch: 50}, quiet); err != nil {
		t.Fatal(err)
	}
	var preview, payload string
	var dedupe *string
	_ = pool.QueryRow(ctx, `SELECT last_message_preview FROM chat.conversations WHERE id = $1`, convID).Scan(&preview)
	_ = pool.QueryRow(ctx, `SELECT payload::text, dedupe_key FROM chat.outbox_events WHERE id = $1`, outboxID).Scan(&payload, &dedupe)
	if !fieldcrypt.IsSealed(preview) || !fieldcrypt.IsSealedJSON([]byte(payload)) || dedupe == nil || *dedupe == "" {
		t.Fatalf("after backfill: preview sealed=%v payload sealed=%v dedupe=%v", fieldcrypt.IsSealed(preview), fieldcrypt.IsSealedJSON([]byte(payload)), dedupe)
	}
	if got, _ := store.GetConversation(ctx, convID); got.LastMessagePreview != secret {
		t.Fatalf("backfilled preview reads %q", got.LastMessagePreview)
	}
	events, err := store.FetchUnpublishedOutboxEvents(ctx, 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.ID == outboxID {
			found = true
			var p map[string]string
			if json.Unmarshal(e.Payload, &p) != nil || p["text"] != secret {
				t.Fatalf("backfilled outbox payload reads %s", e.Payload)
			}
		}
	}
	if !found {
		t.Fatal("the backfilled outbox row did not open for the relay")
	}
	reports, err := fieldbackfill.Run(ctx, c, store.BackfillTargets(), store, fieldbackfill.Options{Batch: 50, Restart: true}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		if r.Sealed != 0 {
			t.Fatalf("second run re-sealed %d rows of %s", r.Sealed, r.Name)
		}
	}
}

func TestFieldCryptKeyRingTableAllowsOneFirstKey(t *testing.T) {
	_, pool := disappearingStore(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM chat.field_key_ring`); err != nil {
		t.Fatal(err)
	}
	ring := NewFieldKeyRingStore(pool)
	kc := map[string]string{"purpose": "chat-field", "scope": "chat.field", "environment": "test"}
	if err := ring.CreateFirstWrappedKey(ctx, fieldcrypt.WrappedKey{Wrapped: []byte("w1"), KMSKeyID: "k", Context: kc}); err != nil {
		t.Fatal(err)
	}
	if err := ring.CreateFirstWrappedKey(ctx, fieldcrypt.WrappedKey{Wrapped: []byte("w2"), KMSKeyID: "k", Context: kc}); !errors.Is(err, fieldcrypt.ErrActiveKeyExists) {
		t.Fatalf("a second first key was accepted: %v", err)
	}
	v, err := ring.RotateWrappedKey(ctx, fieldcrypt.WrappedKey{Wrapped: []byte("w3"), KMSKeyID: "k", Context: kc})
	if err != nil || v != 2 {
		t.Fatalf("rotate = %d, %v", v, err)
	}
	keys, err := ring.ListWrappedKeys(ctx)
	if err != nil || len(keys) != 2 || string(keys[0].Wrapped) != "w1" || keys[1].Context["environment"] != "test" {
		t.Fatalf("keys = %+v, %v", keys, err)
	}
	var active int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM chat.field_key_ring WHERE active`).Scan(&active)
	if active != 1 {
		t.Fatalf("%d active keys", active)
	}
}

func TestFieldCryptOutboxRetentionDeletesOnlyOldPublishedRows(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	ins := func(published *time.Time) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO chat.outbox_events (event_type, payload, published_at) VALUES ('X', '{}', $1) RETURNING id`, published).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	oldID, recentID, pendingID := ins(&old), ins(&recent), ins(nil)
	if _, err := store.PruneOutboxEvents(ctx, time.Now().Add(-7*24*time.Hour), 10000); err != nil {
		t.Fatal(err)
	}
	exists := func(id int64) bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM chat.outbox_events WHERE id = $1`, id).Scan(&n)
		return n == 1
	}
	if exists(oldID) || !exists(recentID) || !exists(pendingID) {
		t.Fatalf("old=%v recent=%v pending=%v; want false true true", exists(oldID), exists(recentID), exists(pendingID))
	}
}
