package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/channel-service/internal/store"
	"github.com/google/uuid"
)

// Communities invite-only pilot (2026-09-12).
//
// The founder's decision: "launch as an invite-only pilot with no public
// discovery directory ... Until a named owner is assigned, keep access
// internal-only. Do not expand into a public launch."
//
// Everything in this file is pure policy: it reads no database and has no
// side effects, so every guard below is unit-testable and mutation-checkable.
// See docs/runbooks/communities-invite-only-pilot.md.
//
// Env flags (defaults in DefaultCommunityPolicy):
//
//	COMMUNITIES_ENABLED              true — false = whole product answers 404
//	COMMUNITIES_PILOT                true — private-by-default + invite-only join
//	COMMUNITIES_ALLOWED_CREATORS     ""   — user ids that may CREATE; EMPTY = NOBODY
//	COMMUNITIES_ALLOWED_PARTICIPANTS ""   — user ids that may JOIN; empty = only creators
//	COMMUNITIES_INVITE_BASE_URL      ""   — prefix for the invite `url` field

// Pilot / policy refusals. Handlers map these to stable wire codes; see
// internal/http/handler.go.
var (
	// ErrPublicCommunityNotAllowed refuses a create/update that would make
	// a community publicly visible while the pilot flag is on.
	ErrPublicCommunityNotAllowed = errors.New("communities are in an invite-only pilot: public communities cannot be created and a private community cannot be made public")
	// ErrCreatorNotAllowlisted refuses creation by a user outside
	// COMMUNITIES_ALLOWED_CREATORS. An EMPTY allowlist refuses everyone:
	// see CommunityPolicy.CreatorAllowed.
	ErrCreatorNotAllowlisted = errors.New("communities are in an invite-only pilot: community creation is restricted to the pilot allowlist, which is currently closed")
	// ErrParticipantNotAllowlisted refuses a join by a user outside
	// COMMUNITIES_ALLOWED_PARTICIPANTS (creators included). An invite code
	// is a bearer token, so joining is gated as well as creation, or the
	// pilot is internal-only in name only.
	ErrParticipantNotAllowlisted = errors.New("communities are in an internal-only pilot: joining is restricted to the pilot allowlist")
	// ErrPaidCommunityNotAllowed refuses a paid community or paid
	// membership activation. Paid is OUT of the pilot by decision.
	ErrPaidCommunityNotAllowed = errors.New("paid communities are not part of the communities pilot: paid access, a subscription price and channel_type=paid are all refused")
	// ErrInviteRequired refuses a direct subscribe to a private community.
	ErrInviteRequired = errors.New("this community is invite-only: an invite link is required to join")
	// ErrInviteNotFound is an unknown (or malformed) invite code.
	ErrInviteNotFound = errors.New("invite link not found")
	// ErrInviteNotLive is a revoked, expired or exhausted invite code.
	ErrInviteNotLive = errors.New("invite link is invalid, expired, revoked or exhausted")
	// ErrCommunitiesDisabled is the whole-product kill switch.
	ErrCommunitiesDisabled = errors.New("communities are disabled")
)

// Moderation refusals (member removal / ban).
var (
	ErrCannotModerateSelf      = errors.New("invalid: you cannot remove or ban yourself; unsubscribe instead")
	ErrCannotModerateOwner     = errors.New("forbidden: the channel owner cannot be removed or banned")
	ErrCannotModeratePeerAdmin = errors.New("forbidden: an admin cannot remove or ban another admin; the owner can")
	ErrNotAMember              = errors.New("not a member of this channel")
)

// CommunityPolicy is the resolved launch-safety configuration.
type CommunityPolicy struct {
	// Enabled is COMMUNITIES_ENABLED. False = the whole product is off.
	Enabled bool
	// PilotMode is COMMUNITIES_PILOT. True = private by default, no public
	// communities, invite-only joining.
	PilotMode bool
	// AllowedCreators is the parsed COMMUNITIES_ALLOWED_CREATORS list.
	// EMPTY MEANS NOBODY while PilotMode is on — see CreatorAllowed.
	AllowedCreators []uuid.UUID
	// AllowedParticipants is the parsed COMMUNITIES_ALLOWED_PARTICIPANTS
	// list: who may JOIN a pilot community. Empty means nobody but the
	// allowlisted creators — see ParticipantAllowed.
	AllowedParticipants []uuid.UUID
	// CreatorAllowlistConfigured records that the env var was non-empty,
	// separately from whether anything parsed out of it, so the boot log
	// can tell "nobody configured this" from "this was configured and every
	// entry was malformed". Both refuse creation; only the operator's fix
	// differs.
	CreatorAllowlistConfigured bool
	// ParticipantAllowlistConfigured is the same distinction for joining.
	ParticipantAllowlistConfigured bool
	// InviteBaseURL prefixes the `url` field of an invite response.
	InviteBaseURL string
}

// DefaultCommunityPolicy is the shipped default: the product is on, the
// pilot is on, and both allowlists are empty — which means the pilot admits
// NOBODY. Nothing can be created and nothing can be joined until an
// operator supplies authorised account ids.
//
// That is deliberate and is the founder's 2026-09-12 decision. A default
// that granted creation to every authenticated user whenever the allowlist
// was unset made the enforcement mechanism inert exactly when it was needed,
// and left a boot warning as the only thing between an unconfigured
// deployment and an open product.
func DefaultCommunityPolicy() CommunityPolicy {
	return CommunityPolicy{Enabled: true, PilotMode: true}
}

// LoadCommunityPolicy resolves the policy from an environment lookup.
// Warnings are returned rather than logged so cmd/server owns the boot log
// and tests can assert on them.
func LoadCommunityPolicy(getenv func(string) string) (CommunityPolicy, []string) {
	p := DefaultCommunityPolicy()
	var warnings []string

	if v, ok := parseEnvBool(getenv("COMMUNITIES_ENABLED")); ok {
		p.Enabled = v
	} else if raw := strings.TrimSpace(getenv("COMMUNITIES_ENABLED")); raw != "" {
		warnings = append(warnings, fmt.Sprintf("COMMUNITIES_ENABLED=%q is not a boolean; keeping default true", raw))
	}
	if v, ok := parseEnvBool(getenv("COMMUNITIES_PILOT")); ok {
		p.PilotMode = v
	} else if raw := strings.TrimSpace(getenv("COMMUNITIES_PILOT")); raw != "" {
		warnings = append(warnings, fmt.Sprintf("COMMUNITIES_PILOT=%q is not a boolean; keeping default true", raw))
	}
	if !p.PilotMode {
		warnings = append(warnings, "COMMUNITIES_PILOT=false — communities are NOT in the invite-only pilot: public communities can be created and private communities can be joined without an invite. The founder's 2026-09-12 decision was an invite-only pilot; do not run this in production without a named moderation owner.")
	}

	p.AllowedCreators, p.CreatorAllowlistConfigured, warnings =
		parseAllowlist(getenv, "COMMUNITIES_ALLOWED_CREATORS", warnings)
	p.AllowedParticipants, p.ParticipantAllowlistConfigured, warnings =
		parseAllowlist(getenv, "COMMUNITIES_ALLOWED_PARTICIPANTS", warnings)

	if p.PilotMode {
		if p.CreatorAllowlistConfigured && len(p.AllowedCreators) == 0 {
			warnings = append(warnings, "COMMUNITIES_ALLOWED_CREATORS was set but no entry parsed as a UUID — community creation is CLOSED TO EVERYONE. That is the fail-closed outcome by decision, but it is probably not what was intended: fix the value.")
		}
		if !p.CreatorAllowlistConfigured {
			warnings = append(warnings, "COMMUNITIES_ALLOWED_CREATORS is EMPTY — community creation is CLOSED TO EVERYONE (fail closed, per the founder's 2026-09-12 decision). No account is authorised and none is inferred. Set it to an authorised user id to open the pilot.")
		}
		if !p.ParticipantAllowlistConfigured {
			warnings = append(warnings, "COMMUNITIES_ALLOWED_PARTICIPANTS is EMPTY — only allowlisted creators may join a pilot community; an invite code alone is not enough. An invite is a bearer token, so joining is gated as well as creation.")
		}
	}

	p.InviteBaseURL = strings.TrimSpace(getenv("COMMUNITIES_INVITE_BASE_URL"))
	return p, warnings
}

// parseAllowlist reads one comma-separated UUID allowlist. It returns the
// parsed ids, whether the variable was set at all (non-empty), and the
// warnings so far plus any it added.
//
// "set but unparseable" and "not set" are kept distinct because they need
// different fixes, even though both refuse everyone.
func parseAllowlist(getenv func(string) string, name string, warnings []string) ([]uuid.UUID, bool, []string) {
	raw := strings.TrimSpace(getenv(name))
	configured := raw != ""
	var ids []uuid.UUID
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s entry %q is not a UUID and was dropped", name, part))
			continue
		}
		ids = append(ids, id)
	}
	return ids, configured, warnings
}

// parseEnvBool reports the boolean an env value names, and whether it named
// one at all (so an unset var keeps the default).
func parseEnvBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "t", "true", "yes", "y", "on":
		return true, true
	case "0", "f", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

// CreatorAllowed reports whether this user may create a community.
//
// ─── FAIL CLOSED, BY DECISION ───────────────────────────────────────────
//
// Founder, 2026-09-12: "An empty creator allowlist must permit NOBODY to
// create a community, not everyone. Missing or invalid configuration must
// not enable unrestricted creation."
//
// The first version of this returned true when the allowlist was unset, so
// the pilot's own enforcement mechanism was inert by default and the only
// signal was a boot warning. A warning is not a control: the decision was
// internal-only access, and an unconfigured deployment silently granted
// creation to every authenticated user on the platform.
//
// So while the pilot is on, an EMPTY allowlist — unset, blank, or set to
// something that parsed to no valid ids — permits nobody. There is no
// default creator and none is inferred from ownership, the operator id or
// anything else: the founder has not supplied an authorised UUID, and
// picking one would be inventing the authorisation this gate exists to
// require.
func (p CommunityPolicy) CreatorAllowed(userID uuid.UUID) bool {
	// The load-bearing line. The original read
	//   if !p.PilotMode || !p.CreatorAllowlistConfigured { return true }
	// and that second clause is what made an unconfigured deployment open.
	// Nothing about "was it configured" may appear in this decision: only
	// whether THIS user is on the list.
	if !p.PilotMode {
		return true
	}
	// Belt and braces. The loop below already returns false on an empty
	// list, so this is not the guard — it is here to state the intent at
	// the point someone would otherwise add a fail-open shortcut.
	if len(p.AllowedCreators) == 0 {
		return false
	}
	for _, id := range p.AllowedCreators {
		if id == userID {
			return true
		}
	}
	return false
}

// ParticipantAllowed reports whether this user may JOIN a pilot community.
//
// The creator allowlist controls creation only, which the founder called
// out: "An allowlist controls creation only. Verify that existing
// communities, discovery, invitations and joining cannot expose an
// 'internal-only' pilot to unapproved users."
//
// Creation alone cannot enforce that. An invite code is a bearer token by
// design — an approved creator could mint one and hand it to anybody, and
// the pilot would be internal-only in name only. So joining is gated too,
// and it fails closed the same way.
//
// An allowlisted CREATOR is implicitly a participant: they own the
// community and cannot be locked out of it. That is not a widening — it
// names people the founder has already authorised.
func (p CommunityPolicy) ParticipantAllowed(userID uuid.UUID) bool {
	if !p.PilotMode {
		return true
	}
	for _, id := range p.AllowedParticipants {
		if id == userID {
			return true
		}
	}
	// A creator is a participant in their own community.
	for _, id := range p.AllowedCreators {
		if id == userID {
			return true
		}
	}
	return false
}

// PilotClosed reports that the pilot admits nobody at all: the pilot is on
// and no user is authorised to create or to join. This is the state the
// founder asked for until they supply an authorised UUID and name a
// moderation owner, and cmd/server states it plainly at boot.
func (p CommunityPolicy) PilotClosed() bool {
	return p.PilotMode && len(p.AllowedCreators) == 0 && len(p.AllowedParticipants) == 0
}

// GuardPaidCommunity refuses anything that would create a paid community or
// switch on paid membership.
//
// Founder, 2026-09-12: "Paid communities are OUT of this pilot. Explicitly
// reject new paid-community creation and paid membership/purchase
// activation server-side; do not let type=paid pass merely because it is
// treated as private."
//
// That last clause names the exact hole. VisibilityOf("paid") is "private",
// so the pilot's public-community refusal waved `paid` straight through:
// the one type with money attached was the one type the gate could not see.
// Three things are refused here, because "paid community" is expressible in
// three ways: the channel_type, the paid_access switch, and a non-zero
// subscription price.
//
// This governs NEW writes only. Existing paid communities are not deleted,
// not retyped, and their entitlements and financial records are untouched —
// the founder reserved that for a scoped decision.
func (p CommunityPolicy) GuardPaidCommunity(channelType string, paidAccess bool, subscriptionPriceCents int) error {
	if !p.PilotMode {
		return nil
	}
	if strings.ToLower(strings.TrimSpace(channelType)) == "paid" {
		return ErrPaidCommunityNotAllowed
	}
	if paidAccess {
		return ErrPaidCommunityNotAllowed
	}
	if subscriptionPriceCents > 0 {
		return ErrPaidCommunityNotAllowed
	}
	return nil
}

// IsPublicChannelType reports whether a stored channel_type is publicly
// visible — the same set /discover lists (store.DiscoverChannelsFiltered)
// and the inverse of VisibilityOf == "private".
func IsPublicChannelType(channelType string) bool {
	return VisibilityOf(channelType) == "public"
}

// ResolveCreateChannelType decides the stored channel_type for a create
// request. `visibility` (the community wire field) wins over the raw
// `channel_type`; while the pilot is on, an omitted value becomes PRIVATE
// and anything publicly visible is refused.
func (p CommunityPolicy) ResolveCreateChannelType(visibility, channelType string) (string, error) {
	ct := strings.ToLower(strings.TrimSpace(channelType))
	v, err := channelTypeForVisibility(visibility)
	if err != nil {
		return "", err
	}
	if v != "" {
		ct = v
	}
	if ct == "" {
		// The pilot default. Outside the pilot the historical default was
		// public, which is exactly the leak the pilot closes.
		if p.PilotMode {
			return "private", nil
		}
		return "public", nil
	}
	if !validChannelTypes[ct] {
		return "", fmt.Errorf("invalid: channel_type is not valid")
	}
	if p.PilotMode && IsPublicChannelType(ct) {
		return "", ErrPublicCommunityNotAllowed
	}
	return ct, nil
}

// GuardVisibilityChange refuses an update that would make a private
// community public while the pilot is on. `current` is the stored
// channel_type, `requested` the one the update resolved to. A legacy public
// channel that stays public is untouched — only the private → public flip is
// blocked, whether it arrives as `visibility` or as a raw `channel_type`.
func (p CommunityPolicy) GuardVisibilityChange(current, requested string) error {
	if !p.PilotMode {
		return nil
	}
	if IsPublicChannelType(current) {
		return nil
	}
	if IsPublicChannelType(requested) {
		return ErrPublicCommunityNotAllowed
	}
	return nil
}

// GuardDirectSubscribe refuses a direct POST /{id}/subscribe on a private
// community while the pilot is on: joining goes through an invite code.
// Public (legacy) channels keep working.
func (p CommunityPolicy) GuardDirectSubscribe(channelType string) error {
	if !p.PilotMode {
		return nil
	}
	if IsPublicChannelType(channelType) {
		return nil
	}
	return ErrInviteRequired
}

// channelIsServable reports whether a channel row in this status may be
// read, listed or fanned out. Only 'active' may: 'suspended' is the
// single-channel emergency disable, 'archived' and 'deleted' are terminal.
// GetChannelByID/GetMyChannels only exclude 'deleted' at SQL level, so this
// is the gate that makes `suspended` mean something.
func channelIsServable(status string) bool {
	return status == "active"
}

// channelReadable is the whole GET /{id} read decision, kept pure so it can
// be tested without a database:
//
//  1. a non-active channel reads as NOT FOUND (the suspension gate — the
//     per-channel emergency disable, and the reason `suspended` means
//     something now that GetChannelByID only excludes 'deleted');
//  2. a private/paid channel reads as NOT FOUND for anyone who is not the
//     owner or an active member (audit CCh3 — an outsider should not even
//     learn the community exists).
//
// viewerRole is "" for a non-member or an unauthenticated caller.
func channelReadable(ch *store.BroadcastChannel, isOwner bool, viewerRole string) error {
	if ch == nil || !channelIsServable(ch.Status) {
		return ErrChannelNotFound
	}
	if VisibilityOf(ch.ChannelType) == "private" && !isOwner {
		if viewerRole == "" || viewerRole == "banned" {
			return ErrChannelNotFound
		}
	}
	return nil
}

// servableChannels drops non-active rows from a list. GetMyChannels reads
// `status != 'deleted'` in SQL, so without this a suspended community would
// still appear on its owner's and members' /my list.
func servableChannels(in []store.BroadcastChannel) []store.BroadcastChannel {
	out := make([]store.BroadcastChannel, 0, len(in))
	for i := range in {
		if channelIsServable(in[i].Status) {
			out = append(out, in[i])
		}
	}
	return out
}

// WithCommunityPolicy installs the launch-safety policy. cmd/server calls
// this once at boot; New() starts from DefaultCommunityPolicy so a caller
// that forgets still gets the pilot, not the open product.
func (s *Service) WithCommunityPolicy(p CommunityPolicy) *Service {
	s.policy = p
	return s
}

// CommunityPolicy returns the installed policy.
func (s *Service) CommunityPolicy() CommunityPolicy {
	return s.policy
}
