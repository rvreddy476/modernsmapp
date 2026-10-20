//go:build integration

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	pgstore "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// DM consent model end to end (contract §5): the real routes, service,
// Postgres (a *_test database), Scylla (a *_test keyspace) and Redis DB 15,
// with the parity fakes for identity / graph / directory. Fresh random users
// per test, so the per-sender request quota in Redis never carries over.

type consentEnv struct {
	*parityEnv
	t *testing.T
}

func newConsentEnv(t *testing.T) *consentEnv {
	return &consentEnv{parityEnv: newParityEnv(t), t: t}
}

func (e *consentEnv) decide(actor, target uuid.UUID, decision map[string]any) {
	e.fakes.mu.Lock()
	defer e.fakes.mu.Unlock()
	e.fakes.msgDecisions[actor.String()+">"+target.String()] = decision
}

var (
	asRequest = map[string]any{"allowed": false, "fallback": "message_request"}
	asDirect  = map[string]any{"allowed": true}
)

type convOut struct {
	ID        uuid.UUID `json:"id"`
	IsRequest bool      `json:"is_request"`
	Members   []struct {
		UserID     uuid.UUID  `json:"user_id"`
		LastReadAt *time.Time `json:"last_read_at"`
	} `json:"members"`
}

func (e *consentEnv) open(actor, target uuid.UUID) (int, convOut, []byte) {
	e.t.Helper()
	code, raw := e.do("POST", "/v1/chat/conversations/direct", actor, map[string]string{"other_user_id": target.String()})
	var c convOut
	if code == 200 {
		dataOf(e.t, raw, &c)
	}
	return code, c, raw
}

func (e *consentEnv) send(user, convID uuid.UUID, text string) map[string]any {
	e.t.Helper()
	code, raw := e.do("POST", "/v1/chat/conversations/"+convID.String()+"/messages", user, map[string]any{"type": "text", "text": text})
	e.must(code, 200, raw, "send "+text)
	var m map[string]any
	dataOf(e.t, raw, &m)
	return m
}

type requestsOut struct {
	Data []struct {
		ID        uuid.UUID `json:"id"`
		Filtered  bool      `json:"filtered"`
		Direction string    `json:"direction"`
		Preview   string    `json:"preview"`
	} `json:"data"`
	Meta struct {
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	} `json:"meta"`
}

func (e *consentEnv) requests(user uuid.UUID, query string) requestsOut {
	e.t.Helper()
	code, raw := e.do("GET", "/v1/chat/requests"+query, user, nil)
	e.must(code, 200, raw, "requests "+query)
	var out requestsOut
	if err := json.Unmarshal(raw, &out); err != nil {
		e.t.Fatalf("decode %s: %v", raw, err)
	}
	return out
}

func (out requestsOut) has(id uuid.UUID) bool {
	for _, r := range out.Data {
		if r.ID == id {
			return true
		}
	}
	return false
}

func (e *consentEnv) count(user uuid.UUID) (int, int) {
	e.t.Helper()
	code, raw := e.do("GET", "/v1/chat/requests/count", user, nil)
	e.must(code, 200, raw, "count")
	var c struct {
		Count         int `json:"count"`
		FilteredCount int `json:"filtered_count"`
	}
	dataOf(e.t, raw, &c)
	return c.Count, c.FilteredCount
}

func (e *consentEnv) accepted(a, b uuid.UUID) bool {
	e.t.Helper()
	req := fmt.Sprintf(`{"user_a":%q,"user_b":%q}`, a, b)
	w := postDatingMatch(e.router, "/internal/v1/chat/direct-chat/state", req, map[string]string{"X-Internal-Service-Key": parityKey})
	e.must(w.Code, 200, w.Body.Bytes(), "direct-chat state")
	var out struct {
		Accepted bool `json:"accepted"`
	}
	dataOf(e.t, w.Body.Bytes(), &out)
	return out.Accepted
}

func TestDMConsentRequestLifecycle(t *testing.T) {
	e := newConsentEnv(t)
	ctx := context.Background()
	sender, recipient := uuid.New(), uuid.New()
	e.decide(sender, recipient, asRequest)

	code, conv, raw := e.open(sender, recipient)
	e.must(code, 200, raw, "open request")
	if !conv.IsRequest {
		t.Fatalf("a stranger's chat must open as a request: %s", raw)
	}
	reqPath := "/v1/chat/conversations/" + conv.ID.String()
	msg := e.send(sender, conv.ID, "hello there")

	// Recipient's Requests: listed, with the message; the badge counts it.
	list := e.requests(recipient, "")
	if !list.has(conv.ID) || list.Data[0].Preview != "hello there" || list.Data[0].Direction != "incoming" || list.Meta.HasMore {
		t.Fatalf("recipient requests = %+v", list)
	}
	if n, _ := e.count(recipient); n != 1 {
		t.Fatalf("badge = %d", n)
	}
	// The sender's default list is incoming only; outgoing shows it.
	if e.requests(sender, "").has(conv.ID) || !e.requests(sender, "?direction=outgoing").has(conv.ID) {
		t.Fatal("incoming-by-default / outgoing lists are wrong for the sender")
	}
	if accepted := e.accepted(sender, recipient); accepted {
		t.Fatal("a pending request must not count as an accepted chat for calls")
	}

	// --- 5. reading a request discloses nothing to its sender ---
	sub := e.rdb.Subscribe(ctx, "chat:"+sender.String())
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	code, raw = e.do("POST", reqPath+"/read", recipient, map[string]string{"message_id": fmt.Sprint(msg["msg_id"])})
	e.must(code, 200, raw, "recipient reads the request")
	select {
	case m := <-sub.Channel():
		if strings.Contains(m.Payload, "read_receipt") {
			t.Fatalf("a read receipt reached the requester: %s", m.Payload)
		}
	case <-time.After(400 * time.Millisecond):
	}
	code, raw = e.do("GET", reqPath, sender, nil)
	e.must(code, 200, raw, "sender reloads the request")
	var reload convOut
	dataOf(t, raw, &reload)
	for _, m := range reload.Members {
		if m.UserID == recipient && m.LastReadAt != nil {
			t.Fatalf("reload disclosed the recipient's read cursor: %s", raw)
		}
	}
	code, raw = e.do("GET", "/v1/chat/conversations", sender, nil)
	e.must(code, 200, raw, "sender inbox")
	if strings.Contains(string(raw), `"own_status":"read"`) {
		t.Fatalf("sender inbox shows the request as read: %s", raw)
	}

	// --- 2. Delete: out of Requests, sender not told, 7-day cooldown ---
	code, raw = e.do("POST", reqPath+"/requests/decline", recipient, nil)
	e.must(code, 200, raw, "delete request")
	if e.requests(recipient, "").has(conv.ID) {
		t.Fatal("a deleted request is still in Requests")
	}
	if n, _ := e.count(recipient); n != 0 {
		t.Fatalf("badge after delete = %d", n)
	}
	if !e.requests(sender, "?direction=outgoing").has(conv.ID) {
		t.Fatal("the sender's outgoing view changed on delete: the sender must not be told")
	}
	// Inside 7 days the requester gets the same request back as if it were
	// still pending: 200, unchanged, nothing reaches the recipient, and a
	// further send is the ordinary "awaiting acceptance".
	code, within, raw := e.open(sender, recipient)
	e.must(code, 200, raw, "re-request inside 7 days")
	if within.ID != conv.ID || !within.IsRequest || strings.Contains(strings.ToLower(string(raw)), "cooldown") {
		t.Fatalf("re-request inside 7 days must return the request unchanged: %s", raw)
	}
	if e.requests(recipient, "").has(conv.ID) || e.requests(recipient, "?filtered=true").has(conv.ID) {
		t.Fatal("a re-request inside the cooldown reached the recipient's Requests")
	}
	if n, f := e.count(recipient); n != 0 || f != 0 {
		t.Fatalf("badge after a cooldown re-request = %d/%d", n, f)
	}
	if code, raw := e.do("POST", reqPath+"/messages", sender, map[string]any{"type": "text", "text": "did you see?"}); code != 409 ||
		!strings.Contains(string(raw), "AWAITING_REQUEST_ACCEPTANCE") {
		t.Fatalf("send inside the cooldown: %d %s", code, raw)
	}
	if _, err := e.pool.Exec(ctx, `UPDATE chat.message_requests SET responded_at = NOW() - INTERVAL '8 days' WHERE conversation_id = $1`, conv.ID); err != nil {
		t.Fatal(err)
	}
	code, again, raw := e.open(sender, recipient)
	e.must(code, 200, raw, "re-request after 7 days")
	if again.ID != conv.ID || !again.IsRequest {
		t.Fatalf("after 7 days: %s", raw)
	}
	list = e.requests(recipient, "")
	if !list.has(conv.ID) || list.Data[0].Preview != "" {
		t.Fatalf("the fresh request must be back in Requests with no message yet: %+v", list)
	}
	e.send(sender, conv.ID, "second try")

	// --- 3. 30-day expiry: gone from Requests at once, swept to 'expired' ---
	if _, err := e.pool.Exec(ctx, `UPDATE chat.message_requests SET expires_at = NOW() - INTERVAL '1 second' WHERE conversation_id = $1`, conv.ID); err != nil {
		t.Fatal(err)
	}
	if e.requests(recipient, "").has(conv.ID) {
		t.Fatal("a request past its expiry is still listed")
	}
	if code, raw := e.do("POST", reqPath+"/messages", sender, map[string]any{"type": "text", "text": "anyone?"}); code != 409 {
		t.Fatalf("sending into an expired request: %d %s", code, raw)
	}
	expired, err := pgstore.New(e.pool).ExpireDueMessageRequests(ctx, 50)
	if err != nil || len(expired) != 1 || expired[0].ConversationID != conv.ID {
		t.Fatalf("sweep = %+v err=%v", expired, err)
	}
	// Expired behaves like deleted for re-requesting, without a cooldown.
	if code, again, raw := e.open(sender, recipient); code != 200 || !again.IsRequest {
		t.Fatalf("re-request after expiry: %d %s", code, raw)
	}
	if !e.requests(recipient, "").has(conv.ID) {
		t.Fatal("re-request after expiry is not in Requests")
	}
}

func TestDMConsentAcceptAllowPromoteAndReasons(t *testing.T) {
	e := newConsentEnv(t)
	recipient := uuid.New()

	// --- 1. Accept opens the chat and makes NO graph connection (the
	// fake graph fails the test if connections/ensure is called) ---
	a := uuid.New()
	e.decide(a, recipient, asRequest)
	_, conv, _ := e.open(a, recipient)
	e.send(a, conv.ID, "hi")
	code, raw := e.do("POST", "/v1/chat/conversations/"+conv.ID.String()+"/requests/accept", recipient, nil)
	e.must(code, 200, raw, "accept")
	if !e.accepted(a, recipient) || !e.accepted(recipient, a) {
		t.Fatal("an accepted request must count as an accepted chat, either order")
	}

	// --- 6. recipient follows sender: graph allows, a NORMAL chat ---
	b := uuid.New()
	e.decide(b, recipient, asDirect)
	code, direct, raw := e.open(b, recipient)
	e.must(code, 200, raw, "allowed open")
	if direct.IsRequest || e.requests(recipient, "").has(direct.ID) {
		t.Fatalf("allow must make a normal chat: %s", raw)
	}
	if !e.accepted(b, recipient) {
		t.Fatal("a direct chat counts as accepted")
	}

	// A pending request, then the recipient follows the sender: promoted.
	c := uuid.New()
	e.decide(c, recipient, asRequest)
	_, pending, _ := e.open(c, recipient)
	e.send(c, pending.ID, "hey")
	e.decide(c, recipient, asDirect)
	code, promoted, raw := e.open(c, recipient)
	e.must(code, 200, raw, "promote")
	if promoted.ID != pending.ID || promoted.IsRequest || e.requests(recipient, "").has(pending.ID) {
		t.Fatalf("promotion: %s", raw)
	}
	code, raw = e.do("GET", "/v1/chat/conversations", recipient, nil)
	e.must(code, 200, raw, "recipient primary")
	if !strings.Contains(string(raw), pending.ID.String()) {
		t.Fatal("the promoted chat must be in the recipient's Chats")
	}

	// A blocked request inside 7 days: the neutral "unavailable", never a
	// cooldown (even if graph has not caught up with the block yet).
	g := uuid.New()
	e.decide(g, recipient, asRequest)
	_, blocked, _ := e.open(g, recipient)
	e.send(g, blocked.ID, "hello?")
	code, raw = e.do("POST", "/v1/chat/conversations/"+blocked.ID.String()+"/requests/block", recipient, nil)
	e.must(code, 200, raw, "block request")
	code, _, raw = e.open(g, recipient)
	if code != 403 || !strings.Contains(string(raw), `"reason":"unavailable"`) || strings.Contains(strings.ToLower(string(raw)), "cooldown") {
		t.Fatalf("re-request after a block: %d %s", code, raw)
	}

	// --- 7. deny reasons reach the client ---
	for graphReason, want := range map[string]string{"followers_only": "followers_only", "people_i_follow": "people_i_follow", "blocked": "unavailable"} {
		d := uuid.New()
		e.decide(d, recipient, map[string]any{"allowed": false, "reason": graphReason})
		code, _, raw := e.open(d, recipient)
		var env struct {
			Error struct {
				Code   string `json:"code"`
				Reason string `json:"reason"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		if code != 403 || env.Error.Code != "MESSAGING_NOT_ALLOWED" || env.Error.Reason != want {
			t.Fatalf("deny %s: %d %s", graphReason, code, raw)
		}
	}
}

func TestDMConsentRequestsPagingFilteredCount(t *testing.T) {
	e := newConsentEnv(t)
	recipient := uuid.New()
	e.fakes.mu.Lock()
	e.fakes.keywords[recipient] = []string{"spamword"}
	e.fakes.mu.Unlock()

	var plain, hidden []uuid.UUID
	for i := 0; i < 23; i++ {
		s := uuid.New()
		e.decide(s, recipient, asRequest)
		code, conv, raw := e.open(s, recipient)
		e.must(code, 200, raw, "open")
		if i%4 == 0 {
			e.send(s, conv.ID, fmt.Sprintf("buy spamword now %d", i))
			hidden = append(hidden, conv.ID)
		} else {
			e.send(s, conv.ID, fmt.Sprintf("hello %d", i))
			plain = append(plain, conv.ID)
		}
	}

	seen := map[uuid.UUID]bool{}
	cursor, pages := "", 0
	for {
		q := "?limit=7"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		page := e.requests(recipient, q)
		pages++
		for _, r := range page.Data {
			if r.Filtered || seen[r.ID] {
				t.Fatalf("page %d: filtered or repeated row %v", pages, r.ID)
			}
			seen[r.ID] = true
		}
		if !page.Meta.HasMore {
			break
		}
		cursor = page.Meta.NextCursor
	}
	if len(seen) != len(plain) || pages != 3 {
		t.Fatalf("paged %d of %d plain requests over %d pages", len(seen), len(plain), pages)
	}
	h := e.requests(recipient, "?filtered=true&limit=50")
	if len(h.Data) != len(hidden) || !h.Data[0].Filtered {
		t.Fatalf("hidden list = %+v, want %d", h, len(hidden))
	}
	if legacy := e.requests(recipient, "?filter=filtered&limit=50"); len(legacy.Data) != len(hidden) {
		t.Fatal("the legacy filter=filtered spelling must keep working")
	}
	if n, f := e.count(recipient); n != len(plain) || f != len(hidden) {
		t.Fatalf("count = %d/%d, want %d/%d", n, f, len(plain), len(hidden))
	}
}
