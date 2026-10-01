package service

// Free hearts and top supporters (2 Oct 2026). No money anywhere: a heart is
// a free tap. Clients batch taps and POST about once a second.
//
// A batch is counted in one transaction (the sender's row, capped at
// HeartsPerUserCap, and the stream's heart_count). Viewers are told through
// the room channel with an aggregated frame
//
//	{"type":"hearts","stream_id":"…","at":"…","count":n,"heart_count":total}
//
// at most once per heartFrameWindow per stream: count is the hearts counted
// since the previous frame. It names nobody.

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// EventHearts is the aggregated hearts frame.
const EventHearts = "hearts"

const (
	// HeartsMaxPerRequest is the largest batch one POST may carry.
	HeartsMaxPerRequest = 20
	// HeartsPerUserCap is the most hearts one viewer counts for on one
	// stream; past it hearts are accepted and ignored.
	HeartsPerUserCap = 10000
	// heartRateMax hearts per heartRateWindow per viewer per stream.
	heartRateMax    = 60
	heartRateWindow = 10 * time.Second
	// heartFrameWindow: at most one hearts frame per stream per window.
	heartFrameWindow = time.Second
)

// HeartLimiter is the per-viewer-per-stream rate limit.
type HeartLimiter interface {
	// Allow counts n hearts against the window and reports whether the
	// viewer is still inside the limit. An error means it could not be
	// decided; the caller refuses.
	Allow(ctx context.Context, streamID, userID uuid.UUID, n int) (bool, error)
}

// HeartTally holds the hearts counted since the last frame of each stream.
type HeartTally interface {
	Add(ctx context.Context, streamID uuid.UUID, n int)
	// Take returns the tally and resets it to zero.
	Take(ctx context.Context, streamID uuid.UUID) int
}

// heartState is the limiter, the tally and the pending trailing flushes.
type heartState struct {
	limiter HeartLimiter
	tally   HeartTally
	// after schedules the trailing flush; time.AfterFunc unless a test
	// replaced it.
	after func(d time.Duration, f func())

	mu    sync.Mutex
	armed map[uuid.UUID]bool
}

// newHeartState uses Redis when there is one (so the limit and the tally are
// shared by every replica) and this process's memory otherwise.
func newHeartState(rdb *redis.Client, now func() time.Time) *heartState {
	h := &heartState{
		after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		armed: map[uuid.UUID]bool{},
	}
	if rdb != nil {
		h.limiter = redisHeartLimiter{rdb: rdb}
		h.tally = redisHeartTally{rdb: rdb}
	} else {
		h.limiter = &memHeartLimiter{now: now, windows: map[string]*heartWindow{}}
		h.tally = &memHeartTally{pending: map[uuid.UUID]int{}}
	}
	return h
}

// heartState returns the service's heart counters, building them on first
// use (a Service made without New has neither Redis nor a pinned clock yet).
func (s *Service) heartState() *heartState {
	s.heartsOnce.Do(func() { s.hearts = newHeartState(s.redis, s.clock) })
	return s.hearts
}

// SendHearts counts a batch of n hearts from userID and returns the stream's
// heart_count. The checks, in order: n in 1..HeartsMaxPerRequest; the stream
// exists; the viewer may watch it (visibility, blocks, stream ban); the
// viewer is not under a platform live ban; the stream is live or
// reconnecting; the rate limit. The host may send hearts: they count in the
// total, and the host is never listed as a supporter.
func (s *Service) SendHearts(ctx context.Context, streamID, userID uuid.UUID, n int) (int64, error) {
	if n < 1 || n > HeartsMaxPerRequest {
		return 0, ErrInvalidHeartCount
	}
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return 0, mapStoreErr(err)
	}
	if err := s.authorizeViewer(ctx, st, userID); err != nil {
		return 0, err
	}
	banned, err := s.store.IsPlatformBanned(ctx, userID)
	if err != nil {
		return 0, ErrAuthorityUnavailable
	}
	if banned {
		return 0, ErrLiveBanned
	}
	if !chatOpen(st.Status) {
		return 0, ErrStreamNotLive
	}
	ok, err := s.heartState().limiter.Allow(ctx, streamID, userID, n)
	if err != nil {
		return 0, ErrAuthorityUnavailable // fail closed, like the chat limit
	}
	if !ok {
		return 0, ErrHeartsRateLimited
	}
	res, err := s.store.AddHearts(ctx, streamID, userID, n, HeartsPerUserCap)
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		return 0, ErrStreamNotFound
	case errors.Is(err, postgres.ErrNotOnAir):
		return 0, ErrStreamNotLive // it ended between the read and the write
	case err != nil:
		return 0, err
	}
	if res.Added > 0 && s.rt != nil { // no room channel: nothing to tally for
		s.heartState().tally.Add(ctx, streamID, res.Added)
		s.flushHearts(ctx, streamID, res.Total, true)
	}
	return res.Total, nil
}

// flushHearts publishes the stream's tally as one frame if this window's
// frame has not gone out yet; otherwise it arms one trailing flush, so the
// last hearts of a burst are announced even when nobody taps again.
// totalKnown=false (the trailing flush) reads heart_count from the store.
func (s *Service) flushHearts(ctx context.Context, streamID uuid.UUID, total int64, totalKnown bool) {
	if s.rt == nil {
		return
	}
	if !s.rt.Allow(ctx, "live:hearts_tick:"+streamID.String(), heartFrameWindow) {
		s.armHeartFlush(streamID)
		return
	}
	count := s.heartState().tally.Take(ctx, streamID)
	if count <= 0 {
		return
	}
	if !totalKnown {
		st, err := s.store.GetByID(ctx, streamID)
		if err != nil {
			return
		}
		total = st.HeartCount
	}
	s.publish(ctx, streamID, EventHearts, map[string]any{
		"stream_id":   streamID.String(),
		"count":       count,
		"heart_count": total,
	})
}

// armHeartFlush schedules one trailing flush per stream (per replica).
func (s *Service) armHeartFlush(streamID uuid.UUID) {
	h := s.heartState()
	h.mu.Lock()
	if h.armed[streamID] {
		h.mu.Unlock()
		return
	}
	h.armed[streamID] = true
	h.mu.Unlock()
	h.after(heartFrameWindow, func() {
		h.mu.Lock()
		delete(h.armed, streamID)
		h.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.flushHearts(ctx, streamID, 0, false)
	})
}

// --- top supporters ---

// SupporterRow is one row of GET /streams/:id/supporters.
type SupporterRow struct {
	User     *postgres.UserCard `json:"user"`
	Hearts   int                `json:"hearts"`
	Messages int                `json:"messages"`
	Rank     int                `json:"rank"`
}

// ListSupporters ranks the stream's viewers by hearts, then chat messages
// that were not removed, then who was active first. It is behind the chat
// list's gate (visibility, blocks, stream ban; a signed-out reader passes on
// a public stream) and works after the stream ended. The host, banned
// viewers and anyone with neither a heart nor a message are not listed.
func (s *Service) ListSupporters(ctx context.Context, streamID, viewerID uuid.UUID, limit int) ([]SupporterRow, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.authorizeViewer(ctx, st, viewerID); err != nil {
		return nil, err
	}
	rows, err := s.store.ListSupporters(ctx, streamID, limit)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.UserID
	}
	cards := s.userCards(ctx, ids)
	out := make([]SupporterRow, 0, len(rows))
	for i, r := range rows {
		out = append(out, SupporterRow{User: cards[r.UserID], Hearts: r.Hearts, Messages: r.Messages, Rank: i + 1})
	}
	return out, nil
}

// --- limiter and tally: Redis ---

type redisHeartLimiter struct{ rdb *redis.Client }

// Allow is a fixed window: the first batch of a window starts it. A batch
// that goes over is refused and still counted, so a client that keeps
// sending stays refused until the window ends.
func (l redisHeartLimiter) Allow(ctx context.Context, streamID, userID uuid.UUID, n int) (bool, error) {
	key := "live_hearts_rl:" + streamID.String() + ":" + userID.String()
	pipe := l.rdb.Pipeline()
	incr := pipe.IncrBy(ctx, key, int64(n))
	ttl := pipe.TTL(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, err
	}
	if ttl.Val() < 0 { // new key (or one that lost its expiry): start the window
		if err := l.rdb.Expire(ctx, key, heartRateWindow).Err(); err != nil {
			return false, err
		}
	}
	return incr.Val() <= heartRateMax, nil
}

type redisHeartTally struct{ rdb *redis.Client }

func heartTallyKey(streamID uuid.UUID) string { return "live:hearts_pending:" + streamID.String() }

// Add is best effort: a lost increment only makes one frame's count smaller
// (heart_count on the frame is the truth).
func (t redisHeartTally) Add(ctx context.Context, streamID uuid.UUID, n int) {
	key := heartTallyKey(streamID)
	pipe := t.rdb.Pipeline()
	pipe.IncrBy(ctx, key, int64(n))
	pipe.Expire(ctx, key, time.Minute)
	_, _ = pipe.Exec(ctx)
}

func (t redisHeartTally) Take(ctx context.Context, streamID uuid.UUID) int {
	key := heartTallyKey(streamID)
	var get *redis.StringCmd
	_, err := t.rdb.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		get = pipe.Get(ctx, key)
		pipe.Del(ctx, key)
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0
	}
	n, err := get.Int()
	if err != nil {
		return 0
	}
	return n
}

// --- limiter and tally: this process ---

type heartWindow struct {
	start time.Time
	count int
}

type memHeartLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	windows map[string]*heartWindow
}

func (l *memHeartLimiter) Allow(_ context.Context, streamID, userID uuid.UUID, n int) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	key := streamID.String() + ":" + userID.String()
	w := l.windows[key]
	if w == nil || now.Sub(w.start) >= heartRateWindow {
		if len(l.windows) > 50000 {
			l.windows = map[string]*heartWindow{}
		}
		w = &heartWindow{start: now}
		l.windows[key] = w
	}
	w.count += n
	return w.count <= heartRateMax, nil
}

type memHeartTally struct {
	mu      sync.Mutex
	pending map[uuid.UUID]int
}

func (t *memHeartTally) Add(_ context.Context, streamID uuid.UUID, n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[streamID] += n
}

func (t *memHeartTally) Take(_ context.Context, streamID uuid.UUID) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := t.pending[streamID]
	delete(t.pending, streamID)
	return n
}
