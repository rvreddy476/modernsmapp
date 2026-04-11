package events

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafka.Writer
}

func NewProducer(brokers []string, topic string) *Producer {
	return NewProducerWithDialer(brokers, topic, nil)
}

func NewProducerWithDialer(brokers []string, topic string, dialer *kafka.Dialer) *Producer {
	w := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
		Dialer:   dialer,
	})
	return &Producer{writer: w}
}

func (p *Producer) PublishQuestionCreated(ctx context.Context, questionID, authorID uuid.UUID, title string) error {
	return p.publish(ctx, events.EventQAQuestionCreated, &authorID, QuestionCreatedPayload{
		QuestionID: questionID.String(), AuthorID: authorID.String(), Title: title, CreatedAt: time.Now(),
	})
}

func (p *Producer) PublishQuestionUpdated(ctx context.Context, questionID, authorID uuid.UUID) error {
	return p.publish(ctx, events.EventQAQuestionUpdated, &authorID, QuestionUpdatedPayload{
		QuestionID: questionID.String(), AuthorID: authorID.String(), UpdatedAt: time.Now(),
	})
}

func (p *Producer) PublishQuestionDeleted(ctx context.Context, questionID, actorID uuid.UUID) error {
	return p.publish(ctx, events.EventQAQuestionDeleted, &actorID, QuestionDeletedPayload{
		QuestionID: questionID.String(), ActorID: actorID.String(), DeletedAt: time.Now(),
	})
}

func (p *Producer) PublishQuestionClosed(ctx context.Context, questionID, closedBy uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventQAQuestionClosed, &closedBy, QuestionClosedPayload{
		QuestionID: questionID.String(), ClosedBy: closedBy.String(), Reason: reason, ClosedAt: time.Now(),
	})
}

func (p *Producer) PublishAnswerCreated(ctx context.Context, answerID, questionID, authorID uuid.UUID) error {
	return p.publish(ctx, events.EventQAAnswerCreated, &authorID, AnswerCreatedPayload{
		AnswerID: answerID.String(), QuestionID: questionID.String(), AuthorID: authorID.String(), CreatedAt: time.Now(),
	})
}

func (p *Producer) PublishAnswerUpdated(ctx context.Context, answerID, authorID uuid.UUID) error {
	return p.publish(ctx, events.EventQAAnswerUpdated, &authorID, AnswerUpdatedPayload{
		AnswerID: answerID.String(), AuthorID: authorID.String(), UpdatedAt: time.Now(),
	})
}

func (p *Producer) PublishAnswerDeleted(ctx context.Context, answerID, actorID uuid.UUID) error {
	return p.publish(ctx, events.EventQAAnswerDeleted, &actorID, AnswerDeletedPayload{
		AnswerID: answerID.String(), ActorID: actorID.String(), DeletedAt: time.Now(),
	})
}

func (p *Producer) PublishBestAnswerSelected(ctx context.Context, questionID, answerID, selectorID, answerAuthorID uuid.UUID) error {
	return p.publish(ctx, events.EventQABestAnswerSelected, &selectorID, BestAnswerSelectedPayload{
		QuestionID: questionID.String(), AnswerID: answerID.String(),
		SelectorID: selectorID.String(), AnswerAuthorID: answerAuthorID.String(), SelectedAt: time.Now(),
	})
}

func (p *Producer) PublishCommentCreated(ctx context.Context, commentID, answerID, authorID uuid.UUID) error {
	return p.publish(ctx, events.EventQAAnswerCommentCreated, &authorID, CommentCreatedPayload{
		CommentID: commentID.String(), AnswerID: answerID.String(), AuthorID: authorID.String(), CreatedAt: time.Now(),
	})
}

func (p *Producer) PublishQuestionVoted(ctx context.Context, questionID, voterID uuid.UUID, voteType string) error {
	return p.publish(ctx, events.EventQAQuestionVoted, &voterID, VotePayload{
		TargetID: questionID.String(), VoterID: voterID.String(), VoteType: voteType, VotedAt: time.Now(),
	})
}

func (p *Producer) PublishAnswerVoted(ctx context.Context, answerID, voterID uuid.UUID, voteType string) error {
	return p.publish(ctx, events.EventQAAnswerVoted, &voterID, VotePayload{
		TargetID: answerID.String(), VoterID: voterID.String(), VoteType: voteType, VotedAt: time.Now(),
	})
}

func (p *Producer) PublishAnswerRequested(ctx context.Context, requestID, questionID, requesterID, requestedUserID uuid.UUID) error {
	return p.publish(ctx, events.EventQAAnswerRequested, &requesterID, AnswerRequestedPayload{
		RequestID: requestID.String(), QuestionID: questionID.String(),
		RequesterID: requesterID.String(), RequestedUserID: requestedUserID.String(), RequestedAt: time.Now(),
	})
}

func (p *Producer) PublishReputationChanged(ctx context.Context, userID uuid.UUID, eventType string, points, newScore int) error {
	return p.publish(ctx, events.EventQAReputationChanged, &userID, ReputationChangedPayload{
		UserID: userID.String(), EventType: eventType, Points: points, NewScore: newScore, ChangedAt: time.Now(),
	})
}

func (p *Producer) PublishModerationAction(ctx context.Context, actionID, actorID uuid.UUID, targetType string, targetID uuid.UUID, actionType string) error {
	return p.publish(ctx, events.EventQAModerationAction, &actorID, ModerationActionPayload{
		ActionID: actionID.String(), ActorID: actorID.String(),
		TargetType: targetType, TargetID: targetID.String(), ActionType: actionType, ActedAt: time.Now(),
	})
}

func (p *Producer) publish(ctx context.Context, eventType string, actorID *uuid.UUID, payload any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	var actorStr *string
	if actorID != nil {
		s := actorID.String()
		actorStr = &s
	}
	envelope := events.NewEnvelope(ctx, eventType, actorStr, payloadBytes)
	envelopeBytes, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal envelope: %w", err)
	}
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(envelope.EventID),
		Value: envelopeBytes,
	})
}

func (p *Producer) Close() error {
	return p.writer.Close()
}

// --- Payload types ---

type QuestionCreatedPayload struct {
	QuestionID string    `json:"question_id"`
	AuthorID   string    `json:"author_id"`
	Title      string    `json:"title"`
	CreatedAt  time.Time `json:"created_at"`
}

type QuestionUpdatedPayload struct {
	QuestionID string    `json:"question_id"`
	AuthorID   string    `json:"author_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type QuestionDeletedPayload struct {
	QuestionID string    `json:"question_id"`
	ActorID    string    `json:"actor_id"`
	DeletedAt  time.Time `json:"deleted_at"`
}

type QuestionClosedPayload struct {
	QuestionID string    `json:"question_id"`
	ClosedBy   string    `json:"closed_by"`
	Reason     string    `json:"reason"`
	ClosedAt   time.Time `json:"closed_at"`
}

type AnswerCreatedPayload struct {
	AnswerID   string    `json:"answer_id"`
	QuestionID string    `json:"question_id"`
	AuthorID   string    `json:"author_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type AnswerUpdatedPayload struct {
	AnswerID  string    `json:"answer_id"`
	AuthorID  string    `json:"author_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AnswerDeletedPayload struct {
	AnswerID  string    `json:"answer_id"`
	ActorID   string    `json:"actor_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

type BestAnswerSelectedPayload struct {
	QuestionID     string    `json:"question_id"`
	AnswerID       string    `json:"answer_id"`
	SelectorID     string    `json:"selector_id"`
	AnswerAuthorID string    `json:"answer_author_id"`
	SelectedAt     time.Time `json:"selected_at"`
}

type CommentCreatedPayload struct {
	CommentID string    `json:"comment_id"`
	AnswerID  string    `json:"answer_id"`
	AuthorID  string    `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

type VotePayload struct {
	TargetID string    `json:"target_id"`
	VoterID  string    `json:"voter_id"`
	VoteType string    `json:"vote_type"`
	VotedAt  time.Time `json:"voted_at"`
}

type AnswerRequestedPayload struct {
	RequestID       string    `json:"request_id"`
	QuestionID      string    `json:"question_id"`
	RequesterID     string    `json:"requester_id"`
	RequestedUserID string    `json:"requested_user_id"`
	RequestedAt     time.Time `json:"requested_at"`
}

type ReputationChangedPayload struct {
	UserID    string    `json:"user_id"`
	EventType string    `json:"event_type"`
	Points    int       `json:"points"`
	NewScore  int       `json:"new_score"`
	ChangedAt time.Time `json:"changed_at"`
}

type ModerationActionPayload struct {
	ActionID   string    `json:"action_id"`
	ActorID    string    `json:"actor_id"`
	TargetType string    `json:"target_type"`
	TargetID   string    `json:"target_id"`
	ActionType string    `json:"action_type"`
	ActedAt    time.Time `json:"acted_at"`
}
