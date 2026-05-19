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

type ConnectionRequest struct {
	SenderID   uuid.UUID `json:"sender_id"`
	ReceiverID uuid.UUID `json:"receiver_id"`
	Status     string    `json:"status"`
	Source     string    `json:"source"`
	Message    *string   `json:"message,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	ExpiresAt  time.Time `json:"expires_at"`
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
//
// Audit HG5: previously the count increments fired unconditionally
// even when ON CONFLICT DO NOTHING skipped the insert, so concurrent
// duplicate follows drifted the counters upward forever (reconciler
// only ran hourly). Now the increments are gated on the insert's
// RowsAffected — duplicate calls are no-ops on both sides.
func (s *Store) CreateFollow(ctx context.Context, followerID, followeeID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	cmdTag, err := tx.Exec(ctx, `
		INSERT INTO follows (follower_id, followee_id, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (follower_id, followee_id) DO NOTHING
	`, followerID, followeeID)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		// Duplicate follow — return success without touching counts.
		return tx.Commit(ctx)
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

// --- Connections ---

// normalizePair ensures a < b (lexicographic UUID ordering).
func normalizePair(a, b uuid.UUID) (uuid.UUID, uuid.UUID) {
	if a.String() < b.String() {
		return a, b
	}
	return b, a
}

// SendConnectionRequest creates a pending connection request, or re-opens a
// previously declined/cancelled/expired one for the same pair.
func (s *Store) SendConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID, source, message string) error {
	if source == "" {
		source = "profile"
	}
	var msg *string
	if message != "" {
		msg = &message
	}
	// expires_at is computed in Go and passed as its own parameter.
	// Deriving it in SQL ($5 + INTERVAL '30 days') made Postgres deduce
	// conflicting types for $5 — it is also used as created_at/updated_at.
	now := time.Now()
	expiresAt := now.AddDate(0, 0, 30)
	_, err := s.db.Exec(ctx, `
		INSERT INTO connection_requests (sender_id, receiver_id, status, source, message, created_at, updated_at, expires_at)
		VALUES ($1, $2, 'pending', $3, $4, $5, $5, $6)
		ON CONFLICT (sender_id, receiver_id) DO UPDATE
		SET status = 'pending', source = EXCLUDED.source, message = EXCLUDED.message,
		    created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at,
		    expires_at = EXCLUDED.expires_at, responded_at = NULL
		WHERE connection_requests.status IN ('declined', 'cancelled', 'expired')
	`, senderID, receiverID, source, msg, now, expiresAt)
	return err
}

// AcceptConnectionRequest accepts a pending request and creates the connection.
func (s *Store) AcceptConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// 1. Update request status
	cmdTag, err := tx.Exec(ctx, `
		UPDATE connection_requests SET status = 'accepted', responded_at = NOW(), updated_at = NOW()
		WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'
	`, senderID, receiverID)
	if err != nil {
		return err
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("no pending connection request found")
	}

	// 2. Insert into connections table (normalized order)
	userA, userB := normalizePair(senderID, receiverID)
	_, err = tx.Exec(ctx, `
		INSERT INTO connections (user_a, user_b, created_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (user_a, user_b) DO NOTHING
	`, userA, userB)
	if err != nil {
		return err
	}

	// 3. Increment connection count for both users
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

// DeclineConnectionRequest declines a pending request (receiver action).
func (s *Store) DeclineConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE connection_requests SET status = 'declined', responded_at = NOW(), updated_at = NOW()
		WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'
	`, senderID, receiverID)
	return err
}

// CancelConnectionRequest lets the sender withdraw their own pending request.
// Returns true when a pending request was actually cancelled.
func (s *Store) CancelConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) (bool, error) {
	cmdTag, err := s.db.Exec(ctx, `
		UPDATE connection_requests SET status = 'cancelled', responded_at = NOW(), updated_at = NOW()
		WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'
	`, senderID, receiverID)
	if err != nil {
		return false, err
	}
	return cmdTag.RowsAffected() > 0, nil
}

// ExpireStaleConnectionRequests flips pending requests past expires_at to
// 'expired'. Returns the number of rows expired. Driven by the sweeper.
func (s *Store) ExpireStaleConnectionRequests(ctx context.Context) (int64, error) {
	cmdTag, err := s.db.Exec(ctx, `
		UPDATE connection_requests SET status = 'expired', updated_at = NOW()
		WHERE status = 'pending' AND expires_at < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return cmdTag.RowsAffected(), nil
}

// RemoveConnection removes a connection and decrements counts.
func (s *Store) RemoveConnection(ctx context.Context, userA, userB uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	nA, nB := normalizePair(userA, userB)
	cmdTag, err := tx.Exec(ctx, `
		DELETE FROM connections WHERE user_a = $1 AND user_b = $2
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

		// Clean up the connection_requests row
		_, _ = tx.Exec(ctx, `DELETE FROM connection_requests WHERE sender_id = $1 AND receiver_id = $2`, userA, userB)
		_, _ = tx.Exec(ctx, `DELETE FROM connection_requests WHERE sender_id = $1 AND receiver_id = $2`, userB, userA)

		// Cascade: a removed connection can no longer be a close friend
		// (friends-sheets spec §10). Both directions, same transaction.
		_, _ = tx.Exec(ctx,
			`DELETE FROM close_friends WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)`,
			userA, userB)
	}

	return tx.Commit(ctx)
}

// CheckConnection returns true if userA and userB are connected.
func (s *Store) CheckConnection(ctx context.Context, userA, userB uuid.UUID) (bool, error) {
	nA, nB := normalizePair(userA, userB)
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM connections WHERE user_a = $1 AND user_b = $2)
	`, nA, nB).Scan(&exists)
	return exists, err
}

// GetConnectionRequestStatus returns the request status between actor and
// target: "none", "pending_sent", "pending_received", or a terminal status.
func (s *Store) GetConnectionRequestStatus(ctx context.Context, actorID, targetID uuid.UUID) (string, error) {
	// Check actor → target
	var status string
	err := s.db.QueryRow(ctx, `
		SELECT status FROM connection_requests
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
		SELECT status FROM connection_requests
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

// GetConnections returns a paginated list of connection user IDs.
func (s *Store) GetConnections(ctx context.Context, userID uuid.UUID, limit, offset int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT CASE WHEN user_a = $1 THEN user_b ELSE user_a END AS connection_id
		FROM connections
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

// GetPendingConnectionRequests returns pending requests received by the user
// that are NOT auto-filtered — i.e. the visible "main inbox" queue. Filtered
// requests are retrieved separately via GetFilteredConnectionRequests.
func (s *Store) GetPendingConnectionRequests(ctx context.Context, userID uuid.UUID) ([]ConnectionRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT sender_id, receiver_id, status, source, message, created_at, updated_at, expires_at
		FROM connection_requests
		WHERE receiver_id = $1 AND status = 'pending' AND is_filtered = FALSE
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reqs []ConnectionRequest
	for rows.Next() {
		var r ConnectionRequest
		if err := rows.Scan(&r.SenderID, &r.ReceiverID, &r.Status, &r.Source, &r.Message, &r.CreatedAt, &r.UpdatedAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		reqs = append(reqs, r)
	}
	return reqs, rows.Err()
}

// GetFilteredConnectionRequests returns pending requests received by the user
// that trust-safety-service has auto-filtered as abusive — the hidden queue.
func (s *Store) GetFilteredConnectionRequests(ctx context.Context, receiverID uuid.UUID) ([]ConnectionRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT sender_id, receiver_id, status, source, message, created_at, updated_at, expires_at
		FROM connection_requests
		WHERE receiver_id = $1 AND status = 'pending' AND is_filtered = TRUE
		ORDER BY filtered_at DESC
	`, receiverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reqs []ConnectionRequest
	for rows.Next() {
		var r ConnectionRequest
		if err := rows.Scan(&r.SenderID, &r.ReceiverID, &r.Status, &r.Source, &r.Message, &r.CreatedAt, &r.UpdatedAt, &r.ExpiresAt); err != nil {
			return nil, err
		}
		reqs = append(reqs, r)
	}
	return reqs, rows.Err()
}

// SetRequestFiltered moves a pending request into the recipient's hidden queue.
// Driven by trust-safety-service auto-scoring.
func (s *Store) SetRequestFiltered(ctx context.Context, senderID, receiverID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE connection_requests
		SET is_filtered = TRUE, filtered_at = NOW(), updated_at = NOW()
		WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'
	`, senderID, receiverID)
	return err
}

// UnfilterConnectionRequest moves a pending request back out of the hidden
// queue into the recipient's visible inbox.
func (s *Store) UnfilterConnectionRequest(ctx context.Context, senderID, receiverID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE connection_requests
		SET is_filtered = FALSE, filtered_at = NULL, updated_at = NOW()
		WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'
	`, senderID, receiverID)
	return err
}

// GetSentConnectionRequests returns the pending requests the user has sent
// (outgoing). Powers the "sent requests" UI; spec §9.2.
func (s *Store) GetSentConnectionRequests(ctx context.Context, userID uuid.UUID) ([]ConnectionRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT sender_id, receiver_id, status, source, message, created_at, updated_at, expires_at
		FROM connection_requests
		WHERE sender_id = $1 AND status = 'pending'
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reqs []ConnectionRequest
	for rows.Next() {
		var r ConnectionRequest
		if err := rows.Scan(&r.SenderID, &r.ReceiverID, &r.Status, &r.Source, &r.Message, &r.CreatedAt, &r.UpdatedAt, &r.ExpiresAt); err != nil {
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

// CheckMute returns true when muterID has muted mutedID.
//
// Audit CG1: previously absent, so service.GetRelationship returned
// IsMuted=false even when the row existed in graph.mutes — feed-service
// (and any client that gates UI on this flag) silently broke for muted
// pairs.
func (s *Store) CheckMute(ctx context.Context, muterID, mutedID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM graph.mutes WHERE muter_id = $1 AND muted_id = $2)`,
		muterID, mutedID).Scan(&exists)
	return exists, err
}

// RelationshipFull is the consolidated relationship snapshot returned
// by GetRelationshipFull. It carries everything service.GetRelationship
// needs in a single DB round trip.
type RelationshipFull struct {
	Follows                  bool
	FollowedBy               bool
	Blocked                  bool
	IsMuted                  bool
	IsConnection             bool
	ConnectionRequestSent     bool // actor → target row exists with status='pending'
	ConnectionRequestReceived bool // target → actor row exists with status='pending'
}

// GetRelationshipFull collapses CheckFollow (×2) + CheckBlock + CheckMute
// + CheckConnection + GetConnectionRequestStatus (×2) into a single
// EXISTS-based query. Audit HG1: the previous service-layer code did
// up to 6 sequential pg round trips per /v1/graph/relationship hit,
// which is the dominant cost of the feed-hydration profile bar.
func (s *Store) GetRelationshipFull(ctx context.Context, actorID, targetID uuid.UUID) (*RelationshipFull, error) {
	connA, connB := normalizePair(actorID, targetID)
	row := s.db.QueryRow(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM follows WHERE follower_id = $1 AND followee_id = $2),
			EXISTS(SELECT 1 FROM follows WHERE follower_id = $2 AND followee_id = $1),
			EXISTS(SELECT 1 FROM blocks  WHERE blocker_id  = $2 AND blocked_id  = $1),
			EXISTS(SELECT 1 FROM graph.mutes WHERE muter_id = $1 AND muted_id = $2),
			EXISTS(SELECT 1 FROM connections WHERE user_a = $3 AND user_b = $4),
			EXISTS(SELECT 1 FROM connection_requests WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'),
			EXISTS(SELECT 1 FROM connection_requests WHERE sender_id = $2 AND receiver_id = $1 AND status = 'pending')
	`, actorID, targetID, connA, connB)

	var r RelationshipFull
	if err := row.Scan(
		&r.Follows,
		&r.FollowedBy,
		&r.Blocked,
		&r.IsMuted,
		&r.IsConnection,
		&r.ConnectionRequestSent,
		&r.ConnectionRequestReceived,
	); err != nil {
		return nil, err
	}
	return &r, nil
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

// ═══════════════════════════════════════════════════════════
// Close Friends
// ═══════════════════════════════════════════════════════════

func (s *Store) AddCloseFriend(ctx context.Context, userID, friendID uuid.UUID, source string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO close_friends (user_id, friend_id, source) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		userID, friendID, source)
	return err
}

func (s *Store) RemoveCloseFriend(ctx context.Context, userID, friendID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM close_friends WHERE user_id = $1 AND friend_id = $2`,
		userID, friendID)
	return err
}

func (s *Store) GetCloseFriends(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT friend_id FROM close_friends WHERE user_id = $1 ORDER BY added_at DESC`,
		userID)
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

func (s *Store) IsCloseFriend(ctx context.Context, userID, friendID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM close_friends WHERE user_id = $1 AND friend_id = $2)`,
		userID, friendID).Scan(&exists)
	return exists, err
}

// CountCloseFriends returns how many members are in userID's Trusted Circle.
func (s *Store) CountCloseFriends(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM close_friends WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// ═══════════════════════════════════════════════════════════
// Circles
// ═══════════════════════════════════════════════════════════

type Circle struct {
	ID        uuid.UUID `json:"id"`
	OwnerID   uuid.UUID `json:"owner_id"`
	Name      string    `json:"name"`
	Emoji     *string   `json:"emoji,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) CreateCircle(ctx context.Context, ownerID uuid.UUID, name string, emoji *string) (*Circle, error) {
	c := &Circle{}
	err := s.db.QueryRow(ctx,
		`INSERT INTO circles (owner_id, name, emoji) VALUES ($1, $2, $3)
		 RETURNING id, owner_id, name, emoji, created_at, updated_at`,
		ownerID, name, emoji).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Emoji, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Store) GetCircle(ctx context.Context, circleID, ownerID uuid.UUID) (*Circle, error) {
	c := &Circle{}
	err := s.db.QueryRow(ctx,
		`SELECT id, owner_id, name, emoji, created_at, updated_at FROM circles WHERE id = $1 AND owner_id = $2`,
		circleID, ownerID).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Emoji, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (s *Store) UpdateCircle(ctx context.Context, circleID, ownerID uuid.UUID, name string, emoji *string) (*Circle, error) {
	c := &Circle{}
	err := s.db.QueryRow(ctx,
		`UPDATE circles SET name=$3, emoji=$4, updated_at=NOW() WHERE id=$1 AND owner_id=$2
		 RETURNING id, owner_id, name, emoji, created_at, updated_at`,
		circleID, ownerID, name, emoji).
		Scan(&c.ID, &c.OwnerID, &c.Name, &c.Emoji, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

func (s *Store) DeleteCircle(ctx context.Context, circleID, ownerID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM circles WHERE id = $1 AND owner_id = $2`,
		circleID, ownerID)
	return err
}

func (s *Store) ListCircles(ctx context.Context, ownerID uuid.UUID) ([]Circle, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, owner_id, name, emoji, created_at, updated_at FROM circles WHERE owner_id = $1 ORDER BY created_at DESC`,
		ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var circles []Circle
	for rows.Next() {
		var c Circle
		if err := rows.Scan(&c.ID, &c.OwnerID, &c.Name, &c.Emoji, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		circles = append(circles, c)
	}
	return circles, rows.Err()
}

func (s *Store) AddCircleMember(ctx context.Context, circleID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO circle_members (circle_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		circleID, userID)
	return err
}

func (s *Store) RemoveCircleMember(ctx context.Context, circleID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM circle_members WHERE circle_id = $1 AND user_id = $2`,
		circleID, userID)
	return err
}

func (s *Store) GetCircleMembers(ctx context.Context, circleID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT user_id FROM circle_members WHERE circle_id = $1 ORDER BY added_at DESC`,
		circleID)
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

// ═══════════════════════════════════════════════════════════
// Relationship Labels
// ═══════════════════════════════════════════════════════════

type RelationshipLabel struct {
	UserID    uuid.UUID `json:"user_id"`
	TargetID  uuid.UUID `json:"target_id"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) UpsertRelationshipLabel(ctx context.Context, userID, targetID uuid.UUID, label string) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO relationship_labels (user_id, target_id, label) VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, target_id) DO UPDATE SET label = EXCLUDED.label`,
		userID, targetID, label)
	return err
}

func (s *Store) DeleteRelationshipLabel(ctx context.Context, userID, targetID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM relationship_labels WHERE user_id = $1 AND target_id = $2`,
		userID, targetID)
	return err
}

func (s *Store) ListRelationshipLabels(ctx context.Context, userID uuid.UUID) ([]RelationshipLabel, error) {
	rows, err := s.db.Query(ctx,
		`SELECT user_id, target_id, label, created_at FROM relationship_labels WHERE user_id = $1 ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var labels []RelationshipLabel
	for rows.Next() {
		var l RelationshipLabel
		if err := rows.Scan(&l.UserID, &l.TargetID, &l.Label, &l.CreatedAt); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}

// ═══════════════════════════════════════════════════════════
// Favorites
// ═══════════════════════════════════════════════════════════

func (s *Store) AddFavorite(ctx context.Context, userID, targetID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO favorites (user_id, target_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, targetID)
	return err
}

func (s *Store) RemoveFavorite(ctx context.Context, userID, targetID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM favorites WHERE user_id = $1 AND target_id = $2`,
		userID, targetID)
	return err
}

func (s *Store) GetFavorites(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT target_id FROM favorites WHERE user_id = $1 ORDER BY added_at DESC`,
		userID)
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
