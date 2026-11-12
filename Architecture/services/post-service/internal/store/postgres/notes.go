package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Echo notes (docs/designs/echo-contract.md, migration 082).
//
// A note is a posts row with content_type = 'note' plus the conversation
// columns. Every read and write of those columns lives in this file; the
// shared postCols projection is deliberately left alone, so no existing
// read changes shape or depends on migration 082.
//
// Notes appear in no existing listing. Each listing query in this package
// carries NotNoteSQL (notes_listing_guard_test.go pins every one); the
// note reads below are the only listings that return them.

// NoteContentType is the content_type of every note row.
const NoteContentType = "note"

// NotNoteSQL is the listing predicate that keeps notes out, qualified with
// alias ("p" → "p.content_type <> 'note'").
func NotNoteSQL(alias string) string {
	if alias == "" {
		return "content_type <> '" + NoteContentType + "'"
	}
	return alias + ".content_type <> '" + NoteContentType + "'"
}

// NoteFields are the conversation columns of a note row.
type NoteFields struct {
	ReplyToID      *uuid.UUID
	ConversationID *uuid.UUID
	QuoteOfID      *uuid.UUID
	ReplyPolicy    string
	ReplyCount     int
	EditedAt       *time.Time
}

// Note is a note row: the post and its conversation columns.
type Note struct {
	Post Post
	NoteFields
}

// noteCols is postCols plus the conversation columns, unqualified.
const noteCols = postCols + `,
	reply_to_id, conversation_id, reply_policy, reply_count, quote_of_id, edited_at`

func scanNoteRows(rows pgx.Rows, extra ...any) ([]Note, error) {
	var out []Note
	for rows.Next() {
		var n Note
		dest := append(postScanDestinations(&n.Post),
			&n.ReplyToID, &n.ConversationID, &n.ReplyPolicy, &n.ReplyCount, &n.QuoteOfID, &n.EditedAt)
		dest = append(dest, extra...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		n.Post.deriveScheduled()
		n.Post.deriveAudio()
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) attachNoteMedia(ctx context.Context, notes []Note) error {
	if len(notes) == 0 {
		return nil
	}
	posts := make([]Post, len(notes))
	for i := range notes {
		posts[i] = notes[i].Post
	}
	if err := s.attachPostMedia(ctx, posts); err != nil {
		return err
	}
	for i := range notes {
		notes[i].Post.Media = posts[i].Media
	}
	return nil
}

// GetNotes returns the live (not deleted) notes among ids, whatever their
// review state: the service gates every read. A row that is not a note is
// absent, exactly like a missing one.
func (s *Store) GetNotes(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*Note, error) {
	out := make(map[uuid.UUID]*Note, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `SELECT `+noteCols+`
		FROM posts
		WHERE id = ANY($1) AND content_type = '`+NoteContentType+`' AND deleted_at IS NULL`, ids)
	if err != nil {
		return nil, fmt.Errorf("get notes: %w", err)
	}
	notes, err := scanNoteRows(rows)
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("scan notes: %w", err)
	}
	if err := s.attachNoteMedia(ctx, notes); err != nil {
		return nil, err
	}
	for i := range notes {
		n := notes[i]
		out[n.Post.ID] = &n
	}
	return out, nil
}

// GetNoteThread returns the live entries of the note thread rooted at
// rootID in thread order (the thread columns, migration 027).
func (s *Store) GetNoteThread(ctx context.Context, rootID uuid.UUID) ([]Note, error) {
	rows, err := s.db.Query(ctx, `SELECT `+noteCols+`
		FROM posts
		WHERE thread_root_id = $1 AND content_type = '`+NoteContentType+`' AND deleted_at IS NULL
		ORDER BY thread_seq ASC`, rootID)
	if err != nil {
		return nil, fmt.Errorf("get note thread: %w", err)
	}
	notes, err := scanNoteRows(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	return notes, s.attachNoteMedia(ctx, notes)
}

// NoteListTab is a profile Echo tab (GET /v1/notes/users/:userId?tab=).
type NoteListTab string

const (
	NoteTabNotes   NoteListTab = "notes"
	NoteTabReplies NoteListTab = "replies"
	NoteTabReposts NoteListTab = "reposts"
)

// NoteListQuery is one page of a note listing. Exactly one of ParentID
// (direct replies, oldest first) and AuthorID (a profile tab, newest
// first) is set.
type NoteListQuery struct {
	ParentID *uuid.UUID
	AuthorID *uuid.UUID
	Tab      NoteListTab
	// IncludeOwn keeps the author's own not-yet-approved rows (the
	// author reading their own profile).
	IncludeOwn bool
	Limit      int
	Cursor     string
}

// noteListSQL builds a note listing's query: pure, so the filters are
// pinned without a database. Every branch keeps content_type = 'note'.
func noteListSQL(q NoteListQuery) (string, []any) {
	args := []any{q.Limit + 1}
	eligible := ViewerEligibleSQL("p")
	if q.IncludeOwn {
		eligible = "p.deleted_at IS NULL AND p.publish_at IS NULL"
	}
	cursorAt, cursorKey, hasCursor := decodeKeysetCursor(q.Cursor)
	var cursorID uuid.UUID
	if hasCursor {
		id, err := uuid.Parse(cursorKey)
		if err != nil {
			hasCursor = false
		} else {
			cursorID = id
		}
	}
	cols := qualifiedCols("p", noteCols)

	switch {
	case q.ParentID != nil:
		args = append(args, *q.ParentID)
		query := `SELECT ` + cols + `, p.created_at
			FROM posts p
			WHERE p.content_type = '` + NoteContentType + `' AND p.reply_to_id = $2 AND ` + eligible
		if hasCursor {
			args = append(args, cursorAt, cursorID)
			query += fmt.Sprintf(` AND (p.created_at, p.id) > ($%d, $%d)`, len(args)-1, len(args))
		}
		return query + ` ORDER BY p.created_at ASC, p.id ASC LIMIT $1`, args

	case q.Tab == NoteTabReposts:
		args = append(args, *q.AuthorID)
		query := `SELECT ` + cols + `, r.created_at
			FROM post_reposts r
			JOIN posts p ON p.id = r.original_post_id
			WHERE r.user_id = $2 AND r.status = 'active' AND r.repost_type = 'plain'
			  AND p.content_type = '` + NoteContentType + `' AND ` + ViewerEligibleSQL("p")
		if hasCursor {
			args = append(args, cursorAt, cursorID)
			query += fmt.Sprintf(` AND (r.created_at, p.id) < ($%d, $%d)`, len(args)-1, len(args))
		}
		return query + ` ORDER BY r.created_at DESC, p.id DESC LIMIT $1`, args

	default:
		args = append(args, *q.AuthorID)
		query := `SELECT ` + cols + `, p.created_at
			FROM posts p
			WHERE p.content_type = '` + NoteContentType + `' AND p.author_id = $2 AND ` + eligible
		if q.Tab == NoteTabReplies {
			query += ` AND p.reply_to_id IS NOT NULL`
		} else {
			query += ` AND p.reply_to_id IS NULL`
		}
		if hasCursor {
			args = append(args, cursorAt, cursorID)
			query += fmt.Sprintf(` AND (p.created_at, p.id) < ($%d, $%d)`, len(args)-1, len(args))
		}
		return query + ` ORDER BY p.created_at DESC, p.id DESC LIMIT $1`, args
	}
}

// qualifiedCols prefixes every column of a comma list with alias.
func qualifiedCols(alias, cols string) string {
	out := make([]byte, 0, len(cols)+64)
	for i, c := range splitCols(cols) {
		if i > 0 {
			out = append(out, ", "...)
		}
		out = append(out, alias+"."+c...)
	}
	return string(out)
}

func splitCols(cols string) []string {
	var out []string
	cur := make([]byte, 0, 32)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = cur[:0]
		}
	}
	for i := 0; i < len(cols); i++ {
		switch c := cols[i]; c {
		case ',':
			flush()
		case ' ', '\n', '\t', '\r':
		default:
			cur = append(cur, c)
		}
	}
	flush()
	return out
}

// ListNotes answers one page of q (limit clamped to 1..50, default 20).
func (s *Store) ListNotes(ctx context.Context, q NoteListQuery) ([]Note, string, error) {
	if q.ParentID == nil && q.AuthorID == nil {
		return nil, "", errors.New("list notes: no parent and no author")
	}
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Limit > 50 {
		q.Limit = 50
	}
	query, args := noteListSQL(q)
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list notes: %w", err)
	}
	var keys []time.Time
	notes := make([]Note, 0, q.Limit+1)
	for rows.Next() {
		var n Note
		var key time.Time
		dest := append(postScanDestinations(&n.Post),
			&n.ReplyToID, &n.ConversationID, &n.ReplyPolicy, &n.ReplyCount, &n.QuoteOfID, &n.EditedAt, &key)
		if err := rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, "", fmt.Errorf("scan notes: %w", err)
		}
		n.Post.deriveScheduled()
		n.Post.deriveAudio()
		notes = append(notes, n)
		keys = append(keys, key)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(notes) > q.Limit {
		last := q.Limit - 1
		next = encodeKeysetCursor(keys[last], notes[last].Post.ID.String())
		notes = notes[:q.Limit]
	}
	if err := s.attachNoteMedia(ctx, notes); err != nil {
		return nil, "", err
	}
	return notes, next, nil
}

// NoteMediaRow is what the note media rule reads about one asset.
type NoteMediaRow struct {
	UploaderID       uuid.UUID
	Kind             string
	MimeType         string
	ProcessingStatus string
	ModerationStatus string
}

// NoteMedia reads kind, MIME type and state for ids in one round trip.
// Missing rows are absent from the map.
func (s *Store) NoteMedia(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]NoteMediaRow, error) {
	out := make(map[uuid.UUID]NoteMediaRow, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, uploader_id, file_type, COALESCE(mime_type, ''),
		       COALESCE(processing_status, ''), COALESCE(moderation_status, '')
		FROM media_assets WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("note media: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var m NoteMediaRow
		if err := rows.Scan(&id, &m.UploaderID, &m.Kind, &m.MimeType, &m.ProcessingStatus, &m.ModerationStatus); err != nil {
			return nil, fmt.Errorf("note media scan: %w", err)
		}
		out[id] = m
	}
	return out, rows.Err()
}

// NoteWrite is one note create (a single note, or every entry of a thread)
// with everything that commits with it.
type NoteWrite struct {
	// Notes are inserted in order; Fields[i] belongs to Notes[i].
	Notes  []*Post
	Fields []NoteFields
	// Idem is the durable claim, keyed on the FIRST note's id.
	Idem *CreateIdempotency
	// ReplyParent, when set, gains one reply_count.
	ReplyParent *uuid.UUID
	// QuoteRepost, when set, is the quote repost row the quote writes
	// (post_reposts, repost_type 'quote'); an active repost of the same
	// target by the same user already holds the slot and is kept.
	QuoteRepost *Repost
	// Events are written to the outbox in the same transaction.
	Events []ThreadOutboxEvent
}

// NoteWriteResult reports what CreateNotes wrote beyond the notes.
type NoteWriteResult struct {
	// QuoteRepostWritten is true when the quote repost row was inserted
	// (false when an active repost of the target already existed).
	QuoteRepostWritten bool
}

// CreateNotes writes w in ONE transaction: the idempotency claim, every
// note row (insertPostTx, then its conversation columns), the parent's
// reply_count, the quote repost row and the outbox events. A replay or a
// reused key is reported as ErrCreateKeyReplay / ErrCreateKeyConflict
// exactly as CreatePostWithEventIdempotent does.
func (s *Store) CreateNotes(ctx context.Context, w *NoteWrite) (*NoteWriteResult, error) {
	if len(w.Notes) == 0 || len(w.Notes) != len(w.Fields) {
		return nil, errors.New("create notes: notes and fields disagree")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if idem := w.Idem; idem != nil {
		var claimed uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO post_create_idempotency (actor_id, client_key, fingerprint, post_id)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (actor_id, client_key) DO NOTHING
			RETURNING post_id`,
			idem.ActorID, idem.ClientKey, idem.Fingerprint, w.Notes[0].ID).Scan(&claimed)
		if errors.Is(err, pgx.ErrNoRows) {
			var existingID uuid.UUID
			var existingFingerprint string
			if err := tx.QueryRow(ctx, `
				SELECT post_id, fingerprint FROM post_create_idempotency
				WHERE actor_id = $1 AND client_key = $2`,
				idem.ActorID, idem.ClientKey).Scan(&existingID, &existingFingerprint); err != nil {
				return nil, fmt.Errorf("read existing create claim: %w", err)
			}
			if existingFingerprint != idem.Fingerprint {
				return nil, ErrCreateKeyConflict
			}
			return nil, ErrCreateKeyReplay{PostID: existingID}
		}
		if err != nil {
			return nil, fmt.Errorf("claim create idempotency: %w", err)
		}
	}

	for i, p := range w.Notes {
		if p.ContentType != NoteContentType {
			return nil, fmt.Errorf("create notes: entry %d is %q, not a note", i, p.ContentType)
		}
		if err := insertPostTx(ctx, tx, p); err != nil {
			return nil, err
		}
		f := w.Fields[i]
		policy := f.ReplyPolicy
		if policy == "" {
			policy = "everyone"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE posts SET reply_to_id = $2, conversation_id = $3, quote_of_id = $4, reply_policy = $5,
				reply_count = $6
			WHERE id = $1`, p.ID, f.ReplyToID, f.ConversationID, f.QuoteOfID, policy, f.ReplyCount); err != nil {
			return nil, fmt.Errorf("note fields: %w", err)
		}
	}

	if w.ReplyParent != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE posts SET reply_count = reply_count + 1
			WHERE id = $1 AND content_type = '`+NoteContentType+`'`, *w.ReplyParent); err != nil {
			return nil, fmt.Errorf("reply count: %w", err)
		}
	}

	res := &NoteWriteResult{}
	if r := w.QuoteRepost; r != nil {
		r.ID = uuid.New()
		r.Status = "active"
		now := time.Now()
		r.CreatedAt, r.UpdatedAt = now, now
		var qt *string
		if r.QuoteText != "" {
			qt = &r.QuoteText
		}
		var id uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO post_reposts (id, user_id, original_post_id, repost_type, quote_text,
				visibility, status, created_at, updated_at)
			VALUES ($1, $2, $3, 'quote', $4, $5, 'active', $6, $6)
			ON CONFLICT DO NOTHING
			RETURNING id`,
			r.ID, r.UserID, r.OriginalPostID, qt, r.Visibility, now).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return nil, fmt.Errorf("quote repost: %w", err)
		default:
			r.RepostType = "quote"
			res.QuoteRepostWritten = true
		}
	}

	for _, evt := range w.Events {
		if err := InsertOutboxEventTx(ctx, tx, evt.EventType, "post", evt.PostID, evt.Payload); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return res, nil
}

// NoteEdit is a PATCH /v1/notes/:id: nil leaves a field as it is.
type NoteEdit struct {
	Text        *string
	Hashtags    []string
	Mentions    []string
	ReplyPolicy *string
	EditedAt    time.Time
}

// UpdateNote applies e to the author's live note. Text (with its hashtags
// and mentions) also stamps edited_at. false = no such live note of
// authorID's.
func (s *Store) UpdateNote(ctx context.Context, id, authorID uuid.UUID, e NoteEdit) (bool, error) {
	hashtags, mentions := e.Hashtags, e.Mentions
	if hashtags == nil {
		hashtags = []string{}
	}
	if mentions == nil {
		mentions = []string{}
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE posts SET
			text              = COALESCE($3, text),
			hashtags          = CASE WHEN $3::text IS NULL THEN hashtags ELSE $4::text[] END,
			mention_usernames = CASE WHEN $3::text IS NULL THEN mention_usernames ELSE $5::text[] END,
			edited_at         = CASE WHEN $3::text IS NULL THEN edited_at ELSE $6 END,
			reply_policy      = COALESCE($7, reply_policy),
			updated_at        = NOW()
		WHERE id = $1 AND author_id = $2 AND content_type = '`+NoteContentType+`' AND deleted_at IS NULL`,
		id, authorID, e.Text, hashtags, mentions, e.EditedAt, e.ReplyPolicy)
	if err != nil {
		return false, fmt.Errorf("update note: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DecrementNoteReplyCount takes one reply off parentID (never below zero).
func (s *Store) DecrementNoteReplyCount(ctx context.Context, parentID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE posts SET reply_count = GREATEST(reply_count - 1, 0)
		WHERE id = $1 AND content_type = '`+NoteContentType+`'`, parentID)
	return err
}
