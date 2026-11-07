//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Unsend against a real Postgres (a *_test database only): a MessageCreated
// row the relay has not published loses its text and preview but stays
// queued, and the attachment is no longer served to members.
func TestUnsendStripsTheQueuedEventAndRevokesTheAttachment(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	sender, member := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, sender, member)
	msgID, mediaID := uuid.New(), uuid.New()
	dedupe := "message-created:" + msgID.String()
	if err := store.InsertOutboxEventOnce(ctx, dedupe, "MessageCreated", map[string]string{
		"message_id": msgID.String(), "text": "unsent words", "preview": "unsent words",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertMessageMediaReference(ctx, msgID, mediaID, convID); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.ViewerMayAccessChatMedia(ctx, member, mediaID); err != nil || !ok {
		t.Fatalf("control: member access before unsend = %v, %v", ok, err)
	}

	for i := 0; i < 2; i++ { // idempotent: a client retry runs it again
		if err := store.RedactUnsentMessageRecords(ctx, convID, msgID); err != nil {
			t.Fatalf("redact %d: %v", i, err)
		}
	}

	var hasText, hasPreview, published bool
	if err := pool.QueryRow(ctx, `
		SELECT payload ? 'text', payload ? 'preview', published_at IS NOT NULL
		FROM chat.outbox_events WHERE dedupe_key = $1`, dedupe).Scan(&hasText, &hasPreview, &published); err != nil {
		t.Fatal(err)
	}
	if hasText || hasPreview || published {
		t.Fatalf("queued row after unsend: text=%v preview=%v published=%v, want stripped and still queued", hasText, hasPreview, published)
	}
	if ok, err := store.ViewerMayAccessChatMedia(ctx, member, mediaID); err != nil || ok {
		t.Fatalf("member access after unsend = %v, %v; want the attachment revoked", ok, err)
	}
}
