package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Emoji reactions on comments (migration 050).

	comment_reactions holds ONE row per (comment, viewer). PUT replaces the
	viewer's emoji, DELETE removes it, and the legacy /like route toggles the
	viewer's row between LikeEmoji and none on the same table — so old and
	new clients count the same thing.

	like_count is computed at read time: like_count = reaction_count =
	COUNT(*) of the comment's rows. comments.like_count is no longer written
	for reactions (the dislike toggle's mutual-exclusion write still touches
	the column, but no read surface uses it any more).

	A page of comments is hydrated with TWO queries, never N+1:
	  SELECT comment_id, emoji, COUNT(*) ... WHERE comment_id = ANY($1) GROUP BY 1,2
	  SELECT comment_id, emoji FROM comment_reactions WHERE user_id = $2 AND comment_id = ANY($1)
*/

// LikeEmoji is what the legacy POST /v1/comments/:id/like route writes.
const LikeEmoji = "❤️"

// CommentReactionTop is how many distinct emojis a Comment carries on the
// wire (by count desc, then emoji asc). reaction_count still counts all.
const CommentReactionTop = 3

// CommentReaction is one emoji's tally on a comment.
type CommentReaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
}

// CommentReactionSummary is the body PUT/DELETE /v1/comments/:id/reaction
// answer with. Emoji is the emoji just set (nil after a DELETE).
type CommentReactionSummary struct {
	Emoji          *string           `json:"emoji"`
	ReactionCount  int               `json:"reaction_count"`
	Reactions      []CommentReaction `json:"reactions"`
	ViewerReaction *string           `json:"viewer_reaction"`
}

// commentReactionAgg is the per-comment result of the batch load.
type commentReactionAgg struct {
	total  int
	top    []CommentReaction
	viewer *string
}

// SetCommentReaction upserts the viewer's single reaction: a new emoji
// replaces the old one. Validation of the emoji (1–16 chars) is the
// service's; the table's CHECK is the backstop.
func (s *Store) SetCommentReaction(ctx context.Context, commentID, userID uuid.UUID, emoji string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO comment_reactions (comment_id, user_id, emoji)
		VALUES ($1, $2, $3)
		ON CONFLICT (comment_id, user_id) DO UPDATE
		SET emoji = EXCLUDED.emoji, created_at = now()`,
		commentID, userID, emoji)
	if err != nil {
		return fmt.Errorf("set comment reaction: %w", err)
	}
	return nil
}

// RemoveCommentReaction deletes the viewer's reaction. removed reports
// whether there was one.
func (s *Store) RemoveCommentReaction(ctx context.Context, commentID, userID uuid.UUID) (removed bool, err error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM comment_reactions WHERE comment_id = $1 AND user_id = $2`, commentID, userID)
	if err != nil {
		return false, fmt.Errorf("remove comment reaction: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// ToggleCommentHeart is the legacy like: if the viewer's reaction is
// LikeEmoji it is removed, otherwise LikeEmoji is set (replacing any other
// emoji). Returns whether the heart is set afterwards.
func (s *Store) ToggleCommentHeart(ctx context.Context, commentID, userID uuid.UUID) (liked bool, err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var current string
	err = tx.QueryRow(ctx,
		`SELECT emoji FROM comment_reactions WHERE comment_id = $1 AND user_id = $2 FOR UPDATE`,
		commentID, userID).Scan(&current)
	switch {
	case err == nil && current == LikeEmoji:
		if _, err := tx.Exec(ctx,
			`DELETE FROM comment_reactions WHERE comment_id = $1 AND user_id = $2`, commentID, userID); err != nil {
			return false, fmt.Errorf("toggle comment heart off: %w", err)
		}
		liked = false
	case err == nil || errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, `
			INSERT INTO comment_reactions (comment_id, user_id, emoji)
			VALUES ($1, $2, $3)
			ON CONFLICT (comment_id, user_id) DO UPDATE
			SET emoji = EXCLUDED.emoji, created_at = now()`,
			commentID, userID, LikeEmoji); err != nil {
			return false, fmt.Errorf("toggle comment heart on: %w", err)
		}
		liked = true
	default:
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return liked, nil
}

// GetCommentReactionSummary is the one-comment read behind the PUT/DELETE
// responses. viewerID nil means no viewer_reaction.
func (s *Store) GetCommentReactionSummary(ctx context.Context, commentID uuid.UUID, viewerID *uuid.UUID) (*CommentReactionSummary, error) {
	aggs, err := s.loadCommentReactions(ctx, []uuid.UUID{commentID}, viewerID)
	if err != nil {
		return nil, err
	}
	a := aggs[commentID]
	out := &CommentReactionSummary{ReactionCount: a.total, Reactions: a.top, ViewerReaction: a.viewer}
	if out.Reactions == nil {
		out.Reactions = []CommentReaction{}
	}
	return out, nil
}

// loadCommentReactions runs the two batch queries for a set of comment ids.
// Every id gets an entry (zero when the comment has no reactions).
func (s *Store) loadCommentReactions(ctx context.Context, ids []uuid.UUID, viewerID *uuid.UUID) (map[uuid.UUID]commentReactionAgg, error) {
	out := make(map[uuid.UUID]commentReactionAgg, len(ids))
	for _, id := range ids {
		out[id] = commentReactionAgg{top: []CommentReaction{}}
	}
	if len(ids) == 0 {
		return out, nil
	}

	rows, err := s.db.Query(ctx, `
		SELECT comment_id, emoji, COUNT(*)
		FROM comment_reactions
		WHERE comment_id = ANY($1)
		GROUP BY 1, 2`, ids)
	if err != nil {
		return nil, fmt.Errorf("load comment reactions: %w", err)
	}
	defer rows.Close()
	all := make(map[uuid.UUID][]CommentReaction, len(ids))
	for rows.Next() {
		var id uuid.UUID
		var r CommentReaction
		if err := rows.Scan(&id, &r.Emoji, &r.Count); err != nil {
			return nil, err
		}
		all[id] = append(all[id], r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id, list := range all {
		sort.Slice(list, func(i, j int) bool {
			if list[i].Count != list[j].Count {
				return list[i].Count > list[j].Count
			}
			return list[i].Emoji < list[j].Emoji
		})
		a := out[id]
		for _, r := range list {
			a.total += r.Count
		}
		if len(list) > CommentReactionTop {
			list = list[:CommentReactionTop]
		}
		a.top = list
		out[id] = a
	}

	if viewerID != nil {
		vrows, err := s.db.Query(ctx, `
			SELECT comment_id, emoji FROM comment_reactions
			WHERE user_id = $2 AND comment_id = ANY($1)`, ids, *viewerID)
		if err != nil {
			return nil, fmt.Errorf("load viewer comment reactions: %w", err)
		}
		defer vrows.Close()
		for vrows.Next() {
			var id uuid.UUID
			var emoji string
			if err := vrows.Scan(&id, &emoji); err != nil {
				return nil, err
			}
			a := out[id]
			e := emoji
			a.viewer = &e
			out[id] = a
		}
		if err := vrows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// hydrateCommentReactions fills reactions / reaction_count / viewer_reaction
// (and like_count, which is the same figure) on a page of comments and
// their inline replies, in two queries for the whole page. On failure the
// page ships with empty reactions rather than failing the read.
func (s *Store) hydrateCommentReactions(ctx context.Context, comments []Comment, viewerID *uuid.UUID) error {
	var ids []uuid.UUID
	for i := range comments {
		ids = append(ids, comments[i].ID)
		if comments[i].Reply != nil {
			ids = append(ids, comments[i].Reply.ID)
		}
	}
	aggs, err := s.loadCommentReactions(ctx, ids, viewerID)
	apply := func(c *Comment) {
		if c == nil {
			return
		}
		a := aggs[c.ID]
		c.Reactions = a.top
		if c.Reactions == nil {
			c.Reactions = []CommentReaction{}
		}
		c.ReactionCount = a.total
		c.ViewerReaction = a.viewer
		c.LikeCount = a.total
	}
	for i := range comments {
		apply(&comments[i])
		apply(comments[i].Reply)
	}
	return err
}
