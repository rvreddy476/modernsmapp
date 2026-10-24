package permission

import "testing"

func TestResolveMessage(t *testing.T) {
	cases := []struct {
		name     string
		facts    Facts
		privacy  Privacy
		want     bool
		fallback string
	}{
		{
			name:    "block denies even a connection",
			facts:   Facts{Blocked: true, IsConnection: true},
			privacy: Privacy{WhoCanMessage: "connections_only"},
			want:    false,
		},
		{
			name:    "connection may DM directly",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanMessage: "connections_only"},
			want:    true,
		},
		{
			name:    "no_one blocks even a connection",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanMessage: "no_one"},
			want:    false,
		},
		{
			name:    "stranger denied under connections_only, no fallback",
			facts:   Facts{},
			privacy: Privacy{WhoCanMessage: "connections_only"},
			want:    false,
		},
		{
			name:     "stranger gets message-request fallback under everyone_message_requests",
			facts:    Facts{},
			privacy:  Privacy{WhoCanMessage: "everyone_message_requests"},
			want:     false,
			fallback: "message_request",
		},
		{
			name:    "mutual follower may DM under connections_and_mutual_followers",
			facts:   Facts{ActorFollowsTarget: true, TargetFollowsActor: true},
			privacy: Privacy{WhoCanMessage: "connections_and_mutual_followers"},
			want:    true,
		},
		{
			name:     "follower gets request fallback under followers_message_requests",
			facts:    Facts{ActorFollowsTarget: true},
			privacy:  Privacy{WhoCanMessage: "followers_message_requests"},
			want:     false,
			fallback: "message_request",
		},
		{
			name:    "non-follower denied under followers_message_requests",
			facts:   Facts{},
			privacy: Privacy{WhoCanMessage: "followers_message_requests"},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(ActionMessage, tc.facts, tc.privacy)
			if got.Allowed != tc.want {
				t.Errorf("Allowed = %v, want %v (%+v)", got.Allowed, tc.want, got)
			}
			if got.Fallback != tc.fallback {
				t.Errorf("Fallback = %q, want %q", got.Fallback, tc.fallback)
			}
		})
	}
}

func TestResolveConnectAndFollow(t *testing.T) {
	if d := Resolve(ActionConnect, Facts{IsConnection: true}, Privacy{}); d.Allowed {
		t.Error("connect to an existing connection should be denied")
	}
	// Connections are retired (24 Oct 2026): connect is denied for every
	// relationship and every settings snapshot, with its own reason
	// (who_can_send_connection_request itself was removed the same day).
	for _, p := range []Privacy{{}, {WhoCanMessage: "everyone_message_requests", WhoCanAddToGroups: "everyone_with_approval"}, strictForTest()} {
		for _, f := range []Facts{{}, {TargetFollowsActor: true}, {ActorFollowsTarget: true, TargetFollowsActor: true}, {SecondDegree: true}, {AcceptedChat: true}} {
			d := Resolve(ActionConnect, f, p)
			if d.Allowed || d.Reason != ReasonConnectionsRetired {
				t.Errorf("connect with privacy=%+v facts=%+v = %+v, want deny %s", p, f, d, ReasonConnectionsRetired)
			}
		}
	}
	if d := Resolve(ActionConnect, Facts{IsConnection: true}, Privacy{}); d.Reason != "already_connected" {
		t.Errorf("connect to an existing connection reason = %q, want already_connected", d.Reason)
	}
	if d := Resolve(ActionFollow, Facts{}, Privacy{}); !d.Allowed {
		t.Error("public follow should always be allowed")
	}
	if d := Resolve(ActionFollow, Facts{Blocked: true}, Privacy{}); d.Allowed {
		t.Error("follow should be denied when blocked")
	}
}

// strictForTest is a settings snapshot with every field set to a value.
func strictForTest() Privacy {
	return Privacy{
		WhoCanMessage: "no_one", WhoCanCall: "no_one", WhoCanAddToGroups: "no_one",
		WhoCanSeeOnlineStatus: "no_one", WhoCanSeeReadReceipts: "no_one", WhoCanSeeLastSeen: "no_one",
		WhoCanSeeProfilePhoto: "no_one", ChatAvailability: "enabled",
	}
}

// H3 — view_profile is no longer unconditionally allowed; it follows the
// who_can_see_profile_photo setting.
func TestResolveViewProfile(t *testing.T) {
	cases := []struct {
		name    string
		facts   Facts
		privacy Privacy
		want    bool
	}{
		{
			name:    "everyone may view a public profile",
			privacy: Privacy{WhoCanSeeProfilePhoto: "everyone"},
			want:    true,
		},
		{
			name:    "stranger denied when private",
			privacy: Privacy{WhoCanSeeProfilePhoto: "connections_only"},
			want:    false,
		},
		{
			name:    "connection may view a private profile",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanSeeProfilePhoto: "connections_only"},
			want:    true,
		},
		{
			name:    "block denies even a connection",
			facts:   Facts{IsConnection: true, Blocked: true},
			privacy: Privacy{WhoCanSeeProfilePhoto: "everyone"},
			want:    false,
		},
		{
			name:    "no_one denies everyone",
			facts:   Facts{IsConnection: true},
			privacy: Privacy{WhoCanSeeProfilePhoto: "no_one"},
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(ActionViewProfile, tc.facts, tc.privacy)
			if got.Allowed != tc.want {
				t.Errorf("Allowed=%v want %v (reason=%q)", got.Allowed, tc.want, got.Reason)
			}
		})
	}
}
