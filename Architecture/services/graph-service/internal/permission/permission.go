// Package permission resolves "can actor do X to target" decisions from the
// relationship graph and the target's privacy settings — the messaging/
// privacy spec v2 §4 permission matrix.
//
// The matrix logic here is pure: it takes a relationship snapshot (Facts) and
// the target's privacy settings (Privacy) and returns a Decision. Gathering
// those inputs (DB lookups, the privacy fetch) is the caller's job.
package permission

// Action is one entry in the §4 permission matrix.
type Action string

const (
	ActionMessage Action = "message"
	// ActionStartConversation is an alias of ActionMessage (DM consent
	// contract §4 names both); it resolves through the same decision and is
	// keyed by its own name in the response.
	ActionStartConversation Action = "start_conversation"
	ActionCall              Action = "call"
	ActionConnect           Action = "connect"
	ActionFollow            Action = "follow"
	ActionAddToGroup        Action = "add_to_group"
	ActionSeeOnlineStatus   Action = "see_online_status"
	ActionSeeReadReceipts   Action = "see_read_receipts"
	ActionSeeLastSeen       Action = "see_last_seen"
	ActionViewProfile       Action = "view_profile"
	// ActionComment gates commenting on the target's content
	// (allow_comments_from: everyone | friends).
	ActionComment Action = "comment"
	// ActionViewPosts gates reading the target's post surfaces
	// (account_visibility: public | private).
	ActionViewPosts Action = "view_posts"
)

// Facts is the relationship snapshot between an actor and a target.
type Facts struct {
	// Blocked is true when a block exists in EITHER direction (spec §4:
	// a block denies everything regardless of who blocked whom).
	Blocked            bool
	IsConnection       bool
	ActorFollowsTarget bool
	TargetFollowsActor bool
	// SecondDegree is true when actor and target share at least one active
	// accepted connection (friends-of-friends, chat directive §3.1). Computed
	// by the graph store as a bounded EXISTS — adjacency lists never leave
	// the service.
	SecondDegree bool
	// DatingMatch is true when the pair holds an OPEN dating match — the
	// same conversation row that gates dating messages in chat-service
	// (source_app='dating', closed_at IS NULL, neither side departed).
	//
	// A dating match is deliberately NOT a graph connection: it grants the
	// live-call channel and nothing else. It does not make the pair
	// second-degree-adjacent, does not satisfy who_can_message, and does not
	// appear on any friends-scoped surface.
	//
	// It is populated only when the dating-calls feature flag is on, and it
	// fails CLOSED: an unreachable or erroring provider yields false, which
	// reduces the call rule to exactly its pre-flag behaviour.
	DatingMatch bool
	// AcceptedChat is true when the pair holds an ACCEPTED 1:1 chat (a direct
	// conversation, or a message request the recipient accepted). It grants
	// calls under who_can_call=accepted_chats_only, keeps the pair messaging,
	// and satisfies connections_only for online status and last seen (founder,
	// 23 Oct 2026: an accepted chat IS the connection for presence). The
	// state lives in chat message-service; like DatingMatch it is populated
	// only when its provider is wired (ACCEPTED_CHAT_CALLS_ENABLED) and fails
	// CLOSED to false.
	AcceptedChat bool
	// ActorChatPaused is the SENDER's own chat_availability=paused. A paused
	// account cannot start or send either (DM consent contract §2), so the
	// message decision denies it with reason you_paused.
	ActorChatPaused bool
	// ActorSettingsUnavailable is true when the sender's settings could not
	// be read. The message decision fails closed (reason unavailable) rather
	// than guess whether the sender is paused.
	ActorSettingsUnavailable bool
	// ActorHidesOnline / ActorHidesLastSeen are the VIEWER's own presence
	// settings (reciprocity, as on WhatsApp): someone who shares their online
	// status / last seen with nobody, or who paused chat, sees nobody's.
	// Populated only when see_online_status / see_last_seen is requested; an
	// unreadable viewer setting counts as hidden (fail closed).
	ActorHidesOnline   bool
	ActorHidesLastSeen bool
}

func (f Facts) mutualFollow() bool { return f.ActorFollowsTarget && f.TargetFollowsActor }

// Privacy is the subset of the target's privacy settings the matrix consults.
type Privacy struct {
	WhoCanMessage               string
	WhoCanCall                  string
	WhoCanAddToGroups           string
	WhoCanSendConnectionRequest string
	WhoCanSeeOnlineStatus       string
	WhoCanSeeReadReceipts       string
	WhoCanSeeLastSeen           string
	WhoCanSeeProfilePhoto       string
	// ChatAvailability 'paused' is stronger than any who_can_message value:
	// it denies new conversations, requests and group adds toward the target
	// and suppresses the target's presence/receipt disclosures (chat
	// directive §3.2). Empty means 'enabled' (older snapshots).
	ChatAvailability string
	// AccountVisibility is "public" or "private" (TikTok-style private
	// accounts). Empty means UNKNOWN and fails closed for view_posts (treated
	// like private: follower-only) while leaving the follow path public, so a
	// privacy-fetch outage neither leaks a private account's posts nor turns
	// every follow into a lingering request.
	AccountVisibility string
	// AllowCommentsFrom is "everyone" or "friends". Empty means UNKNOWN and
	// denies (fail closed).
	AllowCommentsFrom string
	// Unavailable marks the strict-default snapshot used when the target's
	// real settings could not be fetched. It is never serialised (only
	// successful fetches are cached), so a cached record is always real.
	// The message decision reports it as reason unavailable.
	Unavailable bool `json:"-"`
}

// privateAccount reports whether the target has EXPLICITLY chosen a private
// account. Unknown ("") is deliberately not private here — this drives the
// follow→request conversion, which must not fire on a fetch failure.
func (p Privacy) privateAccount() bool { return p.AccountVisibility == "private" }

// chatPaused reports whether the target has paused chat entirely.
func (p Privacy) chatPaused() bool { return p.ChatAvailability == "paused" }

// Decision is the resolved outcome for one action. Fallback names an
// alternative path when the direct action is denied — e.g. message_direct is
// denied but a Message Request is permitted (spec §9.8 response shape).
//
// For message / start_conversation there are exactly three outcomes:
//
//	allow            {"allowed":true}
//	message_request  {"allowed":false,"fallback":"message_request"}
//	deny             {"allowed":false,"reason":<one of the Reason* consts>}
//
// A block is ALWAYS reported as reason "unavailable", for every action, so a
// permission answer can never be used to discover a block.
type Decision struct {
	Allowed  bool   `json:"allowed"`
	Fallback string `json:"fallback,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Resolve decides a single action.
func Resolve(action Action, f Facts, p Privacy) Decision {
	// A block in either direction denies everything (spec §4). The reason is
	// "unavailable", never "blocked": the blocked party must not be able to
	// tell a block from an unreachable account (DM consent contract §4).
	if f.Blocked {
		return Decision{Allowed: false, Reason: ReasonUnavailable}
	}
	switch action {
	case ActionMessage, ActionStartConversation:
		return resolveMessage(f, p)
	case ActionCall:
		return resolveCall(f, p)
	case ActionConnect:
		return resolveConnect(f, p)
	case ActionFollow:
		// Public-account follow is always allowed. A PRIVATE account is
		// followable too, but through the request channel: the follow endpoint
		// (POST /v1/graph/follow) converts the follow into a pending
		// follow_request and returns {"status":"requested"} instead of
		// creating the edge. Fallback names that channel so clients can render
		// "Requested" instead of "Following".
		if p.privateAccount() && !f.ActorFollowsTarget {
			return Decision{Allowed: true, Fallback: "follow_request", Reason: "private_account"}
		}
		return Decision{Allowed: true}
	case ActionComment:
		return resolveComment(f, p)
	case ActionViewPosts:
		return resolveViewPosts(f, p)
	case ActionAddToGroup:
		return resolveAddToGroup(f, p)
	case ActionSeeOnlineStatus:
		return resolvePresence(f, p, p.WhoCanSeeOnlineStatus, f.ActorHidesOnline)
	case ActionSeeReadReceipts:
		return resolvePausableVisibility(f, p, p.WhoCanSeeReadReceipts)
	case ActionSeeLastSeen:
		return resolvePresence(f, p, p.WhoCanSeeLastSeen, f.ActorHidesLastSeen)
	case ActionViewProfile:
		// Gate on the profile-photo visibility setting — H3 fix. A private-
		// account user (WhoCanSeeProfilePhoto != "everyone") should not have
		// their full profile rendered to strangers.
		return resolveVisibility(f, p.WhoCanSeeProfilePhoto)
	default:
		return Decision{Allowed: false, Reason: "unknown_action"}
	}
}

// ResolveAll decides every requested action against one snapshot.
func ResolveAll(actions []Action, f Facts, p Privacy) map[Action]Decision {
	out := make(map[Action]Decision, len(actions))
	for _, a := range actions {
		out[a] = Resolve(a, f, p)
	}
	return out
}

// Message deny reasons (DM consent contract §4). Every message /
// start_conversation deny carries exactly one of these.
const (
	// ReasonUnavailable covers a block in either direction, a hidden
	// (deactivated / deletion-scheduled) account, and unreadable settings on
	// either side. Clients hide the Message button.
	ReasonUnavailable = "unavailable"
	// ReasonMessagesOff: the recipient has paused messages.
	ReasonMessagesOff = "messages_off"
	// ReasonYouPaused: the sender has paused their own messages.
	ReasonYouPaused = "you_paused"
	// ReasonNoOne: the recipient's who_can_message is no_one.
	ReasonNoOne = "no_one"
	// ReasonFollowersOnly: followers_message_requests and the sender does not
	// follow the recipient — following them would earn a request.
	ReasonFollowersOnly = "followers_only"
	// ReasonPeopleIFollow: people_i_follow and the recipient does not follow
	// the sender.
	ReasonPeopleIFollow = "people_i_follow"
)

// FallbackMessageRequest is the request channel offered instead of a direct
// thread.
const FallbackMessageRequest = "message_request"

// The four who_can_message values (DM consent contract §1).
const (
	WhoCanMessageEveryone      = "everyone_message_requests"
	WhoCanMessageFollowers     = "followers_message_requests"
	WhoCanMessagePeopleIFollow = "people_i_follow"
	WhoCanMessageNoOne         = "no_one"
)

// canonicalWhoCanMessage folds legacy stored values onto the four current
// ones with the same map identity applies on write and in its migration.
// Anything unrecognised (including empty) takes the strictest non-no_one
// value, people_i_follow: an unknown setting never opens a request channel.
func canonicalWhoCanMessage(v string) string {
	switch v {
	case WhoCanMessageEveryone, WhoCanMessageFollowers, WhoCanMessagePeopleIFollow, WhoCanMessageNoOne:
		return v
	case "friends_of_friends_requests":
		return WhoCanMessageFollowers
	default: // connections_only, connections_and_mutual_followers, unknown
		return WhoCanMessagePeopleIFollow
	}
}

// resolveMessage implements the message / start_conversation decision of the
// DM consent contract §4. The order is the contract's and is load-bearing:
//
//  1. block either way            -> deny unavailable (checked in Resolve)
//     settings unreadable (either) -> deny unavailable (fail closed)
//  2. recipient paused            -> deny messages_off
//  3. sender paused               -> deny you_paused
//  4. no_one                      -> deny no_one
//  5. recipient follows sender, or a legacy accepted connection -> allow
//  6. by value:
//     everyone        -> message_request
//     followers       -> sender follows recipient ? message_request : deny followers_only
//     people_i_follow -> deny people_i_follow
//
// "Recipient follows sender" is the consent signal: following someone IS
// consent to hear from them.
func resolveMessage(f Facts, p Privacy) Decision {
	if p.Unavailable || f.ActorSettingsUnavailable {
		return Decision{Allowed: false, Reason: ReasonUnavailable}
	}
	// Pause is stronger than every who_can_message value and has NO request
	// fallback: a paused account receives no new conversations, requests or
	// messages until it unpauses.
	if p.chatPaused() {
		return Decision{Allowed: false, Reason: ReasonMessagesOff}
	}
	if f.ActorChatPaused {
		return Decision{Allowed: false, Reason: ReasonYouPaused}
	}
	// An accepted 1:1 chat is consent already given: the pair keeps
	// messaging under every value, no_one included ("chats you already have
	// stay"). Only a block or a pause, above, ends it.
	if f.AcceptedChat {
		return Decision{Allowed: true}
	}
	value := canonicalWhoCanMessage(p.WhoCanMessage)
	// no_one beats every relationship, the follow-consent and legacy
	// connections included.
	if value == WhoCanMessageNoOne {
		return Decision{Allowed: false, Reason: ReasonNoOne}
	}
	if f.TargetFollowsActor || f.IsConnection {
		return Decision{Allowed: true}
	}
	switch value {
	case WhoCanMessageEveryone:
		return Decision{Allowed: false, Fallback: FallbackMessageRequest}
	case WhoCanMessageFollowers:
		if f.ActorFollowsTarget {
			return Decision{Allowed: false, Fallback: FallbackMessageRequest}
		}
		return Decision{Allowed: false, Reason: ReasonFollowersOnly}
	default: // people_i_follow
		return Decision{Allowed: false, Reason: ReasonPeopleIFollow}
	}
}

// resolveCall implements the "Start call" row of §4, plus the dating-match
// grant: two people who match get chat AND live calls, and nobody else does.
//
//	allow = !blocked
//	        && who_can_call != "no_one"
//	        && (IsConnection || AcceptedChat || DatingMatch)
//
// Order matters and is load-bearing:
//   - Blocked is fatal before this function is reached (top of Resolve), so a
//     blocked pair is denied even while their match row is still open.
//   - no_one is checked FIRST and has no dating exception. Someone who has
//     switched calls off is not reachable by their match either; the match
//     opens a channel, it does not overrule the callee's own setting.
//   - The dating grant sits alongside IsConnection, never above no_one.
//
// DatingMatch and AcceptedChat are false whenever their feature flag is off or
// their provider errored, so this collapses to the previous connection-only
// rule.
func resolveCall(f Facts, p Privacy) Decision {
	switch p.WhoCanCall {
	case "no_one":
		return Decision{Allowed: false, Reason: "privacy_no_one"}
	case "connections_only", "accepted_chats_only":
		// connections_only is the legacy value identity maps onto
		// accepted_chats_only, so both resolve the same way. An accepted 1:1
		// chat grants the call (AcceptedChat, read from chat message-service);
		// a legacy accepted connection keeps granting it, because accepting a
		// message request used to create one.
		if f.IsConnection {
			return Decision{Allowed: true}
		}
		if f.AcceptedChat {
			return Decision{Allowed: true, Reason: "accepted_chat"}
		}
		if f.DatingMatch {
			return Decision{Allowed: true, Reason: "dating_match"}
		}
		return Decision{Allowed: false, Reason: "privacy_connections_only"}
	default:
		return Decision{Allowed: false, Reason: "privacy_disallows"}
	}
}

func resolveConnect(f Facts, p Privacy) Decision {
	if f.IsConnection {
		return Decision{Allowed: false, Reason: "already_connected"}
	}
	if p.WhoCanSendConnectionRequest == "no_one" {
		return Decision{Allowed: false, Reason: "privacy_no_one"}
	}
	// everyone / friends_of_friends* / contacts_only are all permitted in
	// Phase 1 — friend-of-friend and contact gating arrives with the
	// contact-sync work (spec §11 compliance track).
	return Decision{Allowed: true}
}

// resolveComment implements the allow_comments_from row. A block in either
// direction is already fatal at the top of Resolve.
//
//	everyone → allow
//	friends  → mutual follow OR an accepted connection
//	unknown  → deny (fail closed; an unreadable setting is not "everyone")
func resolveComment(f Facts, p Privacy) Decision {
	switch p.AllowCommentsFrom {
	case "everyone":
		return Decision{Allowed: true}
	case "friends":
		if f.IsConnection || f.mutualFollow() {
			return Decision{Allowed: true}
		}
		return Decision{Allowed: false, Reason: "privacy_friends_only"}
	default:
		return Decision{Allowed: false, Reason: "privacy_disallows"}
	}
}

// resolveViewPosts implements the account_visibility row.
//
//	public            → allow
//	private / unknown → only a follower may view (the owner's self-view is
//	                    short-circuited by the caller before the matrix runs)
//
// Unknown deliberately takes the private branch: during a privacy-fetch
// outage a private account's posts must not open up.
func resolveViewPosts(f Facts, p Privacy) Decision {
	if p.AccountVisibility == "public" {
		return Decision{Allowed: true}
	}
	if f.ActorFollowsTarget {
		return Decision{Allowed: true}
	}
	return Decision{Allowed: false, Reason: "private_account"}
}

// resolveAddToGroup implements the group-add row (chat directive §3.4).
//
// Allowed=true means a DIRECT add is permitted (the target consented in
// advance via their setting). Fallback="group_invitation" means the actor may
// create an invitation the target must accept — consent is required and
// membership must NOT be created until the target accepts.
func resolveAddToGroup(f Facts, p Privacy) Decision {
	if p.chatPaused() {
		return Decision{Allowed: false, Reason: "chat_paused"}
	}
	switch p.WhoCanAddToGroups {
	case "no_one":
		return Decision{Allowed: false, Reason: "privacy_no_one"}
	case "everyone_with_approval":
		// "With approval" IS the consent requirement: never a silent add.
		// (This previously resolved Allowed=true; no consumer existed, so no
		// behaviour shipped on the old value.)
		return Decision{Allowed: false, Fallback: "group_invitation", Reason: "consent_required"}
	case "friends_of_friends_invites":
		if f.IsConnection {
			return Decision{Allowed: true}
		}
		if f.SecondDegree {
			return Decision{Allowed: false, Fallback: "group_invitation", Reason: "consent_required"}
		}
		return Decision{Allowed: false, Reason: "privacy_disallows"}
	case "connections_only", "connections_and_contacts":
		if f.IsConnection {
			return Decision{Allowed: true}
		}
		return Decision{Allowed: false, Reason: "privacy_connections_only"}
	default:
		return Decision{Allowed: false, Reason: "privacy_disallows"}
	}
}

// resolvePausableVisibility gates presence-style disclosures. A paused target
// discloses nothing regardless of the per-field setting.
func resolvePausableVisibility(f Facts, p Privacy, setting string) Decision {
	if p.chatPaused() {
		return Decision{Allowed: false, Reason: "chat_paused"}
	}
	return resolveVisibility(f, setting)
}

// ReasonReciprocity denies presence to a viewer who hides their own.
const ReasonReciprocity = "reciprocity"

// resolvePresence gates online status and last seen (founder, 23 Oct 2026):
//
//	target paused                -> deny (chat_paused)
//	viewer hides their own       -> deny (reciprocity, as on WhatsApp)
//	everyone                     -> allow
//	connections_only             -> a legacy accepted connection OR an
//	                                accepted 1:1 chat. Accepting a message
//	                                request no longer creates a connection
//	                                (DM consent model, 20 Oct), so the old
//	                                connection-only check hid presence from
//	                                almost every pair — the "no green dot,
//	                                no last seen" bug, twin of the 22 Oct
//	                                single-tick bug.
//	no_one / unknown             -> deny
//
// A block in either direction is already fatal at the top of Resolve.
func resolvePresence(f Facts, p Privacy, setting string, viewerHides bool) Decision {
	if p.chatPaused() {
		return Decision{Allowed: false, Reason: "chat_paused"}
	}
	if viewerHides {
		return Decision{Allowed: false, Reason: ReasonReciprocity}
	}
	switch setting {
	case "everyone":
		return Decision{Allowed: true}
	case "connections_only":
		if f.IsConnection || f.AcceptedChat {
			return Decision{Allowed: true}
		}
		return Decision{Allowed: false, Reason: "privacy_connections_only"}
	default: // no_one, unknown
		return Decision{Allowed: false, Reason: "privacy_no_one"}
	}
}

// HidesPresence reports whether a user's own setting shares the value with
// nobody (or chat is paused / the settings are unreadable) — the reciprocity
// input for [Facts.ActorHidesOnline] / [Facts.ActorHidesLastSeen].
func HidesPresence(p Privacy, setting string) bool {
	return p.Unavailable || p.chatPaused() || setting == "no_one" || setting == ""
}

func resolveVisibility(f Facts, setting string) Decision {
	switch setting {
	case "everyone":
		return Decision{Allowed: true}
	case "connections_only":
		if f.IsConnection {
			return Decision{Allowed: true}
		}
		return Decision{Allowed: false, Reason: "privacy_connections_only"}
	default: // no_one
		return Decision{Allowed: false, Reason: "privacy_no_one"}
	}
}

// ParseActions maps spec action names to Action values, skipping unknowns.
func ParseActions(names []string) []Action {
	known := map[string]Action{
		"message":            ActionMessage,
		"start_conversation": ActionStartConversation,
		"call":               ActionCall,
		"connect":            ActionConnect,
		"follow":             ActionFollow,
		"add_to_group":       ActionAddToGroup,
		"see_online_status":  ActionSeeOnlineStatus,
		"see_read_receipts":  ActionSeeReadReceipts,
		"see_last_seen":      ActionSeeLastSeen,
		"view_profile":       ActionViewProfile,
		"comment":            ActionComment,
		"view_posts":         ActionViewPosts,
	}
	out := make([]Action, 0, len(names))
	for _, n := range names {
		if a, ok := known[n]; ok {
			out = append(out, a)
		}
	}
	return out
}
