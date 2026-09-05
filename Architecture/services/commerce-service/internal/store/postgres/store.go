package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store { return &Store{db: db} }

// DB returns the underlying pool for callers that need to open a
// cross-domain transaction (e.g. AcceptRFQQuote spans orders +
// rfq_quotes + rfqs). Most code paths should use the helper methods
// on Store; reach for DB() sparingly.
func (s *Store) DB() *pgxpool.Pool { return s.db }

// ─── Seller ──────────────────────────────────────────────────

func (s *Store) CreateSeller(ctx context.Context, sel *Seller) error {
	sel.ID = uuid.New()
	sel.CreatedAt = time.Now()
	sel.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO sellers (id, user_id, seller_type, store_name, brand_name, slug, description,
		  email, phone, gst_number, state, city, postal_code, verification_status, store_status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		sel.ID, sel.UserID, sel.SellerType, sel.StoreName, sel.BrandName, sel.Slug, sel.Description,
		sel.Email, sel.Phone, sel.GSTNumber, sel.State, sel.City, sel.PostalCode,
		sel.VerificationStatus, sel.StoreStatus, sel.CreatedAt, sel.UpdatedAt,
	)
	return err
}

func (s *Store) GetSellerByUserID(ctx context.Context, userID uuid.UUID) (*Seller, error) {
	var sel Seller
	// `status` and `onboarding_step` are selected, and that is not cosmetic.
	//
	// They were absent from this list, so `GET /v1/commerce/sellers/me`
	// always answered `"status": ""` and `"onboarding_step": 0` no matter what
	// the row actually held. A client could not tell a draft seller from an
	// approved one — which is precisely the question any seller UI has to
	// answer before it decides what to show, and the question that decides
	// whether `POST /products/:id/submit` will be refused.
	//
	// `status` is the onboarding/approval state machine (draft → submitted →
	// approved). `verification_status` is the separate KYC column, and the two
	// are not interchangeable: migration 014 added `format_ok`, which means
	// only that a regex liked the document's shape.
	err := s.db.QueryRow(ctx, `SELECT id,user_id,seller_type,store_name,brand_name,slug,description,
		logo_media_id,banner_media_id,email,phone,gst_number,pan_number,state,city,postal_code,
		verification_status,store_status,quality_score,performance_tier,avg_rating,review_count,
		follower_count,total_products,total_orders,created_at,updated_at,
		status,onboarding_step
		FROM sellers WHERE user_id=$1`, userID).Scan(
		&sel.ID, &sel.UserID, &sel.SellerType, &sel.StoreName, &sel.BrandName, &sel.Slug, &sel.Description,
		&sel.LogoMediaID, &sel.BannerMediaID, &sel.Email, &sel.Phone, &sel.GSTNumber, &sel.PANNumber,
		&sel.State, &sel.City, &sel.PostalCode, &sel.VerificationStatus, &sel.StoreStatus,
		&sel.QualityScore, &sel.PerformanceTier, &sel.AvgRating, &sel.ReviewCount,
		&sel.FollowerCount, &sel.TotalProducts, &sel.TotalOrders, &sel.CreatedAt, &sel.UpdatedAt,
		&sel.Status, &sel.OnboardingStep,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		// The caller has no seller account.
		//
		// This used to `return &sel, err` — a non-nil, zero-valued Seller
		// alongside pgx.ErrNoRows. Every caller guarding with
		// `if seller == nil { return ErrNoSellerProfile }` was therefore
		// holding a check that could not fire, and the raw pgx error reached
		// the edge as a 500 instead of a 403.
		//
		// The nil is what makes those guards live. The typed sentinel is what
		// lets a handler tell "this user is not a seller" apart from "the
		// database is down" — the existing handlers map *any* error here to
		// 403 NO_SELLER, which reports an outage as an authorisation failure.
		return nil, ErrNoSellerRow
	}
	if err != nil {
		return nil, err
	}
	return &sel, nil
}

// ErrNoSellerRow means the user has no seller account.
//
// It is an error rather than a (nil, nil) return because the onboarding path
// reads "no row" as "start a new draft", and that branch predates this
// package's nil-and-sentinel convention.
var ErrNoSellerRow = errors.New("commerce: this user has no seller account")

func (s *Store) GetSellerByID(ctx context.Context, id uuid.UUID) (*Seller, error) {
	var sel Seller
	err := s.db.QueryRow(ctx, `SELECT id,user_id,seller_type,store_name,brand_name,slug,description,
		logo_media_id,banner_media_id,email,phone,gst_number,pan_number,state,city,postal_code,
		verification_status,store_status,quality_score,performance_tier,avg_rating,review_count,
		follower_count,total_products,total_orders,created_at,updated_at
		FROM sellers WHERE id=$1`, id).Scan(
		&sel.ID, &sel.UserID, &sel.SellerType, &sel.StoreName, &sel.BrandName, &sel.Slug, &sel.Description,
		&sel.LogoMediaID, &sel.BannerMediaID, &sel.Email, &sel.Phone, &sel.GSTNumber, &sel.PANNumber,
		&sel.State, &sel.City, &sel.PostalCode, &sel.VerificationStatus, &sel.StoreStatus,
		&sel.QualityScore, &sel.PerformanceTier, &sel.AvgRating, &sel.ReviewCount,
		&sel.FollowerCount, &sel.TotalProducts, &sel.TotalOrders, &sel.CreatedAt, &sel.UpdatedAt,
	)
	return &sel, err
}

func (s *Store) UpdateSellerStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `UPDATE sellers SET store_status=$2, updated_at=NOW() WHERE id=$1`, id, status)
	return err
}

// ─── Categories ──────────────────────────────────────────────

func (s *Store) ListCategories(ctx context.Context) ([]*ProductCategory, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id,parent_id,name,slug,description,display_order,is_active,is_featured,created_at
		FROM product_categories WHERE is_active=TRUE ORDER BY display_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cats []*ProductCategory
	for rows.Next() {
		var c ProductCategory
		if err := rows.Scan(&c.ID, &c.ParentID, &c.Name, &c.Slug, &c.Description,
			&c.DisplayOrder, &c.IsActive, &c.IsFeatured, &c.CreatedAt); err != nil {
			return nil, err
		}
		cats = append(cats, &c)
	}
	return cats, nil
}

// ─── Products ────────────────────────────────────────────────

func (s *Store) CreateProduct(ctx context.Context, p *Product) error {
	p.ID = uuid.New()
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO products (id,seller_id,category_id,brand_id,tax_class_id,title,short_title,slug,description,
		  short_description,brand_name,manufacturer_name,product_type,condition,sku_root,status,visibility,approval_status,
		  primary_image_media_id,video_media_id,weight_grams,length_cm,width_cm,height_cm,
		  country_of_origin,warranty_info,return_policy_type,return_policy_days,
		  hsn_code,search_keywords,meta_title,meta_description,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34)`,
		p.ID, p.SellerID, p.CategoryID, p.BrandID, p.TaxClassID, p.Title, p.ShortTitle, p.Slug, p.Description,
		p.ShortDescription, p.BrandName, p.ManufacturerName, p.ProductType, p.Condition, p.SKURoot, p.Status, p.Visibility, p.ApprovalStatus,
		p.PrimaryImageMediaID, p.VideoMediaID, p.WeightGrams, p.LengthCm, p.WidthCm, p.HeightCm,
		p.CountryOfOrigin, p.WarrantyInfo, p.ReturnPolicyType, p.ReturnPolicyDays,
		p.HSNCode, p.SearchKeywords, p.MetaTitle, p.MetaDescription, p.CreatedAt, p.UpdatedAt,
	)
	return err
}

func (s *Store) GetProductByID(ctx context.Context, id uuid.UUID) (*Product, error) {
	var p Product
	err := s.db.QueryRow(ctx, `
		SELECT id,seller_id,category_id,brand_id,tax_class_id,title,short_title,slug,description,
		  short_description,brand_name,manufacturer_name,product_type,condition,sku_root,status,visibility,approval_status,
		  rejection_reason,primary_image_media_id,video_media_id,weight_grams,length_cm,width_cm,height_cm,
		  country_of_origin,warranty_info,return_policy_type,return_policy_days,hsn_code,search_keywords,
		  meta_title,meta_description,
		  avg_rating,review_count,order_count,view_count,wishlist_count,is_featured,created_at,updated_at,published_at
		  ,(SELECT value FROM product_attributes WHERE product_id=products.id AND name='source_image_url' ORDER BY sort_order LIMIT 1)
		  ,(SELECT store_name FROM sellers WHERE id=products.seller_id)
		FROM products WHERE id=$1`, id).Scan(
		&p.ID, &p.SellerID, &p.CategoryID, &p.BrandID, &p.TaxClassID,
		&p.Title, &p.ShortTitle, &p.Slug, &p.Description, &p.ShortDescription,
		&p.BrandName, &p.ManufacturerName,
		&p.ProductType, &p.Condition, &p.SKURoot, &p.Status, &p.Visibility,
		&p.ApprovalStatus, &p.RejectionReason, &p.PrimaryImageMediaID, &p.VideoMediaID,
		&p.WeightGrams, &p.LengthCm, &p.WidthCm, &p.HeightCm,
		&p.CountryOfOrigin, &p.WarrantyInfo,
		&p.ReturnPolicyType, &p.ReturnPolicyDays, &p.HSNCode, &p.SearchKeywords,
		&p.MetaTitle, &p.MetaDescription, &p.AvgRating, &p.ReviewCount,
		&p.OrderCount, &p.ViewCount, &p.WishlistCount, &p.IsFeatured,
		&p.CreatedAt, &p.UpdatedAt, &p.PublishedAt, &p.SourceImageURL, &p.RetailerName,
	)
	return &p, err
}

// ─── Product Media + Attributes (Phase 3.1) ─────────────────

// AddProductMedia attaches an image / video / size-chart / infographic
// (already uploaded via media-service) to a product, ordering it among
// the product's gallery.
func (s *Store) AddProductMedia(ctx context.Context, productID, mediaID uuid.UUID, mediaType string, sortOrder int) error {
	if mediaType == "" {
		mediaType = "image"
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO product_media (product_id, media_id, media_type, sort_order)
		 VALUES ($1, $2, $3, $4)`,
		productID, mediaID, mediaType, sortOrder,
	)
	return err
}

// ListProductMedia returns the gallery for a product, ordered for display.
func (s *Store) ListProductMedia(ctx context.Context, productID uuid.UUID) ([]ProductMedia, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, product_id, media_id, media_type, sort_order, created_at
		 FROM product_media WHERE product_id=$1 ORDER BY sort_order ASC, created_at ASC`,
		productID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductMedia
	for rows.Next() {
		var m ProductMedia
		if err := rows.Scan(&m.ID, &m.ProductID, &m.MediaID, &m.MediaType, &m.SortOrder, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

// RemoveProductMedia deletes a media row by id. Caller verifies seller ownership.
func (s *Store) RemoveProductMedia(ctx context.Context, productMediaID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM product_media WHERE id=$1`, productMediaID)
	return err
}

// SetProductAttributes replaces the product's attribute list in one
// atomic UPDATE. The schema allows free-form name/value/unit triples for
// the structured spec block.
func (s *Store) SetProductAttributes(ctx context.Context, productID uuid.UUID, attrs []ProductAttribute) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM product_attributes WHERE product_id=$1`, productID); err != nil {
		return err
	}
	for i, a := range attrs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO product_attributes (product_id, name, value, unit, sort_order)
			 VALUES ($1, $2, $3, $4, $5)`,
			productID, a.Name, a.Value, a.Unit, i,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// GetProductAttributes returns the attribute rows for the product.
func (s *Store) GetProductAttributes(ctx context.Context, productID uuid.UUID) ([]ProductAttribute, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, product_id, name, value, unit, sort_order
		 FROM product_attributes WHERE product_id=$1 ORDER BY sort_order ASC`,
		productID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProductAttribute
	for rows.Next() {
		var a ProductAttribute
		if err := rows.Scan(&a.ID, &a.ProductID, &a.Name, &a.Value, &a.Unit, &a.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// ListSellerProducts lists one seller's products.
//
// publicOnly is the difference between a storefront and a dashboard, and it
// is not cosmetic. Without it this listed every row for a seller id supplied
// in the URL — including `draft` products the seller has not released and
// `rejected` ones moderation turned down. The route is unauthenticated, so
// anyone who knew a seller id could read a competitor's unreleased catalogue
// and their moderation failures.
//
// The public predicate is the same one the browse and search surfaces use
// (`status='active' AND approval_status='approved'`), so a product
// cannot be visible on a storefront while being invisible in search.
func (s *Store) ListSellerProducts(ctx context.Context, sellerID uuid.UUID, status string, publicOnly bool, limit, offset int) ([]*Product, int, error) {
	where := "WHERE seller_id=$1"
	args := []any{sellerID}
	if publicOnly {
		where += " AND status = 'active' AND approval_status = 'approved'"
	}
	if status != "" {
		where += fmt.Sprintf(" AND status=$%d", len(args)+1)
		args = append(args, status)
	}
	var total int
	_ = s.db.QueryRow(ctx, "SELECT COUNT(*) FROM products "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	// primary_image_media_id is selected, and that is not cosmetic.
	//
	// It was absent from this list while the two browse queries below both
	// select it, so a storefront or seller-dashboard row came back with a nil
	// image id no matter what the row held. Hydrating image URLs on the way
	// out cannot help a field the query never returned — the product would
	// still render as a placeholder, and the cause would look like a
	// media-service problem rather than a missing column.
	rows, err := s.db.Query(ctx, `SELECT id,seller_id,category_id,title,slug,status,approval_status,
		avg_rating,review_count,order_count,view_count,created_at,updated_at,
		primary_image_media_id FROM products `+
		where+fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var products []*Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.Title, &p.Slug,
			&p.Status, &p.ApprovalStatus, &p.AvgRating, &p.ReviewCount, &p.OrderCount,
			&p.ViewCount, &p.CreatedAt, &p.UpdatedAt, &p.PrimaryImageMediaID); err != nil {
			return nil, 0, err
		}
		products = append(products, &p)
	}
	return products, total, nil
}

// ProductFilter is the rich filter set the customer-facing browse
// surface uses. All fields are optional. Cursor takes precedence over
// Offset: pass `cursor` for keyset pagination (recommended at scale —
// O(1) regardless of page depth) or `offset` for the legacy
// admin-style offset pagination. Cursor format is `<unix_micros>:<id>`
// matching the (created_at DESC, id DESC) sort order.
type ProductFilter struct {
	CategoryID *uuid.UUID
	Query      string
	// Price filter (selling_price range, inclusive). Either or both may
	// be zero to skip that side.
	MinPrice float64
	MaxPrice float64
	// Minimum average rating, 1-5; 0 = no filter.
	MinRating float64
	// Restrict to one seller (used by /seller/:id storefronts).
	SellerID *uuid.UUID
	// In-stock filter: when true we require total_stock > 0 across
	// the product's active variants.
	InStockOnly bool
	Limit       int
	// Either Cursor (recommended) or Offset is used; never both.
	Cursor string
	Offset int
}

// productSearchCondition keeps the customer catalog's search behavior
// consistent across cursor and offset pagination. All values remain bound
// parameters; the generated SQL contains no user-provided text.
func productSearchCondition(param int) string {
	placeholder := fmt.Sprintf("$%d", param)
	return `(
		p.title ILIKE ` + placeholder + ` OR
		COALESCE(p.short_title, '') ILIKE ` + placeholder + ` OR
		COALESCE(p.description, '') ILIKE ` + placeholder + ` OR
		COALESCE(p.short_description, '') ILIKE ` + placeholder + ` OR
		COALESCE(p.brand_name, '') ILIKE ` + placeholder + ` OR
		COALESCE(array_to_string(p.search_keywords, ' '), '') ILIKE ` + placeholder + ` OR
		EXISTS (SELECT 1 FROM product_categories pc WHERE pc.id=p.category_id AND (pc.name ILIKE ` + placeholder + ` OR pc.slug ILIKE ` + placeholder + `)) OR
		EXISTS (SELECT 1 FROM sellers ss WHERE ss.id=p.seller_id AND (ss.store_name ILIKE ` + placeholder + ` OR COALESCE(ss.brand_name, '') ILIKE ` + placeholder + `)) OR
		EXISTS (SELECT 1 FROM product_variants psv WHERE psv.product_id=p.id AND (psv.sku ILIKE ` + placeholder + ` OR COALESCE(psv.barcode, '') ILIKE ` + placeholder + `))
	)`
}

// ListProductsFiltered is the scale-friendly variant. Cursor pagination
// is the default; offset is supported only as a fallback for legacy
// callers (admin grid). Returns the products + a `nextCursor` the
// client should pass to keep paging; empty nextCursor means end of
// list.
func (s *Store) ListProductsFiltered(ctx context.Context, f ProductFilter) ([]*Product, string, error) {
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	conds := []string{"p.status = 'active'", "p.approval_status = 'approved'"}
	args := []any{}
	idx := 1
	if f.CategoryID != nil {
		conds = append(conds, fmt.Sprintf("p.category_id = $%d", idx))
		args = append(args, *f.CategoryID)
		idx++
	}
	if f.SellerID != nil {
		conds = append(conds, fmt.Sprintf("p.seller_id = $%d", idx))
		args = append(args, *f.SellerID)
		idx++
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		conds = append(conds, productSearchCondition(idx))
		args = append(args, "%"+q+"%")
		idx++
	}
	if f.MinRating > 0 {
		conds = append(conds, fmt.Sprintf("p.avg_rating >= $%d", idx))
		args = append(args, f.MinRating)
		idx++
	}
	// Keyset cursor: rows are sorted (created_at DESC, id DESC) so a
	// cursor of `(t,c)` means "give me rows older than (t, c)".
	// Format: "<unix_micros>:<id>". Falls back to offset when not
	// supplied.
	if f.Cursor != "" {
		cParts := strings.SplitN(f.Cursor, ":", 2)
		if len(cParts) == 2 {
			tsMicros, err := strconv.ParseInt(cParts[0], 10, 64)
			if err == nil {
				cursorID, err2 := uuid.Parse(cParts[1])
				if err2 == nil {
					ts := time.UnixMicro(tsMicros).UTC()
					conds = append(conds, fmt.Sprintf("(p.created_at, p.id) < ($%d, $%d)", idx, idx+1))
					args = append(args, ts, cursorID)
					idx += 2
				}
			}
		}
	}
	where := "WHERE " + strings.Join(conds, " AND ")

	// Price + in-stock filters apply on the LATERAL-derived columns,
	// so they go into the outer WHERE via HAVING-equivalent (we use a
	// SELECT-from-subquery to keep the SQL portable). To stay simple,
	// embed them in the outer WHERE using the LATERAL output cols.
	priceFilter := ""
	if f.MinPrice > 0 || f.MaxPrice > 0 || f.InStockOnly {
		var clauses []string
		if f.MinPrice > 0 {
			clauses = append(clauses, fmt.Sprintf("v.min_selling_price >= $%d", idx))
			args = append(args, f.MinPrice)
			idx++
		}
		if f.MaxPrice > 0 {
			clauses = append(clauses, fmt.Sprintf("v.min_selling_price <= $%d", idx))
			args = append(args, f.MaxPrice)
			idx++
		}
		if f.InStockOnly {
			clauses = append(clauses, "COALESCE(s.total_stock, 0) > 0")
		}
		priceFilter = " AND " + strings.Join(clauses, " AND ")
	}

	args = append(args, limit+1) // +1 to peek whether there is a next page
	query := `
		SELECT p.id, p.seller_id, p.category_id, p.title, p.slug, p.status, p.approval_status,
		       p.avg_rating, p.review_count, p.order_count, p.view_count, p.created_at, p.updated_at,
		       p.primary_image_media_id,
		       (SELECT value FROM product_attributes WHERE product_id=p.id AND name='source_image_url' ORDER BY sort_order LIMIT 1),
		       sl.store_name,
		       v.id  AS default_variant_id,
		       v.min_selling_price,
		       v.min_mrp,
		       v.min_price_minor,
		       v.mrp_minor,
		       COALESCE(s.total_stock, 0) AS total_stock,
		       (COALESCE(s.total_stock, 0) > 0) AS in_stock
		FROM products p
		JOIN sellers sl ON sl.id = p.seller_id
		LEFT JOIN LATERAL (
			-- Paise alongside the rupee floats, from the same row, using the
			-- same NULLIF fallback pricing uses: a legacy variant whose minor
			-- column is still the DEFAULT 0 falls back to its float rather
			-- than advertising a free product.
			SELECT id, selling_price AS min_selling_price, mrp AS min_mrp,
			       COALESCE(NULLIF(selling_price_minor, 0), ROUND(selling_price*100))::bigint AS min_price_minor,
			       COALESCE(NULLIF(mrp_minor, 0), ROUND(mrp*100))::bigint AS mrp_minor
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
		) s ON true
		` + where + priceFilter + fmt.Sprintf(`
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $%d`, idx)
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var products []*Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.Title, &p.Slug,
			&p.Status, &p.ApprovalStatus, &p.AvgRating, &p.ReviewCount, &p.OrderCount,
			&p.ViewCount, &p.CreatedAt, &p.UpdatedAt,
			&p.PrimaryImageMediaID, &p.SourceImageURL, &p.RetailerName,
			&p.DefaultVariantID, &p.MinSellingPrice, &p.MinMRP,
			&p.MinPriceMinor, &p.MRPMinor, &p.TotalStock, &p.InStock); err != nil {
			return nil, "", err
		}
		products = append(products, &p)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	// Use the +1 peek to derive nextCursor.
	var nextCursor string
	if len(products) > limit {
		last := products[limit-1]
		nextCursor = fmt.Sprintf("%d:%s", last.CreatedAt.UnixMicro(), last.ID.String())
		products = products[:limit]
	}
	return products, nextCursor, nil
}

// ListProducts returns paginated products for the customer-facing browse
// surface: active + approved only, optionally filtered by category and a
// title search. Newest first. Returns total count for pagination.
//
// status values per the products_status_check constraint: draft, active,
// paused, archived. approval_status: draft, submitted, under_review,
// approved, rejected, hidden, archived. We surface active+approved.
func (s *Store) ListProducts(ctx context.Context, categoryID *uuid.UUID, query string, limit, offset int) ([]*Product, int, error) {
	conds := []string{"p.status = 'active'", "p.approval_status = 'approved'"}
	args := []any{}
	idx := 1
	if categoryID != nil {
		conds = append(conds, fmt.Sprintf("p.category_id = $%d", idx))
		args = append(args, *categoryID)
		idx++
	}
	if q := strings.TrimSpace(query); q != "" {
		conds = append(conds, productSearchCondition(idx))
		args = append(args, "%"+q+"%")
		idx++
	}
	where := "WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM products p "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	// Phase F1 — enrich each row with variant pricing + stock so the
	// catalog grid renders without N+1 detail fetches. LATERAL picks
	// the cheapest active variant as the card's "from price"; mobile
	// uses default_variant_id to add to cart in one click.
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.seller_id, p.category_id, p.title, p.slug, p.status, p.approval_status,
		       p.avg_rating, p.review_count, p.order_count, p.view_count, p.created_at, p.updated_at,
		       p.primary_image_media_id,
		       (SELECT value FROM product_attributes WHERE product_id=p.id AND name='source_image_url' ORDER BY sort_order LIMIT 1),
		       sl.store_name,
		       v.id  AS default_variant_id,
		       v.min_selling_price,
		       v.min_mrp,
		       COALESCE(s.total_stock, 0) AS total_stock
		FROM products p
		JOIN sellers sl ON sl.id = p.seller_id
		LEFT JOIN LATERAL (
			SELECT id, selling_price AS min_selling_price, mrp AS min_mrp
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
		) s ON true
		`+where+fmt.Sprintf(`
		ORDER BY p.created_at DESC
		LIMIT $%d OFFSET $%d`, idx, idx+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var products []*Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(&p.ID, &p.SellerID, &p.CategoryID, &p.Title, &p.Slug,
			&p.Status, &p.ApprovalStatus, &p.AvgRating, &p.ReviewCount, &p.OrderCount,
			&p.ViewCount, &p.CreatedAt, &p.UpdatedAt,
			&p.PrimaryImageMediaID, &p.SourceImageURL, &p.RetailerName,
			&p.DefaultVariantID, &p.MinSellingPrice, &p.MinMRP, &p.TotalStock); err != nil {
			return nil, 0, err
		}
		products = append(products, &p)
	}
	return products, total, rows.Err()
}

func (s *Store) UpdateProduct(ctx context.Context, id uuid.UUID, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	sets := make([]string, 0, len(updates))
	args := make([]any, 0, len(updates)+1)
	i := 1
	for k, v := range updates {
		sets = append(sets, fmt.Sprintf("%s=$%d", k, i))
		args = append(args, v)
		i++
	}
	args = append(args, id)
	_, err := s.db.Exec(ctx,
		"UPDATE products SET "+strings.Join(sets, ",")+",updated_at=NOW() WHERE id=$"+fmt.Sprint(i),
		args...,
	)
	return err
}

// IncrProductViewCount is the Redis-nil fallback for the sharded
// product-view counter. Production traffic flows through
// Service.adjustProductViewCount → counters.Counter → flush-worker
// SetProductViewCount.
func (s *Store) IncrProductViewCount(ctx context.Context, id uuid.UUID) {
	_, _ = s.db.Exec(ctx, "UPDATE products SET view_count=view_count+1 WHERE id=$1", id)
}

// SetProductViewCount overwrites products.view_count to the absolute
// sum from the sharded Redis counter. Called by the flush worker
// every ~10s per dirty product.
func (s *Store) SetProductViewCount(ctx context.Context, id uuid.UUID, total int64) error {
	_, err := s.db.Exec(ctx, "UPDATE products SET view_count=$2 WHERE id=$1", id, total)
	return err
}

// ─── Product Variants ────────────────────────────────────────

func (s *Store) CreateVariant(ctx context.Context, v *ProductVariant) error {
	v.ID = uuid.New()
	v.CreatedAt = time.Now()
	v.UpdatedAt = time.Now()
	// THE minor columns are written here, and they are not optional.
	//
	// Every pricing path reads `COALESCE(selling_price_minor, ROUND(selling_price*100))`.
	// Migration 007 backfilled the existing estate and then set these columns
	// to DEFAULT 0 — not NULL. So a variant inserted WITHOUT them got
	// `selling_price_minor = 0`, COALESCE found a non-NULL zero, the float
	// fallback never ran, and checkout charged nothing.
	//
	// This was the only code path that creates products. A seller entering
	// ₹1,299 produced a variant the buyer could take for free, and no test
	// caught it because every fixture in the suite inserts the minor columns
	// explicitly — supplying exactly the field production dropped.
	//
	// Rupee floats are converted once, here, at the boundary. Below this line
	// the money is integer paise and stays that way.
	_, err := s.db.Exec(ctx, `
		INSERT INTO product_variants (id,product_id,sku,barcode,option_1_name,option_1_value,
		  option_2_name,option_2_value,option_3_name,option_3_value,mrp,selling_price,cost_price,
		  mrp_minor,selling_price_minor,cost_price_minor,
		  currency_code,status,image_media_id,weight_grams,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`,
		v.ID, v.ProductID, v.SKU, v.Barcode, v.Option1Name, v.Option1Value,
		v.Option2Name, v.Option2Value, v.Option3Name, v.Option3Value,
		v.MRP, v.SellingPrice, v.CostPrice,
		// Paise from the caller when they have it, converted rupees when they
		// do not. The direction matters: a client that typed a price in paise
		// must not have it round-tripped through a float on the way in, which
		// is the whole reason the create route now accepts `*_minor`.
		orConvert(v.MRPMinorIn, v.MRP),
		orConvert(v.SellingPriceMinorIn, v.SellingPrice),
		orConvertPtr(v.CostPriceMinorIn, v.CostPrice),
		v.CurrencyCode, v.Status,
		v.ImageMediaID, v.WeightGrams, v.CreatedAt, v.UpdatedAt,
	)
	return err
}

func (s *Store) GetVariantByID(ctx context.Context, id uuid.UUID) (*ProductVariant, error) {
	var v ProductVariant
	err := s.db.QueryRow(ctx, `SELECT id,product_id,sku,barcode,option_1_name,option_1_value,
		option_2_name,option_2_value,option_3_name,option_3_value,mrp,selling_price,cost_price,
		currency_code,status,image_media_id,weight_grams,created_at,updated_at
		FROM product_variants WHERE id=$1`, id).Scan(
		&v.ID, &v.ProductID, &v.SKU, &v.Barcode, &v.Option1Name, &v.Option1Value,
		&v.Option2Name, &v.Option2Value, &v.Option3Name, &v.Option3Value,
		&v.MRP, &v.SellingPrice, &v.CostPrice, &v.CurrencyCode, &v.Status,
		&v.ImageMediaID, &v.WeightGrams, &v.CreatedAt, &v.UpdatedAt,
	)
	return &v, err
}

func (s *Store) GetVariantsByProduct(ctx context.Context, productID uuid.UUID) ([]*ProductVariant, error) {
	rows, err := s.db.Query(ctx, `SELECT id,product_id,sku,option_1_name,option_1_value,
		option_2_name,option_2_value,mrp,selling_price,currency_code,status,image_media_id,created_at
		FROM product_variants WHERE product_id=$1 AND status='active' ORDER BY created_at`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var variants []*ProductVariant
	for rows.Next() {
		var v ProductVariant
		if err := rows.Scan(&v.ID, &v.ProductID, &v.SKU, &v.Option1Name, &v.Option1Value,
			&v.Option2Name, &v.Option2Value, &v.MRP, &v.SellingPrice, &v.CurrencyCode,
			&v.Status, &v.ImageMediaID, &v.CreatedAt); err != nil {
			return nil, err
		}
		variants = append(variants, &v)
	}
	return variants, nil
}

// UpdateVariant patches the mutable fields of an existing variant.
// Returns ErrProductNotFound when the variant doesn't exist. The
// product_id + sku are intentionally NOT updatable (sku is used as the
// merge key for bulk import; product_id is a foreign key that defines
// what the variant belongs to — moving it would break orders + carts).
func (s *Store) UpdateVariant(ctx context.Context, id uuid.UUID, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	allowed := map[string]bool{
		"option_1_name": true, "option_1_value": true,
		"option_2_name": true, "option_2_value": true,
		"option_3_name": true, "option_3_value": true,
		"mrp": true, "selling_price": true, "cost_price": true,
		"currency_code": true, "status": true, "image_media_id": true,
		"weight_grams": true, "barcode": true,
	}

	// Repricing must move the column pricing actually READS, and the two
	// columns must never end up disagreeing.
	//
	// The original defect: a seller lowering a price updated `selling_price`
	// while `selling_price_minor` kept its old value — and since checkout
	// reads the minor column, the buyer was charged the price before the
	// change. The first fix derived the minor column from the rupee one, so
	// they could not diverge.
	//
	// That is still the rule; only the DIRECTION has changed. Paise are now
	// the authority on the way in, because the create route accepts them and a
	// price entered exactly as 129999 must not become a float on its way
	// through an edit. `normaliseVariantMoney` resolves each pair before
	// anything is written: whichever side the caller supplied becomes both.
	money, err := normaliseVariantMoney(updates)
	if err != nil {
		return err
	}

	setClauses := []string{}
	args := []any{}
	idx := 1
	for k, v := range updates {
		if !allowed[k] || variantMoneyPairs[k] != "" {
			// Money is written from `money` below, never straight from the
			// caller's map, so a rupee field cannot slip past the pairing.
			continue
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", k, idx))
		args = append(args, v)
		idx++
	}
	for _, m := range money {
		setClauses = append(setClauses,
			fmt.Sprintf("%s = $%d", m.rupeeCol, idx),
			fmt.Sprintf("%s = $%d", m.minorCol, idx+1))
		args = append(args, m.rupees, m.minor)
		idx += 2
	}
	if len(setClauses) == 0 {
		return nil
	}
	setClauses = append(setClauses, "updated_at = NOW()")
	args = append(args, id)
	q := "UPDATE product_variants SET " + strings.Join(setClauses, ", ") +
		" WHERE id = $" + strconv.Itoa(idx)
	cmd, err := s.db.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrProductNotFound
	}
	return nil
}

// ArchiveVariant flips a variant to status='archived' so it's hidden
// from browse + cart but kept on existing orders for history.
// Deleting variants is intentionally not supported — referential
// integrity from orders/cart_items would break.
func (s *Store) ArchiveVariant(ctx context.Context, id uuid.UUID) error {
	cmd, err := s.db.Exec(ctx, `
		UPDATE product_variants SET status='archived', updated_at=NOW()
		WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrProductNotFound
	}
	return nil
}

// ─── Inventory ───────────────────────────────────────────────

func (s *Store) UpsertInventory(ctx context.Context, variantID, sellerID uuid.UUID, totalQty int) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO inventory_items (id,variant_id,seller_id,total_qty,updated_at)
		VALUES (gen_random_uuid(),$1,$2,$3,NOW())
		ON CONFLICT (variant_id) DO UPDATE SET total_qty=$3, updated_at=NOW()`,
		variantID, sellerID, totalQty,
	)
	return err
}

func (s *Store) GetInventory(ctx context.Context, variantID uuid.UUID) (*InventoryItem, error) {
	var inv InventoryItem
	err := s.db.QueryRow(ctx, `SELECT id,variant_id,seller_id,total_qty,reserved_qty,damaged_qty,
		returned_qty,safety_stock,low_stock_alert,updated_at
		FROM inventory_items WHERE variant_id=$1`, variantID).Scan(
		&inv.ID, &inv.VariantID, &inv.SellerID, &inv.TotalQty, &inv.ReservedQty,
		&inv.DamagedQty, &inv.ReturnedQty, &inv.SafetyStock, &inv.LowStockAlert, &inv.UpdatedAt,
	)
	return &inv, err
}

// ReserveStock atomically reserves qty for a cart/order. Returns error if insufficient.
func (s *Store) ReserveStock(ctx context.Context, variantID, userID uuid.UUID, qty int, orderID *uuid.UUID, resType string, ttl time.Duration) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock row and check availability
	var avail int
	err = tx.QueryRow(ctx, `
		SELECT total_qty - reserved_qty FROM inventory_items WHERE variant_id=$1 FOR UPDATE`,
		variantID).Scan(&avail)
	if err != nil {
		return fmt.Errorf("inventory not found: %w", err)
	}
	if avail < qty {
		return fmt.Errorf("insufficient stock: available=%d requested=%d", avail, qty)
	}

	// Increment reserved_qty
	if _, err = tx.Exec(ctx, `UPDATE inventory_items SET reserved_qty=reserved_qty+$2,updated_at=NOW() WHERE variant_id=$1`, variantID, qty); err != nil {
		return err
	}

	// Create reservation record
	if _, err = tx.Exec(ctx, `
		INSERT INTO inventory_reservations (id,variant_id,order_id,user_id,quantity,type,expires_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6)`,
		variantID, orderID, userID, qty, resType, time.Now().Add(ttl)); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ReleaseReservation releases a cart hold — one reservation not attached to an
// order — and rolls back exactly the quantity that reservation recorded.
//
// ─── WHAT THIS USED TO BE ───────────────────────────────────────────────
//
// It was broken twice over, and compiled, and read as correct:
//
//	UPDATE inventory_items SET reserved_qty = GREATEST(0, reserved_qty - $2) ...
//	DELETE FROM inventory_reservations
//	 WHERE variant_id=$1 AND user_id=$2 AND order_id IS NULL LIMIT 1
//
// First, PostgreSQL does not accept LIMIT on DELETE. The statement raised a
// syntax error on every call, the transaction rolled back, and the release was
// a no-op — while its only caller logged the failure at Warn and moved on.
//
// Second, and worse if the syntax error were ever fixed naively: the caller
// passes a quantity, and the DELETE targets `order_id IS NULL`. Checkout
// creates reservations WITH an order id. So a "release this order's hold" call
// would decrement reserved_qty by the order's quantity while deleting an
// unrelated cart reservation — corrupting a live hold that belonged to
// somebody else's session.
//
// It is currently unreachable — `MarkPaymentFailed` is its only caller and
// cmd/server wires the P0 consumer, which uses ApplyPaymentFailed instead. It
// is repaired rather than deleted because the next person to need a
// cancel-cart-hold path will find this function and use it.
//
// ─── WHAT IT IS NOW ─────────────────────────────────────────────────────
//
// The reservation row is the authority for how much to give back, not the
// caller. One statement selects the row, deletes it and returns its quantity;
// reserved_qty is then decremented by exactly that. A caller who passes a
// stale quantity can no longer corrupt the count, and a release with nothing
// to release changes nothing instead of silently subtracting.
//
// Order-attached reservations are explicitly out of scope: releasing those
// belongs to ApplyPaymentFailed / CancelOrder / ExpireInventoryReservations,
// which do it inside the order's own state transition and write the ledger.
func (s *Store) ReleaseReservation(ctx context.Context, variantID, userID uuid.UUID, _ int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Delete exactly one cart hold and learn what it was actually holding.
	// The ctid subselect is how a single row is targeted without LIMIT.
	var released int
	err = tx.QueryRow(ctx, `
		DELETE FROM inventory_reservations
		 WHERE ctid = (
		     SELECT ctid FROM inventory_reservations
		      WHERE variant_id = $1 AND user_id = $2 AND order_id IS NULL
		      ORDER BY created_at
		      LIMIT 1
		 )
		RETURNING quantity`, variantID, userID).Scan(&released)
	if errors.Is(err, pgx.ErrNoRows) {
		// Nothing held. Idempotent by construction: a second release finds no
		// row and subtracts nothing, where the previous version would have
		// subtracted the caller's quantity a second time.
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}

	if _, err = tx.Exec(ctx, `
		UPDATE inventory_items
		   SET reserved_qty = GREATEST(0, reserved_qty - $2), updated_at = NOW()
		 WHERE variant_id = $1`, variantID, released); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO inventory_ledger (variant_id, delta_total, delta_reserved, reason, actor_id, actor_type)
		VALUES ($1, 0, $2, 'checkout_release_cancel', $3, 'customer')`,
		variantID, -released, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// DeductStock commits stock after successful payment (releases reservation, deducts from total).
func (s *Store) DeductStock(ctx context.Context, variantID uuid.UUID, qty int, orderID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err = tx.Exec(ctx, `
		UPDATE inventory_items
		SET total_qty=GREATEST(0,total_qty-$2),
		    reserved_qty=GREATEST(0,reserved_qty-$2),
		    updated_at=NOW()
		WHERE variant_id=$1`, variantID, qty); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `
		DELETE FROM inventory_reservations WHERE variant_id=$1 AND order_id=$2`,
		variantID, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Cart ────────────────────────────────────────────────────

func (s *Store) GetOrCreateCart(ctx context.Context, userID uuid.UUID) (*Cart, error) {
	cart := &Cart{}
	err := s.db.QueryRow(ctx, `SELECT id,user_id,expires_at,updated_at FROM carts WHERE user_id=$1`, userID).
		Scan(&cart.ID, &cart.UserID, &cart.ExpiresAt, &cart.UpdatedAt)
	if err != nil {
		// Create new cart
		cart.ID = uuid.New()
		cart.UserID = userID
		cart.UpdatedAt = time.Now()
		_, err = s.db.Exec(ctx, `INSERT INTO carts (id,user_id,updated_at) VALUES ($1,$2,$3)`,
			cart.ID, cart.UserID, cart.UpdatedAt)
		return cart, err
	}
	return cart, nil
}

// VariantSellingPriceMinor reads a variant's price in paise.
//
// B5/LB-19. It uses the SAME expression lockAndPriceLines uses inside the
// checkout transaction — `COALESCE(selling_price_minor, ROUND(selling_price*100))`
// — so the snapshot written at add-to-cart is comparable to the price charged
// at checkout. Deriving it any other way is how a price-change check ends up
// comparing two numbers that were never the same kind of thing.
func (s *Store) VariantSellingPriceMinor(ctx context.Context, variantID uuid.UUID) (money.Paise, error) {
	var minor int64
	err := s.db.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(selling_price_minor, 0), ROUND(selling_price*100))
		   FROM product_variants WHERE id = $1`, variantID).Scan(&minor)
	if err != nil {
		return 0, err
	}
	return money.Paise(minor), nil
}

// UpsertCartItem adds or updates a cart line.
//
// B5/LB-19 — this took a rupees-major float and wrote ONLY `price_snapshot`,
// never `price_snapshot_minor`. It is reachable from the live AddToCart and
// UpdateCartItem routes, so it is not legacy dead code, and the consequence
// was quiet: the P0 checkout reads `price_snapshot_minor` to detect a price
// change between add-to-cart and checkout, and
//
//	if l.SnapshotMinor != 0 && l.SnapshotMinor != priced[i].UnitMinor
//
// skips that check entirely when the column is 0. Every line added through
// this path therefore had NO price-change detection: a seller could raise the
// price after the customer added the item and the customer would be charged
// the new price without the ErrPriceChanged response the flow exists to
// produce.
//
// The parameter is paise now. The float column is still written, from the
// integer, because analytics readers still scan it during the deprecation
// window — but it is derived from the minor value rather than being the
// source of it.
func (s *Store) UpsertCartItem(ctx context.Context, cartID, variantID, productID uuid.UUID, qty int, priceMinor money.Paise) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO cart_items (id,cart_id,variant_id,product_id,quantity,price_snapshot,price_snapshot_minor,added_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5::numeric/100,$5,NOW())
		ON CONFLICT (cart_id,variant_id) DO UPDATE
		   SET quantity = $4,
		       price_snapshot = $5::numeric/100,
		       price_snapshot_minor = $5`,
		cartID, variantID, productID, qty, priceMinor.Int64(),
	)
	return err
}

func (s *Store) RemoveCartItem(ctx context.Context, cartID, variantID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM cart_items WHERE cart_id=$1 AND variant_id=$2`, cartID, variantID)
	return err
}

func (s *Store) GetCartItems(ctx context.Context, cartID uuid.UUID) ([]*CartItem, error) {
	rows, err := s.db.Query(ctx, `SELECT id,cart_id,variant_id,product_id,quantity,price_snapshot,added_at
		FROM cart_items WHERE cart_id=$1 ORDER BY added_at`, cartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*CartItem
	for rows.Next() {
		var ci CartItem
		if err := rows.Scan(&ci.ID, &ci.CartID, &ci.VariantID, &ci.ProductID, &ci.Quantity, &ci.PriceSnapshot, &ci.AddedAt); err != nil {
			return nil, err
		}
		items = append(items, &ci)
	}
	return items, nil
}

func (s *Store) ClearCart(ctx context.Context, cartID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM cart_items WHERE cart_id=$1`, cartID)
	return err
}

// ─── Orders ──────────────────────────────────────────────────

func (s *Store) CreateOrder(ctx context.Context, o *Order, items []*OrderItem) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	o.ID = uuid.New()
	o.CreatedAt = time.Now()
	o.UpdatedAt = time.Now()

	// Generate human-readable order number
	var orderNum string
	if err = tx.QueryRow(ctx, `SELECT generate_order_number()`).Scan(&orderNum); err != nil {
		return fmt.Errorf("generate order number: %w", err)
	}
	o.OrderNumber = orderNum

	addrSnapshot, _ := json.Marshal(o.DeliveryAddressSnapshot)

	if _, err = tx.Exec(ctx, `
		INSERT INTO orders (id,customer_user_id,order_number,subtotal,discount_amount,shipping_charges,
		  tax_amount,coupon_code,coupon_discount,final_amount,currency_code,payment_method,payment_status,
		  delivery_address_id,delivery_address_snapshot,gift_message,status,idempotency_key,created_at,updated_at,
		  organization_id,po_number,cost_center,billing_address_snapshot,invoice_email,
		  approval_status,credit_terms_days,payment_due_date)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
		        $21,$22,$23,$24,$25,$26,$27,$28)`,
		o.ID, o.CustomerUserID, o.OrderNumber, o.Subtotal, o.DiscountAmount, o.ShippingCharges,
		o.TaxAmount, o.CouponCode, o.CouponDiscount, o.FinalAmount, o.CurrencyCode,
		o.PaymentMethod, o.PaymentStatus, o.DeliveryAddressID, addrSnapshot, o.GiftMessage,
		o.Status, o.IdempotencyKey, o.CreatedAt, o.UpdatedAt,
		o.OrganizationID, o.PONumber, o.CostCenter, o.BillingAddressSnapshot, o.InvoiceEmail,
		o.ApprovalStatus, o.CreditTermsDays, o.PaymentDueDate,
	); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}

	for _, item := range items {
		item.ID = uuid.New()
		item.OrderID = o.ID
		item.CreatedAt = time.Now()
		varDetails, _ := json.Marshal(item.VariantDetails)
		if _, err = tx.Exec(ctx, `
			INSERT INTO order_items (id,order_id,product_id,variant_id,seller_id,product_title,
			  variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,tax_amount,
			  final_price,status,return_eligible_until,created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
			item.ID, item.OrderID, item.ProductID, item.VariantID, item.SellerID,
			item.ProductTitle, varDetails, item.SKU, item.Quantity,
			item.UnitMRP, item.UnitPrice, item.DiscountAmount, item.TaxAmount,
			item.FinalPrice, item.Status, item.ReturnEligibleUntil, item.CreatedAt,
		); err != nil {
			return fmt.Errorf("insert order item: %w", err)
		}
	}

	// Record initial status
	if _, err = tx.Exec(ctx, `
		INSERT INTO order_status_history (id,order_id,to_status,actor_type,created_at)
		VALUES (gen_random_uuid(),$1,$2,'system',NOW())`, o.ID, o.Status,
	); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetOrderByIdempotencyKey returns the order (if any) created by `userID`
// against `key`. Underlies H4 — Checkout uses this to short-circuit a
// retried/double-tap checkout into the original order. Returns (nil, nil)
// when no row matches.
func (s *Store) GetOrderByIdempotencyKey(ctx context.Context, userID uuid.UUID, key string) (*Order, error) {
	var o Order
	err := s.db.QueryRow(ctx, `SELECT id,customer_user_id,order_number,subtotal,discount_amount,
		shipping_charges,tax_amount,coupon_code,coupon_discount,final_amount,currency_code,
		payment_method,payment_status,payment_id,payment_gateway,delivery_address_id,
		delivery_address_snapshot,gift_message,status,cancellation_reason,cancelled_by,
		idempotency_key,created_at,updated_at,
		organization_id,po_number,cost_center,billing_address_snapshot,invoice_email,
		approval_status,approved_by_user_id,approved_at,approval_notes,credit_terms_days,payment_due_date
		FROM orders WHERE customer_user_id=$1 AND idempotency_key=$2`, userID, key).Scan(
		&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.Subtotal, &o.DiscountAmount,
		&o.ShippingCharges, &o.TaxAmount, &o.CouponCode, &o.CouponDiscount, &o.FinalAmount,
		&o.CurrencyCode, &o.PaymentMethod, &o.PaymentStatus, &o.PaymentID, &o.PaymentGateway,
		&o.DeliveryAddressID, &o.DeliveryAddressSnapshot, &o.GiftMessage, &o.Status,
		&o.CancellationReason, &o.CancelledBy, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt,
		&o.OrganizationID, &o.PONumber, &o.CostCenter, &o.BillingAddressSnapshot, &o.InvoiceEmail,
		&o.ApprovalStatus, &o.ApprovedByUserID, &o.ApprovedAt, &o.ApprovalNotes,
		&o.CreditTermsDays, &o.PaymentDueDate,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (s *Store) GetOrderByID(ctx context.Context, id uuid.UUID) (*Order, error) {
	var o Order
	err := s.db.QueryRow(ctx, `SELECT id,customer_user_id,order_number,subtotal,discount_amount,
		shipping_charges,tax_amount,coupon_code,coupon_discount,final_amount,currency_code,
		payment_method,payment_status,payment_id,payment_gateway,delivery_address_id,
		delivery_address_snapshot,gift_message,status,cancellation_reason,cancelled_by,
		idempotency_key,created_at,updated_at,
		organization_id,po_number,cost_center,billing_address_snapshot,invoice_email,
		approval_status,approved_by_user_id,approved_at,approval_notes,credit_terms_days,payment_due_date
		FROM orders WHERE id=$1`, id).Scan(
		&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.Subtotal, &o.DiscountAmount,
		&o.ShippingCharges, &o.TaxAmount, &o.CouponCode, &o.CouponDiscount, &o.FinalAmount,
		&o.CurrencyCode, &o.PaymentMethod, &o.PaymentStatus, &o.PaymentID, &o.PaymentGateway,
		&o.DeliveryAddressID, &o.DeliveryAddressSnapshot, &o.GiftMessage, &o.Status,
		&o.CancellationReason, &o.CancelledBy, &o.IdempotencyKey, &o.CreatedAt, &o.UpdatedAt,
		&o.OrganizationID, &o.PONumber, &o.CostCenter, &o.BillingAddressSnapshot, &o.InvoiceEmail,
		&o.ApprovalStatus, &o.ApprovedByUserID, &o.ApprovedAt, &o.ApprovalNotes,
		&o.CreditTermsDays, &o.PaymentDueDate,
	)
	return &o, err
}

// OrderCard is the customer order-list row — Phase 2.1. Adds item +
// seller counts and the first item's product so the customer can tell
// orders apart without opening every one. Aggregates come from a single
// LATERAL subquery so the list query is O(page-size) instead of N+1.
// OrderCard is one row of the buyer's order list.
//
// ─── WHY THE MONEY IS IN MINOR UNITS ────────────────────────────────────
//
// This carried `final_amount`, a float64 read from `orders.final_amount`.
// Migration 007 made the minor-unit columns authoritative and stopped
// maintaining the rupee ones, so `final_amount` is 0.00 on every order the
// P0 checkout has ever written — the list rendered every order as ₹0 while
// `final_amount_minor` held the real total.
//
// The client was already right: Android's OrderDto reads `total_minor` and
// friends as Paise and never referenced `final_amount`. This is the server
// catching up to the contract the rest of P0 uses, not a new one.
type OrderCard struct {
	ID          uuid.UUID `json:"id"`
	OrderNumber string    `json:"order_number"`

	SubtotalMinor money.Paise `json:"subtotal_minor"`
	DiscountMinor money.Paise `json:"discount_minor"`
	ShippingMinor money.Paise `json:"shipping_minor"`
	TaxMinor      money.Paise `json:"tax_minor"`
	TotalMinor    money.Paise `json:"total_minor"`
	Currency      string      `json:"currency"`

	PaymentMethod     *string    `json:"payment_method,omitempty"`
	PaymentStatus     string     `json:"payment_status"`
	Status            string     `json:"status"`
	ItemCount         int        `json:"item_count"`
	SellerCount       int        `json:"seller_count"`
	FirstProductID    *uuid.UUID `json:"first_product_id,omitempty"`
	FirstProductTitle string     `json:"first_product_title,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	// CreatedAtEpoch is what the client actually parses; the RFC3339 form
	// above stays for anything reading the API by hand.
	CreatedAtEpoch int64 `json:"created_at_epoch"`
}

// ListOrderCardsByCustomer returns one page of order cards for the
// customer using keyset pagination on (created_at, id). cursorTime nil
// means first page. Returns the page (up to limit) plus a flag whether
// more rows exist (so the caller can mint a next cursor).
func (s *Store) ListOrderCardsByCustomer(ctx context.Context, userID uuid.UUID, cursorTime *time.Time, cursorID *uuid.UUID, limit int) ([]OrderCard, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	// Fetch limit+1 so we can detect whether a next page exists without a
	// separate COUNT(*) — count would index-scan the full set per request.
	rows, err := s.db.Query(ctx, `
		SELECT o.id, o.order_number, o.currency_code,
		       o.subtotal_minor, o.discount_amount_minor, o.shipping_charges_minor,
		       o.tax_amount_minor, o.final_amount_minor,
		       o.payment_method, o.payment_status, o.status, o.created_at,
		       COALESCE(items.item_count, 0),
		       COALESCE(items.seller_count, 0),
		       items.first_product_id,
		       items.first_product_title
		FROM orders o
		LEFT JOIN LATERAL (
			SELECT
				COUNT(*) AS item_count,
				COUNT(DISTINCT seller_id) AS seller_count,
				(ARRAY_AGG(product_id ORDER BY created_at ASC))[1] AS first_product_id,
				(ARRAY_AGG(product_title ORDER BY created_at ASC))[1] AS first_product_title
			FROM order_items oi
			WHERE oi.order_id = o.id
		) items ON TRUE
		WHERE o.customer_user_id = $1
		  AND ($2::TIMESTAMPTZ IS NULL OR (o.created_at, o.id) < ($2, $3::UUID))
		ORDER BY o.created_at DESC, o.id DESC
		LIMIT $4
	`, userID, cursorTime, cursorID, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	out := make([]OrderCard, 0, limit+1)
	for rows.Next() {
		var c OrderCard
		var firstProductID *uuid.UUID
		var firstProductTitle *string
		if err := rows.Scan(
			&c.ID, &c.OrderNumber, &c.Currency,
			&c.SubtotalMinor, &c.DiscountMinor, &c.ShippingMinor,
			&c.TaxMinor, &c.TotalMinor,
			&c.PaymentMethod, &c.PaymentStatus, &c.Status, &c.CreatedAt,
			&c.ItemCount, &c.SellerCount,
			&firstProductID, &firstProductTitle,
		); err != nil {
			return nil, false, err
		}
		c.FirstProductID = firstProductID
		if firstProductTitle != nil {
			c.FirstProductTitle = *firstProductTitle
		}
		c.CreatedAtEpoch = c.CreatedAt.Unix()
		out = append(out, c)
	}
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}
	return out, hasMore, nil
}

func (s *Store) GetOrdersByCustomer(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*Order, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM orders WHERE customer_user_id=$1`, userID).Scan(&total)

	rows, err := s.db.Query(ctx, `SELECT id,customer_user_id,order_number,final_amount,currency_code,
		payment_status,status,created_at,updated_at FROM orders
		WHERE customer_user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.FinalAmount,
			&o.CurrencyCode, &o.PaymentStatus, &o.Status, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, 0, err
		}
		orders = append(orders, &o)
	}
	return orders, total, nil
}

// GetOrdersBySeller returns orders containing at least one item sold by the given seller.
func (s *Store) GetOrdersBySeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*Order, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(DISTINCT o.id) FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE oi.seller_id = $1
	`, sellerID).Scan(&total)

	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT o.id, o.customer_user_id, o.order_number, o.final_amount, o.currency_code,
			o.payment_status, o.status, o.created_at, o.updated_at
		FROM orders o
		JOIN order_items oi ON oi.order_id = o.id
		WHERE oi.seller_id = $1
		ORDER BY o.created_at DESC
		LIMIT $2 OFFSET $3
	`, sellerID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.CustomerUserID, &o.OrderNumber, &o.FinalAmount,
			&o.CurrencyCode, &o.PaymentStatus, &o.Status, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, 0, err
		}
		orders = append(orders, &o)
	}
	return orders, total, nil
}

func (s *Store) UpdateOrderStatus(ctx context.Context, orderID uuid.UUID, toStatus string, actorID *uuid.UUID, actorType, notes string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var fromStatus string
	if err = tx.QueryRow(ctx, `UPDATE orders SET status=$2,updated_at=NOW() WHERE id=$1 RETURNING (SELECT status FROM orders WHERE id=$1)`, orderID, toStatus).Scan(&fromStatus); err != nil {
		// Fallback: just update
		if _, err2 := tx.Exec(ctx, `UPDATE orders SET status=$2,updated_at=NOW() WHERE id=$1`, orderID, toStatus); err2 != nil {
			return err2
		}
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO order_status_history (id,order_id,from_status,to_status,changed_by,actor_type,notes,created_at)
		VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6,NOW())`,
		orderID, fromStatus, toStatus, actorID, actorType, notes,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetOrderItems(ctx context.Context, orderID uuid.UUID) ([]*OrderItem, error) {
	rows, err := s.db.Query(ctx, `SELECT id,order_id,product_id,variant_id,seller_id,product_title,
		variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,tax_amount,final_price,
		status,shipment_id,tracking_number,return_eligible_until,delivered_at,created_at
		FROM order_items WHERE order_id=$1`, orderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*OrderItem
	for rows.Next() {
		var item OrderItem
		if err := rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.VariantID, &item.SellerID,
			&item.ProductTitle, &item.VariantDetails, &item.SKU, &item.Quantity,
			&item.UnitMRP, &item.UnitPrice, &item.DiscountAmount, &item.TaxAmount, &item.FinalPrice,
			&item.Status, &item.ShipmentID, &item.TrackingNumber, &item.ReturnEligibleUntil,
			&item.DeliveredAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, &item)
	}
	return items, nil
}

// GetOrderItemByID fetches a single order item. Used by the review-create
// path (Phase 0.6) to validate that the reviewer's order item actually
// matches the product + seller they're trying to review.
func (s *Store) GetOrderItemByID(ctx context.Context, id uuid.UUID) (*OrderItem, error) {
	var item OrderItem
	err := s.db.QueryRow(ctx, `SELECT id,order_id,product_id,variant_id,seller_id,product_title,
		variant_details,sku,quantity,unit_mrp,unit_price,discount_amount,tax_amount,final_price,
		status,shipment_id,tracking_number,return_eligible_until,delivered_at,created_at
		FROM order_items WHERE id=$1`, id).Scan(
		&item.ID, &item.OrderID, &item.ProductID, &item.VariantID, &item.SellerID,
		&item.ProductTitle, &item.VariantDetails, &item.SKU, &item.Quantity,
		&item.UnitMRP, &item.UnitPrice, &item.DiscountAmount, &item.TaxAmount, &item.FinalPrice,
		&item.Status, &item.ShipmentID, &item.TrackingNumber, &item.ReturnEligibleUntil,
		&item.DeliveredAt, &item.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// Payment-status writes live in order_transitions.go (MarkOrderPaid /
// TransitionPaymentStatus) — every transition is guarded by the allowed
// source states so concurrent callers converge on one applied write.

// ─── Customer Addresses ──────────────────────────────────────

// SealedAddressWrite carries the encrypted identifying fields alongside the
// row they belong to.
//
// B4. The ciphertext and the business row are written in ONE statement, so
// there is no window in which a customer address exists without its
// ciphertext — a window a crash would turn into a permanently plaintext row
// that the backfill would then have to find.
//
// Sealing happens in the service, because it is a KMS network call and the
// store's contract is that it does no I/O beyond the database. The store's
// job is to make the write atomic.
type SealedAddressWrite struct {
	ContactName  []byte
	Phone        []byte
	AddressLine1 []byte
	AddressLine2 []byte
	Landmark     []byte
	KeyVersion   int
	LookupHash   string

	// WritePlaintext is the cutover switch (pii.Mode.WritesPlaintext).
	//
	// True during the dual-write window, so the previous image can still read
	// every row and a rollback is survivable. False after cutover, when the
	// identifying columns are written empty and ciphertext is the only copy.
	WritePlaintext bool
}

// identifying returns the plaintext to store in the identifying columns.
//
// Empty strings rather than NULL: contact_name, phone and address_line_1 are
// NOT NULL, and widening them is a contract change this pass does not make.
// "Nonblank identifying plaintext" is therefore the property the scrub
// verifies, and ” is the scrubbed state.
func (w SealedAddressWrite) identifying(
	name, phone, line1 string, line2, landmark *string,
) (string, string, string, *string, *string) {
	if w.WritePlaintext {
		return name, phone, line1, line2, landmark
	}
	// address_line_2 and landmark are nullable, so their scrubbed state is
	// NULL rather than ''. The three NOT NULL columns take ''.
	return "", "", "", nil, nil
}

// CreateAddress writes a customer address with its identifying fields sealed.
//
// B4: `sealed` is REQUIRED. There is no path here that stores a customer's
// name, phone or street in plaintext only — the previous version of this
// function had no other path, which is why every address in the database is
// currently readable by anyone holding a database credential.
func (s *Store) CreateAddress(ctx context.Context, addr *CustomerAddress, sealed SealedAddressWrite) error {
	if sealed.KeyVersion <= 0 || len(sealed.AddressLine1) == 0 {
		// A write that reached here without ciphertext would be a silent
		// plaintext row. Refusing costs the customer one retry; accepting
		// costs an address that the scrub will later clear and that nothing
		// can then recover.
		return fmt.Errorf("commerce: refusing to store an address without sealed identifying fields")
	}
	addr.ID = uuid.New()
	addr.CreatedAt = time.Now()
	name, phone, line1, line2, landmark := sealed.identifying(
		addr.ContactName, addr.Phone, addr.AddressLine1, addr.AddressLine2, addr.Landmark)

	_, err := s.db.Exec(ctx, `
		INSERT INTO customer_addresses (id,user_id,label,contact_name,phone,address_line_1,
		  address_line_2,landmark,city,state,country,postal_code,address_type,is_default,created_at,
		  contact_name_enc,phone_enc,address_line_1_enc,address_line_2_enc,landmark_enc,
		  pii_key_version,pii_scope,lookup_hash)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,
		        $16,$17,$18,$19,$20,$21,'profile',NULLIF($22,''))`,
		addr.ID, addr.UserID, addr.Label, name, phone, line1,
		line2, landmark, addr.City, addr.State, addr.Country, addr.PostalCode,
		addr.AddressType, addr.IsDefault, addr.CreatedAt,
		sealed.ContactName, sealed.Phone, sealed.AddressLine1, sealed.AddressLine2, sealed.Landmark,
		sealed.KeyVersion, sealed.LookupHash,
	)
	return err
}

// TaxClass holds GST percentages for a given class (e.g. "GST 18%").
type TaxClass struct {
	ID             uuid.UUID `db:"id"`
	Name           string    `db:"name"`
	CGSTPercentage float64   `db:"cgst_percentage"`
	SGSTPercentage float64   `db:"sgst_percentage"`
	IGSTPercentage float64   `db:"igst_percentage"`
	CESSPercentage float64   `db:"cess_percentage"`
}

func (s *Store) GetTaxClass(ctx context.Context, id uuid.UUID) (*TaxClass, error) {
	tc := &TaxClass{}
	err := s.db.QueryRow(ctx, `
		SELECT id, name, cgst_percentage, sgst_percentage, igst_percentage, cess_percentage
		FROM tax_classes WHERE id = $1
	`, id).Scan(&tc.ID, &tc.Name, &tc.CGSTPercentage, &tc.SGSTPercentage, &tc.IGSTPercentage, &tc.CESSPercentage)
	if err != nil {
		return nil, err
	}
	return tc, nil
}

func (s *Store) GetAddressByID(ctx context.Context, id uuid.UUID) (*CustomerAddress, error) {
	a := &CustomerAddress{}
	err := s.db.QueryRow(ctx, `SELECT id,user_id,label,contact_name,phone,address_line_1,
		address_line_2,landmark,city,state,country,postal_code,address_type,is_default,created_at
		FROM customer_addresses WHERE id=$1`, id).Scan(
		&a.ID, &a.UserID, &a.Label, &a.ContactName, &a.Phone, &a.AddressLine1,
		&a.AddressLine2, &a.Landmark, &a.City, &a.State, &a.Country, &a.PostalCode,
		&a.AddressType, &a.IsDefault, &a.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// UpdateAddress replaces an address, re-sealing its identifying fields.
//
// B4: the ciphertext columns are ALWAYS overwritten, including when the
// cutover is still writing plaintext. An update that refreshed the plaintext
// and left stale ciphertext behind would be the worst of both — the row would
// look encrypted, and what it decrypts to would be the previous occupant's
// address.
func (s *Store) UpdateAddress(ctx context.Context, id, userID uuid.UUID, addr *CustomerAddress, sealed SealedAddressWrite) error {
	if sealed.KeyVersion <= 0 || len(sealed.AddressLine1) == 0 {
		return fmt.Errorf("commerce: refusing to update an address without sealed identifying fields")
	}
	name, phone, line1, line2, landmark := sealed.identifying(
		addr.ContactName, addr.Phone, addr.AddressLine1, addr.AddressLine2, addr.Landmark)

	tag, err := s.db.Exec(ctx, `
		UPDATE customer_addresses SET
			contact_name=$3, phone=$4, address_line_1=$5, address_line_2=$6,
			landmark=$7, city=$8, state=$9, country=$10, postal_code=$11,
			address_type=$12, is_default=$13,
			contact_name_enc=$14, phone_enc=$15, address_line_1_enc=$16,
			address_line_2_enc=$17, landmark_enc=$18,
			pii_key_version=$19, lookup_hash=NULLIF($20,''),
			updated_at=NOW()
		WHERE id=$1 AND user_id=$2`,
		id, userID, name, phone, line1, line2,
		landmark, addr.City, addr.State, addr.Country, addr.PostalCode,
		addr.AddressType, addr.IsDefault,
		sealed.ContactName, sealed.Phone, sealed.AddressLine1,
		sealed.AddressLine2, sealed.Landmark,
		sealed.KeyVersion, sealed.LookupHash,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found")
	}
	return nil
}

func (s *Store) DeleteAddress(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM customer_addresses WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found")
	}
	return nil
}

// SetDefaultAddress atomically clears any existing default and sets the given address as default.
func (s *Store) SetDefaultAddress(ctx context.Context, id, userID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `UPDATE customer_addresses SET is_default=false WHERE user_id=$1`, userID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE customer_addresses SET is_default=true WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("address not found")
	}
	return tx.Commit(ctx)
}

func (s *Store) GetAddressesByUser(ctx context.Context, userID uuid.UUID) ([]*CustomerAddress, error) {
	rows, err := s.db.Query(ctx, `SELECT id,user_id,label,contact_name,phone,address_line_1,
		address_line_2,landmark,city,state,country,postal_code,address_type,is_default,created_at
		FROM customer_addresses WHERE user_id=$1 ORDER BY is_default DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var addrs []*CustomerAddress
	for rows.Next() {
		var a CustomerAddress
		if err := rows.Scan(&a.ID, &a.UserID, &a.Label, &a.ContactName, &a.Phone, &a.AddressLine1,
			&a.AddressLine2, &a.Landmark, &a.City, &a.State, &a.Country, &a.PostalCode,
			&a.AddressType, &a.IsDefault, &a.CreatedAt); err != nil {
			return nil, err
		}
		addrs = append(addrs, &a)
	}
	return addrs, nil
}

// ─── Reviews ─────────────────────────────────────────────────

func (s *Store) CreateReview(ctx context.Context, r *Review) error {
	r.ID = uuid.New()
	r.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO reviews (id,product_id,seller_id,order_item_id,reviewer_id,
		  rating,title,body,is_verified_purchase,is_published,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		r.ID, r.ProductID, r.SellerID, r.OrderItemID, r.ReviewerID,
		r.Rating, r.Title, r.Body, r.IsVerifiedPurchase, r.IsPublished, r.CreatedAt,
	)
	return err
}

func (s *Store) GetProductReviews(ctx context.Context, productID uuid.UUID, limit, offset int) ([]*Review, int, error) {
	var total int
	_ = s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM reviews
		WHERE product_id=$1 AND is_published=TRUE
		  AND COALESCE(moderation_status,'approved') <> 'rejected'
	`, productID).Scan(&total)

	rows, err := s.db.Query(ctx, `SELECT id,product_id,seller_id,order_item_id,reviewer_id,
		rating,title,body,is_verified_purchase,helpful_count,
		COALESCE(moderation_status,'approved'),seller_response,seller_responded_at,created_at
		FROM reviews WHERE product_id=$1 AND is_published=TRUE
		  AND COALESCE(moderation_status,'approved') <> 'rejected'
		ORDER BY helpful_count DESC, created_at DESC LIMIT $2 OFFSET $3`, productID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var reviews []*Review
	for rows.Next() {
		var r Review
		if err := rows.Scan(&r.ID, &r.ProductID, &r.SellerID, &r.OrderItemID, &r.ReviewerID,
			&r.Rating, &r.Title, &r.Body, &r.IsVerifiedPurchase, &r.HelpfulCount,
			&r.ModerationStatus, &r.SellerResponse, &r.SellerRespondedAt, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		reviews = append(reviews, &r)
	}
	return reviews, total, nil
}

// GetReviewByID fetches a single review — used by the seller-response
// handler (Phase 2.4) to verify the actor is the seller before allowing
// a response, and to return the updated row.
func (s *Store) GetReviewByID(ctx context.Context, id uuid.UUID) (*Review, error) {
	var r Review
	err := s.db.QueryRow(ctx, `SELECT id,product_id,seller_id,order_item_id,reviewer_id,
		rating,title,body,is_verified_purchase,helpful_count,
		COALESCE(moderation_status,'approved'),seller_response,seller_responded_at,created_at
		FROM reviews WHERE id=$1`, id).Scan(
		&r.ID, &r.ProductID, &r.SellerID, &r.OrderItemID, &r.ReviewerID,
		&r.Rating, &r.Title, &r.Body, &r.IsVerifiedPurchase, &r.HelpfulCount,
		&r.ModerationStatus, &r.SellerResponse, &r.SellerRespondedAt, &r.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SetSellerResponse adds (or replaces) the seller's response to a review.
// Returns the affected row count so the service layer can distinguish a
// no-op (id not found) from a legitimate skip.
func (s *Store) SetSellerResponse(ctx context.Context, id uuid.UUID, response string) error {
	_, err := s.db.Exec(ctx,
		`UPDATE reviews SET seller_response=$2, seller_responded_at=NOW() WHERE id=$1`,
		id, response,
	)
	return err
}

// ─── Coupons ─────────────────────────────────────────────────

func (s *Store) GetCouponByCode(ctx context.Context, code string) (*Coupon, error) {
	var c Coupon
	err := s.db.QueryRow(ctx, `SELECT id,seller_id,code,description,discount_type,discount_value,
		max_discount_amount,min_order_amount,max_uses,uses_count,max_uses_per_user,
		applicable_to,is_active,starts_at,expires_at
		FROM coupons WHERE code=$1 AND is_active=TRUE AND starts_at<=NOW()
		AND (expires_at IS NULL OR expires_at>NOW())`, code).Scan(
		&c.ID, &c.SellerID, &c.Code, &c.Description, &c.DiscountType, &c.DiscountValue,
		&c.MaxDiscountAmount, &c.MinOrderAmount, &c.MaxUses, &c.UsesCount, &c.MaxUsesPerUser,
		&c.ApplicableTo, &c.IsActive, &c.StartsAt, &c.ExpiresAt,
	)
	return &c, err
}

// CountCouponUsagesByUser returns how many times a user has already
// redeemed a coupon. Audit O10: the service uses this against
// max_uses_per_user before applying the discount at checkout.
func (s *Store) CountCouponUsagesByUser(ctx context.Context, couponID, userID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM coupon_usages WHERE coupon_id = $1 AND user_id = $2`,
		couponID, userID).Scan(&n)
	return n, err
}

func (s *Store) IncrCouponUsage(ctx context.Context, couponID, userID, orderID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `UPDATE coupons SET uses_count=uses_count+1 WHERE id=$1`, couponID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO coupon_usages (coupon_id,user_id,order_id) VALUES ($1,$2,$3)`,
		couponID, userID, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Return Requests ─────────────────────────────────────────

func (s *Store) CreateReturnRequest(ctx context.Context, r *ReturnRequest) error {
	r.ID = uuid.New()
	r.RequestedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO return_requests (id,order_id,order_item_id,customer_user_id,seller_id,
		  reason_code,reason_description,status,requested_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		r.ID, r.OrderID, r.OrderItemID, r.CustomerUserID, r.SellerID,
		r.ReasonCode, r.ReasonDescription, r.Status, r.RequestedAt,
	)
	return err
}

// ListReturnsBySeller returns return requests for a seller, optionally
// filtered by status. Phase 4.3 — feeds the seller returns inbox.
// status="" returns all states.
func (s *Store) ListReturnsBySeller(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*ReturnRequest, error) {
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = s.db.Query(ctx, `
			SELECT id, order_id, order_item_id, customer_user_id, seller_id, reason_code,
				reason_description, status, approved_at, rejected_at, rejection_reason,
				requested_at, refund_amount
			FROM return_requests
			WHERE seller_id = $1
			ORDER BY requested_at DESC
			LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	} else {
		rows, err = s.db.Query(ctx, `
			SELECT id, order_id, order_item_id, customer_user_id, seller_id, reason_code,
				reason_description, status, approved_at, rejected_at, rejection_reason,
				requested_at, refund_amount
			FROM return_requests
			WHERE seller_id = $1 AND status = $2
			ORDER BY requested_at DESC
			LIMIT $3 OFFSET $4`, sellerID, status, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReturnRequest
	for rows.Next() {
		var r ReturnRequest
		if err := rows.Scan(&r.ID, &r.OrderID, &r.OrderItemID, &r.CustomerUserID, &r.SellerID,
			&r.ReasonCode, &r.ReasonDescription, &r.Status, &r.ApprovedAt, &r.RejectedAt,
			&r.RejectionReason, &r.RequestedAt, &r.RefundAmount); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// ListReturnsByCustomer returns a customer's return requests across all orders.
func (s *Store) ListReturnsByCustomer(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*ReturnRequest, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, order_id, order_item_id, customer_user_id, seller_id, reason_code,
			reason_description, status, approved_at, rejected_at, rejection_reason,
			requested_at
		FROM return_requests
		WHERE customer_user_id = $1
		ORDER BY requested_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReturnRequest
	for rows.Next() {
		var r ReturnRequest
		if err := rows.Scan(&r.ID, &r.OrderID, &r.OrderItemID, &r.CustomerUserID, &r.SellerID,
			&r.ReasonCode, &r.ReasonDescription, &r.Status, &r.ApprovedAt, &r.RejectedAt,
			&r.RejectionReason, &r.RequestedAt); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, nil
}

// UpdateReturnStatus advances a return through requested → approved/rejected.
// rejReason is only persisted when status is 'rejected'; pass nil otherwise.
//
// (Earlier signature took an actorID for audit, but no approved_by /
// rejected_by columns exist on return_requests so it was always discarded.
// Dropped to avoid a pgx parameter-type-inference error when callers passed
// untyped nils.)
func (s *Store) UpdateReturnStatus(ctx context.Context, id uuid.UUID, status string, rejReason *string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests SET status=$2,
		  approved_at=CASE WHEN $2='approved' THEN NOW() ELSE approved_at END,
		  rejected_at=CASE WHEN $2='rejected' THEN NOW() ELSE rejected_at END,
		  rejection_reason=COALESCE($3,rejection_reason)
		WHERE id=$1`, id, status, rejReason)
	return err
}

// GetReturnRequestByID returns a single return request for inspection.
func (s *Store) GetReturnRequestByID(ctx context.Context, id uuid.UUID) (*ReturnRequest, error) {
	r := &ReturnRequest{}
	err := s.db.QueryRow(ctx, `
		SELECT id, order_id, order_item_id, customer_user_id, seller_id,
		       reason_code, reason_description, status,
		       approved_at, rejected_at, rejection_reason, refund_amount,
		       requested_at
		FROM return_requests WHERE id=$1`, id).Scan(
		&r.ID, &r.OrderID, &r.OrderItemID, &r.CustomerUserID, &r.SellerID,
		&r.ReasonCode, &r.ReasonDescription, &r.Status,
		&r.ApprovedAt, &r.RejectedAt, &r.RejectionReason, &r.RefundAmount,
		&r.RequestedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// SetReturnPickupLabel records the courier-issued return shipping details
// (pickup at the customer's address, drop at the seller). Called after
// ApproveReturn books a pickup with the courier.
func (s *Store) SetReturnPickupLabel(ctx context.Context, returnID uuid.UUID, courierName, awb, labelURL string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests
		SET pickup_courier=$2, pickup_tracking_number=$3, pickup_label_url=$4
		WHERE id=$1`, returnID, courierName, awb, labelURL)
	return err
}

// CreateCODRemittance inserts a COD remittance row. Idempotent on
// shipment_id (the table has a UNIQUE constraint) — a second delivery
// webhook for the same shipment is dropped silently. Returns nil on
// successful insert OR on conflict, both of which are "fine".
func (s *Store) CreateCODRemittance(ctx context.Context, r *CODRemittance) error {
	r.ID = uuid.New()
	r.CreatedAt = time.Now()
	_, err := s.db.Exec(ctx, `
		INSERT INTO cod_remittances (
			id, shipment_id, order_id, seller_id,
			gross_amount, commission_amount, platform_fee, tds_amount, net_amount,
			currency_code, status, delivered_at, created_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (shipment_id) DO NOTHING`,
		r.ID, r.ShipmentID, r.OrderID, r.SellerID,
		r.GrossAmount, r.CommissionAmount, r.PlatformFee, r.TDSAmount, r.NetAmount,
		r.CurrencyCode, r.Status, r.DeliveredAt, r.CreatedAt,
	)
	return err
}

// ListCODRemittancesBySeller returns the seller's COD remittances newest
// first, optionally filtered by status. Used by the seller payouts UI.
func (s *Store) ListCODRemittancesBySeller(ctx context.Context, sellerID uuid.UUID, status string, limit, offset int) ([]*CODRemittance, int, error) {
	conds := []string{"seller_id = $1"}
	args := []any{sellerID}
	idx := 2
	if status != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, status)
		idx++
	}
	where := "WHERE " + strings.Join(conds, " AND ")

	var total int
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM cod_remittances "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := s.db.Query(ctx, `
		SELECT id, shipment_id, order_id, seller_id,
		       gross_amount, commission_amount, platform_fee, tds_amount, net_amount,
		       currency_code, status, delivered_at, settled_at, payout_batch_id, created_at
		FROM cod_remittances `+where+
		fmt.Sprintf(" ORDER BY delivered_at DESC LIMIT $%d OFFSET $%d", idx, idx+1), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*CODRemittance
	for rows.Next() {
		r := &CODRemittance{}
		if err := rows.Scan(&r.ID, &r.ShipmentID, &r.OrderID, &r.SellerID,
			&r.GrossAmount, &r.CommissionAmount, &r.PlatformFee, &r.TDSAmount, &r.NetAmount,
			&r.CurrencyCode, &r.Status, &r.DeliveredAt, &r.SettledAt, &r.PayoutBatchID, &r.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

// PendingPayoutSummary groups outstanding (unsettled) COD remittances by
// seller so the admin reconciliation dashboard can show "how much do we owe
// each seller" in one query. Phase 4.5.
type PendingPayoutSummary struct {
	SellerID         uuid.UUID `db:"seller_id" json:"seller_id"`
	StoreName        string    `db:"store_name" json:"store_name"`
	Email            string    `db:"email" json:"email"`
	RemittanceCount  int       `db:"remittance_count" json:"remittance_count"`
	TotalGross       float64   `db:"total_gross" json:"total_gross"`
	TotalCommission  float64   `db:"total_commission" json:"total_commission"`
	TotalPlatformFee float64   `db:"total_platform_fee" json:"total_platform_fee"`
	TotalTDS         float64   `db:"total_tds" json:"total_tds"`
	TotalNet         float64   `db:"total_net" json:"total_net"`
	OldestDelivered  time.Time `db:"oldest_delivered" json:"oldest_delivered"`
}

// ListPendingPayoutsBySeller returns one row per seller with an outstanding
// COD remittance balance. Used by the admin reconciliation dashboard.
// Phase 4.5.
func (s *Store) ListPendingPayoutsBySeller(ctx context.Context, limit int) ([]*PendingPayoutSummary, error) {
	rows, err := s.db.Query(ctx, `
		SELECT
			cr.seller_id,
			COALESCE(sl.store_name, '') AS store_name,
			COALESCE(sl.email, '') AS email,
			COUNT(*)::int AS remittance_count,
			COALESCE(SUM(cr.gross_amount), 0)      AS total_gross,
			COALESCE(SUM(cr.commission_amount), 0) AS total_commission,
			COALESCE(SUM(cr.platform_fee), 0)      AS total_platform_fee,
			COALESCE(SUM(cr.tds_amount), 0)        AS total_tds,
			COALESCE(SUM(cr.net_amount), 0)        AS total_net,
			MIN(cr.delivered_at)                   AS oldest_delivered
		FROM cod_remittances cr
		LEFT JOIN sellers sl ON sl.id = cr.seller_id
		WHERE cr.status = 'pending'
		GROUP BY cr.seller_id, sl.store_name, sl.email
		ORDER BY MIN(cr.delivered_at) ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PendingPayoutSummary
	for rows.Next() {
		var r PendingPayoutSummary
		if err := rows.Scan(&r.SellerID, &r.StoreName, &r.Email, &r.RemittanceCount,
			&r.TotalGross, &r.TotalCommission, &r.TotalPlatformFee, &r.TotalTDS, &r.TotalNet,
			&r.OldestDelivered); err != nil {
			return nil, err
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

// DeliveredItemRow joins a delivered order item with its order header so the
// caller can render or compute earnings without a second round trip.
// Phase 4.4.
type DeliveredItemRow struct {
	Item          *OrderItem
	OrderNumber   string
	PaymentMethod *string
}

// ListDeliveredItemsForSeller returns delivered order items for the seller,
// newest first. The order header carries payment_method so the service layer
// can split COD vs prepaid (COD has its own remittance ledger).
// Phase 4.4.
func (s *Store) ListDeliveredItemsForSeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]*DeliveredItemRow, error) {
	rows, err := s.db.Query(ctx, `
		SELECT oi.id, oi.order_id, oi.product_id, oi.variant_id, oi.seller_id, oi.product_title,
		       oi.sku, oi.quantity, oi.unit_mrp, oi.unit_price, oi.discount_amount, oi.tax_amount,
		       oi.final_price, oi.status, oi.shipment_id, oi.tracking_number, oi.return_eligible_until,
		       oi.delivered_at, oi.created_at,
		       o.order_number, o.payment_method
		FROM order_items oi
		JOIN orders o ON o.id = oi.order_id
		WHERE oi.seller_id = $1
		  AND (oi.status = 'delivered' OR oi.delivered_at IS NOT NULL)
		ORDER BY oi.delivered_at DESC NULLS LAST
		LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DeliveredItemRow
	for rows.Next() {
		var it OrderItem
		var orderNumber string
		var paymentMethod *string
		if err := rows.Scan(&it.ID, &it.OrderID, &it.ProductID, &it.VariantID, &it.SellerID,
			&it.ProductTitle, &it.SKU, &it.Quantity, &it.UnitMRP, &it.UnitPrice, &it.DiscountAmount,
			&it.TaxAmount, &it.FinalPrice, &it.Status, &it.ShipmentID, &it.TrackingNumber,
			&it.ReturnEligibleUntil, &it.DeliveredAt, &it.CreatedAt,
			&orderNumber, &paymentMethod); err != nil {
			return nil, err
		}
		out = append(out, &DeliveredItemRow{Item: &it, OrderNumber: orderNumber, PaymentMethod: paymentMethod})
	}
	return out, rows.Err()
}

// MarkCODRemittanceSettled flips a pending remittance to settled and stamps
// the payout batch. Used by the Ops-side payout job when cash actually
// transfers to the seller's bank/UPI. payoutBatchID may be uuid.Nil for a
// standalone settlement (Ops marked it paid outside any batch); we store
// NULL in that case so the row isn't tied to a non-existent batch.
func (s *Store) MarkCODRemittanceSettled(ctx context.Context, remittanceID, payoutBatchID uuid.UUID) error {
	var batchArg interface{}
	if payoutBatchID != uuid.Nil {
		batchArg = payoutBatchID
	}
	_, err := s.db.Exec(ctx, `
		UPDATE cod_remittances
		SET status = 'settled',
		    settled_at = NOW(),
		    payout_batch_id = $2
		WHERE id = $1 AND status = 'pending'`, remittanceID, batchArg)
	return err
}

// SetReturnRefund stamps the refund intent + status onto the return. Used
// once payments-service accepts the refund — even if the gateway is async,
// we record the intent ID immediately so a follow-up webhook can find it.
func (s *Store) SetReturnRefund(ctx context.Context, returnID uuid.UUID, refundIntentID, status string, amount float64) error { // money-exempt: returns are fenced at /v1/commerce/returns (LB-11)
	_, err := s.db.Exec(ctx, `
		UPDATE return_requests
		SET refund_intent_id=$2, refund_status=$3, refund_amount=$4
		WHERE id=$1`, returnID, refundIntentID, status, amount)
	return err
}

// MarkReturnRefundSucceededByIntent flips a return's refund_status to
// 'succeeded' once payments-service confirms the refund via Kafka. Keyed
// on refund_intent_id (set by SetReturnRefund at approve time) so the
// consumer doesn't need to know the return ID. Returns the affected row
// count so the caller can log a no-op gracefully (event for a refund
// that was never tied to a return).
func (s *Store) MarkReturnRefundSucceededByIntent(ctx context.Context, intentID string) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
		UPDATE return_requests
		SET refund_status='succeeded'
		WHERE refund_intent_id=$1 AND refund_status IN ('pending','processing')`,
		intentID)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// StampOrderRefundIntent records the refund intent ID + flips the
// order's payment_status to 'refund_pending'. Used by CancelOrder when
// it kicks off the refund on payments-service so the later
// payment.refunded event can find the order via intent_id.
func (s *Store) StampOrderRefundIntent(ctx context.Context, orderID uuid.UUID, intentID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE orders
		SET refund_intent_id = $2,
		    payment_status = 'refund_pending',
		    updated_at = NOW()
		WHERE id = $1 AND payment_status = 'paid'`,
		orderID, intentID)
	return err
}

// MarkOrderRefundedByPayment is used by the consumer when a refund
// event arrives for an order-level intent (i.e. a CancelOrder refund
// rather than a per-line return). Flips orders.payment_status to
// 'refunded' if currently 'paid'. Keyed on the intent id stamped onto
// the order at refund-initiation time. Returns affected row count.
func (s *Store) MarkOrderRefundedByPayment(ctx context.Context, intentID string) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
		UPDATE orders
		SET payment_status='refunded', updated_at=NOW()
		WHERE refund_intent_id=$1 AND payment_status IN ('paid','refund_pending')`,
		intentID)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// ─── Payout Batches ──────────────────────────────────────────

func (s *Store) CreatePayoutBatch(ctx context.Context, batch *PayoutBatch, txns []*PayoutTransaction) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	batch.ID = uuid.New()
	batch.CreatedAt = time.Now()
	if _, err = tx.Exec(ctx, `
		INSERT INTO payout_batches (id,batch_date,payout_cycle_start,payout_cycle_end,
		  total_sellers,total_amount,status,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		batch.ID, batch.BatchDate, batch.CycleStart, batch.CycleEnd,
		batch.TotalSellers, batch.TotalAmount, batch.Status, batch.CreatedAt,
	); err != nil {
		return err
	}

	for _, t := range txns {
		t.ID = uuid.New()
		t.BatchID = batch.ID
		t.InitiatedAt = time.Now()
		if _, err = tx.Exec(ctx, `
			INSERT INTO payout_transactions (id,batch_id,seller_id,gross_amount,commission_amount,
			  platform_fee,tax_deducted,adjustment_amount,net_amount,bank_account_id,status,initiated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			t.ID, t.BatchID, t.SellerID, t.GrossAmount, t.CommissionAmount,
			t.PlatformFee, t.TaxDeducted, t.AdjustmentAmount, t.NetAmount,
			t.BankAccountID, t.Status, t.InitiatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ─── Rupee → paise, in exactly one place ─────────────────────────────
//
// The product/variant HTTP contract takes rupee floats (`mrp`,
// `selling_price`, `cost_price`) and the columns behind them are
// NUMERIC(12,2). Every pricing path downstream reads the integer `_minor`
// columns. Something has to convert, and it must happen once, at the write
// boundary, so a rounding choice made here cannot be made differently
// somewhere else.
//
// math.Round, not a truncating int64 cast: 12.99 arrives from JSON as
// 12.989999999999998, and `int64(12.989999999999998*100)` is 1298 — a paisa
// lost on a price nobody would ever suspect. Rounding gives 1299.

// rupeesToMinor converts a rupee amount to paise.
func rupeesToMinor(rupees float64) int64 { return int64(math.Round(rupees * 100)) }

// rupeesToMinorPtr converts an optional rupee amount, preserving nil.
//
// nil means "not stated" — a variant with no cost price — and must stay
// distinguishable from a stated zero.
func rupeesToMinorPtr(rupees *float64) *int64 {
	if rupees == nil {
		return nil
	}
	m := rupeesToMinor(*rupees)
	return &m
}

// anyRupeesToMinor converts a value arriving from an untyped JSON patch.
//
// `PATCH /variants/:id` binds into map[string]any, so a price is whatever
// encoding/json produced. A non-numeric value is refused rather than
// coerced: silently treating "1299" or true as zero would reprice the
// variant to free, which is the defect this whole helper exists to close.
func anyRupeesToMinor(v any) (any, error) {
	switch n := v.(type) {
	case nil:
		return nil, nil
	case float64:
		return rupeesToMinor(n), nil
	case float32:
		return rupeesToMinor(float64(n)), nil
	case int:
		return int64(n) * 100, nil
	case int64:
		return n * 100, nil
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("price %q is not a number", n.String())
		}
		return rupeesToMinor(f), nil
	default:
		return nil, fmt.Errorf("price must be a number, got %T", v)
	}
}

// ─── Seller pickup addresses ─────────────────────────────────────────

// SellerAddress is a seller's pickup, warehouse or business address.
//
// The courier needs the pickup PIN as the origin of every shipment, and
// `PrepareQuote` reads it for the delivery rate. Until now nothing in
// production ever wrote one: `POST /sellers/onboard` writes only the flat
// `state`/`city`/`postal_code` columns on `sellers`, and the onboarding wizard
// leaves `pickup_address_id` NULL. `SellerPickupPin`'s "fall back to the
// seller's own postcode" branch was therefore the ONLY live branch — and that
// column is optional, so a seller who skipped it quoted deliveries from an
// empty origin.
type SellerAddress struct {
	ID           uuid.UUID
	SellerID     uuid.UUID
	AddressType  string
	ContactName  string
	Phone        string
	AddressLine1 string
	AddressLine2 *string
	City         string
	State        string
	PostalCode   string
	Country      string
	IsDefault    bool
}

// UpsertSellerAddress writes a seller address with its identifying fields
// sealed, replacing any existing address of the same type.
//
// B4 applies here exactly as it does to customer addresses: a seller's contact
// name, phone and street are identifying PII, they live in the same estate the
// backfill covers, and the gated scrub will clear their plaintext. A write
// that stored them unsealed would be a row the scrub destroys.
//
// One address per (seller, type). A seller has one pickup point at a time, and
// letting two exist would make `SellerPickupPin`'s ORDER BY the thing that
// silently decides where couriers collect from.
func (s *Store) UpsertSellerAddress(ctx context.Context, a SellerAddress, sealed SealedAddressWrite) error {
	if sealed.KeyVersion <= 0 || len(sealed.AddressLine1) == 0 {
		return fmt.Errorf("commerce: refusing to store a seller address without sealed identifying fields")
	}
	if strings.TrimSpace(a.PostalCode) == "" {
		// The whole reason this row exists. A pickup address with no PIN
		// cannot originate a shipment, and the courier call would quote from
		// nowhere.
		return fmt.Errorf("commerce: a seller address requires a postal code")
	}
	if strings.TrimSpace(a.State) == "" {
		// GST place of supply. An empty seller state silently bills CGST+SGST
		// on an interstate sale (ErrPlaceOfSupplyUnknown).
		return fmt.Errorf("commerce: a seller address requires a state")
	}

	name, phone, line1, line2, _ := sealed.identifying(
		a.ContactName, a.Phone, a.AddressLine1, a.AddressLine2, nil)

	_, err := s.db.Exec(ctx, `
		INSERT INTO seller_addresses
		    (seller_id, address_type, contact_name, phone, address_line_1, address_line_2,
		     city, state, country, postal_code, is_default,
		     contact_name_enc, phone_enc, address_line_1_enc, address_line_2_enc, pii_key_version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (seller_id, address_type) DO UPDATE SET
		    contact_name       = EXCLUDED.contact_name,
		    phone              = EXCLUDED.phone,
		    address_line_1     = EXCLUDED.address_line_1,
		    address_line_2     = EXCLUDED.address_line_2,
		    city               = EXCLUDED.city,
		    state              = EXCLUDED.state,
		    country            = EXCLUDED.country,
		    postal_code        = EXCLUDED.postal_code,
		    is_default         = EXCLUDED.is_default,
		    contact_name_enc   = EXCLUDED.contact_name_enc,
		    phone_enc          = EXCLUDED.phone_enc,
		    address_line_1_enc = EXCLUDED.address_line_1_enc,
		    address_line_2_enc = EXCLUDED.address_line_2_enc,
		    pii_key_version    = EXCLUDED.pii_key_version`,
		a.SellerID, a.AddressType, name, phone, line1, line2,
		a.City, a.State, orDefaultCountry(a.Country), a.PostalCode, a.IsDefault,
		sealed.ContactName, sealed.Phone, sealed.AddressLine1, sealed.AddressLine2,
		sealed.KeyVersion,
	)
	return err
}

func orDefaultCountry(c string) string {
	if strings.TrimSpace(c) == "" {
		return "IN"
	}
	return c
}

// orConvert prefers paise the caller supplied over converting their rupees.
//
// The conversion is the fallback, not the path. A price typed as paise that
// gets converted from a float on the way in has already lost whatever the
// float lost, and every exact figure computed from it downstream is exact
// about the wrong number.
func orConvert(minor *int64, rupees float64) int64 {
	if minor != nil {
		return *minor
	}
	return rupeesToMinor(rupees)
}

// orConvertPtr is orConvert for an optional amount. Absent stays absent — a
// nil cost price means "not recorded", never zero.
func orConvertPtr(minor *int64, rupees *float64) *int64 {
	if minor != nil {
		return minor
	}
	return rupeesToMinorPtr(rupees)
}

// ─── Variant repricing ───────────────────────────────────────────────

// variantMoneyPairs maps each rupee column to its paise column.
//
// Both names are accepted from a caller: `selling_price` is the legacy shape
// and `selling_price_minor` is what a client written today sends. They are
// resolved into one another before either is written.
var variantMoneyPairs = map[string]string{
	"mrp":                 "mrp_minor",
	"selling_price":       "selling_price_minor",
	"cost_price":          "cost_price_minor",
	"mrp_minor":           "mrp",
	"selling_price_minor": "selling_price",
	"cost_price_minor":    "cost_price",
}

// variantMoneyUpdate is one resolved amount, ready for both columns.
type variantMoneyUpdate struct {
	rupeeCol string
	minorCol string
	rupees   any // *float64-compatible: nil clears an optional amount
	minor    any
}

// ErrPriceDisagreement means a caller sent both shapes of the same amount and
// they do not describe the same money.
//
// Refused rather than resolved. Picking one silently decides what the buyer
// pays, and there is no reading of "selling_price: 1299, selling_price_minor:
// 99900" that is safe to guess at.
var ErrPriceDisagreement = errors.New("commerce: the rupee and paise forms of a price disagree")

// ErrPriceNotPositive means a repricing would make a variant free.
//
// The catalogue has no concept of a giveaway, and checkout happily charges
// zero. A seller who typed a price wrong should be told, not have their stock
// taken for nothing.
var ErrPriceNotPositive = errors.New("commerce: a price must be greater than zero")

// normaliseVariantMoney resolves every money field the caller supplied into a
// matched (rupees, paise) pair.
func normaliseVariantMoney(updates map[string]any) ([]variantMoneyUpdate, error) {
	out := []variantMoneyUpdate{}
	for _, rupeeCol := range []string{"mrp", "selling_price", "cost_price"} {
		minorCol := variantMoneyPairs[rupeeCol]
		rawRupees, hasRupees := updates[rupeeCol]
		rawMinor, hasMinor := updates[minorCol]
		if !hasRupees && !hasMinor {
			continue
		}

		// An explicit null clears an optional amount. Only cost price is
		// optional; a null selling price would make the variant unpriceable
		// rather than free, and the NOT NULL column would reject it anyway.
		if (hasMinor && rawMinor == nil) || (hasRupees && rawRupees == nil) {
			if rupeeCol != "cost_price" {
				return nil, fmt.Errorf("%w: %s", ErrPriceNotPositive, rupeeCol)
			}
			out = append(out, variantMoneyUpdate{rupeeCol, minorCol, nil, nil})
			continue
		}

		var minor int64
		var err error
		switch {
		case hasMinor:
			minor, err = anyToMinor(rawMinor)
		default:
			minor, err = rupeesToMinorStrict(rawRupees)
		}
		if err != nil {
			return nil, fmt.Errorf("commerce: %s: %w", rupeeCol, err)
		}

		// Both sent: they must agree. This is the one case where guessing
		// changes what a buyer is charged.
		if hasMinor && hasRupees {
			fromRupees, convErr := rupeesToMinorStrict(rawRupees)
			if convErr != nil {
				return nil, fmt.Errorf("commerce: %s: %w", rupeeCol, convErr)
			}
			if fromRupees != minor {
				return nil, fmt.Errorf("%w: %s says %d paise, %s says %d",
					ErrPriceDisagreement, rupeeCol, fromRupees, minorCol, minor)
			}
		}

		if minor <= 0 && rupeeCol != "cost_price" {
			return nil, fmt.Errorf("%w: %s", ErrPriceNotPositive, rupeeCol)
		}
		out = append(out, variantMoneyUpdate{
			rupeeCol: rupeeCol,
			minorCol: minorCol,
			// The rupee column is a mirror of the paise, written for the
			// analytics readers that still scan it.
			rupees: float64(minor) / 100.0, // money-exempt: NUMERIC mirror of the minor column
			minor:  minor,
		})
	}
	return out, nil
}

// anyToMinor reads a paise value out of a decoded JSON body.
//
// encoding/json gives float64 for every number, so an integer count of paise
// arrives as a float and has to come back out. It must be a WHOLE number —
// there is no such thing as a fraction of a paise, and accepting one would
// reintroduce the rounding this path exists to remove.
func anyToMinor(v any) (int64, error) {
	switch n := v.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case float64:
		if n != math.Trunc(n) {
			return 0, fmt.Errorf("paise must be a whole number, got %v", n)
		}
		return int64(n), nil
	case json.Number:
		return n.Int64()
	default:
		return 0, fmt.Errorf("expected a number of paise, got %T", v)
	}
}

// rupeesToMinorStrict is anyRupeesToMinor with a concrete return type.
//
// anyRupeesToMinor returns `any` because it feeds a query-argument slice,
// where a nil has to stay a nil. The repricing path needs to COMPARE two
// amounts, so it needs the number.
func rupeesToMinorStrict(v any) (int64, error) {
	converted, err := anyRupeesToMinor(v)
	if err != nil {
		return 0, err
	}
	switch n := converted.(type) {
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	default:
		return 0, fmt.Errorf("expected a rupee amount, got %T", v)
	}
}
