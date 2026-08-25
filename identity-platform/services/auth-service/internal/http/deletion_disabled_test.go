package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 3 M3-P0-7 / LB-6 — self-service deletion is DISABLED and mutates
// nothing.
//
// SR-7 corrected the messaging but kept the mutation: the account was still
// marked `pending_deletion` and `user.deletion_requested` was still emitted.
// Nothing in this repository purges a pending_deletion account, but that event
// DOES reach other services — so some could erase their slice while the rest
// of the data stays, producing partial irreversible erasure. That is worse
// than either finishing the pipeline or not starting it.

// deletionTripwireService fails the test if the endpoint mutates anything.
type deletionTripwireService struct {
	stubAuthService
	t *testing.T
}

func (s *deletionTripwireService) DeleteAccount(_ context.Context, _ uuid.UUID) error {
	s.t.Fatal("the deletion endpoint called DeleteAccount. That marks the account " +
		"pending_deletion and emits user.deletion_requested into an INCOMPLETE " +
		"cross-service workflow, which can erase part of the user's data and " +
		"leave the rest — the exact outcome the disable exists to prevent.")
	return nil
}

func (s *deletionTripwireService) LogoutAll(_ context.Context, _ uuid.UUID) (int64, error) {
	s.t.Fatal("the deletion endpoint revoked sessions. It is supposed to change " +
		"NOTHING; signing the user out is a real, user-visible effect.")
	return 0, nil
}

func deletionRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&deletionTripwireService{t: t}, &config.Config{}, nil, nil)
	h.RegisterRoutes(r, noopMiddleware(), noopMiddleware())
	return r
}

func deleteAccount(t *testing.T, r *gin.Engine) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/v1/auth/account", nil)
	req.Header.Set("X-User-Id", uuid.New().String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	return resp
}

// THE CLOSURE PROOF: zero user/session/outbox changes.
func TestDeletionEndpointMutatesNothing(t *testing.T) {
	r := deletionRouter(t)
	resp := deleteAccount(t, r)

	// The tripwires above fail the test if any mutation was attempted.
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 — the endpoint must answer a stable "+
			"unavailable response, not a success", resp.Code)
	}
}

// The user must stay signed in. Clearing auth cookies is a real effect.
func TestDeletionEndpointDoesNotClearAuthCookies(t *testing.T) {
	r := deletionRouter(t)
	resp := deleteAccount(t, r)

	for _, c := range resp.Result().Cookies() {
		if c.Name == accessTokenCookieName || c.Name == refreshTokenCookieName {
			if c.MaxAge < 0 || c.Value == "" {
				t.Fatalf("the endpoint cleared %s: the user was signed out by a "+
					"request that is supposed to change nothing", c.Name)
			}
		}
	}
}

// The response must name the real path to deletion. A refusal with no next
// step leaves a user who wants their data gone with nowhere to go.
func TestDeletionResponseIsHonestAndActionable(t *testing.T) {
	r := deletionRouter(t)
	resp := deleteAccount(t, r)

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				AccountChanged  bool   `json:"account_changed"`
				SessionsRevoked bool   `json:"sessions_revoked"`
				DataErased      bool   `json:"data_erased"`
				RequestVia      string `json:"request_via"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", resp.Body.String(), err)
	}

	if body.Error.Code != DeletionUnavailableCode {
		t.Errorf("code = %q, want %q", body.Error.Code, DeletionUnavailableCode)
	}
	if body.Error.Details.AccountChanged || body.Error.Details.SessionsRevoked ||
		body.Error.Details.DataErased {
		t.Errorf("the response claims an effect that did not happen: %+v", body.Error.Details)
	}
	if !strings.Contains(body.Error.Details.RequestVia, "@") {
		t.Errorf("no support channel offered: %q", body.Error.Details.RequestVia)
	}
	// It must not claim scheduling or erasure.
	lower := strings.ToLower(body.Error.Message)
	for _, forbidden := range []string{"scheduled for deletion", "will be deleted", "30 days"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("the message claims %q, which does not happen: %s",
				forbidden, body.Error.Message)
		}
	}
}
