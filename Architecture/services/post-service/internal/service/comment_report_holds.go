package service

import (
	"context"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ApplyTrustSafetyCommentReport counts a comment report filed in
// trust-safety-service (its ReportFiled event, consumers/comment_report_hold.go)
// toward the comment auto-review hold (TikTok parity T-13).
//
// The store does the counting, the hold and the comment_count move in one
// transaction, idempotent per (comment, reporter): a redelivered event or
// a second report by the same reporter changes nothing. When this report
// is the one that holds the comment, the thread is told, the same frame
// SubmitReport and the admin override send.
func (s *Service) ApplyTrustSafetyCommentReport(ctx context.Context, reportID, commentID, reporterID uuid.UUID) (postgres.CommentReportOutcome, error) {
	out, err := s.pgStore.ApplyCommentReportHold(ctx, reportID, commentID, reporterID)
	if err != nil {
		return out, err
	}
	if out.Held {
		s.publishCommentChange(out.PostID, commentID, nil, CommentChangeModerated, reporterID)
	}
	return out, nil
}
