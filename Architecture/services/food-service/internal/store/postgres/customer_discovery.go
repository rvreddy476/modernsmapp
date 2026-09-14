package postgres

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Feast customer gaps: near-me serviceability on the restaurant list and
// detail, the schedule's open-now, and the customer menu's variants and
// add-ons. Every response field is additive (models.go). The serviceability
// decision itself is evaluateServiceability (serviceability.go), the one
// PlaceOrder applies.

// addressPointTx reads the caller's address pin from the nullable columns
// (getAddressTx COALESCEs a missing pin to 0, which would place the customer in
// the Gulf of Guinea). Another user's address, or a deleted one, is
// pgx.ErrNoRows.
func addressPointTx(ctx context.Context, tx pgx.Tx, userID, addressID uuid.UUID) (*deliveryPoint, error) {
	p := &deliveryPoint{}
	if err := tx.QueryRow(ctx, `
		SELECT latitude::float8, longitude::float8
		FROM food.customer_addresses
		WHERE id = $1 AND user_id = $2 AND is_deleted = FALSE
	`, addressID, userID).Scan(&p.Lat, &p.Lng); err != nil {
		return nil, err
	}
	return p, nil
}

// fillServiceability sets the additive serviceability fields on each
// restaurant from one batch read of their facts.
func (s *Store) fillServiceability(ctx context.Context, restaurants []*RestaurantSummary, near *GeoPoint) error {
	if len(restaurants) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(restaurants))
	for i, r := range restaurants {
		ids[i] = r.ID
	}
	facts, err := loadServiceabilityFacts(ctx, s.db, ids)
	if err != nil {
		return err
	}
	now := s.ordering.Now()
	for _, r := range restaurants {
		if f, ok := facts[r.ID]; ok {
			s.ordering.describeServiceability(r, now, *f, near)
		}
	}
	return nil
}

// describeServiceability sets is_open_now / next_opens_at always, and
// serviceable, distance_meters and the refusal only when the request named a
// point. The refusal is exactly what PlaceOrder returns for an address there.
func (c OrderingConfig) describeServiceability(r *RestaurantSummary, now time.Time, f serviceabilityFacts, near *GeoPoint) {
	local := now.In(c.Location)
	r.IsOpenNow = openAt(local, f.Windows)
	if next, ok := nextOpenAt(local, f.Windows); ok {
		v := next.Format(time.RFC3339)
		r.NextOpensAt = &v
	}
	if near == nil {
		return
	}
	distanceKM, err := c.evaluateServiceability(now, f, pointOf(near))
	serviceable := err == nil
	r.Serviceable = &serviceable
	r.Unserviceable = err
	if distanceKM != nil {
		m := int64(math.Round(*distanceKM * 1000))
		r.DistanceMeters = &m
	}
}

// rankByServiceability orders a near-me list: serviceable first, then
// nearest; a restaurant with no distance (no map pin) goes last in its group.
// Ties keep the incoming order.
func rankByServiceability(restaurants []RestaurantSummary) {
	sort.SliceStable(restaurants, func(i, j int) bool {
		a, b := restaurants[i], restaurants[j]
		sa, sb := a.Serviceable != nil && *a.Serviceable, b.Serviceable != nil && *b.Serviceable
		if sa != sb {
			return sa
		}
		switch {
		case a.DistanceMeters == nil:
			return false
		case b.DistanceMeters == nil:
			return true
		}
		return *a.DistanceMeters < *b.DistanceMeters
	})
}

// attachCustomerMenuExtras gives every customer menu item its available
// variants and its add-on groups with their available add-ons, in the partner
// item read's shape (MenuVariant, MenuAddonGroup). price_paise is read from
// the NUMERIC column in SQL, never derived from the float.
func (s *Store) attachCustomerMenuExtras(ctx context.Context, categories []MenuCategory) error {
	var ids []uuid.UUID
	for _, c := range categories {
		for _, item := range c.Items {
			ids = append(ids, item.ID)
		}
	}
	variants := map[uuid.UUID][]MenuVariant{}
	groups := map[uuid.UUID][]MenuAddonGroup{}
	if len(ids) > 0 {
		rows, err := s.db.Query(ctx, `
			SELECT id, menu_item_id, name, price::float8, ROUND(price * 100)::bigint, is_available, sort_order
			FROM food.menu_item_variants
			WHERE menu_item_id = ANY($1::uuid[]) AND is_available = TRUE
			ORDER BY sort_order, name, id
		`, ids)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v MenuVariant
			if err := rows.Scan(&v.ID, &v.MenuItemID, &v.Name, &v.Price, &v.PricePaise, &v.IsAvailable, &v.SortOrder); err != nil {
				rows.Close()
				return err
			}
			variants[v.MenuItemID] = append(variants[v.MenuItemID], v)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		rows, err = s.db.Query(ctx, `
			SELECT `+groupColumns+`
			FROM food.menu_item_addon_groups
			WHERE menu_item_id = ANY($1::uuid[])
			ORDER BY sort_order, name, id
		`, ids)
		if err != nil {
			return err
		}
		type slot struct {
			item uuid.UUID
			i    int
		}
		index := map[uuid.UUID]slot{}
		for rows.Next() {
			g, err := scanGroup(rows)
			if err != nil {
				rows.Close()
				return err
			}
			index[g.ID] = slot{item: g.MenuItemID, i: len(groups[g.MenuItemID])}
			groups[g.MenuItemID] = append(groups[g.MenuItemID], *g)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		if len(index) > 0 {
			rows, err = s.db.Query(ctx, `
				SELECT a.id, a.addon_group_id, a.name, a.price::float8, ROUND(a.price * 100)::bigint, a.is_available, a.sort_order
				FROM food.menu_item_addons a
				JOIN food.menu_item_addon_groups g ON g.id = a.addon_group_id
				WHERE g.menu_item_id = ANY($1::uuid[]) AND a.is_available = TRUE
				ORDER BY a.sort_order, a.name, a.id
			`, ids)
			if err != nil {
				return err
			}
			for rows.Next() {
				var a MenuAddon
				if err := rows.Scan(&a.ID, &a.AddonGroupID, &a.Name, &a.Price, &a.PricePaise, &a.IsAvailable, &a.SortOrder); err != nil {
					rows.Close()
					return err
				}
				if at, ok := index[a.AddonGroupID]; ok {
					groups[at.item][at.i].Addons = append(groups[at.item][at.i].Addons, a)
				}
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
	}
	for ci := range categories {
		for ii := range categories[ci].Items {
			item := &categories[ci].Items[ii]
			v, g := variants[item.ID], groups[item.ID]
			if v == nil {
				v = []MenuVariant{}
			}
			if g == nil {
				g = []MenuAddonGroup{}
			}
			item.Variants, item.AddonGroups = &v, &g
		}
	}
	return nil
}
