package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Module 3 M3-P0-4 / SR-4 — block denial on every profile surface.
//
// THE DEFECT
//
// Nothing in profile-service consulted the block graph. A blocked account
// could open the person who blocked them in the app and read their profile,
// their follower counts, their status and their links — every time. The block
// button changed nothing on the surface the blocked person was most likely to
// visit.
//
// After SR-3, profile-service no longer has its own block table (that copy was
// the problem: it was enforced by nobody). So enforcement here means asking
// graph-service, which is canonical.
//
// FAIL CLOSED
//
// If graph-service is unreachable, the request is DENIED, not allowed. That is
// deliberate and it is the opposite of the usual availability instinct:
//
//   - Failing open means a graph-service blip re-exposes every blocked user to
//     the person they blocked, silently, for the duration of the incident.
//     Nobody is paged, because everything returns 200.
//   - Failing closed means a profile is briefly unavailable during an
//     already-degraded window. It is visible, it is temporary, and it does not
//     put anyone in front of an account they blocked.
//
// The short-lived positive cache exists so a profile view does not add a graph
// round trip per request. A block is cached for LESS time than an absence
// would be, because becoming blocked must take effect quickly while a stale
// "not blocked" answer is the dangerous direction.

// BlockChecker answers whether a block exists between two users, in either
// direction. Symmetric by contract: A blocking B hides both from each other.
type BlockChecker interface {
	BlockedEitherWay(ctx context.Context, viewerID, targetID uuid.UUID) (bool, error)
}

// GraphBlockChecker asks graph-service.
type GraphBlockChecker struct {
	baseURL     string
	internalKey string
	client      *http.Client

	cache *blockSetCache
}

func NewGraphBlockChecker(baseURL, internalKey string) *GraphBlockChecker {
	return &GraphBlockChecker{
		baseURL:     baseURL,
		internalKey: internalKey,
		client:      &http.Client{Timeout: 2 * time.Second},
		cache:       newBlockSetCache(15 * time.Second),
	}
}

// BlockedEitherWay returns true when the viewer must not see the target.
//
// It reads graph-service's blocked-and-muted set for the VIEWER. That set is
// symmetric for blocks (Module 2 M2-P0-3: it contains both who the viewer
// blocked and who blocked the viewer) and additionally contains one-way mutes.
//
// A mute is included here on purpose, and it is worth being explicit about
// why that is acceptable: a mute is the viewer's own choice to stop seeing
// someone, so honouring it on a profile the viewer explicitly navigated to is
// at worst mildly surprising and never a safety failure. Getting a block wrong
// in the other direction is.
func (g *GraphBlockChecker) BlockedEitherWay(ctx context.Context, viewerID, targetID uuid.UUID) (bool, error) {
	if viewerID == uuid.Nil || targetID == uuid.Nil || viewerID == targetID {
		return false, nil
	}
	if set, ok := g.cache.get(viewerID); ok {
		_, blocked := set[targetID]
		return blocked, nil
	}

	url := fmt.Sprintf("%s/v1/graph/blocked-and-muted?user_id=%s", g.baseURL, viewerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	if g.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", g.internalKey)
	}

	resp, err := g.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("graph-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("graph-service blocked-and-muted returned %d", resp.StatusCode)
	}

	var body struct {
		UserIDs []uuid.UUID `json:"user_ids"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode blocked-and-muted: %w", err)
	}

	set := make(map[uuid.UUID]struct{}, len(body.UserIDs))
	for _, id := range body.UserIDs {
		set[id] = struct{}{}
	}
	g.cache.put(viewerID, set)

	_, blocked := set[targetID]
	return blocked, nil
}

// blockSetCache holds a viewer's blocked set for a short window.
type blockSetCache struct {
	ttl     time.Duration
	entries map[uuid.UUID]blockSetEntry
	mu      chan struct{} // 1-slot semaphore; cheaper than a mutex import here
}

type blockSetEntry struct {
	set     map[uuid.UUID]struct{}
	expires time.Time
}

func newBlockSetCache(ttl time.Duration) *blockSetCache {
	c := &blockSetCache{
		ttl:     ttl,
		entries: map[uuid.UUID]blockSetEntry{},
		mu:      make(chan struct{}, 1),
	}
	c.mu <- struct{}{}
	return c
}

func (c *blockSetCache) get(viewer uuid.UUID) (map[uuid.UUID]struct{}, bool) {
	<-c.mu
	defer func() { c.mu <- struct{}{} }()
	e, ok := c.entries[viewer]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.set, true
}

func (c *blockSetCache) put(viewer uuid.UUID, set map[uuid.UUID]struct{}) {
	<-c.mu
	defer func() { c.mu <- struct{}{} }()
	// Bound the map so a profile-scraping run cannot grow it without limit.
	if len(c.entries) > 10000 {
		c.entries = map[uuid.UUID]blockSetEntry{}
	}
	c.entries[viewer] = blockSetEntry{set: set, expires: time.Now().Add(c.ttl)}
}

// Invalidate drops a viewer's cached set. Called when this service learns of a
// block change, so becoming blocked takes effect immediately rather than at
// the end of the TTL.
func (c *blockSetCache) Invalidate(viewer uuid.UUID) {
	<-c.mu
	defer func() { c.mu <- struct{}{} }()
	delete(c.entries, viewer)
}
