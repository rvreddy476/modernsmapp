package http

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeWatcher is a LiveViewAuthorizer whose answer a test can turn.
type fakeWatcher struct {
	answer bool
	err    error
	calls  int
	asked  []string
}

func (f *fakeWatcher) MayWatch(_ context.Context, viewerID, streamID string) (bool, error) {
	f.calls++
	f.asked = append(f.asked, viewerID+"→"+streamID)
	return f.answer, f.err
}

// fakeRoomPubSub records Subscribe/Unsubscribe calls.
type fakeRoomPubSub struct {
	subscribed   []string
	unsubscribed []string
	subErr       error
}

func (f *fakeRoomPubSub) Subscribe(_ context.Context, channels ...string) error {
	if f.subErr != nil {
		return f.subErr
	}
	f.subscribed = append(f.subscribed, channels...)
	return nil
}

func (f *fakeRoomPubSub) Unsubscribe(_ context.Context, channels ...string) error {
	f.unsubscribed = append(f.unsubscribed, channels...)
	return nil
}

func liveServer(viewer LiveViewAuthorizer) *Server {
	return &Server{opts: ServerOptions{EnableLiveRooms: true, LiveViewer: viewer}}
}

func TestLiveRoomAdmitsOnlyTheAuthoritysYes(t *testing.T) {
	user, stream := uuid.New(), uuid.New()
	now := time.Now()

	yes := &fakeWatcher{answer: true}
	id, ok := liveServer(yes).mayJoinLiveRoom(context.Background(), newLiveRoomGrants(), user, stream.String(), now)
	if !ok || id != stream.String() {
		t.Fatalf("a viewer the authority admits was refused (ok=%v id=%s)", ok, id)
	}
	if yes.asked[0] != user.String()+"→"+stream.String() {
		t.Fatalf("asked %q, want the connection's own user and the parsed stream", yes.asked[0])
	}

	no := &fakeWatcher{answer: false}
	if id, ok := liveServer(no).mayJoinLiveRoom(context.Background(), newLiveRoomGrants(), user, stream.String(), now); ok || id != stream.String() {
		t.Fatalf("a viewer the authority refuses joined the room (ok=%v id=%q)", ok, id)
	}

	broken := &fakeWatcher{answer: true, err: errors.New("live-service down")}
	if _, ok := liveServer(broken).mayJoinLiveRoom(context.Background(), newLiveRoomGrants(), user, stream.String(), now); ok {
		t.Fatal("an undecided authority admitted a viewer — must fail closed")
	}
}

func TestLiveRoomRefusedWhenDisabledUnconfiguredOrMalformed(t *testing.T) {
	user := uuid.New()
	yes := &fakeWatcher{answer: true}
	off := &Server{opts: ServerOptions{EnableLiveRooms: false, LiveViewer: yes}}
	if _, ok := off.mayJoinLiveRoom(context.Background(), newLiveRoomGrants(), user, uuid.NewString(), time.Now()); ok {
		t.Fatal("live rooms joined while disabled")
	}
	noViewer := &Server{opts: ServerOptions{EnableLiveRooms: true}}
	if _, ok := noViewer.mayJoinLiveRoom(context.Background(), newLiveRoomGrants(), user, uuid.NewString(), time.Now()); ok {
		t.Fatal("live rooms joined with no authority configured")
	}
	on := liveServer(yes)
	for _, raw := range []string{"", "../chat:someone", "x*", uuid.NewString() + ":extra"} {
		if _, ok := on.mayJoinLiveRoom(context.Background(), newLiveRoomGrants(), user, raw, time.Now()); ok {
			t.Fatalf("non-uuid room name %q was accepted — the room name must come from a parsed id", raw)
		}
	}
	if yes.calls != 0 {
		t.Fatal("the authority was asked while disabled or about a malformed id")
	}
}

// A yes is reused for strictly less than 30 seconds, and only on the
// connection that earned it.
func TestLiveRoomAllowIsCachedAtMostThirtySeconds(t *testing.T) {
	if liveRoomGrantTTL > 30*time.Second || liveRoomRecheckInterval > 30*time.Second {
		t.Fatalf("live grant TTL %s / re-check %s exceed the 30s contract", liveRoomGrantTTL, liveRoomRecheckInterval)
	}
	user, stream := uuid.New(), uuid.NewString()
	viewer := &fakeWatcher{answer: true}
	s := liveServer(viewer)
	grants := newLiveRoomGrants()
	t0 := time.Now()

	s.mayJoinLiveRoom(context.Background(), grants, user, stream, t0)
	s.mayJoinLiveRoom(context.Background(), grants, user, stream, t0.Add(29*time.Second))
	if viewer.calls != 1 {
		t.Fatalf("a re-subscribe inside the window re-asked (%d calls)", viewer.calls)
	}
	s.mayJoinLiveRoom(context.Background(), grants, user, stream, t0.Add(30*time.Second))
	if viewer.calls != 2 {
		t.Fatalf("a yes was reused at 30s (%d calls)", viewer.calls)
	}
	// A new connection (a reconnect) has its own, empty grant set.
	s.mayJoinLiveRoom(context.Background(), newLiveRoomGrants(), user, stream, t0.Add(31*time.Second))
	if viewer.calls != 3 {
		t.Fatal("a grant leaked across connections — a reconnect must re-ask")
	}
}

func TestLiveSubscribeJoinsOnYesOnly(t *testing.T) {
	user, stream := uuid.New(), uuid.NewString()
	ps := &fakeRoomPubSub{}
	out := make(chan []byte, 4)
	if !liveServer(&fakeWatcher{answer: true}).handleLiveSubscribe(context.Background(), ps, out, newLiveRoomGrants(), user, stream, time.Now()) {
		t.Fatal("an allowed subscribe failed")
	}
	if len(ps.subscribed) != 1 || ps.subscribed[0] != "live:stream:"+stream {
		t.Fatalf("subscribed %v", ps.subscribed)
	}
	if len(out) != 0 {
		t.Fatalf("an allowed subscribe sent a frame: %s", <-out)
	}
}

func TestLiveSubscribeRefusalSendsErrorFrameAndNoSubscription(t *testing.T) {
	user, stream := uuid.New(), uuid.NewString()
	for name, viewer := range map[string]*fakeWatcher{
		"blocked by host": {answer: false},
		"upstream error":  {answer: true, err: errors.New("503")},
	} {
		t.Run(name, func(t *testing.T) {
			ps := &fakeRoomPubSub{}
			out := make(chan []byte, 4)
			if liveServer(viewer).handleLiveSubscribe(context.Background(), ps, out, newLiveRoomGrants(), user, stream, time.Now()) {
				t.Fatal("refused subscribe reported success")
			}
			if len(ps.subscribed) != 0 {
				t.Fatalf("a refused viewer was subscribed to %v", ps.subscribed)
			}
			assertRefusedFrame(t, out, stream)
		})
	}

	t.Run("malformed id", func(t *testing.T) {
		ps := &fakeRoomPubSub{}
		out := make(chan []byte, 4)
		liveServer(&fakeWatcher{answer: true}).handleLiveSubscribe(context.Background(), ps, out, newLiveRoomGrants(), user, "nope", time.Now())
		if len(ps.subscribed) != 0 || len(ps.unsubscribed) != 0 {
			t.Fatalf("a malformed id touched redis: sub=%v unsub=%v", ps.subscribed, ps.unsubscribed)
		}
		assertRefusedFrame(t, out, "")
	})
}

// A held room whose re-subscribe is later refused (the 30s yes ran out
// and the host has since blocked the viewer) must be LEFT, not kept.
func TestLiveSubscribeRefusalLeavesAHeldRoom(t *testing.T) {
	user, stream := uuid.New(), uuid.NewString()
	viewer := &fakeWatcher{answer: true}
	s := liveServer(viewer)
	grants := newLiveRoomGrants()
	ps := &fakeRoomPubSub{}
	out := make(chan []byte, 4)
	t0 := time.Now()
	s.handleLiveSubscribe(context.Background(), ps, out, grants, user, stream, t0)

	viewer.answer = false
	s.handleLiveSubscribe(context.Background(), ps, out, grants, user, stream, t0.Add(liveRoomGrantTTL))
	if len(ps.unsubscribed) != 1 || ps.unsubscribed[0] != "live:stream:"+stream {
		t.Fatalf("the refused viewer kept the earlier seat (unsubscribed %v)", ps.unsubscribed)
	}
	if grants.held(stream) {
		t.Fatal("the refused viewer kept the grant")
	}
	assertRefusedFrame(t, out, stream)
}

func TestLiveSubscribeRedisFailureIsNotAGrant(t *testing.T) {
	user, stream := uuid.New(), uuid.NewString()
	grants := newLiveRoomGrants()
	out := make(chan []byte, 4)
	liveServer(&fakeWatcher{answer: true}).handleLiveSubscribe(context.Background(), &fakeRoomPubSub{subErr: errors.New("redis")}, out, grants, user, stream, time.Now())
	if grants.held(stream) {
		t.Fatal("a failed redis subscribe left a grant the re-check would keep renewing")
	}
	assertRefusedFrame(t, out, stream)
}

func assertRefusedFrame(t *testing.T, out <-chan []byte, stream string) {
	t.Helper()
	select {
	case frame := <-out:
		var m map[string]any
		if err := json.Unmarshal(frame, &m); err != nil {
			t.Fatal(err)
		}
		if m["type"] != "error" || m["code"] != "LIVE_ROOM_REFUSED" {
			t.Fatalf("refusal frame %s", frame)
		}
		got, has := m["stream_id"]
		if stream == "" && has {
			t.Fatalf("refusal frame echoed an unparsed id: %s", frame)
		}
		if stream != "" && got != stream {
			t.Fatalf("refusal frame %s does not name stream %s", frame, stream)
		}
	default:
		t.Fatal("the client was not told its subscribe was refused")
	}
}

func TestLiveRoomRecheckEvictsOnNoAndOnErrorAndRenewsOnYes(t *testing.T) {
	user := uuid.New()
	keep, deny, broken := uuid.NewString(), uuid.NewString(), uuid.NewString()
	decisions := map[string]func() (bool, error){
		keep:   func() (bool, error) { return true, nil },
		deny:   func() (bool, error) { return false, nil },
		broken: func() (bool, error) { return true, errors.New("timeout") },
	}
	viewer := &scriptedWatcher{decide: func(stream string) (bool, error) { return decisions[stream]() }}
	s := liveServer(viewer)
	grants := newLiveRoomGrants()
	t0 := time.Now()
	for _, id := range []string{keep, deny, broken} {
		grants.grant(id, t0)
	}
	at := t0.Add(liveRoomRecheckInterval)
	evicted := s.recheckLiveRooms(context.Background(), grants, user, at, liveRoomRecheckBatch)
	got := map[string]bool{}
	for _, id := range evicted {
		got[id] = true
	}
	if len(evicted) != 2 || !got[deny] || !got[broken] {
		t.Fatalf("evicted %v; want the denied and the undecided room (fail closed, no blip tolerance)", evicted)
	}
	if grants.held(deny) || grants.held(broken) {
		t.Fatal("an evicted room kept its grant")
	}
	if !grants.fresh(keep, at.Add(liveRoomGrantTTL-time.Second)) {
		t.Fatal("a re-verified room was not renewed")
	}
}

func TestLiveRoomRecheckWithoutAuthorityEvicts(t *testing.T) {
	stream := uuid.NewString()
	for name, s := range map[string]*Server{
		"no authority": {opts: ServerOptions{EnableLiveRooms: true}},
		"disabled":     {opts: ServerOptions{EnableLiveRooms: false, LiveViewer: &fakeWatcher{answer: true}}},
	} {
		g := newLiveRoomGrants()
		g.grant(stream, time.Now())
		if evicted := s.recheckLiveRooms(context.Background(), g, uuid.New(), time.Now(), liveRoomRecheckBatch); len(evicted) != 1 {
			t.Fatalf("%s: a held room was kept with nothing to vouch for it", name)
		}
	}
}

func TestLiveRoomEvictionLeavesRoomAndTellsClient(t *testing.T) {
	stream := uuid.NewString()
	ps := &fakeUnsubscriber{}
	out := make(chan []byte, 2)
	(&Server{}).evictLiveRooms(context.Background(), ps, out, uuid.New(), []string{stream})
	if len(ps.channels) != 1 || ps.channels[0] != "live:stream:"+stream {
		t.Fatalf("eviction left %v", ps.channels)
	}
	select {
	case frame := <-out:
		var m map[string]any
		_ = json.Unmarshal(frame, &m)
		if m["type"] != "subscription_revoked" || m["stream_id"] != stream || len(m) != 2 {
			t.Fatalf("revocation frame %s", frame)
		}
	default:
		t.Fatal("the client was not told its live room was revoked")
	}

	// Never blocks the reconcile loop on a client that is not draining.
	done := make(chan struct{})
	go func() {
		(&Server{}).evictLiveRooms(context.Background(), &fakeUnsubscriber{}, make(chan []byte), uuid.New(), []string{stream})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("eviction blocked on a slow client")
	}
}

type scriptedWatcher struct {
	decide func(stream string) (bool, error)
	calls  int
}

func (w *scriptedWatcher) MayWatch(_ context.Context, _ string, stream string) (bool, error) {
	w.calls++
	return w.decide(stream)
}

func TestBetaGateAdmitsLiveRoomsOnlyWhenOwnerChecked(t *testing.T) {
	off := &Server{opts: ServerOptions{EnableScopedRooms: false, EnableLiveRooms: false}}
	for _, f := range []string{"subscribe_live_stream", "unsubscribe_live_stream"} {
		if !off.betaRoomGateRejects(f) {
			t.Errorf("%s passed the gate with live rooms disabled", f)
		}
	}
	on := &Server{opts: ServerOptions{EnableScopedRooms: false, EnableLiveRooms: true}}
	for _, f := range []string{"subscribe_live_stream", "unsubscribe_live_stream"} {
		if on.betaRoomGateRejects(f) {
			t.Errorf("%s gated although live rooms are owner-checked", f)
		}
	}
	// The live flag opens nothing else, and it does not need scoped rooms.
	for _, f := range []string{
		"subscribe_post", "unsubscribe_post", "subscribe_call", "unsubscribe_call",
		"subscribe_update", "subscribe_group_post", "group_post_typing",
		"call_join", "call_leave", "conversation.enter", "conversation.heartbeat", "typing.start",
	} {
		if !on.betaRoomGateRejects(f) {
			t.Errorf("%s slipped through the gate on the live-rooms flag", f)
		}
	}
}

func TestHTTPLiveViewAuthorizerAsksLiveServiceV2(t *testing.T) {
	var gotPath, gotQuery, gotKey, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery, gotKey = r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Internal-Service-Key")
		_, _ = w.Write([]byte(`{"data":{"allowed":true}}`))
	}))
	defer srv.Close()
	ok, err := NewHTTPLiveViewAuthorizer(srv.URL+"/", "k", srv.Client()).MayWatch(context.Background(), "viewer-1", "stream-1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if gotMethod != http.MethodGet || gotPath != "/v1/livestream/internal/streams/stream-1/viewer" ||
		gotQuery != "user_id=viewer-1" || gotKey != "k" {
		t.Fatalf("asked %s %s?%s key=%q", gotMethod, gotPath, gotQuery, gotKey)
	}
}

func TestHTTPLiveViewAuthorizerFailsClosed(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		wantOK bool
		err    bool
	}{
		"explicit no":                {200, `{"data":{"allowed":false}}`, false, false},
		"5xx even if body says yes":  {503, `{"data":{"allowed":true}}`, false, true},
		"401 even if body says yes":  {401, `{"data":{"allowed":true}}`, false, true},
		"404 route not deployed yet": {404, `{"error":"not found"}`, false, true},
		"empty object":               {200, `{}`, false, true},
		"data without allowed":       {200, `{"data":{}}`, false, true},
		"wrong shape (top-level)":    {200, `{"allowed":true}`, false, true},
		"not json":                   {200, `<html>ok</html>`, false, true},
		"allowed as string":          {200, `{"data":{"allowed":"true"}}`, false, true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			ok, err := NewHTTPLiveViewAuthorizer(srv.URL, "k", srv.Client()).MayWatch(context.Background(), "v", "s")
			if ok != c.wantOK || (err != nil) != c.err {
				t.Fatalf("ok=%v err=%v; want ok=%v err=%v", ok, err, c.wantOK, c.err)
			}
		})
	}

	t.Run("timeout", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			<-release
			_, _ = w.Write([]byte(`{"data":{"allowed":true}}`))
		}))
		defer srv.Close()
		defer close(release)
		client := srv.Client()
		client.Timeout = 50 * time.Millisecond
		if ok, err := NewHTTPLiveViewAuthorizer(srv.URL, "k", client).MayWatch(context.Background(), "v", "s"); ok || err == nil {
			t.Fatalf("a timed-out authority answered ok=%v err=%v", ok, err)
		}
	})

	t.Run("unconfigured", func(t *testing.T) {
		if ok, err := NewHTTPLiveViewAuthorizer("", "k", nil).MayWatch(context.Background(), "v", "s"); ok || err == nil {
			t.Fatal("an unconfigured authority must be undecided")
		}
	})
}

// The read loop's subscribe_live_stream case must go through
// handleLiveSubscribe (which asks the authority first) and must not call
// Redis Subscribe itself; and handleLiveSubscribe must decide before it
// subscribes.
func TestLiveSubscribeCaseIsOwnerCheckedInSource(t *testing.T) {
	server, err := parser.ParseFile(token.NewFileSet(), "server.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var readLoop *ast.FuncDecl
	for _, decl := range server.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "readLoop" {
			readLoop = fn
		}
	}
	if readLoop == nil {
		t.Fatal("readLoop not found in server.go")
	}
	found := false
	ast.Inspect(readLoop.Body, func(node ast.Node) bool {
		clause, ok := node.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, e := range clause.List {
			lit, ok := e.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if v, _ := strconv.Unquote(lit.Value); v != "subscribe_live_stream" {
				continue
			}
			found = true
			if containsRedisSubscribe(clause) {
				t.Error("subscribe_live_stream subscribes to Redis directly instead of through handleLiveSubscribe")
			}
			if !containsCallNamed(clause, "handleLiveSubscribe") {
				t.Error("subscribe_live_stream does not go through handleLiveSubscribe")
			}
		}
		return true
	})
	if !found {
		t.Fatal("no subscribe_live_stream case in the read loop")
	}

	rooms, err := parser.ParseFile(token.NewFileSet(), "liverooms.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range rooms.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "handleLiveSubscribe" {
			continue
		}
		decide, subscribe := token.NoPos, token.NoPos
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				switch sel.Sel.Name {
				case "mayJoinLiveRoom":
					if decide == token.NoPos {
						decide = call.Pos()
					}
				case "Subscribe":
					if subscribe == token.NoPos {
						subscribe = call.Pos()
					}
				}
			}
			return true
		})
		if decide == token.NoPos || subscribe == token.NoPos || decide > subscribe {
			t.Fatal("handleLiveSubscribe must call mayJoinLiveRoom before Redis Subscribe")
		}
		return
	}
	t.Fatal("handleLiveSubscribe not found")
}

func containsCallNamed(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}
