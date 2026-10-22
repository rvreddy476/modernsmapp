package postgres

import (
	"context"
	"time"
)

// PruneOutboxEvents deletes up to limit outbox rows that were published
// before publishedBefore and returns how many it removed (retention, 22 Oct
// 2026). Unpublished rows are never touched. A published row has done its
// job — Kafka has the event — and kept forever it was a copy of every
// message's text and preview.
//
// The dedupe key goes with the row. Inside the window nothing re-inserts an
// event for a completed delivery (the send path returns early on a completed
// intent and repair only replays incomplete ones), so the window, not the
// row's lifetime, is what deduplicates.
func (s *ConversationStore) PruneOutboxEvents(ctx context.Context, publishedBefore time.Time, limit int) (int64, error) {
	if limit <= 0 {
		return 0, nil
	}
	tag, err := s.db.Exec(ctx, `
		DELETE FROM chat.outbox_events
		WHERE id IN (
			SELECT id FROM chat.outbox_events
			WHERE published_at IS NOT NULL AND published_at < $1
			ORDER BY published_at
			LIMIT $2
		)
	`, publishedBefore, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
