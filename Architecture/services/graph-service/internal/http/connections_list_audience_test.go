package http

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /v1/graph/connections/:userId had no audience rule: anyone could read
// anyone's connection list (24 Oct 2026). It now sits behind the follower /
// following lists' rule (callerAllowedToReadConnections).

func connectionListRouter(rule *fakeConnections, list *fakeLegacyConnections) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil).WithCanonicalWriteSource(true, nil)
	h.connections = rule
	h.legacyConnections = list
	h.RegisterRoutes(r)
	return r
}

func connectionsPath(owner uuid.UUID) string { return "/v1/graph/connections/" + owner.String() }

func TestConnectionListAudience(t *testing.T) {
	owner, follower, blocked, stranger := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	conns := []uuid.UUID{uuid.New(), uuid.New()}

	t.Run("public account", func(t *testing.T) {
		rule := newFakeConnections()
		rule.blocks[blockEdge{owner, blocked}] = true
		list := &fakeLegacyConnections{conns: conns}
		r := connectionListRouter(rule, list)
		path := connectionsPath(owner)

		wantList(t, "owner", listGet(r, path, true, owner.String()), conns)
		wantList(t, "stranger", listGet(r, path, true, stranger.String()), conns)
		wantList(t, "signed-out", listGet(r, path, true, ""), conns)
		wantEmpty(t, "blocked by the owner", listGet(r, path, true, blocked.String()))
	})

	t.Run("private account", func(t *testing.T) {
		rule := newFakeConnections()
		rule.private[owner] = true
		rule.follows[blockEdge{follower, owner}] = true
		rule.follows[blockEdge{blocked, owner}] = true
		rule.blocks[blockEdge{owner, blocked}] = true
		list := &fakeLegacyConnections{conns: conns}
		r := connectionListRouter(rule, list)
		path := connectionsPath(owner)

		wantList(t, "owner", listGet(r, path, true, owner.String()), conns)
		wantList(t, "approved follower", listGet(r, path, true, follower.String()), conns)
		wantEmpty(t, "stranger", listGet(r, path, true, stranger.String()))
		wantEmpty(t, "signed-out", listGet(r, path, true, ""))
		wantEmpty(t, "blocked follower", listGet(r, path, true, blocked.String()))
	})

	t.Run("refusals never reach the store", func(t *testing.T) {
		rule := newFakeConnections()
		rule.private[owner] = true
		list := &fakeLegacyConnections{conns: conns}
		r := connectionListRouter(rule, list)
		wantEmpty(t, "stranger", listGet(r, connectionsPath(owner), true, stranger.String()))
		if calls := list.called(); len(calls) != 0 {
			t.Fatalf("a refused read still queried the list: %v", calls)
		}
	})

	t.Run("follow lookup failure fails closed for a private owner", func(t *testing.T) {
		rule := newFakeConnections()
		rule.private[owner] = true
		rule.follows[blockEdge{follower, owner}] = true
		rule.followErr = errors.New("store down")
		r := connectionListRouter(rule, &fakeLegacyConnections{conns: conns})
		wantEmpty(t, "follower during an outage", listGet(r, connectionsPath(owner), true, follower.String()))
	})

	t.Run("in-cluster callers read for the owner", func(t *testing.T) {
		rule := newFakeConnections()
		rule.private[owner] = true
		r := connectionListRouter(rule, &fakeLegacyConnections{conns: conns})
		wantList(t, "no viewer, no gateway stamp", listGet(r, connectionsPath(owner), false, ""), conns)
	})

	t.Run("bad id is still a 400", func(t *testing.T) {
		r := connectionListRouter(newFakeConnections(), &fakeLegacyConnections{conns: conns})
		if resp := listGet(r, "/v1/graph/connections/not-a-uuid", true, stranger.String()); resp.Code != http.StatusBadRequest {
			t.Fatalf("status %d, want 400", resp.Code)
		}
	})
}
