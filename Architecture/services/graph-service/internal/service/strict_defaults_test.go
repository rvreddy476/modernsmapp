package service

// P0-2 correction-pass proof: when the target's real privacy settings cannot
// be fetched, the strict defaults must DENY chat actions and presence
// disclosure even for an existing connection. The independent review showed
// the previous connections_only posture let a connection open a chat and
// read online/receipt state during an identity outage, overriding a real
// setting of no_one or paused. Reverting strictPrivacyDefaults fails this
// test.

import (
	"testing"

	"github.com/atpost/graph-service/internal/permission"
)

func TestStrictDefaultsDenyChatAndPresenceEvenForConnections(t *testing.T) {
	p := strictPrivacyDefaults()
	connected := permission.Facts{IsConnection: true}

	for _, action := range []permission.Action{
		permission.ActionMessage,
		permission.ActionAddToGroup,
		permission.ActionSeeOnlineStatus,
		permission.ActionSeeReadReceipts,
		permission.ActionSeeLastSeen,
	} {
		d := permission.Resolve(action, connected, p)
		if d.Allowed {
			t.Errorf("strict defaults must deny %s for a connection while settings are unknown", action)
		}
		if d.Fallback != "" {
			t.Errorf("strict defaults must not offer a %s fallback for %s — a request/invite born of an outage outlives it", d.Fallback, action)
		}
	}
}
