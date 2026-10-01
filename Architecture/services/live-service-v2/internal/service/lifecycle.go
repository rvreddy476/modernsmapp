package service

// Truthful lifecycle (1 Oct 2026).
//
//	scheduled ──start──▶ starting ──host track──▶ live ◀──host track── reconnecting
//	                       │                       │  └──host left/unpublished──▶ │
//	                       │ (> LIVE_START_TIMEOUT,│                              │ (> LIVE_RECONNECT_GRACE)
//	                       ▼  no host track)       ▼                              ▼
//	                     failed(no_media)        ended(host_ended | room_finished | admin_stopped | host_lost)
//
// Next is the whole table, pure and clock-free (the age of the current
// status is measured by the database and handed in). Every write goes
// through Store.ApplyTransition, which runs Next on the locked row.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/events"
	"github.com/atpost/live-service-v2/internal/livekit"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// Trigger is what happened to a stream.
type Trigger int

const (
	TrigStart        Trigger = iota + 1 // host POST /start
	TrigHostTrack                       // LiveKit track_published by the host identity
	TrigHostLost                        // host participant_left / track_unpublished (no tracks left)
	TrigHostEnd                         // host POST /end
	TrigRoomFinished                    // LiveKit room_finished
	TrigAdminStop                       // admin stop
	TrigTimeout                         // sweeper: the current status outlived its limit
)

// Ended reasons.
const (
	ReasonHostEnded    = "host_ended"
	ReasonHostLost     = "host_lost"
	ReasonRoomFinished = "room_finished"
	ReasonAdminStopped = "admin_stopped"
	ReasonNoMedia      = "no_media"
)

// Limits are the two timeouts the sweeper applies.
type Limits struct {
	StartTimeout   time.Duration
	ReconnectGrace time.Duration
}

const (
	stScheduled    = postgres.StatusScheduled
	stStarting     = postgres.StatusStarting
	stLive         = postgres.StatusLive
	stReconnecting = postgres.StatusReconnecting
	stEnded        = postgres.StatusEnded
	stFailed       = postgres.StatusFailed
)

// Next decides the next status for cur under trig. ok=false refuses the
// trigger in this status; a Decision whose To equals cur is a no-op.
func Next(cur string, trig Trigger, age time.Duration, lim Limits) (postgres.Decision, bool) {
	same := postgres.Decision{To: cur}
	end := func(reason string) (postgres.Decision, bool) {
		return postgres.Decision{To: stEnded, Reason: reason}, true
	}
	switch trig {
	case TrigStart:
		switch cur {
		case stScheduled, stFailed:
			return postgres.Decision{To: stStarting}, true
		case stStarting, stLive, stReconnecting:
			return same, true // a rejoining host gets a fresh token, nothing moves
		}
	case TrigHostTrack:
		switch cur {
		case stStarting, stReconnecting:
			return postgres.Decision{To: stLive}, true
		case stLive:
			return same, true
		}
	case TrigHostLost:
		switch cur {
		case stLive:
			return postgres.Decision{To: stReconnecting}, true
		case stStarting, stReconnecting:
			return same, true
		}
	case TrigHostEnd:
		switch cur {
		case stScheduled, stStarting, stLive, stReconnecting:
			return end(ReasonHostEnded)
		case stEnded, stFailed:
			return same, true
		}
	case TrigRoomFinished:
		switch cur {
		case stStarting, stLive, stReconnecting:
			return end(ReasonRoomFinished)
		case stScheduled, stEnded, stFailed:
			return same, true
		}
	case TrigAdminStop:
		switch cur {
		case stScheduled, stStarting, stLive, stReconnecting:
			return end(ReasonAdminStopped)
		}
	case TrigTimeout:
		switch cur {
		case stStarting:
			if age >= lim.StartTimeout {
				return postgres.Decision{To: stFailed, Reason: ReasonNoMedia}, true
			}
			return same, true
		case stReconnecting:
			if age >= lim.ReconnectGrace {
				return end(ReasonHostLost)
			}
			return same, true
		default:
			return same, true
		}
	}
	return postgres.Decision{}, false
}

// lifecycleEvents is the outbox rows a committed change implies:
// live.stream.started on the FIRST move to live, live.stream.ended when a
// stream that was ever live ends. Idempotency keys make each fire once per
// stream.
func lifecycleEvents(ctx context.Context) postgres.EventsFunc {
	return func(prev, next *postgres.LiveStream) ([]postgres.OutboxEvent, error) {
		var out []postgres.OutboxEvent
		if next.Status == stLive && prev.StartedAt == nil && next.StartedAt != nil {
			e, err := events.StreamStarted(ctx, next)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		if next.Status == stEnded && !postgres.IsTerminal(prev.Status) && next.StartedAt != nil {
			e, err := events.StreamEnded(ctx, next)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
		return out, nil
	}
}

// transition applies trig to the stream and, after commit, the side effects
// (egress, room close) and the status.changed room event.
func (s *Service) transition(ctx context.Context, id uuid.UUID, trig Trigger, audit *postgres.AuditEntry) (*postgres.TransitionResult, error) {
	lim := s.limits
	res, err := s.store.ApplyTransition(ctx, id, func(cur *postgres.LiveStream, age time.Duration) (postgres.Decision, bool) {
		return Next(cur.Status, trig, age, lim)
	}, lifecycleEvents(ctx), audit)
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		return nil, ErrStreamNotFound
	case errors.Is(err, postgres.ErrStateConflict):
		return nil, ErrStateConflict
	case err != nil:
		return nil, err
	}
	if res.Changed {
		s.afterTransition(ctx, res, trig)
	}
	return res, nil
}

// afterTransition runs the best-effort side effects of a committed change.
// None of them can un-commit the status: the database is the truth.
func (s *Service) afterTransition(ctx context.Context, res *postgres.TransitionResult, trig Trigger) {
	next := res.Next
	if next.Status == stLive && res.Prev.Status == stStarting && (next.EgressID == nil || *next.EgressID == "") && s.livekit != nil {
		objectKey := recordingObjectKeyPrefix + next.ID.String() + ".mp4"
		if egressID, err := s.livekit.StartEgressToS3(ctx, next.LiveKitRoom, objectKey); err != nil {
			// The broadcast goes on without a recording.
			slog.Warn("live-v2: egress start failed; live without recording", "stream_id", next.ID, "err", err)
		} else if err := s.store.SetEgressID(ctx, next.ID, egressID); err != nil {
			slog.Warn("live-v2: egress id not stored", "stream_id", next.ID, "err", err)
		}
	}
	if postgres.IsTerminal(next.Status) && s.livekit != nil {
		if next.EgressID != nil && *next.EgressID != "" {
			if err := s.livekit.StopEgress(ctx, *next.EgressID); err != nil {
				slog.Warn("live-v2: stop egress", "stream_id", next.ID, "err", err)
			}
		}
		// room_finished means LiveKit already closed it; admin stop closed
		// it before the transition. Everything else closes it now so no
		// viewer stays connected to an ended stream.
		if trig != TrigRoomFinished && trig != TrigAdminStop {
			if err := s.livekit.DeleteRoom(ctx, next.LiveKitRoom); err != nil {
				slog.Warn("live-v2: close room", "stream_id", next.ID, "err", err)
			}
		}
	}
	s.publish(ctx, next.ID, EventStatusChanged, statusPayload(next))
}

func statusPayload(st *postgres.LiveStream) map[string]any {
	return map[string]any{
		"stream_id":         st.ID.String(),
		"status":            st.Status,
		"ended_reason":      st.EndedReason,
		"status_changed_at": st.StatusChangedAt,
		"started_at":        st.StartedAt,
		"ended_at":          st.EndedAt,
		"viewer_count":      st.ViewerCount,
		"viewer_peak":       st.ViewerPeak,
	}
}

// --- LiveKit webhooks ---

// WebhookEvent is the slice of a verified LiveKit WebhookEvent the service
// acts on (internal/http/webhook.go parses and verifies it).
type WebhookEvent struct {
	ID    string
	Event string
	Room  string
	// Participant is the event's participant (identity and the tracks it
	// still has, as LiveKit reported them).
	ParticipantIdentity  string
	ParticipantTrackSIDs []string
	// TrackSID is the published/unpublished track.
	TrackSID string
	Egress   *EgressResult
}

// EgressResult is an egress_ended payload's outcome.
type EgressResult struct {
	EgressID   string
	RoomName   string
	Status     string // EGRESS_COMPLETE, EGRESS_FAILED, ...
	Location   string
	Filename   string // the object key egress wrote
	DurationNs int64
}

// HandleWebhook applies one verified LiveKit event. It is idempotent on the
// event id: an id already applied is acknowledged without re-applying, and
// an id is recorded only after it was applied, so a failure (returned as an
// error, answered 5xx so LiveKit retries) is retried in full.
func (s *Service) HandleWebhook(ctx context.Context, ev WebhookEvent) error {
	if ev.ID != "" {
		seen, err := s.store.WebhookSeen(ctx, ev.ID)
		if err != nil {
			return err
		}
		if seen {
			return nil
		}
	}
	if err := s.applyWebhook(ctx, ev); err != nil {
		return err
	}
	if ev.ID != "" {
		return s.store.MarkWebhook(ctx, ev.ID, ev.Event)
	}
	return nil
}

func (s *Service) applyWebhook(ctx context.Context, ev WebhookEvent) error {
	room := ev.Room
	if ev.Event == "egress_ended" && ev.Egress != nil && ev.Egress.RoomName != "" {
		room = ev.Egress.RoomName
	}
	streamID, ok := StreamIDFromRoom(room)
	if !ok {
		return nil // not one of ours
	}
	st, err := s.store.GetByID(ctx, streamID)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	isHost := ev.ParticipantIdentity != "" && ev.ParticipantIdentity == st.CreatorUserID.String()

	var trig Trigger
	switch ev.Event {
	case "track_published":
		if isHost {
			trig = TrigHostTrack
		}
	case "track_unpublished":
		if isHost && len(remainingTracks(ev.ParticipantTrackSIDs, ev.TrackSID)) == 0 {
			trig = TrigHostLost
		}
	case "participant_left", "participant_connection_aborted":
		if isHost {
			trig = TrigHostLost
		} else {
			return s.applyPresence(ctx, st, ev.ParticipantIdentity, false)
		}
	case "participant_joined":
		if !isHost {
			return s.applyPresence(ctx, st, ev.ParticipantIdentity, true)
		}
	case "room_finished":
		trig = TrigRoomFinished
	case "egress_ended":
		return s.onEgressEnded(ctx, st, ev.Egress)
	}
	if trig == 0 {
		return nil
	}
	_, err = s.transition(ctx, streamID, trig, nil)
	if errors.Is(err, ErrStateConflict) || errors.Is(err, ErrStreamNotFound) {
		return nil // the event does not apply in this status: acknowledged, ignored
	}
	return err
}

func remainingTracks(all []string, gone string) []string {
	var out []string
	for _, sid := range all {
		if sid != "" && sid != gone {
			out = append(out, sid)
		}
	}
	return out
}

// applyPresence records a viewer join/leave and publishes viewer.count
// (throttled to one per 5s per stream).
func (s *Service) applyPresence(ctx context.Context, st *postgres.LiveStream, identity string, present bool) error {
	userID, err := uuid.Parse(identity)
	if err != nil || userID == uuid.Nil {
		return nil // not a user identity (e.g. an egress participant)
	}
	next, changed, err := s.store.ApplyPresence(ctx, st.ID, userID, present)
	if errors.Is(err, postgres.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if changed {
		s.publishViewerCount(ctx, next)
	}
	return nil
}

// viewerCountWindow: viewer.count goes out at most once per window per stream.
const viewerCountWindow = 5 * time.Second

func (s *Service) publishViewerCount(ctx context.Context, st *postgres.LiveStream) {
	if s.rt == nil || !s.rt.Allow(ctx, "live:viewer_count_tick:"+st.ID.String(), viewerCountWindow) {
		return
	}
	s.publish(ctx, st.ID, EventViewerCount, map[string]any{
		"stream_id":    st.ID.String(),
		"viewer_count": st.ViewerCount,
		"viewer_peak":  st.ViewerPeak,
	})
}

// onEgressEnded is in recording.go.

// StreamIDFromRoom unpacks "stream_<uuid>".
func StreamIDFromRoom(room string) (uuid.UUID, bool) {
	const prefix = "stream_"
	if len(room) <= len(prefix) || room[:len(prefix)] != prefix {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(room[len(prefix):])
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// --- sweeper ---

// SweepInterval is how often the sweeper runs.
const SweepInterval = 15 * time.Second

// webhookIDRetention bounds the idempotency table.
const webhookIDRetention = 24 * time.Hour

// Sweep applies the timeouts (database clock), reconciles on-air streams
// against LiveKit's own participant list, re-announces viewer counts and
// prunes old webhook ids. Safe to run on every replica at once: each change
// is a locked transition that re-checks its condition.
func (s *Service) Sweep(ctx context.Context) error {
	ids, err := s.store.ListDueForTimeout(ctx, s.limits.StartTimeout, s.limits.ReconnectGrace, 200)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := s.transition(ctx, id, TrigTimeout, nil); err != nil && !errors.Is(err, ErrStateConflict) && !errors.Is(err, ErrStreamNotFound) {
			slog.Warn("live-v2 sweeper: timeout transition", "stream_id", id, "err", err)
		}
	}
	s.reconcile(ctx)
	s.RunImports(ctx)
	if err := s.store.PruneWebhookEvents(ctx, webhookIDRetention); err != nil {
		slog.Warn("live-v2 sweeper: prune webhook ids", "err", err)
	}
	return nil
}

// reconcile asks LiveKit who is in each on-air room, so a lost webhook can
// neither keep a Live badge on a vanished host nor keep a publishing host
// stuck in starting/reconnecting. Only a definite answer moves anything: a
// LiveKit error changes nothing.
func (s *Service) reconcile(ctx context.Context) {
	if s.livekit == nil {
		return
	}
	streams, err := s.store.ListByStatuses(ctx, []string{stStarting, stLive, stReconnecting}, 200)
	if err != nil {
		slog.Warn("live-v2 sweeper: list on-air streams", "err", err)
		return
	}
	for _, as := range streams {
		st := &as.LiveStream
		parts, err := s.livekit.ListParticipants(ctx, st.LiveKitRoom)
		if errors.Is(err, livekit.ErrRoomNotFound) {
			parts, err = nil, nil
		}
		if err != nil {
			continue
		}
		hostPublishing := false
		for _, p := range parts {
			if p.Identity == st.CreatorUserID.String() && len(p.Tracks) > 0 {
				hostPublishing = true
			}
		}
		var trig Trigger
		switch {
		case hostPublishing && st.Status != stLive:
			trig = TrigHostTrack
		case !hostPublishing && st.Status == stLive:
			trig = TrigHostLost
		}
		if trig != 0 {
			if _, err := s.transition(ctx, st.ID, trig, nil); err != nil && !errors.Is(err, ErrStateConflict) {
				slog.Warn("live-v2 sweeper: reconcile", "stream_id", st.ID, "err", err)
			}
			continue
		}
		if st.Status == stLive {
			s.publishViewerCount(ctx, st)
		}
	}
}

// RunSweeper runs Sweep every interval until ctx ends.
func (s *Service) RunSweeper(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = SweepInterval
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.Sweep(ctx); err != nil {
				slog.Warn("live-v2 sweeper", "err", err)
			}
		}
	}
}

// marshalDetail is a helper for audit detail objects.
func marshalDetail(v map[string]any) []byte {
	b, _ := json.Marshal(v)
	return b
}
