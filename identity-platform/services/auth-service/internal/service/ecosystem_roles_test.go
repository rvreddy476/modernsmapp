package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// ecosystemStore records what the service layer actually wrote, including the
// audit rows and — crucially — WHICH audit method was used, because a service
// actor must never be recorded as though a user had acted.
type ecosystemStore struct {
	*fakeAnomalyStore
	// granted models auth.user_roles, keyed the same way the real table is:
	// PRIMARY KEY (user_id, role). Re-granting therefore cannot duplicate.
	granted map[uuid.UUID]map[string]bool
	// grantCalls counts INSERT attempts (not rows), so an idempotent second
	// grant is visible as "called twice, one row".
	grantCalls   int
	revokeCalls  int
	userAudits   []string // "action:allowed" — written via InsertAdminAudit
	svcAudits    []string // "service|action|detail|allowed"
	dbRoles      map[uuid.UUID][]string
	rolesErr     error
	grantErr     error
	revokeErr    error
	auditWriteEr error
}

func newEcosystemStore() *ecosystemStore {
	return &ecosystemStore{
		fakeAnomalyStore: &fakeAnomalyStore{},
		granted:          map[uuid.UUID]map[string]bool{},
		dbRoles:          map[uuid.UUID][]string{},
	}
}

func (e *ecosystemStore) GrantRole(_ context.Context, userID, _ uuid.UUID, role string) error {
	e.grantCalls++
	if e.grantErr != nil {
		return e.grantErr
	}
	if !store.ValidRole(role) {
		return errors.New("invalid role: " + role)
	}
	if e.granted[userID] == nil {
		e.granted[userID] = map[string]bool{}
	}
	e.granted[userID][role] = true // ON CONFLICT DO NOTHING
	return nil
}

func (e *ecosystemStore) RevokeRole(_ context.Context, userID uuid.UUID, role string) error {
	e.revokeCalls++
	if e.revokeErr != nil {
		return e.revokeErr
	}
	delete(e.granted[userID], role)
	return nil
}

func (e *ecosystemStore) RolesForUser(_ context.Context, userID uuid.UUID) ([]string, error) {
	if e.rolesErr != nil {
		return nil, e.rolesErr
	}
	if len(e.dbRoles[userID]) > 0 {
		return e.dbRoles[userID], nil
	}
	var out []string
	for r := range e.granted[userID] {
		out = append(out, r)
	}
	return out, nil
}

func (e *ecosystemStore) InsertAdminAudit(_ context.Context, _, _ uuid.UUID, action, _ string, allowed bool) error {
	e.userAudits = append(e.userAudits, action+":"+allowedWord(allowed))
	return e.auditWriteEr
}

func (e *ecosystemStore) InsertServiceAudit(_ context.Context, _ uuid.UUID, svc, action, detail string, allowed bool) error {
	e.svcAudits = append(e.svcAudits, strings.Join([]string{svc, action, detail, allowedWord(allowed)}, "|"))
	return e.auditWriteEr
}

func allowedWord(ok bool) string {
	if ok {
		return "ok"
	}
	return "denied"
}

func newEcosystemSvc(t *testing.T, st *ecosystemStore) *Service {
	t.Helper()
	return New(st, nil, &config.Config{}, nil, nil, nil)
}

// TestServiceCannotMintPlatformRoles is the whole security boundary of the
// internal endpoint.
//
// The internal API is authenticated by one shared key that every service in
// the cluster holds. If it could grant `admin`, then any of those services —
// or anything that ever obtains the key — could hand platform authority to an
// arbitrary account. The worst it may do is make someone a seller.
func TestServiceCannotMintPlatformRoles(t *testing.T) {
	target := uuid.New()
	for _, role := range []string{"superadmin", "admin", "moderator"} {
		st := newEcosystemStore()
		svc := newEcosystemSvc(t, st)

		err := svc.GrantEcosystemRole(context.Background(), "commerce-service", target, role, "")
		if !errors.Is(err, ErrRoleNotGrantableByService) {
			t.Fatalf("grant %q: got %v, want ErrRoleNotGrantableByService", role, err)
		}
		if st.grantCalls != 0 {
			t.Fatalf("grant %q reached the store — the check must happen BEFORE the write", role)
		}
		if len(st.svcAudits) != 1 || !strings.HasSuffix(st.svcAudits[0], "|denied") {
			t.Fatalf("grant %q: audits=%v — a service asking for a platform role is either "+
				"a bug or an attack and must leave a record", role, st.svcAudits)
		}

		// Revoke is the same boundary: a service must not be able to strip a
		// superadmin either.
		st = newEcosystemStore()
		svc = newEcosystemSvc(t, st)
		if err := svc.RevokeEcosystemRole(context.Background(), "rider-service", target, role, ""); !errors.Is(err, ErrRoleNotGrantableByService) {
			t.Fatalf("revoke %q: got %v, want ErrRoleNotGrantableByService", role, err)
		}
		if st.revokeCalls != 0 {
			t.Fatalf("revoke %q reached the store", role)
		}
	}

	// And nothing outside the vocabulary at all.
	for _, role := range []string{"customer", "bogus", "", "SELLER"} {
		st := newEcosystemStore()
		svc := newEcosystemSvc(t, st)
		if err := svc.GrantEcosystemRole(context.Background(), "food-service", target, role, ""); !errors.Is(err, ErrRoleNotGrantableByService) {
			t.Fatalf("grant %q: got %v, want ErrRoleNotGrantableByService", role, err)
		}
	}
}

func TestGrantEcosystemRole_AllFourAllowed(t *testing.T) {
	target := uuid.New()
	for _, role := range roles.Ecosystem() {
		st := newEcosystemStore()
		svc := newEcosystemSvc(t, st)
		if err := svc.GrantEcosystemRole(context.Background(), "commerce-service", target, role, ""); err != nil {
			t.Fatalf("grant %q: %v", role, err)
		}
		if !st.granted[target][role] {
			t.Fatalf("grant %q did not reach the store", role)
		}
	}
}

// TestGrantEcosystemRole_Idempotent — an approval flow that retries, or a
// backfill run twice, must not fail.
func TestGrantEcosystemRole_Idempotent(t *testing.T) {
	st := newEcosystemStore()
	svc := newEcosystemSvc(t, st)
	target := uuid.New()

	for i := 0; i < 3; i++ {
		if err := svc.GrantEcosystemRole(context.Background(), "commerce-service", target, "seller", "SA-1042"); err != nil {
			t.Fatalf("grant #%d: %v", i+1, err)
		}
	}
	if st.grantCalls != 3 {
		t.Fatalf("grantCalls=%d want 3", st.grantCalls)
	}
	if len(st.granted[target]) != 1 || !st.granted[target]["seller"] {
		t.Fatalf("granted=%v — (user_id, role) is the primary key; three grants are one row", st.granted[target])
	}
	if len(st.svcAudits) != 3 {
		t.Fatalf("svcAudits=%v — each accepted call is still an audited event", st.svcAudits)
	}
}

// TestRevokeEcosystemRole covers the suspension path, including revoking a
// role the user never held (also a success).
func TestRevokeEcosystemRole(t *testing.T) {
	st := newEcosystemStore()
	svc := newEcosystemSvc(t, st)
	target := uuid.New()

	if err := svc.GrantEcosystemRole(context.Background(), "commerce-service", target, "seller", ""); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := svc.RevokeEcosystemRole(context.Background(), "commerce-service", target, "seller", "policy violation"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if st.granted[target]["seller"] {
		t.Fatal("seller still granted after revoke")
	}
	// Revoking again is a success, not an error.
	if err := svc.RevokeEcosystemRole(context.Background(), "commerce-service", target, "seller", ""); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
}

// TestServiceRoleAuditNamesTheService — the actor of a service-driven change
// is the service, recorded through InsertServiceAudit (actor_id NULL +
// actor_service). It must never land in the user-actor audit path, which would
// require a uuid that does not honestly exist.
func TestServiceRoleAuditNamesTheService(t *testing.T) {
	st := newEcosystemStore()
	svc := newEcosystemSvc(t, st)
	target := uuid.New()

	if err := svc.GrantEcosystemRole(context.Background(), "food-service", target, "restaurant_owner", "onboarded RP-77"); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if len(st.userAudits) != 0 {
		t.Fatalf("userAudits=%v — a service is not a user and must not be written as one", st.userAudits)
	}
	want := "food-service|role.grant|role=restaurant_owner reason=onboarded RP-77|ok"
	if len(st.svcAudits) != 1 || st.svcAudits[0] != want {
		t.Fatalf("svcAudits=%v want [%s]", st.svcAudits, want)
	}
}

// TestEcosystemRoleRequiresCallingService — the service name is what the audit
// row records as the actor, so a blank one would produce an unattributable
// row. Refused before anything is written.
func TestEcosystemRoleRequiresCallingService(t *testing.T) {
	st := newEcosystemStore()
	svc := newEcosystemSvc(t, st)
	target := uuid.New()

	for _, name := range []string{"", "   "} {
		if err := svc.GrantEcosystemRole(context.Background(), name, target, "seller", ""); !errors.Is(err, ErrCallingServiceRequired) {
			t.Fatalf("grant with service=%q: got %v, want ErrCallingServiceRequired", name, err)
		}
		if err := svc.RevokeEcosystemRole(context.Background(), name, target, "seller", ""); !errors.Is(err, ErrCallingServiceRequired) {
			t.Fatalf("revoke with service=%q: got %v, want ErrCallingServiceRequired", name, err)
		}
	}
	if st.grantCalls != 0 || st.revokeCalls != 0 || len(st.svcAudits) != 0 {
		t.Fatal("an unattributed call must not write anything at all")
	}
}

// TestResolveRolesUnionsEnvAndDB — the read path /v1/auth/me and
// /v1/auth/me/capabilities both use.
func TestResolveRolesUnionsEnvAndDB(t *testing.T) {
	target := uuid.New()
	st := newEcosystemStore()
	st.dbRoles[target] = []string{"seller"}
	svc := New(st, nil, &config.Config{
		ScopeAdminUserIDs: map[string]struct{}{target.String(): {}},
	}, nil, nil, nil)

	got := svc.ResolveRoles(context.Background(), target)
	want := []string{"admin", "moderator", "seller"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveRoles = %v, want %v (env admin ∪ db seller, expanded)", got, want)
	}
	// And the token claim is the same list, space-separated.
	if scopes := svc.resolveScopes(context.Background(), target); scopes != "admin moderator seller" {
		t.Fatalf("resolveScopes = %q, want %q", scopes, "admin moderator seller")
	}
}

// TestResolveRolesDegradesToEnvOnDBError — a roles-table outage must not lock
// admins out or block ordinary logins, matching the mint path's behaviour.
func TestResolveRolesDegradesToEnvOnDBError(t *testing.T) {
	target := uuid.New()
	st := newEcosystemStore()
	st.rolesErr = errors.New("connection refused")
	svc := New(st, nil, &config.Config{
		ScopeSuperadminUserIDs: map[string]struct{}{target.String(): {}},
	}, nil, nil, nil)

	got := svc.ResolveRoles(context.Background(), target)
	want := []string{"superadmin", "admin", "moderator"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResolveRoles on db error = %v, want %v", got, want)
	}
}

// TestCapabilitiesForUser — the switcher payload. is_customer is a constant
// true and `capabilities` names every role, held or not.
func TestCapabilitiesForUser(t *testing.T) {
	target := uuid.New()
	st := newEcosystemStore()
	st.dbRoles[target] = []string{"seller", "moderator"}
	svc := newEcosystemSvc(t, st)

	caps := svc.CapabilitiesForUser(context.Background(), target)

	if !caps.IsCustomer {
		t.Fatal("is_customer must be true unconditionally — every account is a customer")
	}
	if caps.UserID != target.String() {
		t.Fatalf("user_id=%q want %q", caps.UserID, target)
	}
	if want := []string{"moderator", "seller"}; !reflect.DeepEqual(caps.Roles, want) {
		t.Fatalf("roles=%v want %v", caps.Roles, want)
	}
	if len(caps.Capabilities) != len(roles.All()) {
		t.Fatalf("capabilities has %d entries, want one per role (%d) so a client "+
			"never has to know which roles exist", len(caps.Capabilities), len(roles.All()))
	}
	if _, ok := caps.Capabilities["customer"]; ok {
		t.Fatal("capabilities must not contain a `customer` key — it is not a role")
	}
	for role, want := range map[string]bool{
		"seller": true, "moderator": true,
		"superadmin": false, "admin": false, "restaurant_owner": false,
		"delivery_partner": false, "rider_partner": false,
	} {
		if caps.Capabilities[role] != want {
			t.Fatalf("capabilities[%q]=%v want %v", role, caps.Capabilities[role], want)
		}
	}
	// Switcher: the customer hat first, then each role held, in canonical
	// order, with human wording.
	wantSwitcher := []CapabilitySwitch{
		{Role: "customer", Label: "Customer"},
		{Role: "moderator", Label: "Moderator"},
		{Role: "seller", Label: "Seller"},
	}
	if !reflect.DeepEqual(caps.Switcher, wantSwitcher) {
		t.Fatalf("switcher=%v want %v", caps.Switcher, wantSwitcher)
	}
}

// TestCapabilitiesForRolelessUser — the common case. A plain account is a
// customer and nothing else, and the payload has to say so without nulls.
func TestCapabilitiesForRolelessUser(t *testing.T) {
	st := newEcosystemStore()
	caps := newEcosystemSvc(t, st).CapabilitiesForUser(context.Background(), uuid.New())

	if caps.Roles == nil {
		t.Fatal("roles must serialise as [] not null")
	}
	if len(caps.Roles) != 0 {
		t.Fatalf("roles=%v want empty", caps.Roles)
	}
	if !caps.IsCustomer {
		t.Fatal("a roleless account is still a customer")
	}
	if len(caps.Switcher) != 1 || caps.Switcher[0].Role != CustomerSwitchRole {
		t.Fatalf("switcher=%v want just the customer row", caps.Switcher)
	}
	for _, v := range caps.Capabilities {
		if v {
			t.Fatalf("capabilities=%v — a roleless account holds nothing", caps.Capabilities)
		}
	}
}

// TestSuperadminIsNotASeller restates the no-implication rule at the layer a
// caller actually touches, not just in the roles package.
func TestSuperadminIsNotASeller(t *testing.T) {
	target := uuid.New()
	st := newEcosystemStore()
	svc := New(st, nil, &config.Config{
		ScopeSuperadminUserIDs: map[string]struct{}{target.String(): {}},
	}, nil, nil, nil)

	caps := svc.CapabilitiesForUser(context.Background(), target)
	for _, r := range roles.Ecosystem() {
		if caps.Capabilities[r] {
			t.Fatalf("a superadmin came out as %q — platform roles must not imply ecosystem roles", r)
		}
	}
}
