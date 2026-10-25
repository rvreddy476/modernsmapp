package service

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Disappearing messages: expiry is captured at send time, "off" schedules
// nothing, a later timer change never moves an existing expiry, the worker
// takes only what the claim hands it (bounded by the batch) and deletes it for
// everyone along the unsend path, and expired messages are never served.

func ms(v int64) *int64 { return &v }

// --- expiry computation ---

func TestDisappearExpiryIsSendTimePlusTimer(t *testing.T) {
	sent := time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	cases := []struct {
		name  string
		timer *int64
		want  *time.Time
	}{
		{"off (nil)", nil, nil},
		{"off (0)", ms(0), nil},
		{"24h", ms(Disappear24h), timePtr(sent.Add(24 * time.Hour))},
		{"7d", ms(Disappear7d), timePtr(sent.Add(7 * 24 * time.Hour))},
		{"90d", ms(Disappear90d), timePtr(sent.Add(90 * 24 * time.Hour))},
	}
	for _, tc := range cases {
		got := disappearExpiry(sent, tc.timer)
		if (got == nil) != (tc.want == nil) || (got != nil && !got.Equal(*tc.want)) {
			t.Errorf("%s: expiry = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func timePtr(t time.Time) *time.Time { return &t }

func TestOnlyTheFourTimerValuesAreAllowed(t *testing.T) {
	for _, v := range []int64{0, 86400000, 604800000, 7776000000} {
		if !ValidDisappearAfterMs(v) {
			t.Errorf("%d must be allowed", v)
		}
	}
	for _, v := range []int64{-1, 1, 3600000, 30000, 86400001, 2592000000, 7776000001} {
		if ValidDisappearAfterMs(v) {
			t.Errorf("%d must be refused", v)
		}
	}
}

// --- fakes ---

type fakeExpiryRow struct {
	e    postgres.MessageExpiry
	next time.Time
}

// expiryFake models chat.message_expiries and the conversation timer. Its
// claim applies the SQL's due filter (next_attempt_at <= now) and the limit.
type expiryFake struct {
	now       time.Time
	timers    map[uuid.UUID]*int64
	rows      map[uuid.UUID]*fakeExpiryRow
	order     []uuid.UUID
	members   []uuid.UUID
	lastLimit int

	recordsErr   error
	recordsCalls []bool // alreadyGone per call
	recordsDone  map[uuid.UUID]bool
	payloads     []interface{}
	completed    []uuid.UUID
	deferred     []uuid.UUID
	setCalls     int
}

func newExpiryFake(now time.Time) *expiryFake {
	return &expiryFake{now: now, timers: map[uuid.UUID]*int64{}, rows: map[uuid.UUID]*fakeExpiryRow{},
		recordsDone: map[uuid.UUID]bool{}}
}

func (f *expiryFake) GetDisappearTimer(_ context.Context, id uuid.UUID) (*postgres.DisappearTimer, error) {
	return &postgres.DisappearTimer{AfterMs: f.timers[id]}, nil
}

func (f *expiryFake) SetDisappearTimer(_ context.Context, id uuid.UUID, afterMs *int64, _ uuid.UUID) error {
	f.setCalls++
	f.timers[id] = afterMs
	return nil
}

func (f *expiryFake) ScheduleMessageExpiry(_ context.Context, e postgres.MessageExpiry) error {
	if _, ok := f.rows[e.MessageID]; ok {
		return nil // ON CONFLICT DO NOTHING
	}
	f.rows[e.MessageID] = &fakeExpiryRow{e: e, next: e.ExpiresAt}
	f.order = append(f.order, e.MessageID)
	return nil
}

func (f *expiryFake) ClaimDueMessageExpiries(_ context.Context, limit int, lease time.Duration) ([]postgres.MessageExpiry, error) {
	f.lastLimit = limit
	var out []postgres.MessageExpiry
	for _, id := range f.order {
		r, ok := f.rows[id]
		if !ok || r.next.After(f.now) {
			continue
		}
		if len(out) >= limit {
			break
		}
		r.e.AttemptCount++
		r.next = f.now.Add(lease)
		out = append(out, r.e)
	}
	return out, nil
}

func (f *expiryFake) CompleteMessageExpiry(_ context.Context, id uuid.UUID) error {
	delete(f.rows, id)
	f.completed = append(f.completed, id)
	return nil
}

func (f *expiryFake) DeferMessageExpiry(_ context.Context, id uuid.UUID, retryIn time.Duration, _ string) error {
	if r, ok := f.rows[id]; ok {
		r.next = f.now.Add(retryIn)
	}
	f.deferred = append(f.deferred, id)
	return nil
}

func (f *expiryFake) ExpireMessageRecords(_ context.Context, e postgres.MessageExpiry, alreadyGone bool, _ string, payload interface{}) ([]uuid.UUID, error) {
	if f.recordsErr != nil {
		return nil, f.recordsErr
	}
	if f.recordsDone[e.MessageID] {
		return f.members, nil
	}
	f.recordsCalls = append(f.recordsCalls, alreadyGone)
	f.payloads = append(f.payloads, payload)
	f.recordsDone[e.MessageID] = true
	if r, ok := f.rows[e.MessageID]; ok {
		now := f.now
		r.e.DeletedAt = &now
	}
	return f.members, nil
}

// redactMsgStore adds the Scylla content wipe to the preview message store.
type redactMsgStore struct {
	*previewMsgStore
	redacts    int
	inboxDrops []uuid.UUID
}

func (m *redactMsgStore) RedactMessage(context.Context, uuid.UUID, string, time.Time, uuid.UUID) error {
	if m.journal != nil {
		*m.journal = append(*m.journal, "scylla-redact")
	}
	m.redacts++
	if m.target != nil {
		m.target.IsDeleted = true
		m.target.Text = ""
		m.target.Extra = ""
	}
	return nil
}

func (m *redactMsgStore) DeleteInboxProjection(_ context.Context, members []uuid.UUID, _ uuid.UUID, _ time.Time) error {
	m.inboxDrops = append(m.inboxDrops, members...)
	return nil
}

type expiryConvStore struct {
	*previewConvStore
	*expiryFake
}

type expiryFixture struct {
	sender, reader uuid.UUID
	convID, msgID  uuid.UUID
	ts             time.Time
	conv           *expiryConvStore
	msgs           *redactMsgStore
	svc            *Service
}

func newExpiryFixture(t *testing.T) *expiryFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	fx := &expiryFixture{sender: uuid.New(), reader: uuid.New(), convID: uuid.New(), msgID: uuid.New(),
		ts: now.Add(-25 * time.Hour)}
	f := newGovernanceFake()
	f.conversations[fx.convID] = &postgres.Conversation{ID: fx.convID, Type: "direct"}
	f.roles[fx.convID] = map[uuid.UUID]string{fx.sender: "member", fx.reader: "member"}
	pc := newPreviewConvStore(f)
	pc.currentLastMessageAt = fx.ts
	ef := newExpiryFake(now)
	ef.members = []uuid.UUID{fx.sender, fx.reader}
	fx.conv = &expiryConvStore{previewConvStore: pc, expiryFake: ef}
	fx.msgs = &redactMsgStore{previewMsgStore: &previewMsgStore{
		target: &scylla.Message{ConversationID: fx.convID, MsgID: fx.msgID, SenderID: fx.sender, Ts: fx.ts,
			Text: "this goes in 24 hours"},
		surviving: []scylla.Message{{ConversationID: fx.convID, SenderID: fx.reader, Ts: fx.ts.Add(-time.Minute), Text: "older, permanent"}},
	}}
	fx.svc = &Service{convStore: fx.conv, msgStore: fx.msgs, rdb: offlineRedis(), log: slog.Default()}
	fx.svc.SetClock(func() time.Time { return now })
	return fx
}

func (fx *expiryFixture) schedule(id uuid.UUID, expiresAt time.Time) {
	_ = fx.conv.ScheduleMessageExpiry(context.Background(), postgres.MessageExpiry{
		MessageID: id, ConversationID: fx.convID, Bucket: fx.ts.UTC().Format("200601"),
		MessageTS: fx.ts, SenderID: fx.sender, ExpiresAt: expiresAt,
	})
}

// --- the send path captures the timer ---

type disappearSendStore struct {
	*postSendStore
	*expiryFake
}

func TestSendCapturesTheTimerInForceAtSendTime(t *testing.T) {
	base := newPostFixture(t)
	ef := newExpiryFake(time.Now())
	store := &disappearSendStore{postSendStore: base.store, expiryFake: ef}
	base.svc.convStore = store
	meta := &postgres.ConversationMeta{SourceApp: "chat", DisappearAfterMs: ms(Disappear24h)}
	base.store.meta[base.convID] = meta
	ctx := context.Background()

	// 1. Sent under 24 h.
	first, err := base.sendText(base.userA, "under 24h", "dm-1")
	if err != nil {
		t.Fatal(err)
	}
	if first.ExpiresAt == nil || !first.ExpiresAt.Equal(first.CreatedAt.Add(24*time.Hour)) {
		t.Fatalf("expires_at = %v, want created_at + 24h (%v)", first.ExpiresAt, first.CreatedAt.Add(24*time.Hour))
	}
	row, ok := ef.rows[first.MsgID]
	if !ok || !row.e.ExpiresAt.Equal(*first.ExpiresAt) || !row.next.Equal(*first.ExpiresAt) {
		t.Fatalf("expiry row not scheduled at the message's expiry: %+v", row)
	}
	stored := decodeExtra(base.msgs.created[0].Extra)
	if stored == nil || stored.ExpiresAt == nil || !stored.ExpiresAt.Equal(*first.ExpiresAt) {
		t.Fatalf("the Scylla row must carry expires_at: %+v", stored)
	}

	// 2. The timer changes to 7 days: only NEW messages get it.
	meta.DisappearAfterMs = ms(Disappear7d)
	second, err := base.sendText(base.userA, "under 7d", "dm-2")
	if err != nil {
		t.Fatal(err)
	}
	if second.ExpiresAt == nil || !second.ExpiresAt.Equal(second.CreatedAt.Add(7*24*time.Hour)) {
		t.Fatalf("second expires_at = %v, want created_at + 7d", second.ExpiresAt)
	}

	// 3. Turned off: nothing new is scheduled, nothing old is cancelled or
	// extended.
	meta.DisappearAfterMs = nil
	third, err := base.sendText(base.userA, "permanent", "dm-3")
	if err != nil {
		t.Fatal(err)
	}
	if third.ExpiresAt != nil {
		t.Fatalf("a message sent with the timer off must not expire: %v", third.ExpiresAt)
	}
	if _, ok := ef.rows[third.MsgID]; ok {
		t.Fatal("timer off must not schedule an expiry")
	}
	if len(ef.rows) != 2 || !ef.rows[first.MsgID].e.ExpiresAt.Equal(*first.ExpiresAt) {
		t.Fatalf("turning the timer off moved or dropped earlier expiries: %d rows", len(ef.rows))
	}

	// 4. A delivery replay after the change keeps the first expiry: it comes
	// from the durable intent, never from the current timer.
	meta.DisappearAfterMs = ms(Disappear90d)
	intent := base.store.intents["dm-1"]
	intent.CompletedAt = nil
	if err := base.svc.completeMessageDelivery(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if !ef.rows[first.MsgID].e.ExpiresAt.Equal(*first.ExpiresAt) {
		t.Fatal("a replay moved the expiry")
	}
	replayed := decodeExtra(base.msgs.created[len(base.msgs.created)-1].Extra)
	if replayed == nil || replayed.ExpiresAt == nil || !replayed.ExpiresAt.Equal(*first.ExpiresAt) {
		t.Fatal("the replayed Scylla row lost or changed its expiry")
	}
}

// --- worker selection ---

func TestExpiryPassTakesOnlyDueRowsUpToTheBatch(t *testing.T) {
	fx := newExpiryFixture(t)
	now := fx.conv.now
	due := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	for i, id := range due {
		fx.schedule(id, now.Add(-time.Duration(i+1)*time.Minute))
	}
	future := uuid.New()
	fx.schedule(future, now.Add(time.Hour))

	n, err := fx.svc.runMessageExpiryPass(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if fx.conv.lastLimit != 2 {
		t.Fatalf("claim limit = %d, want the batch size 2", fx.conv.lastLimit)
	}
	if n != 2 || len(fx.conv.completed) != 2 {
		t.Fatalf("expired %d (completed %d), want exactly the batch of 2", n, len(fx.conv.completed))
	}
	for _, id := range fx.conv.completed {
		if id == future {
			t.Fatal("a message that is not due yet was expired")
		}
	}
	if _, ok := fx.conv.rows[future]; !ok {
		t.Fatal("the not-yet-due row must stay scheduled")
	}

	// The next pass takes the remaining due row and still leaves the future one.
	if n, err := fx.svc.runMessageExpiryPass(context.Background(), 2); err != nil || n != 1 {
		t.Fatalf("second pass expired %d (err %v), want 1", n, err)
	}
	if _, ok := fx.conv.rows[future]; !ok || len(fx.conv.rows) != 1 {
		t.Fatalf("after two passes only the future row may remain, have %d", len(fx.conv.rows))
	}
}

// --- deleting for everyone ---

func TestDueMessageIsDeletedForEveryoneAlongTheUnsendPath(t *testing.T) {
	fx := newExpiryFixture(t)
	var journal []string
	fx.conv.journal = &journal
	fx.msgs.journal = &journal
	fx.schedule(fx.msgID, fx.ts.Add(24*time.Hour))

	n, err := fx.svc.runMessageExpiryPass(context.Background(), 10)
	if err != nil || n != 1 {
		t.Fatalf("pass expired %d (err %v), want 1", n, err)
	}
	if len(fx.conv.recordsCalls) != 1 || fx.conv.recordsCalls[0] {
		t.Fatalf("the Postgres side (unread release + MessageDeleted) must run once as a live delete: %v", fx.conv.recordsCalls)
	}
	p, ok := fx.conv.payloads[0].(messageExpiredPayload)
	if !ok || p.Reason != "expired" || p.MessageID != fx.msgID.String() || p.ConversationID != fx.convID.String() {
		t.Fatalf("MessageDeleted payload = %+v", fx.conv.payloads[0])
	}
	if fx.msgs.redacts != 1 || !fx.msgs.target.IsDeleted || fx.msgs.target.Text != "" {
		t.Fatalf("the Scylla row must be redacted: redacts=%d deleted=%v text=%q",
			fx.msgs.redacts, fx.msgs.target.IsDeleted, fx.msgs.target.Text)
	}
	// MP-LB-1 write-ahead holds for expiry too.
	if len(journal) < 2 || journal[0] != "obligation" || journal[1] != "scylla-redact" {
		t.Fatalf("preview obligation must precede the Scylla mutation: %v", journal)
	}
	if !fx.conv.applied || fx.conv.lastPreview != "older, permanent" {
		t.Fatalf("inbox preview not repaired: applied=%v preview=%q", fx.conv.applied, fx.conv.lastPreview)
	}
	if len(fx.conv.obligations) != 0 {
		t.Fatal("the resolved preview obligation must be completed")
	}
	if len(fx.msgs.inboxDrops) != 2 {
		t.Fatalf("each member's inbox projection row must be dropped, got %v", fx.msgs.inboxDrops)
	}
	if _, ok := fx.conv.rows[fx.msgID]; ok {
		t.Fatal("the expiry row must be removed once the message is gone")
	}
}

func TestMessageUnsentBeforeExpiryOnlyHasItsContentWiped(t *testing.T) {
	fx := newExpiryFixture(t)
	fx.msgs.target.IsDeleted = true // a member unsent it already
	fx.schedule(fx.msgID, fx.ts.Add(24*time.Hour))

	if n, err := fx.svc.runMessageExpiryPass(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("pass expired %d (err %v)", n, err)
	}
	if len(fx.conv.recordsCalls) != 1 || !fx.conv.recordsCalls[0] {
		t.Fatal("unsend already released unread and announced it: expiry must not do it twice")
	}
	if len(fx.conv.obligations) != 0 || fx.conv.replaceCalls != 0 {
		t.Fatal("unsend already repaired the preview")
	}
	if fx.msgs.redacts != 1 {
		t.Fatal("the soft-deleted row still carries its text and must be redacted")
	}
}

func TestPendingDeliveryDefersTheExpiry(t *testing.T) {
	fx := newExpiryFixture(t)
	fx.conv.recordsErr = postgres.ErrExpiryDeliveryPending
	fx.schedule(fx.msgID, fx.ts.Add(24*time.Hour))

	n, err := fx.svc.runMessageExpiryPass(context.Background(), 10)
	if err != nil || n != 0 {
		t.Fatalf("pass expired %d (err %v), want 0", n, err)
	}
	if len(fx.conv.deferred) != 1 {
		t.Fatal("a pending delivery must defer the row, not drop it")
	}
	if fx.msgs.redacts != 0 || fx.msgs.target.IsDeleted {
		t.Fatal("nothing may be deleted while the delivery could still write the message back")
	}
	if _, ok := fx.conv.rows[fx.msgID]; !ok {
		t.Fatal("the row must stay scheduled")
	}
}

func TestRetryAfterAPartialExpiryFinishesWithoutRepeatingThePostgresSide(t *testing.T) {
	fx := newExpiryFixture(t)
	fx.schedule(fx.msgID, fx.ts.Add(24*time.Hour))
	fx.msgs.previewMsgStore.getMessageErr = nil
	// First attempt: the Postgres side commits, then the obligation write
	// fails (the process "dies" before Scylla is touched).
	fx.conv.createObligationErr = errors.New("postgres blip")
	if n, _ := fx.svc.runMessageExpiryPass(context.Background(), 10); n != 0 {
		t.Fatal("the failed attempt must not complete the row")
	}
	if fx.msgs.redacts != 0 {
		t.Fatal("Scylla must not be touched without the obligation")
	}
	// Retry once the lease/backoff elapsed.
	fx.conv.createObligationErr = nil
	fx.conv.now = fx.conv.now.Add(time.Hour)
	if n, err := fx.svc.runMessageExpiryPass(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("retry expired %d (err %v), want 1", n, err)
	}
	if len(fx.conv.recordsCalls) != 1 {
		t.Fatalf("the unread release / event must run exactly once, ran %d times", len(fx.conv.recordsCalls))
	}
	if fx.msgs.redacts != 1 || !fx.conv.applied {
		t.Fatal("the retry must finish the redaction and the preview repair")
	}
}

// --- read side ---

func TestExpiredMessagesAreNeverServed(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	past, _ := encodeExtra(&MessageExtra{ExpiresAt: timePtr(now.Add(-time.Second))})
	exact, _ := encodeExtra(&MessageExtra{ExpiresAt: timePtr(now)})
	future, _ := encodeExtra(&MessageExtra{ExpiresAt: timePtr(now.Add(time.Second))})
	msgs := []scylla.Message{
		{Text: "expired", Extra: string(past)},
		{Text: "expires now", Extra: string(exact)},
		{Text: "still here", Extra: string(future)},
		{Text: "permanent"},
	}
	kept := dropExpired(msgs, now)
	if len(kept) != 2 || kept[0].Text != "still here" || kept[1].Text != "permanent" {
		t.Fatalf("kept %+v", kept)
	}
	if len(msgs) != 4 || msgs[0].Text != "expired" {
		t.Fatal("dropExpired must not modify its input")
	}
}

// --- setting the timer ---

func TestTimerChangeRules(t *testing.T) {
	owner, admin, member, a, b := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	groupID, dmID := uuid.New(), uuid.New()
	f := newGovernanceFake()
	f.addGroup(groupID, owner, map[uuid.UUID]string{admin: "admin", member: "member"})
	f.conversations[dmID] = &postgres.Conversation{ID: dmID, Type: "direct"}
	f.roles[dmID] = map[uuid.UUID]string{a: "member", b: "member"}
	ef := newExpiryFake(time.Now())
	store := &struct {
		*governanceFake
		*expiryFake
	}{f, ef}
	svc := &Service{convStore: store, msgStore: &previewMsgStore{}, rdb: offlineRedis(), log: slog.Default()}
	ctx := context.Background()

	if _, err := svc.SetDisappearingTimer(ctx, a, dmID, 3600000); !errors.Is(err, ErrInvalidDisappearTimer) {
		t.Fatalf("1 h must be refused, got %v", err)
	}
	if got, err := svc.SetDisappearingTimer(ctx, b, dmID, Disappear7d); err != nil || got.DisappearAfterMs != Disappear7d {
		t.Fatalf("either DM member may set it: %+v %v", got, err)
	}
	if got, err := svc.SetDisappearingTimer(ctx, a, dmID, DisappearOff); err != nil || got.DisappearAfterMs != 0 || ef.timers[dmID] != nil {
		t.Fatalf("0 turns it off (stored NULL): %+v %v stored=%v", got, err, ef.timers[dmID])
	}
	calls := ef.setCalls
	if _, err := svc.SetDisappearingTimer(ctx, a, dmID, DisappearOff); err != nil || ef.setCalls != calls {
		t.Fatal("an unchanged value must not be written again")
	}
	if _, err := svc.SetDisappearingTimer(ctx, member, groupID, Disappear24h); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("a plain group member must be refused, got %v", err)
	}
	for _, u := range []uuid.UUID{owner, admin} {
		ef.timers[groupID] = nil
		if _, err := svc.SetDisappearingTimer(ctx, u, groupID, Disappear90d); err != nil {
			t.Fatalf("owner/admin may set it: %v", err)
		}
	}
	if _, err := svc.SetDisappearingTimer(ctx, uuid.New(), dmID, Disappear24h); err == nil {
		t.Fatal("a non-member must be refused")
	}
}

// --- pins: membership and conversation scoping ---

type pinStore struct {
	*governanceFake
	MessengerExtrasStore // nil: only the pin methods below are reached
	pins                 map[uuid.UUID]postgres.ConversationPin
}

func (p *pinStore) PinMessage(_ context.Context, convID, messageID, by uuid.UUID) (*postgres.ConversationPin, error) {
	pin := postgres.ConversationPin{ID: uuid.New(), ConversationID: convID, MessageID: messageID, PinnedBy: by}
	p.pins[pin.ID] = pin
	return &pin, nil
}

// UnpinMessage models DELETE ... WHERE id = $1 AND conversation_id = $2.
func (p *pinStore) UnpinMessage(_ context.Context, convID, pinID uuid.UUID) (*postgres.ConversationPin, error) {
	pin, ok := p.pins[pinID]
	if !ok || pin.ConversationID != convID {
		return nil, nil
	}
	delete(p.pins, pinID)
	return &pin, nil
}

func TestUnpinRequiresMembershipAndAPinOfThatConversation(t *testing.T) {
	member, outsider := uuid.New(), uuid.New()
	convA, convB := uuid.New(), uuid.New()
	f := newGovernanceFake()
	f.conversations[convA] = &postgres.Conversation{ID: convA, Type: "direct"}
	f.conversations[convB] = &postgres.Conversation{ID: convB, Type: "direct"}
	f.roles[convA] = map[uuid.UUID]string{member: "member"}
	f.roles[convB] = map[uuid.UUID]string{member: "member", outsider: "member"}
	store := &pinStore{governanceFake: f, pins: map[uuid.UUID]postgres.ConversationPin{}}
	svc := &Service{convStore: store, msgStore: &previewMsgStore{}, rdb: offlineRedis(), log: slog.Default()}
	ctx := context.Background()

	pin, err := svc.PinMessageSvc(ctx, member, convA, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	// A non-member of convA, naming convA and the real pin id.
	if err := svc.UnpinMessageSvc(ctx, outsider, convA, pin.ID); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member unpin = %v, want ErrNotMember", err)
	}
	// A member of convB naming convB with convA's pin id.
	if err := svc.UnpinMessageSvc(ctx, outsider, convB, pin.ID); !errors.Is(err, ErrPinNotFound) {
		t.Fatalf("cross-conversation unpin = %v, want ErrPinNotFound", err)
	}
	if _, ok := store.pins[pin.ID]; !ok {
		t.Fatal("a refused unpin deleted the pin")
	}
	if err := svc.UnpinMessageSvc(ctx, member, convA, pin.ID); err != nil {
		t.Fatalf("member unpin in the right conversation: %v", err)
	}
	if _, ok := store.pins[pin.ID]; ok {
		t.Fatal("the pin must be gone")
	}
}
