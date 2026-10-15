package service

import (
	"context"
	"errors"
	"log"
	"strconv"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	The viewer's liked reels (GET /v1/reels/liked, 2026-10-15).

	The list used to read Scylla user_reel_likes, which no code wrote, so
	it was always empty. The like paths write different stores:

	  POST/DELETE /v1/posts/:id/reactions  Scylla reactions_by_post
	                                       (web reels and Android use this)
	  POST /v1/posts/:id/like              Redis toggle + reactions_by_post
	  POST /v1/posts/:id/react             Postgres reactions + reactions_by_post
	  POST/DELETE /v1/reels/:id/react      Scylla reel_reactions

	reactions_by_post is the heart's state, but it is keyed by post and
	cannot be listed by viewer. Postgres reactions can (idx_reactions_user),
	and is already what /react writes and every post purge cleans up. So it
	is the per-viewer record: every path above now writes it too (the
	idempotent routes before their Scylla write, so a failure returns an
	error the client can retry; the /like toggle after its Redis toggle,
	logged on failure, since a retried toggle would flip back), and the
	list reads only it.

	Backfill: likes recorded before this change exist only in Scylla.
	A one-off copy of reactions_by_post into reactions (target_type 'post',
	reaction_type via postgres.IndexReactionType, created_at from the
	Scylla row) brings them into the list; until it runs, the list starts
	from this deploy.

	The page: ids only, in liked order (newest first), each a short-form
	post the viewer may still open: live (not deleted, not scheduled,
	approved) in SQL, then the post detail's read gate (audience, the
	author's privacy and hidden state, the age gate; fail closed) on each
	id. A filtered page can be short with a next_cursor; the client follows
	the cursor until it is empty.
*/

// likedReelsStore is the store slice the liked list and the like index
// writes need; an interface so they are testable without a database.
type likedReelsStore interface {
	UpsertPostReaction(ctx context.Context, postID, userID uuid.UUID, reaction string) error
	DeletePostReaction(ctx context.Context, postID, userID uuid.UUID) error
	ListLikedShortFormPosts(ctx context.Context, userID uuid.UUID, limit int, after *postgres.LikedCursor) ([]postgres.LikedPost, error)
}

const (
	// DefaultLikedReelsLimit is the page size when ?limit= is absent.
	DefaultLikedReelsLimit = 50
	// MaxLikedReelsLimit caps ?limit=: every id runs the read gate.
	MaxLikedReelsLimit = 100
	// likedReelsMaxRounds bounds how many batches one page reads to fill
	// itself when the read gate drops rows.
	likedReelsMaxRounds = 4
)

// ErrInvalidLikedCursor is ?cursor= that is not a next_cursor this route gave.
var ErrInvalidLikedCursor = errors.New("invalid cursor")

// NormalizeLikedReelsLimit resolves ?limit=: absent or unreadable is the
// default, below 1 is the default, above the cap is the cap.
func NormalizeLikedReelsLimit(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return DefaultLikedReelsLimit
	}
	if n > MaxLikedReelsLimit {
		return MaxLikedReelsLimit
	}
	return n
}

// ListLikedReels is GET /v1/reels/liked: the ids of the short-form posts
// the viewer liked and may still open, newest like first, and the cursor
// of the next page ("" when there is none).
func (s *Service) ListLikedReels(ctx context.Context, viewerID uuid.UUID, limit int, cursor string) ([]string, string, error) {
	if s.likedReels == nil {
		return nil, "", ErrAuthoringStoreUnavailable
	}
	if limit < 1 || limit > MaxLikedReelsLimit {
		limit = DefaultLikedReelsLimit
	}
	after, err := postgres.ParseLikedCursor(cursor)
	if err != nil {
		return nil, "", ErrInvalidLikedCursor
	}

	out := make([]string, 0, limit)
	batch := limit + 1
	var last *postgres.LikedCursor
	for round := 0; round < likedReelsMaxRounds; round++ {
		rows, err := s.likedReels.ListLikedShortFormPosts(ctx, viewerID, batch, after)
		if err != nil {
			return nil, "", err
		}
		ids := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			ids[i] = r.PostID
		}
		viewer := viewerID
		ok := s.viewablePostSet(ctx, ids, &viewer)
		for _, r := range rows {
			if len(out) == limit {
				// A full page with rows still behind it.
				return out, postgres.EncodeLikedCursor(*last), nil
			}
			last = &postgres.LikedCursor{LikedAt: r.LikedAt, PostID: r.PostID}
			if ok[r.PostID] {
				out = append(out, r.PostID.String())
			}
		}
		if len(rows) < batch {
			return out, "", nil // read to the end
		}
		after = last
	}
	// Out of rounds with rows left: a short page, and the place to go on.
	return out, postgres.EncodeLikedCursor(*last), nil
}

// indexReaction writes the viewer's like (set) or its removal to the
// per-viewer record the liked list reads. Nil-safe: no Postgres store, no
// index.
func (s *Service) indexReaction(ctx context.Context, postID, userID uuid.UUID, reaction string, set bool) error {
	if s.likedReels == nil {
		return nil
	}
	if set {
		return s.likedReels.UpsertPostReaction(ctx, postID, userID, reaction)
	}
	return s.likedReels.DeletePostReaction(ctx, postID, userID)
}

// indexReactionBestEffort is indexReaction for a toggle whose primary
// write has already happened and cannot be retried safely.
func (s *Service) indexReactionBestEffort(ctx context.Context, postID, userID uuid.UUID, reaction string, set bool) {
	if err := s.indexReaction(ctx, postID, userID, reaction, set); err != nil {
		log.Printf("Warning: failed to index reaction for the liked list (post %s): %v", postID, err)
	}
}
