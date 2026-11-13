package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// Notes timeline maintenance (cmd/timeline-maintenance, 2026-11-13).
//
// RebuildNotesTimeline puts one user's notes_timeline back to what the live
// writers would have produced: the user's own live notes and the recent
// live notes of every pushed account they follow are (re)written, and every
// row that should not be there is deleted. Writes come first, so the
// timeline is never empty part-way. A dry run (apply=false) only counts.
//
// The author purge (every row one author wrote, in every partition) is a
// token-range pass in the store: scylla.Maintainer.PurgeNotesAuthor.

// notesRebuildPerAuthor bounds the notes copied per followed account, as
// the follow backfill does (a rebuild is a backfill of every follow).
const notesRebuildPerAuthor = notesBackfillLimit

// notesRebuildScanPage is the page size of the stale sweep's timeline read.
const notesRebuildScanPage = 200

// NotesRebuildStats counts one rebuild.
type NotesRebuildStats struct {
	Following int // accounts the user follows
	Pulled    int // of those, pulled at read time (nothing written)
	Written   int // rows (re)written
	Scanned   int // rows read by the stale sweep
	Stale     int // rows deleted (or, in a dry run, that would be)
}

// notesRowStale decides whether one existing notes_timeline row of owner's
// stays. It goes when its note is unknown to echo_notes, deleted, or a
// reply; when its author is neither owner nor someone owner follows; or
// when it is someone else's note that would not be listed to followers.
// owner's own note stays at any visibility, as pushNote writes it.
func notesRowStale(owner, author uuid.UUID, meta postgres.NoteRow, found bool, follows map[uuid.UUID]struct{}) bool {
	if !found || meta.Deleted || !meta.TopLevel {
		return true
	}
	if author == owner {
		return false
	}
	if _, ok := follows[author]; !ok {
		return true
	}
	return !listableVisibility(meta.Visibility)
}

// RebuildNotesTimeline rebuilds userID's notes_timeline (see above). The
// follow list must be read in full first: without it nothing is written
// or deleted.
func (s *Service) RebuildNotesTimeline(ctx context.Context, userID uuid.UUID, apply bool) (NotesRebuildStats, error) {
	var st NotesRebuildStats
	if s.notesMeta == nil || s.notesTimelines == nil {
		return st, errNotesStoresMissing
	}
	if userID == uuid.Nil {
		return st, fmt.Errorf("notes rebuild: empty user id")
	}
	following, err := s.fetchFollowing(ctx, userID)
	if err != nil {
		return st, fmt.Errorf("notes rebuild: follow list unavailable, nothing changed: %w", err)
	}
	follows := make(map[uuid.UUID]struct{}, len(following))
	for _, id := range following {
		if id != userID {
			follows[id] = struct{}{}
		}
	}
	st.Following = len(follows)

	// 1. Write: the user's own notes, then each pushed followed account's.
	upper := time.Now().Add(time.Minute)
	write := func(author uuid.UUID) error {
		notes, err := s.notesMeta.LiveNotesByAuthors(ctx, []uuid.UUID{author}, upper, notesRebuildPerAuthor)
		if err != nil {
			return fmt.Errorf("notes rebuild: read notes of %s: %w", author, err)
		}
		for _, n := range notes {
			if apply {
				if err := s.notesTimelines.AddToNotesTimeline(ctx, userID, n.PostID, n.AuthorID, n.CreatedAt); err != nil {
					return fmt.Errorf("notes rebuild: write %s: %w", n.PostID, err)
				}
			}
			st.Written++
		}
		return nil
	}
	if err := write(userID); err != nil {
		return st, err
	}
	for author := range follows {
		pulled, err := s.notesAuthorPulled(ctx, author)
		if err != nil {
			return st, fmt.Errorf("notes rebuild: pull model check for %s: %w", author, err)
		}
		if pulled {
			st.Pulled++
			continue // read live at Following time
		}
		if err := write(author); err != nil {
			return st, err
		}
	}

	// 2. Sweep: every existing row, newest first, judged by notesRowStale.
	before := ""
	for {
		rows, err := s.notesTimelines.GetNotesTimelineBefore(ctx, userID, before, notesRebuildScanPage)
		if err != nil {
			return st, fmt.Errorf("notes rebuild: read timeline: %w", err)
		}
		if len(rows) == 0 {
			return st, nil
		}
		for _, r := range rows {
			st.Scanned++
			if err := s.sweepNotesRow(ctx, userID, r, follows, apply, &st); err != nil {
				return st, err
			}
		}
		next := rows[len(rows)-1].CursorToken
		if next == "" || next == before {
			return st, nil
		}
		before = next
	}
}

func (s *Service) sweepNotesRow(ctx context.Context, userID uuid.UUID, r scylla.FeedItem, follows map[uuid.UUID]struct{}, apply bool, st *NotesRebuildStats) error {
	meta, found, err := s.notesMeta.NoteMeta(ctx, r.PostID)
	if err != nil {
		return fmt.Errorf("notes rebuild: read note %s: %w", r.PostID, err)
	}
	if !notesRowStale(userID, r.AuthorID, meta, found, follows) {
		return nil
	}
	st.Stale++
	if !apply {
		return nil
	}
	if err := s.notesTimelines.DeleteNotesTimelineRow(ctx, userID, r.PostID, r.CursorToken); err != nil {
		return fmt.Errorf("notes rebuild: delete %s: %w", r.PostID, err)
	}
	return nil
}
