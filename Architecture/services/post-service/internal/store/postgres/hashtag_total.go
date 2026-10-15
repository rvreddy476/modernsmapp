package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// HashtagTotalCap bounds the hashtag page's post count (T-34). Past it the
// page says "10,000+" rather than paying for an exact count of a huge tag.
const HashtagTotalCap = 10000

// hashtagPostsFilter is the WHERE clause of every hashtag-page query: the
// list (getPostsByHashtagRecent / getPostsByHashtagTop) and the count
// below, so the count can never describe a different set than the list.
//
//   - the tag: `hashtags @> ARRAY[$1]` is `$1 = ANY(hashtags)` for a TEXT[]
//     column, in the form idx_posts_hashtags (GIN) can serve;
//   - addressed to everyone: visibility 'public' (followers-only, private
//     and shared posts are never listed on a tag page);
//   - viewer-eligible: live, not scheduled, and EFFECTIVELY approved
//     (ViewerEligibleSQL) — a pending, flagged, rejected or restricted
//     post is not listed or counted;
//   - optionally narrowed to contentTypes.
//
// The author gate (private accounts, blocks, deactivated authors) is
// graph-service's answer, applied by the service on the authors of the
// rows (GetPostsByHashtag / HashtagTotal); excludeAuthors carries the
// authors it has already denied into the count.
//
// $1 must be the tag. contentTypes and excludeAuthors are appended to
// args; the returned args are the ones to pass.
func hashtagPostsFilter(args []any, contentTypes []string, excludeAuthors []uuid.UUID) (string, []any) {
	where := `p.hashtags @> ARRAY[$1]::text[] AND p.visibility = 'public' AND ` + ViewerEligibleSQL("p")
	if len(contentTypes) > 0 {
		args = append(args, contentTypes)
		where += fmt.Sprintf(` AND p.content_type = ANY($%d)`, len(args))
	}
	if len(excludeAuthors) > 0 {
		args = append(args, excludeAuthors)
		where += fmt.Sprintf(` AND p.author_id <> ALL($%d::uuid[])`, len(args))
	}
	return where, args
}

// CountPostsByHashtag counts, per author, the posts the hashtag page lists
// for tag (hashtagPostsFilter, minus excludeAuthors), looking at no more
// than HashtagTotalCap+1 posts. truncated reports that the bound was hit,
// so more posts may exist than the counts add up to.
//
// Per author, not a single number, because the list drops posts whose
// author the viewer may not see (a private account they do not follow, a
// block, a deactivated author) and only graph-service can say which those
// are: service.HashtagTotal asks about these authors and sums the rest.
//
// Not hashtags.use_count: that counter is bumped for every post published
// with the tag — followers-only and private ones included — and never goes
// down on delete, so it is not "how many posts you can see here".
func (s *Store) CountPostsByHashtag(ctx context.Context, hashtag string, contentTypes []string, excludeAuthors []uuid.UUID) (byAuthor map[uuid.UUID]int, truncated bool, err error) {
	where, args := hashtagPostsFilter([]any{hashtag, HashtagTotalCap + 1}, contentTypes, excludeAuthors)
	rows, err := s.db.Query(ctx, `
		SELECT author_id, COUNT(*) FROM (
			SELECT p.author_id FROM posts p
			WHERE `+where+`
			LIMIT $2
		) bounded
		GROUP BY author_id`, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	byAuthor = make(map[uuid.UUID]int)
	scanned := 0
	for rows.Next() {
		var author uuid.UUID
		var n int
		if err := rows.Scan(&author, &n); err != nil {
			return nil, false, err
		}
		byAuthor[author] = n
		scanned += n
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return byAuthor, scanned > HashtagTotalCap, nil
}
