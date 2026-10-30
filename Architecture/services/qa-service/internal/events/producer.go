package events

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/qa-service/internal/qaevent"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Producer writes the Q&A events that are not enqueued inside a store
// transaction to the outbox (qaevent, audit L2). It used to write to Kafka
// from a detached goroutine with a 5 s timeout, so a shutdown or a broker
// hiccup lost the event; now each call inserts a qa.outbox_events row and
// shared/outbox.Publisher relays it.
//
// Question/answer creation and edits and comment creation are enqueued by
// the store in the same transaction as the write (store/outbox.go) and have
// no method here, so nothing can publish them twice.
//
// The call happens after the service's write committed, so a crash in
// between still loses the event; the paths that matter for user-visible
// state (above) are transactional.
type Producer struct {
	enqueue enqueueFunc
	authors AuthorLookup
}

type enqueueFunc func(ctx context.Context, eventType, partitionKey string, actor *uuid.UUID, payload any) error

// AuthorLookup resolves the recipients the payloads carry (H4/M10:
// notification-service used to look them up through the public API, which
// masks anonymous authors and refuses private-community questions).
type AuthorLookup interface {
	// QuestionAuthor is the question's author and whether it is anonymous.
	QuestionAuthor(ctx context.Context, questionID uuid.UUID) (uuid.UUID, bool, error)
	// AnswerAuthor is the answer's author and whether it is anonymous.
	AnswerAuthor(ctx context.Context, answerID uuid.UUID) (uuid.UUID, bool, error)
}

// NewProducer returns a producer that writes to qa.outbox_events on db.
func NewProducer(db *pgxpool.Pool) *Producer {
	return &Producer{
		enqueue: func(ctx context.Context, eventType, key string, actor *uuid.UUID, payload any) error {
			return qaevent.EnqueuePool(ctx, db, eventType, key, actor, payload)
		},
		authors: pgAuthors{db: db},
	}
}

// pgAuthors reads authors straight from the Q&A tables (any status: a
// recipient is still the author of a closed question).
type pgAuthors struct{ db *pgxpool.Pool }

func (p pgAuthors) QuestionAuthor(ctx context.Context, id uuid.UUID) (uuid.UUID, bool, error) {
	var author uuid.UUID
	var anon bool
	err := p.db.QueryRow(ctx, `SELECT author_id, COALESCE(is_anonymous, false) FROM questions WHERE id = $1`, id).Scan(&author, &anon)
	return author, anon, err
}

func (p pgAuthors) AnswerAuthor(ctx context.Context, id uuid.UUID) (uuid.UUID, bool, error) {
	var author uuid.UUID
	var anon bool
	err := p.db.QueryRow(ctx, `SELECT author_id, COALESCE(is_anonymous, false) FROM answers WHERE id = $1`, id).Scan(&author, &anon)
	return author, anon, err
}

func (p *Producer) PublishQuestionDeleted(ctx context.Context, questionID, actorID uuid.UUID) error {
	return p.publish(ctx, events.EventQAQuestionDeleted, qaevent.QuestionKey(questionID), &actorID, QuestionDeletedPayload{
		QuestionID: questionID.String(), ActorID: actorID.String(), DeletedAt: time.Now(),
	})
}

func (p *Producer) PublishQuestionClosed(ctx context.Context, questionID, closedBy uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventQAQuestionClosed, qaevent.QuestionKey(questionID), &closedBy, QuestionClosedPayload{
		QuestionID: questionID.String(), ClosedBy: closedBy.String(), Reason: reason, ClosedAt: time.Now(),
	})
}

func (p *Producer) PublishAnswerDeleted(ctx context.Context, answerID, actorID uuid.UUID) error {
	return p.publish(ctx, events.EventQAAnswerDeleted, "answer:"+answerID.String(), &actorID, AnswerDeletedPayload{
		AnswerID: answerID.String(), ActorID: actorID.String(), DeletedAt: time.Now(),
	})
}

// PublishBestAnswerSelected also records whether the selector (the question's
// author) asked anonymously, so the answerer's notification does not name
// them (H4).
func (p *Producer) PublishBestAnswerSelected(ctx context.Context, questionID, answerID, selectorID, answerAuthorID uuid.UUID) error {
	_, selectorAnon, err := p.questionAuthor(ctx, questionID)
	if err != nil {
		// Unknown: treat as anonymous rather than risk naming them.
		selectorAnon = true
	}
	return p.publish(ctx, events.EventQABestAnswerSelected, qaevent.QuestionKey(questionID), qaevent.ActorFor(selectorID, selectorAnon), BestAnswerSelectedPayload{
		QuestionID: questionID.String(), AnswerID: answerID.String(),
		SelectorID: selectorID.String(), SelectorAnonymous: selectorAnon,
		AnswerAuthorID: qaevent.IDString(answerAuthorID), SelectedAt: time.Now(),
	})
}

// PublishQuestionVoted carries the question's author as the recipient.
func (p *Producer) PublishQuestionVoted(ctx context.Context, questionID, voterID uuid.UUID, voteType string) error {
	author, _, _ := p.questionAuthor(ctx, questionID)
	return p.publish(ctx, events.EventQAQuestionVoted, qaevent.QuestionKey(questionID), &voterID, VotePayload{
		TargetID: questionID.String(), VoterID: voterID.String(), VoteType: voteType,
		TargetAuthorID: qaevent.IDString(author), VotedAt: time.Now(),
	})
}

// PublishAnswerVoted carries the answer's author as the recipient.
func (p *Producer) PublishAnswerVoted(ctx context.Context, answerID, voterID uuid.UUID, voteType string) error {
	author, _, _ := p.answerAuthor(ctx, answerID)
	return p.publish(ctx, events.EventQAAnswerVoted, "answer:"+answerID.String(), &voterID, VotePayload{
		TargetID: answerID.String(), VoterID: voterID.String(), VoteType: voteType,
		TargetAuthorID: qaevent.IDString(author), VotedAt: time.Now(),
	})
}

func (p *Producer) PublishAnswerRequested(ctx context.Context, requestID, questionID, requesterID, requestedUserID uuid.UUID) error {
	return p.publish(ctx, events.EventQAAnswerRequested, qaevent.QuestionKey(questionID), &requesterID, AnswerRequestedPayload{
		RequestID: requestID.String(), QuestionID: questionID.String(),
		RequesterID: requesterID.String(), RequestedUserID: requestedUserID.String(), RequestedAt: time.Now(),
	})
}

func (p *Producer) PublishReputationChanged(ctx context.Context, userID uuid.UUID, eventType string, points, newScore int) error {
	return p.publish(ctx, events.EventQAReputationChanged, qaevent.UserKey(userID), &userID, ReputationChangedPayload{
		UserID: userID.String(), EventType: eventType, Points: points, NewScore: newScore, ChangedAt: time.Now(),
	})
}

func (p *Producer) PublishModerationAction(ctx context.Context, actionID, actorID uuid.UUID, targetType string, targetID uuid.UUID, actionType string) error {
	return p.publish(ctx, events.EventQAModerationAction, targetType+":"+targetID.String(), &actorID, ModerationActionPayload{
		ActionID: actionID.String(), ActorID: actorID.String(),
		TargetType: targetType, TargetID: targetID.String(), ActionType: actionType, ActedAt: time.Now(),
	})
}

// PublishQuestionPinned carries the question's author as the recipient.
func (p *Producer) PublishQuestionPinned(ctx context.Context, questionID, communityID, actorID uuid.UUID, pinned bool) error {
	author, _, _ := p.questionAuthor(ctx, questionID)
	return p.publish(ctx, events.EventQAQuestionPinned, qaevent.QuestionKey(questionID), &actorID, QuestionPinnedPayload{
		QuestionID:       questionID.String(),
		CommunityID:      communityID.String(),
		ActorID:          actorID.String(),
		Pinned:           pinned,
		QuestionAuthorID: qaevent.IDString(author),
		At:               time.Now(),
	})
}

func (p *Producer) PublishQuestionReported(ctx context.Context, reportID, questionID, reporterID uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventQAQuestionReported, qaevent.QuestionKey(questionID), &reporterID, ReportedPayload{
		ReportID: reportID.String(), TargetID: questionID.String(),
		ReporterID: reporterID.String(), Reason: reason, ReportedAt: time.Now(),
	})
}

func (p *Producer) PublishAnswerReported(ctx context.Context, reportID, answerID, reporterID uuid.UUID, reason string) error {
	return p.publish(ctx, events.EventQAAnswerReported, "answer:"+answerID.String(), &reporterID, ReportedPayload{
		ReportID: reportID.String(), TargetID: answerID.String(),
		ReporterID: reporterID.String(), Reason: reason, ReportedAt: time.Now(),
	})
}

func (p *Producer) questionAuthor(ctx context.Context, id uuid.UUID) (uuid.UUID, bool, error) {
	if p.authors == nil {
		return uuid.Nil, false, errNoLookup
	}
	author, anon, err := p.authors.QuestionAuthor(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("qa-events: question author lookup failed", "question_id", id, "error", err)
	}
	return author, anon, err
}

func (p *Producer) answerAuthor(ctx context.Context, id uuid.UUID) (uuid.UUID, bool, error) {
	if p.authors == nil {
		return uuid.Nil, false, errNoLookup
	}
	author, anon, err := p.authors.AnswerAuthor(ctx, id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("qa-events: answer author lookup failed", "answer_id", id, "error", err)
	}
	return author, anon, err
}

var errNoLookup = errors.New("qa-events: no author lookup")

// publish inserts the event into the outbox. Callers ignore the error (an
// event never fails the request it reports), so it is logged here.
func (p *Producer) publish(ctx context.Context, eventType, partitionKey string, actor *uuid.UUID, payload any) error {
	if p == nil || p.enqueue == nil {
		return nil
	}
	// The request may be cancelled the moment it answers; the insert must
	// still happen.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := p.enqueue(ctx, eventType, partitionKey, actor, payload); err != nil {
		slog.Warn("qa-events: outbox insert failed", "event_type", eventType, "error", err)
		return err
	}
	return nil
}

// Close is kept for main's shutdown hook; the outbox needs no flush.
func (p *Producer) Close() error { return nil }

// --- Payload types (the contract lives in qaevent) ---

type (
	QuestionCreatedPayload    = qaevent.QuestionCreatedPayload
	QuestionUpdatedPayload    = qaevent.QuestionUpdatedPayload
	QuestionDeletedPayload    = qaevent.QuestionDeletedPayload
	QuestionClosedPayload     = qaevent.QuestionClosedPayload
	AnswerCreatedPayload      = qaevent.AnswerCreatedPayload
	AnswerUpdatedPayload      = qaevent.AnswerUpdatedPayload
	AnswerDeletedPayload      = qaevent.AnswerDeletedPayload
	BestAnswerSelectedPayload = qaevent.BestAnswerSelectedPayload
	CommentCreatedPayload     = qaevent.CommentCreatedPayload
	VotePayload               = qaevent.VotePayload
	AnswerRequestedPayload    = qaevent.AnswerRequestedPayload
	ReputationChangedPayload  = qaevent.ReputationChangedPayload
	ModerationActionPayload   = qaevent.ModerationActionPayload
	QuestionPinnedPayload     = qaevent.QuestionPinnedPayload
	ReportedPayload           = qaevent.ReportedPayload
)
