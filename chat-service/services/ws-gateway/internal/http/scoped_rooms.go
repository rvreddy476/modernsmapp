package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	nethttp "net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

/*
	Scoped rooms (audit L2): call:<id>, update:<id> and group_post:<id>.

	These rooms ride EnableScopedRooms, which stays off in production. Before
	this file, turning it on would have let any socket subscribe to, and
	publish into, any room it named: eavesdrop on a group call's signalling,
	inject call_join/call_leave frames, or read any group post's comment
	feed. Now every subscribe and every publish into one of these rooms is
	authorized by the room's owner first, exactly like post and live rooms:

	  - call:<id>        CallRoomAuthorizer asks call-service (GET
	                     /v1/calls/:id with the connection's own bearer):
	                     the viewer must be a participant who has not left
	                     or been removed, of a call that has not ended.
	  - update:<id>      UpdateRoomAuthorizer (channel-service).
	  - group_post:<id>  GroupPostRoomAuthorizer (group-service).

	A nil authorizer refuses every request for its kind. channel-service and
	group-service expose no viewer-check route today, so those two kinds stay
	closed even with the flag on until one is built and wired.

	A yes is cached per connection for scopedRoomGrantTTL; the reconcile loop
	(server.go) re-asks for every held room on its 30 s tick and evicts on a
	no, or on two undecided answers in a row, with
	  {"type":"subscription_revoked","room":"call","call_id":"<uuid>"}
*/

// ScopedRoomAuthorizer answers whether userID may use room roomID. bearer is
// the connection's own access token, for owners that authorize the user's
// request directly. An error means "could not decide" and refuses.
type ScopedRoomAuthorizer interface {
	MayJoin(ctx context.Context, bearer string, userID uuid.UUID, roomID string) (bool, error)
}

const (
	scopedRoomCall      = "call"
	scopedRoomUpdate    = "update"
	scopedRoomGroupPost = "group_post"

	// scopedRoomGrantTTL bounds how long one yes is trusted before a
	// publish re-asks; the reconcile tick re-asks on the same cadence.
	scopedRoomGrantTTL         = 30 * time.Second
	scopedRoomRecheckBatch     = 200
	scopedRoomRecheckUndecided = 2
)

// scopedRoomIDKey is the frame field that carries each kind's id.
var scopedRoomIDKey = map[string]string{
	scopedRoomCall:      "call_id",
	scopedRoomUpdate:    "update_id",
	scopedRoomGroupPost: "post_id",
}

func (s *Server) scopedRoomAuthorizer(kind string) ScopedRoomAuthorizer {
	switch kind {
	case scopedRoomCall:
		return s.opts.CallRoomAuthorizer
	case scopedRoomUpdate:
		return s.opts.UpdateRoomAuthorizer
	case scopedRoomGroupPost:
		return s.opts.GroupPostRoomAuthorizer
	}
	return nil
}

// mayUseScopedRoom is the one decision for subscribing to, or publishing
// into, a scoped room: the flag is on, the id is a UUID, and the owner said
// yes (now, or within scopedRoomGrantTTL). It returns the room's channel,
// built from the parsed id, never from client text.
func (s *Server) mayUseScopedRoom(ctx context.Context, grants *postRoomGrants, kind string, userID uuid.UUID, bearer, rawID string, now time.Time) (string, bool) {
	if !s.opts.EnableScopedRooms {
		return "", false
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		return "", false
	}
	channel := kind + ":" + id.String()
	if grants.fresh(channel, now) {
		return channel, true
	}
	auth := s.scopedRoomAuthorizer(kind)
	if auth == nil {
		return channel, false
	}
	ok, err := auth.MayJoin(ctx, bearer, userID, id.String())
	if err != nil {
		if s.log != nil {
			s.log.Warn("scoped room authority unresolved", "err", err, "user_id", userID, "room", channel)
		}
		return channel, false
	}
	if !ok {
		return channel, false
	}
	grants.grant(channel, now)
	return channel, true
}

// scopedRoomChannel parses a channel the connection may have joined.
func scopedRoomChannel(kind, rawID string) (string, bool) {
	id, err := uuid.Parse(rawID)
	if err != nil {
		return "", false
	}
	return kind + ":" + id.String(), true
}

// recheckScopedRooms re-authorizes up to limit held scoped rooms and returns
// the channels to drop (their grants already forgotten).
func (s *Server) recheckScopedRooms(ctx context.Context, grants *postRoomGrants, userID uuid.UUID, bearer string, now time.Time, limit int) []string {
	var evicted []string
	for _, channel := range grants.nextBatch(limit) {
		if !grants.held(channel) {
			continue
		}
		kind, id, _ := strings.Cut(channel, ":")
		auth := s.scopedRoomAuthorizer(kind)
		if !s.opts.EnableScopedRooms || auth == nil {
			grants.revoke(channel)
			evicted = append(evicted, channel)
			continue
		}
		ok, err := auth.MayJoin(ctx, bearer, userID, id)
		switch {
		case err != nil:
			if grants.markUndecided(channel) < scopedRoomRecheckUndecided {
				continue
			}
			grants.revoke(channel)
			evicted = append(evicted, channel)
		case !ok:
			grants.revoke(channel)
			evicted = append(evicted, channel)
		default:
			grants.grant(channel, now)
		}
	}
	return evicted
}

// evictScopedRooms leaves each room and tells the client.
func (s *Server) evictScopedRooms(ctx context.Context, pubsub roomUnsubscriber, outbound chan<- []byte, userID uuid.UUID, evicted []string) {
	for _, channel := range evicted {
		if err := pubsub.Unsubscribe(ctx, channel); err != nil && s.log != nil {
			s.log.Warn("scoped room unsubscribe failed", "err", err, "user_id", userID, "room", channel)
		}
		select {
		case outbound <- scopedRoomRevokedFrame(channel):
		default:
		}
	}
}

func scopedRoomRevokedFrame(channel string) []byte {
	kind, id, _ := strings.Cut(channel, ":")
	frame, _ := json.Marshal(map[string]any{"type": "subscription_revoked", "room": kind, scopedRoomIDKey[kind]: id})
	return frame
}

// HTTPCallRoomAuthorizer asks call-service whether the viewer is in a call:
// GET /v1/calls/:id with the connection's own bearer answers 200 only to a
// participant, and the body says whether the call is live and whether the
// viewer is still in it.
type HTTPCallRoomAuthorizer struct {
	baseURL string
	client  *nethttp.Client
}

func NewHTTPCallRoomAuthorizer(baseURL string, client *nethttp.Client) *HTTPCallRoomAuthorizer {
	if client == nil {
		client = &nethttp.Client{Timeout: 3 * time.Second}
	}
	return &HTTPCallRoomAuthorizer{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (a *HTTPCallRoomAuthorizer) MayJoin(ctx context.Context, bearer string, userID uuid.UUID, callID string) (bool, error) {
	if a == nil || a.baseURL == "" {
		return false, fmt.Errorf("call room authority not configured")
	}
	if bearer == "" {
		return false, fmt.Errorf("no bearer token for call room check")
	}
	u := fmt.Sprintf("%s/v1/calls/%s", a.baseURL, url.PathEscape(callID))
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := a.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case nethttp.StatusOK:
	case nethttp.StatusForbidden, nethttp.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("call room authority returned %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			State        string `json:"state"`
			Participants []struct {
				UserID    string `json:"user_id"`
				JoinState string `json:"join_state"`
			} `json:"participants"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 256*1024)).Decode(&out); err != nil {
		return false, err
	}
	switch out.Data.State {
	case "initiated", "ringing", "active":
	default:
		return false, nil
	}
	for _, p := range out.Data.Participants {
		if strings.EqualFold(p.UserID, userID.String()) {
			return p.JoinState != "left" && p.JoinState != "removed", nil
		}
	}
	return false, nil
}
