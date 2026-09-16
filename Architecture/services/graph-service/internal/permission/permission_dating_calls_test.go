package permission

import "testing"

// Dating-match call grant: two people who match get chat AND live calls, and
// nobody else does.
//
//	allow = !blocked && who_can_call != "no_one" && (IsConnection || DatingMatch)
//
// A closed match and a flag-off deployment both arrive here as
// DatingMatch=false — chat-service reports a closed match as no match, and the
// fact is never fetched while the kill switch is off — so they are the same
// row of this table, asserted separately for the record.
func TestResolveCallDatingMatrix(t *testing.T) {
	cases := []struct {
		name    string
		facts   Facts
		privacy Privacy
		want    bool
		reason  string
	}{
		// --- no_one is stronger than a match -------------------------------
		{
			name:    "match + no_one denies: a match never overrules the callee's own setting",
			facts:   Facts{DatingMatch: true},
			privacy: Privacy{WhoCanCall: "no_one"},
			want:    false,
			reason:  "privacy_no_one",
		},
		{
			name:    "connection + match + no_one still denies",
			facts:   Facts{IsConnection: true, DatingMatch: true},
			privacy: Privacy{WhoCanCall: "no_one"},
			want:    false,
			reason:  "privacy_no_one",
		},

		// --- a block is fatal regardless of the match ----------------------
		{
			name:    "match + blocked denies",
			facts:   Facts{Blocked: true, DatingMatch: true},
			privacy: Privacy{WhoCanCall: "connections_only"},
			want:    false,
			reason:  "blocked",
		},
		{
			name:    "match + blocked denies under accepted_chats_only too",
			facts:   Facts{Blocked: true, DatingMatch: true},
			privacy: Privacy{WhoCanCall: "accepted_chats_only"},
			want:    false,
			reason:  "blocked",
		},
		{
			name:    "connection + match + blocked denies",
			facts:   Facts{Blocked: true, IsConnection: true, DatingMatch: true},
			privacy: Privacy{WhoCanCall: "connections_only"},
			want:    false,
			reason:  "blocked",
		},

		// --- the grant itself ----------------------------------------------
		{
			name:    "match + connections_only allows, with no graph connection",
			facts:   Facts{DatingMatch: true},
			privacy: Privacy{WhoCanCall: "connections_only"},
			want:    true,
			reason:  "dating_match",
		},
		{
			name:    "match + accepted_chats_only allows",
			facts:   Facts{DatingMatch: true},
			privacy: Privacy{WhoCanCall: "accepted_chats_only"},
			want:    true,
			reason:  "dating_match",
		},

		// --- closed match / flag off: both are DatingMatch=false ------------
		{
			name:    "closed match denies (chat-service reports a closed match as no match)",
			facts:   Facts{DatingMatch: false},
			privacy: Privacy{WhoCanCall: "connections_only"},
			want:    false,
			reason:  "privacy_connections_only",
		},
		{
			name:    "flag off denies (the fact is never fetched, so it is false)",
			facts:   Facts{DatingMatch: false},
			privacy: Privacy{WhoCanCall: "accepted_chats_only"},
			want:    false,
			reason:  "privacy_connections_only",
		},

		// --- neither fact ---------------------------------------------------
		{
			name:    "neither connection nor match denies",
			facts:   Facts{},
			privacy: Privacy{WhoCanCall: "connections_only"},
			want:    false,
			reason:  "privacy_connections_only",
		},
		{
			name:    "a mere mutual follow is not a match and denies",
			facts:   Facts{ActorFollowsTarget: true, TargetFollowsActor: true},
			privacy: Privacy{WhoCanCall: "connections_only"},
			want:    false,
			reason:  "privacy_connections_only",
		},
		{
			name:    "second degree is not a match and denies",
			facts:   Facts{SecondDegree: true},
			privacy: Privacy{WhoCanCall: "connections_only"},
			want:    false,
			reason:  "privacy_connections_only",
		},

		// --- the pre-existing connection rule is untouched -------------------
		{
			name:    "connection without a match still allows",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanCall: "connections_only"},
			want:    true,
		},
		{
			name:    "connection under accepted_chats_only still allows",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanCall: "accepted_chats_only"},
			want:    true,
		},
		{
			name:    "unknown who_can_call denies even with a match (fail closed)",
			facts:   Facts{DatingMatch: true},
			privacy: Privacy{WhoCanCall: ""},
			want:    false,
			reason:  "privacy_disallows",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(ActionCall, tc.facts, tc.privacy)
			if got.Allowed != tc.want {
				t.Fatalf("allowed = %v, want %v (reason %q)", got.Allowed, tc.want, got.Reason)
			}
			if tc.reason != "" && got.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", got.Reason, tc.reason)
			}
		})
	}
}

// A dating match grants the CALL channel and nothing else. If this ever fails,
// a match has silently become a social connection: second-degree facts,
// who_can_message and every friends-scoped surface would move with it.
func TestDatingMatchGrantsCallsOnly(t *testing.T) {
	f := Facts{DatingMatch: true}
	p := Privacy{
		WhoCanMessage:               "connections_only",
		WhoCanCall:                  "connections_only",
		WhoCanAddToGroups:           "connections_only",
		WhoCanSendConnectionRequest: "everyone",
		WhoCanSeeOnlineStatus:       "connections_only",
		WhoCanSeeReadReceipts:       "connections_only",
		WhoCanSeeLastSeen:           "connections_only",
		WhoCanSeeProfilePhoto:       "connections_only",
		AllowCommentsFrom:           "friends",
		AccountVisibility:           "private",
	}
	denied := []Action{
		ActionMessage, ActionAddToGroup, ActionSeeOnlineStatus,
		ActionSeeReadReceipts, ActionSeeLastSeen, ActionViewProfile,
		ActionComment, ActionViewPosts,
	}
	for _, a := range denied {
		if d := Resolve(a, f, p); d.Allowed {
			t.Errorf("action %q allowed on a bare dating match; the match must grant calls only", a)
		}
	}
	if d := Resolve(ActionCall, f, p); !d.Allowed {
		t.Fatalf("call denied on an open dating match: %+v", d)
	}
}
