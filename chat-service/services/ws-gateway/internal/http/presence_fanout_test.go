package http

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/chat-shared/presence"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type fakePeers struct{ peers []uuid.UUID }

func (f fakePeers) DMPeers(context.Context, uuid.UUID, int) ([]uuid.UUID, error) {
	return f.peers, nil
}

// fakeGraph answers per viewer; the target is always the announcing user.
type fakeGraph map[uuid.UUID]Visibility

func (g fakeGraph) Visibility(_ context.Context, viewer, _ uuid.UUID) Visibility { return g[viewer] }

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// collect subscribes to each peer's personal channel and returns the frames
// received within the window.
func collect(t *testing.T, rdb *redis.Client, peers []uuid.UUID, run func()) map[uuid.UUID][]map[string]any {
	t.Helper()
	ctx := context.Background()
	channels := make([]string, len(peers))
	for i, p := range peers {
		channels[i] = "chat:" + p.String()
	}
	ps := rdb.Subscribe(ctx, channels...)
	defer ps.Close()
	if _, err := ps.Receive(ctx); err != nil {
		t.Fatal(err)
	}
	run()
	out := map[uuid.UUID][]map[string]any{}
	ch := ps.Channel()
	timeout := time.After(300 * time.Millisecond)
	for {
		select {
		case msg := <-ch:
			id, _ := uuid.Parse(strings.TrimPrefix(msg.Channel, "chat:"))
			var frame map[string]any
			_ = json.Unmarshal([]byte(msg.Payload), &frame)
			out[id] = append(out[id], frame)
		case <-timeout:
			return out
		}
	}
}

func TestPresenceFanoutFiltersByPermission(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()

	user := uuid.New()
	full, onlineOnly, lastSeenOnly, denied := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	peers := []uuid.UUID{full, onlineOnly, lastSeenOnly, denied, user}
	graph := fakeGraph{
		full:         {Online: true, LastSeen: true},
		onlineOnly:   {Online: true},
		lastSeenOnly: {LastSeen: true},
		user:         {Online: true, LastSeen: true}, // only the self-skip keeps it out
	}
	store := presence.NewRedisStore(rdb, presence.Options{})
	if _, err := store.ConnectionOpened(ctx, user.String(), "c1", time.Now()); err != nil {
		t.Fatal(err)
	}
	f := newPresenceFanout(fakePeers{peers}, graph, rdb, quietLog())

	got := collect(t, rdb, peers, func() { f.Announce(ctx, user) })

	if len(got[full]) != 1 || len(got[onlineOnly]) != 1 {
		t.Fatalf("permitted peers must get one frame each: %v", got)
	}
	if len(got[denied]) != 0 {
		t.Fatal("peer without see_online_status received presence")
	}
	if len(got[lastSeenOnly]) != 0 {
		t.Fatal("last-seen permission alone must not earn a presence frame")
	}
	if len(got[user]) != 0 {
		t.Fatal("own presence echoed to self")
	}
	frame := got[full][0]
	payload, _ := frame["payload"].(map[string]any)
	if frame["type"] != "presence_update" || payload["user_id"] != user.String() || payload["is_online"] != true {
		t.Fatalf("bad frame %v", frame)
	}
	if payload["last_active_at"] == nil {
		t.Fatal("see_last_seen peer must get last_active_at")
	}
	op, _ := got[onlineOnly][0]["payload"].(map[string]any)
	if v, present := op["last_active_at"]; !present || v != nil {
		t.Fatalf("peer without see_last_seen must get last_active_at:null, got %v", op)
	}
}

// Announce re-reads state, so an offline announce publishes is_online:false.
func TestPresenceFanoutPublishesCurrentState(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	user, peer := uuid.New(), uuid.New()
	store := presence.NewRedisStore(rdb, presence.Options{})
	_, _ = store.ConnectionOpened(ctx, user.String(), "c1", time.Now())
	_, _ = store.ConnectionClosed(ctx, user.String(), "c1", time.Now())
	f := newPresenceFanout(fakePeers{[]uuid.UUID{peer}}, fakeGraph{peer: {Online: true, LastSeen: true}}, rdb, quietLog())
	got := collect(t, rdb, []uuid.UUID{peer}, func() { f.Announce(ctx, user) })
	if len(got[peer]) != 1 {
		t.Fatalf("expected one frame, got %v", got)
	}
	payload, _ := got[peer][0]["payload"].(map[string]any)
	if payload["is_online"] != false || got[peer][0]["online"] != false {
		t.Fatalf("offline announce must say offline: %v", got[peer][0])
	}
}

func TestGraphVisibilityBatchesActionsCachesAndFailsClosed(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	viewer, target := uuid.New(), uuid.New()
	var hits int32
	var fail atomic.Bool
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		atomic.AddInt32(&hits, 1)
		if fail.Load() {
			w.WriteHeader(nethttp.StatusInternalServerError)
			return
		}
		if r.Header.Get("X-User-Id") != viewer.String() || r.URL.Query().Get("target_user_id") != target.String() ||
			r.URL.Query().Get("actions") != "see_online_status,see_last_seen" || r.Header.Get("X-Internal-Service-Key") != "k" {
			w.WriteHeader(nethttp.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"decisions":{"see_online_status":{"allowed":true},"see_last_seen":{"allowed":false}}}}`))
	}))
	defer srv.Close()

	g := &GraphVisibility{BaseURL: srv.URL, InternalServiceKey: "k", Redis: rdb, Log: quietLog()}
	ctx := context.Background()
	if v := g.Visibility(ctx, viewer, target); !v.Online || v.LastSeen {
		t.Fatalf("decision = %+v", v)
	}
	if v := g.Visibility(ctx, viewer, target); !v.Online || v.LastSeen || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("second read must come from cache (hits %d, %+v)", hits, v)
	}
	if cached, _ := rdb.Get(ctx, onlineVisKey(viewer, target)).Result(); cached != "1" {
		t.Fatal("online decision must share message-service's onlinevis cache key")
	}

	mr.FlushAll()
	fail.Store(true)
	if v := g.Visibility(ctx, viewer, target); v.Online || v.LastSeen {
		t.Fatalf("graph failure must deny, got %+v", v)
	}
}
