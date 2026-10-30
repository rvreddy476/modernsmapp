package store

import (
	"context"
	"unicode/utf8"
)

// maxDeadLetterError bounds the stored error text.
const maxDeadLetterError = 1000

// RecordPlatformEventDeadLetter parks a platform event the consumer could
// not apply after its retries (migration 004, audit M8). Idempotent per
// (topic, partition, offset): a redelivered message updates its row.
func (s *Store) RecordPlatformEventDeadLetter(ctx context.Context, topic string, partition int, offset int64, eventType string, value []byte, attempts int, lastErr string) error {
	if len(lastErr) > maxDeadLetterError {
		lastErr = lastErr[:maxDeadLetterError]
		for !utf8.ValidString(lastErr) {
			lastErr = lastErr[:len(lastErr)-1]
		}
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO qa_platform_event_dead_letters (event_type, topic, kafka_partition, kafka_offset, value, attempts, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (topic, kafka_partition, kafka_offset) DO UPDATE SET
			attempts = qa_platform_event_dead_letters.attempts + EXCLUDED.attempts,
			last_error = EXCLUDED.last_error,
			resolved_at = NULL`,
		eventType, topic, partition, offset, value, attempts, lastErr)
	return err
}
