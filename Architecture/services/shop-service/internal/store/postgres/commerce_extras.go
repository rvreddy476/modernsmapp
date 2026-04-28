package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// ─── Types ───────────────────────────────────────────────────────────────────

type Storefront struct {
	ID            uuid.UUID       `json:"id"`
	SellerID      uuid.UUID       `json:"seller_id"`
	Handle        string          `json:"handle"`
	DisplayName   string          `json:"display_name"`
	Tagline       string          `json:"tagline"`
	BannerMediaID *uuid.UUID      `json:"banner_media_id,omitempty"`
	LogoMediaID   *uuid.UUID      `json:"logo_media_id,omitempty"`
	About         string          `json:"about"`
	Policies      json.RawMessage `json:"policies,omitempty"`
	IsVerified    bool            `json:"is_verified"`
	TotalSales    int64           `json:"total_sales"`
	AvgRating     float32         `json:"avg_rating"`
	ReviewCount   int             `json:"review_count"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type StorefrontCollection struct {
	ID           uuid.UUID  `json:"id"`
	StorefrontID uuid.UUID  `json:"storefront_id"`
	Name         string     `json:"name"`
	CoverMediaID *uuid.UUID `json:"cover_media_id,omitempty"`
	SortOrder    int        `json:"sort_order"`
	CreatedAt    time.Time  `json:"created_at"`
}

type PostProductTag struct {
	ID         uuid.UUID       `json:"id"`
	PostID     uuid.UUID       `json:"post_id"`
	ListingID  uuid.UUID       `json:"listing_id"`
	Position   json.RawMessage `json:"position,omitempty"`
	AppearAtMs *int            `json:"appear_at_ms,omitempty"`
	HideAtMs   *int            `json:"hide_at_ms,omitempty"`
	ClickCount int64           `json:"click_count"`
	CreatedAt  time.Time       `json:"created_at"`
}

type Wishlist struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Name      string    `json:"name"`
	IsPublic  bool      `json:"is_public"`
	CreatedAt time.Time `json:"created_at"`
}

type WishlistItem struct {
	WishlistID uuid.UUID `json:"wishlist_id"`
	ListingID  uuid.UUID `json:"listing_id"`
	AddedAt    time.Time `json:"added_at"`
}

type StockAlert struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	ListingID uuid.UUID `json:"listing_id"`
	Alerted   bool      `json:"alerted"`
	CreatedAt time.Time `json:"created_at"`
}

type GroupBuy struct {
	ID              uuid.UUID `json:"id"`
	ListingID       uuid.UUID `json:"listing_id"`
	InitiatorID     uuid.UUID `json:"initiator_id"`
	TargetQty       int       `json:"target_qty"`
	CurrentQty      int       `json:"current_qty"`
	DiscountedPrice float64   `json:"discounted_price"`
	OriginalPrice   float64   `json:"original_price"`
	ExpiresAt       time.Time `json:"expires_at"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
}

type GroupBuyParticipant struct {
	GroupBuyID      uuid.UUID  `json:"group_buy_id"`
	UserID          uuid.UUID  `json:"user_id"`
	PaymentIntentID *uuid.UUID `json:"payment_intent_id,omitempty"`
	JoinedAt        time.Time  `json:"joined_at"`
}

type AdCampaign struct {
	ID                   uuid.UUID  `json:"id"`
	AdvertiserID         uuid.UUID  `json:"advertiser_id"`
	Name                 string     `json:"name"`
	Objective            string     `json:"objective"`
	Status               string     `json:"status"`
	BudgetType           string     `json:"budget_type"`
	BudgetAmount         float64    `json:"budget_amount"`
	Currency             string     `json:"currency"`
	StartsAt             time.Time  `json:"starts_at"`
	EndsAt               *time.Time `json:"ends_at,omitempty"`
	SpentAmount          float64    `json:"spent_amount"`
	AttributionWindowDays int       `json:"attribution_window_days"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type AdSet struct {
	ID          uuid.UUID       `json:"id"`
	CampaignID  uuid.UUID       `json:"campaign_id"`
	Name        string          `json:"name"`
	Targeting   json.RawMessage `json:"targeting"`
	Placement   []string        `json:"placement"`
	BidType     string          `json:"bid_type"`
	BidAmount   *float64        `json:"bid_amount,omitempty"`
	DailyBudget *float64        `json:"daily_budget,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

type AdCreative struct {
	ID          uuid.UUID  `json:"id"`
	CampaignID  uuid.UUID  `json:"campaign_id"`
	ContentType string     `json:"content_type"`
	PostID      *uuid.UUID `json:"post_id,omitempty"`
	Headline    string     `json:"headline"`
	BodyText    string     `json:"body_text"`
	CTAType     string     `json:"cta_type"`
	CTAURL      string     `json:"cta_url"`
	MediaIDs    []string   `json:"media_ids"`
	CreatedAt   time.Time  `json:"created_at"`
}

type AdPerformance struct {
	CampaignID  uuid.UUID  `json:"campaign_id"`
	CreativeID  *uuid.UUID `json:"creative_id,omitempty"`
	Date        time.Time  `json:"date"`
	Impressions int64      `json:"impressions"`
	Clicks      int64      `json:"clicks"`
	Conversions int64      `json:"conversions"`
	Spend       float64    `json:"spend"`
	Reach       int64      `json:"reach"`
}

// ─── Storefronts ─────────────────────────────────────────────────────────────

func (s *Store) CreateStorefront(ctx context.Context, sf *Storefront) (*Storefront, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO shop.storefronts (seller_id, handle, display_name, tagline, about, policies)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, seller_id, handle, display_name, tagline, banner_media_id, logo_media_id,
		          about, policies, is_verified, total_sales, avg_rating, review_count, created_at, updated_at
	`, sf.SellerID, sf.Handle, sf.DisplayName, sf.Tagline, sf.About, sf.Policies).Scan(
		&sf.ID, &sf.SellerID, &sf.Handle, &sf.DisplayName, &sf.Tagline,
		&sf.BannerMediaID, &sf.LogoMediaID, &sf.About, &sf.Policies,
		&sf.IsVerified, &sf.TotalSales, &sf.AvgRating, &sf.ReviewCount,
		&sf.CreatedAt, &sf.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return sf, nil
}

func (s *Store) GetStorefrontByHandle(ctx context.Context, handle string) (*Storefront, error) {
	var sf Storefront
	err := s.db.QueryRow(ctx, `
		SELECT id, seller_id, handle, display_name, tagline, banner_media_id, logo_media_id,
		       about, policies, is_verified, total_sales, avg_rating, review_count, created_at, updated_at
		FROM shop.storefronts WHERE handle = $1
	`, handle).Scan(
		&sf.ID, &sf.SellerID, &sf.Handle, &sf.DisplayName, &sf.Tagline,
		&sf.BannerMediaID, &sf.LogoMediaID, &sf.About, &sf.Policies,
		&sf.IsVerified, &sf.TotalSales, &sf.AvgRating, &sf.ReviewCount,
		&sf.CreatedAt, &sf.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &sf, nil
}

func (s *Store) GetStorefrontBySeller(ctx context.Context, sellerID uuid.UUID) (*Storefront, error) {
	var sf Storefront
	err := s.db.QueryRow(ctx, `
		SELECT id, seller_id, handle, display_name, tagline, banner_media_id, logo_media_id,
		       about, policies, is_verified, total_sales, avg_rating, review_count, created_at, updated_at
		FROM shop.storefronts WHERE seller_id = $1
	`, sellerID).Scan(
		&sf.ID, &sf.SellerID, &sf.Handle, &sf.DisplayName, &sf.Tagline,
		&sf.BannerMediaID, &sf.LogoMediaID, &sf.About, &sf.Policies,
		&sf.IsVerified, &sf.TotalSales, &sf.AvgRating, &sf.ReviewCount,
		&sf.CreatedAt, &sf.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &sf, nil
}

func (s *Store) UpdateStorefront(ctx context.Context, id uuid.UUID, sf *Storefront) error {
	_, err := s.db.Exec(ctx, `
		UPDATE shop.storefronts SET display_name=$2, tagline=$3, about=$4, policies=$5, updated_at=NOW()
		WHERE id=$1
	`, id, sf.DisplayName, sf.Tagline, sf.About, sf.Policies)
	return err
}

func (s *Store) SetFeaturedListings(ctx context.Context, storefrontID uuid.UUID, listingIDs []uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM shop.storefront_featured WHERE storefront_id = $1`, storefrontID)
	if err != nil {
		return err
	}

	for i, lid := range listingIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO shop.storefront_featured (storefront_id, listing_id, position) VALUES ($1, $2, $3)
		`, storefrontID, lid, i)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) GetFeaturedListings(ctx context.Context, storefrontID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT listing_id FROM shop.storefront_featured WHERE storefront_id = $1 ORDER BY position
	`, storefrontID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *Store) CreateCollection(ctx context.Context, c *StorefrontCollection) (*StorefrontCollection, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO shop.storefront_collections (storefront_id, name, cover_media_id, sort_order)
		VALUES ($1, $2, $3, $4)
		RETURNING id, storefront_id, name, cover_media_id, sort_order, created_at
	`, c.StorefrontID, c.Name, c.CoverMediaID, c.SortOrder).Scan(
		&c.ID, &c.StorefrontID, &c.Name, &c.CoverMediaID, &c.SortOrder, &c.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) GetCollections(ctx context.Context, storefrontID uuid.UUID) ([]StorefrontCollection, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, storefront_id, name, cover_media_id, sort_order, created_at
		FROM shop.storefront_collections WHERE storefront_id = $1 ORDER BY sort_order
	`, storefrontID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []StorefrontCollection
	for rows.Next() {
		var c StorefrontCollection
		if err := rows.Scan(&c.ID, &c.StorefrontID, &c.Name, &c.CoverMediaID, &c.SortOrder, &c.CreatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, nil
}

func (s *Store) AddListingToCollection(ctx context.Context, collectionID, listingID uuid.UUID, position int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shop.collection_listings (collection_id, listing_id, position)
		VALUES ($1, $2, $3)
		ON CONFLICT (collection_id, position) DO UPDATE SET listing_id = EXCLUDED.listing_id
	`, collectionID, listingID, position)
	return err
}

func (s *Store) GetCollectionListings(ctx context.Context, collectionID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT listing_id FROM shop.collection_listings WHERE collection_id = $1 ORDER BY position
	`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ─── Product Tags ─────────────────────────────────────────────────────────────

func (s *Store) UpsertPostProductTags(ctx context.Context, postID uuid.UUID, tags []PostProductTag) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `DELETE FROM shop.post_product_tags WHERE post_id = $1`, postID)
	if err != nil {
		return err
	}

	for _, tag := range tags {
		_, err = tx.Exec(ctx, `
			INSERT INTO shop.post_product_tags (post_id, listing_id, position, appear_at_ms, hide_at_ms)
			VALUES ($1, $2, $3, $4, $5)
		`, postID, tag.ListingID, tag.Position, tag.AppearAtMs, tag.HideAtMs)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) GetPostProductTags(ctx context.Context, postID uuid.UUID) ([]PostProductTag, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, post_id, listing_id, position, appear_at_ms, hide_at_ms, click_count, created_at
		FROM shop.post_product_tags WHERE post_id = $1
	`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []PostProductTag
	for rows.Next() {
		var t PostProductTag
		if err := rows.Scan(&t.ID, &t.PostID, &t.ListingID, &t.Position, &t.AppearAtMs, &t.HideAtMs, &t.ClickCount, &t.CreatedAt); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	return tags, nil
}

func (s *Store) IncrementTagClickCount(ctx context.Context, tagID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE shop.post_product_tags SET click_count = click_count + 1 WHERE id = $1`, tagID)
	return err
}

// ─── Wishlist ─────────────────────────────────────────────────────────────────

func (s *Store) GetOrCreateDefaultWishlist(ctx context.Context, userID uuid.UUID) (*Wishlist, error) {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shop.wishlists (user_id, name) VALUES ($1, 'My Wishlist')
		ON CONFLICT DO NOTHING
	`, userID)
	if err != nil {
		return nil, err
	}

	var w Wishlist
	err = s.db.QueryRow(ctx, `
		SELECT id, user_id, name, is_public, created_at FROM shop.wishlists WHERE user_id = $1 LIMIT 1
	`, userID).Scan(&w.ID, &w.UserID, &w.Name, &w.IsPublic, &w.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &w, nil
}

func (s *Store) AddToWishlist(ctx context.Context, wishlistID, listingID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shop.wishlist_items (wishlist_id, listing_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, wishlistID, listingID)
	return err
}

func (s *Store) RemoveFromWishlist(ctx context.Context, wishlistID, listingID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM shop.wishlist_items WHERE wishlist_id = $1 AND listing_id = $2`, wishlistID, listingID)
	return err
}

func (s *Store) GetWishlistItems(ctx context.Context, wishlistID uuid.UUID, limit, offset int) ([]WishlistItem, error) {
	rows, err := s.db.Query(ctx, `
		SELECT wishlist_id, listing_id, added_at FROM shop.wishlist_items
		WHERE wishlist_id = $1 ORDER BY added_at DESC LIMIT $2 OFFSET $3
	`, wishlistID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []WishlistItem
	for rows.Next() {
		var wi WishlistItem
		if err := rows.Scan(&wi.WishlistID, &wi.ListingID, &wi.AddedAt); err != nil {
			return nil, err
		}
		items = append(items, wi)
	}
	return items, nil
}

func (s *Store) CreateStockAlert(ctx context.Context, userID, listingID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shop.stock_alerts (user_id, listing_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING
	`, userID, listingID)
	return err
}

func (s *Store) RemoveStockAlert(ctx context.Context, userID, listingID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM shop.stock_alerts WHERE user_id = $1 AND listing_id = $2`, userID, listingID)
	return err
}

func (s *Store) GetStockAlerts(ctx context.Context, listingID uuid.UUID) ([]StockAlert, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, listing_id, alerted, created_at FROM shop.stock_alerts
		WHERE listing_id = $1 AND alerted = FALSE
	`, listingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []StockAlert
	for rows.Next() {
		var a StockAlert
		if err := rows.Scan(&a.ID, &a.UserID, &a.ListingID, &a.Alerted, &a.CreatedAt); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, nil
}

// ─── Group Buy ────────────────────────────────────────────────────────────────

func (s *Store) CreateGroupBuy(ctx context.Context, g *GroupBuy) (*GroupBuy, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO shop.group_buys (listing_id, initiator_id, target_qty, discounted_price, original_price, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, listing_id, initiator_id, target_qty, current_qty, discounted_price, original_price, expires_at, status, created_at
	`, g.ListingID, g.InitiatorID, g.TargetQty, g.DiscountedPrice, g.OriginalPrice, g.ExpiresAt).Scan(
		&g.ID, &g.ListingID, &g.InitiatorID, &g.TargetQty, &g.CurrentQty,
		&g.DiscountedPrice, &g.OriginalPrice, &g.ExpiresAt, &g.Status, &g.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return g, nil
}

func (s *Store) JoinGroupBuy(ctx context.Context, groupBuyID, userID uuid.UUID, paymentIntentID *uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO shop.group_buy_participants (group_buy_id, user_id, payment_intent_id) VALUES ($1, $2, $3)
	`, groupBuyID, userID, paymentIntentID)
	if err != nil {
		return err
	}

	var currentQty, targetQty int
	err = tx.QueryRow(ctx, `
		UPDATE shop.group_buys SET current_qty = current_qty + 1
		WHERE id = $1
		RETURNING current_qty, target_qty
	`, groupBuyID).Scan(&currentQty, &targetQty)
	if err != nil {
		return err
	}

	if currentQty >= targetQty {
		_, err = tx.Exec(ctx, `UPDATE shop.group_buys SET status = 'fulfilled' WHERE id = $1`, groupBuyID)
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) GetGroupBuy(ctx context.Context, id uuid.UUID) (*GroupBuy, error) {
	var g GroupBuy
	err := s.db.QueryRow(ctx, `
		SELECT id, listing_id, initiator_id, target_qty, current_qty, discounted_price, original_price, expires_at, status, created_at
		FROM shop.group_buys WHERE id = $1
	`, id).Scan(
		&g.ID, &g.ListingID, &g.InitiatorID, &g.TargetQty, &g.CurrentQty,
		&g.DiscountedPrice, &g.OriginalPrice, &g.ExpiresAt, &g.Status, &g.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (s *Store) ListActiveGroupBuys(ctx context.Context, listingID uuid.UUID) ([]GroupBuy, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, listing_id, initiator_id, target_qty, current_qty, discounted_price, original_price, expires_at, status, created_at
		FROM shop.group_buys WHERE listing_id = $1 AND status = 'open' AND expires_at > NOW()
	`, listingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var buys []GroupBuy
	for rows.Next() {
		var g GroupBuy
		if err := rows.Scan(
			&g.ID, &g.ListingID, &g.InitiatorID, &g.TargetQty, &g.CurrentQty,
			&g.DiscountedPrice, &g.OriginalPrice, &g.ExpiresAt, &g.Status, &g.CreatedAt,
		); err != nil {
			return nil, err
		}
		buys = append(buys, g)
	}
	return buys, nil
}

func (s *Store) GetGroupBuyParticipants(ctx context.Context, groupBuyID uuid.UUID) ([]GroupBuyParticipant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT group_buy_id, user_id, payment_intent_id, joined_at
		FROM shop.group_buy_participants WHERE group_buy_id = $1 ORDER BY joined_at
	`, groupBuyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participants []GroupBuyParticipant
	for rows.Next() {
		var p GroupBuyParticipant
		if err := rows.Scan(&p.GroupBuyID, &p.UserID, &p.PaymentIntentID, &p.JoinedAt); err != nil {
			return nil, err
		}
		participants = append(participants, p)
	}
	return participants, nil
}

// ─── Ads ──────────────────────────────────────────────────────────────────────

func (s *Store) CreateAdCampaign(ctx context.Context, c *AdCampaign) (*AdCampaign, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO shop.ad_campaigns (advertiser_id, name, objective, budget_type, budget_amount, currency, starts_at, ends_at, attribution_window_days)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, advertiser_id, name, objective, status, budget_type, budget_amount, currency,
		          starts_at, ends_at, spent_amount, attribution_window_days, created_at, updated_at
	`, c.AdvertiserID, c.Name, c.Objective, c.BudgetType, c.BudgetAmount, c.Currency, c.StartsAt, c.EndsAt, c.AttributionWindowDays).Scan(
		&c.ID, &c.AdvertiserID, &c.Name, &c.Objective, &c.Status, &c.BudgetType,
		&c.BudgetAmount, &c.Currency, &c.StartsAt, &c.EndsAt, &c.SpentAmount,
		&c.AttributionWindowDays, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) GetAdCampaign(ctx context.Context, id uuid.UUID) (*AdCampaign, error) {
	var c AdCampaign
	err := s.db.QueryRow(ctx, `
		SELECT id, advertiser_id, name, objective, status, budget_type, budget_amount, currency,
		       starts_at, ends_at, spent_amount, attribution_window_days, created_at, updated_at
		FROM shop.ad_campaigns WHERE id = $1
	`, id).Scan(
		&c.ID, &c.AdvertiserID, &c.Name, &c.Objective, &c.Status, &c.BudgetType,
		&c.BudgetAmount, &c.Currency, &c.StartsAt, &c.EndsAt, &c.SpentAmount,
		&c.AttributionWindowDays, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) ListAdCampaigns(ctx context.Context, advertiserID uuid.UUID, status string, limit, offset int) ([]AdCampaign, error) {
	query := `
		SELECT id, advertiser_id, name, objective, status, budget_type, budget_amount, currency,
		       starts_at, ends_at, spent_amount, attribution_window_days, created_at, updated_at
		FROM shop.ad_campaigns WHERE advertiser_id = $1`
	args := []interface{}{advertiserID}
	if status != "" {
		query += ` AND status = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`
		args = append(args, status, limit, offset)
	} else {
		query += ` ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		args = append(args, limit, offset)
	}

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var campaigns []AdCampaign
	for rows.Next() {
		var c AdCampaign
		if err := rows.Scan(
			&c.ID, &c.AdvertiserID, &c.Name, &c.Objective, &c.Status, &c.BudgetType,
			&c.BudgetAmount, &c.Currency, &c.StartsAt, &c.EndsAt, &c.SpentAmount,
			&c.AttributionWindowDays, &c.CreatedAt, &c.UpdatedAt,
		); err != nil {
			return nil, err
		}
		campaigns = append(campaigns, c)
	}
	return campaigns, nil
}

func (s *Store) UpdateAdCampaignStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE shop.ad_campaigns SET status=$2, updated_at=NOW() WHERE id=$1`, id, status)
	return err
}

func (s *Store) CreateAdSet(ctx context.Context, as *AdSet) (*AdSet, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO shop.ad_sets (campaign_id, name, targeting, placement, bid_type, bid_amount, daily_budget)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, campaign_id, name, targeting, placement, bid_type, bid_amount, daily_budget, created_at
	`, as.CampaignID, as.Name, as.Targeting, as.Placement, as.BidType, as.BidAmount, as.DailyBudget).Scan(
		&as.ID, &as.CampaignID, &as.Name, &as.Targeting, &as.Placement,
		&as.BidType, &as.BidAmount, &as.DailyBudget, &as.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return as, nil
}

func (s *Store) CreateAdCreative(ctx context.Context, c *AdCreative) (*AdCreative, error) {
	err := s.db.QueryRow(ctx, `
		INSERT INTO shop.ad_creatives (campaign_id, content_type, post_id, headline, body_text, cta_type, cta_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, campaign_id, content_type, post_id, headline, body_text, cta_type, cta_url, media_ids, created_at
	`, c.CampaignID, c.ContentType, c.PostID, c.Headline, c.BodyText, c.CTAType, c.CTAURL).Scan(
		&c.ID, &c.CampaignID, &c.ContentType, &c.PostID, &c.Headline, &c.BodyText,
		&c.CTAType, &c.CTAURL, &c.MediaIDs, &c.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Store) UpsertAdPerformance(ctx context.Context, p *AdPerformance) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shop.ad_performance (campaign_id, creative_id, date, impressions, clicks, conversions, spend, reach)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (campaign_id, date) DO UPDATE SET
		    impressions = shop.ad_performance.impressions + EXCLUDED.impressions,
		    clicks      = shop.ad_performance.clicks + EXCLUDED.clicks,
		    conversions = shop.ad_performance.conversions + EXCLUDED.conversions,
		    spend       = shop.ad_performance.spend + EXCLUDED.spend,
		    reach       = shop.ad_performance.reach + EXCLUDED.reach
	`, p.CampaignID, p.CreativeID, p.Date, p.Impressions, p.Clicks, p.Conversions, p.Spend, p.Reach)
	return err
}

func (s *Store) GetAdPerformance(ctx context.Context, campaignID uuid.UUID, startDate, endDate time.Time) ([]AdPerformance, error) {
	rows, err := s.db.Query(ctx, `
		SELECT campaign_id, creative_id, date, impressions, clicks, conversions, spend, reach
		FROM shop.ad_performance WHERE campaign_id = $1 AND date >= $2 AND date <= $3 ORDER BY date
	`, campaignID, startDate, endDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var perf []AdPerformance
	for rows.Next() {
		var p AdPerformance
		if err := rows.Scan(&p.CampaignID, &p.CreativeID, &p.Date, &p.Impressions, &p.Clicks, &p.Conversions, &p.Spend, &p.Reach); err != nil {
			return nil, err
		}
		perf = append(perf, p)
	}
	return perf, nil
}

func (s *Store) SetAdFrequencyCap(ctx context.Context, campaignID uuid.UUID, perDay, perWeek int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO shop.ad_frequency_caps (campaign_id, max_per_user_per_day, max_per_user_per_week)
		VALUES ($1, $2, $3)
		ON CONFLICT (campaign_id) DO UPDATE SET
		    max_per_user_per_day  = EXCLUDED.max_per_user_per_day,
		    max_per_user_per_week = EXCLUDED.max_per_user_per_week
	`, campaignID, perDay, perWeek)
	return err
}
