package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/atpost/user-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// About-section audience (24 Oct 2026). resolveViewerAccess read an
// `is_friend` key that /v1/graph/relationship never returns, so "friends"
// items were hidden from everyone, and it filled IsFollower from followed_by
// (the OWNER following the viewer) instead of follows (the viewer following
// the owner). The fake graph below answers with the real wire keys only.

type aboutGraph struct {
	mu     sync.Mutex
	rel    map[string]any
	status int
	calls  int
	query  []string
	actors []string
	keys   []string
}

func (g *aboutGraph) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/graph/relationship", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.calls++
		g.query = append(g.query, r.URL.RawQuery)
		g.actors = append(g.actors, r.Header.Get("X-User-Id"))
		g.keys = append(g.keys, r.Header.Get("X-Internal-Service-Key"))
		if g.status != 0 {
			w.WriteHeader(g.status)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": g.rel})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func resolveAbout(t *testing.T, g *aboutGraph, viewer, owner uuid.UUID) service.ViewerAccess {
	t.Helper()
	srv := g.server(t)
	gin.SetMode(gin.TestMode)
	h := &Handler{graphURL: srv.URL, graphClient: srv.Client(), internalRouteKey: "k-internal"}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/users/"+owner.String()+"/about", nil)
	c.Request.Header.Set("X-User-Id", viewer.String())
	return h.resolveViewerAccess(c, owner)
}

func TestAboutFollowerIsTheViewerFollowingTheOwner(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()

	got := resolveAbout(t, &aboutGraph{rel: map[string]any{"follows": true}}, viewer, owner)
	if !got.IsFollower {
		t.Fatal("a viewer who follows the owner is not in the followers audience")
	}
	if got.IsFriend {
		t.Fatal("a one-way follow by the viewer counted as friends")
	}

	got = resolveAbout(t, &aboutGraph{rel: map[string]any{"followed_by": true}}, viewer, owner)
	if got.IsFollower || got.IsFriend {
		t.Fatalf("the owner following the viewer admitted them to followers/friends: %+v", got)
	}
}

func TestAboutFriendsIsMutualFollowOrLegacyConnection(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()

	got := resolveAbout(t, &aboutGraph{rel: map[string]any{"follows": true, "followed_by": true}}, viewer, owner)
	if !got.IsFriend || !got.IsFollower {
		t.Fatalf("a mutual follow is not friends (the is_friend bug): %+v", got)
	}

	got = resolveAbout(t, &aboutGraph{rel: map[string]any{"is_connection": true}}, viewer, owner)
	if !got.IsFriend {
		t.Fatal("a legacy connection lost the friends audience")
	}
}

func TestAboutBlockEitherWayAdmitsPublicOnly(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()
	for _, key := range []string{"blocked", "blocked_by", "viewer_blocked_target", "target_blocked_viewer"} {
		rel := map[string]any{"follows": true, "followed_by": true, "is_connection": true, key: true}
		if got := resolveAbout(t, &aboutGraph{rel: rel}, viewer, owner); got != (service.ViewerAccess{}) {
			t.Fatalf("%s: a blocked pair kept About access %+v", key, got)
		}
	}
}

func TestAboutAccessFailsClosedOnGraphError(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()
	g := &aboutGraph{status: http.StatusInternalServerError, rel: map[string]any{"follows": true, "followed_by": true, "is_connection": true}}
	if got := resolveAbout(t, g, viewer, owner); got != (service.ViewerAccess{}) {
		t.Fatalf("a non-200 graph answer was believed: %+v", got)
	}
}

func TestAboutAccessAsksGraphForTheViewerWithTheKey(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()
	g := &aboutGraph{rel: map[string]any{}}
	resolveAbout(t, g, viewer, owner)
	if g.calls != 1 {
		t.Fatalf("graph calls = %d, want 1", g.calls)
	}
	if want := "user_id=" + viewer.String() + "&other_id=" + owner.String(); g.query[0] != want {
		t.Fatalf("query %q, want %q (viewer first, owner second)", g.query[0], want)
	}
	if g.actors[0] != viewer.String() || g.keys[0] != "k-internal" {
		t.Fatalf("actor %q key %q", g.actors[0], g.keys[0])
	}
}

func TestAboutOwnerSeesEverythingWithoutAskingGraph(t *testing.T) {
	owner := uuid.New()
	g := &aboutGraph{rel: map[string]any{"blocked": true}}
	got := resolveAbout(t, g, owner, owner)
	if !got.IsSelf {
		t.Fatal("the owner is not IsSelf")
	}
	if g.calls != 0 {
		t.Fatal("an owner read asked graph-service")
	}
}
