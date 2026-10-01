package service

// Going live from streaming software or a camera (1 Oct 2026).
//
// An 'encoder' stream takes its media from OBS, a hardware encoder or a
// camera through a LiveKit RTMP ingress instead of a browser publisher:
//
//	create (source=encoder) -> POST /ingress (server URL + stream key)
//	  -> POST /start ('starting', no publisher token)
//	  -> the encoder connects; its track is published as "encoder_<stream id>"
//	  -> 'live' -> encoder stops -> 'reconnecting' -> grace -> ended(host_lost)
//
// The stream key is a credential. It is never stored (the row keeps the
// ingress id and reads the key back from LiveKit), never logged, and leaves
// this service only in the answer to the host's own POST /ingress.

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

// Stream sources.
const (
	SourceDevice  = postgres.SourceDevice
	SourceEncoder = postgres.SourceEncoder

	// encoderParticipantName is what viewers' clients see as the publishing
	// participant's name.
	encoderParticipantName = "Host"

	// DefaultEncoderStartTimeout is how long an encoder stream may stay
	// 'starting': the host has to paste the key and press Start Streaming.
	DefaultEncoderStartTimeout = 10 * time.Minute
)

var (
	// ErrInvalidSource: source is neither device nor encoder.
	ErrInvalidSource = errors.New("invalid: source must be device or encoder")
	// ErrNotEncoderStream: the stream goes live from this device, so it has
	// no server URL or stream key.
	ErrNotEncoderStream = errors.New("invalid: this stream goes live from this device, not from streaming software")
	// ErrIngressUnavailable: LiveKit refused or could not be reached. The
	// message is safe to show; the cause is logged, never returned.
	ErrIngressUnavailable = errors.New("could not set up the connection for your streaming software right now; try again")
)

// normalizeSource maps the request's source to a stored one ("" when it is
// not a source). Absent means this device.
func normalizeSource(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", SourceDevice:
		return SourceDevice
	case SourceEncoder:
		return SourceEncoder
	default:
		return ""
	}
}

// EncoderIdentity is the LiveKit participant identity an encoder stream's
// ingress publishes as. It is built from the stream's row id — never from
// the room name, whose id is not the stream's — and it is not the host's
// user id: the host watches their own stream under that identity and
// LiveKit evicts a duplicate.
func EncoderIdentity(streamID uuid.UUID) string { return "encoder_" + streamID.String() }

// isEncoderIdentity: identity is the participant this stream's ingress
// publishes as (the identity stored on the row when the ingress was issued).
func isEncoderIdentity(st *postgres.LiveStream, identity string) bool {
	return identity != "" && st.Source == SourceEncoder &&
		st.EncoderIdentity != nil && *st.EncoderIdentity == identity
}

// isHostIdentity: identity is the host — the creator's own connection or
// the stream's encoder. Neither is ever counted as a viewer.
func isHostIdentity(st *postgres.LiveStream, identity string) bool {
	if identity == "" {
		return false
	}
	return identity == st.CreatorUserID.String() || isEncoderIdentity(st, identity)
}

// isMediaIdentity: identity is the participant whose tracks are the host's
// media — its first track makes the stream live, its departure is the host
// being lost. On a device stream that is the creator. On an encoder stream
// it is the encoder only: the creator joins with a viewer token to watch,
// publishes nothing, and closing that tab must not put a stream the encoder
// is still feeding into 'reconnecting'.
func isMediaIdentity(st *postgres.LiveStream, identity string) bool {
	if st.Source == SourceEncoder {
		return isEncoderIdentity(st, identity)
	}
	return identity != "" && identity == st.CreatorUserID.String()
}

// decide is the lifecycle table (Next) with the two things that depend on
// the stream's source: an encoder stream's start timeout, and room_finished
// while an encoder stream is waiting for its encoder. LiveKit closes an
// empty room after minutes, sooner than the encoder start timeout, and
// again shortly after the encoder drops; the ingress re-opens the room when
// the encoder (re)connects, so those two waits are ended by their own
// timeouts (failed no_media, ended host_lost) and not by the room closing.
func decide(cur *postgres.LiveStream, trig Trigger, age time.Duration, lim Limits) (postgres.Decision, bool) {
	if cur.Source == SourceEncoder {
		lim.StartTimeout = lim.EncoderStartTimeout
		if trig == TrigRoomFinished && (cur.Status == stStarting || cur.Status == stReconnecting) {
			return postgres.Decision{To: cur.Status}, true
		}
	}
	return Next(cur.Status, trig, age, lim)
}

// IngressResult is what the host pastes into their streaming software.
type IngressResult struct {
	ServerURL string `json:"server_url"`
	StreamKey string `json:"stream_key"`
	IngressID string `json:"ingress_id"`
}

// String never prints the stream key.
func (r IngressResult) String() string {
	return fmt.Sprintf("IngressResult{ServerURL:%s IngressID:%s StreamKey:[redacted]}", r.ServerURL, r.IngressID)
}

// GoString never prints the stream key.
func (r IngressResult) GoString() string { return r.String() }

// LogValue never logs the stream key.
func (r IngressResult) LogValue() slog.Value {
	return slog.GroupValue(slog.String("server_url", r.ServerURL), slog.String("ingress_id", r.IngressID))
}

func ingressResult(ing *livekit.Ingress) *IngressResult {
	return &IngressResult{ServerURL: ing.URL, StreamKey: ing.StreamKey, IngressID: ing.ID}
}

// ingressOpen: the stream has not ended, so its key may be issued or read
// again — also on air, where a host who reloaded the page reads the same
// key back, and after a failed start, where "Try again" keeps it.
func ingressOpen(status string) bool { return status != stEnded }

// CreateIngress answers the server URL and stream key of the stream's
// ingress, issuing one when the stream has none. It is idempotent: while an
// ingress exists the same one is returned. Host only, behind the same pilot
// and live-ban gate as start; the stream must be an encoder stream that has
// not ended.
func (s *Service) CreateIngress(ctx context.Context, streamID, hostID uuid.UUID) (*IngressResult, error) {
	if err := s.requireMayGoLive(ctx, hostID); err != nil {
		return nil, err
	}
	st, err := s.requireCreator(ctx, streamID, hostID)
	if err != nil {
		return nil, err
	}
	if st.Source != SourceEncoder {
		return nil, ErrNotEncoderStream
	}
	if !ingressOpen(st.Status) {
		return nil, ErrStateConflict
	}
	if s.livekit == nil {
		return nil, ErrIngressUnavailable
	}
	// Two rounds: when a concurrent request (or a reset) changed the row
	// between the read and the write, read it again once.
	for attempt := 0; attempt < 2; attempt++ {
		if st.IngressID != nil && *st.IngressID != "" {
			ing, err := s.livekit.GetIngress(ctx, *st.IngressID)
			if err != nil {
				slog.WarnContext(ctx, "live-v2: read ingress", "stream_id", st.ID, "ingress_id", *st.IngressID, "err", err)
				return nil, ErrIngressUnavailable
			}
			if ing != nil {
				return ingressResult(ing), nil
			}
			// LiveKit no longer has it: forget it and issue a new one.
			if err := s.store.ClearIngress(ctx, st.ID, *st.IngressID); err != nil {
				return nil, err
			}
		}
		ing, err := s.livekit.CreateRTMPIngress(ctx, livekit.IngressRequest{
			Name:            "live_" + st.ID.String(),
			Room:            st.LiveKitRoom,
			Identity:        EncoderIdentity(st.ID),
			ParticipantName: encoderParticipantName,
		})
		if err != nil {
			slog.WarnContext(ctx, "live-v2: create ingress", "stream_id", st.ID, "err", err)
			return nil, ErrIngressUnavailable
		}
		stored, err := s.store.SetIngress(ctx, st.ID, ing.ID, EncoderIdentity(st.ID))
		if err == nil && stored {
			slog.InfoContext(ctx, "live-v2: ingress issued", "stream_id", st.ID, "ingress_id", ing.ID)
			return ingressResult(ing), nil
		}
		// Not recorded: nothing may keep a key the row does not know about.
		if derr := s.livekit.DeleteIngress(ctx, ing.ID); derr != nil {
			slog.WarnContext(ctx, "live-v2: delete an unrecorded ingress", "stream_id", st.ID, "ingress_id", ing.ID, "err", derr)
		}
		if err != nil {
			return nil, err
		}
		if st, err = s.store.GetByID(ctx, streamID); err != nil {
			return nil, mapStoreErr(err)
		}
		if !ingressOpen(st.Status) {
			return nil, ErrStateConflict
		}
	}
	return nil, ErrStateConflict
}

// DeleteIngress deletes the stream's ingress, so the key stops working; the
// next CreateIngress issues a new one ("Reset key"). Host only. A stream
// without an ingress is a no-op.
func (s *Service) DeleteIngress(ctx context.Context, streamID, hostID uuid.UUID) error {
	st, err := s.requireCreator(ctx, streamID, hostID)
	if err != nil {
		return err
	}
	if st.IngressID == nil || *st.IngressID == "" {
		return nil
	}
	if s.livekit == nil {
		return ErrIngressUnavailable
	}
	if err := s.livekit.DeleteIngress(ctx, *st.IngressID); err != nil {
		slog.WarnContext(ctx, "live-v2: delete ingress", "stream_id", st.ID, "ingress_id", *st.IngressID, "err", err)
		return ErrIngressUnavailable
	}
	return s.store.ClearIngress(ctx, st.ID, *st.IngressID)
}

// dropIngress deletes an ended stream's ingress and forgets it. Best
// effort: a failure is logged and the sweeper retries (sweepIngresses).
func (s *Service) dropIngress(ctx context.Context, streamID uuid.UUID, ingressID string) {
	if s.livekit == nil || ingressID == "" {
		return
	}
	if err := s.livekit.DeleteIngress(ctx, ingressID); err != nil {
		slog.Warn("live-v2: delete ingress of a finished stream", "stream_id", streamID, "ingress_id", ingressID, "err", err)
		return
	}
	if err := s.store.ClearIngress(ctx, streamID, ingressID); err != nil {
		slog.Warn("live-v2: ingress id not cleared", "stream_id", streamID, "err", err)
	}
}

// failedIngressKeep is how long a stream that failed to start keeps its
// ingress (and so its key) for another attempt.
const failedIngressKeep = 24 * time.Hour

// sweepIngresses retries the ingress deletions that failed when their
// streams ended, and deletes the ingress of a stream left 'failed' for
// longer than failedIngressKeep.
func (s *Service) sweepIngresses(ctx context.Context) {
	if s.livekit == nil {
		return
	}
	left, err := s.store.ListEndedWithIngress(ctx, failedIngressKeep, 100)
	if err != nil {
		slog.Warn("live-v2 sweeper: list ended streams with an ingress", "err", err)
		return
	}
	for _, si := range left {
		s.dropIngress(ctx, si.StreamID, si.IngressID)
	}
}

// withHasIngress returns a copy of st carrying has_ingress, for the host
// and the stream's moderators.
func withHasIngress(st *postgres.LiveStream) *postgres.LiveStream {
	cp := *st
	has := st.IngressID != nil && *st.IngressID != ""
	cp.HasIngress = &has
	return &cp
}
