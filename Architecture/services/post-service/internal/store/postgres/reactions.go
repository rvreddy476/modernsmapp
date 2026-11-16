package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Reaction represents a user's reaction to a target (post, video, comment, story).
type Reaction struct {
	ID           uuid.UUID `json:"id"`
	TargetType   string    `json:"target_type"`
	TargetID     uuid.UUID `json:"target_id"`
	UserID       uuid.UUID `json:"user_id"`
	ReactionType string    `json:"reaction_type"`
	CreatedAt    time.Time `json:"created_at"`
}

// ReactionCounts holds counts by reaction type for a target.
type ReactionCounts struct {
	Like  int64 `json:"like"`
	Love  int64 `json:"love"`
	Haha  int64 `json:"haha"`
	Wow   int64 `json:"wow"`
	Sad   int64 `json:"sad"`
	Angry int64 `json:"angry"`
	Total int64 `json:"total"`
}

// ValidReactionTypes lists the allowed reaction type values.
var ValidReactionTypes = map[string]bool{
	"like": true, "love": true, "haha": true,
	"wow": true, "sad": true, "angry": true,
}

// ReactionToggle is the outcome of ToggleReaction.
type ReactionToggle struct {
	// NewType is the reaction now on the target ("" when it was removed).
	NewType string
	// PrevType is the reaction that was there before ("" when there was none).
	PrevType string
	// IsSet reports whether the viewer has a reaction after the toggle.
	IsSet bool
}

// ReactionEventFunc builds the outbox event for a toggle, or returns ok =
// false for none. It runs inside the toggle's transaction.
type ReactionEventFunc func(t ReactionToggle) (eventType string, payload interface{}, ok bool)

// ReactionMirrorFunc copies a toggle's outcome to another store (the Scylla
// row feed hydration reads). It runs inside the toggle's transaction, after
// the reaction row is written and before the commit, while the viewer's
// lock on the target is still held; an error rolls the toggle back.
type ReactionMirrorFunc func(ctx context.Context, t ReactionToggle) error

const (
	// reactionLockSQL serialises one viewer's toggles on one target for the
	// length of the transaction. A row lock cannot do it: the first tap has
	// no row to lock.
	reactionLockSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`
	reactionReadSQL = `
		SELECT reaction_type FROM reactions
		WHERE target_type = $1 AND target_id = $2 AND user_id = $3
	`
	reactionDeleteSQL = `
		DELETE FROM reactions
		WHERE target_type = $1 AND target_id = $2 AND user_id = $3
	`
	reactionUpdateSQL = `
		UPDATE reactions SET reaction_type = $4, created_at = $5
		WHERE target_type = $1 AND target_id = $2 AND user_id = $3
	`
	// ON CONFLICT DO NOTHING: a like path that does not take the lock (the
	// liked list's upsert, liked_reels.go) may have written the row since
	// the read. The insert then reports no row, and the toggle settles on
	// what is stored instead of failing the request on the unique index.
	reactionInsertSQL = `
		INSERT INTO reactions (id, target_type, target_id, user_id, reaction_type, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (target_type, target_id, user_id) DO NOTHING
	`
)

// reactionLockKey is the advisory lock key of one viewer's reaction on one
// target (reactionLockSQL hashes it).
func reactionLockKey(targetType string, targetID, userID uuid.UUID) string {
	return "reactions:" + targetType + ":" + targetID.String() + ":" + userID.String()
}

// ToggleReaction is ToggleReactionMirrored with nothing to mirror.
func (s *Store) ToggleReaction(ctx context.Context, targetType string, targetID, userID uuid.UUID, reactionType string, event ReactionEventFunc) (ReactionToggle, error) {
	return s.ToggleReactionMirrored(ctx, targetType, targetID, userID, reactionType, event, nil)
}

/*
ToggleReactionMirrored inserts, changes or removes a viewer's reaction on
a target. The same type again removes it (toggle off); a different type
replaces it.

One viewer's toggles on one target run one at a time (feed-30). This used
to read the row with no lock and write from what it read, so two taps in
flight together both saw "no reaction" (one insert then failed on the
unique index: a 500) or both saw "liked" (both deleted, and BOTH wrote a
PostUnreacted event, so feed-service's velocity counter came down twice
for one removed like; fifty requests drained it by fifty). Now each toggle
takes an advisory lock on (target, viewer) first, so the read, the write,
the event and the mirror of one tap all happen before the next tap reads.

The outcome is what the write did, not what the read expected: when the
planned statement touched no row (a writer that does not take this lock
changed it in between), the toggle settles on the stored reaction and
announces nothing (settleReactionToggle).

event, when non-nil, is asked for the outbox event of the outcome, and the
event is written in the SAME transaction as the reaction row. mirror, when
non-nil, then copies the outcome to the store feed hydration reads, still
under the lock, so two taps' copies are applied in the order their rows
were written; it used to run after the commit in no fixed order, and a
late "like" copy could land after the "unlike" copy and leave the feed
card liked with the row gone. If the mirror fails the toggle is rolled
back and the caller gets the error to retry on.
*/
func (s *Store) ToggleReactionMirrored(ctx context.Context, targetType string, targetID, userID uuid.UUID, reactionType string, event ReactionEventFunc, mirror ReactionMirrorFunc) (ReactionToggle, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ReactionToggle{}, err
	}
	defer tx.Rollback(ctx)

	result, err := toggleReactionTx(ctx, tx, targetType, targetID, userID, reactionType, event, mirror)
	if err != nil {
		return ReactionToggle{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReactionToggle{}, err
	}
	return result, nil
}

// toggleReactionTx is ToggleReactionMirrored inside the caller's transaction:
// the lock, the read, the write, the event and the mirror, in that order.
// The caller commits on nil and rolls back on an error.
func toggleReactionTx(ctx context.Context, tx pgx.Tx, targetType string, targetID, userID uuid.UUID, reactionType string, event ReactionEventFunc, mirror ReactionMirrorFunc) (ReactionToggle, error) {
	if _, err := tx.Exec(ctx, reactionLockSQL, reactionLockKey(targetType, targetID, userID)); err != nil { // feed-30
		return ReactionToggle{}, fmt.Errorf("lock reaction: %w", err)
	}

	existingType, err := readReactionTx(ctx, tx, targetType, targetID, userID)
	if err != nil {
		return ReactionToggle{}, err
	}

	result := nextReactionToggle(targetType, existingType, reactionType)
	var tag pgconn.CommandTag
	switch {
	case !result.IsSet:
		// Toggle off (remove)
		tag, err = tx.Exec(ctx, reactionDeleteSQL, targetType, targetID, userID)
	case existingType != "":
		// Different type — update
		tag, err = tx.Exec(ctx, reactionUpdateSQL, targetType, targetID, userID, reactionType, time.Now())
	default:
		// No existing — insert
		tag, err = tx.Exec(ctx, reactionInsertSQL, uuid.New(), targetType, targetID, userID, reactionType, time.Now())
	}
	if err != nil {
		return ReactionToggle{}, err
	}
	if tag.RowsAffected() == 0 {
		// The row is not what the read saw. Settle on what is stored now.
		current, err := readReactionTx(ctx, tx, targetType, targetID, userID)
		if err != nil {
			return ReactionToggle{}, err
		}
		result = settleReactionToggle(current)
	}

	if event != nil {
		if eventType, payload, ok := event(result); ok {
			if err := InsertOutboxEventTx(ctx, tx, eventType, targetType, targetID, payload); err != nil {
				return ReactionToggle{}, fmt.Errorf("emit %s: %w", eventType, err)
			}
		}
	}
	if mirror != nil {
		if err := mirror(ctx, result); err != nil {
			return ReactionToggle{}, fmt.Errorf("mirror reaction: %w", err)
		}
	}
	return result, nil
}

// readReactionTx is the viewer's stored reaction type ("" for none).
func readReactionTx(ctx context.Context, tx pgx.Tx, targetType string, targetID, userID uuid.UUID) (string, error) {
	var reactionType string
	err := tx.QueryRow(ctx, reactionReadSQL, targetType, targetID, userID).Scan(&reactionType)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	return reactionType, nil
}

// nextReactionToggle decides what a toggle does. The same type toggles off;
// a different type switches; none adds. A post takes the heart only
// (service/heart_only.go, 24 Oct 2026), so on a post ANY existing reaction
// toggles off: a viewer whose old emoji reads as the heart un-hearts on the
// next tap instead of being switched to "like" and left hearted.
func nextReactionToggle(targetType, existingType, reactionType string) ReactionToggle {
	switch {
	case existingType == "":
		return ReactionToggle{NewType: reactionType, IsSet: true}
	case existingType == reactionType, targetType == "post":
		return ReactionToggle{PrevType: existingType}
	}
	return ReactionToggle{NewType: reactionType, PrevType: existingType, IsSet: true}
}

// settleReactionToggle is the outcome of a toggle whose write touched no
// row: this toggle neither added nor removed a reaction, so the viewer's
// state is whatever is stored now (current, "" for none) and was the same
// before, which is what makes the caller's event builder announce nothing.
func settleReactionToggle(current string) ReactionToggle {
	return ReactionToggle{NewType: current, PrevType: current, IsSet: current != ""}
}

// GetReactionCounts returns reaction counts by type for a target.
func (s *Store) GetReactionCounts(ctx context.Context, targetType string, targetID uuid.UUID) (*ReactionCounts, error) {
	rows, err := s.db.Query(ctx, `
		SELECT reaction_type, COUNT(*) FROM reactions
		WHERE target_type = $1 AND target_id = $2
		GROUP BY reaction_type
	`, targetType, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := &ReactionCounts{}
	for rows.Next() {
		var rtype string
		var count int64
		if err := rows.Scan(&rtype, &count); err != nil {
			return nil, err
		}
		counts.Total += count
		switch rtype {
		case "like":
			counts.Like = count
		case "love":
			counts.Love = count
		case "haha":
			counts.Haha = count
		case "wow":
			counts.Wow = count
		case "sad":
			counts.Sad = count
		case "angry":
			counts.Angry = count
		}
	}

	return counts, rows.Err()
}

// GetUserReaction returns the user's current reaction type on a target ("" if none).
func (s *Store) GetUserReaction(ctx context.Context, targetType string, targetID, userID uuid.UUID) (string, error) {
	var reactionType string
	err := s.db.QueryRow(ctx, `
		SELECT reaction_type FROM reactions
		WHERE target_type = $1 AND target_id = $2 AND user_id = $3
	`, targetType, targetID, userID).Scan(&reactionType)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return reactionType, err
}
