package store

import (
	"errors"
	"strings"
	"testing"
)

// The suspension state machine (admin_suspension.go). Pure; the transaction
// and its audit row run in admin_moderation_integration_test.go (CI).
func TestSuspensionTransition(t *testing.T) {
	cases := []struct {
		current     string
		suspend     bool
		wantTo      string
		wantChanged bool
		wantErr     error
	}{
		{"active", true, "suspended", true, nil},
		{"suspended", true, "suspended", false, nil},
		{"suspended", false, "active", true, nil},
		{"active", false, "active", false, nil},
		{"archived", true, "archived", false, ErrAdminConflict},
		{"archived", false, "archived", false, ErrAdminConflict},
		{"deleted", true, "deleted", false, ErrAdminConflict},
		{"deleted", false, "deleted", false, ErrAdminConflict},
		{"", true, "", false, ErrAdminConflict},
	}
	for _, tc := range cases {
		to, changed, err := SuspensionTransition(tc.current, tc.suspend)
		if to != tc.wantTo || changed != tc.wantChanged || !errors.Is(err, tc.wantErr) || (tc.wantErr == nil && err != nil) {
			t.Errorf("SuspensionTransition(%q, suspend=%v) = (%q, %v, %v), want (%q, %v, %v)",
				tc.current, tc.suspend, to, changed, err, tc.wantTo, tc.wantChanged, tc.wantErr)
		}
	}
}

func TestSuspensionAuditActions(t *testing.T) {
	if suspensionAuditAction(true) != AuditCommunitySuspended || suspensionAuditAction(false) != AuditCommunityUnsuspended {
		t.Fatal("suspend and unsuspend are audited under the wrong action")
	}
	if AuditCommunitySuspended == AuditCommunityUnsuspended {
		t.Fatal("the two actions are indistinguishable in the audit trail")
	}
}

// The UPDATEs move only from the state the transition checked (a racing
// archive cannot be resurrected), write or clear all three columns, and
// stamp a strictly newer updated_at (search orders snapshots by it).
func TestSuspensionStatements(t *testing.T) {
	for name, tc := range map[string]struct {
		sql  string
		must []string
	}{
		"suspend": {suspendCommunitySQL, []string{
			"SET status = 'suspended'", "suspended_at = NOW()", "suspended_by = $2", "suspension_reason_code = $3",
			"WHERE id = $1 AND status = 'active'",
		}},
		"unsuspend": {unsuspendCommunitySQL, []string{
			"SET status = 'active'", "suspended_at = NULL", "suspended_by = NULL", "suspension_reason_code = NULL",
			"WHERE id = $1 AND status = 'suspended'",
		}},
	} {
		for _, m := range tc.must {
			if !strings.Contains(tc.sql, m) {
				t.Errorf("%s statement lost %q", name, m)
			}
		}
		if stamped, problem := checkStamp(tc.sql); !stamped || problem != "" {
			t.Errorf("%s statement does not stamp a strictly newer updated_at: %s", name, problem)
		}
	}
}

// The transaction: row locked, transition checked, the UPDATE refuses a row
// that moved, the audit row is written, and the row is re-read before commit.
func TestAdminSetCommunitySuspendedWiring(t *testing.T) {
	fn := storeFunc(t, "admin_suspension.go", "AdminSetCommunitySuspended")
	order := []string{
		"if actor == uuid.Nil {",
		"FOR UPDATE",
		"SuspensionTransition(current, suspend)",
		"if tag.RowsAffected() != 1 {",
		"INSERT INTO community_admin_audit",
		"readCommunityForSuspension(ctx, tx, communityID)",
		"tx.Commit(ctx)",
	}
	last := -1
	for _, must := range order {
		i := strings.Index(fn, must)
		if i < 0 || i < last {
			t.Fatalf("AdminSetCommunitySuspended lost or reordered %q", must)
		}
		last = i
	}
}
