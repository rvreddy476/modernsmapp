// Package events builds live-service-v2's lifecycle events for the
// transactional outbox (live_v2.outbox_events). They used to be written to
// Kafka fire-and-forget from the request path, so a broker blip lost them;
// now each is enqueued in the SAME transaction as the status change that
// implies it and shared/outbox.Publisher drains the table to the
// social.events.v1 topic (commerce-service's pattern, migration 005).
//
// Payloads are the shared contract (shared/events Live*Payload); the ended
// payload additionally carries ended_reason, which consumers decoding the
// shared struct ignore. The partition key is the creator id (order per
// creator), as before.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	sharedevents "github.com/atpost/shared/events"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// liveStreamEndedPayload is the shared LiveStreamEndedPayload plus
// ended_reason.
type liveStreamEndedPayload struct {
	sharedevents.LiveStreamEndedPayload
	EndedReason string `json:"ended_reason,omitempty"`
}

// StreamStarted is live.stream.started for a stream that just went live.
func StreamStarted(ctx context.Context, st *postgres.LiveStream) (postgres.OutboxEvent, error) {
	startedAt := time.Now().UTC()
	if st.StartedAt != nil {
		startedAt = *st.StartedAt
	}
	return envelope(ctx, sharedevents.LiveStreamStarted, st, "started:"+st.ID.String(), sharedevents.LiveStreamStartedPayload{
		StreamID:   st.ID.String(),
		CreatorID:  st.CreatorUserID.String(),
		Title:      st.Title,
		Visibility: st.Visibility,
		StartedAt:  startedAt,
		// Live surfaces (2 Oct 2026). Who asked to be reminded is NOT in the
		// event: notification-service pages it from
		// GET /v1/livestream/internal/streams/:id/reminders.
		Orientation:   orientationOrDefault(st.Orientation),
		Category:      st.Category,
		CreatorUserID: st.CreatorUserID.String(),
	})
}

// orientationOrDefault never leaves the event's orientation empty: a row
// built without one (a test, an older reader) is a landscape stream.
func orientationOrDefault(o string) string {
	if o == "" {
		return postgres.OrientationLandscape
	}
	return o
}

// StreamEnded is live.stream.ended for a stream that was live and ended.
func StreamEnded(ctx context.Context, st *postgres.LiveStream) (postgres.OutboxEvent, error) {
	endedAt := time.Now().UTC()
	if st.EndedAt != nil {
		endedAt = *st.EndedAt
	}
	reason := ""
	if st.EndedReason != nil {
		reason = *st.EndedReason
	}
	return envelope(ctx, sharedevents.LiveStreamEnded, st, "ended:"+st.ID.String(), liveStreamEndedPayload{
		LiveStreamEndedPayload: sharedevents.LiveStreamEndedPayload{
			StreamID:   st.ID.String(),
			CreatorID:  st.CreatorUserID.String(),
			EndedAt:    endedAt,
			ViewerPeak: st.ViewerPeak,
		},
		EndedReason: reason,
	})
}

// liveStreamVODReadyPayload is the shared payload plus the two extensions
// post-service's consumer reads (post-service internal/consumers/live_vod.go
// LiveVODEvent): media_asset_id, which resolveRecordingMedia takes first,
// and the stream title.
type liveStreamVODReadyPayload struct {
	sharedevents.LiveStreamVODReadyPayload
	MediaAssetID string `json:"media_asset_id"`
	Title        string `json:"title,omitempty"`
}

// VODReady is live.stream.vod_ready once the recording is a registered
// media asset. mediaID is required: post-service skips a VOD it cannot
// resolve to a media asset, so an event without one would be lost work.
func VODReady(ctx context.Context, st *postgres.LiveStream, recordingURL string, durationSec int, mediaID uuid.UUID) (postgres.OutboxEvent, error) {
	if mediaID == uuid.Nil {
		return postgres.OutboxEvent{}, fmt.Errorf("vod_ready without a media id")
	}
	return envelope(ctx, sharedevents.LiveStreamVODReady, st, "vod_ready:"+st.ID.String(), liveStreamVODReadyPayload{
		LiveStreamVODReadyPayload: sharedevents.LiveStreamVODReadyPayload{
			StreamID:     st.ID.String(),
			CreatorID:    st.CreatorUserID.String(),
			RecordingURL: recordingURL,
			DurationSec:  durationSec,
		},
		MediaAssetID: mediaID.String(),
		Title:        st.Title,
	})
}

func envelope(ctx context.Context, eventType string, st *postgres.LiveStream, idemKey string, payload any) (postgres.OutboxEvent, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return postgres.OutboxEvent{}, fmt.Errorf("marshal %s payload: %w", eventType, err)
	}
	actor := st.CreatorUserID.String()
	env := sharedevents.NewEnvelope(ctx, eventType, &actor, payloadBytes)
	envBytes, err := json.Marshal(env)
	if err != nil {
		return postgres.OutboxEvent{}, fmt.Errorf("marshal %s envelope: %w", eventType, err)
	}
	return postgres.OutboxEvent{
		EventType:      eventType,
		PartitionKey:   actor,
		IdempotencyKey: "live." + idemKey,
		Payload:        envBytes,
	}, nil
}
