package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Ticks (founder, 22 Oct 2026): the pure rules plus the history path over
// a fake store. These are the privacy rules the mutation check targets:
// read hidden when receipts are off on EITHER side, read hidden in a
// pending request, delivered always shown.

func pol(vis string) ChatPolicy {
	return ChatPolicy{Known: true, ReadReceiptsVisibility: vis}
}

func TestReadReceiptVisible(t *testing.T) {
	unknown := ChatPolicy{Known: false, ReadReceiptsVisibility: "everyone"}
	paused := ChatPolicy{Known: true, ChatPaused: true, ReadReceiptsVisibility: "everyone"}
	cases := []struct {
		name           string
		viewer, reader ChatPolicy
		want           bool
	}{
		{"both everyone", pol("everyone"), pol("everyone"), true},
		{"reader connections_only in an accepted chat", pol("everyone"), pol("connections_only"), true},
		{"viewer connections_only", pol("connections_only"), pol("everyone"), true},
		{"reader off", pol("everyone"), pol("no_one"), false},
		{"viewer off (reciprocity)", pol("no_one"), pol("everyone"), false},
		{"both off", pol("no_one"), pol("no_one"), false},
		{"reader unknown fails closed", pol("everyone"), unknown, false},
		{"viewer unknown fails closed", unknown, pol("everyone"), false},
		{"reader paused", pol("everyone"), paused, false},
		{"viewer paused", paused, pol("everyone"), false},
		{"reader unknown value", pol("everyone"), pol("contacts"), false},
		{"reader empty value", pol("everyone"), pol(""), false},
	}
	for _, tc := range cases {
		if got := readReceiptVisible(tc.viewer, tc.reader); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

var t0 = time.Date(2026, 10, 22, 10, 0, 0, 0, time.UTC)

func at(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }

func TestTickFor(t *testing.T) {
	d, r := at(10), at(5)
	w := ReceiptWatermarks{DeliveredUpTo: &d, ReadUpTo: &r}
	for _, tc := range []struct {
		ts   time.Time
		want string
	}{
		{at(1), TickRead}, {at(5), TickRead}, {at(6), TickDelivered}, {at(10), TickDelivered}, {at(11), TickSent},
	} {
		if got := tickFor(tc.ts, w); got != tc.want {
			t.Errorf("ts %v: %s, want %s", tc.ts, got, tc.want)
		}
	}
	if got := tickFor(at(1), ReceiptWatermarks{}); got != TickSent {
		t.Errorf("no watermarks: %s", got)
	}
}

func TestComputeWatermarksDirect(t *testing.T) {
	peer := receiptPeer{UserID: uuid.New(), JoinedAt: at(0), Delivered: at(10), Read: at(5), ReadDisclosed: true}

	w := computeWatermarks([]receiptPeer{peer}, true, true)
	if w.DeliveredUpTo == nil || !w.DeliveredUpTo.Equal(at(10)) || w.ReadUpTo == nil || !w.ReadUpTo.Equal(at(5)) {
		t.Fatalf("accepted chat: %+v", w)
	}
	// Read withheld (either side off) -> delivered still shown.
	off := peer
	off.ReadDisclosed = false
	w = computeWatermarks([]receiptPeer{off}, true, true)
	if w.ReadUpTo != nil {
		t.Fatalf("read shown with receipts off: %v", *w.ReadUpTo)
	}
	if w.DeliveredUpTo == nil || !w.DeliveredUpTo.Equal(at(10)) {
		t.Fatalf("delivered must be shown with receipts off: %+v", w)
	}
	// Pending request (gate closed) -> no read, delivered shown.
	w = computeWatermarks([]receiptPeer{peer}, true, false)
	if w.ReadUpTo != nil || w.DeliveredUpTo == nil {
		t.Fatalf("request: %+v", w)
	}
	// Reading implies delivery: a read cursor past the delivered one counts.
	ahead := peer
	ahead.Delivered, ahead.Read = time.Time{}, at(7)
	w = computeWatermarks([]receiptPeer{ahead}, true, true)
	if w.DeliveredUpTo == nil || !w.DeliveredUpTo.Equal(at(7)) {
		t.Fatalf("read without an ack: %+v", w)
	}
	// Nobody else: no ticks beyond sent.
	if w := computeWatermarks(nil, false, true); w.DeliveredUpTo != nil || w.ReadUpTo != nil {
		t.Fatalf("empty: %+v", w)
	}
}

func TestComputeWatermarksGroup(t *testing.T) {
	a := receiptPeer{UserID: uuid.New(), JoinedAt: at(0), Delivered: at(20), Read: at(15), ReadDisclosed: true}
	// b joined at 30 and has acked nothing: not a recipient of earlier messages.
	b := receiptPeer{UserID: uuid.New(), JoinedAt: at(30)}
	w := computeWatermarks([]receiptPeer{a, b}, false, true)
	if tickFor(at(10), w) != TickRead || tickFor(at(18), w) != TickDelivered || tickFor(at(25), w) != TickSent {
		t.Fatalf("newcomer handling: %+v", w)
	}
	if tickFor(at(31), w) != TickSent {
		t.Fatal("a message after the newcomer joined needs their device too")
	}
	// Every member must have it: c without any cursor holds delivered back.
	c := receiptPeer{UserID: uuid.New(), JoinedAt: at(0)}
	w = computeWatermarks([]receiptPeer{a, c}, false, true)
	if tickFor(at(10), w) != TickSent {
		t.Fatalf("one member without the message: %s", tickFor(at(10), w))
	}
	// A withheld reader never makes a message read, but delivered stays.
	cOff := receiptPeer{UserID: uuid.New(), JoinedAt: at(0), Delivered: at(20), Read: at(20), ReadDisclosed: false}
	w = computeWatermarks([]receiptPeer{a, cOff}, false, true)
	if tickFor(at(10), w) != TickDelivered {
		t.Fatalf("withheld group reader: %s", tickFor(at(10), w))
	}
}

func TestAggregateMoves(t *testing.T) {
	s, a, b := uuid.New(), uuid.New(), uuid.New()
	// Direct: the actor's move always reaches the other member.
	moves := aggregateMoves(map[uuid.UUID]time.Time{s: at(0), a: at(10)}, a, at(5), at(10))
	if len(moves) != 1 || !moves[s].Equal(at(10)) {
		t.Fatalf("direct: %v", moves)
	}
	// No move, no frames.
	if m := aggregateMoves(map[uuid.UUID]time.Time{s: at(0), a: at(10)}, a, at(10), at(10)); len(m) != 0 {
		t.Fatalf("no-op: %v", m)
	}
	// Group: a moves 5 -> 12 but b sits at 8. For s the watermark moves
	// 5 -> 8; for b (whose own cursor does not count for b) it moves 5 -> 12.
	eff := map[uuid.UUID]time.Time{s: at(30), a: at(12), b: at(8)}
	moves = aggregateMoves(eff, a, at(5), at(12))
	if !moves[s].Equal(at(8)) || !moves[b].Equal(at(12)) || len(moves) != 2 {
		t.Fatalf("group: %v", moves)
	}
	// a was not holding anyone back: b at 3 is the minimum for s already.
	eff = map[uuid.UUID]time.Time{s: at(30), a: at(12), b: at(3)}
	moves = aggregateMoves(eff, a, at(5), at(12))
	if _, ok := moves[s]; ok {
		t.Fatalf("s's watermark did not move: %v", moves)
	}
	if !moves[b].Equal(at(12)) {
		t.Fatalf("b's watermark is a's alone: %v", moves)
	}
}

// --- the history path over a fake store ---

type ticksFake struct {
	*consentFake
	joined    map[uuid.UUID]time.Time
	delivered map[uuid.UUID]postgres.DeliveredCursor
}

func newTicksFake() *ticksFake {
	return &ticksFake{consentFake: newConsentFake(), joined: map[uuid.UUID]time.Time{}, delivered: map[uuid.UUID]postgres.DeliveredCursor{}}
}

func (f *ticksFake) GetMembers(ctx context.Context, convID uuid.UUID) ([]postgres.Member, error) {
	var out []postgres.Member
	for u, r := range f.roles[convID] {
		out = append(out, postgres.Member{UserID: u, Role: r, JoinedAt: f.joined[u]})
	}
	return out, nil
}

func (f *ticksFake) UpsertDeliveredCursor(_ context.Context, convID, userID uuid.UUID, msgID *uuid.UUID, at time.Time) (bool, error) {
	if cur, ok := f.delivered[userID]; ok && !cur.LastDeliveredAt.Before(at) {
		return false, nil
	}
	f.delivered[userID] = postgres.DeliveredCursor{ConversationID: convID, UserID: userID, LastDeliveredAt: at}
	return true, nil
}

func (f *ticksFake) GetConversationDeliveredCursors(context.Context, uuid.UUID) (map[uuid.UUID]postgres.DeliveredCursor, error) {
	out := map[uuid.UUID]postgres.DeliveredCursor{}
	for k, v := range f.delivered {
		out[k] = v
	}
	return out, nil
}

func (f *ticksFake) GetOtherMembersDeliveredCursors(context.Context, uuid.UUID, []uuid.UUID) (map[uuid.UUID]map[uuid.UUID]postgres.DeliveredCursor, error) {
	return nil, nil
}

func (f *ticksFake) GetUserPolicies(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.UserPolicy, error) {
	out := map[uuid.UUID]postgres.UserPolicy{}
	for _, id := range ids {
		if p, ok := f.policies[id]; ok {
			out[id] = *p
		}
	}
	return out, nil
}

func (f *ticksFake) GetReadCursor(_ context.Context, _ uuid.UUID, userID uuid.UUID) (*postgres.ReadCursor, error) {
	if rc, ok := f.cursors[userID]; ok {
		return &rc, nil
	}
	return nil, nil
}

func ticksService(f *ticksFake) *Service {
	s := consentService(f.consentFake, "")
	s.convStore = f
	s.nowFn = func() time.Time { return at(60) }
	return s
}

func ownPage(sender uuid.UUID, mins ...int) []MessageResponse {
	out := make([]MessageResponse, len(mins))
	for i, m := range mins {
		out[i] = MessageResponse{MsgID: uuid.New(), SenderID: sender, Ts: at(m)}
	}
	return out
}

func statuses(msgs []MessageResponse) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		if m.Status != nil {
			out[i] = *m.Status
		}
	}
	return out
}

func TestHistoryTicksDirectPrivacy(t *testing.T) {
	ctx := context.Background()
	f := newTicksFake()
	sender, reader := uuid.New(), uuid.New()
	conv := uuid.New()
	f.conversations[conv] = &postgres.Conversation{ID: conv, Type: "direct"}
	f.roles[conv] = map[uuid.UUID]string{sender: "member", reader: "member"}
	f.joined[sender], f.joined[reader] = at(0), at(0)
	f.freshPolicy(sender, false)
	f.freshPolicy(reader, false)
	f.delivered[reader] = postgres.DeliveredCursor{UserID: reader, LastDeliveredAt: at(20)}
	f.cursors[reader] = postgres.ReadCursor{UserID: reader, LastReadAt: at(10)}
	s := ticksService(f)

	check := func(name string, want ...string) {
		t.Helper()
		page := ownPage(sender, 5, 15, 25)
		page = append(page, MessageResponse{MsgID: uuid.New(), SenderID: reader, Ts: at(6)})
		s.attachTickStatus(ctx, sender, conv, page)
		got := statuses(page)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: got %v, want %v", name, got, want)
			}
		}
		if page[3].Status != nil {
			t.Fatalf("%s: another member's message carries a status", name)
		}
	}
	check("both on", TickRead, TickDelivered, TickSent)

	f.policies[reader].ReadReceiptsVisibility = "no_one"
	check("reader off", TickDelivered, TickDelivered, TickSent)
	f.policies[reader].ReadReceiptsVisibility = "everyone"

	f.policies[sender].ReadReceiptsVisibility = "no_one"
	check("sender off (reciprocity)", TickDelivered, TickDelivered, TickSent)
	f.policies[sender].ReadReceiptsVisibility = "everyone"

	f.policies[reader].ReadReceiptsVisibility = "connections_only"
	check("reader connections_only", TickRead, TickDelivered, TickSent)
	f.policies[reader].ReadReceiptsVisibility = "everyone"

	f.conversations[conv].IsRequest = true
	check("pending request", TickDelivered, TickDelivered, TickSent)
	f.conversations[conv].IsRequest = false

	// The conversation response carries the same watermarks.
	resp := &ConversationResponse{ID: conv}
	s.attachConversationReceipts(ctx, sender, resp)
	if resp.Receipts == nil || resp.Receipts.ReadUpTo == nil || !resp.Receipts.ReadUpTo.Equal(at(10)) ||
		resp.Receipts.DeliveredUpTo == nil || !resp.Receipts.DeliveredUpTo.Equal(at(20)) {
		t.Fatalf("conversation receipts: %+v", resp.Receipts)
	}
}

func TestMarkDelivered(t *testing.T) {
	ctx := context.Background()
	f := newTicksFake()
	sender, reader, stranger := uuid.New(), uuid.New(), uuid.New()
	conv := uuid.New()
	f.conversations[conv] = &postgres.Conversation{ID: conv, Type: "direct", IsRequest: true}
	f.roles[conv] = map[uuid.UUID]string{sender: "member", reader: "member"}
	f.joined[sender], f.joined[reader] = at(0), at(0)
	s := ticksService(f)

	if _, err := s.MarkDelivered(ctx, stranger, conv, nil, nil); !errors.Is(err, ErrNotMember) {
		t.Fatalf("stranger: %v", err)
	}
	ts := at(30)
	ack, err := s.MarkDelivered(ctx, reader, conv, nil, &ts)
	if err != nil || !ack.Advanced || ack.DeliveredUpTo == nil || !ack.DeliveredUpTo.Equal(at(30)) {
		t.Fatalf("ack: %+v %v", ack, err)
	}
	// Monotonic: an older ack does not move it back.
	old := at(10)
	ack, err = s.MarkDelivered(ctx, reader, conv, nil, &old)
	if err != nil || ack.Advanced || !ack.DeliveredUpTo.Equal(at(30)) {
		t.Fatalf("older ack: %+v %v", ack, err)
	}
	// A future ts is capped at now.
	future := at(600)
	if ack, err = s.MarkDelivered(ctx, reader, conv, nil, &future); err != nil || !ack.DeliveredUpTo.Equal(at(60)) {
		t.Fatalf("future ts: %+v %v", ack, err)
	}
	// Delivered shows in a pending request; read does not.
	f.freshPolicy(sender, false)
	f.freshPolicy(reader, false)
	f.cursors[reader] = postgres.ReadCursor{UserID: reader, LastReadAt: at(40)}
	page := ownPage(sender, 20)
	s.attachTickStatus(ctx, sender, conv, page)
	if got := statuses(page)[0]; got != TickDelivered {
		t.Fatalf("request thread tick: %s", got)
	}
	// Rate limited.
	s.rateLimiter = &fakeLimiter{allowed: false}
	if _, err := s.MarkDelivered(ctx, reader, conv, nil, nil); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("rate limit: %v", err)
	}
}
