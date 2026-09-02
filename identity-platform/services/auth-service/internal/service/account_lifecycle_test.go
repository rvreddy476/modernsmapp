package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ── Pure state machine ──────────────────────────────────────────────────────

func TestResolveLoginLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * 24 * time.Hour)
	past := now.Add(-time.Second)

	cases := []struct {
		name       string
		status     string
		purge      *time.Time
		wantAction lifecycleAction
		wantErr    error
	}{
		{"active proceeds", store.AccountStatusActive, nil, lifecycleProceed, nil},
		{"suspended proceeds (own gate later)", store.AccountStatusSuspended, nil, lifecycleProceed, nil},
		{"pending_verification proceeds (LB-5 gate runs first)", store.AccountStatusPendingVerification, nil, lifecycleProceed, nil},
		{"unknown status proceeds", "weird", nil, lifecycleProceed, nil},
		{"deactivated reactivates", store.AccountStatusDeactivated, nil, lifecycleReactivate, nil},
		{"pending_deletion inside window cancels", store.AccountStatusPendingDeletion, &future, lifecycleCancelDeletion, nil},
		{"pending_deletion at exact boundary is closed", store.AccountStatusPendingDeletion, &now, lifecycleProceed, ErrAccountPendingPurge},
		{"pending_deletion past window fails closed", store.AccountStatusPendingDeletion, &past, lifecycleProceed, ErrAccountPendingPurge},
		{"pending_deletion with NULL date (legacy SR-7 row) fails closed", store.AccountStatusPendingDeletion, nil, lifecycleProceed, ErrAccountPendingPurge},
		{"purged is terminal", store.AccountStatusPurged, nil, lifecycleProceed, ErrAccountPurged},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			action, err := resolveLoginLifecycle(tc.status, tc.purge, now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if action != tc.wantAction {
				t.Fatalf("action = %v, want %v", action, tc.wantAction)
			}
		})
	}
}

// ── Through LoginWithPassword with the fake store ───────────────────────────

const lifecycleTestPassword = "CallTest#2026"

func seedLifecycleUser(t *testing.T, f *fakeAnomalyStore, status string, purge *time.Time) *store.User {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(lifecycleTestPassword), 4)
	if err != nil {
		t.Fatal(err)
	}
	email := "lifecycle-" + uuid.NewString()[:8] + "@example.com"
	u := &store.User{
		ID:                 uuid.New(),
		Email:              &email,
		EmailVerified:      true,
		PasswordHash:       string(hash),
		AccountStatus:      status,
		ScheduledPurgeDate: purge,
	}
	f.users[u.ID] = u
	return u
}

func TestLoginWithPassword_LifecycleStateMachine(t *testing.T) {
	future := time.Now().Add(10 * 24 * time.Hour)
	past := time.Now().Add(-time.Minute)

	cases := []struct {
		name           string
		status         string
		purge          *time.Time
		wantErr        error
		wantStatus     string
		wantTransition string // last lifecycle op recorded, "" for none
		wantSession    bool
	}{
		{"deactivated → reactivated + session", store.AccountStatusDeactivated, nil, nil, store.AccountStatusActive, "reactivate", true},
		{"pending_deletion in window → cancelled + session", store.AccountStatusPendingDeletion, &future, nil, store.AccountStatusActive, "cancel", true},
		{"pending_deletion past window → 403, untouched", store.AccountStatusPendingDeletion, &past, ErrAccountPendingPurge, store.AccountStatusPendingDeletion, "", false},
		{"purged → 403, untouched", store.AccountStatusPurged, nil, ErrAccountPurged, store.AccountStatusPurged, "", false},
		{"active → plain login", store.AccountStatusActive, nil, nil, store.AccountStatusActive, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fstore, _ := newTestService(t, "shadow")
			fstore.users = map[uuid.UUID]*store.User{}
			u := seedLifecycleUser(t, fstore, tc.status, tc.purge)

			resp, err := svc.LoginWithPassword(context.Background(), *u.Email, lifecycleTestPassword, "dev-1", "ios", "10.0.0.5", "ua")

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if u.AccountStatus != tc.wantStatus {
				t.Fatalf("status = %q, want %q", u.AccountStatus, tc.wantStatus)
			}
			last := ""
			if n := len(fstore.lifecycle); n > 0 {
				last = fstore.lifecycle[n-1]
			}
			if last != tc.wantTransition {
				t.Fatalf("transition = %q, want %q (all: %v)", last, tc.wantTransition, fstore.lifecycle)
			}
			gotSession := resp != nil && resp.Tokens.AccessToken != ""
			if gotSession != tc.wantSession {
				t.Fatalf("session issued = %v, want %v", gotSession, tc.wantSession)
			}
			if !tc.wantSession && len(fstore.sessions) != 0 {
				t.Fatalf("a session row was created for a refused login: %+v", fstore.sessions)
			}
		})
	}
}

// A wrong password must be the same generic failure regardless of lifecycle
// state, and must not trigger any transition — otherwise the login endpoint
// becomes an oracle for "is this account deactivated?" and an attacker could
// reactivate accounts without the password.
func TestLoginWithPassword_WrongPasswordNeverTransitions(t *testing.T) {
	for _, status := range []string{store.AccountStatusDeactivated, store.AccountStatusPendingDeletion, store.AccountStatusPurged} {
		t.Run(status, func(t *testing.T) {
			svc, fstore, _ := newTestService(t, "shadow")
			fstore.users = map[uuid.UUID]*store.User{}
			future := time.Now().Add(24 * time.Hour)
			u := seedLifecycleUser(t, fstore, status, &future)

			_, err := svc.LoginWithPassword(context.Background(), *u.Email, "not-the-password", "dev-1", "ios", "10.0.0.5", "ua")
			if err == nil || err.Error() != "invalid credentials" {
				t.Fatalf("err = %v, want generic invalid credentials", err)
			}
			if errors.Is(err, ErrAccountPurged) || errors.Is(err, ErrAccountPendingPurge) {
				t.Fatal("lifecycle state leaked through a wrong-password attempt")
			}
			if u.AccountStatus != status || len(fstore.lifecycle) != 0 {
				t.Fatalf("transition happened without the password: status=%q ops=%v", u.AccountStatus, fstore.lifecycle)
			}
		})
	}
}

// ── Deactivate / Delete service methods ─────────────────────────────────────

func TestDeactivateAccount_ReverifiesPasswordAndRevokesSessions(t *testing.T) {
	svc, fstore, _ := newTestService(t, "shadow")
	fstore.users = map[uuid.UUID]*store.User{}
	u := seedLifecycleUser(t, fstore, store.AccountStatusActive, nil)

	// Wrong password: refused, nothing revoked, nothing flipped.
	err := svc.DeactivateAccount(context.Background(), u.ID, "wrong")
	if !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("err = %v, want ErrInvalidPassword", err)
	}
	if fstore.revokeAllCalls != 0 || u.AccountStatus != store.AccountStatusActive {
		t.Fatalf("mutated on a refused request: revokes=%d status=%q", fstore.revokeAllCalls, u.AccountStatus)
	}

	// Right password: every session revoked, then the flip.
	if err := svc.DeactivateAccount(context.Background(), u.ID, lifecycleTestPassword); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if fstore.revokeAllCalls != 1 {
		t.Fatalf("RevokeAllSessions calls = %d, want 1", fstore.revokeAllCalls)
	}
	if u.AccountStatus != store.AccountStatusDeactivated || u.DeactivatedAt == nil {
		t.Fatalf("status=%q deactivated_at=%v", u.AccountStatus, u.DeactivatedAt)
	}

	// Deactivating again is a state conflict, not a silent success.
	if err := svc.DeactivateAccount(context.Background(), u.ID, lifecycleTestPassword); !errors.Is(err, ErrLifecycleConflict) {
		t.Fatalf("second deactivate err = %v, want ErrLifecycleConflict", err)
	}
}

func TestDeleteAccount_SchedulesThirtyDaysOutAndRevokesSessions(t *testing.T) {
	svc, fstore, _ := newTestService(t, "shadow")
	fstore.users = map[uuid.UUID]*store.User{}
	u := seedLifecycleUser(t, fstore, store.AccountStatusActive, nil)

	if _, err := svc.DeleteAccount(context.Background(), u.ID, "wrong"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("err = %v, want ErrInvalidPassword", err)
	}
	if fstore.revokeAllCalls != 0 || u.AccountStatus != store.AccountStatusActive {
		t.Fatal("mutated on a refused request")
	}

	before := time.Now()
	sched, err := svc.DeleteAccount(context.Background(), u.ID, lifecycleTestPassword)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if fstore.revokeAllCalls != 1 {
		t.Fatalf("RevokeAllSessions calls = %d, want 1", fstore.revokeAllCalls)
	}
	if sched.AccountStatus != store.AccountStatusPendingDeletion || !sched.CancelByLoggingIn {
		t.Fatalf("schedule = %+v", sched)
	}
	gap := sched.ScheduledPurgeDate.Sub(before)
	if gap < store.DeletionGracePeriod-time.Minute || gap > store.DeletionGracePeriod+time.Minute {
		t.Fatalf("purge date %v is not ~30 days out (gap %v)", sched.ScheduledPurgeDate, gap)
	}
	if u.AccountStatus != store.AccountStatusPendingDeletion || u.ScheduledPurgeDate == nil {
		t.Fatalf("status=%q purge=%v", u.AccountStatus, u.ScheduledPurgeDate)
	}

	// The rescue: logging in inside the window cancels and mints a session.
	resp, err := svc.LoginWithPassword(context.Background(), *u.Email, lifecycleTestPassword, "dev-1", "ios", "10.0.0.5", "ua")
	if err != nil || resp == nil || resp.Tokens.AccessToken == "" {
		t.Fatalf("rescue login: err=%v resp=%+v", err, resp)
	}
	if u.AccountStatus != store.AccountStatusActive || u.ScheduledPurgeDate != nil {
		t.Fatalf("deletion not cancelled: status=%q purge=%v", u.AccountStatus, u.ScheduledPurgeDate)
	}
}

// A deactivated user may still choose permanent deletion.
func TestDeleteAccount_FromDeactivated(t *testing.T) {
	svc, fstore, _ := newTestService(t, "shadow")
	fstore.users = map[uuid.UUID]*store.User{}
	u := seedLifecycleUser(t, fstore, store.AccountStatusDeactivated, nil)

	if _, err := svc.DeleteAccount(context.Background(), u.ID, lifecycleTestPassword); err != nil {
		t.Fatalf("delete from deactivated: %v", err)
	}
	if u.AccountStatus != store.AccountStatusPendingDeletion || u.DeactivatedAt != nil {
		t.Fatalf("status=%q deactivated_at=%v", u.AccountStatus, u.DeactivatedAt)
	}
}

// ── Refresh honours account_status ──────────────────────────────────────────

func TestRefreshSession_DeniedForNonActiveAccount(t *testing.T) {
	for _, status := range []string{store.AccountStatusDeactivated, store.AccountStatusPendingDeletion, store.AccountStatusPurged, store.AccountStatusSuspended} {
		t.Run(status, func(t *testing.T) {
			svc, fstore, _ := newTestService(t, "shadow")
			fstore.users = map[uuid.UUID]*store.User{}
			u := seedLifecycleUser(t, fstore, status, nil)

			rs := &refreshStatusStore{fakeAnomalyStore: fstore, sess: &store.Session{
				ID: uuid.New(), UserID: u.ID, IP: "10.0.0.5", UserAgent: "ua",
				IsActive: true, ExpiresAt: time.Now().Add(time.Hour),
			}}
			svc.store = rs

			_, err := svc.RefreshSession(context.Background(), "some-refresh-token", "10.0.0.5", "ua")
			if err == nil {
				t.Fatal("refresh succeeded for a non-active account")
			}
			if !rs.revoked {
				t.Fatal("the live session was not burned on a denied refresh")
			}
		})
	}
}

// refreshStatusStore lends the fake a live session so RefreshSession reaches
// the account_status guard.
type refreshStatusStore struct {
	*fakeAnomalyStore
	sess    *store.Session
	revoked bool
}

func (r *refreshStatusStore) GetSessionByRefreshTokenHash(_ context.Context, _ string) (*store.Session, error) {
	return r.sess, nil
}
func (r *refreshStatusStore) RevokeSession(_ context.Context, _ uuid.UUID) error {
	r.revoked = true
	return nil
}
