package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// ─── Report takedowns (reels-29) ──────────────────────────────────────────────
//
// A report on a post (reel, video or post) resolved as upheld owes
// post-service one reject decision: POST /v1/posts/internal/moderation with
// a post:moderation.write token and post_moderation capability claims
// {subject_id: the post, content_revision: read from moderation-subject,
// decision "reject", reason: the moderator's notes, decision_id: the report
// id, policy_version "report-v1", actor_id: the moderator}. post-service
// then rejects the post, takes it out of search and tells the author
// (post.author_notice). The obligation is written on the report in the
// resolution's transaction (store/postgres/report_takedown.go); this file
// sends it once at resolution and the sweeper retries it, so a post-service
// outage delays the takedown but never loses it. A dismissed report sends
// nothing. Reports on any other subject are not taken down here.

// Errors the takedown sender acts on (besides ErrPostSubjectNotFound,
// ErrPostDecisionSuperseded and ErrPostDecisionConflict).
var (
	// ErrPostSubjectStale: the post's revision moved between the subject
	// read and the command; read it again and retry.
	ErrPostSubjectStale = errors.New("post-service refused the decision: the post changed since it was read")
	// ErrPostDecisionRefused: post-service refused the command as malformed
	// (400); a retry with the same claims cannot succeed.
	ErrPostDecisionRefused = errors.New("post-service refused the decision as invalid")
)

// reportTakedownSender is the post-service command a takedown sends;
// *HTTPPostModerationClient implements it.
type reportTakedownSender interface {
	RejectReportedPost(ctx context.Context, reportID, postID, moderatorID uuid.UUID, contentRevision int64, reason string) error
}

// reportTakedownStore is the takedown queue on trust.reports;
// *postgres.ReportStore implements it.
type reportTakedownStore interface {
	ClaimReportTakedowns(ctx context.Context, only *uuid.UUID, lease time.Duration, limit int) ([]postgres.ReportTakedown, error)
	RecordReportTakedown(ctx context.Context, reportID uuid.UUID, status, lastError string, retryAfter time.Duration) error
}

const (
	// reportTakedownLease is how long a claimed takedown is held from other
	// senders: well past the client's two 5 s calls.
	reportTakedownLease = 2 * time.Minute
	// reportTakedownBatch is how many due takedowns one sweep claims.
	reportTakedownBatch = 50
	// ReportTakedownSweepInterval is how often the sweeper looks for due
	// takedowns.
	ReportTakedownSweepInterval = 15 * time.Second
	// reportTakedownFirstRetry and reportTakedownMaxRetry bound the backoff.
	reportTakedownFirstRetry = 15 * time.Second
	reportTakedownMaxRetry   = 15 * time.Minute
)

// SetReportTakedownStore overrides the takedown queue (tests); nil means
// the report store.
func (s *Service) SetReportTakedownStore(st reportTakedownStore) { s.takedowns = st }

func (s *Service) takedownStore() reportTakedownStore {
	if s.takedowns != nil {
		return s.takedowns
	}
	if s.store == nil {
		return nil
	}
	return s.store
}

// reportTakedownBackoff is the wait before attempt attempts+1, doubling
// from reportTakedownFirstRetry up to reportTakedownMaxRetry.
func reportTakedownBackoff(attempts int) time.Duration {
	d := reportTakedownFirstRetry
	for i := 1; i < attempts && d < reportTakedownMaxRetry; i++ {
		d *= 2
	}
	if d > reportTakedownMaxRetry {
		d = reportTakedownMaxRetry
	}
	return d
}

// takedownOutcome is what one attempt's error means for the report:
// the status to record and the error to keep visible on it.
func takedownOutcome(err error) (status, lastError string) {
	switch {
	case err == nil:
		return postgres.TakedownSent, ""
	case errors.Is(err, ErrPostSubjectNotFound), errors.Is(err, ErrPostDecisionSuperseded):
		// The post is gone or cannot be rejected from its state: nothing
		// is left to take down.
		return postgres.TakedownSuperseded, err.Error()
	case errors.Is(err, ErrPostDecisionConflict), errors.Is(err, ErrPostDecisionRefused):
		return postgres.TakedownFailed, err.Error()
	default:
		return postgres.TakedownPending, err.Error()
	}
}

// sendReportTakedown makes one attempt: a fresh subject read for the
// revision, then the signed reject.
func (s *Service) sendReportTakedown(ctx context.Context, t postgres.ReportTakedown) error {
	if s.postModeration == nil || s.takedownSender == nil {
		return ErrPostModerationNotConfigured
	}
	subject, err := s.postModeration.GetSubject(ctx, t.PostID)
	if err != nil {
		return err
	}
	if subject == nil || subject.Deleted {
		return ErrPostSubjectNotFound
	}
	return s.takedownSender.RejectReportedPost(ctx, t.ReportID, t.PostID, t.ActorID, subject.ContentRevision, t.Reason)
}

// ProcessReportTakedowns claims due takedowns (only that report's when only
// is set), sends each and records the outcome. It returns how many reached
// a final state.
func (s *Service) ProcessReportTakedowns(ctx context.Context, only *uuid.UUID) (int, error) {
	store := s.takedownStore()
	if store == nil {
		return 0, nil
	}
	due, err := store.ClaimReportTakedowns(ctx, only, reportTakedownLease, reportTakedownBatch)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, t := range due {
		status, lastError := takedownOutcome(s.sendReportTakedown(ctx, t))
		retry := time.Duration(0)
		if status == postgres.TakedownPending {
			retry = reportTakedownBackoff(t.Attempts + 1)
			slog.Warn("report takedown not delivered; will retry", "report_id", t.ReportID, "post_id", t.PostID,
				"attempt", t.Attempts+1, "retry_in", retry, "error", lastError)
		} else {
			done++
			if status == postgres.TakedownFailed {
				slog.Error("report takedown refused by post-service", "report_id", t.ReportID, "post_id", t.PostID, "error", lastError)
			}
		}
		// A recording failure leaves the claim to lapse: the row is sent
		// again after the lease, as the same decision.
		if err := store.RecordReportTakedown(ctx, t.ReportID, status, lastError, retry); err != nil {
			slog.Error("report takedown outcome not recorded", "report_id", t.ReportID, "status", status, "error", err)
		}
	}
	return done, nil
}

// RunReportTakedownSweeper sends due takedowns every interval until ctx
// ends: the retries after a failed first attempt, and any resolution whose
// first attempt never ran.
func (s *Service) RunReportTakedownSweeper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.ProcessReportTakedowns(ctx, nil); err != nil {
				slog.Warn("report takedown sweep failed", "error", err)
			}
		}
	}
}

// attemptReportTakedown is the first attempt, made right after the
// resolution committed, so the post usually goes down before the
// moderator's response returns. Failure is only logged: the row stays
// pending for the sweeper.
func (s *Service) attemptReportTakedown(ctx context.Context, reportID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
	defer cancel()
	if _, err := s.ProcessReportTakedowns(ctx, &reportID); err != nil {
		slog.Warn("report takedown first attempt failed; the sweeper will retry", "report_id", reportID, "error", err)
	}
}
