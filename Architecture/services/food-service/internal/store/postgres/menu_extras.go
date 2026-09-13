package postgres

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Menu variants and add-on groups on the partner routes (B8). The customer
// side already prices both (customer.go loadCart, addons.go); these routes let
// the restaurant manage them.
//
// Ownership is checked item -> restaurant inside the statement that reads or
// writes, so another restaurant's item, variant, group or add-on is
// pgx.ErrNoRows (404), never a partial write. Prices are taken in paise (or
// as rupees with at most two decimals) and stored exactly as NUMERIC(12,2).

const (
	CodeMenuNameRequired        = "FOOD_MENU_NAME_REQUIRED"
	CodeMenuPriceRequired       = "FOOD_MENU_PRICE_REQUIRED"
	CodeMenuPriceInvalid        = "FOOD_MENU_PRICE_INVALID"
	CodeMenuPriceMismatch       = "FOOD_MENU_PRICE_MISMATCH"
	CodeAddonGroupSelectInvalid = "FOOD_ADDON_GROUP_SELECT_INVALID"
)

// maxMenuPricePaise is the largest NUMERIC(12,2) amount, in paise.
const maxMenuPricePaise int64 = 999_999_999_999

func menuFieldErr(code, field, msg string) error {
	return &onboarding.FieldError{Code: code, Field: field, Message: msg}
}

// ResolvePricePaise reads a price given as price_paise, as price (rupees), or
// both (they must agree). Nil with no error when neither is given and the
// price is optional.
func ResolvePricePaise(paise *int64, rupees *float64, required bool) (*int64, error) {
	var fromRupees *int64
	if rupees != nil {
		r := *rupees
		if math.IsNaN(r) || math.IsInf(r, 0) || r < 0 {
			return nil, menuFieldErr(CodeMenuPriceInvalid, "price", "price must be zero or more")
		}
		p := math.Round(r * 100)
		if math.Abs(r*100-p) > 1e-6 || p > float64(maxMenuPricePaise) {
			return nil, menuFieldErr(CodeMenuPriceInvalid, "price", "price must be rupees with at most two decimal places")
		}
		v := int64(p)
		fromRupees = &v
	}
	if paise != nil && (*paise < 0 || *paise > maxMenuPricePaise) {
		return nil, menuFieldErr(CodeMenuPriceInvalid, "price_paise", "price_paise must be zero or more")
	}
	switch {
	case paise != nil && fromRupees != nil:
		if *paise != *fromRupees {
			return nil, menuFieldErr(CodeMenuPriceMismatch, "price_paise", "price and price_paise disagree")
		}
		return paise, nil
	case paise != nil:
		return paise, nil
	case fromRupees != nil:
		return fromRupees, nil
	case required:
		return nil, menuFieldErr(CodeMenuPriceRequired, "price_paise", "price_paise (or price) is required")
	}
	return nil, nil
}

// menuName trims a name; nil stays nil unless required.
func menuName(in *string, maxLen int, required bool) (*string, error) {
	if in == nil {
		if required {
			return nil, menuFieldErr(CodeMenuNameRequired, "name", "name is required")
		}
		return nil, nil
	}
	v := strings.TrimSpace(*in)
	if v == "" || utf8.RuneCountInString(v) > maxLen {
		return nil, menuFieldErr(CodeMenuNameRequired, "name", "name is required and at most the column length")
	}
	return &v, nil
}

// ValidateAddonGroupRule: at least one choice allowed, and no more required
// than allowed.
func ValidateAddonGroupRule(minSelect, maxSelect int) error {
	if minSelect < 0 || maxSelect < 1 || maxSelect < minSelect || maxSelect > 100 {
		return menuFieldErr(CodeAddonGroupSelectInvalid, "max_select", "min_select must be 0 or more and max_select at least 1 and at least min_select")
	}
	return nil
}

// ─── Shapes ─────────────────────────────────────────────────────────────────

type MenuVariant struct {
	ID          uuid.UUID `json:"id"`
	MenuItemID  uuid.UUID `json:"menu_item_id"`
	Name        string    `json:"name"`
	Price       float64   `json:"price"`
	PricePaise  int64     `json:"price_paise"`
	IsAvailable bool      `json:"is_available"`
	SortOrder   int       `json:"sort_order"`
}

type MenuAddon struct {
	ID           uuid.UUID `json:"id"`
	AddonGroupID uuid.UUID `json:"addon_group_id"`
	Name         string    `json:"name"`
	Price        float64   `json:"price"`
	PricePaise   int64     `json:"price_paise"`
	IsAvailable  bool      `json:"is_available"`
	SortOrder    int       `json:"sort_order"`
}

type MenuAddonGroup struct {
	ID         uuid.UUID   `json:"id"`
	MenuItemID uuid.UUID   `json:"menu_item_id"`
	Name       string      `json:"name"`
	MinSelect  int         `json:"min_select"`
	MaxSelect  int         `json:"max_select"`
	IsRequired bool        `json:"is_required"`
	SortOrder  int         `json:"sort_order"`
	Addons     []MenuAddon `json:"addons"`
}

// MenuPriceInput is a variant or add-on body: every field optional on update.
type MenuPriceInput struct {
	Name        *string
	Price       *float64
	PricePaise  *int64
	IsAvailable *bool
	SortOrder   *int
}

type MenuAddonGroupInput struct {
	Name       *string
	MinSelect  *int
	MaxSelect  *int
	IsRequired *bool
	SortOrder  *int
}

// PartnerMenuItem is GET /v1/food/partner/menu/items/:itemId.
type PartnerMenuItem struct {
	MenuItem
	Variants    []MenuVariant    `json:"variants"`
	AddonGroups []MenuAddonGroup `json:"addon_groups"`
}

type scanner interface {
	Scan(dest ...any) error
}

const variantColumns = `id, menu_item_id, name, price::float8, is_available, sort_order`

func scanVariant(row scanner) (*MenuVariant, error) {
	var v MenuVariant
	if err := row.Scan(&v.ID, &v.MenuItemID, &v.Name, &v.Price, &v.IsAvailable, &v.SortOrder); err != nil {
		return nil, err
	}
	v.PricePaise = PaiseOf(v.Price)
	return &v, nil
}

const addonColumns = `id, addon_group_id, name, price::float8, is_available, sort_order`

func scanAddon(row scanner) (*MenuAddon, error) {
	var a MenuAddon
	if err := row.Scan(&a.ID, &a.AddonGroupID, &a.Name, &a.Price, &a.IsAvailable, &a.SortOrder); err != nil {
		return nil, err
	}
	a.PricePaise = PaiseOf(a.Price)
	return &a, nil
}

const groupColumns = `id, menu_item_id, name, min_select, max_select, is_required, sort_order`

func scanGroup(row scanner) (*MenuAddonGroup, error) {
	var g MenuAddonGroup
	if err := row.Scan(&g.ID, &g.MenuItemID, &g.Name, &g.MinSelect, &g.MaxSelect, &g.IsRequired, &g.SortOrder); err != nil {
		return nil, err
	}
	g.Addons = []MenuAddon{}
	return &g, nil
}

// requireOwnedMenuItem: an active item of a restaurant the caller owns.
func requireOwnedMenuItem(ctx context.Context, q rowQuerier, ownerID, itemID uuid.UUID) error {
	var id uuid.UUID
	return q.QueryRow(ctx, `
		SELECT i.id
		FROM food.menu_items i
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE i.id = $1 AND r.owner_user_id = $2 AND i.is_active
	`, itemID, ownerID).Scan(&id)
}

// ─── Partner menu item ──────────────────────────────────────────────────────

func (s *Store) GetPartnerMenuItem(ctx context.Context, ownerID, itemID uuid.UUID) (*PartnerMenuItem, error) {
	var out PartnerMenuItem
	var categoryID *uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT i.id, i.restaurant_id, i.category_id, i.name, COALESCE(i.description, ''), i.food_type::text,
			i.base_price::float8, COALESCE(i.discount_price, 0)::float8, COALESCE(i.image_url, ''),
			i.preparation_minutes, i.is_available, i.is_recommended, i.tax_percentage::float8, i.media_id
		FROM food.menu_items i
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE i.id = $1 AND r.owner_user_id = $2 AND i.is_active
	`, itemID, ownerID).Scan(&out.ID, &out.RestaurantID, &categoryID, &out.Name, &out.Description, &out.FoodType,
		&out.BasePrice, &out.DiscountPrice, &out.ImageURL, &out.PreparationMinutes, &out.IsAvailable,
		&out.IsRecommended, &out.TaxPercentage, &out.ImageMediaID); err != nil {
		return nil, err
	}
	if categoryID != nil {
		out.CategoryID = *categoryID
	}
	out.FillPaise()
	variants, err := s.listVariants(ctx, itemID)
	if err != nil {
		return nil, err
	}
	groups, err := s.listAddonGroups(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out.Variants, out.AddonGroups = variants, groups
	return &out, nil
}

// ─── Variants ───────────────────────────────────────────────────────────────

func (s *Store) listVariants(ctx context.Context, itemID uuid.UUID) ([]MenuVariant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+variantColumns+`
		FROM food.menu_item_variants
		WHERE menu_item_id = $1
		ORDER BY sort_order, name, id
	`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MenuVariant{}
	for rows.Next() {
		v, err := scanVariant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func (s *Store) ListMenuVariants(ctx context.Context, ownerID, itemID uuid.UUID) ([]MenuVariant, error) {
	if err := requireOwnedMenuItem(ctx, s.db, ownerID, itemID); err != nil {
		return nil, err
	}
	return s.listVariants(ctx, itemID)
}

func (s *Store) CreateMenuVariant(ctx context.Context, ownerID, itemID uuid.UUID, in MenuPriceInput) (*MenuVariant, error) {
	name, err := menuName(in.Name, 120, true)
	if err != nil {
		return nil, err
	}
	price, err := ResolvePricePaise(in.PricePaise, in.Price, true)
	if err != nil {
		return nil, err
	}
	available, sortOrder := true, 0
	if in.IsAvailable != nil {
		available = *in.IsAvailable
	}
	if in.SortOrder != nil {
		sortOrder = *in.SortOrder
	}
	return scanVariant(s.db.QueryRow(ctx, `
		INSERT INTO food.menu_item_variants (menu_item_id, name, price, is_available, sort_order)
		SELECT i.id, $3, $4::bigint / 100.0, $5, $6
		FROM food.menu_items i
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE i.id = $1 AND r.owner_user_id = $2 AND i.is_active
		RETURNING `+variantColumns, itemID, ownerID, *name, *price, available, sortOrder))
}

func (s *Store) UpdateMenuVariant(ctx context.Context, ownerID, itemID, variantID uuid.UUID, in MenuPriceInput) (*MenuVariant, error) {
	name, err := menuName(in.Name, 120, false)
	if err != nil {
		return nil, err
	}
	price, err := ResolvePricePaise(in.PricePaise, in.Price, false)
	if err != nil {
		return nil, err
	}
	return scanVariant(s.db.QueryRow(ctx, `
		UPDATE food.menu_item_variants v
		SET name = COALESCE($4::text, v.name),
			price = COALESCE($5::bigint / 100.0, v.price),
			is_available = COALESCE($6::boolean, v.is_available),
			sort_order = COALESCE($7::int, v.sort_order)
		FROM food.menu_items i
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE v.id = $3 AND v.menu_item_id = $1 AND i.id = v.menu_item_id
			AND r.owner_user_id = $2 AND i.is_active
		RETURNING v.id, v.menu_item_id, v.name, v.price::float8, v.is_available, v.sort_order
	`, itemID, ownerID, variantID, name, price, in.IsAvailable, in.SortOrder))
}

// DeleteMenuVariant removes the variant. Carts and orders that referenced it
// keep their rows (their FKs are ON DELETE SET NULL; orders keep snapshots).
func (s *Store) DeleteMenuVariant(ctx context.Context, ownerID, itemID, variantID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM food.menu_item_variants v
		USING food.menu_items i, food.restaurants r
		WHERE v.id = $3 AND v.menu_item_id = $1 AND i.id = v.menu_item_id
			AND r.id = i.restaurant_id AND r.owner_user_id = $2
	`, itemID, ownerID, variantID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// ─── Add-on groups ──────────────────────────────────────────────────────────

func (s *Store) listAddonGroups(ctx context.Context, itemID uuid.UUID) ([]MenuAddonGroup, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+groupColumns+`
		FROM food.menu_item_addon_groups
		WHERE menu_item_id = $1
		ORDER BY sort_order, name, id
	`, itemID)
	if err != nil {
		return nil, err
	}
	groups := []MenuAddonGroup{}
	index := map[uuid.UUID]int{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		index[g.ID] = len(groups)
		groups = append(groups, *g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return groups, nil
	}
	addonRows, err := s.db.Query(ctx, `
		SELECT a.id, a.addon_group_id, a.name, a.price::float8, a.is_available, a.sort_order
		FROM food.menu_item_addons a
		JOIN food.menu_item_addon_groups g ON g.id = a.addon_group_id
		WHERE g.menu_item_id = $1
		ORDER BY a.sort_order, a.name, a.id
	`, itemID)
	if err != nil {
		return nil, err
	}
	defer addonRows.Close()
	for addonRows.Next() {
		a, err := scanAddon(addonRows)
		if err != nil {
			return nil, err
		}
		if i, ok := index[a.AddonGroupID]; ok {
			groups[i].Addons = append(groups[i].Addons, *a)
		}
	}
	return groups, addonRows.Err()
}

func (s *Store) ListAddonGroups(ctx context.Context, ownerID, itemID uuid.UUID) ([]MenuAddonGroup, error) {
	if err := requireOwnedMenuItem(ctx, s.db, ownerID, itemID); err != nil {
		return nil, err
	}
	return s.listAddonGroups(ctx, itemID)
}

func (s *Store) CreateAddonGroup(ctx context.Context, ownerID, itemID uuid.UUID, in MenuAddonGroupInput) (*MenuAddonGroup, error) {
	name, err := menuName(in.Name, 150, true)
	if err != nil {
		return nil, err
	}
	minSelect, maxSelect, required, sortOrder := 0, 1, false, 0
	if in.MinSelect != nil {
		minSelect = *in.MinSelect
	}
	if in.MaxSelect != nil {
		maxSelect = *in.MaxSelect
	}
	if in.IsRequired != nil {
		required = *in.IsRequired
	}
	if in.SortOrder != nil {
		sortOrder = *in.SortOrder
	}
	if err := ValidateAddonGroupRule(minSelect, maxSelect); err != nil {
		return nil, err
	}
	return scanGroup(s.db.QueryRow(ctx, `
		INSERT INTO food.menu_item_addon_groups (menu_item_id, name, min_select, max_select, is_required, sort_order)
		SELECT i.id, $3, $4, $5, $6, $7
		FROM food.menu_items i
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE i.id = $1 AND r.owner_user_id = $2 AND i.is_active
		RETURNING `+groupColumns, itemID, ownerID, *name, minSelect, maxSelect, required, sortOrder))
}

// lockOwnedGroupTx locks a group of an active item the caller owns.
func lockOwnedGroupTx(ctx context.Context, tx pgx.Tx, ownerID, itemID, groupID uuid.UUID) (*MenuAddonGroup, error) {
	return scanGroup(tx.QueryRow(ctx, `
		SELECT g.id, g.menu_item_id, g.name, g.min_select, g.max_select, g.is_required, g.sort_order
		FROM food.menu_item_addon_groups g
		JOIN food.menu_items i ON i.id = g.menu_item_id
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE g.id = $3 AND g.menu_item_id = $1 AND r.owner_user_id = $2 AND i.is_active
		FOR UPDATE OF g
	`, itemID, ownerID, groupID))
}

func (s *Store) UpdateAddonGroup(ctx context.Context, ownerID, itemID, groupID uuid.UUID, in MenuAddonGroupInput) (*MenuAddonGroup, error) {
	name, err := menuName(in.Name, 150, false)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	g, err := lockOwnedGroupTx(ctx, tx, ownerID, itemID, groupID)
	if err != nil {
		return nil, err
	}
	if name != nil {
		g.Name = *name
	}
	if in.MinSelect != nil {
		g.MinSelect = *in.MinSelect
	}
	if in.MaxSelect != nil {
		g.MaxSelect = *in.MaxSelect
	}
	if in.IsRequired != nil {
		g.IsRequired = *in.IsRequired
	}
	if in.SortOrder != nil {
		g.SortOrder = *in.SortOrder
	}
	if err := ValidateAddonGroupRule(g.MinSelect, g.MaxSelect); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE food.menu_item_addon_groups
		SET name = $2, min_select = $3, max_select = $4, is_required = $5, sort_order = $6
		WHERE id = $1
	`, groupID, g.Name, g.MinSelect, g.MaxSelect, g.IsRequired, g.SortOrder); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	groups, err := s.listAddonGroups(ctx, itemID)
	if err != nil {
		return nil, err
	}
	for i := range groups {
		if groups[i].ID == groupID {
			return &groups[i], nil
		}
	}
	return nil, pgx.ErrNoRows
}

// DeleteAddonGroup removes the group and its add-ons. An add-on still chosen
// in someone's cart is taken out of that cart first (cart_item_addons is ON
// DELETE RESTRICT); placed orders keep their snapshots.
func (s *Store) DeleteAddonGroup(ctx context.Context, ownerID, itemID, groupID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := lockOwnedGroupTx(ctx, tx, ownerID, itemID, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM food.cart_item_addons
		WHERE addon_id IN (SELECT id FROM food.menu_item_addons WHERE addon_group_id = $1)
	`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM food.menu_item_addon_groups WHERE id = $1`, groupID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ─── Add-ons ────────────────────────────────────────────────────────────────

func (s *Store) CreateAddon(ctx context.Context, ownerID, itemID, groupID uuid.UUID, in MenuPriceInput) (*MenuAddon, error) {
	name, err := menuName(in.Name, 150, true)
	if err != nil {
		return nil, err
	}
	price, err := ResolvePricePaise(in.PricePaise, in.Price, true)
	if err != nil {
		return nil, err
	}
	available, sortOrder := true, 0
	if in.IsAvailable != nil {
		available = *in.IsAvailable
	}
	if in.SortOrder != nil {
		sortOrder = *in.SortOrder
	}
	return scanAddon(s.db.QueryRow(ctx, `
		INSERT INTO food.menu_item_addons (addon_group_id, name, price, is_available, sort_order)
		SELECT g.id, $4, $5::bigint / 100.0, $6, $7
		FROM food.menu_item_addon_groups g
		JOIN food.menu_items i ON i.id = g.menu_item_id
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE g.id = $3 AND g.menu_item_id = $1 AND r.owner_user_id = $2 AND i.is_active
		RETURNING `+addonColumns, itemID, ownerID, groupID, *name, *price, available, sortOrder))
}

func (s *Store) UpdateAddon(ctx context.Context, ownerID, itemID, groupID, addonID uuid.UUID, in MenuPriceInput) (*MenuAddon, error) {
	name, err := menuName(in.Name, 150, false)
	if err != nil {
		return nil, err
	}
	price, err := ResolvePricePaise(in.PricePaise, in.Price, false)
	if err != nil {
		return nil, err
	}
	return scanAddon(s.db.QueryRow(ctx, `
		UPDATE food.menu_item_addons a
		SET name = COALESCE($5::text, a.name),
			price = COALESCE($6::bigint / 100.0, a.price),
			is_available = COALESCE($7::boolean, a.is_available),
			sort_order = COALESCE($8::int, a.sort_order)
		FROM food.menu_item_addon_groups g
		JOIN food.menu_items i ON i.id = g.menu_item_id
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE a.id = $4 AND a.addon_group_id = $3 AND g.id = a.addon_group_id AND g.menu_item_id = $1
			AND r.owner_user_id = $2 AND i.is_active
		RETURNING a.id, a.addon_group_id, a.name, a.price::float8, a.is_available, a.sort_order
	`, itemID, ownerID, groupID, addonID, name, price, in.IsAvailable, in.SortOrder))
}

// DeleteAddon removes one add-on, taking it out of any cart first.
func (s *Store) DeleteAddon(ctx context.Context, ownerID, itemID, groupID, addonID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT a.id
		FROM food.menu_item_addons a
		JOIN food.menu_item_addon_groups g ON g.id = a.addon_group_id
		JOIN food.menu_items i ON i.id = g.menu_item_id
		JOIN food.restaurants r ON r.id = i.restaurant_id
		WHERE a.id = $4 AND a.addon_group_id = $3 AND g.menu_item_id = $1 AND r.owner_user_id = $2
		FOR UPDATE OF a
	`, itemID, ownerID, groupID, addonID).Scan(&id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM food.cart_item_addons WHERE addon_id = $1`, addonID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM food.menu_item_addons WHERE id = $1`, addonID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
