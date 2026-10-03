package store

import (
	"context"
	"time"

	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// curOptionPrices is the CTE of option prices current at $2 in city $1.
const curOptionPrices = `
	cur AS (
		SELECT p.id, p.option_id, p.price_paise, p.mrp_paise
		FROM doorstep.city_prices p
		WHERE p.city_code = $1 AND p.option_id IS NOT NULL
		  AND p.effective_from <= $2 AND (p.effective_to IS NULL OR p.effective_to > $2)
	),
	cheapest AS (
		SELECT DISTINCT ON (o.service_id) o.service_id, cur.price_paise, cur.mrp_paise
		FROM doorstep.service_options o
		JOIN cur ON cur.option_id = o.id
		WHERE o.active
		ORDER BY o.service_id, cur.price_paise, o.sort_order, o.id
	),
	svc AS (
		SELECT s.id, s.category_id, ch.price_paise, ch.mrp_paise
		FROM doorstep.services s
		JOIN cheapest ch ON ch.service_id = s.id
		WHERE s.active
	)`

// City returns an active city and its GST state code.
func (s *Store) City(ctx context.Context, code string) (model.CityRef, string, error) {
	var c model.CityRef
	var state string
	err := s.db.QueryRow(ctx, `SELECT code, name, state_code FROM doorstep.cities WHERE code = $1 AND active`, code).
		Scan(&c.Code, &c.Name, &state)
	return c, state, mapErr(err)
}

func scanCategorySummary(r pgx.Rows) (model.CategorySummary, error) {
	var c model.CategorySummary
	err := r.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Family, &c.GenderRule, &c.ImageURL, &c.SortOrder,
		&c.ServiceCount, &c.StartingPricePaise)
	return c, err
}

const categorySummarySelect = `
	SELECT c.id, c.slug, c.name, c.description, c.family, c.gender_rule, c.image_url, c.sort_order,
	       COUNT(svc.id)::int, COALESCE(MIN(svc.price_paise), 0)::bigint
	FROM doorstep.categories c`

// CategorySummaries lists active categories with at least one active, priced
// service in the city at `at`.
func (s *Store) CategorySummaries(ctx context.Context, city string, at time.Time) ([]model.CategorySummary, error) {
	rows, err := s.db.Query(ctx, `WITH `+curOptionPrices+categorySummarySelect+`
		JOIN svc ON svc.category_id = c.id
		WHERE c.active
		GROUP BY c.id
		ORDER BY c.sort_order, c.name, c.id`, city, at)
	if err != nil {
		return nil, err
	}
	return collect(rows, scanCategorySummary)
}

// CategoryBySlug returns one active category (service_count may be 0).
func (s *Store) CategoryBySlug(ctx context.Context, city, slug string, at time.Time) (*model.CategorySummary, error) {
	rows, err := s.db.Query(ctx, `WITH `+curOptionPrices+categorySummarySelect+`
		LEFT JOIN svc ON svc.category_id = c.id
		WHERE c.active AND c.slug = $3
		GROUP BY c.id`, city, at, slug)
	if err != nil {
		return nil, err
	}
	list, err := collect(rows, scanCategorySummary)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// ServiceSummaries lists a category's active, priced services in the city.
func (s *Store) ServiceSummaries(ctx context.Context, city string, categoryID uuid.UUID, at time.Time) ([]model.ServiceSummary, error) {
	rows, err := s.db.Query(ctx, `WITH `+curOptionPrices+`
		SELECT s.id, s.category_id, s.slug, s.name, s.description, s.duration_minutes, s.image_url,
		       svc.price_paise, svc.mrp_paise
		FROM doorstep.services s
		JOIN svc ON svc.id = s.id
		WHERE s.category_id = $3
		ORDER BY s.sort_order, s.name, s.id`, city, at, categoryID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.ServiceSummary, error) {
		var v model.ServiceSummary
		err := r.Scan(&v.ID, &v.CategoryID, &v.Slug, &v.Name, &v.Description, &v.DurationMinutes, &v.ImageURL,
			&v.StartingPricePaise, &v.StartingMRPPaise)
		return v, err
	})
}

// ServiceBundle loads one service with its category, options and add-on
// groups, priced for the city at `at`. ErrNotFound when the service or an
// active city does not exist; visibility is the caller's decision.
func (s *Store) ServiceBundle(ctx context.Context, city string, serviceID uuid.UUID, at time.Time) (*catalogue.ServiceBundle, error) {
	b := &catalogue.ServiceBundle{}
	err := s.db.QueryRow(ctx, `
		SELECT ci.code, ci.name,
		       c.id, c.slug, c.name, c.family, c.gender_rule, c.extras_policy, c.active,
		       s.id, s.slug, s.name, s.description, s.duration_minutes, s.inclusions, s.exclusions, s.image_url,
		       s.crew_size, s.rework_days, s.min_before_photos, s.min_after_photos, s.active
		FROM doorstep.services s
		JOIN doorstep.categories c ON c.id = s.category_id
		JOIN doorstep.cities ci ON ci.code = $1 AND ci.active
		WHERE s.id = $2`, city, serviceID).Scan(
		&b.City.Code, &b.City.Name,
		&b.Category.ID, &b.Category.Slug, &b.Category.Name, &b.Category.Family, &b.Category.GenderRule, &b.Category.ExtrasPolicy, &b.Category.Active,
		&b.Service.ID, &b.Service.Slug, &b.Service.Name, &b.Service.Description, &b.Service.DurationMinutes,
		&b.Service.Inclusions, &b.Service.Exclusions, &b.Service.ImageURL,
		&b.Service.CrewSize, &b.Service.ReworkDays, &b.Service.MinBeforePhotos, &b.Service.MinAfterPhotos, &b.Service.Active)
	if err != nil {
		return nil, mapErr(err)
	}

	rows, err := s.db.Query(ctx, `
		SELECT o.id, o.name, o.description, o.duration_minutes, o.max_quantity, o.is_default, o.active,
		       p.id, p.price_paise, p.mrp_paise
		FROM doorstep.service_options o
		LEFT JOIN doorstep.city_prices p
		       ON p.option_id = o.id AND p.city_code = $1
		      AND p.effective_from <= $3 AND (p.effective_to IS NULL OR p.effective_to > $3)
		WHERE o.service_id = $2
		ORDER BY o.sort_order, o.name, o.id`, city, serviceID, at)
	if err != nil {
		return nil, err
	}
	b.Options, err = collect(rows, func(r pgx.Rows) (catalogue.OptionRow, error) {
		var o catalogue.OptionRow
		var pid *uuid.UUID
		var price *int64
		var mrp *int64
		if err := r.Scan(&o.ID, &o.Name, &o.Description, &o.DurationMinutes, &o.MaxQuantity, &o.IsDefault, &o.Active, &pid, &price, &mrp); err != nil {
			return o, err
		}
		if pid != nil && price != nil {
			o.Price = &catalogue.Price{ID: *pid, PricePaise: *price, MRPPaise: mrp}
		}
		return o, nil
	})
	if err != nil {
		return nil, err
	}

	rows, err = s.db.Query(ctx, `
		SELECT id, name, min_select, max_select, is_required, active
		FROM doorstep.addon_groups WHERE service_id = $1
		ORDER BY sort_order, name, id`, serviceID)
	if err != nil {
		return nil, err
	}
	b.Groups, err = collect(rows, func(r pgx.Rows) (catalogue.GroupRow, error) {
		var g catalogue.GroupRow
		err := r.Scan(&g.ID, &g.Name, &g.MinSelect, &g.MaxSelect, &g.IsRequired, &g.Active)
		return g, err
	})
	if err != nil {
		return nil, err
	}
	byGroup := map[uuid.UUID]int{}
	for i, g := range b.Groups {
		byGroup[g.ID] = i
	}
	rows, err = s.db.Query(ctx, `
		SELECT a.group_id, a.id, a.name, a.description, a.extra_duration_minutes, a.active,
		       p.id, p.price_paise, p.mrp_paise
		FROM doorstep.addons a
		JOIN doorstep.addon_groups g ON g.id = a.group_id
		LEFT JOIN doorstep.city_prices p
		       ON p.addon_id = a.id AND p.city_code = $1
		      AND p.effective_from <= $3 AND (p.effective_to IS NULL OR p.effective_to > $3)
		WHERE g.service_id = $2
		ORDER BY a.sort_order, a.name, a.id`, city, serviceID, at)
	if err != nil {
		return nil, err
	}
	type addonWithGroup struct {
		group uuid.UUID
		row   catalogue.AddonRow
	}
	addons, err := collect(rows, func(r pgx.Rows) (addonWithGroup, error) {
		var v addonWithGroup
		var pid *uuid.UUID
		var price, mrp *int64
		if err := r.Scan(&v.group, &v.row.ID, &v.row.Name, &v.row.Description, &v.row.ExtraDurationMinutes, &v.row.Active, &pid, &price, &mrp); err != nil {
			return v, err
		}
		if pid != nil && price != nil {
			v.row.Price = &catalogue.Price{ID: *pid, PricePaise: *price, MRPPaise: mrp}
		}
		return v, nil
	})
	if err != nil {
		return nil, err
	}
	for _, a := range addons {
		if i, ok := byGroup[a.group]; ok {
			b.Groups[i].Addons = append(b.Groups[i].Addons, a.row)
		}
	}
	return b, nil
}

// LocateZone returns the active zone of an active city covering the point
// (ST_Covers: a point on the boundary is inside), lowest name then id first;
// nil when the point is outside every zone.
func (s *Store) LocateZone(ctx context.Context, lat, lng float64) (*catalogue.ZoneHit, error) {
	var h catalogue.ZoneHit
	err := s.db.QueryRow(ctx, `
		SELECT z.id, z.name, c.code, c.name, c.state_code
		FROM doorstep.zones z
		JOIN doorstep.cities c ON c.code = z.city_code
		WHERE z.active AND c.active
		  AND ST_Covers(z.boundary, ST_SetSRID(ST_MakePoint($2, $1), 4326)::geography)
		ORDER BY z.name, z.id
		LIMIT 1`, lat, lng).Scan(&h.Zone.ID, &h.Zone.Name, &h.City.Code, &h.City.Name, &h.StateCode)
	if err != nil {
		if mapped := mapErr(err); mapped == ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &h, nil
}

// InsertQuote stores a priced quote and its lines.
func (s *Store) InsertQuote(ctx context.Context, q *model.Quote, customer uuid.UUID, lat, lng float64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO doorstep.quotes (id, customer_user_id, service_id, option_id, quantity, city_code, zone_id, location,
		                             total_paise, taxable_paise, tax_paise, tax_provisional, tax_note, duration_minutes,
		                             status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, ST_SetSRID(ST_MakePoint($9, $8), 4326)::geography,
		        $10, $11, $12, $13, $14, $15, 'open', $16, $17)`,
		q.ID, customer, q.ServiceID, q.OptionID, q.Quantity, q.CityCode, q.ZoneID, lat, lng,
		q.TotalPaise, q.TaxablePaise, q.TaxPaise, q.TaxProvisional, q.TaxNote, q.DurationMinutes, q.ExpiresAt, q.CreatedAt); err != nil {
		return mapErr(err)
	}
	for i, l := range q.Lines {
		if _, err := tx.Exec(ctx, `
			INSERT INTO doorstep.quote_items (quote_id, line_no, kind, ref_id, price_id, name, quantity, unit_price_paise,
			                                  line_total_paise, taxable_paise, tax_paise, tax_rate_bps, gst_category, sac)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			q.ID, i+1, l.Kind, l.RefID, l.PriceID, l.Name, l.Quantity, l.UnitPricePaise, l.LineTotalPaise,
			l.TaxablePaise, l.TaxPaise, l.TaxRateBPS, l.GSTCategory, l.SAC); err != nil {
			return mapErr(err)
		}
	}
	return mapErr(tx.Commit(ctx))
}

// Quote returns one of customer's quotes (ErrNotFound for anyone else's).
// Status is the stored one ("open" or "consumed"); expiry is the caller's.
func (s *Store) Quote(ctx context.Context, id, customer uuid.UUID) (*model.Quote, error) {
	q := &model.Quote{PricesIncludeTax: true}
	err := s.db.QueryRow(ctx, `
		SELECT id, status, service_id, option_id, quantity, city_code, zone_id, total_paise, taxable_paise, tax_paise,
		       tax_provisional, tax_note, duration_minutes, expires_at, created_at
		FROM doorstep.quotes WHERE id = $1 AND customer_user_id = $2`, id, customer).Scan(
		&q.ID, &q.Status, &q.ServiceID, &q.OptionID, &q.Quantity, &q.CityCode, &q.ZoneID, &q.TotalPaise, &q.TaxablePaise,
		&q.TaxPaise, &q.TaxProvisional, &q.TaxNote, &q.DurationMinutes, &q.ExpiresAt, &q.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	q.ExpiresAt, q.CreatedAt = q.ExpiresAt.UTC(), q.CreatedAt.UTC()
	rows, err := s.db.Query(ctx, `
		SELECT kind, ref_id, price_id, name, quantity, unit_price_paise, line_total_paise, taxable_paise, tax_paise,
		       tax_rate_bps, gst_category, sac
		FROM doorstep.quote_items WHERE quote_id = $1 ORDER BY line_no`, id)
	if err != nil {
		return nil, err
	}
	q.Lines, err = collect(rows, func(r pgx.Rows) (model.QuoteLine, error) {
		var l model.QuoteLine
		err := r.Scan(&l.Kind, &l.RefID, &l.PriceID, &l.Name, &l.Quantity, &l.UnitPricePaise, &l.LineTotalPaise,
			&l.TaxablePaise, &l.TaxPaise, &l.TaxRateBPS, &l.GSTCategory, &l.SAC)
		return l, err
	})
	if err != nil {
		return nil, err
	}
	return q, nil
}
