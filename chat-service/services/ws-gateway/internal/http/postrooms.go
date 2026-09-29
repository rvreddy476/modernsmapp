package http

import (
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"net/url"
	"sort"
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

	The decision is also NOT final (plan P-8 e). A post goes private,
	scheduled or deleted, or its author's account turns private, after the
	socket joined; the join-time yes must not keep the seat. So the
	connection's reconcile loop (server.go) re-asks the same authority for
	every held post room every postRoomRecheckInterval, at most
	postRoomRecheckBatch posts per sweep, round-robin so a socket holding
	many rooms is never starved. A "no" evicts at once; an undecided answer
	(post-service down, 5xx, timeout) keeps the seat for one sweep and
	evicts on the second consecutive one, so a transient blip does not
	empty every thread while a sustained outage still fails closed. An
	eviction unsubscribes the socket, forgets the grant, and tells the
	client with the same control frame conversation rooms use:
	  {"type":"subscription_revoked","post_id":"<uuid>"}
	Post-service has no batch visibility route, so each post is one GET.

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
// when, so a re-subscribe within the window is free. The same map is the
// set of post rooms the connection holds, which the periodic re-check
// walks; `undecided` counts consecutive re-checks the authority could not
// answer for a post, and `cursor` is where the last round-robin sweep
// stopped.
type postRoomGrants struct {
	mu        sync.Mutex
	granted   map[string]time.Time
	undecided map[string]int
	cursor    string
	ttl       time.Duration
}

func newPostRoomGrants(ttl time.Duration) *postRoomGrants {
	return &postRoomGrants{granted: map[string]time.Time{}, undecided: map[string]int{}, ttl: ttl}
}

func (g *postRoomGrants) fresh(postID string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.granted[postID]
	return ok && now.Sub(at) < g.ttl
}

// grant records a fresh yes. It also clears any undecided streak: a
// definite answer supersedes the blips before it.
func (g *postRoomGrants) grant(postID string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.granted[postID] = now
	delete(g.undecided, postID)
}

func (g *postRoomGrants) revoke(postID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.granted, postID)
	delete(g.undecided, postID)
}

// held reports whether the connection still holds a grant for postID,
// regardless of its age (the re-check re-asks about stale grants too, so a
// room the client never re-subscribes is still re-authorized).
func (g *postRoomGrants) held(postID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.granted[postID]
	return ok
}

// markUndecided counts one more consecutive undecided re-check for postID
// and returns the new streak length.
func (g *postRoomGrants) markUndecided(postID string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.undecided[postID]++
	return g.undecided[postID]
}

// nextBatch returns up to limit held post ids in sorted order, starting
// after the cursor of the previous call and wrapping, so successive calls
// walk every room round-robin however many the connection holds.
func (g *postRoomGrants) nextBatch(limit int) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if limit <= 0 || len(g.granted) == 0 {
		return nil
	}
	ids := make([]string, 0, len(g.granted))
	for id := range g.granted {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	start := sort.SearchStrings(ids, g.cursor)
	if start < len(ids) && ids[start] == g.cursor {
		start++
	}
	if limit > len(ids) {
		limit = len(ids)
	}
	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, ids[(start+i)%len(ids)])
	}
	g.cursor = out[len(out)-1]
	return out
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

// Re-check cadence and bounds (P-8 e). The interval matches the grant TTL:
// a seat is never older than one interval without a fresh yes behind it.
const (
	postRoomRecheckInterval     = 5 * time.Minute
	postRoomRecheckBatch        = 200
	postRoomRecheckMaxUndecided = 2
)

// postRoomEviction is one seat the re-check decided to drop and why:
// "denied" (the authority said no) or "unresolved" (it could not answer
// postRoomRecheckMaxUndecided sweeps in a row).
type postRoomEviction struct {
	PostID string
	Reason string
}

// recheckPostRooms re-authorizes up to limit of this connection's post
// rooms against the authority and returns the seats to drop. Each returned
// seat's grant is already forgotten, so a racing re-subscribe re-asks
// rather than reusing the cached yes. A yes refreshes the grant; a no
// evicts; an undecided answer keeps the seat until the streak reaches
// postRoomRecheckMaxUndecided, then evicts — fail closed, but not on the
// first blip.
func (s *Server) recheckPostRooms(ctx context.Context, grants *postRoomGrants, userID uuid.UUID, now time.Time, limit int) []postRoomEviction {
	var evicted []postRoomEviction
	for _, postID := range grants.nextBatch(limit) {
		if !grants.held(postID) {
			continue // unsubscribed while this sweep was running
		}
		if s.opts.PostViewer == nil {
			grants.revoke(postID)
			evicted = append(evicted, postRoomEviction{PostID: postID, Reason: "unresolved"})
			continue
		}
		ok, err := s.opts.PostViewer.MayView(ctx, userID.String(), postID)
		switch {
		case err != nil:
			streak := grants.markUndecided(postID)
			if streak < postRoomRecheckMaxUndecided {
				if s.log != nil {
					s.log.Debug("post room re-check undecided; keeping the seat once",
						"err", err, "user_id", userID, "post_id", postID, "streak", streak)
				}
				continue
			}
			grants.revoke(postID)
			evicted = append(evicted, postRoomEviction{PostID: postID, Reason: "unresolved"})
		case !ok:
			grants.revoke(postID)
			evicted = append(evicted, postRoomEviction{PostID: postID, Reason: "denied"})
		default:
			grants.grant(postID, now)
		}
	}
	return evicted
}

// roomUnsubscriber is the one pubsub method eviction needs; *redis.PubSub
// satisfies it and a test can stand in a fake.
type roomUnsubscriber interface {
	Unsubscribe(ctx context.Context, channels ...string) error
}

// evictPostRooms leaves each room and tells the client. The send is
// non-blocking like the redis relay's: a client too slow to drain its
// buffer is about to be closed anyway, and the seat is already gone.
func (s *Server) evictPostRooms(ctx context.Context, pubsub roomUnsubscriber, outbound chan<- []byte, userID uuid.UUID, evicted []postRoomEviction) {
	for _, ev := range evicted {
		if s.log != nil {
			s.log.Debug("post room dropped by re-check", "user_id", userID, "post_id", ev.PostID, "reason", ev.Reason)
		}
		if err := pubsub.Unsubscribe(ctx, "post:"+ev.PostID); err != nil && s.log != nil {
			s.log.Warn("post room unsubscribe failed", "err", err, "user_id", userID, "post_id", ev.PostID)
		}
		select {
		case outbound <- postRoomRevokedFrame(ev.PostID):
		default:
		}
	}
}

// postRoomRevokedFrame is the post-room twin of the conversation-room
// control frame message-service publishes ({"type":"subscription_revoked",
// "conversation_id":...}): same type, the room's own id key.
func postRoomRevokedFrame(postID string) []byte {
	frame, _ := json.Marshal(map[string]any{"type": "subscription_revoked", "post_id": postID})
	return frame
}

// groupPostTypingFrame is the only shape a group-post typing indicator
// takes on the wire: which post, nothing about who. See the relay in
// server.go for why.
func groupPostTypingFrame(postID string) []byte {
	frame, _ := json.Marshal(map[string]any{"type": "group_post_typing", "post_id": postID})
	return frame
}
