package store

import (
	"context"
	"time"

	"github.com/atpost/qa-service/internal/qaevent"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Domain events written in the write's own transaction (audit L2) ─────────
//
// The store enqueues the events of the writes it owns end to end (question
// and answer creation and edits, comment creation) into qa.outbox_events in
// the same transaction, so the event exists if and only if the write
// committed (qaevent). The service no longer publishes these.

// questionFansOut reports whether a new question may be announced under its
// author: not anonymous, already public (not waiting for approval), and not
// in a private or invite-only community. feed-service fans question.created
// out to the author's followers by author_id, so an anonymous or private
// question would otherwise land in their home feeds under the author's name.
func questionFansOut(isAnonymous bool, status, communityType string) bool {
	if isAnonymous || status != "open" {
		return false
	}
	return communityType != "private" && communityType != "invite"
}

func enqueueQuestionCreated(ctx context.Context, tx pgx.Tx, id, authorID uuid.UUID, communityID *uuid.UUID, title, status, communityType string, isAnonymous bool) error {
	p := qaevent.QuestionCreatedPayload{
		QuestionID:  id.String(),
		Title:       title,
		IsAnonymous: isAnonymous,
		Status:      status,
		CreatedAt:   time.Now().UTC(),
	}
	if communityID != nil {
		p.CommunityID = communityID.String()
	}
	actor := qaevent.ActorFor(authorID, !questionFansOut(isAnonymous, status, communityType))
	if actor != nil {
		p.AuthorID = authorID.String()
	}
	return qaevent.Enqueue(ctx, tx, events.EventQAQuestionCreated, qaevent.QuestionKey(id), actor, p)
}

func enqueueQuestionUpdated(ctx context.Context, tx pgx.Tx, id, authorID, revisionID uuid.UUID, isAnonymous bool) error {
	p := qaevent.QuestionUpdatedPayload{
		QuestionID:  id.String(),
		IsAnonymous: isAnonymous,
		RevisionID:  qaevent.IDString(revisionID),
		UpdatedAt:   time.Now().UTC(),
	}
	actor := qaevent.ActorFor(authorID, isAnonymous)
	if actor != nil {
		p.AuthorID = authorID.String()
	}
	return qaevent.Enqueue(ctx, tx, events.EventQAQuestionUpdated, qaevent.QuestionKey(id), actor, p)
}

func enqueueAnswerCreated(ctx context.Context, tx pgx.Tx, a *Answer, questionAuthorID uuid.UUID) error {
	return qaevent.Enqueue(ctx, tx, events.EventQAAnswerCreated, qaevent.QuestionKey(a.QuestionID),
		qaevent.ActorFor(a.AuthorID, a.IsAnonymous), qaevent.AnswerCreatedPayload{
			AnswerID:         a.ID.String(),
			QuestionID:       a.QuestionID.String(),
			AuthorID:         a.AuthorID.String(),
			IsAnonymous:      a.IsAnonymous,
			QuestionAuthorID: qaevent.IDString(questionAuthorID),
			CreatedAt:        a.CreatedAt,
		})
}

func enqueueAnswerUpdated(ctx context.Context, tx pgx.Tx, answerID, questionID, authorID, revisionID uuid.UUID, isAnonymous bool) error {
	p := qaevent.AnswerUpdatedPayload{
		AnswerID:    answerID.String(),
		QuestionID:  qaevent.IDString(questionID),
		IsAnonymous: isAnonymous,
		RevisionID:  qaevent.IDString(revisionID),
		UpdatedAt:   time.Now().UTC(),
	}
	actor := qaevent.ActorFor(authorID, isAnonymous)
	if actor != nil {
		p.AuthorID = authorID.String()
	}
	return qaevent.Enqueue(ctx, tx, events.EventQAAnswerUpdated, qaevent.QuestionKey(questionID), actor, p)
}

func enqueueCommentCreated(ctx context.Context, tx pgx.Tx, c *AnswerComment, questionID, answerAuthorID uuid.UUID) error {
	return qaevent.Enqueue(ctx, tx, events.EventQAAnswerCommentCreated, qaevent.QuestionKey(questionID),
		&c.AuthorID, qaevent.CommentCreatedPayload{
			CommentID:      c.ID.String(),
			AnswerID:       c.AnswerID.String(),
			QuestionID:     qaevent.IDString(questionID),
			AuthorID:       c.AuthorID.String(),
			AnswerAuthorID: qaevent.IDString(answerAuthorID),
			CreatedAt:      c.CreatedAt,
		})
}
