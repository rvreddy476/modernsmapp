package postgres

// The document search reads back — and the one definition of "visible".
//
// ─── WHY A READ-BACK AND NOT A FAT EVENT ────────────────────────────────
//
// commerce-service tells search-service that a listing became visible. It
// does NOT tell it what the listing says. The event carries ids; the
// consumer comes back here and asks what the product is right now.
//
// The alternative — putting title, price, category and stock in the event
// payload — is a copy of the catalogue frozen at publish time, and every
// way a message stream can misbehave turns that copy into a lie:
//
//	delayed      the seller edited the price while the message sat in a
//	             partition; the index gets the old one.
//	replayed     a DLQ replay a day later re-indexes yesterday's title over
//	             today's.
//	out of order two transitions land backwards and the index settles on
//	             whichever event happened to arrive last.
//
// A read-back has none of those failure modes, because there is only ever
// one answer to "what does this product say" and it is the one the database
// gives when asked. An event that arrives twice produces the same document.
// An event that arrives late produces the CURRENT document, not a stale one.
// An unpublish that overtakes its publish still ends with the index agreeing
// with the catalogue, because `Visible` below is read at read-back time and
// not inferred from which event type arrived.
//
// The cost is one query per transition rather than zero. Product approvals
// are a moderator-rate event, not a checkout-rate one.
//
// ─── WHAT IS IN THE DOCUMENT AND WHY ────────────────────────────────────
//
// Everything a result card renders and everything a filter or sort reads:
//
//	title / description / brand      what a query matches
//	seller id + store name           the shop attribution on the card
//	category, WITH ITS ANCESTORS     so a "Books" filter matches a listing
//	                                 filed under Books › Textbooks. Without
//	                                 the chain, every filter is exact-match
//	                                 on a leaf and the top-level categories
//	                                 a buyer actually clicks return nothing.
//	price range in MINOR units       paise, integers. A float rupee price
//	                                 sorts wrong at the boundaries and is
//	                                 the money bug this codebase has spent
//	                                 two migrations getting out of the
//	                                 catalogue.
//	stock                            so out-of-stock can be deranked or
//	                                 filtered without a second lookup.
//	image + rating                   the card.
//	attributes                       `products.attributes_doc` VERBATIM.
//
// The attributes are the projection, not a re-derivation. attributes_doc is
// rebuilt inside the same transaction as any value write
// (rebuildAttributesDocTx), so it cannot disagree with the typed rows; a
// second reader that re-joined `product_attributes` here would be a third
// opinion about the same values, and the one that drifts.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── The visibility rule, once ──────────────────────────────────────────

// ProductLifecycle is the three columns the shopper-facing visibility rule
// is made of, for one product.
type ProductLifecycle struct {
	ProductID      uuid.UUID `json:"product_id"`
	SellerID       uuid.UUID `json:"seller_id"`
	Status         string    `json:"status"`
	ApprovalStatus string    `json:"approval_status"`
}

// Visible reports whether buyers can see this listing.
//
// It is `productSummaryLive` in Go — the same rule the storefront SELECTs
// apply — and it is expressed here rather than repeated at the call sites
// so that a change to what "live" means moves both together. A publish
// decision that spelled the rule out for itself would be the fourth copy,
// and copies of this rule are how a rejected listing ends up in a search
// result.
func (l ProductLifecycle) Visible() bool {
	return l.Status == "active" && l.ApprovalStatus == "approved"
}

// GetProductLifecycle reads the three columns after a transition has
// committed.
//
// Deliberately a separate read rather than a value threaded out of each
// store transition: five transitions each returning their own idea of the
// resulting state is five places to get it wrong, and the whole point of
// syncOfferLifecycleTx (see productoffers.go) is that a transition writes
// the row and something else READS it. This is the same discipline one
// layer up.
func (s *Store) GetProductLifecycle(ctx context.Context, productID uuid.UUID) (*ProductLifecycle, error) {
	var l ProductLifecycle
	err := s.db.QueryRow(ctx,
		`SELECT id, seller_id, status, approval_status FROM products WHERE id = $1`,
		productID).Scan(&l.ProductID, &l.SellerID, &l.Status, &l.ApprovalStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProductNotFound
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// ─── The document ───────────────────────────────────────────────────────

// SearchDocCategory is one rung of the category chain.
//
// Ids AND names AND slugs, because the three are used for different things:
// the filter matches on id (stable), the breadcrumb renders the name
// (translatable), and a URL is built from the slug.
type SearchDocCategory struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Slug string    `json:"slug"`
}

// SearchDoc is one product as the search index should hold it.
//
// `Visible` is the field the consumer acts on. It is computed here from the
// product's own columns at the moment of the read, which is what makes the
// consumer's decision independent of which event type woke it up.
type SearchDoc struct {
	ProductID  uuid.UUID `json:"product_id"`
	SellerID   uuid.UUID `json:"seller_id"`
	SellerName string    `json:"seller_name,omitempty"`

	Visible        bool   `json:"visible"`
	Status         string `json:"status"`
	ApprovalStatus string `json:"approval_status"`

	Title            string `json:"title"`
	Description      string `json:"description,omitempty"`
	ShortDescription string `json:"short_description,omitempty"`
	BrandName        string `json:"brand_name,omitempty"`
	Condition        string `json:"condition,omitempty"`
	ProductType      string `json:"product_type,omitempty"`
	Slug             string `json:"slug,omitempty"`

	// CategoryID is the leaf the seller filed the product under.
	// CategoryPath is root-first and INCLUDES that leaf as its last rung,
	// so a consumer wanting "every category this product should answer a
	// filter for" reads exactly one field.
	CategoryID   *uuid.UUID          `json:"category_id,omitempty"`
	CategoryName string              `json:"category_name,omitempty"`
	CategoryPath []SearchDocCategory `json:"category_path"`

	// Minor units — paise. MinPriceMinor is what a price sort orders on and
	// what a price-range filter compares; MaxPriceMinor exists so a listing
	// whose variants span a range can render "from ₹X" honestly.
	MinPriceMinor int64  `json:"min_price_minor"`
	MaxPriceMinor int64  `json:"max_price_minor"`
	MRPMinor      int64  `json:"mrp_minor,omitempty"`
	Currency      string `json:"currency"`

	TotalStock int  `json:"total_stock"`
	InStock    bool `json:"in_stock"`

	ImageMediaID *uuid.UUID `json:"image_media_id,omitempty"`
	ImageURL     string     `json:"image_url,omitempty"`

	AvgRating   float64 `json:"avg_rating"`
	ReviewCount int     `json:"review_count"`
	OrderCount  int     `json:"order_count"`
	ViewCount   int     `json:"view_count"`

	// Attributes is products.attributes_doc as stored: definition CODE →
	// value, where a measure is {"value":…,"unit":…} and a multi_enum is
	// always an array. Codes, never labels — a label is presentation and
	// renaming one must not invalidate an index.
	Attributes map[string]any `json:"attributes"`

	SearchKeywords []string   `json:"search_keywords,omitempty"`
	PublishedAt    *time.Time `json:"published_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// searchDocColumns is the one SELECT list for the document.
//
// The COALESCE(NULLIF(...)) on the minor columns is the shape the rest of
// this package uses and is not optional: migration 007 defaulted them to 0
// rather than NULL, so a plain COALESCE finds a non-NULL zero on an
// unmigrated row and would index a paid product as free.
const searchDocColumns = `
	p.id, p.seller_id, sl.store_name,
	p.status, p.approval_status,
	p.title, p.description, p.short_description, p.brand_name, p.condition,
	p.product_type, p.slug,
	p.category_id, pc.name AS category_name,
	COALESCE(pr.min_price_minor, 0)::bigint,
	COALESCE(pr.max_price_minor, 0)::bigint,
	COALESCE(pr.mrp_minor, 0)::bigint,
	COALESCE(pr.currency_code, 'INR'),
	COALESCE(st.total_stock, 0),
	COALESCE(p.primary_image_media_id, (
		SELECT pm.media_id FROM product_media pm
		 WHERE pm.product_id = p.id AND pm.media_type = 'image'
		 ORDER BY pm.sort_order ASC, pm.created_at ASC LIMIT 1
	)) AS image_media_id,
	p.source_image_url,
	p.avg_rating, p.review_count, p.order_count, p.view_count,
	p.attributes_doc,
	p.search_keywords,
	p.published_at, p.created_at, p.updated_at`

// searchDocFrom supplies every alias searchDocColumns reads.
//
// The price roll-up is one LATERAL over the ACTIVE variants — active,
// because an archived variant is a price a buyer cannot pay and letting it
// set min_price_minor puts a listing at the top of a cheapest-first sort
// for an offer that no longer exists.
const searchDocFrom = `
	FROM products p
	JOIN sellers sl ON sl.id = p.seller_id
	LEFT JOIN product_categories pc ON pc.id = p.category_id
	LEFT JOIN LATERAL (
		SELECT MIN(COALESCE(NULLIF(v.selling_price_minor, 0), ROUND(v.selling_price*100)))::bigint AS min_price_minor,
		       MAX(COALESCE(NULLIF(v.selling_price_minor, 0), ROUND(v.selling_price*100)))::bigint AS max_price_minor,
		       MIN(COALESCE(NULLIF(v.mrp_minor, 0),           ROUND(v.mrp*100)))::bigint           AS mrp_minor,
		       MIN(v.currency_code)                                                                AS currency_code
		  FROM product_variants v
		 WHERE v.product_id = p.id AND v.status = 'active'
	) pr ON true
	LEFT JOIN LATERAL (
		SELECT SUM(GREATEST(i.total_qty - i.reserved_qty, 0))::int AS total_stock
		  FROM product_variants pv
		  JOIN inventory_items i ON i.variant_id = pv.id
		 WHERE pv.product_id = p.id AND pv.status = 'active'
	) st ON true`

func scanSearchDoc(row rowScanner) (*SearchDoc, error) {
	var d SearchDoc
	var attrs []byte
	var description, shortDescription, brandName, condition, productType *string
	var storeName, categoryName, sourceImageURL *string
	if err := row.Scan(
		&d.ProductID, &d.SellerID, &storeName,
		&d.Status, &d.ApprovalStatus,
		&d.Title, &description, &shortDescription, &brandName, &condition,
		&productType, &d.Slug,
		&d.CategoryID, &categoryName,
		&d.MinPriceMinor, &d.MaxPriceMinor, &d.MRPMinor, &d.Currency,
		&d.TotalStock,
		&d.ImageMediaID, &sourceImageURL,
		&d.AvgRating, &d.ReviewCount, &d.OrderCount, &d.ViewCount,
		&attrs,
		&d.SearchKeywords,
		&d.PublishedAt, &d.CreatedAt, &d.UpdatedAt,
	); err != nil {
		return nil, err
	}
	d.SellerName = derefStr(storeName)
	d.Description = derefStr(description)
	d.ShortDescription = derefStr(shortDescription)
	d.BrandName = derefStr(brandName)
	d.Condition = derefStr(condition)
	d.ProductType = derefStr(productType)
	d.CategoryName = derefStr(categoryName)
	d.ImageURL = derefStr(sourceImageURL)
	d.InStock = d.TotalStock > 0
	d.Visible = ProductLifecycle{Status: d.Status, ApprovalStatus: d.ApprovalStatus}.Visible()

	// An empty doc is `{}`, never nil: a consumer that has to distinguish
	// "no attributes" from "the field was absent" would have to know which
	// of the two this service means, and there is no useful difference.
	d.Attributes = map[string]any{}
	if len(attrs) > 0 {
		if err := json.Unmarshal(attrs, &d.Attributes); err != nil {
			return nil, err
		}
	}
	d.CategoryPath = []SearchDocCategory{}
	return &d, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ProductSearchDoc reads one product's search document, ancestors included.
//
// Two queries, not one: folding the recursive category walk into the
// product SELECT would run it per row when the list form below reads a
// page, and the chains repeat heavily across a page of products from the
// same department. `attachCategoryPaths` resolves each distinct category
// once.
func (s *Store) ProductSearchDoc(ctx context.Context, productID uuid.UUID) (*SearchDoc, error) {
	row := s.db.QueryRow(ctx, `SELECT `+searchDocColumns+searchDocFrom+` WHERE p.id = $1`, productID)
	d, err := scanSearchDoc(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrProductNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := s.attachCategoryPaths(ctx, []*SearchDoc{d}); err != nil {
		return nil, err
	}
	return d, nil
}

// ListProductSearchDocs walks the catalogue for a reindex, keyset-paged on
// (created_at, id).
//
// Keyset and not OFFSET: a reindex of a live catalogue that pages by offset
// skips or repeats rows every time a product is inserted underneath it, and
// the whole point of a reindex is that afterwards you can say what is in
// the index.
//
// `visibleOnly` is what a search reindex passes. The false form exists for
// an operator asking "what would the index hold" about a listing that is
// not live.
func (s *Store) ListProductSearchDocs(
	ctx context.Context, visibleOnly bool, afterCreatedAt *time.Time, afterID *uuid.UUID, limit int,
) ([]*SearchDoc, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	where := "WHERE ($1 OR " + productSummaryLive + ")"
	args := []any{!visibleOnly}
	if afterCreatedAt != nil && afterID != nil {
		where += " AND (p.created_at, p.id) > ($2, $3)"
		args = append(args, *afterCreatedAt, *afterID)
	}
	rows, err := s.db.Query(ctx,
		`SELECT `+searchDocColumns+searchDocFrom+` `+where+
			` ORDER BY p.created_at ASC, p.id ASC LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*SearchDoc{}
	for rows.Next() {
		d, err := scanSearchDoc(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachCategoryPaths(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// CountVisibleProducts is what a reindex reports against.
func (s *Store) CountVisibleProducts(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM products p WHERE `+productSummaryLive).Scan(&n)
	return n, err
}

// attachCategoryPaths fills CategoryPath on every doc, resolving each
// distinct category chain once.
//
// Root-first, leaf LAST — the order a breadcrumb is read in and the order
// a consumer wants when it takes the tail as "the category" and the whole
// slice as "everything this product should match".
func (s *Store) attachCategoryPaths(ctx context.Context, docs []*SearchDoc) error {
	want := map[uuid.UUID]struct{}{}
	for _, d := range docs {
		if d.CategoryID != nil {
			want[*d.CategoryID] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}

	// One recursive walk for every leaf at once. `leaf` is carried down the
	// recursion so each row knows which chain it belongs to; depth 0 is the
	// leaf itself and the final ORDER BY depth DESC puts the root first.
	//
	// The depth < 32 guard is the one the rest of this package uses: a
	// category tree with a cycle (parent_id is a self-reference with no
	// constraint behind it) would otherwise recurse until the connection
	// died, and it would do so during a reindex of the whole catalogue.
	rows, err := s.db.Query(ctx, `
		WITH RECURSIVE chain AS (
		    SELECT c.id AS leaf, c.id, c.parent_id, c.name, c.slug, 0 AS depth
		      FROM product_categories c
		     WHERE c.id = ANY($1::uuid[])
		    UNION ALL
		    SELECT ch.leaf, p.id, p.parent_id, p.name, p.slug, ch.depth + 1
		      FROM product_categories p
		      JOIN chain ch ON p.id = ch.parent_id
		     WHERE ch.depth < 32
		)
		SELECT leaf, id, name, slug FROM chain ORDER BY leaf, depth DESC`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()

	paths := map[uuid.UUID][]SearchDocCategory{}
	for rows.Next() {
		var leaf uuid.UUID
		var rung SearchDocCategory
		if err := rows.Scan(&leaf, &rung.ID, &rung.Name, &rung.Slug); err != nil {
			return err
		}
		paths[leaf] = append(paths[leaf], rung)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, d := range docs {
		if d.CategoryID == nil {
			continue
		}
		if p, ok := paths[*d.CategoryID]; ok {
			d.CategoryPath = p
		}
	}
	return nil
}

// ─── Facet definitions ──────────────────────────────────────────────────

// FacetDefinition is one attribute definition that a search surface may
// build a facet from, with its options.
//
// `is_filterable` on the definition is the entire gate. It is an operator's
// switch in the admin console, so turning a field into a facet — or off
// again — is a no-deploy change; a hardcoded list of facetable codes in
// search-service would make it a release.
//
// Both codes and labels travel. The code is the stable key a filter is
// expressed in and an index is written with; the label is what a human
// reads and may be re-worded at any time. A facet response that carried
// only labels would make renaming a field break every saved filter, and one
// that carried only codes would make search-service invent presentation it
// has no authority over.
type FacetDefinition struct {
	Code       string        `json:"code"`
	Label      string        `json:"label"`
	DataType   string        `json:"data_type"`
	UnitFamily *string       `json:"unit_family,omitempty"`
	Group      string        `json:"display_group"`
	AppliesTo  string        `json:"applies_to"`
	Options    []FacetOption `json:"options"`
}

// FacetOption is one enum option — code and label, same reasoning.
type FacetOption struct {
	Code      string  `json:"code"`
	Label     string  `json:"label"`
	SwatchHex *string `json:"swatch_hex,omitempty"`
	SortOrder int     `json:"sort_order"`
}

// FacetDefinitions returns the ACTIVE, FILTERABLE definitions with their
// active options, ordered the way a filter rail should draw them.
//
// Inactive definitions are excluded here where AttributeDefinitionsByCodes
// includes them, and the difference is deliberate: that one resolves codes a
// WRITE named and must still accept a retired field a product already
// answered; this one builds the questions a BUYER is offered, and offering a
// retired one is offering a filter the catalogue is no longer being told to
// fill in.
func (s *Store) FacetDefinitions(ctx context.Context) ([]*FacetDefinition, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.code, d.label, d.data_type, d.unit_family, d.display_group, d.applies_to
		  FROM attribute_definitions d
		 WHERE d.is_active AND d.is_filterable
		 ORDER BY d.display_group, d.code`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []*FacetDefinition{}
	byCode := map[string]*FacetDefinition{}
	for rows.Next() {
		f := &FacetDefinition{Options: []FacetOption{}}
		if err := rows.Scan(&f.Code, &f.Label, &f.DataType, &f.UnitFamily, &f.Group, &f.AppliesTo); err != nil {
			return nil, err
		}
		out = append(out, f)
		byCode[f.Code] = f
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	optRows, err := s.db.Query(ctx, `
		SELECT d.code, e.code, e.label, e.swatch_hex, e.sort_order
		  FROM attribute_enum_values e
		  JOIN attribute_definitions d ON d.id = e.definition_id
		 WHERE d.is_active AND d.is_filterable AND e.is_active
		 ORDER BY d.code, e.sort_order, e.code`)
	if err != nil {
		return nil, err
	}
	defer optRows.Close()
	for optRows.Next() {
		var defCode string
		var o FacetOption
		if err := optRows.Scan(&defCode, &o.Code, &o.Label, &o.SwatchHex, &o.SortOrder); err != nil {
			return nil, err
		}
		if f, ok := byCode[defCode]; ok {
			f.Options = append(f.Options, o)
		}
	}
	return out, optRows.Err()
}
