package permission

import "testing"

// DM consent contract §4 (founder-approved 2026-10-20): every who_can_message
// value against every relationship, asserting the outcome AND the reason.
//
// Outcomes are written out by hand per value — not recomputed from the rule —
// so a change to the decision order fails here.

const (
	outAllow   = "allow"
	outRequest = "request"
)

type dmRelation struct {
	name    string
	facts   Facts
	privacy Privacy // merged over the value under test
}

var dmRelations = []dmRelation{
	{name: "stranger"},
	{name: "sender_follows", facts: Facts{ActorFollowsTarget: true}},
	{name: "recipient_follows", facts: Facts{TargetFollowsActor: true}},
	{name: "mutual", facts: Facts{ActorFollowsTarget: true, TargetFollowsActor: true}},
	{name: "legacy_connection", facts: Facts{IsConnection: true}},
	{name: "second_degree_only", facts: Facts{SecondDegree: true}},
	{name: "blocked", facts: Facts{Blocked: true, IsConnection: true, ActorFollowsTarget: true, TargetFollowsActor: true}},
	{name: "recipient_paused", facts: Facts{IsConnection: true, TargetFollowsActor: true}, privacy: Privacy{ChatAvailability: "paused"}},
	{name: "sender_paused", facts: Facts{IsConnection: true, TargetFollowsActor: true, ActorChatPaused: true}},
	{name: "sender_settings_unavailable", facts: Facts{IsConnection: true, ActorSettingsUnavailable: true}},
	{name: "recipient_settings_unavailable", facts: Facts{IsConnection: true}, privacy: Privacy{Unavailable: true}},
}

// The rows shared by every value: block, pause and unreadable settings come
// before the value is consulted at all.
var dmCommon = map[string]string{
	"blocked":                        ReasonUnavailable,
	"recipient_paused":               ReasonMessagesOff,
	"sender_paused":                  ReasonYouPaused,
	"sender_settings_unavailable":    ReasonUnavailable,
	"recipient_settings_unavailable": ReasonUnavailable,
}

func withCommon(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+len(dmCommon))
	for k, v := range dmCommon {
		out[k] = v
	}
	for k, v := range m {
		out[k] = v
	}
	return out
}

var dmExpected = map[string]map[string]string{
	"everyone_message_requests": withCommon(map[string]string{
		"stranger":           outRequest,
		"sender_follows":     outRequest,
		"recipient_follows":  outAllow,
		"mutual":             outAllow,
		"legacy_connection":  outAllow,
		"second_degree_only": outRequest,
	}),
	"followers_message_requests": withCommon(map[string]string{
		"stranger":           ReasonFollowersOnly,
		"sender_follows":     outRequest,
		"recipient_follows":  outAllow,
		"mutual":             outAllow,
		"legacy_connection":  outAllow,
		"second_degree_only": ReasonFollowersOnly,
	}),
	"people_i_follow": withCommon(map[string]string{
		"stranger":           ReasonPeopleIFollow,
		"sender_follows":     ReasonPeopleIFollow,
		"recipient_follows":  outAllow,
		"mutual":             outAllow,
		"legacy_connection":  outAllow,
		"second_degree_only": ReasonPeopleIFollow,
	}),
	"no_one": withCommon(map[string]string{
		"stranger":           ReasonNoOne,
		"sender_follows":     ReasonNoOne,
		"recipient_follows":  ReasonNoOne,
		"mutual":             ReasonNoOne,
		"legacy_connection":  ReasonNoOne,
		"second_degree_only": ReasonNoOne,
	}),
}

// Legacy and unknown stored values resolve exactly like the value identity
// maps them to.
var dmLegacy = map[string]string{
	"connections_only":                 "people_i_follow",
	"connections_and_mutual_followers": "people_i_follow",
	"friends_of_friends_requests":      "followers_message_requests",
	"":                                 "people_i_follow",
	"something_new":                    "people_i_follow",
}

func outcome(d Decision) string {
	switch {
	case d.Allowed:
		return outAllow
	case d.Fallback == FallbackMessageRequest:
		return outRequest
	default:
		return d.Reason
	}
}

func checkDMDecision(t *testing.T, d Decision, want string) {
	t.Helper()
	if got := outcome(d); got != want {
		t.Fatalf("outcome = %q, want %q (decision %+v)", got, want, d)
	}
	switch want {
	case outAllow:
		if d.Fallback != "" || d.Reason != "" {
			t.Fatalf("allow must carry no fallback/reason: %+v", d)
		}
	case outRequest:
		if d.Allowed || d.Reason != "" {
			t.Fatalf("message_request must be allowed=false with no reason: %+v", d)
		}
	default:
		if d.Allowed || d.Fallback != "" {
			t.Fatalf("deny must be allowed=false with no fallback: %+v", d)
		}
	}
}

func dmPrivacy(value string, extra Privacy) Privacy {
	p := extra
	p.WhoCanMessage = value
	return p
}

func TestDMConsentMatrix(t *testing.T) {
	values := make(map[string]string, len(dmExpected)+len(dmLegacy))
	for v := range dmExpected {
		values[v] = v
	}
	for legacy, canonical := range dmLegacy {
		values[legacy] = canonical
	}
	for value, canonical := range values {
		want := dmExpected[canonical]
		for _, rel := range dmRelations {
			expected, ok := want[rel.name]
			if !ok {
				t.Fatalf("no expectation for %s x %s", canonical, rel.name)
			}
			for _, action := range []Action{ActionMessage, ActionStartConversation} {
				t.Run(string(action)+"/"+value+"/"+rel.name, func(t *testing.T) {
					checkDMDecision(t, Resolve(action, rel.facts, dmPrivacy(value, rel.privacy)), expected)
				})
			}
		}
	}
}

// The order of the early denies is the contract's: block, then recipient
// pause, then sender pause, then no_one.
func TestDMConsentOrder(t *testing.T) {
	cases := []struct {
		name    string
		facts   Facts
		privacy Privacy
		want    string
	}{
		{"block beats recipient pause", Facts{Blocked: true}, Privacy{WhoCanMessage: "everyone_message_requests", ChatAvailability: "paused"}, ReasonUnavailable},
		{"block beats sender pause", Facts{Blocked: true, ActorChatPaused: true}, Privacy{WhoCanMessage: "no_one"}, ReasonUnavailable},
		{"recipient pause beats sender pause", Facts{ActorChatPaused: true}, Privacy{WhoCanMessage: "no_one", ChatAvailability: "paused"}, ReasonMessagesOff},
		{"sender pause beats no_one", Facts{ActorChatPaused: true}, Privacy{WhoCanMessage: "no_one"}, ReasonYouPaused},
		{"no_one beats recipient-follows consent", Facts{TargetFollowsActor: true, IsConnection: true}, Privacy{WhoCanMessage: "no_one"}, ReasonNoOne},
		{"unreadable recipient settings never reveal the fail-safe no_one", Facts{}, Privacy{WhoCanMessage: "no_one", Unavailable: true}, ReasonUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkDMDecision(t, Resolve(ActionMessage, tc.facts, tc.privacy), tc.want)
		})
	}
}

// An accepted 1:1 chat keeps the pair messaging under every value (no_one
// included: "chats you already have stay"); only a block or a pause ends it.
func TestAcceptedChatKeepsMessaging(t *testing.T) {
	for _, value := range []string{"everyone_message_requests", "followers_message_requests", "people_i_follow", "no_one"} {
		for _, action := range []Action{ActionMessage, ActionStartConversation} {
			t.Run(value+"/"+string(action), func(t *testing.T) {
				checkDMDecision(t, Resolve(action, Facts{AcceptedChat: true}, Privacy{WhoCanMessage: value}), outAllow)
			})
		}
	}
	cases := []struct {
		name    string
		facts   Facts
		privacy Privacy
		want    string
	}{
		{"block beats an accepted chat", Facts{AcceptedChat: true, Blocked: true}, Privacy{WhoCanMessage: "everyone_message_requests"}, ReasonUnavailable},
		{"recipient pause beats an accepted chat", Facts{AcceptedChat: true}, Privacy{WhoCanMessage: "everyone_message_requests", ChatAvailability: "paused"}, ReasonMessagesOff},
		{"sender pause beats an accepted chat", Facts{AcceptedChat: true, ActorChatPaused: true}, Privacy{WhoCanMessage: "everyone_message_requests"}, ReasonYouPaused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkDMDecision(t, Resolve(ActionMessage, tc.facts, tc.privacy), tc.want)
		})
	}
}

// Every message deny reason is one of the six the contract lists.
func TestDMConsentReasonsAreTheContractSet(t *testing.T) {
	allowed := map[string]bool{
		ReasonUnavailable: true, ReasonMessagesOff: true, ReasonYouPaused: true,
		ReasonNoOne: true, ReasonFollowersOnly: true, ReasonPeopleIFollow: true,
	}
	for value, want := range dmExpected {
		for rel, out := range want {
			if out != outAllow && out != outRequest && !allowed[out] {
				t.Errorf("%s x %s expects reason %q outside the contract set", value, rel, out)
			}
		}
	}
	if len(allowed) != 6 {
		t.Fatalf("contract lists six reasons, constants define %d", len(allowed))
	}
}

// A block is never revealed, on any action.
func TestBlockIsReportedAsUnavailableForEveryAction(t *testing.T) {
	for _, a := range []Action{
		ActionMessage, ActionStartConversation, ActionCall, ActionConnect, ActionFollow,
		ActionAddToGroup, ActionSeeOnlineStatus, ActionSeeReadReceipts, ActionSeeLastSeen,
		ActionViewProfile, ActionComment, ActionViewPosts,
	} {
		d := Resolve(a, Facts{Blocked: true, IsConnection: true}, Privacy{WhoCanMessage: "everyone_message_requests", WhoCanCall: "accepted_chats_only"})
		if d.Allowed || d.Reason != ReasonUnavailable || d.Fallback != "" {
			t.Errorf("%s on a blocked pair = %+v, want deny reason unavailable", a, d)
		}
	}
}

func TestStartConversationIsAParsedAction(t *testing.T) {
	got := ParseActions([]string{"start_conversation", "message"})
	if len(got) != 2 || got[0] != ActionStartConversation || got[1] != ActionMessage {
		t.Fatalf("ParseActions = %v", got)
	}
}

// Calls under accepted_chats_only (and legacy connections_only, which
// identity maps onto it): an accepted 1:1 chat grants the call, because
// accepting a request no longer creates a graph connection.
func TestCallAcceptedChatsOnly(t *testing.T) {
	cases := []struct {
		name  string
		facts Facts
		value string
		want  bool
	}{
		{"accepted chat allows", Facts{AcceptedChat: true}, "accepted_chats_only", true},
		{"accepted chat allows under legacy connections_only", Facts{AcceptedChat: true}, "connections_only", true},
		{"legacy connection still allows", Facts{IsConnection: true}, "accepted_chats_only", true},
		{"stranger denied", Facts{}, "accepted_chats_only", false},
		{"recipient follows is not a call grant", Facts{TargetFollowsActor: true, ActorFollowsTarget: true}, "accepted_chats_only", false},
		{"no_one beats an accepted chat", Facts{AcceptedChat: true}, "no_one", false},
		{"block beats an accepted chat", Facts{AcceptedChat: true, Blocked: true}, "accepted_chats_only", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Resolve(ActionCall, tc.facts, Privacy{WhoCanCall: tc.value})
			if d.Allowed != tc.want {
				t.Fatalf("allowed = %v, want %v (%+v)", d.Allowed, tc.want, d)
			}
		})
	}
	// The accepted chat grants calls and messaging (TestAcceptedChatKeepsMessaging)
	// and nothing else: no group-adds, no online status.
	f := Facts{AcceptedChat: true}
	p := Privacy{WhoCanMessage: "people_i_follow", WhoCanAddToGroups: "connections_only", WhoCanSeeOnlineStatus: "connections_only"}
	for _, a := range []Action{ActionAddToGroup, ActionSeeOnlineStatus} {
		if d := Resolve(a, f, p); d.Allowed {
			t.Errorf("%s allowed on a bare accepted chat; it must grant calls and messaging only", a)
		}
	}
}
