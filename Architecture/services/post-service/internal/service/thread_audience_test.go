package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Module 1 fixes-v2 / Codex P1-6.
//
// Two defects being pinned here:
//  1. `trusted`/`close_friends` accepted ANY connection, which is
//     strictly broader than the author's close-friends list — connected
//     but not trusted users could read restricted threads.
//  2. only one block direction was consulted, so a viewer who had blocked
//     the author still received the author's restricted content.

// stubGraph serves a fixed relationship payload.
func stubGraph(t *testing.T, rel map[string]bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rel})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func threadSvc(t *testing.T, rel map[string]bool) *Service {
	srv := stubGraph(t, rel)
	s := &Service{graphServiceURL: srv.URL}
	s.httpClient = http.DefaultClient
	return s
}

func TestCanViewThread_CloseFriendsRequiresExactMembership(t *testing.T) {
	author, viewer := uuid.New(), uuid.New()
	post := &postgres.Post{AuthorID: author, Visibility: "close_friends"}

	// Connected but NOT a close friend: this is the leak v1 allowed.
	svc := threadSvc(t, map[string]bool{"is_connection": true, "is_close_friend": false})
	if svc.canViewThread(t.Context(), post, &viewer) {
		t.Fatal("a connection who is not a close friend must NOT read a close-friends thread")
	}

	// Actual close friend: allowed.
	svc = threadSvc(t, map[string]bool{"is_connection": true, "is_close_friend": true})
	if !svc.canViewThread(t.Context(), post, &viewer) {
		t.Fatal("an actual close friend must be able to read the thread")
	}

	// 'trusted' resolves to the same close-friends audience.
	trusted := &postgres.Post{AuthorID: author, Visibility: "trusted"}
	svc = threadSvc(t, map[string]bool{"is_connection": true, "is_close_friend": false})
	if svc.canViewThread(t.Context(), trusted, &viewer) {
		t.Fatal("'trusted' must use exact close-friends membership, not connection")
	}
}

func TestCanViewThread_BothBlockDirectionsDeny(t *testing.T) {
	author, viewer := uuid.New(), uuid.New()
	post := &postgres.Post{AuthorID: author, Visibility: "followers"}

	// Author blocked viewer.
	svc := threadSvc(t, map[string]bool{"follows": true, "blocked": true})
	if svc.canViewThread(t.Context(), post, &viewer) {
		t.Fatal("author-blocks-viewer must deny access")
	}

	// Viewer blocked author — the direction v1 ignored.
	svc = threadSvc(t, map[string]bool{"follows": true, "blocked_by": true})
	if svc.canViewThread(t.Context(), post, &viewer) {
		t.Fatal("viewer-blocks-author must also deny access")
	}

	// Neither blocked and following: allowed.
	svc = threadSvc(t, map[string]bool{"follows": true})
	if !svc.canViewThread(t.Context(), post, &viewer) {
		t.Fatal("a following viewer with no block must be able to read")
	}
}

func TestCanViewThread_FollowersRequiresFollow(t *testing.T) {
	author, viewer := uuid.New(), uuid.New()
	post := &postgres.Post{AuthorID: author, Visibility: "followers"}

	svc := threadSvc(t, map[string]bool{"follows": false, "is_connection": true})
	if svc.canViewThread(t.Context(), post, &viewer) {
		t.Fatal("a non-follower must not read a followers-scoped thread")
	}
}

// Graph unavailable ⇒ deny restricted content (fail closed).
func TestCanViewThread_FailsClosedWithoutGraph(t *testing.T) {
	author, viewer := uuid.New(), uuid.New()
	svc := &Service{} // no graph URL

	for _, vis := range []string{"followers", "trusted", "close_friends", "private"} {
		post := &postgres.Post{AuthorID: author, Visibility: vis}
		if svc.canViewThread(t.Context(), post, &viewer) {
			t.Errorf("%s must fail closed when the graph is unavailable", vis)
		}
	}
	// Public is unaffected by graph availability.
	pub := &postgres.Post{AuthorID: author, Visibility: "public"}
	if !svc.canViewThread(t.Context(), pub, &viewer) {
		t.Error("public threads must remain readable without a graph lookup")
	}
}
