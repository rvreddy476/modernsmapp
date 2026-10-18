package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Chat-parity CONTRACT §8: the push-preview fields chat-service adds to
// MessageCreated (text, media kind, per-recipient alert tone) must reach the
// delivery call; a message request never carries text into a push.

func processRaw(t *testing.T, notifier *recordingNotifier, eventType string, payload string) {
	t.Helper()
	envelope, err := json.Marshal(chatEnvelope{
		EventID: uuid.NewString(), EventType: eventType, Payload: json.RawMessage(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	consumer := &ChatConsumer{service: notifier}
	if err := consumer.processMessage(context.Background(), kafka.Message{Value: envelope}); err != nil {
		t.Fatalf("processMessage: %v", err)
	}
}

func TestMessageCreatedCarriesPreviewFieldsPerRecipient(t *testing.T) {
	sender, a, b, conv, mid := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	payload := `{"message_id":"` + mid.String() + `","conversation_id":"` + conv.String() + `",
		"sender_id":"` + sender.String() + `","type":"text","text":"hello there",
		"recipient_ids":["` + a.String() + `","` + b.String() + `"],
		"alert_tone":"chime","alert_tones":{"` + b.String() + `":"bell"},
		"created_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}`
	n := &recordingNotifier{}
	processRaw(t, n, chatEventMessageCreated, payload)

	ra, ok := n.forUser(a)
	if !ok {
		t.Fatal("recipient a not notified")
	}
	if ra.msg.Text != "hello there" || ra.msg.MessageID != mid || ra.msg.AlertTone != "chime" {
		t.Fatalf("recipient a got %+v", ra.msg)
	}
	rb, _ := n.forUser(b)
	if rb.msg.AlertTone != "bell" {
		t.Fatalf("per-recipient tone not applied: %+v", rb.msg)
	}
}

func TestMessageCreatedPreviewFallbackAndMediaKind(t *testing.T) {
	sender, r := uuid.New(), uuid.New()
	base := `"message_id":"` + uuid.NewString() + `","conversation_id":"` + uuid.NewString() + `","sender_id":"` + sender.String() + `","recipient_ids":["` + r.String() + `"]`

	n := &recordingNotifier{}
	processRaw(t, n, chatEventMessageCreated, `{`+base+`,"type":"text","preview":"from preview"}`)
	if got, _ := n.forUser(r); got.msg.Text != "from preview" {
		t.Fatalf("text fallback: %+v", got.msg)
	}

	n = &recordingNotifier{}
	processRaw(t, n, chatEventMessageCreated, `{`+base+`,"type":"media","media":{"kind":"video"},"preview":"caption"}`)
	got, _ := n.forUser(r)
	if got.msg.MediaKind != "video" || got.msg.Text != "" {
		t.Fatalf("media: kind %q text %q (captions never ride a media push)", got.msg.MediaKind, got.msg.Text)
	}

	n = &recordingNotifier{}
	processRaw(t, n, chatEventMessageCreated, `{`+base+`,"type":"media","media_kind":"image"}`)
	if got, _ := n.forUser(r); got.msg.MediaKind != "image" {
		t.Fatalf("media_kind: %+v", got.msg)
	}

	// Older publisher: no preview fields at all.
	n = &recordingNotifier{}
	processRaw(t, n, chatEventMessageCreated, `{`+base+`,"type":"text"}`)
	if got, _ := n.forUser(r); got.msg.Text != "" || got.msg.AlertTone != "" {
		t.Fatalf("absent fields invented: %+v", got.msg)
	}
}

// A stranger's first message never becomes a text preview: MessageCreated
// with is_request is dropped, and the request row goes through the generic
// CreateNotification path, not the chat-message path.
func TestMessageRequestNeverCarriesText(t *testing.T) {
	sender, r, conv := uuid.New(), uuid.New(), uuid.New()
	n := &recordingNotifier{}
	processRaw(t, n, chatEventMessageCreated, `{"message_id":"`+uuid.NewString()+`","conversation_id":"`+conv.String()+
		`","sender_id":"`+sender.String()+`","type":"text","text":"buy crypto","is_request":true,"recipient_ids":["`+r.String()+`"]}`)
	processRaw(t, n, chatEventMessageRequestCreated, `{"conversation_id":"`+conv.String()+`","sender_id":"`+sender.String()+
		`","receiver_id":"`+r.String()+`","preview":"buy crypto"}`)
	if len(n.created) != 1 {
		t.Fatalf("%d notifications, want only the request row", len(n.created))
	}
	if n.created[0].notifType == "dm" || n.created[0].msg.Text != "" {
		t.Fatalf("request went through the preview path: %+v", n.created[0])
	}
}

type invalidations struct{ users []uuid.UUID }

func (i *invalidations) InvalidateChatPreview(_ context.Context, u uuid.UUID) error {
	i.users = append(i.users, u)
	return nil
}

func TestSettingsChangedInvalidatesPreviewCache(t *testing.T) {
	user := uuid.New()
	inv := &invalidations{}
	c := &IdentitySettingsConsumer{service: inv}
	msgs := []string{
		`{"event_type":"user.settings_changed","payload":{"user_id":"` + user.String() + `","privacy_version":4}}`,
		`{"event_type":"user.modules_changed","payload":{"user_id":"` + uuid.NewString() + `"}}`,
		`{"event_type":"user.settings_changed","payload":{"user_id":"nope"}}`,
		`garbage`,
	}
	for _, m := range msgs {
		if err := c.processMessage(context.Background(), kafka.Message{Value: []byte(m)}); err != nil {
			t.Fatalf("%s: %v", m, err)
		}
	}
	if len(inv.users) != 1 || inv.users[0] != user {
		t.Fatalf("invalidated %v, want only %s", inv.users, user)
	}
}
