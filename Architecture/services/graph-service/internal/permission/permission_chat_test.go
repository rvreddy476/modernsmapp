package permission

import "testing"

// Production chat pass (directive §3): pause, friends-of-friends and the
// group-invitation consent fallback.

func TestResolveMessageChatRules(t *testing.T) {
	cases := []struct {
		name     string
		facts    Facts
		privacy  Privacy
		want     bool
		fallback string
		reason   string
	}{
		{
			name:    "paused denies even a connection with no fallback",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanMessage: "everyone_message_requests", ChatAvailability: "paused"},
			want:    false,
			reason:  "chat_paused",
		},
		{
			name:    "paused denies the stranger request fallback too",
			facts:   Facts{},
			privacy: Privacy{WhoCanMessage: "everyone_message_requests", ChatAvailability: "paused"},
			want:    false,
			reason:  "chat_paused",
		},
		{
			name:     "second degree earns a REQUEST under friends_of_friends_requests",
			facts:    Facts{SecondDegree: true},
			privacy:  Privacy{WhoCanMessage: "friends_of_friends_requests"},
			want:     false,
			fallback: "message_request",
		},
		{
			name:    "true stranger denied under friends_of_friends_requests",
			facts:   Facts{},
			privacy: Privacy{WhoCanMessage: "friends_of_friends_requests"},
			want:    false,
			reason:  "privacy_disallows",
		},
		{
			name:    "connection still DMs directly under friends_of_friends_requests",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanMessage: "friends_of_friends_requests"},
			want:    true,
		},
		{
			name:    "empty chat_availability means enabled (older snapshots)",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanMessage: "connections_only"},
			want:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Resolve(ActionMessage, tc.facts, tc.privacy)
			if d.Allowed != tc.want || d.Fallback != tc.fallback {
				t.Fatalf("got allowed=%v fallback=%q, want allowed=%v fallback=%q",
					d.Allowed, d.Fallback, tc.want, tc.fallback)
			}
			if tc.reason != "" && d.Reason != tc.reason {
				t.Fatalf("got reason=%q, want %q", d.Reason, tc.reason)
			}
		})
	}
}

func TestResolveAddToGroupChatRules(t *testing.T) {
	cases := []struct {
		name     string
		facts    Facts
		privacy  Privacy
		want     bool
		fallback string
	}{
		{
			name:    "connection direct-adds under connections_only",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanAddToGroups: "connections_only"},
			want:    true,
		},
		{
			name:     "everyone_with_approval always requires an INVITATION, never a silent add",
			facts:    Facts{IsConnection: true},
			privacy:  Privacy{WhoCanAddToGroups: "everyone_with_approval"},
			want:     false,
			fallback: "group_invitation",
		},
		{
			name:     "second degree earns an invitation under friends_of_friends_invites",
			facts:    Facts{SecondDegree: true},
			privacy:  Privacy{WhoCanAddToGroups: "friends_of_friends_invites"},
			want:     false,
			fallback: "group_invitation",
		},
		{
			name:    "connection direct-adds under friends_of_friends_invites",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanAddToGroups: "friends_of_friends_invites"},
			want:    true,
		},
		{
			name:    "stranger denied under friends_of_friends_invites",
			facts:   Facts{},
			privacy: Privacy{WhoCanAddToGroups: "friends_of_friends_invites"},
			want:    false,
		},
		{
			name:    "paused denies adds outright",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanAddToGroups: "connections_only", ChatAvailability: "paused"},
			want:    false,
		},
		{
			name:    "blocked pair can never be co-added",
			facts:   Facts{Blocked: true, IsConnection: true},
			privacy: Privacy{WhoCanAddToGroups: "connections_only"},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Resolve(ActionAddToGroup, tc.facts, tc.privacy)
			if d.Allowed != tc.want || d.Fallback != tc.fallback {
				t.Fatalf("got allowed=%v fallback=%q, want allowed=%v fallback=%q",
					d.Allowed, d.Fallback, tc.want, tc.fallback)
			}
		})
	}
}

func TestResolvePresenceDisclosuresPaused(t *testing.T) {
	privacy := Privacy{
		WhoCanSeeOnlineStatus: "everyone",
		WhoCanSeeReadReceipts: "everyone",
		WhoCanSeeLastSeen:     "everyone",
		ChatAvailability:      "paused",
	}
	for _, action := range []Action{ActionSeeOnlineStatus, ActionSeeReadReceipts, ActionSeeLastSeen} {
		if d := Resolve(action, Facts{IsConnection: true}, privacy); d.Allowed {
			t.Fatalf("%s should be denied while target is paused", action)
		}
	}
}

func TestResolveSeeLastSeen(t *testing.T) {
	if d := Resolve(ActionSeeLastSeen, Facts{IsConnection: true},
		Privacy{WhoCanSeeLastSeen: "connections_only"}); !d.Allowed {
		t.Fatal("connection should see last-seen under connections_only")
	}
	if d := Resolve(ActionSeeLastSeen, Facts{},
		Privacy{WhoCanSeeLastSeen: "connections_only"}); d.Allowed {
		t.Fatal("stranger must not see last-seen under connections_only")
	}
	if d := Resolve(ActionSeeLastSeen, Facts{IsConnection: true},
		Privacy{WhoCanSeeLastSeen: "no_one"}); d.Allowed {
		t.Fatal("no_one must hide last-seen from everyone")
	}
}
