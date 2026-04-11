package store

import (
	"context"
	"encoding/json"
	"fmt"
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
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
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
	status, seller_id, created_at, updated_at`

func scanPage(row interface {
	Scan(...any) error
}) (*BusinessPage, error) {
	var p BusinessPage
	err := row.Scan(
		&p.ID, &p.UserID, &p.PageHandle, &p.PageName, &p.Category, &p.Description,
		&p.Address, &p.Lat, &p.Lng, &p.BusinessHours, &p.Phone, &p.Whatsapp, &p.BusinessEmail, &p.Services,
		&p.PriceRange, &p.BookingURL, &p.MenuURLs, &p.Website, &p.CoverMediaID, &p.AvatarMediaID,
		&p.IsVerified, &p.AvgRating, &p.ReviewCount, &p.FollowerCount, &p.FAQ,
		&p.Status, &p.SellerID, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// CreateBusinessPage creates a new business page.
func (s *Store) CreateBusinessPage(ctx context.Context, p *BusinessPage) error {
	p.ID = uuid.New()
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Status == "" {
		p.Status = "active"
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO business_pages (id, user_id, page_handle, page_name, category, description,
			address, lat, lng, business_hours, phone, whatsapp, business_email, services,
			price_range, booking_url, menu_urls, website, cover_media_id, avatar_media_id,
			is_verified, avg_rating, review_count, follower_count, faq, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28)
	`, p.ID, p.UserID, p.PageHandle, p.PageName, p.Category, p.Description,
		p.Address, p.Lat, p.Lng, p.BusinessHours, p.Phone, p.Whatsapp, p.BusinessEmail, p.Services,
		p.PriceRange, p.BookingURL, p.MenuURLs, p.Website, p.CoverMediaID, p.AvatarMediaID,
		p.IsVerified, p.AvgRating, p.ReviewCount, p.FollowerCount, p.FAQ, p.Status,
		p.CreatedAt, p.UpdatedAt)
	return err
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

// DeleteBusinessPage soft-deletes (actually hard-deletes) a page owned by the user.
func (s *Store) DeleteBusinessPage(ctx context.Context, pageID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM business_pages WHERE id=$1 AND user_id=$2`, pageID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("PAGE_NOT_FOUND")
	}
	return nil
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

// FollowPage adds a follower and increments the count atomically.
func (s *Store) FollowPage(ctx context.Context, pageID, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx,
		`INSERT INTO page_followers (page_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
		pageID, userID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`UPDATE business_pages SET follower_count = follower_count+1, updated_at=NOW() WHERE id=$1`,
		pageID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UnfollowPage removes a follower and decrements the count atomically.
func (s *Store) UnfollowPage(ctx context.Context, pageID, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`DELETE FROM page_followers WHERE page_id=$1 AND user_id=$2`, pageID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return tx.Rollback(ctx)
	}
	_, err = tx.Exec(ctx,
		`UPDATE business_pages SET follower_count = GREATEST(0, follower_count-1), updated_at=NOW() WHERE id=$1`,
		pageID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func scanPages(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]BusinessPage, error) {
	var pages []BusinessPage
	for rows.Next() {
		var p BusinessPage
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.PageHandle, &p.PageName, &p.Category, &p.Description,
			&p.Address, &p.Lat, &p.Lng, &p.BusinessHours, &p.Phone, &p.Whatsapp, &p.BusinessEmail, &p.Services,
			&p.PriceRange, &p.BookingURL, &p.MenuURLs, &p.Website, &p.CoverMediaID, &p.AvatarMediaID,
			&p.IsVerified, &p.AvgRating, &p.ReviewCount, &p.FollowerCount, &p.FAQ,
			&p.Status, &p.SellerID, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}
