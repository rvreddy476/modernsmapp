// Package qaevent is the Q&A event contract and its transactional outbox
// (Q&A audit L2, H4 and M10; 30 Oct 2026).
//
// Every Q&A domain event is written to qa.outbox_events (database migration
// 001) and relayed to the qa-events topic by shared/outbox.Publisher
// (cmd/server). Events whose state change happens in one store transaction
// (question created/updated, answer created/updated, comment created) are
// enqueued in that same transaction: the event exists if and only if the
// write committed. The rest go through events.Producer, which inserts the
// row right after the service's write (durable once inserted; no more
// fire-and-forget goroutine that a shutdown or broker hiccup drops).
//
// The row payload is the whole shared envelope, so the Kafka value is the
// shape the direct producer always wrote and consumers decode it unchanged.
//
// Payloads now carry what consumers used to fetch from qa-service's public
// API (the recipient's user id) and whether the acting user posted
// anonymously, so notification-service no longer looks authors up and never
// names an anonymous actor (H4, M10).
//
// This package is a leaf: store and events both import it.
package qaevent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/outbox"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxSchema is the schema of qa-service's outbox table. qa-service shares
// the app database, whose public.outbox_events belongs to another
// declaration (the food/dating reason for a schema-local table).
const OutboxSchema = "qa"

// OutboxTable is the table as shared/outbox names it.
const OutboxTable = OutboxSchema + ".outbox_events"

var queue = outbox.NewQueuer(OutboxSchema)

// Encode wraps payload in the shared envelope. actor nil leaves the
// envelope's actor empty (anonymous content never names its author there).
func Encode(ctx context.Context, eventType string, actor *uuid.UUID, payload any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s payload: %w", eventType, err)
	}
	var actorStr *string
	if actor != nil && *actor != uuid.Nil {
		s := actor.String()
		actorStr = &s
	}
	env, err := json.Marshal(events.NewEnvelope(ctx, eventType, actorStr, body))
	if err != nil {
		return nil, fmt.Errorf("marshal %s envelope: %w", eventType, err)
	}
	return env, nil
}

// Enqueue writes one event inside tx.
func Enqueue(ctx context.Context, tx pgx.Tx, eventType, partitionKey string, actor *uuid.UUID, payload any) error {
	env, err := Encode(ctx, eventType, actor, payload)
	if err != nil {
		return err
	}
	if err := queue.Enqueue(ctx, tx, eventType, partitionKey, env); err != nil {
		return fmt.Errorf("enqueue %s: %w", eventType, err)
	}
	return nil
}

// EnqueuePool writes one event outside a transaction (after the write it
// reports has committed).
func EnqueuePool(ctx context.Context, db *pgxpool.Pool, eventType, partitionKey string, actor *uuid.UUID, payload any) error {
	env, err := Encode(ctx, eventType, actor, payload)
	if err != nil {
		return err
	}
	if err := queue.EnqueuePool(ctx, db, eventType, partitionKey, env); err != nil {
		return fmt.Errorf("enqueue %s: %w", eventType, err)
	}
	return nil
}

// QuestionKey keys every event about one question (and its answers and
// comments) to one partition, so consumers see them in order.
func QuestionKey(questionID uuid.UUID) string { return "question:" + questionID.String() }

// UserKey keys events about a user (reputation, requests).
func UserKey(userID uuid.UUID) string { return "user:" + userID.String() }

// ActorFor is the envelope actor for content by authorID: none when the
// content is anonymous.
func ActorFor(authorID uuid.UUID, anonymous bool) *uuid.UUID {
	if anonymous || authorID == uuid.Nil {
		return nil
	}
	return &authorID
}

// IDString is id as text, or "" for uuid.Nil (omitted by omitempty).
func IDString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// --- Payloads ---
//
// Fields added on 30 Oct 2026 are omitempty and additive: consumers that
// predate them ignore them.

// QuestionCreatedPayload is qa.question.created. AuthorID is empty when the
// question must not be fanned out under its author: it is anonymous, waits
// for approval, or lives in a private or invite-only community.
// feed-service fans a question out to the author's followers by this id and
// skips an event without one.
type QuestionCreatedPayload struct {
	QuestionID  string    `json:"question_id"`
	AuthorID    string    `json:"author_id,omitempty"`
	Title       string    `json:"title"`
	IsAnonymous bool      `json:"is_anonymous,omitempty"`
	CommunityID string    `json:"community_id,omitempty"`
	Status      string    `json:"status,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// QuestionUpdatedPayload is qa.question.updated. AuthorID is empty for an
// anonymous question.
type QuestionUpdatedPayload struct {
	QuestionID  string    `json:"question_id"`
	AuthorID    string    `json:"author_id,omitempty"`
	IsAnonymous bool      `json:"is_anonymous,omitempty"`
	RevisionID  string    `json:"revision_id,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
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

// AnswerCreatedPayload is qa.answer.created. QuestionAuthorID is the
// recipient of "answered your question"; IsAnonymous means the answerer must
// never be named (AuthorID is kept so the consumer can skip self-answers).
type AnswerCreatedPayload struct {
	AnswerID         string    `json:"answer_id"`
	QuestionID       string    `json:"question_id"`
	AuthorID         string    `json:"author_id"`
	IsAnonymous      bool      `json:"is_anonymous"`
	QuestionAuthorID string    `json:"question_author_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type AnswerUpdatedPayload struct {
	AnswerID    string    `json:"answer_id"`
	QuestionID  string    `json:"question_id,omitempty"`
	AuthorID    string    `json:"author_id,omitempty"`
	IsAnonymous bool      `json:"is_anonymous,omitempty"`
	RevisionID  string    `json:"revision_id,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type AnswerDeletedPayload struct {
	AnswerID  string    `json:"answer_id"`
	ActorID   string    `json:"actor_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

// BestAnswerSelectedPayload is qa.answer.best_selected. SelectorAnonymous is
// set when the selector (the question's author) asked anonymously.
type BestAnswerSelectedPayload struct {
	QuestionID        string    `json:"question_id"`
	AnswerID          string    `json:"answer_id"`
	SelectorID        string    `json:"selector_id"`
	SelectorAnonymous bool      `json:"selector_anonymous,omitempty"`
	AnswerAuthorID    string    `json:"answer_author_id"`
	SelectedAt        time.Time `json:"selected_at"`
}

// CommentCreatedPayload is qa.answer.comment.created. AnswerAuthorID is the
// recipient of "commented on your answer" (the answer may be anonymous; the
// recipient is never shown to anyone else).
type CommentCreatedPayload struct {
	CommentID      string    `json:"comment_id"`
	AnswerID       string    `json:"answer_id"`
	QuestionID     string    `json:"question_id,omitempty"`
	AuthorID       string    `json:"author_id"`
	AnswerAuthorID string    `json:"answer_author_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// VotePayload is qa.question.voted / qa.answer.voted. TargetAuthorID is the
// recipient of "upvoted your ...".
type VotePayload struct {
	TargetID       string    `json:"target_id"`
	VoterID        string    `json:"voter_id"`
	VoteType       string    `json:"vote_type"`
	TargetAuthorID string    `json:"target_author_id,omitempty"`
	VotedAt        time.Time `json:"voted_at"`
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

// QuestionPinnedPayload is qa.question.pinned. QuestionAuthorID is the
// recipient of "your question was pinned".
type QuestionPinnedPayload struct {
	QuestionID       string    `json:"question_id"`
	CommunityID      string    `json:"community_id"`
	ActorID          string    `json:"actor_id"`
	Pinned           bool      `json:"pinned"`
	QuestionAuthorID string    `json:"question_author_id,omitempty"`
	At               time.Time `json:"at"`
}

type ReportedPayload struct {
	ReportID   string    `json:"report_id"`
	TargetID   string    `json:"target_id"`
	ReporterID string    `json:"reporter_id"`
	Reason     string    `json:"reason"`
	ReportedAt time.Time `json:"reported_at"`
}
