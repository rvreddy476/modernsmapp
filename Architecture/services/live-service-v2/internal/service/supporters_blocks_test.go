package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// The top supporters list and the viewer's blocks (2 Oct 2026).

// supporterRig is a stream with named supporters: the earlier the name, the
// more hearts (so the unfiltered ranking is the order given).
func supporterRig(t *testing.T, names ...string) (*heartRig, *postgres.LiveStream, map[string]uuid.UUID) {
	t.Helper()
	host := uuid.New()
	r := newHeartRig(host)
	st := r.store.AddStream(host)
	u := map[string]uuid.UUID{"host": host}
	for i, n := range names {
		u[n] = uuid.New()
		left := 2 * (len(names) - i) // hearts are capped at 20 a batch
		for left > 0 {
			b := left
			if b > 20 {
				b = 20
			}
			if _, err := r.svc.SendHearts(ctx, st.ID, u[n], b); err != nil {
				t.Fatalf("%s: %v", n, err)
			}
			left -= b
		}
	}
	return r, st, u
}

func block(g *fakeGraph, viewer, other uuid.UUID) {
	g.blocked[viewer.String()+":"+other.String()] = true
}

// wantSupporters checks the rows are exactly these users, ranked 1..n.
func wantSupporters(t *testing.T, what string, rows []SupporterRow, u map[string]uuid.UUID, names ...string) {
	t.Helper()
	if len(rows) != len(names) {
		t.Fatalf("%s: %d rows, want %v", what, len(rows), names)
	}
	for i, n := range names {
		if rows[i].User == nil || rows[i].User.UserID != u[n] || rows[i].Rank != i+1 {
			t.Fatalf("%s: row %d = user %+v rank %d, want %s rank %d", what, i, rows[i].User, rows[i].Rank, n, i+1)
		}
	}
}

// graphServer is graph-service's relationships/batch: per target, `blocked`
// (the viewer blocked them) or `blocked_by` (they blocked the viewer).
type graphServer struct {
	mu        sync.Mutex
	blocked   map[uuid.UUID]bool
	blockedBy map[uuid.UUID]bool
	asked     [][]string // target_ids of each request
	viewers   []string
}

func (g *graphServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var body struct {
		ViewerID  string   `json:"viewer_id"`
		TargetIDs []string `json:"target_ids"`
	}
	_ = json.NewDecoder(req.Body).Decode(&body)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked = append(g.asked, body.TargetIDs)
	g.viewers = append(g.viewers, body.ViewerID)
	out := map[string]map[string]bool{}
	for _, id := range body.TargetIDs {
		uid, _ := uuid.Parse(id)
		out[id] = map[string]bool{"follows": false, "blocked": g.blocked[uid], "blocked_by": g.blockedBy[uid]}
	}
	_ = json.NewEncoder(w).Encode(out)
}

// TestSupportersDropBlocksInEitherDirection goes through the real graph
// client: a supporter the viewer blocked and a supporter who blocked the
// viewer are both gone, and the rest are ranked 1..n. Signed out, the list
// is whole and graph-service is not asked.
func TestSupportersDropBlocksInEitherDirection(t *testing.T) {
	r, st, u := supporterRig(t, "a", "iBlocked", "blockedMe", "d", "me")
	gs := &graphServer{blocked: map[uuid.UUID]bool{u["iBlocked"]: true}, blockedBy: map[uuid.UUID]bool{u["blockedMe"]: true}}
	srv := httptest.NewServer(gs)
	defer srv.Close()
	r.svc.graph = NewHTTPGraphClient(srv.URL, "k")

	rows, err := r.svc.ListSupporters(ctx, st.ID, u["me"], 10)
	if err != nil {
		t.Fatal(err)
	}
	wantSupporters(t, "signed in", rows, u, "a", "d", "me")
	if rows[0].Hearts != 10 || rows[1].Hearts != 4 || rows[2].Hearts != 2 {
		t.Fatalf("the rows kept their own numbers: %+v", rows)
	}
	// One batch for the supporters (the first request is the stream gate's
	// check of the host), about the others only, as the viewer.
	if len(gs.asked) != 2 || len(gs.asked[1]) != 4 {
		t.Fatalf("graph requests: %v", gs.asked)
	}
	for _, id := range gs.asked[1] {
		if id == u["me"].String() {
			t.Fatal("the viewer was looked up against themself")
		}
	}
	if gs.viewers[1] != u["me"].String() {
		t.Fatalf("looked up as %s", gs.viewers[1])
	}

	gs.asked = nil
	out, err := r.svc.ListSupporters(ctx, st.ID, uuid.Nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantSupporters(t, "signed out", out, u, "a", "iBlocked", "blockedMe", "d", "me")
	if len(gs.asked) != 0 {
		t.Fatalf("a signed-out read asked graph-service: %v", gs.asked)
	}
}

// TestSupportersBlocksWithoutBatch: a graph client with no batch call is
// asked per supporter, never more than the rows read; the host as viewer is
// filtered like anyone else.
func TestSupportersBlocksWithoutBatch(t *testing.T) {
	r, st, u := supporterRig(t, "a", "b", "c", "d")
	viewer := uuid.New()
	block(r.graph, viewer, u["a"])
	block(r.graph, viewer, u["c"])
	block(r.graph, u["host"], u["d"])

	r.graph.calls = 0
	rows, err := r.svc.ListSupporters(ctx, st.ID, viewer, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantSupporters(t, "viewer", rows, u, "b", "d")
	if r.graph.calls != 1+4 { // the stream gate, then one per supporter
		t.Fatalf("%d graph calls for 4 supporters", r.graph.calls)
	}
	hostRows, err := r.svc.ListSupporters(ctx, st.ID, u["host"], 10)
	if err != nil {
		t.Fatal(err)
	}
	wantSupporters(t, "host", hostRows, u, "a", "b", "c")
	other, _ := r.svc.ListSupporters(ctx, st.ID, uuid.New(), 10)
	wantSupporters(t, "someone with no blocks", other, u, "a", "b", "c", "d")
}

// failFor is a GraphClient that cannot answer for some users.
type failFor struct {
	*fakeGraph
	fail map[uuid.UUID]bool
}

func (g *failFor) Relationship(c context.Context, viewer, other uuid.UUID) (Relationship, error) {
	if g.fail[other] {
		return Relationship{}, errors.New("graph down")
	}
	return g.fakeGraph.Relationship(c, viewer, other)
}

// TestSupportersLookupFailureDropsTheRow: a supporter whose block state is
// unknown is not shown.
func TestSupportersLookupFailureDropsTheRow(t *testing.T) {
	r, st, u := supporterRig(t, "a", "b", "me", "c")

	// Per supporter: only the one that failed is dropped.
	r.svc.graph = &failFor{fakeGraph: r.graph, fail: map[uuid.UUID]bool{u["b"]: true}}
	rows, err := r.svc.ListSupporters(ctx, st.ID, u["me"], 10)
	if err != nil {
		t.Fatal(err)
	}
	wantSupporters(t, "one lookup failed", rows, u, "a", "me", "c")

	// Batch: an entry missing from the answer is unknown.
	batch := &batchFakeGraph{fakeGraph: r.graph, omit: map[uuid.UUID]bool{u["a"]: true}}
	r.svc.graph = batch
	rows, err = r.svc.ListSupporters(ctx, st.ID, u["me"], 10)
	if err != nil {
		t.Fatal(err)
	}
	wantSupporters(t, "an entry missing", rows, u, "b", "me", "c")

	// Batch failed: nobody else can be shown; the viewer's own row needs no
	// lookup. The read itself still succeeds.
	batch.omit, batch.batchErr = nil, errors.New("graph down")
	rows, err = r.svc.ListSupporters(ctx, st.ID, u["me"], 10)
	if err != nil {
		t.Fatal(err)
	}
	wantSupporters(t, "the batch failed", rows, u, "me")
	stranger, err := r.svc.ListSupporters(ctx, st.ID, uuid.New(), 10)
	if err != nil || stranger == nil || len(stranger) != 0 {
		t.Fatalf("the batch failed, another viewer: %#v %v", stranger, err)
	}
	// Signed out is untouched by graph-service being down.
	out, _ := r.svc.ListSupporters(ctx, st.ID, uuid.Nil, 10)
	wantSupporters(t, "signed out", out, u, "a", "b", "me", "c")
}

// TestSupportersExclusionsHoldForASignedInViewer: the host, a viewer banned
// from the stream and one under a platform live ban stay out, blocks or not.
func TestSupportersExclusionsHoldForASignedInViewer(t *testing.T) {
	r, st, u := supporterRig(t, "streamBanned", "liveBanned", "a", "blocked", "b")
	if _, err := r.svc.SendHearts(ctx, st.ID, u["host"], 20); err != nil {
		t.Fatal(err)
	}
	_ = r.store.BanFromStream(ctx, st.ID, u["streamBanned"], u["host"], "")
	_ = r.store.AdminSetPlatformBan(ctx, u["liveBanned"], true, "x", postgres.AuditEntry{ActorID: uuid.New()})
	viewer := uuid.New()
	block(r.graph, viewer, u["blocked"])

	for name, graph := range map[string]GraphClient{"per supporter": r.graph, "batch": &batchFakeGraph{fakeGraph: r.graph}} {
		r.svc.graph = graph
		rows, err := r.svc.ListSupporters(ctx, st.ID, viewer, 10)
		if err != nil {
			t.Fatal(err)
		}
		wantSupporters(t, name, rows, u, "a", "b")
	}
	out, _ := r.svc.ListSupporters(ctx, st.ID, uuid.Nil, 10)
	wantSupporters(t, "signed out", out, u, "a", "blocked", "b")
}

// TestSupportersPageStaysFullAndBounded: dropped rows are replaced by the
// next ranked ones, and one read never checks more than supportersMax users.
func TestSupportersPageStaysFullAndBounded(t *testing.T) {
	names := []string{"n01", "n02", "n03", "n04", "n05", "n06", "n07", "n08", "n09", "n10", "n11", "n12"}
	r, st, u := supporterRig(t, names...)
	viewer := uuid.New()
	block(r.graph, viewer, u["n01"])
	block(r.graph, viewer, u["n03"])
	batch := &batchFakeGraph{fakeGraph: r.graph}
	r.svc.graph = batch

	rows, err := r.svc.ListSupporters(ctx, st.ID, viewer, 5)
	if err != nil {
		t.Fatal(err)
	}
	wantSupporters(t, "limit 5 with two blocked above", rows, u, "n02", "n04", "n05", "n06", "n07")
	if batch.batchCalls != 1 {
		t.Fatalf("%d batch calls for one read", batch.batchCalls)
	}

	big := r.store.AddStream(u["host"])
	for i := 0; i < 80; i++ {
		_, _ = r.svc.SendHearts(ctx, big.ID, uuid.New(), 1)
	}
	counting := &countingBatch{batchFakeGraph: &batchFakeGraph{fakeGraph: r.graph}}
	r.svc.graph = counting
	for _, limit := range []int{10, 50, 500} {
		counting.most = 0
		got, err := r.svc.ListSupporters(ctx, big.ID, viewer, limit)
		want := limit
		if want > supportersMax {
			want = supportersMax
		}
		if err != nil || len(got) != want || counting.most > supportersMax {
			t.Fatalf("limit %d of 80: %d rows, %d users checked, err %v", limit, len(got), counting.most, err)
		}
	}
	r.svc.graph = r.graph // per supporter
	r.graph.calls = 0
	if _, err := r.svc.ListSupporters(ctx, big.ID, viewer, 500); err != nil || r.graph.calls > 1+supportersMax {
		t.Fatalf("per-supporter lookups: %d %v", r.graph.calls, err)
	}
}

type countingBatch struct {
	*batchFakeGraph
	most int
}

func (g *countingBatch) Relationships(c context.Context, viewer uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]Relationship, error) {
	if len(ids) > g.most {
		g.most = len(ids)
	}
	return g.batchFakeGraph.Relationships(c, viewer, ids)
}
