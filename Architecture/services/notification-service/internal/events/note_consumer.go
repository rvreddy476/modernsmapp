package events

// Echo note notifications (2026-11-12, docs/designs/echo-contract.md):
// note.replied and note.quoted from post-service's outbox, relayed on
// social.events.v1. planNoteNotice is pure; the block check runs against
// graph-service; delivery is the service's CreateNoteNotification
// (service/note_notify.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// noteNotifier is the service seam; *service.Service satisfies it.
type noteNotifier interface {
	CreateNoteNotification(ctx context.Context, n service.NoteNotification) error
}

// noteBlockChecker answers whether a block stands between two people in
// either direction; *graph.Client satisfies it (EitherBlocked).
type noteBlockChecker interface {
	EitherBlocked(ctx context.Context, userID, otherID uuid.UUID) (bool, error)
}

// planNoteNotice turns one note event into the notice it delivers. ok is
// false for a payload that names no deliverable recipient: an id that does
// not parse, or a notice to oneself (post-service already emits nothing for
// a self-reply or self-quote; this is defence in depth).
func planNoteNotice(eventType, dedupID string, raw json.RawMessage, now time.Time) (service.NoteNotification, bool, error) {
	var n service.NoteNotification
	var noteID, parentID, actorID, recipientID string
	switch eventType {
	case events.EventNoteReplied:
		var p events.NoteRepliedPayload
		if err := unmarshalPayload(raw, &p); err != nil {
			return n, false, err
		}
		n = service.NoteNotification{EventID: dedupID, Type: service.NoteReplyType, CreatedAt: orNow(p.CreatedAt, now)}
		noteID, parentID, actorID, recipientID = p.NoteID, p.ReplyToID, p.ActorID, p.RecipientID
	case events.EventNoteQuoted:
		var p events.NoteQuotedPayload
		if err := unmarshalPayload(raw, &p); err != nil {
			return n, false, err
		}
		n = service.NoteNotification{EventID: dedupID, Type: service.NoteQuoteType, CreatedAt: orNow(p.CreatedAt, now)}
		noteID, parentID, actorID, recipientID = p.NoteID, p.QuoteOfID, p.ActorID, p.RecipientID
	default:
		return n, false, nil
	}
	var err error
	if n.RecipientID, err = uuid.Parse(recipientID); err != nil || n.RecipientID == uuid.Nil {
		return n, false, nil
	}
	if n.ActorID, err = uuid.Parse(actorID); err != nil || n.ActorID == uuid.Nil {
		return n, false, nil
	}
	if n.NoteID, err = uuid.Parse(noteID); err != nil || n.NoteID == uuid.Nil {
		return n, false, nil
	}
	// The parent only keys the device collapse; a missing one still notifies.
	n.ParentID, _ = uuid.Parse(parentID)
	if n.RecipientID == n.ActorID {
		return n, false, nil
	}
	return n, true, nil
}

// handleNoteEvent delivers one note.replied / note.quoted notice.
//
// Blocks fail CLOSED: with no graph client, or when graph-service cannot be
// asked, the notice is dropped and logged. A missed reply notification costs
// little; notifying someone across a block does not. The event rides the
// worker pool (committed when queued), so an error here would not be
// retried anyway.
func (c *Consumer) handleNoteEvent(ctx context.Context, env events.EventEnvelope, value []byte) error {
	n, ok, err := planNoteNotice(env.EventType, followRequestDedupID(env, value), env.Payload, time.Now())
	if err != nil || !ok {
		return err
	}
	if c.noteNotify == nil {
		return nil
	}
	if c.noteBlocks == nil {
		slog.Warn("note notification dropped: no block checker", "type", n.Type, "note_id", n.NoteID)
		return nil
	}
	blocked, err := c.noteBlocks.EitherBlocked(ctx, n.RecipientID, n.ActorID)
	if err != nil {
		slog.Warn("note notification dropped: block check failed", "type", n.Type, "note_id", n.NoteID, "error", err)
		return nil
	}
	if blocked {
		return nil
	}
	return c.noteNotify.CreateNoteNotification(ctx, n)
}

// inboxEntityDeleter removes every row in one user's inbox that points at an
// entity; *service.Service satisfies it (DeleteNotificationsForEntity).
type inboxEntityDeleter interface {
	DeleteNotificationsForEntity(ctx context.Context, userID, entityID uuid.UUID) (int, error)
}

// noteNoticeHolder is whose inbox holds the note_reply / note_quote notice
// a deleted note produced: the author of the note it replied to or quoted,
// which post-service names on PostDeleted (target_author_id). ok is false
// for anything that is not a reply or quote note, a payload without a
// parseable target author (an older producer), or a target that is the
// deleter themself (no notice was sent; the author's inbox is cleared
// anyway).
func noteNoticeHolder(e events.PostDeletedPayload, authorID uuid.UUID) (uuid.UUID, bool) {
	if e.ContentType != "note" || (e.ReplyToID == "" && e.QuoteOfID == "") {
		return uuid.Nil, false
	}
	holder, err := uuid.Parse(e.TargetAuthorID)
	if err != nil || holder == uuid.Nil || holder == authorID {
		return uuid.Nil, false
	}
	return holder, true
}

// handlePostDeleted takes a soft-deleted post's notifications back: the rows
// in the author's own inbox (likes, comments, mentions, reposts on the
// post) and, for an Echo reply or quote, the note_reply / note_quote row in
// the target author's inbox (its entity is the deleted note). Both
// deletes are idempotent, so an error is returned and the event retried. A
// restore (PostRestored) re-creates neither.
func (c *Consumer) handlePostDeleted(ctx context.Context, e events.PostDeletedPayload) error {
	authorID, err := uuid.Parse(e.AuthorID)
	if err != nil {
		return nil
	}
	postID, err := uuid.Parse(e.PostID)
	if err != nil {
		return nil
	}
	if c.inboxDeleter == nil {
		return nil
	}
	n, err := c.inboxDeleter.DeleteNotificationsForEntity(ctx, authorID, postID)
	if err != nil {
		return fmt.Errorf("PostDeleted: remove author inbox rows for %s: %w", postID, err)
	}
	slog.Info("post deleted: author inbox rows removed", "post_id", postID, "author_id", authorID, "rows", n)
	holder, ok := noteNoticeHolder(e, authorID)
	if !ok {
		return nil
	}
	n, err = c.inboxDeleter.DeleteNotificationsForEntity(ctx, holder, postID)
	if err != nil {
		return fmt.Errorf("PostDeleted: remove note notice for %s from %s: %w", postID, holder, err)
	}
	slog.Info("note deleted: target author's notice removed", "note_id", postID, "recipient_id", holder, "rows", n)
	return nil
}
