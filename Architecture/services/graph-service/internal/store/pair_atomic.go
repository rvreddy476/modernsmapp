package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/atpost/shared/events"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 3 M3-P0-6 / SR-2 — atomic pair mutations and durable safety events.
//
// THE DEFECT
//
// Block was a sequence of independent statements:
//
//	CreateBlock(...)                  // separate statement
//	DeleteFollow(blocked, blocker)    // separate statement
//	DeleteFollow(blocker, blocked)    // separate statement
//	store.RemoveConnection(...)       // separate statement, error DISCARDED
//	publishUserBlockedAsync(...)      // fire-and-forget goroutine
//
// Three consequences, all reachable in normal operation:
//
//  1. A concurrent follow could be inserted between CreateBlock and
//     DeleteFollow, leaving a live follow edge across a block. Feed and
//     search would then legitimately serve that content, because their
//     filters are correct — the graph was wrong.
//  2. A failure partway through left a block with some relationships still
//     standing, and the caller saw success.
//  3. The event could be lost entirely: a pod eviction or broker outage
//     during the goroutine meant chat never severed the conversation and
//     notifications never learned. Nothing retried, because nothing had
//     recorded that an event was owed.
//
// THE FIX, AND WHY THE FIRST ATTEMPT DID NOT WORK
//
// One transaction, taken under a deterministic per-pair advisory lock, that
// performs every removal and writes the outbox row before committing.
//
// The first version of this file took the lock in BlockAtomic ONLY. A mutual
// exclusion lock that one side does not take excludes nothing: the follow,
// connection-request and accept paths went straight to an unlocked INSERT, so
// the block still raced exactly the write it was supposed to serialize
// against. The negative control could not detect the lock's removal because
// the lock was never doing the work.
//
// So every relationship-CREATING path now runs through withPairLock and
// re-checks blocks AFTER the lock is held. The re-check is the point: a check
// performed before acquiring the lock is a TOCTOU read that a concurrent block
// invalidates before the insert lands.

// ErrBlockedPair is returned when a relationship cannot be created because a
// block exists in either direction. Blocks are SYMMETRIC for the purpose of
// creating a relationship: if A blocked B, B must not be able to follow,
// connect to, favourite or label A either. A one-way check leaves the blocked
// party able to re-establish a link the blocker explicitly severed.
var ErrBlockedPair = errors.New("relationship refused: a block exists between these users")

// canonicalPair orders two ids deterministically so both directions of a
// relationship map to the same lock and the same sequence row.
func canonicalPair(a, b uuid.UUID) (lo, hi uuid.UUID) {
	if a.String() <= b.String() {
		return a, b
	}
	return b, a
}

// pairLockKey derives ONE bigint for pg_advisory_xact_lock from the
// canonical pair.
//
// It must be the single-argument bigint overload, not the (int4, int4) one:
// PostgreSQL exposes both, and passing int64 values to the two-argument form
// fails at encode time with "greater than maximum value for int4". The live
// suite caught exactly that.
//
// The transaction-scoped variant releases on commit or rollback, so there is
// no leak path. Hash collisions merely make two unrelated pairs serialize,
// which is a throughput detail and never a correctness one.
func pairLockKey(a, b uuid.UUID) int64 {
	lo, hi := canonicalPair(a, b)
	var acc uint64 = 1469598103934665603 // FNV-1a offset basis
	mix := func(u uuid.UUID) {
		for _, x := range u {
			acc ^= uint64(x)
			acc *= 1099511628211
		}
	}
	mix(lo)
	mix(hi)
	return int64(acc & 0x7FFFFFFFFFFFFFFF)
}

// withPairLock runs fn inside one transaction holding the pair's advisory
// lock. EVERY relationship-creating path must go through here — a path that
// does not take the lock is not excluded by the ones that do.
func (s *Store) withPairLock(ctx context.Context, a, b uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pair tx: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pairLockKey(a, b)); err != nil {
		return fmt.Errorf("pair tx: lock: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("pair tx: commit: %w", err)
	}
	return nil
}

// blockedEitherWayTx is the post-lock symmetric block check.
//
// It MUST run after the advisory lock is held. The same query run before the
// lock is a TOCTOU read: a block committing between the read and the insert
// produces exactly the surviving-edge-across-a-block state this whole file
// exists to prevent.
func blockedEitherWayTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM blocks
			WHERE (blocker_id = $1 AND blocked_id = $2)
			   OR (blocker_id = $2 AND blocked_id = $1)
		)`, a, b).Scan(&exists)
	return exists, err
}

// guardPairTx is the common preamble for every relationship-creating path.
func guardPairTx(ctx context.Context, tx pgx.Tx, a, b uuid.UUID) error {
	if a == b {
		return fmt.Errorf("cannot create a relationship with self")
	}
	blocked, err := blockedEitherWayTx(ctx, tx, a, b)
	if err != nil {
		return fmt.Errorf("block check: %w", err)
	}
	if blocked {
		return ErrBlockedPair
	}
	return nil
}

// ── Relationship-creating paths, all under the pair lock ────────────────────

// FollowAtomic creates a follow edge under the pair lock, refusing if a block
// exists in either direction at the moment the lock is held.
//
// Returns whether a new row landed, so the caller can skip its counter bump on
// a duplicate.
func (s *Store) FollowAtomic(ctx context.Context, followerID, followeeID uuid.UUID) (bool, error) {
	var inserted bool
	err := s.withPairLock(ctx, followerID, followeeID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, followerID, followeeID); err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `
			INSERT INTO follows (follower_id, followee_id, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (follower_id, followee_id) DO NOTHING`, followerID, followeeID)
		if err != nil {
			return fmt.Errorf("follow: insert: %w", err)
		}
		inserted = ct.RowsAffected() > 0
		if inserted {
			// LB-2: canonical event type and payload — consumers switch on
			// events.UserFollowed, not a lowercase string.
			if _, err := appendGraphOutboxTx(ctx, tx, events.UserFollowed, followerID, followeeID,
				events.UserFollowedPayload{
					FollowerID: followerID.String(),
					FolloweeID: followeeID.String(),
					CreatedAt:  time.Now().UTC(),
				}); err != nil {
				return fmt.Errorf("follow: outbox: %w", err)
			}
		}
		return nil
	})
	return inserted, err
}

// SendConnectionRequestAtomic creates or revives a pending connection request
// under the pair lock.
func (s *Store) SendConnectionRequestAtomic(ctx context.Context, senderID, receiverID uuid.UUID, source, message string) error {
	if source == "" {
		source = "profile"
	}
	var msg *string
	if message != "" {
		msg = &message
	}
	now := time.Now()
	expiresAt := now.AddDate(0, 0, 30)

	return s.withPairLock(ctx, senderID, receiverID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, senderID, receiverID); err != nil {
			return err
		}
		// expires_at is computed in Go and passed as its own parameter.
		// Deriving it in SQL ($5 + INTERVAL '30 days') made Postgres deduce
		// conflicting types for $5 — it is also used as created_at/updated_at.
		_, err := tx.Exec(ctx, `
			INSERT INTO connection_requests (sender_id, receiver_id, status, source, message, created_at, updated_at, expires_at)
			VALUES ($1, $2, 'pending', $3, $4, $5, $5, $6)
			ON CONFLICT (sender_id, receiver_id) DO UPDATE
			SET status = 'pending', source = EXCLUDED.source, message = EXCLUDED.message,
			    created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at,
			    expires_at = EXCLUDED.expires_at, responded_at = NULL
			WHERE connection_requests.status IN ('declined', 'cancelled', 'expired')`,
			senderID, receiverID, source, msg, now, expiresAt)
		if err != nil {
			return fmt.Errorf("connection request: %w", err)
		}
		return nil
	})
}

// AcceptConnectionRequestAtomic accepts a pending request and creates the
// connection under the pair lock.
//
// This path mattered most: accepting a request is a relationship CREATION that
// happens long after the request was sent, so the block may have landed in
// between. Without the lock and the re-check, a block could be applied and the
// pending request accepted concurrently, leaving a live connection.
func (s *Store) AcceptConnectionRequestAtomic(ctx context.Context, senderID, receiverID uuid.UUID) error {
	return s.withPairLock(ctx, senderID, receiverID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, senderID, receiverID); err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `
			UPDATE connection_requests SET status = 'accepted', responded_at = NOW(), updated_at = NOW()
			WHERE sender_id = $1 AND receiver_id = $2 AND status = 'pending'`, senderID, receiverID)
		if err != nil {
			return fmt.Errorf("accept: update request: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return fmt.Errorf("no pending connection request found")
		}

		userA, userB := normalizePair(senderID, receiverID)
		if _, err := tx.Exec(ctx, `
			INSERT INTO connections (user_a, user_b, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (user_a, user_b) DO NOTHING`, userA, userB); err != nil {
			return fmt.Errorf("accept: insert connection: %w", err)
		}

		for _, uid := range []uuid.UUID{senderID, receiverID} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO counts (user_id, follower_count, following_count, friend_count, updated_at)
				VALUES ($1, 0, 0, 1, NOW())
				ON CONFLICT (user_id) DO UPDATE SET friend_count = counts.friend_count + 1, updated_at = NOW()`,
				uid); err != nil {
				return fmt.Errorf("accept: bump friend_count: %w", err)
			}
		}
		return nil
	})
}

// AddCloseFriendAtomic adds a Trusted Circle member under the pair lock.
// Close friends is an AUDIENCE: a surviving row after a block keeps the
// blocked person able to see `close_friends`-visibility content.
func (s *Store) AddCloseFriendAtomic(ctx context.Context, userID, friendID uuid.UUID, source string) error {
	return s.withPairLock(ctx, userID, friendID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, userID, friendID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO close_friends (user_id, friend_id, source) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
			userID, friendID, source)
		return err
	})
}

// AddFavoriteAtomic pins an account to the top of the owner's feed.
func (s *Store) AddFavoriteAtomic(ctx context.Context, userID, targetID uuid.UUID) error {
	return s.withPairLock(ctx, userID, targetID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, userID, targetID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO favorites (user_id, target_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			userID, targetID)
		return err
	})
}

// UpsertRelationshipLabelAtomic records a label ("family", "colleague", ...).
func (s *Store) UpsertRelationshipLabelAtomic(ctx context.Context, userID, targetID uuid.UUID, label string) error {
	return s.withPairLock(ctx, userID, targetID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, userID, targetID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO relationship_labels (user_id, target_id, label) VALUES ($1, $2, $3)
			 ON CONFLICT (user_id, target_id) DO UPDATE SET label = EXCLUDED.label`,
			userID, targetID, label)
		return err
	})
}

// AddCircleMemberAtomic adds a user to one of ownerID's circles. The pair
// under contention is (owner, member) — the circle is just the container, and
// a circle is an audience the same way close friends is.
func (s *Store) AddCircleMemberAtomic(ctx context.Context, circleID, ownerID, userID uuid.UUID) error {
	return s.withPairLock(ctx, ownerID, userID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, ownerID, userID); err != nil {
			return err
		}
		// Ownership is re-verified inside the lock so a circle deleted or
		// transferred concurrently cannot receive a member.
		var owns bool
		if err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM circles WHERE id = $1 AND owner_id = $2)`,
			circleID, ownerID).Scan(&owns); err != nil {
			return fmt.Errorf("circle member: ownership check: %w", err)
		}
		if !owns {
			return fmt.Errorf("circle not found")
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO circle_members (circle_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			circleID, userID)
		return err
	})
}

// ── Block ───────────────────────────────────────────────────────────────────

// BlockResult reports what the atomic block actually removed, so the caller
// can adjust counters and the test can assert on real effects.
type BlockResult struct {
	Created              bool
	RemovedFollowForward bool // blocker -> blocked
	RemovedFollowReverse bool // blocked -> blocker
	RemovedConnection    bool
	RemovedRequest       int
	RemovedFollowRequest int
	RemovedCloseFriend   int
	RemovedCircleMember  int
	RemovedFavorite      int
	RemovedLabel         int
	PairSeq              int64
}

// blockSweepTables are the relationship tables a block must sweep, with the
// real column names from database/migrations/004_social_graph_extensions.sql.
//
// SR-2: the previous version guessed `owner_id`/`member_id` for four of these
// tables. Every one of those statements would have failed with 42703
// (undefined column) at runtime, aborting the block transaction — meaning
// Block would have failed outright in any deployment where migration 004 had
// been applied. It appeared to pass only because the test database lacked
// those tables entirely, so the to_regclass probe skipped all four. That is
// why the live suite below now builds the COMPLETE schema and seeds every
// table: a sweep test that runs against absent tables asserts nothing.
//
//	close_friends       (user_id, friend_id)
//	circle_members      (circle_id, user_id) + circles(owner_id)
//	favorites           (user_id, target_id)
//	relationship_labels (user_id, target_id)
//	connections         (user_a, user_b)
//	connection_requests (sender_id, receiver_id)

// BlockAtomic creates the block and severs every relationship that could
// expose either user to the other, in ONE transaction, and records the
// safety event in the same transaction.
func (s *Store) BlockAtomic(ctx context.Context, blockerID, blockedID uuid.UUID) (BlockResult, error) {
	var res BlockResult
	if blockerID == blockedID {
		return res, fmt.Errorf("cannot block self")
	}

	err := s.withPairLock(ctx, blockerID, blockedID, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx, `
			INSERT INTO blocks (blocker_id, blocked_id, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (blocker_id, blocked_id) DO NOTHING`, blockerID, blockedID)
		if err != nil {
			return fmt.Errorf("block: insert: %w", err)
		}
		res.Created = ct.RowsAffected() > 0

		// Follows, both directions.
		if ct, err = tx.Exec(ctx,
			`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, blockerID, blockedID); err != nil {
			return fmt.Errorf("block: delete forward follow: %w", err)
		}
		res.RemovedFollowForward = ct.RowsAffected() > 0

		if ct, err = tx.Exec(ctx,
			`DELETE FROM follows WHERE follower_id = $1 AND followee_id = $2`, blockedID, blockerID); err != nil {
			return fmt.Errorf("block: delete reverse follow: %w", err)
		}
		res.RemovedFollowReverse = ct.RowsAffected() > 0

		// The remaining tables arrive in later migrations and are absent in
		// some deployments. Their absence must not fail the block, but ANY
		// other error must, because it would mean an unswept relationship.
		//
		// Detecting absence by catching 42P01 does NOT work inside a
		// transaction: the first failing statement aborts the whole
		// transaction and every subsequent statement returns 25P02. The live
		// suite proved this — the swallow "worked" and then the next delete
		// died. So the existence check happens up front with to_regclass,
		// which never errors, and nothing inside the transaction is allowed
		// to fail silently.
		present := map[string]bool{}
		for _, name := range []string{
			"close_friends", "circle_members", "circles", "favorites",
			"relationship_labels", "connections", "connection_requests",
			"follow_requests",
		} {
			var reg *string
			if err := tx.QueryRow(ctx, `SELECT to_regclass($1)::text`, "public."+name).Scan(&reg); err != nil {
				return fmt.Errorf("block: probe %s: %w", name, err)
			}
			present[name] = reg != nil
		}

		sweep := func(table, sql string, args ...any) (int, error) {
			if !present[table] {
				return 0, nil
			}
			ct, err := tx.Exec(ctx, sql, args...)
			if err != nil {
				return 0, err
			}
			return int(ct.RowsAffected()), nil
		}

		if n, err := sweep("connections", `
			DELETE FROM connections
			WHERE (user_a = $1 AND user_b = $2) OR (user_a = $2 AND user_b = $1)`,
			blockerID, blockedID); err != nil {
			return fmt.Errorf("block: remove connection: %w", err)
		} else if n > 0 {
			res.RemovedConnection = true
		}

		if n, err := sweep("connection_requests", `
			DELETE FROM connection_requests
			WHERE (sender_id = $1 AND receiver_id = $2) OR (sender_id = $2 AND receiver_id = $1)`,
			blockerID, blockedID); err != nil {
			return fmt.Errorf("block: remove connection requests: %w", err)
		} else {
			res.RemovedRequest = n
		}

		// Follow requests (private accounts): a pending request that survives
		// a block keeps a "Requested" affordance alive between two people the
		// block just severed, and an accept would recreate the edge. Removed
		// in both directions, same as connection_requests.
		if n, err := sweep("follow_requests", `
			DELETE FROM follow_requests
			WHERE (requester_id = $1 AND target_id = $2) OR (requester_id = $2 AND target_id = $1)`,
			blockerID, blockedID); err != nil {
			return fmt.Errorf("block: remove follow requests: %w", err)
		} else {
			res.RemovedFollowRequest = n
		}

		if n, err := sweep("close_friends", `
			DELETE FROM close_friends
			WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)`,
			blockerID, blockedID); err != nil {
			return fmt.Errorf("block: remove close friends: %w", err)
		} else {
			res.RemovedCloseFriend = n
		}

		if n, err := sweep("circle_members", `
			DELETE FROM circle_members
			WHERE (user_id = $2 AND circle_id IN (SELECT id FROM circles WHERE owner_id = $1))
			   OR (user_id = $1 AND circle_id IN (SELECT id FROM circles WHERE owner_id = $2))`,
			blockerID, blockedID); err != nil {
			return fmt.Errorf("block: remove circle members: %w", err)
		} else {
			res.RemovedCircleMember = n
		}

		if n, err := sweep("favorites", `
			DELETE FROM favorites
			WHERE (user_id = $1 AND target_id = $2) OR (user_id = $2 AND target_id = $1)`,
			blockerID, blockedID); err != nil {
			return fmt.Errorf("block: remove favorites: %w", err)
		} else {
			res.RemovedFavorite = n
		}

		if n, err := sweep("relationship_labels", `
			DELETE FROM relationship_labels
			WHERE (user_id = $1 AND target_id = $2) OR (user_id = $2 AND target_id = $1)`,
			blockerID, blockedID); err != nil {
			return fmt.Errorf("block: remove labels: %w", err)
		} else {
			res.RemovedLabel = n
		}

		// The safety event goes in the SAME transaction. If the commit fails
		// there is no block and no event; if it succeeds both exist. There is
		// no window in which a block is real but unannounced.
		//
		// LB-2, two corrections:
		//
		// 1. The CANONICAL event type and payload. This previously wrote the
		//    string "user.blocked" while every existing consumer switches on
		//    events.UserBlocked ("UserBlocked"). The relay published
		//    successfully and marked the row published, and every consumer
		//    ignored it — the durable path reported success at every step
		//    while delivering nothing.
		//
		// 2. ONE event per actual TRANSITION. This used to write a row
		//    unconditionally, so a repeated block of an already-blocked user
		//    announced a state change that had not happened. A consumer
		//    without dedupe applies the safety effect twice; one with dedupe
		//    still has to process and discard it. Nothing changed, so nothing
		//    is announced.
		//
		//    "Changed" includes a sweep that removed something: if a
		//    relationship slipped in after the original block, severing it now
		//    IS a transition downstream consumers need to hear about.
		changed := res.Created ||
			res.RemovedFollowForward || res.RemovedFollowReverse || res.RemovedConnection ||
			res.RemovedRequest > 0 || res.RemovedFollowRequest > 0 || res.RemovedCloseFriend > 0 ||
			res.RemovedCircleMember > 0 || res.RemovedFavorite > 0 || res.RemovedLabel > 0
		if !changed {
			return nil
		}

		seq, err := appendGraphOutboxTx(ctx, tx, events.UserBlocked, blockerID, blockedID,
			events.UserBlockedPayload{
				BlockerID: blockerID.String(),
				BlockedID: blockedID.String(),
				BlockedAt: time.Now().UTC(),
			})
		if err != nil {
			return fmt.Errorf("block: outbox: %w", err)
		}
		res.PairSeq = seq
		return nil
	})
	return res, err
}

// UnblockAtomic removes the block and records the event durably. Chat severs
// conversations on block and needs the matching unblock signal; publishing it
// from a goroutine meant the sever could be permanent if the process died.
func (s *Store) UnblockAtomic(ctx context.Context, blockerID, blockedID uuid.UUID) error {
	return s.withPairLock(ctx, blockerID, blockedID, func(tx pgx.Tx) error {
		ct, err := tx.Exec(ctx,
			`DELETE FROM blocks WHERE blocker_id = $1 AND blocked_id = $2`, blockerID, blockedID)
		if err != nil {
			return fmt.Errorf("unblock: delete: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return nil // idempotent: nothing was blocked, nothing to announce
		}
		// LB-2: canonical event type and payload.
		if _, err := appendGraphOutboxTx(ctx, tx, events.UserUnblocked, blockerID, blockedID,
			events.UserUnblockedPayload{
				BlockerID:   blockerID.String(),
				BlockedID:   blockedID.String(),
				UnblockedAt: time.Now().UTC(),
			}); err != nil {
			return fmt.Errorf("unblock: outbox: %w", err)
		}
		return nil
	})
}

// appendGraphOutboxTx writes one outbox row and bumps the per-pair
// sequence, inside the caller's transaction.
//
// LB-2: `payload` is now a TYPED shared payload struct
// (events.UserBlockedPayload and friends) rather than a map. The map allowed
// this file to invent field names and event types that no consumer read —
// which is exactly what happened: it wrote "user.blocked" while every consumer
// switched on events.UserBlocked. Taking the shared type makes a mismatch a
// compile error instead of a silent delivery failure.
func appendGraphOutboxTx(ctx context.Context, tx pgx.Tx, eventType string,
	actorID, targetID uuid.UUID, payload any) (int64, error) {

	lo, hi := canonicalPair(actorID, targetID)
	var seq int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO graph_pair_seq (lo_id, hi_id, seq)
		VALUES ($1, $2, 1)
		ON CONFLICT (lo_id, hi_id) DO UPDATE SET seq = graph_pair_seq.seq + 1
		RETURNING seq`, lo, hi).Scan(&seq); err != nil {
		return 0, fmt.Errorf("pair seq: %w", err)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal payload: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO graph_outbox_events
			(event_type, actor_id, target_id, pair_seq, payload)
		VALUES ($1, $2, $3, $4, $5)`,
		eventType, actorID, targetID, seq, raw); err != nil {
		return 0, fmt.Errorf("insert outbox: %w", err)
	}
	return seq, nil
}
