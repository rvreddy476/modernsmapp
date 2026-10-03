// Package devseed seeds the Hyderabad pilot catalogue on a development
// database: city HYD, two zones, every launch category with its services,
// options, add-ons, prices (paise, GST-inclusive), rate cards, slot config,
// placeholder cancellation rules and commission.
//
// Ids are deterministic (ID), every insert is ON CONFLICT DO NOTHING, so the
// seed is idempotent and never overwrites a row an admin has changed. Run
// refuses anything but an explicit local/dev/development environment.
package devseed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/doorstep-service/internal/runtimeenv"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CityCode is the pilot city.
const CityCode = "HYD"

// PricesFrom is when every seeded price starts (1 Oct 2026 00:00 IST).
var PricesFrom = time.Date(2026, 9, 30, 18, 30, 0, 0, time.UTC)

var namespace = uuid.MustParse("5d0a5c1e-7a43-4c39-9a51-2f0c7b8e4d10")

// ID is the deterministic id of a seeded row, e.g. ID("service", "salon-women/facial").
func ID(kind, key string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte("doorstep:"+kind+":"+key))
}

// ErrNotDevelopment is returned by Run outside local/dev/development.
var ErrNotDevelopment = errors.New("devseed: the Hyderabad seed only runs when ENV is local, dev or development")

// Run seeds after checking the environment.
func Run(ctx context.Context, db *pgxpool.Pool, getenv func(string) string) error {
	if !runtimeenv.IsDevelopment(getenv) {
		return ErrNotDevelopment
	}
	return Seed(ctx, db)
}

// Seed writes the catalogue in one transaction. Callers other than Run must
// guarantee a development or test database.
func Seed(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	ex := func(sql string, args ...any) {
		if err != nil {
			return
		}
		if _, e := tx.Exec(ctx, sql, args...); e != nil {
			err = fmt.Errorf("devseed: %w", e)
		}
	}

	ex(`INSERT INTO doorstep.cities (code, name, state_code, active) VALUES ($1, 'Hyderabad', '36', TRUE)
		ON CONFLICT DO NOTHING`, CityCode)
	for _, z := range zones {
		ex(`INSERT INTO doorstep.zones (id, city_code, name, slug, boundary, travel_buffer_minutes, active)
			VALUES ($1, $2, $3, $4, ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON($5::text), 4326))::geography, 30, TRUE)
			ON CONFLICT DO NOTHING`, ID("zone", z.slug), CityCode, z.name, z.slug, z.polygon)
	}
	for _, k := range skills {
		ex(`INSERT INTO doorstep.skills (code, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, k[0], k[1])
	}
	price := func(kind string, item uuid.UUID, key string, paise, mrp int64) {
		var mrpArg *int64
		if mrp > 0 {
			mrpArg = &mrp
		}
		col := "option_id"
		if kind == "addon" {
			col = "addon_id"
		}
		ex(`INSERT INTO doorstep.city_prices (id, city_code, item_kind, `+col+`, price_paise, mrp_paise, effective_from)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
			ID("price", key), CityCode, kind, item, paise, mrpArg, PricesFrom)
	}
	for ci, c := range categories {
		catID := ID("category", c.slug)
		ex(`INSERT INTO doorstep.categories (id, slug, name, description, family, gender_rule, extras_policy, sort_order, active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE) ON CONFLICT DO NOTHING`,
			catID, c.slug, c.name, c.description, c.family, c.genderRule, c.extrasPolicy, (ci+1)*10)
		for si, s := range c.services {
			skey := c.slug + "/" + s.slug
			svcID := ID("service", skey)
			inc, exc := s.inclusions, s.exclusions
			if inc == nil {
				inc = []string{}
			}
			if exc == nil {
				exc = []string{}
			}
			ex(`INSERT INTO doorstep.services (id, category_id, slug, name, description, duration_minutes, required_skill,
				    inclusions, exclusions, crew_size, min_before_photos, min_after_photos, rework_days, sort_order, active)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, 2, 2, $10, $11, TRUE) ON CONFLICT DO NOTHING`,
				svcID, catID, s.slug, s.name, s.description, s.duration, s.skill, inc, exc, s.reworkDays, (si+1)*10)
			for oi, o := range s.options {
				okey := skey + "/" + o.key
				optID := ID("option", okey)
				ex(`INSERT INTO doorstep.service_options (id, service_id, name, duration_minutes, max_quantity, is_default, sort_order, active)
					VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE) ON CONFLICT DO NOTHING`,
					optID, svcID, o.name, o.duration, o.maxQty, o.isDefault, (oi+1)*10)
				price("option", optID, okey, o.price, o.mrp)
			}
			for gi, g := range s.groups {
				gkey := skey + "/" + g.key
				grpID := ID("addon_group", gkey)
				ex(`INSERT INTO doorstep.addon_groups (id, service_id, name, min_select, max_select, is_required, sort_order, active)
					VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE) ON CONFLICT DO NOTHING`,
					grpID, svcID, g.name, g.min, g.max, g.required, (gi+1)*10)
				for ai, a := range g.addons {
					akey := gkey + "/" + a.key
					addID := ID("addon", akey)
					ex(`INSERT INTO doorstep.addons (id, group_id, name, extra_duration_minutes, sort_order, active)
						VALUES ($1, $2, $3, $4, $5, TRUE) ON CONFLICT DO NOTHING`, addID, grpID, a.name, a.extra, (ai+1)*10)
					price("addon", addID, akey, a.price, 0)
				}
			}
		}
		for ri, r := range c.rates {
			ex(`INSERT INTO doorstep.rate_cards (id, city_code, category_id, code, name, unit, price_paise, max_quantity, is_part, sort_order, active)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, TRUE) ON CONFLICT DO NOTHING`,
				ID("rate_card", c.slug+"/"+r.code), CityCode, catID, r.code, r.name, r.unit, r.price, r.maxQty, r.isPart, (ri+1)*10)
		}
	}
	ex(`INSERT INTO doorstep.slot_configs (id, city_code, open_time, close_time, slot_step_minutes, min_lead_minutes, horizon_days, hold_minutes, active)
		VALUES ($1, $2, '08:00', '20:00', 30, 120, 7, 10, TRUE) ON CONFLICT DO NOTHING`, ID("slot_config", CityCode), CityCode)
	ex(`INSERT INTO doorstep.slot_configs (id, city_code, category_id, open_time, close_time, slot_step_minutes, min_lead_minutes, horizon_days, hold_minutes, active)
		VALUES ($1, $2, $3, '09:00', '19:00', 30, 120, 7, 10, TRUE) ON CONFLICT DO NOTHING`,
		ID("slot_config", CityCode+"/salon-women"), CityCode, ID("category", "salon-women"))
	for _, r := range cancellationRules {
		var lt *int
		if r.ltMinutes > 0 {
			v := r.ltMinutes
			lt = &v
		}
		ex(`INSERT INTO doorstep.cancellation_rules (id, city_code, stage, minutes_before_lt, fee_paise, allowed, sort_order, active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE) ON CONFLICT DO NOTHING`,
			ID("cancellation_rule", CityCode+"/"+r.key), CityCode, r.stage, lt, r.fee, r.allowed, r.sort)
	}
	ex(`INSERT INTO doorstep.commission_rules (id, city_code, commission_bps, effective_from, active)
		VALUES ($1, $2, 2000, $3, TRUE) ON CONFLICT DO NOTHING`, ID("commission_rule", CityCode), CityCode, PricesFrom)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Counts reports what the seed defines (for logs and tests).
func Counts() (cats, services, options, addons, rates int) {
	for _, c := range categories {
		cats++
		rates += len(c.rates)
		for _, s := range c.services {
			services++
			options += len(s.options)
			for _, g := range s.groups {
				addons += len(g.addons)
			}
		}
	}
	return
}
