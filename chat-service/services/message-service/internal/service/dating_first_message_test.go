package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Dating lane D4. Until chat told dating-service about a dating
// conversation's first message, first_message_at stayed NULL and the dating
// sweeper expired every match seven days after it formed. These tests pin:
// exactly one call per conversation, the right match and actor, the request
// shape dating-service's internal family accepts, retry/terminal handling,
// and that a dating outage never fails a send.

const testInternalKey = "test-internal-key"

// --- fake dating-service ---

type datingCall struct {
	method string
	path   string
	actor  string
	header http.Header
}

type fakeDating struct {
	mu       sync.Mutex
	statuses []int // answered in order; the last one repeats; empty → 200
	calls    []datingCall
	srv      *httptest.Server
}

func newFakeDating(t *testing.T, statuses ...int) *fakeDating {
	t.Helper()
	d := &fakeDating{statuses: statuses}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ActorID string `json:"actor_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.mu.Lock()
		i := len(d.calls)
		d.calls = append(d.calls, datingCall{method: r.Method, path: r.URL.Path, actor: body.ActorID, header: r.Header.Clone()})
		status := http.StatusOK
		if len(d.statuses) > 0 {
			if i < len(d.statuses) {
				status = d.statuses[i]
			} else {
				status = d.statuses[len(d.statuses)-1]
			}
		}
		d.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":{"recorded":true}}`))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeDating) snapshot() []datingCall {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]datingCall(nil), d.calls...)
}

// --- fake stores: governance fake + durable delivery + D4 obligations ---

type fakeFirstMessageRow struct {
	n          postgres.DatingFirstMessageNotification
	delivered  bool
	terminal   bool
	lastStatus int
	defers     []time.Duration
}

type datingSendStore struct {
	*governanceFake
	meta    map[uuid.UUID]*postgres.ConversationMeta
	intents map[string]*postgres.MessageDeliveryIntent
	rows    map[uuid.UUID]*fakeFirstMessageRow
	order   []uuid.UUID
}

func newDatingSendStore() *datingSendStore {
	return &datingSendStore{
		governanceFake: newGovernanceFake(),
		meta:           map[uuid.UUID]*postgres.ConversationMeta{},
		intents:        map[string]*postgres.MessageDeliveryIntent{},
		rows:           map[uuid.UUID]*fakeFirstMessageRow{},
	}
}

func (s *datingSendStore) GetConversationMeta(_ context.Context, id uuid.UUID) (*postgres.ConversationMeta, error) {
	return s.meta[id], nil
}

func (s *datingSendStore) ReserveMessageDeliveryIntent(_ context.Context, intent postgres.MessageDeliveryIntent) (*postgres.MessageDeliveryIntent, error) {
	if existing, ok := s.intents[intent.IdempotencyKey]; ok {
		if existing.RequestHash != intent.RequestHash {
			return nil, postgres.ErrDeliveryIntentConflict
		}
		copied := *existing
		return &copied, nil
	}
	stored := intent
	s.intents[intent.IdempotencyKey] = &stored
	copied := stored
	return &copied, nil
}

func (s *datingSendStore) FetchPendingMessageDeliveryIntents(context.Context, int) ([]postgres.MessageDeliveryIntent, error) {
	var out []postgres.MessageDeliveryIntent
	for _, i := range s.intents {
		if i.CompletedAt == nil {
			out = append(out, *i)
		}
	}
	return out, nil
}

func (s *datingSendStore) CompleteMessageDeliveryIntent(_ context.Context, key string) error {
	if i, ok := s.intents[key]; ok && i.CompletedAt == nil {
		now := time.Now()
		i.CompletedAt = &now
	}
	return nil
}

func (s *datingSendStore) InsertOutboxEventOnce(context.Context, string, string, interface{}) error {
	return nil
}
func (s *datingSendStore) InsertMessageMediaReference(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (s *datingSendStore) ViewerMayAccessChatMedia(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (s *datingSendStore) SetLastMessage(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error {
	return nil
}

// EnqueueDatingFirstMessage models ON CONFLICT (conversation_id) DO NOTHING.
func (s *datingSendStore) EnqueueDatingFirstMessage(_ context.Context, n postgres.DatingFirstMessageNotification) (bool, error) {
	if _, ok := s.rows[n.ConversationID]; ok {
		return false, nil
	}
	s.rows[n.ConversationID] = &fakeFirstMessageRow{n: n}
	s.order = append(s.order, n.ConversationID)
	return true, nil
}

// ClaimDueDatingFirstMessages returns every pending row: the fake has no
// clock, so each pass stands for "the backoff has elapsed". Delivered and
// terminal rows are never returned, as in SQL.
func (s *datingSendStore) ClaimDueDatingFirstMessages(_ context.Context, limit int, _ time.Duration) ([]postgres.DatingFirstMessageNotification, error) {
	var out []postgres.DatingFirstMessageNotification
	for _, id := range s.order {
		r := s.rows[id]
		if r.delivered || r.terminal {
			continue
		}
		r.n.AttemptCount++
		out = append(out, r.n)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *datingSendStore) MarkDatingFirstMessageDelivered(_ context.Context, id uuid.UUID, status int) error {
	s.rows[id].delivered = true
	s.rows[id].lastStatus = status
	return nil
}

func (s *datingSendStore) MarkDatingFirstMessageTerminal(_ context.Context, id uuid.UUID, status int, _ string) error {
	s.rows[id].terminal = true
	s.rows[id].lastStatus = status
	return nil
}

func (s *datingSendStore) DeferDatingFirstMessage(_ context.Context, id uuid.UUID, retryIn time.Duration, status int, _ string) error {
	s.rows[id].defers = append(s.rows[id].defers, retryIn)
	s.rows[id].lastStatus = status
	return nil
}

// --- fixture ---

type datingFixture struct {
	svc     *Service
	store   *datingSendStore
	convID  uuid.UUID
	matchID uuid.UUID
	userA   uuid.UUID
	userB   uuid.UUID
}

// newDatingFixture builds a two-member dating conversation (or a plain chat
// one when dating is false) whose sends run the real SendMessage path.
func newDatingFixture(t *testing.T, datingURL string, dating bool) *datingFixture {
	t.Helper()
	st := newDatingSendStore()
	a, b := uuid.New(), uuid.New()
	convID, matchID := uuid.New(), uuid.New()
	st.conversations[convID] = &postgres.Conversation{ID: convID, Type: "direct"}
	st.roles[convID] = map[uuid.UUID]string{a: "member", b: "member"}
	st.freshPolicy(a, false)
	st.freshPolicy(b, false)
	if dating {
		m := matchID
		st.meta[convID] = &postgres.ConversationMeta{SourceApp: "dating", MatchID: &m}
	}
	svc := &Service{
		convStore:          st,
		msgStore:           &previewMsgStore{},
		rdb:                offlineRedis(),
		log:                slog.Default(),
		internalServiceKey: testInternalKey,
		datingServiceURL:   datingURL,
		httpClient:         &http.Client{Timeout: 2 * time.Second},
	}
	return &datingFixture{svc: svc, store: st, convID: convID, matchID: matchID, userA: a, userB: b}
}

func (f *datingFixture) send(t *testing.T, from uuid.UUID, key string) {
	t.Helper()
	if _, err := f.svc.SendMessage(context.Background(), from, f.convID, "text", "hello", nil, nil, key); err != nil {
		t.Fatalf("send %s: %v", key, err)
	}
}

func (f *datingFixture) pass(t *testing.T) {
	t.Helper()
	if err := f.svc.runDatingFirstMessagePass(context.Background()); err != nil {
		t.Fatalf("worker pass: %v", err)
	}
}

// --- tests ---

func TestFirstDatingMessageNotifiesDatingExactlyOnce(t *testing.T) {
	dating := newFakeDating(t)
	f := newDatingFixture(t, dating.srv.URL, true)

	f.send(t, f.userA, "d4-first")
	f.pass(t)

	calls := dating.snapshot()
	if len(calls) != 1 {
		t.Fatalf("first dating message must produce exactly one dating call, got %d", len(calls))
	}
	wantPath := "/v1/dating/internal/matches/" + f.matchID.String() + "/first-message"
	if calls[0].method != http.MethodPost || calls[0].path != wantPath {
		t.Fatalf("call = %s %s, want POST %s", calls[0].method, calls[0].path, wantPath)
	}
	if calls[0].actor != f.userA.String() {
		t.Fatalf("actor_id = %q, want the first sender %s", calls[0].actor, f.userA)
	}
	if !f.store.rows[f.convID].delivered {
		t.Fatal("a 200 from dating must retire the obligation")
	}
}

func TestLaterDatingMessagesDoNotNotifyDatingAgain(t *testing.T) {
	dating := newFakeDating(t)
	f := newDatingFixture(t, dating.srv.URL, true)

	f.send(t, f.userA, "d4-1")
	f.pass(t)
	f.send(t, f.userB, "d4-2")
	f.send(t, f.userA, "d4-3")
	f.pass(t)
	f.pass(t)

	if calls := dating.snapshot(); len(calls) != 1 {
		t.Fatalf("later messages must not call dating again, got %d calls", len(calls))
	}

	// Two messages landing before the worker runs still make one call, for
	// the first sender.
	dating2 := newFakeDating(t)
	g := newDatingFixture(t, dating2.srv.URL, true)
	g.send(t, g.userB, "d4-early-1")
	g.send(t, g.userA, "d4-early-2")
	g.pass(t)
	g.pass(t)
	calls := dating2.snapshot()
	if len(calls) != 1 || calls[0].actor != g.userB.String() {
		t.Fatalf("burst before the worker ran: calls=%d (want 1 for the first sender)", len(calls))
	}
}

func TestNonDatingConversationDoesNotNotifyDating(t *testing.T) {
	dating := newFakeDating(t)
	f := newDatingFixture(t, dating.srv.URL, false)

	f.send(t, f.userA, "chat-1")
	f.send(t, f.userB, "chat-2")
	f.pass(t)

	if calls := dating.snapshot(); len(calls) != 0 {
		t.Fatalf("a non-dating conversation must never call dating, got %d calls", len(calls))
	}
	if len(f.store.rows) != 0 {
		t.Fatalf("a non-dating conversation must not queue a notification, got %d", len(f.store.rows))
	}
}

func TestDatingFirstMessageRequestCarriesInternalKeyAndNoUserIdentity(t *testing.T) {
	dating := newFakeDating(t)
	f := newDatingFixture(t, dating.srv.URL, true)

	f.send(t, f.userA, "d4-shape")
	f.pass(t)

	calls := dating.snapshot()
	if len(calls) != 1 {
		t.Fatalf("want one call, got %d", len(calls))
	}
	h := calls[0].header
	if got := h.Get("X-Internal-Service-Key"); got != testInternalKey {
		t.Fatalf("X-Internal-Service-Key = %q, want the configured key", got)
	}
	// dating-service answers 403 USER_CALLER_REFUSED to any internal call
	// carrying a gateway identity header.
	for _, name := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
		if v := h.Get(name); v != "" {
			t.Fatalf("service call must not carry %s (got %q)", name, v)
		}
	}
	if ct := h.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestDatingFirstMessageRetriesServerErrorsUntilDelivered(t *testing.T) {
	dating := newFakeDating(t, http.StatusInternalServerError, http.StatusInternalServerError, http.StatusOK)
	f := newDatingFixture(t, dating.srv.URL, true)

	f.send(t, f.userA, "d4-retry")
	for i := 0; i < 6; i++ {
		f.pass(t)
	}

	calls := dating.snapshot()
	if len(calls) != 3 {
		t.Fatalf("500, 500, 200 must take exactly three calls and stop, got %d", len(calls))
	}
	row := f.store.rows[f.convID]
	if !row.delivered || row.terminal {
		t.Fatalf("row delivered=%v terminal=%v, want delivered", row.delivered, row.terminal)
	}
	if len(row.defers) != 2 || row.defers[0] <= 0 || row.defers[1] < row.defers[0] {
		t.Fatalf("each 500 must defer with a growing backoff, got %v", row.defers)
	}
}

func TestDatingFirstMessagePermanentRefusalIsTerminal(t *testing.T) {
	for _, status := range []int{http.StatusGone, http.StatusNotFound, http.StatusConflict} {
		dating := newFakeDating(t, status)
		f := newDatingFixture(t, dating.srv.URL, true)

		f.send(t, f.userA, "d4-terminal")
		for i := 0; i < 5; i++ {
			f.pass(t)
		}

		if calls := dating.snapshot(); len(calls) != 1 {
			t.Fatalf("status %d must be terminal after one call, got %d calls", status, len(calls))
		}
		row := f.store.rows[f.convID]
		if !row.terminal || row.delivered || len(row.defers) != 0 || row.lastStatus != status {
			t.Fatalf("status %d: terminal=%v delivered=%v defers=%v last=%d", status, row.terminal, row.delivered, row.defers, row.lastStatus)
		}
	}
}

func TestDatingSendSucceedsWhileDatingIsDown(t *testing.T) {
	down := httptest.NewServer(http.NotFoundHandler())
	downURL := down.URL
	down.Close() // connection refused from here on

	f := newDatingFixture(t, downURL, true)
	resp, err := f.svc.SendMessage(context.Background(), f.userA, f.convID, "text", "hi", nil, nil, "d4-down")
	if err != nil || resp == nil {
		t.Fatalf("send must succeed while dating-service is down: resp=%v err=%v", resp, err)
	}
	if f.store.intents["d4-down"].CompletedAt == nil {
		t.Fatal("the message delivery must complete without dating-service")
	}

	f.pass(t)
	row := f.store.rows[f.convID]
	if row == nil || row.delivered || row.terminal || len(row.defers) != 1 {
		t.Fatalf("an unreachable dating-service must defer, not drop: %+v", row)
	}

	// dating-service comes back: the queued obligation is delivered.
	dating := newFakeDating(t)
	f.svc.SetDatingService(dating.srv.URL)
	f.pass(t)
	if calls := dating.snapshot(); len(calls) != 1 || !row.delivered {
		t.Fatalf("recovered dating-service: calls=%d delivered=%v", len(calls), row.delivered)
	}
}

func TestDatingFirstMessageEnqueueFailureLeavesDeliveryPending(t *testing.T) {
	// A store that cannot record the obligation must not silently drop it:
	// the delivery stays pending for the repair worker.
	f := newDatingFixture(t, "http://unused", true)
	intent := &postgres.MessageDeliveryIntent{
		IdempotencyKey: "d4-pending", ConversationID: f.convID, SenderID: f.userA,
		MessageID: uuid.New(), Bucket: "202609", MessageTS: time.Now(),
		MessageType: "text", MessageText: "hi", MemberIDs: []uuid.UUID{f.userA, f.userB},
		SourceApp: "dating", MatchID: &f.matchID,
	}
	f.svc.convStore = enqueueFailingStore{f.store}
	if err := f.svc.completeMessageDelivery(context.Background(), intent); err == nil {
		t.Fatal("an enqueue failure must fail the delivery so repair retries it")
	}
}

type enqueueFailingStore struct{ *datingSendStore }

func (enqueueFailingStore) EnqueueDatingFirstMessage(context.Context, postgres.DatingFirstMessageNotification) (bool, error) {
	return false, errors.New("injected enqueue failure")
}

func TestDatingFirstMessageBackoffGrowsAndCaps(t *testing.T) {
	if got := datingFirstMessageBackoff(1); got != datingFirstMessageBackoffBase {
		t.Fatalf("attempt 1 backoff = %v", got)
	}
	if datingFirstMessageBackoff(3) <= datingFirstMessageBackoff(2) {
		t.Fatal("backoff must grow with attempts")
	}
	if got := datingFirstMessageBackoff(100); got != datingFirstMessageBackoffCap {
		t.Fatalf("backoff must cap at %v, got %v", datingFirstMessageBackoffCap, got)
	}
	for status, want := range map[int]datingCallOutcome{
		200: datingCallDelivered, 204: datingCallDelivered,
		400: datingCallTerminal, 404: datingCallTerminal, 409: datingCallTerminal, 410: datingCallTerminal,
		401: datingCallRetry, 403: datingCallRetry, 429: datingCallRetry, 500: datingCallRetry, 503: datingCallRetry,
	} {
		if got := classifyDatingFirstMessageStatus(status); got != want {
			t.Fatalf("status %d classified %v, want %v", status, got, want)
		}
	}
}
