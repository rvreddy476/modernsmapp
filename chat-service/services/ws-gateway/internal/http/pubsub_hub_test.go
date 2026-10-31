package http

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/chat-shared/roomauth"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newTestHub(t *testing.T) (*pubsubHub, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	h := newPubsubHub(rdb, quietLog())
	t.Cleanup(h.Close)
	return h, rdb, mr
}

func redisSubscribers(t *testing.T, rdb *redis.Client, channel string) int64 {
	t.Helper()
	got, err := rdb.PubSubNumSub(context.Background(), channel).Result()
	if err != nil {
		t.Fatal(err)
	}
	return got[channel]
}

func waitRedisSubscribers(t *testing.T, rdb *redis.Client, channel string, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if got := redisSubscribers(t, rdb, channel); got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: Redis subscribers = %d, want %d", channel, redisSubscribers(t, rdb, channel), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func recvWithin(t *testing.T, s *hubSubscriber, d time.Duration) *redis.Message {
	t.Helper()
	select {
	case m := <-s.Channel():
		return m
	case <-time.After(d):
		t.Fatal("no message delivered")
		return nil
	}
}

func assertNothing(t *testing.T, s *hubSubscriber, d time.Duration) {
	t.Helper()
	select {
	case m := <-s.Channel():
		t.Fatalf("unexpected delivery on %s: %s", m.Channel, m.Payload)
	case <-time.After(d):
	}
}

// Redis is subscribed once per process however many sockets share a channel,
// and unsubscribed only when the last one leaves.
func TestHubRefCountsChannels(t *testing.T) {
	h, rdb, _ := newTestHub(t)
	ctx := context.Background()
	a, b := h.newSubscriber(), h.newSubscriber()

	if err := a.Subscribe(ctx, "room:1"); err != nil {
		t.Fatal(err)
	}
	// Subscribe returns only after Redis acknowledged: no polling needed.
	if n := redisSubscribers(t, rdb, "room:1"); n != 1 {
		t.Fatalf("after first subscribe Redis has %d subscribers, want 1", n)
	}
	if err := b.Subscribe(ctx, "room:1"); err != nil {
		t.Fatal(err)
	}
	// A repeat by the same socket takes no second reference.
	if err := a.Subscribe(ctx, "room:1"); err != nil {
		t.Fatal(err)
	}
	if n := h.localSubscribers("room:1"); n != 2 {
		t.Fatalf("local subscribers = %d, want 2", n)
	}
	if n := redisSubscribers(t, rdb, "room:1"); n != 1 {
		t.Fatalf("two sockets opened %d Redis subscriptions, want 1", n)
	}

	_ = a.Unsubscribe(ctx, "room:1")
	_ = a.Unsubscribe(ctx, "room:1") // a second leave is a no-op, not another release
	if n := h.localSubscribers("room:1"); n != 1 {
		t.Fatalf("after one leave local subscribers = %d, want 1", n)
	}
	time.Sleep(50 * time.Millisecond)
	if n := redisSubscribers(t, rdb, "room:1"); n != 1 {
		t.Fatalf("Redis dropped the channel while a socket still held it (%d)", n)
	}
	_ = b.Close()
	if n := h.localSubscribers("room:1"); n != 0 {
		t.Fatalf("after the last leave local subscribers = %d", n)
	}
	waitRedisSubscribers(t, rdb, "room:1", 0)
}

// A message reaches every socket registered for its channel, in publish
// order, and no socket registered elsewhere.
func TestHubFansOutInOrderToRegisteredSocketsOnly(t *testing.T) {
	h, rdb, _ := newTestHub(t)
	ctx := context.Background()
	a, b, other := h.newSubscriber(), h.newSubscriber(), h.newSubscriber()
	for _, s := range []*hubSubscriber{a, b} {
		if err := s.Subscribe(ctx, "room:fan"); err != nil {
			t.Fatal(err)
		}
	}
	if err := other.Subscribe(ctx, "room:else"); err != nil {
		t.Fatal(err)
	}
	const n = 100
	for i := 0; i < n; i++ {
		if err := rdb.Publish(ctx, "room:fan", fmt.Sprint(i)).Err(); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []*hubSubscriber{a, b} {
		for i := 0; i < n; i++ {
			m := recvWithin(t, s, 2*time.Second)
			if m.Channel != "room:fan" || m.Payload != fmt.Sprint(i) {
				t.Fatalf("message %d = %s/%s, want room:fan/%d", i, m.Channel, m.Payload, i)
			}
		}
	}
	assertNothing(t, other, 100*time.Millisecond)

	// After leaving, a socket hears nothing more from the channel.
	_ = a.Unsubscribe(ctx, "room:fan")
	_ = rdb.Publish(ctx, "room:fan", "after").Err()
	if m := recvWithin(t, b, 2*time.Second); m.Payload != "after" {
		t.Fatalf("remaining socket got %s", m.Payload)
	}
	assertNothing(t, a, 100*time.Millisecond)
}

// A socket that stops draining is cut off (Done closes) once its buffer is
// full; every other socket on the channel keeps receiving everything.
func TestHubSlowConsumerDoesNotBlockOthers(t *testing.T) {
	h, rdb, _ := newTestHub(t)
	ctx := context.Background()
	slow, fast := h.newSubscriber(), h.newSubscriber()
	for _, s := range []*hubSubscriber{slow, fast} {
		if err := s.Subscribe(ctx, "room:busy"); err != nil {
			t.Fatal(err)
		}
	}
	total := hubSubscriberBuffer + 50
	var wg sync.WaitGroup
	wg.Add(1)
	received := 0
	go func() {
		defer wg.Done()
		timeout := time.After(5 * time.Second)
		for received < total {
			select {
			case <-fast.Channel():
				received++
			case <-timeout:
				return
			}
		}
	}()
	for i := 0; i < total; i++ {
		if err := rdb.Publish(ctx, "room:busy", fmt.Sprint(i)).Err(); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	if received != total {
		t.Fatalf("fast socket received %d of %d while another socket was stalled", received, total)
	}
	select {
	case <-slow.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("a socket with a full buffer was not marked slow")
	}
	if got := len(slow.Channel()); got != hubSubscriberBuffer {
		t.Fatalf("slow socket buffered %d, want exactly its bound %d", got, hubSubscriberBuffer)
	}
	// Its teardown releases its reference like any other close.
	_ = slow.Close()
	if n := h.localSubscribers("room:busy"); n != 1 {
		t.Fatalf("local subscribers after the slow socket closed = %d, want 1", n)
	}
}

// After Redis drops the connection the hub reconnects and re-SUBSCRIBEs every
// channel a socket still holds — including one registered during the outage —
// and not one that every socket left meanwhile.
func TestHubResubscribesAfterRedisRestart(t *testing.T) {
	h, rdb, mr := newTestHub(t)
	ctx := context.Background()
	keep, leave := h.newSubscriber(), h.newSubscriber()
	if err := keep.Subscribe(ctx, "room:keep"); err != nil {
		t.Fatal(err)
	}
	if err := leave.Subscribe(ctx, "room:gone"); err != nil {
		t.Fatal(err)
	}
	before := h.reconnects.Load()

	mr.Close()
	_ = leave.Unsubscribe(ctx, "room:gone")
	joined := make(chan error, 1)
	late := h.newSubscriber()
	go func() { joined <- late.Subscribe(ctx, "room:late") }()
	time.Sleep(150 * time.Millisecond)
	if err := mr.Restart(); err != nil {
		t.Fatal(err)
	}

	if err := <-joined; err != nil {
		t.Fatalf("a subscribe made during the outage failed: %v", err)
	}
	waitRedisSubscribers(t, rdb, "room:keep", 1)
	waitRedisSubscribers(t, rdb, "room:late", 1)
	if n := redisSubscribers(t, rdb, "room:gone"); n != 0 {
		t.Fatalf("a channel nobody holds was resubscribed (%d)", n)
	}
	if h.reconnects.Load() == before {
		t.Fatal("the hub did not record a reconnect")
	}
	if err := rdb.Publish(ctx, "room:keep", "back").Err(); err != nil {
		t.Fatal(err)
	}
	if m := recvWithin(t, keep, 2*time.Second); m.Payload != "back" {
		t.Fatalf("after reconnect got %s", m.Payload)
	}
	_ = rdb.Publish(ctx, "room:late", "late").Err()
	if m := recvWithin(t, late, 2*time.Second); m.Payload != "late" {
		t.Fatalf("late subscriber got %s", m.Payload)
	}
}

// An idle but healthy connection answers the hub's PING and is kept.
func TestHubIdlePingKeepsHealthyConnection(t *testing.T) {
	h, rdb, _ := newTestHub(t)
	h.pingInterval = 30 * time.Millisecond
	ctx := context.Background()
	s := h.newSubscriber()
	if err := s.Subscribe(ctx, "room:idle"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // many ping intervals with no traffic
	if n := h.reconnects.Load(); n != 0 {
		t.Fatalf("a healthy idle connection was replaced %d times", n)
	}
	_ = rdb.Publish(ctx, "room:idle", "still here").Err()
	if m := recvWithin(t, s, 2*time.Second); m.Payload != "still here" {
		t.Fatalf("got %s", m.Payload)
	}
}

// A closed hub or socket refuses new subscriptions instead of pretending.
func TestHubRefusesAfterClose(t *testing.T) {
	h, _, _ := newTestHub(t)
	ctx := context.Background()
	s := h.newSubscriber()
	_ = s.Close()
	if err := s.Subscribe(ctx, "room:x"); !errors.Is(err, errSubscriberClosed) {
		t.Fatalf("closed subscriber: err = %v", err)
	}
	h.Close()
	if err := h.newSubscriber().Subscribe(ctx, "room:x"); !errors.Is(err, errHubClosed) {
		t.Fatalf("closed hub: err = %v", err)
	}
	if n := h.localSubscribers("room:x"); n != 0 {
		t.Fatalf("a refused subscribe left %d registrations", n)
	}
}

// Subscribe waits for Redis: with Redis gone it fails (bounded by ctx) and
// leaves no registration behind.
func TestHubSubscribeFailsCleanlyWithoutRedis(t *testing.T) {
	h, _, mr := newTestHub(t)
	mr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	s := h.newSubscriber()
	if err := s.Subscribe(ctx, "room:down"); err == nil {
		t.Fatal("subscribe succeeded with Redis down")
	}
	if n := h.localSubscribers("room:down"); n != 0 {
		t.Fatalf("a failed subscribe left %d registrations", n)
	}
}

// The shared subscription does not share authorization: with a member holding
// a conversation room on this gateway (so the process IS subscribed in
// Redis), another user's socket presenting the member's token, or a forged
// one, still never receives the room's frames.
func TestWSSharedRoomStillRequiresEachSocketsEntitlement(t *testing.T) {
	h := newLiveHarness(t, func(o *ServerOptions) { o.EntitlementSecret = hiddenEntSecret })
	member, intruder := uuid.New(), uuid.New()
	conv := uuid.NewString()
	sign := func(subject, secret string) string {
		tok, err := roomauth.Sign(roomauth.Claims{Version: 1, Subject: subject, ConversationID: conv,
			Audience: roomauth.Audience, ExpiresAt: time.Now().Add(roomauth.TTL).Unix(), Nonce: uuid.NewString(), Gen: 1}, secret)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	memberToken := sign(member.String(), hiddenEntSecret)

	in := h.dial(member)
	h.send(in, map[string]any{"type": "conversation.subscribe", "entitlement": memberToken})
	h.waitLocalSubscribers("convroom:"+conv, 1)
	h.waitSubscribers("convroom:"+conv, 1)

	out := h.dial(intruder)
	h.send(out, map[string]any{"type": "conversation.subscribe", "entitlement": memberToken})                             // someone else's token
	h.send(out, map[string]any{"type": "conversation.subscribe", "entitlement": sign(intruder.String(), "wrong-secret")}) // forged
	h.send(out, map[string]any{"type": "conversation.subscribe", "conversation_id": conv})                                // no token, no fetcher
	// A frame the intruder's read loop handles after the three above: once
	// its effect is visible, the subscribes have all been processed.
	ping := uuid.NewString()
	pingTok, _ := roomauth.Sign(roomauth.Claims{Version: 1, Subject: intruder.String(), ConversationID: ping,
		Audience: roomauth.Audience, ExpiresAt: time.Now().Add(roomauth.TTL).Unix(), Nonce: "p", Gen: 1}, hiddenEntSecret)
	h.send(out, map[string]any{"type": "conversation.subscribe", "entitlement": pingTok})
	h.waitLocalSubscribers("convroom:"+ping, 1)

	if n := h.server.hub.localSubscribers("convroom:" + conv); n != 1 {
		t.Fatalf("convroom held by %d local sockets, want only the member's", n)
	}
	room := `{"type":"message","payload":{"text":"members only"}}`
	h.publish("convroom:"+conv, room)
	if got := readFrame(t, in); got != room {
		t.Fatalf("member did not get the room frame: %s", got)
	}
	marker := fmt.Sprintf(`{"type":"marker","id":%q}`, uuid.NewString())
	h.publish("chat:"+intruder.String(), marker)
	if got := readFrame(t, out); got != marker {
		t.Fatalf("an unentitled socket received a shared room's frame: %s", got)
	}
}

// freezableProxy relays TCP to Redis; freeze() silences every connection
// open at that moment (bytes stop flowing both ways, the sockets stay open —
// a half-dead network path), while later connections relay normally.
type freezableProxy struct {
	ln     net.Listener
	target string
	mu     sync.Mutex
	live   []chan struct{}
	conns  []net.Conn
}

func newFreezableProxy(t *testing.T, target string) *freezableProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &freezableProxy{ln: ln, target: target}
	t.Cleanup(func() {
		_ = ln.Close()
		p.mu.Lock()
		defer p.mu.Unlock()
		for _, c := range p.conns {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.serve(c)
		}
	}()
	return p
}

func (p *freezableProxy) serve(c net.Conn) {
	up, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = c.Close()
		return
	}
	stop := make(chan struct{})
	p.mu.Lock()
	p.live = append(p.live, stop)
	p.conns = append(p.conns, c, up)
	p.mu.Unlock()
	pipe := func(dst, src net.Conn) {
		buf := make([]byte, 32*1024)
		for {
			n, err := src.Read(buf)
			if err != nil {
				return
			}
			select {
			case <-stop:
				continue // swallow: the path is silent, not closed
			default:
			}
			if _, err := dst.Write(buf[:n]); err != nil {
				return
			}
		}
	}
	go pipe(up, c)
	go pipe(c, up)
}

func (p *freezableProxy) freeze() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, stop := range p.live {
		close(stop)
	}
	p.live = nil
}

// A connection that stops answering (no error, no bytes) is detected by the
// unanswered PING and replaced, and the channel works again.
func TestHubReplacesSilentConnection(t *testing.T) {
	mr := miniredis.RunT(t)
	proxy := newFreezableProxy(t, mr.Addr())
	rdb := redis.NewClient(&redis.Options{Addr: proxy.ln.Addr().String()})
	t.Cleanup(func() { _ = rdb.Close() })
	h := newPubsubHub(rdb, quietLog())
	h.pingInterval = 50 * time.Millisecond
	t.Cleanup(h.Close)
	ctx := context.Background()
	s := h.newSubscriber()
	if err := s.Subscribe(ctx, "room:quiet"); err != nil {
		t.Fatal(err)
	}

	proxy.freeze()
	deadline := time.Now().Add(3 * time.Second)
	for h.reconnects.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a silent connection was never replaced")
		}
		time.Sleep(10 * time.Millisecond)
	}
	direct := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = direct.Close() })
	// The replacement re-SUBSCRIBEs; poll until a publish lands.
	deadline = time.Now().Add(3 * time.Second)
	for {
		_ = direct.Publish(ctx, "room:quiet", "again").Err()
		select {
		case m := <-s.Channel():
			if m.Payload != "again" {
				t.Fatalf("got %s", m.Payload)
			}
			return
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("the channel did not recover after the silent connection was replaced")
		}
	}
}

// The relay loop closes a socket the hub marked slow, exactly as it does
// when the socket's own outbound queue overflows.
func TestRedisLoopClosesSocketMarkedSlow(t *testing.T) {
	h, _, _ := newTestHub(t)
	s := &Server{log: quietLog(), hub: h}
	sub := h.newSubscriber()
	if err := sub.Subscribe(context.Background(), "chat:slow"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.redisLoop(ctx, cancel, sub, make(chan []byte, 1), uuid.New(), newRoomSubscriptions())
		close(done)
	}()
	sub.markSlow()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a socket the hub cut off was left open")
	}
	if ctx.Err() == nil {
		t.Fatal("the relay loop returned without cancelling the connection")
	}
}
