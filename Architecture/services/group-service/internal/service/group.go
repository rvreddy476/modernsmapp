package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/atpost/group-service/internal/store"
	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store             *store.Store
	rdb               *redis.Client
	messageServiceURL string
	postServiceURL    string
	jwtSecret         string
}

func New(s *store.Store, rdb *redis.Client, msgURL, postURL, jwtSecret string) *Service {
	return &Service{
		store:             s,
		rdb:               rdb,
		messageServiceURL: msgURL,
		postServiceURL:    postURL,
		jwtSecret:         jwtSecret,
	}
}

// signServiceToken creates a short-lived JWT for service-to-service calls to message-service.
func (s *Service) signServiceToken(userID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := fmt.Sprintf(`{"sub":"%s","user_id":"%s","exp":%d}`, userID, userID, time.Now().Add(5*time.Minute).Unix())
	payloadEnc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	signingInput := header + "." + payloadEnc
	mac := hmac.New(sha256.New, []byte(s.jwtSecret))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

// --- Groups ---

// CreateGroup creates a new group with the actor as admin, and optionally creates a group chat.
func (s *Service) CreateGroup(ctx context.Context, actorID uuid.UUID, name, desc, visibility string) (*store.Group, error) {
	g := &store.Group{
		Name:        name,
		Description: desc,
		CreatorID:   actorID,
		Visibility:  visibility,
	}

	if err := s.store.CreateGroup(ctx, g); err != nil {
		return nil, err
	}

	// Create group chat conversation via message-service (fire-and-forget on failure)
	go func() {
		convID, err := s.createGroupChat(actorID, g.ID, g.Name)
		if err != nil {
			log.Printf("WARNING: failed to create group chat for group %s: %v", g.ID, err)
			return
		}
		if err := s.store.SetChatConversationID(context.Background(), g.ID, convID); err != nil {
			log.Printf("WARNING: failed to store chat conversation ID for group %s: %v", g.ID, err)
		}
		s.invalidateGroupCache(context.Background(), g.ID)
	}()

	return g, nil
}

// GetGroup returns a group, using cache-aside with Redis. Private groups require membership.
func (s *Service) GetGroup(ctx context.Context, actorID, groupID uuid.UUID) (*store.Group, error) {
	cacheKey := fmt.Sprintf("group:%s", groupID)

	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var g store.Group
		if err := json.Unmarshal([]byte(val), &g); err == nil {
			if g.Visibility == "private" {
				isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
				if err != nil {
					return nil, err
				}
				if !isMember {
					return nil, fmt.Errorf("forbidden: not a member of this private group")
				}
			}
			return &g, nil
		}
	}

	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, nil
	}

	if g.Visibility == "private" {
		isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
		if err != nil {
			return nil, err
		}
		if !isMember {
			return nil, fmt.Errorf("forbidden: not a member of this private group")
		}
	}

	go func() {
		data, _ := json.Marshal(g)
		s.rdb.Set(context.Background(), cacheKey, data, 60*time.Second)
	}()

	return g, nil
}

// UpdateGroup updates a group's details. Only admins may update.
func (s *Service) UpdateGroup(ctx context.Context, actorID, groupID uuid.UUID, name, desc string, avatar, cover *uuid.UUID, visibility string) error {
	member, err := s.store.GetMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil || member.Role != "admin" {
		return fmt.Errorf("forbidden: only admins can update the group")
	}

	if err := s.store.UpdateGroup(ctx, groupID, name, desc, avatar, cover, visibility); err != nil {
		return err
	}

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// DeleteGroup deletes a group. Only the creator (who is an admin) may delete.
func (s *Service) DeleteGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	member, err := s.store.GetMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil || member.Role != "admin" || g.CreatorID != actorID {
		return fmt.Errorf("forbidden: only the group creator can delete the group")
	}

	if err := s.store.DeleteGroup(ctx, groupID); err != nil {
		return err
	}

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// --- Membership ---

// JoinGroup allows a user to join a public group.
func (s *Service) JoinGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return err
	}
	if g == nil {
		return fmt.Errorf("group not found")
	}

	if g.Visibility == "private" {
		return fmt.Errorf("forbidden: private groups require an invite")
	}

	if err := s.store.AddMember(ctx, groupID, actorID, "member"); err != nil {
		return err
	}

	if g.ChatConversationID != nil {
		s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, true)
	}

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// LeaveGroup allows a user to leave a group. The sole admin cannot leave without transferring.
func (s *Service) LeaveGroup(ctx context.Context, actorID, groupID uuid.UUID) error {
	member, err := s.store.GetMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil {
		return fmt.Errorf("not a member of this group")
	}

	if member.Role == "admin" {
		// Check if there is another admin
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

	if err := s.store.RemoveMember(ctx, groupID, actorID); err != nil {
		return err
	}

	if g != nil && g.ChatConversationID != nil {
		s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, false)
	}

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// ListMembers returns the members of a group. Private groups require membership.
func (s *Service) ListMembers(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupMember, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if g.Visibility == "private" {
		isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
		if err != nil {
			return nil, err
		}
		if !isMember {
			return nil, fmt.Errorf("forbidden: not a member of this private group")
		}
	}

	return s.store.ListMembers(ctx, groupID, limit, offset)
}

// UpdateMemberRole changes a member's role. Only admins may do this.
func (s *Service) UpdateMemberRole(ctx context.Context, actorID, groupID, targetID uuid.UUID, role string) error {
	member, err := s.store.GetMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if member == nil || member.Role != "admin" {
		return fmt.Errorf("forbidden: only admins can update member roles")
	}

	return s.store.UpdateMemberRole(ctx, groupID, targetID, role)
}

// RemoveMember kicks a member from the group. Admins and moderators can do this; mods cannot remove admins.
func (s *Service) RemoveMember(ctx context.Context, actorID, groupID, targetID uuid.UUID) error {
	actor, err := s.store.GetMember(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if actor == nil || (actor.Role != "admin" && actor.Role != "moderator") {
		return fmt.Errorf("forbidden: only admins or moderators can remove members")
	}

	target, err := s.store.GetMember(ctx, groupID, targetID)
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

	if err := s.store.RemoveMember(ctx, groupID, targetID); err != nil {
		return err
	}

	if g != nil && g.ChatConversationID != nil {
		s.syncMemberToChat(ctx, *g.ChatConversationID, targetID, false)
	}

	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// --- Invites ---

// InviteUser invites a user to the group. The actor must be a member.
func (s *Service) InviteUser(ctx context.Context, actorID, groupID, inviteeID uuid.UUID) error {
	isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if !isMember {
		return fmt.Errorf("forbidden: only members can invite users")
	}

	alreadyMember, err := s.store.CheckMembership(ctx, groupID, inviteeID)
	if err != nil {
		return err
	}
	if alreadyMember {
		return fmt.Errorf("user is already a member of this group")
	}

	inv := &store.GroupInvite{
		GroupID:   groupID,
		InviterID: actorID,
		InviteeID: inviteeID,
	}
	return s.store.CreateInvite(ctx, inv)
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

	if err := s.store.UpdateInviteStatus(ctx, inviteID, "accepted"); err != nil {
		return err
	}

	if err := s.store.AddMember(ctx, inv.GroupID, actorID, "member"); err != nil {
		return err
	}

	g, err := s.store.GetGroupByID(ctx, inv.GroupID)
	if err != nil {
		return err
	}
	if g != nil && g.ChatConversationID != nil {
		s.syncMemberToChat(ctx, *g.ChatConversationID, actorID, true)
	}

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

	return s.store.UpdateInviteStatus(ctx, inviteID, "rejected")
}

// ListGroupInvites returns pending invites for a group. Only members can view.
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

// --- Feed & Posts ---

// GetGroupFeed returns posts in a group. Private groups require membership.
func (s *Service) GetGroupFeed(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupPost, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, fmt.Errorf("group not found")
	}

	if g.Visibility == "private" {
		isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
		if err != nil {
			return nil, err
		}
		if !isMember {
			return nil, fmt.Errorf("forbidden: not a member of this private group")
		}
	}

	return s.store.ListGroupPosts(ctx, groupID, limit, offset)
}

// CreateGroupPost proxies a post creation to the post-service and records it in the group.
func (s *Service) CreateGroupPost(ctx context.Context, actorID, groupID uuid.UUID, postBody json.RawMessage) (uuid.UUID, error) {
	isMember, err := s.store.CheckMembership(ctx, groupID, actorID)
	if err != nil {
		return uuid.Nil, err
	}
	if !isMember {
		return uuid.Nil, fmt.Errorf("forbidden: only members can post in the group")
	}

	// Proxy to post-service
	url := fmt.Sprintf("%s/v1/posts", s.postServiceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(postBody))
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to create post request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", actorID.String())

	resp, err := httpclient.New(5 * time.Second).Do(req)
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

	s.invalidateGroupCache(ctx, groupID)
	return postID, nil
}

// --- Discovery ---

// GetMyGroups returns groups that the actor is a member of.
func (s *Service) GetMyGroups(ctx context.Context, actorID uuid.UUID, limit, offset int) ([]store.Group, error) {
	return s.store.ListGroupsByUser(ctx, actorID, limit, offset)
}

// DiscoverGroups returns public, non-archived groups for discovery.
func (s *Service) DiscoverGroups(ctx context.Context, limit, offset int) ([]store.Group, error) {
	return s.store.DiscoverPublicGroups(ctx, limit, offset)
}

// SearchGroups performs full-text search on group names.
func (s *Service) SearchGroups(ctx context.Context, query string, limit, offset int) ([]store.Group, error) {
	return s.store.SearchGroups(ctx, query, limit, offset)
}

// --- Helpers ---

// invalidateGroupCache removes a group from the Redis cache.
func (s *Service) invalidateGroupCache(ctx context.Context, groupID uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("group:%s", groupID))
}

// createGroupChat calls the message-service to create a group chat conversation.
func (s *Service) createGroupChat(creatorID uuid.UUID, groupID uuid.UUID, groupName string) (uuid.UUID, error) {
	url := fmt.Sprintf("%s/v1/chat/conversations/group", s.messageServiceURL)
	body, _ := json.Marshal(map[string]interface{}{
		"title":      groupName,
		"member_ids": []string{creatorID.String()},
	})

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return uuid.Nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.signServiceToken(creatorID.String()))

	resp, err := httpclient.New(5 * time.Second).Do(req)
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

// syncMemberToChat adds or removes a member from the group chat conversation (fire-and-forget).
func (s *Service) syncMemberToChat(ctx context.Context, conversationID, userID uuid.UUID, add bool) {
	go func() {
		var method string
		var url string
		var bodyReader io.Reader

		if add {
			method = http.MethodPost
			url = fmt.Sprintf("%s/v1/chat/conversations/%s/members", s.messageServiceURL, conversationID)
			body, _ := json.Marshal(map[string]string{"user_id": userID.String()})
			bodyReader = bytes.NewReader(body)
		} else {
			method = http.MethodDelete
			url = fmt.Sprintf("%s/v1/chat/conversations/%s/members/%s", s.messageServiceURL, conversationID, userID)
			bodyReader = nil
		}

		req, err := http.NewRequest(method, url, bodyReader)
		if err != nil {
			log.Printf("WARNING: failed to create chat sync request: %v", err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.signServiceToken(userID.String()))

		resp, err := httpclient.New(5 * time.Second).Do(req)
		if err != nil {
			log.Printf("WARNING: failed to sync member to chat: %v", err)
			return
		}
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			log.Printf("WARNING: chat sync returned status %d for conversation %s, user %s", resp.StatusCode, conversationID, userID)
		}
	}()
}
