package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Security walk 10 Nov 2026: a block in either direction makes another
// account's profile read exactly like a missing one, by id, by handle and on
// the sub-resource reads behind requireServableUser.

type pairKey struct{ a, b uuid.UUID }

// fakeBlocks models the blocks table: blocker -> blocked rows.
type fakeBlocks struct {
	mu    sync.Mutex
	rows  map[pairKey]bool
	err   error
	calls []pairKey
}

func (f *fakeBlocks) BlockedEitherWay(_ context.Context, viewer, owner uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, pairKey{viewer, owner})
	if f.err != nil {
		return false, f.err
	}
	return f.rows[pairKey{viewer, owner}] || f.rows[pairKey{owner, viewer}], nil
}

func blockRouter(b blockChecker) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{users: newPrivacyFixture(), pageAdmins: map[string]bool{}}
	h.WithInternalRoutes(pvInternalKey)
	h.WithBlockChecker(b)
	r := gin.New()
	h.RegisterRoutes(r)
	// A terminal handler stands in for a guarded sub-resource (links needs
	// Postgres); it sits behind the same guard as the real routes.
	r.GET("/test/users/:userId/links", h.requireServableUser, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r
}

// What the gateway forwards for a signed-in viewer.
func signedIn(viewer uuid.UUID) map[string]string {
	return map[string]string{"X-Internal-Service-Key": pvInternalKey, "X-User-Id": viewer.String()}
}

func serve(r *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

var blockDirections = []struct {
	name          string
	blocker, blkd uuid.UUID
}{
	{"owner blocked viewer", pvOwnerID, pvStrangerID},
	{"viewer blocked owner", pvStrangerID, pvOwnerID},
}

func TestBlockedProfileReadsExactlyLikeAMissingAccount(t *testing.T) {
	for _, dir := range blockDirections {
		b := &fakeBlocks{rows: map[pairKey]bool{{dir.blocker, dir.blkd}: true}}
		r := blockRouter(b)
		// A truly missing account, asked by the same viewer.
		missing := serve(r, "/v1/users/"+uuid.NewString(), signedIn(pvStrangerID))
		if missing.Code != http.StatusNotFound {
			t.Fatalf("missing user: status %d, want 404", missing.Code)
		}
		missingByHandle := serve(r, "/v1/users/by-username/nobody_here", signedIn(pvStrangerID))
		if missingByHandle.Body.String() != missing.Body.String() {
			t.Fatalf("missing by id and by handle differ: %s vs %s", missing.Body, missingByHandle.Body)
		}
		for route, path := range userObjectRoutes {
			t.Run(dir.name+"/"+route, func(t *testing.T) {
				w := serve(r, path, signedIn(pvStrangerID))
				if w.Code != http.StatusNotFound {
					t.Fatalf("a blocked pair was served the profile: status %d, want 404", w.Code)
				}
				if w.Body.String() != missing.Body.String() {
					t.Fatalf("the blocked 404 differs from a missing account's, so the block is detectable:\n got %s\nwant %s",
						w.Body.String(), missing.Body.String())
				}
				if strings.Contains(w.Body.String(), pvUsername) || strings.Contains(w.Body.String(), "Owner") {
					t.Fatalf("the 404 carries profile data: %s", w.Body.String())
				}
			})
		}
		t.Run(dir.name+"/sub-resource", func(t *testing.T) {
			if w := serve(r, "/test/users/"+pvOwnerID.String()+"/links", signedIn(pvStrangerID)); w.Code != http.StatusNotFound ||
				w.Body.String() != missing.Body.String() {
				t.Fatalf("guarded sub-resource across a block: status %d body %s, want the missing-account 404", w.Code, w.Body)
			}
		})
	}
}

func TestBlockGateAsksForTheViewerAndOwner(t *testing.T) {
	b := &fakeBlocks{rows: map[pairKey]bool{}}
	r := blockRouter(b)
	if w := serve(r, "/v1/users/by-username/"+pvUsername, signedIn(pvStrangerID)); w.Code != http.StatusOK {
		t.Fatalf("no block: status %d, want 200 (%s)", w.Code, w.Body)
	}
	if w := serve(r, "/v1/users/"+pvOwnerID.String(), signedIn(pvStrangerID)); w.Code != http.StatusOK {
		t.Fatalf("no block: status %d, want 200 (%s)", w.Code, w.Body)
	}
	if len(b.calls) != 2 {
		t.Fatalf("block checks = %d, want 2", len(b.calls))
	}
	for _, c := range b.calls {
		if c != (pairKey{pvStrangerID, pvOwnerID}) {
			t.Fatalf("block check asked %v, want viewer=%s owner=%s", c, pvStrangerID, pvOwnerID)
		}
	}
}

func TestBlockGateLeavesOwnerAndServiceCallersAlone(t *testing.T) {
	// Every pair reads as blocked; the callers below must not be asked.
	b := &fakeBlocks{err: nil, rows: map[pairKey]bool{
		{pvOwnerID, pvStrangerID}: true, {pvOwnerID, pvOwnerID}: true,
	}}
	r := blockRouter(b)
	for route, path := range userObjectRoutes {
		if w := serve(r, path, signedIn(pvOwnerID)); w.Code != http.StatusOK {
			t.Fatalf("%s: the owner lost their own profile: status %d", route, w.Code)
		}
		// In-cluster callers and signed-out reads name no viewer.
		if w := serve(r, path, map[string]string{"X-Internal-Service-Key": pvInternalKey}); w.Code != http.StatusOK {
			t.Fatalf("%s: a caller with no viewer was refused: status %d", route, w.Code)
		}
		if w := serve(r, path, nil); w.Code != http.StatusOK {
			t.Fatalf("%s: a signed-out read was refused: status %d", route, w.Code)
		}
	}
	if len(b.calls) != 0 {
		t.Fatalf("the block source was asked %d times for callers that are not other viewers", len(b.calls))
	}
}

func TestBlockGateFailsClosedWhenGraphIsDown(t *testing.T) {
	r := blockRouter(&fakeBlocks{err: errors.New("graph down")})
	for route, path := range userObjectRoutes {
		w := serve(r, path, signedIn(pvStrangerID))
		if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), CodeProfileUnavailable) {
			t.Fatalf("%s: graph failure: status %d body %s, want 503 %s", route, w.Code, w.Body, CodeProfileUnavailable)
		}
		if strings.Contains(w.Body.String(), pvUsername) {
			t.Fatalf("%s: the 503 carries profile data: %s", route, w.Body)
		}
	}
	// The owner never depends on the graph.
	if w := serve(r, userObjectRoutes["by-id"], signedIn(pvOwnerID)); w.Code != http.StatusOK {
		t.Fatalf("owner during a graph outage: status %d, want 200", w.Code)
	}
}

// The production checker reads graph-service's real wire keys. The in-cluster
// relationship call still carries target_blocked_viewer truthfully (the edge
// route masks it), and either legacy key counts too.
func TestGraphBlocksReadsEveryBlockKey(t *testing.T) {
	viewer, owner := uuid.New(), uuid.New()
	for _, key := range []string{"blocked", "blocked_by", "viewer_blocked_target", "target_blocked_viewer"} {
		g := &aboutGraph{rel: map[string]any{key: true}}
		srv := g.server(t)
		h := &Handler{graphURL: srv.URL, graphClient: srv.Client(), internalRouteKey: "k-internal"}
		blocked, err := graphBlocks{h: h}.BlockedEitherWay(context.Background(), viewer, owner)
		if err != nil || !blocked {
			t.Fatalf("%s: blocked=%v err=%v, want true", key, blocked, err)
		}
		if want := "user_id=" + viewer.String() + "&other_id=" + owner.String(); g.query[0] != want {
			t.Fatalf("query %q, want %q", g.query[0], want)
		}
	}
	g := &aboutGraph{rel: map[string]any{"follows": true}}
	srv := g.server(t)
	h := &Handler{graphURL: srv.URL, graphClient: srv.Client()}
	if blocked, err := (graphBlocks{h: h}).BlockedEitherWay(context.Background(), viewer, owner); err != nil || blocked {
		t.Fatalf("no block: blocked=%v err=%v", blocked, err)
	}
	bad := &aboutGraph{status: http.StatusInternalServerError, rel: map[string]any{}}
	srv = bad.server(t)
	h = &Handler{graphURL: srv.URL, graphClient: srv.Client()}
	if _, err := (graphBlocks{h: h}).BlockedEitherWay(context.Background(), viewer, owner); err == nil {
		t.Fatal("a non-200 graph answer read as 'not blocked'; it must be an error")
	}
}

func TestNewWiresTheGraphBlockChecker(t *testing.T) {
	h := New(nil, nil, nil)
	if _, ok := h.blocks.(graphBlocks); !ok {
		t.Fatalf("New left the profile block gate unwired (blocks = %T)", h.blocks)
	}
}
