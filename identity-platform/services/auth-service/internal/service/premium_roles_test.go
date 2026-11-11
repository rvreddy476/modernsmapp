package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Momentum Premium (short-posts design 3.1, 11 Nov 2026): monetization-service
// grants the timed role `premium` through the internal role API, always with
// a future expires_at. These tests pin the service-layer rules; the SQL upsert
// and CHECK are covered by premium_roles_integration_test.go.

const monetization = "monetization-service"

// timedRow is one platform-wide auth.user_roles row as the fake keeps it.
type timedRow struct {
	expiresAt *time.Time
	reason    string
}

// premiumStore models auth.user_roles with expiry, mirroring store.ChangeRole:
// a grant is an upsert that REPLACES expires_at; a shortening, or a revoke of
// a live row (or of a lapsed one unless LapsedRevokeKeepsSessions), revokes
// the user's sessions; RolesForUser returns only unexpired rows, as the SQL
// predicate does.
type premiumStore struct {
	*ecosystemStore
	rows         map[uuid.UUID]map[string]timedRow
	liveSessions map[uuid.UUID][]uuid.UUID
	changeCalls  int
	txAudits     []store.RoleAudit
	now          func() time.Time
}

func newPremiumStore() *premiumStore {
	return &premiumStore{
		ecosystemStore: newEcosystemStore(),
		rows:           map[uuid.UUID]map[string]timedRow{},
		liveSessions:   map[uuid.UUID][]uuid.UUID{},
		now:            time.Now,
	}
}

func (p *premiumStore) active(r timedRow) bool {
	return r.expiresAt == nil || r.expiresAt.After(p.now())
}

func (p *premiumStore) ChangeRole(_ context.Context, ch store.RoleChange, audit store.RoleAudit,
	_ func([]store.SuperadminHolder) error) (store.RoleChangeResult, error) {
	p.changeCalls++
	if ch.App != "" {
		return store.RoleChangeResult{}, errors.New("timed roles are platform-wide")
	}
	if p.rows[ch.UserID] == nil {
		p.rows[ch.UserID] = map[string]timedRow{}
	}
	prev, exists := p.rows[ch.UserID][ch.Role]
	var res store.RoleChangeResult
	reduces := false
	if ch.Revoke {
		if exists {
			delete(p.rows[ch.UserID], ch.Role)
			res.Changed = true
			reduces = p.active(prev) || !ch.LapsedRevokeKeepsSessions
		}
	} else {
		reduces = exists && store.ExpiryShortened(prev.expiresAt, ch.ExpiresAt)
		p.rows[ch.UserID][ch.Role] = timedRow{expiresAt: ch.ExpiresAt, reason: ch.Reason}
		res.Changed = true
	}
	if reduces {
		res.RevokedSessions = p.liveSessions[ch.UserID]
		delete(p.liveSessions, ch.UserID)
	}
	p.txAudits = append(p.txAudits, audit)
	return res, nil
}

func (p *premiumStore) RolesForUser(ctx context.Context, userID uuid.UUID) ([]string, error) {
	out, err := p.ecosystemStore.RolesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	for role, row := range p.rows[userID] {
		if p.active(row) {
			out = append(out, role)
		}
	}
	return out, nil
}

func premiumSvc(t *testing.T, st *premiumStore) (*Service, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return New(st, nil, &config.Config{AccessTokenTTL: 15 * time.Minute}, nil, rdb, nil), mr
}

func at(d time.Duration) *time.Time {
	v := time.Now().Add(d).UTC().Truncate(time.Second)
	return &v
}

// TestPremiumRequiresFutureExpiry: no expiry is 400 EXPIRES_AT_REQUIRED, a
// past or present one is 400 INVALID_EXPIRY, and neither reaches the store.
func TestPremiumRequiresFutureExpiry(t *testing.T) {
	target := uuid.New()
	zero := time.Time{}
	for _, tc := range []struct {
		name string
		exp  *time.Time
		want error
	}{
		{"absent", nil, ErrExpiryRequired},
		{"zero", &zero, ErrExpiryRequired},
		{"past", at(-time.Hour), ErrInvalidExpiry},
		{"now", at(0), ErrInvalidExpiry},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newPremiumStore()
			svc, _ := premiumSvc(t, st)
			err := svc.GrantEcosystemRole(context.Background(), monetization, target, roles.Premium, "pass", tc.exp)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if st.changeCalls != 0 || st.grantCalls != 0 {
				t.Fatal("a refused premium grant reached the store")
			}
			if len(st.svcAudits) != 1 || !strings.HasSuffix(st.svcAudits[0], "|denied") {
				t.Fatalf("audits=%v — a refused grant must leave a denied record", st.svcAudits)
			}
		})
	}
}

// TestPremiumGrantThenExtend: the grant is an upsert that replaces
// expires_at, so a second pass extends the role. An extension revokes nothing.
func TestPremiumGrantThenExtend(t *testing.T) {
	st := newPremiumStore()
	svc, mr := premiumSvc(t, st)
	target, sid := uuid.New(), uuid.New()
	st.liveSessions[target] = []uuid.UUID{sid}
	ctx := context.Background()

	first, later := at(30*24*time.Hour), at(60*24*time.Hour)
	if err := svc.GrantEcosystemRole(ctx, monetization, target, roles.Premium, "pass PP-1", first); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if err := svc.GrantEcosystemRole(ctx, monetization, target, roles.Premium, "pass PP-2", later); err != nil {
		t.Fatalf("extend: %v", err)
	}
	row := st.rows[target][roles.Premium]
	if row.expiresAt == nil || !row.expiresAt.Equal(*later) || row.reason != "pass PP-2" {
		t.Fatalf("row = %+v, want expiry %v reason PP-2 — the extension did not land", row, later)
	}
	if st.grantCalls != 0 {
		t.Fatal("premium went through GrantRole (ON CONFLICT DO NOTHING): an extension would be lost")
	}
	if len(st.liveSessions[target]) != 1 || mr.Exists("sess_revoked:"+sid.String()) {
		t.Fatal("an extension must not revoke sessions")
	}
	if len(st.txAudits) != 2 || st.txAudits[1].ActorService != monetization ||
		st.txAudits[1].ActorID != uuid.Nil || st.txAudits[1].Action != "role.grant" ||
		!strings.Contains(st.txAudits[1].Detail, "expires_at="+later.Format(time.RFC3339)) {
		t.Fatalf("audits = %+v — the service must be the actor and the expiry recorded", st.txAudits)
	}
	// Same expiry again: idempotent success.
	if err := svc.GrantEcosystemRole(ctx, monetization, target, roles.Premium, "pass PP-2", later); err != nil {
		t.Fatalf("re-grant same expiry: %v", err)
	}
	if len(st.liveSessions[target]) != 1 {
		t.Fatal("re-granting the same expiry revoked sessions")
	}
}

// TestPremiumShorterExpiryRevokesSessions: a refund that moves the expiry
// earlier revokes the user's sessions and marks each sess_revoked for the
// gateway, so a token minted under the longer period cannot keep the scope.
func TestPremiumShorterExpiryRevokesSessions(t *testing.T) {
	st := newPremiumStore()
	svc, mr := premiumSvc(t, st)
	target, s1, s2 := uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()

	if err := svc.GrantEcosystemRole(ctx, monetization, target, roles.Premium, "annual", at(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st.liveSessions[target] = []uuid.UUID{s1, s2}
	if err := svc.GrantEcosystemRole(ctx, monetization, target, roles.Premium, "partial refund", at(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []uuid.UUID{s1, s2} {
		if v, _ := mr.Get("sess_revoked:" + sid.String()); v != "1" {
			t.Fatalf("session %s not marked revoked after a shortening", sid)
		}
	}
}

// TestPremiumExpiredNotInScopes: an expired premium row never reaches the
// token's `scopes`; an active one does.
func TestPremiumExpiredNotInScopes(t *testing.T) {
	st := newPremiumStore()
	svc, _ := premiumSvc(t, st)
	target := uuid.New()
	ctx := context.Background()

	if err := svc.GrantEcosystemRole(ctx, monetization, target, roles.Premium, "pass", at(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := svc.resolveScopes(ctx, target); got != "premium" {
		t.Fatalf("scopes = %q, want premium while the pass is live", got)
	}
	st.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if got := svc.resolveScopes(ctx, target); strings.Contains(got, "premium") {
		t.Fatalf("scopes = %q after expiry — a lapsed member kept the scope", got)
	}
	if caps := svc.CapabilitiesForUser(ctx, target); caps.Capabilities["premium"] {
		t.Fatal("capabilities still say premium after expiry")
	}
}

// TestPremiumRevoke: the owning service can revoke premium; a live row's
// revoke signs the user out everywhere, a lapsed row's revoke does not.
func TestPremiumRevoke(t *testing.T) {
	ctx := context.Background()

	t.Run("live", func(t *testing.T) {
		st := newPremiumStore()
		svc, mr := premiumSvc(t, st)
		target, sid := uuid.New(), uuid.New()
		if err := svc.GrantEcosystemRole(ctx, monetization, target, roles.Premium, "pass", at(time.Hour)); err != nil {
			t.Fatal(err)
		}
		st.liveSessions[target] = []uuid.UUID{sid}
		if err := svc.RevokeEcosystemRole(ctx, monetization, target, roles.Premium, "refunded"); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, ok := st.rows[target][roles.Premium]; ok {
			t.Fatal("premium row survived the revoke")
		}
		if v, _ := mr.Get("sess_revoked:" + sid.String()); v != "1" {
			t.Fatal("revoking a live premium row did not revoke sessions")
		}
		if got := svc.ResolveRoles(ctx, target); len(got) != 0 {
			t.Fatalf("roles after revoke = %v", got)
		}
		// Idempotent.
		if err := svc.RevokeEcosystemRole(ctx, monetization, target, roles.Premium, "again"); err != nil {
			t.Fatalf("second revoke: %v", err)
		}
	})

	t.Run("already lapsed", func(t *testing.T) {
		st := newPremiumStore()
		svc, mr := premiumSvc(t, st)
		target, sid := uuid.New(), uuid.New()
		if err := svc.GrantEcosystemRole(ctx, monetization, target, roles.Premium, "pass", at(time.Hour)); err != nil {
			t.Fatal(err)
		}
		st.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
		st.liveSessions[target] = []uuid.UUID{sid}
		if err := svc.RevokeEcosystemRole(ctx, monetization, target, roles.Premium, "period ended"); err != nil {
			t.Fatal(err)
		}
		if mr.Exists("sess_revoked:" + sid.String()) {
			t.Fatal("revoking an already-lapsed premium row signed the member out")
		}
	})
}

// TestPremiumOnlyFromMonetization: any other calling service is refused with
// ErrTimedRoleWrongService (a 403 ROLE_NOT_GRANTABLE), on grant and revoke.
func TestPremiumOnlyFromMonetization(t *testing.T) {
	target := uuid.New()
	ctx := context.Background()
	for _, caller := range []string{"commerce-service", "food-service", "Monetization-Service", "monetization"} {
		st := newPremiumStore()
		svc, _ := premiumSvc(t, st)
		err := svc.GrantEcosystemRole(ctx, caller, target, roles.Premium, "", at(time.Hour))
		if !errors.Is(err, ErrTimedRoleWrongService) || !errors.Is(err, ErrRoleNotGrantableByService) {
			t.Fatalf("grant from %q: got %v, want ErrTimedRoleWrongService", caller, err)
		}
		if err := svc.RevokeEcosystemRole(ctx, caller, target, roles.Premium, ""); !errors.Is(err, ErrTimedRoleWrongService) {
			t.Fatalf("revoke from %q: got %v", caller, err)
		}
		if st.changeCalls != 0 {
			t.Fatalf("a refused caller %q reached the store", caller)
		}
	}
}

// TestEcosystemRolesUnchangedByPremium: ecosystem grants keep their old path
// (ON CONFLICT DO NOTHING, no expiry), and an expiry on one is refused rather
// than silently ignored. Monetization gets no wider authority.
func TestEcosystemRolesUnchangedByPremium(t *testing.T) {
	target := uuid.New()
	ctx := context.Background()
	for _, role := range roles.Ecosystem() {
		st := newPremiumStore()
		svc, _ := premiumSvc(t, st)
		if err := svc.GrantEcosystemRole(ctx, "commerce-service", target, role, "", nil); err != nil {
			t.Fatalf("grant %q: %v", role, err)
		}
		if st.grantCalls != 1 || st.changeCalls != 0 {
			t.Fatalf("grant %q took the wrong store path (grant=%d change=%d)", role, st.grantCalls, st.changeCalls)
		}
		if err := svc.GrantEcosystemRole(ctx, "commerce-service", target, role, "", at(time.Hour)); !errors.Is(err, ErrExpiryNotAllowed) {
			t.Fatalf("grant %q with expiry: got %v, want ErrExpiryNotAllowed", role, err)
		}
	}
	for _, role := range []string{"superadmin", "admin", "moderator", "finance"} {
		st := newPremiumStore()
		svc, _ := premiumSvc(t, st)
		if err := svc.GrantEcosystemRole(ctx, monetization, target, role, "", at(time.Hour)); !errors.Is(err, ErrRoleNotGrantableByService) {
			t.Fatalf("monetization granting %q: got %v", role, err)
		}
	}
}
