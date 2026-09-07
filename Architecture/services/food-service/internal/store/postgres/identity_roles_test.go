package postgres

import (
	"strings"
	"testing"

	"github.com/atpost/food-service/database"
	"github.com/atpost/shared/identityroles"
)

// TestSetupSQLMatchesSharedSchema guards the one copy Go cannot import.
//
// shared/identityroles.SchemaSQL("") is the single definition of the intent
// queue; food's setup.sql carries a hand-written copy because this service
// bootstraps its whole schema from one embedded .sql file. If the two drift,
// the worker's queries stop matching the table and role grants stop being
// delivered — silently, because a grant failure never fails a request. This
// fails first instead.
func TestSetupSQLMatchesSharedSchema(t *testing.T) {
	got := normalizeSQL(database.SetupSQL)
	want := normalizeSQL(identityroles.SchemaSQL(""))
	if !strings.Contains(got, want) {
		t.Fatalf("setup.sql has drifted from identityroles.SchemaSQL(\"\").\nshared wants:\n%s", want)
	}
}

// TestDeliveryPartnerRoleForStatus pins the grant/revoke table for food's
// delivery-partner statuses. The three services must agree on this shape; see
// shared/identityroles' package doc.
func TestDeliveryPartnerRoleForStatus(t *testing.T) {
	cases := []struct {
		status string
		wantOp identityroles.Op
		wantOK bool
		why    string
	}{
		{"DRAFT", identityroles.OpGrant, true, "on the journey from the first save"},
		{"PENDING_REVIEW", identityroles.OpGrant, true, "must reach the partner area to upload documents"},
		{"APPROVED", identityroles.OpGrant, true, "idempotent re-grant, the safety net"},
		{"ACTIVE", identityroles.OpGrant, true, "idempotent re-grant"},
		{"REJECTED", identityroles.OpRevoke, true, "terminal denial ends the journey"},
		{"CLOSED", identityroles.OpRevoke, true, "partner removed"},
		{"SUSPENDED", "", false, "a pause: they still need the partner area to appeal"},
		{"OFFLINE", "", false, "availability, not membership"},
	}
	for _, tc := range cases {
		gotOp, gotOK := deliveryPartnerRoleForStatus(tc.status)
		if gotOp != tc.wantOp || gotOK != tc.wantOK {
			t.Errorf("%s: got (%q,%v), want (%q,%v) — %s",
				tc.status, gotOp, gotOK, tc.wantOp, tc.wantOK, tc.why)
		}
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
