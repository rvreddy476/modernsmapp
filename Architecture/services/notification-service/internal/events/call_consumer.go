package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// errPermanentEvent marks input that can NEVER succeed no matter how often it
// is retried: malformed JSON, an invalid envelope, or invalid required
// fields. ONLY these enter the poison path (CALL-LB-4). A valid event whose
// processing fails on a dependency — database, Redis, device lookup,
// FCM/APNs — is transient and must remain uncommitted until it succeeds or
// the process shuts down; a 30-second outage must never lose a ring.
var errPermanentEvent = errors.New("permanently unprocessable call event")

func permanentf(format string, args ...interface{}) error {
	return fmt.Errorf("%w: "+format, append([]interface{}{errPermanentEvent}, args...)...)
}

// callNotifier is the narrow service seam the consumer needs (same pattern
// as the chat consumer's chatNotifier): the AT-LEAST-ONCE call delivery
// (CALL-LB-4). CreateCallNotification PROPAGATES realtime, device-lookup and
// push failures — unlike the general at-most-once inbox path — so a failed
// delivery keeps this consumer's offset uncommitted and the broker retries;
// the deterministic identity keeps the durable row single and the call
// collapse key keeps retried pushes from stacking. *service.Service
// satisfies it.
type callNotifier interface {
	CreateCallNotification(ctx context.Context, userID, actorID uuid.UUID,
		notifType, entityType string, entityID uuid.UUID, deepLink string,
		createdAt time.Time, identity string) error
}

// callMessageSource is the broker seam: fetch and commit, exactly what
// kafka.Reader provides. An interface so the wiring-guard and live tests
// drive the PRODUCTION Start path, not an extracted helper.
type callMessageSource interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
}

// poisonQuarantine persists a permanently unprocessable message DURABLY
// before its source offset is committed. Logging is not durable evidence.
type poisonQuarantine interface {
	Quarantine(ctx context.Context, m kafka.Message, reason string) error
}

// kafkaDLQ quarantines poison to a dead-letter TOPIC with full acks — the
// record survives the consumer, carries the original bytes, and is
// inspectable/replayable with ordinary tooling.
type kafkaDLQ struct {
	writer *kafka.Writer
}

func newKafkaDLQ(brokers []string, sourceTopic string, dialer *kafka.Dialer) *kafkaDLQ {
	writer := &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Topic:                  sourceTopic + ".dlq",
		RequiredAcks:           kafka.RequireAll,
		AllowAutoTopicCreation: true,
	}
	if dialer != nil {
		// The DLQ writes to the SAME cluster the reader consumes from. On
		// a TLS/SASL-secured broker a plaintext writer can never land the
		// first poison record, and the fail-closed loop would then stall
		// that partition forever — so the writer carries the reader's
		// security transport.
		writer.Transport = &kafka.Transport{
			TLS:  dialer.TLS,
			SASL: dialer.SASLMechanism,
		}
	}
	return &kafkaDLQ{writer: writer}
}

func (q *kafkaDLQ) Quarantine(ctx context.Context, m kafka.Message, reason string) error {
	return q.writer.WriteMessages(ctx, kafka.Message{
		Key:   m.Key,
		Value: m.Value,
		Headers: append(append([]kafka.Header(nil), m.Headers...),
			kafka.Header{Key: "x-quarantine-reason", Value: []byte(reason)},
			kafka.Header{Key: "x-source-topic", Value: []byte(m.Topic)},
			kafka.Header{Key: "x-source-partition", Value: []byte(fmt.Sprintf("%d", m.Partition))},
			kafka.Header{Key: "x-source-offset", Value: []byte(fmt.Sprintf("%d", m.Offset))},
		),
	})
}

func (q *kafkaDLQ) Close() error { return q.writer.Close() }

// CallConsumer handles call-related notification events from the
// call.notifications topic.
type CallConsumer struct {
	source       callMessageSource
	quarantine   poisonQuarantine
	notifier     callNotifier
	retryBackoff time.Duration

	reader *kafka.Reader // nil in tests; owned for Close()
	dlq    *kafkaDLQ     // nil in tests; owned for Close()
}

func NewCallConsumer(brokers []string, groupID string, topic string, svc callNotifier) *CallConsumer {
	return NewCallConsumerWithDialer(brokers, groupID, topic, svc, nil)
}

func NewCallConsumerWithDialer(brokers []string, groupID string, topic string, svc callNotifier, dialer *kafka.Dialer) *CallConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	dlq := newKafkaDLQ(brokers, topic, dialer)
	return &CallConsumer{
		source:       reader,
		quarantine:   dlq,
		notifier:     svc,
		retryBackoff: consumeRetryBackoff,
		reader:       reader,
		dlq:          dlq,
	}
}

// newCallConsumerForTest wires the SAME production Start loop over scripted
// seams — the wiring guard and the live broker tests use it so a regression
// in Start itself (e.g. back to ReadMessage auto-commit) fails them.
func newCallConsumerForTest(source callMessageSource, quarantine poisonQuarantine,
	notifier callNotifier, retryBackoff time.Duration) *CallConsumer {
	return &CallConsumer{
		source:       source,
		quarantine:   quarantine,
		notifier:     notifier,
		retryBackoff: retryBackoff,
	}
}

// Start consumes call events DURABLY (CALL-LB-4): fetch → process → commit.
//
//   - TRANSIENT failure (any dependency error on a valid event): retry the
//     SAME message with capped linear backoff until it succeeds or the
//     context ends. The offset is NEVER committed for an unprocessed valid
//     event — there is no attempt budget that can turn an outage into a
//     permanently lost ring.
//   - PERMANENT failure (errPermanentEvent: malformed JSON / envelope /
//     required fields): the message is written to the durable DLQ topic
//     FIRST; only a successful quarantine write allows the source offset to
//     commit. A failed quarantine write is itself transient.
//   - Shutdown during any retry commits nothing, so the broker redelivers.
func (c *CallConsumer) Start(ctx context.Context) {
	for {
		m, err := c.source.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("Call consumer fetch error: %v\n", err)
			}
			return
		}
		if !c.handleUntilDurable(ctx, m) {
			return // ctx ended mid-retry: nothing committed, broker redelivers
		}
		if err := c.source.CommitMessages(ctx, m); err != nil {
			log.Printf("Call consumer commit error: %v\n", err)
			return
		}
	}
}

// handleUntilDurable returns true only when the message has a durable
// outcome: processed successfully, or quarantined to the DLQ. false means
// the context ended first and NOTHING may be committed.
func (c *CallConsumer) handleUntilDurable(ctx context.Context, m kafka.Message) bool {
	for attempt := 1; ; attempt++ {
		err := c.processMessage(ctx, m)
		if err == nil {
			return true
		}
		if errors.Is(err, errPermanentEvent) {
			qErr := c.quarantine.Quarantine(ctx, m, err.Error())
			if qErr == nil {
				log.Printf("Quarantined permanent poison call message to DLQ (offset %d): %v\n",
					m.Offset, err)
				return true
			}
			// The DLQ write failed: the offset must NOT move past a
			// message with no durable record. Retry as transient.
			err = fmt.Errorf("quarantine poison message: %w", qErr)
		}
		log.Printf("Transient call message failure (attempt %d, offset %d), retrying: %v\n",
			attempt, m.Offset, err)
		backoff := c.retryBackoff * time.Duration(attempt)
		if backoff > maxConsumeRetryBackoff {
			backoff = maxConsumeRetryBackoff
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
	}
}

const (
	consumeRetryBackoff    = 2 * time.Second
	maxConsumeRetryBackoff = 30 * time.Second
)

// parseRequiredUUID rejects a missing or malformed required id as PERMANENT
// input failure — the old code swallowed the parse error and continued with
// uuid.Nil, notifying nobody (or the zero user) forever.
func parseRequiredUUID(field, value string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, permanentf("invalid %s %q: %v", field, value, err)
	}
	if id == uuid.Nil {
		return uuid.Nil, permanentf("nil %s", field)
	}
	return id, nil
}

func (c *CallConsumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope events.EventEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return permanentf("malformed envelope JSON: %v", err)
	}
	if envelope.EventID == "" || envelope.EventType == "" {
		return permanentf("envelope missing event_id/event_type")
	}

	switch envelope.EventType {
	case events.EventCallInvited:
		var e events.CallInvitedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return permanentf("malformed CallInvited payload: %v", err)
		}

		inviterID, err := parseRequiredUUID("inviter_user_id", e.InviterUserID)
		if err != nil {
			return err
		}
		inviteeID, err := parseRequiredUUID("invitee_user_id", e.InviteeUserID)
		if err != nil {
			return err
		}
		callID, err := parseRequiredUUID("call_id", e.CallID)
		if err != nil {
			return err
		}

		notifType := "incoming_call"
		// Call types are direct_video / group_video — the bare "video"
		// comparison never matched, so video calls pushed as plain
		// incoming_call.
		if strings.HasSuffix(e.CallType, "video") {
			notifType = "incoming_video_call"
		}
		deepLink := fmt.Sprintf("/call/%s", e.CallID)
		// Idempotency identity = event id + recipient: broker redelivery
		// of the same event cannot create a second durable inbox row, and
		// push/realtime fire only on the attempt that created the row.
		identity := fmt.Sprintf("call:%s:%s", envelope.EventID, inviteeID)
		return c.notifier.CreateCallNotification(ctx, inviteeID, inviterID,
			notifType, "call", callID, deepLink, e.CreatedAt, identity)

	case events.EventCallEnded:
		var e events.CallEndedPayload
		if err := unmarshalPayload(envelope.Payload, &e); err != nil {
			return permanentf("malformed CallEnded payload: %v", err)
		}

		if e.EndedReason != "missed" && e.EndedReason != "no_answer" {
			return nil
		}

		initiatorID, err := parseRequiredUUID("initiator_user_id", e.InitiatorUserID)
		if err != nil {
			return err
		}
		callID, err := parseRequiredUUID("call_id", e.CallID)
		if err != nil {
			return err
		}

		deepLink := fmt.Sprintf("/call/history?callId=%s", e.CallID)
		identity := fmt.Sprintf("call:%s:%s", envelope.EventID, initiatorID)
		return c.notifier.CreateCallNotification(ctx, initiatorID, uuid.Nil,
			"missed_call", "call", callID, deepLink, e.EndedAt, identity)

	default:
		return nil
	}
}

func (c *CallConsumer) Close() error {
	var firstErr error
	if c.reader != nil {
		firstErr = c.reader.Close()
	}
	if c.dlq != nil {
		if err := c.dlq.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
