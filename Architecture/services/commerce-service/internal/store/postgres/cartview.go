package postgres

// The cart, as a client can actually read it.
//
// ─── WHAT WAS WRONG ─────────────────────────────────────────────────────
//
// `GET /v1/commerce/cart` serialised the service's internal `CartSummary`
// straight to the wire. That struct carries no JSON tags, so Go emitted its
// FIELD NAMES, and its items are a three-way nesting of the raw row structs:
//
//	{"data":{"CartID":"…","ItemCount":1,"Items":[
//	  {"Item":{"price_snapshot":1299,…},"Product":{…},"Variant":{"selling_price":1299,…}}]}}
//
// The Android client asks for `cart_id`, a flat `items[]`, and `*_minor`
// paise. It got `CartID`, `Items`, and rupee floats. Nothing deserialised, so
// **every buyer's cart rendered as empty** no matter what was in it — the
// screen worked, the API worked, and the two had never been introduced.
//
// This is the same shape as B-LB-1: the defect was not in either side's logic
// but in what the client was HANDED. A store-level test cannot see it, and
// neither can a screen test with a hand-written fixture — only a request
// through the real route table against a real database.
//
// ─── AND THE MONEY ──────────────────────────────────────────────────────
//
// The nested shape also leaked `price_snapshot` and `selling_price` as
// NUMERIC rupees. Had the client been fixed to read that shape instead, the
// cart would have become the one surface where money crossed the wire as a
// float — precisely what the money gate exists to prevent. So the fix is a
// purpose-built projection in paise, not a set of JSON tags on the internal
// struct.
//
// ─── WHY THE PRICE IS READ TWICE ────────────────────────────────────────
//
// `price_snapshot_minor` is what the buyer saw when they added the line.
// `selling_price_minor` is what the catalogue says now. Both are returned so
// the cart can show "was ₹1,299" against a moved price, which is the same
// disagreement checkout refuses with PRICE_CHANGED. A cart that silently
// showed only the new price would make that refusal arrive without warning.

import (
	"context"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// CartView is the cart as the client reads it. Every money field is paise.
type CartView struct {
	CartID        uuid.UUID      `json:"cart_id"`
	Items         []CartViewLine `json:"items"`
	SubtotalMinor money.Paise    `json:"subtotal_minor"`
	ItemCount     int            `json:"item_count"`
	// SellerID and SellerName are set only when every line comes from one
	// seller. A mixed cart cannot check out (ErrMultipleSellers), and naming
	// one of the two sellers on the cart screen would tell the buyer the
	// wrong thing about why.
	SellerID   *uuid.UUID `json:"seller_id,omitempty"`
	SellerName string     `json:"seller_name,omitempty"`
}

// CartViewLine is one line, flat, in paise.
type CartViewLine struct {
	VariantID uuid.UUID `json:"variant_id"`
	ProductID uuid.UUID `json:"product_id"`
	Title     string    `json:"title"`
	SKU       string    `json:"sku,omitempty"`

	ImageMediaID *uuid.UUID `json:"image_media_id,omitempty"`
	// ImageURL is resolved server-side; empty when media-service could not
	// answer, which the client renders as a placeholder.
	ImageURL     string `json:"image_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`

	Quantity       int         `json:"quantity"`
	UnitPriceMinor money.Paise `json:"unit_price_minor"`
	LineTotalMinor money.Paise `json:"line_total_minor"`
	// PriceWasMinor is set ONLY when the catalogue price has moved since the
	// line was added. Nil is the ordinary case and means "nothing to warn
	// about" — distinct from zero, which would render as "was ₹0.00".
	PriceWasMinor *money.Paise `json:"price_was_minor,omitempty"`

	AvailableQty int       `json:"available_qty"`
	SellerID     uuid.UUID `json:"seller_id"`
	SellerName   string    `json:"seller_name,omitempty"`
	// Sellable is false when the product or variant has left the catalogue
	// since it was added — archived, paused, or un-approved. Checkout refuses
	// these (ErrProductUnavailable); the cart says so first.
	Sellable bool `json:"sellable"`
}

// CartViewFor reads a user's cart in one query, in paise.
func (s *Store) CartViewFor(ctx context.Context, cartID uuid.UUID) (*CartView, error) {
	rows, err := s.db.Query(ctx, `
		SELECT ci.variant_id, ci.product_id, p.title, v.sku,
		       COALESCE(v.image_media_id, p.primary_image_media_id),
		       ci.quantity,
		       COALESCE(NULLIF(ci.price_snapshot_minor, 0), ROUND(ci.price_snapshot * 100))::bigint,
		       COALESCE(NULLIF(v.selling_price_minor, 0), ROUND(v.selling_price * 100))::bigint,
		       COALESCE(i.total_qty - i.reserved_qty, 0),
		       p.seller_id, COALESCE(s.store_name, ''),
		       (p.status = 'active' AND p.approval_status = 'approved' AND v.status = 'active')
		  FROM cart_items ci
		  JOIN product_variants v ON v.id = ci.variant_id
		  JOIN products p         ON p.id = v.product_id
		  LEFT JOIN sellers s     ON s.id = p.seller_id
		  LEFT JOIN inventory_items i ON i.variant_id = v.id
		 WHERE ci.cart_id = $1
		 ORDER BY ci.added_at`, cartID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	view := &CartView{CartID: cartID, Items: []CartViewLine{}}
	sellers := map[uuid.UUID]string{}
	for rows.Next() {
		var l CartViewLine
		var snapshot, current int64
		if err := rows.Scan(&l.VariantID, &l.ProductID, &l.Title, &l.SKU,
			&l.ImageMediaID, &l.Quantity, &snapshot, &current,
			&l.AvailableQty, &l.SellerID, &l.SellerName, &l.Sellable); err != nil {
			return nil, err
		}

		// The buyer pays what the catalogue says now — checkout re-prices
		// under a row lock and refuses a moved total, so showing the stale
		// snapshot here would set an expectation checkout then breaks.
		l.UnitPriceMinor = money.Paise(current)
		l.LineTotalMinor = money.Paise(current * int64(l.Quantity))
		if snapshot > 0 && snapshot != current {
			was := money.Paise(snapshot)
			l.PriceWasMinor = &was
		}
		if l.AvailableQty < 0 {
			l.AvailableQty = 0
		}

		view.SubtotalMinor += l.LineTotalMinor
		view.ItemCount += l.Quantity
		sellers[l.SellerID] = l.SellerName
		view.Items = append(view.Items, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if len(sellers) == 1 {
		for id, name := range sellers {
			sellerID := id
			view.SellerID = &sellerID
			view.SellerName = name
		}
	}
	return view, nil
}
