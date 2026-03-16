package workers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/atpost/live-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// StartAll launches all background goroutines needed by live-service.
// It is non-blocking: each worker runs in its own goroutine and exits when ctx is cancelled.
func StartAll(ctx context.Context, store *postgres.Store, kafkaWriter *kafka.Writer) {
	go reminderWorker(ctx, store, kafkaWriter)
	go staleSessionCleanup(ctx, store)
	go scheduledStreamActivation(ctx, store, kafkaWriter)
	go dvrExpiry(ctx, store)
	go audioRoomAutoClose(ctx, store)
	slog.Info("background workers started")
}

// reminderWorker runs every 5 minutes and publishes LiveReminder Kafka events for
// scheduled streams starting within the next 15 minutes.
func reminderWorker(ctx context.Context, store *postgres.Store, w *kafka.Writer) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runReminderPass(ctx, store, w)
		}
	}
}

func runReminderPass(ctx context.Context, store *postgres.Store, w *kafka.Writer) {
	scheduled, err := store.FindScheduledStreamsForReminder(ctx, 15)
	if err != nil {
		slog.Error("reminder worker: failed to find scheduled streams", "error", err)
		return
	}

	for _, ss := range scheduled {
		actorID := ss.HostID.String()
		if err := publishEvent(ctx, w, "LiveReminder", actorID, map[string]interface{}{
			"scheduled_stream_id": ss.ID.String(),
			"host_id":             actorID,
			"title":               ss.Title,
			"scheduled_at":        ss.ScheduledAt,
		}); err != nil {
			slog.Error("reminder worker: failed to publish LiveReminder", "scheduled_id", ss.ID, "error", err)
			continue
		}

		if err := store.MarkReminderSent(ctx, ss.ID); err != nil {
			slog.Error("reminder worker: failed to mark reminder sent", "scheduled_id", ss.ID, "error", err)
		} else {
			slog.Info("reminder worker: reminder sent", "scheduled_id", ss.ID, "host_id", ss.HostID)
		}
	}
}

// staleSessionCleanup runs every 10 minutes and ends viewer sessions that have been
// open for longer than 6 hours (catches clients that disconnected without calling leave).
func staleSessionCleanup(ctx context.Context, store *postgres.Store) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := store.EndStaleViewerSessions(ctx, 6)
			if err != nil {
				slog.Error("stale session cleanup: error", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("stale session cleanup: closed stale sessions", "count", n)
			}
		}
	}
}

// scheduledStreamActivation runs every 1 minute and auto-transitions scheduled streams
// to live when their start_time has been reached and the host has not manually gone live.
func scheduledStreamActivation(ctx context.Context, store *postgres.Store, w *kafka.Writer) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runActivationPass(ctx, store, w)
		}
	}
}

func runActivationPass(ctx context.Context, store *postgres.Store, w *kafka.Writer) {
	toActivate, err := store.FindScheduledStreamsToActivate(ctx)
	if err != nil {
		slog.Error("activation worker: failed to find streams to activate", "error", err)
		return
	}

	for _, ss := range toActivate {
		now := time.Now()
		st := &postgres.Stream{
			ID:          uuid.New(),
			HostID:      ss.HostID,
			Title:       ss.Title,
			Description: ss.Description,
			StreamKey:   newStreamKey(),
			Status:      "idle",
			Visibility:  "public",
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		if err := store.CreateStream(ctx, st); err != nil {
			slog.Error("activation worker: failed to create stream", "scheduled_id", ss.ID, "error", err)
			continue
		}

		if err := store.GoLive(ctx, st.ID); err != nil {
			slog.Error("activation worker: failed to go live", "stream_id", st.ID, "error", err)
			continue
		}

		if err := store.LinkScheduledStreamToLive(ctx, ss.ID, st.ID); err != nil {
			slog.Error("activation worker: failed to link scheduled stream", "scheduled_id", ss.ID, "stream_id", st.ID, "error", err)
		}

		hostStr := ss.HostID.String()
		if err := publishEvent(ctx, w, "LiveStarted", hostStr, events.LiveStartedPayload{
			StreamID:  st.ID.String(),
			HostID:    hostStr,
			Title:     st.Title,
			StartedAt: now,
		}); err != nil {
			slog.Error("activation worker: failed to publish LiveStarted", "stream_id", st.ID, "error", err)
		}

		slog.Info("activation worker: auto-activated scheduled stream", "scheduled_id", ss.ID, "stream_id", st.ID)
	}
}

// dvrExpiry runs every hour and deletes DVR segments older than 7 days.
func dvrExpiry(ctx context.Context, store *postgres.Store) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cutoff := time.Now().Add(-7 * 24 * time.Hour)
			n, err := store.ExpireDVRSegments(ctx, cutoff)
			if err != nil {
				slog.Error("dvr expiry: error", "error", err)
				continue
			}
			if n > 0 {
				slog.Info("dvr expiry: deleted old segments", "count", n, "older_than", cutoff)
			}
		}
	}
}

// audioRoomAutoClose runs every 2 minutes and ends audio rooms that have had
// 0 active members for more than 5 minutes.
func audioRoomAutoClose(ctx context.Context, store *postgres.Store) {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := store.EndIdleAudioRooms(ctx, 5)
			if err != nil {
				slog.Error("audio room auto-close: error", "error", err)
				continue
			}
			if len(ids) > 0 {
				slog.Info("audio room auto-close: ended idle rooms", "count", len(ids))
			}
		}
	}
}

// --- Helpers ---

func publishEvent(ctx context.Context, w *kafka.Writer, eventType, actorID string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	env := events.NewEnvelope(ctx, eventType, &actorID, data)
	envData, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return w.WriteMessages(ctx, kafka.Message{
		Key:   []byte(eventType),
		Value: envData,
	})
}

func newStreamKey() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck — crypto/rand.Read never returns an error on supported platforms
	return "live_" + hex.EncodeToString(b)
}
