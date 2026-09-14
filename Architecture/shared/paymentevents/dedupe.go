package paymentevents

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Why the dedupe lives in the consumer's own transaction.
//
// A consumer that marks an event "seen" anywhere other than the transaction
// that applies it can lose money: if it dies between the mark and the effect,
// the redelivery finds the mark, skips the event, and a captured payment never
// reaches the order. The inbox row, the money check and the effect must commit
// or roll back together. So this package does not own a table or a
// transaction. Each consumer keeps its own inbox table and implements Inbox
// over it, and ApplyOnce runs the claim and the effect inside the transaction
// the consumer began.

var (
	// ErrDuplicate: the event's inbox row already existed, so it was applied
	// before. The effect did not run. Roll back (nothing was written) and
	// treat the delivery as handled.
	ErrDuplicate = errors.New("paymentevents: payment event already applied")
	// ErrNoEventID: an event without an id cannot be deduped, and one blank
	// key would mask every later event. The effect did not run.
	ErrNoEventID = errors.New("payment event has no event_id")
)

// Claim is what an inbox row records about an event.
type Claim struct {
	EventID   string
	EventType string
	// IntentID is the payments intent; may be empty.
	IntentID string
	// ReferenceID is the consumer's own record (an order).
	ReferenceID uuid.UUID
	AmountMinor int64
	// Currency may be empty; an inbox without the column ignores it.
	Currency string
}

// Inbox is a consumer's own inbox table, reached through the consumer's own
// transaction type (pgx.Tx for both current services).
type Inbox[Tx any] interface {
	// Claim inserts the event's row in tx and reports whether it was new.
	// false means the row already existed. It must not commit.
	Claim(ctx context.Context, tx Tx, c Claim) (fresh bool, err error)
}

// ApplyOnce claims c in tx and, only when the claim is new, runs effect in
// the same tx. It never commits or rolls back; the caller does, so the inbox
// row and the effect share one outcome.
//
// It returns ErrNoEventID for a blank event id, ErrDuplicate when the row
// already existed, the claim's error, or the effect's error.
func ApplyOnce[Tx any](ctx context.Context, tx Tx, inbox Inbox[Tx], c Claim, effect func(ctx context.Context, tx Tx) error) error {
	if strings.TrimSpace(c.EventID) == "" {
		return ErrNoEventID
	}
	fresh, err := inbox.Claim(ctx, tx, c)
	if err != nil {
		return err
	}
	if !fresh {
		return ErrDuplicate
	}
	return effect(ctx, tx)
}
