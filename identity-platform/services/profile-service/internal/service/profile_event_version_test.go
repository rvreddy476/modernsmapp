package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/events"
	"github.com/atpost/identity-profile-service/internal/store"
	sharedEvents "github.com/atpost/identity-shared/events"
	"github.com/segmentio/kafka-go"
)

// Both profile events are versioned by the row the write returned
// (store.UpdateProfile sets updated_at = NOW() and RETURNING carries it).

type eventWriter struct{ msgs []kafka.Message }

func (w *eventWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *eventWriter) Close() error { return nil }

func onlyPayload(t *testing.T, w *eventWriter, eventType string, into any) {
	t.Helper()
	if len(w.msgs) != 1 {
		t.Fatalf("published %d events, want 1", len(w.msgs))
	}
	var env sharedEvents.EventEnvelope
	if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
		t.Fatal(err)
	}
	if env.EventType != eventType {
		t.Fatalf("event = %s, want %s", env.EventType, eventType)
	}
	if err := json.Unmarshal(env.Payload, into); err != nil {
		t.Fatal(err)
	}
}

var committedAt = time.Date(2026, 11, 16, 8, 30, 0, 654321000, time.UTC)

func TestUpdateProfile_PublishesTheRowUpdatedAt(t *testing.T) {
	stored := storedProfile()
	stored.UpdatedAt = committedAt
	f := &fakeProfileWrites{profile: stored}
	w := &eventWriter{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(nil, nil, events.NewProducerForWriter(w), nil, logger).WithProfileWriteStore(f, func() time.Time { return refNow })

	if _, err := svc.UpdateProfile(context.Background(), testUserID, store.UpdateProfileParams{Bio: strOf("new bio")}); err != nil {
		t.Fatal(err)
	}
	var p sharedEvents.UserProfileUpdatedPayload
	onlyPayload(t, w, sharedEvents.UserProfileUpdated, &p)
	if !p.UpdatedAt.Equal(committedAt) || p.Bio != "new bio" {
		t.Fatalf("updated_at = %v bio = %q, want the row's %v", p.UpdatedAt, p.Bio, committedAt)
	}
}

func TestChangeHandle_PublishesTheRowUpdatedAt(t *testing.T) {
	f := &fakeHandleStore{profile: &store.Profile{UserID: testUserID, Username: strOf("old_handle"), UpdatedAt: committedAt}}
	w := &eventWriter{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(nil, nil, events.NewProducerForWriter(w), nil, logger).WithHandleChangeStore(f)

	if _, err := svc.ChangeHandle(context.Background(), testUserID, "new_handle"); err != nil {
		t.Fatal(err)
	}
	var p sharedEvents.HandleChangedPayload
	onlyPayload(t, w, sharedEvents.HandleChanged, &p)
	if !p.ChangedAt.Equal(committedAt) || p.NewUsername != "new_handle" || p.OldUsername != "old_handle" {
		t.Fatalf("payload = %+v, want new_handle at the row's %v", p, committedAt)
	}
}
