package service

// Recording parts (15 Nov 2026, migration 008).
//
// The gap: a stream records through ONE LiveKit room-composite egress,
// started on the first move to live. When an encoder (OBS, RTMP ingress)
// drops long enough for LiveKit to close the room, the egress ends with the
// room; the encoder's reconnect makes LiveKit open a NEW room under the same
// name, the stream goes reconnecting -> live, and nothing recorded again.
// Everything after the reconnect was missing from the saved video.
//
// Now every egress is a part (store live_recording_parts):
//
//   - ensureRecording reserves the next part under the stream's row lock
//     only while the stream is LIVE and no part is starting or recording,
//     then starts an egress for it and attaches the egress id under the same
//     lock. It runs on every move to live (starting -> live, and
//     reconnecting -> live), on an egress_ended that leaves a live stream
//     with nothing recording while LiveKit shows the host publishing (the
//     old room's egress_ended may arrive after the host is back in the new
//     room), and from the sweeper's reconcile for the same case when a
//     webhook was lost. A reconnect into the SAME room finds the old part
//     still recording and starts nothing.
//   - An ended or admin-stopped stream never gets a part: the reservation
//     needs status live; a part whose stream ended between reservation and
//     attach is failed and its egress stopped; the terminal transition stops
//     every part still recording. egress_ended after the end only finishes
//     its part. Admin stop deletes the room BEFORE its transition commits,
//     so an egress_ended in that window sees a live stream — but LiveKit no
//     longer lists the host, and the restart needs the host publishing.
//   - Duplicate and out-of-order egress_ended: a part already complete or
//     failed is left alone; the event id dedupe in HandleWebhook comes first.
//   - At most maxRecordingParts per stream, so a failing egress cannot loop.
//
// The import. media-service imports ONE MP4 per stream (idempotent on
// source_ref = the stream id) and post-service makes ONE video per stream,
// so neither "parts as separate videos" nor a join is something the
// existing import supports, and joining MP4s needs a remux (ffmpeg) this
// service does not have. The smallest correct step here: every part is kept
// in the recordings bucket under its own key (nothing overwrites part 1),
// the import waits until the stream has ended and no part is still
// recording, and then imports the LONGEST complete part (ties: the
// earliest). A join belongs in media-service's worker (it already runs
// ffmpeg) and can read the part list from live_recording_parts.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

const (
	// maxRecordingParts caps the egresses of one stream.
	maxRecordingParts = 20
	// staleRecordingStart: a reservation with no egress id after this long
	// is taken for abandoned (a crash between reserve and attach).
	staleRecordingStart = 2 * time.Minute
	// importWaitActive: how long after the end a part still recording holds
	// the import back (its egress_ended may never come).
	importWaitActive = time.Hour
	// awaitingImportWindow bounds the sweeper's backstop query.
	awaitingImportWindow = 7 * 24 * time.Hour
)

// recordingPartKey is part n's object key: part 1 keeps the key every
// recording used before parts; later parts carry the stream id too, as
// media-service requires of an imported key.
func recordingPartKey(streamID uuid.UUID, part int) string {
	if part <= 1 {
		return recordingObjectKeyPrefix + streamID.String() + ".mp4"
	}
	return fmt.Sprintf("%s%s_part%d.mp4", recordingObjectKeyPrefix, streamID, part)
}

// recordingURLForKey is the recording URL of an object key in the bucket.
func (s *Service) recordingURLForKey(key string) string {
	if s.recordingPublicBaseURL != "" {
		return strings.TrimRight(s.recordingPublicBaseURL, "/") + "/" + key
	}
	if s.s3Endpoint != "" && s.s3Bucket != "" {
		return strings.TrimRight(s.s3Endpoint, "/") + "/" + s.s3Bucket + "/" + key
	}
	return key
}

// ensureRecording starts the stream's next recording part when it is live
// and nothing records it. Best effort: the broadcast goes on without one.
func (s *Service) ensureRecording(ctx context.Context, st *postgres.LiveStream) {
	if s.livekit == nil {
		return
	}
	part, ok, err := s.store.ReserveRecordingPart(ctx, st.ID, postgres.ReservePartParams{
		KeyFor:     func(n int) string { return recordingPartKey(st.ID, n) },
		MaxParts:   maxRecordingParts,
		StaleStart: staleRecordingStart,
	})
	if err != nil {
		slog.Warn("live-v2: reserve recording part", "stream_id", st.ID, "err", err)
		return
	}
	if !ok {
		return
	}
	egressID, err := s.livekit.StartEgressToS3(ctx, st.LiveKitRoom, part.ObjectKey)
	if err != nil || egressID == "" {
		slog.Warn("live-v2: egress start failed; live without recording", "stream_id", st.ID, "part", part.Part, "err", err)
		msg := "egress start failed"
		if err != nil {
			msg += ": " + err.Error()
		}
		if ferr := s.store.FailRecordingPart(ctx, st.ID, part.Part, msg); ferr != nil {
			slog.Warn("live-v2: recording part not failed", "stream_id", st.ID, "err", ferr)
		}
		return
	}
	attached, err := s.store.AttachRecordingEgress(ctx, st.ID, part.Part, egressID)
	if err != nil {
		slog.Warn("live-v2: egress id not stored", "stream_id", st.ID, "part", part.Part, "err", err)
	}
	if !attached {
		// The stream ended meanwhile (or the reservation was abandoned): an
		// egress no part records must not keep running.
		if err := s.livekit.StopEgress(ctx, egressID); err != nil {
			slog.Warn("live-v2: stop an unattached egress", "stream_id", st.ID, "err", err)
		}
		return
	}
	slog.Info("live-v2: recording part started", "stream_id", st.ID, "part", part.Part)
}

// hostPublishing asks LiveKit whether the stream's media identity is
// publishing in its room now. Undecidable (LiveKit down) is false.
func (s *Service) hostPublishing(ctx context.Context, st *postgres.LiveStream) bool {
	if s.livekit == nil {
		return false
	}
	parts, err := s.livekit.ListParticipants(ctx, st.LiveKitRoom)
	if err != nil {
		if !errors.Is(err, livekit.ErrRoomNotFound) {
			slog.Warn("live-v2: list participants", "stream_id", st.ID, "err", err)
		}
		return false
	}
	for _, p := range parts {
		if isMediaIdentity(st, p.Identity) && len(p.Tracks) > 0 {
			return true
		}
	}
	return false
}

// stopRecording stops every part still recording (and a pre-parts egress).
func (s *Service) stopRecording(ctx context.Context, st *postgres.LiveStream) {
	if s.livekit == nil {
		return
	}
	ids, err := s.store.ActiveRecordingEgresses(ctx, st.ID)
	if err != nil {
		slog.Warn("live-v2: list recording egresses", "stream_id", st.ID, "err", err)
	}
	if st.EgressID != nil && *st.EgressID != "" {
		ids = append(ids, *st.EgressID)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := s.livekit.StopEgress(ctx, id); err != nil {
			slog.Warn("live-v2: stop egress", "stream_id", st.ID, "err", err)
		}
	}
}

// queueImport queues the stream's import once it ended and nothing records
// it any more (QueueRecordingImport decides).
func (s *Service) queueImport(ctx context.Context, streamID uuid.UUID) error {
	queued, err := s.store.QueueRecordingImport(ctx, streamID, postgres.QueueImportParams{
		Bucket:     s.s3Bucket,
		URLFor:     s.recordingURLForKey,
		WaitActive: importWaitActive,
	})
	if queued {
		slog.Info("live-v2: recording import queued", "stream_id", streamID)
	}
	return mapStoreErr(err)
}

// sweepRecordings queues the imports no webhook or transition queued.
func (s *Service) sweepRecordings(ctx context.Context) {
	ids, err := s.store.ListAwaitingImport(ctx, awaitingImportWindow, 50)
	if err != nil {
		slog.Warn("live-v2 sweeper: list streams awaiting import", "err", err)
		return
	}
	for _, id := range ids {
		if err := s.queueImport(ctx, id); err != nil {
			slog.Warn("live-v2 sweeper: queue import", "stream_id", id, "err", err)
		}
	}
}
