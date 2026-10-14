package postgres

import (
	"context"
	"fmt"
)

// HashtagTotalCap bounds the hashtag page's post count (T-34). Past it the
// page says "10,000+" rather than paying for an exact count of a huge tag.
const HashtagTotalCap = 10000

// CountPostsByHashtag counts the posts the hashtag page lists for tag:
// exactly the predicate getPostsByHashtagRecent / getPostsByHashtagTop page
// over (live, public, not scheduled, optionally narrowed to contentTypes),
// stopping at HashtagTotalCap. capped reports that the cap was reached, in
// which case total is the cap.
//
// Not hashtags.use_count: that counter is bumped for every post published
// with the tag — followers-only and private ones included — and never goes
// down on delete, so it is not "how many posts you can see here".
//
// `hashtags @> ARRAY[tag]` is `tag = ANY(hashtags)` for a TEXT[] column, but
// in a form idx_posts_hashtags (GIN) can serve.
func (s *Store) CountPostsByHashtag(ctx context.Context, hashtag string, contentTypes []string) (total int, capped bool, err error) {
	args := []any{hashtag, HashtagTotalCap + 1}
	query := `SELECT COUNT(*) FROM (
		SELECT 1 FROM posts p
		WHERE p.hashtags @> ARRAY[$1]::text[] AND p.deleted_at IS NULL AND p.visibility = 'public' AND p.publish_at IS NULL`
	if len(contentTypes) > 0 {
		args = append(args, contentTypes)
		query += fmt.Sprintf(` AND p.content_type = ANY($%d)`, len(args))
	}
	query += ` LIMIT $2) bounded`
	var n int
	if err := s.db.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		return 0, false, err
	}
	if n > HashtagTotalCap {
		return HashtagTotalCap, true, nil
	}
	return n, false, nil
}
