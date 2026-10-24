package service

import (
	"testing"

	"github.com/atpost/graph-service/internal/permission"
)

// sources builds scopedFactSources whose lookups count their calls.
func sources(actor permission.Privacy, accepted, followSecond bool, calls *map[string]int) scopedFactSources {
	bump := func(k string) {
		if calls != nil {
			(*calls)[k]++
		}
	}
	return scopedFactSources{
		actorPrivacy:       func() permission.Privacy { bump("actor"); return actor },
		acceptedChat:       func() bool { bump("chat"); return accepted },
		followSecondDegree: func() bool { bump("fof"); return followSecond },
	}
}

// connectionsOnlyTarget sets every audience setting to connections_only.
func connectionsOnlyTarget() permission.Privacy {
	return permission.Privacy{
		WhoCanSeeOnlineStatus: "connections_only", WhoCanSeeLastSeen: "connections_only",
		WhoCanSeeReadReceipts: "connections_only", WhoCanSeeProfilePhoto: "connections_only",
		WhoCanAddToGroups: "connections_only",
	}
}

// Presence wiring (founder, 23 Oct 2026): see_online_status / see_last_seen
// must fetch the accepted-chat fact and the viewer's own settings, and a
// blocked pair fetches nothing.
func TestActionScopedFactsForPresence(t *testing.T) {
	shares := permission.Privacy{WhoCanSeeOnlineStatus: "everyone", WhoCanSeeLastSeen: "connections_only"}
	target := connectionsOnlyTarget()

	for _, a := range []permission.Action{permission.ActionSeeOnlineStatus, permission.ActionSeeLastSeen} {
		f := withActionScopedFacts([]permission.Action{a}, permission.Facts{}, target, sources(shares, true, false, nil))
		if !f.AcceptedChat {
			t.Errorf("%s did not fetch the accepted-chat fact", a)
		}
		if f.ActorHidesOnline || f.ActorHidesLastSeen {
			t.Errorf("%s: a sharing viewer marked as hiding: %+v", a, f)
		}
	}

	hides := permission.Privacy{WhoCanSeeOnlineStatus: "no_one", WhoCanSeeLastSeen: "everyone"}
	f := withActionScopedFacts([]permission.Action{permission.ActionSeeOnlineStatus, permission.ActionSeeLastSeen}, permission.Facts{}, target, sources(hides, true, false, nil))
	if !f.ActorHidesOnline || f.ActorHidesLastSeen {
		t.Fatalf("reciprocity facts = online %v / last seen %v, want true / false", f.ActorHidesOnline, f.ActorHidesLastSeen)
	}

	calls := map[string]int{}
	f = withActionScopedFacts([]permission.Action{permission.ActionSeeOnlineStatus, permission.ActionAddToGroup}, permission.Facts{Blocked: true},
		permission.Privacy{WhoCanSeeOnlineStatus: "connections_only", WhoCanAddToGroups: "friends_of_friends_invites"}, sources(shares, true, true, &calls))
	if len(calls) != 0 || f.AcceptedChat || f.FollowSecondDegree {
		t.Fatalf("a blocked pair fetched %v (facts %+v); want none", calls, f)
	}

	// The actor's settings are fetched once even when message and presence
	// are asked together.
	calls = map[string]int{}
	withActionScopedFacts([]permission.Action{permission.ActionMessage, permission.ActionSeeOnlineStatus}, permission.Facts{}, target, sources(shares, true, false, &calls))
	if calls["actor"] != 1 {
		t.Fatalf("actor settings fetched %d times, want 1", calls["actor"])
	}
}

// 24 Oct 2026: read receipts, profile views and group adds resolve through
// the connections_only audience too, so they fetch the accepted-chat fact —
// but only when the target's setting for them is connections_only and the
// target does not already follow the actor.
func TestActionScopedFactsForTheConnectionsAudience(t *testing.T) {
	audience := []permission.Action{permission.ActionSeeReadReceipts, permission.ActionViewProfile, permission.ActionAddToGroup}
	for _, a := range audience {
		f := withActionScopedFacts([]permission.Action{a}, permission.Facts{}, connectionsOnlyTarget(), sources(permission.Privacy{}, true, false, nil))
		if !f.AcceptedChat {
			t.Errorf("%s under connections_only did not fetch the accepted-chat fact", a)
		}
		// End to end through the matrix: an accepted chat alone is admitted.
		if d := permission.Resolve(a, f, connectionsOnlyTarget()); !d.Allowed {
			t.Errorf("%s with an accepted chat = %+v, want allowed", a, d)
		}
	}

	open := permission.Privacy{WhoCanSeeReadReceipts: "everyone", WhoCanSeeProfilePhoto: "everyone", WhoCanAddToGroups: "everyone_with_approval"}
	skip := []struct {
		name   string
		facts  permission.Facts
		target permission.Privacy
	}{
		{"setting is not connections_only", permission.Facts{}, open},
		{"target already follows the actor", permission.Facts{TargetFollowsActor: true}, connectionsOnlyTarget()},
		{"already a legacy connection", permission.Facts{IsConnection: true}, connectionsOnlyTarget()},
	}
	for _, tc := range skip {
		calls := map[string]int{}
		withActionScopedFacts(audience, tc.facts, tc.target, sources(permission.Privacy{}, true, false, &calls))
		if calls["chat"] != 0 {
			t.Errorf("%s: accepted-chat fetched %d times, want 0", tc.name, calls["chat"])
		}
	}

	// Calls and messages keep fetching it whatever the audience settings.
	calls := map[string]int{}
	withActionScopedFacts([]permission.Action{permission.ActionCall}, permission.Facts{TargetFollowsActor: true}, open, sources(permission.Privacy{}, true, false, &calls))
	if calls["chat"] != 1 {
		t.Fatalf("call did not fetch the accepted-chat fact (%v)", calls)
	}
}

// The follow-graph friends-of-friends lookup runs for add_to_group under
// friends_of_friends_invites only, and only when nothing else has decided.
func TestActionScopedFactsFollowSecondDegree(t *testing.T) {
	fof := permission.Privacy{WhoCanAddToGroups: "friends_of_friends_invites"}
	add := []permission.Action{permission.ActionAddToGroup}

	f := withActionScopedFacts(add, permission.Facts{}, fof, sources(permission.Privacy{}, false, true, nil))
	if !f.FollowSecondDegree {
		t.Fatal("friends_of_friends_invites did not fetch the follow second-degree fact")
	}
	if d := permission.Resolve(permission.ActionAddToGroup, f, fof); d.Fallback != "group_invitation" {
		t.Fatalf("follow second degree = %+v, want the group_invitation fallback", d)
	}

	for _, tc := range []struct {
		name    string
		actions []permission.Action
		facts   permission.Facts
		target  permission.Privacy
		chat    bool
	}{
		{"other setting", add, permission.Facts{}, permission.Privacy{WhoCanAddToGroups: "connections_only"}, false},
		{"add_to_group not asked", []permission.Action{permission.ActionViewProfile}, permission.Facts{}, fof, false},
		{"target follows actor", add, permission.Facts{TargetFollowsActor: true}, fof, false},
		{"legacy connection", add, permission.Facts{IsConnection: true}, fof, false},
		{"legacy second degree", add, permission.Facts{SecondDegree: true}, fof, false},
		{"accepted chat", add, permission.Facts{}, fof, true},
	} {
		calls := map[string]int{}
		f := withActionScopedFacts(tc.actions, tc.facts, tc.target, sources(permission.Privacy{}, tc.chat, true, &calls))
		if calls["fof"] != 0 || f.FollowSecondDegree {
			t.Errorf("%s: follow second-degree fetched %d times (fact %v), want 0", tc.name, calls["fof"], f.FollowSecondDegree)
		}
	}
}
