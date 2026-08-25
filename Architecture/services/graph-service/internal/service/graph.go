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
	"github.com/atpost/shared/counters"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
)

// ErrRateLimited is returned by write actions when the caller has exceeded
// the per-user, per-action quota (spec §10.4). Handlers map it to HTTP 429.
var ErrRateLimited = errors.New("rate limit exceeded")

// ErrWrongEntityType is returned when a relationship operation is invoked
// against the wrong entity kind — e.g. a friend request whose target is a
// page, or a follow whose target is a user. Handlers map it to HTTP 400
// with code WRONG_ENTITY_TYPE per the relationship separation spec §2.3.
var ErrWrongEntityType = errors.New("wrong entity type for operation")

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

	// followerCounter / followingCounter shard counts.follower_count
	// and counts.following_count across Redis so a celebrity with 10M
	// followers no longer contends on a single counts row per follow.
	// Nil-safe: when Redis is nil we fall back to the legacy per-event
	// PG UPDATE inside store.CreateFollow / DeleteFollow.
	followerCounter  *counters.Counter
	followingCounter *counters.Counter
}

func New(s *store.Store, rdb *redis.Client, producer *events.Producer) *Service {
	svc := &Service{
		store:      s,
		rdb:        rdb,
		producer:   producer,
		rateLimit:  ratelimit.New(rdb),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
	if rdb != nil {
		svc.followerCounter = counters.New(rdb, counters.Config{EntityKind: "graph_follower_count", Shards: 32})
		svc.followingCounter = counters.New(rdb, counters.Config{EntityKind: "graph_following_count", Shards: 32})
	}
	return svc
}

// FollowerCounter / FollowingCounter expose the sharded counters so
// cmd/server can attach flush workers. Returns nil when Redis isn't
// configured.
func (s *Service) FollowerCounter() *counters.Counter  { return s.followerCounter }
func (s *Service) FollowingCounter() *counters.Counter { return s.followingCounter }

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
	Follows    bool `json:"follows"`
	FollowedBy bool `json:"followed_by"`
	// Blocked: the target blocked the actor.
	Blocked bool `json:"blocked"`
	// BlockedBy: the actor blocked the target. Callers must check BOTH
	// directions before granting access (fixes-v2 / Codex P1-6).
	BlockedBy    bool `json:"blocked_by"`
	IsMuted      bool `json:"is_muted"`
	IsConnection bool `json:"is_connection"`
	// IsCloseFriend: exact membership of the target's close-friends list,
	// which is the audience for `trusted` / `close_friends` visibility.
	// Do NOT substitute IsConnection — it is strictly broader.
	IsCloseFriend    bool   `json:"is_close_friend"`
	ConnectionStatus string `json:"connection_status"` // none, pending_sent, pending_received, accepted
}

// --- Follows ---

func (s *Service) Follow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	// Product pivot 2026-06-12 (FB model): users AND pages are both
	// followable; friendship is the mutual layer on top. Only unknown
	// targets are rejected. (Supersedes relationship-separation §2.3.)
	et, err := s.store.LookupEntityType(ctx, followeeID)
	if err != nil {
		return fmt.Errorf("lookup followee entity type: %w", err)
	}
	if et != store.EntityTypePage && et != store.EntityTypeUser {
		return ErrWrongEntityType
	}

	// Per-action rate limit (spec §10.4): 200 follows / 24h / user.
	if allowed, _ := s.rateLimit.Allow(ctx, ratelimit.ActionFollow, followerID); !allowed {
		return ErrRateLimited
	}

	// SR-2: the block check and the insert happen together, inside the pair
	// lock, in FollowAtomic.
	//
	// Previously this was CheckBlock followed by an unlocked CreateFollow.
	// Two defects in three lines: the check was one-directional (it asked
	// only whether the followee had blocked the follower, so a user could
	// follow someone they had themselves blocked), and the gap between the
	// check and the insert was a TOCTOU window a concurrent Block walked
	// straight through — leaving a live follow edge across a block, which
	// feed and search then correctly serve.
	inserted, err := s.store.FollowAtomic(ctx, followerID, followeeID)
	if err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot follow: blocked")
		}
		return err
	}

	// Only bump counters when a genuinely new follow row landed —
	// duplicates (already-following) must not drift the counts. Gated
	// at the service layer now because the store no longer writes to
	// the counts row inside a tx.
	if inserted {
		s.adjustCount(ctx, s.followingCounter, followerID, "following_count", 1)
		s.adjustCount(ctx, s.followerCounter, followeeID, "follower_count", 1)
	}

	s.invalidateRel(ctx, followerID, followeeID)
	s.invalidateCounts(ctx, followerID, followeeID)

	// LB-2: the direct publish is REMOVED for this transition.
	//
	// FollowAtomic writes the event to the outbox in the same transaction as
	// the edge, and the relay delivers it. Publishing again here produced TWO
	// Kafka messages for one state change, with different event IDs and
	// different payload shapes, so a consumer had no way to tell they were the
	// same event. "At least once with dedupe on (actor, target, pair_seq)" is
	// only true if every copy carries that sequence — the direct publish did
	// not, so it defeated the dedupe contract it was supposed to be a latency
	// optimisation for.
	//
	// The relay drains continuously and only sleeps when the backlog is empty,
	// so the latency this replaced is a poll interval, not a broker round trip
	// on the request path.
	return nil
}

func (s *Service) Unfollow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	// Symmetric with Follow: users and pages can both be unfollowed.
	et, err := s.store.LookupEntityType(ctx, followeeID)
	if err != nil {
		return fmt.Errorf("lookup followee entity type: %w", err)
	}
	if et != store.EntityTypePage && et != store.EntityTypeUser {
		return ErrWrongEntityType
	}

	removed, err := s.store.DeleteFollow(ctx, followerID, followeeID)
	if err != nil {
		return err
	}
	if removed {
		s.adjustCount(ctx, s.followingCounter, followerID, "following_count", -1)
		s.adjustCount(ctx, s.followerCounter, followeeID, "follower_count", -1)
	}

	s.invalidateRel(ctx, followerID, followeeID)
	s.invalidateCounts(ctx, followerID, followeeID)

	s.publishUserUnfollowedAsync(followerID, followeeID)
	return nil
}

// adjustCount fans an increment/decrement to the sharded Redis counter
// when available, otherwise falls back to the legacy per-event PG
// UPDATE. Failure inside the Redis path is logged but not fatal — the
// hourly CountReconciler backfills any drift the next tick.
func (s *Service) adjustCount(ctx context.Context, c *counters.Counter, userID uuid.UUID, column string, delta int64) {
	if c != nil {
		if err := c.Inc(ctx, userID.String(), delta); err == nil {
			return
		} else {
			log.Printf("[graph] sharded %s counter inc failed (user=%s delta=%d): %v — falling back to PG UPDATE",
				column, userID, delta, err)
		}
	}
	if err := s.store.IncrementCountColumn(ctx, userID, column, delta); err != nil {
		log.Printf("[graph] PG %s UPDATE failed (user=%s delta=%d): %v",
			column, userID, delta, err)
	}
}

// Block severs every relationship between the pair atomically.
//
// M3-P0-6: this used to be a sequence of independent statements with the
// connection-removal error discarded and the safety event published from a
// fire-and-forget goroutine. That allowed a concurrent follow to survive a
// block, a partial failure to report success, and the downstream event to be
// lost entirely with nothing recording that it was owed.
//
// Now a single transaction, taken under a deterministic per-pair advisory
// lock, performs every removal and writes the outbox row before committing.
// The commit is the success boundary: after it, the block is real AND the
// event is guaranteed to be delivered by the relay.
func (s *Service) Block(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	res, err := s.store.BlockAtomic(ctx, blockerID, blockedID)
	if err != nil {
		return err
	}

	// Counter adjustments follow the committed truth. They are deliberately
	// OUTSIDE the transaction: counters are reconcilable and must not hold a
	// pair lock open, whereas edge correctness is not reconcilable and must
	// be synchronous.
	if res.RemovedFollowReverse {
		s.adjustCount(ctx, s.followingCounter, blockedID, "following_count", -1)
		s.adjustCount(ctx, s.followerCounter, blockerID, "follower_count", -1)
	}
	if res.RemovedFollowForward {
		s.adjustCount(ctx, s.followingCounter, blockerID, "following_count", -1)
		s.adjustCount(ctx, s.followerCounter, blockedID, "follower_count", -1)
	}

	s.invalidateRel(ctx, blockerID, blockedID)
	s.invalidateRel(ctx, blockedID, blockerID)
	s.invalidateCounts(ctx, blockerID, blockedID)

	// LB-2: the direct publish is REMOVED. BlockAtomic wrote the canonical
	// event to the outbox in the same transaction; publishing again here
	// produced a SECOND Kafka message for one state change, with a different
	// event id and a different payload shape, which a consumer cannot
	// recognise as the same event. That defeated the (actor, target,
	// pair_seq) dedupe contract the outbox exists to provide.
	return nil
}

// Unblock removes a block. The block event consumers (chat-service severs
// active conversations on block) need the matching unblock signal.
func (s *Service) Unblock(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	// SR-2: the unblock event is recorded in the same transaction as the
	// delete. chat-service severs active conversations on block; if the
	// unblock event is lost, that sever is permanent and the users cannot
	// talk again even though the block is gone.
	if err := s.store.UnblockAtomic(ctx, blockerID, blockedID); err != nil {
		return err
	}
	s.invalidateRel(ctx, blockerID, blockedID)
	s.invalidateRel(ctx, blockedID, blockerID)
	// LB-2: removed for the same reason as the block path — UnblockAtomic
	// already wrote the canonical event durably.
	return nil
}

// publishUserFollowedAsync / Unfollowed / Blocked fire-and-forget the
// Kafka publish on a fresh background context so the HTTP handler can
// ack the user action immediately. If the broker is slow or unavailable
// the failure is logged and the durable downstream path (counter
// reconciliation + outbox replay) closes the gap.
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
		BlockedBy:        full.BlockedBy,
		IsMuted:          full.IsMuted,
		IsConnection:     full.IsConnection,
		IsCloseFriend:    full.IsCloseFriend,
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

// GetFollowingIDs returns the top-`limit` user IDs that `userID` follows,
// most-recent first. Capped at 500 (see Store.GetFollowingIDs).
func (s *Service) GetFollowingIDs(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error) {
	return s.store.GetFollowingIDs(ctx, userID, limit)
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
	// Relationship separation spec §2.3: friend requests are users-only.
	// A page UUID as receiver silently created an orphan connection_request
	// row before — the row was unreachable from the page's UI and the user
	// who sent it got stuck with "Requested" forever.
	et, err := s.store.LookupEntityType(ctx, receiverID)
	if err != nil {
		return fmt.Errorf("lookup receiver entity type: %w", err)
	}
	if et != store.EntityTypeUser {
		return ErrWrongEntityType
	}

	// Per-action rate limit (spec §10.4): 30 connection requests / 24h / user.
	if allowed, _ := s.rateLimit.Allow(ctx, ratelimit.ActionConnectionRequest, senderID); !allowed {
		return ErrRateLimited
	}

	// SR-2: the authoritative, symmetric block check now happens inside the
	// pair lock in SendConnectionRequestAtomic / AcceptConnectionRequestAtomic
	// below. This early check stays as a cheap rejection so an obviously
	// blocked request does not pay for a transaction — it is an optimisation,
	// NOT the guarantee.
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

	// UH5: if the receiver has already sent a pending request the other
	// way, auto-accept it instead of creating a parallel one. Without
	// this the bidirectional race (A→B and B→A racing on the same RTT)
	// leaves two pending rows and neither side becomes connected until
	// somebody clicks Accept — surprising UX and easy to overlook.
	reverseStatus, err := s.store.GetConnectionRequestStatus(ctx, receiverID, senderID)
	if err != nil {
		return err
	}
	if reverseStatus == "pending_sent" {
		if err := s.store.AcceptConnectionRequestAtomic(ctx, receiverID, senderID); err != nil {
			if errors.Is(err, store.ErrBlockedPair) {
				return fmt.Errorf("cannot connect: blocked")
			}
			return err
		}
		s.invalidateRel(ctx, senderID, receiverID)
		s.invalidateRel(ctx, receiverID, senderID)
		s.invalidateCounts(ctx, senderID, receiverID)
		if s.producer != nil {
			if err := s.producer.PublishConnectionAccepted(ctx, receiverID, senderID); err != nil {
				log.Printf("[graph] Failed to publish ConnectionAccepted event: %v", err)
			}
		}
		return nil
	}

	if err := s.store.SendConnectionRequestAtomic(ctx, senderID, receiverID, source, message); err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot send connection request: blocked")
		}
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
	// SR-2: accepting is a relationship CREATION that happens long after the
	// request was sent, so a block may have landed in between. It runs under
	// the pair lock with a post-lock symmetric block re-check.
	if err := s.store.AcceptConnectionRequestAtomic(ctx, senderID, receiverID); err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot accept: blocked")
		}
		return err
	}

	// FB model: becoming friends auto-follows both ways so each side
	// sees the other's posts and shows "Following". Idempotent inserts;
	// counters bump only on genuinely new rows. These go through the atomic
	// path too — an auto-follow is still a follow, and a block committed
	// between the accept and these inserts must stop them.
	for _, pair := range [][2]uuid.UUID{{senderID, receiverID}, {receiverID, senderID}} {
		inserted, err := s.store.FollowAtomic(ctx, pair[0], pair[1])
		if err != nil {
			log.Printf("[graph] auto-follow on friendship accept failed: %v", err)
			continue
		}
		if inserted {
			s.adjustCount(ctx, s.followingCounter, pair[0], "following_count", 1)
			s.adjustCount(ctx, s.followerCounter, pair[1], "follower_count", 1)
		}
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

// BlockedWithAny reports which of the candidates hold a block with userID in
// either direction. Internal-only: chat's group-roster gate (P0-5).
func (s *Service) BlockedWithAny(ctx context.Context, userID uuid.UUID, candidates []uuid.UUID) ([]uuid.UUID, error) {
	return s.store.BlockedWithAny(ctx, userID, candidates)
}

func (s *Service) GetRelationshipBatch(ctx context.Context, viewerID uuid.UUID, targetIDs []uuid.UUID) (map[uuid.UUID]store.Relationship, error) {
	// M4-P0-1: this used to truncate to the first 100 targets and return
	// normally. Every target past the cap came back absent from the map,
	// which callers read as "no relationship" — so a viewer blocked by the
	// 101st author was reported unblocked. Silent truncation of a safety
	// answer is worse than no answer; the store now rejects an oversized
	// batch and the caller chunks.
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
	if err := s.store.AddCloseFriendAtomic(ctx, userID, friendID, "manual"); err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot add close friend: blocked")
		}
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
	// Verify circle belongs to owner. Ownership is re-verified inside the
	// transaction too — this read is only a cheap early rejection.
	c, err := s.store.GetCircle(ctx, circleID, ownerID)
	if err != nil {
		return err
	}
	if c == nil {
		return fmt.Errorf("circle not found")
	}
	// SR-2: a circle is an AUDIENCE. A membership row that survives a block
	// keeps the blocked person able to see circle-visibility content, so the
	// insert runs under the pair lock with a post-lock block check.
	if err := s.store.AddCircleMemberAtomic(ctx, circleID, ownerID, userID); err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot add circle member: blocked")
		}
		return err
	}
	return nil
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
	// SR-2: labels are pair state. Creating one across a block re-establishes
	// a relationship the blocker severed.
	if err := s.store.UpsertRelationshipLabelAtomic(ctx, userID, targetID, label); err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot label: blocked")
		}
		return err
	}
	return nil
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
	// SR-2: the durable write happens FIRST, under the pair lock.
	//
	// This used to populate the Redis set the feed ranker reads and only then
	// attempt the insert. On any rejection — including a blocked pair — the
	// cache entry survived, so the feed kept pinning an account the database
	// had refused to favourite. Cache after commit, never before.
	if err := s.store.AddFavoriteAtomic(ctx, userID, targetID); err != nil {
		if errors.Is(err, store.ErrBlockedPair) {
			return fmt.Errorf("cannot favourite: blocked")
		}
		return err
	}
	s.rdb.SAdd(ctx, fmt.Sprintf("favorites:%s", userID.String()), targetID.String())
	s.rdb.Expire(ctx, fmt.Sprintf("favorites:%s", userID.String()), 24*time.Hour)
	return nil
}

func (s *Service) RemoveFavorite(ctx context.Context, userID, targetID uuid.UUID) error {
	s.rdb.SRem(ctx, fmt.Sprintf("favorites:%s", userID.String()), targetID.String())
	return s.store.RemoveFavorite(ctx, userID, targetID)
}

func (s *Service) GetFavorites(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	return s.store.GetFavorites(ctx, userID)
}
