package postgres

// Stock adjustment — the only way a seller changes stock after a product exists.
//
// Before this file, stock was set exactly once. `CreateProduct` calls
// `UpsertInventory` with the quantity typed into the create form, and nothing
// else in the live surface ever writes `inventory_items.total_qty` upward. The
// one restock path that existed — bulk import — is behind the launch fence.
// So a seller who sold out was sold out permanently, and a seller who typed
// 10 when they meant 100 had no way back.
//
// ─── WHY A DELTA AND NOT A NEW TOTAL ────────────────────────────────────
//
// `UpsertInventory` sets total_qty absolutely. Exposed to a seller, that is a
// lost-update generator: the app renders "42 in stock", two units sell while
// the seller is typing, the seller submits "52" meaning "I added ten", and the
// two sold units are silently restored to the shelf. The seller knows one true
// number — how many units they added or removed — so that is what the API
// takes. The current total is the database's business, read under a lock.
//
// ─── WHY reserved_qty IS A FLOOR ────────────────────────────────────────
//
// Reserved units are already promised to orders that are mid-checkout. Letting
// total_qty fall below reserved_qty means those orders cannot be fulfilled and
// leaves `chk_inv_reserved_le_total` violated. A seller writing down damaged
// stock must be told the units are spoken for, not handed a 500 from a CHECK
// constraint.
//
// ─── WHY OWNERSHIP COMES FROM THE PRODUCT ───────────────────────────────
//
// `inventory_items.seller_id` is a copy, written at product creation. The
// authority for "who owns this variant" is the variant's product. This
// resolves ownership through that join every time, so a wrong or stale
// denormalised seller_id cannot hand one seller write access to another's
// stock.

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrVariantNotFound means no such variant exists at all.
	ErrVariantNotFound = errors.New("commerce: no such variant")

	// ErrNotYourVariant means the variant exists but belongs to another
	// seller. Handlers map this to 403, never 404 — the caller has already
	// proven the variant exists by other means, so hiding it buys nothing
	// and a 404 here reads as "deleted" and triggers client cleanup.
	ErrNotYourVariant = errors.New("commerce: this variant belongs to another seller")

	// ErrStockBelowReserved means the adjustment would drop total_qty under
	// the units already promised to live orders.
	ErrStockBelowReserved = errors.New("commerce: stock cannot fall below the units reserved for live orders")

	// ErrZeroAdjustment means a delta of zero — nothing to record.
	ErrZeroAdjustment = errors.New("commerce: a stock adjustment needs a non-zero delta")
)

// StockAdjustment is one seller-initiated change to a variant's stock.
type StockAdjustment struct {
	VariantID uuid.UUID
	SellerID  uuid.UUID // the caller's own seller id, verified against the product
	ActorID   uuid.UUID // the user behind the seller account, for the ledger
	Delta     int       // +restock, -writedown; never an absolute total
	Reason    string    // inventory_adjustments.reason_code
	Notes     string
}

// StockLevel is what a variant's stock looks like after an adjustment.
type StockLevel struct {
	VariantID   uuid.UUID `json:"variant_id"`
	TotalQty    int       `json:"total_qty"`
	ReservedQty int       `json:"reserved_qty"`
	Available   int       `json:"available"`
}

// AdjustStock applies a signed delta to a variant's stock in one transaction,
// under a row lock, and records it in both the audit table and the ledger.
//
// It returns the resulting level so the caller never has to guess and never
// has to re-read (which would race the next adjustment).
func (s *Store) AdjustStock(ctx context.Context, adj StockAdjustment) (*StockLevel, error) {
	if adj.Delta == 0 {
		return nil, ErrZeroAdjustment
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Ownership, from the product — not from the denormalised copy on
	// inventory_items. A variant with no inventory row still resolves here,
	// which is what lets the create-on-demand branch below stay safe.
	var owner uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT p.seller_id
		  FROM product_variants v
		  JOIN products p ON p.id = v.product_id
		 WHERE v.id = $1`, adj.VariantID).Scan(&owner)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, ErrVariantNotFound
	case err != nil:
		return nil, err
	case owner != adj.SellerID:
		return nil, ErrNotYourVariant
	}

	// Lock the inventory row. FOR UPDATE is the whole reason two sellers'
	// tabs, or a seller and a checkout, cannot lose one another's write.
	var total, reserved int
	err = tx.QueryRow(ctx, `
		SELECT total_qty, reserved_qty FROM inventory_items
		 WHERE variant_id = $1 FOR UPDATE`, adj.VariantID).Scan(&total, &reserved)
	if errors.Is(err, pgx.ErrNoRows) {
		// No inventory row: a variant created by a path that skipped
		// UpsertInventory. Without this branch such a variant is
		// permanently unstockable. Only a restock can create one — there is
		// nothing to write down.
		if adj.Delta < 0 {
			return nil, fmt.Errorf("commerce: variant %s has no stock to remove", adj.VariantID)
		}
		if _, err = tx.Exec(ctx, `
			INSERT INTO inventory_items (id, variant_id, seller_id, total_qty, updated_at)
			VALUES (gen_random_uuid(), $1, $2, 0, NOW())`, adj.VariantID, owner); err != nil {
			return nil, err
		}
		total, reserved = 0, 0
	} else if err != nil {
		return nil, err
	}

	newTotal := total + adj.Delta
	if newTotal < 0 {
		return nil, fmt.Errorf("commerce: cannot remove %d units from a stock of %d", -adj.Delta, total)
	}
	if newTotal < reserved {
		return nil, fmt.Errorf("%w: %d reserved, adjustment would leave %d",
			ErrStockBelowReserved, reserved, newTotal)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE inventory_items SET total_qty = $2, updated_at = NOW()
		 WHERE variant_id = $1`, adj.VariantID, newTotal); err != nil {
		return nil, err
	}

	if _, err = tx.Exec(ctx, `
		INSERT INTO inventory_adjustments
		    (variant_id, seller_id, delta, reason_code, notes, created_by)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),$6)`,
		adj.VariantID, owner, adj.Delta, adj.Reason, adj.Notes, nullUUID(adj.ActorID)); err != nil {
		return nil, err
	}

	// The ledger is the append-only account of every movement, the same one
	// checkout, payment commit and expiry write to. A seller adjustment that
	// skipped it would make the ledger stop reconciling against total_qty.
	if _, err = tx.Exec(ctx, `
		INSERT INTO inventory_ledger
		    (variant_id, delta_total, delta_reserved, reason, actor_id, actor_type, notes)
		VALUES ($1,$2,0,'seller_adjust',$3,'seller',NULLIF($4,''))`,
		adj.VariantID, adj.Delta, nullUUID(adj.ActorID), adj.Notes); err != nil {
		return nil, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &StockLevel{
		VariantID:   adj.VariantID,
		TotalQty:    newTotal,
		ReservedQty: reserved,
		Available:   newTotal - reserved,
	}, nil
}

// StockFor reads a variant's current level, ownership-checked.
func (s *Store) StockFor(ctx context.Context, variantID, sellerID uuid.UUID) (*StockLevel, error) {
	var owner uuid.UUID
	var total, reserved int
	err := s.db.QueryRow(ctx, `
		SELECT p.seller_id, COALESCE(i.total_qty,0), COALESCE(i.reserved_qty,0)
		  FROM product_variants v
		  JOIN products p ON p.id = v.product_id
		  LEFT JOIN inventory_items i ON i.variant_id = v.id
		 WHERE v.id = $1`, variantID).Scan(&owner, &total, &reserved)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVariantNotFound
	}
	if err != nil {
		return nil, err
	}
	if owner != sellerID {
		return nil, ErrNotYourVariant
	}
	return &StockLevel{VariantID: variantID, TotalQty: total, ReservedQty: reserved,
		Available: total - reserved}, nil
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

// ─── One variant, as its seller sees it ──────────────────────────────

// SellerVariant is a variant's own row: what it costs, and whether it is on
// sale. Money is paise.
type SellerVariant struct {
	VariantID uuid.UUID `json:"variant_id"`
	ProductID uuid.UUID `json:"product_id"`
	Title     string    `json:"title"`
	SKU       string    `json:"sku"`

	SellingPriceMinor int64 `json:"selling_price_minor"`
	MRPMinor          int64 `json:"mrp_minor"`

	// Status is the variant's own switch. Distinct from the product's
	// approval state: a seller can pause a variant of a product moderation
	// has approved, and the two answer different questions.
	Status string `json:"status"`

	TotalQty    int `json:"total_qty"`
	ReservedQty int `json:"reserved_qty"`
	Available   int `json:"available"`
}

// SellerVariantFor reads one of the caller's own variants.
//
// The prices come from the same COALESCE the pricing path uses, so an edit
// screen shows the figure a buyer would actually be charged rather than the
// NUMERIC mirror — which is the column the original repricing defect left
// stale.
func (s *Store) SellerVariantFor(ctx context.Context, variantID, sellerID uuid.UUID) (*SellerVariant, error) {
	var v SellerVariant
	var owner uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT v.id, p.id, p.title, v.sku,
		       COALESCE(NULLIF(v.selling_price_minor, 0), ROUND(v.selling_price * 100))::bigint,
		       COALESCE(NULLIF(v.mrp_minor, 0), ROUND(v.mrp * 100))::bigint,
		       v.status,
		       COALESCE(i.total_qty, 0), COALESCE(i.reserved_qty, 0),
		       p.seller_id
		  FROM product_variants v
		  JOIN products p ON p.id = v.product_id
		  LEFT JOIN inventory_items i ON i.variant_id = v.id
		 WHERE v.id = $1`, variantID).Scan(
		&v.VariantID, &v.ProductID, &v.Title, &v.SKU,
		&v.SellingPriceMinor, &v.MRPMinor, &v.Status,
		&v.TotalQty, &v.ReservedQty, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrVariantNotFound
	}
	if err != nil {
		return nil, err
	}
	if owner != sellerID {
		return nil, ErrNotYourVariant
	}
	v.Available = v.TotalQty - v.ReservedQty
	if v.Available < 0 {
		v.Available = 0
	}
	return &v, nil
}
