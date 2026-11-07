package service

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"
)

// On-demand generation, off the request path (audit SG-M3).
//
// A viewer with no stored candidates used to have the whole friend batch run
// inside their GET, under a 5-second timeout that cut it off mid-way, and the
// empty result was never cached, so every request from that viewer paid for
// it again. Now the GET serves what it has (the popular fallback), caches the
// empty result briefly, and asks for generation here: at most one run per
// viewer and type per onDemandLockTTL, and at most onDemandSlots at once
// across the process. A run that finds a full house is dropped, not queued;
// the next request after the empty cache expires asks again, and the 6-hour
// batch covers everyone regardless.

const (
	// emptyCacheTTL is how long an empty list is served from the cache.
	emptyCacheTTL = 2 * time.Minute
	// onDemandLockTTL is the per-viewer, per-type de-duplication window.
	onDemandLockTTL = 10 * time.Minute
	// onDemandTimeout bounds one background run.
	onDemandTimeout = 2 * time.Minute
	// onDemandSlots bounds concurrent background runs per process.
	onDemandSlots = 4
)

func onDemandLockKey(viewerID uuid.UUID, suggType string) string {
	return "suggestions:gen:" + viewerID.String() + ":" + suggType
}

// generatorFor is the batch that fills a viewer's stored candidates of a type.
func (s *Service) generatorFor(suggType string) func(context.Context, uuid.UUID) error {
	if s.generate != nil {
		return s.generate
	}
	if suggType == "follow" {
		return s.runFollowBatchForUser
	}
	return s.runFriendBatchForUser
}

// requestGeneration starts a background run for one viewer and type, and
// reports whether it did. It never blocks the caller.
func (s *Service) requestGeneration(viewerID uuid.UUID, suggType string) bool {
	if s.rdb == nil || s.genSlots == nil {
		return false
	}
	lockKey := onDemandLockKey(viewerID, suggType)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	acquired, err := s.rdb.SetNX(ctx, lockKey, "1", onDemandLockTTL).Result()
	cancel()
	if err != nil || !acquired {
		return false
	}
	select {
	case s.genSlots <- struct{}{}:
	default:
		// Full house: give the window back so a later request can ask again.
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		s.rdb.Del(ctx, lockKey)
		cancel()
		return false
	}
	gen := s.generatorFor(suggType)
	go func() {
		defer func() { <-s.genSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), onDemandTimeout)
		defer cancel()
		if err := gen(ctx, viewerID); err != nil {
			log.Printf("[suggestions] on-demand %s generation for %s failed: %v", suggType, viewerID, err)
		}
	}()
	return true
}
