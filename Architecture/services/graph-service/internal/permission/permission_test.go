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
	if d := Resolve(ActionConnect, Facts{}, Privacy{WhoCanSendConnectionRequest: "no_one"}); d.Allowed {
		t.Error("connect should be denied when who_can_send_connection_request=no_one")
	}
	if d := Resolve(ActionConnect, Facts{}, Privacy{WhoCanSendConnectionRequest: "everyone"}); !d.Allowed {
		t.Error("connect should be allowed when who_can_send_connection_request=everyone")
	}
	if d := Resolve(ActionFollow, Facts{}, Privacy{}); !d.Allowed {
		t.Error("public follow should always be allowed")
	}
	if d := Resolve(ActionFollow, Facts{Blocked: true}, Privacy{}); d.Allowed {
		t.Error("follow should be denied when blocked")
	}
}
