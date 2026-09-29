package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

var (
	ErrAppealNotEligible = errors.New("content is not eligible for appeal")
	ErrAppealTransition  = errors.New("appeal transition is not allowed")
)

type PostModerationSubject struct {
	PostID          uuid.UUID
	AuthorID        uuid.UUID
	ReviewStatus    string
	ContentRevision int64
	Deleted         bool
}

type PostModerationClient interface {
	GetSubject(ctx context.Context, postID uuid.UUID) (*PostModerationSubject, error)
	OverturnAppeal(ctx context.Context, appeal *postgres.ContentAppeal, reviewerID uuid.UUID, contentRevision int64, reason string) error
}

var validVerificationTypes = map[string]bool{
	"creator": true, "business": true, "organization": true, "government": true,
}

var validSeverities = map[string]bool{
	"warning": true, "strike": true, "severe_strike": true,
}

func (s *Service) SetExtrasStore(store *postgres.TrustExtrasStore)     { s.extras = store }
func (s *Service) SetPostModerationClient(client PostModerationClient) { s.postModeration = client }

func (s *Service) SubmitAppeal(ctx context.Context, userID uuid.UUID, contentType, contentIDStr, reason string) (*postgres.ContentAppeal, error) {
	if s.extras == nil || s.postModeration == nil {
		return nil, errors.New("appeals are unavailable")
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	reason = strings.TrimSpace(reason)
	if contentType != "post" || reason == "" || len(reason) > 2000 {
		return nil, ErrAppealNotEligible
	}
	contentID, err := uuid.Parse(contentIDStr)
	if err != nil {
		return nil, ErrAppealNotEligible
	}
	subject, err := s.postModeration.GetSubject(ctx, contentID)
	if err != nil || subject.Deleted || subject.AuthorID != userID ||
		(subject.ReviewStatus != "rejected" && subject.ReviewStatus != "needs_changes") {
		return nil, ErrAppealNotEligible
	}

	appeal := &postgres.ContentAppeal{
		ID: uuid.New(), UserID: userID, ContentType: "post", ContentID: contentID,
		ActionTaken: subject.ReviewStatus, AppealReason: reason,
		Status: "open", SubmittedAt: time.Now().UTC(),
	}
	if err := s.extras.CreateAppeal(ctx, appeal); err != nil {
		return nil, err
	}
	return appeal, nil
}

// ReviewAppeal applies a human reviewer's decision. The appeal update and
// its audit row share one transaction.
func (s *Service) ReviewAppeal(ctx context.Context, id uuid.UUID, status, note string, meta postgres.AuditMeta) error {
	// Appeals are decided by people: a service actor is not a reviewer.
	if meta.Actor.UserID == uuid.Nil || meta.Actor.Validate() != nil {
		return ErrActorRequired
	}
	reviewerID := meta.Actor.UserID
	if s.extras == nil || s.postModeration == nil {
		return errors.New("appeals are unavailable")
	}
	status = strings.ToLower(strings.TrimSpace(status))
	note = strings.TrimSpace(note)
	appeal, err := s.extras.GetAppeal(ctx, id)
	if err != nil {
		return ErrAppealTransition
	}
	if appeal.Status == status && (status == "upheld" || status == "overturned") {
		return nil
	}
	var from []string
	switch status {
	case "under_review":
		from = []string{"open"}
	case "upheld", "overturned":
		from = []string{"open", "under_review"}
	default:
		return ErrAppealTransition
	}
	if !containsStatus(from, appeal.Status) {
		return ErrAppealTransition
	}

	if status == "overturned" {
		subject, subjectErr := s.postModeration.GetSubject(ctx, appeal.ContentID)
		if subjectErr != nil || subject.AuthorID != appeal.UserID || subject.Deleted {
			return ErrAppealNotEligible
		}
		reason := "Appeal overturned"
		if note != "" {
			reason += ": " + note
		}
		// Canonical post first. Its decision ID is the appeal ID, so retry is
		// safe if the local appeal update fails after this call succeeds.
		if err := s.postModeration.OverturnAppeal(ctx, appeal, reviewerID, subject.ContentRevision, reason); err != nil {
			return fmt.Errorf("canonical post overturn failed: %w", err)
		}
	}
	changed, err := s.extras.TransitionAppeal(ctx, id, from, status, note, meta)
	if err != nil {
		return err
	}
	if !changed {
		return ErrAppealTransition
	}
	return nil
}

func containsStatus(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Service) ListAppeals(ctx context.Context, status string, limit, offset int) ([]postgres.ContentAppeal, error) {
	if s.extras == nil {
		return nil, errors.New("appeals are unavailable")
	}
	return s.extras.ListAppeals(ctx, status, limit, offset)
}

func (s *Service) ListUserAppeals(ctx context.Context, userID uuid.UUID) ([]postgres.ContentAppeal, error) {
	if s.extras == nil {
		return nil, errors.New("appeals are unavailable")
	}
	return s.extras.ListUserAppeals(ctx, userID)
}

// Remaining extras are deliberately retained but are not routed through the
// launch client. Their validation stays server-side.
func (s *Service) AddKeywordFilter(ctx context.Context, scope string, scopeID *uuid.UUID, keyword, action string, addedBy uuid.UUID) (*postgres.KeywordFilter, error) {
	if keyword == "" {
		return nil, fmt.Errorf("keyword must not be empty")
	}
	f := &postgres.KeywordFilter{ID: uuid.New(), Scope: scope, ScopeID: scopeID, Keyword: keyword, Action: action, AddedBy: addedBy, CreatedAt: time.Now()}
	if err := s.extras.CreateKeywordFilter(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

func (s *Service) GetKeywordFilters(ctx context.Context, scope string, scopeID *uuid.UUID) ([]postgres.KeywordFilter, error) {
	return s.extras.GetKeywordFilters(ctx, scope, scopeID)
}

func (s *Service) UpsertTeenAccount(ctx context.Context, t *postgres.TeenAccount) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	return s.extras.UpsertTeenAccount(ctx, t)
}

func (s *Service) GetTeenAccount(ctx context.Context, userID uuid.UUID) (*postgres.TeenAccount, error) {
	return s.extras.GetTeenAccount(ctx, userID)
}

// ErrInvalidStrike is a strike request that fails validation; the message
// says which field.
var ErrInvalidStrike = errors.New("invalid strike")

// IssueStrikeInput is one strike as an admin asks for it. Expiry is not an
// input: strike-v1 fixes it at issue + 90 days for every severity.
type IssueStrikeInput struct {
	UserID      uuid.UUID
	Reason      string
	Severity    string
	ContentType string
	ContentID   *uuid.UUID
	CaseID      *uuid.UUID
	StrikeGroup string
	// IdempotencyKey is required: a retried request with the same key gets
	// the strike the first request issued, never a second one. The admin
	// console mints one per click; a case uses "copyright_case:<id>:strike".
	IdempotencyKey string
}

// Validate normalises the input and refuses anything the store would
// refuse, so a bad request is answered before a transaction starts.
func (in *IssueStrikeInput) Validate() error {
	in.Reason = strings.TrimSpace(in.Reason)
	in.Severity = strings.ToLower(strings.TrimSpace(in.Severity))
	in.ContentType = strings.TrimSpace(in.ContentType)
	in.StrikeGroup = strings.TrimSpace(in.StrikeGroup)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	switch {
	case in.UserID == uuid.Nil:
		return fmt.Errorf("%w: user_id is required", ErrInvalidStrike)
	case in.Reason == "" || len(in.Reason) > 2000:
		return fmt.Errorf("%w: reason is required (at most 2000 characters)", ErrInvalidStrike)
	case !validSeverities[in.Severity]:
		return fmt.Errorf("%w: severity must be warning, strike or severe_strike", ErrInvalidStrike)
	case in.IdempotencyKey == "" || len(in.IdempotencyKey) > 200:
		return fmt.Errorf("%w: idempotency_key is required (at most 200 characters)", ErrInvalidStrike)
	case len(in.ContentType) > 64 || len(in.StrikeGroup) > 128:
		return fmt.Errorf("%w: content_type or strike_group is too long", ErrInvalidStrike)
	case in.CaseID != nil && *in.CaseID == uuid.Nil:
		return fmt.Errorf("%w: case_id must not be the nil uuid", ErrInvalidStrike)
	}
	return nil
}

// IssueStrike issues one strike under strike-v1, audited and outboxed in
// one transaction; a replayed idempotency key returns the existing strike
// with created=false. The issuing admin is meta's human actor.
func (s *Service) IssueStrike(ctx context.Context, in IssueStrikeInput, meta postgres.AuditMeta) (*postgres.UserStrike, bool, error) {
	if s.extras == nil {
		return nil, false, errors.New("strikes are unavailable")
	}
	if err := in.Validate(); err != nil {
		return nil, false, err
	}
	// Strikes are issued by people: a service actor is not an issuer.
	if meta.Actor.UserID == uuid.Nil || meta.Actor.Validate() != nil {
		return nil, false, ErrActorRequired
	}
	now := time.Now().UTC()
	createdBy := meta.Actor.UserID
	st := &postgres.UserStrike{
		ID:             uuid.New(),
		UserID:         in.UserID,
		Reason:         in.Reason,
		ContentType:    nilIfBlank(in.ContentType),
		ContentID:      in.ContentID,
		Severity:       in.Severity,
		CaseID:         in.CaseID,
		StrikeGroup:    nilIfBlank(in.StrikeGroup),
		PolicyVersion:  postgres.StrikePolicyVersion,
		IdempotencyKey: &in.IdempotencyKey,
		ExpiresAt:      now.Add(postgres.StrikeDuration),
		CreatedBy:      &createdBy,
		CreatedAt:      now,
	}
	return s.extras.IssueStrike(ctx, st, meta)
}

// VoidStrike voids one of userID's strikes (never deletes it), audited and
// outboxed in one transaction. A replay on an already-voided strike is a
// no-op with changed=false. postgres.ErrStrikeNotFound when the strike does
// not exist or is another user's.
func (s *Service) VoidStrike(ctx context.Context, userID, strikeID uuid.UUID, reason string, meta postgres.AuditMeta) (*postgres.UserStrike, bool, error) {
	if s.extras == nil {
		return nil, false, errors.New("strikes are unavailable")
	}
	if meta.Actor.UserID == uuid.Nil || meta.Actor.Validate() != nil {
		return nil, false, ErrActorRequired
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > 2000 {
		return nil, false, fmt.Errorf("%w: reason is required (at most 2000 characters)", ErrInvalidStrike)
	}
	return s.extras.VoidStrike(ctx, strikeID, &userID, reason, meta)
}

// GetUserStrikes lists the strikes that count now (not voided, not expired).
func (s *Service) GetUserStrikes(ctx context.Context, userID uuid.UUID) ([]postgres.UserStrike, error) {
	return s.extras.GetActiveStrikes(ctx, userID)
}

func nilIfBlank(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func (s *Service) SubmitVerificationRequest(ctx context.Context, userID uuid.UUID, vtype string, docs map[string]string) (*postgres.VerificationRequest, error) {
	if !validVerificationTypes[vtype] {
		return nil, fmt.Errorf("invalid verification type: %s", vtype)
	}
	pending, err := s.extras.HasPendingVerification(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("verification check failed: %w", err)
	}
	if pending {
		return nil, fmt.Errorf("a pending verification request already exists for this user")
	}
	now := time.Now()
	req := &postgres.VerificationRequest{ID: uuid.New(), UserID: userID, Type: vtype, Status: "pending", SubmittedDocs: docs, CreatedAt: now, UpdatedAt: now}
	if err := s.extras.CreateVerificationRequest(ctx, req); err != nil {
		return nil, err
	}
	return req, nil
}

func (s *Service) ReviewVerificationRequest(ctx context.Context, id uuid.UUID, status, rejectionReason string, reviewedBy uuid.UUID) error {
	validStatuses := map[string]bool{"approved": true, "rejected": true, "more_info_needed": true}
	if !validStatuses[status] {
		return fmt.Errorf("invalid status: %s", status)
	}
	return s.extras.UpdateVerificationStatus(ctx, id, status, rejectionReason, &reviewedBy)
}

func (s *Service) ListVerificationRequestsAdmin(ctx context.Context, status string, limit, offset int) ([]postgres.VerificationRequest, error) {
	return s.extras.ListVerificationRequests(ctx, status, limit, offset)
}

func (s *Service) AddMediaLabel(ctx context.Context, mediaAssetID uuid.UUID, labelType string, confidence float32, source string) (*postgres.MediaLabel, error) {
	label := &postgres.MediaLabel{ID: uuid.New(), MediaAssetID: mediaAssetID, LabelType: labelType, Confidence: confidence, Source: source, LabeledAt: time.Now()}
	if err := s.extras.CreateMediaLabel(ctx, label); err != nil {
		return nil, err
	}
	return label, nil
}

func (s *Service) GetMediaLabels(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaLabel, error) {
	return s.extras.GetMediaLabels(ctx, mediaAssetID)
}
