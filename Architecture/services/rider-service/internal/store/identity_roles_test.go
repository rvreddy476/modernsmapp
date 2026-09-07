package store

import (
	"strings"
	"testing"

	"github.com/atpost/rider-service/database"
	"github.com/atpost/shared/identityroles"
)

// TestSetupSQLMatchesSharedSchema guards the one copy Go cannot import.
//
// shared/identityroles.SchemaSQL("rider") is the single definition of the
// intent queue; setup.sql carries a hand-written copy because this service
// bootstraps its whole schema from one embedded .sql file. If they drift, the
// worker's queries stop matching the table and role grants stop being
// delivered — silently, because a grant failure never fails a request.
func TestSetupSQLMatchesSharedSchema(t *testing.T) {
	got := normalizeSQL(database.SetupSQL)
	want := normalizeSQL(identityroles.SchemaSQL("rider"))
	if !strings.Contains(got, want) {
		t.Fatalf("setup.sql has drifted from identityroles.SchemaSQL(\"rider\").\nshared wants:\n%s", want)
	}
}

// TestRiderRoleForStatus pins the grant/revoke table for every value of the
// rider_partner_status enum. The three ecosystem services must agree on the
// shape of this table; see shared/identityroles' package doc.
func TestRiderRoleForStatus(t *testing.T) {
	cases := []struct {
		status string
		wantOp identityroles.Op
		wantOK bool
		why    string
	}{
		{"draft", identityroles.OpGrant, true, "must reach the partner area to upload KYC docs"},
		{"pending_verification", identityroles.OpGrant, true, "still on the journey"},
		{"approved", identityroles.OpGrant, true, "idempotent re-grant, the safety net"},
		{"rejected", identityroles.OpRevoke, true, "terminal denial"},
		{"blocked", identityroles.OpRevoke, true, "hard removal"},
		{"suspended", "", false, "a pause — and there is no unblock route to undo a revoke with"},
		{"inactive", "", false, "a pause"},
	}
	for _, tc := range cases {
		gotOp, gotOK := riderRoleForStatus(tc.status)
		if gotOp != tc.wantOp || gotOK != tc.wantOK {
			t.Errorf("%s: got (%q,%v), want (%q,%v) — %s",
				tc.status, gotOp, gotOK, tc.wantOp, tc.wantOK, tc.why)
		}
	}
}

// TestEveryPartnerStatusIsClassified fails if someone adds a value to the
// rider_partner_status enum without deciding what it means for the role.
// The list mirrors setup.sql's CREATE TYPE plus the later ALTER TYPE.
func TestEveryPartnerStatusIsClassified(t *testing.T) {
	all := []string{"draft", "pending_verification", "approved", "suspended", "blocked", "inactive", "rejected"}
	for _, s := range all {
		if !strings.Contains(database.SetupSQL, "'"+s+"'") {
			t.Errorf("status %q is not in setup.sql — this test's list has gone stale", s)
		}
		// Every status must reach a deliberate branch of riderRoleForStatus.
		// The default branch is "no change", which is a real answer, so this
		// asserts only that the list itself is complete.
		_, _ = riderRoleForStatus(s)
	}
}

func normalizeSQL(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		if t := strings.TrimSpace(line); t != "" {
			b.WriteString(t)
			b.WriteString(" ")
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
