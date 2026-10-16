package scylla

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// One pass over every timeline row, for the rows the live writers can no
// longer fix by themselves:
//
//   - DUPLICATES. Before TimelineTS, every re-write of a post (a follow
//     backfill after a refollow, a restore, a Kafka redelivery) added a
//     second row at a fresh timeuuid. Those rows are still there.
//   - RETENTION. default_time_to_live and USING TTL apply to rows written
//     after them; a row written before has no TTL and would stay forever.
//   - THE INDEX. Rows written before HF4 have no index entry at all, and
//     rows written before F-28 are in the legacy unsharded table only. With
//     the ALLOW FILTERING fallback off, an unindexed row cannot be
//     reclassified or deleted.
//
// PlanPartition decides, per (owner, bucket) partition, and is pure;
// Maintainer scans token ranges and carries the plan out.

// TimelineRow is one row of home_timeline_by_user ("home") or
// author_timeline_by_author ("author") as the maintenance pass reads it.
type TimelineRow struct {
	Kind        string
	Owner       gocql.UUID // user_id or author_id
	Bucket      int
	TS          gocql.UUID
	PostID      *gocql.UUID // nil: a ghost an UPDATE created
	AuthorID    gocql.UUID  // home rows; author rows use Owner
	CreatedAt   time.Time   // zero when the column is null
	ContentType string
	HasTTL      bool
}

// Maintenance operations.
const (
	OpDelete = "delete" // delete the row and its index entries
	OpWrite  = "write"  // upsert the row (at its TS) with TTL
	OpIndex  = "index"  // upsert the row's v2 index entry with TTL
)

// Reasons, for the counts a dry run prints.
const (
	WhyGhost     = "ghost"     // post_id is null
	WhyExpired   = "expired"   // older than TimelineRetention
	WhyDuplicate = "duplicate" // a second key for a post the partition holds
	WhyRekey     = "rekey"     // the post's only row, at a non-canonical key
	WhyTTL       = "ttl"       // canonical row with no TTL
	WhyIndex     = "index"     // index entry for a kept row
)

// MaintenanceAction is one step of a plan.
type MaintenanceAction struct {
	Op  string
	Why string
	Row TimelineRow
	TTL int // seconds, for OpWrite and OpIndex
}

// PlanPartition plans one partition's rows (one owner, one bucket, any
// order). For every post it keeps exactly one row, at TimelineTS(post,
// created_at) with the TTL left of its retention, indexed in v2; every
// other row of that post is deleted and unindexed. A post past retention
// loses every row. Rows with no post_id are deleted.
//
// The kept row is written before its duplicates are deleted, so a pass
// that dies half-way never leaves a post with no row at all.
func PlanPartition(rows []TimelineRow, now time.Time) []MaintenanceAction {
	var plan []MaintenanceAction
	type group struct {
		post    gocql.UUID
		rows    []TimelineRow
		created time.Time
	}
	var order []gocql.UUID
	groups := map[gocql.UUID]*group{}
	for _, r := range rows {
		if r.PostID == nil {
			plan = append(plan, MaintenanceAction{Op: OpDelete, Why: WhyGhost, Row: r})
			continue
		}
		g, ok := groups[*r.PostID]
		if !ok {
			g = &group{post: *r.PostID}
			groups[*r.PostID] = g
			order = append(order, *r.PostID)
		}
		g.rows = append(g.rows, r)
		created := r.CreatedAt
		if created.IsZero() {
			created = r.TS.Time()
		}
		if g.created.IsZero() || created.Before(g.created) {
			g.created = created
		}
	}

	for _, id := range order {
		g := groups[id]
		ttl := rowTTL(g.created, now)
		if ttl == 0 {
			for _, r := range g.rows {
				plan = append(plan, MaintenanceAction{Op: OpDelete, Why: WhyExpired, Row: r})
			}
			continue
		}
		canonical := TimelineTS(uuid.UUID(g.post), g.created)
		canonicalBucket := bucket(g.created)
		why := WhyRekey
		if len(g.rows) > 1 {
			why = WhyDuplicate
		}
		var kept *TimelineRow
		var deletes []MaintenanceAction
		for i := range g.rows {
			r := g.rows[i]
			if r.TS == canonical && r.Bucket == canonicalBucket {
				kept = &g.rows[i]
				continue
			}
			deletes = append(deletes, MaintenanceAction{Op: OpDelete, Why: why, Row: r})
		}
		if kept == nil {
			// No row at the canonical key: write one from a copy (any copy:
			// they carry the same post).
			src := g.rows[0]
			src.TS = canonical
			src.Bucket = canonicalBucket
			src.CreatedAt = g.created.UTC().Truncate(time.Millisecond)
			plan = append(plan, MaintenanceAction{Op: OpWrite, Why: why, Row: src, TTL: ttl})
			kept = &src
		} else if !kept.HasTTL {
			plan = append(plan, MaintenanceAction{Op: OpWrite, Why: WhyTTL, Row: *kept, TTL: ttl})
		}
		plan = append(plan, MaintenanceAction{Op: OpIndex, Why: WhyIndex, Row: *kept, TTL: ttl})
		plan = append(plan, deletes...)
	}
	return plan
}

// TokenRange is an inclusive Murmur3 token range.
type TokenRange struct{ Start, End int64 }

// SplitTokenRing splits the whole Murmur3 ring into n contiguous ranges.
func SplitTokenRing(n int) []TokenRange {
	if n < 1 {
		n = 1
	}
	step := math.MaxUint64 / uint64(n)
	out := make([]TokenRange, n)
	start := uint64(0)
	for i := 0; i < n; i++ {
		end := start + step - 1
		if i == n-1 {
			end = math.MaxUint64
		}
		out[i] = TokenRange{Start: int64(start ^ (1 << 63)), End: int64(end ^ (1 << 63))}
		start = end + 1
	}
	return out
}

// MaintenanceStats counts a pass, by operation and reason.
type MaintenanceStats struct {
	Rows    int
	Actions map[string]int // "op/why"
}

// Add counts one action.
func (s *MaintenanceStats) Add(a MaintenanceAction) {
	if s.Actions == nil {
		s.Actions = map[string]int{}
	}
	s.Actions[a.Op+"/"+a.Why]++
}

// Merge adds other's counts into s.
func (s *MaintenanceStats) Merge(other MaintenanceStats) {
	s.Rows += other.Rows
	for k, v := range other.Actions {
		if s.Actions == nil {
			s.Actions = map[string]int{}
		}
		s.Actions[k] += v
	}
}

// Maintainer runs PlanPartition over a token range of one table.
type Maintainer struct {
	session *gocql.Session
	apply   bool
	now     func() time.Time
}

// NewMaintainer returns a maintainer; apply=false only counts.
func NewMaintainer(session *gocql.Session, apply bool) *Maintainer {
	return &Maintainer{session: session, apply: apply, now: time.Now}
}

// Range plans, and with apply carries out, every partition of kind's
// table ("home" or "author") whose token is in r.
func (m *Maintainer) Range(ctx context.Context, kind string, r TokenRange) (MaintenanceStats, error) {
	var stats MaintenanceStats
	var stmt string
	switch kind {
	case "home":
		stmt = `SELECT user_id, bucket, ts, post_id, created_at, content_type, TTL(post_id), author_id
		        FROM home_timeline_by_user WHERE token(user_id, bucket) >= ? AND token(user_id, bucket) <= ?`
	case "author":
		stmt = `SELECT author_id, bucket, ts, post_id, created_at, content_type, TTL(post_id)
		        FROM author_timeline_by_author WHERE token(author_id, bucket) >= ? AND token(author_id, bucket) <= ?`
	default:
		return stats, fmt.Errorf("unknown timeline kind %q", kind)
	}
	iter := m.session.Query(stmt, r.Start, r.End).WithContext(ctx).PageSize(1000).Iter()

	var partition []TimelineRow
	flush := func() error {
		if len(partition) == 0 {
			return nil
		}
		for _, a := range PlanPartition(partition, m.now()) {
			stats.Add(a)
			if m.apply {
				if err := m.do(ctx, a); err != nil {
					return err
				}
			}
		}
		partition = partition[:0]
		return nil
	}

	for {
		var (
			row       = TimelineRow{Kind: kind}
			postID    *gocql.UUID
			authorID  *gocql.UUID
			createdAt *time.Time
			ct        *string
			ttl       *int
		)
		dest := []interface{}{&row.Owner, &row.Bucket, &row.TS, &postID, &createdAt, &ct, &ttl}
		if kind == "home" {
			dest = append(dest, &authorID)
		}
		if !iter.Scan(dest...) {
			break
		}
		stats.Rows++
		row.PostID = postID
		if authorID != nil {
			row.AuthorID = *authorID
		}
		if createdAt != nil {
			row.CreatedAt = *createdAt
		}
		if ct != nil {
			row.ContentType = *ct
		}
		row.HasTTL = ttl != nil && *ttl > 0
		if n := len(partition); n > 0 && (partition[n-1].Owner != row.Owner || partition[n-1].Bucket != row.Bucket) {
			if err := flush(); err != nil {
				_ = iter.Close()
				return stats, err
			}
		}
		partition = append(partition, row)
	}
	if err := iter.Close(); err != nil {
		return stats, err
	}
	return stats, flush()
}

func (m *Maintainer) do(ctx context.Context, a MaintenanceAction) error {
	r := a.Row
	q := func(stmt string, args ...interface{}) error {
		return m.session.Query(stmt, args...).WithContext(ctx).Exec()
	}
	switch a.Op {
	case OpDelete:
		table, col := "home_timeline_by_user", "user_id"
		if r.Kind == "author" {
			table, col = "author_timeline_by_author", "author_id"
		}
		if err := q(`DELETE FROM `+table+` WHERE `+col+` = ? AND bucket = ? AND ts = ?`, r.Owner, r.Bucket, r.TS); err != nil {
			return err
		}
		if r.PostID == nil {
			return nil // a ghost has no index entry to find
		}
		return m.unindex(ctx, r)
	case OpWrite:
		if r.Kind == "author" {
			return q(`INSERT INTO author_timeline_by_author (author_id, bucket, ts, post_id, created_at, content_type)
			          VALUES (?, ?, ?, ?, ?, ?) USING TTL ?`,
				r.Owner, r.Bucket, r.TS, *r.PostID, r.CreatedAt, r.ContentType, a.TTL)
		}
		return q(`INSERT INTO home_timeline_by_user (user_id, bucket, ts, post_id, author_id, created_at, content_type)
		          VALUES (?, ?, ?, ?, ?, ?, ?) USING TTL ?`,
			r.Owner, r.Bucket, r.TS, *r.PostID, r.AuthorID, r.CreatedAt, r.ContentType, a.TTL)
	case OpIndex:
		return q(`INSERT INTO timeline_index_by_post_v2 (post_id, shard, timeline_kind, owner_id, bucket, ts)
		          VALUES (?, ?, ?, ?, ?, ?) USING TTL ?`,
			*r.PostID, IndexShard(uuid.UUID(r.Owner)), r.Kind, r.Owner, r.Bucket, r.TS, a.TTL)
	}
	return fmt.Errorf("unknown maintenance op %q", a.Op)
}

func (m *Maintainer) unindex(ctx context.Context, r TimelineRow) error {
	if err := m.session.Query(`DELETE FROM timeline_index_by_post_v2
		WHERE post_id = ? AND shard = ? AND timeline_kind = ? AND owner_id = ? AND bucket = ? AND ts = ?`,
		*r.PostID, IndexShard(uuid.UUID(r.Owner)), r.Kind, r.Owner, r.Bucket, r.TS).WithContext(ctx).Exec(); err != nil {
		return err
	}
	return m.session.Query(`DELETE FROM timeline_index_by_post
		WHERE post_id = ? AND timeline_kind = ? AND owner_id = ? AND bucket = ? AND ts = ?`,
		*r.PostID, r.Kind, r.Owner, r.Bucket, r.TS).WithContext(ctx).Exec()
}

// TruncateLegacyIndex empties the unsharded timeline_index_by_post. Only
// after a full -apply pass, and with FEED_INDEX_LEGACY_READ=false deployed.
func (m *Maintainer) TruncateLegacyIndex(ctx context.Context) error {
	return m.session.Query(`TRUNCATE timeline_index_by_post`).WithContext(ctx).Exec()
}
