//go:build integration

package integration

import (
	"net/http"
	"os"
	"testing"

	"github.com/google/uuid"
)

// The community reviewer programme is closed for launch. One switch,
// REVIEWER_PUBLIC_ENABLED (default "false"), is read by BOTH the gateway
// (api-gateway main.go, serveReviewerLaunchGate: every path under
// /v1/reviewer) and reviewer-service (requirePublicReviewer: its public
// routes). While it is off the answer is 503 REVIEWER_PROGRAM_UNAVAILABLE.
//
// reviewerPublicEnv tells these flows which way the stack under test was
// started: "1" when the stack runs with REVIEWER_PUBLIC_ENABLED=true on the
// gateway and on reviewer-service (and the gateway's REVIEWER_SERVICE_URL
// points at reviewer-service), anything else when the programme is closed.
// The flows never guess: each asserts the answer of the mode it was told.
const reviewerPublicEnv = "ATPOST_E2E_REVIEWER_PUBLIC"

func reviewerPublic() bool { return os.Getenv(reviewerPublicEnv) == "1" }

const reviewerClosedCode = "REVIEWER_PROGRAM_UNAVAILABLE"

// TestE2E_Reviewer_ClosedAtLaunch guards the launch gate: with the programme
// closed, nobody opts in, reads a reviewer profile, pulls work or reaches the
// admin family, signed in or not. Every one is 503
// REVIEWER_PROGRAM_UNAVAILABLE, never a 2xx and never a proxied answer.
func TestE2E_Reviewer_ClosedAtLaunch(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	if reviewerPublic() {
		t.Skipf("%s=1: this stack runs with the reviewer programme open", reviewerPublicEnv)
	}

	user := NewHTTPClient(gw, uuid.New())
	anonymous := NewHTTPClient(gw, uuid.Nil)
	optIn := map[string]any{"languages": []string{"en"}, "region": "IN"}

	for _, tc := range []struct {
		who    string
		client *HTTPClient
		method string
		path   string
		body   any
	}{
		{"signed-in user", user, "POST", "/v1/reviewer/opt-in", optIn},
		{"signed-in user", user, "GET", "/v1/reviewer/me", nil},
		{"signed-in user", user, "GET", "/v1/reviewer/me/stats", nil},
		{"signed-in user", user, "GET", "/v1/reviewer/queue", nil},
		{"signed-in user", user, "GET", "/v1/reviewer/assignments/next", nil},
		// The gateway closes the whole prefix, the admin family included.
		{"signed-in user", user, "GET", "/v1/reviewer/admin/stats", nil},
		{"signed-in user", user, "GET", "/v1/reviewer", nil},
		{"anonymous caller", anonymous, "POST", "/v1/reviewer/opt-in", optIn},
		{"anonymous caller", anonymous, "GET", "/v1/reviewer/me", nil},
	} {
		env := tc.client.MustDo(t, ctx, tc.method, tc.path, tc.body)
		wantRefusal(t, env, tc.who+" "+tc.method+" "+tc.path, http.StatusServiceUnavailable, reviewerClosedCode)
	}
}

// TestE2E_Reviewer_OptInAndKYCGate guards the reviewer onboarding + KYC gate
// on a stack that runs with the programme open:
//
//	opt-in → /me reflects it → pulling work without verified KYC is refused
//	(403 KYC_REQUIRED)
//
// The KYC gate (no review work until identity-verified) is the core compliance
// control of the reviewer module; this asserts it fires for a fresh reviewer.
func TestE2E_Reviewer_OptInAndKYCGate(t *testing.T) {
	gw, ctx := gatewayForFlow(t)
	if !reviewerPublic() {
		t.Skipf("the reviewer programme is closed for launch; start the gateway and reviewer-service with REVIEWER_PUBLIC_ENABLED=true and set %s=1 to run the open path", reviewerPublicEnv)
	}

	id := uuid.New()
	rv := NewHTTPClient(gw, id)

	type reviewer struct {
		UserID      uuid.UUID `json:"user_id"`
		Status      string    `json:"status"`
		Region      string    `json:"region"`
		Languages   []string  `json:"languages"`
		KYCVerified bool      `json:"kyc_verified"`
	}

	// A user who never opted in is not a reviewer.
	wantRefusal(t, rv.MustDo(t, ctx, "GET", "/v1/reviewer/me", nil),
		"GET /v1/reviewer/me before opt-in", http.StatusNotFound, "NOT_FOUND")
	wantRefusal(t, rv.MustDo(t, ctx, "GET", "/v1/reviewer/assignments/next", nil),
		"GET /v1/reviewer/assignments/next before opt-in", http.StatusForbidden, "NOT_REVIEWER")

	var opted reviewer
	if !wantData(t, rv.MustDo(t, ctx, "POST", "/v1/reviewer/opt-in", map[string]any{
		"languages": []string{"en"},
		"region":    "IN",
	}), "POST /v1/reviewer/opt-in", http.StatusOK, &opted) {
		t.FailNow()
	}
	if opted.UserID != id {
		t.Errorf("opt-in: reviewer is for user %s, want the caller %s", opted.UserID, id)
	}
	if opted.KYCVerified {
		t.Errorf("opt-in: a fresh reviewer must not be KYC-verified")
	}

	var me reviewer
	if wantData(t, rv.MustDo(t, ctx, "GET", "/v1/reviewer/me", nil), "GET /v1/reviewer/me", http.StatusOK, &me) {
		if me.UserID != id || me.Region != "IN" {
			t.Errorf("/me: got user %s region %q, want user %s region %q", me.UserID, me.Region, id, "IN")
		}
	}

	// No verified KYC → must not be handed review work.
	wantRefusal(t, rv.MustDo(t, ctx, "GET", "/v1/reviewer/assignments/next", nil),
		"reviewer without verified KYC on GET /v1/reviewer/assignments/next", http.StatusForbidden, "KYC_REQUIRED")

	// The admin family stays closed to a reviewer without the admin scope.
	wantRefusal(t, rv.MustDo(t, ctx, "GET", "/v1/reviewer/admin/stats", nil),
		"non-admin on GET /v1/reviewer/admin/stats", http.StatusForbidden, "FORBIDDEN")
}

// TestE2E_Reviewer_AdminEnqueue guards the edge boundary for the reviewer
// intake: /internal/ routes are service-only, so the gateway refuses them from
// the edge with 404 whatever the token carries, admin included, and whether
// the programme is open or closed (the internal-path refusal runs before the
// launch gate). Enqueue is reached in-cluster by the services that produce
// review work.
func TestE2E_Reviewer_AdminEnqueue(t *testing.T) {
	gw, ctx := gatewayForFlow(t)

	admin := NewHTTPClient(gw, uuid.New()).WithAdminRole()
	if env := admin.MustDo(t, ctx, "POST", "/v1/reviewer/internal/enqueue", map[string]any{
		"content_id":      uuid.NewString(),
		"creator_id":      uuid.NewString(),
		"content_type":    "flick",
		"languages":       []string{"en"},
		"content_seconds": 30,
	}); env.Status != 404 {
		t.Errorf("admin on /internal/ enqueue from the edge: want 404, got %d", env.Status)
	}

	// A plain user is refused the same way.
	normal := NewHTTPClient(gw, uuid.New())
	env := normal.MustDo(t, ctx, "POST", "/v1/reviewer/internal/enqueue", map[string]any{
		"content_id": uuid.NewString(), "creator_id": uuid.NewString(),
	})
	if env.Status != 404 {
		t.Errorf("non-admin on /internal/ enqueue from the edge: want 404, got %d", env.Status)
	}
}
