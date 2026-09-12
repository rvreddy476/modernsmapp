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
//	COMMUNITIES_ENABLED          true  — false = whole product answers 404
//	COMMUNITIES_PILOT            true  — private-by-default + invite-only join
//	COMMUNITIES_ALLOWED_CREATORS ""    — comma-separated user ids; empty = anyone
//	COMMUNITIES_INVITE_BASE_URL  ""    — prefix for the invite `url` field

// Pilot / policy refusals. Handlers map these to stable wire codes; see
// internal/http/handler.go.
var (
	// ErrPublicCommunityNotAllowed refuses a create/update that would make
	// a community publicly visible while the pilot flag is on.
	ErrPublicCommunityNotAllowed = errors.New("communities are in an invite-only pilot: public communities cannot be created and a private community cannot be made public")
	// ErrCreatorNotAllowlisted refuses creation by a user outside
	// COMMUNITIES_ALLOWED_CREATORS.
	ErrCreatorNotAllowlisted = errors.New("communities are in an invite-only pilot: community creation is restricted to the pilot allowlist")
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
	AllowedCreators []uuid.UUID
	// CreatorAllowlistConfigured records that the env var was non-empty,
	// separately from whether anything parsed out of it. A non-empty value
	// that yields zero valid ids restricts creation to NOBODY (fail closed)
	// rather than silently reverting to "anyone".
	CreatorAllowlistConfigured bool
	// InviteBaseURL prefixes the `url` field of an invite response.
	InviteBaseURL string
}

// DefaultCommunityPolicy is the shipped default: the product is on and the
// pilot is on, because the pilot IS the current state of communities. No
// creator allowlist, which means creation is unrestricted — that is the
// "no named moderation owner yet" state and the boot log says so loudly.
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

	raw := strings.TrimSpace(getenv("COMMUNITIES_ALLOWED_CREATORS"))
	p.CreatorAllowlistConfigured = raw != ""
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := uuid.Parse(part)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("COMMUNITIES_ALLOWED_CREATORS entry %q is not a UUID and was dropped", part))
			continue
		}
		p.AllowedCreators = append(p.AllowedCreators, id)
	}
	if p.CreatorAllowlistConfigured && len(p.AllowedCreators) == 0 {
		warnings = append(warnings, "COMMUNITIES_ALLOWED_CREATORS was set but no entry parsed as a UUID — community creation is now closed to EVERYONE (fail closed). Fix the value or unset it.")
	}
	if p.PilotMode && !p.CreatorAllowlistConfigured {
		warnings = append(warnings, "COMMUNITIES_ALLOWED_CREATORS is EMPTY — ANY authenticated user can create a community. This is the \"no named moderation owner\" state: per the founder's 2026-09-12 decision, access must stay internal-only until an owner is named. Set COMMUNITIES_ALLOWED_CREATORS to the pilot owner's user id.")
	}

	p.InviteBaseURL = strings.TrimSpace(getenv("COMMUNITIES_INVITE_BASE_URL"))
	return p, warnings
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
func (p CommunityPolicy) CreatorAllowed(userID uuid.UUID) bool {
	if !p.PilotMode || !p.CreatorAllowlistConfigured {
		return true
	}
	for _, id := range p.AllowedCreators {
		if id == userID {
			return true
		}
	}
	return false
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
