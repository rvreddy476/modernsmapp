package permission

import "testing"

// Connections are retired (24 Oct 2026): no new connection can form, so every
// setting that admitted "connections_only" froze at the pairs connected
// before that day. The audience is now an existing connection, OR the target
// following the actor, OR an accepted 1:1 chat (InConnectionsAudience).

// audienceCase is one action resolved under one setting value.
type audienceCase struct {
	name    string
	action  Action
	privacy Privacy
}

// connectionsOnlySettings lists every action whose setting admits the
// connections_only audience, with that setting chosen.
func connectionsOnlySettings() []audienceCase {
	return []audienceCase{
		{"online status", ActionSeeOnlineStatus, Privacy{WhoCanSeeOnlineStatus: "connections_only"}},
		{"last seen", ActionSeeLastSeen, Privacy{WhoCanSeeLastSeen: "connections_only"}},
		{"read receipts", ActionSeeReadReceipts, Privacy{WhoCanSeeReadReceipts: "connections_only"}},
		{"profile", ActionViewProfile, Privacy{WhoCanSeeProfilePhoto: "connections_only"}},
		{"group add connections_only", ActionAddToGroup, Privacy{WhoCanAddToGroups: "connections_only"}},
		{"group add connections_and_contacts", ActionAddToGroup, Privacy{WhoCanAddToGroups: "connections_and_contacts"}},
		{"group add friends_of_friends_invites", ActionAddToGroup, Privacy{WhoCanAddToGroups: "friends_of_friends_invites"}},
	}
}

func TestConnectionsOnlyAudienceAfterRetirement(t *testing.T) {
	facts := []struct {
		name   string
		facts  Facts
		allow  bool
		reason string // checked on a deny
	}{
		{"legacy connection", Facts{IsConnection: true}, true, ""},
		{"target follows actor", Facts{TargetFollowsActor: true}, true, ""},
		{"accepted chat", Facts{AcceptedChat: true}, true, ""},
		{"mutual follow", Facts{ActorFollowsTarget: true, TargetFollowsActor: true}, true, ""},
		// Anyone can follow anyone: the actor's own follow must not open
		// connections_only to everyone.
		{"actor follows target only", Facts{ActorFollowsTarget: true}, false, ""},
		{"a dating match is not the audience", Facts{DatingMatch: true}, false, ""},
		{"none", Facts{}, false, ""},
		{"block beats every relationship", Facts{Blocked: true, IsConnection: true, TargetFollowsActor: true, AcceptedChat: true}, false, ReasonUnavailable},
	}
	for _, s := range connectionsOnlySettings() {
		for _, f := range facts {
			t.Run(s.name+"/"+f.name, func(t *testing.T) {
				d := Resolve(s.action, f.facts, s.privacy)
				if d.Allowed != f.allow {
					t.Fatalf("%s = %+v, want allowed=%v", s.action, d, f.allow)
				}
				if f.allow && d.Fallback != "" {
					t.Fatalf("an audience member got a fallback, not a direct allow: %+v", d)
				}
				if f.reason != "" && d.Reason != f.reason {
					t.Fatalf("reason %q, want %q", d.Reason, f.reason)
				}
			})
		}
	}
}

// The widening is only for connections_only: no_one still denies everyone,
// everyone still admits a stranger, and a paused target still discloses
// nothing and takes no group adds.
func TestConnectionsOnlyWideningLeavesOtherValuesAlone(t *testing.T) {
	member := Facts{IsConnection: true, TargetFollowsActor: true, AcceptedChat: true}
	for _, tc := range []struct {
		action Action
		p      Privacy
		facts  Facts
		allow  bool
	}{
		{ActionSeeReadReceipts, Privacy{WhoCanSeeReadReceipts: "no_one"}, member, false},
		{ActionViewProfile, Privacy{WhoCanSeeProfilePhoto: "no_one"}, member, false},
		{ActionAddToGroup, Privacy{WhoCanAddToGroups: "no_one"}, member, false},
		{ActionSeeReadReceipts, Privacy{WhoCanSeeReadReceipts: "everyone"}, Facts{}, true},
		{ActionViewProfile, Privacy{WhoCanSeeProfilePhoto: "everyone"}, Facts{}, true},
		{ActionSeeReadReceipts, Privacy{WhoCanSeeReadReceipts: "connections_only", ChatAvailability: "paused"}, member, false},
		{ActionAddToGroup, Privacy{WhoCanAddToGroups: "connections_only", ChatAvailability: "paused"}, member, false},
		// Unknown values fail closed even for a member.
		{ActionViewProfile, Privacy{WhoCanSeeProfilePhoto: ""}, member, false},
		{ActionAddToGroup, Privacy{WhoCanAddToGroups: ""}, member, false},
	} {
		if d := Resolve(tc.action, tc.facts, tc.p); d.Allowed != tc.allow {
			t.Errorf("%s with %+v = %+v, want allowed=%v", tc.action, tc.p, d, tc.allow)
		}
	}
}

// friends_of_friends_invites: the audience adds directly; a friend of a
// friend — a shared legacy connection, or a third party following both —
// may only invite; everyone else is denied.
func TestFriendsOfFriendsInvitesOverFollows(t *testing.T) {
	p := Privacy{WhoCanAddToGroups: "friends_of_friends_invites"}
	for _, tc := range []struct {
		name     string
		facts    Facts
		allow    bool
		fallback string
	}{
		{"follow second degree invites", Facts{FollowSecondDegree: true}, false, "group_invitation"},
		{"legacy second degree invites", Facts{SecondDegree: true}, false, "group_invitation"},
		{"target follows actor adds", Facts{TargetFollowsActor: true, FollowSecondDegree: true}, true, ""},
		{"accepted chat adds", Facts{AcceptedChat: true}, true, ""},
		{"actor follows target only is denied", Facts{ActorFollowsTarget: true}, false, ""},
		{"stranger denied", Facts{}, false, ""},
		{"block beats second degree", Facts{Blocked: true, FollowSecondDegree: true}, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Resolve(ActionAddToGroup, tc.facts, p)
			if d.Allowed != tc.allow || d.Fallback != tc.fallback {
				t.Fatalf("= %+v, want allowed=%v fallback=%q", d, tc.allow, tc.fallback)
			}
		})
	}
	// FollowSecondDegree is read by friends_of_friends_invites only: it is
	// not the connections_only audience.
	for _, v := range []string{"connections_only", "connections_and_contacts"} {
		if d := Resolve(ActionAddToGroup, Facts{FollowSecondDegree: true}, Privacy{WhoCanAddToGroups: v}); d.Allowed || d.Fallback != "" {
			t.Errorf("%s: follow second degree = %+v, want a plain deny", v, d)
		}
	}
}
