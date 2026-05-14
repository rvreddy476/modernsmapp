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
	maskAnonymousQuestion(q)
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

// ReopenQuestion now requires the actor to be the question author.
// Audit CQ1: previously had no auth at all — any caller could reopen
// any closed question, bypassing the close decision.
func (s *Service) ReopenQuestion(ctx context.Context, questionID, actorID uuid.UUID) error {
	q, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return err
	}
	if q == nil {
		return fmt.Errorf("question not found")
	}
	if q.AuthorID != actorID {
		return fmt.Errorf("forbidden: only the question author can reopen it")
	}
	return s.store.ReopenQuestion(ctx, questionID)
}

func (s *Service) FindSimilarQuestions(ctx context.Context, title string, limit int) ([]store.QuestionSummary, error) {
	return s.store.GetSimilarQuestions(ctx, title, limit)
}

func (s *Service) SearchQuestions(ctx context.Context, q string, communityID, topicID *uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.SearchQuestions(ctx, q, communityID, topicID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) ListQuestionsByAuthor(ctx context.Context, authorID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.ListQuestionsByAuthor(ctx, authorID, limit, offset)
	if err != nil {
		return nil, err
	}
	// Author's own questions: leave AuthorID intact (they posted, they see).
	// We still surface the is_anonymous flag so the UI can show "posted as anonymous".
	return results, nil
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
	results, err := s.store.ListQuestionsByTopic(ctx, topicID, sortBy, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) GetTopContributors(ctx context.Context, topicID uuid.UUID, limit int) ([]store.QAProfile, error) {
	return s.store.GetTopContributors(ctx, topicID, limit)
}

// --- Answers ---

// maxAnswersPerQuestion caps how many answers can live on a single
// question. Audit HQ5: previously unbounded — a malicious actor could
// dump thousands of answers on one question, blowing up answer_count
// + pagination + downstream notifications.
const maxAnswersPerQuestion = 500

func (s *Service) CreateAnswer(ctx context.Context, questionID, authorID uuid.UUID, body, bodyHTML string, isAnonymous bool) (*store.Answer, error) {
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
	if q.AnswerCount >= maxAnswersPerQuestion {
		return nil, fmt.Errorf("forbidden: question has reached the maximum number of answers (%d)", maxAnswersPerQuestion)
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
	a, err := s.store.CreateAnswer(ctx, questionID, authorID, body, bodyHTML, isAnonymous)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		_ = s.producer.PublishAnswerCreated(ctx, a.ID, questionID, authorID)
	}
	s.awardReputation(ctx, authorID, "answer_posted", ReputationAnswerPosted, "answer", &a.ID)
	return a, nil
}

func (s *Service) GetAnswer(ctx context.Context, answerID uuid.UUID, viewerID *uuid.UUID) (*store.Answer, error) {
	a, err := s.store.GetAnswer(ctx, answerID)
	if err != nil {
		return nil, fmt.Errorf("not_found: answer not found")
	}
	if err := s.EnsureQuestionVisible(ctx, a.QuestionID, viewerID); err != nil {
		return nil, err
	}
	maskAnonymousAnswer(a)
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
	answers, err := s.store.ListAnswersByQuestion(ctx, questionID, sortBy, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range answers {
		maskAnonymousAnswer(&answers[i])
	}
	return answers, nil
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

// UpdateComment now requires actorID and verifies ownership.
// Audit CQ5: previously had no auth — any caller could rewrite any
// comment.
func (s *Service) UpdateComment(ctx context.Context, commentID, actorID uuid.UUID, body string) (*store.AnswerComment, error) {
	existing, err := s.store.GetComment(ctx, commentID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("comment not found")
	}
	if existing.AuthorID != actorID {
		return nil, fmt.Errorf("forbidden: only the comment author can edit")
	}
	return s.store.UpdateComment(ctx, commentID, actorID, body)
}

// DeleteComment now requires actorID and verifies ownership (audit CQ5).
func (s *Service) DeleteComment(ctx context.Context, commentID, actorID uuid.UUID) error {
	existing, err := s.store.GetComment(ctx, commentID)
	if err != nil {
		return err
	}
	if existing == nil {
		return fmt.Errorf("comment not found")
	}
	if existing.AuthorID != actorID {
		return fmt.Errorf("forbidden: only the comment author can delete")
	}
	return s.store.DeleteComment(ctx, commentID, actorID)
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
	results, err := s.store.GetSavedQuestions(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) SaveAnswer(ctx context.Context, userID, answerID uuid.UUID) error {
	return s.store.SaveAnswer(ctx, userID, answerID)
}

func (s *Service) UnsaveAnswer(ctx context.Context, userID, answerID uuid.UUID) error {
	return s.store.UnsaveAnswer(ctx, userID, answerID)
}

func (s *Service) GetSavedAnswers(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.Answer, error) {
	results, err := s.store.GetSavedAnswers(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousAnswer(&results[i])
	}
	return results, nil
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

// RespondToAnswerRequest verifies the actor is the targeted user before
// updating status. Audit CQ2: previously had no scoping at all — any
// authenticated caller could accept/decline any other user's pending
// answer request.
func (s *Service) RespondToAnswerRequest(ctx context.Context, requestID, actorID uuid.UUID, status string) error {
	r, err := s.store.GetAnswerRequestByID(ctx, requestID)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("answer request not found")
	}
	if r.RequestedUserID != actorID {
		return fmt.Errorf("forbidden: this request is not for you")
	}
	return s.store.RespondToAnswerRequest(ctx, requestID, actorID, status)
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
	results, err := s.store.GetHomeFeed(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) GetTrendingQuestions(ctx context.Context, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetTrendingQuestions(ctx, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) GetUnansweredQuestions(ctx context.Context, topicID *uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetUnansweredQuestions(ctx, topicID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) GetFollowingFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetFollowingFeed(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) GetForYouFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetForYouFeed(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) GetLocalFeed(ctx context.Context, lat, lng float64, radiusKm, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetLocalFeed(ctx, lat, lng, radiusKm, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

func (s *Service) GetAnswerQueue(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetAnswerQueue(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results, nil
}

// --- Moderation ---

func (s *Service) CreateReport(ctx context.Context, reporterID uuid.UUID, targetType string, targetID uuid.UUID, reason, details string) (*store.ModerationReport, error) {
	report, err := s.store.CreateReport(ctx, reporterID, targetType, targetID, reason, details)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		switch targetType {
		case "question":
			_ = s.producer.PublishQuestionReported(ctx, report.ID, targetID, reporterID, reason)
		case "answer":
			_ = s.producer.PublishAnswerReported(ctx, report.ID, targetID, reporterID, reason)
		}
	}
	return report, nil
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

// --- anonymity helpers ---
// When is_anonymous = true, the author_id is masked to uuid.Nil and any
// joined Author profile is dropped. The is_anonymous boolean stays true
// on the JSON so clients can render an "Anonymous" label.

func maskAnonymousQuestion(q *store.Question) {
	if q == nil || !q.IsAnonymous {
		return
	}
	q.AuthorID = uuid.Nil
	q.Author = nil
}

func maskAnonymousAnswer(a *store.Answer) {
	if a == nil || !a.IsAnonymous {
		return
	}
	a.AuthorID = uuid.Nil
	a.Author = nil
}

func maskAnonymousSummary(q *store.QuestionSummary) {
	if q == nil || !q.IsAnonymous {
		return
	}
	q.AuthorID = uuid.Nil
	q.Author = nil
}
