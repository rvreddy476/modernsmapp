package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// presenceGraph is a graph-service stand-in that records every permission
// call. allow decides see_online_status per target; a target in omit gets no
// entry in the response at all.
type presenceGraph struct {
	mu         sync.Mutex
	allow      map[uuid.UUID]bool
	omit       map[uuid.UUID]bool
	status     int
	delay      time.Duration
	batchCalls [][]string
	singleHits int
	actors     []string
	keys       []string
}

func (g *presenceGraph) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/permissions/check", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.singleHits++
		g.mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/v1/permissions/check-batch", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			TargetUserIDs []string `json:"target_user_ids"`
			Actions       []string `json:"actions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.batchCalls = append(g.batchCalls, req.TargetUserIDs)
		g.actors = append(g.actors, r.Header.Get("X-User-Id"))
		g.keys = append(g.keys, r.Header.Get("X-Internal-Service-Key"))
		status, delay := g.status, g.delay
		g.mu.Unlock()
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		if len(req.TargetUserIDs) > graphPermissionBatchMax {
			// Real graph silently truncates; a stub that answered more would
			// hide a missing chunking step.
			req.TargetUserIDs = req.TargetUserIDs[:graphPermissionBatchMax]
		}
		results := map[string]any{}
		g.mu.Lock()
		for _, raw := range req.TargetUserIDs {
			id, err := uuid.Parse(raw)
			if err != nil || g.omit[id] {
				continue
			}
			results[raw] = map[string]any{
				"see_online_status": map[string]any{"allowed": g.allow[id]},
			}
		}
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"results": results}})
	})
	return mux
}

func (g *presenceGraph) calls() (batches [][]string, singles int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]string(nil), g.batchCalls...), g.singleHits
}

type staticHidden map[uuid.UUID]bool

func (h staticHidden) HiddenUsers(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		if h[id] {
			out[id] = true
		}
	}
	return out, nil
}

func newPresenceService(t *testing.T, g *presenceGraph) (*Service, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	srv := httptest.NewServer(g.handler())
	t.Cleanup(srv.Close)
	return &Service{
		log:                slog.Default(),
		rdb:                rdb,
		httpClient:         srv.Client(),
		graphServiceURL:    srv.URL,
		internalServiceKey: "test-internal-key",
	}, mr
}

func setOnline(t *testing.T, mr *miniredis.Miniredis, ids ...uuid.UUID) {
	t.Helper()
	for _, id := range ids {
		if err := mr.Set("presence:"+id.String(), "1"); err != nil {
			t.Fatal(err)
		}
	}
}

// L10: the presence endpoint resolves every online target's disclosure in
// ONE batched graph call, and applies each target's decision exactly.
func TestGetPresenceBatchesDisclosureAndKeepsPrivacy(t *testing.T) {
	requester := uuid.New()
	allowed, denied, omitted, offline, hidden := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	g := &presenceGraph{
		allow: map[uuid.UUID]bool{allowed: true, denied: false, offline: true, hidden: true},
		omit:  map[uuid.UUID]bool{omitted: true},
	}
	svc, mr := newPresenceService(t, g)
	svc.hiddenUsers = staticHidden{hidden: true}
	setOnline(t, mr, requester, allowed, denied, omitted, hidden)

	got, err := svc.GetPresence(context.Background(), requester,
		[]uuid.UUID{requester, allowed, denied, omitted, offline, hidden})
	if err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]bool{requester: true, allowed: true, denied: false, omitted: false, offline: false, hidden: false}
	for id, w := range want {
		if got[id.String()] != w {
			t.Errorf("presence[%s] = %v, want %v", id, got[id.String()], w)
		}
	}

	batches, singles := g.calls()
	if singles != 0 {
		t.Fatalf("per-target /check calls = %d, want 0", singles)
	}
	if len(batches) != 1 {
		t.Fatalf("graph batch calls = %d, want exactly 1", len(batches))
	}
	asked := map[string]bool{}
	for _, id := range batches[0] {
		asked[id] = true
	}
	// Only online, unhidden, non-self targets need a decision.
	for _, id := range []uuid.UUID{allowed, denied, omitted} {
		if !asked[id.String()] {
			t.Errorf("online target %s was not asked about", id)
		}
	}
	for _, id := range []uuid.UUID{requester, offline, hidden} {
		if asked[id.String()] {
			t.Errorf("target %s must not be sent to graph", id)
		}
	}
	if g.actors[0] != requester.String() || g.keys[0] != "test-internal-key" {
		t.Fatalf("batch call must carry the requester as X-User-Id and the internal key, got actor=%q key-set=%v",
			g.actors[0], g.keys[0] != "")
	}
}

// 50 online targets (the endpoint's cap) cost one graph call, not 50.
func TestGetPresenceFiftyTargetsOneGraphCall(t *testing.T) {
	requester := uuid.New()
	g := &presenceGraph{allow: map[uuid.UUID]bool{}}
	svc, mr := newPresenceService(t, g)
	ids := make([]uuid.UUID, 60)
	for i := range ids {
		ids[i] = uuid.New()
		g.allow[ids[i]] = i%2 == 0
	}
	setOnline(t, mr, ids...)

	got, err := svc.GetPresence(context.Background(), requester, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 50 {
		t.Fatalf("presence entries = %d, want 50 (endpoint cap)", len(got))
	}
	for i, id := range ids[:50] {
		if got[id.String()] != (i%2 == 0) {
			t.Fatalf("presence[%d] = %v, want %v", i, got[id.String()], i%2 == 0)
		}
	}
	batches, _ := g.calls()
	if len(batches) != 1 || len(batches[0]) != 50 {
		t.Fatalf("want one batch of 50, got %d batches", len(batches))
	}
}

// Rosters can be longer than graph's 50-target cap: they are chunked, and a
// target past the first chunk is still decided (not silently dropped).
func TestRosterFilterChunksPastGraphCap(t *testing.T) {
	requester := uuid.New()
	g := &presenceGraph{allow: map[uuid.UUID]bool{}}
	svc, _ := newPresenceService(t, g)
	raw := []string{requester.String(), "not-a-uuid"}
	var wantKept []string
	wantKept = append(wantKept, requester.String())
	for i := 0; i < 120; i++ {
		id := uuid.New()
		g.allow[id] = i%3 == 0
		raw = append(raw, id.String())
		if i%3 == 0 {
			wantKept = append(wantKept, id.String())
		}
	}
	got := svc.filterPresenceDisclosure(context.Background(), requester, raw)
	if len(got) != len(wantKept) {
		t.Fatalf("kept %d, want %d", len(got), len(wantKept))
	}
	for i := range got {
		if got[i] != wantKept[i] {
			t.Fatalf("kept[%d] = %s, want %s (order preserved)", i, got[i], wantKept[i])
		}
	}
	batches, singles := g.calls()
	if singles != 0 || len(batches) != 3 {
		t.Fatalf("want 3 batch calls (50+50+20) and no single calls, got %d batches, %d singles", len(batches), singles)
	}
	for _, b := range batches {
		if len(b) > graphPermissionBatchMax {
			t.Fatalf("batch of %d exceeds graph cap %d", len(b), graphPermissionBatchMax)
		}
	}
}

// Decisions are cached per pair (30 s) and reused: a cached "0" keeps the
// target hidden without asking graph, a cached "1" shows it.
func TestDisclosureCacheReusedAndRespected(t *testing.T) {
	requester := uuid.New()
	a, b := uuid.New(), uuid.New()
	g := &presenceGraph{allow: map[uuid.UUID]bool{a: true, b: false}}
	svc, mr := newPresenceService(t, g)
	setOnline(t, mr, a, b)

	first, _ := svc.GetPresence(context.Background(), requester, []uuid.UUID{a, b})
	if !first[a.String()] || first[b.String()] {
		t.Fatalf("first lookup = %v", first)
	}
	if ttl := mr.TTL(onlineVisibilityCacheKey(requester, a)); ttl <= 0 || ttl > onlineVisibilityCacheTTL {
		t.Fatalf("cache ttl = %v, want (0, %v]", ttl, onlineVisibilityCacheTTL)
	}
	// Graph now flips both; the cache must win until it expires.
	g.mu.Lock()
	g.allow[a], g.allow[b] = false, true
	g.mu.Unlock()
	second, _ := svc.GetPresence(context.Background(), requester, []uuid.UUID{a, b})
	if !second[a.String()] || second[b.String()] {
		t.Fatalf("cached lookup = %v, want unchanged", second)
	}
	if batches, _ := g.calls(); len(batches) != 1 {
		t.Fatalf("graph calls = %d, want 1 (second lookup served from cache)", len(batches))
	}
	mr.FastForward(onlineVisibilityCacheTTL + time.Second)
	setOnline(t, mr, a, b)
	third, _ := svc.GetPresence(context.Background(), requester, []uuid.UUID{a, b})
	if third[a.String()] || !third[b.String()] {
		t.Fatalf("after expiry = %v, want graph's new decisions", third)
	}
}

// A failing graph hides everyone (fail closed) and is not cached as a denial.
func TestDisclosureFailsClosedAndDoesNotCacheFailures(t *testing.T) {
	requester := uuid.New()
	a := uuid.New()
	g := &presenceGraph{allow: map[uuid.UUID]bool{a: true}, status: http.StatusInternalServerError}
	svc, mr := newPresenceService(t, g)
	setOnline(t, mr, a)

	got, _ := svc.GetPresence(context.Background(), requester, []uuid.UUID{a})
	if got[a.String()] {
		t.Fatal("graph failure must report the target offline")
	}
	if mr.Exists(onlineVisibilityCacheKey(requester, a)) {
		t.Fatal("a failed lookup must not be cached")
	}
	g.mu.Lock()
	g.status = http.StatusOK
	g.mu.Unlock()
	got, _ = svc.GetPresence(context.Background(), requester, []uuid.UUID{a})
	if !got[a.String()] {
		t.Fatal("once graph recovers the allowed target must be online")
	}
}

// A slow graph costs at most the budget, and everything it did not answer is
// offline.
func TestDisclosureBudgetBoundsSlowGraph(t *testing.T) {
	prev := onlineVisibilityBudget
	onlineVisibilityBudget = 100 * time.Millisecond
	t.Cleanup(func() { onlineVisibilityBudget = prev })

	requester := uuid.New()
	a := uuid.New()
	g := &presenceGraph{allow: map[uuid.UUID]bool{a: true}, delay: 3 * time.Second}
	svc, mr := newPresenceService(t, g)
	setOnline(t, mr, a)

	start := time.Now()
	got, err := svc.GetPresence(context.Background(), requester, []uuid.UUID{a})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if got[a.String()] {
		t.Fatal("a target graph did not answer in time must be offline")
	}
	if elapsed > time.Second {
		t.Fatalf("lookup took %v, want about the %v budget", elapsed, onlineVisibilityBudget)
	}
}

// Without a graph URL nothing is disclosed (unchanged fail-closed default).
func TestDisclosureWithoutGraphHidesAll(t *testing.T) {
	requester, a := uuid.New(), uuid.New()
	svc := &Service{log: slog.Default()}
	got := svc.discloseOnlineToMany(context.Background(), requester, []uuid.UUID{a, a})
	if len(got) != 1 || got[a] {
		t.Fatalf("got %v, want a single hidden entry", got)
	}
}
