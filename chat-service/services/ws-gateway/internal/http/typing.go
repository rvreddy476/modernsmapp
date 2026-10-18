package http

// Typing frames (chat parity §4, 18 Oct 2026).
//
// `typing.start` / `typing.stop` are accepted only for a conversation the
// connection is SUBSCRIBED to — the subscription is an owner-issued
// entitlement (message-service checked ACTIVE membership and signed it), so
// the gateway never relays typing into a room the sender does not belong to.
//
// The gateway does not decide disclosure itself. The sender's
// send_typing_indicators toggle, a paused chat_availability, an unknown
// policy (fail closed) and the deactivated-account rule all live in
// message-service's REST typing path (service.SetTyping → GetChatPolicy), and
// there is no shared helper for them. Duplicating that policy here would let
// the two drift, so every transition is forwarded to
// `POST /v1/chat/conversations/:id/typing {"is_typing": bool}` with the
// connection's own bearer token; message-service applies the policy and
// publishes the `typing {conversation_id, user_id, is_typing}` frame.
//
// The tracker turns a stream of keystroke frames into transitions: the first
// start sends is_typing:true, further starts only re-arm the 6-second timer
// (and re-assert true at most every typingRefresh so a late joiner sees it),
// and stop — explicit, on the timer, on unsubscribe or on disconnect — sends
// is_typing:false exactly once.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	nethttp "net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// typingTimeout is when an unrefreshed typing indicator auto-stops.
	typingTimeout = 6 * time.Second
	// typingRefresh bounds how often a continuing typist re-asserts true.
	typingRefresh = 4 * time.Second
	// typingQueueSize bounds per-connection pending typing calls; typing is
	// best-effort, so a full queue drops rather than blocking the read loop.
	typingQueueSize = 32
	typingCallTimeout = 3 * time.Second
)

// TypingNotifier forwards one typing transition to the policy owner.
type TypingNotifier interface {
	SetTyping(ctx context.Context, bearerToken string, conversationID uuid.UUID, isTyping bool) error
}

// HTTPTypingNotifier calls message-service's REST typing route.
type HTTPTypingNotifier struct {
	BaseURL string
	Client  *nethttp.Client
}

func NewHTTPTypingNotifier(baseURL string, client *nethttp.Client) *HTTPTypingNotifier {
	if client == nil {
		client = &nethttp.Client{Timeout: typingCallTimeout}
	}
	return &HTTPTypingNotifier{BaseURL: strings.TrimRight(baseURL, "/"), Client: client}
}

func (n *HTTPTypingNotifier) SetTyping(ctx context.Context, bearerToken string, conversationID uuid.UUID, isTyping bool) error {
	if n == nil || n.BaseURL == "" {
		return fmt.Errorf("typing notifier not configured")
	}
	body, _ := json.Marshal(map[string]bool{"is_typing": isTyping})
	url := fmt.Sprintf("%s/v1/chat/conversations/%s/typing", n.BaseURL, conversationID)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	resp, err := n.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("typing call: status %d", resp.StatusCode)
	}
	return nil
}

// typingClock is the tracker's time source, faked in tests.
type typingClock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) typingTimer
}

type typingTimer interface{ Stop() bool }

type realTypingClock struct{}

func (realTypingClock) Now() time.Time { return time.Now() }
func (realTypingClock) AfterFunc(d time.Duration, f func()) typingTimer {
	return time.AfterFunc(d, f)
}

type typingEntry struct {
	lastSent time.Time
	timer    typingTimer
	gen      uint64
}

// typingTracker is one connection's typing state, keyed by conversation.
type typingTracker struct {
	mu      sync.Mutex
	clock   typingClock
	timeout time.Duration
	refresh time.Duration
	active  map[string]*typingEntry
	closed  bool
	emit    func(conversationID string, isTyping bool)
}

func newTypingTracker(clock typingClock, timeout, refresh time.Duration, emit func(string, bool)) *typingTracker {
	if clock == nil {
		clock = realTypingClock{}
	}
	if timeout <= 0 {
		timeout = typingTimeout
	}
	if refresh <= 0 {
		refresh = typingRefresh
	}
	return &typingTracker{
		clock: clock, timeout: timeout, refresh: refresh,
		active: map[string]*typingEntry{}, emit: emit,
	}
}

// Start records a typing.start: emits true on a transition (or when the last
// true is older than refresh) and re-arms the auto-stop timer.
func (t *typingTracker) Start(conversationID string) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	now := t.clock.Now()
	e := t.active[conversationID]
	send := e == nil || now.Sub(e.lastSent) >= t.refresh
	if e == nil {
		e = &typingEntry{}
		t.active[conversationID] = e
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	e.gen++
	gen := e.gen
	e.timer = t.clock.AfterFunc(t.timeout, func() { t.expire(conversationID, gen) })
	if send {
		e.lastSent = now
	}
	t.mu.Unlock()
	if send {
		t.emit(conversationID, true)
	}
}

// Stop records a typing.stop; a stop with nothing active emits nothing.
func (t *typingTracker) Stop(conversationID string) {
	t.mu.Lock()
	e := t.active[conversationID]
	if e == nil {
		t.mu.Unlock()
		return
	}
	if e.timer != nil {
		e.timer.Stop()
	}
	delete(t.active, conversationID)
	t.mu.Unlock()
	t.emit(conversationID, false)
}

func (t *typingTracker) expire(conversationID string, gen uint64) {
	t.mu.Lock()
	e := t.active[conversationID]
	if e == nil || e.gen != gen {
		t.mu.Unlock()
		return
	}
	delete(t.active, conversationID)
	t.mu.Unlock()
	t.emit(conversationID, false)
}

// StopAll ends every active indicator (disconnect) and refuses later starts.
func (t *typingTracker) StopAll() {
	t.mu.Lock()
	t.closed = true
	ids := make([]string, 0, len(t.active))
	for id, e := range t.active {
		if e.timer != nil {
			e.timer.Stop()
		}
		ids = append(ids, id)
	}
	t.active = map[string]*typingEntry{}
	t.mu.Unlock()
	for _, id := range ids {
		t.emit(id, false)
	}
}

// typingQueue serializes one connection's typing calls so true/false arrive
// at message-service in order, without blocking the read loop.
type typingQueue struct {
	mu     sync.Mutex
	closed bool
	ch     chan typingEvent
}

type typingEvent struct {
	conversationID uuid.UUID
	isTyping       bool
}

func newTypingQueue() *typingQueue { return &typingQueue{ch: make(chan typingEvent, typingQueueSize)} }

func (q *typingQueue) push(ev typingEvent) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	select {
	case q.ch <- ev:
		return true
	default:
		return false
	}
}

func (q *typingQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}

// run drains the queue until close. Each call gets its own timeout and a
// context detached from the connection, so the final is_typing:false of a
// disconnect still goes out.
func (q *typingQueue) run(notifier TypingNotifier, token string, userID uuid.UUID, log *slog.Logger) {
	for ev := range q.ch {
		if notifier == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), typingCallTimeout)
		if err := notifier.SetTyping(ctx, token, ev.conversationID, ev.isTyping); err != nil {
			log.Warn("typing forward failed", "err", err, "user_id", userID,
				"conversation_id", ev.conversationID, "is_typing", ev.isTyping)
		}
		cancel()
	}
}
