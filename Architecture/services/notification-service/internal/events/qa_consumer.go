package events

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// qaClient is a small read-only HTTP client over qa-service used to look up
// the author_id for events whose payload only carries the question/answer ID.
// We make this lookup so the producer payloads can stay narrow.
type qaClient struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func newQAClient() *qaClient {
	baseURL := os.Getenv("QA_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://qa-service:8108"
	}
	return &qaClient{
		baseURL:     baseURL,
		internalKey: os.Getenv("INTERNAL_SERVICE_KEY"),
		http:        &http.Client{Timeout: 6 * time.Second},
	}
}

// qaEnvelope mirrors the api.JSON response shape used by qa-service.
type qaEnvelope[T any] struct {
	Data T `json:"data"`
}

type qaQuestionResponse struct {
	ID          string `json:"id"`
	AuthorID    string `json:"author_id"`
	Title       string `json:"title"`
	IsAnonymous bool   `json:"is_anonymous"`
	Community   *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"community,omitempty"`
}

type qaAnswerResponse struct {
	ID          string `json:"id"`
	QuestionID  string `json:"question_id"`
	AuthorID    string `json:"author_id"`
	IsAnonymous bool   `json:"is_anonymous"`
}

func (c *qaClient) getQuestion(ctx context.Context, id string) (*qaQuestionResponse, error) {
	url := fmt.Sprintf("%s/v1/qa/questions/%s", c.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qa get question: status %d", resp.StatusCode)
	}
	// qa-service responds with the question object directly (not enveloped) in
	// most paths; try both shapes for safety.
	var direct qaQuestionResponse
	body, err := decodeJSON(resp.Body, &direct)
	if err == nil && direct.ID != "" {
		return &direct, nil
	}
	var enveloped qaEnvelope[qaQuestionResponse]
	if err := json.Unmarshal(body, &enveloped); err == nil && enveloped.Data.ID != "" {
		return &enveloped.Data, nil
	}
	return &direct, nil
}

func (c *qaClient) getAnswer(ctx context.Context, id string) (*qaAnswerResponse, error) {
	url := fmt.Sprintf("%s/v1/qa/answers/%s", c.baseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qa get answer: status %d", resp.StatusCode)
	}
	var direct qaAnswerResponse
	body, err := decodeJSON(resp.Body, &direct)
	if err == nil && direct.ID != "" {
		return &direct, nil
	}
	var enveloped qaEnvelope[qaAnswerResponse]
	if err := json.Unmarshal(body, &enveloped); err == nil && enveloped.Data.ID != "" {
		return &enveloped.Data, nil
	}
	return &direct, nil
}

// decodeJSON reads the body, attempts to decode into v, and returns the body
// bytes so the caller can re-decode into a different envelope shape.
func decodeJSON(r io.Reader, v any) ([]byte, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return b, json.Unmarshal(b, v)
}

// handleQAEvent routes a Q&A event envelope to the notification creator.
// Returns true when the event was handled (so the parent consumer doesn't
// fall through to the default branch).
func (c *Consumer) handleQAEvent(ctx context.Context, envelope events.EventEnvelope) (bool, error) {
	switch envelope.EventType {
	case events.EventQAAnswerCreated:
		return true, c.handleQAAnswerCreated(ctx, envelope.Payload)
	case events.EventQABestAnswerSelected:
		return true, c.handleQABestAnswerSelected(ctx, envelope.Payload)
	case events.EventQAAnswerCommentCreated:
		return true, c.handleQAAnswerCommentCreated(ctx, envelope.Payload)
	case events.EventQAAnswerRequested:
		return true, c.handleQAAnswerRequested(ctx, envelope.Payload)
	case events.EventQAQuestionVoted:
		return true, c.handleQAQuestionVoted(ctx, envelope.Payload)
	case events.EventQAAnswerVoted:
		return true, c.handleQAAnswerVoted(ctx, envelope.Payload)
	case events.EventQAQuestionPinned:
		return true, c.handleQAQuestionPinned(ctx, envelope.Payload)
	case events.EventQAQuestionReported, events.EventQAAnswerReported:
		// V1: log and skip — moderation queue is admin-only.
		slog.Info("qa moderation report received", "event_type", envelope.EventType)
		return true, nil
	}
	return false, nil
}

type qaAnswerCreatedPayload struct {
	AnswerID   string    `json:"answer_id"`
	QuestionID string    `json:"question_id"`
	AuthorID   string    `json:"author_id"`
	CreatedAt  time.Time `json:"created_at"`
}

func (c *Consumer) handleQAAnswerCreated(ctx context.Context, raw json.RawMessage) error {
	var e qaAnswerCreatedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	answererID, _ := uuid.Parse(e.AuthorID)
	questionID, _ := uuid.Parse(e.QuestionID)

	q, err := newQAClient().getQuestion(ctx, e.QuestionID)
	if err != nil || q == nil || q.AuthorID == "" {
		slog.Warn("qa: lookup question for AnswerCreated failed", "question_id", e.QuestionID, "error", err)
		return nil
	}
	questionAuthorID, _ := uuid.Parse(q.AuthorID)
	if questionAuthorID == uuid.Nil || questionAuthorID == answererID {
		return nil
	}
	deepLink := fmt.Sprintf("/qa/questions/%s", e.QuestionID)
	return c.service.CreateNotification(ctx, questionAuthorID, answererID, "qa.answer.created", "qa_question", questionID, deepLink, e.CreatedAt)
}

type qaBestAnswerPayload struct {
	QuestionID     string    `json:"question_id"`
	AnswerID       string    `json:"answer_id"`
	SelectorID     string    `json:"selector_id"`
	AnswerAuthorID string    `json:"answer_author_id"`
	SelectedAt     time.Time `json:"selected_at"`
}

func (c *Consumer) handleQABestAnswerSelected(ctx context.Context, raw json.RawMessage) error {
	var e qaBestAnswerPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	answerAuthorID, _ := uuid.Parse(e.AnswerAuthorID)
	selectorID, _ := uuid.Parse(e.SelectorID)
	answerID, _ := uuid.Parse(e.AnswerID)
	if answerAuthorID == uuid.Nil || answerAuthorID == selectorID {
		return nil
	}
	deepLink := fmt.Sprintf("/qa/questions/%s#answer-%s", e.QuestionID, e.AnswerID)
	return c.service.CreateNotification(ctx, answerAuthorID, selectorID, "qa.answer.best_selected", "qa_answer", answerID, deepLink, e.SelectedAt)
}

type qaCommentCreatedPayload struct {
	CommentID string    `json:"comment_id"`
	AnswerID  string    `json:"answer_id"`
	AuthorID  string    `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

func (c *Consumer) handleQAAnswerCommentCreated(ctx context.Context, raw json.RawMessage) error {
	var e qaCommentCreatedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	commenterID, _ := uuid.Parse(e.AuthorID)
	answerID, _ := uuid.Parse(e.AnswerID)
	a, err := newQAClient().getAnswer(ctx, e.AnswerID)
	if err != nil || a == nil || a.AuthorID == "" {
		slog.Warn("qa: lookup answer for CommentCreated failed", "answer_id", e.AnswerID, "error", err)
		return nil
	}
	answerAuthorID, _ := uuid.Parse(a.AuthorID)
	if answerAuthorID == uuid.Nil || answerAuthorID == commenterID {
		return nil
	}
	deepLink := fmt.Sprintf("/qa/answers/%s", e.AnswerID)
	return c.service.CreateNotification(ctx, answerAuthorID, commenterID, "qa.answer.comment.created", "qa_answer", answerID, deepLink, e.CreatedAt)
}

type qaAnswerRequestedPayload struct {
	RequestID       string    `json:"request_id"`
	QuestionID      string    `json:"question_id"`
	RequesterID     string    `json:"requester_id"`
	RequestedUserID string    `json:"requested_user_id"`
	RequestedAt     time.Time `json:"requested_at"`
}

func (c *Consumer) handleQAAnswerRequested(ctx context.Context, raw json.RawMessage) error {
	var e qaAnswerRequestedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	requestedID, _ := uuid.Parse(e.RequestedUserID)
	requesterID, _ := uuid.Parse(e.RequesterID)
	questionID, _ := uuid.Parse(e.QuestionID)
	if requestedID == uuid.Nil || requestedID == requesterID {
		return nil
	}
	deepLink := fmt.Sprintf("/qa/questions/%s", e.QuestionID)
	return c.service.CreateNotification(ctx, requestedID, requesterID, "qa.answer.requested", "qa_question", questionID, deepLink, e.RequestedAt)
}

type qaVotePayload struct {
	TargetID string    `json:"target_id"`
	VoterID  string    `json:"voter_id"`
	VoteType string    `json:"vote_type"`
	VotedAt  time.Time `json:"voted_at"`
}

func (c *Consumer) handleQAQuestionVoted(ctx context.Context, raw json.RawMessage) error {
	var e qaVotePayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	if e.VoteType != "up" {
		return nil
	}
	voterID, _ := uuid.Parse(e.VoterID)
	questionID, _ := uuid.Parse(e.TargetID)
	q, err := newQAClient().getQuestion(ctx, e.TargetID)
	if err != nil || q == nil || q.AuthorID == "" {
		slog.Warn("qa: lookup question for QuestionVoted failed", "question_id", e.TargetID, "error", err)
		return nil
	}
	questionAuthorID, _ := uuid.Parse(q.AuthorID)
	if questionAuthorID == uuid.Nil || questionAuthorID == voterID {
		return nil
	}
	deepLink := fmt.Sprintf("/qa/questions/%s", e.TargetID)
	return c.service.CreateNotification(ctx, questionAuthorID, voterID, "qa.question.voted", "qa_question", questionID, deepLink, e.VotedAt)
}

func (c *Consumer) handleQAAnswerVoted(ctx context.Context, raw json.RawMessage) error {
	var e qaVotePayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	if e.VoteType != "up" {
		return nil
	}
	voterID, _ := uuid.Parse(e.VoterID)
	answerID, _ := uuid.Parse(e.TargetID)
	a, err := newQAClient().getAnswer(ctx, e.TargetID)
	if err != nil || a == nil || a.AuthorID == "" {
		slog.Warn("qa: lookup answer for AnswerVoted failed", "answer_id", e.TargetID, "error", err)
		return nil
	}
	answerAuthorID, _ := uuid.Parse(a.AuthorID)
	if answerAuthorID == uuid.Nil || answerAuthorID == voterID {
		return nil
	}
	deepLink := fmt.Sprintf("/qa/answers/%s", e.TargetID)
	return c.service.CreateNotification(ctx, answerAuthorID, voterID, "qa.answer.voted", "qa_answer", answerID, deepLink, e.VotedAt)
}

type qaQuestionPinnedPayload struct {
	QuestionID  string    `json:"question_id"`
	CommunityID string    `json:"community_id"`
	ActorID     string    `json:"actor_id"`
	Pinned      bool      `json:"pinned"`
	At          time.Time `json:"at"`
}

func (c *Consumer) handleQAQuestionPinned(ctx context.Context, raw json.RawMessage) error {
	var e qaQuestionPinnedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	// Only notify on pin events, not unpin.
	if !e.Pinned {
		return nil
	}
	actorID, _ := uuid.Parse(e.ActorID)
	questionID, _ := uuid.Parse(e.QuestionID)
	q, err := newQAClient().getQuestion(ctx, e.QuestionID)
	if err != nil || q == nil || q.AuthorID == "" {
		slog.Warn("qa: lookup question for QuestionPinned failed", "question_id", e.QuestionID, "error", err)
		return nil
	}
	questionAuthorID, _ := uuid.Parse(q.AuthorID)
	if questionAuthorID == uuid.Nil || questionAuthorID == actorID {
		return nil
	}
	deepLink := fmt.Sprintf("/qa/questions/%s", e.QuestionID)
	return c.service.CreateNotification(ctx, questionAuthorID, actorID, "qa.question.pinned", "qa_question", questionID, deepLink, e.At)
}
