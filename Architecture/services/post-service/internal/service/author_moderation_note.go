package service

import (
	"context"
	"log/slog"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// moderationNoteReader is the one read behind the author's takedown reason.
// *postgres.Store implements it.
type moderationNoteReader interface {
	LatestModerationDecision(ctx context.Context, postID uuid.UUID) (*postgres.AuthorModerationNote, error)
}

func (s *Service) moderationNotes() moderationNoteReader {
	if s.moderationNotesSeam != nil {
		return s.moderationNotesSeam
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// attachModerationNote tells the AUTHOR of a rejected post why (2026-11-18):
// moderation_reason and moderation_appealable on GET /v1/posts/:id, set only
// when
//   - the viewer is the post's author (never another viewer: everyone else
//     gets the post's 404 before this runs, and this check holds even if a
//     later change let a rejected post through), and
//   - the post is rejected now, and its latest base moderation decision is
//     the rejecting one (an admin decision, an upheld report, or a denied
//     appeal). A rejection by the video gate or the ML pre-filter has no
//     moderator-written reason and gets no fields.
//
// Appealable: a takedown by a moderator or a report may be appealed; one
// that is itself an appeal's outcome may not. A failed read leaves the
// fields absent (logged); the post still opens.
func (s *Service) attachModerationNote(ctx context.Context, d *PostDetail, viewerID *uuid.UUID) {
	if d == nil || d.Post == nil || viewerID == nil || *viewerID != d.AuthorID {
		return
	}
	if d.ReviewStatus != "rejected" {
		return
	}
	store := s.moderationNotes()
	if store == nil {
		return
	}
	note, err := store.LatestModerationDecision(ctx, d.ID)
	if err != nil {
		slog.WarnContext(ctx, "author moderation note unavailable", "post_id", d.ID, "err", err)
		return
	}
	if note == nil || note.ResultingStatus != "rejected" || note.Reason == "" {
		return
	}
	reason := note.Reason
	appealable := note.Source != "appeal"
	d.ModerationReason = &reason
	d.ModerationAppealable = &appealable
}
