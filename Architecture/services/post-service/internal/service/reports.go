package service

import (
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
		// Best-effort: report is already recorded, missing the count
		// bump is recoverable from a moderator dashboard.
		_ = s.pgStore.IncrementCommentFlaggedCount(ctx, report.TargetID)
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
func (s *Service) SetCommentModerationStatus(ctx context.Context, commentID uuid.UUID, status string) error {
	return s.pgStore.SetCommentModerationStatus(ctx, commentID, status)
}

// ListReports returns content reports, optionally filtered by status. Used by admin dashboard.
func (s *Service) ListReports(ctx context.Context, status string, limit, offset int) ([]postgres.ContentReport, error) {
	return s.pgStore.GetContentReports(ctx, status, limit, offset)
}

// ReviewReport updates the status and review note of a report. Used by admin dashboard.
func (s *Service) ReviewReport(ctx context.Context, reportID uuid.UUID, status, reviewerID, reviewNote string) error {
	return s.pgStore.UpdateReportStatus(ctx, reportID, status, reviewerID, reviewNote)
}
