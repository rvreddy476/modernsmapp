package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Online status (24 Oct 2026). GetOnlineStatus read an `is_friend` key that
// /v1/graph/relationship never returns (it says is_connection), so every
// other user was reported offline; the batch route admitted only the
// requester's legacy connections, a list that can no longer grow. Both now
// ask graph-service's see_online_status permission, which admits follows and
// accepted chats under connections_only and applies the target's setting.

type fakePresence struct{ online map[string]bool }

func (f *fakePresence) SetOnline(_ context.Context, id string) error {
	f.online[id] = true
	return nil
}

func (f *fakePresence) IsOnline(_ context.Context, id string) (bool, error) {
	return f.online[id], nil
}

func (f *fakePresence) AreOnline(_ context.Context, ids []string) (map[string]bool, error) {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = f.online[id]
	}
	return out, nil
}

// fakeGraph answers the permission batch from allow[actor][target], and the
// legacy reads the old code used the way the real graph-service does: the
// relationship carries is_connection (never is_friend), and the connection
// list is empty because no new connection can form.
type fakeGraph struct {
	mu        sync.Mutex
	allow     map[string]map[string]bool
	down      bool
	calls     int
	maxBatch  int
	actors    []string
	keys      []string
	relations int
}

func (g *fakeGraph) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/permissions/check-batch", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.calls++
		if r.Method != http.MethodPost {
			t.Errorf("permission check used %s", r.Method)
		}
		var req struct {
			TargetUserIDs []string `json:"target_user_ids"`
			Actions       []string `json:"actions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode permission body: %v", err)
		}
		if len(req.Actions) != 1 || req.Actions[0] != "see_online_status" {
			t.Errorf("actions %v, want [see_online_status]", req.Actions)
		}
		if len(req.TargetUserIDs) > g.maxBatch {
			g.maxBatch = len(req.TargetUserIDs)
		}
		actor := r.Header.Get("X-User-Id")
		g.actors = append(g.actors, actor)
		g.keys = append(g.keys, r.Header.Get("X-Internal-Service-Key"))
		results := map[string]map[string]map[string]any{}
		for _, id := range req.TargetUserIDs {
			if len(results) >= 50 { // graph truncates past 50
				break
			}
			results[id] = map[string]map[string]any{"see_online_status": {"allowed": g.allow[actor][id]}}
		}
		// A failing graph that still writes an "allowed" body must not be
		// believed: only a 200 admits.
		if g.down {
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"results": results}})
	})
	mux.HandleFunc("/v1/graph/relationship", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.relations++
		g.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"is_connection": true, "followed_by": true}})
	})
	mux.HandleFunc("/v1/graph/connections/", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []string{}})
	})
	return mux
}

func onlineRouter(t *testing.T, g *fakeGraph, p *fakePresence) *gin.Engine {
	t.Helper()
	srv := httptest.NewServer(g.handler(t))
	t.Cleanup(srv.Close)
	gin.SetMode(gin.TestMode)
	h := &Handler{graphURL: srv.URL, graphClient: srv.Client(), presenceStore: p, internalRouteKey: "k-internal"}
	r := gin.New()
	r.GET("/v1/users/:userId/online", h.GetOnlineStatus)
	r.POST("/v1/users/online/batch", h.GetOnlineStatusBatch)
	return r
}

func getOnline(t *testing.T, r *gin.Engine, requester, target uuid.UUID) bool {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/users/"+target.String()+"/online", nil)
	req.Header.Set("X-User-Id", requester.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	var env struct {
		Data struct {
			Online bool `json:"online"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Data.Online
}

func batchOnline(t *testing.T, r *gin.Engine, requester uuid.UUID, ids []string) map[string]bool {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"user_ids": ids})
	req := httptest.NewRequest(http.MethodPost, "/v1/users/online/batch", bytes.NewReader(raw))
	req.Header.Set("X-User-Id", requester.String())
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	var env struct {
		Data struct {
			Online map[string]bool `json:"online"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Data.Online
}

func TestOnlineStatusFollowsGraphPresencePermission(t *testing.T) {
	viewer, admits, refuses := uuid.New(), uuid.New(), uuid.New()
	g := &fakeGraph{allow: map[string]map[string]bool{viewer.String(): {admits.String(): true}}}
	p := &fakePresence{online: map[string]bool{admits.String(): true, refuses.String(): true, viewer.String(): true}}
	r := onlineRouter(t, g, p)

	// A target whose permission admits the viewer (e.g. they follow the
	// viewer, or the pair has an accepted chat) shows its real status.
	if !getOnline(t, r, viewer, admits) {
		t.Fatal("an admitted target reads offline (the is_friend bug)")
	}
	if getOnline(t, r, viewer, refuses) {
		t.Fatal("a refusing target leaked its online status")
	}
	// Self is truthful without asking graph.
	before := g.calls
	if !getOnline(t, r, viewer, viewer) {
		t.Fatal("self reads offline")
	}
	if g.calls != before {
		t.Fatal("a self-check asked graph-service")
	}
	// The permission is asked for exactly the requester, with the key.
	for i, a := range g.actors {
		if a != viewer.String() || g.keys[i] != "k-internal" {
			t.Fatalf("graph call %d: actor %q key %q", i, a, g.keys[i])
		}
	}
	// An offline admitted target is offline.
	p.online[admits.String()] = false
	if getOnline(t, r, viewer, admits) {
		t.Fatal("an offline target reads online")
	}
}

func TestOnlineStatusFailsClosed(t *testing.T) {
	viewer, target := uuid.New(), uuid.New()
	g := &fakeGraph{down: true, allow: map[string]map[string]bool{viewer.String(): {target.String(): true}}}
	p := &fakePresence{online: map[string]bool{target.String(): true}}
	r := onlineRouter(t, g, p)
	if getOnline(t, r, viewer, target) {
		t.Fatal("graph down: status leaked on the single route")
	}
	if got := batchOnline(t, r, viewer, []string{target.String()}); got[target.String()] {
		t.Fatal("graph down: status leaked on the batch route")
	}
}

func TestOnlineStatusBatchUsesThePermissionInChunks(t *testing.T) {
	viewer := uuid.New()
	g := &fakeGraph{allow: map[string]map[string]bool{viewer.String(): {}}}
	p := &fakePresence{online: map[string]bool{viewer.String(): true}}
	var ids []string
	for i := 0; i < 70; i++ {
		id := uuid.New().String()
		ids = append(ids, id)
		p.online[id] = true
		if i%2 == 0 {
			g.allow[viewer.String()][id] = true
		}
	}
	ids = append(ids, viewer.String())
	r := onlineRouter(t, g, p)

	got := batchOnline(t, r, viewer, ids)
	for i, id := range ids[:70] {
		if want := i%2 == 0; got[id] != want {
			t.Fatalf("target %d online=%v, want %v", i, got[id], want)
		}
	}
	if !got[viewer.String()] {
		t.Fatal("self reads offline in the batch")
	}
	if g.maxBatch > 50 {
		t.Fatalf("sent %d targets in one call; graph truncates past 50", g.maxBatch)
	}
}
