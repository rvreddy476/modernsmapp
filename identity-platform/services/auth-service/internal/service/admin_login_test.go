package service

// Admin console sign-in (B3) — unit tests over the A2 in-memory fixture.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
)

func newAdminLoginFixture(t *testing.T) *adminFixture {
	t.Helper()
	f := newAdminFixture(t)
	f.svc.cfg.AdminSessionTTL = 8 * time.Hour
	return f
}

// adminSignIn runs both admin steps with a TOTP code for now.
func (f *adminFixture) adminSignIn(t *testing.T, u *store.User) *AuthResponse {
	t.Helper()
	ch, err := f.svc.AdminLogin(context.Background(), *u.Email, adminTestPassword, "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	resp, err := f.svc.AdminVerify2FA(context.Background(), ch.PendingToken, f.code(t, u, time.Now()))
	if err != nil {
		t.Fatalf("admin verify: %v", err)
	}
	return resp
}

func (f *adminFixture) sessionCount() int { return len(f.st.byID) }

func TestAdminSignIn_PasswordStepCreatesNoSession(t *testing.T) {
	f := newAdminLoginFixture(t)
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}

	ch, err := f.svc.AdminLogin(context.Background(), *u.Email, adminTestPassword, "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Requires2FA || ch.PendingToken == "" {
		t.Fatalf("challenge = %+v", ch)
	}
	if f.sessionCount() != 0 {
		t.Fatal("the password step created a session; an admin session needs TOTP")
	}
}

func TestAdminSignIn_TOTPCreatesAdminMFASession(t *testing.T) {
	f := newAdminLoginFixture(t)
	u := f.addUser(t, true)
	f.st.grants[u.ID] = []store.RoleGrant{{Role: "moderator", App: "dating"}}

	resp := f.adminSignIn(t, u)
	c := parseClaims(t, resp.Tokens.AccessToken)
	if !c.AdminMFA {
		t.Fatal("admin sign-in did not yield admin_mfa=true")
	}
	if c.SessionKind != accesstoken.SessionKindAdmin {
		t.Fatalf("sk=%q, want admin", c.SessionKind)
	}
	if !hasAMR(c.AMR, AMRPassword) || !hasAMR(c.AMR, AMROTP) {
		t.Fatalf("amr=%v", c.AMR)
	}
	if ttlOf(c) > accesstoken.AdminSessionMaxTTL {
		t.Fatalf("admin access TTL %v exceeds the cap", ttlOf(c))
	}
	sess := f.st.byID[resp.SessionID]
	if sess == nil || sess.Kind != store.SessionKindAdmin {
		t.Fatalf("session row kind = %+v", sess)
	}
	if life := time.Until(sess.ExpiresAt); life > 8*time.Hour || life < 7*time.Hour {
		t.Fatalf("admin session lifetime %v, want ADMIN_SESSION_TTL (8h), not the consumer 30 days", life)
	}
}

func TestAdminSignIn_Refusals(t *testing.T) {
	f := newAdminLoginFixture(t)
	ctx := context.Background()

	notAdmin := f.addUser(t, true)
	f.st.platformRoles[notAdmin.ID] = []string{"seller"}
	if _, err := f.svc.AdminLogin(ctx, *notAdmin.Email, adminTestPassword, "", ""); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin: %v", err)
	}

	noTOTP := f.addUser(t, false)
	f.st.platformRoles[noTOTP.ID] = []string{"superadmin"}
	if _, err := f.svc.AdminLogin(ctx, *noTOTP.Email, adminTestPassword, "", ""); !errors.Is(err, ErrTOTPNotEnrolled) {
		t.Fatalf("admin without TOTP: %v", err)
	}

	admin := f.addUser(t, true)
	f.st.platformRoles[admin.ID] = []string{"admin"}
	if _, err := f.svc.AdminLogin(ctx, *admin.Email, "wrong-password", "", ""); !errors.Is(err, ErrAdminInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := f.svc.AdminLogin(ctx, "nobody@example.test", adminTestPassword, "", ""); !errors.Is(err, ErrAdminInvalidCredentials) {
		t.Fatalf("unknown account: %v", err)
	}

	inactive := f.addUser(t, true)
	f.st.platformRoles[inactive.ID] = []string{"admin"}
	inactive.AccountStatus = "deactivated"
	if _, err := f.svc.AdminLogin(ctx, *inactive.Email, adminTestPassword, "", ""); !errors.Is(err, ErrAdminAccountInactive) {
		t.Fatalf("inactive: %v", err)
	}
	if inactive.AccountStatus != "deactivated" {
		t.Fatal("the admin sign-in reactivated an account")
	}
	if f.sessionCount() != 0 {
		t.Fatal("a refused sign-in created a session")
	}
}

func TestAdminSignIn_SecondStepNeedsAValidTOTPOnce(t *testing.T) {
	f := newAdminLoginFixture(t)
	ctx := context.Background()
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}

	ch, err := f.svc.AdminLogin(ctx, *u.Email, adminTestPassword, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AdminVerify2FA(ctx, ch.PendingToken, "000000"); !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("wrong code: %v", err)
	}
	if _, err := f.svc.AdminVerify2FA(ctx, ch.PendingToken, ""); err == nil {
		t.Fatal("empty code accepted")
	}
	if f.sessionCount() != 0 {
		t.Fatal("a failed code created a session")
	}
	if _, err := f.svc.AdminVerify2FA(ctx, ch.PendingToken, f.code(t, u, time.Now())); err != nil {
		t.Fatalf("valid code: %v", err)
	}
	// The pending sign-in is single use.
	if _, err := f.svc.AdminVerify2FA(ctx, ch.PendingToken, f.code(t, u, time.Now().Add(30*time.Second))); !errors.Is(err, ErrAdminPendingInvalid) {
		t.Fatalf("reused pending token: %v", err)
	}
}

// Consumer and admin pending sign-ins cannot be completed on each other's route.
func TestAdminSignIn_PendingTokensDoNotCross(t *testing.T) {
	f := newAdminLoginFixture(t)
	ctx := context.Background()
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}

	consumer, err := f.svc.LoginWithPassword(ctx, *u.Email, adminTestPassword, "dev", "web", "", "")
	if err != nil || !consumer.Requires2FA {
		t.Fatalf("consumer login: %v %+v", err, consumer)
	}
	if _, err := f.svc.AdminVerify2FA(ctx, consumer.PendingToken, f.code(t, u, time.Now())); !errors.Is(err, ErrAdminPendingInvalid) {
		t.Fatalf("consumer pending token completed an admin sign-in: %v", err)
	}

	ch, err := f.svc.AdminLogin(ctx, *u.Email, adminTestPassword, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Verify2FA(ctx, u.ID, f.code(t, u, time.Now().Add(30*time.Second)), ch.PendingToken); err == nil {
		t.Fatal("admin pending token completed a consumer sign-in")
	}
	if f.sessionCount() != 0 {
		t.Fatal("a crossed pending token created a session")
	}
}

// A consumer 2FA login of the same admin is still a consumer session.
func TestConsumerLoginOfAdminIsNotAnAdminSession(t *testing.T) {
	f := newAdminLoginFixture(t)
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}
	resp := f.login(t, u)
	if c := parseClaims(t, resp.Tokens.AccessToken); c.SessionKind != "" {
		t.Fatalf("consumer token carries sk=%q", c.SessionKind)
	}
	if f.st.byID[resp.SessionID].Kind == store.SessionKindAdmin {
		t.Fatal("consumer login created an admin session row")
	}
}

func TestAdminRefresh_ScopedToAdminSessions(t *testing.T) {
	f := newAdminLoginFixture(t)
	ctx := context.Background()
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}

	consumer := f.login(t, u)
	f.mr.FlushAll() // the next sign-in reuses the current TOTP step
	admin := f.adminSignIn(t, u)
	adminExpiry := f.st.byID[admin.SessionID].ExpiresAt

	// A consumer session's refresh token does not authenticate the admin refresh…
	if _, err := f.svc.AdminRefreshSession(ctx, consumer.Tokens.RefreshToken, "", ""); !errors.Is(err, ErrWrongSessionKind) {
		t.Fatalf("admin refresh accepted a consumer token: %v", err)
	}
	if f.st.byID[consumer.SessionID].RevokedAt != nil {
		t.Fatal("admin refresh revoked a consumer session")
	}
	// …and the consumer refresh refuses an admin session.
	if _, err := f.svc.RefreshSession(ctx, admin.Tokens.RefreshToken, "", ""); !errors.Is(err, ErrWrongSessionKind) {
		t.Fatalf("consumer refresh accepted an admin token: %v", err)
	}
	if f.st.byID[admin.SessionID].RevokedAt != nil {
		t.Fatal("consumer refresh revoked the admin session")
	}

	rotated, err := f.svc.AdminRefreshSession(ctx, admin.Tokens.RefreshToken, "", "")
	if err != nil {
		t.Fatalf("admin refresh: %v", err)
	}
	c := parseClaims(t, rotated.Tokens.AccessToken)
	if !c.AdminMFA || c.SessionKind != accesstoken.SessionKindAdmin {
		t.Fatalf("refreshed admin token admin_mfa=%v sk=%q", c.AdminMFA, c.SessionKind)
	}
	if rotated.Tokens.RefreshToken == admin.Tokens.RefreshToken {
		t.Fatal("refresh token not rotated")
	}
	if !f.st.byID[admin.SessionID].ExpiresAt.Equal(adminExpiry) {
		t.Fatal("admin refresh extended the absolute session lifetime")
	}
}

func TestAdminRefresh_EndsWhenAdminAccessIsLost(t *testing.T) {
	f := newAdminLoginFixture(t)
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}
	admin := f.adminSignIn(t, u)

	f.st.platformRoles[u.ID] = nil // role revoked
	if _, err := f.svc.AdminRefreshSession(context.Background(), admin.Tokens.RefreshToken, "", ""); !errors.Is(err, ErrAdminAccessLost) {
		t.Fatalf("refresh after role loss: %v", err)
	}
	if f.st.byID[admin.SessionID].RevokedAt == nil {
		t.Fatal("admin session survived the loss of admin access")
	}
}

func TestAdminLogout_OnlyEndsAdminSessions(t *testing.T) {
	f := newAdminLoginFixture(t)
	ctx := context.Background()
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}
	consumer := f.login(t, u)
	f.mr.FlushAll()
	admin := f.adminSignIn(t, u)

	if err := f.svc.AdminLogout(ctx, consumer.Tokens.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if f.st.byID[consumer.SessionID].RevokedAt != nil {
		t.Fatal("admin logout revoked a consumer session")
	}
	if err := f.svc.AdminLogout(ctx, admin.Tokens.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if f.st.byID[admin.SessionID].RevokedAt == nil {
		t.Fatal("admin logout did not revoke the admin session")
	}
}

func TestStepUp_KindsDoNotCross(t *testing.T) {
	f := newAdminLoginFixture(t)
	ctx := context.Background()
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}
	consumer := f.login(t, u)
	f.mr.FlushAll()
	admin := f.adminSignIn(t, u)
	f.mr.FlushAll()
	code := f.code(t, u, time.Now())

	if _, err := f.svc.AdminStepUp(ctx, u.ID, consumer.SessionID, code); !errors.Is(err, ErrWrongSessionKind) {
		t.Fatalf("admin step-up on a consumer session: %v", err)
	}
	if _, err := f.svc.StepUp(ctx, u.ID, admin.SessionID, code); !errors.Is(err, ErrWrongSessionKind) {
		t.Fatalf("consumer step-up on an admin session: %v", err)
	}
	resp, err := f.svc.AdminStepUp(ctx, u.ID, admin.SessionID, code)
	if err != nil {
		t.Fatalf("admin step-up: %v", err)
	}
	c := parseClaims(t, resp.AccessToken)
	if c.StepUpAt == 0 || !c.AdminMFA || c.SessionKind != accesstoken.SessionKindAdmin {
		t.Fatalf("stepped-up admin token: step_up_at=%d admin_mfa=%v sk=%q", c.StepUpAt, c.AdminMFA, c.SessionKind)
	}
}
