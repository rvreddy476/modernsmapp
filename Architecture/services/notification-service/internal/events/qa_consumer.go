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

// qaClient is a small read-only HTTP client over qa-service's public reads,
// used only for events published before qa-service put the recipient in the
// payload (see qaDelivery below). It cannot see anonymous authors (masked)
// or private-community questions (403), which is why the payloads carry the
// recipient now.
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
	var lookup qaAuthorLookup = lazyQAClient{}
	var d *qaDelivery
	var err error
	switch envelope.EventType {
	case events.EventQAAnswerCreated:
		d, err = qaAnswerCreatedDelivery(ctx, envelope.Payload, lookup)
	case events.EventQABestAnswerSelected:
		d, err = qaBestAnswerDelivery(ctx, envelope.Payload, lookup)
	case events.EventQAAnswerCommentCreated:
		d, err = qaCommentCreatedDelivery(ctx, envelope.Payload, lookup)
	case events.EventQAAnswerRequested:
		d, err = qaAnswerRequestedDelivery(envelope.Payload)
	case events.EventQAQuestionVoted:
		d, err = qaVoteDelivery(ctx, envelope.Payload, lookup, false)
	case events.EventQAAnswerVoted:
		d, err = qaVoteDelivery(ctx, envelope.Payload, lookup, true)
	case events.EventQAQuestionPinned:
		d, err = qaQuestionPinnedDelivery(ctx, envelope.Payload, lookup)
	case events.EventQAQuestionReported, events.EventQAAnswerReported:
		// V1: log and skip — moderation queue is admin-only.
		slog.Info("qa moderation report received", "event_type", envelope.EventType)
		return true, nil
	default:
		return false, nil
	}
	if err != nil || d == nil {
		return true, err
	}
	return true, c.service.CreateNotification(ctx, d.Recipient, d.Actor, d.Type, d.EntityType, d.EntityID, d.DeepLink, d.At)
}

// ── Recipients and actors (Q&A audit H4 and M10, 30 Oct 2026) ──────────────
//
// qa-service now puts the recipient in every payload (question_author_id,
// answer_author_id, target_author_id) and says when the acting user posted
// anonymously (is_anonymous on answer.created, selector_anonymous on
// best_selected). The consumer used to look the recipient up through
// qa-service's public GET, which masks an anonymous author to the nil id and
// refuses a private-community question, so askers of those questions were
// never told about answers, votes or pins; and it named an anonymous
// answerer (or an anonymous asker accepting an answer) as the actor.
//
// Now an anonymous actor is never named: the notification is sent with no
// actor ("Someone answered your question"). Events from before the change
// (no recipient field) fall back to the old lookup; when that lookup cannot
// tell whether the actor was anonymous, the actor is left out.

// qaDelivery is one notification to write.
type qaDelivery struct {
	Recipient  uuid.UUID
	Actor      uuid.UUID // uuid.Nil: no actor is named
	Type       string
	EntityType string
	EntityID   uuid.UUID
	DeepLink   string
	At         time.Time
}

// qaAuthorLookup is the legacy recipient lookup (qaClient).
type qaAuthorLookup interface {
	getQuestion(ctx context.Context, id string) (*qaQuestionResponse, error)
	getAnswer(ctx context.Context, id string) (*qaAnswerResponse, error)
}

// lazyQAClient builds the HTTP client only when a legacy event needs it.
type lazyQAClient struct{}

func (lazyQAClient) getQuestion(ctx context.Context, id string) (*qaQuestionResponse, error) {
	return newQAClient().getQuestion(ctx, id)
}

func (lazyQAClient) getAnswer(ctx context.Context, id string) (*qaAnswerResponse, error) {
	return newQAClient().getAnswer(ctx, id)
}

// parseID parses a payload id; "" or garbage is uuid.Nil.
func parseID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// legacyQuestionAuthor is the question's author by lookup, or uuid.Nil.
func legacyQuestionAuthor(ctx context.Context, lookup qaAuthorLookup, questionID, event string) uuid.UUID {
	q, err := lookup.getQuestion(ctx, questionID)
	if err != nil || q == nil {
		slog.Warn("qa: legacy question author lookup failed", "event", event, "question_id", questionID, "error", err)
		return uuid.Nil
	}
	return parseID(q.AuthorID)
}

// legacyAnswerAuthor is the answer's author by lookup, or uuid.Nil.
func legacyAnswerAuthor(ctx context.Context, lookup qaAuthorLookup, answerID, event string) uuid.UUID {
	a, err := lookup.getAnswer(ctx, answerID)
	if err != nil || a == nil {
		slog.Warn("qa: legacy answer author lookup failed", "event", event, "answer_id", answerID, "error", err)
		return uuid.Nil
	}
	return parseID(a.AuthorID)
}

// notifiable reports whether recipient should be told about actorUser's
// action: someone to tell, and not their own action.
func notifiable(recipient, actorUser uuid.UUID) bool {
	return recipient != uuid.Nil && recipient != actorUser
}

// namedActor is actorUser, or no actor when they acted anonymously.
func namedActor(actorUser uuid.UUID, anonymous bool) uuid.UUID {
	if anonymous {
		return uuid.Nil
	}
	return actorUser
}

type qaAnswerCreatedPayload struct {
	AnswerID         string    `json:"answer_id"`
	QuestionID       string    `json:"question_id"`
	AuthorID         string    `json:"author_id"`
	IsAnonymous      *bool     `json:"is_anonymous"`
	QuestionAuthorID string    `json:"question_author_id"`
	CreatedAt        time.Time `json:"created_at"`
}

func qaAnswerCreatedDelivery(ctx context.Context, raw json.RawMessage, lookup qaAuthorLookup) (*qaDelivery, error) {
	var e qaAnswerCreatedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return nil, err
	}
	answererID := parseID(e.AuthorID)
	recipient := parseID(e.QuestionAuthorID)
	anonymous := e.IsAnonymous != nil && *e.IsAnonymous
	if e.QuestionAuthorID == "" { // legacy event
		recipient = legacyQuestionAuthor(ctx, lookup, e.QuestionID, "answer.created")
		if e.IsAnonymous == nil {
			// The old payload never said; the public answer read masks an
			// anonymous author. Unknown means do not name them.
			a, err := lookup.getAnswer(ctx, e.AnswerID)
			anonymous = err != nil || a == nil || a.IsAnonymous || parseID(a.AuthorID) == uuid.Nil
		}
	}
	if !notifiable(recipient, answererID) {
		return nil, nil
	}
	return &qaDelivery{
		Recipient: recipient, Actor: namedActor(answererID, anonymous),
		Type: "qa.answer.created", EntityType: "qa_question", EntityID: parseID(e.QuestionID),
		DeepLink: fmt.Sprintf("/qa/questions/%s", e.QuestionID), At: e.CreatedAt,
	}, nil
}

type qaBestAnswerPayload struct {
	QuestionID        string    `json:"question_id"`
	AnswerID          string    `json:"answer_id"`
	SelectorID        string    `json:"selector_id"`
	SelectorAnonymous *bool     `json:"selector_anonymous"`
	AnswerAuthorID    string    `json:"answer_author_id"`
	SelectedAt        time.Time `json:"selected_at"`
}

func qaBestAnswerDelivery(ctx context.Context, raw json.RawMessage, lookup qaAuthorLookup) (*qaDelivery, error) {
	var e qaBestAnswerPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return nil, err
	}
	selectorID := parseID(e.SelectorID)
	recipient := parseID(e.AnswerAuthorID)
	if recipient == uuid.Nil {
		recipient = legacyAnswerAuthor(ctx, lookup, e.AnswerID, "best_selected")
	}
	if !notifiable(recipient, selectorID) {
		return nil, nil
	}
	anonymous := e.SelectorAnonymous != nil && *e.SelectorAnonymous
	if e.SelectorAnonymous == nil {
		// Legacy event: the selector is the question's author; an anonymous
		// question reads back with a masked author. Unknown: do not name.
		q, err := lookup.getQuestion(ctx, e.QuestionID)
		anonymous = err != nil || q == nil || q.IsAnonymous || parseID(q.AuthorID) == uuid.Nil
	}
	return &qaDelivery{
		Recipient: recipient, Actor: namedActor(selectorID, anonymous),
		Type: "qa.answer.best_selected", EntityType: "qa_answer", EntityID: parseID(e.AnswerID),
		DeepLink: fmt.Sprintf("/qa/questions/%s#answer-%s", e.QuestionID, e.AnswerID), At: e.SelectedAt,
	}, nil
}

type qaCommentCreatedPayload struct {
	CommentID      string    `json:"comment_id"`
	AnswerID       string    `json:"answer_id"`
	AuthorID       string    `json:"author_id"`
	AnswerAuthorID string    `json:"answer_author_id"`
	CreatedAt      time.Time `json:"created_at"`
}

func qaCommentCreatedDelivery(ctx context.Context, raw json.RawMessage, lookup qaAuthorLookup) (*qaDelivery, error) {
	var e qaCommentCreatedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return nil, err
	}
	commenterID := parseID(e.AuthorID)
	recipient := parseID(e.AnswerAuthorID)
	if e.AnswerAuthorID == "" {
		recipient = legacyAnswerAuthor(ctx, lookup, e.AnswerID, "comment.created")
	}
	if !notifiable(recipient, commenterID) {
		return nil, nil
	}
	return &qaDelivery{
		Recipient: recipient, Actor: commenterID,
		Type: "qa.answer.comment.created", EntityType: "qa_answer", EntityID: parseID(e.AnswerID),
		DeepLink: fmt.Sprintf("/qa/answers/%s", e.AnswerID), At: e.CreatedAt,
	}, nil
}

type qaAnswerRequestedPayload struct {
	RequestID       string    `json:"request_id"`
	QuestionID      string    `json:"question_id"`
	RequesterID     string    `json:"requester_id"`
	RequestedUserID string    `json:"requested_user_id"`
	RequestedAt     time.Time `json:"requested_at"`
}

func qaAnswerRequestedDelivery(raw json.RawMessage) (*qaDelivery, error) {
	var e qaAnswerRequestedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return nil, err
	}
	requestedID, requesterID := parseID(e.RequestedUserID), parseID(e.RequesterID)
	if !notifiable(requestedID, requesterID) {
		return nil, nil
	}
	return &qaDelivery{
		Recipient: requestedID, Actor: requesterID,
		Type: "qa.answer.requested", EntityType: "qa_question", EntityID: parseID(e.QuestionID),
		DeepLink: fmt.Sprintf("/qa/questions/%s", e.QuestionID), At: e.RequestedAt,
	}, nil
}

type qaVotePayload struct {
	TargetID       string    `json:"target_id"`
	VoterID        string    `json:"voter_id"`
	VoteType       string    `json:"vote_type"`
	TargetAuthorID string    `json:"target_author_id"`
	VotedAt        time.Time `json:"voted_at"`
}

func qaVoteDelivery(ctx context.Context, raw json.RawMessage, lookup qaAuthorLookup, onAnswer bool) (*qaDelivery, error) {
	var e qaVotePayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return nil, err
	}
	if e.VoteType != "up" {
		return nil, nil
	}
	voterID := parseID(e.VoterID)
	recipient := parseID(e.TargetAuthorID)
	if e.TargetAuthorID == "" {
		if onAnswer {
			recipient = legacyAnswerAuthor(ctx, lookup, e.TargetID, "answer.voted")
		} else {
			recipient = legacyQuestionAuthor(ctx, lookup, e.TargetID, "question.voted")
		}
	}
	if !notifiable(recipient, voterID) {
		return nil, nil
	}
	d := &qaDelivery{
		Recipient: recipient, Actor: voterID,
		Type: "qa.question.voted", EntityType: "qa_question", EntityID: parseID(e.TargetID),
		DeepLink: fmt.Sprintf("/qa/questions/%s", e.TargetID), At: e.VotedAt,
	}
	if onAnswer {
		d.Type, d.EntityType, d.DeepLink = "qa.answer.voted", "qa_answer", fmt.Sprintf("/qa/answers/%s", e.TargetID)
	}
	return d, nil
}

type qaQuestionPinnedPayload struct {
	QuestionID       string    `json:"question_id"`
	CommunityID      string    `json:"community_id"`
	ActorID          string    `json:"actor_id"`
	Pinned           bool      `json:"pinned"`
	QuestionAuthorID string    `json:"question_author_id"`
	At               time.Time `json:"at"`
}

func qaQuestionPinnedDelivery(ctx context.Context, raw json.RawMessage, lookup qaAuthorLookup) (*qaDelivery, error) {
	var e qaQuestionPinnedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return nil, err
	}
	// Only notify on pin events, not unpin.
	if !e.Pinned {
		return nil, nil
	}
	actorID := parseID(e.ActorID)
	recipient := parseID(e.QuestionAuthorID)
	if e.QuestionAuthorID == "" {
		recipient = legacyQuestionAuthor(ctx, lookup, e.QuestionID, "question.pinned")
	}
	if !notifiable(recipient, actorID) {
		return nil, nil
	}
	return &qaDelivery{
		Recipient: recipient, Actor: actorID,
		Type: "qa.question.pinned", EntityType: "qa_question", EntityID: parseID(e.QuestionID),
		DeepLink: fmt.Sprintf("/qa/questions/%s", e.QuestionID), At: e.At,
	}, nil
}
