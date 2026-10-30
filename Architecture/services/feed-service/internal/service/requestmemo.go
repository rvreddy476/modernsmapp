package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Per-request memo and short graph caches (audit M3, 2026-10-30).
//
// One request used to resolve the same things again and again: a reels
// category page walks up to 15 windows and each re-resolved the viewer's
// block set (graph-service + Postgres) and following list, and the
// Following / Friends tabs paged the whole follow or connection list from
// graph-service, uncached, on every request (50 sequential round trips for
// a viewer following 5,000 accounts).
//
//   - WithRequestMemo puts a memo on the request context (the HTTP layer
//     does it for every request; the category pages do it themselves too).
//     resolveBlockedSet and viewerFollowing answer from it after the first
//     call, so N windows cost one resolution. Only successes are kept: a
//     failed resolution is retried (and fails closed) by the next caller.
//   - viewerFollowing and viewerConnections sit behind short Redis caches.
//     The following list shares FollowingKey with discovery's followingSet
//     (5 min; the follow / unfollow consumers drop it at once). The
//     connection list has no invalidating event, so it is kept for one
//     minute only (connectionsCacheTTL).

// connectionsCacheTTL bounds how stale a viewer's cached connection list
// may be. Nothing invalidates it early, hence a minute.
const connectionsCacheTTL = time.Minute

// ConnectionsKey is the Redis key of a viewer's cached connection list.
func ConnectionsKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("feed:connections:%s", viewerID)
}

type requestMemoKey struct{}

type requestMemo struct {
	mu        sync.Mutex
	blocked   map[uuid.UUID]map[uuid.UUID]struct{}
	following map[uuid.UUID][]uuid.UUID
	mutes     map[uuid.UUID]feedMuteSet
}

// WithRequestMemo returns ctx carrying a fresh memo, or ctx itself when it
// already carries one (a nested call shares the outer request's memo).
func WithRequestMemo(ctx context.Context) context.Context {
	if memoFrom(ctx) != nil {
		return ctx
	}
	return context.WithValue(ctx, requestMemoKey{}, &requestMemo{
		blocked:   map[uuid.UUID]map[uuid.UUID]struct{}{},
		following: map[uuid.UUID][]uuid.UUID{},
		mutes:     map[uuid.UUID]feedMuteSet{},
	})
}

func memoFrom(ctx context.Context) *requestMemo {
	m, _ := ctx.Value(requestMemoKey{}).(*requestMemo)
	return m
}

// resolveBlockedSet is resolveBlockedSetUncached behind the request memo.
func (s *Service) resolveBlockedSet(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]struct{}, error) {
	m := memoFrom(ctx)
	if m != nil {
		m.mu.Lock()
		set, ok := m.blocked[userID]
		m.mu.Unlock()
		if ok {
			return set, nil
		}
	}
	set, err := s.resolveBlockedSetUncached(ctx, userID)
	if err != nil {
		return nil, err
	}
	if m != nil {
		m.mu.Lock()
		m.blocked[userID] = set
		m.mu.Unlock()
	}
	return set, nil
}

// viewerFollowing is the list of accounts the viewer follows, for the
// request path: the request memo, then the FollowingKey Redis cache, then
// graph-service. Errors are never cached. Fan-out code that needs an
// exact answer for an author calls fetchFollowing directly.
func (s *Service) viewerFollowing(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	m := memoFrom(ctx)
	if m != nil {
		m.mu.Lock()
		ids, ok := m.following[viewerID]
		m.mu.Unlock()
		if ok {
			return ids, nil
		}
	}
	ids, err := s.cachedIDList(ctx, FollowingKey(viewerID), followingCacheTTL, func() ([]uuid.UUID, error) {
		return s.fetchFollowing(ctx, viewerID)
	})
	if err != nil {
		return nil, err
	}
	if m != nil {
		m.mu.Lock()
		m.following[viewerID] = ids
		m.mu.Unlock()
	}
	return ids, nil
}

// viewerConnections is the viewer's connection list behind a one-minute
// Redis cache (connectionsCacheTTL), for the Friends tab.
func (s *Service) viewerConnections(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	return s.cachedIDList(ctx, ConnectionsKey(viewerID), connectionsCacheTTL, func() ([]uuid.UUID, error) {
		return s.fetchCircleMembers(ctx, viewerID)
	})
}

// cachedIDList reads a JSON id list from Redis, or fetches and stores it.
// A Redis miss or error falls through to fetch; a fetch error is returned
// and nothing is stored.
func (s *Service) cachedIDList(ctx context.Context, key string, ttl time.Duration, fetch func() ([]uuid.UUID, error)) ([]uuid.UUID, error) {
	if s.rdb != nil {
		if raw, err := s.rdb.Get(ctx, key).Bytes(); err == nil {
			var ids []uuid.UUID
			if json.Unmarshal(raw, &ids) == nil {
				return ids, nil
			}
		}
	}
	ids, err := fetch()
	if err != nil {
		return nil, err
	}
	if s.rdb != nil {
		if raw, err := json.Marshal(ids); err == nil {
			s.rdb.Set(ctx, key, raw, ttl)
		}
	}
	return ids, nil
}
