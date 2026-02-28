package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/facebook-like/graph-service/internal/events"
	"github.com/facebook-like/graph-service/internal/store"
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
	return nil
}

func (s *Service) Unfollow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	if err := s.store.DeleteFollow(ctx, followerID, followeeID); err != nil {
		return err
	}
	s.invalidateRel(ctx, followerID, followeeID)
	s.invalidateCounts(ctx, followerID, followeeID)
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
	return nil
}

func (s *Service) RemoveFriend(ctx context.Context, userA, userB uuid.UUID) error {
	if err := s.store.RemoveFriend(ctx, userA, userB); err != nil {
		return err
	}

	s.invalidateRel(ctx, userA, userB)
	s.invalidateRel(ctx, userB, userA)
	s.invalidateCounts(ctx, userA, userB)
	return nil
}

func (s *Service) GetFriends(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	return s.store.GetFriends(ctx, userID, limit, offset)
}

func (s *Service) GetPendingRequests(ctx context.Context, userID uuid.UUID) ([]store.FriendRequest, error) {
	return s.store.GetPendingRequests(ctx, userID)
}

// --- Cache Invalidation ---

func (s *Service) invalidateRel(ctx context.Context, a, b uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("rel:%s:%s", a, b))
}

func (s *Service) invalidateCounts(ctx context.Context, a, b uuid.UUID) {
	s.rdb.Del(ctx, fmt.Sprintf("graph:counts:%s", a))
	s.rdb.Del(ctx, fmt.Sprintf("graph:counts:%s", b))
}
