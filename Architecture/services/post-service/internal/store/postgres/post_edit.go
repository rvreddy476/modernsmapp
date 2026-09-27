package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Edit after publish (MTube, 2026-09-27; migration 051 part F).

	PATCH /v1/posts/:id lets the owner change the descriptive fields of a
	post — never its media. Every write here:

	  * is a single UPDATE on the row, guarded by author_id, so the store
	    re-checks ownership even though the service already did;
	  * writes one append-only post_edit_audit row in the same transaction
	    with {field: {from, to}} for every scalar that changed (text as
	    {"changed": true} only — no body in the audit);
	  * bumps the search revision and emits PostSearchEligibilityChanged in
	    the same transaction when visibility changed (the same choke point
	    PublishPost and the moderation writes use), so search and every
	    feed projection learn about a public/unlisted/private flip.
*/

// PostEditPatch is a partial edit: a nil pointer is "leave it alone". The
// service has already validated every value (taxonomy, visibility enum,
// title / text ceilings, hashtag alphabet, media ownership).
type PostEditPatch struct {
	Title         *string
	Text          *string
	Tags          *[]string
	Hashtags      *[]string
	Category      *string
	Visibility    *string
	CoverMediaID  *uuid.UUID
	AllowDownload *bool
	NoComments    *bool
	MadeForKids   *bool
	Language      *string
}

// PostEditChange is one field's before/after in the audit row.
type PostEditChange struct {
	From any `json:"from,omitempty"`
	To   any `json:"to,omitempty"`
	// Changed is set instead of From/To for the post body.
	Changed bool `json:"changed,omitempty"`
}

// ErrPostEditNotOwned: the row exists but author_id is not the caller
// (or the post is deleted). The service maps it to 403 after it has
// already established existence, so the two never disagree.
var ErrPostEditNotOwned = errors.New("post is not owned by the caller")

// UpdatePostFields applies patch to the caller's post and returns the row
// as it now stands (media attached). Returns pgx.ErrNoRows for a post that
// does not exist or is deleted, ErrPostEditNotOwned for someone else's.
func (s *Store) UpdatePostFields(ctx context.Context, postID, actorID uuid.UUID, patch PostEditPatch) (*Post, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	before, err := scanPost(tx.QueryRow(ctx,
		`SELECT `+postCols+` FROM posts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, postID))
	if err != nil {
		return nil, err // pgx.ErrNoRows for a missing / deleted post
	}
	if before.AuthorID != actorID {
		return nil, ErrPostEditNotOwned
	}

	changes := postEditChanges(before, patch)
	if len(changes) == 0 {
		// Nothing to write; still answer the current row.
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return s.GetPost(ctx, postID)
	}

	after, err := scanPost(tx.QueryRow(ctx, `
		UPDATE posts SET
			title            = COALESCE($3, title),
			text             = COALESCE($4, text),
			tags             = COALESCE($5, tags),
			hashtags         = COALESCE($6, hashtags),
			category         = COALESCE($7, category),
			visibility       = COALESCE($8, visibility),
			cover_media_id   = COALESCE($9, cover_media_id),
			allow_download   = COALESCE($10, allow_download),
			no_comments      = COALESCE($11, no_comments),
			is_made_for_kids = COALESCE($12, is_made_for_kids),
			language         = COALESCE($13, language),
			updated_at       = NOW()
		WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL
		RETURNING `+postCols,
		postID, actorID,
		patch.Title, patch.Text, textArrayArg(patch.Tags), textArrayArg(patch.Hashtags),
		patch.Category, patch.Visibility, patch.CoverMediaID,
		patch.AllowDownload, patch.NoComments, patch.MadeForKids, patch.Language))
	if err != nil {
		return nil, fmt.Errorf("update post fields: %w", err)
	}

	if err := insertPostEditAudit(ctx, tx, postID, actorID, "post.edit", changes); err != nil {
		return nil, err
	}
	if _, changed := changes["visibility"]; changed {
		if err := BumpSearchRevAndEmitTx(ctx, tx, postID); err != nil {
			return nil, fmt.Errorf("emit search eligibility on edit: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	media, err := s.loadPostMediaForPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	after.Media = media
	return after, nil
}

// textArrayArg turns an optional slice into a nullable TEXT[] argument: nil
// pointer -> SQL NULL (COALESCE keeps the column), empty slice -> '{}'.
func textArrayArg(v *[]string) interface{} {
	if v == nil {
		return nil
	}
	if *v == nil {
		return []string{}
	}
	return *v
}

// postEditChanges diffs the patch against the row so the audit records only
// what actually changed, and so a no-op PATCH writes nothing.
func postEditChanges(before *Post, patch PostEditPatch) map[string]PostEditChange {
	changes := map[string]PostEditChange{}
	str := func(field string, cur string, next *string) {
		if next != nil && *next != cur {
			changes[field] = PostEditChange{From: cur, To: *next}
		}
	}
	boolean := func(field string, cur bool, next *bool) {
		if next != nil && *next != cur {
			changes[field] = PostEditChange{From: cur, To: *next}
		}
	}
	str("title", before.Title, patch.Title)
	if patch.Text != nil && *patch.Text != before.Text {
		changes["text"] = PostEditChange{Changed: true}
	}
	if patch.Tags != nil && !sameStrings(before.Tags, *patch.Tags) {
		changes["tags"] = PostEditChange{From: nonNil(before.Tags), To: nonNil(*patch.Tags)}
	}
	if patch.Hashtags != nil && !sameStrings(before.Hashtags, *patch.Hashtags) {
		changes["hashtags"] = PostEditChange{From: nonNil(before.Hashtags), To: nonNil(*patch.Hashtags)}
	}
	str("category", before.Category, patch.Category)
	str("visibility", before.Visibility, patch.Visibility)
	if patch.CoverMediaID != nil && (before.CoverMediaID == nil || *before.CoverMediaID != *patch.CoverMediaID) {
		var from any
		if before.CoverMediaID != nil {
			from = before.CoverMediaID.String()
		}
		changes["cover_media_id"] = PostEditChange{From: from, To: patch.CoverMediaID.String()}
	}
	boolean("allow_download", before.AllowDownload, patch.AllowDownload)
	boolean("no_comments", before.NoComments, patch.NoComments)
	boolean("is_made_for_kids", before.IsMadeForKids, patch.MadeForKids)
	str("language", before.Language, patch.Language)
	return changes
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func insertPostEditAudit(ctx context.Context, tx pgx.Tx, postID, actorID uuid.UUID, action string, changes map[string]PostEditChange) error {
	body, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("marshal edit audit: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO post_edit_audit (post_id, actor_user_id, action, changes)
		VALUES ($1, $2, $3, $4)`, postID, actorID, action, body); err != nil {
		return fmt.Errorf("insert edit audit: %w", err)
	}
	return nil
}

// BulkVisibilityOutcome is one id's result from BulkSetVisibility.
type BulkVisibilityOutcome struct {
	ID    uuid.UUID `json:"id"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
}

// BulkSetVisibility applies one visibility to the caller's posts, one
// transaction per post (audit + search revision each), and reports a per-id
// outcome. The service has already established that every id belongs to the
// caller; the UPDATE is still guarded by author_id.
func (s *Store) BulkSetVisibility(ctx context.Context, actorID uuid.UUID, postIDs []uuid.UUID, visibility string) []BulkVisibilityOutcome {
	out := make([]BulkVisibilityOutcome, 0, len(postIDs))
	for _, id := range postIDs {
		outcome := BulkVisibilityOutcome{ID: id, OK: true}
		_, err := s.WithSearchEligibilityTx(ctx, id, func(ctx context.Context, tx pgx.Tx) (bool, error) {
			var previous string
			err := tx.QueryRow(ctx,
				`SELECT visibility FROM posts WHERE id = $1 AND author_id = $2 AND deleted_at IS NULL FOR UPDATE`,
				id, actorID).Scan(&previous)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return false, fmt.Errorf("NOT_FOUND")
				}
				return false, err
			}
			if previous == visibility {
				return false, nil
			}
			if _, err := tx.Exec(ctx,
				`UPDATE posts SET visibility = $3, updated_at = NOW() WHERE id = $1 AND author_id = $2`,
				id, actorID, visibility); err != nil {
				return false, err
			}
			return true, insertPostEditAudit(ctx, tx, id, actorID, "post.bulk_visibility",
				map[string]PostEditChange{"visibility": {From: previous, To: visibility}})
		})
		if err != nil {
			outcome.OK = false
			outcome.Error = err.Error()
		}
		out = append(out, outcome)
	}
	return out
}

// PostAuthorsByIDs returns author_id for each live post id (missing and
// deleted ids are absent), for the bulk route's owner-of-all check.
func (s *Store) PostAuthorsByIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := make(map[uuid.UUID]uuid.UUID, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT id, author_id FROM posts WHERE id = ANY($1) AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, author uuid.UUID
		if err := rows.Scan(&id, &author); err != nil {
			return nil, err
		}
		out[id] = author
	}
	return out, rows.Err()
}

// CreatorCounts is GET /v1/posts/me/summary's content half.
type CreatorCounts struct {
	Videos      int64
	Shorts      int64
	Live        int64
	Collections int64
}

// CountCreatorContent counts the author's live (not deleted) posts by kind:
// long videos, shorts, and posts that came from a live stream (source =
// 'live'), plus their user playlists. Scheduled posts count: the Creator
// Hub shows them.
func (s *Store) CountCreatorContent(ctx context.Context, authorID uuid.UUID) (CreatorCounts, error) {
	var c CreatorCounts
	err := s.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN content_type IN ('video', 'long_video') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN content_type IN ('flick', 'reel') THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN source = 'live' THEN 1 ELSE 0 END), 0),
			(SELECT COUNT(*) FROM playlists WHERE creator_id = $1 AND kind = 'user')
		FROM posts
		WHERE author_id = $1 AND deleted_at IS NULL`, authorID,
	).Scan(&c.Videos, &c.Shorts, &c.Live, &c.Collections)
	return c, err
}

// UpdatePostCategory sets posts.category (already validated against the
// taxonomy by the service) on a live post.
func (s *Store) UpdatePostCategory(ctx context.Context, postID uuid.UUID, category string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE posts SET category = $2, updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL`,
		postID, category)
	return err
}
