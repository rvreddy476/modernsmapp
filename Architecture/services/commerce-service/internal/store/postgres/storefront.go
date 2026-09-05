package postgres

// The shop's front: one product-summary shape, a landing page built from real
// catalogue data, favourites, and the category strip.
//
// ─── WHY ONE SUMMARY SHAPE ──────────────────────────────────────────────
//
// Before this file there were two hand-written product SELECTs — one in
// ListProducts, one in ListProductsFiltered — with different column lists and
// different scan orders, and every new surface added a third. That is exactly
// how `image_url` came to exist on the model and be absent from the browse
// grid: the field was added, one of the queries learned to fill it, and
// nothing made the other one follow.
//
// `productSummaryColumns` + `scanProductSummary` are now the ONLY way a
// product summary is read. Home sections, favourites, the browse grid and the
// seller's catalogue all go through them, so a field cannot be present on one
// surface and missing on the next.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── The one product-summary projection ─────────────────────────────────

// productSummaryColumns is the SELECT list every product-summary read uses.
//
// `p` is products, `sl` sellers, `pc` product_categories, `v` the cheapest
// active variant and `s` the stock roll-up — all supplied by
// productSummaryFrom, which must always accompany this list.
const productSummaryColumns = `
	p.id, p.seller_id, p.category_id, p.title, p.slug, p.status, p.approval_status,
	p.avg_rating, p.review_count, p.order_count, p.view_count, p.created_at, p.updated_at,
	p.primary_image_media_id,
	(SELECT value FROM product_attributes
	  WHERE product_id = p.id AND name = 'source_image_url'
	  ORDER BY sort_order LIMIT 1)                              AS source_image_url,
	sl.store_name,
	pc.name                                                     AS category_name,
	-- The gallery's cover: the first image in the product's own media, used
	-- when primary_image_media_id was never set. Sellers attach a gallery and
	-- never touch the legacy single-image column, so without this fallback a
	-- product with eight photographs still rendered as a grey box.
	(SELECT pm.media_id FROM product_media pm
	  WHERE pm.product_id = p.id AND pm.media_type = 'image'
	  ORDER BY pm.sort_order ASC, pm.created_at ASC LIMIT 1)    AS cover_media_id,
	v.id                                                        AS default_variant_id,
	v.min_selling_price, v.min_mrp, v.min_price_minor, v.mrp_minor,
	COALESCE(s.total_stock, 0)                                  AS total_stock,
	(COALESCE(s.total_stock, 0) > 0)                            AS in_stock`

// productSummaryFrom supplies every alias productSummaryColumns reads.
//
// The COALESCE(NULLIF(...)) on the minor columns is the same shape pricing
// uses and is not optional: migration 007 defaulted them to 0 rather than
// NULL, so a plain COALESCE finds a non-NULL zero on an unmigrated row and
// advertises a paid product as free.
const productSummaryFrom = `
	FROM products p
	JOIN sellers sl ON sl.id = p.seller_id
	LEFT JOIN product_categories pc ON pc.id = p.category_id
	LEFT JOIN LATERAL (
		SELECT id, selling_price AS min_selling_price, mrp AS min_mrp,
		       COALESCE(NULLIF(selling_price_minor, 0), ROUND(selling_price*100))::bigint AS min_price_minor,
		       COALESCE(NULLIF(mrp_minor, 0),           ROUND(mrp*100))::bigint           AS mrp_minor
		FROM product_variants
		WHERE product_id = p.id AND status = 'active'
		ORDER BY selling_price ASC
		LIMIT 1
	) v ON true
	LEFT JOIN LATERAL (
		SELECT SUM(GREATEST(i.total_qty - i.reserved_qty, 0))::int AS total_stock
		FROM product_variants pv
		JOIN inventory_items i ON i.variant_id = pv.id
		WHERE pv.product_id = p.id AND pv.status = 'active'
	) s ON true`

// productSummaryLive is the shopper-facing visibility rule. A surface that
// forgets it shows drafts and moderation rejections to buyers.
const productSummaryLive = `p.status = 'active' AND p.approval_status = 'approved'`

// scanProductSummary reads one row of productSummaryColumns, in order.
func scanProductSummary(rows pgx.Rows) (*Product, error) {
	var p Product
	if err := rows.Scan(
		&p.ID, &p.SellerID, &p.CategoryID, &p.Title, &p.Slug, &p.Status, &p.ApprovalStatus,
		&p.AvgRating, &p.ReviewCount, &p.OrderCount, &p.ViewCount, &p.CreatedAt, &p.UpdatedAt,
		&p.PrimaryImageMediaID, &p.SourceImageURL, &p.RetailerName, &p.CategoryName,
		&p.CoverMediaID,
		&p.DefaultVariantID, &p.MinSellingPrice, &p.MinMRP, &p.MinPriceMinor, &p.MRPMinor,
		&p.TotalStock, &p.InStock,
	); err != nil {
		return nil, err
	}
	return &p, nil
}

func collectProductSummaries(rows pgx.Rows) ([]*Product, error) {
	defer rows.Close()
	var out []*Product
	for rows.Next() {
		p, err := scanProductSummary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ─── Product media (the gallery a seller controls) ──────────────────────

// MaxProductMedia caps a product's gallery.
//
// Eight is the number the client's pager is built for, and the cap exists in
// the store as well as the service because the bulk-import path writes here
// too. A product with two hundred images is a product page that never
// finishes loading and a media batch that costs four upstream calls.
const MaxProductMedia = 8

// SetProductMedia replaces a product's gallery with exactly these ids, in
// this order, in ONE transaction.
//
// Replace rather than append: the client hands up the gallery it wants, and
// making the endpoint additive would mean a seller who removed a photograph
// in the editor and saved would still be showing it. The transaction is what
// stops a failure halfway through leaving the product with no images at all.
//
// sort_order is the slice index, so the first id is the cover.
func (s *Store) SetProductMedia(ctx context.Context, productID uuid.UUID, mediaIDs []uuid.UUID) error {
	if len(mediaIDs) > MaxProductMedia {
		return fmt.Errorf("commerce: a product may carry at most %d media", MaxProductMedia)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `DELETE FROM product_media WHERE product_id=$1`, productID); err != nil {
		return err
	}
	for i, id := range mediaIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO product_media (product_id, media_id, media_type, sort_order)
			 VALUES ($1,$2,'image',$3)`, productID, id, i); err != nil {
			return err
		}
	}
	// The cover is also the legacy single-image column, because everything
	// written before the gallery existed — the search index, the order-item
	// snapshot, exports — reads that one and would otherwise stay blank.
	var cover *uuid.UUID
	if len(mediaIDs) > 0 {
		cover = &mediaIDs[0]
	}
	if _, err := tx.Exec(ctx,
		`UPDATE products SET primary_image_media_id=$2, updated_at=NOW() WHERE id=$1`,
		productID, cover); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeleteProductMediaByMediaID removes one asset from a product's gallery and
// re-densifies sort_order so the remaining images keep their relative order
// without a hole. Returns whether a row was actually removed.
func (s *Store) DeleteProductMediaByMediaID(ctx context.Context, productID, mediaID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx,
		`DELETE FROM product_media WHERE product_id=$1 AND media_id=$2`, productID, mediaID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE product_media pm SET sort_order = ranked.rn - 1
		FROM (
			SELECT id, ROW_NUMBER() OVER (ORDER BY sort_order ASC, created_at ASC) AS rn
			FROM product_media WHERE product_id=$1
		) ranked
		WHERE pm.id = ranked.id`, productID); err != nil {
		return false, err
	}
	// Deleting the cover must move the cover, not leave products pointing at
	// an asset that is no longer in the gallery.
	if _, err := tx.Exec(ctx, `
		UPDATE products SET primary_image_media_id = (
			SELECT media_id FROM product_media
			WHERE product_id=$1 AND media_type='image'
			ORDER BY sort_order ASC, created_at ASC LIMIT 1
		), updated_at=NOW()
		WHERE id=$1`, productID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// ProductMediaIDs returns the gallery's media ids in display order.
func (s *Store) ProductMediaIDs(ctx context.Context, productID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT media_id FROM product_media WHERE product_id=$1
		 ORDER BY sort_order ASC, created_at ASC`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// orderItemCoverSQL resolves an order line's picture from the product the
// line points at — the legacy single image first, then the gallery cover.
//
// One SELECT-list fragment shared by both order-item reads, because they are
// two separately maintained queries that must return the same columns in the
// same order; the last field added to one and not the other is exactly how
// the paise columns came to be present on the detail read and absent from the
// list read. Correlates on the bare `order_items` table name, which is the
// alias both callers use.
const orderItemCoverSQL = `
	(SELECT COALESCE(pr.primary_image_media_id,
	        (SELECT pm.media_id FROM product_media pm
	          WHERE pm.product_id = pr.id AND pm.media_type = 'image'
	          ORDER BY pm.sort_order ASC, pm.created_at ASC LIMIT 1))
	   FROM products pr WHERE pr.id = order_items.product_id) AS cover_media_id`

// ─── Favourites ─────────────────────────────────────────────────────────

// AddFavourite records that a user likes a product. Idempotent by primary
// key: a double-tap, or the retry a flaky connection produces, is a no-op
// rather than a duplicate row.
func (s *Store) AddFavourite(ctx context.Context, userID, productID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO commerce_favourites (user_id, product_id) VALUES ($1,$2)
		 ON CONFLICT (user_id, product_id) DO NOTHING`, userID, productID)
	return err
}

// RemoveFavourite drops the like. Removing one that is not there succeeds:
// the caller asked for a state, not for an event, and reporting 404 would
// make an unfavourite that raced with another device look like a failure.
func (s *Store) RemoveFavourite(ctx context.Context, userID, productID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM commerce_favourites WHERE user_id=$1 AND product_id=$2`, userID, productID)
	return err
}

// ListFavourites returns the user's favourites newest-first, keyset-paged on
// (created_at, product_id) so a long list stays O(1) per page.
//
// Products the user favourited that have since been unpublished are excluded
// rather than shown as dead cards.
func (s *Store) ListFavourites(ctx context.Context, userID uuid.UUID, limit int, cursor string) ([]*Product, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	args := []any{userID}
	where := "WHERE f.user_id = $1 AND " + productSummaryLive
	idx := 2
	if ts, id, ok := parseKeysetCursor(cursor); ok {
		where += fmt.Sprintf(" AND (f.created_at, f.product_id) < ($%d, $%d)", idx, idx+1)
		args = append(args, ts, id)
		idx += 2
	}
	args = append(args, limit+1)

	rows, err := s.db.Query(ctx, `
		SELECT `+productSummaryColumns+`, f.created_at AS favourited_at
		`+productSummaryFrom+`
		JOIN commerce_favourites f ON f.product_id = p.id
		`+where+fmt.Sprintf(`
		ORDER BY f.created_at DESC, f.product_id DESC
		LIMIT $%d`, idx), args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()

	var out []*Product
	var stamps []time.Time
	for rows.Next() {
		var p Product
		var favAt time.Time
		if err := rows.Scan(
			&p.ID, &p.SellerID, &p.CategoryID, &p.Title, &p.Slug, &p.Status, &p.ApprovalStatus,
			&p.AvgRating, &p.ReviewCount, &p.OrderCount, &p.ViewCount, &p.CreatedAt, &p.UpdatedAt,
			&p.PrimaryImageMediaID, &p.SourceImageURL, &p.RetailerName, &p.CategoryName,
			&p.CoverMediaID,
			&p.DefaultVariantID, &p.MinSellingPrice, &p.MinMRP, &p.MinPriceMinor, &p.MRPMinor,
			&p.TotalStock, &p.InStock, &favAt); err != nil {
			return nil, "", err
		}
		fav := true
		p.IsFavourite = &fav
		out = append(out, &p)
		stamps = append(stamps, favAt)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}

	var next string
	if len(out) > limit {
		next = fmt.Sprintf("%d:%s", stamps[limit-1].UnixMicro(), out[limit-1].ID)
		out = out[:limit]
	}
	return out, next, nil
}

// FavouriteSet reports which of these products the user has favourited, in
// ONE query. The alternative — a per-row EXISTS in the summary projection —
// would put a subquery on every product read for every caller, including the
// anonymous ones that have no user at all.
func (s *Store) FavouriteSet(ctx context.Context, userID uuid.UUID, productIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(productIDs))
	if userID == uuid.Nil || len(productIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT product_id FROM commerce_favourites WHERE user_id=$1 AND product_id = ANY($2)`,
		userID, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// ─── Categories for the strip ───────────────────────────────────────────

// CategoryCard is a category as the home strip renders it: the row, plus the
// count of live products behind it and the media id for its tile.
type CategoryCard struct {
	ProductCategory
	// ImageMediaID is the category's own artwork. It is COALESCEd from the
	// existing icon/banner columns rather than given a third column of its
	// own — the taxonomy already had two and adding a third would leave the
	// strip asking which one it should draw.
	ImageMediaID *uuid.UUID `json:"image_media_id,omitempty"`
	ImageURL     string     `json:"image_url,omitempty"`
	ThumbnailURL string     `json:"thumbnail_url,omitempty"`
	ProductCount int        `json:"product_count"`
}

// ListCategoryCards returns the browsable top level with live product counts.
//
// The count is a correlated aggregate rather than a stored counter because a
// stored one drifts the moment a product is unpublished, and a category strip
// that says "Electronics (12)" and opens onto four products reads as broken.
//
// Two things this deliberately does NOT do, both because the phone reads this
// route and is frozen:
//
// Only roots. Once the taxonomy has depth, a flat list of every node puts
// "Textbooks" beside "Electronics" in a strip that is meant to be the top
// level, and the phone would start showing children the moment one is
// authored in the admin console. A picker that genuinely needs the hierarchy
// asks for ?tree=true. Every category seeded to date is a root, so this is a
// no-op on today's data and a fence against tomorrow's.
//
// The count spans the subtree. A root whose products all live under its
// children would otherwise read as empty and be greyed out — the strip would
// hide the very category the catalogue is filling up. Roots have no children
// today, so this too returns exactly the numbers it returned before.
func (s *Store) ListCategoryCards(ctx context.Context) ([]*CategoryCard, error) {
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE subtree AS (
			SELECT id AS root_id, id AS node_id
			FROM product_categories WHERE parent_id IS NULL AND is_active = TRUE
			UNION ALL
			SELECT s.root_id, c.id
			FROM product_categories c
			JOIN subtree s ON c.parent_id = s.node_id
			WHERE c.is_active = TRUE
		)
		SELECT c.id, c.parent_id, c.name, c.slug, c.description, c.display_order,
		       c.is_active, c.is_featured, c.created_at,
		       COALESCE(c.icon_media_id, c.banner_media_id) AS image_media_id,
		       COALESCE(n.cnt, 0)                           AS product_count
		FROM product_categories c
		LEFT JOIN LATERAL (
			SELECT COUNT(*)::int AS cnt FROM products p
			WHERE p.category_id IN (SELECT node_id FROM subtree WHERE root_id = c.id)
			  AND `+productSummaryLive+`
		) n ON true
		WHERE c.is_active = TRUE AND c.parent_id IS NULL
		ORDER BY c.display_order, c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*CategoryCard
	for rows.Next() {
		var c CategoryCard
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Description,
			&c.DisplayOrder, &c.IsActive, &c.IsFeatured, &c.CreatedAt,
			&c.ImageMediaID, &c.ProductCount); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// ─── Home sections ──────────────────────────────────────────────────────

// DealProducts are live products the seller is actually discounting: the
// cheapest active variant's selling price is below its MRP. Ordered by how
// deep the cut is, so "Deals of the day" leads with the best one.
//
// The discount is computed in SQL from the same COALESCE(NULLIF(...)) paise
// the client is shown, so the ordering and the badge can never disagree.
func (s *Store) DealProducts(ctx context.Context, limit int) ([]*Product, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+productSummaryColumns+`
		`+productSummaryFrom+`
		WHERE `+productSummaryLive+`
		  AND v.mrp_minor > 0
		  AND v.min_price_minor > 0
		  AND v.min_price_minor < v.mrp_minor
		ORDER BY ((v.mrp_minor - v.min_price_minor)::numeric / v.mrp_minor) DESC,
		         p.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return collectProductSummaries(rows)
}

// BestSellerProducts ranks by units actually sold in the window.
//
// Cancelled lines are excluded: a bulk order somebody placed and cancelled is
// not evidence that anything sells. Orders still awaiting payment are
// excluded for the same reason.
func (s *Store) BestSellerProducts(ctx context.Context, window time.Duration, limit int) ([]*Product, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+productSummaryColumns+`
		`+productSummaryFrom+`
		JOIN LATERAL (
			SELECT COALESCE(SUM(oi.quantity), 0)::int AS units
			FROM order_items oi
			JOIN orders o ON o.id = oi.order_id
			WHERE oi.product_id = p.id
			  AND oi.created_at >= NOW() - $1::interval
			  AND oi.status NOT IN ('cancelled','returned')
			  AND o.status NOT IN ('cancelled','payment_pending','payment_failed')
		) sold ON true
		WHERE `+productSummaryLive+` AND sold.units > 0
		ORDER BY sold.units DESC, p.avg_rating DESC, p.created_at DESC
		LIMIT $2`, fmt.Sprintf("%d hours", int(window.Hours())), limit)
	if err != nil {
		return nil, err
	}
	return collectProductSummaries(rows)
}

// MostViewedProducts is the best-sellers fallback for a catalogue that has
// not sold anything yet — a brand-new marketplace, or a dev stack. Returning
// nothing at all would leave the home page with one section on it.
func (s *Store) MostViewedProducts(ctx context.Context, limit int) ([]*Product, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+productSummaryColumns+`
		`+productSummaryFrom+`
		WHERE `+productSummaryLive+`
		ORDER BY p.view_count DESC, p.order_count DESC, p.avg_rating DESC, p.created_at DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return collectProductSummaries(rows)
}

// NewArrivalProducts is the most recently approved catalogue.
//
// published_at first, created_at as the fallback: a product drafted in March
// and approved yesterday is a new arrival, and ordering on created_at alone
// would bury it.
func (s *Store) NewArrivalProducts(ctx context.Context, limit int) ([]*Product, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+productSummaryColumns+`
		`+productSummaryFrom+`
		WHERE `+productSummaryLive+`
		ORDER BY COALESCE(p.published_at, p.created_at) DESC, p.id DESC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return collectProductSummaries(rows)
}

// CategoryProducts is one featured category's rail.
func (s *Store) CategoryProducts(ctx context.Context, categoryID uuid.UUID, limit int) ([]*Product, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+productSummaryColumns+`
		`+productSummaryFrom+`
		WHERE `+productSummaryLive+` AND p.category_id = $1
		ORDER BY p.avg_rating DESC, p.created_at DESC
		LIMIT $2`, categoryID, limit)
	if err != nil {
		return nil, err
	}
	return collectProductSummaries(rows)
}

// ─── Banners ────────────────────────────────────────────────────────────

// Banner is one card in the home rail.
type Banner struct {
	ID           uuid.UUID  `json:"id"`
	Title        string     `json:"title"`
	Subtitle     *string    `json:"subtitle,omitempty"`
	ImageMediaID *uuid.UUID `json:"image_media_id,omitempty"`
	ImageURL     string     `json:"image_url,omitempty"`
	TargetType   string     `json:"target_type"`
	TargetID     string     `json:"target_id"`
	Position     int        `json:"position"`
	Active       bool       `json:"active"`
	StartsAt     *time.Time `json:"starts_at,omitempty"`
	EndsAt       *time.Time `json:"ends_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at,omitempty"`
}

// LiveBanners returns the banners a shopper should see right now: switched
// on, and inside their scheduling window.
func (s *Store) LiveBanners(ctx context.Context, limit int) ([]*Banner, error) {
	return s.queryBanners(ctx, `
		WHERE active = TRUE
		  AND (starts_at IS NULL OR starts_at <= NOW())
		  AND (ends_at   IS NULL OR ends_at   >  NOW())
		ORDER BY position ASC, created_at ASC
		LIMIT $1`, limit)
}

// ListAllBanners is the admin view: every row, live or not, so an operator
// can see the ones that are switched off or expired.
func (s *Store) ListAllBanners(ctx context.Context) ([]*Banner, error) {
	return s.queryBanners(ctx, `ORDER BY position ASC, created_at ASC`)
}

func (s *Store) queryBanners(ctx context.Context, tail string, args ...any) ([]*Banner, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, title, subtitle, image_media_id, target_type, target_id,
		       position, active, starts_at, ends_at, created_at, updated_at
		FROM commerce_banners `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Banner
	for rows.Next() {
		var b Banner
		if err := rows.Scan(&b.ID, &b.Title, &b.Subtitle, &b.ImageMediaID, &b.TargetType,
			&b.TargetID, &b.Position, &b.Active, &b.StartsAt, &b.EndsAt,
			&b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}

// UpsertBanner creates or replaces one banner. A zero ID means create.
func (s *Store) UpsertBanner(ctx context.Context, b *Banner) error {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	return s.db.QueryRow(ctx, `
		INSERT INTO commerce_banners
		  (id, title, subtitle, image_media_id, target_type, target_id, position, active, starts_at, ends_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
		  title=EXCLUDED.title, subtitle=EXCLUDED.subtitle,
		  image_media_id=EXCLUDED.image_media_id,
		  target_type=EXCLUDED.target_type, target_id=EXCLUDED.target_id,
		  position=EXCLUDED.position, active=EXCLUDED.active,
		  starts_at=EXCLUDED.starts_at, ends_at=EXCLUDED.ends_at,
		  updated_at=NOW()
		RETURNING created_at, updated_at`,
		b.ID, b.Title, b.Subtitle, b.ImageMediaID, b.TargetType, b.TargetID,
		b.Position, b.Active, b.StartsAt, b.EndsAt,
	).Scan(&b.CreatedAt, &b.UpdatedAt)
}

// DeleteBanner removes one. Reports whether the row existed.
func (s *Store) DeleteBanner(ctx context.Context, id uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM commerce_banners WHERE id=$1`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ─── Media access: commerce as a content authority ──────────────────────

// VisibleProductMediaIDs reports which of these media ids a viewer may see
// because they are attached to a product.
//
// ─── WHY COMMERCE HAS TO ANSWER THIS ────────────────────────────────────
//
// media-service does not serve a protected asset on the strength of the
// asset alone: it asks the service that OWNS the content referencing it —
// post-service for post media, chat-message-service for attachments,
// identity-profile for avatars. That list has never included commerce, so
// every product photograph came back denied for every shopper with
// `no_visible_post_or_story`, and the catalogue rendered as grey boxes no
// matter how many images were attached.
//
// Commerce is the authority for product media. This is its answer.
//
// ─── THE RULE ───────────────────────────────────────────────────────────
//
//	A media id is visible to ANYONE, signed in or not, when it is attached
//	to a product that is live — active and approved.
//
// Anonymous is deliberately included: a product page is public, and a
// shopper who has not signed in must still see what is for sale. That is the
// same audience category identity-profile already allows for public profile
// photos, and it is why this is safe: the visibility comes from the PRODUCT
// being published, not from who is asking.
//
// A draft or moderation-rejected product's media stays invisible to
// everyone but the owning seller — otherwise a competitor holding a media id
// could read the pipeline the storefront route already hides.
//
// All three places a product can reference an image are checked, because a
// catalogue built over several years uses all three: the gallery
// (`product_media`), the legacy single image (`products`) and the per-variant
// swatch (`product_variants`).
func (s *Store) VisibleProductMediaIDs(ctx context.Context, viewerID uuid.UUID, mediaIDs []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := make(map[uuid.UUID]bool, len(mediaIDs))
	if len(mediaIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		WITH asked(media_id) AS (SELECT unnest($1::uuid[])),
		referenced AS (
			SELECT a.media_id, p.status, p.approval_status, sl.user_id AS seller_user_id
			FROM asked a
			JOIN products p ON (
				    p.primary_image_media_id = a.media_id
				 OR EXISTS (SELECT 1 FROM product_media pm
				             WHERE pm.product_id = p.id AND pm.media_id = a.media_id)
				 OR EXISTS (SELECT 1 FROM product_variants pv
				             WHERE pv.product_id = p.id AND pv.image_media_id = a.media_id)
			)
			JOIN sellers sl ON sl.id = p.seller_id
		)
		SELECT DISTINCT media_id FROM referenced
		WHERE (status = 'active' AND approval_status = 'approved')
		   OR ($2::uuid IS NOT NULL AND seller_user_id = $2::uuid)`,
		mediaIDs, nullableUUID(viewerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// nullableUUID renders the anonymous viewer as SQL NULL rather than as the
// all-zeroes UUID, so the seller-owns-it arm cannot match a row whose
// seller_user_id was somehow also nil.
func nullableUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// ─── shared keyset-cursor parsing ───────────────────────────────────────

// parseKeysetCursor decodes "<unix_micros>:<uuid>". A cursor that does not
// parse is treated as absent — the caller gets page one rather than an error,
// because a mangled cursor is a client bug that should not brick a browse
// screen.
func parseKeysetCursor(cursor string) (time.Time, uuid.UUID, bool) {
	if cursor == "" {
		return time.Time{}, uuid.Nil, false
	}
	parts := strings.SplitN(cursor, ":", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.Nil, false
	}
	micros, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.Nil, false
	}
	return time.UnixMicro(micros).UTC(), id, true
}
