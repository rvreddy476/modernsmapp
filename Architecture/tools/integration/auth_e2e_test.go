//go:build integration

package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// authResponse mirrors the relevant slice of identity-auth's AuthResponse
// envelope (auth-service internal/service/auth.go). A registration answer
// carries requires_verification + verification_token and NO tokens; a sign-in
// or refresh carries tokens.
type authResponse struct {
	Tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	} `json:"tokens"`
	User struct {
		ID string `json:"id"`
	} `json:"user"`
	RequiresVerification bool   `json:"requires_verification"`
	VerificationToken    string `json:"verification_token"`
}

// parseSession decodes an answer that must carry a session. The body is not
// printed on failure: it may hold credentials.
func parseSession(t *testing.T, step string, raw json.RawMessage) authResponse {
	t.Helper()
	var a authResponse
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatalf("%s: decode auth response: %v", step, err)
	}
	if a.Tokens.AccessToken == "" {
		t.Fatalf("%s: auth response carried no access token", step)
	}
	return a
}

// errCode is the envelope's error code, "" when there is none.
func errCode(env *Envelope) string {
	if env == nil || env.Error == nil {
		return ""
	}
	return env.Error.Code
}

// devMailCodeRE matches the code line of the verification email
// (auth-service internal/service/verification.go RequestEmailVerification).
var devMailCodeRE = regexp.MustCompile(`verification code is (\d{4,10})`)

// devMailCode returns the newest verification code "sent" to the address.
//
// No stack these flows run on delivers email: outside production
// identity-auth uses its development log sender (auth-service
// internal/email/sender.go LogSender), which writes each message, code
// included, to the service log as one JSON line. That log is the inbox.
// ATPOST_E2E_MAIL_LOG names the file identity-auth's output goes to
// (services.log in .github/workflows/e2e.yml).
//
// The code is a credential: it is returned, never logged.
func devMailCode(t *testing.T, to string) string {
	t.Helper()
	path := os.Getenv("ATPOST_E2E_MAIL_LOG")
	if path == "" {
		missingStack(t, "set ATPOST_E2E_MAIL_LOG to the file identity-auth logs to; the development log sender writes the verification email there")
	}
	read := func() string {
		f, err := os.Open(path)
		if err != nil {
			return ""
		}
		defer f.Close()
		code := ""
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			line := sc.Text()
			if !strings.Contains(line, to) {
				continue
			}
			// run-local.sh prefixes each line with "[service] ".
			start := strings.IndexByte(line, '{')
			if start < 0 {
				continue
			}
			var rec struct {
				To   string `json:"to"`
				Body string `json:"body"`
			}
			if json.Unmarshal([]byte(line[start:]), &rec) != nil || rec.To != to {
				continue
			}
			if m := devMailCodeRE.FindStringSubmatch(rec.Body); m != nil {
				code = m[1] // keep the last: a resend replaces the code
			}
		}
		return code
	}
	code := ""
	Eventually(t, 30*time.Second, "verification email for the new account appears in ATPOST_E2E_MAIL_LOG (is identity-auth's output line-buffered into that file?)", func() bool {
		code = read()
		return code != ""
	})
	return code
}

// e2ePassword satisfies the password policy; the account exists only in the
// run's own database.
const e2ePassword = "Sup3r!Secret-123"

// registerVerifiedUser takes a brand-new account through the whole sign-up
// the product defines today and returns its first session:
//
//	register (no consent)   → 422 CONSENT_REQUIRED, names the terms version
//	register                → 201, account PENDING, no session
//	login while pending     → 403 EMAIL_NOT_VERIFIED + a fresh verification token
//	verify-email            → 200 (token + the emailed code)
//	login                   → 200 with a session
//
// Registration stopped returning a session when accounts became pending
// until the emailed code is verified (auth-service
// internal/service/registration.go, LB-5/CLB-3).
func registerVerifiedUser(t *testing.T, ctx context.Context, gw *HTTPClient) (authResponse, string) {
	t.Helper()
	email := fmt.Sprintf("e2e-%s@test.local", uuid.NewString())
	// Letters, spaces, hyphens and apostrophes only (validatePersonName):
	// the old "E2E" was refused for its digit.
	body := map[string]any{
		"email":      email,
		"password":   e2ePassword,
		"first_name": "Asha",
		"last_name":  "O'Neil-Tester",
		"dob":        "1990-01-15",
	}

	// Consent is never assumed: without accepted_terms the account is not
	// created, and the refusal names the terms version in force.
	refused := gw.MustDo(t, ctx, "POST", "/v1/auth/register", body)
	if refused.Status != http.StatusUnprocessableEntity || errCode(refused) != "CONSENT_REQUIRED" {
		t.Fatalf("register without consent: got %d %q, want 422 CONSENT_REQUIRED", refused.Status, errCode(refused))
	}
	var consent struct {
		CurrentTermsVersion string `json:"current_terms_version"`
	}
	if err := json.Unmarshal(refused.Error.Details, &consent); err != nil || consent.CurrentTermsVersion == "" {
		t.Fatalf("CONSENT_REQUIRED carried no current_terms_version (err=%v)", err)
	}
	body["accepted_terms"] = true
	body["terms_version"] = consent.CurrentTermsVersion

	// Register: a pending account and a verification token, no session.
	var reg authResponse
	if err := json.Unmarshal(gw.MustOK(t, ctx, "POST", "/v1/auth/register", body), &reg); err != nil {
		t.Fatalf("register: decode: %v", err)
	}
	if reg.User.ID == "" {
		t.Fatal("register returned no user id")
	}
	if !reg.RequiresVerification || reg.VerificationToken == "" {
		t.Fatal("register: want requires_verification=true and a verification_token")
	}
	if reg.Tokens.AccessToken != "" || reg.Tokens.RefreshToken != "" {
		t.Fatal("register issued a session for an unverified email address")
	}

	// A pending account cannot sign in. The right password gets a fresh
	// verification token (which retires the one from registration), never a
	// session.
	pending := gw.MustDo(t, ctx, "POST", "/v1/auth/login", map[string]any{
		"identifier": email,
		"password":   e2ePassword,
	})
	if pending.Status != http.StatusForbidden || errCode(pending) != "EMAIL_NOT_VERIFIED" {
		t.Fatalf("login before verification: got %d %q, want 403 EMAIL_NOT_VERIFIED", pending.Status, errCode(pending))
	}
	var resume struct {
		VerificationToken string `json:"verification_token"`
	}
	if err := json.Unmarshal(pending.Error.Details, &resume); err != nil || resume.VerificationToken == "" {
		t.Fatalf("EMAIL_NOT_VERIFIED carried no verification_token (err=%v)", err)
	}

	// Verify with the emailed code.
	gw.MustOK(t, ctx, "POST", "/v1/auth/verify-email", map[string]any{
		"verification_token": resume.VerificationToken,
		"code":               devMailCode(t, email),
	})

	// Now the same credentials sign in.
	login := parseSession(t, "login", gw.MustOK(t, ctx, "POST", "/v1/auth/login", map[string]any{
		"identifier": email,
		"password":   e2ePassword,
	}))
	if login.User.ID != reg.User.ID {
		t.Fatalf("login returned user %q, registered %q", login.User.ID, reg.User.ID)
	}
	return login, email
}

// TestE2E_Auth_RegisterLoginRefresh exercises the real identity flow through
// the gateway end to end:
//
//	register → verify email → login → /me (token works) → refresh →
//	/me (rotated token works)
//	negative: a normal user cannot reach a superadmin-only endpoint (403)
//
// Identity comes only from the verified token (the client sends no X-User-Id
// on these calls), and authorization is enforced server-side, not by a
// client header.
func TestE2E_Auth_RegisterLoginRefresh(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	gw := NewHTTPClient(urls.APIGateway, uuid.Nil) // unauthenticated edge client
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	login, email := registerVerifiedUser(t, ctx, gw)

	// The access token authenticates /me through the gateway, and /me is
	// this account, verified.
	checkMe := func(step, token string) {
		t.Helper()
		var me struct {
			ID            string `json:"id"`
			Email         string `json:"email"`
			EmailVerified bool   `json:"email_verified"`
		}
		raw := gw.WithBearer(token).MustOK(t, ctx, "GET", "/v1/auth/me", nil)
		if err := json.Unmarshal(raw, &me); err != nil {
			t.Fatalf("%s: decode /me: %v", step, err)
		}
		if me.ID != login.User.ID || me.Email != email || !me.EmailVerified {
			t.Fatalf("%s: /me = id %q email %q verified %v, want id %q email %q verified true",
				step, me.ID, me.Email, me.EmailVerified, login.User.ID, email)
		}
	}
	checkMe("after login", login.Tokens.AccessToken)

	// Refresh rotates the token; the new one still authenticates.
	ref := parseSession(t, "refresh", gw.MustOK(t, ctx, "POST", "/v1/auth/refresh", map[string]any{
		"refresh_token": login.Tokens.RefreshToken,
	}))
	checkMe("after refresh", ref.Tokens.AccessToken)

	// Negative: a normal user must NOT be able to read the RBAC roles of any
	// user — that endpoint is superadmin-only and enforced server-side.
	env := gw.WithBearer(ref.Tokens.AccessToken).MustDo(t, ctx, "GET", "/v1/auth/admin/roles/"+login.User.ID, nil)
	if env.Status != http.StatusForbidden {
		t.Errorf("normal user on superadmin endpoint: expected 403, got %d", env.Status)
	}
}

// TestE2E_Auth_RoleGrantNeedsSuperadmin pins the server-side gate on the
// role-grant route: an ordinary signed-in account that asks for a role is
// refused, and holds no role afterwards.
//
// This replaces TestE2E_Auth_RBACGrant, which skipped unless it was handed a
// superadmin bearer token and could not have passed with one: a grant now
// needs a superadmin whose session is an admin MFA session (TOTP enrolled)
// with a step-up in the last five minutes, and a reason in the body
// (auth-service internal/service/roles.go authorizePrivileged and
// validateRoleChange, admin_session.go requireFreshAdminSession). The granted
// path needs such an account to exist before the run; nothing in the e2e
// stack creates one, so it is not exercised here.
func TestE2E_Auth_RoleGrantNeedsSuperadmin(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	gw := NewHTTPClient(urls.APIGateway, uuid.Nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	login, _ := registerVerifiedUser(t, ctx, gw)
	user := gw.WithBearer(login.Tokens.AccessToken)

	// A complete, valid request, so the refusal is the authorization gate
	// and not input validation.
	env := user.MustDo(t, ctx, "POST", "/v1/auth/admin/roles", map[string]any{
		"user_id": login.User.ID,
		"role":    "moderator",
		"reason":  "e2e: an ordinary account asks for a role",
	})
	if env.Status != http.StatusForbidden || errCode(env) != "FORBIDDEN" {
		t.Fatalf("role grant by an ordinary account: got %d %q, want 403 FORBIDDEN", env.Status, errCode(env))
	}

	// Nothing was granted: the account's live role list is still empty.
	var caps struct {
		Roles        []string        `json:"roles"`
		Capabilities map[string]bool `json:"capabilities"`
	}
	raw := user.MustOK(t, ctx, "GET", "/v1/auth/me/capabilities", nil)
	if err := json.Unmarshal(raw, &caps); err != nil {
		t.Fatalf("decode /me/capabilities: %v", err)
	}
	if len(caps.Roles) != 0 || caps.Capabilities["moderator"] {
		t.Errorf("refused grant left roles behind: roles=%v moderator=%v", caps.Roles, caps.Capabilities["moderator"])
	}
}
