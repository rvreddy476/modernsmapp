package scylla

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// Echo notes timeline (docs/designs/echo-contract.md, "feed-service";
// short-posts-design.md §5.5).
//
// notes_timeline has the home timeline's shape and bucketing:
//
//	PRIMARY KEY ((user_id, bucket), ts) WITH CLUSTERING ORDER BY (ts DESC)
//
// (DDL: database/scylla/notes_timeline.cql). Notes get their own table
// because they are rare next to other posts, and a per-kind read of the home
// timeline walks its buckets until it finds enough rows of that kind
// (collectByContentTypes), which would be slow for a kind this sparse.
//
// Every row is keyed by TimelineTS, so a re-write of a note (a retry, a
// restore, a follow backfill) is an upsert, and each row has a reverse-index
// row of kind NotesTimelineKind in timeline_index_by_post_v2, so
// DeletePostFromTimelines finds and removes it with the home and author rows.

// NotesTimelineKind is the timeline_kind of a notes_timeline row in the
// post reverse index.
const NotesTimelineKind = "notes"

// noteContentType is the content_type every notes_timeline row carries.
const noteContentType = "note"

// AddToNotesTimeline writes one note into ownerID's notes timeline, with its
// reverse-index row. The row lives until TimelineRetention after createdAt;
// a note already past it is not written.
func (s *TimelineStore) AddToNotesTimeline(ctx context.Context, ownerID, postID, authorID uuid.UUID, createdAt time.Time) error {
	ttl := rowTTL(createdAt, time.Now())
	if ttl == 0 {
		return nil
	}
	b := bucket(createdAt)
	ts := TimelineTS(postID, createdAt)
	if err := s.session.Query(`
		INSERT INTO notes_timeline (user_id, bucket, ts, post_id, author_id, created_at, content_type)
		VALUES (?, ?, ?, ?, ?, ?, ?) USING TTL ?
	`, toGocql(ownerID), b, ts, toGocql(postID), toGocql(authorID), createdAt, noteContentType, ttl).WithContext(ctx).Exec(); err != nil {
		return err
	}
	// Best-effort, as for the home row: a missing index row leaves the
	// timeline row unreachable by post until it ages out, which is better
	// than refusing the note.
	_ = s.session.Query(`
		INSERT INTO timeline_index_by_post_v2 (post_id, shard, timeline_kind, owner_id, bucket, ts)
		VALUES (?, ?, 'notes', ?, ?, ?) USING TTL ?
	`, toGocql(postID), IndexShard(ownerID), toGocql(ownerID), b, ts, ttl).WithContext(ctx).Exec()
	return nil
}

// GetNotesTimelineBefore returns up to limit rows of ownerID's notes
// timeline strictly older than beforeToken ("" = from the newest), newest
// first, each carrying its exact key as CursorToken: the Following keyset.
func (s *TimelineStore) GetNotesTimelineBefore(ctx context.Context, ownerID uuid.UUID, beforeToken string, limit int) ([]FeedItem, error) {
	if limit <= 0 {
		return nil, nil
	}
	start := time.Now().UTC()
	var before *gocql.UUID
	if beforeToken != "" {
		parsed, err := gocql.ParseUUID(beforeToken)
		if err != nil {
			return nil, err
		}
		before = &parsed
		start = parsed.Time().UTC()
	}
	items := make([]FeedItem, 0, limit)
	current := monthStart(start)
	for i := 0; i < maxTimelineBucketLookback && len(items) < limit; i++ {
		batch, err := s.queryNotesBucket(ctx, ownerID, bucket(current), before, limit-len(items))
		if err != nil {
			return nil, err
		}
		items = append(items, batch...)
		current = current.AddDate(0, -1, 0)
		before = nil
	}
	return items, nil
}

func (s *TimelineStore) queryNotesBucket(ctx context.Context, ownerID uuid.UUID, bucketID int, before *gocql.UUID, limit int) ([]FeedItem, error) {
	var iter *gocql.Iter
	if before != nil && bucket(before.Time().UTC()) == bucketID {
		iter = s.session.Query(`
			SELECT ts, post_id, author_id, created_at FROM notes_timeline
			WHERE user_id = ? AND bucket = ? AND ts < ?
			ORDER BY ts DESC
			LIMIT ?
		`, toGocql(ownerID), bucketID, *before, limit).WithContext(ctx).Iter()
	} else {
		iter = s.session.Query(`
			SELECT ts, post_id, author_id, created_at FROM notes_timeline
			WHERE user_id = ? AND bucket = ?
			ORDER BY ts DESC
			LIMIT ?
		`, toGocql(ownerID), bucketID, limit).WithContext(ctx).Iter()
	}
	var items []FeedItem
	var ts, pid, aid gocql.UUID
	var createdAt time.Time
	for iter.Scan(&ts, &pid, &aid, &createdAt) {
		items = append(items, FeedItem{
			PostID:      uuid.UUID(pid),
			AuthorID:    uuid.UUID(aid),
			CreatedAt:   createdAt,
			ContentType: noteContentType,
			CursorToken: ts.String(),
		})
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return items, nil
}

// DeleteNotesTimelineEntriesByAuthorForUser is the unfollow purge for the
// notes timeline: DeleteHomeTimelineEntriesByAuthorForUser's twin, with the
// same rules. Rows are deleted on the full primary key (user_id, bucket, ts)
// only — post_id is a regular column, and naming it makes Scylla refuse the
// statement — and each deleted row's reverse-index rows go with it. Every
// bucket and row is attempted; the first error is returned at the end.
func (s *TimelineStore) DeleteNotesTimelineEntriesByAuthorForUser(ctx context.Context, userID, authorID uuid.UUID, bucketLookback int) (int, error) {
	if bucketLookback <= 0 {
		bucketLookback = 3
	}
	if bucketLookback > maxTimelineBucketLookback {
		bucketLookback = maxTimelineBucketLookback
	}
	now := time.Now().UTC()
	gAuthor := toGocql(authorID)
	gUser := toGocql(userID)

	var deleted, failed int
	var firstErr error
	fail := func(err error) {
		failed++
		if firstErr == nil {
			firstErr = err
		}
	}
	type doomedRow struct {
		ts     gocql.UUID
		postID gocql.UUID
	}
	for i := 0; i < bucketLookback; i++ {
		b := bucket(now.AddDate(0, -i, 0))
		iter := s.session.Query(`
			SELECT ts, post_id, author_id FROM notes_timeline
			WHERE user_id = ? AND bucket = ?
		`, gUser, b).WithContext(ctx).Iter()
		var doomed []doomedRow
		var ts, pid, aid gocql.UUID
		for iter.Scan(&ts, &pid, &aid) {
			if aid == gAuthor {
				doomed = append(doomed, doomedRow{ts: ts, postID: pid})
			}
		}
		if err := iter.Close(); err != nil {
			fail(fmt.Errorf("scan notes bucket %d: %w", b, err))
			continue
		}
		for _, d := range doomed {
			if err := s.session.Query(`
				DELETE FROM notes_timeline
				WHERE user_id = ? AND bucket = ? AND ts = ?
			`, gUser, b, d.ts).WithContext(ctx).Exec(); err != nil {
				fail(fmt.Errorf("delete notes bucket %d ts %s: %w", b, d.ts, err))
				continue
			}
			deleted++
			if err := s.session.Query(`
				DELETE FROM timeline_index_by_post_v2
				WHERE post_id = ? AND shard = ? AND timeline_kind = 'notes' AND owner_id = ? AND bucket = ? AND ts = ?
			`, d.postID, IndexShard(userID), gUser, b, d.ts).WithContext(ctx).Exec(); err != nil {
				fail(fmt.Errorf("delete notes index post %s ts %s: %w", d.postID, d.ts, err))
			}
		}
	}
	if firstErr != nil {
		return deleted, fmt.Errorf("purged %d note row(s), %d operation(s) failed, first: %w", deleted, failed, firstErr)
	}
	return deleted, nil
}

// UserInteractedPosts returns up to limit post ids userID has interacted
// with (liked or commented on) in the last week: user_post_interactions,
// the durable record PostReacted / CommentCreated write. One partition.
// Echo's For You reads it for the accounts the viewer follows.
func (s *TimelineStore) UserInteractedPosts(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		return nil, nil
	}
	iter := s.session.Query(`
		SELECT post_id FROM user_post_interactions WHERE user_id = ? LIMIT ?`,
		toGocql(userID), limit,
	).WithContext(ctx).Iter()
	var out []uuid.UUID
	var pid gocql.UUID
	for iter.Scan(&pid) {
		out = append(out, uuid.UUID(pid))
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return out, nil
}
