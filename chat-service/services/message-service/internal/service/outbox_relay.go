package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/atpost/chat-message-service/internal/events"
	"github.com/atpost/chat-message-service/internal/purge"
	"github.com/atpost/chat-message-service/internal/store/postgres"
)

// Outbox relay (audit H3/H4, 30 Oct 2026).
//
// Each poll, one replica (the holder of the relay advisory lock) claims
// batches of rows with a lease, publishes each batch in one Kafka write keyed
// by conversation id with a stable event id per row, and marks the batch
// published in one statement. It keeps draining while batches come back full,
// so a backlog clears at Kafka speed instead of one event per second.
//
// A row that can never be published (too large, or a payload that does not
// open OutboxMaxAttempts times) is dead-lettered and skipped, so it cannot
// block the head of the queue. A transient failure (Kafka unreachable)
// releases the claim and ends the pass, so the next poll retries the same
// rows first and per-conversation order holds.
const (
	outboxRelayBatch     = 100
	outboxRelayMaxRounds = 50
)

// outboxRelayStore is the claim/mark/dead-letter half of the relay
// (postgres.ConversationStore). A store without it (unit-test fakes) is
// relayed row by row with MarkOutboxEventPublished.
type outboxRelayStore interface {
	MarkOutboxEventsPublished(ctx context.Context, ids []int64) error
	RecordOutboxEventFailure(ctx context.Context, id int64, f postgres.OutboxFailure) (bool, error)
}

// outboxRelayLocker elects the relaying replica for one pass.
type outboxRelayLocker interface {
	TryOutboxRelayLock(ctx context.Context) (release func(), ok bool, err error)
}

// outboxBatchPublisher is events.Producer.PublishOutbox. A producer without
// it (test fakes) gets one PublishRaw per row.
type outboxBatchPublisher interface {
	PublishOutbox(ctx context.Context, msgs []events.OutboxMessage) []error
}

func (s *Service) relayOutbox(ctx context.Context) {
	if locker, ok := s.convStore.(outboxRelayLocker); ok {
		release, held, err := locker.TryOutboxRelayLock(ctx)
		if err != nil {
			s.log.Error("outbox relay: lock failed", "err", err)
			return
		}
		if !held {
			return // another replica is relaying this pass
		}
		defer release()
	}
	for round := 0; round < outboxRelayMaxRounds && ctx.Err() == nil; round++ {
		n, more := s.relayOutboxBatch(ctx)
		if !more || n < outboxRelayBatch {
			return
		}
	}
}

// relayOutboxBatch claims and publishes one batch. It returns how many rows
// were claimed and whether the pass may continue (false after a transient
// failure or a fetch error).
func (s *Service) relayOutboxBatch(ctx context.Context) (int, bool) {
	rows, err := s.convStore.FetchUnpublishedOutboxEvents(ctx, outboxRelayBatch)
	if err != nil {
		s.log.Error("failed to fetch outbox events", "err", err)
		return 0, false
	}
	if len(rows) == 0 {
		return 0, false
	}
	var chat, acks []postgres.OutboxEvent
	for _, e := range rows {
		if e.EventType == purge.EventUserPurgeAcked {
			acks = append(acks, e)
		} else {
			chat = append(chat, e)
		}
	}
	ok := true
	if len(chat) > 0 && !s.publishOutboxRows(ctx, s.producer, chat, outboxChatKey) {
		ok = false
	}
	if len(acks) > 0 {
		// Account control ack rides the same durable outbox but lands on
		// platform.purge-acks.v1. Left unpublished (retried) when no ack
		// producer is configured — never dropped.
		if s.purgeAckProducer == nil {
			s.log.Error("purge ack outbox rows have no purge-acks producer; leaving unpublished", "count", len(acks))
			s.releaseOutboxRows(ctx, acks, errors.New("no purge-acks producer configured"))
			ok = false
		} else if !s.publishOutboxRows(ctx, s.purgeAckProducer, acks, func(e postgres.OutboxEvent) string {
			return purge.AckUserID(e.Payload)
		}) {
			ok = false
		}
	}
	return len(rows), ok
}

// outboxChatKey is the partition key of a chat event: its conversation id,
// so every event of one conversation lands on one partition in order. An
// event without one round-robins.
func outboxChatKey(e postgres.OutboxEvent) string {
	var p struct {
		ConversationID string `json:"conversation_id"`
	}
	if json.Unmarshal(e.Payload, &p) != nil {
		return ""
	}
	return p.ConversationID
}

// publishOutboxRows publishes rows (already claimed, in id order) through p
// and records the outcome of each. It returns false when a transient failure
// should end this pass.
func (s *Service) publishOutboxRows(ctx context.Context, p EventProducer, rows []postgres.OutboxEvent, key func(postgres.OutboxEvent) string) bool {
	msgs := make([]events.OutboxMessage, len(rows))
	for i, e := range rows {
		msgs[i] = events.OutboxMessage{
			RowID: e.ID, DedupeKey: e.DedupeKey, EventType: e.EventType,
			Key: key(e), OccurredAt: e.CreatedAt, Payload: e.Payload,
		}
	}
	var errs []error
	if bp, ok := p.(outboxBatchPublisher); ok {
		errs = bp.PublishOutbox(ctx, msgs)
	} else {
		errs = make([]error, len(msgs))
		for i, m := range msgs {
			errs[i] = p.PublishRaw(ctx, m.EventType, m.Key, m.Payload)
		}
	}

	published := make([]int64, 0, len(rows))
	transient := false
	for i, e := range rows {
		err := errs[i]
		if err == nil {
			published = append(published, e.ID)
			continue
		}
		f := classifyPublishError(err)
		if !f.Permanent && !f.Counted {
			transient = true
		}
		s.recordOutboxFailure(ctx, e, f)
	}
	s.markOutboxPublished(ctx, published)
	return !transient
}

// classifyPublishError maps a publish error to how the row is treated.
func classifyPublishError(err error) postgres.OutboxFailure {
	f := postgres.OutboxFailure{Reason: err.Error()}
	switch {
	case errors.Is(err, events.ErrMessageTooLarge):
		f.Permanent = true
	case events.IsRecordError(err):
		f.Counted = true
	}
	return f
}

func (s *Service) recordOutboxFailure(ctx context.Context, e postgres.OutboxEvent, f postgres.OutboxFailure) {
	rs, ok := s.convStore.(outboxRelayStore)
	if !ok {
		s.log.Error("failed to publish outbox event", "err", f.Reason, "event_id", e.ID, "event_type", e.EventType)
		return
	}
	dead, err := rs.RecordOutboxEventFailure(ctx, e.ID, f)
	if err != nil {
		s.log.Error("failed to record outbox publish failure", "err", err, "event_id", e.ID)
		return
	}
	if dead {
		// Operator alarm: grep for this message (no metrics stack in this
		// service). The row stays in chat.outbox_events with failed_at set.
		s.log.Error("outbox event dead-lettered", "event_id", e.ID, "event_type", e.EventType,
			"permanent", f.Permanent, "last_error", f.Reason)
		return
	}
	s.log.Warn("failed to publish outbox event; will retry", "event_id", e.ID, "event_type", e.EventType,
		"counted", f.Counted, "err", f.Reason)
}

// releaseOutboxRows gives claimed rows back without counting an attempt.
func (s *Service) releaseOutboxRows(ctx context.Context, rows []postgres.OutboxEvent, cause error) {
	for _, e := range rows {
		s.recordOutboxFailure(ctx, e, postgres.OutboxFailure{Reason: cause.Error()})
	}
}

func (s *Service) markOutboxPublished(ctx context.Context, ids []int64) {
	if len(ids) == 0 {
		return
	}
	if rs, ok := s.convStore.(outboxRelayStore); ok {
		if err := rs.MarkOutboxEventsPublished(ctx, ids); err != nil {
			s.log.Error("failed to mark outbox events published", "err", err, "count", len(ids))
		}
		return
	}
	for _, id := range ids {
		if err := s.convStore.MarkOutboxEventPublished(ctx, id); err != nil {
			s.log.Error("failed to mark outbox event published", "err", err, "event_id", id)
		}
	}
}
