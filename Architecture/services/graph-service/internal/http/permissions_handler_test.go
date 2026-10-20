package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/graph-service/internal/permission"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The permission routes must carry each decision's reason on the wire (DM
// consent contract §4) so clients can explain a disabled Message button.
// The fake resolves through the REAL permission matrix; only the relationship
// snapshot and settings are fixed per target.

type fakePermissionResolver struct {
	snap map[uuid.UUID]struct {
		facts   permission.Facts
		privacy permission.Privacy
	}
}

func (f *fakePermissionResolver) ResolvePermissions(_ context.Context, _, targetID uuid.UUID, actions []permission.Action) (map[permission.Action]permission.Decision, error) {
	s := f.snap[targetID]
	return permission.ResolveAll(actions, s.facts, s.privacy), nil
}

var (
	permStranger  = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	permRequester = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	permBlocked   = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	permFollower  = uuid.MustParse("44444444-4444-4444-8444-444444444444")
)

func permissionRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	fake := &fakePermissionResolver{snap: map[uuid.UUID]struct {
		facts   permission.Facts
		privacy permission.Privacy
	}{
		permStranger:  {privacy: permission.Privacy{WhoCanMessage: "followers_message_requests"}},
		permRequester: {facts: permission.Facts{ActorFollowsTarget: true}, privacy: permission.Privacy{WhoCanMessage: "followers_message_requests"}},
		permBlocked:   {facts: permission.Facts{Blocked: true}, privacy: permission.Privacy{WhoCanMessage: "everyone_message_requests"}},
		permFollower:  {facts: permission.Facts{TargetFollowsActor: true}, privacy: permission.Privacy{WhoCanMessage: "people_i_follow"}},
	}}
	r := gin.New()
	h := New(nil)
	h.permissions = fake
	h.RegisterRoutes(r)
	return r
}

type wireDecision struct {
	Allowed  *bool   `json:"allowed"`
	Fallback *string `json:"fallback"`
	Reason   *string `json:"reason"`
}

func assertWire(t *testing.T, label string, d wireDecision, allowed bool, fallback, reason string) {
	t.Helper()
	if d.Allowed == nil || *d.Allowed != allowed {
		t.Errorf("%s: allowed = %v, want %v", label, d.Allowed, allowed)
	}
	got := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	if got(d.Fallback) != fallback {
		t.Errorf("%s: fallback = %q, want %q", label, got(d.Fallback), fallback)
	}
	if got(d.Reason) != reason {
		t.Errorf("%s: reason = %q, want %q", label, got(d.Reason), reason)
	}
	// Empty fields are omitted, not sent as "".
	if d.Fallback != nil && *d.Fallback == "" || d.Reason != nil && *d.Reason == "" {
		t.Errorf("%s: empty fallback/reason must be omitted", label)
	}
}

func TestCheckBatchReturnsReason(t *testing.T) {
	r := permissionRouter()
	body := `{"target_user_ids":["` + permStranger.String() + `","` + permRequester.String() + `","` +
		permBlocked.String() + `","` + permFollower.String() + `"],"actions":["message","start_conversation"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/permissions/check-batch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", validUser)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	var env struct {
		Data struct {
			Results map[string]map[string]wireDecision `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v: %s", err, resp.Body.String())
	}
	if strings.Contains(resp.Body.String(), `"blocked"`) {
		t.Fatalf("response reveals a block: %s", resp.Body.String())
	}
	for _, action := range []string{"message", "start_conversation"} {
		assertWire(t, "stranger/"+action, env.Data.Results[permStranger.String()][action], false, "", "followers_only")
		assertWire(t, "requester/"+action, env.Data.Results[permRequester.String()][action], false, "message_request", "")
		assertWire(t, "blocked/"+action, env.Data.Results[permBlocked.String()][action], false, "", "unavailable")
		assertWire(t, "follower/"+action, env.Data.Results[permFollower.String()][action], true, "", "")
	}
}

func TestCheckReturnsReason(t *testing.T) {
	r := permissionRouter()
	req := httptest.NewRequest(http.MethodGet, "/v1/permissions/check?target_user_id="+permStranger.String()+"&actions=message", nil)
	req.Header.Set("X-User-Id", validUser)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	var env struct {
		Data struct {
			Decisions map[string]wireDecision `json:"decisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	assertWire(t, "check/message", env.Data.Decisions["message"], false, "", "followers_only")
}
