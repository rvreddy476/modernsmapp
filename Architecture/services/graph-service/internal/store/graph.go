package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Follow struct {
	FollowerID uuid.UUID `json:"follower_id"`
	FolloweeID uuid.UUID `json:"followee_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type Relationship struct {
	Follows    bool `json:"follows"`
	FollowedBy bool `json:"followed_by"`
	Blocked    bool `json:"blocked"`
	IsMuted    bool `json:"is_muted"`
}

type Block struct {
	BlockerID uuid.UUID `json:"blocker_id"`
	BlockedID uuid.UUID `json:"blocked_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Counts struct {
	UserID         uuid.UUID `json:"user_id"`
	FollowerCount  int64     `json:"follower_count"`
	FollowingCount int64     `json:"following_count"`
	FriendCount    int64     `json:"friend_count"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type FriendRequest struct {
	SenderID   uuid.UUID `json:"sender_id"`
	ReceiverID uuid.UUID `json:"receiver_id"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// --- Follows ---

// CheckBlock returns true if blocker has blocked blockedID.
func (s *Store) CheckBlock(ctx context.Context, blockerID, blockedID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM blocks WHERE blocker_id = $1 AND blocked_id = $2)
	`, blockerID, blockedID).Scan(&exists)
	return exists, err
}

// CreateFollow adds a follow relationship.
func (s *Store) CreateFollow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO follows (follower_id, followee_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (follower_id, followee_id) DO NOTHING
	`, followerID, followeeID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO counts (user_id, following_count, follower_count, friend_count, updated_at)
		VALUES ($1, 1, 0, 0, NOW())
		ON CONFLICT (user_id) DO UPDATE SET following_count = counts.following_count + 1, updated_at = NOW()
	`, followerID)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO counts (user_id, following_count, follower_count, friend_count, updated_at)
		VALUES ($1, 0, 1, 0, NOW())
		ON CONFLICT (user_id) DO UPDATE SET follower_count = counts.follower_count + 1, updated_at = NOW()
	`, followeeID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// DeleteFollow removes a follow relationship.
func (s *Store) DeleteFollow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `
		DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2
	`, followerID, followeeID)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		_, err = tx.Exec(ctx, `UPDATE counts SET following_count = following_count - 1 WHERE user_id = $1`, followerID)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE counts SET follower_count = follower_count - 1 WHERE user_id = $1`, followeeID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// CreateBlock adds a block.
func (s *Store) CreateBlock(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO blocks (blocker_id, blocked_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (blocker_id, blocked_id) DO NOTHING
	`, blockerID, blockedID)
	return err
}

// DeleteBlock removes a block.
func (s *Store) DeleteBlock(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM blocks WHERE blocker_id = $1 AND blocked_id = $2
	`, blockerID, blockedID)
	return err
}

// CheckFollow checks if A follows B.
func (s *Store) CheckFollow(ctx context.Context, followerID, followeeID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2)
	`, followerID, followeeID).Scan(&exists)
	return exists, err
}

// GetCounts returns follower/following/friend counts for a user.
func (s *Store) GetCounts(ctx context.Context, userID uuid.UUID) (*Counts, error) {
	var c Counts
	err := s.db.QueryRow(ctx, `
		SELECT user_id, follower_count, following_count, friend_count, updated_at
		FROM counts WHERE user_id = $1
	`, userID).Scan(&c.UserID, &c.FollowerCount, &c.FollowingCount, &c.FriendCount, &c.UpdatedAt)
	if err != nil {
		return &Counts{UserID: userID}, nil
	}
	return &c, nil
}

// GetFollowers returns paginated list of user IDs that follow the given user.
func (s *Store) GetFollowers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT follower_id FROM follows
		WHERE followee_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetFollowing returns paginated list of user IDs that the given user follows.
func (s *Store) GetFollowing(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT followee_id FROM follows
		WHERE follower_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetMutualFollowers returns user IDs that follow both userA and userB.
func (s *Store) GetMutualFollowers(ctx context.Context, userA, userB uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT f1.follower_id FROM follows f1
		JOIN follows f2 ON f1.follower_id = f2.follower_id
		WHERE f1.followee_id = $1 AND f2.followee_id = $2
		LIMIT $3
	`, userA, userB, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- Friends ---

// normalizePair ensures a < b (lexicographic UUID ordering).
func normalizePair(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
	if a.String() < b.String() {
		return a, b
	}
	return b, a
}

// SendFriendRequest creates a pending friend request.
func (s *Store) SendFriendRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	now := time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO friend_requests (sender_id, receiver_id, status, created_at, updated_at)
		VALUES ($1, $2, 'pending', $3, $3)
		ON CONFLICT (sender_id, receiver_id) DO UPDATE
		SET status = 'pending', updated_at = $3
		WHERE friend_requests.status = 'rejected'
	`, senderID, receiverID, now)
	return err
}

// AcceptFriendRequest accepts a pending request and creates the friendship.
func (s *Store) AcceptFriendRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 1. Update request status
	cmdTag, err := tx.Exec(ctx, `
		UPDATE friend_requests SET status = 'accepted', updated_at = NOW()
		WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'
	`, senderID, receiverID)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("no pending friend request found")
	}

	// 2. Insert into friends table (normalized order)
	userA, userB := normalizePair(senderID, receiverID)
	_, err = tx.Exec(ctx, `
		INSERT INTO friends (user_a, user_b, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_a, user_b) DO NOTHING
	`, userA, userB)
	if err != nil {
		return err
	}

	// 3. Increment friend_count for both users
	for _, uid := range []uuid.UUID{senderID, receiverID} {
		_, err = tx.Exec(ctx, `
			INSERT INTO counts (user_id, follower_count, following_count, friend_count, updated_at)
			VALUES ($1, 0, 0, 1, NOW())
			ON CONFLICT (user_id) DO UPDATE SET friend_count = counts.friend_count + 1, updated_at = NOW()
		`, uid)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// RejectFriendRequest rejects a pending request.
func (s *Store) RejectFriendRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE friend_requests SET status = 'rejected', updated_at = NOW()
		WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'
	`, senderID, receiverID)
	return err
}

// RemoveFriend removes a friendship and decrements counts.
func (s *Store) RemoveFriend(ctx context.Context, userA, userB uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	nA, nB := normalizePair(userA, userB)
	cmdTag, err := tx.Exec(ctx, `
		DELETE FROM friends WHERE user_a = $1 AND user_b = $2
	`, nA, nB)
	if err != nil {
		return err
	}

	if cmdTag.RowsAffected() > 0 {
		for _, uid := range []uuid.UUID{userA, userB} {
			_, err = tx.Exec(ctx, `
				UPDATE counts SET friend_count = GREATEST(friend_count - 1, 0), updated_at = NOW()
				WHERE user_id = $1
			`, uid)
			if err != nil {
				return err
			}
		}

		// Clean up the friend_requests row
		_, _ = tx.Exec(ctx, `DELETE FROM friend_requests WHERE sender_id = $1 AND receiver_id = $2`, userA, userB)
		_, _ = tx.Exec(ctx, `DELETE FROM friend_requests WHERE sender_id = $1 AND receiver_id = $2`, userB, userA)
	}

	return tx.Commit(ctx)
}

// CheckFriendship returns true if userA and userB are friends.
func (s *Store) CheckFriendship(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	nA, nB := normalizePair(userA, userB)
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM friends WHERE user_a = $1 AND user_b = $2)
	`, nA, nB).Scan(&exists)
	return exists, err
}

// GetFriendRequestStatus returns the friend request status between actor and target.
// Returns: "none", "pending_sent", "pending_received", "accepted", "rejected".
func (s *Store) GetFriendRequestStatus(ctx context.Context, actorID, targetID uuid.UUID) (string, error) {
	// Check actor → target
	var status string
	err := s.db.QueryRow(ctx, `
		SELECT status FROM friend_requests
		WHERE sender_id = $1 AND receiver_id = $2
	`, actorID, targetID).Scan(&status)
	if err == nil {
		if status == "pending" {
			return "pending_sent", nil
		}
		return status, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}

	// Check target → actor
	err = s.db.QueryRow(ctx, `
		SELECT status FROM friend_requests
		WHERE sender_id = $1 AND receiver_id = $2
	`, targetID, actorID).Scan(&status)
	if err == nil {
		if status == "pending" {
			return "pending_received", nil
		}
		return status, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}

	return "none", nil
}

// GetFriends returns paginated list of friend user IDs.
func (s *Store) GetFriends(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS friend_id
		FROM friends
		WHERE user_a = $1 OR user_b = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetPendingRequests returns pending friend requests received by the user.
func (s *Store) GetPendingRequests(ctx context.Context, userID uuid.UUID) ([]FriendRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT sender_id, receiver_id, status, created_at, updated_at
		FROM friend_requests
		WHERE receiver_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reqs []FriendRequest
	for rows.Next() {
		var r FriendRequest
		if err := rows.Scan(&r.SenderID, &r.ReceiverID, &r.Status, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		reqs = append(reqs, r)
	}
	return reqs, rows.Err()
}

// --- Mutes ---

// Mute adds a mute entry (muter_id mutes muted_id)
func (s *Store) Mute(ctx context.Context, muterID, mutedID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO graph.mutes (muter_id, muted_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		muterID, mutedID)
	return err
}

// Unmute removes a mute entry
func (s *Store) Unmute(ctx context.Context, muterID, mutedID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM graph.mutes WHERE muter_id = $1 AND muted_id = $2`,
		muterID, mutedID)
	return err
}

// GetBlockedAndMuted returns all user IDs that the given user has blocked OR muted
func (s *Store) GetBlockedAndMuted(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT blocked_id FROM blocks WHERE blocker_id = $1
		UNION
		SELECT muted_id FROM graph.mutes WHERE muter_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// GetRelationshipBatch returns relationships from viewerID to each of targetIDs (up to 100)
func (s *Store) GetRelationshipBatch(ctx context.Context, viewerID uuid.UUID, targetIDs []uuid.UUID) (map[uuid.UUID]Relationship, error) {
	result := make(map[uuid.UUID]Relationship, len(targetIDs))
	for _, id := range targetIDs {
		result[id] = Relationship{}
	}

	// Follows: viewer → targets
	rows, _ := s.db.Query(ctx,
		`SELECT followee_id FROM follows WHERE follower_id = $1 AND followee_id = ANY($2)`,
		viewerID, targetIDs)
	if rows != nil {
		for rows.Next() {
			var id uuid.UUID
			rows.Scan(&id)
			r := result[id]
			r.Follows = true
			result[id] = r
		}
		rows.Close()
	}

	// Followed by: targets → viewer
	rows, _ = s.db.Query(ctx,
		`SELECT follower_id FROM follows WHERE followee_id = $1 AND follower_id = ANY($2)`,
		viewerID, targetIDs)
	if rows != nil {
		for rows.Next() {
			var id uuid.UUID
			rows.Scan(&id)
			r := result[id]
			r.FollowedBy = true
			result[id] = r
		}
		rows.Close()
	}

	// Blocks: viewer → targets
	rows, _ = s.db.Query(ctx,
		`SELECT blocked_id FROM blocks WHERE blocker_id = $1 AND blocked_id = ANY($2)`,
		viewerID, targetIDs)
	if rows != nil {
		for rows.Next() {
			var id uuid.UUID
			rows.Scan(&id)
			r := result[id]
			r.Blocked = true
			result[id] = r
		}
		rows.Close()
	}

	// Mutes: viewer → targets
	rows, _ = s.db.Query(ctx,
		`SELECT muted_id FROM graph.mutes WHERE muter_id = $1 AND muted_id = ANY($2)`,
		viewerID, targetIDs)
	if rows != nil {
		for rows.Next() {
			var id uuid.UUID
			rows.Scan(&id)
			r := result[id]
			r.IsMuted = true
			result[id] = r
		}
		rows.Close()
	}

	return result, nil
}
