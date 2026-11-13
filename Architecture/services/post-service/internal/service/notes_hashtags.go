package service

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Followed-hashtag notes for feed-service's Echo For You (2026-11-13).
//
//	GET /internal/users/:userId/followed-hashtag-notes?since=<RFC3339>&limit=
//	  -> {"note_ids": ["<uuid>", …]}
//
// The ids of recent live public top-level notes carrying any tag the user
// follows (migration 074), never the user's own, newest first. Internal
// key only; the gateway never forwards /internal. feed-service keeps only
// ids it recorded as live public top-level notes (echo_notes) and runs its
// block, mute, hidden-author, keyword and privacy filters on them, exactly
// as on its other candidates.

const (
	// FollowedHashtagNotesDefaultLimit / Max bound one read.
	FollowedHashtagNotesDefaultLimit = 100
	FollowedHashtagNotesMaxLimit     = 200
	// FollowedHashtagNotesMaxWindow is the oldest `since` accepted; an
	// older or missing one is clamped to it.
	FollowedHashtagNotesMaxWindow = 7 * 24 * time.Hour
)

// followedHashtagNoteStore is the slice of *postgres.Store the read uses.
type followedHashtagNoteStore interface {
	FollowedHashtagTags(ctx context.Context, userID uuid.UUID) ([]string, error)
	FollowedHashtagNoteIDs(ctx context.Context, tags []string, viewerID uuid.UUID, since time.Time, limit int) ([]uuid.UUID, error)
}

// WithFollowedHashtagNoteStore installs a stand-in store (tests).
func (s *Service) WithFollowedHashtagNoteStore(st followedHashtagNoteStore) *Service {
	s.noteHashtagSeam = st
	return s
}

func (s *Service) followedHashtagNoteStore() (followedHashtagNoteStore, error) {
	if s.noteHashtagSeam != nil {
		return s.noteHashtagSeam, nil
	}
	if s.pgStore != nil {
		return s.pgStore, nil
	}
	return nil, ErrNoteStoreUnavailable
}

// clampFollowedHashtagNotes applies the read's bounds to limit and since.
func clampFollowedHashtagNotes(limit int, since, now time.Time) (int, time.Time) {
	if limit <= 0 {
		limit = FollowedHashtagNotesDefaultLimit
	}
	if limit > FollowedHashtagNotesMaxLimit {
		limit = FollowedHashtagNotesMaxLimit
	}
	if oldest := now.Add(-FollowedHashtagNotesMaxWindow); since.Before(oldest) {
		since = oldest
	}
	return limit, since
}

// FollowedHashtagNoteIDs answers the internal route. A user who follows no
// tag gets an empty list without a posts query.
func (s *Service) FollowedHashtagNoteIDs(ctx context.Context, userID uuid.UUID, since time.Time, limit int) ([]uuid.UUID, error) {
	st, err := s.followedHashtagNoteStore()
	if err != nil {
		return nil, err
	}
	tags, err := st.FollowedHashtagTags(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return []uuid.UUID{}, nil
	}
	limit, since = clampFollowedHashtagNotes(limit, since, s.clock())
	return st.FollowedHashtagNoteIDs(ctx, tags, userID, since, limit)
}
