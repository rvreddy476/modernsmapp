package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// A13 anomaly enforcement tests.
//
// We exercise the policy classifier directly (pure function) and the
// service-level gate via a fake Store + miniredis. No Postgres is
// required — every Store method that the anomaly path touches is
// implemented by fakeAnomalyStore below and the bits that don't matter
// for these tests (OAuth, mini-app sessions, etc.) panic so a future
// regression that calls them is loud.

// --- Pure policy tests -----------------------------------------------------

func TestClassifyAnomalyRisk_LowRisk(t *testing.T) {
	// No new IP, no new device → low risk band (no enforcement).
	band := classifyAnomalyRisk("1.2.3.4", "1.2.3.4", false, false, false)
	if band != anomalyLow {
		t.Fatalf("expected anomalyLow, got %v", band)
	}
}

func TestClassifyAnomalyRisk_HighRisk_NewIPDifferentSubnetAndNewDevice(t *testing.T) {
	// The audit defines high risk as new IP on a different /24 AND new
	// device. This is the only branch that should land in step-up.
	band := classifyAnomalyRisk("10.0.0.5", "192.168.1.5", true, true, false)
	if band != anomalyHigh {
		t.Fatalf("expected anomalyHigh, got %v", band)
	}
}

func TestClassifyAnomalyRisk_MediumRisk_NewDeviceSameSubnet(t *testing.T) {
	// New device, but the IP is in the same /24 — common when a user
	// reinstalls the app on the same WiFi. Notify but don't gate.
	band := classifyAnomalyRisk("10.0.0.5", "10.0.0.99", true, true, false)
	if band != anomalyMedium {
		t.Fatalf("expected anomalyMedium, got %v", band)
	}
}

func TestClassifyAnomalyRisk_MediumRisk_NewIPOnly(t *testing.T) {
	// IP changed but device is still trusted — typical DHCP / VPN hop.
	band := classifyAnomalyRisk("10.0.0.5", "10.0.1.5", true, false, false)
	if band != anomalyMedium {
		t.Fatalf("expected anomalyMedium, got %v", band)
	}
}

func TestAllowedStepUpMethods_EmailAndTOTP(t *testing.T) {
	email := "u@example.test"
	u := &store.User{
		Email:            &email,
		EmailVerified:    true,
		TwoFactorEnabled: true,
	}
	methods := allowedStepUpMethods(u)
	if len(methods) != 2 {
		t.Fatalf("expected 2 methods, got %d (%v)", len(methods), methods)
	}
	if methods[0] != StepUpMethodEmail || methods[1] != StepUpMethod2FA {
		t.Fatalf("unexpected methods: %v", methods)
	}
}

func TestAllowedStepUpMethods_NoChannel(t *testing.T) {
	// No verified email, no 2FA — no step-up possible.
	u := &store.User{}
	methods := allowedStepUpMethods(u)
	if len(methods) != 0 {
		t.Fatalf("expected 0 methods for account with no channels, got %v", methods)
	}
}

// --- Service-level gate tests ---------------------------------------------

func newTestService(t *testing.T, enforceMode string) (*Service, *fakeAnomalyStore, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	cfg := &config.Config{
		AccessTokenTTL:      15 * time.Minute,
		RefreshTokenTTL:     30 * 24 * time.Hour,
		JWTSecret:           "test-secret-do-not-use-in-prod",
		OTPDigits:           6,
		OTPExpiry:           5 * time.Minute,
		OTPMaxAttempts:      5,
		BcryptCost:          4, // fast for tests
		LoginAnomalyEnforce: enforceMode,
	}

	fstore := &fakeAnomalyStore{
		anomalies: make([]store.LoginAnomaly, 0),
		sessions:  make([]storeSessionRecord, 0),
	}
	prod := &fakeProducer{}

	svc := New(fstore, prod, cfg, slog.Default(), rdb, nil)
	return svc, fstore, mr
}

// storeSessionRecord captures a CreateSession call so tests can assert
// whether or not a session was minted.
type storeSessionRecord struct {
	ID     uuid.UUID
	UserID uuid.UUID
}

// TestCreateSessionForUser_LowRiskShadowMode_IssuesSession verifies
// the regression baseline: when there's no anomaly signal (same IP,
// same device — actually a brand-new user with no last-IP) the
// session is minted and no step-up envelope is returned. Shadow mode
// is the default — exercise that explicitly here.
func TestCreateSessionForUser_LowRiskShadowMode_IssuesSession(t *testing.T) {
	svc, fstore, _ := newTestService(t, "shadow")

	user := &store.User{ID: uuid.New()}
	fstore.users = map[uuid.UUID]*store.User{user.ID: user}

	resp, err := svc.createSessionForUser(context.Background(), user, "dev-1", "ios", "10.0.0.5", "ua")
	if err != nil {
		t.Fatalf("createSessionForUser: unexpected err: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.RequiresStepUp {
		t.Fatal("did not expect RequiresStepUp on low-risk login")
	}
	if resp.Tokens.AccessToken == "" {
		t.Fatal("expected an access token to be issued")
	}
	if len(fstore.sessions) != 1 {
		t.Fatalf("expected 1 session row, got %d", len(fstore.sessions))
	}
}

// TestCreateSessionForUser_HighRiskEnforceMode_BlocksSession is the
// primary A13 enforcement test: enforce=on AND the signal is high-risk
// (new IP on a different /24 AND new device) → the service must
// return the step-up envelope, the ErrAnomalyStepUpRequired sentinel,
// and MUST NOT mint a session.
func TestCreateSessionForUser_HighRiskEnforceMode_BlocksSession(t *testing.T) {
	svc, fstore, mr := newTestService(t, "enforce")

	email := "u@example.test"
	user := &store.User{
		ID:               uuid.New(),
		Email:            &email,
		EmailVerified:    true,
		TwoFactorEnabled: false,
	}
	fstore.users = map[uuid.UUID]*store.User{user.ID: user}

	// Seed a prior last_ip so the new login looks novel. Different /24.
	mr.Set("last_ip:"+user.ID.String(), "203.0.113.10")

	// No trusted devices: device id "new-device" will look novel.
	resp, err := svc.createSessionForUser(context.Background(), user, "new-device-1", "ios", "198.51.100.99", "ua")

	if err == nil {
		t.Fatal("expected ErrAnomalyStepUpRequired, got nil")
	}
	if !errorsIs(err, ErrAnomalyStepUpRequired) {
		t.Fatalf("expected ErrAnomalyStepUpRequired, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected step-up envelope, got nil")
	}
	if !resp.RequiresStepUp {
		t.Fatal("expected RequiresStepUp=true on the envelope")
	}
	if resp.PendingToken == "" {
		t.Fatal("expected a pending_token in the envelope")
	}
	if len(resp.StepUpMethods) == 0 {
		t.Fatal("expected at least one StepUpMethod (email_otp) for verified-email user")
	}
	if resp.Tokens.AccessToken != "" {
		t.Fatal("must NOT issue an access token on a gated step-up")
	}
	if len(fstore.sessions) != 0 {
		t.Fatalf("must NOT create a session row on a gated step-up; got %d", len(fstore.sessions))
	}
	// last_ip MUST NOT advance — otherwise the next attempt would see
	// the same IP as already-known and bypass the gate.
	if v, _ := mr.Get("last_ip:" + user.ID.String()); v != "203.0.113.10" {
		t.Fatalf("last_ip advanced to %q despite step-up gate (kill the regression)", v)
	}
}

// TestCreateSessionForUser_HighRiskShadowMode_IssuesSessionAndRecordsAnomaly
// confirms the kill switch works: with LOGIN_ANOMALY_ENFORCE=shadow
// (default), the same high-risk attempt still mints a session AND
// records an anomaly row, exactly as the legacy behaviour did.
func TestCreateSessionForUser_HighRiskShadowMode_IssuesSessionAndRecordsAnomaly(t *testing.T) {
	svc, fstore, mr := newTestService(t, "shadow")

	email := "u@example.test"
	user := &store.User{
		ID:            uuid.New(),
		Email:         &email,
		EmailVerified: true,
	}
	fstore.users = map[uuid.UUID]*store.User{user.ID: user}

	// Same high-risk signal as the enforce test.
	mr.Set("last_ip:"+user.ID.String(), "203.0.113.10")

	resp, err := svc.createSessionForUser(context.Background(), user, "new-device-1", "ios", "198.51.100.99", "ua")
	if err != nil {
		t.Fatalf("shadow mode must not block: %v", err)
	}
	if resp == nil || resp.Tokens.AccessToken == "" {
		t.Fatal("shadow mode must mint a session")
	}
	if resp.RequiresStepUp {
		t.Fatal("shadow mode must NOT return RequiresStepUp")
	}
	if len(fstore.sessions) != 1 {
		t.Fatalf("expected 1 session row, got %d", len(fstore.sessions))
	}
	if len(fstore.anomalies) == 0 {
		t.Fatal("expected an anomaly audit row in shadow mode (telemetry preserved)")
	}
	// In shadow mode the last_ip cache DOES advance — legacy behaviour.
	if v, _ := mr.Get("last_ip:" + user.ID.String()); v != "198.51.100.99" {
		t.Fatalf("expected last_ip to advance to new IP in shadow mode, got %q", v)
	}
}

// errorsIs is a tiny local wrapper to avoid importing errors at the top
// (we already have one).
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// --- fake collaborators ----------------------------------------------------

type fakeProducer struct{}

func (p *fakeProducer) PublishUserRegistered(_ context.Context, _ uuid.UUID, _ string, _ *string, _, _, _, _ string) error {
	return nil
}
func (p *fakeProducer) PublishUserLoggedIn(_ context.Context, _, _ uuid.UUID, _, _, _ string) error {
	return nil
}
func (p *fakeProducer) PublishRaw(_ context.Context, _, _ string, _ json.RawMessage) error {
	return nil
}

// fakeAnomalyStore implements the Store interface with the bare
// minimum needed by createSessionForUser + the anomaly probe. Methods
// not exercised by the tests panic so a future caller can't silently
// adopt this stub.
type fakeAnomalyStore struct {
	users     map[uuid.UUID]*store.User
	anomalies []store.LoginAnomaly
	sessions  []storeSessionRecord
}

func (f *fakeAnomalyStore) DB() *pgxpool.Pool { return nil }
func (f *fakeAnomalyStore) SaveOTP(_ context.Context, _, _, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeAnomalyStore) GetOTP(_ context.Context, _, _ string) (*store.OTP, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) IncrementOTPAttempts(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (f *fakeAnomalyStore) DeleteOTP(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeAnomalyStore) GetUserByPhone(_ context.Context, _ string) (*store.User, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) CreateUser(_ context.Context, _ string) (*store.User, error) {
	panic("not implemented")
}
func (f *fakeAnomalyStore) CreateUserTx(_ context.Context, _ pgx.Tx, _ string) (*store.User, error) {
	panic("not implemented")
}
func (f *fakeAnomalyStore) CreateUserWithPassword(_ context.Context, _, _, _ string) (*store.User, error) {
	panic("not implemented")
}
func (f *fakeAnomalyStore) CreateUserWithPasswordTx(_ context.Context, _ pgx.Tx, _, _, _ string) (*store.User, error) {
	panic("not implemented")
}
func (f *fakeAnomalyStore) GetUserByEmail(_ context.Context, _ string) (*store.User, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) GetUserByID(_ context.Context, id uuid.UUID) (*store.User, error) {
	if u, ok := f.users[id]; ok {
		return u, nil
	}
	return nil, nil
}
func (f *fakeAnomalyStore) UpdateLastLogin(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeAnomalyStore) SoftDeleteUser(_ context.Context, _ uuid.UUID) error  { return nil }
func (f *fakeAnomalyStore) UpdatePassword(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (f *fakeAnomalyStore) MarkEmailVerified(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeAnomalyStore) MarkPhoneVerified(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeAnomalyStore) GrantRole(_ context.Context, _, _ uuid.UUID, _ string) error { return nil }
func (f *fakeAnomalyStore) RevokeRole(_ context.Context, _ uuid.UUID, _ string) error    { return nil }
func (f *fakeAnomalyStore) RolesForUser(_ context.Context, _ uuid.UUID) ([]string, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) ListUserRoles(_ context.Context, _ uuid.UUID) ([]store.UserRole, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) InsertAdminAudit(_ context.Context, _, _ uuid.UUID, _, _ string, _ bool) error {
	return nil
}
func (f *fakeAnomalyStore) CreateSession(_ context.Context, sess *store.Session) error {
	f.sessions = append(f.sessions, storeSessionRecord{ID: sess.ID, UserID: sess.UserID})
	return nil
}
func (f *fakeAnomalyStore) GetSessionByRefreshTokenHash(_ context.Context, _ string) (*store.Session, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) GetSessionByID(_ context.Context, _ uuid.UUID) (*store.Session, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) ListActiveSessions(_ context.Context, _ uuid.UUID) ([]store.Session, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) RotateSessionRefreshToken(_ context.Context, _ uuid.UUID, _ string, _ time.Time) error {
	return nil
}
func (f *fakeAnomalyStore) RotateSessionWithFingerprint(_ context.Context, _ uuid.UUID, _, _ string, _ time.Time, _ bool) error {
	return nil
}
func (f *fakeAnomalyStore) RevokeSession(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeAnomalyStore) RevokeAllSessions(_ context.Context, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeAnomalyStore) RecordLoginAnomaly(_ context.Context, userID uuid.UUID, atype, ip, ua, deviceID, country string, risk int, challenged bool, metadata map[string]any) error {
	f.anomalies = append(f.anomalies, store.LoginAnomaly{
		ID:          uuid.New(),
		UserID:      userID,
		AnomalyType: atype,
		IP:          ip,
		UserAgent:   ua,
		DeviceID:    deviceID,
		CountryCode: country,
		Metadata:    metadata,
		RiskScore:   risk,
		Challenged:  challenged,
		OccurredAt:  time.Now(),
	})
	return nil
}
func (f *fakeAnomalyStore) ListLoginAnomalies(_ context.Context, _ uuid.UUID, _ int) ([]store.LoginAnomaly, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) AcknowledgeAnomaly(_ context.Context, _, _ uuid.UUID) (int64, error) {
	return 0, nil
}
func (f *fakeAnomalyStore) UpsertTrustedDevice(_ context.Context, _ *store.TrustedDevice) error {
	return nil
}
func (f *fakeAnomalyStore) ListTrustedDevices(_ context.Context, _ uuid.UUID) ([]store.TrustedDevice, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) DeleteTrustedDevice(_ context.Context, _, _ uuid.UUID) error { return nil }
func (f *fakeAnomalyStore) Enable2FA(_ context.Context, _ uuid.UUID, _ string) error    { return nil }
func (f *fakeAnomalyStore) Disable2FA(_ context.Context, _ uuid.UUID) error             { return nil }
func (f *fakeAnomalyStore) Get2FASecret(_ context.Context, _ uuid.UUID) (string, error) {
	return "", nil
}
func (f *fakeAnomalyStore) GetUserByLoginProvider(_ context.Context, _, _ string) (*store.User, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) CreateUserWithOAuth(_ context.Context, _, _, _ string) (*store.User, error) {
	panic("not implemented")
}
func (f *fakeAnomalyStore) CreateUserWithOAuthTx(_ context.Context, _ pgx.Tx, _, _, _ string) (*store.User, error) {
	panic("not implemented")
}
func (f *fakeAnomalyStore) CreateUserWithOAuthExtendedTx(_ context.Context, _ pgx.Tx, _, _, _, _ string, _, _ bool) (*store.User, error) {
	panic("not implemented")
}
func (f *fakeAnomalyStore) LinkOAuthProvider(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (f *fakeAnomalyStore) CreateUserRecordTx(_ context.Context, _ pgx.Tx, _ uuid.UUID) error {
	return nil
}
func (f *fakeAnomalyStore) CreateProfileTx(_ context.Context, _ pgx.Tx, _ uuid.UUID, _, _, _, _, _ string) error {
	return nil
}
func (f *fakeAnomalyStore) InsertOutboxEventTx(_ context.Context, _ pgx.Tx, _, _ string, _ interface{}) error {
	return nil
}
func (f *fakeAnomalyStore) FetchUnpublishedOutboxEvents(_ context.Context, _ int) ([]store.OutboxEvent, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) MarkOutboxEventPublished(_ context.Context, _ int64) error { return nil }
func (f *fakeAnomalyStore) StoreRecoveryCodes(_ context.Context, _ uuid.UUID, _ []string) error {
	return nil
}
func (f *fakeAnomalyStore) GetUnusedRecoveryCodes(_ context.Context, _ uuid.UUID) ([]store.RecoveryCode, error) {
	return nil, nil
}
func (f *fakeAnomalyStore) MarkRecoveryCodeUsed(_ context.Context, _ uuid.UUID) error { return nil }
