package service

import (
	"context"
	"errors"
	"strings"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

// ─── THE GROUP ACCESS GATE (audit 2026-11-07, G-M2..G-M4, G-L1) ─────────
//
// Every read and every member-side write of a group goes through one
// decision, decideGroupAccess, reached through gate / authorize. Before this
// file each route carried its own check, and several carried none: the
// rules, channels and wiki of a private group were readable by anyone, a
// stash, echo or view landed in a private group from outside it, and a
// private group answered "forbidden: not a member" where a missing one
// answered "not found", which told an outsider that it existed.
//
// The shape is channel-service's (internal/service/access.go):
//
//   - the decision is pure, so it is tested without a database;
//   - the member lookup fails CLOSED: a database error is returned, never
//     read as "not a member" (which on a public group would admit a banned
//     caller);
//   - a private group answers a non-member exactly as a missing group;
//   - the creator's authority comes from groups.creator_id, not from the
//     member row, so it cannot be removed by banning or demoting that row.

// accessMode says what the caller is about to do.
type accessMode int

const (
	// accessCard: the group's own page (GET /:groupId, /by-handle, join,
	// join request). A banned viewer of a visible group still gets the card,
	// labelled viewer_role "banned", so the client can say so.
	accessCard accessMode = iota
	// accessRead: any of the group's content — feed, posts, comments,
	// events, rules, channels, wiki, media, members, stats.
	accessRead
	// accessWrite: a member-side write (comment, wiki edit, invites list...).
	// Needs an active membership.
	accessWrite
)

// The gate's refusals. ErrGroupNotFound is the one answer for "missing",
// "deleted" and "private and you are not in it"; handleServiceError maps
// "not found" to 404 and "forbidden" to 403.
var (
	ErrGroupNotFound  = errors.New("not found: group not found")
	ErrGroupBanned    = errors.New("forbidden: you are banned from this group")
	ErrNotGroupMember = errors.New("forbidden: only members of this group can do this")
)

// Member ranks. The stored role column holds only admin, moderator and
// member; the owner rank comes from groups.creator_id.
const (
	rankNone = iota
	rankMember
	rankModerator
	rankAdmin
	rankOwner
)

// roleRank is the rank a stored role carries.
func roleRank(role string) int {
	switch role {
	case "admin":
		return rankAdmin
	case "moderator":
		return rankModerator
	case "member":
		return rankMember
	}
	return rankNone
}

// groupIsPrivate: either column marks a members-only group (visibility is
// the legacy one, kept in step by CreateGroup).
func groupIsPrivate(g *store.Group) bool {
	return g.PrivacyLevel == "private" || g.Visibility == "private"
}

func isActive(m *store.GroupMember) bool { return m != nil && m.Status == "active" }
func isBanned(m *store.GroupMember) bool { return m != nil && m.Status == "banned" }

// memberRank is the caller's standing in g: the creator outranks any row,
// otherwise an active row's role, otherwise none.
func memberRank(g *store.Group, viewerID uuid.UUID, m *store.GroupMember) int {
	if viewerID != uuid.Nil && g.CreatorID == viewerID {
		return rankOwner
	}
	if isActive(m) {
		return roleRank(m.Role)
	}
	return rankNone
}

// decideGroupAccess is the whole group-level decision.
//
//	missing or deleted                    -> not found, for everyone
//	the creator                           -> allowed
//	private, not an active member         -> not found (same answer as missing)
//	banned, anything but the card         -> banned
//	write without an active membership    -> not a member
//
// m is the caller's group_members row (any status), nil for none or for an
// anonymous caller.
func decideGroupAccess(g *store.Group, viewerID uuid.UUID, m *store.GroupMember, mode accessMode) error {
	if g == nil || g.Status == "deleted" {
		return ErrGroupNotFound
	}
	if viewerID != uuid.Nil && g.CreatorID == viewerID {
		return nil
	}
	if groupIsPrivate(g) && !isActive(m) {
		return ErrGroupNotFound
	}
	if isBanned(m) && mode != accessCard {
		return ErrGroupBanned
	}
	if mode == accessWrite && (viewerID == uuid.Nil || !isActive(m)) {
		return ErrNotGroupMember
	}
	return nil
}

// groupAccess is what a route gets back from the gate.
type groupAccess struct {
	g      *store.Group
	member *store.GroupMember // the caller's row, any status; nil for none
	rank   int
}

// gate applies decideGroupAccess to a group the caller already holds (GetGroup
// reads it through the cache). The member read fails closed.
func (s *Service) gate(ctx context.Context, g *store.Group, actorID uuid.UUID, mode accessMode) (*groupAccess, error) {
	if g == nil {
		return nil, ErrGroupNotFound
	}
	var m *store.GroupMember
	if actorID != uuid.Nil {
		var err error
		if m, err = s.store.GetMember(ctx, g.ID, actorID); err != nil {
			return nil, err
		}
	}
	if err := decideGroupAccess(g, actorID, m, mode); err != nil {
		return nil, err
	}
	return &groupAccess{g: g, member: m, rank: memberRank(g, actorID, m)}, nil
}

// authorize loads the group and applies the gate: the entry point for every
// route under /v1/groups/:groupId.
func (s *Service) authorize(ctx context.Context, groupID, actorID uuid.UUID, mode accessMode) (*groupAccess, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	return s.gate(ctx, g, actorID, mode)
}

// authorizeRank is authorize for a write that needs at least minRank;
// refusal is the route's own wording for "your role cannot do this".
func (s *Service) authorizeRank(ctx context.Context, groupID, actorID uuid.UUID, minRank int, refusal error) (*groupAccess, error) {
	acc, err := s.authorize(ctx, groupID, actorID, accessWrite)
	if err != nil {
		return nil, err
	}
	if acc.rank < minRank {
		return nil, refusal
	}
	return acc, nil
}

// checkGroupAccess is the read rule: the one predicate the feed, the post
// search and engagementGate share (group_post_search.go explains why there
// is exactly one).
func (s *Service) checkGroupAccess(ctx context.Context, g *store.Group, actorID uuid.UUID) error {
	_, err := s.gate(ctx, g, actorID, accessRead)
	return err
}

// isAccessRefusal reports whether err is a policy refusal from the gate (or
// a route's own "forbidden"), as opposed to an infrastructure failure.
func isAccessRefusal(err error) bool {
	return errors.Is(err, ErrGroupNotFound) || errors.Is(err, ErrGroupBanned) ||
		errors.Is(err, ErrNotGroupMember) || strings.Contains(err.Error(), "forbidden")
}

// settingAllows applies a who_can_* / comment_permission setting to a rank.
// Unknown values behave as all_members, as checkPermission always has.
func settingAllows(setting string, rank int) bool {
	switch setting {
	case "admins_mods":
		return rank >= rankModerator
	case "admins_only":
		return rank >= rankAdmin
	}
	return rank >= rankMember
}

// ─── Moderation authority (G-M2) ────────────────────────────────────────

// Moderation refusals.
var (
	ErrCannotModerateSelf  = errors.New("forbidden: you cannot do this to yourself")
	ErrCannotModerateOwner = errors.New("forbidden: nobody can act on the group owner")
	ErrCannotModeratePeer  = errors.New("forbidden: you can only act on members below your own role")
	ErrOnlyOwnerPromotes   = errors.New("forbidden: only the group owner can promote to admin")
	ErrBanPlacedAbove      = errors.New("forbidden: this ban was placed by a higher role; only that role or above can lift it")
)

// guardMemberModeration is the pure authority rule for removing or banning a
// member, mirroring channel-service's guardMemberModeration:
//
//	only moderators and above may act
//	nobody may act on themselves (leave instead)
//	nobody may act on the owner
//	only a strictly higher rank may act: a moderator on members, an admin on
//	moderators and members, the owner on admins too
func guardMemberModeration(actorRank, targetRank int, actorID, targetID uuid.UUID, refusal error) error {
	if actorRank < rankModerator {
		return refusal
	}
	if actorID == targetID {
		return ErrCannotModerateSelf
	}
	if targetRank == rankOwner {
		return ErrCannotModerateOwner
	}
	if targetRank >= actorRank {
		return ErrCannotModeratePeer
	}
	return nil
}

// guardRoleChange is the authority rule for setting a member's role: admins
// and above only, never on yourself or the owner, only on a lower rank, and
// never to a rank at or above your own (so only the owner makes admins).
func guardRoleChange(actorRank, targetRank, newRank int, actorID, targetID uuid.UUID) error {
	if actorRank < rankAdmin {
		return errors.New("forbidden: only admins can update member roles")
	}
	if actorID == targetID {
		return ErrCannotModerateSelf
	}
	if targetRank == rankOwner {
		return ErrCannotModerateOwner
	}
	if targetRank >= actorRank {
		return ErrCannotModeratePeer
	}
	if newRank >= actorRank {
		return ErrOnlyOwnerPromotes
	}
	return nil
}

// guardUnban: admins and above lift bans, and only a ban placed by someone
// of their rank or below — an admin cannot undo the owner's ban.
func guardUnban(actorRank, bannerRank int) error {
	if actorRank < rankAdmin {
		return errors.New("forbidden: only admins or the owner can unban members")
	}
	if bannerRank > actorRank {
		return ErrBanPlacedAbove
	}
	return nil
}
