package http

// Presence fan-out (chat parity §4, 18 Oct 2026).
//
// On a user's offline -> online transition (first live connection) and
// online -> offline transition (last connection closed) the gateway pushes
//
//	{"type":"presence_update",
//	 "payload":{"user_id","is_online","last_active_at"},
//	 "user_id","online"}                       // legacy flat mirror
//
// to chat:<peer> for each DM peer PERMITTED to see it. Nothing is published
// on a global channel: presence:updates was a cross-user leak and is still
// never subscribed.
//
// Permission is the TARGET's privacy, resolved by graph-service for the pair
// (viewer = peer, target = the user whose state changed):
//   - no see_online_status  -> the peer gets nothing at all (activity status
//     is who_can_see_online_status; a last-active push would leak the same
//     fact, so last-seen alone never earns a frame);
//   - see_online_status but no see_last_seen -> last_active_at is null.
// graph denies both when the target's chat_availability is paused, and on a
// block in either direction. Any graph failure denies (fail closed).
//
// Decisions are cached 30 s in Redis under the SAME onlinevis:<viewer>:<target>
// key message-service's GetPresence uses, plus lastseenvis:<viewer>:<target>.
// The DM peer list is cached in-process for peerCacheTTL.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"time"

	"github.com/atpost/chat-shared/presence"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	presenceMaxPeers     = 200
	presenceConcurrency  = 8
	presenceVisCacheTTL  = 30 * time.Second
	peerCacheTTL         = 60 * time.Second
	peerCacheMaxEntries  = 10000
	presenceFanoutBudget = 10 * time.Second
)

// PeerSource lists a user's DM peers (most recent first, at most limit).
type PeerSource interface {
	DMPeers(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error)
}

// Visibility is one viewer's permission over one target's presence.
type Visibility struct {
	Online   bool
	LastSeen bool
}

// VisibilityChecker resolves whether viewer may see target's presence.
// Implementations must fail closed (zero Visibility) on any error.
type VisibilityChecker interface {
	Visibility(ctx context.Context, viewerID, targetID uuid.UUID) Visibility
}

type presenceFanout struct {
	peers   PeerSource
	vis     VisibilityChecker
	rdb     *redis.Client
	log     *slog.Logger
	cache   *peerCache
	userMus sync.Map // userID -> *sync.Mutex: one announce per user at a time
}

func newPresenceFanout(peers PeerSource, vis VisibilityChecker, rdb *redis.Client, log *slog.Logger) *presenceFanout {
	if peers == nil || vis == nil || rdb == nil {
		return nil
	}
	return &presenceFanout{peers: peers, vis: vis, rdb: rdb, log: log, cache: newPeerCache(peerCacheTTL, peerCacheMaxEntries)}
}

// presenceFrame builds the frame one permitted peer receives.
func presenceFrame(userID uuid.UUID, online bool, lastActive time.Time, showLastSeen bool) []byte {
	var last any
	if showLastSeen && !lastActive.IsZero() {
		last = lastActive.UTC().Format(time.RFC3339)
	}
	frame, _ := json.Marshal(map[string]any{
		"type": "presence_update",
		"payload": map[string]any{
			"user_id":        userID.String(),
			"is_online":      online,
			"last_active_at": last,
		},
		"user_id": userID.String(),
		"online":  online,
	})
	return frame
}

// Announce publishes userID's CURRENT presence to its permitted DM peers.
// Announces for one user are serialized and each re-reads the stored state,
// so a fast connect/disconnect pair can never leave peers on a stale value.
func (f *presenceFanout) Announce(ctx context.Context, userID uuid.UUID) {
	if f == nil {
		return
	}
	muAny, _ := f.userMus.LoadOrStore(userID, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	state, err := presence.Lookup(ctx, f.rdb, []string{userID.String()})
	if err != nil {
		f.log.Warn("presence fan-out: state read failed", "err", err, "user_id", userID)
		return
	}
	current := state[userID.String()]

	peers, err := f.peerList(ctx, userID)
	if err != nil {
		f.log.Warn("presence fan-out: peer lookup failed", "err", err, "user_id", userID)
		return
	}
	sem := make(chan struct{}, presenceConcurrency)
	var wg sync.WaitGroup
	// Exactly once per peer per transition, whatever the peer source returns
	// (a pair with two direct conversations used to be listed twice).
	sent := make(map[uuid.UUID]struct{}, len(peers))
	for _, peer := range peers {
		if peer == userID {
			continue
		}
		if _, dup := sent[peer]; dup {
			continue
		}
		sent[peer] = struct{}{}
		wg.Add(1)
		sem <- struct{}{}
		go func(peer uuid.UUID) {
			defer wg.Done()
			defer func() { <-sem }()
			v := f.vis.Visibility(ctx, peer, userID)
			if !v.Online {
				return
			}
			frame := presenceFrame(userID, current.Online, current.LastActiveAt, v.LastSeen)
			if err := f.rdb.Publish(ctx, "chat:"+peer.String(), string(frame)).Err(); err != nil {
				f.log.Warn("presence fan-out publish failed", "err", err, "user_id", userID, "peer", peer)
			}
		}(peer)
	}
	wg.Wait()
}

func (f *presenceFanout) peerList(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	if cached, ok := f.cache.get(userID, time.Now()); ok {
		return cached, nil
	}
	peers, err := f.peers.DMPeers(ctx, userID, presenceMaxPeers)
	if err != nil {
		return nil, err
	}
	f.cache.put(userID, peers, time.Now())
	return peers, nil
}

type peerCacheEntry struct {
	peers   []uuid.UUID
	expires time.Time
}

type peerCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[uuid.UUID]peerCacheEntry
}

func newPeerCache(ttl time.Duration, max int) *peerCache {
	return &peerCache{ttl: ttl, max: max, entries: map[uuid.UUID]peerCacheEntry{}}
}

func (c *peerCache) get(id uuid.UUID, now time.Time) ([]uuid.UUID, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[id]
	if !ok || now.After(e.expires) {
		return nil, false
	}
	return e.peers, true
}

func (c *peerCache) put(id uuid.UUID, peers []uuid.UUID, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		c.entries = map[uuid.UUID]peerCacheEntry{} // coarse bound; refills lazily
	}
	c.entries[id] = peerCacheEntry{peers: peers, expires: now.Add(c.ttl)}
}

// GraphVisibility asks graph-service GET /v1/permissions/check for both
// actions in one call, caching each decision in Redis.
type GraphVisibility struct {
	BaseURL            string
	InternalServiceKey string
	Client             *nethttp.Client
	Redis              *redis.Client
	Log                *slog.Logger
}

func onlineVisKey(viewer, target uuid.UUID) string {
	return "onlinevis:" + viewer.String() + ":" + target.String()
}

func lastSeenVisKey(viewer, target uuid.UUID) string {
	return "lastseenvis:" + viewer.String() + ":" + target.String()
}

func (g *GraphVisibility) Visibility(ctx context.Context, viewerID, targetID uuid.UUID) Visibility {
	if g == nil {
		return Visibility{}
	}
	if g.Redis != nil {
		vals, err := g.Redis.MGet(ctx, onlineVisKey(viewerID, targetID), lastSeenVisKey(viewerID, targetID)).Result()
		if err == nil && len(vals) == 2 && vals[0] != nil && vals[1] != nil {
			return Visibility{Online: vals[0] == "1", LastSeen: vals[1] == "1"}
		}
	}
	v, err := g.fetch(ctx, viewerID, targetID)
	if err != nil {
		if g.Log != nil {
			g.Log.Warn("presence visibility check failed; denying", "err", err, "viewer", viewerID, "target", targetID)
		}
		return Visibility{}
	}
	if g.Redis != nil {
		pipe := g.Redis.Pipeline()
		pipe.Set(ctx, onlineVisKey(viewerID, targetID), boolDigit(v.Online), presenceVisCacheTTL)
		pipe.Set(ctx, lastSeenVisKey(viewerID, targetID), boolDigit(v.LastSeen), presenceVisCacheTTL)
		_, _ = pipe.Exec(ctx)
	}
	return v
}

func boolDigit(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (g *GraphVisibility) fetch(ctx context.Context, viewerID, targetID uuid.UUID) (Visibility, error) {
	base := strings.TrimRight(g.BaseURL, "/")
	if base == "" {
		return Visibility{}, errors.New("graph service not configured")
	}
	url := fmt.Sprintf("%s/v1/permissions/check?target_user_id=%s&actions=see_online_status,see_last_seen", base, targetID)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodGet, url, nil)
	if err != nil {
		return Visibility{}, err
	}
	req.Header.Set("X-User-Id", viewerID.String())
	if g.InternalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", g.InternalServiceKey)
	}
	client := g.Client
	if client == nil {
		client = &nethttp.Client{Timeout: 3 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Visibility{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != nethttp.StatusOK {
		return Visibility{}, fmt.Errorf("graph status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return Visibility{}, err
	}
	var envelope struct {
		Data struct {
			Decisions map[string]struct {
				Allowed bool `json:"allowed"`
			} `json:"decisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return Visibility{}, err
	}
	return Visibility{
		Online:   envelope.Data.Decisions["see_online_status"].Allowed,
		LastSeen: envelope.Data.Decisions["see_last_seen"].Allowed,
	}, nil
}
