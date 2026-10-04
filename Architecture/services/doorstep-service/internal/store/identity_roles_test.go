package store

import (
	"testing"

	"github.com/atpost/shared/identityroles"
)

// The status table shared with commerce, food and rider: grant while on the
// journey, revoke on the terminal statuses, no change on suspension.
func TestProRoleForStatus(t *testing.T) {
	for status, want := range map[string]struct {
		op identityroles.Op
		ok bool
	}{
		"draft":                {identityroles.OpGrant, true},
		"pending_verification": {identityroles.OpGrant, true},
		"approved":             {identityroles.OpGrant, true},
		"rejected":             {identityroles.OpRevoke, true},
		"blocked":              {identityroles.OpRevoke, true},
		"suspended":            {"", false},
		"unknown":              {"", false},
	} {
		op, ok := proRoleForStatus(status)
		if op != want.op || ok != want.ok {
			t.Errorf("%s: %q %v, want %q %v", status, op, ok, want.op, want.ok)
		}
	}
}
