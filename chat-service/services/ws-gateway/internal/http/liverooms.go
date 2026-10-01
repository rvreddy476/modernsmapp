package http

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

/*
	live:stream:<id> rooms — the real-time side of a live stream (chat,
	status, viewer count, moderation events; live-service-v2 publishes them
	all as JSON with a `type`).

	The rule mirrors post rooms (postrooms.go): before a socket joins
	`live:stream:<id>` the gateway asks the room's owner, live-service-v2,
	the same question its viewer-token route answers (visibility, blocks,
	bans) through the internal route
	  GET /v1/livestream/internal/streams/<id>/viewer?user_id=<uuid>
	  -> {"data":{"allowed":true|false}}
	and joins only on a plain yes. Anything else — a no, a non-200, a body
	without data.allowed, a timeout, no authority configured — refuses. The
	room name is derived from the stream id this gateway parsed, never from
	client text.

	A yes is trusted for at most liveRoomGrantTTL (30s):
	  - a re-subscribe inside that window does not re-ask;
	  - every liveRoomRecheckInterval (30s) the connection's reconcile loop
	    re-asks for every held live room and evicts on anything but a yes
	    (no streak tolerance here — a host's block or ban must land within
	    one sweep, and an undecided answer is a refusal);
	  - a socket never keeps a grant past its own lifetime, so a reconnect
	    always re-asks.

	Client protocol:
	  {"type":"subscribe_live_stream","stream_id":"<uuid>"}
	  {"type":"unsubscribe_live_stream","stream_id":"<uuid>"}
	A refused subscribe is answered with
	  {"type":"error","code":"LIVE_ROOM_REFUSED","stream_id":"<uuid>"}
	(stream_id is omitted when the id did not parse) and no subscription;
	if the socket already held that room it is left as well. A seat dropped
	by the periodic re-check is announced with the post/conversation room
	control frame:
	  {"type":"subscription_revoked","stream_id":"<uuid>"}
	The client's fallback on either frame is the authorized HTTP chat list.
*/

// LiveViewAuthorizer answers whether viewer may watch stream. An error
// means "could not decide"; both a false and an error refuse the room.
type LiveViewAuthorizer interface {
	MayWatch(ctx context.Context, viewerID, streamID string) (bool, error)
}

// HTTPLiveViewAuthorizer asks live-service-v2's internal viewer route.
type HTTPLiveViewAuthorizer struct {
	baseURL     string
	internalKey string
	client      *nethttp.Client
}

func NewHTTPLiveViewAuthorizer(baseURL, internalKey string, client *nethttp.Client) *HTTPLiveViewAuthorizer {
	if client == nil {
		client = &nethttp.Client{Timeout: 3 * time.Second}
	}
	return &HTTPLiveViewAuthorizer{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: client}
}

func (a *HTTPLiveViewAuthorizer) MayWatch(ctx context.Context, viewerID, streamID string) (bool, error) {
	if a == nil || a.baseURL == "" {
		return false, fmt.Errorf("live view authority not configured")
	}
	u := fmt.Sprintf("%s/v1/livestream/internal/streams/%s/viewer?user_id=%s",
		a.baseURL, url.PathEscape(streamID), url.QueryEscape(viewerID))
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	if a.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", a.internalKey)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK {
		return false, fmt.Errorf("live view authority returned %d", resp.StatusCode)
	}
	var out struct {
		Data *struct {
			Allowed *bool `json:"allowed"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	// A 200 that does not say "allowed" either way is not a yes: a proxy
	// error page, an older route shape, or an empty envelope must refuse.
	if out.Data == nil || out.Data.Allowed == nil {
		return false, fmt.Errorf("live view authority answered without data.allowed")
	}
	return *out.Data.Allowed, nil
}

// liveRoomGrantTTL is the longest a yes is reused without re-asking.
const liveRoomGrantTTL = 30 * time.Second

// Re-check cadence and bound for held live rooms. The interval equals the
// grant TTL, so no seat is older than one interval without a fresh yes.
const (
	liveRoomRecheckInterval = 30 * time.Second
	liveRoomRecheckBatch    = 50
)

// liveChannel is the one place the room name is built, from a parsed id.
func liveChannel(streamID string) string {
	return "live:stream:" + streamID
}

// newLiveRoomGrants is the per-connection grant set for live rooms. It is
// the same structure post rooms use (a per-id time of the last yes, which
// is also the set of rooms held), with the live TTL.
func newLiveRoomGrants() *postRoomGrants {
	return newPostRoomGrants(liveRoomGrantTTL)
}

// mayJoinLiveRoom is the one decision: rooms enabled, an authority, a
// well-formed stream id, and the authority's yes (or a fresh cached yes).
// It returns the canonical id whenever the raw id parsed, refused or not.
func (s *Server) mayJoinLiveRoom(ctx context.Context, grants *postRoomGrants, userID uuid.UUID, rawStreamID string, now time.Time) (string, bool) {
	if !s.opts.EnableLiveRooms || s.opts.LiveViewer == nil {
		return "", false
	}
	streamID, err := uuid.Parse(rawStreamID)
	if err != nil {
		return "", false
	}
	id := streamID.String()
	if grants.fresh(id, now) {
		return id, true
	}
	ok, err := s.opts.LiveViewer.MayWatch(ctx, userID.String(), id)
	if err != nil {
		if s.log != nil {
			s.log.Warn("live room authority unresolved", "err", err, "user_id", userID, "stream_id", id)
		}
		return id, false
	}
	if !ok {
		return id, false
	}
	grants.grant(id, now)
	return id, true
}

// roomSubscriber is the pubsub surface the live subscribe path needs;
// *redis.PubSub satisfies it.
type roomSubscriber interface {
	Subscribe(ctx context.Context, channels ...string) error
	Unsubscribe(ctx context.Context, channels ...string) error
}

// handleLiveSubscribe runs one subscribe_live_stream frame: join on a yes,
// otherwise answer with the refusal frame, forget any grant and leave the
// room if this socket already held it (a later "no" must not leave an
// earlier seat open).
func (s *Server) handleLiveSubscribe(ctx context.Context, pubsub roomSubscriber, outbound chan<- []byte, grants *postRoomGrants, userID uuid.UUID, rawStreamID string, now time.Time) bool {
	streamID, ok := s.mayJoinLiveRoom(ctx, grants, userID, rawStreamID, now)
	if !ok {
		if s.log != nil {
			s.log.Info("live room subscribe refused", "user_id", userID, "stream_id", rawStreamID)
		}
		if streamID != "" {
			grants.revoke(streamID)
			if err := pubsub.Unsubscribe(ctx, liveChannel(streamID)); err != nil && s.log != nil {
				s.log.Warn("live room unsubscribe failed", "err", err, "user_id", userID, "stream_id", streamID)
			}
		}
		select {
		case outbound <- liveRoomRefusedFrame(streamID):
		default:
		}
		return false
	}
	if err := pubsub.Subscribe(ctx, liveChannel(streamID)); err != nil {
		grants.revoke(streamID)
		if s.log != nil {
			s.log.Warn("live room subscribe failed", "err", err, "user_id", userID, "stream_id", streamID)
		}
		select {
		case outbound <- liveRoomRefusedFrame(streamID):
		default:
		}
		return false
	}
	return true
}

// recheckLiveRooms re-asks the authority for up to limit held live rooms
// and returns the ids to drop. Anything but a yes evicts at once.
func (s *Server) recheckLiveRooms(ctx context.Context, grants *postRoomGrants, userID uuid.UUID, now time.Time, limit int) []string {
	var evicted []string
	for _, streamID := range grants.nextBatch(limit) {
		if !grants.held(streamID) {
			continue
		}
		ok := false
		if s.opts.EnableLiveRooms && s.opts.LiveViewer != nil {
			var err error
			ok, err = s.opts.LiveViewer.MayWatch(ctx, userID.String(), streamID)
			if err != nil {
				ok = false
				if s.log != nil {
					s.log.Warn("live room re-check unresolved; evicting", "err", err, "user_id", userID, "stream_id", streamID)
				}
			}
		}
		if ok {
			grants.grant(streamID, now)
			continue
		}
		grants.revoke(streamID)
		evicted = append(evicted, streamID)
	}
	return evicted
}

// evictLiveRooms leaves each room and tells the client, without blocking
// on a slow client (the seat is already gone either way).
func (s *Server) evictLiveRooms(ctx context.Context, pubsub roomUnsubscriber, outbound chan<- []byte, userID uuid.UUID, evicted []string) {
	for _, streamID := range evicted {
		if s.log != nil {
			s.log.Info("live room dropped by re-check", "user_id", userID, "stream_id", streamID)
		}
		if err := pubsub.Unsubscribe(ctx, liveChannel(streamID)); err != nil && s.log != nil {
			s.log.Warn("live room unsubscribe failed", "err", err, "user_id", userID, "stream_id", streamID)
		}
		select {
		case outbound <- liveRoomRevokedFrame(streamID):
		default:
		}
	}
}

// refuseLiveSubscribe answers a subscribe_live_stream the beta gate
// rejected (live rooms disabled) with the same refusal frame. Nothing is
// asked and nothing is subscribed.
func (s *Server) refuseLiveSubscribe(outbound chan<- []byte, rawStreamID any) {
	streamID := ""
	if raw, ok := rawStreamID.(string); ok {
		if parsed, err := uuid.Parse(raw); err == nil {
			streamID = parsed.String()
		}
	}
	select {
	case outbound <- liveRoomRefusedFrame(streamID):
	default:
	}
}

func liveRoomRefusedFrame(streamID string) []byte {
	m := map[string]any{"type": "error", "code": "LIVE_ROOM_REFUSED"}
	if streamID != "" {
		m["stream_id"] = streamID
	}
	frame, _ := json.Marshal(m)
	return frame
}

func liveRoomRevokedFrame(streamID string) []byte {
	frame, _ := json.Marshal(map[string]any{"type": "subscription_revoked", "stream_id": streamID})
	return frame
}
