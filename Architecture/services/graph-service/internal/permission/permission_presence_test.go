package permission

import "testing"

// Presence (founder, 23 Oct 2026): "when user online no green mark, last
// seen". connections_only — the identity default for online status and last
// seen — was checked against the legacy connections list, which accepted
// message requests no longer create, so nearly every pair saw nothing. An
// accepted 1:1 chat now counts, and a viewer who hides their own presence
// sees nobody's (WhatsApp reciprocity).
func TestPresenceAcceptedChat(t *testing.T) {
	cases := []struct {
		name    string
		facts   Facts
		privacy Privacy
		want    bool
		reason  string
	}{
		{"accepted chat sees connections_only", Facts{AcceptedChat: true}, Privacy{WhoCanSeeOnlineStatus: "connections_only", WhoCanSeeLastSeen: "connections_only"}, true, ""},
		{"legacy connection still sees", Facts{IsConnection: true}, Privacy{WhoCanSeeOnlineStatus: "connections_only", WhoCanSeeLastSeen: "connections_only"}, true, ""},
		{"stranger denied under connections_only", Facts{}, Privacy{WhoCanSeeOnlineStatus: "connections_only", WhoCanSeeLastSeen: "connections_only"}, false, "privacy_connections_only"},
		{"a follow is not a chat", Facts{ActorFollowsTarget: true, TargetFollowsActor: true}, Privacy{WhoCanSeeOnlineStatus: "connections_only", WhoCanSeeLastSeen: "connections_only"}, false, "privacy_connections_only"},
		{"a bare dating match is not a chat", Facts{DatingMatch: true}, Privacy{WhoCanSeeOnlineStatus: "connections_only", WhoCanSeeLastSeen: "connections_only"}, false, "privacy_connections_only"},
		{"everyone sees a stranger", Facts{}, Privacy{WhoCanSeeOnlineStatus: "everyone", WhoCanSeeLastSeen: "everyone"}, true, ""},
		{"no_one beats an accepted chat", Facts{AcceptedChat: true, IsConnection: true}, Privacy{WhoCanSeeOnlineStatus: "no_one", WhoCanSeeLastSeen: "no_one"}, false, "privacy_no_one"},
		{"unknown value fails closed", Facts{AcceptedChat: true}, Privacy{WhoCanSeeOnlineStatus: "contacts", WhoCanSeeLastSeen: "contacts"}, false, "privacy_no_one"},
		{"paused target discloses nothing", Facts{AcceptedChat: true}, Privacy{WhoCanSeeOnlineStatus: "everyone", WhoCanSeeLastSeen: "everyone", ChatAvailability: "paused"}, false, "chat_paused"},
		{"block beats everything", Facts{AcceptedChat: true, IsConnection: true, Blocked: true}, Privacy{WhoCanSeeOnlineStatus: "everyone", WhoCanSeeLastSeen: "everyone"}, false, ReasonUnavailable},
		{"viewer hiding sees nobody", Facts{AcceptedChat: true, ActorHidesOnline: true, ActorHidesLastSeen: true}, Privacy{WhoCanSeeOnlineStatus: "everyone", WhoCanSeeLastSeen: "everyone"}, false, ReasonReciprocity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, a := range []Action{ActionSeeOnlineStatus, ActionSeeLastSeen} {
				d := Resolve(a, tc.facts, tc.privacy)
				if d.Allowed != tc.want || d.Reason != tc.reason {
					t.Fatalf("%s = %+v, want allowed=%v reason=%q", a, d, tc.want, tc.reason)
				}
			}
		})
	}
}

// Reciprocity is per field: hiding last seen does not hide others' online.
func TestPresenceReciprocityIsPerField(t *testing.T) {
	f := Facts{AcceptedChat: true, ActorHidesLastSeen: true}
	p := Privacy{WhoCanSeeOnlineStatus: "everyone", WhoCanSeeLastSeen: "everyone"}
	if d := Resolve(ActionSeeOnlineStatus, f, p); !d.Allowed {
		t.Fatalf("online denied by a last-seen-only reciprocity: %+v", d)
	}
	if d := Resolve(ActionSeeLastSeen, f, p); d.Allowed {
		t.Fatalf("last seen shown to a viewer hiding their own: %+v", d)
	}
}

func TestHidesPresence(t *testing.T) {
	cases := []struct {
		name string
		p    Privacy
		v    string
		want bool
	}{
		{"everyone shares", Privacy{}, "everyone", false},
		{"connections_only shares", Privacy{}, "connections_only", false},
		{"no_one hides", Privacy{}, "no_one", true},
		{"missing value hides", Privacy{}, "", true},
		{"paused hides", Privacy{ChatAvailability: "paused"}, "everyone", true},
		{"unreadable hides", Privacy{Unavailable: true}, "everyone", true},
	}
	for _, tc := range cases {
		if got := HidesPresence(tc.p, tc.v); got != tc.want {
			t.Errorf("%s: HidesPresence = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Read receipts keep their own rule (chat message-service readReceiptVisible);
// the presence change must not widen see_read_receipts.
func TestReadReceiptsUnchangedByPresenceRule(t *testing.T) {
	if d := Resolve(ActionSeeReadReceipts, Facts{AcceptedChat: true}, Privacy{WhoCanSeeReadReceipts: "connections_only"}); d.Allowed {
		t.Fatalf("see_read_receipts widened by an accepted chat: %+v", d)
	}
}
