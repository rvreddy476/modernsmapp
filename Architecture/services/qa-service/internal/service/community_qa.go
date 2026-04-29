package service

import (
	"context"
	"fmt"

	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
)

type UpdateCommunityQASettingsParams struct {
	QAEnabled         *bool       `json:"qa_enabled"`
	AskPermission     *string     `json:"ask_permission"`
	AnswerPermission  *string     `json:"answer_permission"`
	AutoSuggestTopics *bool       `json:"auto_suggest_topics"`
	SuggestedTopicIDs []uuid.UUID `json:"suggested_topic_ids"`
	RequireApproval   *bool       `json:"require_approval"`
	WelcomeMessage    *string     `json:"welcome_message"`
}

func communityRoleLevel(role string) int {
	switch role {
	case "owner":
		return 7
	case "admin":
		return 6
	case "moderator":
		return 5
	case "space_manager":
		return 4
	case "expert":
		return 3
	case "member":
		return 2
	case "pending":
		return 1
	default:
		return 0
	}
}

func isCommunityMember(role string) bool {
	return communityRoleLevel(role) >= communityRoleLevel("member")
}

func isCommunityModerator(role string) bool {
	return communityRoleLevel(role) >= communityRoleLevel("moderator")
}

func isCommunityAdmin(role string) bool {
	return communityRoleLevel(role) >= communityRoleLevel("admin")
}

func isRestrictedCommunity(communityType string) bool {
	return communityType == "private" || communityType == "invite"
}

func canPerformCommunityAction(permission, role string) bool {
	switch permission {
	case "", "everyone":
		return true
	case "members":
		return isCommunityMember(role)
	case "moderators":
		return isCommunityModerator(role)
	default:
		return false
	}
}

func (s *Service) getCommunityAccess(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID) (*store.MirroredCommunity, string, *store.CommunityQASettings, error) {
	community, err := s.store.GetCommunity(ctx, communityID)
	if err != nil {
		return nil, "", nil, fmt.Errorf("not_found: community not found")
	}

	role := ""
	if viewerID != nil {
		role, err = s.store.GetCommunityMemberRole(ctx, communityID, *viewerID)
		if err != nil {
			return nil, "", nil, err
		}
	}

	if isRestrictedCommunity(community.CommunityType) && !isCommunityMember(role) {
		return community, role, nil, fmt.Errorf("forbidden: community questions are only visible to members")
	}

	settings, err := s.store.GetCommunityQASettings(ctx, communityID)
	if err != nil {
		return nil, "", nil, err
	}

	return community, role, settings, nil
}

func (s *Service) ensureQuestionVisible(ctx context.Context, q *store.Question, viewerID *uuid.UUID) error {
	if q == nil || q.CommunityID == nil {
		return nil
	}
	_, _, _, err := s.getCommunityAccess(ctx, *q.CommunityID, viewerID)
	return err
}

func (s *Service) EnsureQuestionVisible(ctx context.Context, questionID uuid.UUID, viewerID *uuid.UUID) error {
	q, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return fmt.Errorf("not_found: question not found")
	}
	return s.ensureQuestionVisible(ctx, q, viewerID)
}

func (s *Service) ListQuestions(ctx context.Context, viewerID *uuid.UUID, topicSlug string, communityID *uuid.UUID, scope, sortBy, status string, limit, offset int) ([]store.QuestionSummary, error) {
	var topicID *uuid.UUID
	if topicSlug != "" {
		topic, err := s.store.GetTopicBySlug(ctx, topicSlug)
		if err != nil {
			return nil, fmt.Errorf("not_found: topic not found")
		}
		topicID = &topic.ID
	}

	if communityID != nil {
		if _, _, _, err := s.getCommunityAccess(ctx, *communityID, viewerID); err != nil {
			return nil, err
		}
	}

	results, err := s.store.ListQuestions(ctx, viewerID, store.ListQuestionsParams{
		TopicID:     topicID,
		CommunityID: communityID,
		Scope:       scope,
		SortBy:      sortBy,
		Status:      status,
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) ListCommunityQuestions(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID, topicSlug, sortBy, status string, limit, offset int) ([]store.QuestionSummary, []store.CommunityTopicOption, *store.CommunityQASettings, error) {
	_, _, settings, err := s.getCommunityAccess(ctx, communityID, viewerID)
	if err != nil {
		return nil, nil, nil, err
	}

	questions, err := s.ListQuestions(ctx, viewerID, topicSlug, &communityID, "community", sortBy, status, limit, offset)
	if err != nil {
		return nil, nil, nil, err
	}

	availableTopics, err := s.store.ListCommunityAvailableTopics(ctx, communityID)
	if err != nil {
		return nil, nil, nil, err
	}

	return questions, availableTopics, settings, nil
}

func (s *Service) GetCommunityQASettings(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID) (*store.CommunityQASettings, error) {
	_, _, settings, err := s.getCommunityAccess(ctx, communityID, viewerID)
	if err != nil {
		return nil, err
	}
	return settings, nil
}

func validateCommunityPermission(value string) bool {
	switch value {
	case "everyone", "members", "moderators":
		return true
	default:
		return false
	}
}

func (s *Service) UpdateCommunityQASettings(ctx context.Context, communityID, actorID uuid.UUID, p UpdateCommunityQASettingsParams) (*store.CommunityQASettings, error) {
	_, role, current, err := s.getCommunityAccess(ctx, communityID, &actorID)
	if err != nil {
		return nil, err
	}
	if !isCommunityAdmin(role) {
		return nil, fmt.Errorf("forbidden: only community admins can update q&a settings")
	}

	next := *current
	if p.QAEnabled != nil {
		next.QAEnabled = *p.QAEnabled
	}
	if p.AskPermission != nil {
		if !validateCommunityPermission(*p.AskPermission) {
			return nil, fmt.Errorf("invalid: ask_permission must be everyone, members, or moderators")
		}
		next.AskPermission = *p.AskPermission
	}
	if p.AnswerPermission != nil {
		if !validateCommunityPermission(*p.AnswerPermission) {
			return nil, fmt.Errorf("invalid: answer_permission must be everyone, members, or moderators")
		}
		next.AnswerPermission = *p.AnswerPermission
	}
	if p.AutoSuggestTopics != nil {
		next.AutoSuggestTopics = *p.AutoSuggestTopics
	}
	if p.SuggestedTopicIDs != nil {
		next.SuggestedTopicIDs = p.SuggestedTopicIDs
	}
	if p.RequireApproval != nil {
		next.RequireApproval = *p.RequireApproval
	}
	if p.WelcomeMessage != nil {
		next.WelcomeMessage = *p.WelcomeMessage
	}

	return s.store.UpsertCommunityQASettings(ctx, next)
}

func (s *Service) GetCommunityPopularTopics(ctx context.Context, communityID uuid.UUID, viewerID *uuid.UUID, limit int) ([]store.CommunityTopicAffinity, error) {
	if _, _, _, err := s.getCommunityAccess(ctx, communityID, viewerID); err != nil {
		return nil, err
	}
	return s.store.GetCommunityPopularTopics(ctx, communityID, limit)
}

func (s *Service) SetCommunityQuestionPinned(ctx context.Context, communityID, questionID, actorID uuid.UUID, pinned bool, reason string) error {
	_, role, _, err := s.getCommunityAccess(ctx, communityID, &actorID)
	if err != nil {
		return err
	}
	if !isCommunityModerator(role) {
		return fmt.Errorf("forbidden: only moderators and above can pin community questions")
	}

	q, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return err
	}
	if q.CommunityID == nil || *q.CommunityID != communityID {
		return fmt.Errorf("invalid: question does not belong to this community")
	}

	if err := s.store.PinCommunityQuestion(ctx, communityID, questionID, pinned, &actorID, reason); err != nil {
		return err
	}
	if s.producer != nil {
		_ = s.producer.PublishQuestionPinned(ctx, questionID, communityID, actorID, pinned)
	}
	return nil
}
