package service

// Admin console, Wave 2 — Content. Thin reads for the admin-service token
// family (internal/http/admin_token.go). Writes reuse the existing audited
// paths: ModeratePost, AutoResolveFlagged, PromoteStaged, ReviewReport and
// SetCommentModerationStatus.

import (
	"context"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// PostContentKind returns a post's admin kind (post, reel or video),
// soft-deleted rows included; postgres.ErrPostNotFound when absent.
func (s *Service) PostContentKind(ctx context.Context, postID uuid.UUID) (string, error) {
	ct, err := s.pgStore.PostContentType(ctx, postID)
	if err != nil {
		return "", err
	}
	return postgres.ContentKindOf(ct), nil
}

// ListReviewQueue is the flagged or staged queue for one kind.
func (s *Service) ListReviewQueue(ctx context.Context, queue, kind string, before time.Time, limit int) ([]postgres.ReviewQueueItem, error) {
	return s.pgStore.ListReviewQueue(ctx, queue, kind, before, limit)
}

// PostModerationHistory is everything recorded about a post's moderation:
// canonical decisions and review_status / visibility changes.
type PostModerationHistory struct {
	PostID       uuid.UUID                     `json:"post_id"`
	Kind         string                        `json:"kind"`
	Decisions    []postgres.ModerationDecision `json:"decisions"`
	ReviewAudit  []postgres.ReviewAuditEntry   `json:"review_audit"`
	ReviewStatus string                        `json:"review_status"`
	Deleted      bool                          `json:"deleted"`
}

// GetPostModerationHistory reads a post's moderation history.
func (s *Service) GetPostModerationHistory(ctx context.Context, postID uuid.UUID, kind string) (*PostModerationHistory, error) {
	subject, err := s.pgStore.GetModerationSubject(ctx, postID)
	if err != nil {
		return nil, err
	}
	decisions, err := s.pgStore.ListModerationDecisions(ctx, postID)
	if err != nil {
		return nil, err
	}
	audit, err := s.pgStore.ListReviewAudit(ctx, postID)
	if err != nil {
		return nil, err
	}
	if decisions == nil {
		decisions = []postgres.ModerationDecision{}
	}
	if audit == nil {
		audit = []postgres.ReviewAuditEntry{}
	}
	return &PostModerationHistory{
		PostID: postID, Kind: kind, Decisions: decisions, ReviewAudit: audit,
		ReviewStatus: subject.ReviewStatus, Deleted: subject.Deleted,
	}, nil
}

// GetContentReport reads one content report.
func (s *Service) GetContentReport(ctx context.Context, reportID uuid.UUID) (*postgres.ContentReport, error) {
	return s.pgStore.GetContentReport(ctx, reportID)
}

// ListReportsOfTypes lists reports limited to targetTypes (nil = all).
func (s *Service) ListReportsOfTypes(ctx context.Context, status string, targetTypes []string, limit, offset int) ([]postgres.ContentReport, error) {
	return s.pgStore.ListContentReports(ctx, status, targetTypes, limit, offset)
}

// ListCommentAudit is a comment's moderation audit, newest first.
func (s *Service) ListCommentAudit(ctx context.Context, commentID uuid.UUID) ([]postgres.AdminAuditEntry, error) {
	return s.pgStore.ListAdminAudit(ctx, "comment", commentID)
}

// SocialAdminStats and TubeAdminStats are the dashboard counts.
func (s *Service) SocialAdminStats(ctx context.Context) (*postgres.SocialAdminStats, error) {
	return s.pgStore.SocialAdminStats(ctx)
}

func (s *Service) TubeAdminStats(ctx context.Context) (*postgres.TubeAdminStats, error) {
	return s.pgStore.TubeAdminStats(ctx)
}

// AdminListVideoSeriesByCreator lists every series of a creator, private ones
// included: a moderator reviewing a creator must see what the public cannot.
func (s *Service) AdminListVideoSeriesByCreator(ctx context.Context, creatorID uuid.UUID, limit, offset int) ([]postgres.VideoSeries, error) {
	return s.ListVideoSeriesByCreator(ctx, creatorID, &creatorID, limit, offset)
}

// AdminGetPost reads the stored post without the viewer privacy gate
// (postgres.ErrPostNotFound when absent or soft-deleted).
func (s *Service) AdminGetPost(ctx context.Context, postID uuid.UUID) (*postgres.Post, error) {
	posts, err := s.pgStore.GetPostsByIDs(ctx, []uuid.UUID{postID})
	if err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return nil, postgres.ErrPostNotFound
	}
	return &posts[0], nil
}
