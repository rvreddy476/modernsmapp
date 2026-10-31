package http

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

/*
	One Redis pub/sub connection per gateway process (audit M5).

	Every socket used to open its own go-redis PubSub, so each live socket
	held one Redis TCP connection. The hub replaces that with ONE PubSub per
	process and routes in memory:

	  - A socket gets a hubSubscriber. Subscribe(channel) registers the socket
	    for that channel; the hub sends SUBSCRIBE to Redis only when the first
	    local socket needs the channel, and UNSUBSCRIBE when the last leaves
	    (reference counting per channel).
	  - One receive goroutine reads the shared connection and hands each
	    message to every socket registered for its channel, in order, through
	    a bounded per-socket buffer. A full buffer never blocks the hub: that
	    socket is marked slow (Done closes) and stops receiving, and its
	    connection tears down exactly as a full outbound buffer always has.
	  - On a broken or silent connection the hub drops it, opens a new one and
	    re-SUBSCRIBEs every channel that still has a local socket.

	Authorization is not the hub's business: every caller runs its room's
	owner / entitlement check first and only then calls Subscribe, exactly as
	it did with the per-socket PubSub.
*/

const (
	// hubSubscriberBuffer is each socket's queue between the hub and its
	// relay loop (the same depth as the socket's outbound queue).
	hubSubscriberBuffer = 256
	// hubSubscribeTimeout bounds how long Subscribe waits for Redis to
	// acknowledge a channel the process did not already hold.
	hubSubscribeTimeout = 5 * time.Second
	// hubPingInterval: after this long with no traffic the hub PINGs; a
	// second silent interval means the connection is dead and is replaced.
	hubPingInterval = 15 * time.Second
	hubBackoffMin   = 100 * time.Millisecond
	hubBackoffMax   = 5 * time.Second
)

var (
	errHubClosed        = errors.New("pubsub hub closed")
	errSubscriberClosed = errors.New("pubsub subscriber closed")
	errSubscribeTimeout = errors.New("pubsub subscribe not acknowledged in time")
	errHubStale         = errors.New("pubsub connection silent; replacing it")
)

// hubChannel is one Redis channel's local state.
type hubChannel struct {
	subs map[*hubSubscriber]struct{}
	// ready is closed once Redis has acknowledged the SUBSCRIBE on the
	// current connection; a reconnect replaces it with a fresh open one.
	ready     chan struct{}
	confirmed bool
}

type pubsubHub struct {
	rdb *redis.Client
	log *slog.Logger

	pingInterval time.Duration // tests shorten it

	// cmdMu serialises every SUBSCRIBE / UNSUBSCRIBE sent to Redis together
	// with the ref-count transition that caused it, so a channel can never
	// be unsubscribed in Redis while a local socket still holds it (or the
	// other way round). It is never held by the receive path.
	cmdMu sync.Mutex

	// mu guards ps, channels and closed. The receive path takes it only to
	// look up subscribers; nothing does I/O while holding it.
	mu       sync.RWMutex
	ps       *redis.PubSub // nil while (re)connecting
	channels map[string]*hubChannel
	closed   bool

	startOnce sync.Once
	running   atomic.Bool
	stop      chan struct{}
	stopped   chan struct{}

	reconnects atomic.Int64 // observability and tests
}

func newPubsubHub(rdb *redis.Client, log *slog.Logger) *pubsubHub {
	if log == nil {
		log = slog.Default()
	}
	return &pubsubHub{
		rdb:          rdb,
		log:          log,
		pingInterval: hubPingInterval,
		channels:     map[string]*hubChannel{},
		stop:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
}

// start launches the receive loop once, on first use, so a Server that never
// takes a socket (unit tests, tooling) holds no Redis connection.
func (h *pubsubHub) start() {
	h.startOnce.Do(func() {
		h.running.Store(true)
		go h.run()
	})
}

// Close stops the hub and its Redis connection. Subscribers still registered
// get errHubClosed on their next Subscribe; their buffers simply stop filling.
func (h *pubsubHub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	ps := h.ps
	h.ps = nil
	h.mu.Unlock()
	close(h.stop)
	if ps != nil {
		_ = ps.Close() // unblocks the receive loop's read
	}
	if h.running.Load() {
		<-h.stopped
	}
}

// newSubscriber returns one socket's handle on the hub.
func (h *pubsubHub) newSubscriber() *hubSubscriber {
	h.start()
	return &hubSubscriber{
		hub:      h,
		ch:       make(chan *redis.Message, hubSubscriberBuffer),
		done:     make(chan struct{}),
		channels: map[string]struct{}{},
	}
}

// hubSubscriber is one socket's view of the hub. Subscribe / Unsubscribe
// satisfy roomSubscriber and roomUnsubscriber, so every room path that used a
// per-socket *redis.PubSub is unchanged.
type hubSubscriber struct {
	hub  *pubsubHub
	ch   chan *redis.Message
	done chan struct{} // closed when the hub evicts this subscriber as slow

	slow     atomic.Bool
	slowOnce sync.Once

	mu       sync.Mutex
	channels map[string]struct{}
	closed   bool
}

// Channel delivers this socket's messages, in Redis order.
func (s *hubSubscriber) Channel() <-chan *redis.Message { return s.ch }

// Done is closed when the hub dropped this socket for not keeping up.
func (s *hubSubscriber) Done() <-chan struct{} { return s.done }

func (s *hubSubscriber) markSlow() {
	s.slowOnce.Do(func() {
		s.slow.Store(true)
		close(s.done)
	})
}

// Subscribe registers the socket for each channel. Callers authorize the
// room first; the hub only routes. It returns once Redis has acknowledged
// every channel (immediately for one the process already held), or an error,
// in which case none of channels is held by this socket.
func (s *hubSubscriber) Subscribe(ctx context.Context, channels ...string) error {
	var added []string
	for _, c := range channels {
		ok, err := s.subscribeOne(ctx, c)
		if err != nil {
			_ = s.Unsubscribe(context.Background(), added...)
			return err
		}
		if ok {
			added = append(added, c)
		}
	}
	return nil
}

// subscribeOne registers one channel; ok is false when the socket already
// held it (no new reference was taken).
func (s *hubSubscriber) subscribeOne(ctx context.Context, channel string) (bool, error) {
	h := s.hub
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return false, errSubscriberClosed
	}
	if _, held := s.channels[channel]; held {
		s.mu.Unlock()
		return false, nil
	}
	s.channels[channel] = struct{}{}
	s.mu.Unlock()

	h.cmdMu.Lock()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		h.cmdMu.Unlock()
		s.forget(channel)
		return false, errHubClosed
	}
	hc := h.channels[channel]
	first := hc == nil
	if first {
		hc = &hubChannel{subs: map[*hubSubscriber]struct{}{}, ready: make(chan struct{})}
		h.channels[channel] = hc
	}
	hc.subs[s] = struct{}{}
	ready := hc.ready
	confirmed := hc.confirmed
	ps := h.ps
	h.mu.Unlock()
	if first && ps != nil {
		// A failed write means the connection is broken: the receive loop
		// replaces it and re-SUBSCRIBEs every registered channel, this one
		// included, so the wait below still ends in an acknowledgement (or
		// the timeout). With no live connection the same reconnect covers it.
		if err := ps.Subscribe(ctx, channel); err != nil {
			h.log.Debug("pubsub hub subscribe write failed; waiting for reconnect", "err", err, "channel", channel)
		}
	}
	h.cmdMu.Unlock()

	var err error
	if !confirmed {
		err = waitReady(ctx, ready, h.stop)
	}
	if err != nil {
		s.forget(channel)
		h.release(s, channel)
		return false, err
	}
	return true, nil
}

func waitReady(ctx context.Context, ready <-chan struct{}, stop <-chan struct{}) error {
	t := time.NewTimer(hubSubscribeTimeout)
	defer t.Stop()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-stop:
		return errHubClosed
	case <-t.C:
		return errSubscribeTimeout
	}
}

// Unsubscribe drops the socket from each channel it holds; the hub
// unsubscribes Redis when the last local socket leaves.
func (s *hubSubscriber) Unsubscribe(_ context.Context, channels ...string) error {
	for _, c := range channels {
		if s.forget(c) {
			s.hub.release(s, c)
		}
	}
	return nil
}

// forget removes channel from the socket's own set; false if not held.
func (s *hubSubscriber) forget(channel string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, held := s.channels[channel]; !held {
		return false
	}
	delete(s.channels, channel)
	return true
}

// Close releases every channel the socket holds. Safe to call twice.
func (s *hubSubscriber) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	held := make([]string, 0, len(s.channels))
	for c := range s.channels {
		held = append(held, c)
	}
	s.channels = map[string]struct{}{}
	s.mu.Unlock()
	for _, c := range held {
		s.hub.release(s, c)
	}
	return nil
}

// release drops one socket's reference on channel and UNSUBSCRIBEs Redis when
// it was the last.
func (h *pubsubHub) release(s *hubSubscriber, channel string) {
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	h.mu.Lock()
	hc := h.channels[channel]
	if hc == nil {
		h.mu.Unlock()
		return
	}
	delete(hc.subs, s)
	last := len(hc.subs) == 0
	if last {
		delete(h.channels, channel)
	}
	ps := h.ps
	h.mu.Unlock()
	if last && ps != nil {
		// Best effort: if the write fails the connection is broken, and its
		// replacement only re-SUBSCRIBEs channels that still have sockets.
		if err := ps.Unsubscribe(context.Background(), channel); err != nil {
			h.log.Warn("pubsub hub unsubscribe failed", "err", err, "channel", channel)
		}
	}
}

// run owns the shared connection: connect, read until it breaks, replace it.
func (h *pubsubHub) run() {
	defer close(h.stopped)
	backoff := hubBackoffMin
	for {
		select {
		case <-h.stop:
			return
		default:
		}
		ps, ok := h.connect()
		if !ok {
			return
		}
		healthy, err := h.receive(ps)
		h.mu.Lock()
		if h.ps == ps {
			h.ps = nil
		}
		closed := h.closed
		h.mu.Unlock()
		_ = ps.Close()
		if closed {
			return
		}
		if errors.Is(err, redis.ErrClosed) {
			// The Redis client itself was closed (shutdown): nothing to
			// reconnect to.
			return
		}
		h.reconnects.Add(1)
		if healthy {
			backoff = hubBackoffMin
		}
		h.log.Warn("pubsub hub connection lost; resubscribing", "err", err, "retry_in", backoff)
		select {
		case <-h.stop:
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > hubBackoffMax {
			backoff = hubBackoffMax
		}
	}
}

// connect installs a fresh PubSub and re-SUBSCRIBEs every channel a local
// socket still holds. Each channel waits for its new acknowledgement before
// a newly arriving socket is told it is subscribed.
func (h *pubsubHub) connect() (*redis.PubSub, bool) {
	ps := h.rdb.Subscribe(context.Background()) // no channels: no I/O yet
	h.cmdMu.Lock()
	defer h.cmdMu.Unlock()
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = ps.Close()
		return nil, false
	}
	active := make([]string, 0, len(h.channels))
	for name, hc := range h.channels {
		if hc.confirmed {
			hc.confirmed = false
			hc.ready = make(chan struct{})
		}
		active = append(active, name)
	}
	h.ps = ps
	h.mu.Unlock()
	if len(active) > 0 {
		// An error here surfaces again from the first read, which replaces
		// the connection.
		if err := ps.Subscribe(context.Background(), active...); err != nil {
			h.log.Warn("pubsub hub resubscribe failed", "err", err, "channels", len(active))
		}
	}
	return ps, true
}

// receive reads ps until it fails. healthy reports whether anything was read.
func (h *pubsubHub) receive(ps *redis.PubSub) (healthy bool, err error) {
	pingOutstanding := false
	for {
		msg, err := ps.ReceiveTimeout(context.Background(), h.pingInterval)
		if err != nil {
			if isTimeout(err) {
				if pingOutstanding {
					return healthy, errHubStale
				}
				if err := ps.Ping(context.Background()); err != nil {
					return healthy, err
				}
				pingOutstanding = true
				continue
			}
			return healthy, err
		}
		healthy = true
		pingOutstanding = false
		switch m := msg.(type) {
		case *redis.Message:
			h.dispatch(m)
		case *redis.Subscription:
			if m.Kind == "subscribe" {
				h.confirm(m.Channel)
			}
		}
	}
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (h *pubsubHub) confirm(channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hc := h.channels[channel]; hc != nil && !hc.confirmed {
		hc.confirmed = true
		close(hc.ready)
	}
}

// dispatch hands msg to every socket registered for its channel without ever
// blocking: a socket whose buffer is full is marked slow and skipped from
// then on, so it cannot hold up anyone else.
func (h *pubsubHub) dispatch(msg *redis.Message) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	hc := h.channels[msg.Channel]
	if hc == nil {
		return
	}
	for s := range hc.subs {
		if s.slow.Load() {
			continue
		}
		select {
		case s.ch <- msg:
		default:
			s.markSlow()
		}
	}
}

// localSubscribers is how many sockets on this process hold channel.
func (h *pubsubHub) localSubscribers(channel string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if hc := h.channels[channel]; hc != nil {
		return len(hc.subs)
	}
	return 0
}
