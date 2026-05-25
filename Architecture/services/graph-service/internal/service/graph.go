package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/graph-service/internal/events"
	"github.com/atpost/graph-service/internal/ratelimit"
	"github.com/atpost/graph-service/internal/store"
	"github.com/atpost/graph-service/internal/userclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
)

// ErrRateLimited is returned by write actions when the caller has exceeded
// the per-user, per-action quota (spec §10.4). Handlers map it to HTTP 429.
var ErrRateLimited = errors.New("rate limit exceeded")

// Trusted Circle (close-friends) validation errors — friends-sheets spec §4.1.
// Handlers map each to its own HTTP status + error code.
var (
	ErrCannotAddSelf    = errors.New("cannot add yourself to your Trusted Circle")
	ErrNotAFriend       = errors.New("user is not a connection")
	ErrCircleCapReached = errors.New("Trusted Circle is full")
	ErrAlreadyMember    = errors.New("already in your Trusted Circle")
	// ErrUserUnavailable means the target is a graph connection but has no row
	// in the local users projection yet — close_friends FK-references users,
	// so the insert would 23503. Surfaced as a clean 4xx instead of a raw 500.
	ErrUserUnavailable = errors.New("this person's profile isn't ready yet")
)

// isForeignKeyViolation reports whether err is a Postgres FK-violation (23503).
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

type Service struct {
	store     *store.Store
	rdb       *redis.Client
	producer  *events.Producer
	rateLimit *ratelimit.Limiter

	// Permission resolver dependencies (spec §4 / §9.8). userServiceURL is
	// the base URL of user-service; internalKey authenticates the call.
	userServiceURL string
	internalKey    string
	httpClient     *http.Client

	// userClient repairs the app.users projection on demand before a write
	// whose FK references it (e.g. close_friends). Nil = no read-through.
	userClient *userclient.Client
}

func New(s *store.Store, rdb *redis.Client, producer *events.Producer) *Service {
	return &Service{
		store:      s,
		rdb:        rdb,
		producer:   producer,
		rateLimit:  ratelimit.New(rdb),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// WithPermissionSource wires the user-service endpoint the permission
// resolver reads privacy settings from. Without it the resolver falls back
// to strict defaults.
func (s *Service) WithPermissionSource(userServiceURL, internalKey string) *Service {
	s.userServiceURL = strings.TrimRight(userServiceURL, "/")
	s.internalKey = internalKey
	return s
}

// WithUserEnsurer wires the user-service client used for read-through repair
// of the app.users projection before close_friends inserts.
func (s *Service) WithUserEnsurer(c *userclient.Client) *Service {
	s.userClient = c
	return s
}

type Relationship struct {
	Follows          bool   `json:"follows"`
	FollowedBy       bool   `json:"followed_by"`
	Blocked          bool   `json:"blocked"`
	IsMuted          bool   `json:"is_muted"`
	IsConnection     bool   `json:"is_connection"`
	ConnectionStatus string `json:"connection_status"` // none, pending_sent, pending_received, accepted
}

// --- Follows ---

func (s *Service) Follow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	// Per-action rate limit (spec §10.4): 200 follows / 24h / user.
	if allowed, _ := s.rateLimit.Allow(ctx, ratelimit.ActionFollow, followerID); !allowed {
		return ErrRateLimited
	}

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

	// Audit CG3: publish asynchronously so the follow ack doesn't wait
	// on the Kafka broker. A 50-200 ms WriteMessages ACK was on the
	// critical path of every follow request.
	s.publishUserFollowedAsync(followerID, followeeID)
	return nil
}

func (s *Service) Unfollow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	if err := s.store.DeleteFollow(ctx, followerID, followeeID); err != nil {
		return err
	}
	s.invalidateRel(ctx, followerID, followeeID)
	s.invalidateCounts(ctx, followerID, followeeID)

	s.publishUserUnfollowedAsync(followerID, followeeID)
	return nil
}

func (s *Service) Block(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	if err := s.store.CreateBlock(ctx, blockerID, blockedID); err != nil {
		return err
	}
	s.store.DeleteFollow(ctx, blockedID, blockerID)
	s.store.DeleteFollow(ctx, blockerID, blockedID)

	// Also remove the connection if one exists.
	s.store.RemoveConnection(ctx, blockerID, blockedID)

	s.invalidateRel(ctx, blockerID, blockedID)
	s.invalidateRel(ctx, blockedID, blockerID)
	s.invalidateCounts(ctx, blockerID, blockedID)

	s.publishUserBlockedAsync(blockerID, blockedID)
	return nil
}

// Unblock removes a block. The block event consumers (chat-service severs
// active conversations on block) need the matching unblock signal.
func (s *Service) Unblock(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	if err := s.store.DeleteBlock(ctx, blockerID, blockedID); err != nil {
		return err
	}
	s.invalidateRel(ctx, blockerID, blockedID)
	s.invalidateRel(ctx, blockedID, blockerID)
	s.publishUserUnblockedAsync(blockerID, blockedID)
	return nil
}

// publishUserFollowedAsync / Unfollowed / Blocked fire-and-forget the
// Kafka publish on a fresh background context so the HTTP handler can
// ack the user action immediately. If the broker is slow or unavailable
// the failure is logged and the durable downstream path (counter
// reconciliation + outbox replay) closes the gap.
func (s *Service) publishUserFollowedAsync(followerID, followeeID uuid.UUID) {
	if s.producer == nil {
		return
	}
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.producer.PublishUserFollowed(pubCtx, followerID, followeeID); err != nil {
			log.Printf("[graph] async PublishUserFollowed failed: %v", err)
		}
	}()
}

func (s *Service) publishUserUnfollowedAsync(followerID, followeeID uuid.UUID) {
	if s.producer == nil {
		return
	}
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.producer.PublishUserUnfollowed(pubCtx, followerID, followeeID); err != nil {
			log.Printf("[graph] async PublishUserUnfollowed failed: %v", err)
		}
	}()
}

func (s *Service) publishUserBlockedAsync(blockerID, blockedID uuid.UUID) {
	if s.producer == nil {
		return
	}
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.producer.PublishUserBlocked(pubCtx, blockerID, blockedID); err != nil {
			log.Printf("[graph] async PublishUserBlocked failed: %v", err)
		}
	}()
}

func (s *Service) publishUserUnblockedAsync(blockerID, blockedID uuid.UUID) {
	if s.producer == nil {
		return
	}
	go func() {
		pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.producer.PublishUserUnblocked(pubCtx, blockerID, blockedID); err != nil {
			log.Printf("[graph] async PublishUserUnblocked failed: %v", err)
		}
	}()
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

	// Audit HG1: collapse 6 sequential round trips (CheckFollow ×2 +
	// CheckBlock + CheckMute + CheckFriendship + GetFriendRequestStatus
	// ×2) into a single EXISTS-based query.
	full, err := s.store.GetRelationshipFull(ctx, actorID, targetID)
	if err != nil {
		return nil, err
	}

	connectionStatus := "none"
	switch {
	case full.IsConnection:
		connectionStatus = "accepted"
	case full.ConnectionRequestSent:
		connectionStatus = "pending_sent"
	case full.ConnectionRequestReceived:
		connectionStatus = "pending_received"
	}

	rel := &Relationship{
		Follows:          full.Follows,
		FollowedBy:       full.FollowedBy,
		Blocked:          full.Blocked,
		IsMuted:          full.IsMuted,
		IsConnection:     full.IsConnection,
		ConnectionStatus: connectionStatus,
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

// GetFollowersCursor / GetFollowingCursor are the scale-friendly
// variants (HG2). Keyset pagination on (created_at, user_id) stays
// O(log n) even on celebrities with millions of edges.
func (s *Service) GetFollowersCursor(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]store.FollowEdge, string, error) {
	return s.store.GetFollowersCursor(ctx, userID, limit, cursor)
}

func (s *Service) GetFollowingCursor(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]store.FollowEdge, string, error) {
	return s.store.GetFollowingCursor(ctx, userID, limit, cursor)
}

func (s *Service) GetMutualFollowers(ctx context.Context, userA, userB uuid.UUID, limit int) ([]uuid.UUID, error) {
	return s.store.GetMutualFollowers(ctx, userA, userB, limit)
}

// --- Connections ---

func (s *Service) SendConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID, source, message string) error {
	// Per-action rate limit (spec §10.4): 30 connection requests / 24h / user.
	if allowed, _ := s.rateLimit.Allow(ctx, ratelimit.ActionConnectionRequest, senderID); !allowed {
		return ErrRateLimited
	}

	// Check not blocked
	blocked, err := s.store.CheckBlock(ctx, receiverID, senderID)
	if err != nil {
		return err
	}
	if blocked {
		return fmt.Errorf("cannot send connection request: blocked")
	}

	// Check not already connected
	isConnection, err := s.store.CheckConnection(ctx, senderID, receiverID)
	if err != nil {
		return err
	}
	if isConnection {
		return fmt.Errorf("already connected")
	}

	if err := s.store.SendConnectionRequest(ctx, senderID, receiverID, source, message); err != nil {
		return err
	}

	s.invalidateRel(ctx, senderID, receiverID)
	s.invalidateRel(ctx, receiverID, senderID)

	if s.producer != nil {
		if err := s.producer.PublishConnectionRequested(ctx, senderID, receiverID); err != nil {
			log.Printf("[graph] Failed to publish ConnectionRequested event: %v", err)
		}
	}
	return nil
}

func (s *Service) AcceptConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	if err := s.store.AcceptConnectionRequest(ctx, senderID, receiverID); err != nil {
		return err
	}

	s.invalidateRel(ctx, senderID, receiverID)
	s.invalidateRel(ctx, receiverID, senderID)
	s.invalidateCounts(ctx, senderID, receiverID)

	if s.producer != nil {
		if err := s.producer.PublishConnectionAccepted(ctx, senderID, receiverID); err != nil {
			log.Printf("[graph] Failed to publish ConnectionAccepted event: %v", err)
		}
	}
	return nil
}

func (s *Service) DeclineConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	if err := s.store.DeclineConnectionRequest(ctx, senderID, receiverID); err != nil {
		return err
	}

	s.invalidateRel(ctx, senderID, receiverID)
	s.invalidateRel(ctx, receiverID, senderID)

	if s.producer != nil {
		if err := s.producer.PublishConnectionDeclined(ctx, senderID, receiverID); err != nil {
			log.Printf("[graph] Failed to publish ConnectionDeclined event: %v", err)
		}
	}
	return nil
}

// CancelConnectionRequest withdraws the actor's own pending request to target.
func (s *Service) CancelConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	cancelled, err := s.store.CancelConnectionRequest(ctx, senderID, receiverID)
	if err != nil {
		return err
	}
	if !cancelled {
		return fmt.Errorf("no pending connection request to cancel")
	}

	s.invalidateRel(ctx, senderID, receiverID)
	s.invalidateRel(ctx, receiverID, senderID)

	if s.producer != nil {
		if err := s.producer.PublishConnectionRequestCancelled(ctx, senderID, receiverID); err != nil {
			log.Printf("[graph] Failed to publish ConnectionRequestCancelled event: %v", err)
		}
	}
	return nil
}

func (s *Service) RemoveConnection(ctx context.Context, userA, userB uuid.UUID) error {
	if err := s.store.RemoveConnection(ctx, userA, userB); err != nil {
		return err
	}

	s.invalidateRel(ctx, userA, userB)
	s.invalidateRel(ctx, userB, userA)
	s.invalidateCounts(ctx, userA, userB)

	if s.producer != nil {
		if err := s.producer.PublishConnectionRemoved(ctx, userA, userB, userA); err != nil {
			log.Printf("[graph] Failed to publish ConnectionRemoved event: %v", err)
		}
	}
	return nil
}

func (s *Service) GetConnections(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	return s.store.GetConnections(ctx, userID, limit, offset)
}

func (s *Service) GetPendingConnectionRequests(ctx context.Context, userID uuid.UUID) ([]store.ConnectionRequest, error) {
	return s.store.GetPendingConnectionRequests(ctx, userID)
}

func (s *Service) GetSentConnectionRequests(ctx context.Context, userID uuid.UUID) ([]store.ConnectionRequest, error) {
	return s.store.GetSentConnectionRequests(ctx, userID)
}

// GetFilteredConnectionRequests lists the user's auto-filtered (hidden) pending
// connection requests.
func (s *Service) GetFilteredConnectionRequests(ctx context.Context, userID uuid.UUID) ([]store.ConnectionRequest, error) {
	return s.store.GetFilteredConnectionRequests(ctx, userID)
}

// SetRequestFiltered marks a pending request as auto-filtered. Driven by
// trust-safety-service.
func (s *Service) SetRequestFiltered(ctx context.Context, senderID, receiverID uuid.UUID) error {
	return s.store.SetRequestFiltered(ctx, senderID, receiverID)
}

// UnfilterConnectionRequest moves a request back into the recipient's visible
// inbox.
func (s *Service) UnfilterConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	return s.store.UnfilterConnectionRequest(ctx, senderID, receiverID)
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

// IsBlockedBy returns true when blockerID has blocked candidateID.
// Used by handlers that need to suppress responses (e.g., the
// follower/following list cannot be read by users the owner has
// blocked — audit HG4).
func (s *Service) IsBlockedBy(ctx context.Context, blockerID, candidateID uuid.UUID) (bool, error) {
	return s.store.CheckBlock(ctx, blockerID, candidateID)
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

// MG1: invalidator goroutines used to drop Redis errors silently,
// so a transient Redis outage during a follow/unfollow / block flow
// would leave stale relationship-cache entries that survived for the
// full TTL (60s) — long enough that a freshly-blocked user could
// still see the blocker's posts. Now logged at WARN so the issue
// surfaces in metrics + ops dashboards. Per-key DEL to keep the
// log line useful when one key fails.
func (s *Service) invalidateRel(ctx context.Context, a, b uuid.UUID) {
	key := fmt.Sprintf("rel:%s:%s", a, b)
	if err := s.rdb.Del(ctx, key).Err(); err != nil {
		log.Printf("[graph] cache invalidate failed: key=%s err=%v", key, err)
	}
}

func (s *Service) invalidateCounts(ctx context.Context, a, b uuid.UUID) {
	for _, id := range [2]uuid.UUID{a, b} {
		key := fmt.Sprintf("graph:counts:%s", id)
		if err := s.rdb.Del(ctx, key).Err(); err != nil {
			log.Printf("[graph] cache invalidate failed: key=%s err=%v", key, err)
		}
	}
}

// ═══════════════════════════════════════════════════════════
// Close Friends
// ═══════════════════════════════════════════════════════════

// CloseFriendCap is the maximum Trusted Circle size (friends-sheets spec §3.1,
// decision D3 — hardcoded for v1).
const CloseFriendCap = 10

func (s *Service) AddCloseFriend(ctx context.Context, userID, friendID uuid.UUID) error {
	if userID == friendID {
		return ErrCannotAddSelf
	}
	// Per-action rate limit: 30 Trusted Circle adds / 24h / user.
	if allowed, _ := s.rateLimit.Allow(ctx, ratelimit.ActionCloseFriendAdd, userID); !allowed {
		return ErrRateLimited
	}
	// A close friend must be an existing connection (spec §3.1).
	connected, err := s.store.CheckConnection(ctx, userID, friendID)
	if err != nil {
		return err
	}
	if !connected {
		return ErrNotAFriend
	}
	already, err := s.store.IsCloseFriend(ctx, userID, friendID)
	if err != nil {
		return err
	}
	if already {
		return ErrAlreadyMember
	}
	count, err := s.store.CountCloseFriends(ctx, userID)
	if err != nil {
		return err
	}
	if count >= CloseFriendCap {
		return ErrCircleCapReached
	}
	// Read-through repair: close_friends FK-references the app.users
	// projection. If a UserRegistered event was lost, the row is missing and
	// the insert would 23503. Repair it from identity first so the action
	// succeeds. ErrUserNotFound = the user genuinely does not exist; a
	// transport error is non-fatal — fall through and let the FK backstop.
	if s.userClient != nil {
		if err := s.userClient.EnsureUser(ctx, friendID); err != nil {
			if errors.Is(err, userclient.ErrUserNotFound) {
				return ErrUserUnavailable
			}
			log.Printf("[graph] EnsureUser(%s) failed, proceeding: %v", friendID, err)
		}
	}
	if err := s.store.AddCloseFriend(ctx, userID, friendID, "manual"); err != nil {
		if isForeignKeyViolation(err) {
			// Connection exists but the user projection hasn't caught up.
			return ErrUserUnavailable
		}
		return err
	}
	if s.producer != nil {
		if err := s.producer.PublishCloseFriendAdded(ctx, userID, friendID); err != nil {
			log.Printf("[graph] Failed to publish CloseFriendAdded event: %v", err)
		}
	}
	return nil
}

func (s *Service) RemoveCloseFriend(ctx context.Context, userID, friendID uuid.UUID) error {
	if err := s.store.RemoveCloseFriend(ctx, userID, friendID); err != nil {
		return err
	}
	if s.producer != nil {
		if err := s.producer.PublishCloseFriendRemoved(ctx, userID, friendID); err != nil {
			log.Printf("[graph] Failed to publish CloseFriendRemoved event: %v", err)
		}
	}
	return nil
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
