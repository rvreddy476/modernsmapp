package scylla

import (
	"context"
	"os"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

type TimelineStore struct {
	session *gocql.Session
}

func New(session *gocql.Session) *TimelineStore {
	return &TimelineStore{session: session}
}

// FeedItem represents a post in a timeline
type FeedItem struct {
	PostID      uuid.UUID
	AuthorID    uuid.UUID
	CreatedAt   time.Time
	ContentType string // "post", "reel", "video", or "" for legacy rows
}

const maxTimelineBucketLookback = 12

// bucket returns YYYYMM int from a time
func bucket(t time.Time) int {
	return t.Year()*100 + int(t.Month())
}

// currentBucket returns the current month bucket
func currentBucket() int {
	return bucket(time.Now().UTC())
}

func monthStart(t time.Time) time.Time {
	utc := t.UTC()
	return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// toGocql converts google/uuid to gocql UUID
func toGocql(id uuid.UUID) gocql.UUID {
	return gocql.UUID(id)
}

// AddToHomeTimeline (Push). Also writes the HF4 reverse-index row so
// UpdatePostContentType can find this row by post_id without scanning.
func (s *TimelineStore) AddToHomeTimeline(ctx context.Context, userID uuid.UUID, postID, authorID uuid.UUID, createdAt time.Time, contentType string) error {
	b := bucket(createdAt)
	ts := gocql.UUIDFromTime(createdAt)

	if err := s.session.Query(`
		INSERT INTO home_timeline_by_user (user_id, bucket, ts, post_id, author_id, created_at, content_type)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, toGocql(userID), b, ts, toGocql(postID), toGocql(authorID), createdAt, contentType).Exec(); err != nil {
		return err
	}
	// Best-effort: a reverse-index write failure leaves the timeline
	// row addressable only by ALLOW FILTERING (slow but correct). The
	// alternative — refusing the timeline insert when the index write
	// fails — would lose the post from the user's feed entirely, which
	// is worse. Log + continue.
	if err := s.session.Query(`
		INSERT INTO timeline_index_by_post (post_id, timeline_kind, owner_id, bucket, ts)
		VALUES (?, 'home', ?, ?, ?)
	`, toGocql(postID), toGocql(userID), b, ts).Exec(); err != nil {
		// Intentional: don't surface the index error.
		_ = err
	}
	return nil
}

// AddToAuthorTimeline (Pull source). Also writes the HF4 reverse-index
// row for the author copy.
func (s *TimelineStore) AddToAuthorTimeline(ctx context.Context, authorID uuid.UUID, postID uuid.UUID, createdAt time.Time, contentType string) error {
	b := bucket(createdAt)
	ts := gocql.UUIDFromTime(createdAt)

	if err := s.session.Query(`
		INSERT INTO author_timeline_by_author (author_id, bucket, ts, post_id, created_at, content_type)
		VALUES (?, ?, ?, ?, ?, ?)
	`, toGocql(authorID), b, ts, toGocql(postID), createdAt, contentType).Exec(); err != nil {
		return err
	}
	if err := s.session.Query(`
		INSERT INTO timeline_index_by_post (post_id, timeline_kind, owner_id, bucket, ts)
		VALUES (?, 'author', ?, ?, ?)
	`, toGocql(postID), toGocql(authorID), b, ts).Exec(); err != nil {
		_ = err
	}
	return nil
}

// UpdatePostContentType rewrites the content_type column on every
// timeline row that references the given post — both the
// home_timeline_by_user fan-out copies and the author_timeline_by_author
// canonical row. Without this, /v1/feed/reels (which filters on the
// timeline's content_type) keeps returning stale results after a
// MediaTranscodeConsumer reclassification flips a long_video back to
// flick (or any future content_type transition).
//
// HF4: uses social_feed.timeline_index_by_post — a partition lookup
// keyed on post_id — instead of the legacy ALLOW FILTERING scan. The
// index is written transactionally with each AddTo* insert, so any
// timeline row created after HF4 lands is addressable in O(1) per
// owner. Pre-HF4 rows have no index entries; for those we fall back
// to the scan so reclassifications still complete. The fallback is
// gated on `FEED_HF4_FALLBACK=true` (default true) so a future cutover
// can turn it off once the back-populate worker has run.
//
// Returns (rowsRewritten, err). Idempotent: re-running it with the
// same newType is a no-op stream of UPDATEs.
func (s *TimelineStore) UpdatePostContentType(ctx context.Context, postID uuid.UUID, newType string) (int, error) {
	rows := 0

	// Index path — single partition lookup per post.
	indexed := 0
	{
		iter := s.session.Query(
			`SELECT timeline_kind, owner_id, bucket, ts
			 FROM timeline_index_by_post WHERE post_id = ?`,
			toGocql(postID),
		).WithContext(ctx).Iter()
		var kind string
		var owner gocql.UUID
		var b int
		var ts gocql.UUID
		for iter.Scan(&kind, &owner, &b, &ts) {
			indexed++
			var stmt string
			switch kind {
			case "home":
				stmt = `UPDATE home_timeline_by_user SET content_type = ?
				        WHERE user_id = ? AND bucket = ? AND ts = ?`
			case "author":
				stmt = `UPDATE author_timeline_by_author SET content_type = ?
				        WHERE author_id = ? AND bucket = ? AND ts = ?`
			default:
				continue
			}
			if err := s.session.Query(stmt, newType, owner, b, ts).WithContext(ctx).Exec(); err != nil {
				_ = iter.Close()
				return rows, err
			}
			rows++
		}
		if err := iter.Close(); err != nil {
			return rows, err
		}
	}

	// If the index returned any rows, trust it — the index is the
	// source of truth for HF4-era timeline rows.
	if indexed > 0 {
		return rows, nil
	}

	// Fallback path: pre-HF4 row with no index entries. Scans both
	// fan-out tables once. Gated by FEED_HF4_FALLBACK=false to disable
	// after back-populate; defaults to enabled.
	if os.Getenv("FEED_HF4_FALLBACK") == "false" {
		return rows, nil
	}

	// home_timeline_by_user: one row per follower.
	{
		iter := s.session.Query(
			`SELECT user_id, bucket, ts FROM home_timeline_by_user WHERE post_id = ? ALLOW FILTERING`,
			toGocql(postID),
		).WithContext(ctx).Iter()
		var userID gocql.UUID
		var b int
		var ts gocql.UUID
		for iter.Scan(&userID, &b, &ts) {
			if err := s.session.Query(
				`UPDATE home_timeline_by_user SET content_type = ?
				 WHERE user_id = ? AND bucket = ? AND ts = ?`,
				newType, userID, b, ts,
			).WithContext(ctx).Exec(); err != nil {
				_ = iter.Close()
				return rows, err
			}
			// Back-populate the index for this row so future reclassifies
			// hit the fast path.
			_ = s.session.Query(`
				INSERT INTO timeline_index_by_post (post_id, timeline_kind, owner_id, bucket, ts)
				VALUES (?, 'home', ?, ?, ?)
			`, toGocql(postID), userID, b, ts).WithContext(ctx).Exec()
			rows++
		}
		if err := iter.Close(); err != nil {
			return rows, err
		}
	}

	// author_timeline_by_author: one row (the author's canonical copy).
	{
		iter := s.session.Query(
			`SELECT author_id, bucket, ts FROM author_timeline_by_author WHERE post_id = ? ALLOW FILTERING`,
			toGocql(postID),
		).WithContext(ctx).Iter()
		var authorID gocql.UUID
		var b int
		var ts gocql.UUID
		for iter.Scan(&authorID, &b, &ts) {
			if err := s.session.Query(
				`UPDATE author_timeline_by_author SET content_type = ?
				 WHERE author_id = ? AND bucket = ? AND ts = ?`,
				newType, authorID, b, ts,
			).WithContext(ctx).Exec(); err != nil {
				_ = iter.Close()
				return rows, err
			}
			_ = s.session.Query(`
				INSERT INTO timeline_index_by_post (post_id, timeline_kind, owner_id, bucket, ts)
				VALUES (?, 'author', ?, ?, ?)
			`, toGocql(postID), authorID, b, ts).WithContext(ctx).Exec()
			rows++
		}
		if err := iter.Close(); err != nil {
			return rows, err
		}
	}

	return rows, nil
}

// GetHomeTimeline returns all timeline items for the current month bucket.
func (s *TimelineStore) GetHomeTimeline(ctx context.Context, userID uuid.UUID, limit int) ([]FeedItem, error) {
	return s.collectHomeTimeline(ctx, userID, time.Now().UTC(), nil, limit)
}

// GetHomeTimelineBefore returns timeline items older than the provided timestamp.
func (s *TimelineStore) GetHomeTimelineBefore(ctx context.Context, userID uuid.UUID, before time.Time, limit int) ([]FeedItem, error) {
	beforeUTC := before.UTC()
	return s.collectHomeTimeline(ctx, userID, beforeUTC, &beforeUTC, limit)
}

// GetHomeTimelineByContentType returns timeline items filtered to a single
// content_type. Over-fetches and filters in Go since content_type is not a
// clustering key. The partition scan is bounded by (user_id, bucket).
func (s *TimelineStore) GetHomeTimelineByContentType(ctx context.Context, userID uuid.UUID, contentType string, limit int) ([]FeedItem, error) {
	return s.GetHomeTimelineByContentTypes(ctx, userID, []string{contentType}, limit)
}

// GetHomeTimelineByContentTypes returns timeline items filtered to a set of
// content_types. Over-fetches and filters in Go since content_type is not a
// clustering key. The partition scan is bounded by (user_id, bucket).
func (s *TimelineStore) GetHomeTimelineByContentTypes(ctx context.Context, userID uuid.UUID, contentTypes []string, limit int) ([]FeedItem, error) {
	if limit <= 0 {
		return nil, nil
	}

	typeSet := make(map[string]struct{}, len(contentTypes))
	for _, ct := range contentTypes {
		if ct != "" {
			typeSet[ct] = struct{}{}
		}
	}

	current := monthStart(time.Now().UTC())
	items := make([]FeedItem, 0, limit)
	for i := 0; i < maxTimelineBucketLookback && len(items) < limit; i++ {
		fetchLimit := (limit - len(items)) * 5
		if fetchLimit > 1000 {
			fetchLimit = 1000
		}

		batch, err := s.queryHomeTimelineBucket(ctx, userID, bucket(current), nil, fetchLimit)
		if err != nil {
			return nil, err
		}

		for _, item := range batch {
			if _, ok := typeSet[item.ContentType]; !ok {
				continue
			}
			items = append(items, item)
			if len(items) >= limit {
				break
			}
		}

		current = current.AddDate(0, -1, 0)
	}

	return items, nil
}

func (s *TimelineStore) collectHomeTimeline(ctx context.Context, userID uuid.UUID, start time.Time, firstBefore *time.Time, limit int) ([]FeedItem, error) {
	if limit <= 0 {
		return nil, nil
	}

	items := make([]FeedItem, 0, limit)
	current := monthStart(start)
	before := firstBefore

	for i := 0; i < maxTimelineBucketLookback && len(items) < limit; i++ {
		batch, err := s.queryHomeTimelineBucket(ctx, userID, bucket(current), before, limit-len(items))
		if err != nil {
			return nil, err
		}
		items = append(items, batch...)
		current = current.AddDate(0, -1, 0)
		before = nil
	}

	return items, nil
}

func (s *TimelineStore) queryHomeTimelineBucket(ctx context.Context, userID uuid.UUID, bucketID int, before *time.Time, limit int) ([]FeedItem, error) {
	if limit <= 0 {
		return nil, nil
	}

	var iter *gocql.Iter
	if before != nil && bucket(before.UTC()) == bucketID {
		iter = s.session.Query(`
			SELECT post_id, author_id, created_at, content_type FROM home_timeline_by_user
			WHERE user_id = ? AND bucket = ? AND ts < ?
			ORDER BY ts DESC
			LIMIT ?
		`, toGocql(userID), bucketID, gocql.UUIDFromTime(before.UTC()), limit).WithContext(ctx).Iter()
	} else {
		iter = s.session.Query(`
			SELECT post_id, author_id, created_at, content_type FROM home_timeline_by_user
			WHERE user_id = ? AND bucket = ?
			ORDER BY ts DESC
			LIMIT ?
		`, toGocql(userID), bucketID, limit).WithContext(ctx).Iter()
	}

	var items []FeedItem
	var pid, aid gocql.UUID
	var createdAt time.Time
	var contentType *string
	for iter.Scan(&pid, &aid, &createdAt, &contentType) {
		ct := "post"
		if contentType != nil && *contentType != "" {
			ct = *contentType
		}
		items = append(items, FeedItem{
			PostID:      uuid.UUID(pid),
			AuthorID:    uuid.UUID(aid),
			CreatedAt:   createdAt,
			ContentType: ct,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return items, nil
}

// GetAuthorTimeline (for Pull merge)
func (s *TimelineStore) GetAuthorTimeline(ctx context.Context, authorID uuid.UUID, limit int) ([]FeedItem, error) {
	b := currentBucket()

	iter := s.session.Query(`
		SELECT post_id, created_at, content_type FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ?
		ORDER BY ts DESC
		LIMIT ?
	`, toGocql(authorID), b, limit).Iter()

	var items []FeedItem
	var pid gocql.UUID
	var createdAt time.Time
	var contentType *string
	for iter.Scan(&pid, &createdAt, &contentType) {
		ct := "post"
		if contentType != nil && *contentType != "" {
			ct = *contentType
		}
		items = append(items, FeedItem{
			PostID:      uuid.UUID(pid),
			AuthorID:    authorID,
			CreatedAt:   createdAt,
			ContentType: ct,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return items, nil
}

// DeleteHomeTimelineEntriesByAuthorForUser removes from a single user's
// home_timeline_by_user every row whose author_id matches the given
// author. Used by the UserUnfollowed consumer to make unfollows
// immediate: the followee's previously-fanned-out (and previously-
// backfilled) posts disappear from the follower's feed on next read.
//
// home_timeline_by_user is keyed (user_id, bucket) PARTITION, (ts,
// post_id) CLUSTERING — there's no secondary index on author_id, so
// we scan the user's buckets and delete by their full primary key.
// Scoped to the last `bucketLookback` months (default 3) to bound the
// scan; older entries age out naturally.
func (s *TimelineStore) DeleteHomeTimelineEntriesByAuthorForUser(ctx context.Context, userID, authorID uuid.UUID, bucketLookback int) error {
	if bucketLookback <= 0 {
		bucketLookback = 3
	}
	now := time.Now().UTC()
	gAuthor := toGocql(authorID)
	gUser := toGocql(userID)

	for i := 0; i < bucketLookback; i++ {
		b := bucket(now.AddDate(0, -i, 0))

		// Find every (ts, post_id) the followee authored in this bucket
		// for this user. We scan the partition (cheap — keyed by
		// user_id+bucket) and filter author_id client-side.
		iter := s.session.Query(`
			SELECT ts, post_id, author_id FROM home_timeline_by_user
			WHERE user_id = ? AND bucket = ?
		`, gUser, b).WithContext(ctx).Iter()

		type pk struct {
			ts     gocql.UUID
			postID gocql.UUID
		}
		var doomed []pk
		var ts, pid, aid gocql.UUID
		for iter.Scan(&ts, &pid, &aid) {
			if aid == gAuthor {
				doomed = append(doomed, pk{ts: ts, postID: pid})
			}
		}
		if err := iter.Close(); err != nil {
			return err
		}

		for _, d := range doomed {
			if err := s.session.Query(`
				DELETE FROM home_timeline_by_user
				WHERE user_id = ? AND bucket = ? AND ts = ? AND post_id = ?
			`, gUser, b, d.ts, d.postID).WithContext(ctx).Exec(); err != nil {
				return err
			}
		}
	}
	return nil
}

// GetAuthorTimelineMultiBucket pulls recent author-timeline entries
// across `bucketLookback` rolling months (oldest-bucket-first then
// reversed to newest-first). Used by the UserFollowed backfill so a
// freshly-followed account with no posts in the current bucket still
// shows up if they posted in the previous month.
func (s *TimelineStore) GetAuthorTimelineMultiBucket(ctx context.Context, authorID uuid.UUID, limit, bucketLookback int) ([]FeedItem, error) {
	if bucketLookback <= 0 {
		bucketLookback = 3
	}
	now := time.Now().UTC()
	gAuthor := toGocql(authorID)

	out := make([]FeedItem, 0, limit)
	for i := 0; i < bucketLookback && len(out) < limit; i++ {
		b := bucket(now.AddDate(0, -i, 0))
		remaining := limit - len(out)
		iter := s.session.Query(`
			SELECT post_id, created_at, content_type FROM author_timeline_by_author
			WHERE author_id = ? AND bucket = ?
			ORDER BY ts DESC
			LIMIT ?
		`, gAuthor, b, remaining).WithContext(ctx).Iter()

		var pid gocql.UUID
		var createdAt time.Time
		var contentType *string
		for iter.Scan(&pid, &createdAt, &contentType) {
			ct := "post"
			if contentType != nil && *contentType != "" {
				ct = *contentType
			}
			out = append(out, FeedItem{
				PostID:      uuid.UUID(pid),
				AuthorID:    authorID,
				CreatedAt:   createdAt,
				ContentType: ct,
			})
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// DeleteTimelineEntriesByAuthor removes all author-timeline entries for the given
// author (GDPR right-to-erasure). It deletes across a rolling window of the
// current and previous two months from author_timeline_by_author.
// Note: home_timeline entries authored by this user will be naturally pruned
// as they expire or as the feed service skips soft-deleted post references.
func (s *TimelineStore) DeleteTimelineEntriesByAuthor(ctx context.Context, authorID uuid.UUID) error {
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		t := now.AddDate(0, -i, 0)
		b := bucket(t)
		if err := s.session.Query(`
			DELETE FROM author_timeline_by_author
			WHERE author_id = ? AND bucket = ?
		`, toGocql(authorID), b).WithContext(ctx).Exec(); err != nil {
			return err
		}
	}
	return nil
}

// DeleteAuthorTimelineEntry removes a single post entry from the author timeline
// for a given bucket. Used when an upload is deleted to clean up the author's timeline.
func (s *TimelineStore) DeleteAuthorTimelineEntry(ctx context.Context, authorID, postID uuid.UUID, bucket int) error {
	// Since post_id is not a clustering key, we need to find the ts for this post_id first
	// and then delete by (author_id, bucket, ts). For simplicity, we scan the bucket to find it.
	iter := s.session.Query(`
		SELECT ts FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ?
		ORDER BY ts DESC
	`, toGocql(authorID), bucket).Iter()

	var ts gocql.UUID
	found := false
	// Also scan post_id to match
	var pid gocql.UUID
	iter2 := s.session.Query(`
		SELECT ts, post_id FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ?
	`, toGocql(authorID), bucket).Iter()

	for iter2.Scan(&ts, &pid) {
		if uuid.UUID(pid) == postID {
			found = true
			break
		}
	}
	_ = iter.Close()
	_ = iter2.Close()

	if !found {
		return nil
	}

	return s.session.Query(`
		DELETE FROM author_timeline_by_author
		WHERE author_id = ? AND bucket = ? AND ts = ?
	`, toGocql(authorID), bucket, ts).WithContext(ctx).Exec()
}

// RecordInteraction stores a user-post interaction in ScyllaDB as the
// durable source of truth for the already-interacted ranking penalty.
func (s *TimelineStore) RecordInteraction(ctx context.Context, userID, postID uuid.UUID) error {
	return s.session.Query(`
		INSERT INTO user_post_interactions (user_id, post_id) VALUES (?, ?)`,
		toGocql(userID), toGocql(postID),
	).Exec()
}

// CheckInteractions returns the subset of postIDs that the user has
// previously interacted with. Used as a ScyllaDB fallback when Redis
// data has expired.
func (s *TimelineStore) CheckInteractions(ctx context.Context, userID uuid.UUID, postIDs []uuid.UUID) (map[string]bool, error) {
	result := make(map[string]bool, len(postIDs))
	if len(postIDs) == 0 {
		return result, nil
	}

	// Build IN clause with gocql UUIDs
	gocqlIDs := make([]interface{}, len(postIDs))
	for i, id := range postIDs {
		gocqlIDs[i] = toGocql(id)
	}

	iter := s.session.Query(`
		SELECT post_id FROM user_post_interactions
		WHERE user_id = ? AND post_id IN ?`,
		toGocql(userID), gocqlIDs,
	).Iter()

	var pid gocql.UUID
	for iter.Scan(&pid) {
		result[uuid.UUID(pid).String()] = true
	}
	if err := iter.Close(); err != nil {
		return result, err
	}
	return result, nil
}
