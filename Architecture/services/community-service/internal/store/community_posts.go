package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type CommunityPost struct {
	ID               uuid.UUID       `json:"id"`
	CommunityID      uuid.UUID       `json:"community_id"`
	SpaceID          uuid.UUID       `json:"space_id"`
	AuthorID         string          `json:"author_id"`
	ContentType      string          `json:"content_type"`
	Title            *string         `json:"title,omitempty"`
	Body             *string         `json:"body,omitempty"`
	BodyHTML         *string         `json:"body_html,omitempty"`
	TypePayload      json.RawMessage `json:"type_payload"`
	Attachments      json.RawMessage `json:"attachments"`
	Tags             []string        `json:"tags"`
	ParentPostID     *uuid.UUID      `json:"parent_post_id,omitempty"`
	ThreadDepth      int             `json:"thread_depth"`
	ReplyCount       int             `json:"reply_count"`
	NeedsApproval    bool            `json:"needs_approval"`
	ApprovedBy       *string         `json:"approved_by,omitempty"`
	IsPinned         bool            `json:"is_pinned"`
	IsAnnouncement   bool            `json:"is_announcement"`
	IsFeatured       bool            `json:"is_featured"`
	IsAnswered       bool            `json:"is_answered"`
	AcceptedAnswerID *uuid.UUID      `json:"accepted_answer_id,omitempty"`
	IsExpertAnswer   bool            `json:"is_expert_answer"`
	Status           string          `json:"status"`
	SparkCount       int             `json:"spark_count"`
	CommentCount     int             `json:"comment_count"`
	EchoCount        int             `json:"echo_count"`
	ViewCount        int             `json:"view_count"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type WikiPage struct {
	ID          uuid.UUID  `json:"id"`
	CommunityID uuid.UUID `json:"community_id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Content     string     `json:"content"`
	ContentHTML *string    `json:"content_html,omitempty"`
	Category    *string    `json:"category,omitempty"`
	IsPinned    bool       `json:"is_pinned"`
	CreatedBy   string     `json:"created_by"`
	UpdatedBy   *string    `json:"updated_by,omitempty"`
	Version     int        `json:"version"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// --- Community Posts ---

func (s *Store) CreateCommunityPost(ctx context.Context, p *CommunityPost) error {
	p.ID = uuid.New()
	p.CreatedAt = time.Now()
	p.UpdatedAt = p.CreatedAt
	if p.Status == "" {
		p.Status = "published"
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO community_posts (id, community_id, space_id, author_id, content_type, title, body, body_html,
			type_payload, attachments, tags, parent_post_id, thread_depth, reply_count,
			needs_approval, is_pinned, is_announcement, is_featured, is_expert_answer, status,
			spark_count, comment_count, echo_count, view_count, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`,
		p.ID, p.CommunityID, p.SpaceID, p.AuthorID, p.ContentType, p.Title, p.Body, p.BodyHTML,
		p.TypePayload, p.Attachments, p.Tags, p.ParentPostID, p.ThreadDepth, p.ReplyCount,
		p.NeedsApproval, p.IsPinned, p.IsAnnouncement, p.IsFeatured, p.IsExpertAnswer, p.Status,
		p.SparkCount, p.CommentCount, p.EchoCount, p.ViewCount, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	// Increment reply count on parent if this is a reply
	if p.ParentPostID != nil {
		_, _ = s.db.Exec(ctx, `UPDATE community_posts SET reply_count = reply_count + 1 WHERE id = $1`, p.ParentPostID)
	}
	// Increment space + community post count
	_, _ = s.db.Exec(ctx, `UPDATE community_spaces SET post_count = post_count + 1 WHERE id = $1`, p.SpaceID)
	_, _ = s.db.Exec(ctx, `UPDATE communities SET post_count = post_count + 1 WHERE id = $1`, p.CommunityID)
	return nil
}

func (s *Store) GetCommunityPost(ctx context.Context, id uuid.UUID) (*CommunityPost, error) {
	var p CommunityPost
	err := s.db.QueryRow(ctx, `SELECT id, community_id, space_id, author_id, content_type, title, body,
		type_payload, attachments, tags, parent_post_id, thread_depth, reply_count,
		is_pinned, is_announcement, is_featured, is_answered, accepted_answer_id, is_expert_answer,
		status, spark_count, comment_count, echo_count, view_count, created_at, updated_at
		FROM community_posts WHERE id = $1`, id).Scan(
		&p.ID, &p.CommunityID, &p.SpaceID, &p.AuthorID, &p.ContentType, &p.Title, &p.Body,
		&p.TypePayload, &p.Attachments, &p.Tags, &p.ParentPostID, &p.ThreadDepth, &p.ReplyCount,
		&p.IsPinned, &p.IsAnnouncement, &p.IsFeatured, &p.IsAnswered, &p.AcceptedAnswerID, &p.IsExpertAnswer,
		&p.Status, &p.SparkCount, &p.CommentCount, &p.EchoCount, &p.ViewCount, &p.CreatedAt, &p.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return &p, err
}

func (s *Store) ListSpacePosts(ctx context.Context, spaceID uuid.UUID, limit, offset int) ([]CommunityPost, error) {
	rows, err := s.db.Query(ctx, `SELECT id, community_id, space_id, author_id, content_type, title, body,
		type_payload, attachments, tags, parent_post_id, thread_depth, reply_count,
		is_pinned, is_announcement, is_featured, is_answered, accepted_answer_id, is_expert_answer,
		status, spark_count, comment_count, echo_count, view_count, created_at, updated_at
		FROM community_posts WHERE space_id = $1 AND status = 'published' AND parent_post_id IS NULL
		ORDER BY is_pinned DESC, created_at DESC LIMIT $2 OFFSET $3`, spaceID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []CommunityPost
	for rows.Next() {
		var p CommunityPost
		if err := rows.Scan(&p.ID, &p.CommunityID, &p.SpaceID, &p.AuthorID, &p.ContentType, &p.Title, &p.Body,
			&p.TypePayload, &p.Attachments, &p.Tags, &p.ParentPostID, &p.ThreadDepth, &p.ReplyCount,
			&p.IsPinned, &p.IsAnnouncement, &p.IsFeatured, &p.IsAnswered, &p.AcceptedAnswerID, &p.IsExpertAnswer,
			&p.Status, &p.SparkCount, &p.CommentCount, &p.EchoCount, &p.ViewCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func (s *Store) ListCommunityPosts(ctx context.Context, communityID uuid.UUID, limit, offset int) ([]CommunityPost, error) {
	rows, err := s.db.Query(ctx, `SELECT id, community_id, space_id, author_id, content_type, title, body,
		type_payload, attachments, tags, parent_post_id, thread_depth, reply_count,
		is_pinned, is_announcement, is_featured, is_answered, accepted_answer_id, is_expert_answer,
		status, spark_count, comment_count, echo_count, view_count, created_at, updated_at
		FROM community_posts WHERE community_id = $1 AND status = 'published' AND parent_post_id IS NULL
		ORDER BY is_pinned DESC, created_at DESC LIMIT $2 OFFSET $3`, communityID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []CommunityPost
	for rows.Next() {
		var p CommunityPost
		if err := rows.Scan(&p.ID, &p.CommunityID, &p.SpaceID, &p.AuthorID, &p.ContentType, &p.Title, &p.Body,
			&p.TypePayload, &p.Attachments, &p.Tags, &p.ParentPostID, &p.ThreadDepth, &p.ReplyCount,
			&p.IsPinned, &p.IsAnnouncement, &p.IsFeatured, &p.IsAnswered, &p.AcceptedAnswerID, &p.IsExpertAnswer,
			&p.Status, &p.SparkCount, &p.CommentCount, &p.EchoCount, &p.ViewCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func (s *Store) ListFeaturedPosts(ctx context.Context, communityID uuid.UUID, limit int) ([]CommunityPost, error) {
	rows, err := s.db.Query(ctx, `SELECT id, community_id, space_id, author_id, content_type, title, body,
		type_payload, attachments, tags, parent_post_id, thread_depth, reply_count,
		is_pinned, is_announcement, is_featured, is_answered, accepted_answer_id, is_expert_answer,
		status, spark_count, comment_count, echo_count, view_count, created_at, updated_at
		FROM community_posts WHERE community_id = $1 AND is_featured = TRUE AND status = 'published'
		ORDER BY created_at DESC LIMIT $2`, communityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []CommunityPost
	for rows.Next() {
		var p CommunityPost
		if err := rows.Scan(&p.ID, &p.CommunityID, &p.SpaceID, &p.AuthorID, &p.ContentType, &p.Title, &p.Body,
			&p.TypePayload, &p.Attachments, &p.Tags, &p.ParentPostID, &p.ThreadDepth, &p.ReplyCount,
			&p.IsPinned, &p.IsAnnouncement, &p.IsFeatured, &p.IsAnswered, &p.AcceptedAnswerID, &p.IsExpertAnswer,
			&p.Status, &p.SparkCount, &p.CommentCount, &p.EchoCount, &p.ViewCount, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		posts = append(posts, p)
	}
	return posts, nil
}

func (s *Store) DeleteCommunityPost(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE community_posts SET status = 'deleted', updated_at = NOW() WHERE id = $1`, id)
	return err
}

func (s *Store) AcceptAnswer(ctx context.Context, postID, answerID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE community_posts SET is_answered = TRUE, accepted_answer_id = $2, updated_at = NOW() WHERE id = $1`, postID, answerID)
	return err
}

func (s *Store) MarkFeatured(ctx context.Context, postID uuid.UUID, featured bool) error {
	_, err := s.db.Exec(ctx, `UPDATE community_posts SET is_featured = $2, updated_at = NOW() WHERE id = $1`, postID, featured)
	return err
}

func (s *Store) PinCommunityPost(ctx context.Context, postID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE community_posts SET is_pinned = TRUE, updated_at = NOW() WHERE id = $1`, postID)
	return err
}

// --- Engagement ---

func (s *Store) SparkCommunityPost(ctx context.Context, postID uuid.UUID, userID string, isSupernova bool) error {
	weight := 1
	if isSupernova {
		weight = 5
	}
	_, err := s.db.Exec(ctx, `INSERT INTO community_post_sparks (post_id, user_id, is_supernova) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, postID, userID, isSupernova)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE community_posts SET spark_count = spark_count + $2 WHERE id = $1`, postID, weight)
	return err
}

func (s *Store) StashCommunityPost(ctx context.Context, postID uuid.UUID, userID string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO community_post_stashes (post_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, postID, userID)
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) RecordCommunityPostView(ctx context.Context, postID uuid.UUID, userID string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO community_post_views (post_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, postID, userID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `UPDATE community_posts SET view_count = view_count + 1 WHERE id = $1`, postID)
	return err
}

// --- Wiki ---

func (s *Store) CreateWikiPage(ctx context.Context, w *WikiPage) error {
	w.ID = uuid.New()
	w.CreatedAt = time.Now()
	w.UpdatedAt = w.CreatedAt
	w.Version = 1
	_, err := s.db.Exec(ctx, `INSERT INTO community_wiki_pages (id, community_id, title, slug, content, content_html, category, is_pinned, created_by, version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		w.ID, w.CommunityID, w.Title, w.Slug, w.Content, w.ContentHTML, w.Category, w.IsPinned, w.CreatedBy, w.Version, w.CreatedAt, w.UpdatedAt)
	return err
}

func (s *Store) GetWikiPage(ctx context.Context, communityID uuid.UUID, slug string) (*WikiPage, error) {
	var w WikiPage
	err := s.db.QueryRow(ctx, `SELECT id, community_id, title, slug, content, content_html, category, is_pinned, created_by, updated_by, version, created_at, updated_at
		FROM community_wiki_pages WHERE community_id = $1 AND slug = $2`, communityID, slug).Scan(
		&w.ID, &w.CommunityID, &w.Title, &w.Slug, &w.Content, &w.ContentHTML, &w.Category, &w.IsPinned, &w.CreatedBy, &w.UpdatedBy, &w.Version, &w.CreatedAt, &w.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, ErrNotFound
	}
	return &w, err
}

func (s *Store) ListWikiPages(ctx context.Context, communityID uuid.UUID) ([]WikiPage, error) {
	rows, err := s.db.Query(ctx, `SELECT id, community_id, title, slug, content, content_html, category, is_pinned, created_by, updated_by, version, created_at, updated_at
		FROM community_wiki_pages WHERE community_id = $1 ORDER BY is_pinned DESC, title ASC`, communityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pages []WikiPage
	for rows.Next() {
		var w WikiPage
		if err := rows.Scan(&w.ID, &w.CommunityID, &w.Title, &w.Slug, &w.Content, &w.ContentHTML, &w.Category, &w.IsPinned, &w.CreatedBy, &w.UpdatedBy, &w.Version, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		pages = append(pages, w)
	}
	return pages, nil
}

func (s *Store) UpdateWikiPage(ctx context.Context, id uuid.UUID, title, content, contentHTML string, updatedBy string) error {
	_, err := s.db.Exec(ctx, `UPDATE community_wiki_pages SET title=$2, content=$3, content_html=$4, updated_by=$5, version=version+1, updated_at=NOW() WHERE id=$1`,
		id, title, content, contentHTML, updatedBy)
	return err
}

// --- Sentinel ---

var ErrNotFound = errors.New("not found")
