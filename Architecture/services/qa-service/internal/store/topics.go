package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type CreateTopicParams struct {
	Name          string     `json:"name"`
	Slug          string     `json:"slug"`
	Description   string     `json:"description"`
	IconURL       string     `json:"icon_url"`
	ParentTopicID *uuid.UUID `json:"parent_topic_id,omitempty"`
	IsFeatured    bool       `json:"is_featured"`
}

func (s *Store) CreateTopic(ctx context.Context, p CreateTopicParams) (*Topic, error) {
	t := &Topic{}
	slug := p.Slug
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(p.Name, " ", "-"))
	}
	err := s.db.QueryRow(ctx, `
		INSERT INTO topics (name, slug, description, icon_url, parent_topic_id, is_featured)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, slug, description, icon_url, parent_topic_id,
		          question_count, follower_count, is_featured, created_at`,
		p.Name, slug, p.Description, p.IconURL, p.ParentTopicID, p.IsFeatured,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.IconURL, &t.ParentTopicID,
		&t.QuestionCount, &t.FollowerCount, &t.IsFeatured, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create topic: %w", err)
	}
	return t, nil
}

func (s *Store) GetTopic(ctx context.Context, topicID uuid.UUID) (*Topic, error) {
	t := &Topic{}
	err := s.db.QueryRow(ctx, `
		SELECT id, name, slug, description, icon_url, parent_topic_id,
		       question_count, follower_count, is_featured, created_at
		FROM topics WHERE id = $1`, topicID,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.IconURL, &t.ParentTopicID,
		&t.QuestionCount, &t.FollowerCount, &t.IsFeatured, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get topic: %w", err)
	}
	return t, nil
}

func (s *Store) GetTopicBySlug(ctx context.Context, slug string) (*Topic, error) {
	t := &Topic{}
	err := s.db.QueryRow(ctx, `
		SELECT id, name, slug, description, icon_url, parent_topic_id,
		       question_count, follower_count, is_featured, created_at
		FROM topics WHERE slug = $1`, slug,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.IconURL, &t.ParentTopicID,
		&t.QuestionCount, &t.FollowerCount, &t.IsFeatured, &t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get topic by slug: %w", err)
	}
	return t, nil
}

func (s *Store) ListTopics(ctx context.Context, limit, offset int, featuredOnly bool) ([]Topic, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, name, slug, description, icon_url, parent_topic_id,
	                 question_count, follower_count, is_featured, created_at
	          FROM topics`
	args := []any{}
	if featuredOnly {
		query += ` WHERE is_featured = true`
	}
	query += ` ORDER BY question_count DESC LIMIT $1 OFFSET $2`
	if featuredOnly {
		args = append(args, limit, offset)
		query = `SELECT id, name, slug, description, icon_url, parent_topic_id,
	                 question_count, follower_count, is_featured, created_at
	          FROM topics WHERE is_featured = true ORDER BY question_count DESC LIMIT $1 OFFSET $2`
	} else {
		args = append(args, limit, offset)
		query = `SELECT id, name, slug, description, icon_url, parent_topic_id,
	                 question_count, follower_count, is_featured, created_at
	          FROM topics ORDER BY question_count DESC LIMIT $1 OFFSET $2`
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTopics(rows)
}

func (s *Store) UpdateTopic(ctx context.Context, topicID uuid.UUID, p CreateTopicParams) (*Topic, error) {
	_, err := s.db.Exec(ctx, `
		UPDATE topics SET name = $2, description = $3, icon_url = $4, is_featured = $5
		WHERE id = $1`, topicID, p.Name, p.Description, p.IconURL, p.IsFeatured)
	if err != nil {
		return nil, fmt.Errorf("update topic: %w", err)
	}
	return s.GetTopic(ctx, topicID)
}

func (s *Store) ListQuestionsByTopic(ctx context.Context, topicID uuid.UUID, sortBy string, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	orderBy := "q.created_at DESC"
	switch sortBy {
	case "votes":
		orderBy = "q.vote_score DESC"
	case "unanswered":
		orderBy = "q.created_at DESC"
	}
	rows, err := s.db.Query(ctx, fmt.Sprintf(`
		SELECT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at
		FROM questions q JOIN question_topics qt ON q.id = qt.question_id
		WHERE qt.topic_id = $1 AND q.deleted_at IS NULL AND q.status != 'deleted'
		ORDER BY %s LIMIT $2 OFFSET $3`, orderBy), topicID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

func (s *Store) GetTopContributors(ctx context.Context, topicID uuid.UUID, limit int) ([]QAProfile, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT p.user_id, p.display_name, p.bio, p.expertise_areas, p.reputation_score,
		       p.question_count, p.answer_count, p.best_answer_count, p.is_verified, p.created_at, p.updated_at
		FROM qa_profiles p
		JOIN answers a ON a.author_id = p.user_id
		JOIN question_topics qt ON qt.question_id = a.question_id
		WHERE qt.topic_id = $1 AND a.deleted_at IS NULL
		ORDER BY p.reputation_score DESC LIMIT $2`, topicID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProfiles(rows)
}

func (s *Store) CreateTopicAlias(ctx context.Context, topicID uuid.UUID, alias, language string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO topic_aliases (topic_id, alias, language) VALUES ($1, $2, $3)`,
		topicID, alias, language)
	return err
}

func (s *Store) GetTopicAliases(ctx context.Context, topicID uuid.UUID) ([]TopicAlias, error) {
	rows, err := s.db.Query(ctx, `SELECT id, topic_id, alias, language FROM topic_aliases WHERE topic_id = $1`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var results []TopicAlias
	for rows.Next() {
		var a TopicAlias
		if err := rows.Scan(&a.ID, &a.TopicID, &a.Alias, &a.Language); err != nil {
			return nil, err
		}
		results = append(results, a)
	}
	return results, nil
}
