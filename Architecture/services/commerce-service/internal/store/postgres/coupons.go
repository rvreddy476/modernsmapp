package postgres

// MStore coupons (migration 038): what a coupon is worth, whether it applies,
// how capacity is claimed, and how sellers and the admin console write them.
//
// Founder, 1 Oct 2026: seller coupons now; platform coupons built behind
// COMMERCE_PLATFORM_COUPONS_ENABLED, which stays OFF until the tax adviser
// confirms GST on platform-funded discounts. While it is off a platform code
// is refused (ErrCouponNotAvailable) before any capacity is claimed, by the
// quote and by checkout alike, and nothing about the order changes.
//
// ─── THE ONE ARITHMETIC ─────────────────────────────────────────────────
//
// CouponDiscountMinor is the only place a coupon's value is computed. The
// quote, the checkout, the bag's coupon list and the product page's
// best_coupon all call it, in integer paise:
//
//	percentage  floor(base × bps / 10000), then capped by max_discount_minor
//	flat        the value
//	both        never more than the base (no negative order)
//
// "base" is the eligible subtotal: the GST-inclusive value of the lines the
// coupon applies to. A product-scoped coupon is worth a share of THOSE lines,
// and its discount reduces THEIR taxable value only (tax.Line.CouponExcluded):
// the GST on an unrelated line in the same bag is not lowered by a coupon that
// does not apply to it.
//
// ─── WHO FUNDS WHAT ─────────────────────────────────────────────────────
//
// A seller coupon (seller_id set, funded_by 'seller') applies only to that
// seller's goods, whatever its applicable_to says. Before this file an
// applicable_to='all' coupon carrying a seller_id discounted ANY seller's
// cart — one shop's promotion paid for out of another shop's order.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/tax"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audit actions for the admin console's coupon writes (migration 038 widens
// commerce_admin_audit_log's CHECK for them).
const (
	AuditActionCouponCreate = "coupon_create"
	AuditActionCouponUpdate = "coupon_update"
)

// ─── Typed refusals ─────────────────────────────────────────────────────
//
// Each maps to one 422 code a client renders (see CouponRefusalCode). The
// "used up" refusal is ErrCouponExhausted (checkout.go): the total cap and the
// per-buyer cap both read that way to a buyer.

var (
	ErrCouponInvalid      = errors.New("coupon code is not valid")
	ErrCouponNotStarted   = errors.New("coupon is not active yet")
	ErrCouponExpired      = errors.New("coupon has expired")
	ErrCouponMinOrder     = errors.New("order is below the coupon's minimum")
	ErrCouponNotAvailable = errors.New("platform coupons are not available")

	// Writes.
	ErrInvalidCoupon         = errors.New("invalid coupon")
	ErrCouponNotFound        = errors.New("coupon not found")
	ErrCouponCodeTaken       = errors.New("that coupon code already exists")
	ErrCouponProductNotOwned = errors.New("a coupon may only name the seller's own products")
	ErrCouponImmutable       = errors.New("code, discount and scope cannot change after a coupon is created")
	ErrCouponSellerReadOnly  = errors.New("seller coupons are read-only in the admin console")
)

// CouponMinOrderError carries the minimum the bag fell short of.
type CouponMinOrderError struct{ MinOrderMinor money.Paise }

func (e *CouponMinOrderError) Error() string {
	return fmt.Sprintf("order is below the coupon's minimum of %d paise", e.MinOrderMinor.Int64())
}
func (e *CouponMinOrderError) Unwrap() error { return ErrCouponMinOrder }

// CouponNotStartedError carries when the coupon becomes usable.
type CouponNotStartedError struct{ StartsAt time.Time }

func (e *CouponNotStartedError) Error() string {
	return "coupon is not active until " + e.StartsAt.UTC().Format(time.RFC3339)
}
func (e *CouponNotStartedError) Unwrap() error { return ErrCouponNotStarted }

// CouponRefusalCode is the 422 code for a coupon refusal, or ok=false when err
// is not one. The HTTP error mapping and the bag's coupon list both read it,
// so a reason shown beside a coupon is the same code applying it would answer.
func CouponRefusalCode(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, ErrCouponNotAvailable):
		return "COUPON_NOT_AVAILABLE", true
	case errors.Is(err, ErrCouponExpired):
		return "COUPON_EXPIRED", true
	case errors.Is(err, ErrCouponMinOrder):
		return "COUPON_MIN_ORDER", true
	case errors.Is(err, ErrCouponExhausted):
		return "COUPON_USED_UP", true
	case errors.Is(err, ErrCouponNotApplicable):
		return "COUPON_NOT_APPLICABLE", true
	case errors.Is(err, ErrCouponInvalid), errors.Is(err, ErrCouponNotStarted):
		return "COUPON_INVALID", true
	}
	return "", false
}

// ─── Codes ──────────────────────────────────────────────────────────────

var couponCodeRe = regexp.MustCompile(`^[A-Z0-9]{4,20}$`)

// NormalizeCouponCode is how every code is stored and looked up: whitespace
// removed, upper-cased. "diwali 10" and "DIWALI10" are the same coupon.
func NormalizeCouponCode(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}

// ValidCouponCode reports whether a normalised code is 4-20 of [A-Z0-9].
func ValidCouponCode(code string) bool { return couponCodeRe.MatchString(code) }

// ─── The arithmetic ─────────────────────────────────────────────────────

// MaxCouponBasisPoints is 100%.
const MaxCouponBasisPoints = 10000

// CouponDiscountMinor is what a coupon takes off `base` paise. See the file
// comment. An unusable configuration (no amount, an unknown type) is
// ErrCouponInvalid rather than a zero discount, so it cannot pass for a
// coupon that merely happens to be worth nothing.
func CouponDiscountMinor(discType string, bps *int, valueMinor, maxMinor *int64, base money.Paise) (money.Paise, error) {
	var d money.Paise
	switch discType {
	case "percentage":
		if bps == nil || *bps <= 0 || *bps > MaxCouponBasisPoints {
			return 0, ErrCouponInvalid
		}
		if base <= 0 {
			return 0, nil
		}
		d = money.Paise(int64(base) * int64(*bps) / MaxCouponBasisPoints)
		if maxMinor != nil && *maxMinor >= 0 && d > money.Paise(*maxMinor) {
			d = money.Paise(*maxMinor)
		}
	case "flat":
		if valueMinor == nil || *valueMinor <= 0 {
			return 0, ErrCouponInvalid
		}
		d = money.Paise(*valueMinor)
	default:
		// free_shipping / buy_x_get_y exist in the original schema and are
		// not in the launch loop; nothing creates them any more.
		return 0, ErrCouponInvalid
	}
	if base <= 0 {
		return 0, nil
	}
	// A flat coupon larger than the bag must not create a negative order.
	if d > base {
		d = base
	}
	return d, nil
}

// ─── Evaluation (quote, checkout, the bag's list) ───────────────────────

// couponRow is a coupon's terms, read either by CLAIMING capacity (checkout)
// or by PREVIEWING it (quote, the bag's list).
//
// C3-LB-2. The quote must show the buyer the same discount checkout will
// charge, and the only way to guarantee that is for both to run the same
// applicability rules and the same arithmetic.
type couponRow struct {
	id             uuid.UUID
	discType       string
	valueMinor     *int64
	basisPoints    *int
	maxDiscount    *int64
	minOrder       int64
	maxUsesPerUser int
	applicableTo   string
	applicableIDs  []uuid.UUID
	couponSeller   *uuid.UUID
}

// couponColumns is shared so the claim and the preview cannot read different
// fields and therefore reach different conclusions.
const couponColumns = `id, discount_type,
	          discount_value_minor, discount_basis_points, max_discount_amount_minor,
	          COALESCE(min_order_amount_minor,0), max_uses_per_user,
	          applicable_to, applicable_ids, seller_id`

// couponLive is the validity predicate: active, started, unexpired, with
// capacity remaining — and, for a platform coupon ($2), the switch on. The
// claim applies it in an UPDATE, the preview in a SELECT: same text, so
// "valid" means the same thing to both.
const couponLive = `code = $1
		   AND is_active = TRUE
		   AND starts_at <= NOW()
		   AND (expires_at IS NULL OR expires_at > NOW())
		   AND (max_uses IS NULL OR uses_count < max_uses)
		   AND (seller_id IS NOT NULL OR $2::boolean)`

func scanCoupon(row pgx.Row) (couponRow, error) {
	var c couponRow
	err := row.Scan(&c.id, &c.discType,
		&c.valueMinor, &c.basisPoints, &c.maxDiscount,
		&c.minOrder, &c.maxUsesPerUser,
		&c.applicableTo, &c.applicableIDs, &c.couponSeller)
	return c, err
}

// couponOutcome is what a coupon does to a bag.
type couponOutcome struct {
	CouponID uuid.UUID
	Discount money.Paise
	// Eligible[i] reports whether priced line i is one the coupon applies to.
	Eligible []bool
	// Platform is true for a platform-funded coupon.
	Platform bool
}

// applyCouponEligibility marks the tax lines a coupon does not apply to, so
// tax.Compute allocates the discount across the eligible lines only.
func applyCouponEligibility(lines []tax.Line, out couponOutcome) {
	for i := range lines {
		if i < len(out.Eligible) {
			lines[i].CouponExcluded = !out.Eligible[i]
		}
	}
}

// classifyCouponMiss explains why couponLive matched no row, in the caller's
// transaction. The predicate is one boolean; the buyer needs to know which
// clause failed.
func classifyCouponMiss(ctx context.Context, tx pgx.Tx, code string, platformEnabled bool) error {
	var (
		active    bool
		startsAt  time.Time
		expiresAt *time.Time
		maxUses   *int
		uses      int
		seller    *uuid.UUID
		now       time.Time
	)
	err := tx.QueryRow(ctx,
		`SELECT is_active, starts_at, expires_at, max_uses, uses_count, seller_id, NOW()
		   FROM coupons WHERE code = $1`, code).
		Scan(&active, &startsAt, &expiresAt, &maxUses, &uses, &seller, &now)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCouponInvalid
	}
	if err != nil {
		return err
	}
	switch {
	case seller == nil && !platformEnabled:
		return ErrCouponNotAvailable
	case !active:
		return ErrCouponInvalid
	case startsAt.After(now):
		return &CouponNotStartedError{StartsAt: startsAt}
	case expiresAt != nil && !expiresAt.After(now):
		return ErrCouponExpired
	default:
		// max_uses reached — or reached between the claim and this read.
		return ErrCouponExhausted
	}
}

// evaluateCoupon applies every rule that decides IF and BY HOW MUCH a coupon
// discounts this bag. It performs no writes, so the quote, the checkout and
// the bag's list all call it and are guaranteed to agree.
func evaluateCoupon(
	ctx context.Context,
	tx pgx.Tx,
	c couponRow,
	userID, sellerID uuid.UUID,
	lines []pricedLine,
) (couponOutcome, error) {
	out := couponOutcome{CouponID: c.id, Platform: c.couponSeller == nil, Eligible: make([]bool, len(lines))}

	// A seller's coupon is that seller's money: it applies to that seller's
	// goods and nobody else's, whatever its scope says.
	if c.couponSeller != nil && *c.couponSeller != sellerID {
		return out, ErrCouponNotApplicable
	}

	// Per-buyer cap, counted under the caller's transaction. Two checkouts
	// by one buyer serialise on the cart row, so this count is current.
	var used int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM coupon_usages WHERE coupon_id = $1 AND user_id = $2`,
		c.id, userID).Scan(&used); err != nil {
		return out, err
	}
	if used >= c.maxUsesPerUser {
		return out, ErrCouponExhausted
	}

	// ── Applicability (B9) ────────────────────────────────────────────
	//
	// The switch is exhaustive and its default REFUSES: a scope this code
	// does not understand must not be treated as "applies to everything".
	mark := func(pick func(pricedLine) uuid.UUID) {
		set := make(map[uuid.UUID]struct{}, len(c.applicableIDs))
		for _, id := range c.applicableIDs {
			set[id] = struct{}{}
		}
		for i, l := range lines {
			_, out.Eligible[i] = set[pick(l)]
		}
	}
	switch c.applicableTo {
	case "all", "":
		for i := range out.Eligible {
			out.Eligible[i] = true
		}
	case "seller":
		if c.couponSeller == nil && !containsUUID(c.applicableIDs, sellerID) {
			return out, ErrCouponNotApplicable
		}
		for i := range out.Eligible {
			out.Eligible[i] = true
		}
	case "product":
		mark(func(l pricedLine) uuid.UUID { return l.ProductID })
	case "variant":
		mark(func(l pricedLine) uuid.UUID { return l.VariantID })
	case "category":
		inCat, err := productsInCategories(ctx, tx, lines, c.applicableIDs)
		if err != nil {
			return out, err
		}
		for i, l := range lines {
			out.Eligible[i] = inCat[l.ProductID]
		}
	default:
		return out, fmt.Errorf("%w: unknown applicability scope %q", ErrCouponNotApplicable, c.applicableTo)
	}

	var base money.Paise
	anyEligible := false
	for i, l := range lines {
		if out.Eligible[i] {
			anyEligible = true
			base = base.Add(l.UnitMinor.MulQty(l.Quantity))
		}
	}
	if !anyEligible {
		// An empty allowlist matches nothing: a product coupon naming no
		// products is misconfigured, not "every product".
		return out, ErrCouponNotApplicable
	}
	if base < money.Paise(c.minOrder) {
		return out, &CouponMinOrderError{MinOrderMinor: money.Paise(c.minOrder)}
	}
	d, err := CouponDiscountMinor(c.discType, c.basisPoints, c.valueMinor, c.maxDiscount, base)
	if err != nil {
		return out, err
	}
	out.Discount = d
	return out, nil
}

func containsUUID(ids []uuid.UUID, want uuid.UUID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// productsInCategories reports which of the lines' products sit in one of
// the categories, read inside the caller's transaction.
func productsInCategories(ctx context.Context, tx pgx.Tx, lines []pricedLine, categories []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(categories) == 0 || len(lines) == 0 {
		return out, nil
	}
	ids := make([]uuid.UUID, 0, len(lines))
	for _, l := range lines {
		ids = append(ids, l.ProductID)
	}
	rows, err := tx.Query(ctx,
		`SELECT id FROM products WHERE id = ANY($1) AND category_id = ANY($2)`, ids, categories)
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

// claimCoupon takes capacity conditionally, inside the checkout transaction.
//
// M-6: caps used to be READ during pricing and INCREMENTED after the order
// was created, with the increment's error ignored. Fifty concurrent
// checkouts all passed a one-use coupon because none had committed when the
// others read. The conditional UPDATE is the claim: two checkouts racing for
// the last use queue on the coupon row, and the second re-evaluates
// `uses_count < max_uses` against the first's committed count and matches
// nothing (ErrCouponExhausted, via classifyCouponMiss).
func claimCoupon(ctx context.Context, tx pgx.Tx, code string, userID, sellerID uuid.UUID, lines []pricedLine, platformEnabled bool) (*uuid.UUID, couponOutcome, error) {
	c, err := scanCoupon(tx.QueryRow(ctx, `
		UPDATE coupons
		   SET uses_count = uses_count + 1
		 WHERE `+couponLive+`
		RETURNING `+couponColumns, code, platformEnabled))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, couponOutcome{}, classifyCouponMiss(ctx, tx, code, platformEnabled)
		}
		return nil, couponOutcome{}, err
	}
	out, err := evaluateCoupon(ctx, tx, c, userID, sellerID, lines)
	if err != nil {
		return nil, couponOutcome{}, err
	}
	return &c.id, out, nil
}

// previewCoupon prices a coupon WITHOUT claiming capacity, for the quote.
//
// C3-LB-2. A quote is not a promise: holding coupon capacity for every buyer
// who opens a checkout screen would exhaust a one-use code on the first
// person to look at it. Checkout claims atomically and can still refuse.
func previewCoupon(ctx context.Context, tx pgx.Tx, code string, userID, sellerID uuid.UUID, lines []pricedLine, platformEnabled bool) (couponOutcome, error) {
	c, err := scanCoupon(tx.QueryRow(ctx,
		`SELECT `+couponColumns+` FROM coupons WHERE `+couponLive, code, platformEnabled))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return couponOutcome{}, classifyCouponMiss(ctx, tx, code, platformEnabled)
		}
		return couponOutcome{}, err
	}
	return evaluateCoupon(ctx, tx, c, userID, sellerID, lines)
}

// ─── The bag's coupon list ──────────────────────────────────────────────

// CartCoupon is one coupon the buyer could apply to their bag, and what it
// would do. Applicable=false carries the refusal's code in Reason (and, for
// COUPON_MIN_ORDER, the minimum in MinOrderMinor), so the bag can say
// "add ₹X more" rather than hide the coupon.
type CartCoupon struct {
	Code             string     `json:"code"`
	Title            string     `json:"title"`
	Description      *string    `json:"description"`
	DiscountType     string     `json:"discount_type"`
	DiscountValue    int64      `json:"discount_value"`
	MaxDiscountMinor *int64     `json:"max_discount_minor"`
	MinOrderMinor    int64      `json:"min_order_minor"`
	ExpiresAt        *time.Time `json:"expires_at"`
	FundedBy         string     `json:"funded_by"`
	Applicable       bool       `json:"applicable"`
	DiscountMinor    int64      `json:"discount_minor"`
	Reason           *string    `json:"reason"`
}

// CartCoupons is every PUBLIC live coupon that could apply to the caller's
// bag — the bag seller's own, and the platform's only while the switch is on
// — each evaluated by evaluateCoupon against the sellable lines. Applicable
// ones first, largest discount first. Secret codes (is_public = false) are
// never listed. Takes no row locks and claims nothing.
func (s *Store) CartCoupons(ctx context.Context, userID uuid.UUID, platformEnabled bool) ([]CartCoupon, money.Paise, error) {
	out := []CartCoupon{}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var cartID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM carts WHERE user_id = $1`, userID).Scan(&cartID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return out, 0, nil
		}
		return nil, 0, err
	}
	lines, err := lockCartLines(ctx, tx, cartID) // a plain read; see its SQL
	if err != nil {
		return nil, 0, err
	}
	// Price each line on its own so one paused or untaxed product does not
	// hide the coupons for the rest of the bag. The quote still refuses the
	// bag until that line is removed.
	var priced []pricedLine
	sellerID := uuid.Nil
	for _, l := range lines {
		pl, sid, err := priceLinesTx(ctx, tx, []cartLine{l}, false)
		if err != nil {
			if errors.Is(err, ErrProductUnavailable) || errors.Is(err, ErrTaxClassMissing) || errors.Is(err, ErrTaxClassInvalid) {
				continue
			}
			return nil, 0, err
		}
		if sellerID == uuid.Nil {
			sellerID = sid
		} else if sid != sellerID {
			return out, 0, nil // D2: a mixed bag has no single seller's coupons
		}
		priced = append(priced, pl...)
	}
	var subtotal money.Paise
	for _, pl := range priced {
		subtotal = subtotal.Add(pl.UnitMinor.MulQty(pl.Quantity))
	}
	if len(priced) == 0 {
		return out, 0, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT `+couponColumns+`, code, description, expires_at, funded_by
		  FROM coupons
		 WHERE is_active = TRUE AND is_public = TRUE
		   AND starts_at <= NOW()
		   AND (expires_at IS NULL OR expires_at > NOW())
		   AND (max_uses IS NULL OR uses_count < max_uses)
		   AND discount_type IN ('percentage','flat')
		   AND (seller_id = $1 OR (seller_id IS NULL AND $2::boolean))
		 ORDER BY code`, sellerID, platformEnabled)
	if err != nil {
		return nil, 0, err
	}
	type cand struct {
		row       couponRow
		code      string
		desc      *string
		expiresAt *time.Time
		fundedBy  string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.row.id, &c.row.discType,
			&c.row.valueMinor, &c.row.basisPoints, &c.row.maxDiscount,
			&c.row.minOrder, &c.row.maxUsesPerUser,
			&c.row.applicableTo, &c.row.applicableIDs, &c.row.couponSeller,
			&c.code, &c.desc, &c.expiresAt, &c.fundedBy); err != nil {
			rows.Close()
			return nil, 0, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	for _, c := range cands {
		cc := CartCoupon{
			Code: c.code, Description: c.desc,
			DiscountType: c.row.discType, DiscountValue: couponValue(c.row.discType, c.row.basisPoints, c.row.valueMinor),
			MaxDiscountMinor: c.row.maxDiscount, MinOrderMinor: c.row.minOrder,
			ExpiresAt: c.expiresAt, FundedBy: c.fundedBy,
		}
		cc.Title = CouponTitle(c.desc, c.row.discType, c.row.basisPoints, c.row.valueMinor, c.row.maxDiscount)
		res, err := evaluateCoupon(ctx, tx, c.row, userID, sellerID, priced)
		if code, refused := CouponRefusalCode(err); refused {
			reason := code
			cc.Reason = &reason
		} else if err != nil {
			return nil, 0, err
		} else {
			cc.Applicable = true
			cc.DiscountMinor = res.Discount.Int64()
		}
		out = append(out, cc)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Applicable != out[b].Applicable {
			return out[a].Applicable
		}
		if out[a].DiscountMinor != out[b].DiscountMinor {
			return out[a].DiscountMinor > out[b].DiscountMinor
		}
		return out[a].Code < out[b].Code
	})
	return out, subtotal, nil
}

// ─── The product page's best coupon ─────────────────────────────────────

// BestCoupon is the most a buyer saves on ONE unit of a product with a public
// seller coupon, at the product's listed price (its cheapest active variant —
// the price every summary and the product page show).
type BestCoupon struct {
	Code            string `json:"code"`
	DiscountMinor   int64  `json:"discount_minor"`
	PriceAfterMinor int64  `json:"price_after_minor"`
	Title           string `json:"title"`
}

// BestCouponsForProducts answers BestCoupon for a page of products in ONE
// query. Only live, PUBLIC seller coupons of the product's own seller count;
// a coupon whose minimum the single unit does not meet is skipped, and a
// product with nothing qualifying is absent from the map. Ties go to the
// alphabetically first code so the badge is stable across reads.
//
// Per-buyer caps are not consulted: this is a public page, and the bag's own
// list (CartCoupons) is where a buyer's history applies.
func (s *Store) BestCouponsForProducts(ctx context.Context, productIDs []uuid.UUID) (map[uuid.UUID]*BestCoupon, error) {
	out := map[uuid.UUID]*BestCoupon{}
	if len(productIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT pr.product_id, pr.price_minor,
		       c.code, c.description, c.discount_type, c.discount_basis_points,
		       c.discount_value_minor, c.max_discount_amount_minor,
		       COALESCE(c.min_order_amount_minor, 0)
		  FROM (
		        SELECT p.id AS product_id, po.seller_id, v.price_minor
		          FROM products p
		          JOIN product_offers po ON po.product_id = p.id
		          JOIN LATERAL (
		                SELECT COALESCE(NULLIF(selling_price_minor, 0), ROUND(selling_price*100))::bigint AS price_minor
		                  FROM product_variants
		                 WHERE product_id = p.id AND status = 'active'
		                 ORDER BY selling_price ASC
		                 LIMIT 1
		          ) v ON true
		         WHERE p.id = ANY($1)
		       ) pr
		  JOIN coupons c ON c.seller_id = pr.seller_id
		 WHERE c.is_active = TRUE AND c.is_public = TRUE
		   AND c.starts_at <= NOW()
		   AND (c.expires_at IS NULL OR c.expires_at > NOW())
		   AND (c.max_uses IS NULL OR c.uses_count < c.max_uses)
		   AND c.discount_type IN ('percentage','flat')
		   AND (c.applicable_to IN ('all','seller')
		        OR (c.applicable_to = 'product' AND pr.product_id = ANY(c.applicable_ids)))
		   AND COALESCE(c.min_order_amount_minor, 0) <= pr.price_minor`, productIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			pid                  uuid.UUID
			price, minOrder      int64
			code, discType       string
			desc                 *string
			bps                  *int
			valueMinor, maxMinor *int64
		)
		if err := rows.Scan(&pid, &price, &code, &desc, &discType, &bps, &valueMinor, &maxMinor, &minOrder); err != nil {
			return nil, err
		}
		d, err := CouponDiscountMinor(discType, bps, valueMinor, maxMinor, money.Paise(price))
		if err != nil || d <= 0 {
			continue
		}
		cur := out[pid]
		if cur != nil && (cur.DiscountMinor > d.Int64() || (cur.DiscountMinor == d.Int64() && cur.Code < code)) {
			continue
		}
		out[pid] = &BestCoupon{
			Code: code, DiscountMinor: d.Int64(), PriceAfterMinor: price - d.Int64(),
			Title: CouponTitle(desc, discType, bps, valueMinor, maxMinor),
		}
	}
	return out, rows.Err()
}

// CouponTitle is the coupon's own description, or a plain statement of its
// value when it has none: "10% off up to ₹200", "₹50 off".
func CouponTitle(desc *string, discType string, bps *int, valueMinor, maxMinor *int64) string {
	if desc != nil && strings.TrimSpace(*desc) != "" {
		return strings.TrimSpace(*desc)
	}
	switch discType {
	case "percentage":
		if bps == nil {
			return ""
		}
		t := percentText(*bps) + " off"
		if maxMinor != nil && *maxMinor > 0 {
			t += " up to " + RupeeText(*maxMinor)
		}
		return t
	case "flat":
		if valueMinor == nil {
			return ""
		}
		return RupeeText(*valueMinor) + " off"
	}
	return ""
}

func percentText(bps int) string {
	whole, frac := bps/100, bps%100
	if frac == 0 {
		return strconv.Itoa(whole) + "%"
	}
	s := fmt.Sprintf("%d.%02d", whole, frac)
	return strings.TrimRight(s, "0") + "%"
}

// RupeeText renders paise as "₹1,299" or "₹1,299.50", Indian digit grouping.
// Display only.
func RupeeText(paise int64) string {
	neg := paise < 0
	if neg {
		paise = -paise
	}
	rupees, p := paise/100, paise%100
	digits := strconv.FormatInt(rupees, 10)
	if len(digits) > 3 {
		head, tail := digits[:len(digits)-3], digits[len(digits)-3:]
		var parts []string
		for len(head) > 2 {
			parts = append([]string{head[len(head)-2:]}, parts...)
			head = head[:len(head)-2]
		}
		if head != "" {
			parts = append([]string{head}, parts...)
		}
		digits = strings.Join(parts, ",") + "," + tail
	}
	out := "₹" + digits
	if p != 0 {
		out += fmt.Sprintf(".%02d", p)
	}
	if neg {
		out = "-" + out
	}
	return out
}

// couponValue is the wire discount_value: basis points for a percentage
// coupon, paise for a flat one.
func couponValue(discType string, bps *int, valueMinor *int64) int64 {
	if discType == "percentage" {
		if bps != nil {
			return int64(*bps)
		}
		return 0
	}
	if valueMinor != nil {
		return *valueMinor
	}
	return 0
}

// ─── Coupon records (seller and admin writes) ───────────────────────────

// CouponRecord is one coupon as the seller's Coupons page and the admin
// console read it. discount_value is basis points for a percentage coupon
// and paise for a flat one; every amount is paise.
type CouponRecord struct {
	ID               uuid.UUID   `json:"id"`
	Code             string      `json:"code"`
	Description      *string     `json:"description"`
	DiscountType     string      `json:"discount_type"`
	DiscountValue    int64       `json:"discount_value"`
	MaxDiscountMinor *int64      `json:"max_discount_minor"`
	MinOrderMinor    int64       `json:"min_order_minor"`
	MaxUses          *int        `json:"max_uses"`
	MaxUsesPerUser   int         `json:"max_uses_per_user"`
	UsesCount        int         `json:"uses_count"`
	ApplicableTo     string      `json:"applicable_to"`
	ApplicableIDs    []uuid.UUID `json:"applicable_ids"`
	StartsAt         time.Time   `json:"starts_at"`
	ExpiresAt        *time.Time  `json:"expires_at"`
	IsPublic         bool        `json:"is_public"`
	IsActive         bool        `json:"is_active"`
	FundedBy         string      `json:"funded_by"`
	SellerID         *uuid.UUID  `json:"seller_id"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`

	basisPoints *int
	valueMinor  *int64
}

const couponRecordColumns = `id, code, description, discount_type,
	discount_basis_points, discount_value_minor, max_discount_amount_minor,
	COALESCE(min_order_amount_minor, 0), max_uses, max_uses_per_user, uses_count,
	applicable_to, COALESCE(applicable_ids, '{}'::uuid[]),
	starts_at, expires_at, is_public, is_active, funded_by, seller_id,
	created_at, updated_at`

func scanCouponRecord(row pgx.Row) (*CouponRecord, error) {
	var r CouponRecord
	if err := row.Scan(&r.ID, &r.Code, &r.Description, &r.DiscountType,
		&r.basisPoints, &r.valueMinor, &r.MaxDiscountMinor,
		&r.MinOrderMinor, &r.MaxUses, &r.MaxUsesPerUser, &r.UsesCount,
		&r.ApplicableTo, &r.ApplicableIDs,
		&r.StartsAt, &r.ExpiresAt, &r.IsPublic, &r.IsActive, &r.FundedBy, &r.SellerID,
		&r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.DiscountValue = couponValue(r.DiscountType, r.basisPoints, r.valueMinor)
	if r.ApplicableIDs == nil {
		r.ApplicableIDs = []uuid.UUID{}
	}
	return &r, nil
}

func collectCouponRecords(rows pgx.Rows) ([]*CouponRecord, error) {
	defer rows.Close()
	out := []*CouponRecord{}
	for rows.Next() {
		r, err := scanCouponRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListSellerCoupons is one seller's coupons, newest first.
func (s *Store) ListSellerCoupons(ctx context.Context, sellerID uuid.UUID) ([]*CouponRecord, error) {
	rows, err := s.db.Query(ctx, `SELECT `+couponRecordColumns+` FROM coupons
		 WHERE seller_id = $1 ORDER BY created_at DESC, id`, sellerID)
	if err != nil {
		return nil, err
	}
	return collectCouponRecords(rows)
}

// CouponListFilter narrows the admin list.
type CouponListFilter struct {
	FundedBy string // "", "seller" or "platform"
	Limit    int
	Offset   int
}

// ListCoupons is the admin console's list: every coupon, or one funding side,
// by code. Returns the total before paging.
func (s *Store) ListCoupons(ctx context.Context, f CouponListFilter) ([]*CouponRecord, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	var total int
	if err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM coupons WHERE ($1 = '' OR funded_by = $1)`, f.FundedBy).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(ctx, `SELECT `+couponRecordColumns+` FROM coupons
		 WHERE ($1 = '' OR funded_by = $1)
		 ORDER BY code, id LIMIT $2 OFFSET $3`, f.FundedBy, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	out, err := collectCouponRecords(rows)
	return out, total, err
}

// CouponCreate is a new coupon. SellerID nil is a PLATFORM coupon.
type CouponCreate struct {
	SellerID         *uuid.UUID
	Code             string
	Description      string
	DiscountType     string
	DiscountValue    int64
	MaxDiscountMinor *int64
	MinOrderMinor    int64
	MaxUses          *int
	MaxUsesPerUser   int
	ApplicableTo     string
	ApplicableIDs    []uuid.UUID
	StartsAt         *time.Time
	ExpiresAt        *time.Time
	IsPublic         bool
	IsActive         bool

	// CreatedBy is the human: the seller's user, or the admin (the token's
	// act). Admin creates also write a commerce_admin_audit_log row.
	CreatedBy uuid.UUID
	Admin     bool
	Reason    *string
}

// MaxCouponApplicableIDs bounds a scoped coupon's list.
const MaxCouponApplicableIDs = 100

// MaxCouponDescription bounds the description, in characters.
const MaxCouponDescription = 200

// MaxCouponAmountMinor bounds every paise amount on a coupon: the legacy
// NUMERIC(10,2) rupee mirror the original schema still requires cannot hold
// more than ₹9,99,99,999.99.
const MaxCouponAmountMinor = 9_999_999_999

func invalidCoupon(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidCoupon, fmt.Sprintf(format, args...))
}

// ValidateCouponCreate normalises and checks a new coupon. Pure: the store
// re-checks ownership of named products inside its transaction.
func ValidateCouponCreate(in *CouponCreate, now time.Time) error {
	in.Code = NormalizeCouponCode(in.Code)
	if !ValidCouponCode(in.Code) {
		return invalidCoupon("code must be 4-20 letters and digits")
	}
	in.Description = strings.TrimSpace(in.Description)
	if utf8.RuneCountInString(in.Description) > MaxCouponDescription {
		return invalidCoupon("description must be %d characters or fewer", MaxCouponDescription)
	}
	switch in.DiscountType {
	case "percentage":
		if in.DiscountValue < 1 || in.DiscountValue > MaxCouponBasisPoints {
			return invalidCoupon("a percentage coupon's discount_value is basis points, 1 to 10000")
		}
	case "flat":
		if in.DiscountValue <= 0 || in.DiscountValue > MaxCouponAmountMinor {
			return invalidCoupon("a flat coupon's discount_value is paise, above zero")
		}
		if in.MaxDiscountMinor != nil {
			return invalidCoupon("max_discount_minor applies to percentage coupons only")
		}
	default:
		return invalidCoupon("discount_type must be percentage or flat")
	}
	if in.MaxDiscountMinor != nil && (*in.MaxDiscountMinor <= 0 || *in.MaxDiscountMinor > MaxCouponAmountMinor) {
		return invalidCoupon("max_discount_minor must be above zero, or absent for no cap")
	}
	if in.MinOrderMinor < 0 || in.MinOrderMinor > MaxCouponAmountMinor {
		return invalidCoupon("min_order_minor cannot be negative")
	}
	if in.MaxUses != nil && *in.MaxUses <= 0 {
		return invalidCoupon("max_uses must be 1 or more, or absent for no limit")
	}
	if in.MaxUsesPerUser <= 0 {
		return invalidCoupon("max_uses_per_user must be 1 or more")
	}
	if in.MaxUses != nil && in.MaxUsesPerUser > *in.MaxUses {
		return invalidCoupon("max_uses_per_user cannot exceed max_uses")
	}
	scopes := map[string]bool{"all": true, "product": true}
	if in.SellerID == nil {
		scopes["category"], scopes["seller"] = true, true
	}
	if !scopes[in.ApplicableTo] {
		if in.SellerID != nil {
			return invalidCoupon("a seller coupon applies to all of the shop's products or to listed products")
		}
		return invalidCoupon("applicable_to must be all, product, category or seller")
	}
	seen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(in.ApplicableIDs))
	for _, id := range in.ApplicableIDs {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	in.ApplicableIDs = ids
	if in.ApplicableTo == "all" && len(ids) > 0 {
		return invalidCoupon("applicable_ids must be empty when applicable_to is all")
	}
	if in.ApplicableTo != "all" && len(ids) == 0 {
		return invalidCoupon("list at least one id in applicable_ids")
	}
	if len(ids) > MaxCouponApplicableIDs {
		return invalidCoupon("at most %d applicable_ids", MaxCouponApplicableIDs)
	}
	starts := now
	if in.StartsAt != nil {
		starts = *in.StartsAt
	}
	if in.ExpiresAt != nil {
		if !in.ExpiresAt.After(starts) {
			return invalidCoupon("expires_at must be after starts_at")
		}
		if !in.ExpiresAt.After(now) {
			return invalidCoupon("expires_at must be in the future")
		}
	}
	return nil
}

// couponLegacyRupees is the NUMERIC(10,2) mirror the original schema still
// requires (discount_value is NOT NULL) and the legacy coupon-preview route
// reads. Derived from the paise/bps values, never the other way round.
const couponLegacyRupees = `
	discount_value      = CASE WHEN discount_type = 'percentage'
	                           THEN discount_basis_points::numeric / 100
	                           ELSE discount_value_minor::numeric / 100 END,
	max_discount_amount = max_discount_amount_minor::numeric / 100,
	min_order_amount    = COALESCE(min_order_amount_minor, 0)::numeric / 100`

// CreateCoupon validates and inserts a coupon. A seller coupon may name only
// that seller's products; a platform coupon's ids must exist.
func (s *Store) CreateCoupon(ctx context.Context, in CouponCreate) (*CouponRecord, error) {
	if err := ValidateCouponCreate(&in, time.Now()); err != nil {
		return nil, err
	}
	if in.Admin && in.CreatedBy == uuid.Nil {
		return nil, ErrActorRequired
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := checkCouponTargets(ctx, tx, in.SellerID, in.ApplicableTo, in.ApplicableIDs); err != nil {
		return nil, err
	}

	var bps *int
	var valueMinor *int64
	if in.DiscountType == "percentage" {
		v := int(in.DiscountValue)
		bps = &v
	} else {
		v := in.DiscountValue
		valueMinor = &v
	}
	fundedBy := "seller"
	if in.SellerID == nil {
		fundedBy = "platform"
	}
	var desc *string
	if in.Description != "" {
		desc = &in.Description
	}
	var createdBy *uuid.UUID
	if in.CreatedBy != uuid.Nil {
		createdBy = &in.CreatedBy
	}
	id := uuid.New()
	// discount_value (the legacy rupee column) is NOT NULL; it is written as
	// 0 here and derived from the integer columns by couponLegacyRupees in
	// the same transaction.
	_, err = tx.Exec(ctx, `
		INSERT INTO coupons (id, seller_id, code, description, discount_type,
		       discount_value, discount_basis_points, discount_value_minor,
		       max_discount_amount_minor, min_order_amount_minor,
		       max_uses, max_uses_per_user, applicable_to, applicable_ids,
		       is_active, is_public, funded_by, starts_at, expires_at,
		       created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,
		       0, $6, $7,
		       $8, $9,
		       $10, $11, $12, $13,
		       $14, $15, $16, COALESCE($17::timestamptz, NOW()), $18,
		       $19, NOW(), NOW())`,
		id, in.SellerID, in.Code, desc, in.DiscountType,
		bps, valueMinor,
		in.MaxDiscountMinor, in.MinOrderMinor,
		in.MaxUses, in.MaxUsesPerUser, in.ApplicableTo, in.ApplicableIDs,
		in.IsActive, in.IsPublic, fundedBy, in.StartsAt, in.ExpiresAt,
		createdBy)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrCouponCodeTaken
		}
		return nil, fmt.Errorf("create coupon: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE coupons SET `+couponLegacyRupees+` WHERE id = $1`, id); err != nil {
		return nil, fmt.Errorf("create coupon mirror: %w", err)
	}
	rec, err := scanCouponRecord(tx.QueryRow(ctx, `SELECT `+couponRecordColumns+` FROM coupons WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if in.Admin {
		if err := insertAdminAudit(ctx, tx, adminAuditEntry{
			Actor: in.CreatedBy, Action: AuditActionCouponCreate, TargetType: "coupon",
			TargetID: id, After: rec, Reason: in.Reason,
		}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return rec, nil
}

// checkCouponTargets proves every id a scoped coupon names exists — and, for
// a seller coupon, is that seller's own listing.
func checkCouponTargets(ctx context.Context, tx pgx.Tx, sellerID *uuid.UUID, scope string, ids []uuid.UUID) error {
	if scope == "all" || len(ids) == 0 {
		return nil
	}
	var n int
	var err error
	switch {
	case sellerID != nil && scope == "product":
		err = tx.QueryRow(ctx, `SELECT count(DISTINCT product_id) FROM product_offers
			 WHERE seller_id = $1 AND product_id = ANY($2)`, *sellerID, ids).Scan(&n)
		if err == nil && n != len(ids) {
			return ErrCouponProductNotOwned
		}
		return err
	case scope == "product":
		err = tx.QueryRow(ctx, `SELECT count(*) FROM products WHERE id = ANY($1)`, ids).Scan(&n)
	case scope == "category":
		err = tx.QueryRow(ctx, `SELECT count(*) FROM product_categories WHERE id = ANY($1)`, ids).Scan(&n)
	case scope == "seller":
		err = tx.QueryRow(ctx, `SELECT count(*) FROM sellers WHERE id = ANY($1)`, ids).Scan(&n)
	default:
		return invalidCoupon("unknown scope %q", scope)
	}
	if err != nil {
		return err
	}
	if n != len(ids) {
		return invalidCoupon("applicable_ids names a %s that does not exist", scope)
	}
	return nil
}

// Opt is a three-state JSON field: absent (Set=false) means no change, an
// explicit null (Null=true) clears, anything else sets Value.
type Opt[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UnmarshalJSON is called only when the key is present.
func (o *Opt[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

// CouponPatch is an edit. Code, discount type and value, scope, seller and
// funding are fixed at create and are not representable here. A null clears
// description, max_discount_minor, max_uses and expires_at; a null
// min_order_minor is "any order" (0). The others, starts_at included, cannot
// be cleared.
type CouponPatch struct {
	Description      Opt[string]
	MaxDiscountMinor Opt[int64]
	MinOrderMinor    Opt[int64]
	MaxUses          Opt[int]
	MaxUsesPerUser   Opt[int]
	StartsAt         Opt[time.Time]
	ExpiresAt        Opt[time.Time]
	IsActive         Opt[bool]
	IsPublic         Opt[bool]
}

// CouponPatchScope says who is editing: a seller (only their own coupons;
// anyone else's is not found) or an admin (platform coupons only).
type CouponPatchScope struct {
	SellerID *uuid.UUID
	Admin    bool
	Actor    uuid.UUID
	Reason   *string
}

// ApplyCouponPatch merges p onto r (a copy) and validates the result. Pure.
func ApplyCouponPatch(r CouponRecord, p CouponPatch, now time.Time) (CouponRecord, bool, error) {
	before := r
	notNull := func(set, null bool, field string) error {
		if set && null {
			return invalidCoupon("%s cannot be cleared", field)
		}
		return nil
	}
	for _, c := range []struct {
		set, null bool
		f         string
	}{
		{p.MaxUsesPerUser.Set, p.MaxUsesPerUser.Null, "max_uses_per_user"},
		{p.IsActive.Set, p.IsActive.Null, "is_active"},
		{p.IsPublic.Set, p.IsPublic.Null, "is_public"},
	} {
		if err := notNull(c.set, c.null, c.f); err != nil {
			return r, false, err
		}
	}
	if p.Description.Set {
		if p.Description.Null || strings.TrimSpace(p.Description.Value) == "" {
			r.Description = nil
		} else {
			d := strings.TrimSpace(p.Description.Value)
			if utf8.RuneCountInString(d) > MaxCouponDescription {
				return r, false, invalidCoupon("description must be %d characters or fewer", MaxCouponDescription)
			}
			r.Description = &d
		}
	}
	if p.MaxDiscountMinor.Set {
		if p.MaxDiscountMinor.Null {
			r.MaxDiscountMinor = nil
		} else {
			if r.DiscountType != "percentage" {
				return r, false, invalidCoupon("max_discount_minor applies to percentage coupons only")
			}
			if p.MaxDiscountMinor.Value <= 0 || p.MaxDiscountMinor.Value > MaxCouponAmountMinor {
				return r, false, invalidCoupon("max_discount_minor must be above zero, or null for no cap")
			}
			v := p.MaxDiscountMinor.Value
			r.MaxDiscountMinor = &v
		}
	}
	if p.MinOrderMinor.Set {
		if p.MinOrderMinor.Null {
			r.MinOrderMinor = 0
		} else {
			if p.MinOrderMinor.Value < 0 || p.MinOrderMinor.Value > MaxCouponAmountMinor {
				return r, false, invalidCoupon("min_order_minor cannot be negative")
			}
			r.MinOrderMinor = p.MinOrderMinor.Value
		}
	}
	if p.MaxUses.Set {
		if p.MaxUses.Null {
			r.MaxUses = nil
		} else {
			if p.MaxUses.Value <= 0 {
				return r, false, invalidCoupon("max_uses must be 1 or more, or null for no limit")
			}
			v := p.MaxUses.Value
			r.MaxUses = &v
		}
	}
	if p.MaxUsesPerUser.Set {
		if p.MaxUsesPerUser.Value <= 0 {
			return r, false, invalidCoupon("max_uses_per_user must be 1 or more")
		}
		r.MaxUsesPerUser = p.MaxUsesPerUser.Value
	}
	if p.StartsAt.Set {
		if p.StartsAt.Null {
			return r, false, invalidCoupon("starts_at cannot be cleared")
		}
		r.StartsAt = p.StartsAt.Value
	}
	if p.ExpiresAt.Set {
		if p.ExpiresAt.Null {
			r.ExpiresAt = nil
		} else {
			v := p.ExpiresAt.Value
			if !v.After(now) {
				return r, false, invalidCoupon("expires_at must be in the future; deactivate a coupon to end it now")
			}
			r.ExpiresAt = &v
		}
	}
	if p.IsActive.Set {
		r.IsActive = p.IsActive.Value
	}
	if p.IsPublic.Set {
		r.IsPublic = p.IsPublic.Value
	}
	if r.ExpiresAt != nil && !r.ExpiresAt.After(r.StartsAt) {
		return r, false, invalidCoupon("expires_at must be after starts_at")
	}
	if r.MaxUses != nil {
		if *r.MaxUses < r.UsesCount {
			return r, false, invalidCoupon("max_uses cannot be below the %d uses already made", r.UsesCount)
		}
		if r.MaxUsesPerUser > *r.MaxUses {
			return r, false, invalidCoupon("max_uses_per_user cannot exceed max_uses")
		}
	}
	return r, !couponMutableEqual(before, r), nil
}

func couponMutableEqual(a, b CouponRecord) bool {
	eqS := func(x, y *string) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	eqI64 := func(x, y *int64) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	eqI := func(x, y *int) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
	eqT := func(x, y *time.Time) bool { return (x == nil) == (y == nil) && (x == nil || x.Equal(*y)) }
	return eqS(a.Description, b.Description) && eqI64(a.MaxDiscountMinor, b.MaxDiscountMinor) &&
		a.MinOrderMinor == b.MinOrderMinor && eqI(a.MaxUses, b.MaxUses) &&
		a.MaxUsesPerUser == b.MaxUsesPerUser && a.StartsAt.Equal(b.StartsAt) &&
		eqT(a.ExpiresAt, b.ExpiresAt) && a.IsActive == b.IsActive && a.IsPublic == b.IsPublic
}

// PatchCoupon applies an edit under the coupon's row lock. A seller editing
// someone else's coupon gets ErrCouponNotFound (no oracle over ids); an admin
// editing a seller's coupon gets ErrCouponSellerReadOnly.
func (s *Store) PatchCoupon(ctx context.Context, id uuid.UUID, scope CouponPatchScope, p CouponPatch) (*CouponRecord, bool, error) {
	if scope.Admin && scope.Actor == uuid.Nil {
		return nil, false, ErrActorRequired
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	cur, err := scanCouponRecord(tx.QueryRow(ctx,
		`SELECT `+couponRecordColumns+` FROM coupons WHERE id = $1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, ErrCouponNotFound
	}
	if err != nil {
		return nil, false, err
	}
	switch {
	case scope.SellerID != nil:
		if cur.SellerID == nil || *cur.SellerID != *scope.SellerID {
			return nil, false, ErrCouponNotFound
		}
	case scope.Admin:
		if cur.SellerID != nil {
			return nil, false, ErrCouponSellerReadOnly
		}
	default:
		return nil, false, ErrCouponNotFound
	}

	next, changed, err := ApplyCouponPatch(*cur, p, time.Now())
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return cur, false, tx.Commit(ctx)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE coupons
		   SET description = $2, max_discount_amount_minor = $3, min_order_amount_minor = $4,
		       max_uses = $5, max_uses_per_user = $6, starts_at = $7, expires_at = $8,
		       is_active = $9, is_public = $10, updated_at = NOW()
		 WHERE id = $1`,
		id, next.Description, next.MaxDiscountMinor, next.MinOrderMinor,
		next.MaxUses, next.MaxUsesPerUser, next.StartsAt, next.ExpiresAt,
		next.IsActive, next.IsPublic); err != nil {
		return nil, false, fmt.Errorf("patch coupon: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE coupons SET `+couponLegacyRupees+` WHERE id = $1`, id); err != nil {
		return nil, false, fmt.Errorf("patch coupon mirror: %w", err)
	}
	out, err := scanCouponRecord(tx.QueryRow(ctx, `SELECT `+couponRecordColumns+` FROM coupons WHERE id = $1`, id))
	if err != nil {
		return nil, false, err
	}
	if scope.Admin {
		if err := insertAdminAudit(ctx, tx, adminAuditEntry{
			Actor: scope.Actor, Action: AuditActionCouponUpdate, TargetType: "coupon",
			TargetID: id, Before: cur, After: out, Reason: scope.Reason,
		}); err != nil {
			return nil, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return out, true, nil
}
