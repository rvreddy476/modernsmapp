package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Reads and engagement under the one visibility rule (audit H5, H4, M4).

func maskSummaries(results []store.QuestionSummary) []store.QuestionSummary {
	for i := range results {
		maskAnonymousSummary(&results[i])
	}
	return results
}

// --- Comments ---

// ListComments reads the comments of a live answer the viewer can see.
func (s *Service) ListComments(ctx context.Context, answerID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.AnswerComment, error) {
	if _, _, err := s.visibleAnswer(ctx, answerID, viewerID); err != nil {
		return nil, err
	}
	return s.store.ListCommentsByAnswer(ctx, answerID, limit, offset)
}

// --- Follows / saves ---

func (s *Service) FollowQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	if _, _, err := s.visibleQuestion(ctx, questionID, &userID); err != nil {
		return err
	}
	return s.store.FollowQuestion(ctx, userID, questionID)
}

func (s *Service) UnfollowQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	return s.store.UnfollowQuestion(ctx, userID, questionID)
}

func (s *Service) SaveQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	if _, _, err := s.visibleQuestion(ctx, questionID, &userID); err != nil {
		return err
	}
	return s.store.SaveQuestion(ctx, userID, questionID)
}

func (s *Service) UnsaveQuestion(ctx context.Context, userID, questionID uuid.UUID) error {
	return s.store.UnsaveQuestion(ctx, userID, questionID)
}

// GetSavedQuestions lists only saved questions the user can still see: a
// save made before a question went private, pending or deleted does not keep
// it readable.
func (s *Service) GetSavedQuestions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetSavedQuestions(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) SaveAnswer(ctx context.Context, userID, answerID uuid.UUID) error {
	if _, _, err := s.visibleAnswer(ctx, answerID, &userID); err != nil {
		return err
	}
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

// --- Answer requests ---

// CreateAnswerRequest asks requestedUserID to answer. The requester must see
// the question, so must the target (no pushes about a private community's
// question to a non-member), the question must be open, a requester asks a
// given user once per question (store unique index), and a requester has a
// daily cap (M4).
func (s *Service) CreateAnswerRequest(ctx context.Context, questionID, requesterID, requestedUserID uuid.UUID) (*store.AnswerRequest, error) {
	q, _, err := s.visibleQuestion(ctx, questionID, &requesterID)
	if err != nil {
		return nil, err
	}
	if err := answerRequestDecision(q.Status, requesterID, requestedUserID); err != nil {
		return nil, err
	}
	if _, err := s.questionAccess(ctx, q, &requestedUserID); err != nil {
		return nil, fmt.Errorf("forbidden: that user cannot see this question")
	}
	if err := s.checkRate(ctx, requesterID, answerRequestRates); err != nil {
		return nil, err
	}
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
	if status != "accepted" && status != "declined" {
		return fmt.Errorf("invalid: status must be 'accepted' or 'declined'")
	}
	r, err := s.store.GetAnswerRequestByID(ctx, requestID)
	if err != nil || r == nil {
		return fmt.Errorf("not_found: answer request not found")
	}
	if r.RequestedUserID != actorID {
		return fmt.Errorf("forbidden: this request is not for you")
	}
	return s.store.RespondToAnswerRequest(ctx, requestID, actorID, status)
}

func (s *Service) GetAnswerRequests(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.AnswerRequest, error) {
	return s.store.GetAnswerRequestsForUser(ctx, userID, limit, offset)
}

// --- Reports ---

// CreateReport files a report on something the reporter can see and did not
// write. One open report per (reporter, target): a repeat while the first is
// open returns that report with created false (the HTTP layer answers 200
// instead of 201), writes nothing, and does not count against the daily
// report limit. Once a moderator resolves or dismisses it, the reporter may
// report the target again.
func (s *Service) CreateReport(ctx context.Context, reporterID uuid.UUID, targetType string, targetID uuid.UUID, reason, details string) (*store.ModerationReport, bool, error) {
	switch targetType {
	case "question", "answer", "comment", "user":
	default:
		return nil, false, fmt.Errorf("invalid: target_type must be question, answer, comment or user")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, false, fmt.Errorf("invalid: reason is required")
	}
	if err := checkLen("reason", reason, maxReportReasonLen); err != nil {
		return nil, false, err
	}
	if err := checkLen("details", details, maxReportDetailLen); err != nil {
		return nil, false, err
	}
	owner, accessErr := s.reportTargetOwner(ctx, targetType, targetID, reporterID)
	if err := reportTargetDecision(targetType, reporterID, owner, accessErr); err != nil {
		return nil, false, err
	}
	existing, err := s.store.OpenReport(ctx, reporterID, targetType, targetID)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}
	if err := s.checkRate(ctx, reporterID, reportRates); err != nil {
		return nil, false, err
	}
	report, created, err := s.store.CreateReport(ctx, reporterID, targetType, targetID, reason, details)
	if err != nil {
		return nil, false, err
	}
	if created && s.producer != nil {
		switch targetType {
		case "question":
			_ = s.producer.PublishQuestionReported(ctx, report.ID, targetID, reporterID, reason)
		case "answer":
			_ = s.producer.PublishAnswerReported(ctx, report.ID, targetID, reporterID, reason)
		}
	}
	return report, created, nil
}

// reportTargetOwner loads the reported target as reporter would see it
// (the shared visibility rule, access.go) and returns who wrote it; for a
// user target, the user. A user exists for Q&A when they have a Q&A profile
// (the same record GET /profiles/:userId serves).
func (s *Service) reportTargetOwner(ctx context.Context, targetType string, targetID, reporter uuid.UUID) (uuid.UUID, error) {
	switch targetType {
	case "question":
		q, _, err := s.visibleQuestion(ctx, targetID, &reporter)
		if err != nil {
			return uuid.Nil, err
		}
		return q.AuthorID, nil
	case "answer":
		a, _, err := s.visibleAnswer(ctx, targetID, &reporter)
		if err != nil {
			return uuid.Nil, err
		}
		return a.AuthorID, nil
	case "comment":
		c, err := s.visibleComment(ctx, targetID, &reporter)
		if err != nil {
			return uuid.Nil, err
		}
		return c.AuthorID, nil
	case "user":
		p, err := s.store.GetQAProfile(ctx, targetID)
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, fmt.Errorf("not_found: user not found")
		}
		if err != nil {
			return uuid.Nil, err
		}
		return p.UserID, nil
	}
	return uuid.Nil, fmt.Errorf("invalid: target_type must be question, answer, comment or user")
}

// --- Profile lists (H4) ---

// ListQuestionsByAuthor is a profile's question list. The author sees all
// their live questions, anonymous ones included (unmasked: they posted
// them). Anyone else never sees an anonymous question, nor one hidden from
// them by the visibility rule.
func (s *Service) ListQuestionsByAuthor(ctx context.Context, authorID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	return s.store.ListQuestionsByAuthor(ctx, authorID, viewerID, limit, offset)
}

// ListAnswersByAuthor is the same rule for answers.
func (s *Service) ListAnswersByAuthor(ctx context.Context, authorID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.Answer, error) {
	return s.store.ListAnswersByAuthor(ctx, authorID, viewerID, limit, offset)
}

// GetReputationHistory hides, from anyone but the owner, every event whose
// source is an anonymous post.
func (s *Service) GetReputationHistory(ctx context.Context, userID uuid.UUID, viewerID *uuid.UUID, limit, offset int) ([]store.ReputationEvent, error) {
	return s.store.GetReputationHistory(ctx, userID, viewerID, limit, offset)
}

// --- Search, topics, feeds (H5) ---

func (s *Service) FindSimilarQuestions(ctx context.Context, title string, viewerID *uuid.UUID, limit int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetSimilarQuestions(ctx, title, viewerID, limit)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

// SearchQuestions applies the visibility rule to every hit, and a
// community_id filter is refused outright to a viewer who may not see that
// community.
func (s *Service) SearchQuestions(ctx context.Context, q string, communityID, topicID, viewerID *uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	if communityID != nil {
		if _, _, _, err := s.getCommunityAccess(ctx, *communityID, viewerID); err != nil {
			return nil, err
		}
	}
	results, err := s.store.SearchQuestions(ctx, q, communityID, topicID, viewerID, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) GetTopicQuestions(ctx context.Context, topicID uuid.UUID, viewerID *uuid.UUID, sortBy string, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.ListQuestionsByTopic(ctx, topicID, viewerID, sortBy, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) GetHomeFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetHomeFeed(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) GetTrendingQuestions(ctx context.Context, viewerID *uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetTrendingQuestions(ctx, viewerID, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) GetUnansweredQuestions(ctx context.Context, topicID, viewerID *uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetUnansweredQuestions(ctx, topicID, viewerID, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) GetFollowingFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetFollowingFeed(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) GetForYouFeed(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetForYouFeed(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) GetLocalFeed(ctx context.Context, viewerID *uuid.UUID, lat, lng float64, radiusKm, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetLocalFeed(ctx, viewerID, lat, lng, radiusKm, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}

func (s *Service) GetAnswerQueue(ctx context.Context, userID uuid.UUID, limit, offset int) ([]store.QuestionSummary, error) {
	results, err := s.store.GetAnswerQueue(ctx, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	return maskSummaries(results), nil
}
