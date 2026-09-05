package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
)

type recordingChannelNotifier struct {
	events []service.NotificationEvent
}

func (r *recordingChannelNotifier) ProcessNotificationEvent(ctx context.Context, e service.NotificationEvent) error {
	r.events = append(r.events, e)
	return nil
}

func TestChannelConsumerMapsFanoutMessageOntoTemplatePipeline(t *testing.T) {
	rec := &recordingChannelNotifier{}
	c := &ChannelConsumer{service: rec}
	raw, _ := json.Marshal(map[string]any{
		"event_type":   "channel.update.published",
		"recipient_id": "2d598287-eee7-40b4-a7f5-b46b9412e4e7",
		"channel_id":   "5a4b7b1a-0000-4000-8000-000000000001",
		"channel_name": "Weekend Riders",
		"update_id":    "5a4b7b1a-0000-4000-8000-000000000002",
		"update_type":  "event",
		"author_id":    "66668bc2-a3f6-40a5-9cdd-c998dcf72f29",
		"title":        "Weekend Riders: Launch night",
		"body":         "Doors at 6",
		"deep_link":    "/channels/5a4b7b1a-0000-4000-8000-000000000001/updates/5a4b7b1a-0000-4000-8000-000000000002",
		"sent_at":      "2026-09-05T10:00:00Z",
	})
	if err := c.processMessage(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want 1", len(rec.events))
	}
	e := rec.events[0]
	if e.EventType != "channel.update.published" || e.RecipientID != "2d598287-eee7-40b4-a7f5-b46b9412e4e7" {
		t.Fatalf("event: %+v", e)
	}
	if e.ActorID != "66668bc2-a3f6-40a5-9cdd-c998dcf72f29" || e.TargetType != "channel_update" || e.TargetID != "5a4b7b1a-0000-4000-8000-000000000002" {
		t.Fatalf("actor/target: %+v", e)
	}
	if e.Vars["channel"] != "Weekend Riders" || e.Vars["title"] != "Launch night" {
		t.Fatalf("template vars: %v", e.Vars)
	}
	if !e.Timestamp.Equal(time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("timestamp: %v", e.Timestamp)
	}
	// Title rendered by the registered template.
	tmpl := service.GetTemplate(e.EventType)
	if got := service.RenderTitle(tmpl.TitleTemplate, e.Vars); got != "Weekend Riders posted: Launch night" {
		t.Fatalf("rendered title: %q", got)
	}
}

func TestChannelConsumerFallbacks(t *testing.T) {
	e, ok := channelNotificationEvent(channelNotificationMsg{
		RecipientID: "r", UpdateID: "u", ChannelID: "c", ChannelName: "Riders",
		Title: "New update from Riders", Body: "Ride at 7am",
	}, time.Now())
	if !ok || e.Vars["title"] != "Ride at 7am" || e.DeepLink != "/channels/c/updates/u" {
		t.Fatalf("fallbacks: %+v %v", e, ok)
	}
	if _, ok := channelNotificationEvent(channelNotificationMsg{UpdateID: "u"}, time.Now()); ok {
		t.Fatal("message without recipient accepted")
	}
	rec := &recordingChannelNotifier{}
	if err := (&ChannelConsumer{service: rec}).processMessage(context.Background(), []byte(`{"update_id":"u"}`)); err == nil {
		t.Fatal("recipient-less message did not error")
	}
}
