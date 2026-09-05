package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/segmentio/kafka-go"
)

// Chat-app pass (2026-09-05): community (broadcast channel) update
// notifications.
//
// channel-service's fan-out worker resolves the subscriber list itself and
// emits ONE message per recipient on atpost.channel.notifications. That topic
// had no consumer: the worker's own inbox INSERT targeted a table that does
// not exist here, so nobody was ever told about a new update. This consumer
// turns each message into a template-pipeline notification
// (channel.update.published — inbox row, realtime frame, collapsible push).

// ChannelNotificationsTopic is produced by channel-service workers.FanoutWorker.
const ChannelNotificationsTopic = "atpost.channel.notifications"

// channelNotificationMsg mirrors channel-service workers.notificationMsg.
type channelNotificationMsg struct {
	EventType   string `json:"event_type"`
	RecipientID string `json:"recipient_id"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	UpdateID    string `json:"update_id"`
	UpdateType  string `json:"update_type"`
	AuthorID    string `json:"author_id"`
	Title       string `json:"title"`
	Body        string `json:"body"`
	ImageURL    string `json:"image_url"`
	DeepLink    string `json:"deep_link"`
	SentAt      string `json:"sent_at"`
}

// channelNotifier is the narrow slice of the service this consumer uses.
type channelNotifier interface {
	ProcessNotificationEvent(ctx context.Context, event service.NotificationEvent) error
}

type ChannelConsumer struct {
	reader  *kafka.Reader
	service channelNotifier
}

func NewChannelConsumerWithDialer(brokers []string, groupID, topic string, svc *service.Service, dialer *kafka.Dialer) *ChannelConsumer {
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3,
		MaxBytes: 10e6,
		Dialer:   dialer,
	})
	return &ChannelConsumer{reader: reader, service: svc}
}

func (c *ChannelConsumer) Start(ctx context.Context) {
	for {
		m, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("channel consumer shutting down\n")
			} else {
				log.Printf("Channel consumer error: %v\n", err)
			}
			break
		}
		if err := c.processMessage(ctx, m.Value); err != nil {
			log.Printf("Failed to process channel notification: %v\n", err)
		}
	}
}

func (c *ChannelConsumer) processMessage(ctx context.Context, raw []byte) error {
	var msg channelNotificationMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		return err
	}
	event, ok := channelNotificationEvent(msg, time.Now())
	if !ok {
		return fmt.Errorf("channel notification missing recipient/update: %q", string(raw))
	}
	return c.service.ProcessNotificationEvent(ctx, event)
}

// channelNotificationEvent maps one fan-out message onto the template
// pipeline. The title/body variables feed the "{channel} posted: {title}"
// template; an update without a title falls back to its body preview.
func channelNotificationEvent(msg channelNotificationMsg, now time.Time) (service.NotificationEvent, bool) {
	if msg.RecipientID == "" || msg.UpdateID == "" {
		return service.NotificationEvent{}, false
	}
	eventType := msg.EventType
	if eventType == "" {
		eventType = "channel.update.published"
	}
	title := strings.TrimSpace(msg.Title)
	// The worker pre-renders "Channel: Title" / "New update from Channel";
	// keep only the update's own words for the template.
	if msg.ChannelName != "" {
		title = strings.TrimSpace(strings.TrimPrefix(title, msg.ChannelName+":"))
		if strings.EqualFold(title, "New update from "+msg.ChannelName) {
			title = ""
		}
	}
	if title == "" {
		title = strings.TrimSpace(msg.Body)
	}
	if title == "" {
		title = "a new update"
	}
	channel := msg.ChannelName
	if channel == "" {
		channel = "A community you follow"
	}
	ts := now
	if msg.SentAt != "" {
		if parsed, err := time.Parse(time.RFC3339, msg.SentAt); err == nil {
			ts = parsed
		}
	}
	deepLink := msg.DeepLink
	if deepLink == "" {
		deepLink = fmt.Sprintf("/channels/%s/updates/%s", msg.ChannelID, msg.UpdateID)
	}
	return service.NotificationEvent{
		EventType:   eventType,
		RecipientID: msg.RecipientID,
		ActorID:     msg.AuthorID,
		ActorName:   channel,
		TargetID:    msg.UpdateID,
		TargetType:  "channel_update",
		DeepLink:    deepLink,
		Vars: map[string]string{
			"channel":    channel,
			"title":      title,
			"channel_id": msg.ChannelID,
			"update_id":  msg.UpdateID,
			"body":       msg.Body,
		},
		Timestamp: ts,
	}, true
}
