package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/qa-service/internal/qarender"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CreateQuestionParams is a new question. BodyHTML is accepted for client
// compatibility and ignored: body_html is always rendered from Body
// (qarender, audit H3).
type CreateQuestionParams struct {
	// ID, when set by the service, is the new question's id: its media is
	// claimed against it before the row exists. Never from the request.
	ID          uuid.UUID  `json:"-"`
	CommunityID *uuid.UUID `json:"community_id,omitempty"`
	Title       string     `json:"title"`
	Body        string     `json:"body"`
	BodyHTML    string     `json:"body_html"`
	Language    string     `json:"language"`
	Visibility  string     `json:"visibility"`
	Status      string     `json:"-"`
	TopicIDs    []string   `json:"topic_ids,omitempty"`
	Topics      []string   `json:"topics,omitempty"`
	Tags        []string   `json:"tags"`
	MediaIDs    []string   `json:"media_ids"`
	IsAnonymous bool       `json:"is_anonymous,omitempty"`
}

// UpdateQuestionParams is an edit. BodyHTML is accepted for client
// compatibility and ignored: body_html is always rendered from Body
// (qarender, audit H3).
type UpdateQuestionParams struct {
	Title    *string `json:"title,omitempty"`
	Body     *string `json:"body,omitempty"`
	BodyHTML *string `json:"body_html,omitempty"`
}

type ListQuestionsParams struct {
	TopicID     *uuid.UUID
	CommunityID *uuid.UUID
	Scope       string
	SortBy      string
	Status      string
	Limit       int
	Offset      int
}

type dbtx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Store) CreateQuestion(ctx context.Context, authorID uuid.UUID, p CreateQuestionParams) (*Question, error) {
	id := p.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	slug := generateSlug(p.Title, id)
	lang := p.Language
	if lang == "" {
		lang = "en"
	}
	vis := p.Visibility
	if vis == "" {
		vis = "public"
	}
	status := p.Status
	if status == "" {
		status = "open"
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO questions (id, author_id, community_id, title, body, body_html, slug, language, visibility, status, is_anonymous)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		id, authorID, p.CommunityID, p.Title, p.Body, qarender.BodyHTML(p.Body), slug, lang, vis, status, p.IsAnonymous,
	)
	if err != nil {
		return nil, fmt.Errorf("insert question: %w", err)
	}

	topicIDs, err := s.resolveTopicIDsTx(ctx, tx, p.TopicIDs, p.Topics)
	if err != nil {
		return nil, err
	}
	if len(topicIDs) > 0 {
		if err := s.setQuestionTopicsTx(ctx, tx, id, topicIDs); err != nil {
			return nil, err
		}
	}
	if len(p.Tags) > 0 {
		if err := s.setQuestionTagsTx(ctx, tx, id, p.Tags); err != nil {
			return nil, err
		}
	}
	if len(p.MediaIDs) > 0 {
		if err := s.addQuestionMediaTx(ctx, tx, id, p.MediaIDs); err != nil {
			return nil, err
		}
	}

	_, _ = tx.Exec(ctx, `
		INSERT INTO qa_profiles (user_id, question_count) VALUES ($1, 1)
		ON CONFLICT (user_id) DO UPDATE SET question_count = qa_profiles.question_count + 1, updated_at = now()`,
		authorID,
	)

	communityType := ""
	if p.CommunityID != nil {
		var communityName string
		if err := tx.QueryRow(ctx, `
			SELECT name, community_type
			FROM qa_communities
			WHERE id = $1 AND deleted_at IS NULL AND status != 'deleted'`,
			*p.CommunityID,
		).Scan(&communityName, &communityType); err != nil {
			return nil, fmt.Errorf("load community context: %w", err)
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO question_community_context (
				question_id, community_id, community_name_snapshot, community_visibility
			) VALUES ($1, $2, $3, $4)
			ON CONFLICT (question_id) DO UPDATE SET
				community_id = EXCLUDED.community_id,
				community_name_snapshot = EXCLUDED.community_name_snapshot,
				community_visibility = EXCLUDED.community_visibility,
				updated_at = now()`,
			id, *p.CommunityID, communityName, communityVisibilityFromType(communityType),
		)
		if err != nil {
			return nil, fmt.Errorf("insert question community context: %w", err)
		}

		if err := s.updateCommunityQuestionStatsTx(ctx, tx, *p.CommunityID); err != nil {
			return nil, err
		}
		if err := s.bumpCommunityTopicAffinityForQuestionTx(ctx, tx, *p.CommunityID, topicIDs); err != nil {
			return nil, err
		}
	}

	// qa.question.created commits with the row (audit L2).
	if err := enqueueQuestionCreated(ctx, tx, id, authorID, p.CommunityID, p.Title, status, communityType, p.IsAnonymous); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	q, err := s.GetQuestion(ctx, id)
	if err != nil {
		return nil, err
	}
	q.Tags = p.Tags
	return q, nil
}

func (s *Store) GetQuestion(ctx context.Context, questionID uuid.UUID) (*Question, error) {
	q := &Question{}
	var communityIDText string
	var communityName string
	var communityVisibility string
	var communityType string

	err := s.db.QueryRow(ctx, `
		SELECT q.id, q.author_id, COALESCE(q.community_id::text, ''), q.title, q.body, q.body_html, q.slug,
		       q.status, q.visibility, q.language, q.vote_score, q.upvote_count, q.downvote_count,
		       q.answer_count, q.view_count, q.follow_count, q.is_answered, q.best_answer_id,
		       q.closed_reason, q.closed_by, q.merged_into_id, q.created_at, q.updated_at, q.deleted_at,
		       COALESCE(qcc.is_pinned, false),
		       COALESCE(qcc.community_name_snapshot, qc.name, ''),
		       COALESCE(qcc.community_visibility, CASE WHEN qc.community_type IN ('private', 'invite') THEN 'private' ELSE 'public' END, ''),
		       COALESCE(qc.community_type, ''),
		       COALESCE(q.is_anonymous, false)
		FROM questions q
		LEFT JOIN question_community_context qcc ON qcc.question_id = q.id
		LEFT JOIN qa_communities qc ON qc.id = q.community_id
		WHERE q.id = $1 AND q.deleted_at IS NULL`,
		questionID,
	).Scan(
		&q.ID, &q.AuthorID, &communityIDText, &q.Title, &q.Body, &q.BodyHTML, &q.Slug,
		&q.Status, &q.Visibility, &q.Language, &q.VoteScore, &q.UpvoteCount, &q.DownvoteCount,
		&q.AnswerCount, &q.ViewCount, &q.FollowCount, &q.IsAnswered, &q.BestAnswerID,
		&q.ClosedReason, &q.ClosedBy, &q.MergedIntoID, &q.CreatedAt, &q.UpdatedAt, &q.DeletedAt,
		&q.IsPinned, &communityName, &communityVisibility, &communityType,
		&q.IsAnonymous,
	)
	if err != nil {
		return nil, fmt.Errorf("get question: %w", err)
	}
	q.BodyHTML = qarender.BodyHTML(q.Body) // never the stored column (H3)

	if communityIDText != "" {
		communityID, err := uuid.Parse(communityIDText)
		if err != nil {
			return nil, fmt.Errorf("parse question community id: %w", err)
		}
		q.CommunityID = &communityID
		q.Community = &CommunityScope{
			ID:            communityID,
			Name:          communityName,
			Visibility:    communityVisibility,
			CommunityType: communityType,
		}
	}

	topics, _ := s.GetQuestionTopics(ctx, questionID)
	q.Topics = topics
	tags, _ := s.GetQuestionTags(ctx, questionID)
	q.Tags = tags
	return q, nil
}

// GetQuestionBySlug resolves a question link. The slug is fixed when the
// question is created and never changes on edit (audit L3), so a link keeps
// working. Links minted before that fix (when an edit regenerated the slug)
// still resolve through the id prefix every slug ends with.
func (s *Store) GetQuestionBySlug(ctx context.Context, slug string) (*Question, error) {
	var id uuid.UUID
	err := s.db.QueryRow(ctx, `SELECT id FROM questions WHERE slug = $1 AND deleted_at IS NULL`, slug).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		id, err = s.questionIDBySlugSuffix(ctx, slug)
	}
	if err != nil {
		return nil, fmt.Errorf("get question by slug: %w", err)
	}
	return s.GetQuestion(ctx, id)
}

// questionIDBySlugSuffix finds the one live question whose id starts with the
// 12 hex digits a slug ends with (generateSlug). The prefix becomes an id
// range, so the lookup is a primary-key range scan. Two matches are refused:
// a link never resolves to a guess.
func (s *Store) questionIDBySlugSuffix(ctx context.Context, slug string) (uuid.UUID, error) {
	lo, hi, ok := slugIDRange(slug)
	if !ok {
		return uuid.Nil, pgx.ErrNoRows
	}
	rows, err := s.db.Query(ctx, `
		SELECT id FROM questions
		WHERE id BETWEEN $1 AND $2 AND deleted_at IS NULL
		LIMIT 2`, lo, hi)
	if err != nil {
		return uuid.Nil, err
	}
	defer rows.Close()
	var found []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return uuid.Nil, err
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, err
	}
	if len(found) != 1 {
		return uuid.Nil, pgx.ErrNoRows
	}
	return found[0], nil
}

// slugIDRange turns a slug's trailing 12-hex-digit id prefix into the range
// of ids that start with it.
func slugIDRange(slug string) (lo, hi uuid.UUID, ok bool) {
	i := strings.LastIndex(slug, "-")
	suffix := slug[i+1:]
	if len(suffix) != slugIDDigits {
		return uuid.Nil, uuid.Nil, false
	}
	for _, r := range suffix {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return uuid.Nil, uuid.Nil, false
		}
	}
	toUUID := func(hex32 string) (uuid.UUID, bool) {
		u, err := uuid.Parse(hex32[0:8] + "-" + hex32[8:12] + "-" + hex32[12:16] + "-" + hex32[16:20] + "-" + hex32[20:32])
		return u, err == nil
	}
	lo, okLo := toUUID(suffix + strings.Repeat("0", 32-slugIDDigits))
	hi, okHi := toUUID(suffix + strings.Repeat("f", 32-slugIDDigits))
	return lo, hi, okLo && okHi
}

// slugIDDigits is how many hex digits of the id a slug ends with.
const slugIDDigits = 12

// UpdateQuestion edits a question's title and/or body (audit L3):
//   - the slug is NOT regenerated, so every existing link keeps working;
//   - the replaced title and body are kept as a question_revisions row,
//     written in the same transaction as the edit;
//   - body_html is rendered from the body (qarender, audit H3); the
//     client's BodyHTML is ignored;
//   - qa.question.updated commits with the edit (audit L2).
//
// An edit that changes nothing writes nothing.
func (s *Store) UpdateQuestion(ctx context.Context, questionID uuid.UUID, p UpdateQuestionParams) (*Question, error) {
	if p.Title == nil && p.Body == nil {
		return s.GetQuestion(ctx, questionID)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var oldTitle, oldBody string
	var authorID uuid.UUID
	var anonymous bool
	if err := tx.QueryRow(ctx, `
		SELECT title, body, author_id, COALESCE(is_anonymous, false)
		FROM questions WHERE id = $1 AND deleted_at IS NULL
		FOR UPDATE`, questionID,
	).Scan(&oldTitle, &oldBody, &authorID, &anonymous); err != nil {
		return nil, fmt.Errorf("update question: %w", err)
	}
	title, body := oldTitle, oldBody
	if p.Title != nil {
		title = *p.Title
	}
	if p.Body != nil {
		body = *p.Body
	}
	if title == oldTitle && body == oldBody {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return s.GetQuestion(ctx, questionID)
	}

	var revisionID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO question_revisions (question_id, editor_id, title, body)
		VALUES ($1, $2, $3, $4)
		RETURNING id`, questionID, authorID, oldTitle, oldBody,
	).Scan(&revisionID); err != nil {
		return nil, fmt.Errorf("record question revision: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE questions SET title = $2, body = $3, body_html = $4, updated_at = now()
		WHERE id = $1`, questionID, title, body, qarender.BodyHTML(body),
	); err != nil {
		return nil, fmt.Errorf("update question: %w", err)
	}
	if err := enqueueQuestionUpdated(ctx, tx, questionID, authorID, revisionID, anonymous); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetQuestion(ctx, questionID)
}

func (s *Store) DeleteQuestion(ctx context.Context, questionID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE questions SET deleted_at = now(), status = 'deleted' WHERE id = $1 AND deleted_at IS NULL`, questionID)
	return err
}

func (s *Store) ListQuestions(ctx context.Context, viewerID *uuid.UUID, p ListQuestionsParams) ([]QuestionSummary, error) {
	if p.Limit <= 0 {
		p.Limit = 20
	}

	orderBy := "q.created_at DESC"
	switch p.SortBy {
	case "top", "votes":
		orderBy = "q.vote_score DESC, q.created_at DESC"
	case "trending":
		orderBy = "(q.vote_score + q.answer_count * 2 + q.view_count * 0.1) DESC, q.created_at DESC"
	case "unanswered":
		orderBy = "q.created_at DESC"
	default:
		p.SortBy = "recent"
	}

	conditions := []string{"q.deleted_at IS NULL", "q.status != 'deleted'"}
	args := []any{}
	idx := 1

	if p.TopicID != nil {
		conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM question_topics qt WHERE qt.question_id = q.id AND qt.topic_id = $%d)", idx))
		args = append(args, *p.TopicID)
		idx++
	}

	if p.CommunityID != nil {
		conditions = append(conditions, fmt.Sprintf("q.community_id = $%d", idx))
		args = append(args, *p.CommunityID)
		idx++
	}

	switch p.Scope {
	case "global":
		conditions = append(conditions, "q.community_id IS NULL")
	case "community":
		conditions = append(conditions, "q.community_id IS NOT NULL")
	}

	switch p.Status {
	case "", "active", "open":
		conditions = append(conditions, "q.status = 'open'")
	case "closed":
		conditions = append(conditions, "q.status = 'closed'")
	case "pending_approval":
		conditions = append(conditions, "q.status = 'pending_approval'")
	}

	if p.SortBy == "unanswered" {
		conditions = append(conditions, "q.answer_count = 0")
	}

	// One visibility rule (visibility.go, audit H5).
	conditions = append(conditions, questionVisibleSQL("q", fmt.Sprintf("$%d", idx)))
	args = append(args, viewerID)
	idx++

	args = append(args, p.Limit, p.Offset)
	limitIdx := idx
	offsetIdx := idx + 1

	query := fmt.Sprintf(`
		SELECT q.id, q.author_id, COALESCE(q.community_id::text, ''), q.title, q.slug, q.status,
		       q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
		       LEFT(regexp_replace(COALESCE(q.body, ''), '\s+', ' ', 'g'), 180),
		       COALESCE(qcc.is_pinned, false),
		       COALESCE(qcc.community_name_snapshot, qc.name, ''),
		       COALESCE(qcc.community_visibility, CASE WHEN qc.community_type IN ('private', 'invite') THEN 'private' ELSE 'public' END, ''),
		       COALESCE(qc.community_type, ''),
		       COALESCE(q.is_anonymous, false)
		FROM questions q
		LEFT JOIN question_community_context qcc ON qcc.question_id = q.id
		LEFT JOIN qa_communities qc ON qc.id = q.community_id
		WHERE %s
		ORDER BY %s
		LIMIT $%d OFFSET $%d`,
		strings.Join(conditions, " AND "),
		orderBy,
		limitIdx, offsetIdx,
	)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummariesWithCommunity(rows)
}

// ListQuestionsByAuthor is a profile's question list. The author sees every
// live question of theirs, anonymous and awaiting approval included. Anyone
// else never sees an anonymous one (audit H4), nor one they could not see
// (H5).
func (s *Store) ListQuestionsByAuthor(ctx context.Context, authorID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	self := viewerID != nil && *viewerID == authorID
	rows, err := s.db.Query(ctx, `
		SELECT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
		       COALESCE(q.is_anonymous, false)
		FROM questions q WHERE q.author_id = $1 AND q.deleted_at IS NULL
		  AND ($5 OR (NOT COALESCE(q.is_anonymous, false) AND `+questionVisibleSQL("q", "$4")+`))
		ORDER BY q.created_at DESC LIMIT $2 OFFSET $3`, authorID, limit, offset, viewerID, self)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

func (s *Store) IncrementQuestionViewCount(ctx context.Context, questionID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var communityID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		UPDATE questions
		SET view_count = view_count + 1
		WHERE id = $1
		RETURNING community_id`,
		questionID,
	).Scan(&communityID); err != nil {
		return err
	}
	if communityID != nil {
		if err := s.bumpCommunityTopicViewCountTx(ctx, tx, *communityID, questionID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SetQuestionTopics(ctx context.Context, questionID uuid.UUID, topicIDs []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := s.setQuestionTopicsTx(ctx, tx, questionID, topicIDs); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) setQuestionTopicsTx(ctx context.Context, exec dbtx, questionID uuid.UUID, topicIDs []string) error {
	if _, err := exec.Exec(ctx, `DELETE FROM question_topics WHERE question_id = $1`, questionID); err != nil {
		return err
	}
	for _, tid := range topicIDs {
		parsed, err := uuid.Parse(tid)
		if err != nil {
			continue
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO question_topics (question_id, topic_id) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`,
			questionID, parsed,
		); err != nil {
			return err
		}
		_, _ = exec.Exec(ctx, `UPDATE topics SET question_count = question_count + 1 WHERE id = $1`, parsed)
	}
	return nil
}

func (s *Store) SetQuestionTags(ctx context.Context, questionID uuid.UUID, tags []string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := s.setQuestionTagsTx(ctx, tx, questionID, tags); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) setQuestionTagsTx(ctx context.Context, exec dbtx, questionID uuid.UUID, tags []string) error {
	if _, err := exec.Exec(ctx, `DELETE FROM question_tags WHERE question_id = $1`, questionID); err != nil {
		return err
	}
	for _, tag := range tags {
		t := strings.TrimSpace(tag)
		if t == "" {
			continue
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO question_tags (question_id, tag) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`,
			questionID, t,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetQuestionTopics(ctx context.Context, questionID uuid.UUID) ([]Topic, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.name, t.slug, t.description, t.icon_url, t.parent_topic_id,
		       t.question_count, t.follower_count, t.is_featured, t.created_at
		FROM topics t JOIN question_topics qt ON t.id = qt.topic_id
		WHERE qt.question_id = $1`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTopics(rows)
}

func (s *Store) GetQuestionTags(ctx context.Context, questionID uuid.UUID) ([]string, error) {
	rows, err := s.db.Query(ctx, `SELECT tag FROM question_tags WHERE question_id = $1 ORDER BY tag`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func (s *Store) addQuestionMediaTx(ctx context.Context, exec dbtx, questionID uuid.UUID, mediaIDs []string) error {
	for i, mid := range mediaIDs {
		parsed, err := uuid.Parse(mid)
		if err != nil {
			continue
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO question_media (question_id, media_id, sort_order)
			VALUES ($1, $2, $3)
			ON CONFLICT DO NOTHING`,
			questionID, parsed, i,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) GetSimilarQuestions(ctx context.Context, title string, viewerID *uuid.UUID, limit int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 5
	}
	// Indexed title match (search_match.go, audit M6).
	tsq := PrefixTSQuery(title)
	if tsq == "" {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, author_id, title, slug, status, vote_score, answer_count, view_count, is_answered, created_at,
		       COALESCE(is_anonymous, false)
		FROM questions q
		WHERE deleted_at IS NULL AND status = 'open' AND `+titleMatchSQL("q", "$1")+`
		  AND `+questionVisibleSQL("q", "$3")+`
		ORDER BY vote_score DESC LIMIT $2`, tsq, limit, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

// SearchQuestions matches every word of q as a word prefix in the title or
// body through the full-text GIN index (search_match.go, audit M6), with
// optional community and topic filters, best voted first.
func (s *Store) SearchQuestions(ctx context.Context, q string, communityID, topicID, viewerID *uuid.UUID, limit, offset int) ([]QuestionSummary, error) {
	if limit <= 0 {
		limit = 20
	}

	tsq := PrefixTSQuery(q)
	if tsq == "" {
		return nil, nil
	}
	conditions := []string{
		"q.deleted_at IS NULL",
		"q.status != 'deleted'",
		searchMatchSQL("q", "$1"),
	}
	args := []any{tsq}
	idx := 2

	if communityID != nil {
		conditions = append(conditions, fmt.Sprintf("q.community_id = $%d", idx))
		args = append(args, *communityID)
		idx++
	}
	if topicID != nil {
		conditions = append(conditions, fmt.Sprintf("EXISTS (SELECT 1 FROM question_topics qt WHERE qt.question_id = q.id AND qt.topic_id = $%d)", idx))
		args = append(args, *topicID)
		idx++
	}
	// One visibility rule (visibility.go, audit H5).
	conditions = append(conditions, questionVisibleSQL("q", fmt.Sprintf("$%d", idx)))
	args = append(args, viewerID)
	idx++

	args = append(args, limit, offset)
	limitIdx := idx
	offsetIdx := idx + 1

	query := fmt.Sprintf(`
		SELECT q.id, q.author_id, q.title, q.slug, q.status, q.vote_score, q.answer_count, q.view_count, q.is_answered, q.created_at,
		       COALESCE(q.is_anonymous, false)
		FROM questions q
		WHERE %s
		ORDER BY q.vote_score DESC, q.created_at DESC
		LIMIT $%d OFFSET $%d`,
		strings.Join(conditions, " AND "), limitIdx, offsetIdx,
	)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanQuestionSummaries(rows)
}

func (s *Store) resolveTopicIDsTx(ctx context.Context, exec dbtx, rawIDs, rawSlugs []string) ([]string, error) {
	seen := make(map[string]struct{}, len(rawIDs)+len(rawSlugs))
	var resolved []string

	for _, rawID := range rawIDs {
		rawID = strings.TrimSpace(rawID)
		if rawID == "" {
			continue
		}
		if _, ok := seen[rawID]; ok {
			continue
		}
		if _, err := uuid.Parse(rawID); err != nil {
			return nil, fmt.Errorf("invalid topic id: %s", rawID)
		}
		seen[rawID] = struct{}{}
		resolved = append(resolved, rawID)
	}

	for _, slug := range rawSlugs {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		var topicID uuid.UUID
		if err := exec.QueryRow(ctx, `SELECT id FROM topics WHERE slug = $1`, slug).Scan(&topicID); err != nil {
			if err == pgx.ErrNoRows {
				return nil, fmt.Errorf("invalid topic: %s", slug)
			}
			return nil, err
		}
		id := topicID.String()
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		resolved = append(resolved, id)
	}

	return resolved, nil
}

func (s *Store) updateCommunityQuestionStatsTx(ctx context.Context, exec dbtx, communityID uuid.UUID) error {
	if _, err := exec.Exec(ctx, `
		INSERT INTO community_qa_settings (community_id)
		VALUES ($1)
		ON CONFLICT (community_id) DO NOTHING`,
		communityID,
	); err != nil {
		return err
	}

	if _, err := exec.Exec(ctx, `
		UPDATE community_qa_settings
		SET total_questions_count = total_questions_count + 1,
		    updated_at = now()
		WHERE community_id = $1`,
		communityID,
	); err != nil {
		return err
	}

	if _, err := exec.Exec(ctx, `
		UPDATE qa_communities
		SET qa_question_count = qa_question_count + 1,
		    last_qa_activity_at = now(),
		    updated_at = now()
		WHERE id = $1`,
		communityID,
	); err != nil {
		return err
	}

	// The contributor count is recounted in the background (audit M7).
	return markCommunityStatsStaleTx(ctx, exec, communityID)
}

func (s *Store) bumpCommunityTopicAffinityForQuestionTx(ctx context.Context, exec dbtx, communityID uuid.UUID, topicIDs []string) error {
	for _, rawID := range topicIDs {
		topicID, err := uuid.Parse(rawID)
		if err != nil {
			continue
		}
		if _, err := exec.Exec(ctx, `
			INSERT INTO community_topic_affinity (
				community_id, topic_id, question_count, recency_score, affinity_score, last_question_at
			) VALUES ($1, $2, 1, 1.0, 1.0, now())
			ON CONFLICT (community_id, topic_id) DO UPDATE SET
				question_count = community_topic_affinity.question_count + 1,
				recency_score = community_topic_affinity.recency_score + 1.0,
				affinity_score = (
					community_topic_affinity.question_count + 1 +
					community_topic_affinity.answer_count * 0.5 +
					community_topic_affinity.view_count * 0.05
				),
				last_question_at = now(),
				updated_at = now()`,
			communityID, topicID,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) bumpCommunityTopicViewCountTx(ctx context.Context, exec dbtx, communityID, questionID uuid.UUID) error {
	// Read every topic first: an Exec on the transaction while its rows
	// are still open fails with "conn busy".
	topicIDs, err := questionTopicIDsTx(ctx, exec, questionID)
	if err != nil {
		return err
	}
	for _, topicID := range topicIDs {
		if _, err := exec.Exec(ctx, `
			INSERT INTO community_topic_affinity (community_id, topic_id, view_count, affinity_score)
			VALUES ($1, $2, 1, 0.05)
			ON CONFLICT (community_id, topic_id) DO UPDATE SET
				view_count = community_topic_affinity.view_count + 1,
				affinity_score = (
					community_topic_affinity.question_count +
					community_topic_affinity.answer_count * 0.5 +
					(community_topic_affinity.view_count + 1) * 0.05
				),
				updated_at = now()`,
			communityID, topicID,
		); err != nil {
			return err
		}
	}
	return nil
}

func generateSlug(title string, id uuid.UUID) string {
	slug := strings.ToLower(title)
	slug = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		if r == ' ' || r == '-' {
			return '-'
		}
		return -1
	}, slug)
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	if len(slug) > 80 {
		slug = slug[:80]
	}
	// 8 hex chars = 32 bits → birthday collision at ~65k IDs. At
	// platform scale (millions of questions) collisions become real;
	// bump to 12 chars (48 bits → ~16M before 50% collision risk)
	// without bloating URL length materially. The full UUID stays the
	// canonical identifier (questions.id), so the slug suffix is just
	// for URL uniqueness + readability.
	short := strings.ReplaceAll(id.String(), "-", "")[:slugIDDigits]
	return slug + "-" + short
}

func scanQuestionSummaries(rows pgx.Rows) ([]QuestionSummary, error) {
	var results []QuestionSummary
	for rows.Next() {
		var q QuestionSummary
		if err := rows.Scan(&q.ID, &q.AuthorID, &q.Title, &q.Slug, &q.Status, &q.VoteScore, &q.AnswerCount, &q.ViewCount, &q.IsAnswered, &q.CreatedAt, &q.IsAnonymous); err != nil {
			return nil, err
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

func scanQuestionSummariesWithCommunity(rows pgx.Rows) ([]QuestionSummary, error) {
	var results []QuestionSummary
	for rows.Next() {
		var q QuestionSummary
		var communityIDText string
		var communityName string
		var communityVisibility string
		var communityType string
		if err := rows.Scan(
			&q.ID, &q.AuthorID, &communityIDText, &q.Title, &q.Slug, &q.Status,
			&q.VoteScore, &q.AnswerCount, &q.ViewCount, &q.IsAnswered, &q.CreatedAt,
			&q.Excerpt, &q.IsPinned, &communityName, &communityVisibility, &communityType,
			&q.IsAnonymous,
		); err != nil {
			return nil, err
		}
		if communityIDText != "" {
			communityID, err := uuid.Parse(communityIDText)
			if err != nil {
				return nil, err
			}
			q.CommunityID = &communityID
			q.Community = &CommunityScope{
				ID:            communityID,
				Name:          communityName,
				Visibility:    communityVisibility,
				CommunityType: communityType,
			}
		}
		results = append(results, q)
	}
	return results, rows.Err()
}

func scanTopics(rows pgx.Rows) ([]Topic, error) {
	var results []Topic
	for rows.Next() {
		var t Topic
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Description, &t.IconURL, &t.ParentTopicID,
			&t.QuestionCount, &t.FollowerCount, &t.IsFeatured, &t.CreatedAt); err != nil {
			return nil, err
		}
		results = append(results, t)
	}
	return results, rows.Err()
}
