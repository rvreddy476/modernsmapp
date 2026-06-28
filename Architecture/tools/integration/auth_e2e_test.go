//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// authResponse mirrors the relevant slice of identity-auth's AuthResponse
// envelope: { data: { tokens: {...}, user: {...} } }.
type authResponse struct {
	Tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"tokens"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
}

func parseAuth(t *testing.T, raw json.RawMessage) authResponse {
	t.Helper()
	var a authResponse
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("decode auth response: %v (raw: %s)", err, string(raw))
	}
	if a.Tokens.AccessToken == "" {
		t.Fatalf("auth response carried no access token (raw: %s)", string(raw))
	}
	return a
}

// TestE2E_Auth_RegisterLoginRefresh exercises the real identity flow through the
// gateway end to end:
//
//	register → /me (token works) → login → refresh → /me (rotated token works)
//	negative: a normal user cannot reach a superadmin-only endpoint (403)
//
// This guards the server-side-scopes work: identity comes only from the verified
// token (the client sends no X-User-Id on these calls), and authorization is
// enforced server-side, not by a client header.
func TestE2E_Auth_RegisterLoginRefresh(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	gw := NewHTTPClient(urls.APIGateway, uuid.Nil) // unauthenticated edge client
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	email := fmt.Sprintf("e2e-%s@test.local", uuid.NewString())
	phone := fmt.Sprintf("+1555%07d", time.Now().UnixNano()%1e7)
	const password = "Sup3r!Secret-123"

	// 1. Register — returns an authenticated session.
	reg := parseAuth(t, gw.MustOK(t, ctx, "POST", "/v1/auth/register", map[string]any{
		"phone":      phone,
		"email":      email,
		"password":   password,
		"first_name": "E2E",
		"last_name":  "Tester",
	}))
	if reg.User.ID == "" {
		t.Fatal("register returned no user id")
	}

	// 2. The access token authenticates /me through the gateway.
	meRaw := gw.WithBearer(reg.Tokens.AccessToken).MustOK(t, ctx, "GET", "/v1/auth/me", nil)
	if len(meRaw) == 0 {
		t.Fatal("/me returned empty body for a valid token")
	}

	// 3. Login with the same credentials.
	login := parseAuth(t, gw.MustOK(t, ctx, "POST", "/v1/auth/login", map[string]any{
		"identifier": email,
		"password":   password,
	}))

	// 4. Refresh rotates the token; the new one still authenticates.
	refRaw := gw.MustOK(t, ctx, "POST", "/v1/auth/refresh", map[string]any{
		"refresh_token": login.Tokens.RefreshToken,
	})
	ref := parseAuth(t, refRaw)
	gw.WithBearer(ref.Tokens.AccessToken).MustOK(t, ctx, "GET", "/v1/auth/me", nil)

	// 5. Negative: a normal user must NOT be able to read the RBAC roles of any
	// user — that endpoint is superadmin-only and enforced server-side.
	env := gw.WithBearer(reg.Tokens.AccessToken).MustDo(t, ctx, "GET", "/v1/auth/admin/roles/"+reg.User.ID, nil)
	if env.Status != 403 {
		t.Errorf("normal user on superadmin endpoint: expected 403, got %d", env.Status)
	}
}

// TestE2E_Auth_RBACGrant verifies the role-grant path when the stack is started
// with a known superadmin. Skips unless ATPOST_SUPERADMIN_ID + its token are
// provided (the stack must have that UUID in SUPERADMIN_USER_IDS).
func TestE2E_Auth_RBACGrant(t *testing.T) {
	SkipIfNotIntegration(t)
	superToken := envOr("ATPOST_SUPERADMIN_TOKEN", "")
	if superToken == "" {
		t.Skip("set ATPOST_SUPERADMIN_TOKEN (a token for a user in SUPERADMIN_USER_IDS) to run the RBAC grant flow")
	}
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	gw := NewHTTPClient(urls.APIGateway, uuid.Nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	super := gw.WithBearer(superToken)
	target := uuid.NewString()

	// Grant moderator → succeeds for a superadmin.
	super.MustOK(t, ctx, "POST", "/v1/auth/admin/roles", map[string]any{
		"user_id": target, "role": "moderator",
	})
	// It shows up in the target's role list.
	rolesRaw := super.MustOK(t, ctx, "GET", "/v1/auth/admin/roles/"+target, nil)
	if !strings.Contains(string(rolesRaw), "moderator") {
		t.Errorf("granted role not listed: %s", string(rolesRaw))
	}
	// Revoke cleans up.
	super.MustOK(t, ctx, "DELETE", "/v1/auth/admin/roles", map[string]any{
		"user_id": target, "role": "moderator",
	})
}
