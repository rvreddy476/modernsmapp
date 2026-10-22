package service

import (
	"testing"

	"github.com/atpost/graph-service/internal/permission"
)

// Presence wiring (founder, 23 Oct 2026): see_online_status / see_last_seen
// must fetch the accepted-chat fact and the viewer's own settings, and a
// blocked pair fetches nothing.
func TestActionScopedFactsForPresence(t *testing.T) {
	shares := func() permission.Privacy {
		return permission.Privacy{WhoCanSeeOnlineStatus: "everyone", WhoCanSeeLastSeen: "connections_only"}
	}
	accepted := func() bool { return true }

	for _, a := range []permission.Action{permission.ActionSeeOnlineStatus, permission.ActionSeeLastSeen} {
		f := withActionScopedFacts([]permission.Action{a}, permission.Facts{}, shares, accepted)
		if !f.AcceptedChat {
			t.Errorf("%s did not fetch the accepted-chat fact", a)
		}
		if f.ActorHidesOnline || f.ActorHidesLastSeen {
			t.Errorf("%s: a sharing viewer marked as hiding: %+v", a, f)
		}
	}

	hides := func() permission.Privacy {
		return permission.Privacy{WhoCanSeeOnlineStatus: "no_one", WhoCanSeeLastSeen: "everyone"}
	}
	f := withActionScopedFacts([]permission.Action{permission.ActionSeeOnlineStatus, permission.ActionSeeLastSeen}, permission.Facts{}, hides, accepted)
	if !f.ActorHidesOnline || f.ActorHidesLastSeen {
		t.Fatalf("reciprocity facts = online %v / last seen %v, want true / false", f.ActorHidesOnline, f.ActorHidesLastSeen)
	}

	fetched := 0
	f = withActionScopedFacts([]permission.Action{permission.ActionSeeOnlineStatus}, permission.Facts{Blocked: true},
		func() permission.Privacy { fetched++; return shares() },
		func() bool { fetched++; return true })
	if fetched != 0 || f.AcceptedChat {
		t.Fatalf("a blocked pair fetched %d facts (accepted=%v); want none", fetched, f.AcceptedChat)
	}

	// Read receipts and profile views never pull the accepted-chat fact.
	f = withActionScopedFacts([]permission.Action{permission.ActionSeeReadReceipts, permission.ActionViewProfile}, permission.Facts{},
		shares, func() bool { t.Fatal("accepted-chat fetched for a non-presence action"); return true })
	if f.AcceptedChat {
		t.Fatal("accepted-chat set for a non-presence action")
	}

	// The actor's settings are fetched once even when message and presence
	// are asked together.
	calls := 0
	withActionScopedFacts([]permission.Action{permission.ActionMessage, permission.ActionSeeOnlineStatus}, permission.Facts{},
		func() permission.Privacy { calls++; return shares() }, accepted)
	if calls != 1 {
		t.Fatalf("actor settings fetched %d times, want 1", calls)
	}
}
