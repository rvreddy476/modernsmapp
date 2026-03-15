package service

import (
	"context"
	"fmt"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

var validContentTypes = map[string]bool{
	"post":    true,
	"comment": true,
	"media":   true,
	"user":    true,
	"message": true,
}

var validAppealTransitions = map[string]bool{
	"under_review": true,
	"upheld":       true,
	"overturned":   true,
}

var validVerificationTypes = map[string]bool{
	"creator":      true,
	"business":     true,
	"organization": true,
	"government":   true,
}

var validSeverities = map[string]bool{
	"warning":       true,
	"strike":        true,
	"severe_strike": true,
}

// SetExtrasStore attaches the TrustExtrasStore to the Service so the extra
// feature methods have access to it.
func (s *Service) SetExtrasStore(store *postgres.TrustExtrasStore) {
	s.extras = store
}

// ─── Appeals ──────────────────────────────────────────────────────────────────

// SubmitAppeal validates and creates an appeal. Checks: content_type must be
// valid, no open appeal already exists for the same content.
func (s *Service) SubmitAppeal(ctx context.Context, userID uuid.UUID, contentType, contentIDStr, actionTaken, reason string) (*postgres.ContentAppeal, error) {
	if !validContentTypes[contentType] {
		return nil, fmt.Errorf("invalid content_type: %s", contentType)
	}
	contentID, err := uuid.Parse(contentIDStr)
	if err != nil {
		return nil, fmt.Errorf("invalid content_id")
	}
	open, err := s.extras.HasOpenAppeal(ctx, userID, contentID)
	if err != nil {
		return nil, fmt.Errorf("appeal check failed: %w", err)
	}
	if open {
		return nil, fmt.Errorf("an open appeal already exists for this content")
	}

	appeal := &postgres.ContentAppeal{
		ID:           uuid.New(),
		UserID:       userID,
		ContentType:  contentType,
		ContentID:    contentID,
		ActionTaken:  actionTaken,
		AppealReason: reason,
		Status:       "open",
		SubmittedAt:  time.Now(),
	}
	if err := s.extras.CreateAppeal(ctx, appeal); err != nil {
		return nil, err
	}
	return appeal, nil
}

// ReviewAppeal updates appeal status. Only under_review/upheld/overturned are
// valid transitions from open.
func (s *Service) ReviewAppeal(ctx context.Context, id uuid.UUID, status, note string, reviewerID uuid.UUID) error {
	if !validAppealTransitions[status] {
		return fmt.Errorf("invalid appeal status: %s (must be under_review, upheld, or overturned)", status)
	}
	return s.extras.UpdateAppealStatus(ctx, id, status, note, &reviewerID)
}

// ListAppeals returns appeals filtered by status (admin use).
func (s *Service) ListAppeals(ctx context.Context, status string, limit, offset int) ([]postgres.ContentAppeal, error) {
	return s.extras.ListAppeals(ctx, status, limit, offset)
}

// ─── Keyword filters ──────────────────────────────────────────────────────────

// AddKeywordFilter adds a keyword filter for the given scope.
func (s *Service) AddKeywordFilter(ctx context.Context, scope string, scopeID *uuid.UUID, keyword, action string, addedBy uuid.UUID) (*postgres.KeywordFilter, error) {
	if keyword == "" {
		return nil, fmt.Errorf("keyword must not be empty")
	}
	f := &postgres.KeywordFilter{
		ID:        uuid.New(),
		Scope:     scope,
		ScopeID:   scopeID,
		Keyword:   keyword,
		Action:    action,
		AddedBy:   addedBy,
		CreatedAt: time.Now(),
	}
	if err := s.extras.CreateKeywordFilter(ctx, f); err != nil {
		return nil, err
	}
	return f, nil
}

// GetKeywordFilters returns filters for a given scope.
func (s *Service) GetKeywordFilters(ctx context.Context, scope string, scopeID *uuid.UUID) ([]postgres.KeywordFilter, error) {
	return s.extras.GetKeywordFilters(ctx, scope, scopeID)
}

// ─── Teen accounts ────────────────────────────────────────────────────────────

// UpsertTeenAccount creates or updates teen safety settings.
func (s *Service) UpsertTeenAccount(ctx context.Context, t *postgres.TeenAccount) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	return s.extras.UpsertTeenAccount(ctx, t)
}

// GetTeenAccount retrieves teen safety settings for a user.
func (s *Service) GetTeenAccount(ctx context.Context, userID uuid.UUID) (*postgres.TeenAccount, error) {
	return s.extras.GetTeenAccount(ctx, userID)
}

// ─── Strikes ──────────────────────────────────────────────────────────────────

// IssueStrike creates a strike for a user.
func (s *Service) IssueStrike(ctx context.Context, userID uuid.UUID, reason, contentType string, contentID *uuid.UUID, severity string, createdBy uuid.UUID) (*postgres.UserStrike, error) {
	if !validSeverities[severity] {
		return nil, fmt.Errorf("invalid severity: %s (must be warning, strike, or severe_strike)", severity)
	}
	var ct *string
	if contentType != "" {
		ct = &contentType
	}
	strike := &postgres.UserStrike{
		ID:          uuid.New(),
		UserID:      userID,
		Reason:      reason,
		ContentType: ct,
		ContentID:   contentID,
		Severity:    severity,
		CreatedBy:   &createdBy,
		CreatedAt:   time.Now(),
	}
	if err := s.extras.CreateStrike(ctx, strike); err != nil {
		return nil, err
	}
	return strike, nil
}

// GetUserStrikes returns all active strikes for a user.
func (s *Service) GetUserStrikes(ctx context.Context, userID uuid.UUID) ([]postgres.UserStrike, error) {
	return s.extras.GetActiveStrikes(ctx, userID)
}

// ─── Verification requests ────────────────────────────────────────────────────

// SubmitVerificationRequest creates a verification request (one active per user).
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
	req := &postgres.VerificationRequest{
		ID:            uuid.New(),
		UserID:        userID,
		Type:          vtype,
		Status:        "pending",
		SubmittedDocs: docs,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.extras.CreateVerificationRequest(ctx, req); err != nil {
		return nil, err
	}
	return req, nil
}

// ReviewVerificationRequest updates verification status (admin only).
func (s *Service) ReviewVerificationRequest(ctx context.Context, id uuid.UUID, status, rejectionReason string, reviewedBy uuid.UUID) error {
	validStatuses := map[string]bool{
		"approved":         true,
		"rejected":         true,
		"more_info_needed": true,
	}
	if !validStatuses[status] {
		return fmt.Errorf("invalid status: %s", status)
	}
	return s.extras.UpdateVerificationStatus(ctx, id, status, rejectionReason, &reviewedBy)
}

// ListVerificationRequestsAdmin returns verification requests filtered by status.
func (s *Service) ListVerificationRequestsAdmin(ctx context.Context, status string, limit, offset int) ([]postgres.VerificationRequest, error) {
	return s.extras.ListVerificationRequests(ctx, status, limit, offset)
}

// ─── Media labels ─────────────────────────────────────────────────────────────

// AddMediaLabel labels a media asset.
func (s *Service) AddMediaLabel(ctx context.Context, mediaAssetID uuid.UUID, labelType string, confidence float32, source string) (*postgres.MediaLabel, error) {
	label := &postgres.MediaLabel{
		ID:           uuid.New(),
		MediaAssetID: mediaAssetID,
		LabelType:    labelType,
		Confidence:   confidence,
		Source:       source,
		LabeledAt:    time.Now(),
	}
	if err := s.extras.CreateMediaLabel(ctx, label); err != nil {
		return nil, err
	}
	return label, nil
}

// GetMediaLabels retrieves all labels for a media asset.
func (s *Service) GetMediaLabels(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaLabel, error) {
	return s.extras.GetMediaLabels(ctx, mediaAssetID)
}
