package http

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-shared/roomauth"
	"github.com/google/uuid"
)

// Hidden chats on the socket: {conversation_id, open_token} subscribes
// through message-service's gate; a locked conversation without a valid open
// token gets no room; the room's typing calls carry the open token.

const hiddenEntSecret = "hidden-chats-ent-secret"

// fakeMessageService answers the subscription and typing routes the way
// message-service's open-token gate does for ONE locked conversation.
type fakeMessageService struct {
	mu        sync.Mutex
	locked    string // conversation id that needs X-Chat-Open
	goodOpen  string
	subCalls  []string // "conv|open"
	typeCalls []string // "conv|open"
}

func (f *fakeMessageService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest, _ := strings.CutPrefix(r.URL.Path, "/v1/chat/conversations/")
	parts := strings.Split(rest, "/")
	conv, route := parts[0], parts[len(parts)-1]
	open := r.Header.Get("X-Chat-Open")
	f.mu.Lock()
	switch route {
	case "subscription":
		f.subCalls = append(f.subCalls, conv+"|"+open)
	case "typing":
		f.typeCalls = append(f.typeCalls, conv+"|"+open)
	}
	locked, good := f.locked, f.goodOpen
	f.mu.Unlock()
	if conv == locked && open != good {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"CHAT_LOCKED","message":"locked"}}`))
		return
	}
	if route == "typing" {
		w.WriteHeader(http.StatusOK)
		return
	}
	userID := r.URL.Query().Get("u")
	token, _ := roomauth.Sign(roomauth.Claims{Version: 1, Subject: userID, ConversationID: conv,
		Audience: roomauth.Audience, ExpiresAt: time.Now().Add(roomauth.TTL).Unix(), Nonce: "n", Gen: 1}, hiddenEntSecret)
	_, _ = fmt.Fprintf(w, `{"data":{"token":%q,"conversation_id":%q}}`, token, conv)
}

// subjectFetcher wraps the HTTP fetcher so the fake knows the user (the real
// message-service reads it from the bearer).
type subjectFetcher struct {
	inner *HTTPEntitlementFetcher
	user  uuid.UUID
}

func (s subjectFetcher) FetchEntitlement(ctx context.Context, bearer string, conv uuid.UUID, open string) (string, error) {
	f := *s.inner
	f.BaseURL = s.inner.BaseURL
	orig := f.Client.Transport
	f.Client = &http.Client{Transport: addQuery{base: orig, q: "u=" + s.user.String()}}
	return f.FetchEntitlement(ctx, bearer, conv, open)
}

type addQuery struct {
	base http.RoundTripper
	q    string
}

func (a addQuery) RoundTrip(r *http.Request) (*http.Response, error) {
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = a.q
	if a.base == nil {
		return http.DefaultTransport.RoundTrip(r2)
	}
	return a.base.RoundTrip(r2)
}

func TestSubscribeWithOpenTokenGoesThroughTheGate(t *testing.T) {
	user := uuid.New()
	hidden, open := uuid.NewString(), uuid.NewString()
	fake := &fakeMessageService{locked: hidden, goodOpen: "open-ok"}
	msgSrv := httptest.NewServer(fake)
	t.Cleanup(msgSrv.Close)
	h := newLiveHarness(t, func(o *ServerOptions) {
		o.EntitlementSecret = hiddenEntSecret
		o.EnableConversationFrames = true
		o.EntitlementFetcher = subjectFetcher{inner: NewHTTPEntitlementFetcher(msgSrv.URL, msgSrv.Client()), user: user}
		o.TypingNotifier = NewHTTPTypingNotifier(msgSrv.URL, msgSrv.Client())
	})
	conn := h.dial(user)

	// Hidden conversation without the open token: refused, no room.
	h.send(conn, map[string]any{"type": "conversation.subscribe", "conversation_id": hidden})
	got := readJSONFrame(t, conn)
	if got["type"] != "subscription_refused" || got["reason"] != "chat_locked" || got["conversation_id"] != hidden {
		t.Fatalf("refusal frame: %v", got)
	}
	h.publish("convroom:"+hidden, `{"type":"message","payload":{"text":"secret"}}`)
	marker := fmt.Sprintf(`{"type":"marker","id":%q}`, uuid.NewString())
	h.publish("chat:"+user.String(), marker)
	if f := readFrame(t, conn); f != marker {
		t.Fatalf("a locked room's frame reached a socket without an open token: %s", f)
	}

	// With the open token: the gateway fetched the entitlement through the
	// gate and joined the room.
	h.send(conn, map[string]any{"type": "conversation.subscribe", "conversation_id": hidden, "open_token": "open-ok"})
	h.waitSubscribers("convroom:"+hidden, 1)
	room := `{"type":"message","payload":{"text":"for the open session"}}`
	h.publish("convroom:"+hidden, room)
	if f := readFrame(t, conn); f != room {
		t.Fatalf("room frame: %s", f)
	}
	// An ordinary conversation subscribes the same way with no token.
	h.send(conn, map[string]any{"type": "conversation.subscribe", "conversation_id": open})
	h.waitSubscribers("convroom:"+open, 1)

	// Typing in the hidden room forwards the open token.
	h.send(conn, map[string]any{"type": "typing.start", "conversation_id": hidden})
	deadline := time.Now().Add(3 * time.Second)
	for {
		fake.mu.Lock()
		calls := append([]string(nil), fake.typeCalls...)
		fake.mu.Unlock()
		if len(calls) > 0 {
			if calls[0] != hidden+"|open-ok" {
				t.Fatalf("typing call: %v", calls)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no typing call")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.subCalls) != 3 || fake.subCalls[1] != hidden+"|open-ok" || fake.subCalls[2] != open+"|" {
		t.Fatalf("subscription calls: %v", fake.subCalls)
	}
}

// A subscribe that names one conversation but carries an entitlement for
// another joins neither: an open token checked for A must never open B.
func TestSubscribeRefusesAnEntitlementForAnotherConversation(t *testing.T) {
	user := uuid.New()
	named, other := uuid.NewString(), uuid.NewString()
	h := newLiveHarness(t, func(o *ServerOptions) { o.EntitlementSecret = hiddenEntSecret })
	conn := h.dial(user)
	ent, _ := roomauth.Sign(roomauth.Claims{Version: 1, Subject: user.String(), ConversationID: other,
		Audience: roomauth.Audience, ExpiresAt: time.Now().Add(roomauth.TTL).Unix(), Nonce: "n", Gen: 1}, hiddenEntSecret)
	h.send(conn, map[string]any{"type": "conversation.subscribe", "conversation_id": named, "entitlement": ent, "open_token": "open-ok"})
	// A valid subscribe after it: once that room is joined the first frame has
	// been processed (frames are handled in order).
	third := uuid.NewString()
	ent3, _ := roomauth.Sign(roomauth.Claims{Version: 1, Subject: user.String(), ConversationID: third,
		Audience: roomauth.Audience, ExpiresAt: time.Now().Add(roomauth.TTL).Unix(), Nonce: "n3", Gen: 1}, hiddenEntSecret)
	h.send(conn, map[string]any{"type": "conversation.subscribe", "entitlement": ent3})
	h.waitSubscribers("convroom:"+third, 1)
	h.publish("convroom:"+other, `{"type":"message","payload":{"text":"other room"}}`)
	marker := fmt.Sprintf(`{"type":"marker","id":%q}`, uuid.NewString())
	h.publish("chat:"+user.String(), marker)
	if f := readFrame(t, conn); f != marker {
		t.Fatalf("mismatched entitlement joined a room: %s", f)
	}
}
