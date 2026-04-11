package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	qaevents "github.com/atpost/qa-service/internal/events"
	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	producer *qaevents.Producer
}

func New(s *store.Store, rdb *redis.Client) *Service {
	return &Service{store: s, rdb: rdb}
}

func (s *Service) SetProducer(p *qaevents.Producer) {
	s.producer = p
}

func (s *Service) Store() *store.Store {
	return s.store
}

// --- Questions ---

func (s *Service) CreateQuestion(ctx context.Context, authorID uuid.UUID, p store.CreateQuestionParams) (*store.Question, error) {
	if p.Title == "" {
		return nil, fmt.Errorf("invalid: title is required")
	}
	if len(p.TopicIDs) == 0 && len(p.Topics) == 0 {
		return nil, fmt.Errorf("invalid: at least one topic is required")
	}
	if p.CommunityID != nil {
		community, role, settings, err := s.getCommunityAccess(ctx, *p.CommunityID, &authorID)
		if err != nil {
			return nil, err
		}
		if community == nil {
			return nil, fmt.Errorf("not_found: community not found")
		}
		if !settings.QAEnabled {
			return nil, fmt.Errorf("forbidden: q&a is disabled in this community")
		}
		if !canPerformCommunityAction(settings.AskPermission, role) {
			return nil, fmt.Errorf("forbidden: you cannot ask questions in this community")
		}
		if settings.RequireApproval {
			p.Status = "pending_approval"
		}
	}
	q, err := s.store.CreateQuestion(ctx, authorID, p)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		_ = s.producer.PublishQuestionCreated(ctx, q.ID, authorID, q.Title)
	}
	s.awardReputation(ctx, authorID, "question_asked", ReputationQuestionAsked, "question", &q.ID)
	return q, nil
}

func (s *Service) GetQuestion(ctx context.Context, questionID uuid.UUID, viewerID *uuid.UUID) (*store.Question, error) {
	q, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return nil, fmt.Errorf("not_found: question not found")
	}
	if err := s.ensureQuestionVisible(ctx, q, viewerID); err != nil {
		return nil, err
	}
	// Debounce view count via Redis
	if viewerID != nil && s.rdb != nil {
		key := fmt.Sprintf("qa:viewed:%s:%s", questionID, *viewerID)
		if s.rdb.SetNX(ctx, key, 1, time.Hour).Val() {
			_ = s.store.IncrementQuestionViewCount(ctx, questionID)
		}
	}
	return q, nil
}

func (s *Service) UpdateQuestion(ctx context.Context, questionID, authorID uuid.UUID, p store.UpdateQuestionParams) (*store.Question, error) {
	existing, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return nil, err
	}
	if existing.AuthorID != authorID {
		return nil, fmt.Errorf("not authorized")
	}
	q, err := s.store.UpdateQuestion(ctx, questionID, p)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		_ = s.producer.PublishQuestionUpdated(ctx, questionID, authorID)
	}
	return q, nil
}

func (s *Service) DeleteQuestion(ctx context.Context, questionID, actorID uuid.UUID) error {
	existing, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return err
	}
	if existing.AuthorID != actorID {
		return fmt.Errorf("not authorized")
	}
	if err := s.store.DeleteQuestion(ctx, questionID); err != nil {
		return err
	}
	if s.producer != nil {
		_ = s.producer.PublishQuestionDeleted(ctx, questionID, actorID)
	}
	return nil
}

func (s *Service) CloseQuestion(ctx context.Context, questionID, closedBy uuid.UUID, reason string) error {
	if err := s.store.CloseQuestion(ctx, questionID, closedBy, reason); err != nil {
		return err
	}
	if s.producer != nil {
		_ = s.producer.PublishQuestionClosed(ctx, questionID, closedBy, reason)
	}
	return nil
}

func (s *Service) ReopenQuestion(ctx context.Context, questionID uuid.UUID) error {
	return s.store.ReopenQuestion(ctx, questionID)
}

func (s *Service) FindSimilarQuestions(ctx context.Context, title string, limit int) ([]store.QuestionSummary, error) {
	return s.store.GetSimilarQuestions(ctx, title, limit)
}

func (s *Service) ListQuestionsByAuthor(ctx context.Context, authorID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.ListQuestionsByAuthor(ctx, authorID, limit, offset)
}

// --- Topics ---

func (s *Service) CreateTopic(ctx context.Context, p store.CreateTopicParams) (*store.Topic, error) {
	if p.Name == "" {
		return nil, fmt.Errorf("topic name is required")
	}
	return s.store.CreateTopic(ctx, p)
}

func (s *Service) GetTopic(ctx context.Context, topicID uuid.UUID) (*store.Topic, error) {
	return s.store.GetTopic(ctx, topicID)
}

func (s *Service) GetTopicBySlug(ctx context.Context, slug string) (*store.Topic, error) {
	return s.store.GetTopicBySlug(ctx, slug)
}

func (s *Service) ListTopics(ctx context.Context, limit, offset int, featuredOnly bool) ([]store.Topic, error) {
	return s.store.ListTopics(ctx, limit, offset, featuredOnly)
}

func (s *Service) GetTopicQuestions(ctx context.Context, topicID uuid.UUID, sortBy string, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.ListQuestionsByTopic(ctx, topicID, sortBy, limit, offset)
}

func (s *Service) GetTopContributors(ctx context.Context, topicID uuid.UUID, limit int) ([]store.QAProfile, error) {
	return s.store.GetTopContributors(ctx, topicID, limit)
}

// --- Answers ---

func (s *Service) CreateAnswer(ctx context.Context, questionID, authorID uuid.UUID, body, bodyHTML string) (*store.Answer, error) {
	if body == "" {
		return nil, fmt.Errorf("invalid: answer body is required")
	}
	q, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return nil, fmt.Errorf("not_found: question not found")
	}
	if err := s.ensureQuestionVisible(ctx, q, &authorID); err != nil {
		return nil, err
	}
	if q.Status != "open" {
		return nil, fmt.Errorf("forbidden: question is not open for answers")
	}
	if q.CommunityID != nil {
		_, role, settings, err := s.getCommunityAccess(ctx, *q.CommunityID, &authorID)
		if err != nil {
			return nil, err
		}
		if !settings.QAEnabled {
			return nil, fmt.Errorf("forbidden: q&a is disabled in this community")
		}
		if !canPerformCommunityAction(settings.AnswerPermission, role) {
			return nil, fmt.Errorf("forbidden: you cannot answer in this community")
		}
	}
	a, err := s.store.CreateAnswer(ctx, questionID, authorID, body, bodyHTML)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		_ = s.producer.PublishAnswerCreated(ctx, a.ID, questionID, authorID)
	}
	s.awardReputation(ctx, authorID, "answer_posted", ReputationAnswerPosted, "answer", &a.ID)
	return a, nil
}

func (s *Service) UpdateAnswer(ctx context.Context, answerID, authorID uuid.UUID, body, bodyHTML string) (*store.Answer, error) {
	existing, err := s.store.GetAnswer(ctx, answerID)
	if err != nil {
		return nil, err
	}
	if existing.AuthorID != authorID {
		return nil, fmt.Errorf("not authorized")
	}
	return s.store.UpdateAnswer(ctx, answerID, body, bodyHTML)
}

func (s *Service) DeleteAnswer(ctx context.Context, answerID, actorID uuid.UUID) error {
	existing, err := s.store.GetAnswer(ctx, answerID)
	if err != nil {
		return err
	}
	if existing.AuthorID != actorID {
		return fmt.Errorf("not authorized")
	}
	return s.store.DeleteAnswer(ctx, answerID)
}

func (s *Service) ListAnswers(ctx context.Context, questionID uuid.UUID, viewerID *uuid.UUID, sortBy string, limit, offset int) ([]store.Answer, error) {
	q, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureQuestionVisible(ctx, q, viewerID); err != nil {
		return nil, err
	}
	return s.store.ListAnswersByQuestion(ctx, questionID, sortBy, limit, offset)
}

func (s *Service) SelectBestAnswer(ctx context.Context, questionID, answerID, selectorID uuid.UUID) error {
	q, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return err
	}
	if q.AuthorID != selectorID {
		return fmt.Errorf("only the question author can select the best answer")
	}
	if err := s.store.SelectBestAnswer(ctx, questionID, answerID); err != nil {
		return err
	}
	a, _ := s.store.GetAnswer(ctx, answerID)
	if a != nil {
		s.awardReputation(ctx, a.AuthorID, "best_answer_selected", ReputationBestAnswerSelected, "answer", &answerID)
		s.awardReputation(ctx, selectorID, "best_answer_selector", ReputationBestAnswerSelectorBonus, "question", &questionID)
	}
	if s.producer != nil {
		answerAuthorID := uuid.Nil
		if a != nil {
			answerAuthorID = a.AuthorID
		}
		_ = s.producer.PublishBestAnswerSelected(ctx, questionID, answerID, selectorID, answerAuthorID)
	}
	return nil
}

// --- Comments ---

func (s *Service) CreateComment(ctx context.Context, answerID, authorID uuid.UUID, body string) (*store.AnswerComment, error) {
	if body == "" {
		return nil, fmt.Errorf("comment body is required")
	}
	c, err := s.store.CreateComment(ctx, answerID, authorID, body)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		_ = s.producer.PublishCommentCreated(ctx, c.ID, answerID, authorID)
	}
	return c, nil
}

func (s *Service) UpdateComment(ctx context.Context, commentID uuid.UUID, body string) (*store.AnswerComment, error) {
	return s.store.UpdateComment(ctx, commentID, body)
}

func (s *Service) DeleteComment(ctx context.Context, commentID uuid.UUID) error {
	return s.store.DeleteComment(ctx, commentID)
}

func (s *Service) ListComments(ctx context.Context, answerID uuid.UUID, limit, offset int) ([]store.AnswerComment, error) {
	return s.store.ListCommentsByAnswer(ctx, answerID, limit, offset)
}

// --- Votes ---

func (s *Service) VoteQuestion(ctx context.Context, userID, questionID uuid.UUID, voteType string) error {
	if voteType != "up" && voteType != "down" {
		return fmt.Errorf("vote_type must be 'up' or 'down'")
	}
	if err := s.store.VoteQuestion(ctx, userID, questionID, voteType); err != nil {
		return err
	}
	q, _ := s.store.GetQuestion(ctx, questionID)
	if q != nil && q.AuthorID != userID {
		if voteType == "up" {
			s.awardReputation(ctx, q.AuthorID, "question_upvoted", ReputationQuestionUpvoted, "question", &questionID)
		} else {
			s.awardReputation(ctx, q.AuthorID, "question_downvoted", ReputationQuestionDownvoted, "question", &questionID)
		}
	}
	if s.producer != nil {
		_ = s.producer.PublishQuestionVoted(ctx, questionID, userID, voteType)
	}
	return nil
}

func (s *Service) RemoveQuestionVote(ctx context.Context, userID, questionID uuid.UUID) error {
	return s.store.RemoveQuestionVote(ctx, userID, questionID)
}

func (s *Service) VoteAnswer(ctx context.Context, userID, answerID uuid.UUID, voteType string) error {
	if voteType != "up" && voteType != "down" {
		return fmt.Errorf("vote_type must be 'up' or 'down'")
	}
	if err := s.store.VoteAnswer(ctx, userID, answerID, voteType); err != nil {
		return err
	}
	a, _ := s.store.GetAnswer(ctx, answerID)
	if a != nil && a.AuthorID != userID {
		if voteType == "up" {
			s.awardReputation(ctx, a.AuthorID, "answer_upvoted", ReputationAnswerUpvoted, "answer", &answerID)
		} else {
			s.awardReputation(ctx, a.AuthorID, "answer_downvoted", ReputationAnswerDownvoted, "answer", &answerID)
		}
	}
	if s.producer != nil {
		_ = s.producer.PublishAnswerVoted(ctx, answerID, userID, voteType)
	}
	return nil
}

func (s *Service) RemoveAnswerVote(ctx context.Context, userID, answerID uuid.UUID) error {
	return s.store.RemoveAnswerVote(ctx, userID, answerID)
}

func (s *Service) VoteComment(ctx context.Context, userID, commentID uuid.UUID, voteType string) error {
	if voteType != "up" && voteType != "down" {
		return fmt.Errorf("vote_type must be 'up' or 'down'")
	}
	return s.store.VoteComment(ctx, userID, commentID, voteType)
}

func (s *Service) RemoveCommentVote(ctx context.Context, userID, commentID uuid.UUID) error {
	return s.store.RemoveCommentVote(ctx, userID, commentID)
}

// --- Follows/Saves/Requests ---

func (s *Service) FollowQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	return s.store.FollowQuestion(ctx, userID, questionID)
}

func (s *Service) UnfollowQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	return s.store.UnfollowQuestion(ctx, userID, questionID)
}

func (s *Service) FollowTopic(ctx context.Context, userID, topicID uuid.UUID) error {
	return s.store.FollowTopic(ctx, userID, topicID)
}

func (s *Service) UnfollowTopic(ctx context.Context, userID, topicID uuid.UUID) error {
	return s.store.UnfollowTopic(ctx, userID, topicID)
}

func (s *Service) GetFollowedTopics(ctx context.Context, userID uuid.UUID) ([]store.Topic, error) {
	return s.store.GetFollowedTopics(ctx, userID)
}

func (s *Service) FollowContributor(ctx context.Context, followerID, followedID uuid.UUID) error {
	return s.store.FollowContributor(ctx, followerID, followedID)
}

func (s *Service) UnfollowContributor(ctx context.Context, followerID, followedID uuid.UUID) error {
	return s.store.UnfollowContributor(ctx, followerID, followedID)
}

func (s *Service) SaveQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	return s.store.SaveQuestion(ctx, userID, questionID)
}

func (s *Service) UnsaveQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	return s.store.UnsaveQuestion(ctx, userID, questionID)
}

func (s *Service) GetSavedQuestions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.GetSavedQuestions(ctx, userID, limit, offset)
}

func (s *Service) SaveAnswer(ctx context.Context, userID, answerID uuid.UUID) error {
	return s.store.SaveAnswer(ctx, userID, answerID)
}

func (s *Service) UnsaveAnswer(ctx context.Context, userID, answerID uuid.UUID) error {
	return s.store.UnsaveAnswer(ctx, userID, answerID)
}

func (s *Service) GetSavedAnswers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.Answer, error) {
	return s.store.GetSavedAnswers(ctx, userID, limit, offset)
}

func (s *Service) CreateAnswerRequest(ctx context.Context, questionID, requesterID, requestedUserID uuid.UUID) (*store.AnswerRequest, error) {
	r, err := s.store.CreateAnswerRequest(ctx, questionID, requesterID, requestedUserID)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		_ = s.producer.PublishAnswerRequested(ctx, r.ID, questionID, requesterID, requestedUserID)
	}
	return r, nil
}

func (s *Service) RespondToAnswerRequest(ctx context.Context, requestID uuid.UUID, status string) error {
	return s.store.RespondToAnswerRequest(ctx, requestID, status)
}

func (s *Service) GetAnswerRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.AnswerRequest, error) {
	return s.store.GetAnswerRequestsForUser(ctx, userID, limit, offset)
}

// --- Profile ---

func (s *Service) GetOrCreateProfile(ctx context.Context, userID uuid.UUID) (*store.QAProfile, error) {
	return s.store.GetOrCreateQAProfile(ctx, userID)
}

func (s *Service) GetProfile(ctx context.Context, userID uuid.UUID) (*store.QAProfile, error) {
	return s.store.GetQAProfile(ctx, userID)
}

func (s *Service) UpdateProfile(ctx context.Context, userID uuid.UUID, p store.UpdateProfileParams) (*store.QAProfile, error) {
	return s.store.UpdateQAProfile(ctx, userID, p)
}

// --- Reputation ---

func (s *Service) GetReputationHistory(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.ReputationEvent, error) {
	return s.store.GetReputationHistory(ctx, userID, limit, offset)
}

func (s *Service) GetBadges(ctx context.Context, userID uuid.UUID) ([]store.ContributorBadge, error) {
	return s.store.GetBadges(ctx, userID)
}

func (s *Service) GetLeaderboard(ctx context.Context, topicID *uuid.UUID, limit int) ([]store.QAProfile, error) {
	return s.store.GetLeaderboard(ctx, topicID, limit)
}

// --- Feeds ---

func (s *Service) GetHomeFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.GetHomeFeed(ctx, userID, limit, offset)
}

func (s *Service) GetTrendingQuestions(ctx context.Context, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.GetTrendingQuestions(ctx, limit, offset)
}

func (s *Service) GetUnansweredQuestions(ctx context.Context, topicID *uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.GetUnansweredQuestions(ctx, topicID, limit, offset)
}

func (s *Service) GetFollowingFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.GetFollowingFeed(ctx, userID, limit, offset)
}

func (s *Service) GetForYouFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.GetForYouFeed(ctx, userID, limit, offset)
}

func (s *Service) GetLocalFeed(ctx context.Context, lat, lng float64, radiusKm, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.GetLocalFeed(ctx, lat, lng, radiusKm, limit, offset)
}

func (s *Service) GetAnswerQueue(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.GetAnswerQueue(ctx, userID, limit, offset)
}

// --- Moderation ---

func (s *Service) CreateReport(ctx context.Context, reporterID uuid.UUID, targetType string, targetID uuid.UUID, reason, details string) (*store.ModerationReport, error) {
	return s.store.CreateReport(ctx, reporterID, targetType, targetID, reason, details)
}

func (s *Service) ListReports(ctx context.Context, status string, limit, offset int) ([]store.ModerationReport, error) {
	return s.store.ListReports(ctx, status, limit, offset)
}

func (s *Service) GetReport(ctx context.Context, reportID uuid.UUID) (*store.ModerationReport, error) {
	return s.store.GetReport(ctx, reportID)
}

func (s *Service) ResolveReport(ctx context.Context, reportID, reviewedBy uuid.UUID) error {
	return s.store.UpdateReportStatus(ctx, reportID, "resolved", reviewedBy)
}

func (s *Service) DismissReport(ctx context.Context, reportID, reviewedBy uuid.UUID) error {
	return s.store.UpdateReportStatus(ctx, reportID, "dismissed", reviewedBy)
}

func (s *Service) HideContent(ctx context.Context, targetType string, targetID, actorID uuid.UUID, reason string) error {
	return s.store.HideContent(ctx, targetType, targetID, actorID, reason)
}

func (s *Service) LockQuestion(ctx context.Context, questionID, actorID uuid.UUID, reason string) error {
	return s.store.LockQuestion(ctx, questionID, actorID, reason)
}

func (s *Service) MergeQuestion(ctx context.Context, questionID, mergeIntoID, actorID uuid.UUID) error {
	return s.store.MergeQuestion(ctx, questionID, mergeIntoID, actorID)
}

func (s *Service) MarkDuplicate(ctx context.Context, questionID, duplicateOfID, markedBy uuid.UUID) error {
	return s.store.MarkDuplicate(ctx, questionID, duplicateOfID, markedBy)
}

func (s *Service) ListModerationActions(ctx context.Context, limit, offset int) ([]store.ModerationAction, error) {
	return s.store.ListModerationActions(ctx, limit, offset)
}

// --- internal helpers ---

func (s *Service) awardReputation(ctx context.Context, userID uuid.UUID, eventType string, points int, sourceType string, sourceID *uuid.UUID) {
	if err := s.store.AddReputationEvent(ctx, userID, eventType, points, sourceType, sourceID); err != nil {
		slog.Warn("failed to award reputation", "user_id", userID, "event_type", eventType, "error", err)
	}
}
