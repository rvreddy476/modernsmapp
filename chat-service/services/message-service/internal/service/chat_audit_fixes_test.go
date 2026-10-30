package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Chat backend audit 2026-10-30: H2 (edit runs the send gates), H4 (text cap
// on edit and scheduled messages), C1/C2 (one membership sentinel), C3 (edit
// and react see what the parity routes see), L5 (pins), L6 (read receipts).

// --- shared fakes ---

// parityLookup gives a fake store the parity methods visibleMessage and
// MarkRead reach. Any other parity method panics on the nil interface.
type parityLookup struct {
	parityStore
	at map[uuid.UUID]scylla.Message // message id -> its row
}

func newParityLookup(msgs ...scylla.Message) *parityLookup {
	p := &parityLookup{at: map[uuid.UUID]scylla.Message{}}
	for _, m := range msgs {
		p.at[m.MsgID] = m
	}
	return p
}

func (p *parityLookup) LookupMessageTS(_ context.Context, convID, msgID uuid.UUID) (string, time.Time, uuid.UUID, bool, error) {
	m, ok := p.at[msgID]
	if !ok || m.ConversationID != convID {
		return "", time.Time{}, uuid.Nil, false, nil
	}
	return m.Ts.UTC().Format("200601"), m.Ts, m.SenderID, true, nil
}
func (p *parityLookup) GetMemberCutoff(context.Context, uuid.UUID, uuid.UUID) (*time.Time, error) {
	return nil, nil
}
func (p *parityLookup) HiddenMessageIDs(context.Context, uuid.UUID, uuid.UUID, []uuid.UUID) (map[uuid.UUID]bool, error) {
	return nil, nil
}
func (p *parityLookup) ResetUnread(context.Context, uuid.UUID, uuid.UUID) error { return nil }

// metaConvStore overrides the dating meta of an author-rule fixture.
type metaConvStore struct {
	*authorRuleConvStore
	meta *postgres.ConversationMeta
}

func (m *metaConvStore) GetConversationMeta(context.Context, uuid.UUID) (*postgres.ConversationMeta, error) {
	return m.meta, nil
}

// reactionCountingMsgStore counts reaction writes.
type reactionCountingMsgStore struct {
	*authorRuleMsgStore
	adds int
}

func (m *reactionCountingMsgStore) AddReaction(context.Context, uuid.UUID, string, time.Time, uuid.UUID, string, uuid.UUID) error {
	m.adds++
	return nil
}

// publishRecorder is a redis hook that records PUBLISH commands instead of
// sending them (no server is needed).
type publishRecorder struct {
	mu       sync.Mutex
	payloads []string
}

func (r *publishRecorder) DialHook(next redis.DialHook) redis.DialHook { return next }
func (r *publishRecorder) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		r.record(cmd)
		return nil
	}
}
func (r *publishRecorder) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(_ context.Context, cmds []redis.Cmder) error {
		for _, cmd := range cmds {
			r.record(cmd)
		}
		return nil
	}
}
func (r *publishRecorder) record(cmd redis.Cmder) {
	if cmd.Name() != "publish" || len(cmd.Args()) < 3 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch v := cmd.Args()[2].(type) {
	case []byte:
		r.payloads = append(r.payloads, string(v))
	default:
		r.payloads = append(r.payloads, fmt.Sprint(v))
	}
}
func (r *publishRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.payloads...)
}

func recordingRedis() (*redis.Client, *publishRecorder) {
	rec := &publishRecorder{}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	rdb.AddHook(rec)
	return rdb, rec
}

// --- H2 / H4: edit runs the send gates and the send cap ---

func TestEditAppliesTheSendGates(t *testing.T) {
	ctx := context.Background()
	sender, other := uuid.New(), uuid.New()
	link := "verify your account at https://phish.example/login"

	type fixture struct {
		svc  *Service
		f    *governanceFake
		msgs *authorRuleMsgStore
		msg  *scylla.Message
		conv *metaConvStore
	}
	setup := func() fixture {
		f, conv, msgs, msg := authorRuleFixture("direct", sender, map[uuid.UUID]string{sender: "member", other: "member"})
		mc := &metaConvStore{authorRuleConvStore: conv}
		svc := newAuthorRuleService(conv, msgs)
		svc.convStore = mc
		return fixture{svc: svc, f: f, msgs: msgs, msg: msg, conv: mc}
	}
	edit := func(fx fixture, text string) error {
		_, err := fx.svc.EditMessage(ctx, sender, fx.msg.ConversationID, fx.msg.MsgID, fx.msg.Bucket, fx.msg.Ts, text)
		return err
	}
	pending := func(fx fixture, status string) {
		fx.f.conversations[fx.msg.ConversationID].IsRequest = true
		fx.f.latestRequest = &postgres.MessageRequest{ConversationID: fx.msg.ConversationID, SenderID: sender,
			ReceiverID: other, Status: status, Preview: "hi", ExpiresAt: time.Now().Add(24 * time.Hour)}
	}

	cases := []struct {
		name    string
		arrange func(fixture)
		text    string
		want    error
	}{
		{"link in a pending request", func(fx fixture) { pending(fx, "pending") }, link, ErrRequestFirstMessageInvalid},
		{"over 500 characters in a pending request", func(fx fixture) { pending(fx, "pending") }, strings.Repeat("a", 501), ErrRequestFirstMessageInvalid},
		{"a request that is no longer pending", func(fx fixture) { pending(fx, "ignored") }, "hello", ErrAwaitingRequestAcceptance},
		{"sender's chat paused", func(fx fixture) { fx.f.freshPolicy(sender, true) }, "hello", ErrMessagingNotAllowed},
		{"direct recipient paused", func(fx fixture) { fx.f.freshPolicy(other, true) }, "hello", ErrMessagingNotAllowed},
		{"sender policy unknown", func(fx fixture) { delete(fx.f.policies, sender) }, "hello", ErrMessagingNotAllowed},
		{"closed dating match", func(fx fixture) {
			closed := time.Now().Add(-time.Hour)
			fx.conv.meta = &postgres.ConversationMeta{SourceApp: "dating", ClosedAt: &closed}
		}, "hello", ErrMatchClosed},
		{"text over the send cap", func(fixture) {}, strings.Repeat("é", MessageTextMaxRunes+1), ErrInvalidMessageContent},
	}
	for _, tc := range cases {
		fx := setup()
		tc.arrange(fx)
		if err := edit(fx, tc.text); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		if fx.msgs.edits != 0 {
			t.Errorf("%s: a refused edit reached the store", tc.name)
		}
	}

	// Controls: the same edits pass when the gate does not apply.
	fx := setup()
	pending(fx, "pending")
	if err := edit(fx, "hello there"); err != nil || fx.msgs.edits != 1 {
		t.Fatalf("a plain edit inside a pending request: err=%v edits=%d", err, fx.msgs.edits)
	}
	fx = setup()
	if err := edit(fx, link); err != nil || fx.msgs.edits != 1 {
		t.Fatalf("a link in an accepted chat: err=%v edits=%d", err, fx.msgs.edits)
	}
	fx = setup()
	if err := edit(fx, strings.Repeat("é", MessageTextMaxRunes)); err != nil || fx.msgs.edits != 1 {
		t.Fatalf("an edit at exactly the cap: err=%v edits=%d", err, fx.msgs.edits)
	}
	fx = setup()
	open := &postgres.ConversationMeta{SourceApp: "dating"}
	fx.conv.meta = open
	if err := edit(fx, "hello"); err != nil {
		t.Fatalf("an open dating match: %v", err)
	}
}

// --- C3: edit and react refuse an expired disappearing message ---

func TestEditAndReactRefuseAnExpiredMessage(t *testing.T) {
	ctx := context.Background()
	sender, other := uuid.New(), uuid.New()
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)

	for _, tc := range []struct {
		expiresAt string
		visible   bool
	}{{past, false}, {future, true}} {
		_, conv, msgs, msg := authorRuleFixture("direct", sender, map[uuid.UUID]string{sender: "member", other: "member"})
		msg.Extra = `{"expires_at":"` + tc.expiresAt + `"}`
		counting := &reactionCountingMsgStore{authorRuleMsgStore: msgs}
		svc := newAuthorRuleService(conv, msgs)
		svc.msgStore = counting

		_, editErr := svc.EditMessage(ctx, sender, msg.ConversationID, msg.MsgID, msg.Bucket, msg.Ts, "rewritten")
		_, reactErr := svc.ToggleReaction(ctx, other, msg.ConversationID, msg.MsgID, msg.Bucket, msg.Ts, "👍")
		if tc.visible {
			if editErr != nil || reactErr != nil || msgs.edits != 1 || counting.adds != 1 {
				t.Fatalf("live message: edit=%v react=%v edits=%d adds=%d", editErr, reactErr, msgs.edits, counting.adds)
			}
			continue
		}
		if !errors.Is(editErr, ErrMessageNotFound) || !errors.Is(reactErr, ErrMessageNotFound) {
			t.Fatalf("expired message: edit=%v react=%v, want ErrMessageNotFound", editErr, reactErr)
		}
		if msgs.edits != 0 || counting.adds != 0 {
			t.Fatalf("expired message reached the store: edits=%d adds=%d", msgs.edits, counting.adds)
		}
	}
}

// --- C1 / C2: every membership refusal is the shared sentinel ---

func TestMembershipRefusalsAreErrNotMember(t *testing.T) {
	ctx := context.Background()
	sender, stranger := uuid.New(), uuid.New()
	_, conv, msgs, msg := authorRuleFixture("group", sender, map[uuid.UUID]string{sender: "owner"})
	svc := newAuthorRuleService(conv, msgs)
	convID := msg.ConversationID

	checks := map[string]error{}
	_, checks["GetMessages"] = func() ([]MessageResponse, error) {
		out, _, err := svc.GetMessages(ctx, stranger, convID, nil, 10)
		return out, err
	}()
	_, checks["SendMessage"] = svc.SendMessage(ctx, stranger, convID, MessageTypeText, "hi", nil, nil, nil, "k1")
	_, checks["EditMessage"] = svc.EditMessage(ctx, stranger, convID, msg.MsgID, msg.Bucket, msg.Ts, "x")
	checks["DeleteMessage"] = svc.DeleteMessage(ctx, stranger, convID, msg.MsgID, msg.Bucket, msg.Ts)
	_, checks["ToggleReaction"] = svc.ToggleReaction(ctx, stranger, convID, msg.MsgID, msg.Bucket, msg.Ts, "👍")
	checks["SetTyping"] = svc.SetTyping(ctx, stranger, convID)
	checks["MarkRead"] = svc.MarkRead(ctx, stranger, convID, msg.MsgID.String())
	_, checks["GetConversation"] = svc.GetConversation(ctx, stranger, convID)
	_, checks["GetConversationPresence"] = svc.GetConversationPresence(ctx, stranger, convID)
	_, checks["GetSettings"] = svc.GetSettings(ctx, stranger, convID)
	_, checks["GetPins"] = svc.GetPins(ctx, stranger, convID)
	_, checks["PinMessageSvc"] = svc.PinMessageSvc(ctx, stranger, convID, msg.MsgID)
	_, checks["CreateScheduledMessage"] = svc.CreateScheduledMessage(ctx, convID, stranger, MessageTypeText, "later", nil, time.Now().Add(time.Hour))
	_, checks["ListScheduledMessagesSvc"] = svc.ListScheduledMessagesSvc(ctx, stranger, convID)
	_, checks["StarMessageSvc"] = svc.StarMessageSvc(ctx, stranger, convID, msg.MsgID, nil)
	checks["AcceptRequest"] = svc.AcceptRequest(ctx, stranger, convID)
	checks["DeclineRequest"] = svc.DeclineRequest(ctx, stranger, convID)
	for name, err := range checks {
		if !errors.Is(err, ErrNotMember) {
			t.Errorf("%s: err = %v, want ErrNotMember", name, err)
		}
	}
}

// --- H4: scheduled messages are capped when they are created ---

// scheduleStore records scheduled-message writes.
type scheduleStore struct {
	*authorRuleConvStore
	MessengerExtrasStore // nil: only CreateScheduledMessage is reached
	created              int
}

func (s *scheduleStore) CreateScheduledMessage(_ context.Context, m *postgres.ScheduledMessage) (*postgres.ScheduledMessage, error) {
	s.created++
	return m, nil
}

func TestScheduledMessageTextIsCappedAtCreate(t *testing.T) {
	ctx := context.Background()
	sender := uuid.New()
	_, conv, msgs, msg := authorRuleFixture("direct", sender, map[uuid.UUID]string{sender: "member"})
	svc := newAuthorRuleService(conv, msgs)
	store := &scheduleStore{authorRuleConvStore: conv}
	svc.convStore = store

	_, err := svc.CreateScheduledMessage(ctx, msg.ConversationID, sender, MessageTypeText,
		strings.Repeat("a", MessageTextMaxRunes+1), nil, time.Now().Add(time.Hour))
	if !errors.Is(err, ErrInvalidMessageContent) || store.created != 0 {
		t.Fatalf("oversized scheduled text: err=%v stored=%d, want ErrInvalidMessageContent and nothing stored", err, store.created)
	}
	if _, err := svc.CreateScheduledMessage(ctx, msg.ConversationID, sender, MessageTypeText,
		strings.Repeat("a", MessageTextMaxRunes), nil, time.Now().Add(time.Hour)); err != nil || store.created != 1 {
		t.Fatalf("scheduled text at the cap: err=%v stored=%d", err, store.created)
	}
	// The send-side validator caps every caller (the scheduled worker sends
	// through it): text and media captions alike.
	media := uuid.New()
	if err := validateMessageContent(MessageTypeText, strings.Repeat("a", MessageTextMaxRunes+1), nil, nil); !errors.Is(err, ErrInvalidMessageContent) {
		t.Fatalf("text over the cap: %v", err)
	}
	if err := validateMessageContent(MessageTypeMedia, strings.Repeat("a", MessageTextMaxRunes+1), &media, nil); !errors.Is(err, ErrInvalidMessageContent) {
		t.Fatalf("media caption over the cap: %v", err)
	}
	if err := validateMessageContent(MessageTypeText, strings.Repeat("a", MessageTextMaxRunes), nil, nil); err != nil {
		t.Fatalf("text at the cap: %v", err)
	}
}

// --- L5: pins ---

type auditPinStore struct {
	*governanceFake
	*parityLookup
	MessengerExtrasStore // nil: only the pin methods below are reached
	pins                 map[uuid.UUID]postgres.ConversationPin
}

func (p *auditPinStore) PinMessage(_ context.Context, convID, messageID, by uuid.UUID) (*postgres.ConversationPin, error) {
	pin := postgres.ConversationPin{ID: uuid.New(), ConversationID: convID, MessageID: messageID, PinnedBy: by}
	p.pins[pin.ID] = pin
	return &pin, nil
}
func (p *auditPinStore) UnpinMessage(_ context.Context, convID, pinID uuid.UUID) (*postgres.ConversationPin, error) {
	pin, ok := p.pins[pinID]
	if !ok || pin.ConversationID != convID {
		return nil, nil
	}
	delete(p.pins, pinID)
	return &pin, nil
}

func TestPinRightsAndMessageScope(t *testing.T) {
	ctx := context.Background()
	owner, admin, member := uuid.New(), uuid.New(), uuid.New()
	groupID, otherConv := uuid.New(), uuid.New()
	f := newGovernanceFake()
	f.addGroup(groupID, owner, map[uuid.UUID]string{admin: "admin", member: "member"})
	f.addGroup(otherConv, owner, nil)
	inGroup := scylla.Message{MsgID: uuid.New(), ConversationID: groupID, SenderID: member, Ts: time.Now().Add(-time.Minute).UTC(), Type: "text"}
	elsewhere := scylla.Message{MsgID: uuid.New(), ConversationID: otherConv, SenderID: owner, Ts: time.Now().Add(-time.Minute).UTC(), Type: "text"}
	store := &auditPinStore{governanceFake: f, parityLookup: newParityLookup(inGroup, elsewhere), pins: map[uuid.UUID]postgres.ConversationPin{}}
	target := inGroup
	svc := &Service{convStore: store, msgStore: &previewMsgStore{target: &target}, rdb: offlineRedis(), log: slog.Default()}

	if _, err := svc.PinMessageSvc(ctx, member, groupID, inGroup.MsgID); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("a plain group member pinned: %v", err)
	}
	if _, err := svc.PinMessageSvc(ctx, admin, groupID, elsewhere.MsgID); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("a message of another conversation was pinned: %v", err)
	}
	if _, err := svc.PinMessageSvc(ctx, admin, groupID, uuid.New()); !errors.Is(err, ErrMessageNotFound) {
		t.Fatalf("an unknown message id was pinned: %v", err)
	}
	if len(store.pins) != 0 {
		t.Fatalf("a refused pin was stored: %d", len(store.pins))
	}
	pin, err := svc.PinMessageSvc(ctx, admin, groupID, inGroup.MsgID)
	if err != nil {
		t.Fatalf("admin pin: %v", err)
	}
	if err := svc.UnpinMessageSvc(ctx, member, groupID, pin.ID); !errors.Is(err, ErrNotPermittedRole) {
		t.Fatalf("a plain group member unpinned: %v", err)
	}
	if err := svc.UnpinMessageSvc(ctx, owner, groupID, pin.ID); err != nil {
		t.Fatalf("owner unpin: %v", err)
	}

	// A pending request: nobody pins until it is accepted.
	sender, receiver, reqConv := uuid.New(), uuid.New(), uuid.New()
	f.conversations[reqConv] = &postgres.Conversation{ID: reqConv, Type: "direct", IsRequest: true}
	f.roles[reqConv] = map[uuid.UUID]string{sender: "member", receiver: "member"}
	reqMsg := scylla.Message{MsgID: uuid.New(), ConversationID: reqConv, SenderID: sender, Ts: time.Now().UTC(), Type: "text"}
	store.at[reqMsg.MsgID] = reqMsg
	if _, err := svc.PinMessageSvc(ctx, sender, reqConv, reqMsg.MsgID); !errors.Is(err, ErrAwaitingRequestAcceptance) {
		t.Fatalf("a pin inside a pending request: %v", err)
	}
}

// --- L6: read receipts name a message of the conversation ---

func TestReadReceiptForwardsOnlyAMessageOfTheConversation(t *testing.T) {
	ctx := context.Background()
	reader, author := uuid.New(), uuid.New()
	convID, otherConv := uuid.New(), uuid.New()
	f := newGovernanceFake()
	f.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	f.roles[convID] = map[uuid.UUID]string{reader: "member", author: "member"}
	f.freshPolicy(reader, false)
	f.freshPolicy(author, false)
	mine := scylla.Message{MsgID: uuid.New(), ConversationID: convID, SenderID: author, Ts: time.Now().UTC()}
	foreign := scylla.Message{MsgID: uuid.New(), ConversationID: otherConv, SenderID: author, Ts: time.Now().UTC()}
	store := &struct {
		*governanceFake
		*parityLookup
	}{f, newParityLookup(mine, foreign)}
	rdb, rec := recordingRedis()
	defer rdb.Close()
	svc := &Service{convStore: store, msgStore: &previewMsgStore{}, rdb: rdb, log: slog.Default()}

	for _, raw := range []string{"<script>alert(1)</script>", foreign.MsgID.String(), uuid.NewString()} {
		if err := svc.MarkRead(ctx, reader, convID, raw); err != nil {
			t.Fatalf("mark read %q: %v", raw, err)
		}
	}
	for _, p := range rec.all() {
		if strings.Contains(p, "read_receipt") {
			t.Fatalf("a receipt naming no message of this conversation was forwarded: %s", p)
		}
	}
	if err := svc.MarkRead(ctx, reader, convID, mine.MsgID.String()); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range rec.all() {
		if strings.Contains(p, "read_receipt") && strings.Contains(p, mine.MsgID.String()) {
			found = true
		}
	}
	if !found {
		t.Fatalf("control: the receipt for a message of the conversation was not forwarded: %v", rec.all())
	}
}
