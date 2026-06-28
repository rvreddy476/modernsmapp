package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// roleTestStore embeds the full-interface fake and overrides only what the
// privileged-action gate touches, so we can drive IsSuperadmin (via roles) and
// the MFA check (via the user's TwoFactorEnabled) and observe audit writes.
type roleTestStore struct {
	*fakeAnomalyStore
	roles    map[uuid.UUID][]string
	twoFA    map[uuid.UUID]bool
	grants   int
	audits   []string // "action:allowed"
}

func (r *roleTestStore) RolesForUser(_ context.Context, uid uuid.UUID) ([]string, error) {
	return r.roles[uid], nil
}
func (r *roleTestStore) GetUserByID(_ context.Context, uid uuid.UUID) (*store.User, error) {
	return &store.User{ID: uid, TwoFactorEnabled: r.twoFA[uid]}, nil
}
func (r *roleTestStore) GrantRole(_ context.Context, _, _ uuid.UUID, _ string) error {
	r.grants++
	return nil
}
func (r *roleTestStore) InsertAdminAudit(_ context.Context, _, _ uuid.UUID, action, _ string, allowed bool) error {
	r.audits = append(r.audits, action+":"+map[bool]string{true: "ok", false: "denied"}[allowed])
	return nil
}

func newRoleSvc(t *testing.T, st *roleTestStore, requireMFA bool) *Service {
	t.Helper()
	return New(st, nil, &config.Config{RequireMFAForPrivileged: requireMFA}, nil, nil, nil)
}

func TestGrantRole_AuthzAndAudit(t *testing.T) {
	super := uuid.New()
	normal := uuid.New()
	target := uuid.New()

	// Non-superadmin is rejected and the denied attempt is audited.
	st := &roleTestStore{fakeAnomalyStore: &fakeAnomalyStore{}, roles: map[uuid.UUID][]string{}, twoFA: map[uuid.UUID]bool{}}
	svc := newRoleSvc(t, st, false)
	if err := svc.GrantRole(context.Background(), normal, target, "admin"); !errors.Is(err, ErrNotSuperadmin) {
		t.Fatalf("non-superadmin grant: got %v want ErrNotSuperadmin", err)
	}
	if st.grants != 0 {
		t.Fatal("grant should not have happened for non-superadmin")
	}
	if len(st.audits) != 1 || st.audits[0] != "role.grant:denied" {
		t.Fatalf("audits=%v want [role.grant:denied]", st.audits)
	}

	// Superadmin, MFA not required → grant succeeds and is audited.
	st = &roleTestStore{fakeAnomalyStore: &fakeAnomalyStore{}, roles: map[uuid.UUID][]string{super: {"superadmin"}}, twoFA: map[uuid.UUID]bool{}}
	svc = newRoleSvc(t, st, false)
	if err := svc.GrantRole(context.Background(), super, target, "moderator"); err != nil {
		t.Fatalf("superadmin grant: %v", err)
	}
	if st.grants != 1 || len(st.audits) != 1 || st.audits[0] != "role.grant:ok" {
		t.Fatalf("grants=%d audits=%v", st.grants, st.audits)
	}

	// Superadmin, MFA required but NOT enrolled → blocked + audited.
	st = &roleTestStore{fakeAnomalyStore: &fakeAnomalyStore{}, roles: map[uuid.UUID][]string{super: {"superadmin"}}, twoFA: map[uuid.UUID]bool{}}
	svc = newRoleSvc(t, st, true)
	if err := svc.GrantRole(context.Background(), super, target, "admin"); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("MFA-required grant w/o 2FA: got %v want ErrMFARequired", err)
	}
	if st.grants != 0 || st.audits[0] != "role.grant:denied" {
		t.Fatalf("grants=%d audits=%v", st.grants, st.audits)
	}

	// Superadmin, MFA required AND enrolled → grant succeeds.
	st = &roleTestStore{fakeAnomalyStore: &fakeAnomalyStore{}, roles: map[uuid.UUID][]string{super: {"superadmin"}}, twoFA: map[uuid.UUID]bool{super: true}}
	svc = newRoleSvc(t, st, true)
	if err := svc.GrantRole(context.Background(), super, target, "admin"); err != nil {
		t.Fatalf("MFA-required grant w/ 2FA: %v", err)
	}
	if st.grants != 1 || st.audits[0] != "role.grant:ok" {
		t.Fatalf("grants=%d audits=%v", st.grants, st.audits)
	}

	// Invalid role is rejected before any authz/audit.
	st = &roleTestStore{fakeAnomalyStore: &fakeAnomalyStore{}, roles: map[uuid.UUID][]string{super: {"superadmin"}}, twoFA: map[uuid.UUID]bool{}}
	svc = newRoleSvc(t, st, false)
	if err := svc.GrantRole(context.Background(), super, target, "bogus"); err == nil {
		t.Fatal("invalid role should error")
	}
}
