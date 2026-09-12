package main

// The writer. One transaction, every statement ON CONFLICT DO NOTHING, and a
// tally of what was inserted versus already there.
//
// ─── WHY RAW SQL AND NOT THE STORE ──────────────────────────────────────
//
// internal/store/postgres.CreateProduct is the right path for a seller's
// listing: it validates the body, assigns fresh ids, runs the submission
// gate and emits outbox events for search. A demo seed wants none of that.
// It wants fixed ids so a second run is a no-op, 'approved' written directly
// rather than earned through moderation, and no Kafka message announcing a
// placeholder to search-service. The integration fixtures make the same
// choice for the same reasons (see seedOfferFor in internal/http), and this
// file mirrors their statements, in particular the offer row and the
// variant-to-offer link that the storefront's reader join depends on.
//
// ─── WHAT MAKES A PRODUCT VISIBLE ───────────────────────────────────────
//
// productSummaryLive in internal/store/postgres/storefront.go is the rule,
// and it reads three tables: the OFFER must be status='active' and
// approval_status='approved', and the SELLER must be store_status='active'.
// The legacy columns on `products` are still written so the consistency
// checker stays clean, but they are not what a shopper sees. Price comes
// from the cheapest active VARIANT's minor columns and stock from
// inventory_items; a product missing either renders without a price or as
// out of stock, so every one here gets both.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// summary counts rows per table. "skipped" means the ON CONFLICT clause
// fired, which on a second run is every row.
type summary struct {
	inserted map[string]int
	skipped  map[string]int
}

func newSummary() *summary {
	return &summary{inserted: map[string]int{}, skipped: map[string]int{}}
}

// record tallies one statement's outcome by its RowsAffected.
func (s *summary) record(table string, affected int64) {
	if affected > 0 {
		s.inserted[table] += int(affected)
	} else {
		s.skipped[table]++
	}
}

// String renders the tally in a fixed table order so two runs diff cleanly.
func (s *summary) String() string {
	tables := map[string]bool{}
	for t := range s.inserted {
		tables[t] = true
	}
	for t := range s.skipped {
		tables[t] = true
	}
	names := make([]string, 0, len(tables))
	for t := range tables {
		names = append(names, t)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%-28s %9s %9s\n", "table", "inserted", "skipped")
	for _, t := range names {
		fmt.Fprintf(&b, "%-28s %9d %9d\n", t, s.inserted[t], s.skipped[t])
	}
	return b.String()
}

// resolveCategories maps every slug the dataset needs to the id the database
// holds for it, and refuses if any is missing.
//
// By slug and not by migration 023's literal ids, because 023 itself says an
// existing row keeps its id: a database where an operator created
// "Electronics" by hand before 023 ran has a different id for that slug, and
// products written against the literal would point at nothing.
func resolveCategories(ctx context.Context, q pgxQuerier, slugs []string) (map[string]uuid.UUID, error) {
	rows, err := q.Query(ctx,
		`SELECT slug, id FROM product_categories WHERE slug = ANY($1) AND is_active = TRUE`, slugs)
	if err != nil {
		return nil, fmt.Errorf("resolve categories: %w", err)
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var slug string
		var id uuid.UUID
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var missing []string
	for _, s := range slugs {
		if _, ok := out[s]; !ok {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("categories not found (has migration 023 run here?): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// pgxQuerier is the slice of pgx that resolveCategories needs, so it can
// run on the pool before the transaction opens or on the transaction itself.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// seed writes the catalogue and returns the tally. It is the whole job, kept
// separate from main so the integration test can call it twice and assert
// the second run inserts nothing.
func seed(ctx context.Context, pool *pgxpool.Pool, cat demoCatalogue) (*summary, error) {
	if err := cat.validate(); err != nil {
		return nil, err
	}
	categories, err := resolveCategories(ctx, pool, cat.categorySlugs())
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	// Rollback after a successful Commit is a no-op; this is the usual
	// "whatever happens, the transaction ends" shape.
	defer tx.Rollback(ctx)

	sum := newSummary()
	now := time.Now()

	if err := seedSeller(ctx, tx, cat.Seller, sum); err != nil {
		return nil, err
	}
	for i, p := range cat.Products {
		// Spread created_at and published_at an hour apart, newest first in
		// the list, so "New arrivals" has a stable order instead of sixteen
		// rows tied on the same NOW(). The list order is the arrival order.
		at := now.Add(-time.Duration(i) * time.Hour)
		if err := seedProduct(ctx, tx, cat.Seller.ID, categories[p.CategorySlug], p, at, sum); err != nil {
			return nil, fmt.Errorf("product %s: %w", p.Slug, err)
		}
	}
	for _, b := range cat.Banners {
		if err := seedBanner(ctx, tx, b, sum); err != nil {
			return nil, fmt.Errorf("banner %q: %w", b.Title, err)
		}
	}

	// total_products is a denormalised counter the seller's own dashboard
	// reads. Recomputing it is idempotent, so it is not tallied.
	if _, err := tx.Exec(ctx, `
		UPDATE sellers
		   SET total_products = (SELECT COUNT(*) FROM products WHERE seller_id = sellers.id),
		       updated_at = NOW()
		 WHERE id = $1`, cat.Seller.ID); err != nil {
		return nil, fmt.Errorf("seller product count: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return sum, nil
}

// seedSeller writes the shop and its fulfilment defaults.
//
// ON CONFLICT (id): a different demo seller already holding this slug or
// user_id is not something to paper over with a no-op, because every
// product below would then FK to a seller row that does not exist. Letting
// the unique violation surface is the correct failure.
func seedSeller(ctx context.Context, tx pgx.Tx, s demoSeller, sum *summary) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO sellers
		    (id, user_id, seller_type, business_type, store_name, brand_name, legal_business_name,
		     slug, description, tagline, email, support_email, country, state, city,
		     verification_status, store_status, status, onboarding_step,
		     submitted_at, approved_at, is_featured, created_at, updated_at)
		VALUES ($1, $2, 'business', 'retailer', $3, $3, $3,
		        $4, $5, $6, $7, $7, 'IN', $8, $9,
		        'verified', 'active', 'approved', 7,
		        NOW(), NOW(), TRUE, NOW(), NOW())
		ON CONFLICT (id) DO NOTHING`,
		s.ID, s.UserID, s.StoreName, s.Slug, s.Description, s.Tagline, s.Email, s.State, s.City)
	if err != nil {
		return fmt.Errorf("seller: %w", err)
	}
	sum.record("sellers", tag.RowsAffected())

	// COD on and a two-day dispatch SLA: the checkout's payment-method
	// choices read this row, and a demo that can only be paid by card is a
	// demo nobody can complete on a dev stack with no gateway.
	tag, err = tx.Exec(ctx, `
		INSERT INTO seller_fulfillment_settings
		    (id, seller_id, delivery_modes, dispatch_sla_hours, return_supported, return_window_days, cod_enabled, updated_at)
		VALUES (gen_random_uuid(), $1, '{"platform"}', 48, TRUE, 7, TRUE, NOW())
		ON CONFLICT (seller_id) DO NOTHING`, s.ID)
	if err != nil {
		return fmt.Errorf("fulfilment settings: %w", err)
	}
	sum.record("seller_fulfillment_settings", tag.RowsAffected())
	return nil
}

// seedProduct writes the catalogue row, its offer, its variants and their
// stock, in that order, because each FK points at the one before it.
func seedProduct(ctx context.Context, tx pgx.Tx, sellerID, categoryID uuid.UUID, p demoProduct, at time.Time, sum *summary) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO products
		    (id, seller_id, category_id, title, short_title, slug, description, short_description,
		     brand_name, manufacturer_name, product_type, condition, sku_root,
		     status, visibility, approval_status, country_of_origin,
		     return_policy_type, return_policy_days, search_keywords,
		     avg_rating, review_count, view_count, is_featured,
		     source_image_url, published_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
		        $9, $9, 'physical', 'new', $10,
		        'active', 'public', 'approved', 'IN',
		        '7_days', 7, $11,
		        $12, $13, $14, $15,
		        $16, $17, $17, $17)
		ON CONFLICT (id) DO NOTHING`,
		p.ID, sellerID, categoryID, p.Title, p.ShortTitle, p.Slug, p.Description, p.ShortDescription,
		p.Brand, p.SKURoot, p.Keywords,
		p.AvgRating, p.ReviewCount, p.ViewCount, p.Featured,
		demoImageURL(p.Slug), at)
	if err != nil {
		return fmt.Errorf("products: %w", err)
	}
	sum.record("products", tag.RowsAffected())

	// The offer is what the storefront reads (productOfferJoin). Conflict on
	// (product_id, seller_id) rather than id: the offer's identity is the
	// pair, and the store's own insertOfferForProductTx converges on it too.
	tag, err = tx.Exec(ctx, `
		INSERT INTO product_offers
		    (id, product_id, seller_id, status, visibility, approval_status,
		     published_at, condition, handling_time_days, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'active', 'public', 'approved',
		        $3, 'new', 2, $3, $3)
		ON CONFLICT (product_id, seller_id) DO NOTHING`, p.ID, sellerID, at)
	if err != nil {
		return fmt.Errorf("product_offers: %w", err)
	}
	sum.record("product_offers", tag.RowsAffected())

	for _, v := range p.Variants {
		// offer_id is resolved by (product, seller) exactly as
		// linkVariantToOfferTx does, so the variant can never point at
		// another shop's offer. Both price columns are written from the
		// same paise figure; see the catalogue header for why.
		tag, err = tx.Exec(ctx, `
			INSERT INTO product_variants
			    (id, product_id, offer_id, sku, option_1_name, option_1_value,
			     mrp, selling_price, mrp_minor, selling_price_minor,
			     currency_code, status, weight_grams, created_at, updated_at)
			VALUES ($1, $2,
			        (SELECT id FROM product_offers WHERE product_id = $2 AND seller_id = $3),
			        $4, $5, $6,
			        $7, $8, $9, $10,
			        'INR', 'active', $11, $12, $12)
			ON CONFLICT (id) DO NOTHING`,
			v.ID, p.ID, sellerID,
			v.SKU, v.OptionName, v.OptionValue,
			rupees(v.MRPMinor), rupees(v.PriceMinor), v.MRPMinor, v.PriceMinor,
			v.WeightGrams, at)
		if err != nil {
			return fmt.Errorf("product_variants %s: %w", v.SKU, err)
		}
		sum.record("product_variants", tag.RowsAffected())

		// Keyed on variant_id, which is UNIQUE, so no fixed id is needed.
		tag, err = tx.Exec(ctx, `
			INSERT INTO inventory_items (id, variant_id, seller_id, total_qty, low_stock_alert, updated_at)
			VALUES (gen_random_uuid(), $1, $2, $3, 5, NOW())
			ON CONFLICT (variant_id) DO NOTHING`, v.ID, sellerID, v.Stock)
		if err != nil {
			return fmt.Errorf("inventory_items %s: %w", v.SKU, err)
		}
		sum.record("inventory_items", tag.RowsAffected())
	}
	return nil
}

// seedBanner writes one carousel card with no image; migration 024 explains
// why a seed must not invent a media id.
func seedBanner(ctx context.Context, tx pgx.Tx, b demoBanner, sum *summary) error {
	tag, err := tx.Exec(ctx, `
		INSERT INTO commerce_banners
		    (id, title, subtitle, image_media_id, target_type, target_id, position, active)
		VALUES ($1, $2, $3, NULL, $4, $5, $6, TRUE)
		ON CONFLICT (id) DO NOTHING`,
		b.ID, b.Title, b.Subtitle, b.TargetType, b.TargetID, b.Position)
	if err != nil {
		return fmt.Errorf("commerce_banners: %w", err)
	}
	sum.record("commerce_banners", tag.RowsAffected())
	return nil
}
