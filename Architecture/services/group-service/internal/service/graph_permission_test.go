package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
)

// newGraphStub returns a Service wired to a fake graph-service that answers
// every add_to_group check with the given body and status.
func newGraphStub(t *testing.T, status int, body string) (*Service, chan *http.Request) {
	t.Helper()
	seen := make(chan *http.Request, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)

	svc := &Service{graphClient: httpclient.NewWithBreaker(2 * time.Second, "test->graph")}
	svc.SetGraphServiceURL(server.URL)
	return svc, seen
}

const allowedBody = `{"data":{"decisions":{"add_to_group":{"allowed":true}}}}`
const deniedBody = `{"data":{"decisions":{"add_to_group":{"allowed":false}}}}`
const inviteFallbackBody = `{"data":{"decisions":{"add_to_group":{"allowed":false,"fallback":"group_invitation"}}}}`

// The defect this closes: a blocked user could be invited to a group,
// because group invites made no permission call of any kind.
func TestCanAddToGroupRefusesWhenGraphDenies(t *testing.T) {
	svc, _ := newGraphStub(t, http.StatusOK, deniedBody)

	directAdd, invite := svc.canAddToGroup(context.Background(), uuid.New(), uuid.New())

	if directAdd || invite {
		t.Fatalf("a denied add_to_group decision must refuse the invite, got directAdd=%v invite=%v",
			directAdd, invite)
	}
}

func TestCanAddToGroupAllowsWhenGraphAllows(t *testing.T) {
	svc, seen := newGraphStub(t, http.StatusOK, allowedBody)
	actor, target := uuid.New(), uuid.New()

	directAdd, _ := svc.canAddToGroup(context.Background(), actor, target)

	if !directAdd {
		t.Fatal("an allowed decision must permit the invite")
	}
	req := <-seen
	if got := req.URL.Query().Get("target_user_id"); got != target.String() {
		t.Fatalf("target_user_id = %q, want %q — the check must be scoped to the invitee", got, target)
	}
	if got := req.URL.Query().Get("actions"); got != "add_to_group" {
		t.Fatalf("actions = %q, want add_to_group", got)
	}
	if got := req.Header.Get("X-User-Id"); got != actor.String() {
		t.Fatalf("X-User-Id = %q, want the inviter %q", got, actor)
	}
}

// group_invitation is the fallback graph-service returns when the target
// may not be added silently but may be asked. An invite is exactly that.
func TestCanAddToGroupAcceptsInvitationFallback(t *testing.T) {
	svc, _ := newGraphStub(t, http.StatusOK, inviteFallbackBody)

	directAdd, invite := svc.canAddToGroup(context.Background(), uuid.New(), uuid.New())

	if directAdd {
		t.Fatal("fallback-only decision must not permit a silent add")
	}
	if !invite {
		t.Fatal("fallback group_invitation must permit an invite")
	}
}

// Every unknown state fails closed. Allowing an invite when the authority
// cannot be reached would defeat a block, which is a safety guarantee.
func TestCanAddToGroupFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"non-200", http.StatusInternalServerError, `{}`},
		{"unparseable body", http.StatusOK, `not json`},
		{"empty decision", http.StatusOK, `{"data":{"decisions":{}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newGraphStub(t, tc.status, tc.body)

			directAdd, invite := svc.canAddToGroup(context.Background(), uuid.New(), uuid.New())

			if directAdd || invite {
				t.Fatalf("%s must fail closed, got directAdd=%v invite=%v", tc.name, directAdd, invite)
			}
		})
	}
}

// An unconfigured authority must refuse rather than wave invites through.
func TestCanAddToGroupWithoutURLFailsClosed(t *testing.T) {
	svc := &Service{graphClient: httpclient.NewWithBreaker(2*time.Second, "test->graph")}

	directAdd, invite := svc.canAddToGroup(context.Background(), uuid.New(), uuid.New())

	if directAdd || invite {
		t.Fatal("an unset GRAPH_SERVICE_URL must refuse every invite")
	}
}
