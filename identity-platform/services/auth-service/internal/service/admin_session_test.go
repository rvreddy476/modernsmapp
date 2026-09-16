package service

// Admin sessions, mandatory 2FA and step-up (A2) — unit tests over an
// in-memory session store and miniredis.

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-auth-service/pkg/accesstoken"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

const adminTestJWTSecret = "a2-unit-test-secret"

// adminSessionStore keeps sessions, grants and TOTP secrets in memory.
type adminSessionStore struct {
	*fakeAnomalyStore
	platformRoles map[uuid.UUID][]string
	grants        map[uuid.UUID][]store.RoleGrant
	secrets       map[uuid.UUID]string
	byID          map[uuid.UUID]*store.Session
	candidates    []store.HolderCandidate
	forceAudits   []store.RoleAudit
}

func newAdminSessionStore() *adminSessionStore {
	return &adminSessionStore{
		fakeAnomalyStore: &fakeAnomalyStore{users: map[uuid.UUID]*store.User{}},
		platformRoles:    map[uuid.UUID][]string{},
		grants:           map[uuid.UUID][]store.RoleGrant{},
		secrets:          map[uuid.UUID]string{},
		byID:             map[uuid.UUID]*store.Session{},
	}
}

func (a *adminSessionStore) RolesForUser(_ context.Context, uid uuid.UUID) ([]string, error) {
	return a.platformRoles[uid], nil
}
func (a *adminSessionStore) RoleGrantsForUser(_ context.Context, uid uuid.UUID) ([]store.RoleGrant, error) {
	out := append([]store.RoleGrant(nil), a.grants[uid]...)
	for _, r := range a.platformRoles[uid] {
		out = append(out, store.RoleGrant{Role: r})
	}
	return out, nil
}
func (a *adminSessionStore) Get2FASecret(_ context.Context, uid uuid.UUID) (string, error) {
	return a.secrets[uid], nil
}
func (a *adminSessionStore) CreateSession(ctx context.Context, sess *store.Session) error {
	cp := *sess
	cp.IsActive = true
	a.byID[sess.ID] = &cp
	return a.fakeAnomalyStore.CreateSession(ctx, sess)
}
func (a *adminSessionStore) GetSessionByID(_ context.Context, id uuid.UUID) (*store.Session, error) {
	if s, ok := a.byID[id]; ok {
		cp := *s
		return &cp, nil
	}
	return nil, nil
}
func (a *adminSessionStore) GetSessionByRefreshTokenHash(_ context.Context, h string) (*store.Session, error) {
	for _, s := range a.byID {
		if s.RefreshToken == h {
			cp := *s
			return &cp, nil
		}
	}
	return nil, nil
}
func (a *adminSessionStore) RotateSessionWithFingerprint(_ context.Context, id uuid.UUID, h, _ string, exp time.Time, _ bool) error {
	a.byID[id].RefreshToken = h
	a.byID[id].ExpiresAt = exp
	return nil
}
func (a *adminSessionStore) RevokeSession(_ context.Context, id uuid.UUID) error {
	now := time.Now()
	a.byID[id].RevokedAt = &now
	a.byID[id].IsActive = false
	return nil
}
func (a *adminSessionStore) AddSessionAMR(_ context.Context, id uuid.UUID, m string) error {
	s, ok := a.byID[id]
	if !ok || s.RevokedAt != nil {
		return store.ErrSessionNotLive
	}
	s.AMR = withAMR(s.AMR, m)
	return nil
}
func (a *adminSessionStore) RevokeAllSessionsAudited(_ context.Context, uid uuid.UUID, audit store.RoleAudit) ([]uuid.UUID, error) {
	var ids []uuid.UUID
	now := time.Now()
	for id, s := range a.byID {
		if s.UserID == uid && s.RevokedAt == nil {
			s.RevokedAt = &now
			ids = append(ids, id)
		}
	}
	a.forceAudits = append(a.forceAudits, audit)
	return ids, nil
}
func (a *adminSessionStore) AdminHolderCandidates(_ context.Context, _ []uuid.UUID) ([]store.HolderCandidate, error) {
	return a.candidates, nil
}

type adminFixture struct {
	svc *Service
	st  *adminSessionStore
	mr  *miniredis.Miniredis
}

// newAdminFixture uses a 24 h consumer access TTL, like the dev compose
// defaults, so the 15-minute admin cap is visible.
func newAdminFixture(t *testing.T) *adminFixture {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	st := newAdminSessionStore()
	cfg := &config.Config{
		AccessTokenTTL:  24 * time.Hour,
		RefreshTokenTTL: 30 * 24 * time.Hour,
		JWTSecret:       adminTestJWTSecret,
		BcryptCost:      4,
		OTPMaxAttempts:  5,
	}
	svc := New(st, &fakeProducer{}, cfg, slog.Default(), redis.NewClient(&redis.Options{Addr: mr.Addr()}), nil)
	return &adminFixture{svc: svc, st: st, mr: mr}
}

const adminTestPassword = "Str0ng!pass"

// addUser creates an active user who can log in with adminTestPassword.
func (f *adminFixture) addUser(t *testing.T, withTOTP bool) *store.User {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(adminTestPassword), 4)
	email := uuid.NewString() + "@example.test"
	u := &store.User{ID: uuid.New(), Email: &email, PasswordHash: string(hash), AccountStatus: store.AccountStatusActive}
	if withTOTP {
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "a2-test", AccountName: email})
		if err != nil {
			t.Fatal(err)
		}
		u.TwoFactorEnabled = true
		f.st.secrets[u.ID] = key.Secret()
	}
	f.st.users[u.ID] = u
	return u
}

func (f *adminFixture) code(t *testing.T, u *store.User, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCode(f.st.secrets[u.ID], at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func parseClaims(t *testing.T, token string) *accesstoken.Claims {
	t.Helper()
	claims := &accesstoken.Claims{}
	if _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (interface{}, error) {
		return []byte(adminTestJWTSecret), nil
	}); err != nil {
		t.Fatalf("parse token: %v", err)
	}
	return claims
}

func ttlOf(c *accesstoken.Claims) time.Duration { return c.ExpiresAt.Sub(c.IssuedAt.Time) }

func ctxFor(c *accesstoken.Claims) context.Context {
	sid, _ := uuid.Parse(c.SessionID)
	return WithSessionAuth(context.Background(), SessionAuth{
		SessionID: sid, AuthTime: c.AuthTime, AMR: c.AMR, AdminMFA: c.AdminMFA, StepUpAt: c.StepUpAt,
	})
}

// login signs in with the password and, when the account has TOTP, completes
// the 2FA step with a code for now.
func (f *adminFixture) login(t *testing.T, u *store.User) *AuthResponse {
	t.Helper()
	resp, err := f.svc.LoginWithPassword(context.Background(), *u.Email, adminTestPassword, "dev", "web", "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if resp.Requires2FA {
		resp, err = f.svc.Verify2FA(context.Background(), u.ID, f.code(t, u, time.Now()), resp.PendingToken)
		if err != nil {
			t.Fatalf("verify 2fa: %v", err)
		}
	}
	if resp.Tokens.AccessToken == "" {
		t.Fatalf("no session issued: %+v", resp)
	}
	return resp
}

// An admin without TOTP still signs in to consumer apps, but the token and
// capabilities carry no admin authority.
func TestAdminWithoutTOTP_ConsumerSessionOnly(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, false)
	f.st.platformRoles[u.ID] = []string{"superadmin", "seller"}

	resp := f.login(t, u)
	c := parseClaims(t, resp.Tokens.AccessToken)
	if c.AdminMFA {
		t.Fatal("admin without TOTP got admin_mfa=true")
	}
	if strings.Contains(c.Scopes, "admin") || strings.Contains(c.Scopes, "moderator") {
		t.Fatalf("admin scopes leaked into a non-MFA token: %q", c.Scopes)
	}
	if c.Scopes != "seller" {
		t.Fatalf("ecosystem scope must survive: %q", c.Scopes)
	}
	if len(c.AMR) != 1 || c.AMR[0] != AMRPassword || c.AuthTime == 0 {
		t.Fatalf("amr=%v auth_time=%d", c.AMR, c.AuthTime)
	}
	if ttlOf(c) != 24*time.Hour {
		t.Fatalf("consumer TTL changed: %v", ttlOf(c))
	}
	caps := f.svc.CapabilitiesForUser(ctxFor(c), u.ID)
	if !caps.Admin.MFARequired || caps.Admin.MFAEnrolled || !adminEmpty(caps.Admin.Admin) {
		t.Fatalf("capabilities admin = %+v", caps.Admin)
	}
}

// TOTP enrolled, but this session never completed an OTP challenge (passkey).
func TestAdminWithTOTPButNoOTPOnSession(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	f.st.grants[u.ID] = []store.RoleGrant{{Role: "finance", App: "payments"}}

	resp, err := f.svc.IssueSessionForUser(context.Background(), u.ID, "web", "web", "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	c := parseClaims(t, resp.Tokens.AccessToken)
	if c.AdminMFA || !hasAMR(c.AMR, AMRHardwareKey) || hasAMR(c.AMR, AMROTP) {
		t.Fatalf("admin_mfa=%v amr=%v", c.AdminMFA, c.AMR)
	}
	caps := f.svc.CapabilitiesForUser(ctxFor(c), u.ID)
	if !caps.Admin.MFARequired || !caps.Admin.MFAEnrolled || !adminEmpty(caps.Admin.Admin) {
		t.Fatalf("capabilities admin = %+v", caps.Admin)
	}
}

// Password + TOTP at login: admin_mfa=true, TTL capped at 15 minutes, and the
// admin map is visible. Also proves Verify2FA no longer loops back into the
// 2FA gate.
func TestAdminWithOTPLogin_AdminMFAAndShortTTL(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}

	resp := f.login(t, u)
	c := parseClaims(t, resp.Tokens.AccessToken)
	if !c.AdminMFA {
		t.Fatal("OTP login did not produce admin_mfa=true")
	}
	if len(c.AMR) != 2 || c.AMR[0] != AMRPassword || c.AMR[1] != AMROTP {
		t.Fatalf("amr=%v", c.AMR)
	}
	if ttlOf(c) > accesstoken.AdminSessionMaxTTL {
		t.Fatalf("admin TTL %v exceeds 15 minutes", ttlOf(c))
	}
	if !resp.Tokens.ExpiresAt.Before(time.Now().Add(16 * time.Minute)) {
		t.Fatalf("response expires_at %v does not reflect the admin TTL", resp.Tokens.ExpiresAt)
	}
	if !strings.Contains(c.Scopes, "superadmin") {
		t.Fatalf("MFA admin lost scopes: %q", c.Scopes)
	}
	if c.StepUpAt != 0 {
		t.Fatal("a login token must not carry step_up_at")
	}
	caps := f.svc.CapabilitiesForUser(ctxFor(c), u.ID)
	if caps.Admin.MFARequired || !caps.Admin.Has("platform:roles.manage") {
		t.Fatalf("capabilities admin = %+v", caps.Admin)
	}
}

// A recovery code signs in but never yields admin MFA.
func TestRecoveryCodeLoginIsNotAdminMFA(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"admin"}
	// Seed one recovery code in Redis the way promoteRecoveryCodes stores it.
	hash, _ := bcrypt.GenerateFromPassword([]byte("RECOVERYCODE1"), 4)
	f.mr.Set(recoveryCodesPrefix+u.ID.String(), `["`+string(hash)+`"]`)

	pending, err := f.svc.LoginWithPassword(context.Background(), *u.Email, adminTestPassword, "dev", "web", "10.0.0.5", "Mozilla/5.0")
	if err != nil || !pending.Requires2FA {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	resp, err := f.svc.Verify2FA(context.Background(), u.ID, "RECOVERYCODE1", pending.PendingToken)
	if err != nil {
		t.Fatal(err)
	}
	c := parseClaims(t, resp.Tokens.AccessToken)
	if c.AdminMFA || hasAMR(c.AMR, AMROTP) || !hasAMR(c.AMR, AMRRecovery) {
		t.Fatalf("admin_mfa=%v amr=%v", c.AdminMFA, c.AMR)
	}
}

// Refresh keeps auth_time and amr from the session, drops step_up_at, and
// re-resolves roles and TOTP each time.
func TestRefreshKeepsAuthTimeAndAMR_AndRechecksAdmin(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	f.st.platformRoles[u.ID] = []string{"superadmin"}
	resp := f.login(t, u)

	// Age the session's authentication so "unchanged" is observable.
	past := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	f.st.byID[resp.SessionID].AuthTime = &past

	ref, err := f.svc.RefreshSession(context.Background(), resp.Tokens.RefreshToken, "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	c := parseClaims(t, ref.Tokens.AccessToken)
	if c.AuthTime != past.Unix() {
		t.Fatalf("auth_time %d, want %d (unchanged by refresh)", c.AuthTime, past.Unix())
	}
	if len(c.AMR) != 2 || c.AMR[1] != AMROTP || !c.AdminMFA || ttlOf(c) > accesstoken.AdminSessionMaxTTL {
		t.Fatalf("refresh amr=%v admin_mfa=%v ttl=%v", c.AMR, c.AdminMFA, ttlOf(c))
	}

	// Role removed: the next refresh is a consumer token.
	f.st.platformRoles[u.ID] = nil
	ref, err = f.svc.RefreshSession(context.Background(), ref.Tokens.RefreshToken, "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if c := parseClaims(t, ref.Tokens.AccessToken); c.AdminMFA || ttlOf(c) != 24*time.Hour || c.AuthTime != past.Unix() {
		t.Fatalf("after role removal admin_mfa=%v ttl=%v auth_time=%d", c.AdminMFA, ttlOf(c), c.AuthTime)
	}

	// Role back, TOTP disabled: still no admin MFA.
	f.st.platformRoles[u.ID] = []string{"superadmin"}
	u.TwoFactorEnabled = false
	ref, err = f.svc.RefreshSession(context.Background(), ref.Tokens.RefreshToken, "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	if c := parseClaims(t, ref.Tokens.AccessToken); c.AdminMFA {
		t.Fatal("TOTP disabled but refresh still minted admin_mfa=true")
	}
}

// Step-up: fresh token with step_up_at, upgrades the session's amr, refuses
// wrong, replayed and rate-limited attempts.
func TestStepUp(t *testing.T) {
	f := newAdminFixture(t)
	u := f.addUser(t, true)
	f.st.grants[u.ID] = []store.RoleGrant{{Role: "finance", App: "payments"}}
	resp, err := f.svc.IssueSessionForUser(context.Background(), u.ID, "web", "web", "10.0.0.5", "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.StepUp(context.Background(), u.ID, resp.SessionID, "000000"); !errors.Is(err, ErrInvalidOTP) {
		t.Fatalf("wrong code: got %v", err)
	}

	good := f.code(t, u, time.Now())
	before := time.Now().Unix()
	su, err := f.svc.StepUp(context.Background(), u.ID, resp.SessionID, good)
	if err != nil {
		t.Fatalf("step-up: %v", err)
	}
	c := parseClaims(t, su.AccessToken)
	if c.StepUpAt < before || c.StepUpAt > time.Now().Unix() {
		t.Fatalf("step_up_at=%d", c.StepUpAt)
	}
	if !c.AdminMFA || !hasAMR(c.AMR, AMROTP) || ttlOf(c) > accesstoken.AdminSessionMaxTTL || c.SessionID != resp.SessionID.String() {
		t.Fatalf("step-up token admin_mfa=%v amr=%v ttl=%v sid=%s", c.AdminMFA, c.AMR, ttlOf(c), c.SessionID)
	}
	if !su.StepUpValidUntil.Equal(su.StepUpAt.Add(300 * time.Second)) {
		t.Fatalf("valid until %v, step_up_at %v", su.StepUpValidUntil, su.StepUpAt)
	}
	if !hasAMR(f.st.byID[resp.SessionID].AMR, AMROTP) {
		t.Fatal("session amr not upgraded")
	}

	if _, err := f.svc.StepUp(context.Background(), u.ID, resp.SessionID, good); !errors.Is(err, ErrTOTPReplay) {
		t.Fatalf("replay: got %v", err)
	}

	// The same code is also burned for the login 2FA path (shared key).
	if err := f.svc.verifyTOTPOnce(context.Background(), u.ID, good); !errors.Is(err, ErrTOTPReplay) {
		t.Fatalf("cross-path replay: got %v", err)
	}

	// 5 attempts per 15 minutes per account: three used above.
	for i := 0; i < 2; i++ {
		if _, err := f.svc.StepUp(context.Background(), u.ID, resp.SessionID, "000000"); !errors.Is(err, ErrInvalidOTP) {
			t.Fatalf("attempt %d: got %v", i+4, err)
		}
	}
	if _, err := f.svc.StepUp(context.Background(), u.ID, resp.SessionID, f.code(t, u, time.Now().Add(30*time.Second))); err == nil {
		t.Fatal("6th attempt was not rate limited")
	} else if _, ok := AsThrottled(err); !ok {
		t.Fatalf("6th attempt: got %v, want throttled", err)
	}
}

func TestStepUp_Refusals(t *testing.T) {
	f := newAdminFixture(t)
	noTOTP := f.addUser(t, false)
	resp, _ := f.svc.IssueSessionForUser(context.Background(), noTOTP.ID, "web", "web", "", "")
	if _, err := f.svc.StepUp(context.Background(), noTOTP.ID, resp.SessionID, "123456"); !errors.Is(err, ErrTOTPNotEnrolled) {
		t.Fatalf("no TOTP: got %v", err)
	}

	u := f.addUser(t, true)
	resp, _ = f.svc.IssueSessionForUser(context.Background(), u.ID, "web", "web", "", "")
	if _, err := f.svc.StepUp(context.Background(), noTOTP.ID, resp.SessionID, f.code(t, u, time.Now())); !errors.Is(err, ErrSessionNotLive) {
		t.Fatalf("someone else's session: got %v", err)
	}
	_ = f.st.RevokeSession(context.Background(), resp.SessionID)
	if _, err := f.svc.StepUp(context.Background(), u.ID, resp.SessionID, f.code(t, u, time.Now())); !errors.Is(err, ErrSessionNotLive) {
		t.Fatalf("revoked session: got %v", err)
	}
}

// Force logout: gated like role changes, revokes every session, marks each
// sess_revoked, and hands the audit to the store transaction.
func TestForceLogout(t *testing.T) {
	f := newAdminFixture(t)
	super := f.addUser(t, true)
	f.st.platformRoles[super.ID] = []string{"superadmin"}
	target := f.addUser(t, false)
	r1, _ := f.svc.IssueSessionForUser(context.Background(), target.ID, "a", "web", "", "")
	r2, _ := f.svc.IssueSessionForUser(context.Background(), target.ID, "b", "web", "", "")

	mfaNoStepUp := WithSessionAuth(context.Background(), SessionAuth{AdminMFA: true})
	if _, err := f.svc.ForceLogout(mfaNoStepUp, super.ID, target.ID, "compromised"); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("without step-up: got %v", err)
	}
	if _, err := f.svc.ForceLogout(stepped(), super.ID, target.ID, "  "); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("blank reason: got %v", err)
	}
	if len(f.st.forceAudits) != 0 {
		t.Fatal("refused force logout reached the store")
	}

	n, err := f.svc.ForceLogout(stepped(), super.ID, target.ID, "account compromised")
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	for _, sid := range []uuid.UUID{r1.SessionID, r2.SessionID} {
		if v, _ := f.mr.Get("sess_revoked:" + sid.String()); v != "1" {
			t.Fatalf("session %s not marked revoked", sid)
		}
	}
	if len(f.st.forceAudits) != 1 || f.st.forceAudits[0].Action != "session.force_logout" ||
		f.st.forceAudits[0].ActorID != super.ID || f.st.forceAudits[0].Detail != "reason=account compromised" {
		t.Fatalf("audits=%+v", f.st.forceAudits)
	}
}

// Holders: others only, TOTP-enrolled only, permission resolved through the
// catalogue (env superadmins included).
func TestCountOtherHolders(t *testing.T) {
	f := newAdminFixture(t)
	requester, other, noTOTP, support, envSuper := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	f.svc.cfg.ScopeSuperadminUserIDs = map[string]struct{}{envSuper.String(): {}}
	finance := []store.RoleGrant{{Role: "finance", App: "payments"}}
	f.st.candidates = []store.HolderCandidate{
		{UserID: requester, TwoFactorEnabled: true, Grants: finance},
		{UserID: other, TwoFactorEnabled: true, Grants: finance},
		{UserID: noTOTP, TwoFactorEnabled: false, Grants: finance},
		{UserID: support, TwoFactorEnabled: true, Grants: []store.RoleGrant{{Role: "support", App: "payments"}}},
		{UserID: envSuper, TwoFactorEnabled: true},
	}

	n, err := f.svc.CountOtherHolders(context.Background(), "payments:refund.issue", requester)
	if err != nil || n != 2 {
		t.Fatalf("count=%d err=%v, want 2 (other + env superadmin)", n, err)
	}
	// From the other holder's side the requester counts.
	if n, _ := f.svc.CountOtherHolders(context.Background(), "payments:refund.issue", other); n != 2 {
		t.Fatalf("count excluding other = %d, want 2", n)
	}
	// Only the superadmin holds roles.manage.
	if n, _ := f.svc.CountOtherHolders(context.Background(), "platform:roles.manage", envSuper); n != 0 {
		t.Fatalf("roles.manage others = %d, want 0", n)
	}
	for _, bad := range []string{"", "refund.issue", "casino:refund.issue", "payments:", "payments:Refund Issue"} {
		if _, err := f.svc.CountOtherHolders(context.Background(), bad, requester); !errors.Is(err, ErrInvalidPermission) {
			t.Fatalf("%q: got %v", bad, err)
		}
	}
}

func TestStepUpFresh(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		at   int64
		want bool
	}{
		{0, false},
		{now.Unix(), true},
		{now.Add(-300 * time.Second).Unix(), true},
		{now.Add(-301 * time.Second).Unix(), false},
		{now.Add(2 * time.Minute).Unix(), false},
	} {
		if got := accesstoken.StepUpFresh(tc.at, now); got != tc.want {
			t.Fatalf("StepUpFresh(%d) = %v want %v", tc.at-now.Unix(), got, tc.want)
		}
	}
}
