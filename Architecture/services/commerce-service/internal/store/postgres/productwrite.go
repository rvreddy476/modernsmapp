package postgres

// The product WRITE path: creating one atomically, and patching one through a
// typed allowlist.
//
// ─── WHY THIS FILE EXISTS AT ALL ────────────────────────────────────────
//
// Two defects, both in the same few lines of Service.CreateProduct, and both
// invisible from either end:
//
//  1. THE CREATE WAS NOT TRANSACTIONAL. The product was inserted, then each
//     variant, then each variant's inventory row — three separate statements
//     on three separate round trips, in a loop, with the inventory failure
//     swallowed into a slog.Warn. So a create that failed on the second
//     variant left the product and the first variant standing, and a create
//     whose inventory insert failed left a variant with NO stock row at all.
//     Neither state is reachable through any UI. The first is a listing the
//     seller never finished and cannot see the remains of; the second is a
//     listing that renders, sits in a cart, and refuses at checkout because
//     `available_qty` reads NULL. The seller was told the create succeeded.
//
//  2. THE PATCH DID NOT EXIST, and the store method that would have served
//     it — `UpdateProduct(ctx, id, map[string]any)` — had zero callers and
//     interpolated the caller's map KEYS straight into the SQL as column
//     names:
//
//     sets = append(sets, fmt.Sprintf("%s=$%d", k, i))
//
//     Values were parameterised; identifiers were not. Any caller wired to
//     it would have been one un-audited map away from writing
//     `approval_status`, `seller_id` or `view_count` — the map cannot say
//     which keys are permitted, so every caller would have had to remember
//     the whole list. That method is gone. `ProductPatch` below is the
//     replacement: a struct whose fields ARE the allowlist, so a column that
//     is not on it cannot be named, let alone written.
//
// ─── ONE COPY OF EACH INSERT ────────────────────────────────────────────
//
// The transactional path does not get its own second copy of the product,
// variant and inventory SQL. Each statement lives once, in a `…Tx` helper
// taking a pgx.Tx, and the pre-existing non-transactional methods
// (CreateProduct, CreateVariant, UpsertInventory) open a one-statement
// transaction and call the same helper. A second copy is how the list of
// columns in CreateVariant lost `selling_price_minor` once already.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrDuplicateSKU is returned when a create names a SKU this listing already
// uses.
//
// This is the single most likely way a real create fails halfway — a seller
// pasting a code they have already used. Before the atomic create it produced
// a half-built listing AND an unmapped 500, so the seller was told the service
// was broken rather than that their SKU was taken.
//
// The GRAIN of "already in use" changed in migration 031, and it is worth
// being exact about, because the obvious reading is wrong. The rule was
// `UNIQUE(sku)` — global across the catalogue, so a seller could be refused a
// code because a shop they have never heard of used it first, a refusal with
// no honest explanation. It is now
// `UNIQUE NULLS NOT DISTINCT (offer_id, sku)`, and an offer is ONE SHOP'S
// LISTING OF ONE ITEM. So the rule is per LISTING, not per shop and not per
// catalogue:
//
//	two shops, same code                 allowed. This is the point.
//	one shop, two listings, same code    allowed. Wider than the case above
//	                                     needed; see the migration.
//	one listing, two variants, same code REFUSED, and this is that error.
//
// Which means this message is now only true of the third row. It is left as
// it is because it is what the seller filling in a variant form needs to
// read, and the form they are filling in is one listing.
var ErrDuplicateSKU = errors.New("commerce: that SKU is already in use")

// asDuplicateSKU recognises the unique violation on the SKU index.
//
// Matched on the constraint name rather than on 23505 alone: several unique
// indexes sit on this write path (the product slug, the typed-attribute
// index), and reporting any of them as "your SKU is taken" would send the
// seller to change the one field that was fine.
//
// The substring is "sku", not the constraint's full name, and that is what
// carried this through 031: the index was renamed from
// `product_variants_sku_key` to `product_variants_offer_sku_key`, and a
// matcher pinned to the old full name would have started reporting a
// duplicate SKU as a 500 with nothing failing in any test.
func asDuplicateSKU(err error, sku string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		strings.Contains(pgErr.ConstraintName, "sku") {
		return fmt.Errorf("%w: %s", ErrDuplicateSKU, sku)
	}
	return err
}

// ─── The atomic create ──────────────────────────────────────────────────

// NewVariant is one variant plus the opening stock its inventory row is
// created with.
//
// The stock quantity travels WITH the variant rather than in a parallel
// slice, because the two are one fact — "this SKU, this many units" — and a
// parallel slice is how a reordering bug gives one variant another's stock.
type NewVariant struct {
	Variant  *ProductVariant
	StockQty int

	// Options are this variant's values on the product's declared axes, as
	// CODES. Empty for a product that does not vary, which is most of them.
	//
	// Here rather than in a parallel slice for the same reason StockQty is:
	// "this SKU, size L, colour blue" is one fact, and a parallel slice is
	// how a reordering bug gives one variant another's combination — which,
	// unlike a swapped stock count, is invisible until a buyer opens the
	// parcel.
	Options []VariantOption
}

// NewProduct is everything one create writes, as one value.
//
// Attributes are here rather than in a follow-up call for the same reason
// the variants are: a product whose category asks fourteen questions and
// whose answers landed in a second, unrelated transaction is a product that
// can exist with half its answers, and nothing downstream can tell that from
// a seller who only filled in half the form.
type NewProduct struct {
	Product    *Product
	Variants   []NewVariant
	Attributes []AttributeValueSet

	// Axes are what this product varies on, in order. They are written
	// BEFORE the variants, and they have to be: a variant's options carry a
	// composite foreign key to the axis rows, so a variant inserted first
	// could not carry an option at all.
	Axes []VariationAxis
}

// CreateProductAtomic writes the product, every variant, every variant's
// inventory row and every typed attribute value in ONE transaction.
//
// Either the whole listing exists or none of it does. There is no early
// return in here that can leave a row behind: the deferred Rollback covers
// every one of them, including the ones a future edit adds.
//
// The inventory insert is a hard failure, not a warning. A variant with no
// inventory row is not "a variant with unknown stock" — every read in this
// package derives availability from `inventory_items`, so it is a variant the
// storefront shows and the checkout refuses. Swallowing that error is what
// produced listings nobody could buy and no log line anybody read.
func (s *Store) CreateProductAtomic(ctx context.Context, in NewProduct) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := insertProductTx(ctx, tx, in.Product); err != nil {
		return fmt.Errorf("create product: %w", err)
	}
	// Axes before variants, because a variant's options reference them
	// through a composite foreign key. See NewProduct.Axes.
	for _, a := range in.Axes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO product_variation_axes (product_id, definition_id, position)
			VALUES ($1, $2, $3)`, in.Product.ID, a.DefinitionID, a.Position); err != nil {
			return fmt.Errorf("declare variation axis: %w", asVariationConflict(err))
		}
	}
	for _, nv := range in.Variants {
		nv.Variant.ProductID = in.Product.ID
		if err := insertVariantTx(ctx, tx, nv.Variant); err != nil {
			return fmt.Errorf("create variant %s: %w", nv.Variant.SKU, asDuplicateSKU(err, nv.Variant.SKU))
		}
		if err := upsertInventoryTx(ctx, tx, nv.Variant.ID, in.Product.SellerID, nv.StockQty); err != nil {
			return fmt.Errorf("open inventory for variant %s: %w", nv.Variant.SKU, err)
		}
		// After the variant AND after linkVariantToOfferTx inside it: the
		// `variation_key` the option trigger derives is UNIQUE per OFFER, so
		// a variant whose offer_id was still NULL when the options landed
		// would escape the uniqueness check entirely and sit in the
		// catalogue as a second "Blue / M" nobody refused.
		if len(nv.Options) > 0 {
			if err := insertVariantOptionsTx(ctx, tx, nv.Variant.ID, in.Product.ID, nv.Options); err != nil {
				return fmt.Errorf("set options for variant %s: %w", nv.Variant.SKU, err)
			}
		}
	}
	if len(in.Attributes) > 0 {
		if err := putAttributeValuesTx(ctx, tx, in.Product.ID, in.Attributes); err != nil {
			return fmt.Errorf("store attribute values: %w", err)
		}
	}
	return tx.Commit(ctx)
}

// ─── The patch ──────────────────────────────────────────────────────────

// ProductPatch is the allowlist.
//
// Every field is a pointer, and nil means "the caller did not mention this".
// That is the distinction a `map[string]any` could express and a plain struct
// cannot: `{"description": null}` clears the description, and omitting
// `description` leaves it alone. Both are legitimate and they are not the
// same request.
//
// WHAT IS DELIBERATELY ABSENT, and why each one:
//
//	seller_id            ownership. A patch that could move a listing to
//	                     another shop is a catalogue takeover.
//	slug                 the URL. Live links, saved carts and search results
//	                     all key on it; changing it is a redirect problem,
//	                     not a form field.
//	approval_status      the review outcome. It is written by the moderation
//	                     routes and by the revalidation rule in the service
//	                     layer — never by a seller naming a column.
//	rejection_reason     the reviewer's words about this seller's listing.
//	moderation_flags     ditto.
//	status / visibility  the publish switch. An approved listing going dark,
//	                     or a draft going live, is a lifecycle transition
//	                     with its own gate (submit-for-review), not a side
//	                     effect of editing the description. The service layer
//	                     writes `status` in exactly one case — the
//	                     revalidation bounce below — and says so in the
//	                     response.
//	published_at         derived from the transition above.
//	schema_version       stamped by the write path from the PUBLISHED schema
//	                     version, so it records which vintage of the form
//	                     produced these values. A caller choosing it could
//	                     claim its values were checked against bounds that
//	                     never saw them.
//	attributes_doc       a projection, rebuilt inside the value write.
//	source_image_url,    importer-owned identity columns.
//	gtin
//	avg_rating,          counters. Every one is maintained by the surface
//	review_count,        that owns it, and a seller who could set their own
//	order_count,         rating has a five-star shop by lunchtime.
//	view_count,
//	wishlist_count,
//	is_featured          merchandising, an operator's decision.
type ProductPatch struct {
	CategoryID       *uuid.UUID
	BrandID          *uuid.UUID
	TaxClassID       *uuid.UUID
	Title            *string
	ShortTitle       *string
	Description      *string
	ShortDescription *string
	BrandName        *string
	ManufacturerName *string
	ProductType      *string
	Condition        *string

	PrimaryImageMediaID *uuid.UUID
	VideoMediaID        *uuid.UUID

	WeightGrams     *int
	LengthCm        *float64
	WidthCm         *float64
	HeightCm        *float64
	CountryOfOrigin *string
	WarrantyInfo    *string

	ReturnPolicyType *string
	ReturnPolicyDays *int
	HSNCode          *string

	SearchKeywords  *[]string
	MetaTitle       *string
	MetaDescription *string

	// ── Not client-settable ──────────────────────────────────
	//
	// These are below the line: the HTTP layer never fills them. They are
	// how the SERVICE expresses the two things a patch legitimately changes
	// beyond the seller's own copy, and they live on the same struct so
	// there is still exactly one place that names a product column.

	// SchemaVersion stamps `products.schema_version` from the published
	// attribute-schema version at the moment of the write.
	SchemaVersion *int

	// Revalidate sends an approved product back to review:
	// approval_status='submitted', status='draft', published_at=NULL. Set
	// only by the service, only when the caller acknowledged it.
	Revalidate bool
}

// nullableClears lists the fields whose JSON null is a legitimate "clear
// this". Documented here rather than per-field so the set is readable as a
// set: everything nullable in the schema, and nothing that is NOT NULL.
//
// title, product_type, condition, return_policy_type and return_policy_days
// are NOT NULL columns, so a null for them is a bad request rather than a
// clear — the HTTP layer refuses it before it reaches here.

// productPatchColumns maps each patch field to its column and value.
//
// The column names are STRING CONSTANTS in this function and nowhere else.
// That is the whole difference from what this replaces: there is no path by
// which a caller-supplied string becomes an identifier in the statement.
func (p ProductPatch) assignments() ([]string, []any) {
	sets := []string{}
	args := []any{}
	add := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s=$%d", col, len(args)))
	}

	if p.CategoryID != nil {
		add("category_id", *p.CategoryID)
	}
	if p.BrandID != nil {
		add("brand_id", *p.BrandID)
	}
	if p.TaxClassID != nil {
		add("tax_class_id", *p.TaxClassID)
	}
	if p.Title != nil {
		add("title", *p.Title)
	}
	if p.ShortTitle != nil {
		add("short_title", nilIfEmpty(*p.ShortTitle))
	}
	if p.Description != nil {
		add("description", nilIfEmpty(*p.Description))
	}
	if p.ShortDescription != nil {
		add("short_description", nilIfEmpty(*p.ShortDescription))
	}
	if p.BrandName != nil {
		add("brand_name", nilIfEmpty(*p.BrandName))
	}
	if p.ManufacturerName != nil {
		add("manufacturer_name", nilIfEmpty(*p.ManufacturerName))
	}
	if p.ProductType != nil {
		add("product_type", *p.ProductType)
	}
	if p.Condition != nil {
		add("condition", *p.Condition)
	}
	if p.PrimaryImageMediaID != nil {
		add("primary_image_media_id", *p.PrimaryImageMediaID)
	}
	if p.VideoMediaID != nil {
		add("video_media_id", *p.VideoMediaID)
	}
	if p.WeightGrams != nil {
		add("weight_grams", *p.WeightGrams)
	}
	if p.LengthCm != nil {
		add("length_cm", *p.LengthCm)
	}
	if p.WidthCm != nil {
		add("width_cm", *p.WidthCm)
	}
	if p.HeightCm != nil {
		add("height_cm", *p.HeightCm)
	}
	if p.CountryOfOrigin != nil {
		add("country_of_origin", nilIfEmpty(*p.CountryOfOrigin))
	}
	if p.WarrantyInfo != nil {
		add("warranty_info", nilIfEmpty(*p.WarrantyInfo))
	}
	if p.ReturnPolicyType != nil {
		add("return_policy_type", *p.ReturnPolicyType)
	}
	if p.ReturnPolicyDays != nil {
		add("return_policy_days", *p.ReturnPolicyDays)
	}
	if p.HSNCode != nil {
		add("hsn_code", nilIfEmpty(*p.HSNCode))
	}
	if p.SearchKeywords != nil {
		add("search_keywords", *p.SearchKeywords)
	}
	if p.MetaTitle != nil {
		add("meta_title", nilIfEmpty(*p.MetaTitle))
	}
	if p.MetaDescription != nil {
		add("meta_description", nilIfEmpty(*p.MetaDescription))
	}
	if p.SchemaVersion != nil {
		add("schema_version", *p.SchemaVersion)
	}
	if p.Revalidate {
		// Written as literals, not parameters: these three are not values a
		// caller chose, they are the fixed consequence of the revalidation
		// rule. See Service.UpdateProduct for the reasoning behind the rule
		// itself.
		sets = append(sets,
			"approval_status='submitted'",
			"status='draft'",
			"published_at=NULL")
	}
	return sets, args
}

// TouchesAnyColumn reports whether the patch would write anything at all.
func (p ProductPatch) TouchesAnyColumn() bool {
	sets, _ := p.assignments()
	return len(sets) > 0
}

// nilIfEmpty turns "" into a SQL NULL for the nullable text columns.
//
// A seller who cleared the warranty box sends "", and storing the empty
// string leaves `warranty_info <> NULL` true — so every "does this product
// have a warranty note?" read answers yes and renders an empty paragraph.
func nilIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// PatchProduct applies an allowlisted patch and, in the same transaction,
// replaces the named attribute values.
//
// One transaction because the two halves are one edit. A patch whose columns
// landed and whose attribute values did not is a product whose title says one
// thing and whose spec block says another, and the seller has no way to tell
// which half they are looking at.
//
// `attrs` may be empty, in which case nothing in `product_attributes` is
// touched — not even to rebuild the doc, which cannot have changed.
//
// `variation` may be nil, which means "this patch does not mention the
// matrix" and is the case for every patch written before 028. A non-nil one
// REPLACES the whole picture — see VariationUpdate for why a partial change
// to a product's axes has no honest meaning.
func (s *Store) PatchProduct(ctx context.Context, id uuid.UUID, p ProductPatch,
	attrs []AttributeValueSet, variation *VariationUpdate) error {
	sets, args := p.assignments()
	if len(sets) == 0 && len(attrs) == 0 && variation == nil {
		return nil
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if len(sets) > 0 {
		args = append(args, id)
		tag, err := tx.Exec(ctx,
			"UPDATE products SET "+strings.Join(sets, ",")+
				",updated_at=NOW() WHERE id=$"+fmt.Sprint(len(args)), args...)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrProductNotFound
		}
		// The offer's copy of whatever this patch just changed. Run for ANY
		// column set, not only for Revalidate: `condition` is on the patch's
		// allowlist and is an offer column too, and a sync predicated on the
		// revalidation flag would miss it. The sync reads the product row, so
		// running it after a patch that changed nothing it copies is a
		// no-op UPDATE, not a wrong one.
		if err := syncOfferLifecycleTx(ctx, tx, id); err != nil {
			return err
		}
	}
	if len(attrs) > 0 {
		if err := putAttributeValuesTx(ctx, tx, id, attrs); err != nil {
			return err
		}
	}
	// In the SAME transaction as the columns above, for the reason the
	// method comment gives about the attribute half: a patch whose axes
	// landed and whose variant options did not is a product declaring a
	// matrix its variants do not fill, and the seller has no way to tell
	// which half they are looking at.
	if variation != nil {
		if err := replaceVariationTx(ctx, tx, id, *variation); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// ─── The statements, once each ──────────────────────────────────────────

// insertProductTx is the ONLY INSERT into `products` in this package.
//
// `schema_version` is on the column list, and it is not decoration: migration
// 026 added it as "which published attribute-schema version this product's
// values were last validated against", defaulting to 0 for the estate that
// predates any validation. A create that left it at 0 would be claiming its
// values were never checked, which is exactly the signal a later
// reconciliation pass reads to decide what it must re-examine.
func insertProductTx(ctx context.Context, tx pgx.Tx, p *Product) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	now := time.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	_, err := tx.Exec(ctx, `
		INSERT INTO products (id,seller_id,category_id,brand_id,tax_class_id,title,short_title,slug,description,
		  short_description,brand_name,manufacturer_name,product_type,condition,sku_root,status,visibility,approval_status,
		  primary_image_media_id,video_media_id,weight_grams,length_cm,width_cm,height_cm,
		  country_of_origin,warranty_info,return_policy_type,return_policy_days,
		  hsn_code,search_keywords,meta_title,meta_description,schema_version,created_at,updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35)`,
		p.ID, p.SellerID, p.CategoryID, p.BrandID, p.TaxClassID, p.Title, p.ShortTitle, p.Slug, p.Description,
		p.ShortDescription, p.BrandName, p.ManufacturerName, p.ProductType, p.Condition, p.SKURoot, p.Status, p.Visibility, p.ApprovalStatus,
		p.PrimaryImageMediaID, p.VideoMediaID, p.WeightGrams, p.LengthCm, p.WidthCm, p.HeightCm,
		p.CountryOfOrigin, p.WarrantyInfo, p.ReturnPolicyType, p.ReturnPolicyDays,
		p.HSNCode, p.SearchKeywords, p.MetaTitle, p.MetaDescription, p.SchemaVersion, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return err
	}

	// ── The offer, in the same statement pair ────────────────
	//
	// Migration 027 split the seller's OFFER — status, visibility,
	// approval_status, published_at, condition — out of the catalogue row.
	// Nothing reads it yet; everything writes it, starting here.
	//
	// It is written HERE rather than at the two create call sites for the
	// reason the file header gives about the variant columns: a second copy
	// of a write is how the first one loses a column. There is no statement
	// other than this one that can put a row in `products`, so there is no
	// path by which a product comes into existence without its offer.
	return insertOfferForProductTx(ctx, tx, p)
}

// upsertInventoryTx opens (or resets) a variant's stock row.
func upsertInventoryTx(ctx context.Context, tx pgx.Tx, variantID, sellerID uuid.UUID, totalQty int) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO inventory_items (id,variant_id,seller_id,total_qty,updated_at)
		VALUES (gen_random_uuid(),$1,$2,$3,NOW())
		ON CONFLICT (variant_id) DO UPDATE SET total_qty=$3, updated_at=NOW()`,
		variantID, sellerID, totalQty,
	)
	return err
}

// ─── Editability ────────────────────────────────────────────────────────

// ProductEditability answers "may this product be patched, and does patching
// it cost the seller their approval".
//
// The vocabulary is the one migration 022 settled on and gated 1001 closes:
//
//	draft, submitted, under_review, pending, approved, rejected,
//	flagged, changes_requested, hidden, archived
//
// The rule, state by state, and WHY:
//
//	draft                editable. This is the state a create leaves behind
//	                     and the whole point of a draft.
//	pending              editable. The schema's DEFAULT, so it is what every
//	                     product created before the code started writing
//	                     'draft' carries. Refusing it would strand that half
//	                     of the estate with no way to fix a listing.
//	rejected             editable. "Fix it and resubmit" is the only useful
//	changes_requested    response to either of these, and refusing the edit
//	flagged              leaves the seller with a listing they have been told
//	                     to correct and no way to correct it.
//	submitted            REFUSED. A reviewer is looking at it right now. A
//	under_review         body that changes underneath them means the approval
//	                     they grant is for text that is no longer there —
//	                     which is a moderation bypass, not a race.
//	approved             editable, at a price. See below.
//	hidden               REFUSED. An operator took it down. Letting the
//	                     seller edit their way back into the queue routes
//	                     around whoever hid it.
//	archived             REFUSED. Retired, and referenced by order history.
//	                     A new listing is the honest way to sell it again.
func ProductEditability(approvalStatus string) (editable bool, needsRevalidation bool) {
	switch approvalStatus {
	case "draft", "pending", "rejected", "changes_requested", "flagged":
		return true, false
	case "approved":
		return true, true
	default: // submitted, under_review, hidden, archived, and anything new
		return false, false
	}
}
