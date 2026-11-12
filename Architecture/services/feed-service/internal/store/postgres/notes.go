package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Echo notes metadata (migration 009). See internal/service/notes.go for
// how each question is used.

// NoteRow is one echo_notes row.
type NoteRow struct {
	PostID     uuid.UUID
	AuthorID   uuid.UUID
	CreatedAt  time.Time
	TopLevel   bool
	Visibility string
	Deleted    bool
}

// RecordNote stores a note seen on PostCreated. A redelivery keeps the row
// it already has: top_level and created_at never change after creation.
func (s *MetaStore) RecordNote(ctx context.Context, n NoteRow) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO echo_notes (post_id, author_id, created_at, top_level, visibility)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (post_id) DO NOTHING`,
		n.PostID, n.AuthorID, n.CreatedAt, n.TopLevel, n.Visibility)
	return err
}

// NoteMeta returns the note's row; found is false when feed-service never
// recorded it.
func (s *MetaStore) NoteMeta(ctx context.Context, postID uuid.UUID) (NoteRow, bool, error) {
	var n NoteRow
	err := s.db.QueryRow(ctx, `
		SELECT post_id, author_id, created_at, top_level, visibility, deleted
		FROM echo_notes WHERE post_id = $1`, postID).
		Scan(&n.PostID, &n.AuthorID, &n.CreatedAt, &n.TopLevel, &n.Visibility, &n.Deleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return NoteRow{}, false, nil
	}
	if err != nil {
		return NoteRow{}, false, err
	}
	return n, true, nil
}

// SetNoteDeleted follows PostDeleted (true) and PostRestored (false). A
// post that is not a recorded note matches no row.
func (s *MetaStore) SetNoteDeleted(ctx context.Context, postID uuid.UUID, deleted bool) error {
	_, err := s.db.Exec(ctx, `UPDATE echo_notes SET deleted = $2 WHERE post_id = $1`, postID, deleted)
	return err
}

// SetNoteVisibility follows post.visibility_changed.
func (s *MetaStore) SetNoteVisibility(ctx context.Context, postID uuid.UUID, visibility string) error {
	_, err := s.db.Exec(ctx, `UPDATE echo_notes SET visibility = $2 WHERE post_id = $1`, postID, visibility)
	return err
}

// LiveNotesByAuthors returns the live (top-level, not deleted, public)
// notes of the given authors created strictly before `before`, newest
// first, at most limit.
func (s *MetaStore) LiveNotesByAuthors(ctx context.Context, authorIDs []uuid.UUID, before time.Time, limit int) ([]NoteRow, error) {
	if len(authorIDs) == 0 || limit <= 0 {
		return nil, nil
	}
	return s.noteRows(ctx, `
		SELECT post_id, author_id, created_at, top_level, visibility, deleted
		FROM echo_notes
		WHERE author_id = ANY($1) AND top_level AND NOT deleted AND visibility = 'public'
		  AND created_at < $2
		ORDER BY created_at DESC
		LIMIT $3`, authorIDs, before, limit)
}

// RecentLiveNotes returns live public top-level notes created at or after
// since, newest first, at most limit: For You's fast-rising pool.
func (s *MetaStore) RecentLiveNotes(ctx context.Context, since time.Time, limit int) ([]NoteRow, error) {
	if limit <= 0 {
		return nil, nil
	}
	return s.noteRows(ctx, `
		SELECT post_id, author_id, created_at, top_level, visibility, deleted
		FROM echo_notes
		WHERE top_level AND NOT deleted AND visibility = 'public' AND created_at >= $1
		ORDER BY created_at DESC
		LIMIT $2`, since, limit)
}

// LiveNotesAmong returns the subset of postIDs that are live public
// top-level notes: For You's liked-or-reposted-by-people-you-follow source
// keeps only these.
func (s *MetaStore) LiveNotesAmong(ctx context.Context, postIDs []uuid.UUID) ([]NoteRow, error) {
	if len(postIDs) == 0 {
		return nil, nil
	}
	return s.noteRows(ctx, `
		SELECT post_id, author_id, created_at, top_level, visibility, deleted
		FROM echo_notes
		WHERE post_id = ANY($1) AND top_level AND NOT deleted AND visibility = 'public'`, postIDs)
}

func (s *MetaStore) noteRows(ctx context.Context, sql string, args ...any) ([]NoteRow, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NoteRow
	for rows.Next() {
		var n NoteRow
		if err := rows.Scan(&n.PostID, &n.AuthorID, &n.CreatedAt, &n.TopLevel, &n.Visibility, &n.Deleted); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkNotesPullAuthor records that an author's notes are pulled at read
// time (their follower count passed ECHO_FANOUT_MAX_FOLLOWERS).
func (s *MetaStore) MarkNotesPullAuthor(ctx context.Context, authorID uuid.UUID, followers int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO echo_pull_authors (author_id, follower_count, flagged_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (author_id) DO UPDATE
		SET follower_count = EXCLUDED.follower_count, flagged_at = NOW()`, authorID, followers)
	return err
}

// IsNotesPullAuthor reports whether the author's notes are pulled.
func (s *MetaStore) IsNotesPullAuthor(ctx context.Context, authorID uuid.UUID) (bool, error) {
	var one int
	err := s.db.QueryRow(ctx, `SELECT 1 FROM echo_pull_authors WHERE author_id = $1`, authorID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// NotesPullAuthorsAmong returns the subset of authorIDs whose notes are
// pulled (echo_pull_authors).
func (s *MetaStore) NotesPullAuthorsAmong(ctx context.Context, authorIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(authorIDs) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `SELECT author_id FROM echo_pull_authors WHERE author_id = ANY($1)`, authorIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
