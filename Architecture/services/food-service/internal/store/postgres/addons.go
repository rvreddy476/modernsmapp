package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrAddonInvalid: an add-on is unknown, belongs to another menu item, is
// unavailable, duplicated, or breaks a group's min/max. HTTP 422
// FOOD_CART_ADDON_INVALID.
var ErrAddonInvalid = errors.New("add-on selection is invalid")

// CartAddonInput is one entry of the add-to-cart `addons` body field.
type CartAddonInput struct {
	AddonID  uuid.UUID
	Quantity int
}

type addonGroupRule struct {
	ID        uuid.UUID
	Name      string
	MinSelect int
	MaxSelect int
	Required  bool
}

type addonChoice struct {
	AddonID    uuid.UUID
	GroupID    uuid.UUID
	MenuItemID uuid.UUID
	Available  bool
}

// validateAddonSelection checks a requested add-on list against the menu
// item's groups and the add-on rows found by id.
func validateAddonSelection(menuItemID uuid.UUID, groups []addonGroupRule, found map[uuid.UUID]addonChoice, requested []CartAddonInput) error {
	seen := map[uuid.UUID]bool{}
	perGroup := map[uuid.UUID]int{}
	for _, r := range requested {
		if seen[r.AddonID] {
			return fmt.Errorf("%w: add-on %s listed twice", ErrAddonInvalid, r.AddonID)
		}
		seen[r.AddonID] = true
		if r.Quantity < 0 {
			return fmt.Errorf("%w: add-on quantity must be positive", ErrAddonInvalid)
		}
		c, ok := found[r.AddonID]
		if !ok {
			return fmt.Errorf("%w: add-on %s not found", ErrAddonInvalid, r.AddonID)
		}
		if c.MenuItemID != menuItemID {
			return fmt.Errorf("%w: add-on %s does not belong to this menu item", ErrAddonInvalid, r.AddonID)
		}
		if !c.Available {
			return fmt.Errorf("%w: add-on %s is unavailable", ErrAddonInvalid, r.AddonID)
		}
		perGroup[c.GroupID]++
	}
	for _, g := range groups {
		n := perGroup[g.ID]
		minSel := g.MinSelect
		if g.Required && minSel < 1 {
			minSel = 1
		}
		if n < minSel {
			return fmt.Errorf("%w: %q needs at least %d choice(s)", ErrAddonInvalid, g.Name, minSel)
		}
		if n > g.MaxSelect {
			return fmt.Errorf("%w: %q allows at most %d choice(s)", ErrAddonInvalid, g.Name, g.MaxSelect)
		}
	}
	return nil
}

// validateCartAddonsTx loads the item's groups and the requested add-ons and
// validates them. Quantities of 0 are normalised to 1 in place.
func validateCartAddonsTx(ctx context.Context, tx pgx.Tx, menuItemID uuid.UUID, requested []CartAddonInput) error {
	for i := range requested {
		if requested[i].Quantity == 0 {
			requested[i].Quantity = 1
		}
	}
	groupRows, err := tx.Query(ctx, `
		SELECT id, name, min_select, max_select, is_required
		FROM food.menu_item_addon_groups
		WHERE menu_item_id = $1
	`, menuItemID)
	if err != nil {
		return err
	}
	var groups []addonGroupRule
	for groupRows.Next() {
		var g addonGroupRule
		if err := groupRows.Scan(&g.ID, &g.Name, &g.MinSelect, &g.MaxSelect, &g.Required); err != nil {
			groupRows.Close()
			return err
		}
		groups = append(groups, g)
	}
	groupRows.Close()
	if err := groupRows.Err(); err != nil {
		return err
	}
	found := map[uuid.UUID]addonChoice{}
	if len(requested) > 0 {
		ids := make([]uuid.UUID, 0, len(requested))
		for _, r := range requested {
			ids = append(ids, r.AddonID)
		}
		rows, err := tx.Query(ctx, `
			SELECT a.id, a.addon_group_id, g.menu_item_id, a.is_available
			FROM food.menu_item_addons a
			JOIN food.menu_item_addon_groups g ON g.id = a.addon_group_id
			WHERE a.id = ANY($1::uuid[])
		`, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var c addonChoice
			if err := rows.Scan(&c.AddonID, &c.GroupID, &c.MenuItemID, &c.Available); err != nil {
				rows.Close()
				return err
			}
			found[c.AddonID] = c
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return validateAddonSelection(menuItemID, groups, found, requested)
}

// loadCartAddons attaches cart_item_addons (with current add-on name/price)
// to the matching items.
func loadCartAddons(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, cartID uuid.UUID, items []CartItem) error {
	rows, err := q.Query(ctx, `
		SELECT cia.cart_item_id, a.id, a.name, a.price::float8, cia.quantity
		FROM food.cart_item_addons cia
		JOIN food.cart_items ci ON ci.id = cia.cart_item_id
		JOIN food.menu_item_addons a ON a.id = cia.addon_id
		WHERE ci.cart_id = $1
		ORDER BY cia.created_at, cia.id
	`, cartID)
	if err != nil {
		return fmt.Errorf("load cart add-ons: %w", err)
	}
	defer rows.Close()
	index := make(map[uuid.UUID]int, len(items))
	for i := range items {
		index[items[i].ID] = i
	}
	for rows.Next() {
		var itemID uuid.UUID
		var a CartItemAddon
		if err := rows.Scan(&itemID, &a.AddonID, &a.Name, &a.UnitPrice, &a.Quantity); err != nil {
			return err
		}
		if i, ok := index[itemID]; ok {
			items[i].Addons = append(items[i].Addons, a)
		}
	}
	return rows.Err()
}

// priceCartItem computes one line: base = unit x qty; each add-on line =
// add-on price x add-on qty x item qty; the item's tax % applies to both.
func priceCartItem(item *CartItem) {
	item.LineTotal = roundMoney(item.UnitPrice * float64(item.Quantity))
	item.AddonTotal = 0
	for i := range item.Addons {
		a := &item.Addons[i]
		a.LineTotal = roundMoney(a.UnitPrice * float64(a.Quantity) * float64(item.Quantity))
		item.AddonTotal += a.LineTotal
	}
	item.AddonTotal = roundMoney(item.AddonTotal)
	item.TaxAmount = roundMoney((item.LineTotal + item.AddonTotal) * item.TaxPercentage / 100)
}
