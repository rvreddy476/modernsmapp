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
	ActionMessage         Action = "message"
	ActionCall            Action = "call"
	ActionConnect         Action = "connect"
	ActionFollow          Action = "follow"
	ActionAddToGroup      Action = "add_to_group"
	ActionSeeOnlineStatus Action = "see_online_status"
	ActionSeeReadReceipts Action = "see_read_receipts"
	ActionSeeLastSeen     Action = "see_last_seen"
	ActionViewProfile     Action = "view_profile"
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
}

// chatPaused reports whether the target has paused chat entirely.
func (p Privacy) chatPaused() bool { return p.ChatAvailability == "paused" }

// Decision is the resolved outcome for one action. Fallback names an
// alternative path when the direct action is denied — e.g. message_direct is
// denied but a Message Request is permitted (spec §9.8 response shape).
type Decision struct {
	Allowed  bool   `json:"allowed"`
	Fallback string `json:"fallback,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Resolve decides a single action.
func Resolve(action Action, f Facts, p Privacy) Decision {
	// A block in either direction denies everything (spec §4).
	if f.Blocked {
		return Decision{Allowed: false, Reason: "blocked"}
	}
	switch action {
	case ActionMessage:
		return resolveMessage(f, p)
	case ActionCall:
		return resolveCall(f, p)
	case ActionConnect:
		return resolveConnect(f, p)
	case ActionFollow:
		// Public-account follow is always allowed; private-account
		// follow-requests are reserved for a later phase (spec §3.1).
		return Decision{Allowed: true}
	case ActionAddToGroup:
		return resolveAddToGroup(f, p)
	case ActionSeeOnlineStatus:
		return resolvePausableVisibility(f, p, p.WhoCanSeeOnlineStatus)
	case ActionSeeReadReceipts:
		return resolvePausableVisibility(f, p, p.WhoCanSeeReadReceipts)
	case ActionSeeLastSeen:
		return resolvePausableVisibility(f, p, p.WhoCanSeeLastSeen)
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

// resolveMessage implements the "Send DM (direct)" row of §4. A non-connection
// who is denied a direct DM may still be offered the Message Request channel
// (Fallback = "message_request").
func resolveMessage(f Facts, p Privacy) Decision {
	// Chat pause is stronger than every who_can_message value and has NO
	// request fallback (directive §3.2/§3.3): a paused account receives no
	// new conversations, requests or messages until it unpauses.
	if p.chatPaused() {
		return Decision{Allowed: false, Reason: "chat_paused"}
	}
	// no_one is stricter than connections_only — even connections cannot
	// message, so it is checked before the connection shortcut.
	if p.WhoCanMessage == "no_one" {
		return Decision{Allowed: false, Reason: "privacy_no_one"}
	}
	if f.IsConnection {
		return Decision{Allowed: true}
	}
	switch p.WhoCanMessage {
	case "connections_and_mutual_followers":
		if f.mutualFollow() {
			return Decision{Allowed: true}
		}
		return Decision{Allowed: false, Reason: "not_connected"}
	case "followers_message_requests":
		if f.ActorFollowsTarget {
			return Decision{Allowed: false, Fallback: "message_request", Reason: "not_connected"}
		}
		return Decision{Allowed: false, Reason: "privacy_disallows"}
	case "friends_of_friends_requests":
		// Directive §3.1/§3.3: a shared accepted connection earns a MESSAGE
		// REQUEST, never a direct thread.
		if f.SecondDegree {
			return Decision{Allowed: false, Fallback: "message_request", Reason: "not_connected"}
		}
		return Decision{Allowed: false, Reason: "privacy_disallows"}
	case "everyone_message_requests":
		return Decision{Allowed: false, Fallback: "message_request", Reason: "not_connected"}
	default: // connections_only
		return Decision{Allowed: false, Reason: "privacy_connections_only"}
	}
}

func resolveCall(f Facts, p Privacy) Decision {
	switch p.WhoCanCall {
	case "no_one":
		return Decision{Allowed: false, Reason: "privacy_no_one"}
	case "connections_only", "accepted_chats_only":
		// accepted_chats_only is approximated as "is a connection" in
		// Phase 1; true accepted-chat state lives in chat-service.
		if f.IsConnection {
			return Decision{Allowed: true}
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
		"message":           ActionMessage,
		"call":              ActionCall,
		"connect":           ActionConnect,
		"follow":            ActionFollow,
		"add_to_group":      ActionAddToGroup,
		"see_online_status": ActionSeeOnlineStatus,
		"see_read_receipts": ActionSeeReadReceipts,
		"see_last_seen":     ActionSeeLastSeen,
		"view_profile":      ActionViewProfile,
	}
	out := make([]Action, 0, len(names))
	for _, n := range names {
		if a, ok := known[n]; ok {
			out = append(out, a)
		}
	}
	return out
}
