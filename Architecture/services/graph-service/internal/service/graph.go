package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/atpost/graph-service/internal/events"
	"github.com/atpost/graph-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	producer *events.Producer
}

func New(s *store.Store, rdb *redis.Client, producer *events.Producer) *Service {
	return &Service{store: s, rdb: rdb, producer: producer}
}

type Relationship struct {
	Follows      bool   `json:"follows"`
	FollowedBy   bool   `json:"followed_by"`
	Blocked      bool   `json:"blocked"`
	IsMuted      bool   `json:"is_muted"`
	IsFriend     bool   `json:"is_friend"`
	FriendStatus string `json:"friend_status"` // none, pending_sent, pending_received, accepted
}

// --- Follows ---

func (s *Service) Follow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	blocked, err := s.store.CheckBlock(ctx, followeeID, followerID)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("cannot follow: blocked")
	}

	if err := s.store.CreateFollow(ctx, followerID, followeeID); err != nil {
		return err
	}

	s.invalidateRel(ctx, followerID, followeeID)
	s.invalidateCounts(ctx, followerID, followeeID)

	// Publish UserFollowed event for notification-service
	if s.producer != nil {
		if err := s.producer.PublishUserFollowed(ctx, followerID, followeeID); err != nil {
			log.Printf("[graph] Failed to publish UserFollowed event: %v", err)
		}
	}
	return nil
}

func (s *Service) Unfollow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	if err := s.store.DeleteFollow(ctx, followerID, followeeID); err != nil {
		return err
	}
	s.invalidateRel(ctx, followerID, followeeID)
	s.invalidateCounts(ctx, followerID, followeeID)

	// Publish UserUnfollowed event for notification-service
	if s.producer != nil {
		if err := s.producer.PublishUserUnfollowed(ctx, followerID, followeeID); err != nil {
			log.Printf("[graph] Failed to publish UserUnfollowed event: %v", err)
		}
	}
	return nil
}

func (s *Service) Block(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	if err := s.store.CreateBlock(ctx, blockerID, blockedID); err != nil {
		return err
	}
	s.store.DeleteFollow(ctx, blockedID, blockerID)
	s.store.DeleteFollow(ctx, blockerID, blockedID)

	// Also remove friendship if exists
	s.store.RemoveFriend(ctx, blockerID, blockedID)

	s.invalidateRel(ctx, blockerID, blockedID)
	s.invalidateRel(ctx, blockedID, blockerID)
	s.invalidateCounts(ctx, blockerID, blockedID)

	if s.producer != nil {
		if err := s.producer.PublishUserBlocked(ctx, blockerID, blockedID); err != nil {
			log.Printf("[graph] Failed to publish UserBlocked event: %v", err)
		}
	}
	return nil
}

func (s *Service) GetRelationship(ctx context.Context, actorID, targetID uuid.UUID) (*Relationship, error) {
	cacheKey := fmt.Sprintf("rel:%s:%s", actorID, targetID)

	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var rel Relationship
		if err := json.Unmarshal([]byte(val), &rel); err == nil {
			return &rel, nil
		}
	}

	follows, err := s.store.CheckFollow(ctx, actorID, targetID)
	if err != nil {
		return nil, err
	}

	followedBy, err := s.store.CheckFollow(ctx, targetID, actorID)
	if err != nil {
		return nil, err
	}

	blocked, err := s.store.CheckBlock(ctx, targetID, actorID)
	if err != nil {
		return nil, err
	}

	isFriend, err := s.store.CheckFriendship(ctx, actorID, targetID)
	if err != nil {
		return nil, err
	}

	friendStatus := "none"
	if isFriend {
		friendStatus = "accepted"
	} else {
		friendStatus, err = s.store.GetFriendRequestStatus(ctx, actorID, targetID)
		if err != nil {
			return nil, err
		}
	}

	rel := &Relationship{
		Follows:      follows,
		FollowedBy:   followedBy,
		Blocked:      blocked,
		IsFriend:     isFriend,
		FriendStatus: friendStatus,
	}

	go func() {
		data, _ := json.Marshal(rel)
		s.rdb.Set(context.Background(), cacheKey, data, 60*time.Second)
	}()

	return rel, nil
}

// --- Counts ---

func (s *Service) GetCounts(ctx context.Context, userID uuid.UUID) (*store.Counts, error) {
	cacheKey := fmt.Sprintf("graph:counts:%s", userID)

	val, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var c store.Counts
		if err := json.Unmarshal([]byte(val), &c); err == nil {
			return &c, nil
		}
	}

	c, err := s.store.GetCounts(ctx, userID)
	if err != nil {
		return nil, err
	}

	go func() {
		data, _ := json.Marshal(c)
		s.rdb.Set(context.Background(), cacheKey, data, 60*time.Second)
	}()

	return c, nil
}

// --- Lists ---

func (s *Service) GetFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	return s.store.GetFollowers(ctx, userID, limit, offset)
}

func (s *Service) GetFollowing(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	return s.store.GetFollowing(ctx, userID, limit, offset)
}

func (s *Service) GetMutualFollowers(ctx context.Context, userA, userB uuid.UUID, limit int) ([]uuid.UUID, error) {
	return s.store.GetMutualFollowers(ctx, userA, userB, limit)
}

// --- Friends ---

func (s *Service) SendFriendRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	// Check not blocked
	blocked, err := s.store.CheckBlock(ctx, receiverID, senderID)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("cannot send friend request: blocked")
	}

	// Check not already friends
	isFriend, err := s.store.CheckFriendship(ctx, senderID, receiverID)
	if err != nil {
		return err
	}
	if isFriend {
		return fmt.Errorf("already friends")
	}

	if err := s.store.SendFriendRequest(ctx, senderID, receiverID); err != nil {
		return err
	}

	s.invalidateRel(ctx, senderID, receiverID)
	s.invalidateRel(ctx, receiverID, senderID)

	// Publish event for notifications
	if s.producer != nil {
		if err := s.producer.PublishFriendRequestSent(ctx, senderID, receiverID); err != nil {
			log.Printf("[graph] Failed to publish FriendRequestSent event: %v", err)
		}
	}
	return nil
}

func (s *Service) AcceptFriendRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	if err := s.store.AcceptFriendRequest(ctx, senderID, receiverID); err != nil {
		return err
	}

	s.invalidateRel(ctx, senderID, receiverID)
	s.invalidateRel(ctx, receiverID, senderID)
	s.invalidateCounts(ctx, senderID, receiverID)

	// Publish event for notifications
	if s.producer != nil {
		if err := s.producer.PublishFriendRequestAccepted(ctx, senderID, receiverID); err != nil {
			log.Printf("[graph] Failed to publish FriendRequestAccepted event: %v", err)
		}
	}
	return nil
}

func (s *Service) RejectFriendRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	if err := s.store.RejectFriendRequest(ctx, senderID, receiverID); err != nil {
		return err
	}

	s.invalidateRel(ctx, senderID, receiverID)
	s.invalidateRel(ctx, receiverID, senderID)

	if s.producer != nil {
		if err := s.producer.PublishFriendRequestDeclined(ctx, senderID, receiverID); err != nil {
			log.Printf("[graph] Failed to publish FriendRequestDeclined event: %v", err)
		}
	}
	return nil
}

func (s *Service) RemoveFriend(ctx context.Context, userA, userB uuid.UUID) error {
	if err := s.store.RemoveFriend(ctx, userA, userB); err != nil {
		return err
	}

	s.invalidateRel(ctx, userA, userB)
	s.invalidateRel(ctx, userB, userA)
	s.invalidateCounts(ctx, userA, userB)

	if s.producer != nil {
		if err := s.producer.PublishFriendRemoved(ctx, userA, userB, userA); err != nil {
			log.Printf("[graph] Failed to publish FriendRemoved event: %v", err)
		}
	}
	return nil
}

func (s *Service) GetFriends(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	return s.store.GetFriends(ctx, userID, limit, offset)
}

func (s *Service) GetPendingRequests(ctx context.Context, userID uuid.UUID) ([]store.FriendRequest, error) {
	return s.store.GetPendingRequests(ctx, userID)
}

// --- Mutes ---

func (s *Service) Mute(ctx context.Context, muterID, mutedID uuid.UUID) error {
	err := s.store.Mute(ctx, muterID, mutedID)
	// Invalidate relationship cache
	s.invalidateRel(ctx, muterID, mutedID)
	return err
}

func (s *Service) Unmute(ctx context.Context, muterID, mutedID uuid.UUID) error {
	err := s.store.Unmute(ctx, muterID, mutedID)
	s.invalidateRel(ctx, muterID, mutedID)
	return err
}

func (s *Service) GetBlockedAndMuted(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.GetBlockedAndMuted(ctx, userID)
}

func (s *Service) GetRelationshipBatch(ctx context.Context, viewerID uuid.UUID, targetIDs []uuid.UUID) (map[uuid.UUID]store.Relationship, error) {
	if len(targetIDs) > 100 {
		targetIDs = targetIDs[:100]
	}
	return s.store.GetRelationshipBatch(ctx, viewerID, targetIDs)
}

// --- Cache Invalidation ---

func (s *Service) invalidateRel(ctx context.Context, a, b uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("rel:%s:%s", a, b))
}

func (s *Service) invalidateCounts(ctx context.Context, a, b uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("graph:counts:%s", a))
	s.rdb.Del(ctx, fmt.Sprintf("graph:counts:%s", b))
}

// ═══════════════════════════════════════════════════════════
// Close Friends
// ═══════════════════════════════════════════════════════════

func (s *Service) AddCloseFriend(ctx context.Context, userID, friendID uuid.UUID) error {
	return s.store.AddCloseFriend(ctx, userID, friendID)
}

func (s *Service) RemoveCloseFriend(ctx context.Context, userID, friendID uuid.UUID) error {
	return s.store.RemoveCloseFriend(ctx, userID, friendID)
}

func (s *Service) GetCloseFriends(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.GetCloseFriends(ctx, userID)
}

// ═══════════════════════════════════════════════════════════
// Circles
// ═══════════════════════════════════════════════════════════

func (s *Service) CreateCircle(ctx context.Context, ownerID uuid.UUID, name string, emoji *string) (*store.Circle, error) {
	if name == "" {
		return nil, fmt.Errorf("circle name is required")
	}
	return s.store.CreateCircle(ctx, ownerID, name, emoji)
}

func (s *Service) ListCircles(ctx context.Context, ownerID uuid.UUID) ([]store.Circle, error) {
	return s.store.ListCircles(ctx, ownerID)
}

func (s *Service) UpdateCircle(ctx context.Context, circleID, ownerID uuid.UUID, name string, emoji *string) (*store.Circle, error) {
	c, err := s.store.UpdateCircle(ctx, circleID, ownerID, name, emoji)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("circle not found")
	}
	return c, nil
}

func (s *Service) DeleteCircle(ctx context.Context, circleID, ownerID uuid.UUID) error {
	return s.store.DeleteCircle(ctx, circleID, ownerID)
}

func (s *Service) AddCircleMember(ctx context.Context, circleID, ownerID, userID uuid.UUID) error {
	// Verify circle belongs to owner
	c, err := s.store.GetCircle(ctx, circleID, ownerID)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("circle not found")
	}
	return s.store.AddCircleMember(ctx, circleID, userID)
}

func (s *Service) RemoveCircleMember(ctx context.Context, circleID, ownerID, userID uuid.UUID) error {
	c, err := s.store.GetCircle(ctx, circleID, ownerID)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("circle not found")
	}
	return s.store.RemoveCircleMember(ctx, circleID, userID)
}

func (s *Service) GetCircleMembers(ctx context.Context, circleID, ownerID uuid.UUID) ([]uuid.UUID, error) {
	c, err := s.store.GetCircle(ctx, circleID, ownerID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("circle not found")
	}
	return s.store.GetCircleMembers(ctx, circleID)
}

// ═══════════════════════════════════════════════════════════
// Relationship Labels
// ═══════════════════════════════════════════════════════════

func (s *Service) UpsertRelationshipLabel(ctx context.Context, userID, targetID uuid.UUID, label string) error {
	validLabels := map[string]bool{"best_friend": true, "family": true, "colleague": true, "classmate": true, "acquaintance": true}
	if !validLabels[label] {
		return fmt.Errorf("invalid label: must be one of best_friend, family, colleague, classmate, acquaintance")
	}
	return s.store.UpsertRelationshipLabel(ctx, userID, targetID, label)
}

func (s *Service) DeleteRelationshipLabel(ctx context.Context, userID, targetID uuid.UUID) error {
	return s.store.DeleteRelationshipLabel(ctx, userID, targetID)
}

func (s *Service) ListRelationshipLabels(ctx context.Context, userID uuid.UUID) ([]store.RelationshipLabel, error) {
	return s.store.ListRelationshipLabels(ctx, userID)
}

// ═══════════════════════════════════════════════════════════
// Favorites
// ═══════════════════════════════════════════════════════════

func (s *Service) AddFavorite(ctx context.Context, userID, targetID uuid.UUID) error {
	// Cache invalidation for feed ranker
	s.rdb.SAdd(ctx, fmt.Sprintf("favorites:%s", userID.String()), targetID.String())
	s.rdb.Expire(ctx, fmt.Sprintf("favorites:%s", userID.String()), 24*time.Hour)
	return s.store.AddFavorite(ctx, userID, targetID)
}

func (s *Service) RemoveFavorite(ctx context.Context, userID, targetID uuid.UUID) error {
	s.rdb.SRem(ctx, fmt.Sprintf("favorites:%s", userID.String()), targetID.String())
	return s.store.RemoveFavorite(ctx, userID, targetID)
}

func (s *Service) GetFavorites(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.GetFavorites(ctx, userID)
}
