package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/food-service/internal/payments"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Substitution is one row in food.order_substitutions.
//
// Audit 2026-10-30 M2: the money fields are integer paise computed by the
// server. PriceDiff (rupees) is derived from PriceDiffPaise for older
// clients. A nil PriceDiffPaise is a row written before the fix; it moved no
// money and still moves none.
type Substitution struct {
	ID                uuid.UUID  `json:"id"`
	OrderID           uuid.UUID  `json:"order_id"`
	OriginalItemID    uuid.UUID  `json:"original_item_id"`
	OriginalItemName  string     `json:"original_item_name"`
	SuggestedItemID   *uuid.UUID `json:"suggested_item_id,omitempty"`
	SuggestedItemName *string    `json:"suggested_item_name,omitempty"`
	PriceDiff         float64    `json:"price_diff"`
	PriceDiffPaise    *int64     `json:"price_diff_paise,omitempty"`
	SupplyDiffPaise   *int64     `json:"supply_diff_paise,omitempty"`
	TaxDiffPaise      *int64     `json:"tax_diff_paise,omitempty"`
	RefundID          *uuid.UUID `json:"refund_id,omitempty"`
	Note              *string    `json:"note,omitempty"`
	Status            string     `json:"status"`
	ProposedBy        uuid.UUID  `json:"proposed_by"`
	RespondedBy       *uuid.UUID `json:"responded_by,omitempty"`
	RespondedAt       *string    `json:"responded_at,omitempty"`
	CreatedAt         string     `json:"created_at"`
}

// ProposeSubstitutionInput is the request body for partner-side
// substitution proposal.
type ProposeSubstitutionInput struct {
	OrderID           uuid.UUID
	OriginalItemID    uuid.UUID
	SuggestedItemID   *uuid.UUID
	SuggestedItemName *string
	// PriceDiff is the legacy float the partner sent. It is never used as
	// money: a non-zero value without PriceDiffPaise is refused.
	PriceDiff float64
	// PriceDiffPaise is the per-unit price difference (ex-tax) of a free-text
	// suggestion. It is ignored when SuggestedItemID is set: the server reads
	// the suggested item's menu price.
	PriceDiffPaise *int64
	Note           *string
	ProposedBy     uuid.UUID
}

var (
	// ErrSubstitutionPriceIncrease: 422 FOOD_SUBSTITUTION_PRICE_INCREASE. A
	// dearer substitute would change the charge, and there is no re-charge path.
	ErrSubstitutionPriceIncrease = errors.New("a substitution may not cost more than the original item: there is no way to charge the difference")
	// ErrSubstitutionPricePaiseRequired: 422 FOOD_SUBSTITUTION_PRICE_PAISE_REQUIRED.
	ErrSubstitutionPricePaiseRequired = errors.New("send the price difference as integer price_diff_paise")
	// ErrSubstitutionItemInvalid: 422 FOOD_SUBSTITUTION_ITEM_INVALID.
	ErrSubstitutionItemInvalid = errors.New("the suggested item is not an available item of this restaurant")
	// ErrSubstitutionNotAllowed: 409 FOOD_SUBSTITUTION_NOT_ALLOWED (order
	// status, an open substitution on the line, or nothing left to refund).
	ErrSubstitutionNotAllowed = errors.New("the substitution cannot be made on this order now")
	// ErrSubstitutionRefundUnavailable: 409 FOOD_SUBSTITUTION_REFUND_UNAVAILABLE.
	// A cheaper substitute needs a captured online or wallet payment to refund.
	ErrSubstitutionRefundUnavailable = errors.New("the price difference cannot be refunded on this order")
)

// substitutableStatuses are the order statuses a substitution may be proposed
// and approved in: the food is not yet cooked or not yet handed over.
var substitutableStatuses = map[string]bool{"CONFIRMED": true, "PREPARING": true}

// SubstitutionPrice is a substitution's price difference in paise.
type SubstitutionPrice struct {
	SupplyDiffPaise int64
	TaxDiffPaise    int64
	PriceDiffPaise  int64
}

// PriceSubstitution is the pure M2 rule: the supply difference is
// (suggested - original unit price) x quantity, the tax difference is the GST
// on it at the line's rate (half up, sign kept), and the price difference is
// their sum. A positive difference is refused.
func PriceSubstitution(originalUnitPaise, suggestedUnitPaise, quantity, taxRateBP int64) (SubstitutionPrice, error) {
	if quantity <= 0 || originalUnitPaise < 0 || suggestedUnitPaise < 0 || taxRateBP < 0 {
		return SubstitutionPrice{}, ErrSubstitutionItemInvalid
	}
	supply := (suggestedUnitPaise - originalUnitPaise) * quantity
	mag := supply
	if mag < 0 {
		mag = -mag
	}
	tax := (mag*taxRateBP + 5000) / 10000
	if supply < 0 {
		tax = -tax
	}
	p := SubstitutionPrice{SupplyDiffPaise: supply, TaxDiffPaise: tax, PriceDiffPaise: supply + tax}
	if p.PriceDiffPaise > 0 {
		return SubstitutionPrice{}, ErrSubstitutionPriceIncrease
	}
	return p, nil
}

const substitutionColumns = `id, order_id, original_item_id, original_item_name,
	suggested_item_id, suggested_item_name, price_diff::float8, price_diff_paise, supply_diff_paise,
	tax_diff_paise, refund_id, note, status::text,
	proposed_by, responded_by, responded_at::text, created_at::text`

func scanSubstitution(row pgx.Row) (*Substitution, error) {
	var sub Substitution
	if err := row.Scan(&sub.ID, &sub.OrderID, &sub.OriginalItemID, &sub.OriginalItemName,
		&sub.SuggestedItemID, &sub.SuggestedItemName, &sub.PriceDiff, &sub.PriceDiffPaise, &sub.SupplyDiffPaise,
		&sub.TaxDiffPaise, &sub.RefundID, &sub.Note,
		&sub.Status, &sub.ProposedBy, &sub.RespondedBy, &sub.RespondedAt, &sub.CreatedAt); err != nil {
		return nil, err
	}
	return &sub, nil
}

// ProposeSubstitution inserts a substitution proposal. The partner
// must own the restaurant for the order — verified inside the tx. The price
// difference is computed here in paise (audit M2).
func (s *Store) ProposeSubstitution(ctx context.Context, ownerID uuid.UUID, in ProposeSubstitutionInput) (*Substitution, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var orderRestaurantID uuid.UUID
	var orderStatus string
	if err := tx.QueryRow(ctx, `
		SELECT restaurant_id, status::text
		FROM food.orders
		WHERE id = $1
		FOR UPDATE
	`, in.OrderID).Scan(&orderRestaurantID, &orderStatus); err != nil {
		return nil, err
	}
	var owned int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.restaurants
		WHERE id = $1 AND owner_user_id = $2
	`, orderRestaurantID, ownerID).Scan(&owned); err != nil {
		return nil, err
	}
	if owned == 0 {
		return nil, pgx.ErrNoRows
	}
	if !substitutableStatuses[orderStatus] {
		return nil, fmt.Errorf("%w: cannot substitute in status %s", ErrSubstitutionNotAllowed, orderStatus)
	}
	var originalName string
	var originalUnit, quantity, rateBP int64
	if err := tx.QueryRow(ctx, `
		SELECT item_name_snapshot,
			COALESCE(unit_price_paise, ROUND(unit_price_snapshot * 100)::bigint),
			quantity::bigint,
			COALESCE(tax_rate_bp, ROUND(tax_percentage_snapshot * 100)::integer, 0)::bigint
		FROM food.order_items
		WHERE order_id = $1 AND id = $2
	`, in.OrderID, in.OriginalItemID).Scan(&originalName, &originalUnit, &quantity, &rateBP); err != nil {
		return nil, fmt.Errorf("original item lookup: %w", err)
	}
	var open bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM food.order_substitutions
			WHERE order_id = $1 AND original_item_id = $2 AND status IN ('proposed', 'approved'))
	`, in.OrderID, in.OriginalItemID).Scan(&open); err != nil {
		return nil, err
	}
	if open {
		return nil, fmt.Errorf("%w: this line already has an open or approved substitution", ErrSubstitutionNotAllowed)
	}

	suggestedName := in.SuggestedItemName
	var suggestedUnit int64
	if in.SuggestedItemID != nil {
		var name string
		if err := tx.QueryRow(ctx, `
			SELECT name, ROUND(base_price * 100)::bigint
			FROM food.menu_items
			WHERE id = $1 AND restaurant_id = $2 AND is_active AND is_available
		`, *in.SuggestedItemID, orderRestaurantID).Scan(&name, &suggestedUnit); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrSubstitutionItemInvalid
			}
			return nil, err
		}
		suggestedName = &name
	} else {
		var unitDiff int64
		switch {
		case in.PriceDiffPaise != nil:
			unitDiff = *in.PriceDiffPaise
		case in.PriceDiff != 0:
			return nil, ErrSubstitutionPricePaiseRequired
		}
		if unitDiff > 0 {
			return nil, ErrSubstitutionPriceIncrease
		}
		if originalUnit+unitDiff < 0 {
			return nil, ErrSubstitutionItemInvalid
		}
		suggestedUnit = originalUnit + unitDiff
	}
	price, err := PriceSubstitution(originalUnit, suggestedUnit, quantity, rateBP)
	if err != nil {
		return nil, err
	}
	sub, err := scanSubstitution(tx.QueryRow(ctx, `
		INSERT INTO food.order_substitutions
			(order_id, original_item_id, original_item_name,
			 suggested_item_id, suggested_item_name, price_diff, note, proposed_by,
			 price_diff_paise, supply_diff_paise, tax_diff_paise)
		VALUES ($1, $2, $3, $4, $5, ($8::bigint)::numeric / 100, $6, $7, $8, $9, $10)
		RETURNING `+substitutionColumns,
		in.OrderID, in.OriginalItemID, originalName,
		in.SuggestedItemID, suggestedName, in.Note, in.ProposedBy,
		price.PriceDiffPaise, price.SupplyDiffPaise, price.TaxDiffPaise,
	))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return sub, nil
}

// RespondToSubstitution sets status to approved/declined/cancelled.
// `customerID` ownership of the order is enforced. Audit M2: an approval is
// only taken while the order is still CONFIRMED or PREPARING, and approving a
// cheaper substitute queues the partial refund of the difference through the
// durable refund path in the same transaction.
func (s *Store) RespondToSubstitution(ctx context.Context, customerID, subID uuid.UUID, newStatus string) (*Substitution, error) {
	if newStatus != "approved" && newStatus != "declined" && newStatus != "cancelled" {
		return nil, fmt.Errorf("invalid response status: %s", newStatus)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Order first (the lock order every money writer uses), then the row.
	var orderID uuid.UUID
	var orderStatus string
	if err := tx.QueryRow(ctx, `
		SELECT o.id, o.status::text
		FROM food.order_substitutions s
		JOIN food.orders o ON o.id = s.order_id
		WHERE s.id = $1 AND o.user_id = $2
		FOR UPDATE OF o
	`, subID, customerID).Scan(&orderID, &orderStatus); err != nil {
		return nil, err
	}
	var currentStatus string
	var priceDiff *int64
	if err := tx.QueryRow(ctx, `
		SELECT status::text, price_diff_paise FROM food.order_substitutions WHERE id = $1 FOR UPDATE
	`, subID).Scan(&currentStatus, &priceDiff); err != nil {
		return nil, err
	}
	if currentStatus != "proposed" {
		return nil, fmt.Errorf("substitution already responded: %s", currentStatus)
	}
	var refundID *uuid.UUID
	if newStatus == "approved" {
		if !substitutableStatuses[orderStatus] {
			return nil, fmt.Errorf("%w: order is %s", ErrSubstitutionNotAllowed, orderStatus)
		}
		if priceDiff != nil && *priceDiff < 0 {
			id, err := requestSubstitutionRefundTx(ctx, tx, orderID, subID, -*priceDiff)
			if err != nil {
				return nil, err
			}
			refundID = &id
		}
	}
	sub, err := scanSubstitution(tx.QueryRow(ctx, `
		UPDATE food.order_substitutions
		SET status = $2::food.substitution_status,
			responded_by = $3,
			responded_at = NOW(),
			refund_id = COALESCE($4, refund_id)
		WHERE id = $1
		RETURNING `+substitutionColumns,
		subID, newStatus, customerID, refundID,
	))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return sub, nil
}

// requestSubstitutionRefundTx queues the partial refund of an approved
// cheaper substitution: a PENDING food.refunds row (system actor) capped at
// what is still unrefunded, announced as refund_requested. The order status
// does not change; payment.refunded (partially_refunded) settles the row.
func requestSubstitutionRefundTx(ctx context.Context, tx pgx.Tx, orderID, subID uuid.UUID, amountMinor int64) (uuid.UUID, error) {
	var paymentStatus, method, intentID string
	var paymentID *uuid.UUID
	var orderMinor, requestedMinor int64
	if err := tx.QueryRow(ctx, `
		SELECT o.payment_status::text, o.payment_method::text,
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint),
			p.id, COALESCE(p.provider_payment_id, ''),
			COALESCE((
				SELECT SUM(ROUND(amount * 100)::bigint) FROM food.refunds
				WHERE order_id = o.id AND status IN ('PENDING', 'SUBMITTED', 'PROCESSED')
			), 0)::bigint
		FROM food.orders o
		LEFT JOIN LATERAL (
			SELECT id, provider_payment_id FROM food.payments
			WHERE order_id = o.id ORDER BY created_at DESC LIMIT 1
		) p ON TRUE
		WHERE o.id = $1
	`, orderID).Scan(&paymentStatus, &method, &orderMinor, &paymentID, &intentID, &requestedMinor); err != nil {
		return uuid.Nil, err
	}
	switch {
	case paymentID == nil,
		paymentStatus != "CAPTURED" && paymentStatus != "PARTIALLY_REFUNDED",
		method != payments.StoreOnline && method != payments.StoreWallet,
		method == payments.StoreOnline && intentID == "":
		return uuid.Nil, fmt.Errorf("%w: payment is %s %s", ErrSubstitutionRefundUnavailable, method, paymentStatus)
	}
	if amountMinor <= 0 || amountMinor > orderMinor-requestedMinor {
		return uuid.Nil, fmt.Errorf("%w: nothing left to refund for this substitution", ErrSubstitutionNotAllowed)
	}
	var refundID uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO food.refunds (order_id, payment_id, amount, reason, requested_by, status)
		VALUES ($1, $2, ($3::bigint)::numeric / 100, $4, NULL, 'PENDING')
		RETURNING id
	`, orderID, *paymentID, amountMinor, "system: substitution "+subID.String()+" price difference").Scan(&refundID); err != nil {
		return uuid.Nil, err
	}
	if err := enqueueOrderEventTx(ctx, tx, orderID, eventRefundRequested, ""); err != nil {
		return uuid.Nil, err
	}
	return refundID, nil
}

// ListSubstitutions returns every substitution for an order. Either
// the customer or the owning partner can read.
func (s *Store) ListSubstitutions(ctx context.Context, userID, orderID uuid.UUID) ([]Substitution, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+substitutionColumns+`
		FROM food.order_substitutions s
		WHERE s.order_id = $1
		  AND EXISTS (
		    SELECT 1 FROM food.orders o
		    WHERE o.id = s.order_id
		      AND (o.user_id = $2 OR EXISTS (
		        SELECT 1 FROM food.restaurants r
		        WHERE r.id = o.restaurant_id AND r.owner_user_id = $2
		      ))
		  )
		ORDER BY s.created_at DESC
	`, orderID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Substitution
	for rows.Next() {
		sub, err := scanSubstitution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// LogStockoutChange appends to the audit log when a partner toggles
// item availability. Called from SetMenuItemAvailability inside the
// same transaction.
func (s *Store) LogStockoutChange(ctx context.Context, tx pgx.Tx, menuItemID, restaurantID, changedBy uuid.UUID, isAvailable bool, reason string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO food.menu_item_stockouts
			(menu_item_id, restaurant_id, is_available, reason, changed_by)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
	`, menuItemID, restaurantID, isAvailable, reason, changedBy)
	return err
}
