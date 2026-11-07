package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Chat event-type strings. Declared locally — chat-service lives in a
// separate Go workspace, so we mirror the contract here rather than
// importing across modules (same pattern the commerce/dating/rider
// consumers use for foreign event types).
const (
	chatEventMessageCreated        = "MessageCreated"
	chatEventMessageRequestCreated = "MessageRequestCreated"
	// chat.dating.message.new — emitted per-recipient by chat-service when
	// a message lands in a source_app='dating' conversation. Phase 1 §1.
	chatEventDatingMessageNew = "chat.dating.message.new"
)

// chatEnvelope mirrors the JSON envelope chat-service publishes to the
// chat.events.v1 topic: {event_id, event_type, occurred_at,
// actor_user_id, payload}. It is intentionally distinct from
// shared/events.EventEnvelope because chat-service is a separate module
// with its own envelope shape.
type chatEnvelope struct {
	EventID     string          `json:"event_id"`
	EventType   string          `json:"event_type"`
	OccurredAt  time.Time       `json:"occurred_at"`
	ActorUserID string          `json:"actor_user_id"`
	Payload     json.RawMessage `json:"payload"`
}

// messageCreatedPayload is the payload of a MessageCreated chat event.
type messageCreatedPayload struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id"`
	Type           string    `json:"type"`
	CreatedAt      time.Time `json:"created_at"`
	RecipientIDs   []string  `json:"recipient_ids"`
	// ConversationType is chat's conversations.type ("direct" | "group"),
	// named as on chat's ConversationReported event. Optional: chat does not
	// send it on MessageCreated yet, and without it a conversation with more
	// than one recipient is a group (see conversationDeepLink).
	ConversationType string `json:"conversation_type,omitempty"`
	// IsRequest marks the first message of a message request. The
	// MessageRequestCreated event that follows carries the notification for
	// it; a "dm" row on top would tell the recipient about one message twice.
	IsRequest bool `json:"is_request"`
	// Recipients who muted this conversation. Chat owns that setting, so it
	// arrives on the event; absent means nobody muted it.
	MutedRecipientIDs []string `json:"muted_recipient_ids,omitempty"`
	// Recipients who HID this conversation (hidden chats). They get only the
	// content-free "New message" push: no inbox row, no badge, no preview.
	LockedRecipientIDs []string `json:"locked_recipient_ids,omitempty"`

	// Push-preview fields (chat-parity CONTRACT §8). All optional and
	// additive: an older publisher sends none and every push stays generic.
	//
	// Text is the message text (type "text"); Preview is chat's denormalised
	// preview, read only as a fallback for a text message.
	Text    string `json:"text,omitempty"`
	Preview string `json:"preview,omitempty"`
	// MediaKind (image|video|audio) picks "sent a photo/video/voice
	// message" for type "media"; Media.Kind is accepted as the same thing.
	MediaKind string `json:"media_kind,omitempty"`
	Media     *struct {
		Kind string `json:"kind"`
	} `json:"media,omitempty"`
	// AlertTones is each recipient's per-conversation tone
	// (default|chime|pop|bell|silent), keyed by recipient id. AlertTone is a
	// single tone for every recipient, used when a recipient has no entry.
	AlertTones map[string]string `json:"alert_tones,omitempty"`
	AlertTone  string            `json:"alert_tone,omitempty"`
}

// pushFor is the push-relevant content of this message for one recipient.
func (e messageCreatedPayload) pushFor(recipientID string) service.ChatMessagePush {
	msg := service.ChatMessagePush{MessageType: e.Type, MediaKind: e.MediaKind}
	if id, err := uuid.Parse(e.MessageID); err == nil {
		msg.MessageID = id
	}
	if msg.MediaKind == "" && e.Media != nil {
		msg.MediaKind = e.Media.Kind
	}
	if e.Type == service.ChatMessageTypeText || e.Type == "" {
		msg.Text = e.Text
		if msg.Text == "" {
			msg.Text = e.Preview
		}
	}
	msg.AlertTone = e.AlertTone
	if tone, ok := e.AlertTones[recipientID]; ok {
		msg.AlertTone = tone
	}
	return msg
}

// messageRequestCreatedPayload is the payload of a MessageRequestCreated
// chat event.
type messageRequestCreatedPayload struct {
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id"`
	ReceiverID     string    `json:"receiver_id"`
	Preview        string    `json:"preview"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// chatNotifier is the narrow slice of notification-service this consumer
// actually uses. Extracting it (launch-backlog D-D3) is what makes the fan-out
// rules — who gets a row, who gets a push — testable without Scylla, Redis and
// a push transport.
//
// Each method returns an error only from its first durable step (the inbox
// row write, or the device lookup for a hidden-chat push); realtime and push
// failures after it are logged, not returned. That is what makes retrying
// one recipient's delivery safe: a retry never re-sends a push that went out.
type chatNotifier interface {
	CreateNotification(ctx context.Context, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time) error
	// CreateChatMessageNotification is the "dm" row for one chat message: the
	// recipient's show_message_preview and the message content pick the push
	// copy, the data map gains conversation_id/message_id/alert_tone, and
	// muted sends no device push.
	CreateChatMessageNotification(ctx context.Context, userID, actorID, conversationID uuid.UUID, deepLink string, createdAt time.Time, msg service.ChatMessagePush, muted bool) error
	// CreateHiddenChatMessagePush is the only delivery for a recipient who
	// hid the conversation: a content-free push, no inbox row.
	CreateHiddenChatMessagePush(ctx context.Context, userID uuid.UUID, muted bool) error
}

// chatMessageSource is the broker seam: fetch and commit, exactly what
// kafka.Reader provides, so tests drive the production Start loop.
type chatMessageSource interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
}

// ChatConsumer handles direct-message, group-message and message-request
// notification events from the chat.events.v1 topic. A separate
// consumer/group (like CallConsumer) so chat-event lag never blocks
// social-event notification delivery.
type ChatConsumer struct {
	source  chatMessageSource
	service chatNotifier
	// retryBackoff is the base of the linear backoff between delivery
	// attempts and between failed fetches. Zero (tests) retries at once.
	retryBackoff time.Duration

	reader *kafka.Reader // nil in tests; owned for Close()
}

// chatDeliveryAttempts bounds the retries of ONE recipient's delivery. A
// chat push is worth retrying through a short outage, but not worth holding
// the partition behind it: after the last attempt the failure is logged and
// the message is committed. With chatRetryBackoff that is at most
// 1+2+3+4 = 10 s of waiting per recipient.
const (
	chatDeliveryAttempts = 5
	chatRetryBackoff     = time.Second
)

func NewChatConsumerWithDialer(brokers []string, groupID string, topic string, svc *service.Service, dialer *kafka.Dialer) *ChatConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &ChatConsumer{
		source:       reader,
		service:      svc,
		retryBackoff: chatRetryBackoff,
		reader:       reader,
	}
}

// Start consumes chat events: fetch, handle, commit.
//
//   - A fetch error never ends the loop: it is logged and retried with
//     backoff. Only a cancelled context stops the consumer. (Before 7 Nov
//     2026 one broker hiccup ended chat pushes until the next restart.)
//   - A message is committed only once it has been handled: delivered, or
//     given up on after chatDeliveryAttempts per failing recipient (logged).
//   - Shutdown mid-retry commits nothing, so the broker redelivers.
//   - A failed commit is logged and the loop goes on: the next commit on the
//     partition covers this offset, and at worst the message is redelivered.
func (c *ChatConsumer) Start(ctx context.Context) {
	fetchFailures := 0
	for {
		m, err := c.source.FetchMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("chat consumer: shutting down\n")
				return
			}
			if errors.Is(err, io.EOF) {
				// kafka.Reader answers io.EOF only once Close was called: it
				// can never fetch again, so this is shutdown too.
				log.Printf("chat consumer: reader closed, stopping\n")
				return
			}
			fetchFailures++
			log.Printf("chat consumer: fetch failed (attempt %d), retrying: %v\n", fetchFailures, err)
			if !waitChatRetry(ctx, c.backoff(fetchFailures)) {
				return
			}
			continue
		}
		fetchFailures = 0

		if err := c.processMessage(ctx, m); err != nil {
			if ctx.Err() != nil {
				return // shutdown mid-retry: leave it uncommitted for redelivery
			}
			log.Printf("chat consumer: gave up on message (partition %d, offset %d): %v\n", m.Partition, m.Offset, err)
		}
		if err := c.source.CommitMessages(ctx, m); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("chat consumer: commit failed (partition %d, offset %d): %v\n", m.Partition, m.Offset, err)
		}
	}
}

// deliver runs one recipient's delivery, retrying a failure up to
// chatDeliveryAttempts times. It returns nil on success, the context's error
// if shutdown interrupts the retries, and otherwise the last failure.
func (c *ChatConsumer) deliver(ctx context.Context, what string, send func(context.Context) error) error {
	for attempt := 1; ; attempt++ {
		err := send(ctx)
		if err == nil {
			return nil
		}
		if attempt >= chatDeliveryAttempts {
			return fmt.Errorf("%s: gave up after %d attempts: %w", what, attempt, err)
		}
		log.Printf("chat consumer: %s failed (attempt %d of %d), retrying: %v\n", what, attempt, chatDeliveryAttempts, err)
		if !waitChatRetry(ctx, c.backoff(attempt)) {
			return ctx.Err()
		}
	}
}

// backoff is linear in the attempt number, capped like the call consumer's.
func (c *ChatConsumer) backoff(attempt int) time.Duration {
	d := c.retryBackoff * time.Duration(attempt)
	if d > maxConsumeRetryBackoff {
		d = maxConsumeRetryBackoff
	}
	return d
}

// waitChatRetry sleeps for d, or returns false as soon as ctx ends.
func waitChatRetry(ctx context.Context, d time.Duration) bool {
	if ctx.Err() != nil {
		return false
	}
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// processMessage handles one chat event. A malformed event is returned as an
// error at once (retrying cannot fix it); a delivery failure is returned
// only after deliver has used up its attempts.
func (c *ChatConsumer) processMessage(ctx context.Context, m kafka.Message) error {
	var envelope chatEnvelope
	if err := json.Unmarshal(m.Value, &envelope); err != nil {
		return fmt.Errorf("decode chat envelope: %w", err)
	}

	switch envelope.EventType {
	case chatEventMessageCreated:
		var e messageCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &e); err != nil {
			return fmt.Errorf("decode %s payload: %w", envelope.EventType, err)
		}
		return c.handleMessageCreated(ctx, e)

	case chatEventMessageRequestCreated:
		var e messageRequestCreatedPayload
		if err := json.Unmarshal(envelope.Payload, &e); err != nil {
			return fmt.Errorf("decode %s payload: %w", envelope.EventType, err)
		}
		return c.handleMessageRequestCreated(ctx, e)

	case chatEventDatingMessageNew:
		var e chatDatingMessageNewPayload
		if err := json.Unmarshal(envelope.Payload, &e); err != nil {
			return fmt.Errorf("decode %s payload: %w", envelope.EventType, err)
		}
		return c.handleChatDatingMessageNew(ctx, e)

	default:
		// Other chat.events.v1 event types are claimed and ignored.
		return nil
	}
}

// handleMessageCreated fans out a "dm" notification to every recipient of a
// new chat message (direct or group), skipping the sender. Notifications
// carry entity type "conversation" so the collapse-key logic groups every
// message in one conversation into a single device notification.
//
// Each recipient is delivered, and retried, on its own: one failing
// recipient neither stops the others nor makes them receive it twice.
func (c *ChatConsumer) handleMessageCreated(ctx context.Context, e messageCreatedPayload) error {
	// The first message of a message request is announced by the
	// MessageRequestCreated event, with the Accept / Decline / Block row. A
	// "dm" notification on top would tell the recipient twice about one
	// message — and push a "new message" for a conversation they have not
	// agreed to yet.
	if e.IsRequest {
		return nil
	}
	senderID, err := uuid.Parse(e.SenderID)
	if err != nil {
		return fmt.Errorf("%s: invalid sender_id %q", chatEventMessageCreated, e.SenderID)
	}
	conversationID, err := uuid.Parse(e.ConversationID)
	if err != nil {
		return fmt.Errorf("%s: invalid conversation_id %q", chatEventMessageCreated, e.ConversationID)
	}

	createdAt := e.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	deepLink := conversationDeepLink(e, senderID, conversationID)

	// Muted recipients still get the durable inbox row — mute silences the
	// device, it does not hide the conversation — but no push is sent.
	muted := make(map[string]struct{}, len(e.MutedRecipientIDs))
	for _, id := range e.MutedRecipientIDs {
		muted[id] = struct{}{}
	}

	locked := make(map[string]struct{}, len(e.LockedRecipientIDs))
	for _, id := range e.LockedRecipientIDs {
		locked[id] = struct{}{}
	}

	var failed []error
	for _, ridStr := range e.RecipientIDs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		recipientID, err := uuid.Parse(ridStr)
		if err != nil {
			failed = append(failed, fmt.Errorf("invalid recipient_id %q", ridStr))
			continue
		}
		// Skip the sender — they don't get notified of their own message.
		if recipientID == senderID {
			continue
		}
		_, isMuted := muted[ridStr]
		if _, hidden := locked[ridStr]; hidden {
			// Hidden chat: nothing about the sender, the text or the
			// conversation — and no inbox row, so no badge.
			err = c.deliver(ctx, "hidden-chat push to "+ridStr, func(ctx context.Context) error {
				return c.service.CreateHiddenChatMessagePush(ctx, recipientID, isMuted)
			})
		} else {
			msg := e.pushFor(ridStr)
			err = c.deliver(ctx, "dm notification to "+ridStr, func(ctx context.Context) error {
				return c.service.CreateChatMessageNotification(
					ctx, recipientID, senderID, conversationID, deepLink, createdAt, msg, isMuted,
				)
			})
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s %s: %w", chatEventMessageCreated, e.MessageID, errors.Join(failed...))
	}
	return nil
}

// conversationDeepLink is where tapping a message notification lands.
//
// A direct conversation opens from /messenger?user=<sender>: the messenger
// resolves the peer to the conversation ("/messages/<id>" was never a route).
// A group must NOT use that link — it opened a DM with whoever wrote the
// message. A group opens on the messenger's groups lane and names the
// conversation, in the shape the channel links already use
// (/messenger?lane=channels&channel=<id>). Android routes a "dm" push on its
// conversation id (entity_id / conversation_id), not on this link.
//
// Chat does not send conversation_type on MessageCreated yet; until it does,
// more than one recipient means a group (a direct conversation has exactly
// one). A two-person group is the one case that still gets the direct link.
func conversationDeepLink(e messageCreatedPayload, senderID, conversationID uuid.UUID) string {
	group := len(e.RecipientIDs) > 1
	switch e.ConversationType {
	case "group":
		group = true
	case "direct":
		group = false
	}
	if group {
		return fmt.Sprintf("/messenger?lane=groups&group=%s", conversationID)
	}
	return fmt.Sprintf("/messenger?user=%s", senderID)
}

// handleMessageRequestCreated creates a single message-request
// notification for the receiver. All pending requests collapse into one
// quiet notification via the "message_request" collapse-key case.
func (c *ChatConsumer) handleMessageRequestCreated(ctx context.Context, e messageRequestCreatedPayload) error {
	senderID, err := uuid.Parse(e.SenderID)
	if err != nil {
		return fmt.Errorf("%s: invalid sender_id %q", chatEventMessageRequestCreated, e.SenderID)
	}
	receiverID, err := uuid.Parse(e.ReceiverID)
	if err != nil {
		return fmt.Errorf("%s: invalid receiver_id %q", chatEventMessageRequestCreated, e.ReceiverID)
	}
	if receiverID == senderID {
		return nil
	}
	conversationID, err := uuid.Parse(e.ConversationID)
	if err != nil {
		return fmt.Errorf("%s: invalid conversation_id %q", chatEventMessageRequestCreated, e.ConversationID)
	}

	occurredAt := e.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	// Deep link to the message-requests lane rather than the conversation
	// itself — requests are reviewed in a dedicated inbox.
	//
	// This was "/messages/requests", which is not a route: the messenger
	// lives at /messenger. Every message-request notification led to a
	// 404, so the one tap that was supposed to bring the recipient to the
	// request went nowhere.
	deepLink := "/messenger?lane=requests"

	return c.deliver(ctx, "message-request notification to "+e.ReceiverID, func(ctx context.Context) error {
		return c.service.CreateNotification(
			ctx, receiverID, senderID, "message_request", "conversation", conversationID, deepLink, occurredAt,
		)
	})
}

// chatDatingMessageNewPayload is declared in dating_consumer.go (the
// dating-events consumer also handles this shape on its own topic when
// the chat outbox bridge writes there). Reuse it here.
//
// handleChatDatingMessageNew creates a dating-flavoured push for a
// single recipient. Phase 1 §1 — the outbox emits one event per
// recipient so we don't need to fan out here.
//
// Lane D10: the entity is the MATCH, never the conversation, and the deep
// link names both. Both producers of dating.match.new_message (this chat
// bridge and dating_consumer.go) now write the same shape — entity_type
// "dating_match", entity_id = match_id — so a client routes on one rule and
// the collapse key folds a match's messages together.
func (c *ChatConsumer) handleChatDatingMessageNew(ctx context.Context, e chatDatingMessageNewPayload) error {
	recipientID, senderID, matchID, deepLink, ok := datingNewMessageNotification(e)
	if !ok {
		return fmt.Errorf("%s: invalid recipient_id %q", chatEventDatingMessageNew, e.RecipientID)
	}
	if e.HiddenChat {
		// Hidden chat: content-free push only, no match row.
		return c.deliver(ctx, "dating hidden-chat push to "+e.RecipientID, func(ctx context.Context) error {
			return c.service.CreateHiddenChatMessagePush(ctx, recipientID, false)
		})
	}
	sentAt := e.SentAt
	if sentAt.IsZero() {
		sentAt = time.Now().UTC()
	}
	return c.deliver(ctx, "dating message notification to "+e.RecipientID, func(ctx context.Context) error {
		return c.service.CreateNotification(
			ctx, recipientID, senderID, "dating.match.new_message", DatingNewMessageEntityType, matchID, deepLink, sentAt,
		)
	})
}

func (c *ChatConsumer) Close() error {
	if c.reader == nil {
		return nil
	}
	return c.reader.Close()
}
