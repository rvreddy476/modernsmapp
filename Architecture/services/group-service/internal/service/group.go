package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	groupevents "github.com/atpost/group-service/internal/events"
	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store              *store.Store
	rdb                *redis.Client
	messageServiceURL  string
	postServiceURL     string
	userServiceURL     string
	graphServiceURL    string
	jwtSecret          string
	internalServiceKey string
	chatClient         *http.Client
	postClient         *http.Client
	notifyClient       *http.Client
	userClient         *http.Client
	graphClient        *http.Client
	producer           *groupevents.Producer
	rateLimiter        *RateLimiter
}

func New(s *store.Store, rdb *redis.Client, msgURL, postURL, userURL, jwtSecret string) *Service {
	svc := &Service{
		store:             s,
		rdb:               rdb,
		messageServiceURL: msgURL,
		postServiceURL:    postURL,
		userServiceURL:    userURL,
		jwtSecret:         jwtSecret,
		rateLimiter:       NewRateLimiter(rdb),
	}
	svc.chatClient = httpclient.NewWithBreaker(5*time.Second, "group->chat")
	svc.postClient = httpclient.NewWithBreaker(5*time.Second, "group->post")
	svc.notifyClient = httpclient.NewWithBreaker(5*time.Second, "group->notification")
	svc.userClient = httpclient.NewWithBreaker(5*time.Second, "group->user")
	svc.graphClient = httpclient.NewWithBreaker(5*time.Second, "group->graph")
	return svc
}

// SetGraphServiceURL wires the permission authority used by invites. Left
// empty, every invite is refused — see canAddToGroup.
func (s *Service) SetGraphServiceURL(url string) {
	s.graphServiceURL = strings.TrimSpace(url)
}

// SetProducer sets the Kafka producer (called after init in main.go).
func (s *Service) SetProducer(p *groupevents.Producer) {
	s.producer = p
}

func (s *Service) SetInternalServiceKey(key string) {
	s.internalServiceKey = strings.TrimSpace(key)
}

func (s *Service) publishEvent(fn func() error) {
	if s.producer == nil {
		return
	}
	if err := fn(); err != nil {
		slog.Warn("failed to publish event", "error", err)
	}
}

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

	// Default values
	privacyLevel := req.PrivacyLevel
	if privacyLevel == "" {
		privacyLevel = "public"
	}
	joinMode := req.JoinMode
	if joinMode == "" {
		joinMode = "open"
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

	// GCC Phase 1: group type
	groupType := req.GroupType
	if groupType == "" {
		groupType = "public"
	}
	if err := ValidateGroupType(groupType); err != nil {
		return nil, err
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

// GroupWithViewerRole wraps a Group with the viewer's role.
type GroupWithViewerRole struct {
	store.Group
	ViewerRole string `json:"viewer_role"`
}

// GetGroup returns a group, using cache-aside with Redis.
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

	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	viewerRole := "outsider"
	member, err := s.store.GetMember(ctx, g.ID, actorID)
	if err == nil && member != nil {
		if member.Status == "active" {
			viewerRole = member.Role
			if member.Role == "admin" && g.CreatorID == actorID {
				viewerRole = "owner"
			}
		} else if member.Status == "banned" {
			viewerRole = "banned"
		}
	}

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

	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	viewerRole := "outsider"
	member, err := s.store.GetMember(ctx, g.ID, actorID)
	if err == nil && member != nil {
		if member.Status == "active" {
			viewerRole = member.Role
			if member.Role == "admin" && g.CreatorID == actorID {
				viewerRole = "owner"
			}
		} else if member.Status == "banned" {
			viewerRole = "banned"
		}
	}

	return &GroupWithViewerRole{Group: *g, ViewerRole: viewerRole}, nil
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

func (s *Service) checkGroupAccess(ctx context.Context, g *store.Group, actorID uuid.UUID) error {
	if g.PrivacyLevel == "private" || g.Visibility == "private" {
		isMember, err := s.store.CheckMembership(ctx, g.ID, actorID)
		if err != nil {
			return err
		}
		if !isMember {
			return fmt.Errorf("forbidden: not a member of this private group")
		}
	}
	return nil
}

// UpdateGroup updates a group's details. Only admins or the owner may update.
func (s *Service) UpdateGroup(ctx context.Context, actorID, groupID uuid.UUID, name, desc string, avatar, cover *uuid.UUID, visibility string) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	member, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil || (member.Role != "admin" && g.CreatorID != actorID) {
		return fmt.Errorf("forbidden: only admins can update the group")
	}

	if err := s.store.UpdateGroup(ctx, groupID, name, desc, avatar, cover, visibility); err != nil {
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

	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	member, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil || (member.Role != "admin" && g.CreatorID != actorID) {
		return fmt.Errorf("forbidden: only admins can update the group")
	}

	if err := ValidateGroupType(groupType); err != nil {
		return err
	}
	if err := ValidateCommentPermission(commentPermission); err != nil {
		return err
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

// DeleteGroup soft-deletes a group. Only the creator (who is an admin) may delete.
func (s *Service) DeleteGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	member, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil || member.Role != "admin" || g.CreatorID != actorID {
		return fmt.Errorf("forbidden: only the group creator can delete the group")
	}

	if err := s.store.DeleteGroup(ctx, groupID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupDeleted(ctx, groupID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// ArchiveGroup archives a group. Only the owner may archive.
func (s *Service) ArchiveGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}
	if g.CreatorID != actorID {
		return fmt.Errorf("forbidden: only the group owner can archive")
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

// --- Membership ---

// JoinGroup allows a user to join a group based on its join_mode.
func (s *Service) JoinGroup(ctx context.Context, actorID, groupID uuid.UUID) (string, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return "", err
	}
	if g == nil {
		return "", fmt.Errorf("group not found")
	}

	// Check if banned
	banned, err := s.store.CheckBanned(ctx, groupID, actorID)
	if err != nil {
		return "", err
	}
	if banned {
		return "", fmt.Errorf("forbidden: you are banned from this group")
	}

	// Audit HG6: the max-members cap is enforced atomically inside the
	// store's AddMemberWithMax under a row-level lock, so two
	// concurrent joins can no longer both pass a stale precheck and
	// push the group over its limit.

	switch g.JoinMode {
	case "open":
		if err := s.store.AddMemberWithMax(ctx, groupID, actorID, "member", g.MaxMembers); err != nil {
			return "", err
		}
		if g.ChatConversationID != nil {
			if err := s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, true); err != nil {
				return "", err
			}
		}
		s.publishEvent(func() error {
			return s.producer.PublishGroupMemberJoined(ctx, groupID, actorID, "member")
		})
		s.invalidateGroupCache(ctx, groupID)
		return "joined", nil

	case "request":
		// Rate limit: 20 join requests/hr
		if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:join_req:%s", actorID), 20, time.Hour) {
			return "", fmt.Errorf("rate_limited: too many join requests")
		}
		// Check if already a member
		isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
		if err != nil {
			return "", err
		}
		if isMember {
			return "", fmt.Errorf("already a member of this group")
		}
		jr := &store.GroupJoinRequest{
			GroupID: groupID,
			UserID:  actorID,
		}
		if err := s.store.CreateJoinRequest(ctx, jr); err != nil {
			return "", err
		}
		s.store.IncrementPendingRequestCount(ctx, groupID)
		s.publishEvent(func() error {
			return s.producer.PublishGroupJoinRequested(ctx, groupID, actorID, jr.ID)
		})
		return "request_pending", nil

	case "invite_only":
		return "", fmt.Errorf("forbidden: this group is invite-only")

	default:
		// Fallback: check old visibility field
		if g.Visibility == "private" {
			return "", fmt.Errorf("forbidden: private groups require an invite")
		}
		if err := s.store.AddMember(ctx, groupID, actorID, "member"); err != nil {
			return "", err
		}
		if g.ChatConversationID != nil {
			if err := s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, true); err != nil {
				return "", err
			}
		}
		s.publishEvent(func() error {
			return s.producer.PublishGroupMemberJoined(ctx, groupID, actorID, "member")
		})
		s.invalidateGroupCache(ctx, groupID)
		return "joined", nil
	}
}

// LeaveGroup allows a user to leave a group.
func (s *Service) LeaveGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	member, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil {
		return fmt.Errorf("not a member of this group")
	}

	if member.Role == "admin" {
		members, err := s.store.ListMembers(ctx, groupID, 100, 0)
		if err != nil {
			return err
		}
		otherAdmin := false
		for _, m := range members {
			if m.UserID != actorID && m.Role == "admin" {
				otherAdmin = true
				break
			}
		}
		if !otherAdmin {
			return fmt.Errorf("must transfer admin role first")
		}
	}

	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}

	if g != nil && g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, false); err != nil {
			return err
		}
	}

	if err := s.store.RemoveMember(ctx, groupID, actorID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberLeft(ctx, groupID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	s.invalidateMembershipCache(ctx, groupID, actorID)
	return nil
}

// ListMembers returns the members of a group.
func (s *Service) ListMembers(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupMember, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	return s.store.ListMembers(ctx, groupID, limit, offset)
}

// UpdateMemberRole changes a member's role. Only admins or the owner may do this.
func (s *Service) UpdateMemberRole(ctx context.Context, actorID, groupID, targetID uuid.UUID, role string) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	isOwner := g.CreatorID == actorID
	if actor == nil || (!isOwner && actor.Role != "admin") {
		return fmt.Errorf("forbidden: only admins can update member roles")
	}

	// Only owner can promote to admin
	if role == "admin" && !isOwner {
		return fmt.Errorf("forbidden: only the group owner can promote to admin")
	}

	target, err := s.store.GetActiveMember(ctx, groupID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("target user is not a member")
	}

	oldRole := target.Role
	if err := s.store.UpdateMemberRole(ctx, groupID, targetID, role); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberRoleChanged(ctx, groupID, targetID, actorID, oldRole, role)
	})

	return nil
}

// RemoveMember kicks a member from the group.
func (s *Service) RemoveMember(ctx context.Context, actorID, groupID, targetID uuid.UUID) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can remove members")
	}

	target, err := s.store.GetActiveMember(ctx, groupID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("target user is not a member")
	}

	if actor.Role == "moderator" && target.Role == "admin" {
		return fmt.Errorf("forbidden: moderators cannot remove admins")
	}

	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}

	if g != nil && g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, targetID, false); err != nil {
			return err
		}
	}

	if err := s.store.KickMember(ctx, groupID, targetID, actorID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberRemoved(ctx, groupID, targetID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	s.invalidateMembershipCache(ctx, groupID, targetID)
	return nil
}

// BanMember bans a member from the group.
func (s *Service) BanMember(ctx context.Context, actorID, groupID, targetID uuid.UUID, reason string) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can ban members")
	}

	target, err := s.store.GetActiveMember(ctx, groupID, targetID)
	if err != nil {
		return err
	}
	if target == nil {
		return fmt.Errorf("target user is not a member")
	}

	// Role hierarchy: mods can't ban admins, nobody can ban themselves
	if actorID == targetID {
		return fmt.Errorf("forbidden: cannot ban yourself")
	}
	if actor.Role == "moderator" && (target.Role == "admin" || target.Role == "moderator") {
		return fmt.Errorf("forbidden: moderators cannot ban admins or other moderators")
	}

	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}

	if g != nil && g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, targetID, false); err != nil {
			return err
		}
	}

	if reason != "" {
		if err := s.store.BanMemberWithReason(ctx, groupID, targetID, actorID, reason); err != nil {
			return err
		}
	} else {
		if err := s.store.BanMember(ctx, groupID, targetID, actorID); err != nil {
			return err
		}
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberBanned(ctx, groupID, targetID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	s.invalidateMembershipCache(ctx, groupID, targetID)
	return nil
}

// --- Invites ---

// InviteUser adds or invites one person, returning "added" or "invited" so the
// caller can say which actually happened rather than always claiming an invite.
func (s *Service) InviteUser(ctx context.Context, actorID, groupID, inviteeID uuid.UUID) (string, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return "", err
	}
	if g == nil {
		return "", fmt.Errorf("group not found")
	}

	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return "", err
	}
	if actor == nil {
		return "", fmt.Errorf("forbidden: only members can invite users")
	}

	// Enforce who_can_invite
	if err := s.checkPermission(g.WhoCanInvite, actor.Role); err != nil {
		return "", fmt.Errorf("forbidden: %w", err)
	}

	alreadyMember, err := s.store.CheckMembership(ctx, groupID, inviteeID)
	if err != nil {
		return "", err
	}
	if alreadyMember {
		return "", fmt.Errorf("user is already a member of this group")
	}

	/*
		The invitee's own say, not just the group's — this is where a block is
		honoured, and it fails closed.

		Unlike the batch, one person named by one request may be told the
		request was refused: there is nobody else it could have been, so
		ErrInviteNotPermitted leaks nothing the caller did not already choose.
	*/
	outcome, err := s.addOrInvite(ctx, actorID, g, inviteeID)
	if err != nil {
		return "", err
	}
	switch outcome {
	case outcomeAdded:
		return "added", nil
	case outcomeInvited:
		return "invited", nil
	default:
		return "", ErrInviteNotPermitted
	}
}

/*
	AddPeopleResult is what actually happened, in COUNTS rather than names.

	Names are withheld on purpose, and it is the same reason the batch skips a
	refused target instead of failing: telling the inviter which of the people
	they picked was refused tells them who blocked them. Returning added and
	invited as id lists would leak that by subtraction — pick three, get two
	back, and you know exactly who the third is — so the wire carries totals
	and the client re-reads the member list, where an invited person and a
	refused one look the same until one accepts.
*/
type AddPeopleResult struct {
	// Added straight in, because their own privacy allows it.
	Added int `json:"added"`
	// Sent an invitation to accept.
	Invited int `json:"invited"`
	// Already a member, banned, blocked, or refused by their own settings.
	Skipped int `json:"skipped"`
}

type addOutcome int

const (
	outcomeSkipped addOutcome = iota
	outcomeAdded
	outcomeInvited
)

/*
	addOrInvite applies ONE person's own answer to being put in a group.

	graph-service answers with two different permissions and this used to throw
	one of them away: directAdd was computed and then everybody got an
	invitation regardless. So someone whose privacy says "people I am connected
	to can add me to groups" still had to accept an invite — the setting did
	nothing, and a creator who picked three people watched their new group sit
	at one member.

	Both paths go through here so the single invite and the batch cannot give
	the same pair different answers, which is the drift canAddToGroup's comment
	warns about.
*/
func (s *Service) addOrInvite(ctx context.Context, actorID uuid.UUID, g *store.Group, targetID uuid.UUID) (addOutcome, error) {
	if targetID == actorID {
		return outcomeSkipped, nil
	}
	isMember, err := s.store.CheckMembership(ctx, g.ID, targetID)
	if err != nil {
		return outcomeSkipped, err
	}
	if isMember {
		return outcomeSkipped, nil
	}
	isBanned, err := s.store.CheckBanned(ctx, g.ID, targetID)
	if err != nil {
		return outcomeSkipped, err
	}
	if isBanned {
		return outcomeSkipped, nil
	}

	directAdd, invite := s.canAddToGroup(ctx, actorID, targetID)
	switch {
	case directAdd:
		if err := s.store.AddMemberWithInviter(ctx, g.ID, targetID, "member", actorID); err != nil {
			return outcomeSkipped, err
		}
		return outcomeAdded, nil
	case invite:
		inv := &store.GroupInvite{GroupID: g.ID, InviterID: actorID, InviteeID: targetID}
		if err := s.store.CreateInvite(ctx, inv); err != nil {
			return outcomeSkipped, err
		}
		s.publishEvent(func() error {
			return s.producer.PublishGroupInviteSent(ctx, g.ID, actorID, targetID, inv.ID)
		})
		return outcomeInvited, nil
	default:
		// Blocked, or their settings refuse. Skipped without a reason, so the
		// inviter cannot read a block off the response.
		return outcomeSkipped, nil
	}
}

// InviteUsersBatch adds or invites multiple users to a group (max 50).
func (s *Service) InviteUsersBatch(ctx context.Context, actorID, groupID uuid.UUID, inviteeIDs []uuid.UUID) (*AddPeopleResult, error) {
	if len(inviteeIDs) > 50 {
		return nil, fmt.Errorf("maximum 50 invites per batch")
	}
	if len(inviteeIDs) == 0 {
		return nil, fmt.Errorf("no users to invite")
	}

	// Rate limit: 100 invites/day
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:invite:%s", actorID), 100, 24*time.Hour) {
		return nil, fmt.Errorf("rate_limited: too many invites")
	}

	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if actor == nil {
		return nil, fmt.Errorf("forbidden: only members can invite users")
	}

	if err := s.checkPermission(g.WhoCanInvite, actor.Role); err != nil {
		return nil, fmt.Errorf("forbidden: %w", err)
	}

	/*
		Each person gets their own answer, and one refusal does not fail the
		batch — that is deliberate, because a failure naming the refused target
		would tell the inviter which of fifty people blocked them.

		A real error (the database, not a policy) still fails the whole call:
		reporting "3 invited" when the write failed is worse than an error.
	*/
	result := &AddPeopleResult{}
	seen := make(map[uuid.UUID]bool, len(inviteeIDs))
	for _, id := range inviteeIDs {
		// The same id twice is one person, not two.
		if seen[id] {
			continue
		}
		seen[id] = true

		outcome, err := s.addOrInvite(ctx, actorID, g, id)
		if err != nil {
			return nil, err
		}
		switch outcome {
		case outcomeAdded:
			result.Added++
		case outcomeInvited:
			result.Invited++
		default:
			result.Skipped++
		}
	}
	return result, nil
}

// AcceptInvite accepts a pending invite and adds the user to the group.
func (s *Service) AcceptInvite(ctx context.Context, actorID uuid.UUID, inviteID uuid.UUID) error {
	inv, err := s.store.GetInviteByID(ctx, inviteID)
	if err != nil {
		return err
	}
	if inv == nil {
		return fmt.Errorf("invite not found")
	}
	if inv.InviteeID != actorID {
		return fmt.Errorf("forbidden: this invite is not for you")
	}
	if inv.Status != "pending" {
		return fmt.Errorf("invite is no longer pending")
	}
	// Audit CG3: invites carry an ExpiresAt set at creation (~7 days)
	// but the previous code never checked it — a leaked or shared
	// invite link replayed years later still worked. Reject expired
	// invites and bump the status so the row is closed out.
	if inv.ExpiresAt != nil && time.Now().After(*inv.ExpiresAt) {
		_ = s.store.UpdateInviteStatus(ctx, inviteID, "expired")
		return fmt.Errorf("invite expired")
	}

	if err := s.store.UpdateInviteStatus(ctx, inviteID, "accepted"); err != nil {
		return err
	}

	if err := s.store.AddMemberWithInviter(ctx, inv.GroupID, actorID, "member", inv.InviterID); err != nil {
		return err
	}

	g, err := s.store.GetGroupByID(ctx, inv.GroupID)
	if err != nil {
		return err
	}
	if g != nil && g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, true); err != nil {
			return err
		}
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupInviteAccepted(ctx, inv.GroupID, inviteID, actorID)
	})
	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberJoined(ctx, inv.GroupID, actorID, "member")
	})

	s.invalidateGroupCache(ctx, inv.GroupID)
	return nil
}

// RejectInvite rejects a pending invite.
func (s *Service) RejectInvite(ctx context.Context, actorID uuid.UUID, inviteID uuid.UUID) error {
	inv, err := s.store.GetInviteByID(ctx, inviteID)
	if err != nil {
		return err
	}
	if inv == nil {
		return fmt.Errorf("invite not found")
	}
	if inv.InviteeID != actorID {
		return fmt.Errorf("forbidden: this invite is not for you")
	}
	if inv.Status != "pending" {
		return fmt.Errorf("invite is no longer pending")
	}

	if err := s.store.UpdateInviteStatus(ctx, inviteID, "rejected"); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupInviteRejected(ctx, inv.GroupID, inviteID, actorID)
	})

	return nil
}

// ListGroupInvites returns pending invites for a group.
func (s *Service) ListGroupInvites(ctx context.Context, actorID uuid.UUID, groupID uuid.UUID) ([]store.GroupInvite, error) {
	isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, fmt.Errorf("forbidden: only members can view group invites")
	}

	return s.store.ListGroupInvites(ctx, groupID)
}

// --- Join Requests ---

// CreateJoinRequest creates a join request for a request-to-join group.
func (s *Service) CreateJoinRequest(ctx context.Context, actorID, groupID uuid.UUID) (*store.GroupJoinRequest, error) {
	// Rate limit: 20 requests/hr
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:join_req:%s", actorID), 20, time.Hour) {
		return nil, fmt.Errorf("rate_limited: too many join requests")
	}

	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if g.JoinMode != "request" {
		return nil, fmt.Errorf("this group does not accept join requests")
	}

	isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if isMember {
		return nil, fmt.Errorf("already a member of this group")
	}

	banned, err := s.store.CheckBanned(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if banned {
		return nil, fmt.Errorf("forbidden: you are banned from this group")
	}

	jr := &store.GroupJoinRequest{
		GroupID: groupID,
		UserID:  actorID,
	}
	if err := s.store.CreateJoinRequest(ctx, jr); err != nil {
		return nil, err
	}

	s.store.IncrementPendingRequestCount(ctx, groupID)

	s.publishEvent(func() error {
		return s.producer.PublishGroupJoinRequested(ctx, groupID, actorID, jr.ID)
	})

	return jr, nil
}

// ListJoinRequests returns pending join requests for a group.
func (s *Service) ListJoinRequests(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupJoinRequest, error) {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return nil, fmt.Errorf("forbidden: only admins or moderators can view join requests")
	}

	return s.store.ListJoinRequests(ctx, groupID, limit, offset)
}

// ApproveJoinRequest approves a join request.
func (s *Service) ApproveJoinRequest(ctx context.Context, actorID uuid.UUID, requestID uuid.UUID) error {
	jr, err := s.store.GetJoinRequestByID(ctx, requestID)
	if err != nil {
		return err
	}
	if jr == nil {
		return fmt.Errorf("join request not found")
	}
	if jr.Status != "pending" {
		return fmt.Errorf("join request is no longer pending")
	}

	// Check admin/mod permission
	actor, err := s.store.GetActiveMember(ctx, jr.GroupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can approve join requests")
	}

	if err := s.store.UpdateJoinRequestStatus(ctx, requestID, "approved", actorID); err != nil {
		return err
	}

	s.store.DecrementPendingRequestCount(ctx, jr.GroupID)

	if err := s.store.AddMember(ctx, jr.GroupID, jr.UserID, "member"); err != nil {
		return err
	}

	g, err := s.store.GetGroupByID(ctx, jr.GroupID)
	if err != nil {
		return err
	}
	if g != nil && g.ChatConversationID != nil {
		if err := s.syncMemberToChat(ctx, *g.ChatConversationID, jr.UserID, true); err != nil {
			return err
		}
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupJoinApproved(ctx, jr.GroupID, jr.UserID, requestID, actorID)
	})
	s.publishEvent(func() error {
		return s.producer.PublishGroupMemberJoined(ctx, jr.GroupID, jr.UserID, "member")
	})

	s.invalidateGroupCache(ctx, jr.GroupID)
	return nil
}

// RejectJoinRequest rejects a join request.
func (s *Service) RejectJoinRequest(ctx context.Context, actorID uuid.UUID, requestID uuid.UUID) error {
	jr, err := s.store.GetJoinRequestByID(ctx, requestID)
	if err != nil {
		return err
	}
	if jr == nil {
		return fmt.Errorf("join request not found")
	}
	if jr.Status != "pending" {
		return fmt.Errorf("join request is no longer pending")
	}

	actor, err := s.store.GetActiveMember(ctx, jr.GroupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can reject join requests")
	}

	if err := s.store.UpdateJoinRequestStatus(ctx, requestID, "rejected", actorID); err != nil {
		return err
	}

	s.store.DecrementPendingRequestCount(ctx, jr.GroupID)

	s.publishEvent(func() error {
		return s.producer.PublishGroupJoinRejected(ctx, jr.GroupID, jr.UserID, requestID, actorID)
	})

	return nil
}

// --- Rules ---

// GetGroupRules returns the rules for a group.
func (s *Service) GetGroupRules(ctx context.Context, groupID uuid.UUID) ([]store.GroupRule, error) {
	return s.store.ListGroupRules(ctx, groupID)
}

// UpdateGroupRules replaces all rules for a group. Only admins may do this.
func (s *Service) UpdateGroupRules(ctx context.Context, actorID, groupID uuid.UUID, rules []store.GroupRule) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || actor.Role != "admin" {
		return fmt.Errorf("forbidden: only admins can update group rules")
	}

	if len(rules) > 10 {
		return fmt.Errorf("maximum 10 rules per group")
	}

	return s.store.ReplaceGroupRules(ctx, groupID, rules)
}

// --- Feed & Posts ---

// GetGroupFeed returns posts in a group.
func (s *Service) GetGroupFeed(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupPost, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	return s.store.ListGroupPosts(ctx, groupID, limit, offset)
}

// CreateGroupPost proxies a post creation to the post-service and records it in the group.
func (s *Service) CreateGroupPost(ctx context.Context, actorID, groupID uuid.UUID, postBody json.RawMessage) (uuid.UUID, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return uuid.Nil, err
	}
	if g == nil {
		return uuid.Nil, fmt.Errorf("group not found")
	}

	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return uuid.Nil, err
	}
	if actor == nil {
		return uuid.Nil, fmt.Errorf("forbidden: only members can post in the group")
	}

	// Enforce who_can_post
	if err := s.checkPermission(g.WhoCanPost, actor.Role); err != nil {
		return uuid.Nil, fmt.Errorf("forbidden: %w", err)
	}

	// Proxy to post-service
	url := fmt.Sprintf("%s/v1/posts", s.postServiceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(postBody))
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to create post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", actorID.String())

	resp, err := s.postClient.Do(req)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to reach post-service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return uuid.Nil, fmt.Errorf("post-service error (status %d): %s", resp.StatusCode, string(body))
	}

	var postResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&postResp); err != nil {
		return uuid.Nil, fmt.Errorf("failed to decode post-service response: %w", err)
	}

	postID, err := uuid.Parse(postResp.Data.ID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("invalid post ID from post-service: %w", err)
	}

	if err := s.store.AddGroupPost(ctx, groupID, postID, actorID); err != nil {
		return uuid.Nil, err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupPostCreated(ctx, groupID, postID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	return postID, nil
}

// DeleteGroupPost deletes a post from the group.
func (s *Service) DeleteGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	gp, err := s.store.GetGroupPost(ctx, groupID, postID)
	if err != nil {
		return err
	}
	if gp == nil {
		return fmt.Errorf("post not found in this group")
	}

	// Author can delete own post, admin/mod/owner can delete any
	if gp.AuthorID != actorID.String() {
		actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
		if err != nil {
			return err
		}
		isOwner := g.CreatorID == actorID
		if actor == nil || (!isOwner && actor.Role != "admin" && actor.Role != "moderator") {
			return fmt.Errorf("forbidden: only post author, admins, or moderators can delete posts")
		}
	}

	if err := s.store.DeleteGroupPost(ctx, groupID, postID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupPostDeleted(ctx, groupID, postID, actorID)
	})

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// PinPost pins a post in the group (max 3).
func (s *Service) PinPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	isOwner := g.CreatorID == actorID
	if actor == nil || (!isOwner && actor.Role != "admin") {
		return fmt.Errorf("forbidden: only admins or the owner can pin posts")
	}

	gp, err := s.store.GetGroupPost(ctx, groupID, postID)
	if err != nil {
		return err
	}
	if gp == nil {
		return fmt.Errorf("post not found in this group")
	}

	count, err := s.store.CountPinnedPosts(ctx, groupID)
	if err != nil {
		return err
	}
	if count >= 3 {
		return fmt.Errorf("maximum 3 pinned posts per group")
	}

	if err := s.store.PinPost(ctx, postID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupPostPinned(ctx, groupID, postID, actorID)
	})

	return nil
}

// UnpinPost unpins a post in the group.
func (s *Service) UnpinPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	isOwner := g.CreatorID == actorID
	if actor == nil || (!isOwner && actor.Role != "admin") {
		return fmt.Errorf("forbidden: only admins or the owner can unpin posts")
	}

	if err := s.store.UnpinPost(ctx, postID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishGroupPostUnpinned(ctx, groupID, postID, actorID)
	})

	return nil
}

// UnbanMember lifts a ban on a member.
func (s *Service) UnbanMember(ctx context.Context, actorID, groupID, targetID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	isOwner := g.CreatorID == actorID
	if actor == nil || (!isOwner && actor.Role != "admin") {
		return fmt.Errorf("forbidden: only admins or the owner can unban members")
	}

	banned, err := s.store.CheckBanned(ctx, groupID, targetID)
	if err != nil {
		return err
	}
	if !banned {
		return fmt.Errorf("user is not banned from this group")
	}

	if err := s.store.UnbanMember(ctx, groupID, targetID); err != nil {
		return err
	}

	s.publishEvent(func() error {
		return s.producer.PublishMemberBanLifted(ctx, groupID, targetID, actorID)
	})

	return nil
}

// GetGroupMedia returns media posts for a group.
func (s *Service) GetGroupMedia(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupMediaItem, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	return s.store.ListGroupMedia(ctx, groupID, limit, offset)
}

// ListBannedMembers returns banned members for admin/owner view.
func (s *Service) ListBannedMembers(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupMember, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	isOwner := g.CreatorID == actorID
	if actor == nil || (!isOwner && actor.Role != "admin") {
		return nil, fmt.Errorf("forbidden: only admins or the owner can view banned members")
	}

	return s.store.ListBannedMembers(ctx, groupID, limit, offset)
}

// --- Member Stats ---

// GetMemberStats returns activity stats for a member in a group.
func (s *Service) GetMemberStats(ctx context.Context, actorID, groupID, userID uuid.UUID) (*store.MemberStats, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	stats, err := s.store.GetMemberStats(ctx, groupID, userID)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		// Return zero stats instead of nil for convenience
		return &store.MemberStats{GroupID: groupID, UserID: userID}, nil
	}
	return stats, nil
}

// GetTopContributors returns top contributors for a group.
func (s *Service) GetTopContributors(ctx context.Context, actorID, groupID uuid.UUID, limit int) ([]store.MemberStats, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	return s.store.GetTopContributors(ctx, groupID, limit)
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

// --- Helpers ---

func (s *Service) invalidateGroupCache(ctx context.Context, groupID uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("group:%s", groupID))
}

/*
	invalidateMembershipCache drops one person's cached membership in one group.

	CheckMembershipCached caches "gm:<group>:<user>" for five minutes, and
	nothing ever deleted it — invalidateGroupCache only clears "group:<id>".
	AddGroupPostComment is the reader, so banning or removing someone left them
	able to keep commenting for up to five minutes after the ban landed. A ban
	that does not take effect until you notice it has stopped working is worse
	than no ban, because the moderator believes it worked.

	Called on every path that ends a membership.
*/
func (s *Service) invalidateMembershipCache(ctx context.Context, groupID, userID uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("gm:%s:%s", groupID, userID))
}

func (s *Service) checkPermission(setting, role string) error {
	switch setting {
	case "all_members":
		return nil
	case "admins_mods":
		if role == "admin" || role == "owner" || role == "moderator" {
			return nil
		}
		return fmt.Errorf("only admins and moderators are allowed")
	case "admins_only":
		if role == "admin" || role == "owner" {
			return nil
		}
		return fmt.Errorf("only admins are allowed")
	}
	return nil
}

func (s *Service) checkHandleInUserService(handle string) bool {
	if s.userServiceURL == "" {
		return false
	}
	url := fmt.Sprintf("%s/v1/users/by-username/%s", s.userServiceURL, handle)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false // fail open
	}
	resp, err := s.userClient.Do(req)
	if err != nil {
		return false // fail open — but spec says fail closed. User-service down is rare.
	}
	defer resp.Body.Close()
	// If 200, handle is taken by a user
	return resp.StatusCode == http.StatusOK
}

func (s *Service) ensureGroupChat(ctx context.Context, group *store.Group) error {
	if group.ChatConversationID != nil {
		return nil
	}
	conversationID, err := s.createGroupChat(ctx, group.CreatorID, group.ID, group.Name)
	if err != nil {
		return fmt.Errorf("provision group chat: %w", err)
	}
	if err := s.store.SetChatConversationID(ctx, group.ID, conversationID); err != nil {
		return fmt.Errorf("persist group chat identity: %w", err)
	}
	group.ChatConversationID = &conversationID
	s.invalidateGroupCache(ctx, group.ID)
	return nil
}

func (s *Service) createGroupChat(ctx context.Context, creatorID uuid.UUID, groupID uuid.UUID, groupName string) (uuid.UUID, error) {
	url := fmt.Sprintf("%s/internal/v1/chat/groups/conversations", s.messageServiceURL)
	body, _ := json.Marshal(map[string]interface{}{
		"title":           groupName,
		"creator_user_id": creatorID.String(),
		"group_id":        groupID.String(),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return uuid.Nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)

	resp, err := s.chatClient.Do(req)
	if err != nil {
		return uuid.Nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return uuid.Nil, fmt.Errorf("message-service error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var convResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&convResp); err != nil {
		return uuid.Nil, err
	}

	return uuid.Parse(convResp.Data.ID)
}

func (s *Service) syncMemberToChat(ctx context.Context, conversationID, userID uuid.UUID, add bool) error {
	var method string
	var url string
	var bodyReader io.Reader
	if add {
		method = http.MethodPost
		url = fmt.Sprintf("%s/internal/v1/chat/groups/conversations/%s/members", s.messageServiceURL, conversationID)
		body, _ := json.Marshal(map[string]string{"user_id": userID.String()})
		bodyReader = bytes.NewReader(body)
	} else {
		method = http.MethodDelete
		url = fmt.Sprintf("%s/internal/v1/chat/groups/conversations/%s/members/%s", s.messageServiceURL, conversationID, userID)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	resp, err := s.chatClient.Do(req)
	if err != nil {
		return fmt.Errorf("sync chat membership: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("chat sync returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}

// ── Word Blocklist ───────────────────────────────────────────

func (s *Service) AddWordToBlocklist(ctx context.Context, actorID, groupID uuid.UUID, word string) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can manage the word blocklist")
	}
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return fmt.Errorf("word cannot be empty")
	}
	return s.store.AddWordToBlocklist(ctx, groupID, word, actorID)
}

func (s *Service) RemoveWordFromBlocklist(ctx context.Context, actorID, groupID uuid.UUID, word string) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can manage the word blocklist")
	}
	return s.store.RemoveWordFromBlocklist(ctx, groupID, word)
}

func (s *Service) GetWordBlocklist(ctx context.Context, actorID, groupID uuid.UUID) ([]string, error) {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return nil, fmt.Errorf("forbidden: only admins or moderators can view the word blocklist")
	}
	return s.store.GetWordBlocklist(ctx, groupID)
}

// ── Post Approval Queue ──────────────────────────────────────

/*
	The moderation queue, by post id.

	The three methods below replace a queue that never worked. GetApprovalQueue
	reads post_approval_queue, which store.AddToApprovalQueue would populate
	except that nothing calls it — so it has always answered with an empty
	list while posts sat in status='pending_approval' with no route that could
	reach them. A member of a moderated group could post, see a success, and
	have it never appear to anyone, permanently.

	These read group_posts.status instead, which is where the truth has always
	been, and act by POST id rather than by a queue-row id that does not exist.
	The old queue methods are left alone: they return an empty list today and
	will keep returning one, so nothing that calls them breaks.
*/
func (s *Service) requirePostModerator(ctx context.Context, actorID, groupID uuid.UUID) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can review posts")
	}
	return nil
}

func (s *Service) ListPendingPosts(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupPostV2, error) {
	if err := s.requirePostModerator(ctx, actorID, groupID); err != nil {
		return nil, err
	}
	return s.store.ListPendingGroupPostsV2(ctx, groupID, limit, offset)
}

func (s *Service) ApprovePendingPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if err := s.requirePostModerator(ctx, actorID, groupID); err != nil {
		return err
	}
	// The post must belong to THIS group: without the check, a moderator of
	// any group could approve a pending post in a group they cannot see, by
	// guessing or replaying an id.
	post, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if post == nil || post.GroupID != groupID {
		return fmt.Errorf("post not found")
	}
	if err := s.store.ApproveGroupPost(ctx, postID, actorID.String()); err != nil {
		return err
	}
	s.invalidateGroupCache(ctx, groupID)
	return nil
}

func (s *Service) RejectPendingPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if err := s.requirePostModerator(ctx, actorID, groupID); err != nil {
		return err
	}
	post, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if post == nil || post.GroupID != groupID {
		return fmt.Errorf("post not found")
	}
	// No counter change: a pending post was never counted, so rejecting it
	// has nothing to undo.
	if err := s.store.RejectGroupPost(ctx, postID); err != nil {
		return err
	}
	s.invalidateGroupCache(ctx, groupID)
	return nil
}

func (s *Service) GetApprovalQueue(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.ApprovalQueueItem, error) {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return nil, fmt.Errorf("forbidden: only admins or moderators can view the approval queue")
	}
	return s.store.GetApprovalQueue(ctx, groupID, "pending", limit, offset)
}

func (s *Service) ApprovePost(ctx context.Context, actorID, groupID, itemID uuid.UUID) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can approve posts")
	}
	return s.store.ReviewApprovalItem(ctx, itemID, actorID, "approved")
}

func (s *Service) RejectQueuedPost(ctx context.Context, actorID, groupID, itemID uuid.UUID) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can reject posts")
	}
	return s.store.ReviewApprovalItem(ctx, itemID, actorID, "rejected")
}

// ── Group Channels ───────────────────────────────────────────

func (s *Service) CreateGroupChannel(ctx context.Context, actorID, groupID uuid.UUID, name, chanType, description string) (*store.GroupChannel, error) {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return nil, fmt.Errorf("forbidden: only admins or moderators can create channels")
	}
	ch := &store.GroupChannel{
		GroupID:     groupID,
		Name:        name,
		Type:        chanType,
		Description: description,
		CreatedBy:   actorID,
	}
	return s.store.CreateGroupChannel(ctx, ch)
}

func (s *Service) ListGroupChannels(ctx context.Context, groupID uuid.UUID) ([]store.GroupChannel, error) {
	return s.store.ListGroupChannels(ctx, groupID)
}

func (s *Service) DeleteGroupChannel(ctx context.Context, actorID, groupID, channelID uuid.UUID) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can delete channels")
	}
	return s.store.DeleteGroupChannel(ctx, channelID, groupID)
}

// ── Group Wiki ───────────────────────────────────────────────

func (s *Service) CreateWikiPage(ctx context.Context, actorID, groupID uuid.UUID, title, content string) (*store.WikiPage, error) {
	isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, fmt.Errorf("forbidden: only members can create wiki pages")
	}
	return s.store.CreateWikiPage(ctx, groupID, actorID, title, content)
}

func (s *Service) UpdateWikiPage(ctx context.Context, actorID, groupID, pageID uuid.UUID, title, content string) (*store.WikiPage, error) {
	isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, fmt.Errorf("forbidden: only members can update wiki pages")
	}
	return s.store.UpdateWikiPage(ctx, pageID, actorID, title, content)
}

func (s *Service) ListWikiPages(ctx context.Context, groupID uuid.UUID) ([]store.WikiPage, error) {
	return s.store.ListWikiPages(ctx, groupID)
}

func (s *Service) DeleteWikiPage(ctx context.Context, actorID, groupID, pageID uuid.UUID) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can delete wiki pages")
	}
	return s.store.DeleteWikiPage(ctx, pageID, groupID)
}

// ── V2 Group Posts ───────────────────────────────────────────

type CreateGroupPostV2Params struct {
	Body           string          `json:"body"`
	Title          string          `json:"title,omitempty"`
	ContentType    string          `json:"content_type"`
	ChannelID      *uuid.UUID      `json:"channel_id,omitempty"`
	TypePayload    json.RawMessage `json:"type_payload,omitempty"`
	Attachments    json.RawMessage `json:"attachments,omitempty"`
	IsAnnouncement bool            `json:"is_announcement"`
	// Post without the author's name shown to other members. Refused unless
	// the group has opted in.
	IsAnonymous bool `json:"is_anonymous"`
	// Additional groups to post the same body to. Empty for an ordinary
	// single post, which keeps the existing response shape — that is the
	// compatibility hinge for every caller that already exists.
	AlsoPostTo []uuid.UUID `json:"also_post_to,omitempty"`
	// Stable across retries of the SAME composer action. Required only when
	// also_post_to is used, so a single post is unchanged.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

/*
	containsBlockedWord reports whether any of the group's blocked words appears
	in the text being posted, and which one.

	Whole-word matching on a lowercased haystack, not substring: a blocklist
	containing "ass" must not refuse "assignment" or "class". That is the
	difference between a moderation tool and a joke about the Scunthorpe
	problem.

	A failure to READ the blocklist does not block the post. This is the one
	place in this file that deliberately fails open, and the reason is that the
	alternative is worse: a Redis or Postgres blip would stop a whole group
	posting, which is a far bigger outage than a few words slipping past a
	filter that is advisory to begin with.
*/
func (s *Service) containsBlockedWord(ctx context.Context, groupID uuid.UUID, title, body string) (bool, string) {
	words, err := s.store.GetWordBlocklist(ctx, groupID)
	if err != nil || len(words) == 0 {
		return false, ""
	}
	haystack := strings.ToLower(title + " " + body)
	fields := strings.FieldsFunc(haystack, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	present := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		present[f] = struct{}{}
	}
	for _, w := range words {
		if _, hit := present[strings.ToLower(strings.TrimSpace(w))]; hit {
			return true, w
		}
	}
	return false, ""
}

/*
	CreateGroupPostV2 posts to one group.

	The evaluation lives in createOnePost (cross_post.go), shared with every
	target of a cross-post so the two cannot drift into different answers for
	the same person and group. Here a policy refusal becomes an ERROR, because
	a caller who named exactly one group should be told why it failed; in a
	batch the same refusal is an outcome for that target and does not fail the
	request.
*/
func (s *Service) CreateGroupPostV2(ctx context.Context, actorID, groupID uuid.UUID, params CreateGroupPostV2Params) (*store.GroupPostV2, error) {
	// Before any read: a flood should not cost a database round trip each.
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:group_post:%s", actorID), 30, time.Hour) {
		return nil, fmt.Errorf("rate_limited: you are posting too quickly")
	}

	outcome, post, err := s.createOnePost(ctx, actorID, groupID, params, nil)
	if err != nil {
		return nil, err
	}
	if post == nil {
		return nil, outcomeAsError(outcome)
	}
	return post, nil
}

/*
	outcomeAsError turns a per-target outcome into the error a single-group
	caller gets.

	The single path can be specific where a batch cannot: naming the reason
	here tells the author something about a group they explicitly chose, with
	nobody else's privacy involved. The one exception is kept lossy anyway —
	"unavailable" still refuses to distinguish a missing group from a private
	one, because that answer is a probe whether it arrives one at a time or
	five at a time.
*/
func outcomeAsError(outcome string) error {
	switch outcome {
	case OutcomeNotAMember:
		return fmt.Errorf("forbidden: only members can post in the group")
	case OutcomeBanned:
		return fmt.Errorf("forbidden: you are banned from this group")
	case OutcomeNotPermitted:
		return fmt.Errorf("forbidden: your role cannot post in this group")
	case OutcomeAnonNotAllowed:
		return fmt.Errorf("forbidden: this group does not allow anonymous posts")
	case OutcomeBlockedContent:
		return fmt.Errorf("blocked_content: that post contains a word this group does not allow")
	default:
		return fmt.Errorf("group not found")
	}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// viewerKey renders the acting user as the TEXT user id the engagement tables
// store (the same form the spark/echo/stash writers use). An unset actor
// becomes "", which matches no engagement row, so the viewer_* flags come back
// false instead of erroring.
func viewerKey(actorID uuid.UUID) string {
	if actorID == uuid.Nil {
		return ""
	}
	return actorID.String()
}

func (s *Service) GetGroupFeedV2(ctx context.Context, actorID, groupID uuid.UUID, channelID *uuid.UUID, limit, offset int) ([]store.GroupPostV2, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	return s.store.ListGroupPostsV2(ctx, groupID, channelID, viewerKey(actorID), limit, offset)
}

func (s *Service) GetGroupPostV2(ctx context.Context, actorID, groupID, postID uuid.UUID) (*store.GroupPostV2, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}
	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}
	p, err := s.store.GetGroupPostV2ForViewer(ctx, postID, viewerKey(actorID))
	if err != nil {
		return nil, err
	}
	if p.GroupID != groupID {
		return nil, fmt.Errorf("not found: post not found in this group")
	}
	return p, nil
}

func (s *Service) DeleteGroupPostV2(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if p.GroupID != groupID {
		return fmt.Errorf("not found: post not found in this group")
	}

	// Author can delete own post, admin/mod/owner can delete any
	if p.AuthorID != actorID.String() {
		actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
		if err != nil {
			return err
		}
		g, _ := s.store.GetGroupByID(ctx, groupID)
		isOwner := g != nil && g.CreatorID == actorID
		if actor == nil || (!isOwner && actor.Role != "admin" && actor.Role != "moderator") {
			return fmt.Errorf("forbidden: only post author, admins, or moderators can delete posts")
		}
	}

	if err := s.store.DeleteGroupPostV2(ctx, groupID, postID); err != nil {
		return err
	}
	s.publishEvent(func() error {
		return s.producer.PublishGroupPostDeleted(ctx, groupID, postID, actorID)
	})
	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// ── V2 Engagement ────────────────────────────────────────────

func (s *Service) SparkGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID, isSupernova bool) error {
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if p.GroupID != groupID {
		return fmt.Errorf("not found: post not found in this group")
	}
	if err := s.store.SparkGroupPost(ctx, postID, actorID.String(), isSupernova); err != nil {
		return err
	}
	s.store.IncrementMemberSparks(ctx, groupID, actorID, 1)
	return nil
}

func (s *Service) UnsparkGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if p.GroupID != groupID {
		return fmt.Errorf("not found: post not found in this group")
	}
	return s.store.UnsparkGroupPost(ctx, postID, actorID.String())
}

func (s *Service) StashGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if p.GroupID != groupID {
		return fmt.Errorf("not found: post not found in this group")
	}
	return s.store.StashGroupPost(ctx, postID, actorID.String())
}

func (s *Service) UnstashGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if p.GroupID != groupID {
		return fmt.Errorf("not found: post not found in this group")
	}
	return s.store.UnstashGroupPost(ctx, postID, actorID.String())
}

func (s *Service) RecordGroupPostView(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	return s.store.RecordGroupPostView(ctx, postID, actorID.String())
}

func (s *Service) EchoGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID, echoType string) error {
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if p.GroupID != groupID {
		return fmt.Errorf("not found: post not found in this group")
	}
	if echoType == "" {
		echoType = "share"
	}
	return s.store.EchoGroupPost(ctx, postID, actorID.String(), echoType)
}

func (s *Service) UnechoGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if p.GroupID != groupID {
		return fmt.Errorf("not found: post not found in this group")
	}
	return s.store.UnechoGroupPost(ctx, postID, actorID.String())
}

func (s *Service) ListGroupPostComments(ctx context.Context, actorID, groupID, postID uuid.UUID, limit, offset int) ([]store.GroupPostComment, error) {
	// Use cached post-in-group check (Redis → DB fallback)
	exists, err := s.store.PostExistsInGroup(ctx, s.rdb, postID, groupID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("not found: post not found in this group")
	}
	return s.store.ListGroupPostComments(ctx, postID, limit, offset)
}

func (s *Service) AddGroupPostComment(ctx context.Context, actorID, groupID, postID uuid.UUID, body string, parentID *uuid.UUID) (*store.GroupPostComment, error) {
	if strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("body is required")
	}

	// Use cached post-in-group check (Redis hit = ~0.5ms vs DB = ~15ms)
	exists, err := s.store.PostExistsInGroup(ctx, s.rdb, postID, groupID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("not found: post not found in this group")
	}

	// Use cached membership check (Redis hit = ~0.5ms vs DB = ~15ms)
	isMember, err := s.store.CheckMembershipCached(ctx, s.rdb, groupID, actorID)
	if err != nil {
		return nil, err
	}
	if !isMember {
		return nil, fmt.Errorf("forbidden: only members can comment")
	}

	// Insert comment (single DB write — count increment is async inside store)
	comment, err := s.store.AddGroupPostComment(ctx, postID, actorID.String(), body, parentID)
	if err != nil {
		return nil, err
	}

	// Fire-and-forget: publish Kafka event for notifications/realtime
	go func() {
		parentStr := ""
		if parentID != nil {
			parentStr = parentID.String()
		}
		s.publishEvent(func() error {
			return s.producer.PublishGroupPostCommented(context.Background(), groupID, postID, comment.ID, actorID, body, parentStr)
		})
	}()

	return comment, nil
}

func (s *Service) DeleteGroupPostComment(ctx context.Context, actorID, groupID, postID, commentID uuid.UUID) error {
	// Check if actor is admin/mod/owner
	actor, _ := s.store.GetActiveMember(ctx, groupID, actorID)
	g, _ := s.store.GetGroupByID(ctx, groupID)
	isOwner := g != nil && g.CreatorID == actorID
	isPrivileged := isOwner || (actor != nil && (actor.Role == "admin" || actor.Role == "moderator"))
	err := s.store.DeleteGroupPostComment(ctx, commentID, actorID.String(), isPrivileged)
	if err != nil {
		return err
	}

	// Fire-and-forget: publish realtime delete event
	go func() {
		s.publishEvent(func() error {
			return s.producer.PublishGroupPostCommentDeleted(context.Background(), groupID, postID, commentID, actorID)
		})
	}()
	return nil
}

// ── V2 Events ────────────────────────────────────────────────

func (s *Service) CreateGroupEvent(ctx context.Context, actorID, groupID uuid.UUID, event *store.GroupEvent) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator" && actor.Role != "owner") {
		g, _ := s.store.GetGroupByID(ctx, groupID)
		if g == nil || g.CreatorID != actorID {
			return fmt.Errorf("forbidden: only admins or moderators can create events")
		}
	}
	event.GroupID = groupID
	event.CreatorID = actorID.String()
	return s.store.CreateGroupEvent(ctx, event)
}

func (s *Service) ListGroupEvents(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupEvent, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}
	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}
	return s.store.ListGroupEvents(ctx, groupID, limit, offset)
}

func (s *Service) GetGroupEvent(ctx context.Context, actorID, groupID, eventID uuid.UUID) (*store.GroupEvent, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}
	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}
	e, err := s.store.GetGroupEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if e.GroupID != groupID {
		return nil, fmt.Errorf("not found: event not found in this group")
	}
	return e, nil
}

func (s *Service) DeleteGroupEvent(ctx context.Context, actorID, groupID, eventID uuid.UUID) error {
	actor, err := s.store.GetActiveMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator" && actor.Role != "owner") {
		g, _ := s.store.GetGroupByID(ctx, groupID)
		if g == nil || g.CreatorID != actorID {
			return fmt.Errorf("forbidden: only admins or moderators can delete events")
		}
	}
	e, err := s.store.GetGroupEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if e.GroupID != groupID {
		return fmt.Errorf("not found: event not found in this group")
	}
	return s.store.DeleteGroupEvent(ctx, eventID)
}

func (s *Service) RSVPGroupEvent(ctx context.Context, actorID, groupID, eventID uuid.UUID, status string) error {
	if status != "going" && status != "maybe" && status != "not_going" {
		return fmt.Errorf("invalid RSVP status: %s", status)
	}
	// Must be a member
	isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if !isMember {
		return fmt.Errorf("forbidden: only members can RSVP")
	}
	e, err := s.store.GetGroupEvent(ctx, eventID)
	if err != nil {
		return err
	}
	if e.GroupID != groupID {
		return fmt.Errorf("not found: event not found in this group")
	}
	if !e.RSVPEnabled {
		return fmt.Errorf("RSVP is disabled for this event")
	}
	return s.store.RSVPGroupEvent(ctx, eventID, actorID.String(), status)
}

// GetMyGroupsFeed returns the aggregated reverse-chronological feed of
// published posts across all groups the actor belongs to. Access control is
// the membership join itself — only groups the actor is a member of
// contribute posts.
func (s *Service) GetMyGroupsFeed(ctx context.Context, actorID uuid.UUID, limit, offset int) ([]store.GroupPostV2, error) {
	return s.store.ListMyGroupsFeed(ctx, actorID, limit, offset)
}

// ListMyInvites returns the actor's pending group invites with group display
// fields attached.
func (s *Service) ListMyInvites(ctx context.Context, actorID uuid.UUID) ([]store.GroupInviteDetail, error) {
	return s.store.ListInvitesForUserDetailed(ctx, actorID)
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
