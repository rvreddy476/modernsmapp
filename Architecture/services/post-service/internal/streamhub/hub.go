// Streamhub — single Redis PUBSUB subscription per channel, in-memory
// fan-out to many HTTP/SSE clients.
//
// Why this exists: the naive approach is to have each SSE handler
// open its own `s.rdb.Subscribe(ctx, channel)`. At 1M users with even
// 5% on a hashtag page, that's 50k Redis SUB connections per service
// instance — wasted memory on both sides and a hard ceiling on Redis
// connection limits.
//
// This package keeps exactly one Redis SUB per (instance × channel).
// Every HTTP handler that wants the same channel attaches an
// in-memory listener; messages from Redis fan out across all
// listeners with a small bounded buffer. When the last listener
// detaches, the Redis SUB is closed and the channel forgotten.
//
// Drop policy: each listener has a bounded outbound buffer
// (`listenerBufSize`). If a client is too slow to drain it, we DROP
// new messages for that listener rather than block — the SSE stream
// is decorative ("N new posts" / "trending snapshot"), so a tail of
// missed updates is preferable to back-pressuring the publisher path.

package streamhub

import (
	"context"
	"log/slog"
	"sync"

	"github.com/redis/go-redis/v9"
)

// listenerBufSize is the per-listener outbound channel capacity.
// Hashtag/trending streams emit 0–2 msg/min, so this is generous.
// A slow client whose buffer fills loses messages but the SSE
// connection itself stays up via keepalives.
const listenerBufSize = 16

// Hub owns the (channel name → channelHub) map plus the single
// shared Redis client. Safe for concurrent Subscribe / Unsubscribe
// from any goroutine.
type Hub struct {
	rdb *redis.Client
	log *slog.Logger

	mu       sync.Mutex
	channels map[string]*channelHub
}

func New(rdb *redis.Client, log *slog.Logger) *Hub {
	return &Hub{
		rdb:      rdb,
		log:      log.With("component", "streamhub"),
		channels: make(map[string]*channelHub),
	}
}

// Subscribe attaches a new listener to `channel`. Returns:
//   - msgs:  receive-only channel of Redis payloads
//   - cancel: must be called exactly once to detach + release
type Subscription struct {
	Msgs   <-chan []byte
	Cancel func()
}

func (h *Hub) Subscribe(ctx context.Context, channel string) (*Subscription, error) {
	h.mu.Lock()
	ch, ok := h.channels[channel]
	if !ok {
		ch = &channelHub{
			name:      channel,
			listeners: make(map[*listener]struct{}),
		}
		// Open the Redis SUB before exposing the channelHub so a
		// concurrent Subscribe call against the same channel finds a
		// ready-to-go hub.
		pubsub := h.rdb.Subscribe(ctx, channel)
		if _, err := pubsub.Receive(ctx); err != nil {
			h.mu.Unlock()
			_ = pubsub.Close()
			return nil, err
		}
		ch.pubsub = pubsub

		runCtx, cancel := context.WithCancel(context.Background())
		ch.cancel = cancel
		go h.runChannel(runCtx, ch)

		h.channels[channel] = ch
	}
	l := &listener{
		out: make(chan []byte, listenerBufSize),
	}
	ch.attach(l)
	h.mu.Unlock()

	return &Subscription{
		Msgs: l.out,
		Cancel: func() {
			h.detach(channel, ch, l)
		},
	}, nil
}

func (h *Hub) detach(channel string, ch *channelHub, l *listener) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !ch.detach(l) {
		// Listener already gone (double-cancel). Nothing to do.
		return
	}
	if ch.listenerCount() > 0 {
		return
	}
	// Last listener gone → tear down Redis SUB.
	ch.cancel()
	_ = ch.pubsub.Close()
	delete(h.channels, channel)
}

func (h *Hub) runChannel(ctx context.Context, ch *channelHub) {
	msgs := ch.pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			payload := []byte(msg.Payload)
			ch.broadcast(payload)
		}
	}
}

// channelHub owns the listener fan-out for one Redis channel.
type channelHub struct {
	name   string
	pubsub *redis.PubSub
	cancel context.CancelFunc

	mu        sync.Mutex
	listeners map[*listener]struct{}
}

func (c *channelHub) attach(l *listener) {
	c.mu.Lock()
	c.listeners[l] = struct{}{}
	c.mu.Unlock()
}

// detach returns true when the listener was actually removed.
// Idempotent.
func (c *channelHub) detach(l *listener) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.listeners[l]; !ok {
		return false
	}
	delete(c.listeners, l)
	close(l.out)
	return true
}

func (c *channelHub) listenerCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.listeners)
}

func (c *channelHub) broadcast(payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for l := range c.listeners {
		select {
		case l.out <- payload:
		default:
			// Listener buffer full — drop. See drop-policy comment
			// at top of file.
		}
	}
}

type listener struct {
	out chan []byte
}
