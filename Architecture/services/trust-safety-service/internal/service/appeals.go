package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Ordinary appeals (Copyright Match plan section 6.3, P-3) ────────────────
//
// An appeal is bound at submission to the decision it challenges (the
// post's base decision id, when the moderation subject exposes one, and its
// content revision). Adjudication compares that binding with a fresh
// subject read UNDER THE APPEAL'S ROW LOCK: a later decision on the post
// closes the appeal as 'superseded'; a moved revision under the same
// decision refuses the overturn and leaves the appeal open with a note.
// The canonical approve is signed with the revision captured at
// submission, is sent only from 'overturning', and its outcome is recorded
// under the appeal id (the moderationcap decision id) so a replay is safe.

// Each error maps to exactly one response in the handler.
var (
	// ErrAppealNotEligible: the post cannot be appealed by this user (not
	// the author, deleted, not rejected/needs_changes, or unknown).
	ErrAppealNotEligible = errors.New("content is not eligible for appeal")
	// ErrAppealInvalid: the request itself is malformed (type, reason, ids,
	// status value).
	ErrAppealInvalid = errors.New("invalid appeal request")
	// ErrAppealCopyrightCase: the post's last decision is a copyright case;
	// the remedy is a counter-notice, not an appeal.
	ErrAppealCopyrightCase = errors.New("the decision is a copyright case; use a counter-notice")
	// ErrAppealDecisionStale: the decision the author names is no longer
	// the post's current decision.
	ErrAppealDecisionStale = errors.New("the decision appealed is no longer the post's current decision")
	// ErrAppealNotFound: no appeal has the id.
	ErrAppealNotFound = errors.New("appeal not found")
	// ErrAppealTransition: the appeal's state does not allow the change.
	ErrAppealTransition = errors.New("appeal transition is not allowed")
	// ErrAppealSuperseded: a later decision on the post replaced the one
	// appealed; the appeal is now closed as superseded.
	ErrAppealSuperseded = errors.New("a later decision on the post superseded the appeal")
	// ErrAppealSubjectChanged: the post's revision moved since submission
	// under the same decision; the overturn is refused and the appeal
	// stays open.
	ErrAppealSubjectChanged = errors.New("the post changed since the appeal was submitted")
	// ErrAppealOverturnPending: the canonical approve did not complete; the
	// appeal stays 'overturning' and the sweeper (or a retry) replays it.
	ErrAppealOverturnPending = errors.New("the canonical overturn did not complete")
	// ErrAppealOverturnInFlight: another adjudicator's approve for this
	// appeal is presumed still running (it started within the replay
	// grace); it is not replayed yet.
	ErrAppealOverturnInFlight = errors.New("the overturn is in flight")
	// ErrAppealsUnavailable: the store or post-service is not reachable.
	ErrAppealsUnavailable = errors.New("appeals are unavailable")
)

// Errors the post-service client reports.
var (
	// ErrPostSubjectNotFound: post-service has no such moderation subject.
	ErrPostSubjectNotFound = errors.New("post moderation subject not found")
	// ErrPostDecisionSuperseded: post-service refused the command because
	// the post moved on (stale revision or a state that cannot take it).
	ErrPostDecisionSuperseded = errors.New("post-service refused the decision as superseded")
	// ErrPostDecisionConflict: the decision id was already used with
	// different claims.
	ErrPostDecisionConflict = errors.New("post-service refused the decision id as already used with different claims")
)

// PostModerationSubject is post-service's view of one post for appeals.
type PostModerationSubject struct {
	PostID          uuid.UUID
	AuthorID        uuid.UUID
	ReviewStatus    string
	ContentRevision int64
	Deleted         bool
	// LastDecisionID is post-service's latest base decision on the post;
	// nil while the moderation subject does not expose one.
	LastDecisionID *uuid.UUID
	// LastDecisionSource is that decision's source ("admin", "appeal",
	// "copyright", ...); "" while the subject does not expose one, which
	// is read as "not copyright".
	LastDecisionSource string
}

// PostModerationClient is the canonical post authority.
type PostModerationClient interface {
	GetSubject(ctx context.Context, postID uuid.UUID) (*PostModerationSubject, error)
	// OverturnAppeal sends the approve for appeal under decision id
	// appeal.ID, signed with contentRevision. A replay with identical
	// claims is acknowledged again; ErrPostDecisionSuperseded and
	// ErrPostDecisionConflict are post-service's refusals.
	OverturnAppeal(ctx context.Context, appeal *postgres.ContentAppeal, reviewerID uuid.UUID, contentRevision int64, reason string) error
}

// copyrightDecisionSource is the moderation-subject source that turns an
// appeal away to the counter-notice process.
const copyrightDecisionSource = "copyright"

// DefaultOverturnReplayGrace is how long after an appeal entered
// 'overturning' its approve is presumed still in flight: a second
// adjudicator inside it is refused (so racing overturns make ONE canonical
// call), and only after it does a retry or the sweeper replay the same
// claims. It exceeds the post-service client's 5 s timeout by a margin.
const DefaultOverturnReplayGrace = 30 * time.Second

// SetOverturnReplayGrace overrides DefaultOverturnReplayGrace (tests).
func (s *Service) SetOverturnReplayGrace(d time.Duration) { s.overturnGrace = &d }

func (s *Service) overturnReplayGrace() time.Duration {
	if s.overturnGrace != nil {
		return *s.overturnGrace
	}
	return DefaultOverturnReplayGrace
}

// appealableStatuses are the base statuses an author may appeal.
var appealableStatuses = map[string]bool{"rejected": true, "needs_changes": true}

// appealSubmissionCheck applies the submission rules to a subject read.
// requested is the decision id the author names (nil: the current one).
func appealSubmissionCheck(subject *PostModerationSubject, userID uuid.UUID, requested *uuid.UUID) error {
	if subject == nil || subject.Deleted || subject.AuthorID != userID {
		return ErrAppealNotEligible
	}
	if strings.EqualFold(strings.TrimSpace(subject.LastDecisionSource), copyrightDecisionSource) {
		return ErrAppealCopyrightCase
	}
	if !appealableStatuses[subject.ReviewStatus] {
		return ErrAppealNotEligible
	}
	// The binding is post-service's id, never the author's: a named id is
	// only checked against it, and only when post-service exposes one.
	if requested != nil && subject.LastDecisionID != nil && *requested != *subject.LastDecisionID {
		return ErrAppealDecisionStale
	}
	return nil
}

// adjudication is what a fresh subject read says about the appeal's binding.
type adjudication int

const (
	// adjudicationStands: the decision appealed is still the post's decision.
	adjudicationStands adjudication = iota
	// adjudicationSuperseded: a later decision replaced it.
	adjudicationSuperseded
	// adjudicationChanged: same decision, but the post's revision moved
	// (an author edit); the captured revision can no longer be applied.
	adjudicationChanged
)

// adjudicate compares the appeal's binding with a fresh subject read. With
// decision ids on both sides the id is the binding; without, the base
// status IS the decision (the plan's rule for appeals that pre-date the
// binding: base_review_status = action_taken). A copyright decision on
// the post supersedes an ordinary appeal whatever its id.
func adjudicate(appeal *postgres.ContentAppeal, subject *PostModerationSubject) adjudication {
	if strings.EqualFold(strings.TrimSpace(subject.LastDecisionSource), copyrightDecisionSource) {
		return adjudicationSuperseded
	}
	if appeal.AppealedDecisionID != nil && subject.LastDecisionID != nil {
		if *appeal.AppealedDecisionID != *subject.LastDecisionID {
			return adjudicationSuperseded
		}
	} else if subject.ReviewStatus != appeal.ActionTaken {
		return adjudicationSuperseded
	}
	if appeal.AppealedRevision != nil && *appeal.AppealedRevision != subject.ContentRevision {
		return adjudicationChanged
	}
	return adjudicationStands
}

// overturnReason is the reason the canonical approve carries. It is built
// from the note fixed at BeginOverturn so a replay sends identical claims.
func overturnReason(note *string) string {
	reason := "Appeal overturned"
	if note != nil && strings.TrimSpace(*note) != "" {
		reason += ": " + strings.TrimSpace(*note)
	}
	return reason
}

// SubmitAppeal opens an appeal by the author of contentID against the
// post's current decision. decisionID, when given, must be that decision.
func (s *Service) SubmitAppeal(ctx context.Context, userID uuid.UUID, contentType, contentIDStr, reason string, decisionID *uuid.UUID) (*postgres.ContentAppeal, error) {
	if s.extras == nil || s.postModeration == nil {
		return nil, ErrAppealsUnavailable
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	reason = strings.TrimSpace(reason)
	switch {
	case contentType != "post":
		return nil, fmt.Errorf("%w: content_type must be post", ErrAppealInvalid)
	case reason == "" || len(reason) > 2000:
		return nil, fmt.Errorf("%w: appeal_reason is required (at most 2000 characters)", ErrAppealInvalid)
	case decisionID != nil && *decisionID == uuid.Nil:
		return nil, fmt.Errorf("%w: decision_id must not be the nil uuid", ErrAppealInvalid)
	}
	contentID, err := uuid.Parse(strings.TrimSpace(contentIDStr))
	if err != nil {
		return nil, fmt.Errorf("%w: content_id must be a UUID", ErrAppealInvalid)
	}
	subject, err := s.readSubject(ctx, contentID)
	if err != nil {
		return nil, err
	}
	if err := appealSubmissionCheck(subject, userID, decisionID); err != nil {
		return nil, err
	}
	revision := subject.ContentRevision
	appeal := &postgres.ContentAppeal{
		ID: uuid.New(), UserID: userID, ContentType: "post", ContentID: contentID,
		ActionTaken: subject.ReviewStatus, AppealReason: reason,
		Status: postgres.AppealOpen, SubmittedAt: time.Now().UTC(),
		AppealedDecisionID: subject.LastDecisionID, AppealedRevision: &revision,
	}
	if err := s.extras.CreateAppeal(ctx, appeal); err != nil {
		return nil, err
	}
	return appeal, nil
}

// readSubject reads the post; an unknown post is not eligible, an
// unreachable post-service makes appeals unavailable.
func (s *Service) readSubject(ctx context.Context, postID uuid.UUID) (*PostModerationSubject, error) {
	subject, err := s.postModeration.GetSubject(ctx, postID)
	switch {
	case errors.Is(err, ErrPostSubjectNotFound):
		return nil, ErrAppealNotEligible
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrAppealsUnavailable, err)
	case subject == nil:
		return nil, ErrAppealNotEligible
	}
	return subject, nil
}

// ReviewAppeal applies a human reviewer's decision: under_review, upheld
// or overturned. Every state change happens under the appeal's row lock
// with its audit row (and, when terminal, its outbox event) in the same
// transaction; the canonical approve is sent only from 'overturning'.
func (s *Service) ReviewAppeal(ctx context.Context, id uuid.UUID, status, note string, meta postgres.AuditMeta) error {
	// Appeals are decided by people: a service actor is not a reviewer.
	if meta.Actor.UserID == uuid.Nil || meta.Actor.Validate() != nil {
		return ErrActorRequired
	}
	if s.extras == nil || s.postModeration == nil {
		return ErrAppealsUnavailable
	}
	status = strings.ToLower(strings.TrimSpace(status))
	note = strings.TrimSpace(note)
	if len(note) > 2000 {
		return fmt.Errorf("%w: note is at most 2000 characters", ErrAppealInvalid)
	}
	switch status {
	case postgres.AppealUnderReview, postgres.AppealUpheld, postgres.AppealOverturned:
	default:
		return fmt.Errorf("%w: status must be under_review, upheld or overturned", ErrAppealInvalid)
	}
	appeal, err := s.extras.GetAppeal(ctx, id)
	if err != nil {
		if isNoRows(err) {
			return ErrAppealNotFound
		}
		return fmt.Errorf("%w: %v", ErrAppealsUnavailable, err)
	}
	// A repeated terminal decision is the same decision.
	if appeal.Status == status && postgres.AppealTerminal(status) {
		return nil
	}
	switch status {
	case postgres.AppealUnderReview:
		changed, err := s.extras.TransitionAppeal(ctx, id, []string{postgres.AppealOpen}, status, note, meta)
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("%w: %s cannot move to %s", ErrAppealTransition, appeal.Status, status)
		}
		return nil
	case postgres.AppealUpheld:
		return s.upholdAppeal(ctx, appeal, note, meta)
	default:
		return s.overturnAppeal(ctx, appeal, note, meta)
	}
}

// activeFrom are the states a decision may be taken from. 'overturning' is
// not among them: the approve is in flight and only FinishOverturn leaves it.
var activeFrom = []string{postgres.AppealOpen, postgres.AppealUnderReview}

// upholdAppeal closes the appeal as upheld unless a later decision on the
// post has superseded it (then it is closed as superseded instead).
func (s *Service) upholdAppeal(ctx context.Context, appeal *postgres.ContentAppeal, note string, meta postgres.AuditMeta) error {
	if !isActiveStatus(appeal.Status) {
		return refusedByState(appeal.Status, postgres.AppealUpheld)
	}
	subject, err := s.readSubject(ctx, appeal.ContentID)
	if err != nil && !errors.Is(err, ErrAppealNotEligible) {
		return err
	}
	if subject != nil && adjudicate(appeal, subject) == adjudicationSuperseded {
		return s.supersede(ctx, appeal.ID, activeFrom, note, meta)
	}
	_, changed, err := s.extras.ResolveAppeal(ctx, appeal.ID, activeFrom, postgres.AppealUpheld, note, meta)
	if err != nil {
		return err
	}
	if !changed {
		return s.transitionRefused(ctx, appeal.ID, postgres.AppealUpheld)
	}
	return nil
}

// overturnAppeal is the overturn from an active state; from 'overturning'
// it is the replay of the in-flight approve.
func (s *Service) overturnAppeal(ctx context.Context, appeal *postgres.ContentAppeal, note string, meta postgres.AuditMeta) error {
	if appeal.Status == postgres.AppealOverturning {
		// Inside the grace the first adjudicator's approve is still running:
		// refusing here is what makes racing overturns one canonical call.
		if appeal.OverturnStartedAt != nil && time.Since(*appeal.OverturnStartedAt) < s.overturnReplayGrace() {
			return fmt.Errorf("%w: started %s ago", ErrAppealOverturnInFlight, time.Since(*appeal.OverturnStartedAt).Round(time.Millisecond))
		}
		return s.completeOverturn(ctx, appeal, meta)
	}
	if !isActiveStatus(appeal.Status) {
		return refusedByState(appeal.Status, postgres.AppealOverturned)
	}
	subject, err := s.readSubject(ctx, appeal.ContentID)
	if err != nil {
		return err
	}
	if subject.Deleted || subject.AuthorID != appeal.UserID {
		return ErrAppealNotEligible
	}
	// The on-read check: the row is locked by the transition below, so a
	// decision that lands between this read and the lock is caught by
	// post-service's revision fence instead (409 → superseded).
	switch adjudicate(appeal, subject) {
	case adjudicationSuperseded:
		return s.supersede(ctx, appeal.ID, activeFrom, note, meta)
	case adjudicationChanged:
		refusal := fmt.Sprintf("overturn refused: the post moved from revision %d to %d since the appeal was submitted",
			*appeal.AppealedRevision, subject.ContentRevision)
		if err := s.extras.NoteOverturnRefused(ctx, appeal.ID, refusal, meta); err != nil {
			return err
		}
		return ErrAppealSubjectChanged
	}
	// Legacy rows (no captured revision) are bound now, under the lock,
	// to the revision whose base status matched the one appealed.
	started, changed, err := s.extras.BeginOverturn(ctx, appeal.ID, activeFrom, note, subject.ContentRevision, meta)
	if err != nil {
		return err
	}
	if !changed {
		return s.transitionRefused(ctx, appeal.ID, postgres.AppealOverturned)
	}
	return s.completeOverturn(ctx, started, meta)
}

// completeOverturn sends the canonical approve for an appeal in
// 'overturning' and records the outcome under the appeal id. The claims
// come from the row (reviewer, note, captured revision), never from the
// caller, so a retry or the sweeper replays exactly what was first sent.
func (s *Service) completeOverturn(ctx context.Context, appeal *postgres.ContentAppeal, meta postgres.AuditMeta) error {
	if appeal.Status != postgres.AppealOverturning || appeal.ReviewedBy == nil || appeal.AppealedRevision == nil {
		return fmt.Errorf("%w: appeal %s is not a well-formed in-flight overturn", ErrAppealTransition, appeal.ID)
	}
	err := s.postModeration.OverturnAppeal(ctx, appeal, *appeal.ReviewedBy, *appeal.AppealedRevision, overturnReason(appeal.ResolutionNote))
	switch {
	case err == nil:
		return s.finishOverturn(ctx, appeal.ID, postgres.AppealOverturned, "", meta)
	case errors.Is(err, ErrPostDecisionSuperseded):
		return s.finishOverturn(ctx, appeal.ID, postgres.AppealSuperseded, "superseded: post-service refused the approve as stale", meta)
	case errors.Is(err, ErrPostDecisionConflict):
		return err
	default:
		return fmt.Errorf("%w: %v", ErrAppealOverturnPending, err)
	}
}

// finishOverturn leaves 'overturning'. A finisher that lost the race to
// another (retry versus sweeper) reads the winner's outcome.
func (s *Service) finishOverturn(ctx context.Context, id uuid.UUID, outcome, note string, meta postgres.AuditMeta) error {
	current, changed, err := s.extras.FinishOverturn(ctx, id, outcome, note, meta)
	if err != nil {
		return err
	}
	if !changed && current.Status != outcome {
		return s.transitionRefused(ctx, id, outcome)
	}
	if outcome == postgres.AppealSuperseded {
		return ErrAppealSuperseded
	}
	return nil
}

// supersede closes the appeal without an outcome; the error names it.
func (s *Service) supersede(ctx context.Context, id uuid.UUID, from []string, note string, meta postgres.AuditMeta) error {
	closing := "superseded: a later decision on the post replaced the one appealed"
	if note != "" {
		closing += " (" + note + ")"
	}
	current, changed, err := s.extras.ResolveAppeal(ctx, id, from, postgres.AppealSuperseded, closing, meta)
	if err != nil {
		return err
	}
	if !changed && current.Status != postgres.AppealSuperseded {
		return s.transitionRefused(ctx, id, postgres.AppealSuperseded)
	}
	return ErrAppealSuperseded
}

// transitionRefused names the state that refused the change, re-read so
// the message is current.
func (s *Service) transitionRefused(ctx context.Context, id uuid.UUID, to string) error {
	current, err := s.extras.GetAppeal(ctx, id)
	if err != nil {
		if isNoRows(err) {
			return ErrAppealNotFound
		}
		return err
	}
	return refusedByState(current.Status, to)
}

// refusedByState is the error for a change the appeal's state refuses: a
// superseded appeal says so, anything else is a plain transition refusal.
func refusedByState(status, to string) error {
	if status == postgres.AppealSuperseded {
		return ErrAppealSuperseded
	}
	return fmt.Errorf("%w: %s cannot move to %s", ErrAppealTransition, status, to)
}

// isNoRows reports a missing row from the store.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func isActiveStatus(status string) bool {
	return status == postgres.AppealOpen || status == postgres.AppealUnderReview
}

// SweeperActor is the audit actor of the overturn sweeper.
const SweeperActor = "trust-safety-service/appeal-sweeper"

// ResumeOverturningAppeals replays the canonical approve for every appeal
// that has been 'overturning' for at least grace (the plan's sweeper for
// transient failures). It returns how many appeals reached a terminal
// state. Each replay is independent; a failure is logged and the appeal
// stays in flight for the next sweep.
func (s *Service) ResumeOverturningAppeals(ctx context.Context, grace time.Duration) (int, error) {
	if s.extras == nil || s.postModeration == nil {
		return 0, ErrAppealsUnavailable
	}
	pending, err := s.extras.ListOverturningAppeals(ctx, grace, 100)
	if err != nil {
		return 0, err
	}
	meta := postgres.AuditMeta{Actor: postgres.ServiceActor(SweeperActor), Reason: "sweeper replay of an in-flight overturn"}
	done := 0
	for i := range pending {
		err := s.completeOverturn(ctx, &pending[i], meta)
		switch {
		case err == nil, errors.Is(err, ErrAppealSuperseded):
			done++
		default:
			slog.Warn("appeal overturn replay did not complete", "appeal_id", pending[i].ID, "error", err)
		}
	}
	return done, nil
}

// RunOverturnSweeper replays in-flight overturns every interval until ctx
// ends.
func (s *Service) RunOverturnSweeper(ctx context.Context, interval, grace time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.ResumeOverturningAppeals(ctx, grace); err != nil {
				slog.Warn("appeal overturn sweep failed", "error", err)
			} else if n > 0 {
				slog.Info("appeal overturn sweep completed appeals", "count", n)
			}
		}
	}
}

func (s *Service) ListAppeals(ctx context.Context, status string, limit, offset int) ([]postgres.ContentAppeal, error) {
	if s.extras == nil {
		return nil, ErrAppealsUnavailable
	}
	return s.extras.ListAppeals(ctx, status, limit, offset)
}

func (s *Service) ListUserAppeals(ctx context.Context, userID uuid.UUID) ([]postgres.ContentAppeal, error) {
	if s.extras == nil {
		return nil, ErrAppealsUnavailable
	}
	return s.extras.ListUserAppeals(ctx, userID)
}
