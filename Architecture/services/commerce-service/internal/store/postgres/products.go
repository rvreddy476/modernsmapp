// Products: categories, product create/read, product media and attributes,
// the seller and filtered storefront listings, view counters, and the GST
// tax-class lookup. Moved out of store.go unchanged.

package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

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

// CreateProduct inserts a product on its own.
//
// The bulk importer still uses it — it builds one product per CSV row and has
// its own per-row error accounting. The seller-facing create path does NOT:
// it goes through CreateProductAtomic, which writes the product, its variants,
// their inventory and its attribute values in one transaction. See
// productwrite.go for why that mattered.
//
// The INSERT itself lives in insertProductTx and is not repeated here.
func (s *Store) CreateProduct(ctx context.Context, p *Product) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := insertProductTx(ctx, tx, p); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) GetProductByID(ctx context.Context, id uuid.UUID) (*Product, error) {
	var p Product
	err := s.db.QueryRow(ctx, `
		SELECT p.id,
		  -- The five columns the offer is now the authority for. `+"`condition`"+` and
		  -- `+"`rejection_reason`"+` deliberately stay on `+"`products`"+`: the consistency
		  -- checker asserts these five and NOT those two, and says why —
		  -- rejection_reason is free text an operator may have edited on one
		  -- side, and condition becomes a genuinely per-offer fact the moment
		  -- a second shop lists the same item. Moving a read the checker does
		  -- not cover would be moving it on faith.
		  po.seller_id,
		  p.category_id,brand_id,tax_class_id,title,short_title,slug,description,
		  short_description,brand_name,manufacturer_name,product_type,p.condition,sku_root,
		  po.status,po.visibility,po.approval_status,
		  p.rejection_reason,primary_image_media_id,video_media_id,weight_grams,length_cm,width_cm,height_cm,
		  country_of_origin,warranty_info,return_policy_type,return_policy_days,hsn_code,search_keywords,
		  meta_title,meta_description,
		  avg_rating,review_count,order_count,view_count,wishlist_count,is_featured,
		  p.created_at,p.updated_at,po.published_at
		  -- A real column since migration 026, not a per-row dig through
		  -- product_attributes.
		  ,source_image_url
		  -- Which vintage of the category form produced this product's typed
		  -- values. Selected here because the seller's edit screen has to
		  -- show it: a listing stamped with an older version is one whose
		  -- answers were checked against bounds that have since moved.
		  ,schema_version
		  -- Migration 037: the public like and share counters. No dislike
		  -- count exists to select.
		  ,p.like_count,p.share_count
		  -- The shop's name comes from the OFFER's seller, for the same
		  -- reason po.seller_id above does.
		  ,(SELECT store_name FROM sellers WHERE id=po.seller_id)
		  ,(SELECT name FROM product_categories WHERE id=p.category_id)
		  -- The gallery cover, so a detail page whose seller used the gallery
		  -- (and therefore left primary_image_media_id NULL) still hydrates
		  -- to a real image instead of a placeholder.
		  ,(SELECT media_id FROM product_media WHERE product_id=p.id AND media_type='image'
		     ORDER BY sort_order ASC, created_at ASC LIMIT 1)
		`+productsLiveFrom+` WHERE p.id=$1`, id).Scan(
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
		&p.CreatedAt, &p.UpdatedAt, &p.PublishedAt, &p.SourceImageURL, &p.SchemaVersion,
		&p.LikeCount, &p.ShareCount,
		&p.RetailerName, &p.CategoryName, &p.CoverMediaID,
	)
	// A product id that does not exist is a 404, not an outage. pgx's raw
	// "no rows in result set" travelled all the way to the edge, where the
	// unmapped-error arm answered 500 — so GET /products/<any uuid> reported
	// the service as broken rather than the product as absent.
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProductNotFound
	}
	return &p, err
}

// ─── Product Media + Attributes (Phase 3.1) ─────────────────

// AddProductMedia attaches an image / video / size-chart / infographic
// (already uploaded via media-service) to a product, ordering it among
// the product's gallery.
func (s *Store) AddProductMedia(ctx context.Context, productID, mediaID uuid.UUID, mediaType string, sortOrder int, revalidate bool) error {
	if mediaType == "" {
		mediaType = "image"
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx,
		`INSERT INTO product_media (product_id, media_id, media_type, sort_order)
		 VALUES ($1, $2, $3, $4)`,
		productID, mediaID, mediaType, sortOrder,
	); err != nil {
		return err
	}
	// A gallery change on an approved listing is a reviewed-content change;
	// the review state moves in the same transaction. See revalidateProductTx.
	if revalidate {
		if err := revalidateProductTx(ctx, tx, productID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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
//
// This is the LEGACY, untyped write — free text on both sides, no definition,
// no validation. It still exists because it is what the current
// `PUT /products/:id/attributes` route and every client of it use. Typed
// values go through Store.PutProductAttributeValues instead.
//
// It only ever touches the legacy rows: the DELETE is scoped to
// `definition_id IS NULL`, so a seller saving their free-text spec block can
// no longer wipe the product's typed attributes as collateral. Before that
// scope existed this method deleted every row for the product.
func (s *Store) SetProductAttributes(ctx context.Context, productID uuid.UUID, attrs []ProductAttribute) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM product_attributes WHERE product_id=$1 AND definition_id IS NULL`,
		productID); err != nil {
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
	// ── The source_image_url dual-write ──────────────────────
	//
	// Migration 026 moved the fallback image URL to products.source_image_url
	// and moved the three readers onto it. This method is the writer on the
	// other side of that move, and it must keep the column agreeing with the
	// rows it just wrote — INCLUDING clearing it when the new set has no such
	// row, because that is what the old readers did when the DELETE above
	// took the row away.
	//
	// Both places are written, not just the column, for the length of the
	// rolling deploy: a pod on the previous image still reads the EAV row and
	// would show a blank image if this stopped maintaining it. The EAV row is
	// also the rollback path. A later gated migration deletes the old rows and
	// this projection becomes a plain column write.
	if _, err := tx.Exec(ctx, `
		UPDATE products p
		   SET source_image_url = (
		       SELECT pa.value FROM product_attributes pa
		        WHERE pa.product_id = p.id
		          AND pa.name = 'source_image_url'
		          AND pa.definition_id IS NULL
		        ORDER BY pa.sort_order, pa.id LIMIT 1)
		 WHERE p.id = $1`, productID); err != nil {
		return err
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
	// Whose catalogue this is, and what state each listing is in, both come
	// off the offer now — see productOfferJoin. `?status=` filters the
	// seller's own switch, which is an offer column too, so it moves with it.
	where := "WHERE po.seller_id=$1"
	args := []any{sellerID}
	if publicOnly {
		where += " AND " + productSummaryLive
	}
	if status != "" {
		where += fmt.Sprintf(" AND po.status=$%d", len(args)+1)
		args = append(args, status)
	}
	var total int
	_ = s.db.QueryRow(ctx, "SELECT COUNT(*) "+productsLiveFrom+" "+where, args...).Scan(&total)

	args = append(args, limit, offset)
	// The shared summary projection, same as the two browse surfaces.
	//
	// This was a THIRD, shorter column list that selected neither price nor
	// stock nor the gallery cover, so the seller's own catalogue screen and
	// the public storefront both drew rows with no image and no money on
	// them — while the identical product looked complete in the browse grid.
	// The projection now comes from one place (storefront.go), which is what
	// stops a surface being left behind again.
	rows, err := s.db.Query(ctx, `
		SELECT `+productSummaryColumns+`
		`+productSummaryFrom+`
		`+where+fmt.Sprintf(" ORDER BY p.created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	products, err := collectProductSummaries(rows)
	if err != nil {
		return nil, 0, err
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
		EXISTS (SELECT 1 FROM sellers ss WHERE ss.id=po.seller_id AND (ss.store_name ILIKE ` + placeholder + ` OR COALESCE(ss.brand_name, '') ILIKE ` + placeholder + `)) OR
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
	conds := []string{productSummaryLive}
	args := []any{}
	idx := 1
	if f.CategoryID != nil {
		conds = append(conds, fmt.Sprintf("p.category_id = $%d", idx))
		args = append(args, *f.CategoryID)
		idx++
	}
	if f.SellerID != nil {
		conds = append(conds, fmt.Sprintf("po.seller_id = $%d", idx))
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
	// The projection is the shared one (storefront.go). It was two hand-copied
	// column lists — this one and ListProducts's — until the gallery cover and
	// the category name had to appear on both and only one of them learned.
	query := `
		SELECT ` + productSummaryColumns + `
		` + productSummaryFrom + `
		` + where + priceFilter + fmt.Sprintf(`
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $%d`, idx)
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	products, err := collectProductSummaries(rows)
	if err != nil {
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
	conds := []string{productSummaryLive}
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
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) "+productsLiveFrom+" "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	// Phase F1 — enrich each row with variant pricing + stock so the
	// catalog grid renders without N+1 detail fetches. LATERAL picks
	// the cheapest active variant as the card's "from price"; mobile
	// uses default_variant_id to add to cart in one click.
	//
	// This used to be a SECOND, shorter copy of the projection, and the
	// difference was not cosmetic: it never selected the paise columns, so
	// every product returned through the legacy offset path had no
	// `min_price_minor` — and therefore no `discount_pct`, since that is
	// derived from the pair. Both paths now read the shared list.
	rows, err := s.db.Query(ctx, `
		SELECT `+productSummaryColumns+`
		`+productSummaryFrom+`
		`+where+fmt.Sprintf(`
		ORDER BY p.created_at DESC
		LIMIT $%d OFFSET $%d`, idx, idx+1), args...)
	if err != nil {
		return nil, 0, err
	}
	products, err := collectProductSummaries(rows)
	if err != nil {
		return nil, 0, err
	}
	return products, total, nil
}

// UpdateProduct is GONE. It took a `map[string]any` and interpolated the
// caller's KEYS into the statement as column names, so it could write any
// column in `products` — approval_status, seller_id, view_count — and had no
// way to say which ones were permitted. It had zero callers, which is the
// only reason it never did.
//
// Its replacement is Store.PatchProduct with the typed ProductPatch
// allowlist, in productwrite.go.

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
