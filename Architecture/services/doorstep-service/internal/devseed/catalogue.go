package devseed

import (
	"context"
	"fmt"
	"github.com/atpost/doorstep-service/internal/runtimeenv"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunCatalogue repairs the development menu only. It adds no prices, coverage,
// partners, bookings or policy settings, and never overwrites admin edits.
func RunCatalogue(ctx context.Context, db *pgxpool.Pool, getenv func(string) string) error {
	if !runtimeenv.IsDevelopment(getenv) {
		return ErrNotDevelopment
	}
	return SeedCatalogue(ctx, db)
}
func SeedCatalogue(ctx context.Context, db *pgxpool.Pool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ex := func(sql string, args ...any) {
		if err == nil {
			_, err = tx.Exec(ctx, sql, args...)
			if err != nil {
				err = fmt.Errorf("catalogue: %w", err)
			}
		}
	}
	for _, k := range skills {
		ex(`INSERT INTO doorstep.skills(code,name,requires_certificate) VALUES($1,$2,false) ON CONFLICT DO NOTHING`, k[0], k[1])
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
				unit := optionUnits[okey]
				if unit == "" {
					unit = "per_job"
				}
				ex(`INSERT INTO doorstep.service_options (id, service_id, name, duration_minutes, max_quantity, is_default, sort_order, active, unit)
					VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, $8) ON CONFLICT DO NOTHING`,
					optID, svcID, o.name, o.duration, o.maxQty, o.isDefault, (oi+1)*10, unit)
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
				}
			}
		}
	}

	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
