// Package backfill holds one-off data copies that run as their own command.
package backfill

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Likes backfill (2026-10-17).

	GET /v1/reels/liked reads Postgres reactions (see service/liked_reels.go).
	Every like path writes that table since the 16 Oct deploy; likes from
	before it exist only in Scylla: reactions_by_post (the heart's state,
	keyed by post) and reel_reactions (POST /v1/reels/:id/react). This copies
	both into Postgres so the list includes them.

	Rules:
	  - A row already in Postgres wins (ON CONFLICT DO NOTHING): it was
	    written after the deploy and is the more current state.
	  - 'tune' is the private dislike and is never copied as a reaction.
	  - Unknown reaction names become 'like', as the live paths store them
	    (postgres.IndexReactionType).
	  - Only posts that exist in Postgres are copied; rows for posts that
	    were purged are counted and dropped.
	  - The Scylla time is kept, so the list orders old likes correctly; a
	    row without one gets the run's start time.
	  - Dry run by default: the same SQL runs and is rolled back, so the
	    report shows exactly what an applied run would insert.
*/

// Row is one Scylla like ready for Postgres.
type Row struct {
	PostID    uuid.UUID
	UserID    uuid.UUID
	Reaction  string
	CreatedAt time.Time
}

// Skip reasons reported per source row that is not copied.
const (
	SkipBadPostID = "bad_post_id"
	SkipBadUserID = "bad_user_id"
	SkipDislike   = "dislike"
)

// Normalize turns one Scylla row into a Row, or returns why it is skipped.
func Normalize(postID, userID, reaction string, createdAt, runStart time.Time) (Row, string) {
	p, err := uuid.Parse(strings.TrimSpace(postID))
	if err != nil || p == uuid.Nil {
		return Row{}, SkipBadPostID
	}
	u, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil || u == uuid.Nil {
		return Row{}, SkipBadUserID
	}
	r := strings.ToLower(strings.TrimSpace(reaction))
	if r == "tune" {
		return Row{}, SkipDislike
	}
	if createdAt.IsZero() {
		createdAt = runStart
	}
	return Row{PostID: p, UserID: u, Reaction: postgres.IndexReactionType(r), CreatedAt: createdAt.UTC()}, ""
}

// Source yields Scylla rows; Next returns ok=false when the source is done.
type Source interface {
	Name() string
	Next() (postID, userID, reaction string, createdAt time.Time, ok bool)
	Close() error
}

// Writer copies a batch and reports how many rows matched a post and how
// many were inserted (the rest already existed).
type Writer interface {
	WriteBatch(ctx context.Context, rows []Row, apply bool) (known, inserted int64, err error)
}

// Report is the outcome of a run, per source and in total.
type Report struct {
	Applied     bool                        `json:"applied"`
	Read        int64                       `json:"read"`
	Skipped     map[string]int64            `json:"skipped"`
	MissingPost int64                       `json:"missing_post"`
	Inserted    int64                       `json:"inserted"`
	Existing    int64                       `json:"already_present"`
	PerSource   map[string]map[string]int64 `json:"per_source"`
}

// Run copies every source through w in batches of batchSize.
func Run(ctx context.Context, sources []Source, w Writer, apply bool, batchSize int, runStart time.Time) (Report, error) {
	if batchSize < 1 {
		batchSize = 500
	}
	rep := Report{Applied: apply, Skipped: map[string]int64{}, PerSource: map[string]map[string]int64{}}
	for _, src := range sources {
		per := map[string]int64{}
		rep.PerSource[src.Name()] = per
		batch := make([]Row, 0, batchSize)
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			known, inserted, err := w.WriteBatch(ctx, batch, apply)
			if err != nil {
				return fmt.Errorf("%s: write batch: %w", src.Name(), err)
			}
			missing := int64(len(batch)) - known
			rep.MissingPost += missing
			rep.Inserted += inserted
			rep.Existing += known - inserted
			per["missing_post"] += missing
			per["inserted"] += inserted
			per["already_present"] += known - inserted
			batch = batch[:0]
			return nil
		}
		for {
			p, u, r, at, ok := src.Next()
			if !ok {
				break
			}
			rep.Read++
			per["read"]++
			row, skip := Normalize(p, u, r, at, runStart)
			if skip != "" {
				rep.Skipped[skip]++
				per["skipped_"+skip]++
				continue
			}
			batch = append(batch, row)
			if len(batch) == batchSize {
				if err := flush(); err != nil {
					_ = src.Close()
					return rep, err
				}
			}
		}
		if err := flush(); err != nil {
			_ = src.Close()
			return rep, err
		}
		if err := src.Close(); err != nil {
			return rep, fmt.Errorf("%s: read: %w", src.Name(), err)
		}
	}
	return rep, nil
}
