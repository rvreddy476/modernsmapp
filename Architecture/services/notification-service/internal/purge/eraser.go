package purge

import (
	"context"

	"github.com/google/uuid"
)

// InboxStore is the Scylla slice (notifications_by_user partitions).
type InboxStore interface {
	DeleteAllNotificationsForUser(ctx context.Context, userID uuid.UUID) error
}

// PrefsStore is the Postgres slice.
type PrefsStore interface {
	PurgeUser(ctx context.Context, userID uuid.UUID) error
}

// Eraser runs the idempotent Scylla delete first, then the Postgres
// transaction. Satisfies the Eraser interface.
type StoreEraser struct {
	inbox InboxStore
	pg    PrefsStore
}

// NewEraser builds the composite eraser; either store may be nil.
func NewEraser(inbox InboxStore, pg PrefsStore) *StoreEraser { return &StoreEraser{inbox: inbox, pg: pg} }

// PurgeUser implements the notification-service erase.
func (e *StoreEraser) PurgeUser(ctx context.Context, userID uuid.UUID) error {
	if e.inbox != nil {
		if err := e.inbox.DeleteAllNotificationsForUser(ctx, userID); err != nil {
			return err
		}
	}
	if e.pg != nil {
		return e.pg.PurgeUser(ctx, userID)
	}
	return nil
}
