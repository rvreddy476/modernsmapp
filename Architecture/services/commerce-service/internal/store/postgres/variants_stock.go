// Product variants and stock: variant create/read/update/archive, the
// inventory row, and reserving, releasing and deducting stock. Moved out of
// store.go unchanged.

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

// ─── Product Variants ────────────────────────────────────────

// CreateVariant inserts one variant on its own. The statement lives in
// insertVariantTx, which the transactional create path calls directly.
func (s *Store) CreateVariant(ctx context.Context, v *ProductVariant) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := insertVariantTx(ctx, tx, v); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertVariantTx(ctx context.Context, tx pgx.Tx, v *ProductVariant) error {
	if v.ID == uuid.Nil {
		v.ID = uuid.New()
	}
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
	_, err := tx.Exec(ctx, `
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
	if err != nil {
		return err
	}

	// A variant belongs to the seller's OFFER on the item, not to the item.
	// Migration 027 added the column, nullable, and pointed every existing
	// variant at its product's backfilled offer; this keeps new ones pointed
	// the same way. Nothing reads it yet — see internal/store/postgres/
	// productoffers.go for why the shadow is written before it is read.
	//
	// Here for the same reason the offer insert is inside insertProductTx:
	// this is the only statement in the package that creates a variant.
	return linkVariantToOfferTx(ctx, tx, v.ID, v.ProductID)
}

// variantMoneyAndStockSQL is the SELECT-list fragment every variant read
// appends, so the three fields a client renders a variant from come from
// ONE definition rather than from whichever query happened to be edited.
//
// The COALESCE(NULLIF(...)) shape is deliberately identical to the one
// ListProductsFiltered uses for `min_price_minor`: migration 007 defaulted
// the minor columns to 0 rather than NULL, so a plain COALESCE finds a
// non-NULL zero on an unmigrated row and advertises the item as free.
//
// `v` must be the alias of product_variants in the enclosing query.
const variantMoneyAndStockSQL = `
	COALESCE(NULLIF(v.selling_price_minor, 0), ROUND(v.selling_price*100))::bigint AS selling_price_minor,
	COALESCE(NULLIF(v.mrp_minor, 0),           ROUND(v.mrp*100))::bigint           AS mrp_minor,
	(SELECT GREATEST(i.total_qty - i.reserved_qty, 0)
	   FROM inventory_items i WHERE i.variant_id = v.id) AS available_qty`

func (s *Store) GetVariantByID(ctx context.Context, id uuid.UUID) (*ProductVariant, error) {
	var v ProductVariant
	err := s.db.QueryRow(ctx, `SELECT v.id,v.product_id,v.sku,v.barcode,v.option_1_name,v.option_1_value,
		v.option_2_name,v.option_2_value,v.option_3_name,v.option_3_value,v.mrp,v.selling_price,v.cost_price,
		v.currency_code,v.status,v.image_media_id,v.weight_grams,v.created_at,v.updated_at,`+
		variantMoneyAndStockSQL+`
		FROM product_variants v WHERE v.id=$1`, id).Scan(
		&v.ID, &v.ProductID, &v.SKU, &v.Barcode, &v.Option1Name, &v.Option1Value,
		&v.Option2Name, &v.Option2Value, &v.Option3Name, &v.Option3Value,
		&v.MRP, &v.SellingPrice, &v.CostPrice, &v.CurrencyCode, &v.Status,
		&v.ImageMediaID, &v.WeightGrams, &v.CreatedAt, &v.UpdatedAt,
		&v.SellingPriceMinor, &v.MRPMinor, &v.AvailableQty,
	)
	return &v, err
}

// GetVariantsByProduct is what product detail, the variants list and the
// seller's product views all render from — so it is where the variant's
// paise and available stock have to come from.
func (s *Store) GetVariantsByProduct(ctx context.Context, productID uuid.UUID) ([]*ProductVariant, error) {
	rows, err := s.db.Query(ctx, `SELECT v.id,v.product_id,v.sku,v.option_1_name,v.option_1_value,
		v.option_2_name,v.option_2_value,v.mrp,v.selling_price,v.currency_code,v.status,
		v.image_media_id,v.created_at,`+variantMoneyAndStockSQL+`
		FROM product_variants v WHERE v.product_id=$1 AND v.status='active' ORDER BY v.created_at`, productID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var variants []*ProductVariant
	for rows.Next() {
		var v ProductVariant
		if err := rows.Scan(&v.ID, &v.ProductID, &v.SKU, &v.Option1Name, &v.Option1Value,
			&v.Option2Name, &v.Option2Value, &v.MRP, &v.SellingPrice, &v.CurrencyCode,
			&v.Status, &v.ImageMediaID, &v.CreatedAt,
			&v.SellingPriceMinor, &v.MRPMinor, &v.AvailableQty); err != nil {
			return nil, err
		}
		variants = append(variants, &v)
	}
	return variants, rows.Err()
}

// UpdateVariant patches the mutable fields of an existing variant.
// Returns ErrProductNotFound when the variant doesn't exist. product_id is
// intentionally NOT updatable: it is the foreign key that defines what the
// variant belongs to, and moving it would break orders + carts.
//
// `sku` IS updatable here, and was not: it was silently dropped, so a seller
// who corrected a typo in a draft's SKU was told 200 and saw the old code
// come back. Whether a given caller may change it is the SERVICE's rule
// (locked once the listing is approved, see service.ErrSKULocked); the store
// writes what it is handed and reports a collision as ErrDuplicateSKU.
//
// `revalidate` sends the parent product back to review in the same
// transaction — see revalidateProductTx.
func (s *Store) UpdateVariant(ctx context.Context, id uuid.UUID, updates map[string]any, revalidate bool) error {
	if len(updates) == 0 && !revalidate {
		return nil
	}
	allowed := map[string]bool{
		"sku":           true,
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
	if len(setClauses) == 0 && !revalidate {
		return nil
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var productID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT product_id FROM product_variants WHERE id = $1`, id).Scan(&productID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrProductNotFound
		}
		return err
	}
	if len(setClauses) > 0 {
		setClauses = append(setClauses, "updated_at = NOW()")
		args = append(args, id)
		q := "UPDATE product_variants SET " + strings.Join(setClauses, ", ") +
			" WHERE id = $" + strconv.Itoa(idx)
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			sku, _ := updates["sku"].(string)
			return asDuplicateSKU(err, sku)
		}
	}
	// An option or image change on an approved listing is a
	// reviewed-content change; the review state moves in the same
	// transaction. See revalidateProductTx. (A price change is not: it goes
	// live at once, see service.checkVariantPatch.)
	if revalidate {
		if err := revalidateProductTx(ctx, tx, productID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
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

// UpsertInventory opens (or resets) a variant's stock row on its own. The
// statement lives in upsertInventoryTx, which the transactional create calls.
func (s *Store) UpsertInventory(ctx context.Context, variantID, sellerID uuid.UUID, totalQty int) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := upsertInventoryTx(ctx, tx, variantID, sellerID, totalQty); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
