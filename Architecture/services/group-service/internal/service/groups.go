package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

// --- Groups ---

// CreateGroup creates a new group with the actor as admin.
func (s *Service) CreateGroup(ctx context.Context, actorID uuid.UUID, req CreateGroupParams) (*store.Group, error) {
	/*
		A replay is answered before anything else.

		Every check below — the rate limit, the name, the handle — treats this
		as a new group. On a retry of a request that already succeeded they are
		all wrong: the handle check finds the group the FIRST attempt created
		and reports "handle is already taken", and the rate limit counts one
		intent twice. Someone whose connection dropped during "Create group"
		therefore saw a conflict about a group they had just made, and pressing
		the button again could never clear it.

		Idempotency exists precisely so a repeated request returns the original
		result, so that lookup comes first.
	*/
	if existing, err := s.store.FindGroupByIdempotencyKey(ctx, actorID, req.IdempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		// ensureGroupChat is idempotent and runs on the original path too, so
		// a replay also repairs a group whose chat conversation never landed.
		if err := s.ensureGroupChat(ctx, existing); err != nil {
			return nil, err
		}
		return existing, nil
	}

	// Rate limit: 5 groups/day
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:group_create:%s", actorID), 5, 24*time.Hour) {
		return nil, fmt.Errorf("rate_limited: maximum 5 groups per day")
	}

	// Validate name
	if err := ValidateGroupName(req.Name); err != nil {
		return nil, err
	}

	// GCC Phase 1: group type. Read before the privacy defaults, because a
	// "private" or "hidden" type decides the privacy (G-L1).
	groupType := req.GroupType
	if groupType == "" {
		groupType = "public"
	}
	if err := ValidateGroupType(groupType); err != nil {
		return nil, err
	}

	// Default values
	privacyLevel, err := privacyForGroupType(groupType, req.PrivacyLevel)
	if err != nil {
		return nil, err
	}
	if privacyLevel == "" {
		privacyLevel = "public"
	}
	joinMode := req.JoinMode
	if joinMode == "" {
		joinMode = "open"
		if privacyLevel == "private" {
			// The only join mode a private group admits.
			joinMode = "invite_only"
		}
	}
	whoCanPost := req.WhoCanPost
	if whoCanPost == "" {
		whoCanPost = "all_members"
	}
	whoCanInvite := req.WhoCanInvite
	if whoCanInvite == "" {
		whoCanInvite = "all_members"
	}

	// Validate privacy+join combo
	if err := ValidatePrivacyJoinCombo(privacyLevel, joinMode); err != nil {
		return nil, err
	}
	if err := ValidateWhoCanPost(whoCanPost); err != nil {
		return nil, err
	}
	if err := ValidateWhoCanInvite(whoCanInvite); err != nil {
		return nil, err
	}

	// Handle
	handle := req.Handle
	if handle == "" {
		handle = SlugifyName(req.Name)
	}
	if handle != "" {
		if err := ValidateHandle(handle); err != nil {
			return nil, err
		}
		// Check availability in groups
		available, err := s.store.CheckHandleAvailability(ctx, handle)
		if err != nil {
			return nil, err
		}
		if !available {
			return nil, fmt.Errorf("handle '%s' is already taken", handle)
		}
		// Cross-check with user-service
		if taken := s.checkHandleInUserService(handle); taken {
			return nil, fmt.Errorf("handle '%s' is already taken by a user", handle)
		}
	}

	// Map privacy_level to visibility for backward compat
	visibility := "public"
	if privacyLevel == "private" {
		visibility = "private"
	}

	// Comment permission
	commentPermission := req.CommentPermission
	if commentPermission == "" {
		commentPermission = "all_members"
	}
	if err := ValidateCommentPermission(commentPermission); err != nil {
		return nil, err
	}

	memberListVisible := true
	if req.MemberListVisible != nil {
		memberListVisible = *req.MemberListVisible
	}
	linkSharing := true
	if req.LinkSharing != nil {
		linkSharing = *req.LinkSharing
	}

	joinQuestions := req.JoinQuestions
	if joinQuestions == nil {
		joinQuestions = json.RawMessage("[]")
	}

	topicTags := req.TopicTags
	if topicTags == nil {
		topicTags = []string{}
	}

	g := &store.Group{
		Name:              req.Name,
		Description:       req.Description,
		CreatorID:         actorID,
		Visibility:        visibility,
		Handle:            handle,
		Category:          req.Category,
		PrivacyLevel:      privacyLevel,
		JoinMode:          joinMode,
		WhoCanPost:        whoCanPost,
		WhoCanInvite:      whoCanInvite,
		Location:          req.Location,
		Language:          req.Language,
		Status:            "active",
		GroupType:         groupType,
		MaxMembers:        req.MaxMembers,
		JoinQuestions:     joinQuestions,
		TopicTags:         topicTags,
		CommentPermission: commentPermission,
		MemberListVisible: memberListVisible,
		LinkSharing:       linkSharing,
		IsMature:          req.IsMature,
	}

	existingID, err := s.store.CreateGroup(ctx, g, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if existingID != nil {
		g, err = s.store.GetGroupByID(ctx, *existingID)
		if err != nil || g == nil {
			return g, err
		}
	}
	if err := s.ensureGroupChat(ctx, g); err != nil {
		return nil, err
	}

	// Publish event
	if existingID == nil {
		s.publishEvent(func() error {
			return s.producer.PublishGroupCreated(ctx, g.ID, actorID, g.Name, g.Visibility)
		})
	}

	return g, nil
}

// CreateGroupParams holds all parameters for creating a group.
type CreateGroupParams struct {
	Name           string
	Description    string
	Handle         string
	Category       string
	PrivacyLevel   string
	JoinMode       string
	WhoCanPost     string
	WhoCanInvite   string
	Location       string
	Language       string
	IdempotencyKey string
	// GCC Phase 1 fields
	GroupType         string
	MaxMembers        int
	JoinQuestions     json.RawMessage
	TopicTags         []string
	CommentPermission string
	MemberListVisible *bool
	LinkSharing       *bool
	IsMature          bool
}

var validGroupTypes = map[string]bool{
	"public": true, "private": true, "hidden": true, "local": true,
	"study": true, "marketplace": true, "brand": true, "event": true, "family": true,
}

var validCommentPermissions = map[string]bool{
	"all_members": true, "admins_mods": true, "admins_only": true,
}

// ValidateGroupType checks that a group type is one of the allowed values.
func ValidateGroupType(gt string) error {
	if !validGroupTypes[gt] {
		return fmt.Errorf("invalid group type '%s'", gt)
	}
	return nil
}

// ValidateCommentPermission checks that comment permission is valid.
func ValidateCommentPermission(cp string) error {
	if !validCommentPermissions[cp] {
		return fmt.Errorf("invalid comment permission '%s'", cp)
	}
	return nil
}

/*
	displayRole is the role to SHOW for a member of a group.

	group_members.role has a CHECK constraint allowing only 'admin',
	'moderator' and 'member' — there is no owner row to store. Ownership lives
	in groups.creator_id, so "owner" is derived, never read.

	It lives here because it was previously derived in GetGroup and
	GetGroupByHandle and NOT in ListMembers, so the creator was "Owner" on the
	cover and "Admin" in the Members tab: one person holding two roles on one
	screen. Deriving it in three places was the bug; deriving it in one is the
	fix.

	Display only. Every permission check still reads the stored role, and the
	creator is an admin there — promoting the label must not quietly grant
	anything.
*/
func displayRole(memberRole string, isCreator bool) string {
	if isCreator && memberRole == "admin" {
		return "owner"
	}
	return memberRole
}

// GroupWithViewerRole wraps a Group with the viewer's role.
type GroupWithViewerRole struct {
	store.Group
	ViewerRole string `json:"viewer_role"`
}

// GetGroup returns a group, using cache-aside with Redis.
//
// A private group the viewer is not in answers exactly as a missing one
// (nil, nil -> the handler's 404), so the page cannot be used to learn that
// a private group exists (G-M4).
func (s *Service) GetGroup(ctx context.Context, actorID, groupID uuid.UUID) (*GroupWithViewerRole, error) {
	cacheKey := fmt.Sprintf("group:%s", groupID)

	var g *store.Group

	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var cached store.Group
		if err := json.Unmarshal([]byte(val), &cached); err == nil {
			g = &cached
		}
	}

	if g == nil {
		g, err = s.store.GetGroupByID(ctx, groupID)
		if err != nil {
			return nil, err
		}
		if g == nil {
			return nil, nil
		}

		go func() {
			data, _ := json.Marshal(g)
			s.rdb.Set(context.Background(), cacheKey, data, 60*time.Second)
		}()
	}

	acc, err := s.cardAccess(ctx, g, actorID)
	if err != nil || acc == nil {
		return nil, err
	}
	viewerRole := cardViewerRole(acc.member, displayRole(roleOf(acc.member), g.CreatorID == actorID))
	return &GroupWithViewerRole{Group: *g, ViewerRole: viewerRole}, nil
}

// GetGroupByHandle returns a group by its handle.
func (s *Service) GetGroupByHandle(ctx context.Context, actorID uuid.UUID, handle string) (*GroupWithViewerRole, error) {
	g, err := s.store.GetGroupByHandle(ctx, handle)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, nil
	}
	acc, err := s.cardAccess(ctx, g, actorID)
	if err != nil || acc == nil {
		return nil, err
	}
	viewerRole := cardViewerRole(acc.member, displayRole(roleOf(acc.member), g.CreatorID == actorID))
	return &GroupWithViewerRole{Group: *g, ViewerRole: viewerRole}, nil
}

// cardAccess is the gate in card mode for GetGroup and GetGroupByHandle. A
// refusal as "not found" becomes (nil, nil), which the handler answers with the
// same 404 as a missing group.
func (s *Service) cardAccess(ctx context.Context, g *store.Group, actorID uuid.UUID) (*groupAccess, error) {
	acc, err := s.gate(ctx, g, actorID, accessCard)
	if errors.Is(err, ErrGroupNotFound) {
		return nil, nil
	}
	return acc, err
}

// cardViewerRole is the viewer_role on the card: the shown role for an active
// member, "banned" for a banned one, "outsider" otherwise. shown is the
// caller's displayRole result for an active row.
func cardViewerRole(member *store.GroupMember, shown string) string {
	switch {
	case isActive(member):
		return shown
	case isBanned(member):
		return "banned"
	}
	return "outsider"
}

// CheckHandle checks handle availability across groups and user-service.
func (s *Service) CheckHandle(ctx context.Context, actorID uuid.UUID, handle string) (bool, error) {
	// Rate limit: 30 handle checks/min
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:handle_check:%s", actorID), 30, time.Minute) {
		return false, fmt.Errorf("rate_limited: too many handle checks")
	}

	if err := ValidateHandle(handle); err != nil {
		return false, err
	}

	available, err := s.store.CheckHandleAvailability(ctx, handle)
	if err != nil {
		return false, err
	}
	if !available {
		return false, nil
	}

	// Cross-check with user-service
	if taken := s.checkHandleInUserService(handle); taken {
		return false, nil
	}

	return true, nil
}

var errOnlyAdminsUpdate = errors.New("forbidden: only admins can update the group")

// UpdateGroup updates a group's details. Only admins or the owner may update.
//
// avatar and cover are the NEW media, nil meaning "keep the current one": the
// current one is read here, under the group's media lock, never from a read
// the caller made earlier (media_claims.go). A new one must be the actor's own
// finished upload and is claimed before the row names it.
func (s *Service) UpdateGroup(ctx context.Context, actorID, groupID uuid.UUID, name, desc string, avatar, cover *uuid.UUID, visibility string) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankAdmin, errOnlyAdminsUpdate); err != nil {
		return err
	}

	// Every save rewrites the avatar and cover, so every save is serialised
	// with the other writers of this row's media, and re-reads them under
	// the lock.
	unlock, err := s.lockMediaHolder(ctx, groupID)
	if err != nil {
		return err
	}
	defer unlock()
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return ErrGroupNotFound
	}
	nextAvatar, nextCover := keepUnlessSet(g.AvatarMediaID, avatar), keepUnlessSet(g.CoverMediaID, cover)
	if err := s.media.Attach(ctx, actorID, groupMediaChanges(groupID, g.AvatarMediaID, g.CoverMediaID, nextAvatar, nextCover), func() error {
		return s.store.UpdateGroup(ctx, groupID, name, desc, nextAvatar, nextCover, visibility)
	}); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupUpdated(ctx, groupID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// UpdateGroupSettings updates GCC Phase 1 settings fields for a group.
func (s *Service) UpdateGroupSettings(ctx context.Context, actorID, groupID uuid.UUID,
	groupType string, maxMembers int, joinQuestions json.RawMessage, topicTags []string,
	commentPermission string, memberListVisible, linkSharing bool) error {

	acc, err := s.authorizeRank(ctx, groupID, actorID, rankAdmin, errOnlyAdminsUpdate)
	if err != nil {
		return err
	}

	if err := ValidateGroupType(groupType); err != nil {
		return err
	}
	if err := ValidateCommentPermission(commentPermission); err != nil {
		return err
	}
	// G-L1: a "private" or "hidden" type is a promise that outsiders cannot
	// see the group, which only privacy_level keeps. It is set at creation
	// (CreateGroup maps the type onto it); a settings save cannot quietly
	// claim it for a group that is still public.
	if groupTypeNeedsPrivacy(groupType) && acc.g.PrivacyLevel != "private" {
		return fmt.Errorf("invalid: group_type %q needs privacy_level private, which is set when the group is created", groupType)
	}

	if err := s.store.UpdateGroupSettings(ctx, groupID, groupType, maxMembers, joinQuestions, topicTags, commentPermission, memberListVisible, linkSharing); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupUpdated(ctx, groupID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// DeleteGroup soft-deletes a group. Only the creator may delete.
func (s *Service) DeleteGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankOwner,
		errors.New("forbidden: only the group creator can delete the group")); err != nil {
		return err
	}

	unlock, err := s.lockMediaHolder(ctx, groupID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.store.DeleteGroup(ctx, groupID); err != nil {
		return err
	}
	// A deleted group is never shown again (no restore path): its avatar,
	// cover and every event's cover stop being held (media_claims.go).
	s.releaseGroupMedia(ctx, groupID)

	s.publishEvent(func() error {
		return s.producer.PublishGroupDeleted(ctx, groupID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// ArchiveGroup archives a group. Only the owner may archive.
func (s *Service) ArchiveGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankOwner,
		errors.New("forbidden: only the group owner can archive")); err != nil {
		return err
	}

	if err := s.store.ArchiveGroup(ctx, groupID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupArchived(ctx, groupID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// --- Discovery ---

// GetMyGroups returns groups that the actor is a member of.
func (s *Service) GetMyGroups(ctx context.Context, actorID uuid.UUID, limit, offset int) ([]store.Group, error) {
	return s.store.ListGroupsByUser(ctx, actorID, limit, offset)
}

// DiscoverGroups returns public, non-archived groups for discovery.
func (s *Service) DiscoverGroups(ctx context.Context, limit, offset int, groupType string) ([]store.Group, error) {
	if groupType != "" {
		if err := ValidateGroupType(groupType); err != nil {
			return nil, err
		}
		return s.store.DiscoverPublicGroupsByType(ctx, groupType, limit, offset)
	}
	return s.store.DiscoverPublicGroups(ctx, limit, offset)
}

// SearchGroups performs full-text search on group names.
func (s *Service) SearchGroups(ctx context.Context, query string, limit, offset int) ([]store.Group, error) {
	return s.store.SearchGroups(ctx, query, limit, offset)
}

// DiscoveredGroup is a discover result with human-readable reasons the
// client can render under the card ("2 friends are in this space", ...).
type DiscoveredGroup struct {
	store.Group
	FriendsInGroup int      `json:"friends_in_group"`
	Reasons        []string `json:"reasons"`
}

// DiscoverGroupsForUser ranks joinable public + request-to-join spaces for
// the viewer (friends inside > category affinity > same area > popularity)
// and attaches the reasons behind each suggestion. Falls back to plain
// popularity ordering naturally when the viewer has no friends or spaces
// yet (all personalization terms score zero).
func (s *Service) DiscoverGroupsForUser(ctx context.Context, actorID uuid.UUID, limit, offset int, groupType string) ([]DiscoveredGroup, error) {
	if groupType != "" {
		if err := ValidateGroupType(groupType); err != nil {
			return nil, err
		}
	}
	scored, err := s.store.DiscoverGroupsForUser(ctx, actorID, groupType, limit, offset)
	if err != nil {
		return nil, err
	}
	out := make([]DiscoveredGroup, 0, len(scored))
	for _, d := range scored {
		var reasons []string
		switch {
		case d.FriendsInGroup == 1:
			reasons = append(reasons, "1 friend is in this space")
		case d.FriendsInGroup > 1:
			reasons = append(reasons, fmt.Sprintf("%d friends are in this space", d.FriendsInGroup))
		}
		if d.CategoryMatch && d.Category != "" {
			reasons = append(reasons, "Matches your interest in "+d.Category)
		}
		if d.LocationMatch && d.Location != "" {
			reasons = append(reasons, "Active in "+d.Location)
		}
		if len(reasons) == 0 {
			if d.MemberCount >= 10 {
				reasons = append(reasons, "Popular on VChat")
			} else {
				reasons = append(reasons, "New space")
			}
		}
		out = append(out, DiscoveredGroup{Group: d.Group, FriendsInGroup: d.FriendsInGroup, Reasons: reasons})
	}
	return out, nil
}

// groupTypeNeedsPrivacy: the group types whose name promises that outsiders
// cannot see the group. Only privacy_level keeps that promise (the access gate
// and every discover/search query read it), so these types force it.
func groupTypeNeedsPrivacy(groupType string) bool {
	return groupType == "private" || groupType == "hidden"
}

// privacyForGroupType maps group_type onto privacy_level at create time
// (G-L1). Before, a "hidden" group kept privacy_level public and was listed by
// discover. A request that names a private type and a non-private level is
// contradictory and refused rather than silently resolved either way.
func privacyForGroupType(groupType, requested string) (string, error) {
	if !groupTypeNeedsPrivacy(groupType) {
		return requested, nil
	}
	if requested != "" && requested != "private" {
		return "", fmt.Errorf("invalid: group_type %q needs privacy_level private, not %q", groupType, requested)
	}
	return "private", nil
}

// roleOf is a member row's stored role, "" for none.
func roleOf(m *store.GroupMember) string {
	if m == nil {
		return ""
	}
	return m.Role
}
