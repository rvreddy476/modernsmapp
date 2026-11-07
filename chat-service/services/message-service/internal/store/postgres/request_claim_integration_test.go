//go:build integration

package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// One first message per pending request, against a real Postgres (a *_test
// database only): sends racing with different idempotency keys reserve
// exactly one intent; the winner's retry still reserves; the losers reserve
// nothing.
func TestRequestFirstMessageClaimHasOneWinnerUnderConcurrency(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	sender, receiver := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, sender, receiver)
	if _, err := pool.Exec(ctx, `UPDATE chat.conversations SET is_request = TRUE WHERE id = $1`, convID); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateMessageRequest(ctx, convID, sender, receiver); err != nil {
		t.Fatal(err)
	}

	const racers = 12
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		winners []string
		other   []error
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			intent := firstRequestIntent(convID, fmt.Sprintf("race-%d", i))
			intent.SenderID = sender
			_, err := store.ReserveMessageDeliveryIntent(ctx, intent)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				winners = append(winners, intent.IdempotencyKey)
			case !errors.Is(err, ErrRequestFirstMessageTaken):
				other = append(other, err)
			}
		}(i)
	}
	wg.Wait()
	if len(other) > 0 {
		t.Fatalf("unexpected reservation errors: %v", other)
	}
	if len(winners) != 1 {
		t.Fatalf("%d sends won the request's one first message, want exactly 1 (%v)", len(winners), winners)
	}
	var intents int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM chat.message_delivery_intents WHERE conversation_id = $1`, convID).Scan(&intents); err != nil {
		t.Fatal(err)
	}
	if intents != 1 {
		t.Fatalf("%d delivery intents reserved, want 1", intents)
	}

	retry := firstRequestIntent(convID, winners[0])
	retry.SenderID = sender
	if _, err := store.ReserveMessageDeliveryIntent(ctx, retry); err != nil {
		t.Fatalf("the winning send's retry was refused: %v", err)
	}
	late := firstRequestIntent(convID, "late")
	late.SenderID = sender
	if _, err := store.ReserveMessageDeliveryIntent(ctx, late); !errors.Is(err, ErrRequestFirstMessageTaken) {
		t.Fatalf("a later send: err = %v, want ErrRequestFirstMessageTaken", err)
	}

	// A finished request reopened later is a new request with its own one
	// message: the old claim does not carry over.
	if err := store.UpdateMessageRequestStatus(ctx, convID, "ignored"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateMessageRequest(ctx, convID, sender, receiver); err != nil {
		t.Fatal(err)
	}
	reopened := firstRequestIntent(convID, "reopened")
	reopened.SenderID = sender
	if _, err := store.ReserveMessageDeliveryIntent(ctx, reopened); err != nil {
		t.Fatalf("the reopened request's first message was refused: %v", err)
	}
}
