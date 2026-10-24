package http

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/graph-service/internal/store"
)

// Founder decision B (24 Oct 2026): connections are retired. Sending and
// accepting a connection request, and the internal "ensure connection"
// route, answer 410 CONNECTIONS_RETIRED without touching the service. The
// reads of existing data and the routes that only remove keep working.
//
// The real RegisterRoutes wiring, write-source guard strict. svc is nil: a
// retired route that reached the service would panic and fail the test.

type fakeLegacyConnections struct {
	mu    sync.Mutex
	calls []string
	conns []uuid.UUID
	reqs  []store.ConnectionRequest
}

func (f *fakeLegacyConnections) record(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
}

func (f *fakeLegacyConnections) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeLegacyConnections) GetConnections(_ context.Context, _ uuid.UUID, _, _ int) ([]uuid.UUID, error) {
	f.record("GetConnections")
	return f.conns, nil
}

func (f *fakeLegacyConnections) GetPendingConnectionRequests(_ context.Context, _ uuid.UUID) ([]store.ConnectionRequest, error) {
	f.record("GetPendingConnectionRequests")
	return f.reqs, nil
}

func (f *fakeLegacyConnections) GetSentConnectionRequests(_ context.Context, _ uuid.UUID) ([]store.ConnectionRequest, error) {
	f.record("GetSentConnectionRequests")
	return f.reqs, nil
}

func (f *fakeLegacyConnections) DeclineConnectionRequest(_ context.Context, _, _ uuid.UUID) error {
	f.record("DeclineConnectionRequest")
	return nil
}

func (f *fakeLegacyConnections) CancelConnectionRequest(_ context.Context, _, _ uuid.UUID) error {
	f.record("CancelConnectionRequest")
	return nil
}

func (f *fakeLegacyConnections) RemoveConnection(_ context.Context, _, _ uuid.UUID) error {
	f.record("RemoveConnection")
	return nil
}

func retiredRouter(fake *fakeLegacyConnections) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil).WithCanonicalWriteSource(true, nil)
	h.legacyConnections = fake
	h.RegisterRoutes(r)
	return r
}

func TestConnectionCreationIsRetired(t *testing.T) {
	fake := &fakeLegacyConnections{}
	r := retiredRouter(fake)
	writer := anApprovedWriter(t)
	for _, tc := range []struct{ name, path, body, user string }{
		{"send", "/v1/graph/connection-request", `{"user_id":"` + otherUser + `","message":"hi"}`, validUser},
		{"send without a body", "/v1/graph/connection-request", ``, validUser},
		{"send signed out", "/v1/graph/connection-request", `{"user_id":"` + otherUser + `"}`, ""},
		{"accept", "/v1/graph/connection-request/accept", `{"user_id":"` + otherUser + `"}`, validUser},
		{"internal ensure", "/v1/internal/graph/connections/ensure", `{"user_a":"` + validUser + `","user_b":"` + otherUser + `"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doFollowReq(r, http.MethodPost, tc.path, writer, tc.user, tc.body)
			if resp.Code != http.StatusGone {
				t.Fatalf("status %d, want 410 (%s)", resp.Code, resp.Body.String())
			}
			// The literal is the wire contract clients match on.
			if code := errorCode(t, resp); code != "CONNECTIONS_RETIRED" {
				t.Fatalf("code %q, want %q", code, CodeConnectionsRetired)
			}
		})
	}
	if calls := fake.called(); len(calls) != 0 {
		t.Fatalf("retired routes reached the service: %v", calls)
	}
}

// The user-facing creation route still sits behind the write-source guard:
// an unattributed write is refused as before, not answered 410.
func TestRetiredSendStillNeedsAnApprovedWriter(t *testing.T) {
	r := retiredRouter(&fakeLegacyConnections{})
	resp := doFollowReq(r, http.MethodPost, "/v1/graph/connection-request", "", validUser, `{"user_id":"`+otherUser+`"}`)
	if resp.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for an unattributed write", resp.Code)
	}
}

// Existing connections and pending requests stay readable, and old data can
// still be cleared.
func TestExistingConnectionDataStillWorks(t *testing.T) {
	conn := uuid.MustParse(otherUser)
	fake := &fakeLegacyConnections{
		conns: []uuid.UUID{conn},
		reqs:  []store.ConnectionRequest{{SenderID: conn, ReceiverID: uuid.MustParse(validUser), Status: "pending"}},
	}
	r := retiredRouter(fake)
	writer := anApprovedWriter(t)

	for _, tc := range []struct{ method, path, body, want, contains string }{
		{http.MethodGet, "/v1/graph/connections/" + validUser, "", "GetConnections", otherUser},
		{http.MethodGet, "/v1/graph/connection-requests", "", "GetPendingConnectionRequests", `"status":"pending"`},
		{http.MethodGet, "/v1/graph/connection-requests/sent", "", "GetSentConnectionRequests", `"status":"pending"`},
		{http.MethodPost, "/v1/graph/connection-request/decline", `{"user_id":"` + otherUser + `"}`, "DeclineConnectionRequest", "declined"},
		{http.MethodPost, "/v1/graph/connection-request/cancel", `{"user_id":"` + otherUser + `"}`, "CancelConnectionRequest", "cancelled"},
		{http.MethodDelete, "/v1/graph/connection", `{"user_id":"` + otherUser + `"}`, "RemoveConnection", "removed"},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			before := len(fake.called())
			resp := doFollowReq(r, tc.method, tc.path, writer, validUser, tc.body)
			if resp.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 (%s)", resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), tc.contains) {
				t.Fatalf("body %s does not contain %q", resp.Body.String(), tc.contains)
			}
			calls := fake.called()
			if len(calls) != before+1 || calls[len(calls)-1] != tc.want {
				t.Fatalf("service calls %v, want one %s", calls[before:], tc.want)
			}
		})
	}
}

// Production wiring: New hands the service to the surviving connection
// routes. A nil interface here would panic on every connection read.
func TestNewWiresLegacyConnections(t *testing.T) {
	if New(nil).legacyConnections == nil {
		t.Fatal("New does not wire legacyConnections to the service")
	}
}
