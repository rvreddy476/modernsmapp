package events

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sharedEvents "github.com/atpost/identity-shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// The profile events carry the committed row's updated_at, not the publish
// time: search versions users_v1 by it (search-service
// store/search/user_profile_state.go).

type recordingWriter struct{ msgs []kafka.Message }

func (w *recordingWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.msgs = append(w.msgs, msgs...)
	return nil
}

func (w *recordingWriter) Close() error { return nil }

func publishedPayload(t *testing.T, w *recordingWriter, into any) {
	t.Helper()
	if len(w.msgs) != 1 {
		t.Fatalf("messages = %d, want 1", len(w.msgs))
	}
	var env sharedEvents.EventEnvelope
	if err := json.Unmarshal(w.msgs[0].Value, &env); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(env.Payload, into); err != nil {
		t.Fatal(err)
	}
}

var (
	versionUser = uuid.MustParse("5b0c3a6e-2222-4c1d-9e2f-000000000002")
	rowAt       = time.Date(2026, 11, 16, 9, 0, 0, 123456000, time.UTC)
)

func TestPublishUserProfileUpdated_SendsTheRowUpdatedAt(t *testing.T) {
	w := &recordingWriter{}
	if err := NewProducerForWriter(w).PublishUserProfileUpdated(context.Background(), versionUser, "asha", "Asha", "", nil, "Asha", "", rowAt); err != nil {
		t.Fatal(err)
	}
	var p sharedEvents.UserProfileUpdatedPayload
	publishedPayload(t, w, &p)
	if !p.UpdatedAt.Equal(rowAt) {
		t.Fatalf("updated_at = %v, want the row's %v", p.UpdatedAt, rowAt)
	}
}

func TestPublishHandleChanged_SendsTheRowUpdatedAt(t *testing.T) {
	w := &recordingWriter{}
	if err := NewProducerForWriter(w).PublishHandleChanged(context.Background(), versionUser, "old", "new", rowAt); err != nil {
		t.Fatal(err)
	}
	var p sharedEvents.HandleChangedPayload
	publishedPayload(t, w, &p)
	if !p.ChangedAt.Equal(rowAt) || p.NewUsername != "new" {
		t.Fatalf("changed_at = %v new = %q, want the row's %v", p.ChangedAt, p.NewUsername, rowAt)
	}
}

// No row time: the publish time, as before.
func TestPublishProfileEvents_ZeroRowTimeFallsBackToNow(t *testing.T) {
	before := time.Now()
	w := &recordingWriter{}
	if err := NewProducerForWriter(w).PublishUserProfileUpdated(context.Background(), versionUser, "asha", "Asha", "", nil, "", "", time.Time{}); err != nil {
		t.Fatal(err)
	}
	var p sharedEvents.UserProfileUpdatedPayload
	publishedPayload(t, w, &p)
	if p.UpdatedAt.Before(before) || p.UpdatedAt.After(time.Now()) {
		t.Fatalf("updated_at = %v, want now", p.UpdatedAt)
	}
}
