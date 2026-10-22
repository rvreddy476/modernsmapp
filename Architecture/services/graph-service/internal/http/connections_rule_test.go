package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The follower / following list routes and /following-ids over the REAL
// RegisterRoutes wiring, with the connections reader faked: who is blocked,
// who follows whom, who is private, and what each owner's lists hold.
//
// Security sweep 21 Oct 2026: #8 (private accounts' lists were served to
// everyone), #7 (/following-ids skipped the block rule the paginated lists
// apply) and #28 (callerAllowedToReadConnections had no negative test).

type fakeConnections struct {
	blocks    map[blockEdge]bool
	follows   map[blockEdge]bool // follower → followee
	private   map[uuid.UUID]bool
	lists     map[uuid.UUID][]uuid.UUID
	blockErr  error
	followErr error
}

func newFakeConnections() *fakeConnections {
	return &fakeConnections{
		blocks: map[blockEdge]bool{}, follows: map[blockEdge]bool{},
		private: map[uuid.UUID]bool{}, lists: map[uuid.UUID][]uuid.UUID{},
	}
}

func (f *fakeConnections) IsBlockedBy(_ context.Context, blocker, candidate uuid.UUID) (bool, error) {
	if f.blockErr != nil {
		return false, f.blockErr
	}
	return f.blocks[blockEdge{blocker, candidate}], nil
}

func (f *fakeConnections) IsFollowing(_ context.Context, follower, followee uuid.UUID) (bool, error) {
	if f.followErr != nil {
		return false, f.followErr
	}
	return f.follows[blockEdge{follower, followee}], nil
}

func (f *fakeConnections) IsPrivateAccount(_ context.Context, id uuid.UUID) bool {
	return f.private[id]
}

func (f *fakeConnections) GetFollowers(_ context.Context, id uuid.UUID, _, _ int) ([]uuid.UUID, error) {
	return f.lists[id], nil
}

func (f *fakeConnections) GetFollowing(_ context.Context, id uuid.UUID, _, _ int) ([]uuid.UUID, error) {
	return f.lists[id], nil
}

func (f *fakeConnections) GetFollowingIDs(_ context.Context, id uuid.UUID, _ int) ([]uuid.UUID, error) {
	return f.lists[id], nil
}

func connectionsRouter(fake *fakeConnections) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil).WithCanonicalWriteSource(true, nil)
	h.connections = fake
	h.RegisterRoutes(r)
	return r
}

// listRoutes are the three reads under the rule, for one owner.
func listRoutes(owner uuid.UUID) []string {
	return []string{
		"/v1/graph/followers/" + owner.String(),
		"/v1/graph/following/" + owner.String(),
		"/v1/graph/" + owner.String() + "/following-ids",
	}
}

// listGet performs one read. edge stamps the gateway header; viewer sets
// X-User-Id when non-empty.
func listGet(r *gin.Engine, path string, edge bool, viewer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if edge {
		req.Header.Set(GraphWriteSourceHeader, "api-gateway")
	}
	if viewer != "" {
		req.Header.Set("X-User-Id", viewer)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

// idsOf reads the list out of either response shape (bare array, or
// {"items": [...]} for /following-ids).
func idsOf(t *testing.T, resp *httptest.ResponseRecorder) []string {
	t.Helper()
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp.Body.String())
	}
	var ids []string
	if err := json.Unmarshal(env.Data, &ids); err == nil {
		return ids
	}
	var wrapped struct {
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(env.Data, &wrapped); err != nil {
		t.Fatalf("decode items: %v (%s)", err, resp.Body.String())
	}
	return wrapped.Items
}

func wantList(t *testing.T, what string, resp *httptest.ResponseRecorder, want []uuid.UUID) {
	t.Helper()
	got := idsOf(t, resp)
	if len(got) != len(want) {
		t.Fatalf("%s: got %d ids %v, want %d", what, len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i].String() {
			t.Fatalf("%s: id %d is %s, want %s", what, i, got[i], want[i])
		}
	}
}

func wantEmpty(t *testing.T, what string, resp *httptest.ResponseRecorder) {
	t.Helper()
	if got := idsOf(t, resp); len(got) != 0 {
		t.Fatalf("%s: got %v, want the empty list", what, got)
	}
	for _, word := range []string{"blocked", "private", "forbidden"} {
		if strings.Contains(strings.ToLower(resp.Body.String()), word) {
			t.Fatalf("%s: the refusal explains itself (%q): %s", what, word, resp.Body.String())
		}
	}
}

func TestPrivateAccountListsAreForTheOwnerAndApprovedFollowers(t *testing.T) {
	owner, follower, requester, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f := newFakeConnections()
	f.private[owner] = true
	f.follows[blockEdge{follower, owner}] = true
	f.lists[owner] = []uuid.UUID{follower, uuid.New()}
	r := connectionsRouter(f)

	for _, path := range listRoutes(owner) {
		wantList(t, "owner "+path, listGet(r, path, true, owner.String()), f.lists[owner])
		wantList(t, "approved follower "+path, listGet(r, path, true, follower.String()), f.lists[owner])
		// A pending requester has no follow edge yet: still a stranger.
		wantEmpty(t, "requester "+path, listGet(r, path, true, requester.String()))
		wantEmpty(t, "stranger "+path, listGet(r, path, true, stranger.String()))
		// Signed out at the edge: a stranger too.
		wantEmpty(t, "signed-out "+path, listGet(r, path, true, ""))
	}
}

func TestPublicAccountListsAreOpenExceptToTheBlocked(t *testing.T) {
	owner, blocked, stranger := uuid.New(), uuid.New(), uuid.New()
	f := newFakeConnections()
	f.blocks[blockEdge{owner, blocked}] = true
	f.lists[owner] = []uuid.UUID{uuid.New()}
	r := connectionsRouter(f)

	for _, path := range listRoutes(owner) {
		wantList(t, "stranger "+path, listGet(r, path, true, stranger.String()), f.lists[owner])
		wantList(t, "signed-out "+path, listGet(r, path, true, ""), f.lists[owner])
		wantList(t, "owner "+path, listGet(r, path, true, owner.String()), f.lists[owner])
		// #28: the HG4 rule, pinned — including on /following-ids (#7).
		wantEmpty(t, "blocked "+path, listGet(r, path, true, blocked.String()))
	}
}

// A user the owner blocked does not become a reader by following (the
// block severed any edge; a stale one must not reopen the list).
func TestBlockedFollowerOfAPrivateAccountStillSeesNothing(t *testing.T) {
	owner, blocked := uuid.New(), uuid.New()
	f := newFakeConnections()
	f.private[owner] = true
	f.blocks[blockEdge{owner, blocked}] = true
	f.follows[blockEdge{blocked, owner}] = true
	f.lists[owner] = []uuid.UUID{uuid.New()}
	r := connectionsRouter(f)
	for _, path := range listRoutes(owner) {
		wantEmpty(t, path, listGet(r, path, true, blocked.String()))
	}
}

// In-cluster callers (search-service names the owner as X-User-Id;
// live-service-v2 names nobody) keep reading, private accounts included:
// they read on the owner's behalf.
func TestInClusterCallersReadTheOwnersLists(t *testing.T) {
	owner := uuid.New()
	f := newFakeConnections()
	f.private[owner] = true
	f.lists[owner] = []uuid.UUID{uuid.New(), uuid.New()}
	r := connectionsRouter(f)
	for _, path := range listRoutes(owner) {
		wantList(t, "no viewer "+path, listGet(r, path, false, ""), f.lists[owner])
		wantList(t, "owner as viewer "+path, listGet(r, path, false, owner.String()), f.lists[owner])
	}
}

// Failure modes: the block lookup fails open (HG4, unchanged); the follow
// lookup behind a private account fails closed.
func TestConnectionsRuleFailureModes(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	f := newFakeConnections()
	f.lists[owner] = []uuid.UUID{uuid.New()}
	f.blockErr = errors.New("store down")
	r := connectionsRouter(f)
	wantList(t, "block lookup failed, public owner", listGet(r, listRoutes(owner)[0], true, viewer.String()), f.lists[owner])

	f = newFakeConnections()
	f.lists[owner] = []uuid.UUID{uuid.New()}
	f.private[owner] = true
	f.follows[blockEdge{viewer, owner}] = true
	f.followErr = errors.New("store down")
	r = connectionsRouter(f)
	wantEmpty(t, "follow lookup failed, private owner", listGet(r, listRoutes(owner)[0], true, viewer.String()))
}
