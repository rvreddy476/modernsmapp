package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ─── Structs ──────────────────────────────────────────────────────────────────

type ContentAppeal struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	ContentType    string     `json:"content_type"`
	ContentID      uuid.UUID  `json:"content_id"`
	ActionTaken    string     `json:"action_taken"`
	AppealReason   string     `json:"appeal_reason"`
	Status         string     `json:"status"`
	ReviewedBy     *uuid.UUID `json:"reviewed_by,omitempty"`
	ResolutionNote *string    `json:"resolution_note,omitempty"`
	SubmittedAt    time.Time  `json:"submitted_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
}

type KeywordFilter struct {
	ID        uuid.UUID  `json:"id"`
	Scope     string     `json:"scope"`
	ScopeID   *uuid.UUID `json:"scope_id,omitempty"`
	Keyword   string     `json:"keyword"`
	Action    string     `json:"action"`
	AddedBy   uuid.UUID  `json:"added_by"`
	CreatedAt time.Time  `json:"created_at"`
}

type TeenAccount struct {
	UserID           uuid.UUID  `json:"user_id"`
	GuardianID       *uuid.UUID `json:"guardian_id,omitempty"`
	GuardianApproved bool       `json:"guardian_approved"`
	DailyLimitMins   int        `json:"daily_limit_mins"`
	ContentFilter    string     `json:"content_filter"`
	DMRestricted     bool       `json:"dm_restricted"`
	FollowerApproval bool       `json:"follower_approval"`
	LocationHidden   bool       `json:"location_hidden"`
	CreatedAt        time.Time  `json:"created_at"`
}

type MediaLabel struct {
	ID           uuid.UUID `json:"id"`
	MediaAssetID uuid.UUID `json:"media_asset_id"`
	LabelType    string    `json:"label_type"`
	Confidence   float32   `json:"confidence"`
	Source       string    `json:"source"`
	LabeledAt    time.Time `json:"labeled_at"`
}

type UserStrike struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	Reason      string     `json:"reason"`
	ContentType *string    `json:"content_type,omitempty"`
	ContentID   *uuid.UUID `json:"content_id,omitempty"`
	Severity    string     `json:"severity"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedBy   *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type VerificationRequest struct {
	ID              uuid.UUID         `json:"id"`
	UserID          uuid.UUID         `json:"user_id"`
	Type            string            `json:"type"`
	Status          string            `json:"status"`
	SubmittedDocs   map[string]string `json:"submitted_docs,omitempty"`
	RejectionReason *string           `json:"rejection_reason,omitempty"`
	ReviewedBy      *uuid.UUID        `json:"reviewed_by,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// ─── Store ────────────────────────────────────────────────────────────────────

type TrustExtrasStore struct {
	db *pgxpool.Pool
}

func NewExtrasStore(db *pgxpool.Pool) *TrustExtrasStore {
	return &TrustExtrasStore{db: db}
}

// ─── Appeal methods ───────────────────────────────────────────────────────────

func (s *TrustExtrasStore) CreateAppeal(ctx context.Context, appeal *ContentAppeal) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.content_appeals
			(id, user_id, content_type, content_id, action_taken, appeal_reason, status, submitted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, appeal.ID, appeal.UserID, appeal.ContentType, appeal.ContentID,
		appeal.ActionTaken, appeal.AppealReason, appeal.Status, appeal.SubmittedAt)
	return err
}

func (s *TrustExtrasStore) GetAppeal(ctx context.Context, id uuid.UUID) (*ContentAppeal, error) {
	var a ContentAppeal
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, content_type, content_id, action_taken, appeal_reason,
		       status, reviewed_by, resolution_note, submitted_at, resolved_at
		FROM trust.content_appeals WHERE id = $1
	`, id).Scan(
		&a.ID, &a.UserID, &a.ContentType, &a.ContentID, &a.ActionTaken, &a.AppealReason,
		&a.Status, &a.ReviewedBy, &a.ResolutionNote, &a.SubmittedAt, &a.ResolvedAt,
	)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *TrustExtrasStore) ListAppeals(ctx context.Context, status string, limit, offset int) ([]ContentAppeal, error) {
	query := `
		SELECT id, user_id, content_type, content_id, action_taken, appeal_reason,
		       status, reviewed_by, resolution_note, submitted_at, resolved_at
		FROM trust.content_appeals
	`
	args := []interface{}{}
	if status != "" {
		query += " WHERE status = $1 ORDER BY submitted_at DESC LIMIT $2 OFFSET $3"
		args = append(args, status, limit, offset)
	} else {
		query += " ORDER BY submitted_at DESC LIMIT $1 OFFSET $2"
		args = append(args, limit, offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var appeals []ContentAppeal
	for rows.Next() {
		var a ContentAppeal
		if err := rows.Scan(
			&a.ID, &a.UserID, &a.ContentType, &a.ContentID, &a.ActionTaken, &a.AppealReason,
			&a.Status, &a.ReviewedBy, &a.ResolutionNote, &a.SubmittedAt, &a.ResolvedAt,
		); err != nil {
			return nil, err
		}
		appeals = append(appeals, a)
	}
	return appeals, nil
}

func (s *TrustExtrasStore) ListUserAppeals(ctx context.Context, userID uuid.UUID) ([]ContentAppeal, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, content_type, content_id, action_taken, appeal_reason,
		       status, reviewed_by, resolution_note, submitted_at, resolved_at
		FROM trust.content_appeals
		WHERE user_id = $1
		ORDER BY submitted_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var appeals []ContentAppeal
	for rows.Next() {
		var a ContentAppeal
		if err := rows.Scan(
			&a.ID, &a.UserID, &a.ContentType, &a.ContentID, &a.ActionTaken, &a.AppealReason,
			&a.Status, &a.ReviewedBy, &a.ResolutionNote, &a.SubmittedAt, &a.ResolvedAt,
		); err != nil {
			return nil, err
		}
		appeals = append(appeals, a)
	}
	return appeals, nil
}

func (s *TrustExtrasStore) UpdateAppealStatus(ctx context.Context, id uuid.UUID, status, note string, reviewerID *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE trust.content_appeals
		SET status = $2,
		    resolution_note = NULLIF($3, ''),
		    reviewed_by = $4,
		    resolved_at = CASE WHEN $2 IN ('upheld','overturned','expired') THEN NOW() ELSE resolved_at END
		WHERE id = $1
	`, id, status, note, reviewerID)
	return err
}

// HasOpenAppeal checks if an open appeal already exists for the same user+content.
func (s *TrustExtrasStore) HasOpenAppeal(ctx context.Context, userID, contentID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM trust.content_appeals
			WHERE user_id = $1 AND content_id = $2 AND status = 'open'
		)
	`, userID, contentID).Scan(&exists)
	return exists, err
}

// ─── Keyword filter methods ───────────────────────────────────────────────────

func (s *TrustExtrasStore) CreateKeywordFilter(ctx context.Context, f *KeywordFilter) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.keyword_filters (id, scope, scope_id, keyword, action, added_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, f.ID, f.Scope, f.ScopeID, f.Keyword, f.Action, f.AddedBy, f.CreatedAt)
	return err
}

func (s *TrustExtrasStore) GetKeywordFilters(ctx context.Context, scope string, scopeID *uuid.UUID) ([]KeywordFilter, error) {
	var query string
	var args []interface{}

	if scopeID != nil {
		query = `
			SELECT id, scope, scope_id, keyword, action, added_by, created_at
			FROM trust.keyword_filters
			WHERE scope = $1 AND scope_id = $2
			ORDER BY created_at DESC`
		args = []interface{}{scope, scopeID}
	} else {
		query = `
			SELECT id, scope, scope_id, keyword, action, added_by, created_at
			FROM trust.keyword_filters
			WHERE scope = $1 AND scope_id IS NULL
			ORDER BY created_at DESC`
		args = []interface{}{scope}
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var filters []KeywordFilter
	for rows.Next() {
		var f KeywordFilter
		if err := rows.Scan(&f.ID, &f.Scope, &f.ScopeID, &f.Keyword, &f.Action, &f.AddedBy, &f.CreatedAt); err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
	return filters, nil
}

func (s *TrustExtrasStore) DeleteKeywordFilter(ctx context.Context, id, addedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM trust.keyword_filters WHERE id = $1 AND added_by = $2
	`, id, addedBy)
	return err
}

func (s *TrustExtrasStore) MatchKeywords(ctx context.Context, scope string, scopeID *uuid.UUID, text string) ([]KeywordFilter, error) {
	filters, err := s.GetKeywordFilters(ctx, scope, scopeID)
	if err != nil {
		return nil, err
	}
	lower := strings.ToLower(text)
	var matched []KeywordFilter
	for _, f := range filters {
		if strings.Contains(lower, strings.ToLower(f.Keyword)) {
			matched = append(matched, f)
		}
	}
	return matched, nil
}

// ─── Teen account methods ─────────────────────────────────────────────────────

func (s *TrustExtrasStore) UpsertTeenAccount(ctx context.Context, t *TeenAccount) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.teen_accounts
			(user_id, guardian_id, guardian_approved, daily_limit_mins, content_filter,
			 dm_restricted, follower_approval, location_hidden, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id) DO UPDATE SET
			guardian_id       = EXCLUDED.guardian_id,
			daily_limit_mins  = EXCLUDED.daily_limit_mins,
			content_filter    = EXCLUDED.content_filter,
			dm_restricted     = EXCLUDED.dm_restricted,
			follower_approval = EXCLUDED.follower_approval,
			location_hidden   = EXCLUDED.location_hidden
	`, t.UserID, t.GuardianID, t.GuardianApproved, t.DailyLimitMins, t.ContentFilter,
		t.DMRestricted, t.FollowerApproval, t.LocationHidden, t.CreatedAt)
	return err
}

func (s *TrustExtrasStore) GetTeenAccount(ctx context.Context, userID uuid.UUID) (*TeenAccount, error) {
	var t TeenAccount
	err := s.db.QueryRow(ctx, `
		SELECT user_id, guardian_id, guardian_approved, daily_limit_mins, content_filter,
		       dm_restricted, follower_approval, location_hidden, created_at
		FROM trust.teen_accounts WHERE user_id = $1
	`, userID).Scan(
		&t.UserID, &t.GuardianID, &t.GuardianApproved, &t.DailyLimitMins, &t.ContentFilter,
		&t.DMRestricted, &t.FollowerApproval, &t.LocationHidden, &t.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *TrustExtrasStore) SetGuardianApproval(ctx context.Context, userID uuid.UUID, approved bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE trust.teen_accounts SET guardian_approved = $2 WHERE user_id = $1
	`, userID, approved)
	return err
}

// ─── Media label methods ──────────────────────────────────────────────────────

func (s *TrustExtrasStore) CreateMediaLabel(ctx context.Context, l *MediaLabel) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.media_labels (id, media_asset_id, label_type, confidence, source, labeled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, l.ID, l.MediaAssetID, l.LabelType, l.Confidence, l.Source, l.LabeledAt)
	return err
}

func (s *TrustExtrasStore) GetMediaLabels(ctx context.Context, mediaAssetID uuid.UUID) ([]MediaLabel, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, media_asset_id, label_type, confidence, source, labeled_at
		FROM trust.media_labels WHERE media_asset_id = $1
		ORDER BY labeled_at DESC
	`, mediaAssetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var labels []MediaLabel
	for rows.Next() {
		var l MediaLabel
		if err := rows.Scan(&l.ID, &l.MediaAssetID, &l.LabelType, &l.Confidence, &l.Source, &l.LabeledAt); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, nil
}

// ─── Strike methods ───────────────────────────────────────────────────────────

func (s *TrustExtrasStore) CreateStrike(ctx context.Context, st *UserStrike) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.user_strikes
			(id, user_id, reason, content_type, content_id, severity, expires_at, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, st.ID, st.UserID, st.Reason, st.ContentType, st.ContentID,
		st.Severity, st.ExpiresAt, st.CreatedBy, st.CreatedAt)
	return err
}

func (s *TrustExtrasStore) GetActiveStrikes(ctx context.Context, userID uuid.UUID) ([]UserStrike, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, reason, content_type, content_id, severity, expires_at, created_by, created_at
		FROM trust.user_strikes
		WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var strikes []UserStrike
	for rows.Next() {
		var st UserStrike
		if err := rows.Scan(
			&st.ID, &st.UserID, &st.Reason, &st.ContentType, &st.ContentID,
			&st.Severity, &st.ExpiresAt, &st.CreatedBy, &st.CreatedAt,
		); err != nil {
			return nil, err
		}
		strikes = append(strikes, st)
	}
	return strikes, nil
}

func (s *TrustExtrasStore) CountActiveStrikes(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM trust.user_strikes
		WHERE user_id = $1 AND (expires_at IS NULL OR expires_at > NOW())
	`, userID).Scan(&count)
	return count, err
}

// ─── Verification request methods ─────────────────────────────────────────────

func (s *TrustExtrasStore) CreateVerificationRequest(ctx context.Context, r *VerificationRequest) error {
	var docsJSON []byte
	if r.SubmittedDocs != nil {
		var err error
		docsJSON, err = json.Marshal(r.SubmittedDocs)
		if err != nil {
			return err
		}
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO trust.verification_requests
			(id, user_id, type, status, submitted_docs, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, r.ID, r.UserID, r.Type, r.Status, docsJSON, r.CreatedAt, r.UpdatedAt)
	return err
}

func (s *TrustExtrasStore) GetVerificationRequest(ctx context.Context, id uuid.UUID) (*VerificationRequest, error) {
	return s.scanVerificationRequest(ctx, `
		SELECT id, user_id, type, status, submitted_docs, rejection_reason, reviewed_by, created_at, updated_at
		FROM trust.verification_requests WHERE id = $1
	`, id)
}

func (s *TrustExtrasStore) GetUserVerificationRequest(ctx context.Context, userID uuid.UUID) (*VerificationRequest, error) {
	return s.scanVerificationRequest(ctx, `
		SELECT id, user_id, type, status, submitted_docs, rejection_reason, reviewed_by, created_at, updated_at
		FROM trust.verification_requests WHERE user_id = $1
		ORDER BY created_at DESC LIMIT 1
	`, userID)
}

func (s *TrustExtrasStore) scanVerificationRequest(ctx context.Context, query string, arg interface{}) (*VerificationRequest, error) {
	var r VerificationRequest
	var docsRaw []byte
	err := s.db.QueryRow(ctx, query, arg).Scan(
		&r.ID, &r.UserID, &r.Type, &r.Status,
		&docsRaw, &r.RejectionReason, &r.ReviewedBy,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if docsRaw != nil {
		_ = json.Unmarshal(docsRaw, &r.SubmittedDocs)
	}
	return &r, nil
}

func (s *TrustExtrasStore) ListVerificationRequests(ctx context.Context, status string, limit, offset int) ([]VerificationRequest, error) {
	query := `
		SELECT id, user_id, type, status, submitted_docs, rejection_reason, reviewed_by, created_at, updated_at
		FROM trust.verification_requests
	`
	args := []interface{}{}
	if status != "" {
		query += " WHERE status = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3"
		args = append(args, status, limit, offset)
	} else {
		query += " ORDER BY created_at DESC LIMIT $1 OFFSET $2"
		args = append(args, limit, offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var requests []VerificationRequest
	for rows.Next() {
		var r VerificationRequest
		var docsRaw []byte
		if err := rows.Scan(
			&r.ID, &r.UserID, &r.Type, &r.Status,
			&docsRaw, &r.RejectionReason, &r.ReviewedBy,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if docsRaw != nil {
			_ = json.Unmarshal(docsRaw, &r.SubmittedDocs)
		}
		requests = append(requests, r)
	}
	return requests, nil
}

func (s *TrustExtrasStore) HasPendingVerification(ctx context.Context, userID uuid.UUID) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM trust.verification_requests
			WHERE user_id = $1 AND status IN ('pending','more_info_needed')
		)
	`, userID).Scan(&exists)
	return exists, err
}

func (s *TrustExtrasStore) UpdateVerificationStatus(ctx context.Context, id uuid.UUID, status, rejectionReason string, reviewedBy *uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE trust.verification_requests
		SET status = $2,
		    rejection_reason = NULLIF($3, ''),
		    reviewed_by = $4,
		    updated_at = NOW()
		WHERE id = $1
	`, id, status, rejectionReason, reviewedBy)
	return err
}
