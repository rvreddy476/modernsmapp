package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// roleTestStore models auth.user_roles in memory. ChangeRole mirrors the real
// store's contract: guard runs first against the active superadmin rows, and
// a failing audit write leaves no change behind.
type roleTestStore struct {
	*fakeAnomalyStore
	roles     map[uuid.UUID][]string // platform-wide, used by IsSuperadmin
	grants    map[uuid.UUID][]store.RoleGrant
	twoFA     map[uuid.UUID]bool
	changes   []store.RoleChange
	audits    []string // "action:allowed"
	txAudits  []store.RoleAudit
	failAudit bool
}

func newRoleTestStore() *roleTestStore {
	return &roleTestStore{
		fakeAnomalyStore: &fakeAnomalyStore{},
		roles:            map[uuid.UUID][]string{},
		grants:           map[uuid.UUID][]store.RoleGrant{},
		twoFA:            map[uuid.UUID]bool{},
	}
}

func (r *roleTestStore) RolesForUser(_ context.Context, uid uuid.UUID) ([]string, error) {
	return r.roles[uid], nil
}
func (r *roleTestStore) RoleGrantsForUser(_ context.Context, uid uuid.UUID) ([]store.RoleGrant, error) {
	return r.grants[uid], nil
}
func (r *roleTestStore) GetUserByID(_ context.Context, uid uuid.UUID) (*store.User, error) {
	return &store.User{ID: uid, TwoFactorEnabled: r.twoFA[uid]}, nil
}
func (r *roleTestStore) InsertAdminAudit(_ context.Context, _, _ uuid.UUID, action, _ string, allowed bool) error {
	r.audits = append(r.audits, action+":"+map[bool]string{true: "ok", false: "denied"}[allowed])
	return nil
}
func (r *roleTestStore) ChangeRole(_ context.Context, ch store.RoleChange, audit store.RoleAudit,
	guard func([]store.SuperadminHolder) error) (bool, error) {
	if guard != nil {
		var holders []store.SuperadminHolder
		for uid, rs := range r.roles {
			for _, role := range rs {
				if role == "superadmin" {
					holders = append(holders, store.SuperadminHolder{UserID: uid})
				}
			}
		}
		if err := guard(holders); err != nil {
			return false, err
		}
	}
	if r.failAudit {
		return false, errors.New("audit insert failed")
	}
	r.changes = append(r.changes, ch)
	r.txAudits = append(r.txAudits, audit)
	r.audits = append(r.audits, audit.Action+":ok")
	return true, nil
}

func newRoleSvc(t *testing.T, st *roleTestStore, requireMFA bool) *Service {
	t.Helper()
	return New(st, nil, &config.Config{RequireMFAForPrivileged: requireMFA}, nil, nil, nil)
}

func grantReq(target uuid.UUID, role, app string) RoleChangeRequest {
	return RoleChangeRequest{TargetID: target, Role: role, App: app, Reason: "pilot moderator"}
}

func TestGrantRole_AuthzAndAudit(t *testing.T) {
	ctx := context.Background()
	super := uuid.New()
	normal := uuid.New()
	target := uuid.New()

	st := newRoleTestStore()
	svc := newRoleSvc(t, st, false)
	if err := svc.GrantRole(ctx, normal, grantReq(target, "admin", "")); !errors.Is(err, ErrNotSuperadmin) {
		t.Fatalf("non-superadmin grant: got %v want ErrNotSuperadmin", err)
	}
	if len(st.changes) != 0 || len(st.audits) != 1 || st.audits[0] != "role.grant:denied" {
		t.Fatalf("changes=%d audits=%v", len(st.changes), st.audits)
	}

	st = newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc = newRoleSvc(t, st, false)
	if err := svc.GrantRole(ctx, super, grantReq(target, "moderator", "dating")); err != nil {
		t.Fatalf("superadmin grant: %v", err)
	}
	if len(st.changes) != 1 || len(st.audits) != 1 || st.audits[0] != "role.grant:ok" {
		t.Fatalf("changes=%d audits=%v", len(st.changes), st.audits)
	}
	ch, au := st.changes[0], st.txAudits[0]
	if ch.App != "dating" || ch.Role != "moderator" || ch.Reason != "pilot moderator" || ch.GrantedBy != super {
		t.Fatalf("change = %+v", ch)
	}
	if au.ActorID != super || au.TargetID != target ||
		!strings.Contains(au.Detail, "app=dating") || !strings.Contains(au.Detail, "reason=pilot moderator") {
		t.Fatalf("audit = %+v", au)
	}

	st = newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc = newRoleSvc(t, st, true)
	if err := svc.GrantRole(ctx, super, grantReq(target, "admin", "")); !errors.Is(err, ErrMFARequired) {
		t.Fatalf("MFA-required grant w/o 2FA: got %v want ErrMFARequired", err)
	}
	if len(st.changes) != 0 || st.audits[0] != "role.grant:denied" {
		t.Fatalf("changes=%d audits=%v", len(st.changes), st.audits)
	}

	st = newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	st.twoFA[super] = true
	svc = newRoleSvc(t, st, true)
	if err := svc.GrantRole(ctx, super, grantReq(target, "admin", "")); err != nil {
		t.Fatalf("MFA-required grant w/ 2FA: %v", err)
	}
}

func TestGrantRole_Validation(t *testing.T) {
	ctx := context.Background()
	super := uuid.New()
	target := uuid.New()
	past := time.Now().Add(-time.Hour)

	cases := []struct {
		name string
		req  RoleChangeRequest
		want error
	}{
		{"unknown role", grantReq(target, "bogus", ""), ErrInvalidRole},
		{"customer is not a role", grantReq(target, "customer", ""), ErrInvalidRole},
		{"unknown app", grantReq(target, "moderator", "casino"), ErrInvalidApp},
		{"superadmin cannot be app-scoped", grantReq(target, "superadmin", "dating"), ErrRoleNotScopable},
		{"ecosystem role has no app", grantReq(target, "seller", "commerce"), ErrRoleNotScopable},
		{"finance holds nothing in qa", grantReq(target, "finance", "qa"), ErrRoleNotScopable},
		{"missing reason", RoleChangeRequest{TargetID: target, Role: "moderator", App: "dating"}, ErrReasonRequired},
		{"blank reason", RoleChangeRequest{TargetID: target, Role: "moderator", Reason: "   "}, ErrReasonRequired},
		{"expiry in the past", RoleChangeRequest{TargetID: target, Role: "moderator", Reason: "x", ExpiresAt: &past}, ErrInvalidExpiry},
		{"self-grant", grantReq(super, "moderator", "dating"), ErrSelfGrant},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newRoleTestStore()
			st.roles[super] = []string{"superadmin"}
			svc := newRoleSvc(t, st, false)
			if err := svc.GrantRole(ctx, super, tc.req); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if len(st.changes) != 0 {
				t.Fatalf("a refused grant wrote %d changes", len(st.changes))
			}
		})
	}
}

// TestGrantRole_SelfGrantAudited: the refusal leaves a denied record.
func TestGrantRole_SelfGrantAudited(t *testing.T) {
	super := uuid.New()
	st := newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc := newRoleSvc(t, st, false)
	if err := svc.GrantRole(context.Background(), super, grantReq(super, "admin", "")); !errors.Is(err, ErrSelfGrant) {
		t.Fatalf("got %v", err)
	}
	if len(st.audits) != 1 || st.audits[0] != "role.grant:denied" {
		t.Fatalf("audits=%v", st.audits)
	}
}

func TestRevokeRole_LastSuperadmin(t *testing.T) {
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()

	// The only superadmin revoking themselves: refused, nothing changed.
	st := newRoleTestStore()
	st.roles[a] = []string{"superadmin"}
	svc := newRoleSvc(t, st, false)
	if err := svc.RevokeRole(ctx, a, grantReq(a, "superadmin", "")); !errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("revoke last superadmin: got %v", err)
	}
	if len(st.changes) != 0 || len(st.audits) != 1 || st.audits[0] != "role.revoke:denied" {
		t.Fatalf("changes=%d audits=%v", len(st.changes), st.audits)
	}

	// Putting an expiry on the only durable superadmin is refused too. The
	// actor b passes authorisation but is not in the locked holder set, so a
	// is the only durable holder the guard sees.
	st = newRoleTestStore()
	st.roles[a] = []string{"superadmin"}
	svc = New(&superadminActor{roleTestStore: st, actor: b}, nil, &config.Config{}, nil, nil, nil)
	exp := time.Now().Add(24 * time.Hour)
	req := grantReq(a, "superadmin", "")
	req.ExpiresAt = &exp
	if err := svc.GrantRole(ctx, b, req); !errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("expiring the last durable superadmin: got %v", err)
	}

	// With a second durable holder, revoking one is allowed.
	st = newRoleTestStore()
	st.roles[a] = []string{"superadmin"}
	st.roles[b] = []string{"superadmin"}
	svc = newRoleSvc(t, st, false)
	if err := svc.RevokeRole(ctx, a, grantReq(b, "superadmin", "")); err != nil {
		t.Fatalf("revoke with another holder: %v", err)
	}
	if len(st.changes) != 1 || !st.changes[0].Revoke {
		t.Fatalf("changes=%+v", st.changes)
	}
}

// superadminActor makes actor a superadmin for authorisation without adding
// them to the locked holder set ChangeRole hands the guard.
type superadminActor struct {
	*roleTestStore
	actor uuid.UUID
}

func (s *superadminActor) RolesForUser(ctx context.Context, uid uuid.UUID) ([]string, error) {
	if uid == s.actor {
		return []string{"superadmin"}, nil
	}
	return s.roleTestStore.RolesForUser(ctx, uid)
}

func TestRevokeRole_EnvBootstrapRefused(t *testing.T) {
	super := uuid.New()
	envHolder := uuid.New()
	st := newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	svc := New(st, nil, &config.Config{
		ScopeAdminUserIDs: map[string]struct{}{envHolder.String(): {}},
	}, nil, nil, nil)

	for _, role := range []string{"admin", "moderator"} {
		err := svc.RevokeRole(context.Background(), super, grantReq(envHolder, role, ""))
		var envErr *EnvBootstrapRoleError
		if !errors.Is(err, ErrEnvBootstrapRole) || !errors.As(err, &envErr) || envErr.EnvVar != "ADMIN_USER_IDS" {
			t.Fatalf("revoke env %s: got %v", role, err)
		}
		if !strings.Contains(err.Error(), "cannot be revoked through the API") {
			t.Fatalf("error must say why: %q", err.Error())
		}
	}
	// An app-scoped DB grant of the same person is revocable.
	if err := svc.RevokeRole(context.Background(), super, grantReq(envHolder, "moderator", "dating")); err != nil {
		t.Fatalf("app-scoped revoke of env holder: %v", err)
	}
	if len(st.changes) != 1 {
		t.Fatalf("changes=%d", len(st.changes))
	}
}

// TestGrantRole_AuditFailureFailsGrant: the store's transaction reports the
// audit failure and the service surfaces it — no silent success.
func TestGrantRole_AuditFailureFailsGrant(t *testing.T) {
	super, target := uuid.New(), uuid.New()
	st := newRoleTestStore()
	st.roles[super] = []string{"superadmin"}
	st.failAudit = true
	svc := newRoleSvc(t, st, false)
	if err := svc.GrantRole(context.Background(), super, grantReq(target, "moderator", "dating")); err == nil {
		t.Fatal("grant must fail when its audit row cannot be written")
	}
	if len(st.changes) != 0 {
		t.Fatal("grant recorded despite audit failure")
	}
}

func TestPermissionsForUser_EnvAndScopedGrants(t *testing.T) {
	envMod := uuid.New()
	datingMod := uuid.New()
	st := newRoleTestStore()
	past := time.Now().Add(-time.Minute)
	st.grants[datingMod] = []store.RoleGrant{
		{Role: "moderator", App: "dating"},
		{Role: "admin", App: "food", ExpiresAt: &past}, // expired: grants nothing
	}
	svc := New(st, nil, &config.Config{
		ScopeModeratorUserIDs: map[string]struct{}{envMod.String(): {}},
	}, nil, nil, nil)

	got, err := svc.PermissionsForUser(context.Background(), datingMod)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Has("dating:reports.act") || got.Has("food:restaurant.approve") || len(got.Apps) != 1 {
		t.Fatalf("dating moderator map = %+v", got)
	}

	got, _ = svc.PermissionsForUser(context.Background(), envMod)
	if !got.Has("dating:reports.act") || !got.Has("food:reviews.moderate") {
		t.Fatalf("env platform moderator must reach every app: %+v", got)
	}
}
