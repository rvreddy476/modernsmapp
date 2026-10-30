package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	qaevents "github.com/atpost/qa-service/internal/events"
	"github.com/atpost/qa-service/internal/mediaclaims"
	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	store    *store.Store
	rdb      *redis.Client
	producer *qaevents.Producer
	// media is the attach gate and claim protocol for a question's media
	// (media_claims.go). Nil refuses every question that carries media: fail
	// closed.
	media *mediaclaims.Gate
	// holders backs ReleaseRemovedMedia; nil means store (media_claims.go).
	holders MediaHolders
	// limiter enforces per-user posting limits (ratelimit.go); nil (no
	// Redis) disables them.
	limiter RateLimiter
}

func New(s *store.Store, rdb *redis.Client) *Service {
	svc := &Service{store: s, rdb: rdb}
	if rdb != nil {
		svc.limiter = redisLimiter{rdb: rdb}
	}
	return svc
}

func (s *Service) SetProducer(p *qaevents.Producer) {
	s.producer = p
}

func (s *Service) Store() *store.Store {
	return s.store
}

// --- Questions ---

func (s *Service) CreateQuestion(ctx context.Context, authorID uuid.UUID, p store.CreateQuestionParams) (*store.Question, error) {
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" {
		return nil, fmt.Errorf("invalid: title is required")
	}
	if err := checkQuestionText(p.Title, p.Body, p.Tags); err != nil {
		return nil, err
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
	if err := s.checkRate(ctx, authorID, questionRates); err != nil {
		return nil, err
	}
	// The media must be the author's own finished uploads, and is claimed
	// against the question's id before the row exists (media_claims.go).
	mediaIDs, err := parseQuestionMedia(p.MediaIDs)
	if err != nil {
		return nil, err
	}
	p.ID = uuid.New()
	p.MediaIDs = mediaIDStrings(mediaIDs)
	var q *store.Question
	if err := s.media.Attach(ctx, authorID, questionMediaChanges(p.ID, mediaIDs), func() error {
		var err error
		q, err = s.store.CreateQuestion(ctx, authorID, p)
		return err
	}); err != nil {
		return nil, err
	}
	// qa.question.created is enqueued by the store in the insert's
	// transaction (store/outbox.go, audit L2).
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
	var title, body string
	if p.Title != nil {
		if strings.TrimSpace(*p.Title) == "" {
			return nil, fmt.Errorf("invalid: title is required")
		}
		title = *p.Title
	}
	if p.Body != nil {
		body = *p.Body
	}
	if err := checkQuestionText(title, body, nil); err != nil {
		return nil, err
	}
	existing, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return nil, fmt.Errorf("not_found: question not found")
	}
	if existing.AuthorID != authorID {
		return nil, fmt.Errorf("forbidden: only the question author can edit it")
	}
	q, err := s.store.UpdateQuestion(ctx, questionID, p)
	if err != nil {
		return nil, err
	}
	// qa.question.updated is enqueued by the store with the edit (audit L2).
	return q, nil
}

func (s *Service) DeleteQuestion(ctx context.Context, questionID, actorID uuid.UUID) error {
	existing, err := s.store.GetQuestion(ctx, questionID)
	if err != nil {
		return fmt.Errorf("not_found: question not found")
	}
	if existing.AuthorID != actorID {
		return fmt.Errorf("forbidden: only the question author can delete it")
	}
	unlock, err := s.lockMediaHolder(ctx, questionID)
	if err != nil {
		return err
	}
	err = s.store.DeleteQuestion(ctx, questionID)
	unlock() // ReleaseRemovedMedia takes the lock again, per row
	if err != nil {
		return err
	}
	// A deleted question is never shown again (no restore path): its media,
	// and its answers', stops being held (media_claims.go).
	s.ReleaseRemovedMedia(ctx, []uuid.UUID{questionID}, nil)
	if s.producer != nil {
		_ = s.producer.PublishQuestionDeleted(ctx, questionID, actorID)
	}
	return nil
}

// --- Topics ---

func (s *Service) GetTopic(ctx context.Context, topicID uuid.UUID) (*store.Topic, error) {
	return s.store.GetTopic(ctx, topicID)
}

func (s *Service) GetTopicBySlug(ctx context.Context, slug string) (*store.Topic, error) {
	return s.store.GetTopicBySlug(ctx, slug)
}

func (s *Service) ListTopics(ctx context.Context, limit, offset int, featuredOnly bool) ([]store.Topic, error) {
	return s.store.ListTopics(ctx, limit, offset, featuredOnly)
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
	if err := checkAnswerText(body); err != nil {
		return nil, err
	}
	q, _, err := s.visibleQuestion(ctx, questionID, &authorID)
	if err != nil {
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
	if err := s.checkRate(ctx, authorID, answerRates); err != nil {
		return nil, err
	}
	a, err := s.store.CreateAnswer(ctx, questionID, authorID, body, bodyHTML, isAnonymous)
	if err != nil {
		return nil, err
	}
	// qa.answer.created is enqueued by the store with the row (audit L2).
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
	if err := checkAnswerText(body); err != nil {
		return nil, err
	}
	existing, err := s.store.GetAnswer(ctx, answerID)
	if err != nil {
		return nil, fmt.Errorf("not_found: answer not found")
	}
	if existing.AuthorID != authorID {
		return nil, fmt.Errorf("forbidden: only the answer author can edit it")
	}
	return s.store.UpdateAnswer(ctx, answerID, body, bodyHTML)
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

// --- Comments ---

func (s *Service) CreateComment(ctx context.Context, answerID, authorID uuid.UUID, body string) (*store.AnswerComment, error) {
	if err := checkCommentText(body); err != nil {
		return nil, err
	}
	if _, _, err := s.visibleAnswer(ctx, answerID, &authorID); err != nil {
		return nil, err
	}
	if err := s.checkRate(ctx, authorID, commentRates); err != nil {
		return nil, err
	}
	c, err := s.store.CreateComment(ctx, answerID, authorID, body)
	if err != nil {
		return nil, err
	}
	// qa.answer.comment.created is enqueued by the store with the row (audit L2).
	return c, nil
}

// UpdateComment now requires actorID and verifies ownership.
// Audit CQ5: previously had no auth — any caller could rewrite any
// comment.
func (s *Service) UpdateComment(ctx context.Context, commentID, actorID uuid.UUID, body string) (*store.AnswerComment, error) {
	if err := checkCommentText(body); err != nil {
		return nil, err
	}
	existing, err := s.store.GetComment(ctx, commentID)
	if err != nil || existing == nil {
		return nil, fmt.Errorf("not_found: comment not found")
	}
	if existing.AuthorID != actorID {
		return nil, fmt.Errorf("forbidden: only the comment author can edit")
	}
	return s.store.UpdateComment(ctx, commentID, actorID, body)
}

// DeleteComment now requires actorID and verifies ownership (audit CQ5).
func (s *Service) DeleteComment(ctx context.Context, commentID, actorID uuid.UUID) error {
	existing, err := s.store.GetComment(ctx, commentID)
	if err != nil || existing == nil {
		return fmt.Errorf("not_found: comment not found")
	}
	if existing.AuthorID != actorID {
		return fmt.Errorf("forbidden: only the comment author can delete")
	}
	return s.store.DeleteComment(ctx, commentID, actorID)
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

func (s *Service) GetBadges(ctx context.Context, userID uuid.UUID) ([]store.ContributorBadge, error) {
	return s.store.GetBadges(ctx, userID)
}

func (s *Service) GetLeaderboard(ctx context.Context, topicID *uuid.UUID, limit int) ([]store.QAProfile, error) {
	return s.store.GetLeaderboard(ctx, topicID, limit)
}

// --- Moderation ---
//
// Every moderation write is the admin-service token family
// (http/admin_token.go, store/admin_moderation.go); the legacy
// QA_MODERATOR_USER_IDS routes are retired (audit M1).

// HideContent hides one question, answer or comment through the token
// path's store write and releases the media the hidden row held.
func (s *Service) HideContent(ctx context.Context, targetType string, targetID, actorID uuid.UUID, reason string) error {
	if _, err := s.store.AdminHideContent(ctx, actorID, targetType, targetID, nil, reason); err != nil {
		return err
	}
	s.ReleaseHiddenMedia(ctx, targetType, targetID)
	return nil
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
