package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Tube channel subscriptions (2026-09-12): the order of operations behind
// the one button. Follow first through graph-service, row second; unfollow
// first, row second; nothing written when the graph refuses; a retried tap
// never re-emits; the owner cannot subscribe to themself.

// fakeChannelStore subscription half. Rows are keyed (channel, user);
// outbox records every event the real store would enqueue in the same
// transaction, so a test can assert "no second event" directly.
type fakeSubKey struct{ channel, user uuid.UUID }

type fakeSubState struct {
	mu     sync.Mutex
	rows   map[fakeSubKey]*postgres.ChannelSubscription
	outbox []string
	// subscribeErr makes Subscribe fail, to prove the graph write is not
	// rolled back by us (graph-service follow is idempotent; a retry heals).
	subscribeErr error
}

func (f *fakeChannelStore) subs() *fakeSubState {
	if f.subState == nil {
		f.subState = &fakeSubState{rows: map[fakeSubKey]*postgres.ChannelSubscription{}}
	}
	return f.subState
}

func (f *fakeChannelStore) channelByID(id uuid.UUID) *postgres.Channel {
	for _, ch := range f.byUser {
		if ch.ID == id {
			return ch
		}
	}
	return nil
}

func (f *fakeChannelStore) Subscribe(_ context.Context, channelID, userID uuid.UUID, notifyOn string) (bool, error) {
	st := f.subs()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.subscribeErr != nil {
		return false, st.subscribeErr
	}
	ch := f.channelByID(channelID)
	if ch == nil {
		return false, postgres.ErrChannelNotFound
	}
	key := fakeSubKey{channelID, userID}
	if _, ok := st.rows[key]; ok {
		return false, nil
	}
	st.rows[key] = &postgres.ChannelSubscription{ChannelID: channelID, UserID: userID, NotifyOn: notifyOn, SubscribedAt: time.Now()}
	ch.SubscriberCount++
	st.outbox = append(st.outbox, "tube.channel.subscribed")
	return true, nil
}

func (f *fakeChannelStore) Unsubscribe(_ context.Context, channelID, userID uuid.UUID) (bool, error) {
	st := f.subs()
	st.mu.Lock()
	defer st.mu.Unlock()
	ch := f.channelByID(channelID)
	if ch == nil {
		return false, postgres.ErrChannelNotFound
	}
	key := fakeSubKey{channelID, userID}
	if _, ok := st.rows[key]; !ok {
		return false, nil
	}
	delete(st.rows, key)
	ch.SubscriberCount--
	st.outbox = append(st.outbox, "tube.channel.unsubscribed")
	return true, nil
}

func (f *fakeChannelStore) SetNotifyOn(_ context.Context, channelID, userID uuid.UUID, notifyOn string) error {
	st := f.subs()
	st.mu.Lock()
	defer st.mu.Unlock()
	row, ok := st.rows[fakeSubKey{channelID, userID}]
	if !ok {
		return postgres.ErrNotSubscribed
	}
	row.NotifyOn = notifyOn
	return nil
}

func (f *fakeChannelStore) GetSubscription(_ context.Context, channelID, userID uuid.UUID) (*postgres.ChannelSubscription, error) {
	st := f.subs()
	st.mu.Lock()
	defer st.mu.Unlock()
	row, ok := st.rows[fakeSubKey{channelID, userID}]
	if !ok {
		return nil, nil
	}
	r := *row
	return &r, nil
}

func (f *fakeChannelStore) ListSubscriberIDsAfter(_ context.Context, channelID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	st := f.subs()
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []uuid.UUID
	for k, row := range st.rows {
		if k.channel == channelID && row.NotifyOn == postgres.NotifyOnAll && k.user.String() > after.String() {
			out = append(out, k.user)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeChannelStore) ListSubscribedOwnersAfter(_ context.Context, userID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	st := f.subs()
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []uuid.UUID
	for k := range st.rows {
		if k.user != userID {
			continue
		}
		if ch := f.channelByID(k.channel); ch != nil && ch.UserID.String() > after.String() {
			out = append(out, ch.UserID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeChannelStore) ListSubscriptionsForUser(_ context.Context, userID uuid.UUID, cursor *postgres.SubscriptionCursor, limit int) ([]postgres.SubscriptionRow, error) {
	st := f.subs()
	st.mu.Lock()
	defer st.mu.Unlock()
	var out []postgres.SubscriptionRow
	for k, row := range st.rows {
		if k.user != userID {
			continue
		}
		if cursor != nil {
			if row.SubscribedAt.After(cursor.SubscribedAt) || row.SubscribedAt.Equal(cursor.SubscribedAt) && k.channel.String() <= cursor.ChannelID.String() {
				continue
			}
		}
		ch := f.channelByID(k.channel)
		if ch == nil {
			continue
		}
		out = append(out, postgres.SubscriptionRow{Channel: *ch, NotifyOn: row.NotifyOn, SubscribedAt: row.SubscribedAt})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].SubscribedAt.Equal(out[j].SubscribedAt) {
			return out[i].SubscribedAt.After(out[j].SubscribedAt)
		}
		return out[i].Channel.ID.String() < out[j].Channel.ID.String()
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// graphCall is one request the fake graph-service saw.
type graphCall struct {
	Path        string
	UserID      string // X-User-Id
	WriteSource string // X-Graph-Write-Source
	InternalKey string
	TargetID    string // body user_id
}

// fakeGraph is an httptest graph-service: records every call, answers
// with the configured status / follow status.
type fakeGraph struct {
	mu           sync.Mutex
	calls        []graphCall
	statusCode   int
	followStatus string
}

func newFakeGraph(t *testing.T) (*fakeGraph, *httptest.Server) {
	t.Helper()
	g := &fakeGraph{statusCode: http.StatusOK, followStatus: "followed"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			UserID string `json:"user_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.mu.Lock()
		g.calls = append(g.calls, graphCall{
			Path:        r.URL.Path,
			UserID:      r.Header.Get("X-User-Id"),
			WriteSource: r.Header.Get("X-Graph-Write-Source"),
			InternalKey: r.Header.Get("X-Internal-Service-Key"),
			TargetID:    body.UserID,
		})
		code, status := g.statusCode, g.followStatus
		g.mu.Unlock()
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(code)
		if code != http.StatusOK {
			_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR"}}`))
			return
		}
		if r.URL.Path == "/v1/graph/unfollow" {
			status = "unfollowed"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"status": status}})
	}))
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *fakeGraph) snapshot() []graphCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]graphCall(nil), g.calls...)
}

// subscriptionFixture: an owner with a channel, a viewer, a service wired
// to the fake store and the httptest graph.
type subscriptionFixture struct {
	svc    *Service
	store  *fakeChannelStore
	graph  *fakeGraph
	owner  uuid.UUID
	viewer uuid.UUID
}

func newSubscriptionFixture(t *testing.T) *subscriptionFixture {
	t.Helper()
	store := newFakeChannelStore()
	graph, srv := newFakeGraph(t)
	svc := newChannelTestService(store)
	svc.graphServiceURL = srv.URL
	svc.internalServiceKey = "test-internal-key"
	owner, viewer := uuid.New(), uuid.New()
	if err := store.CreateChannel(context.Background(), &postgres.Channel{UserID: owner, Name: "Call B Studio", Handle: "call.b"}); err != nil {
		t.Fatal(err)
	}
	return &subscriptionFixture{svc: svc, store: store, graph: graph, owner: owner, viewer: viewer}
}

func TestSubscribeFollowsThroughGraphThenRecordsRow(t *testing.T) {
	f := newSubscriptionFixture(t)
	ctx := context.Background()

	res, err := f.svc.Subscribe(ctx, f.viewer, "@call.b", "")
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	if res.Status != "subscribed" || res.NotifyOn != "all" || res.Follow != "followed" || res.SubscriberCount != 1 {
		t.Fatalf("result %+v", res)
	}

	calls := f.graph.snapshot()
	if len(calls) != 1 {
		t.Fatalf("graph calls = %d, want 1: %+v", len(calls), calls)
	}
	call := calls[0]
	if call.Path != "/v1/graph/follow" {
		t.Errorf("path %q, want /v1/graph/follow", call.Path)
	}
	if call.UserID != f.viewer.String() {
		t.Errorf("X-User-Id %q, want the subscriber %s", call.UserID, f.viewer)
	}
	if call.TargetID != f.owner.String() {
		t.Errorf("body user_id %q, want the owner %s", call.TargetID, f.owner)
	}
	// SR-3: graph-service refuses an unattributed write. This header is
	// what makes post-service a named writer.
	if call.WriteSource != "post-service" {
		t.Errorf("X-Graph-Write-Source %q, want post-service", call.WriteSource)
	}
	if call.InternalKey != "test-internal-key" {
		t.Errorf("X-Internal-Service-Key %q not forwarded", call.InternalKey)
	}

	sub, _ := f.store.GetSubscription(ctx, f.store.byUser[f.owner].ID, f.viewer)
	if sub == nil || sub.NotifyOn != "all" {
		t.Fatalf("row not written or bell wrong: %+v", sub)
	}
}

func TestSubscribeGraphFailureWritesNothing(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.graph.statusCode = http.StatusInternalServerError

	_, err := f.svc.Subscribe(context.Background(), f.viewer, "call.b", "all")
	if !errors.Is(err, ErrGraphUnavailable) {
		t.Fatalf("err = %v, want ErrGraphUnavailable", err)
	}
	if n := len(f.store.subs().rows); n != 0 {
		t.Fatalf("%d subscription rows written after a graph failure, want 0", n)
	}
	if n := len(f.store.subs().outbox); n != 0 {
		t.Fatalf("%d outbox events after a graph failure, want 0", n)
	}
}

func TestSubscribeTwiceEmitsOnceAndKeepsTheBell(t *testing.T) {
	f := newSubscriptionFixture(t)
	ctx := context.Background()

	if _, err := f.svc.Subscribe(ctx, f.viewer, "call.b", "none"); err != nil {
		t.Fatal(err)
	}
	// The retry asks for 'all'; the existing bell ('none') must survive.
	res, err := f.svc.Subscribe(ctx, f.viewer, "call.b", "all")
	if err != nil {
		t.Fatal(err)
	}
	if res.NotifyOn != "none" {
		t.Fatalf("second subscribe reset the bell: notify_on = %q, want none", res.NotifyOn)
	}
	if res.SubscriberCount != 1 {
		t.Fatalf("subscriber_count = %d after a retried tap, want 1", res.SubscriberCount)
	}
	if got := f.store.subs().outbox; len(got) != 1 || got[0] != "tube.channel.subscribed" {
		t.Fatalf("outbox = %v, want exactly one tube.channel.subscribed", got)
	}
	// graph-service's follow is idempotent, so calling it again is fine and
	// is in fact what heals a row that exists without its edge.
	if n := len(f.graph.snapshot()); n != 2 {
		t.Fatalf("graph follow calls = %d, want 2", n)
	}
}

func TestUnsubscribeUnfollowsThenDeletes(t *testing.T) {
	f := newSubscriptionFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Subscribe(ctx, f.viewer, "call.b", ""); err != nil {
		t.Fatal(err)
	}

	res, err := f.svc.Unsubscribe(ctx, f.viewer, f.owner.String())
	if err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}
	if res.Status != "unsubscribed" || res.SubscriberCount != 0 {
		t.Fatalf("result %+v", res)
	}
	calls := f.graph.snapshot()
	if len(calls) != 2 || calls[1].Path != "/v1/graph/unfollow" || calls[1].UserID != f.viewer.String() || calls[1].TargetID != f.owner.String() {
		t.Fatalf("graph calls %+v, want follow then unfollow by the viewer against the owner", calls)
	}
	if n := len(f.store.subs().rows); n != 0 {
		t.Fatalf("%d rows remain after unsubscribe", n)
	}
	if got := f.store.subs().outbox; len(got) != 2 || got[1] != "tube.channel.unsubscribed" {
		t.Fatalf("outbox = %v, want subscribed then unsubscribed", got)
	}
}

func TestUnsubscribeGraphFailureKeepsTheRow(t *testing.T) {
	f := newSubscriptionFixture(t)
	ctx := context.Background()
	if _, err := f.svc.Subscribe(ctx, f.viewer, "call.b", ""); err != nil {
		t.Fatal(err)
	}
	f.graph.statusCode = http.StatusBadGateway
	if _, err := f.svc.Unsubscribe(ctx, f.viewer, "call.b"); !errors.Is(err, ErrGraphUnavailable) {
		t.Fatalf("err = %v, want ErrGraphUnavailable", err)
	}
	if n := len(f.store.subs().rows); n != 1 {
		t.Fatalf("row count %d after a failed unfollow, want 1 (the two halves must not disagree)", n)
	}
}

func TestSubscribeSelfIsRefusedBeforeGraph(t *testing.T) {
	f := newSubscriptionFixture(t)
	_, err := f.svc.Subscribe(context.Background(), f.owner, "call.b", "")
	if !errors.Is(err, ErrCannotSubscribeSelf) {
		t.Fatalf("err = %v, want ErrCannotSubscribeSelf", err)
	}
	if n := len(f.graph.snapshot()); n != 0 {
		t.Fatalf("graph called %d times for a self-subscribe, want 0", n)
	}
}

func TestSubscribeUnknownChannelIs404(t *testing.T) {
	f := newSubscriptionFixture(t)
	for _, ref := range []string{"nobody.here", uuid.New().String(), "!!"} {
		if _, err := f.svc.Subscribe(context.Background(), f.viewer, ref, ""); !errors.Is(err, ErrChannelNotFound) {
			t.Errorf("ref %q: err = %v, want ErrChannelNotFound", ref, err)
		}
	}
	if n := len(f.graph.snapshot()); n != 0 {
		t.Fatalf("graph called for an unknown channel")
	}
}

func TestSubscribeRequestedPropagates(t *testing.T) {
	f := newSubscriptionFixture(t)
	f.graph.followStatus = "requested"
	res, err := f.svc.Subscribe(context.Background(), f.viewer, "call.b", "")
	if err != nil {
		t.Fatal(err)
	}
	// A private owner: the follow is pending, the subscription is recorded
	// so the client can show the bell, and the caller learns which it was.
	if res.Follow != "requested" || res.Status != "subscribed" {
		t.Fatalf("result %+v, want follow=requested status=subscribed", res)
	}
}

func TestValidateNotifyOn(t *testing.T) {
	cases := map[string]struct {
		want string
		ok   bool
	}{
		"":           {"all", true},
		"all":        {"all", true},
		" NONE ":     {"none", true},
		"highlights": {"", false},
		"uploads":    {"", false},
		"yes":        {"", false},
	}
	for in, tc := range cases {
		got, err := ValidateNotifyOn(in)
		if tc.ok && (err != nil || got != tc.want) {
			t.Errorf("%q: got %q, %v; want %q", in, got, err, tc.want)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidNotifyOn) {
			t.Errorf("%q: got %q, %v; want ErrInvalidNotifyOn", in, got, err)
		}
	}
}

func TestSetNotifyOnRequiresASubscription(t *testing.T) {
	f := newSubscriptionFixture(t)
	ctx := context.Background()
	if _, err := f.svc.SetNotifyOn(ctx, f.viewer, "call.b", "none"); !errors.Is(err, ErrNotSubscribed) {
		t.Fatalf("err = %v, want ErrNotSubscribed", err)
	}
	if _, err := f.svc.SetNotifyOn(ctx, f.viewer, "call.b", "highlights"); !errors.Is(err, ErrInvalidNotifyOn) {
		t.Fatalf("err = %v, want ErrInvalidNotifyOn", err)
	}
	if _, err := f.svc.Subscribe(ctx, f.viewer, "call.b", ""); err != nil {
		t.Fatal(err)
	}
	view, err := f.svc.SetNotifyOn(ctx, f.viewer, "call.b", "none")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Subscribed || view.NotifyOn != "none" || view.SubscribedAt == nil {
		t.Fatalf("view %+v", view)
	}
	// The bell off drops the viewer from the upload fan-out but not from
	// the subscriptions feed.
	chID := f.store.byUser[f.owner].ID
	if ids, _ := f.svc.ListSubscriberIDsAfter(ctx, chID, uuid.Nil, 10); len(ids) != 0 {
		t.Fatalf("bell-off subscriber still in the push fan-out: %v", ids)
	}
	if owners, _ := f.svc.ListSubscribedOwnersAfter(ctx, f.viewer, uuid.Nil, 10); len(owners) != 1 || owners[0] != f.owner {
		t.Fatalf("owners = %v, want [%s]", owners, f.owner)
	}
}

func TestChannelViewCarriesViewerSubscription(t *testing.T) {
	f := newSubscriptionFixture(t)
	ctx := context.Background()

	anon, err := f.svc.GetChannelByRef(ctx, uuid.Nil, "call.b")
	if err != nil {
		t.Fatal(err)
	}
	if anon.IsSubscribed != nil || anon.NotifyOn != nil {
		t.Fatalf("anonymous view must omit is_subscribed / notify_on: %+v", anon)
	}
	before, _ := f.svc.GetChannelByRef(ctx, f.viewer, "call.b")
	if before.IsSubscribed == nil || *before.IsSubscribed || before.SubscriberCount != 0 {
		t.Fatalf("signed-in, not subscribed: %+v", before)
	}
	if _, err := f.svc.Subscribe(ctx, f.viewer, "call.b", "none"); err != nil {
		t.Fatal(err)
	}
	after, _ := f.svc.GetChannelByRef(ctx, f.viewer, "call.b")
	if after.IsSubscribed == nil || !*after.IsSubscribed || after.NotifyOn == nil || *after.NotifyOn != "none" || after.SubscriberCount != 1 {
		t.Fatalf("signed-in, subscribed: %+v", after)
	}
}

func TestListMySubscriptionsPagesNewestFirst(t *testing.T) {
	f := newSubscriptionFixture(t)
	ctx := context.Background()
	// Three channels, subscribed in order; the page must come back newest first.
	var owners []uuid.UUID
	for i, handle := range []string{"first.one", "second.one", "third.one"} {
		owner := uuid.New()
		owners = append(owners, owner)
		if err := f.store.CreateChannel(ctx, &postgres.Channel{UserID: owner, Name: "Channel " + handle, Handle: handle}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Subscribe(ctx, f.viewer, handle, ""); err != nil {
			t.Fatal(err)
		}
		// Distinct subscribed_at so the order is deterministic.
		f.store.subs().rows[fakeSubKey{f.store.byUser[owner].ID, f.viewer}].SubscribedAt = time.Unix(int64(1000+i), 0)
	}

	page1, next, err := f.svc.ListMySubscriptions(ctx, f.viewer, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || next == "" {
		t.Fatalf("page1 len %d next %q", len(page1), next)
	}
	if page1[0].Channel.Handle != "third.one" || page1[1].Channel.Handle != "second.one" {
		t.Fatalf("page1 order: %s, %s", page1[0].Channel.Handle, page1[1].Channel.Handle)
	}
	if page1[0].NotifyOn != "all" || page1[0].Channel.UserID != owners[2] || page1[0].Channel.SubscriberCount != 1 {
		t.Fatalf("row %+v", page1[0])
	}
	page2, next2, err := f.svc.ListMySubscriptions(ctx, f.viewer, next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || page2[0].Channel.Handle != "first.one" || next2 != "" {
		t.Fatalf("page2 %+v next %q", page2, next2)
	}
	if _, _, err := f.svc.ListMySubscriptions(ctx, f.viewer, "not-a-cursor!", 2); !errors.Is(err, ErrInvalidSubscriptionCursor) {
		t.Fatalf("bad cursor err = %v", err)
	}
}

// PostCreated for a video is stamped from the local channel row: id, name
// and handle, with no HTTP hop.
func TestStampChannelFillsIDNameHandle(t *testing.T) {
	f := newSubscriptionFixture(t)
	pc := f.svc.buildPostCreatedPayload(context.Background(),
		&postgres.Post{ID: uuid.New(), AuthorID: f.owner, ContentType: "long_video", Visibility: "public"}, nil, 0, 1)
	ch := f.store.byUser[f.owner]
	if pc.ChannelID != ch.ID.String() || pc.ChannelName != "Call B Studio" || pc.ChannelHandle != "call.b" {
		t.Fatalf("stamp = %q %q %q", pc.ChannelID, pc.ChannelName, pc.ChannelHandle)
	}
	none := f.svc.buildPostCreatedPayload(context.Background(),
		&postgres.Post{ID: uuid.New(), AuthorID: uuid.New(), ContentType: "long_video", Visibility: "public"}, nil, 0, 1)
	if none.ChannelID != "" || none.ChannelName != "" {
		t.Fatalf("author without a channel stamped: %+v", none)
	}
}
