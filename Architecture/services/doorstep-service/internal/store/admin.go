package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Every admin write runs in adminWrite: the row and its audit entry commit
// together. Parent rows named in the path are checked first so a missing
// parent is ErrNotFound (404), not a foreign-key error.

func requireRow(ctx context.Context, tx pgx.Tx, query string, args ...any) error {
	var one int
	if err := tx.QueryRow(ctx, query, args...).Scan(&one); err != nil {
		return mapErr(err)
	}
	return nil
}

// ---------------------------------------------------------------- cities

const cityCols = `code, name, state_code, timezone, active, extras_charge_now_threshold_paise, extras_grace_minutes,
	max_jobs_per_day, offer_window_far_minutes, offer_window_near_minutes, offer_far_threshold_minutes, created_at, updated_at`

func scanCity(r pgx.Row) (model.AdminCity, error) {
	var c model.AdminCity
	err := r.Scan(&c.Code, &c.Name, &c.StateCode, &c.Timezone, &c.Active, &c.ExtrasChargeNowThresholdPaise, &c.ExtrasGraceMinutes,
		&c.MaxJobsPerDay, &c.OfferWindowFarMinutes, &c.OfferWindowNearMinutes, &c.OfferFarThresholdMinutes, &c.CreatedAt, &c.UpdatedAt)
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, err
}

func rowsOf[T any](scan func(pgx.Row) (T, error)) func(pgx.Rows) (T, error) {
	return func(r pgx.Rows) (T, error) { return scan(r) }
}

// ListCities lists every city.
func (s *Store) ListCities(ctx context.Context) ([]model.AdminCity, error) {
	rows, err := s.db.Query(ctx, `SELECT `+cityCols+` FROM doorstep.cities ORDER BY code`)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanCity))
}

// CreateCity inserts a city.
func (s *Store) CreateCity(ctx context.Context, a Actor, in model.AdminCityInput) (*model.AdminCity, error) {
	var out model.AdminCity
	err := s.adminWrite(ctx, a, "city.create", "city", in, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanCity(tx.QueryRow(ctx, `
			INSERT INTO doorstep.cities (code, name, state_code, timezone, active, extras_charge_now_threshold_paise,
			    extras_grace_minutes, max_jobs_per_day, offer_window_far_minutes, offer_window_near_minutes, offer_far_threshold_minutes)
			VALUES ($1, $2, $3, COALESCE($4::text, 'Asia/Kolkata'), COALESCE($5::bool, FALSE), COALESCE($6::bigint, 300000),
			    COALESCE($7::int, 15), COALESCE($8::int, 6), COALESCE($9::int, 120), COALESCE($10::int, 10), COALESCE($11::int, 720))
			RETURNING `+cityCols,
			in.Code, in.Name, in.StateCode, in.Timezone, in.Active, in.ExtrasChargeNowThresholdPaise, in.ExtrasGraceMinutes,
			in.MaxJobsPerDay, in.OfferWindowFarMinutes, in.OfferWindowNearMinutes, in.OfferFarThresholdMinutes))
		return in.Code, err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateCity patches a city.
func (s *Store) UpdateCity(ctx context.Context, a Actor, code string, p model.AdminCityPatch) (*model.AdminCity, error) {
	var out model.AdminCity
	err := s.adminWrite(ctx, a, "city.update", "city", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanCity(tx.QueryRow(ctx, `
			UPDATE doorstep.cities SET
			    name = COALESCE($2::text, name), state_code = COALESCE($3::text, state_code),
			    timezone = COALESCE($4::text, timezone), active = COALESCE($5::bool, active),
			    extras_charge_now_threshold_paise = COALESCE($6::bigint, extras_charge_now_threshold_paise),
			    extras_grace_minutes = COALESCE($7::int, extras_grace_minutes),
			    max_jobs_per_day = COALESCE($8::int, max_jobs_per_day),
			    offer_window_far_minutes = COALESCE($9::int, offer_window_far_minutes),
			    offer_window_near_minutes = COALESCE($10::int, offer_window_near_minutes),
			    offer_far_threshold_minutes = COALESCE($11::int, offer_far_threshold_minutes),
			    updated_at = NOW()
			WHERE code = $1 RETURNING `+cityCols,
			code, p.Name, p.StateCode, p.Timezone, p.Active, p.ExtrasChargeNowThresholdPaise, p.ExtrasGraceMinutes,
			p.MaxJobsPerDay, p.OfferWindowFarMinutes, p.OfferWindowNearMinutes, p.OfferFarThresholdMinutes))
		return code, err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- zones

const zoneCols = `id, city_code, name, slug, active, travel_buffer_minutes, ST_AsGeoJSON(boundary, 6)::text, created_at, updated_at`

func scanZone(r pgx.Row) (model.AdminZone, error) {
	var z model.AdminZone
	var boundary string
	err := r.Scan(&z.ID, &z.CityCode, &z.Name, &z.Slug, &z.Active, &z.TravelBufferMinutes, &boundary, &z.CreatedAt, &z.UpdatedAt)
	z.Boundary = json.RawMessage(boundary)
	z.CreatedAt, z.UpdatedAt = z.CreatedAt.UTC(), z.UpdatedAt.UTC()
	return z, err
}

// checkBoundary asks PostGIS whether the GeoJSON is a valid (multi)polygon.
func checkBoundary(ctx context.Context, tx pgx.Tx, geojson string) error {
	var valid bool
	var kind string
	err := tx.QueryRow(ctx, `
		SELECT ST_IsValid(g), GeometryType(g)
		FROM (SELECT ST_SetSRID(ST_GeomFromGeoJSON($1::text), 4326) AS g) x`, geojson).Scan(&valid, &kind)
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			return fmt.Errorf("%w: %s", ErrZoneInvalid, pg.Message)
		}
		return err
	}
	if !valid || (kind != "POLYGON" && kind != "MULTIPOLYGON") {
		return fmt.Errorf("%w: not a valid polygon", ErrZoneInvalid)
	}
	return nil
}

// ListZones lists zones, optionally of one city.
func (s *Store) ListZones(ctx context.Context, city string) ([]model.AdminZone, error) {
	rows, err := s.db.Query(ctx, `SELECT `+zoneCols+` FROM doorstep.zones
		WHERE ($1::text = '' OR city_code = $1) ORDER BY city_code, name, id`, city)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanZone))
}

// CreateZone inserts a zone from a GeoJSON Polygon/MultiPolygon.
func (s *Store) CreateZone(ctx context.Context, a Actor, in model.AdminZoneInput) (*model.AdminZone, error) {
	var out model.AdminZone
	err := s.adminWrite(ctx, a, "zone.create", "zone", in, func(tx pgx.Tx) (string, error) {
		if err := checkBoundary(ctx, tx, string(in.Boundary)); err != nil {
			return "", err
		}
		var err error
		out, err = scanZone(tx.QueryRow(ctx, `
			INSERT INTO doorstep.zones (city_code, name, slug, boundary, travel_buffer_minutes, active)
			VALUES ($1, $2, $3, ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON($4::text), 4326))::geography,
			        COALESCE($5::int, 30), COALESCE($6::bool, TRUE))
			RETURNING `+zoneCols, in.CityCode, in.Name, in.Slug, string(in.Boundary), in.TravelBufferMinutes, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateZone patches a zone.
func (s *Store) UpdateZone(ctx context.Context, a Actor, id uuid.UUID, p model.AdminZonePatch) (*model.AdminZone, error) {
	var out model.AdminZone
	var boundary *string
	if len(p.Boundary) > 0 && string(p.Boundary) != "null" {
		b := string(p.Boundary)
		boundary = &b
	}
	err := s.adminWrite(ctx, a, "zone.update", "zone", p, func(tx pgx.Tx) (string, error) {
		if boundary != nil {
			if err := checkBoundary(ctx, tx, *boundary); err != nil {
				return "", err
			}
		}
		var err error
		out, err = scanZone(tx.QueryRow(ctx, `
			UPDATE doorstep.zones SET
			    name = COALESCE($2::text, name), active = COALESCE($3::bool, active),
			    travel_buffer_minutes = COALESCE($4::int, travel_buffer_minutes),
			    boundary = CASE WHEN $5::text IS NULL THEN boundary
			                    ELSE ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON($5::text), 4326))::geography END,
			    updated_at = NOW()
			WHERE id = $1 RETURNING `+zoneCols, id, p.Name, p.Active, p.TravelBufferMinutes, boundary))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- categories, skills

const categoryCols = `id, slug, name, description, family, gender_rule, extras_policy, image_url, sort_order, active, created_at, updated_at`

func scanCategory(r pgx.Row) (model.AdminCategory, error) {
	var c model.AdminCategory
	err := r.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Family, &c.GenderRule, &c.ExtrasPolicy, &c.ImageURL,
		&c.SortOrder, &c.Active, &c.CreatedAt, &c.UpdatedAt)
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	return c, err
}

// ListCategories lists every category.
func (s *Store) ListCategories(ctx context.Context) ([]model.AdminCategory, error) {
	rows, err := s.db.Query(ctx, `SELECT `+categoryCols+` FROM doorstep.categories ORDER BY sort_order, name, id`)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanCategory))
}

// CreateCategory inserts a category.
func (s *Store) CreateCategory(ctx context.Context, a Actor, in model.AdminCategoryInput) (*model.AdminCategory, error) {
	var out model.AdminCategory
	err := s.adminWrite(ctx, a, "category.create", "category", in, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanCategory(tx.QueryRow(ctx, `
			INSERT INTO doorstep.categories (slug, name, description, family, gender_rule, extras_policy, image_url, sort_order, active)
			VALUES ($1, $2, COALESCE($3::text, ''), $4, COALESCE($5::text, 'any'), COALESCE($6::text, 'rate_card'), $7,
			        COALESCE($8::int, 0), COALESCE($9::bool, FALSE))
			RETURNING `+categoryCols,
			in.Slug, in.Name, in.Description, in.Family, in.GenderRule, in.ExtrasPolicy, in.ImageURL, in.SortOrder, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateCategory patches a category.
func (s *Store) UpdateCategory(ctx context.Context, a Actor, id uuid.UUID, p model.AdminCategoryPatch) (*model.AdminCategory, error) {
	var out model.AdminCategory
	err := s.adminWrite(ctx, a, "category.update", "category", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanCategory(tx.QueryRow(ctx, `
			UPDATE doorstep.categories SET
			    name = COALESCE($2::text, name), description = COALESCE($3::text, description),
			    family = COALESCE($4::text, family), gender_rule = COALESCE($5::text, gender_rule),
			    extras_policy = COALESCE($6::text, extras_policy),
			    image_url = CASE WHEN $7::bool THEN $8::text ELSE image_url END,
			    sort_order = COALESCE($9::int, sort_order), active = COALESCE($10::bool, active),
			    updated_at = NOW()
			WHERE id = $1 RETURNING `+categoryCols,
			id, p.Name, p.Description, p.Family, p.GenderRule, p.ExtrasPolicy, p.ImageURL.Set, p.ImageURL.Ptr(), p.SortOrder, p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSkills lists skills.
func (s *Store) ListSkills(ctx context.Context) ([]model.Skill, error) {
	rows, err := s.db.Query(ctx, `SELECT code, name, description FROM doorstep.skills ORDER BY code`)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.Skill, error) {
		var k model.Skill
		err := r.Scan(&k.Code, &k.Name, &k.Description)
		return k, err
	})
}

// CreateSkill inserts a skill.
func (s *Store) CreateSkill(ctx context.Context, a Actor, in model.SkillInput) (*model.Skill, error) {
	var out model.Skill
	err := s.adminWrite(ctx, a, "skill.create", "skill", in, func(tx pgx.Tx) (string, error) {
		err := tx.QueryRow(ctx, `INSERT INTO doorstep.skills (code, name, description) VALUES ($1, $2, COALESCE($3::text, ''))
			RETURNING code, name, description`, in.Code, in.Name, in.Description).Scan(&out.Code, &out.Name, &out.Description)
		return in.Code, err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- services

const serviceCols = `id, category_id, slug, name, description, duration_minutes, required_skill, inclusions, exclusions, image_url,
	crew_size, min_before_photos, min_after_photos, rework_days, sort_order, active, created_at, updated_at`

func scanService(r pgx.Row) (model.AdminService, error) {
	var v model.AdminService
	err := r.Scan(&v.ID, &v.CategoryID, &v.Slug, &v.Name, &v.Description, &v.DurationMinutes, &v.RequiredSkill, &v.Inclusions,
		&v.Exclusions, &v.ImageURL, &v.CrewSize, &v.MinBeforePhotos, &v.MinAfterPhotos, &v.ReworkDays, &v.SortOrder, &v.Active,
		&v.CreatedAt, &v.UpdatedAt)
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	if v.Inclusions == nil {
		v.Inclusions = []string{}
	}
	if v.Exclusions == nil {
		v.Exclusions = []string{}
	}
	return v, err
}

// ListServices lists services, optionally of one category.
func (s *Store) ListServices(ctx context.Context, categoryID *uuid.UUID) ([]model.AdminService, error) {
	rows, err := s.db.Query(ctx, `SELECT `+serviceCols+` FROM doorstep.services
		WHERE ($1::uuid IS NULL OR category_id = $1) ORDER BY category_id, sort_order, name, id`, categoryID)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanService))
}

// CreateService inserts a service.
func (s *Store) CreateService(ctx context.Context, a Actor, in model.AdminServiceInput) (*model.AdminService, error) {
	var out model.AdminService
	err := s.adminWrite(ctx, a, "service.create", "service", in, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanService(tx.QueryRow(ctx, `
			INSERT INTO doorstep.services (category_id, slug, name, description, duration_minutes, required_skill, inclusions,
			    exclusions, image_url, crew_size, min_before_photos, min_after_photos, rework_days, sort_order, active)
			VALUES ($1, $2, $3, COALESCE($4::text, ''), $5, $6, COALESCE($7::text[], '{}'), COALESCE($8::text[], '{}'), $9,
			        COALESCE($10::int, 1), COALESCE($11::int, 2), COALESCE($12::int, 2), COALESCE($13::int, 7),
			        COALESCE($14::int, 0), COALESCE($15::bool, FALSE))
			RETURNING `+serviceCols,
			in.CategoryID, in.Slug, in.Name, in.Description, in.DurationMinutes, in.RequiredSkill, in.Inclusions, in.Exclusions,
			in.ImageURL, in.CrewSize, in.MinBeforePhotos, in.MinAfterPhotos, in.ReworkDays, in.SortOrder, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateService patches a service.
func (s *Store) UpdateService(ctx context.Context, a Actor, id uuid.UUID, p model.AdminServicePatch) (*model.AdminService, error) {
	var out model.AdminService
	err := s.adminWrite(ctx, a, "service.update", "service", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanService(tx.QueryRow(ctx, `
			UPDATE doorstep.services SET
			    name = COALESCE($2::text, name), description = COALESCE($3::text, description),
			    duration_minutes = COALESCE($4::int, duration_minutes), required_skill = COALESCE($5::text, required_skill),
			    inclusions = COALESCE($6::text[], inclusions), exclusions = COALESCE($7::text[], exclusions),
			    image_url = CASE WHEN $8::bool THEN $9::text ELSE image_url END,
			    min_before_photos = COALESCE($10::int, min_before_photos), min_after_photos = COALESCE($11::int, min_after_photos),
			    rework_days = COALESCE($12::int, rework_days), sort_order = COALESCE($13::int, sort_order),
			    active = COALESCE($14::bool, active), updated_at = NOW()
			WHERE id = $1 RETURNING `+serviceCols,
			id, p.Name, p.Description, p.DurationMinutes, p.RequiredSkill, p.Inclusions, p.Exclusions, p.ImageURL.Set, p.ImageURL.Ptr(),
			p.MinBeforePhotos, p.MinAfterPhotos, p.ReworkDays, p.SortOrder, p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

const optionCols = `id, service_id, name, description, duration_minutes, max_quantity, is_default, sort_order, active, created_at, updated_at`

func scanOption(r pgx.Row) (model.AdminOption, error) {
	var o model.AdminOption
	err := r.Scan(&o.ID, &o.ServiceID, &o.Name, &o.Description, &o.DurationMinutes, &o.MaxQuantity, &o.IsDefault, &o.SortOrder,
		&o.Active, &o.CreatedAt, &o.UpdatedAt)
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	return o, err
}

const groupCols = `id, service_id, name, min_select, max_select, is_required, sort_order, active, created_at, updated_at`

func scanGroup(r pgx.Row) (model.AdminAddonGroup, error) {
	var g model.AdminAddonGroup
	err := r.Scan(&g.ID, &g.ServiceID, &g.Name, &g.MinSelect, &g.MaxSelect, &g.IsRequired, &g.SortOrder, &g.Active, &g.CreatedAt, &g.UpdatedAt)
	g.CreatedAt, g.UpdatedAt = g.CreatedAt.UTC(), g.UpdatedAt.UTC()
	return g, err
}

const addonCols = `id, group_id, name, description, extra_duration_minutes, sort_order, active, created_at, updated_at`

func scanAddon(r pgx.Row) (model.AdminAddon, error) {
	var v model.AdminAddon
	err := r.Scan(&v.ID, &v.GroupID, &v.Name, &v.Description, &v.ExtraDurationMinutes, &v.SortOrder, &v.Active, &v.CreatedAt, &v.UpdatedAt)
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	return v, err
}

// ServiceTree returns a service with every option, group and add-on.
func (s *Store) ServiceTree(ctx context.Context, id uuid.UUID) (*model.AdminServiceTree, error) {
	svc, err := scanService(s.db.QueryRow(ctx, `SELECT `+serviceCols+` FROM doorstep.services WHERE id = $1`, id))
	if err != nil {
		return nil, mapErr(err)
	}
	t := &model.AdminServiceTree{Service: svc}
	rows, err := s.db.Query(ctx, `SELECT `+optionCols+` FROM doorstep.service_options WHERE service_id = $1 ORDER BY sort_order, name, id`, id)
	if err != nil {
		return nil, err
	}
	if t.Options, err = collect(rows, rowsOf(scanOption)); err != nil {
		return nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT `+groupCols+` FROM doorstep.addon_groups WHERE service_id = $1 ORDER BY sort_order, name, id`, id)
	if err != nil {
		return nil, err
	}
	groups, err := collect(rows, rowsOf(scanGroup))
	if err != nil {
		return nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT a.id, a.group_id, a.name, a.description, a.extra_duration_minutes, a.sort_order, a.active,
			a.created_at, a.updated_at
		FROM doorstep.addons a JOIN doorstep.addon_groups g ON g.id = a.group_id
		WHERE g.service_id = $1 ORDER BY a.sort_order, a.name, a.id`, id)
	if err != nil {
		return nil, err
	}
	addons, err := collect(rows, rowsOf(scanAddon))
	if err != nil {
		return nil, err
	}
	t.AddonGroups = make([]model.AdminAddonGroupTree, len(groups))
	for i, g := range groups {
		t.AddonGroups[i] = model.AdminAddonGroupTree{AdminAddonGroup: g, Addons: []model.AdminAddon{}}
		for _, a := range addons {
			if a.GroupID == g.ID {
				t.AddonGroups[i].Addons = append(t.AddonGroups[i].Addons, a)
			}
		}
	}
	return t, nil
}

// CreateOption adds an option to a service.
func (s *Store) CreateOption(ctx context.Context, a Actor, serviceID uuid.UUID, in model.AdminOptionInput) (*model.AdminOption, error) {
	var out model.AdminOption
	err := s.adminWrite(ctx, a, "option.create", "service_option", map[string]any{"service_id": serviceID, "input": in}, func(tx pgx.Tx) (string, error) {
		if err := requireRow(ctx, tx, `SELECT 1 FROM doorstep.services WHERE id = $1`, serviceID); err != nil {
			return "", err
		}
		var err error
		out, err = scanOption(tx.QueryRow(ctx, `
			INSERT INTO doorstep.service_options (service_id, name, description, duration_minutes, max_quantity, is_default, sort_order, active)
			VALUES ($1, $2, COALESCE($3::text, ''), $4, COALESCE($5::int, 1), COALESCE($6::bool, FALSE), COALESCE($7::int, 0), COALESCE($8::bool, TRUE))
			RETURNING `+optionCols,
			serviceID, in.Name, in.Description, in.DurationMinutes, in.MaxQuantity, in.IsDefault, in.SortOrder, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateOption patches an option.
func (s *Store) UpdateOption(ctx context.Context, a Actor, id uuid.UUID, p model.AdminOptionPatch) (*model.AdminOption, error) {
	var out model.AdminOption
	err := s.adminWrite(ctx, a, "option.update", "service_option", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanOption(tx.QueryRow(ctx, `
			UPDATE doorstep.service_options SET
			    name = COALESCE($2::text, name), description = COALESCE($3::text, description),
			    duration_minutes = COALESCE($4::int, duration_minutes), max_quantity = COALESCE($5::int, max_quantity),
			    is_default = COALESCE($6::bool, is_default), sort_order = COALESCE($7::int, sort_order),
			    active = COALESCE($8::bool, active), updated_at = NOW()
			WHERE id = $1 RETURNING `+optionCols,
			id, p.Name, p.Description, p.DurationMinutes, p.MaxQuantity, p.IsDefault, p.SortOrder, p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateAddonGroup adds an add-on group to a service.
func (s *Store) CreateAddonGroup(ctx context.Context, a Actor, serviceID uuid.UUID, in model.AdminAddonGroupInput) (*model.AdminAddonGroup, error) {
	var out model.AdminAddonGroup
	err := s.adminWrite(ctx, a, "addon_group.create", "addon_group", map[string]any{"service_id": serviceID, "input": in}, func(tx pgx.Tx) (string, error) {
		if err := requireRow(ctx, tx, `SELECT 1 FROM doorstep.services WHERE id = $1`, serviceID); err != nil {
			return "", err
		}
		var err error
		out, err = scanGroup(tx.QueryRow(ctx, `
			INSERT INTO doorstep.addon_groups (service_id, name, min_select, max_select, is_required, sort_order, active)
			VALUES ($1, $2, COALESCE($3::int, 0), $4, COALESCE($5::bool, FALSE), COALESCE($6::int, 0), COALESCE($7::bool, TRUE))
			RETURNING `+groupCols,
			serviceID, in.Name, in.MinSelect, in.MaxSelect, in.IsRequired, in.SortOrder, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAddonGroup patches an add-on group (the min <= max CHECK still holds).
func (s *Store) UpdateAddonGroup(ctx context.Context, a Actor, id uuid.UUID, p model.AdminAddonGroupPatch) (*model.AdminAddonGroup, error) {
	var out model.AdminAddonGroup
	err := s.adminWrite(ctx, a, "addon_group.update", "addon_group", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanGroup(tx.QueryRow(ctx, `
			UPDATE doorstep.addon_groups SET
			    name = COALESCE($2::text, name), min_select = COALESCE($3::int, min_select),
			    max_select = COALESCE($4::int, max_select), is_required = COALESCE($5::bool, is_required),
			    sort_order = COALESCE($6::int, sort_order), active = COALESCE($7::bool, active), updated_at = NOW()
			WHERE id = $1 RETURNING `+groupCols,
			id, p.Name, p.MinSelect, p.MaxSelect, p.IsRequired, p.SortOrder, p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateAddon adds an add-on to a group.
func (s *Store) CreateAddon(ctx context.Context, a Actor, groupID uuid.UUID, in model.AdminAddonInput) (*model.AdminAddon, error) {
	var out model.AdminAddon
	err := s.adminWrite(ctx, a, "addon.create", "addon", map[string]any{"group_id": groupID, "input": in}, func(tx pgx.Tx) (string, error) {
		if err := requireRow(ctx, tx, `SELECT 1 FROM doorstep.addon_groups WHERE id = $1`, groupID); err != nil {
			return "", err
		}
		var err error
		out, err = scanAddon(tx.QueryRow(ctx, `
			INSERT INTO doorstep.addons (group_id, name, description, extra_duration_minutes, sort_order, active)
			VALUES ($1, $2, COALESCE($3::text, ''), COALESCE($4::int, 0), COALESCE($5::int, 0), COALESCE($6::bool, TRUE))
			RETURNING `+addonCols,
			groupID, in.Name, in.Description, in.ExtraDurationMinutes, in.SortOrder, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAddon patches an add-on.
func (s *Store) UpdateAddon(ctx context.Context, a Actor, id uuid.UUID, p model.AdminAddonPatch) (*model.AdminAddon, error) {
	var out model.AdminAddon
	err := s.adminWrite(ctx, a, "addon.update", "addon", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanAddon(tx.QueryRow(ctx, `
			UPDATE doorstep.addons SET
			    name = COALESCE($2::text, name), description = COALESCE($3::text, description),
			    extra_duration_minutes = COALESCE($4::int, extra_duration_minutes),
			    sort_order = COALESCE($5::int, sort_order), active = COALESCE($6::bool, active), updated_at = NOW()
			WHERE id = $1 RETURNING `+addonCols,
			id, p.Name, p.Description, p.ExtraDurationMinutes, p.SortOrder, p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- prices

const priceCols = `id, city_code, item_kind, COALESCE(option_id, addon_id), price_paise, mrp_paise, effective_from, effective_to, created_by, created_at`

func scanPrice(r pgx.Row) (model.AdminPrice, error) {
	var p model.AdminPrice
	err := r.Scan(&p.ID, &p.CityCode, &p.ItemKind, &p.ItemID, &p.PricePaise, &p.MRPPaise, &p.EffectiveFrom, &p.EffectiveTo, &p.CreatedBy, &p.CreatedAt)
	p.EffectiveFrom, p.CreatedAt = p.EffectiveFrom.UTC(), p.CreatedAt.UTC()
	if p.EffectiveTo != nil {
		t := p.EffectiveTo.UTC()
		p.EffectiveTo = &t
	}
	return p, err
}

// ListPrices lists price rows (history included), newest first per item.
func (s *Store) ListPrices(ctx context.Context, city string, itemID *uuid.UUID) ([]model.AdminPrice, error) {
	rows, err := s.db.Query(ctx, `SELECT `+priceCols+` FROM doorstep.city_prices
		WHERE ($1::text = '' OR city_code = $1) AND ($2::uuid IS NULL OR option_id = $2 OR addon_id = $2)
		ORDER BY city_code, COALESCE(option_id, addon_id), effective_from DESC`, city, itemID)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanPrice))
}

// CreatePrice sets an item's price in a city from `from`: the open row of the
// same (city, item) is closed at `from`. A start at or before the open row's
// start, or any overlap with another row, is ErrOverlap.
func (s *Store) CreatePrice(ctx context.Context, a Actor, in model.AdminPriceInput, from time.Time) (*model.AdminPrice, error) {
	var out model.AdminPrice
	col := "option_id"
	if in.ItemKind == "addon" {
		col = "addon_id"
	}
	err := s.adminWrite(ctx, a, "price.create", "city_price", in, func(tx pgx.Tx) (string, error) {
		var openID uuid.UUID
		var openFrom time.Time
		err := tx.QueryRow(ctx, `SELECT id, effective_from FROM doorstep.city_prices
			WHERE city_code = $1 AND `+col+` = $2 AND effective_to IS NULL FOR UPDATE`, in.CityCode, in.ItemID).Scan(&openID, &openFrom)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return "", err
		default:
			if !from.After(openFrom) {
				return "", fmt.Errorf("%w: the new price must start after the current one (%s)", ErrOverlap, openFrom.UTC().Format(time.RFC3339))
			}
			if _, err := tx.Exec(ctx, `UPDATE doorstep.city_prices SET effective_to = $2 WHERE id = $1`, openID, from); err != nil {
				return "", err
			}
		}
		var optionID, addonID *uuid.UUID
		if in.ItemKind == "addon" {
			addonID = in.ItemID
		} else {
			optionID = in.ItemID
		}
		out, err = scanPrice(tx.QueryRow(ctx, `
			INSERT INTO doorstep.city_prices (city_code, item_kind, option_id, addon_id, price_paise, mrp_paise, effective_from, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING `+priceCols,
			in.CityCode, in.ItemKind, optionID, addonID, in.PricePaise, in.MRPPaise, from, a.UserID))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- rate cards

const rateCardCols = `id, city_code, category_id, code, name, description, unit, price_paise, max_quantity, is_part, sort_order, active, created_at, updated_at`

func scanRateCard(r pgx.Row) (model.AdminRateCard, error) {
	var v model.AdminRateCard
	err := r.Scan(&v.ID, &v.CityCode, &v.CategoryID, &v.Code, &v.Name, &v.Description, &v.Unit, &v.PricePaise, &v.MaxQuantity,
		&v.IsPart, &v.SortOrder, &v.Active, &v.CreatedAt, &v.UpdatedAt)
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	return v, err
}

// ListRateCards lists rate-card items.
func (s *Store) ListRateCards(ctx context.Context, city string, categoryID *uuid.UUID) ([]model.AdminRateCard, error) {
	rows, err := s.db.Query(ctx, `SELECT `+rateCardCols+` FROM doorstep.rate_cards
		WHERE ($1::text = '' OR city_code = $1) AND ($2::uuid IS NULL OR category_id = $2)
		ORDER BY city_code, category_id, sort_order, name, id`, city, categoryID)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanRateCard))
}

// CreateRateCard inserts a rate-card item.
func (s *Store) CreateRateCard(ctx context.Context, a Actor, in model.AdminRateCardInput) (*model.AdminRateCard, error) {
	var out model.AdminRateCard
	err := s.adminWrite(ctx, a, "rate_card.create", "rate_card", in, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanRateCard(tx.QueryRow(ctx, `
			INSERT INTO doorstep.rate_cards (city_code, category_id, code, name, description, unit, price_paise, max_quantity, is_part, sort_order, active)
			VALUES ($1, $2, $3, $4, COALESCE($5::text, ''), $6, $7, COALESCE($8::int, 10), COALESCE($9::bool, FALSE),
			        COALESCE($10::int, 0), COALESCE($11::bool, TRUE))
			RETURNING `+rateCardCols,
			in.CityCode, in.CategoryID, in.Code, in.Name, in.Description, in.Unit, in.PricePaise, in.MaxQuantity, in.IsPart, in.SortOrder, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateRateCard patches a rate-card item.
func (s *Store) UpdateRateCard(ctx context.Context, a Actor, id uuid.UUID, p model.AdminRateCardPatch) (*model.AdminRateCard, error) {
	var out model.AdminRateCard
	err := s.adminWrite(ctx, a, "rate_card.update", "rate_card", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanRateCard(tx.QueryRow(ctx, `
			UPDATE doorstep.rate_cards SET
			    name = COALESCE($2::text, name), description = COALESCE($3::text, description), unit = COALESCE($4::text, unit),
			    price_paise = COALESCE($5::bigint, price_paise), max_quantity = COALESCE($6::int, max_quantity),
			    is_part = COALESCE($7::bool, is_part), sort_order = COALESCE($8::int, sort_order),
			    active = COALESCE($9::bool, active), updated_at = NOW()
			WHERE id = $1 RETURNING `+rateCardCols,
			id, p.Name, p.Description, p.Unit, p.PricePaise, p.MaxQuantity, p.IsPart, p.SortOrder, p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- slot configs

const slotCols = `id, city_code, category_id, to_char(open_time, 'HH24:MI'), to_char(close_time, 'HH24:MI'), slot_step_minutes,
	min_lead_minutes, horizon_days, hold_minutes, active, created_at, updated_at`

func scanSlot(r pgx.Row) (model.AdminSlotConfig, error) {
	var v model.AdminSlotConfig
	err := r.Scan(&v.ID, &v.CityCode, &v.CategoryID, &v.OpenTime, &v.CloseTime, &v.SlotStepMinutes, &v.MinLeadMinutes,
		&v.HorizonDays, &v.HoldMinutes, &v.Active, &v.CreatedAt, &v.UpdatedAt)
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	return v, err
}

// ListSlotConfigs lists slot configs.
func (s *Store) ListSlotConfigs(ctx context.Context, city string) ([]model.AdminSlotConfig, error) {
	rows, err := s.db.Query(ctx, `SELECT `+slotCols+` FROM doorstep.slot_configs
		WHERE ($1::text = '' OR city_code = $1) ORDER BY city_code, category_id NULLS FIRST, id`, city)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanSlot))
}

// CreateSlotConfig inserts a slot config.
func (s *Store) CreateSlotConfig(ctx context.Context, a Actor, in model.AdminSlotConfigInput) (*model.AdminSlotConfig, error) {
	var out model.AdminSlotConfig
	err := s.adminWrite(ctx, a, "slot_config.create", "slot_config", in, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanSlot(tx.QueryRow(ctx, `
			INSERT INTO doorstep.slot_configs (city_code, category_id, open_time, close_time, slot_step_minutes, min_lead_minutes,
			    horizon_days, hold_minutes, active)
			VALUES ($1, $2, $3::time, $4::time, COALESCE($5::int, 30), COALESCE($6::int, 120), COALESCE($7::int, 7),
			        COALESCE($8::int, 10), COALESCE($9::bool, TRUE))
			RETURNING `+slotCols,
			in.CityCode, in.CategoryID, in.OpenTime, in.CloseTime, in.SlotStepMinutes, in.MinLeadMinutes, in.HorizonDays, in.HoldMinutes, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateSlotConfig patches a slot config.
func (s *Store) UpdateSlotConfig(ctx context.Context, a Actor, id uuid.UUID, p model.AdminSlotConfigPatch) (*model.AdminSlotConfig, error) {
	var out model.AdminSlotConfig
	err := s.adminWrite(ctx, a, "slot_config.update", "slot_config", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanSlot(tx.QueryRow(ctx, `
			UPDATE doorstep.slot_configs SET
			    open_time = COALESCE($2::time, open_time), close_time = COALESCE($3::time, close_time),
			    slot_step_minutes = COALESCE($4::int, slot_step_minutes), min_lead_minutes = COALESCE($5::int, min_lead_minutes),
			    horizon_days = COALESCE($6::int, horizon_days), hold_minutes = COALESCE($7::int, hold_minutes),
			    active = COALESCE($8::bool, active), updated_at = NOW()
			WHERE id = $1 RETURNING `+slotCols,
			id, p.OpenTime, p.CloseTime, p.SlotStepMinutes, p.MinLeadMinutes, p.HorizonDays, p.HoldMinutes, p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- cancellation rules

const cancelCols = `id, city_code, category_id, stage, minutes_before_lt, fee_paise, allowed, sort_order, active, created_at, updated_at`

func scanCancel(r pgx.Row) (model.AdminCancellationRule, error) {
	var v model.AdminCancellationRule
	err := r.Scan(&v.ID, &v.CityCode, &v.CategoryID, &v.Stage, &v.MinutesBeforeLT, &v.FeePaise, &v.Allowed, &v.SortOrder, &v.Active,
		&v.CreatedAt, &v.UpdatedAt)
	v.CreatedAt, v.UpdatedAt = v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	return v, err
}

// ListCancellationRules lists cancellation rules.
func (s *Store) ListCancellationRules(ctx context.Context, city string) ([]model.AdminCancellationRule, error) {
	rows, err := s.db.Query(ctx, `SELECT `+cancelCols+` FROM doorstep.cancellation_rules
		WHERE ($1::text = '' OR city_code = $1) ORDER BY city_code, category_id NULLS LAST, stage, sort_order, id`, city)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanCancel))
}

// CreateCancellationRule inserts a rule.
func (s *Store) CreateCancellationRule(ctx context.Context, a Actor, in model.AdminCancellationRuleInput) (*model.AdminCancellationRule, error) {
	var out model.AdminCancellationRule
	err := s.adminWrite(ctx, a, "cancellation_rule.create", "cancellation_rule", in, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanCancel(tx.QueryRow(ctx, `
			INSERT INTO doorstep.cancellation_rules (city_code, category_id, stage, minutes_before_lt, fee_paise, allowed, sort_order, active)
			VALUES ($1, $2, $3, $4, $5, COALESCE($6::bool, TRUE), COALESCE($7::int, 0), COALESCE($8::bool, TRUE))
			RETURNING `+cancelCols,
			in.CityCode, in.CategoryID, in.Stage, in.MinutesBeforeLT, in.FeePaise, in.Allowed, in.SortOrder, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateCancellationRule patches a rule.
func (s *Store) UpdateCancellationRule(ctx context.Context, a Actor, id uuid.UUID, p model.AdminCancellationRulePatch) (*model.AdminCancellationRule, error) {
	var out model.AdminCancellationRule
	err := s.adminWrite(ctx, a, "cancellation_rule.update", "cancellation_rule", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanCancel(tx.QueryRow(ctx, `
			UPDATE doorstep.cancellation_rules SET
			    minutes_before_lt = CASE WHEN $2::bool THEN $3::int ELSE minutes_before_lt END,
			    fee_paise = COALESCE($4::bigint, fee_paise), allowed = COALESCE($5::bool, allowed),
			    sort_order = COALESCE($6::int, sort_order), active = COALESCE($7::bool, active), updated_at = NOW()
			WHERE id = $1 RETURNING `+cancelCols,
			id, p.MinutesBeforeLT.Set, p.MinutesBeforeLT.Ptr(), p.FeePaise, p.Allowed, p.SortOrder, p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- commission rules

const commissionCols = `id, city_code, category_id, commission_bps, effective_from, effective_to, active, created_at, updated_at`

func scanCommission(r pgx.Row) (model.AdminCommissionRule, error) {
	var v model.AdminCommissionRule
	err := r.Scan(&v.ID, &v.CityCode, &v.CategoryID, &v.CommissionBPS, &v.EffectiveFrom, &v.EffectiveTo, &v.Active, &v.CreatedAt, &v.UpdatedAt)
	v.EffectiveFrom, v.CreatedAt, v.UpdatedAt = v.EffectiveFrom.UTC(), v.CreatedAt.UTC(), v.UpdatedAt.UTC()
	if v.EffectiveTo != nil {
		t := v.EffectiveTo.UTC()
		v.EffectiveTo = &t
	}
	return v, err
}

// ListCommissionRules lists commission rules.
func (s *Store) ListCommissionRules(ctx context.Context, city string) ([]model.AdminCommissionRule, error) {
	rows, err := s.db.Query(ctx, `SELECT `+commissionCols+` FROM doorstep.commission_rules
		WHERE ($1::text = '' OR city_code = $1) ORDER BY city_code, category_id NULLS FIRST, effective_from DESC, id`, city)
	if err != nil {
		return nil, err
	}
	return collect(rows, rowsOf(scanCommission))
}

// CreateCommissionRule inserts a commission rule.
func (s *Store) CreateCommissionRule(ctx context.Context, a Actor, in model.AdminCommissionRuleInput, from time.Time) (*model.AdminCommissionRule, error) {
	var out model.AdminCommissionRule
	err := s.adminWrite(ctx, a, "commission_rule.create", "commission_rule", in, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanCommission(tx.QueryRow(ctx, `
			INSERT INTO doorstep.commission_rules (city_code, category_id, commission_bps, effective_from, effective_to, active)
			VALUES ($1, $2, $3, $4, $5, COALESCE($6::bool, TRUE))
			RETURNING `+commissionCols,
			in.CityCode, in.CategoryID, in.CommissionBPS, from, in.EffectiveTo, in.Active))
		return out.ID.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateCommissionRule patches a commission rule.
func (s *Store) UpdateCommissionRule(ctx context.Context, a Actor, id uuid.UUID, p model.AdminCommissionRulePatch) (*model.AdminCommissionRule, error) {
	var out model.AdminCommissionRule
	err := s.adminWrite(ctx, a, "commission_rule.update", "commission_rule", p, func(tx pgx.Tx) (string, error) {
		var err error
		out, err = scanCommission(tx.QueryRow(ctx, `
			UPDATE doorstep.commission_rules SET
			    commission_bps = COALESCE($2::int, commission_bps),
			    effective_to = CASE WHEN $3::bool THEN $4::timestamptz ELSE effective_to END,
			    active = COALESCE($5::bool, active), updated_at = NOW()
			WHERE id = $1 RETURNING `+commissionCols,
			id, p.CommissionBPS, p.EffectiveTo.Set, p.EffectiveTo.Ptr(), p.Active))
		return id.String(), err
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------------------------------------------------------------- audit

// ListAuditLogs lists audit rows newest first.
func (s *Store) ListAuditLogs(ctx context.Context, entity string, limit int) ([]model.AuditLog, error) {
	rows, err := s.db.Query(ctx, `SELECT id, actor_user_id, permission, action, entity, entity_id, details::text, created_at
		FROM doorstep.admin_audit_log WHERE ($1::text = '' OR entity = $1) ORDER BY id DESC LIMIT $2`, entity, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.AuditLog, error) {
		var v model.AuditLog
		var details string
		err := r.Scan(&v.ID, &v.ActorUserID, &v.Permission, &v.Action, &v.Entity, &v.EntityID, &details, &v.CreatedAt)
		v.Details = json.RawMessage(details)
		v.CreatedAt = v.CreatedAt.UTC()
		return v, err
	})
}
