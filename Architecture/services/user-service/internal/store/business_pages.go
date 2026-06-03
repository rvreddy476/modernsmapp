package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// BusinessPage represents a business listing page.
type BusinessPage struct {
	ID             uuid.UUID       `json:"id"`
	UserID         uuid.UUID       `json:"user_id"`
	PageHandle     string          `json:"page_handle"`
	PageName       string          `json:"page_name"`
	Category       string          `json:"category"`
	Description    string          `json:"description"`
	Address        string          `json:"address,omitempty"`
	Lat            *float64        `json:"lat,omitempty"`
	Lng            *float64        `json:"lng,omitempty"`
	BusinessHours  json.RawMessage `json:"business_hours,omitempty"`
	Phone          string          `json:"phone,omitempty"`
	Whatsapp       string          `json:"whatsapp,omitempty"`
	BusinessEmail  string          `json:"business_email,omitempty"`
	Services       json.RawMessage `json:"services,omitempty"`
	PriceRange     string          `json:"price_range,omitempty"`
	BookingURL     string          `json:"booking_url,omitempty"`
	MenuURLs       json.RawMessage `json:"menu_urls,omitempty"`
	Website        string          `json:"website,omitempty"`
	CoverMediaID   string          `json:"cover_media_id,omitempty"`
	AvatarMediaID  string          `json:"avatar_media_id,omitempty"`
	IsVerified     bool            `json:"is_verified"`
	AvgRating      float64         `json:"avg_rating"`
	ReviewCount    int             `json:"review_count"`
	FollowerCount  int             `json:"follower_count"`
	IsFollowing    *bool           `json:"is_following,omitempty"`
	FAQ            json.RawMessage `json:"faq,omitempty"`
	Status         string          `json:"status"`
	SellerID       *uuid.UUID      `json:"seller_id,omitempty"`
	// Follow-Only Pages lifecycle fields (spec §3, §5.1).
	PageType           string     `json:"page_type"`
	VerificationStatus string     `json:"verification_status"`
	SubmittedAt        *time.Time `json:"submitted_at,omitempty"`
	ApprovedAt         *time.Time `json:"approved_at,omitempty"`
	ApprovedByUserID   *uuid.UUID `json:"approved_by_user_id,omitempty"`
	RejectedAt         *time.Time `json:"rejected_at,omitempty"`
	RejectionReason    string     `json:"rejection_reason,omitempty"`
	SuspendedAt        *time.Time `json:"suspended_at,omitempty"`
	DisabledAt         *time.Time `json:"disabled_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// BusinessReview represents a user review of a business page.
type BusinessReview struct {
	ID         uuid.UUID `json:"id"`
	PageID     uuid.UUID `json:"page_id"`
	ReviewerID uuid.UUID `json:"reviewer_id"`
	Rating     int       `json:"rating"` // 1-5
	ReviewText string    `json:"review_text"`
	CreatedAt  time.Time `json:"created_at"`
}

const pageSelectCols = `id, user_id, page_handle, page_name, category, description,
	address, lat, lng, business_hours, phone, whatsapp, business_email, services,
	price_range, booking_url, menu_urls, website, cover_media_id, avatar_media_id,
	is_verified, avg_rating, review_count, follower_count, faq,
	status, seller_id,
	COALESCE(page_type, ''), COALESCE(verification_status, 'not_submitted'),
	submitted_at, approved_at, approved_by_user_id, rejected_at,
	COALESCE(rejection_reason, ''), suspended_at, disabled_at,
	created_at, updated_at`

func scanPageRow(p *BusinessPage, row interface{ Scan(...any) error }) error {
	return row.Scan(
		&p.ID, &p.UserID, &p.PageHandle, &p.PageName, &p.Category, &p.Description,
		&p.Address, &p.Lat, &p.Lng, &p.BusinessHours, &p.Phone, &p.Whatsapp, &p.BusinessEmail, &p.Services,
		&p.PriceRange, &p.BookingURL, &p.MenuURLs, &p.Website, &p.CoverMediaID, &p.AvatarMediaID,
		&p.IsVerified, &p.AvgRating, &p.ReviewCount, &p.FollowerCount, &p.FAQ,
		&p.Status, &p.SellerID,
		&p.PageType, &p.VerificationStatus,
		&p.SubmittedAt, &p.ApprovedAt, &p.ApprovedByUserID, &p.RejectedAt,
		&p.RejectionReason, &p.SuspendedAt, &p.DisabledAt,
		&p.CreatedAt, &p.UpdatedAt,
	)
}

func scanPage(row interface {
	Scan(...any) error
}) (*BusinessPage, error) {
	var p BusinessPage
	if err := scanPageRow(&p, row); err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateBusinessPage creates a new business page and auto-inserts the creator's
// owner role row in the same transaction (spec §5.4).
func (s *Store) CreateBusinessPage(ctx context.Context, p *BusinessPage) error {
	p.ID = uuid.New()
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Status == "" {
		p.Status = "draft" // spec §6.1: new pages start in draft
	}
	if p.VerificationStatus == "" {
		p.VerificationStatus = "not_submitted"
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO business_pages (id, user_id, page_handle, page_name, category, description,
			address, lat, lng, business_hours, phone, whatsapp, business_email, services,
			price_range, booking_url, menu_urls, website, cover_media_id, avatar_media_id,
			is_verified, avg_rating, review_count, follower_count, faq, status,
			page_type, verification_status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30)
	`, p.ID, p.UserID, p.PageHandle, p.PageName, p.Category, p.Description,
		p.Address, p.Lat, p.Lng, p.BusinessHours, p.Phone, p.Whatsapp, p.BusinessEmail, p.Services,
		p.PriceRange, p.BookingURL, p.MenuURLs, p.Website, p.CoverMediaID, p.AvatarMediaID,
		p.IsVerified, p.AvgRating, p.ReviewCount, p.FollowerCount, p.FAQ, p.Status,
		p.PageType, p.VerificationStatus, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO page_roles (page_id, user_id, role) VALUES ($1,$2,'owner') ON CONFLICT DO NOTHING`,
		p.ID, p.UserID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ReconcileFollowerCounts recomputes every page's follower_count from the
// active page_followers rows, catching any drift (spec §11). Returns rows touched.
func (s *Store) ReconcileFollowerCounts(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE business_pages bp SET follower_count = sub.cnt
		FROM (
			SELECT page_id, COUNT(*) AS cnt FROM page_followers
			WHERE deleted_at IS NULL GROUP BY page_id
		) sub
		WHERE bp.id = sub.page_id AND bp.follower_count <> sub.cnt`)
	if err != nil {
		return 0, err
	}
	// Pages with zero active followers but a stale non-zero count.
	if _, err = s.db.Exec(ctx, `
		UPDATE business_pages SET follower_count = 0
		WHERE follower_count <> 0
		  AND id NOT IN (SELECT DISTINCT page_id FROM page_followers WHERE deleted_at IS NULL)`); err != nil {
		return tag.RowsAffected(), err
	}
	return tag.RowsAffected(), nil
}

// CountActivePagesOwned returns the number of non-disabled pages a user owns
// (spec §12: hard cap of 20).
func (s *Store) CountActivePagesOwned(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM business_pages WHERE user_id=$1 AND status != 'disabled'`,
		userID).Scan(&n)
	return n, err
}

// SetBusinessPageSellerID links a seller to a business page.
func (s *Store) SetBusinessPageSellerID(ctx context.Context, pageID, sellerID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE business_pages SET seller_id=$2, updated_at=NOW() WHERE id=$1`, pageID, sellerID)
	return err
}

// ActivateBusinessPage sets a business page status to active.
func (s *Store) ActivateBusinessPage(ctx context.Context, pageID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE business_pages SET status='active', updated_at=NOW() WHERE id=$1`, pageID)
	return err
}

// GetBusinessPageByHandle returns a business page by handle, optionally with viewer's follow status.
func (s *Store) GetBusinessPageByHandle(ctx context.Context, handle string, viewerID *uuid.UUID) (*BusinessPage, error) {
	p, err := scanPage(s.db.QueryRow(ctx,
		`SELECT `+pageSelectCols+` FROM business_pages WHERE page_handle = $1`, handle))
	if err != nil {
		return nil, err
	}
	if viewerID != nil {
		var following bool
		_ = s.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM page_followers WHERE page_id=$1 AND user_id=$2)`,
			p.ID, *viewerID).Scan(&following)
		p.IsFollowing = &following
	}
	return p, nil
}

// GetBusinessPageByID returns a business page by ID, optionally with viewer's follow status.
func (s *Store) GetBusinessPageByID(ctx context.Context, id uuid.UUID, viewerID *uuid.UUID) (*BusinessPage, error) {
	p, err := scanPage(s.db.QueryRow(ctx,
		`SELECT `+pageSelectCols+` FROM business_pages WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if viewerID != nil {
		var following bool
		_ = s.db.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM page_followers WHERE page_id=$1 AND user_id=$2)`,
			p.ID, *viewerID).Scan(&following)
		p.IsFollowing = &following
	}
	return p, nil
}

// UpdateBusinessPage updates a business page.
func (s *Store) UpdateBusinessPage(ctx context.Context, p *BusinessPage) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE business_pages SET
			page_name=$2, category=$3, description=$4, address=$5, lat=$6, lng=$7,
			business_hours=$8, phone=$9, whatsapp=$10, business_email=$11, services=$12,
			price_range=$13, booking_url=$14, menu_urls=$15, faq=$16,
			website=$17, cover_media_id=$18, avatar_media_id=$19, updated_at=NOW()
		WHERE id=$1 AND user_id=$20
	`, p.ID, p.PageName, p.Category, p.Description, p.Address, p.Lat, p.Lng,
		p.BusinessHours, p.Phone, p.Whatsapp, p.BusinessEmail, p.Services,
		p.PriceRange, p.BookingURL, p.MenuURLs, p.FAQ,
		p.Website, p.CoverMediaID, p.AvatarMediaID, p.UserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("PAGE_NOT_FOUND")
	}
	return nil
}

// DeleteBusinessPage soft-deletes a page (spec §6.7: owner soft-delete →
// status='disabled', terminal). Existing follow rows are preserved for audit.
func (s *Store) DeleteBusinessPage(ctx context.Context, pageID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`UPDATE business_pages SET status='disabled', disabled_at=NOW(), updated_at=NOW()
		 WHERE id=$1 AND user_id=$2 AND status != 'disabled'`, pageID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("PAGE_NOT_FOUND")
	}
	return nil
}

// UpdatePageStatus performs a page lifecycle transition atomically with its
// side effects (spec §3). Caller must have verified the transition is legal
// (pages.CanTransition) and the actor is authorized. On approval it also flips
// verification_status/is_verified and auto-approves all pending required docs
// (spec §9). reason is stored for reject/suspend.
func (s *Store) UpdatePageStatus(ctx context.Context, pageID uuid.UUID, to string, actorID uuid.UUID, reason string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	switch to {
	case "pending_review":
		_, err = tx.Exec(ctx,
			`UPDATE business_pages SET status='pending_review', submitted_at=NOW(),
			 verification_status='submitted', updated_at=NOW() WHERE id=$1`, pageID)
	case "approved":
		_, err = tx.Exec(ctx,
			`UPDATE business_pages SET status='approved', approved_at=NOW(),
			 approved_by_user_id=$2, verification_status='verified', is_verified=TRUE,
			 updated_at=NOW() WHERE id=$1`, pageID, actorID)
		if err == nil {
			// Auto-approve all pending documents (spec §9 step 2).
			_, err = tx.Exec(ctx,
				`UPDATE page_verification_documents SET status='approved',
				 reviewed_by_user_id=$2, reviewed_at=NOW()
				 WHERE page_id=$1 AND status='pending'`, pageID, actorID)
		}
	case "rejected":
		_, err = tx.Exec(ctx,
			`UPDATE business_pages SET status='rejected', rejected_at=NOW(),
			 rejection_reason=$2, verification_status='rejected', updated_at=NOW()
			 WHERE id=$1`, pageID, reason)
	case "suspended":
		_, err = tx.Exec(ctx,
			`UPDATE business_pages SET status='suspended', suspended_at=NOW(),
			 rejection_reason=$2, updated_at=NOW() WHERE id=$1`, pageID, reason)
	case "disabled":
		_, err = tx.Exec(ctx,
			`UPDATE business_pages SET status='disabled', disabled_at=NOW(), updated_at=NOW()
			 WHERE id=$1`, pageID)
	case "draft":
		_, err = tx.Exec(ctx,
			`UPDATE business_pages SET status='draft', updated_at=NOW() WHERE id=$1`, pageID)
	default:
		return fmt.Errorf("UNSUPPORTED_STATUS")
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// GetPageReviews returns reviews for a business page with pagination.
func (s *Store) GetPageReviews(ctx context.Context, pageID uuid.UUID, cursor time.Time, limit int) ([]BusinessReview, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, page_id, reviewer_id, rating, review_text, created_at
		FROM business_reviews
		WHERE page_id = $1 AND created_at < $2
		ORDER BY created_at DESC
		LIMIT $3
	`, pageID, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reviews []BusinessReview
	for rows.Next() {
		var r BusinessReview
		if err := rows.Scan(&r.ID, &r.PageID, &r.ReviewerID, &r.Rating, &r.ReviewText, &r.CreatedAt); err != nil {
			return nil, err
		}
		reviews = append(reviews, r)
	}
	return reviews, rows.Err()
}

// SubmitReview adds a review and recalculates the average rating.
func (s *Store) SubmitReview(ctx context.Context, r *BusinessReview) error {
	r.ID = uuid.New()
	r.CreatedAt = time.Now()

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO business_reviews (id, page_id, reviewer_id, rating, review_text, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, r.ID, r.PageID, r.ReviewerID, r.Rating, r.ReviewText, r.CreatedAt)
	if err != nil {
		return err
	}

	// Recalculate avg rating
	_, err = tx.Exec(ctx, `
		UPDATE business_pages SET
			avg_rating = (SELECT COALESCE(AVG(rating), 0) FROM business_reviews WHERE page_id = $1),
			review_count = (SELECT COUNT(*) FROM business_reviews WHERE page_id = $1),
			updated_at = NOW()
		WHERE id = $1
	`, r.PageID)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetUserBusinessPages returns all business pages owned by a user.
func (s *Store) GetUserBusinessPages(ctx context.Context, userID uuid.UUID) ([]BusinessPage, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+pageSelectCols+` FROM business_pages WHERE user_id=$1 ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPages(rows)
}

// DiscoverPages returns pages filtered by category and/or search query.
func (s *Store) DiscoverPages(ctx context.Context, category, search string, limit, offset int) ([]BusinessPage, error) {
	q := `SELECT ` + pageSelectCols + ` FROM business_pages WHERE status='active'`
	args := []any{}
	n := 1
	if category != "" {
		q += fmt.Sprintf(" AND category = $%d", n)
		args = append(args, category)
		n++
	}
	if search != "" {
		q += fmt.Sprintf(" AND (page_name ILIKE $%d OR description ILIKE $%d OR category ILIKE $%d)", n, n, n)
		args = append(args, "%"+search+"%")
		n++
	}
	q += fmt.Sprintf(" ORDER BY follower_count DESC, created_at DESC LIMIT $%d OFFSET $%d", n, n+1)
	args = append(args, limit, offset)

	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPages(rows)
}

// Sentinel errors for the follow flow → mapped to spec HTTP codes by handlers.
var (
	ErrPageNotFound     = fmt.Errorf("PAGE_NOT_FOUND")
	ErrPageNotFollowable = fmt.Errorf("PAGE_NOT_FOLLOWABLE")
	ErrCannotFollowOwn  = fmt.Errorf("CANNOT_FOLLOW_OWN_PAGE")
	ErrAlreadyFollowing = fmt.Errorf("ALREADY_FOLLOWING")
)

// FollowPage follows an approved page (spec §6.8). Enforces: page exists +
// approved; viewer is not owner/admin; no existing active follow. Revives a
// soft-deleted row when present. follower_count mutated in the same tx under a
// row lock. Returns the new follower_count.
func (s *Store) FollowPage(ctx context.Context, pageID, userID uuid.UUID) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	// Lock the page row; read status + count.
	var status string
	var ownerID uuid.UUID
	var count int
	err = tx.QueryRow(ctx,
		`SELECT status, user_id, follower_count FROM business_pages WHERE id=$1 FOR UPDATE`,
		pageID).Scan(&status, &ownerID, &count)
	if err != nil {
		if isNoRows(err) {
			return 0, ErrPageNotFound
		}
		return 0, err
	}
	if status != "approved" {
		return 0, ErrPageNotFollowable
	}
	// Owner or any admin/editor/viewer role on the page cannot follow it.
	if ownerID == userID {
		return 0, ErrCannotFollowOwn
	}
	var hasRole bool
	_ = tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM page_roles WHERE page_id=$1 AND user_id=$2
		 AND role IN ('owner','admin') AND deleted_at IS NULL)`, pageID, userID).Scan(&hasRole)
	if hasRole {
		return 0, ErrCannotFollowOwn
	}

	// Already actively following?
	var activeExists bool
	_ = tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM page_followers WHERE page_id=$1 AND user_id=$2 AND deleted_at IS NULL)`,
		pageID, userID).Scan(&activeExists)
	if activeExists {
		return count, ErrAlreadyFollowing
	}

	// Revive a soft-deleted row if present, else insert.
	tag, err := tx.Exec(ctx,
		`UPDATE page_followers SET deleted_at=NULL, created_at=NOW()
		 WHERE page_id=$1 AND user_id=$2 AND deleted_at IS NOT NULL`, pageID, userID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		if _, err = tx.Exec(ctx,
			`INSERT INTO page_followers (page_id, user_id) VALUES ($1,$2)`,
			pageID, userID); err != nil {
			return 0, err
		}
	}
	count++
	if _, err = tx.Exec(ctx,
		`UPDATE business_pages SET follower_count=$2, updated_at=NOW() WHERE id=$1`,
		pageID, count); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

// UnfollowPage soft-deletes the active follow row and decrements the count
// (spec §6.9). Idempotent: if no active row, returns the current count without
// error. Returns the resulting follower_count.
func (s *Store) UnfollowPage(ctx context.Context, pageID, userID uuid.UUID) (int, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var count int
	err = tx.QueryRow(ctx,
		`SELECT follower_count FROM business_pages WHERE id=$1 FOR UPDATE`, pageID).Scan(&count)
	if err != nil {
		if isNoRows(err) {
			return 0, ErrPageNotFound
		}
		return 0, err
	}

	tag, err := tx.Exec(ctx,
		`UPDATE page_followers SET deleted_at=NOW()
		 WHERE page_id=$1 AND user_id=$2 AND deleted_at IS NULL`, pageID, userID)
	if err != nil {
		return 0, err
	}
	if tag.RowsAffected() == 0 {
		// Idempotent no-op — nothing to unfollow.
		return count, tx.Commit(ctx)
	}
	if count > 0 {
		count--
	}
	if _, err = tx.Exec(ctx,
		`UPDATE business_pages SET follower_count=$2, updated_at=NOW() WHERE id=$1`,
		pageID, count); err != nil {
		return 0, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, err
	}
	return count, nil
}

func isNoRows(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no rows")
}

func scanPages(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]BusinessPage, error) {
	var pages []BusinessPage
	for rows.Next() {
		var p BusinessPage
		if err := scanPageRow(&p, rows); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}
