package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func communityVisibilityFromType(communityType string) string {
	switch communityType {
	case "private", "invite":
		return "private"
	default:
		return "public"
	}
}

func (s *Store) UpsertCommunity(ctx context.Context, community MirroredCommunity) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO qa_communities (
			id, owner_id, name, community_type, status, qa_question_count,
			qa_answer_count, qa_contributor_count, last_qa_activity_at, created_at, updated_at, deleted_at
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, now(), now(), $10
		)
		ON CONFLICT (id) DO UPDATE SET
			owner_id = EXCLUDED.owner_id,
			name = EXCLUDED.name,
			community_type = EXCLUDED.community_type,
			status = EXCLUDED.status,
			deleted_at = EXCLUDED.deleted_at,
			updated_at = now()`,
		community.ID, community.OwnerID, community.Name, community.CommunityType, community.Status, community.QAQuestionCount,
		community.QAAnswerCount, community.QAContributorCount, community.LastQAActivityAt, community.DeletedAt,
	)
	return err
}

func (s *Store) TouchCommunity(ctx context.Context, communityID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE qa_communities SET updated_at = now() WHERE id = $1`, communityID)
	return err
}

func (s *Store) GetCommunity(ctx context.Context, communityID uuid.UUID) (*MirroredCommunity, error) {
	community := &MirroredCommunity{}
	err := s.db.QueryRow(ctx, `
		SELECT id, owner_id, name, community_type, status, qa_question_count,
		       qa_answer_count, qa_contributor_count, last_qa_activity_at, created_at, updated_at, deleted_at
		FROM qa_communities
		WHERE id = $1 AND deleted_at IS NULL AND status != 'deleted'`,
		communityID,
	).Scan(
		&community.ID, &community.OwnerID, &community.Name, &community.CommunityType, &community.Status, &community.QAQuestionCount,
		&community.QAAnswerCount, &community.QAContributorCount, &community.LastQAActivityAt, &community.CreatedAt, &community.UpdatedAt, &community.DeletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("community not found")
	}
	return community, err
}

func (s *Store) DeleteCommunitySync(ctx context.Context, communityID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE question_community_context
		SET community_moderation_status = 'community_deleted', community_id = NULL, updated_at = now()
		WHERE community_id = $1`, communityID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE questions
		SET community_id = NULL, updated_at = now()
		WHERE community_id = $1`, communityID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM qa_community_members WHERE community_id = $1`, communityID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM qa_communities WHERE id = $1`, communityID); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Store) UpsertCommunityMember(ctx context.Context, communityID, userID uuid.UUID, role string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO qa_community_members (community_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (community_id, user_id) DO UPDATE SET
			role = EXCLUDED.role,
			updated_at = now()`,
		communityID, userID, role,
	)
	return err
}

func (s *Store) RemoveCommunityMember(ctx context.Context, communityID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM qa_community_members WHERE community_id = $1 AND user_id = $2`, communityID, userID)
	return err
}

func (s *Store) GetCommunityMemberRole(ctx context.Context, communityID, userID uuid.UUID) (string, error) {
	var role string
	err := s.db.QueryRow(ctx, `
		SELECT role FROM qa_community_members
		WHERE community_id = $1 AND user_id = $2`,
		communityID, userID,
	).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return role, err
}

func (s *Store) GetCommunityQASettings(ctx context.Context, communityID uuid.UUID) (*CommunityQASettings, error) {
	if _, err := s.db.Exec(ctx, `
		INSERT INTO community_qa_settings (community_id)
		VALUES ($1)
		ON CONFLICT (community_id) DO NOTHING`,
		communityID,
	); err != nil {
		return nil, err
	}

	settings := &CommunityQASettings{}
	err := s.db.QueryRow(ctx, `
		SELECT community_id, qa_enabled, ask_permission, answer_permission,
		       auto_suggest_topics, suggested_topic_ids, require_approval, welcome_message,
		       total_questions_count, total_answers_count, unique_contributors_count,
		       created_at, updated_at
		FROM community_qa_settings
		WHERE community_id = $1`,
		communityID,
	).Scan(
		&settings.CommunityID, &settings.QAEnabled, &settings.AskPermission, &settings.AnswerPermission,
		&settings.AutoSuggestTopics, &settings.SuggestedTopicIDs, &settings.RequireApproval, &settings.WelcomeMessage,
		&settings.TotalQuestionsCount, &settings.TotalAnswersCount, &settings.UniqueContributorsCount,
		&settings.CreatedAt, &settings.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("community qa settings not found")
	}
	return settings, err
}

func (s *Store) UpsertCommunityQASettings(ctx context.Context, p CommunityQASettings) (*CommunityQASettings, error) {
	_, err := s.db.Exec(ctx, `
		INSERT INTO community_qa_settings (
			community_id, qa_enabled, ask_permission, answer_permission,
			auto_suggest_topics, suggested_topic_ids, require_approval, welcome_message,
			total_questions_count, total_answers_count, unique_contributors_count
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			COALESCE($9, 0), COALESCE($10, 0), COALESCE($11, 0)
		)
		ON CONFLICT (community_id) DO UPDATE SET
			qa_enabled = EXCLUDED.qa_enabled,
			ask_permission = EXCLUDED.ask_permission,
			answer_permission = EXCLUDED.answer_permission,
			auto_suggest_topics = EXCLUDED.auto_suggest_topics,
			suggested_topic_ids = EXCLUDED.suggested_topic_ids,
			require_approval = EXCLUDED.require_approval,
			welcome_message = EXCLUDED.welcome_message,
			updated_at = now()`,
		p.CommunityID, p.QAEnabled, p.AskPermission, p.AnswerPermission,
		p.AutoSuggestTopics, p.SuggestedTopicIDs, p.RequireApproval, p.WelcomeMessage,
		p.TotalQuestionsCount, p.TotalAnswersCount, p.UniqueContributorsCount,
	)
	if err != nil {
		return nil, err
	}
	return s.GetCommunityQASettings(ctx, p.CommunityID)
}

func (s *Store) ListCommunityAvailableTopics(ctx context.Context, communityID uuid.UUID) ([]CommunityTopicOption, error) {
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.name, t.slug, t.description, t.icon_url, t.parent_topic_id,
		       t.question_count, t.follower_count, t.is_featured, t.created_at,
		       COUNT(*)::INT AS community_question_count
		FROM topics t
		JOIN question_topics qt ON qt.topic_id = t.id
		JOIN questions q ON q.id = qt.question_id
		WHERE q.community_id = $1
		  AND q.deleted_at IS NULL
		  AND q.status IN ('open', 'closed', 'pending_approval')
		GROUP BY t.id, t.name, t.slug, t.description, t.icon_url, t.parent_topic_id,
		         t.question_count, t.follower_count, t.is_featured, t.created_at
		ORDER BY community_question_count DESC, t.name ASC`,
		communityID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CommunityTopicOption
	for rows.Next() {
		var item CommunityTopicOption
		if err := rows.Scan(
			&item.Topic.ID, &item.Topic.Name, &item.Topic.Slug, &item.Topic.Description, &item.Topic.IconURL, &item.Topic.ParentTopicID,
			&item.Topic.QuestionCount, &item.Topic.FollowerCount, &item.Topic.IsFeatured, &item.Topic.CreatedAt,
			&item.QuestionCount,
		); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (s *Store) GetCommunityPopularTopics(ctx context.Context, communityID uuid.UUID, limit int) ([]CommunityTopicAffinity, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.Query(ctx, `
		SELECT cta.community_id,
		       t.id, t.name, t.slug, t.description, t.icon_url, t.parent_topic_id,
		       t.question_count, t.follower_count, t.is_featured, t.created_at,
		       cta.question_count, cta.answer_count, cta.view_count, cta.affinity_score::float8, cta.last_question_at
		FROM community_topic_affinity cta
		JOIN topics t ON t.id = cta.topic_id
		WHERE cta.community_id = $1
		ORDER BY cta.affinity_score DESC, cta.question_count DESC, t.name ASC
		LIMIT $2`,
		communityID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []CommunityTopicAffinity
	for rows.Next() {
		var item CommunityTopicAffinity
		if err := rows.Scan(
			&item.CommunityID,
			&item.Topic.ID, &item.Topic.Name, &item.Topic.Slug, &item.Topic.Description, &item.Topic.IconURL, &item.Topic.ParentTopicID,
			&item.Topic.QuestionCount, &item.Topic.FollowerCount, &item.Topic.IsFeatured, &item.Topic.CreatedAt,
			&item.QuestionCount, &item.AnswerCount, &item.ViewCount, &item.AffinityScore, &item.LastQuestionAt,
		); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, rows.Err()
}

func (s *Store) PinCommunityQuestion(ctx context.Context, communityID, questionID uuid.UUID, pinned bool, actorID *uuid.UUID, reason string) error {
	var pinnedAt *time.Time
	if pinned {
		now := time.Now()
		pinnedAt = &now
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO question_community_context (
			question_id, community_id, community_name_snapshot, community_visibility,
			is_pinned, pinned_at, pinned_by_user_id, community_moderation_notes
		)
		SELECT q.id, q.community_id, qc.name, CASE WHEN qc.community_type IN ('private', 'invite') THEN 'private' ELSE 'public' END,
		       $3, $4, $5, $6
		FROM questions q
		JOIN qa_communities qc ON qc.id = q.community_id
		WHERE q.id = $1 AND q.community_id = $2
		ON CONFLICT (question_id) DO UPDATE SET
			is_pinned = EXCLUDED.is_pinned,
			pinned_at = EXCLUDED.pinned_at,
			pinned_by_user_id = EXCLUDED.pinned_by_user_id,
			community_moderation_notes = EXCLUDED.community_moderation_notes,
			updated_at = now()`,
		questionID, communityID, pinned, pinnedAt, actorID, reason,
	)
	return err
}
