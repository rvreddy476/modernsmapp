package http

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

/*
	post:<id> rooms — the live thread under a post.

	A viewer who may read a post may hear that its comments changed. That is
	the whole rule: before a socket joins `post:<id>` the gateway asks
	post-service the same question GET /v1/posts/:id answers (review status,
	scheduled/hidden, visibility, private accounts, blocks), and joins only
	on a plain "yes". Nothing else about the frame is trusted: the room
	name is derived from the post id the service confirmed, and the frames
	relayed into the room (post-service's `comment_change`) carry ids and
	counts, never a comment body, so a subscriber still reads the thread
	through the authorized list endpoint.

	The decision is cached per connection: a re-subscribe (the client's
	reconnect and 30s reconcile both resend subscribe_post) does not re-ask
	inside the cache window, and a socket never keeps a room past its own
	lifetime — there is no server-side room memory across reconnects.

	Client protocol (what the web/mobile sends after connecting):
	  {"type":"subscribe_post","post_id":"<uuid>"}
	  {"type":"unsubscribe_post","post_id":"<uuid>"}
	and what it receives while subscribed:
	  {"type":"comment_change","payload":{event_id, version, post_id,
	   comment_id, parent_id?, change, actor_id, comments}}
	  {"type":"post_update","payload":{post_id, update_type, actor_id,
	   likes, comments, shares, comment_id?}}   (the older count frame)
	The gateway suppresses a frame whose payload.actor_id is the receiving
	user, so a client never hears its own mutation twice.
*/

// PostViewAuthorizer answers whether viewer may see post. An error means
// "could not decide"; both a false and an error refuse the room.
type PostViewAuthorizer interface {
	MayView(ctx context.Context, viewerID, postID string) (bool, error)
}

// HTTPPostViewAuthorizer asks post-service's internal visibility route.
type HTTPPostViewAuthorizer struct {
	baseURL     string
	internalKey string
	client      *nethttp.Client
}

func NewHTTPPostViewAuthorizer(baseURL, internalKey string, client *nethttp.Client) *HTTPPostViewAuthorizer {
	if client == nil {
		client = &nethttp.Client{Timeout: 3 * time.Second}
	}
	return &HTTPPostViewAuthorizer{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: client}
}

func (a *HTTPPostViewAuthorizer) MayView(ctx context.Context, viewerID, postID string) (bool, error) {
	if a == nil || a.baseURL == "" {
		return false, fmt.Errorf("post view authority not configured")
	}
	u := fmt.Sprintf("%s/v1/internal/posts/%s/visibility?viewer_id=%s", a.baseURL, url.PathEscape(postID), url.QueryEscape(viewerID))
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
		return false, fmt.Errorf("post view authority returned %d", resp.StatusCode)
	}
	var out struct {
		Visible bool `json:"visible"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	return out.Visible, nil
}

// postRoomGrants remembers, per connection, which posts were admitted and
// when, so a re-subscribe within the window is free.
type postRoomGrants struct {
	mu      sync.Mutex
	granted map[string]time.Time
	ttl     time.Duration
}

func newPostRoomGrants(ttl time.Duration) *postRoomGrants {
	return &postRoomGrants{granted: map[string]time.Time{}, ttl: ttl}
}

func (g *postRoomGrants) fresh(postID string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.granted[postID]
	return ok && now.Sub(at) < g.ttl
}

func (g *postRoomGrants) grant(postID string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.granted[postID] = now
}

func (g *postRoomGrants) revoke(postID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.granted, postID)
}

// mayJoinPostRoom is the one decision: rooms enabled, a well-formed post
// id, and the authority's yes (or a fresh cached yes).
func (s *Server) mayJoinPostRoom(ctx context.Context, grants *postRoomGrants, userID uuid.UUID, rawPostID string, now time.Time) (string, bool) {
	if !s.opts.EnablePostRooms || s.opts.PostViewer == nil {
		return "", false
	}
	postID, err := uuid.Parse(rawPostID)
	if err != nil {
		return "", false
	}
	id := postID.String()
	if grants.fresh(id, now) {
		return id, true
	}
	ok, err := s.opts.PostViewer.MayView(ctx, userID.String(), id)
	if err != nil {
		// Undecided is refused, and it is worth a warning: a misconfigured
		// authority silently empties every thread.
		if s.log != nil {
			s.log.Warn("post room authority unresolved", "err", err, "user_id", userID, "post_id", id)
		}
		return id, false
	}
	if !ok {
		return id, false
	}
	grants.grant(id, now)
	return id, true
}

const postRoomGrantTTL = 5 * time.Minute

// groupPostTypingFrame is the only shape a group-post typing indicator
// takes on the wire: which post, nothing about who. See the relay in
// server.go for why.
func groupPostTypingFrame(postID string) []byte {
	frame, _ := json.Marshal(map[string]any{"type": "group_post_typing", "post_id": postID})
	return frame
}
