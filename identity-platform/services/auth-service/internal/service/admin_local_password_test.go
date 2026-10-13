package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
)

func localAdminFixture(t *testing.T) (*adminFixture, *store.User) {
	t.Helper()
	f := newAdminLoginFixture(t)
	f.svc.cfg.RuntimeEnvironment = "local"
	f.svc.cfg.AdminLocalPasswordLoginKey = strings.Repeat("k", 32)
	f.svc.cfg.AccessTokenTTL = 365 * 24 * time.Hour
	u := f.addUser(t, false)
	f.st.platformRoles[u.ID] = []string{"superadmin"}
	return f, u
}

func localSignIn(t *testing.T, f *adminFixture, u *store.User) *AuthResponse {
	t.Helper()
	ch, err := f.svc.AdminLogin(WithLocalAdminPasswordLogin(context.Background()), *u.Email, adminTestPassword, "", "")
	if err != nil || ch == nil || ch.LocalSession == nil || ch.Requires2FA {
		t.Fatalf("local login: %v %+v", err, ch)
	}
	return ch.LocalSession
}

func TestLocalAdminPasswordLoginHonestClaimsAndRefresh(t *testing.T) {
	f, u := localAdminFixture(t)
	// Merely configuring the key must not bypass the ordinary endpoint.
	if _, err := f.svc.AdminLogin(context.Background(), *u.Email, adminTestPassword, "", ""); !errors.Is(err, ErrTOTPNotEnrolled) {
		t.Fatalf("unkeyed request bypassed MFA: %v", err)
	}
	resp := localSignIn(t, f, u)
	c := parseClaims(t, resp.Tokens.AccessToken)
	if c.AdminMFA || hasAMR(c.AMR, AMROTP) || !hasAMR(c.AMR, AMRLocalAdminPassword) || c.SessionKind != accesstoken.SessionKindAdmin || !strings.Contains(c.Scopes, "superadmin") {
		t.Fatalf("incorrect local claims: %+v", c)
	}
	if ttlOf(c) > accesstoken.AdminSessionMaxTTL || !resp.LocalAdminPasswordLogin {
		t.Fatal("local admin token inherited long consumer TTL or was labelled MFA")
	}
	expires := f.st.byID[resp.SessionID].ExpiresAt
	if time.Until(expires) > time.Hour {
		t.Fatal("local session exceeds one hour")
	}
	rotated, err := f.svc.AdminRefreshSession(context.Background(), resp.Tokens.RefreshToken, "", "")
	if err != nil || !rotated.LocalAdminPasswordLogin || parseClaims(t, rotated.Tokens.AccessToken).AdminMFA {
		t.Fatalf("local refresh: %v", err)
	}
	if !f.st.byID[resp.SessionID].ExpiresAt.Equal(expires) {
		t.Fatal("refresh extended local lifetime")
	}
	if _, err := f.svc.RefreshSession(context.Background(), rotated.Tokens.RefreshToken, "", ""); !errors.Is(err, ErrWrongSessionKind) {
		t.Fatalf("local admin session escaped into consumer refresh: %v", err)
	}
	if err := f.svc.requireFreshAdminSession(WithSessionAuth(context.Background(), SessionAuth{SessionID: resp.SessionID}), u.ID); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("password session satisfied sensitive action MFA: %v", err)
	}
}

func TestLocalAdminPasswordLoginRetainsGuards(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*adminFixture, *store.User)
		password string
		want     error
	}{
		{"wrong password", func(*adminFixture, *store.User) {}, "wrong", ErrAdminInvalidCredentials},
		{"no role", func(f *adminFixture, u *store.User) { f.st.platformRoles[u.ID] = nil }, adminTestPassword, ErrNotAdmin},
		{"inactive", func(f *adminFixture, u *store.User) { u.AccountStatus = "suspended" }, adminTestPassword, ErrAdminAccountInactive},
		{"production", func(f *adminFixture, u *store.User) { f.svc.cfg.Production = true }, adminTestPassword, ErrTOTPNotEnrolled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, u := localAdminFixture(t)
			tc.change(f, u)
			_, err := f.svc.AdminLogin(WithLocalAdminPasswordLogin(context.Background()), *u.Email, tc.password, "", "")
			if !errors.Is(err, tc.want) || f.sessionCount() != 0 {
				t.Fatalf("guard: %v sessions=%d", err, f.sessionCount())
			}
		})
	}
}

func TestLocalAdminSessionRevokedOnDisableOrRoleLoss(t *testing.T) {
	for _, disable := range []bool{true, false} {
		f, u := localAdminFixture(t)
		resp := localSignIn(t, f, u)
		if disable {
			f.svc.cfg.AdminLocalPasswordLoginKey = ""
		} else {
			f.st.platformRoles[u.ID] = nil
		}
		if _, err := f.svc.AdminRefreshSession(context.Background(), resp.Tokens.RefreshToken, "", ""); !errors.Is(err, ErrAdminAccessLost) {
			t.Fatalf("refresh survived disable=%v: %v", disable, err)
		}
		if f.st.byID[resp.SessionID].RevokedAt == nil {
			t.Fatal("session not revoked")
		}
	}
}

func TestLocalAdminPasswordModeDoesNotUpgradeConsumerSessions(t *testing.T) {
	f, u := localAdminFixture(t)
	resp, err := f.svc.LoginWithPassword(context.Background(), *u.Email, adminTestPassword, "dev", "web", "", "")
	if err != nil {
		t.Fatal(err)
	}
	c := parseClaims(t, resp.Tokens.AccessToken)
	if c.AdminMFA || c.SessionKind == accesstoken.SessionKindAdmin || strings.Contains(c.Scopes, "admin") || hasAMR(c.AMR, AMRLocalAdminPassword) {
		t.Fatalf("local mode upgraded consumer login: %+v", c)
	}
}
