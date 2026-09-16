package store

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/identity-auth-service/database"
	"github.com/atpost/identity-auth-service/internal/permissions"
	"github.com/atpost/identity-auth-service/internal/roles"
)

// The role vocabulary is enforced in three places that must agree:
//
//	database/setup.sql   auth.user_roles CHECK constraint
//	internal/roles       the Go list everything else derives from
//	internal/store       ValidRole / ValidEcosystemRole (thin wrappers)
//
// Go can derive two of those from one list. The CHECK constraint is SQL text
// and cannot be, so this test reads it back out of the embedded schema and
// compares. Without it, adding a role in Go produces a service that accepts
// the grant and a database that rejects it with 23514 — at the first real
// approval, in production.

var roleCheckRE = regexp.MustCompile(`(?is)role\s+TEXT\s+NOT\s+NULL\s+CHECK\s*\(\s*role\s+IN\s*\(([^)]*)\)`)

var alterCheckRE = regexp.MustCompile(
	`(?is)ALTER\s+TABLE\s+auth\.user_roles\s+ADD\s+CONSTRAINT\s+user_roles_role_check\s+CHECK\s*\(\s*role\s+IN\s*\(([^)]*)\)`)

func TestSetupSQLCheckMatchesVocabulary(t *testing.T) {
	want := append([]string(nil), roles.All()...)
	sort.Strings(want)

	m := roleCheckRE.FindStringSubmatch(database.SetupSQL)
	if m == nil {
		t.Fatal("could not find the auth.user_roles inline CHECK in database/setup.sql — " +
			"if the DDL was restructured, update this test rather than deleting it")
	}
	if got := parseSQLStringList(m[1]); !reflect.DeepEqual(got, want) {
		t.Fatalf("CREATE TABLE CHECK lists %v, vocabulary is %v", got, want)
	}
}

// The inline CHECK above only applies to a database that does not yet have the
// table: CREATE TABLE IF NOT EXISTS is a no-op against an existing one. Every
// environment that predates the ecosystem roles therefore still carries the
// old three-role constraint unless the ALTER pair runs. This asserts the ALTER
// exists and lists the same seven values, because an inline CHECK that is
// right and an ALTER that is missing looks correct in review and fails only in
// the environments that already exist — which is all of them.
func TestSetupSQLAlterReplacesCheck(t *testing.T) {
	if !strings.Contains(database.SetupSQL,
		"ALTER TABLE auth.user_roles DROP CONSTRAINT IF EXISTS user_roles_role_check") {
		t.Fatal("database/setup.sql must DROP the old auth.user_roles CHECK before re-adding it; " +
			"an existing database never picks up an edited inline CHECK")
	}
	m := alterCheckRE.FindStringSubmatch(database.SetupSQL)
	if m == nil {
		t.Fatal("database/setup.sql must re-ADD user_roles_role_check with the full vocabulary")
	}
	want := append([]string(nil), roles.All()...)
	sort.Strings(want)
	if got := parseSQLStringList(m[1]); !reflect.DeepEqual(got, want) {
		t.Fatalf("ALTER ... ADD CONSTRAINT lists %v, vocabulary is %v", got, want)
	}
}

// The service-driven audit path writes actor_id NULL + actor_service, so the
// column has to exist and the NOT NULL has to be gone. Both are ALTERs that a
// merge could silently drop.
func TestSetupSQLAllowsServiceActorInAudit(t *testing.T) {
	for _, fragment := range []string{
		"ADD COLUMN IF NOT EXISTS actor_service TEXT",
		"ALTER COLUMN actor_id DROP NOT NULL",
	} {
		if !strings.Contains(database.SetupSQL, fragment) {
			t.Fatalf("database/setup.sql is missing %q — InsertServiceAudit writes "+
				"actor_id NULL and would fail with 23502", fragment)
		}
	}
}

var appCheckRE = regexp.MustCompile(
	`(?is)ADD\s+CONSTRAINT\s+user_roles_app_check\s+CHECK\s*\(\s*app\s+IS\s+NULL\s+OR\s+app\s+IN\s*\(([^)]*)\)`)

var scopedRoleCheckRE = regexp.MustCompile(
	`(?is)ADD\s+CONSTRAINT\s+user_roles_scoped_role_check\s+CHECK\s*\(\s*app\s+IS\s+NULL\s+OR\s+role\s+IN\s*\(([^)]*)\)`)

// TestSetupSQLAppCheckMatchesCatalogue: the app CHECK is a copy of
// permissions.Apps() and the scoped-role CHECK a copy of the app-scopable
// roles (every admin role except superadmin).
func TestSetupSQLAppCheckMatchesCatalogue(t *testing.T) {
	m := appCheckRE.FindStringSubmatch(database.SetupSQL)
	if m == nil {
		t.Fatal("database/setup.sql must ADD user_roles_app_check")
	}
	want := permissions.Apps()
	sort.Strings(want)
	if got := parseSQLStringList(m[1]); !reflect.DeepEqual(got, want) {
		t.Fatalf("app CHECK lists %v, catalogue is %v", got, want)
	}

	m = scopedRoleCheckRE.FindStringSubmatch(database.SetupSQL)
	if m == nil {
		t.Fatal("database/setup.sql must ADD user_roles_scoped_role_check")
	}
	var scopable []string
	for _, r := range roles.AdminRoles() {
		if r != roles.Superadmin {
			scopable = append(scopable, r)
		}
	}
	sort.Strings(scopable)
	if got := parseSQLStringList(m[1]); !reflect.DeepEqual(got, scopable) {
		t.Fatalf("scoped-role CHECK lists %v, want %v", got, scopable)
	}
}

// TestSetupSQLPerAppKey: the migration is additive and idempotent, and the
// old (user_id, role) key is replaced by (user_id, role, COALESCE(app,'')).
func TestSetupSQLPerAppKey(t *testing.T) {
	for _, fragment := range []string{
		"ALTER TABLE auth.user_roles ADD COLUMN IF NOT EXISTS app TEXT",
		"ALTER TABLE auth.user_roles ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ",
		"ALTER TABLE auth.user_roles ADD COLUMN IF NOT EXISTS reason TEXT",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_user_roles_user_role_app",
		"ON auth.user_roles (user_id, role, (COALESCE(app, '')))",
		"ALTER TABLE auth.user_roles DROP CONSTRAINT IF EXISTS user_roles_pkey",
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_admin_audit_role_bootstrap",
	} {
		if !strings.Contains(database.SetupSQL, fragment) {
			t.Fatalf("database/setup.sql is missing %q", fragment)
		}
	}
	// The unique index must exist before the PK is dropped, so there is no
	// window without a key.
	if strings.Index(database.SetupSQL, "CREATE UNIQUE INDEX IF NOT EXISTS uq_user_roles_user_role_app") >
		strings.Index(database.SetupSQL, "DROP CONSTRAINT IF EXISTS user_roles_pkey") {
		t.Fatal("create uq_user_roles_user_role_app before dropping user_roles_pkey")
	}
}

func TestValidRoleTracksVocabulary(t *testing.T) {
	for _, r := range roles.All() {
		if !ValidRole(r) {
			t.Fatalf("ValidRole(%q) = false", r)
		}
	}
	for _, r := range []string{"customer", "bogus", ""} {
		if ValidRole(r) {
			t.Fatalf("ValidRole(%q) = true", r)
		}
	}
	for _, r := range roles.Ecosystem() {
		if !ValidEcosystemRole(r) {
			t.Fatalf("ValidEcosystemRole(%q) = false", r)
		}
	}
	for _, r := range roles.Platform() {
		if ValidEcosystemRole(r) {
			t.Fatalf("ValidEcosystemRole(%q) = true — a service could then mint it", r)
		}
	}
}

// parseSQLStringList turns "'a','b' , 'c'" into a sorted []string{"a","b","c"}.
func parseSQLStringList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "'")
		if part != "" {
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}
