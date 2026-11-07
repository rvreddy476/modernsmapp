package service

import (
	"context"
	"errors"

	"github.com/atpost/community-service/internal/store"
	"github.com/google/uuid"
)

// ─── THE COMMUNITY ACCESS GATE (audit 2026-11-07, K-M1, K-M2, K-L1, K-L2) ─
//
// Every read and every member-side write of a community, and of anything in
// one (spaces, posts, engagement, wiki, events, members), goes through one
// decision, decideCommunityAccess, reached through authorize. Before this file
// most routes called the store straight from the handler: a space's posts were
// readable by anyone holding its id, the wiki, spaces, events and featured
// posts of a private community by anyone holding the community id, and an
// event could be created in any community by any signed-in user.
//
// The shape is channel-service's (internal/service/access.go):
//
//   - the decision is pure, so it is tested without a database;
//   - the member lookup fails CLOSED: a database error is returned, never
//     read as "not a member" (which on a public community would admit a
//     banned caller);
//   - a community that is not active (archived, suspended) and a private
//     community the caller is not in answer exactly as a missing one;
//   - the owner's authority comes from communities.owner_id, so it survives
//     a missing or edited member row.

// accessMode says what the caller is about to do.
type accessMode int

const (
	// accessCard: the community's own page and the join route. A banned
	// viewer of a public community still gets the card (viewer_role
	// "banned"), so the client can say so.
	accessCard accessMode = iota
	// accessRead: any of its content. Anonymous callers allowed on public
	// communities.
	accessRead
	// accessWrite: a member-side write. Needs a member.
	accessWrite
)

// ErrCommunityNotFound is every "this community is not yours to see" answer:
// missing, deleted, archived, suspended, or private and you are not in it.
// The message is the one a missing community has always had, so the two
// cannot be told apart by status or by text.
var ErrCommunityNotFound = store.ErrCommunityNotFound

// Content refusals: something named by id that is not in the path community.
var (
	ErrSpaceNotFound = errors.New("not found: space not found")
	ErrPostNotFound  = errors.New("not found: post not found")
	ErrWikiNotFound  = errors.New("not found: wiki page not found")
)

// communityIsPublic: the one public set (K-L2). Discover, the viewer gate and
// the visibility on community.updated all read it.
func communityIsPublic(c *store.Community) bool {
	return store.IsPublicCommunityType(c.CommunityType)
}

// isMemberRole: a role that counts as being in the community. pending,
// suspended and banned do not.
func isMemberRole(role string) bool {
	return roleLevel(role) >= roleLevel("member")
}

// isBannedRole: banned and suspended both remove a member's voice.
func isBannedRole(role string) bool {
	return role == "banned" || role == "suspended"
}

// decideCommunityAccess is the whole community-level decision.
//
//	missing or not active (archived, suspended)  -> not found, for everyone
//	the owner                                    -> allowed
//	not public, caller not a member               -> not found (banned too)
//	banned or suspended, anything but the card    -> banned
//	write without a member role                   -> not a member
//
// role is the caller's community_members role, "" for none or anonymous.
func decideCommunityAccess(c *store.Community, viewerID *uuid.UUID, role string, mode accessMode) error {
	if c == nil || c.Status != "active" {
		return ErrCommunityNotFound
	}
	if viewerID != nil && c.OwnerID == *viewerID {
		return nil
	}
	if !communityIsPublic(c) && !isMemberRole(role) {
		return ErrCommunityNotFound
	}
	if isBannedRole(role) && mode != accessCard {
		return ErrMemberBanned
	}
	if mode == accessWrite && (viewerID == nil || !isMemberRole(role)) {
		return ErrNotCommunityMember
	}
	return nil
}

// effectiveRole is the caller's standing: the owner outranks the member row.
func effectiveRole(c *store.Community, viewerID *uuid.UUID, role string) string {
	if viewerID != nil && c != nil && c.OwnerID == *viewerID {
		return "owner"
	}
	return role
}

// communityAccess is what a route gets back from the gate.
type communityAccess struct {
	c      *store.Community
	member *store.CommunityMember // the caller's row, any role; nil for none
	role   string                 // effectiveRole: "owner" for the owner, "" for none
}

// authorize loads the community and the caller's row and applies the gate.
// A database error on the member read is returned, never read as "not a
// member".
func (s *Service) authorize(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID, mode accessMode) (*communityAccess, error) {
	c, err := s.store.GetCommunityByID(ctx, communityID)
	if errors.Is(err, store.ErrCommunityNotFound) {
		return nil, ErrCommunityNotFound
	}
	if err != nil {
		return nil, err
	}
	var m *store.CommunityMember
	role := ""
	if viewerID != nil {
		if m, err = s.store.GetMember(ctx, communityID, *viewerID); err != nil {
			return nil, err
		}
		if m != nil {
			role = m.Role
		}
	}
	if err := decideCommunityAccess(c, viewerID, role, mode); err != nil {
		return nil, err
	}
	return &communityAccess{c: c, member: m, role: effectiveRole(c, viewerID, role)}, nil
}

// authorizeRole is authorize for a write that needs at least minRole.
func (s *Service) authorizeRole(ctx context.Context, communityID uuid.UUID, actorID uuid.UUID, minRole string) (*communityAccess, error) {
	acc, err := s.authorize(ctx, communityID, &actorID, accessWrite)
	if err != nil {
		return nil, err
	}
	if !isAtLeast(acc.role, minRole) {
		return nil, ErrInsufficientRole
	}
	return acc, nil
}

// spaceInCommunity is the space-level half: a space named by id must belong
// to the path community, and a quarantined space takes no new posts from
// anyone below moderator (K-M1, K-M2).
func spaceInCommunity(sp *store.CommunitySpace, communityID uuid.UUID, role string, forWrite bool) error {
	if sp == nil || sp.CommunityID != communityID {
		return ErrSpaceNotFound
	}
	if forWrite && sp.IsQuarantined && !isAtLeast(role, "moderator") {
		return ErrSpaceQuarantined
	}
	return nil
}

// ErrSpaceQuarantined: a moderator has frozen this space.
var ErrSpaceQuarantined = errors.New("forbidden: this space is quarantined")

// postVisible is the post-level half: a post must belong to the path
// community (never trust the pairing of two path ids) and be published.
func postVisible(p *store.CommunityPost, communityID uuid.UUID) error {
	if p == nil || p.CommunityID != communityID || p.Status != "published" {
		return ErrPostNotFound
	}
	return nil
}

// space loads a space and applies spaceInCommunity; "not found" from the
// store becomes the one ErrSpaceNotFound.
func (s *Service) space(ctx context.Context, acc *communityAccess, spaceID uuid.UUID, forWrite bool) (*store.CommunitySpace, error) {
	sp, err := s.store.GetSpace(ctx, spaceID)
	if errors.Is(err, store.ErrSpaceNotFound) {
		return nil, ErrSpaceNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := spaceInCommunity(sp, acc.c.ID, acc.role, forWrite); err != nil {
		return nil, err
	}
	return sp, nil
}

// post loads a post and applies postVisible.
func (s *Service) post(ctx context.Context, communityID, postID uuid.UUID) (*store.CommunityPost, error) {
	p, err := s.store.GetCommunityPost(ctx, postID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrPostNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := postVisible(p, communityID); err != nil {
		return nil, err
	}
	return p, nil
}

// ─── Moderation authority (K-L1) ────────────────────────────────────────

var (
	// ErrBannedCannotLeave: leaving deleted the ban row, so a banned member
	// left and rejoined (K-L1). The ban stays until a moderator lifts it.
	ErrBannedCannotLeave = errors.New("forbidden: a banned member cannot leave; the ban stays until it is lifted")
	// ErrAlreadyBanned: a second ban rewrote banned_by, which handed the ban
	// to the re-banner and let a moderator lift an admin's ban in two steps.
	ErrAlreadyBanned = errors.New("already banned")
	// ErrBanPlacedAbove: only a role at or above the one that placed a ban
	// may lift it.
	ErrBanPlacedAbove = errors.New("forbidden: this ban was placed by a higher role; only that role or above can lift it")
	// ErrTargetBanned: a role change cannot be used to unban.
	ErrTargetBanned = errors.New("invalid: this member is banned or suspended; lift the ban instead")
)

// assignableRoles are the roles UpdateMemberRole may set. owner is a
// transfer, and banned, suspended and pending are states set by their own
// routes.
var assignableRoles = map[string]bool{
	"admin": true, "moderator": true, "space_manager": true, "expert": true, "member": true,
}

// guardUnban: moderators and above lift bans, and only a ban placed by
// someone of their own level or below.
func guardUnban(actorRole, bannerRole string) error {
	if !isAtLeast(actorRole, "moderator") {
		return errors.New("forbidden: only moderators and above can unban members")
	}
	if roleLevel(bannerRole) > roleLevel(actorRole) {
		return ErrBanPlacedAbove
	}
	return nil
}

// bannerRole is the CURRENT standing of whoever placed a ban: the owner
// through owner_id, otherwise their member role, otherwise none.
func (s *Service) bannerRole(ctx context.Context, c *store.Community, ban *store.CommunityMember) (string, error) {
	if ban.BannedBy == nil {
		return "", nil
	}
	if *ban.BannedBy == c.OwnerID {
		return "owner", nil
	}
	m, err := s.store.GetMember(ctx, c.ID, *ban.BannedBy)
	if err != nil || m == nil {
		return "", err
	}
	return m.Role, nil
}

// authorizeRoleOr is authorizeRole with the route's own wording for "your role
// cannot do this" (the messages clients already show).
func (s *Service) authorizeRoleOr(ctx context.Context, communityID, actorID uuid.UUID, minRole string, refusal error) (*communityAccess, error) {
	acc, err := s.authorizeRole(ctx, communityID, actorID, minRole)
	if errors.Is(err, ErrInsufficientRole) {
		return nil, refusal
	}
	return acc, err
}

// ─── Join modes (founder, 2026-09-12: private by default, invite-only) ──

// privateJoinMode is the join mode a community outside the public set gets
// when none is named, and what a stored "open" on one is read as. Joining it
// files a join request a moderator approves (JoinCommunity); nobody joins it
// directly by holding its id.
const privateJoinMode = "invite_only"

// ErrPrivateCommunityOpen: a private community cannot be open to join.
var ErrPrivateCommunityOpen = errors.New("invalid: a private community cannot be open to join; use invite_only or request")

// joinModeFor is the join mode a community of this type is created or saved
// with. An empty request takes the default ("open" for a public type,
// privateJoinMode otherwise); an explicit "open" on a private type is
// contradictory and refused rather than silently changed.
func joinModeFor(communityType, requested string) (string, error) {
	public := store.IsPublicCommunityType(communityType)
	if requested == "" {
		if public {
			return "open", nil
		}
		return privateJoinMode, nil
	}
	if requested == "open" && !public {
		return "", ErrPrivateCommunityOpen
	}
	return requested, nil
}

// effectiveJoinMode is the join mode the join route applies. A private
// community whose row still says "open" (created before the default changed,
// or turned private without its join mode being touched) is never joined
// directly: it takes requests.
func effectiveJoinMode(c *store.Community) string {
	if c.JoinMode == "open" && !communityIsPublic(c) {
		return privateJoinMode
	}
	return c.JoinMode
}

// ─── Member directory ───────────────────────────────────────────────────

// ErrMemberDirectoryHidden: member_directory=false keeps the member list to
// the community's moderators and above (as group-service's
// member_list_visible).
var ErrMemberDirectoryHidden = errors.New("forbidden: this community's member list is visible to its moderators only")

// memberDirectoryAllowed applies member_directory to the caller's standing.
func memberDirectoryAllowed(c *store.Community, role string) bool {
	return c.MemberDirectory || isAtLeast(role, "moderator")
}

// settleJoinMode is the save-time half of the rule: a save never leaves a
// private community open to join. An "open" the caller named is refused; a
// stored one (the type was just made private, or the row predates the
// private default) becomes privateJoinMode.
func settleJoinMode(c *store.Community, joinModeNamed bool) error {
	if c.JoinMode != "open" || communityIsPublic(c) {
		return nil
	}
	if joinModeNamed {
		return ErrPrivateCommunityOpen
	}
	c.JoinMode = privateJoinMode
	return nil
}
