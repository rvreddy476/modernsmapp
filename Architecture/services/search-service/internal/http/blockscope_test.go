package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/search-service/internal/graphclient"
	"github.com/atpost/search-service/internal/store/search"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 2 M2-P0-4 — the middleware that resolves the viewer's block set.
//
// The behaviour that matters is the failure mode: when graph-service
// cannot answer, search must refuse to serve rather than serve results it
// cannot prove are safe.

func newBlockScopeRouter(t *testing.T, graphURL string) (*gin.Engine, *bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	h := &Handler{graphClient: graphclient.New(graphURL, "", nil)}

	reached := false
	r := gin.New()
	r.GET("/probe", h.resolveBlockScope(), func(c *gin.Context) {
		reached = true
		if !search.BlockScopeResolved(c.Request.Context()) {
			t.Error("handler ran without a resolved block scope")
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r, &reached
}

// A graph-service outage must produce 503, and the search handler must
// never run. Serving unfiltered results here would show a viewer content
// from accounts they blocked, with nothing in the response to indicate
// the safety filter had been skipped.
func TestResolveBlockScope_FailsClosedWhenGraphServiceIsDown(t *testing.T) {
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer graph.Close()

	r, reached := newBlockScopeRouter(t, graph.URL)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-User-Id", uuid.New().String())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — search must fail closed when block state is unknown", w.Code)
	}
	if *reached {
		t.Fatal("the search handler ran despite an unresolvable block set")
	}
}

// The same must hold when graph-service is unreachable rather than
// erroring — an unroutable address stands in for a network partition.
func TestResolveBlockScope_FailsClosedWhenGraphServiceIsUnreachable(t *testing.T) {
	r, reached := newBlockScopeRouter(t, "http://127.0.0.1:1")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-User-Id", uuid.New().String())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if *reached {
		t.Fatal("the search handler ran despite an unreachable graph-service")
	}
}

// A healthy graph-service resolves the scope and the request proceeds.
func TestResolveBlockScope_AttachesBlockedIDs(t *testing.T) {
	blockedID := uuid.New().String()
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// M2-P0-5: search must request the FULL suppression set — blocks
		// in both directions plus the viewer's outgoing mutes. Asking for
		// include=blocks would drop mute suppression, which the approved
		// contract requires.
		if got := r.URL.Query().Get("include"); got != "" {
			t.Errorf("include=%q, want the default (blocks both ways + viewer mutes)", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_ids":["` + blockedID + `"]}`))
	}))
	defer graph.Close()

	h := &Handler{graphClient: graphclient.New(graph.URL, "", nil)}
	gin.SetMode(gin.TestMode)
	r := gin.New()

	var got []string
	r.GET("/probe", h.resolveBlockScope(), func(c *gin.Context) {
		got = search.BlockedIDsForTest(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-User-Id", uuid.New().String())
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if len(got) != 1 || got[0] != blockedID {
		t.Fatalf("blocked ids = %v, want [%s]", got, blockedID)
	}
}

// Anonymous viewers have no block relationships, so they must not be
// blocked by graph-service being down — they can only ever reach public,
// approved content anyway.
func TestResolveBlockScope_AnonymousViewerPassesWithoutGraphCall(t *testing.T) {
	called := false
	graph := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer graph.Close()

	r, reached := newBlockScopeRouter(t, graph.URL)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an anonymous viewer", w.Code)
	}
	if !*reached {
		t.Fatal("anonymous search should have been served")
	}
	if called {
		t.Fatal("no graph lookup should be made for an anonymous viewer")
	}
}

// A malformed X-User-Id is not the same as no header: we cannot tell
// whose blocks apply, so the request must be rejected rather than treated
// as anonymous.
func TestResolveBlockScope_MalformedViewerIDIsRejected(t *testing.T) {
	r, reached := newBlockScopeRouter(t, "http://127.0.0.1:1")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-User-Id", "not-a-uuid")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if *reached {
		t.Fatal("a request with an unparseable viewer id must not be served")
	}
}
