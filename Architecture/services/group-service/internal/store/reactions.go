package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Emoji reactions on group posts — one current reaction per viewer per post.

	THE ROW IS THE SPARK ROW. group_post_sparks is unique on (post_id, user_id),
	which is exactly "one reaction per viewer per post", so the reaction is a
	column on that row (migration 016) and there is no second table that can
	disagree with the first. Every legacy heart is a 'like' by default.

	TWO COUNTS, TWO MEANINGS, NEITHER DOUBLE-COUNTED.
	  - group_posts.spark_count is the LEGACY heart total the mobile app reads:
	    one per row, except a supernova counts 5. A Love and a Like both move
	    it by one; replacing one with the other does not move it at all.
	  - reaction_counts is per-reaction and computed from the rows at read
	    time (GetGroupPostReactionCounts). It is never stored, so it cannot
	    drift from the rows.

	ATOMICITY. Every write is one transaction that locks the viewer's row
	(SELECT … FOR UPDATE), decides, writes, and moves spark_count only when a
	row was created or removed. Two concurrent first reactions race on the
	INSERT: ON CONFLICT DO NOTHING makes the loser wait for the winner's
	commit and then take the "row exists" path, so the counter moves once.
*/

// ReactionChange reports what SetGroupPostReaction did, so the service can
// decide whether an event and member stats are due (only on Inserted).
type ReactionChange struct {
	Previous string // "" when the viewer had no reaction before
	Current  string
	Inserted bool // a new row: spark_count moved by one
	Replaced bool // the row's reaction changed: spark_count did not move
}

// SetGroupPostReaction sets the viewer's reaction on a post, replacing any
// previous one atomically. Retrying the same reaction is a no-op. The
// reaction must already be validated against the allowlist; the CHECK
// constraint is the last line of defence, not the first.
func (s *Store) SetGroupPostReaction(ctx context.Context, postID uuid.UUID, userID, reaction string) (ReactionChange, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return ReactionChange{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	var prev string
	err = tx.QueryRow(ctx,
		`SELECT reaction FROM group_post_sparks WHERE post_id = $1 AND user_id = $2 FOR UPDATE`,
		postID, userID).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		tag, err := tx.Exec(ctx,
			`INSERT INTO group_post_sparks (post_id, user_id, is_supernova, reaction, created_at, updated_at)
			 VALUES ($1, $2, FALSE, $3, NOW(), NOW())
			 ON CONFLICT (post_id, user_id) DO NOTHING`,
			postID, userID, reaction)
		if err != nil {
			return ReactionChange{}, err
		}
		if tag.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx,
				`UPDATE group_posts SET spark_count = spark_count + 1, updated_at = NOW() WHERE id = $1`,
				postID); err != nil {
				return ReactionChange{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return ReactionChange{}, err
			}
			return ReactionChange{Current: reaction, Inserted: true}, nil
		}
		// Lost the race to a concurrent first reaction: the row exists now.
		// Re-lock it and continue on the replace/no-op path.
		if err := tx.QueryRow(ctx,
			`SELECT reaction FROM group_post_sparks WHERE post_id = $1 AND user_id = $2 FOR UPDATE`,
			postID, userID).Scan(&prev); err != nil {
			return ReactionChange{}, err
		}
	} else if err != nil {
		return ReactionChange{}, err
	}

	if prev == reaction {
		if err := tx.Commit(ctx); err != nil {
			return ReactionChange{}, err
		}
		return ReactionChange{Previous: prev, Current: reaction}, nil
	}
	if _, err := tx.Exec(ctx,
		`UPDATE group_post_sparks SET reaction = $3, updated_at = NOW() WHERE post_id = $1 AND user_id = $2`,
		postID, userID, reaction); err != nil {
		return ReactionChange{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReactionChange{}, err
	}
	return ReactionChange{Previous: prev, Current: reaction, Replaced: true}, nil
}

// RemoveGroupPostReaction removes whatever reaction the viewer has on the
// post. removed is false, with no error, when there was none — a retry of a
// removal is safe. spark_count moves by the row's legacy weight.
func (s *Store) RemoveGroupPostReaction(ctx context.Context, postID uuid.UUID, userID string) (removed bool, previous string, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var isSupernova bool
	err = tx.QueryRow(ctx,
		`DELETE FROM group_post_sparks WHERE post_id = $1 AND user_id = $2 RETURNING reaction, is_supernova`,
		postID, userID).Scan(&previous, &isSupernova)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", tx.Commit(ctx)
	}
	if err != nil {
		return false, "", err
	}
	weight := 1
	if isSupernova {
		weight = 5
	}
	if _, err := tx.Exec(ctx,
		`UPDATE group_posts SET spark_count = GREATEST(spark_count - $2, 0), updated_at = NOW() WHERE id = $1`,
		postID, weight); err != nil {
		return false, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, "", err
	}
	return true, previous, nil
}

// GetGroupPostReactionCounts returns per-reaction tallies for a batch of
// posts in one query. A post with no reactions is absent from the map.
func (s *Store) GetGroupPostReactionCounts(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]map[string]int, error) {
	out := make(map[uuid.UUID]map[string]int, len(postIDs))
	if len(postIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT post_id, reaction, COUNT(*) FROM group_post_sparks
		 WHERE post_id = ANY($1) GROUP BY post_id, reaction`, postIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var reaction string
		var n int
		if err := rows.Scan(&id, &reaction, &n); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]int{}
		}
		out[id][reaction] = n
	}
	return out, rows.Err()
}

// GetViewerReaction returns the viewer's current reaction on a post, or ""
// when there is none.
func (s *Store) GetViewerReaction(ctx context.Context, postID uuid.UUID, userID string) (string, error) {
	var r string
	err := s.db.QueryRow(ctx,
		`SELECT reaction FROM group_post_sparks WHERE post_id = $1 AND user_id = $2`,
		postID, userID).Scan(&r)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return r, err
}

// GetGroupPostSparkCount reads the legacy counter for the authoritative
// after-write state.
func (s *Store) GetGroupPostSparkCount(ctx context.Context, postID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx, `SELECT spark_count FROM group_posts WHERE id = $1`, postID).Scan(&n)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("not found: post not found")
	}
	return n, err
}

/*
	attachReactionCounts fills ReactionCounts on every post in the slice from
	one batched query. Every read surface that hands posts to a client calls
	it — the group feed, the single post, the pending queue, search and the
	cross-group feed — and a test enumerates those surfaces so that a new one
	cannot ship without it. A post with no reactions gets an empty map, never
	nil: a nil map marshals as null and the field is documented as an object.
*/
func (s *Store) attachReactionCounts(ctx context.Context, posts []GroupPostV2) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(posts))
	for i := range posts {
		ids[i] = posts[i].ID
	}
	counts, err := s.GetGroupPostReactionCounts(ctx, ids)
	if err != nil {
		return err
	}
	for i := range posts {
		if c, ok := counts[posts[i].ID]; ok {
			posts[i].ReactionCounts = c
		} else {
			posts[i].ReactionCounts = map[string]int{}
		}
	}
	return nil
}
