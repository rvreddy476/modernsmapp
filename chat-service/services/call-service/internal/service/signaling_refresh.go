package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/chat-call-service/internal/store/postgres"
	"github.com/atpost/chat-shared/callauth"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// DefaultSignalingRefreshInterval is how often active pairs are re-armed.
// A quarter of callauth.DefaultTTL: three sweeps can fail in a row before a
// live call loses signaling.
const DefaultSignalingRefreshInterval = callauth.DefaultTTL / 4

// activeSignalingPairs lists the pairs the database says are still in an
// active call (postgres.CallStore.ListActiveSignalingPairs).
type activeSignalingPairs interface {
	ListActiveSignalingPairs(ctx context.Context) ([]postgres.SignalingPair, error)
}

// signalingAuthRefreshFunc re-arms one pair's TTL; it must never create a key.
type signalingAuthRefreshFunc func(ctx context.Context, a, b, callID uuid.UUID, ttl time.Duration) (bool, error)

// SignalingAuthRefresher keeps ws-gateway's call-signaling authorization
// alive for calls longer than callauth.DefaultTTL (audit M6). Before it, the
// pair key was written once on accept and expired after an hour, after which
// ice_candidate (ICE restart on a network change), call_end and call_upgrade
// frames were silently dropped.
//
// Two rules keep it from extending what should end:
//   - only pairs the database lists as active with both sides still in the
//     call are refreshed (ListActiveSignalingPairs);
//   - callauth.Refresh only extends an existing `active` key of the same
//     call, so a pair whose call ended (and was Cleared) between the query
//     and the refresh stays cleared.
type SignalingAuthRefresher struct {
	pairs    activeSignalingPairs
	refresh  signalingAuthRefreshFunc
	log      *slog.Logger
	interval time.Duration
	ttl      time.Duration
}

// NewSignalingAuthRefresher builds the worker over the call store and Redis.
// interval <= 0 means DefaultSignalingRefreshInterval.
func NewSignalingAuthRefresher(pairs activeSignalingPairs, rdb *redis.Client, log *slog.Logger, interval time.Duration) *SignalingAuthRefresher {
	refresh := func(ctx context.Context, a, b, callID uuid.UUID, ttl time.Duration) (bool, error) {
		return callauth.Refresh(ctx, rdb, a, b, callID, ttl)
	}
	return newSignalingAuthRefresher(pairs, refresh, log, interval)
}

func newSignalingAuthRefresher(pairs activeSignalingPairs, refresh signalingAuthRefreshFunc, log *slog.Logger, interval time.Duration) *SignalingAuthRefresher {
	if log == nil {
		log = slog.Default()
	}
	if interval <= 0 {
		interval = DefaultSignalingRefreshInterval
	}
	return &SignalingAuthRefresher{pairs: pairs, refresh: refresh, log: log, interval: interval, ttl: callauth.DefaultTTL}
}

// Start sweeps every interval until ctx ends.
func (w *SignalingAuthRefresher) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

// sweep refreshes every listed pair and returns how many keys it extended.
func (w *SignalingAuthRefresher) sweep(ctx context.Context) int {
	pairs, err := w.pairs.ListActiveSignalingPairs(ctx)
	if err != nil {
		w.log.Warn("signaling auth refresh: listing active calls failed", "err", err)
		return 0
	}
	extended := 0
	for _, p := range pairs {
		ok, err := w.refresh(ctx, p.InitiatorID, p.ParticipantID, p.CallID, w.ttl)
		if err != nil {
			w.log.Warn("signaling auth refresh failed", "err", err, "call_id", p.CallID)
			continue
		}
		if ok {
			extended++
		}
	}
	return extended
}
