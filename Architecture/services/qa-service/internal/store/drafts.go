package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// QuestionDraft is the server-backed save & resume row for a question.
type QuestionDraft struct {
	ID          uuid.UUID   `json:"id"`
	AuthorID    uuid.UUID   `json:"author_id"`
	CommunityID *uuid.UUID  `json:"community_id,omitempty"`
	Title       string      `json:"title"`
	Body        string      `json:"body"`
	Tags        []string    `json:"tags"`
	TopicIDs    []uuid.UUID `json:"topic_ids"`
	IsAnonymous bool        `json:"is_anonymous"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// AnswerDraft is the server-backed save & resume row for an answer.
type AnswerDraft struct {
	ID          uuid.UUID `json:"id"`
	AuthorID    uuid.UUID `json:"author_id"`
	QuestionID  uuid.UUID `json:"question_id"`
	Body        string    `json:"body"`
	IsAnonymous bool      `json:"is_anonymous"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// UpsertQuestionDraftParams is the input for InsertQuestionDraft / UpdateQuestionDraft.
// When ID is nil, a new row is inserted; otherwise the row is updated only if it
// belongs to the supplied authorID.
type UpsertQuestionDraftParams struct {
	ID          *uuid.UUID
	AuthorID    uuid.UUID
	CommunityID *uuid.UUID
	Title       string
	Body        string
	Tags        []string
	TopicIDs    []uuid.UUID
	IsAnonymous bool
}

type UpsertAnswerDraftParams struct {
	ID          *uuid.UUID
	AuthorID    uuid.UUID
	QuestionID  uuid.UUID
	Body        string
	IsAnonymous bool
}

func (s *Store) UpsertQuestionDraft(ctx context.Context, p UpsertQuestionDraftParams) (*QuestionDraft, error) {
	tags := p.Tags
	if tags == nil {
		tags = []string{}
	}
	topicIDs := p.TopicIDs
	if topicIDs == nil {
		topicIDs = []uuid.UUID{}
	}

	d := &QuestionDraft{}
	if p.ID != nil {
		// Update path — only succeeds when (id, author_id) matches.
		err := s.db.QueryRow(ctx, `
			UPDATE question_drafts
			SET community_id = $3, title = $4, body = $5, tags = $6, topic_ids = $7, is_anonymous = $8, updated_at = now()
			WHERE id = $1 AND author_id = $2
			RETURNING id, author_id, community_id, title, body, tags, topic_ids, is_anonymous, created_at, updated_at`,
			*p.ID, p.AuthorID, p.CommunityID, p.Title, p.Body, tags, topicIDs, p.IsAnonymous,
		).Scan(&d.ID, &d.AuthorID, &d.CommunityID, &d.Title, &d.Body, &d.Tags, &d.TopicIDs, &d.IsAnonymous, &d.CreatedAt, &d.UpdatedAt)
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("not_found: draft not found")
		}
		if err != nil {
			return nil, fmt.Errorf("update question draft: %w", err)
		}
		return d, nil
	}

	err := s.db.QueryRow(ctx, `
		INSERT INTO question_drafts (author_id, community_id, title, body, tags, topic_ids, is_anonymous)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, author_id, community_id, title, body, tags, topic_ids, is_anonymous, created_at, updated_at`,
		p.AuthorID, p.CommunityID, p.Title, p.Body, tags, topicIDs, p.IsAnonymous,
	).Scan(&d.ID, &d.AuthorID, &d.CommunityID, &d.Title, &d.Body, &d.Tags, &d.TopicIDs, &d.IsAnonymous, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert question draft: %w", err)
	}
	return d, nil
}

func (s *Store) ListQuestionDrafts(ctx context.Context, authorID uuid.UUID, limit, offset int) ([]QuestionDraft, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, community_id, title, body, tags, topic_ids, is_anonymous, created_at, updated_at
		FROM question_drafts
		WHERE author_id = $1
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3`,
		authorID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []QuestionDraft
	for rows.Next() {
		var d QuestionDraft
		if err := rows.Scan(&d.ID, &d.AuthorID, &d.CommunityID, &d.Title, &d.Body, &d.Tags, &d.TopicIDs, &d.IsAnonymous, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		results = append(results, d)
	}
	return results, rows.Err()
}

// DeleteQuestionDraft 404s when the draft is missing or doesn't belong to authorID.
func (s *Store) DeleteQuestionDraft(ctx context.Context, draftID, authorID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM question_drafts WHERE id = $1 AND author_id = $2`, draftID, authorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not_found: draft not found")
	}
	return nil
}

func (s *Store) UpsertAnswerDraft(ctx context.Context, p UpsertAnswerDraftParams) (*AnswerDraft, error) {
	d := &AnswerDraft{}
	// Conflict on (author_id, question_id) regardless of whether the caller
	// supplies an explicit ID — the unique index in setup.sql means there is
	// at most one draft per author per question.
	err := s.db.QueryRow(ctx, `
		INSERT INTO answer_drafts (author_id, question_id, body, is_anonymous)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (author_id, question_id) DO UPDATE SET
			body = EXCLUDED.body,
			is_anonymous = EXCLUDED.is_anonymous,
			updated_at = now()
		RETURNING id, author_id, question_id, body, is_anonymous, created_at, updated_at`,
		p.AuthorID, p.QuestionID, p.Body, p.IsAnonymous,
	).Scan(&d.ID, &d.AuthorID, &d.QuestionID, &d.Body, &d.IsAnonymous, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert answer draft: %w", err)
	}
	return d, nil
}

func (s *Store) ListAnswerDrafts(ctx context.Context, authorID uuid.UUID, limit, offset int) ([]AnswerDraft, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, question_id, body, is_anonymous, created_at, updated_at
		FROM answer_drafts
		WHERE author_id = $1
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3`,
		authorID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []AnswerDraft
	for rows.Next() {
		var d AnswerDraft
		if err := rows.Scan(&d.ID, &d.AuthorID, &d.QuestionID, &d.Body, &d.IsAnonymous, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		results = append(results, d)
	}
	return results, rows.Err()
}

func (s *Store) DeleteAnswerDraft(ctx context.Context, draftID, authorID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM answer_drafts WHERE id = $1 AND author_id = $2`, draftID, authorID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("not_found: draft not found")
	}
	return nil
}
