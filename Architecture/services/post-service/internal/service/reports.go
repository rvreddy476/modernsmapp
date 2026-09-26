package service

import (
	"log/slog"
	"context"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// SubmitReport creates a new content report from a user. When the
// report targets a comment, also bumps the comments.flagged_count
// so the comment-moderation queue surfaces it. The flagged_count
// auto-promotes the comment to 'review' status at the threshold,
// so anyone but the author + a moderator stops seeing it.
func (s *Service) SubmitReport(ctx context.Context, report *postgres.ContentReport) error {
	if err := s.pgStore.InsertContentReport(ctx, report); err != nil {
		return err
	}
	if report.TargetType == "comment" {
		// At the auto-review threshold the comment leaves the visible set —
		// and the count — exactly once.
		if postID, flipped, err := s.pgStore.IncrementCommentFlaggedCount(ctx, report.TargetID); err == nil && flipped {
			if err := s.pgStore.AdjustCommentCount(ctx, postID, -1); err != nil {
				slog.Warn("failed to decrement comment_count on auto-review", "post_id", postID, "error", err)
			}
			s.publishCommentChange(postID, report.TargetID, nil, CommentChangeModerated, report.ReporterID)
		}
	}
	return nil
}

// ListFlaggedComments returns the comment moderation queue.
// status="" returns everything in {hidden, removed, review} plus
// visible-but-flagged. status="flagged"/"review"/"hidden"/"removed"
// scopes to that bucket only.
func (s *Service) ListFlaggedComments(ctx context.Context, status string, cursor time.Time, limit int) ([]postgres.FlaggedComment, error) {
	return s.pgStore.ListFlaggedComments(ctx, status, cursor, limit)
}

// SetCommentModerationStatus is the admin override.
// status ∈ {visible, hidden, removed, review}.
// actor is the acting human; the change is audited in post_admin_audit.
func (s *Service) SetCommentModerationStatus(ctx context.Context, actor, commentID uuid.UUID, status string) error {
	postID, previous, err := s.pgStore.SetCommentModerationStatus(ctx, actor, commentID, status)
	if err != nil {
		return err
	}
	// comment_count counts visible comments only; a transition across that
	// boundary moves it by one, in either direction, and nothing else does.
	wasVisible, isVisible := previous == "visible", status == "visible"
	if wasVisible != isVisible {
		delta := int64(-1)
		if isVisible {
			delta = 1
		}
		if err := s.pgStore.AdjustCommentCount(ctx, postID, delta); err != nil {
			slog.Warn("failed to adjust comment_count on moderation", "post_id", postID, "error", err)
		}
	}
	s.publishCommentChange(postID, commentID, nil, CommentChangeModerated, actor)
	return nil
}

// ListReports returns content reports, optionally filtered by status. Used by admin dashboard.
func (s *Service) ListReports(ctx context.Context, status string, limit, offset int) ([]postgres.ContentReport, error) {
	return s.pgStore.GetContentReports(ctx, status, limit, offset)
}

// ReviewReport updates the status and review note of a report. Used by admin dashboard.
// actor is the acting human; the review is audited in post_admin_audit.
func (s *Service) ReviewReport(ctx context.Context, reportID uuid.UUID, status string, actor uuid.UUID, reviewNote string) error {
	return s.pgStore.UpdateReportStatus(ctx, reportID, status, actor, reviewNote)
}
