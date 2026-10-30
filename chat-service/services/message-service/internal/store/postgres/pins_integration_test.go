//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Audit 2026-10-30 L5: the 3-pin cap holds under concurrent pins (the count
// and the insert share the conversation row lock), and re-pinning a message
// returns its existing pin instead of using up the cap.
func TestPinCapHoldsUnderConcurrentPins(t *testing.T) {
	store, pool := disappearingStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	convID := seedDirect(t, pool, a, b)

	const racers = 12
	var wg sync.WaitGroup
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.PinMessage(ctx, convID, uuid.New(), a)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok, limited := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrPinLimitReached):
			limited++
		default:
			t.Fatalf("unexpected pin error: %v", err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM chat.conversation_pins WHERE conversation_id = $1`, convID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != MaxPinsPerConversation || ok != MaxPinsPerConversation || limited != racers-MaxPinsPerConversation {
		t.Fatalf("pins stored=%d accepted=%d refused=%d, want %d/%d/%d", count, ok, limited,
			MaxPinsPerConversation, MaxPinsPerConversation, racers-MaxPinsPerConversation)
	}

	// Re-pinning a pinned message returns the same pin, even at the cap.
	pins, err := store.GetConversationPins(ctx, convID)
	if err != nil || len(pins) == 0 {
		t.Fatalf("pins: %v %d", err, len(pins))
	}
	again, err := store.PinMessage(ctx, convID, pins[0].MessageID, b)
	if err != nil || again.ID != pins[0].ID {
		t.Fatalf("re-pin = %+v %v, want the existing pin %s", again, err, pins[0].ID)
	}
}
