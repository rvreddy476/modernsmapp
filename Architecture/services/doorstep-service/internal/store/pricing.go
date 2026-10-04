package store

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/doorstep-service/internal/catalogue"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Professionals' own prices (B1, 4 Oct 2026). A professional prices the
// options and add-ons of the services whose skill they declared; every new
// or changed price is pending until an admin approves it (the approved row
// stays live meanwhile); only an approved, live row is ever quoted or
// booked. The admin decision, its event and its audit row commit together
// (adminWrite), and the database refuses an approval without that audit row
// (doorstep.require_admin_review, migration 005).

// Pricing sentinels.
var (
	// ErrSkillRequired: the service's skill is not declared by the
	// professional, or an admin revoked it.
	ErrSkillRequired = errors.New("store: the professional has not declared the service's skill")
	// ErrUnchanged: the submitted price is the live approved one and
	// nothing is pending.
	ErrUnchanged = errors.New("store: the price is already the approved one")
)

const proPriceCols = `pp.id, pp.service_id, pp.item_kind, COALESCE(pp.option_id, pp.addon_id), pp.unit, pp.price_paise, pp.status,
	pp.effective_from, pp.effective_to, pp.submitted_at, pp.reviewed_at, pp.reason`

func scanProPrice(r pgx.Row) (model.ProPrice, error) {
	var p model.ProPrice
	err := r.Scan(&p.ID, &p.ServiceID, &p.ItemKind, &p.ItemID, &p.Unit, &p.PricePaise, &p.Status, &p.EffectiveFrom, &p.EffectiveTo,
		&p.SubmittedAt, &p.ReviewedAt, &p.Reason)
	p.SubmittedAt = p.SubmittedAt.UTC()
	utcPtr(&p.EffectiveFrom)
	utcPtr(&p.EffectiveTo)
	utcPtr(&p.ReviewedAt)
	return p, mapErr(err)
}

// PricingFacts is the professional the pricing routes act for.
type PricingFacts struct {
	Status            string
	IncidentSuspended bool
}

// ProPricing lists the services the professional may price (skill declared
// and not revoked; active service in an active category) with every item,
// the live approved price, the pending submission and the newest rejection.
// Bookable is left for the service (it knows the tax families).
func (s *Store) ProPricing(ctx context.Context, proID uuid.UUID, at time.Time) (*PricingFacts, []model.ProServicePricing, error) {
	var f PricingFacts
	var city string
	if err := s.db.QueryRow(ctx, `SELECT status, incident_suspended, city_code FROM doorstep.professionals WHERE id = $1`, proID).
		Scan(&f.Status, &f.IncidentSuspended, &city); err != nil {
		return nil, nil, mapErr(err)
	}
	rows, err := s.db.Query(ctx, `
		SELECT sv.id, sv.name, c.slug, c.name, c.family, sv.required_skill, ps.status, COALESCE(st.same_day, FALSE)
		FROM doorstep.services sv
		JOIN doorstep.categories c ON c.id = sv.category_id
		JOIN doorstep.pro_skills ps ON ps.pro_id = $1 AND ps.skill_code = sv.required_skill AND ps.status IN ('pending', 'verified')
		LEFT JOIN doorstep.pro_service_settings st ON st.pro_id = $1 AND st.service_id = sv.id
		WHERE sv.active AND c.active
		ORDER BY c.sort_order, c.name, sv.sort_order, sv.name, sv.id`, proID)
	if err != nil {
		return nil, nil, err
	}
	services, err := collect(rows, func(r pgx.Rows) (model.ProServicePricing, error) {
		var v model.ProServicePricing
		err := r.Scan(&v.ServiceID, &v.ServiceName, &v.CategorySlug, &v.CategoryName, &v.Family, &v.SkillCode, &v.SkillStatus, &v.SameDay)
		v.Items = []model.ProPriceItem{}
		return v, err
	})
	if err != nil || len(services) == 0 {
		return &f, services, err
	}
	ids := make([]uuid.UUID, len(services))
	idx := map[uuid.UUID]int{}
	for i, v := range services {
		ids[i], idx[v.ServiceID] = v.ServiceID, i
	}
	type itemRow struct {
		service uuid.UUID
		item    model.ProPriceItem
	}
	rows, err = s.db.Query(ctx, `
		SELECT * FROM (
		    SELECT o.service_id, 'option' AS kind, o.id, o.name, o.unit, o.max_quantity, cp.price_paise, 0 AS grp_sort, o.sort_order, o.name AS sort_name
		    FROM doorstep.service_options o
		    LEFT JOIN doorstep.city_prices cp ON cp.option_id = o.id AND cp.city_code = $2
		          AND cp.effective_from <= $3 AND (cp.effective_to IS NULL OR cp.effective_to > $3)
		    WHERE o.service_id = ANY($1) AND o.active
		    UNION ALL
		    SELECT g.service_id, 'addon', a.id, a.name, 'per_job', 1, cp.price_paise, 1 + g.sort_order, a.sort_order, a.name
		    FROM doorstep.addons a
		    JOIN doorstep.addon_groups g ON g.id = a.group_id AND g.active
		    LEFT JOIN doorstep.city_prices cp ON cp.addon_id = a.id AND cp.city_code = $2
		          AND cp.effective_from <= $3 AND (cp.effective_to IS NULL OR cp.effective_to > $3)
		    WHERE g.service_id = ANY($1) AND a.active
		) x ORDER BY service_id, grp_sort, sort_order, sort_name, id`, ids, city, at)
	if err != nil {
		return nil, nil, err
	}
	items, err := collect(rows, func(r pgx.Rows) (itemRow, error) {
		var v itemRow
		var gs, so int
		var sn string
		err := r.Scan(&v.service, &v.item.ItemKind, &v.item.ItemID, &v.item.Name, &v.item.Unit, &v.item.MaxQuantity,
			&v.item.SuggestedPricePaise, &gs, &so, &sn)
		return v, err
	})
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT `+proPriceCols+` FROM doorstep.pro_service_prices pp
		WHERE pp.pro_id = $1 AND pp.service_id = ANY($2) ORDER BY pp.submitted_at, pp.id`, proID, ids)
	if err != nil {
		return nil, nil, err
	}
	prices, err := collect(rows, func(r pgx.Rows) (model.ProPrice, error) { return scanProPrice(r) })
	if err != nil {
		return nil, nil, err
	}
	byItem := map[uuid.UUID][]model.ProPrice{}
	for _, p := range prices {
		byItem[p.ItemID] = append(byItem[p.ItemID], p)
	}
	for _, it := range items {
		v := it.item
		for _, p := range byItem[v.ItemID] {
			p := p
			switch {
			case p.Status == "approved" && p.EffectiveFrom != nil && !p.EffectiveFrom.After(at) && (p.EffectiveTo == nil || p.EffectiveTo.After(at)):
				v.Approved = &p
			case p.Status == "pending":
				v.Pending = &p
			case p.Status == "rejected":
				v.Rejected = &p // oldest first: the newest wins
			}
		}
		// A rejection older than what is live or pending is history.
		if v.Rejected != nil && ((v.Approved != nil && !v.Rejected.SubmittedAt.After(v.Approved.SubmittedAt)) ||
			(v.Pending != nil && !v.Rejected.SubmittedAt.After(v.Pending.SubmittedAt))) {
			v.Rejected = nil
		}
		i := idx[it.service]
		services[i].Items = append(services[i].Items, v)
	}
	return &f, services, nil
}

// SubmitPrice is a professional's price for one option or add-on.
type SubmitPrice struct {
	ProID      uuid.UUID
	ServiceID  uuid.UUID
	ItemKind   string // option | addon
	ItemID     uuid.UUID
	PricePaise int64
	At         time.Time
}

// SubmitProPrice stores a pending price. A submission already pending for
// the item is withdrawn (superseded) in the same transaction; the live
// approved price stays live until an admin approves the new one. The
// service's skill must be declared and not revoked (ErrSkillRequired); the
// item must be an active item of the active service (ErrNotFound); the live
// approved price with nothing pending is ErrUnchanged.
// doorstep.pro.price_submitted goes out with it.
func (s *Store) SubmitProPrice(ctx context.Context, in SubmitPrice) (*model.ProPrice, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var proUser uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT user_id FROM doorstep.professionals WHERE id = $1 FOR UPDATE`, in.ProID).Scan(&proUser); err != nil {
		return nil, mapErr(err)
	}
	var skillOK bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM doorstep.pro_skills ps WHERE ps.pro_id = $2 AND ps.skill_code = sv.required_skill
		                AND ps.status IN ('pending', 'verified'))
		FROM doorstep.services sv JOIN doorstep.categories c ON c.id = sv.category_id
		WHERE sv.id = $1 AND sv.active AND c.active`, in.ServiceID, in.ProID).Scan(&skillOK)
	if err != nil {
		return nil, mapErr(err)
	}
	if !skillOK {
		return nil, ErrSkillRequired
	}
	var unit string
	if in.ItemKind == "option" {
		err = tx.QueryRow(ctx, `SELECT unit FROM doorstep.service_options WHERE id = $1 AND service_id = $2 AND active`,
			in.ItemID, in.ServiceID).Scan(&unit)
	} else {
		unit = catalogue.UnitPerJob
		err = tx.QueryRow(ctx, `SELECT 'per_job' FROM doorstep.addons a JOIN doorstep.addon_groups g ON g.id = a.group_id
			WHERE a.id = $1 AND g.service_id = $2 AND a.active AND g.active`, in.ItemID, in.ServiceID).Scan(&unit)
	}
	if err != nil {
		return nil, mapErr(err)
	}
	col := "option_id"
	if in.ItemKind == "addon" {
		col = "addon_id"
	}
	var live *int64
	var pending int
	if err := tx.QueryRow(ctx, `SELECT
		    (SELECT price_paise FROM doorstep.pro_service_prices WHERE pro_id = $1 AND `+col+` = $2 AND status = 'approved'
		       AND effective_to IS NULL),
		    (SELECT count(*) FROM doorstep.pro_service_prices WHERE pro_id = $1 AND `+col+` = $2 AND status = 'pending')`,
		in.ProID, in.ItemID).Scan(&live, &pending); err != nil {
		return nil, err
	}
	if live != nil && *live == in.PricePaise && pending == 0 {
		return nil, ErrUnchanged
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_service_prices SET status = 'withdrawn', reason = 'superseded by a newer submission',
		updated_at = $3 WHERE pro_id = $1 AND `+col+` = $2 AND status = 'pending'`, in.ProID, in.ItemID, in.At); err != nil {
		return nil, err
	}
	var optID, addID *uuid.UUID
	if in.ItemKind == "option" {
		optID = &in.ItemID
	} else {
		addID = &in.ItemID
	}
	out, err := scanProPrice(tx.QueryRow(ctx, `
		INSERT INTO doorstep.pro_service_prices AS pp (pro_id, service_id, item_kind, option_id, addon_id, unit, price_paise, status,
		                                             submitted_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'pending', $8, $8, $8)
		RETURNING `+proPriceCols, in.ProID, in.ServiceID, in.ItemKind, optID, addID, unit, in.PricePaise, in.At))
	if err != nil {
		return nil, err
	}
	if err := s.enqueueProEvent(ctx, tx, events.ProPriceSubmitted, proUser, events.ProPriceSubmittedData{
		PriceID: out.ID, ProID: in.ProID, ProUserID: proUser, ServiceID: in.ServiceID, ItemKind: in.ItemKind, ItemID: in.ItemID,
		Unit: unit, PricePaise: in.PricePaise, PreviousPricePaise: live}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return &out, nil
}

// WithdrawProPrice takes one of the professional's prices back: a pending
// one is withdrawn; the live approved one stops now (withdrawn, effective_to
// = at), so the item is no longer bookable with this professional. Anything
// else is a *TransitionError; another professional's row is ErrNotFound.
func (s *Store) WithdrawProPrice(ctx context.Context, proID, priceID uuid.UUID, at time.Time) (*model.ProPrice, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cur, err := scanProPrice(tx.QueryRow(ctx, `SELECT `+proPriceCols+` FROM doorstep.pro_service_prices pp
		WHERE pp.id = $1 AND pp.pro_id = $2 FOR UPDATE`, priceID, proID))
	if err != nil {
		return nil, err
	}
	live := cur.Status == "approved" && cur.EffectiveTo == nil
	if cur.Status != "pending" && !live {
		return nil, &TransitionError{Status: cur.Status}
	}
	out, err := scanProPrice(tx.QueryRow(ctx, `UPDATE doorstep.pro_service_prices pp SET status = 'withdrawn',
		    effective_to = CASE WHEN pp.status = 'approved' THEN GREATEST($2, pp.effective_from) END, updated_at = $2
		WHERE pp.id = $1 RETURNING `+proPriceCols, priceID, at))
	if err != nil {
		return nil, err
	}
	return &out, mapErr(tx.Commit(ctx))
}

// SetSameDay records the professional's same-day opt-in for a service whose
// skill they declared (else ErrSkillRequired).
func (s *Store) SetSameDay(ctx context.Context, proID, serviceID uuid.UUID, enabled bool, at time.Time) (*model.SameDaySetting, error) {
	var ok bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM doorstep.services sv JOIN doorstep.pro_skills ps
		ON ps.pro_id = $1 AND ps.skill_code = sv.required_skill AND ps.status IN ('pending', 'verified') WHERE sv.id = $2 AND sv.active)`,
		proID, serviceID).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrSkillRequired
	}
	out := &model.SameDaySetting{ServiceID: serviceID}
	err := s.db.QueryRow(ctx, `INSERT INTO doorstep.pro_service_settings (pro_id, service_id, same_day, updated_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (pro_id, service_id) DO UPDATE SET same_day = EXCLUDED.same_day, updated_at = EXCLUDED.updated_at
		RETURNING same_day, updated_at`, proID, serviceID, enabled, at).Scan(&out.SameDay, &out.UpdatedAt)
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, mapErr(err)
}

// ---------------------------------------------------------------- customer side

// ProItemPrices are the bookable prices (liveProPrice) of the given
// professionals for the given items in city at `at`, by professional and
// item. A professional without one of the items simply lacks it.
func (s *Store) ProItemPrices(ctx context.Context, city string, at time.Time, proIDs, itemIDs []uuid.UUID) (map[uuid.UUID]catalogue.ProPrices, error) {
	out := map[uuid.UUID]catalogue.ProPrices{}
	if len(proIDs) == 0 || len(itemIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT pp.pro_id, COALESCE(pp.option_id, pp.addon_id), pp.id, pp.price_paise, pp.unit
		FROM doorstep.pro_service_prices pp
		JOIN doorstep.professionals p ON p.id = pp.pro_id
		JOIN doorstep.services sv ON sv.id = pp.service_id
		WHERE pp.pro_id = ANY($3) AND COALESCE(pp.option_id, pp.addon_id) = ANY($4) AND `+liveProPrice,
		city, at, proIDs, itemIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var pro, item uuid.UUID
		var p catalogue.ItemPrice
		if err := rows.Scan(&pro, &item, &p.ID, &p.PricePaise, &p.Unit); err != nil {
			return nil, err
		}
		if out[pro] == nil {
			out[pro] = catalogue.ProPrices{}
		}
		out[pro][item] = p
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- admin review

// PriceCursor is the review queue's keyset position (submitted_at, id).
type PriceCursor struct {
	SubmittedAt time.Time
	ID          uuid.UUID
}

const adminPriceSelect = `SELECT ` + proPriceCols + `, p.id, p.display_name, p.status, p.city_code, sv.name, c.slug,
	    COALESCE(o.name, a.name, ''), cp.price_paise,
	    (SELECT cur.price_paise FROM doorstep.pro_service_prices cur WHERE cur.pro_id = pp.pro_id
	        AND COALESCE(cur.option_id, cur.addon_id) = COALESCE(pp.option_id, pp.addon_id)
	        AND cur.status = 'approved' AND cur.effective_to IS NULL AND cur.id <> pp.id),
	    pp.reviewed_by
	FROM doorstep.pro_service_prices pp
	JOIN doorstep.professionals p ON p.id = pp.pro_id
	JOIN doorstep.services sv ON sv.id = pp.service_id
	JOIN doorstep.categories c ON c.id = sv.category_id
	LEFT JOIN doorstep.service_options o ON o.id = pp.option_id
	LEFT JOIN doorstep.addons a ON a.id = pp.addon_id
	LEFT JOIN doorstep.city_prices cp ON cp.city_code = p.city_code
	     AND ((pp.option_id IS NOT NULL AND cp.option_id = pp.option_id) OR (pp.addon_id IS NOT NULL AND cp.addon_id = pp.addon_id))
	     AND cp.effective_from <= NOW() AND (cp.effective_to IS NULL OR cp.effective_to > NOW())`

func scanAdminPrice(r pgx.Row) (model.AdminProPrice, error) {
	var v model.AdminProPrice
	p := &v.ProPrice
	err := r.Scan(&p.ID, &p.ServiceID, &p.ItemKind, &p.ItemID, &p.Unit, &p.PricePaise, &p.Status, &p.EffectiveFrom, &p.EffectiveTo,
		&p.SubmittedAt, &p.ReviewedAt, &p.Reason, &v.ProID, &v.ProDisplayName, &v.ProStatus, &v.CityCode, &v.ServiceName,
		&v.CategorySlug, &v.ItemName, &v.SuggestedPricePaise, &v.CurrentApprovedPaise, &v.ReviewedBy)
	p.SubmittedAt = p.SubmittedAt.UTC()
	utcPtr(&p.EffectiveFrom)
	utcPtr(&p.EffectiveTo)
	utcPtr(&p.ReviewedAt)
	return v, mapErr(err)
}

// ListPriceReviews pages price rows by status (pending: the review queue,
// oldest first), optionally for one city or one professional.
func (s *Store) ListPriceReviews(ctx context.Context, status, city string, proID *uuid.UUID, after *PriceCursor, limit int) ([]model.AdminProPrice, error) {
	var at *time.Time
	var id *uuid.UUID
	if after != nil {
		at, id = &after.SubmittedAt, &after.ID
	}
	rows, err := s.db.Query(ctx, adminPriceSelect+`
		WHERE pp.status = $1 AND ($2::text = '' OR p.city_code = $2) AND ($3::uuid IS NULL OR pp.pro_id = $3)
		  AND ($4::timestamptz IS NULL OR (pp.submitted_at, pp.id) > ($4::timestamptz, $5::uuid))
		ORDER BY pp.submitted_at, pp.id LIMIT $6`, status, city, proID, at, id, limit)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (model.AdminProPrice, error) { return scanAdminPrice(r) })
}

// DecideProPrice approves or rejects a pending price as an admin, audited in
// the same transaction. Approving closes the item's live approved row at
// `at` and makes this one live from `at`. A row that is not pending is a
// *TransitionError. doorstep.pro.price_reviewed goes out with it.
func (s *Store) DecideProPrice(ctx context.Context, a Actor, id uuid.UUID, approve bool, reason *string, at time.Time) (*model.AdminProPrice, error) {
	decision := "rejected"
	if approve {
		decision = "approved"
	}
	details := map[string]any{"decision": decision, "reason": reason}
	err := s.adminWrite(ctx, a, "price."+map[bool]string{true: "approve", false: "reject"}[approve], "pro_service_price", details,
		func(tx pgx.Tx) (string, error) {
			var status, kind string
			var proID, proUser, serviceID, item uuid.UUID
			var price int64
			err := tx.QueryRow(ctx, `SELECT pp.status, pp.item_kind, pp.pro_id, p.user_id, pp.service_id, COALESCE(pp.option_id, pp.addon_id),
				    pp.price_paise
				FROM doorstep.pro_service_prices pp JOIN doorstep.professionals p ON p.id = pp.pro_id
				WHERE pp.id = $1 FOR UPDATE OF pp`, id).Scan(&status, &kind, &proID, &proUser, &serviceID, &item, &price)
			if err != nil {
				return "", err
			}
			if status != "pending" {
				return "", &TransitionError{Status: status}
			}
			col := "option_id"
			if kind == "addon" {
				col = "addon_id"
			}
			if approve {
				if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_service_prices SET effective_to = GREATEST($3, effective_from), updated_at = $3
					WHERE pro_id = $1 AND `+col+` = $2 AND status = 'approved' AND effective_to IS NULL`, proID, item, at); err != nil {
					return "", err
				}
				if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_service_prices SET status = 'approved', effective_from = $2,
					reviewed_by = $3, reviewed_at = $2, reason = $4, updated_at = $2 WHERE id = $1`, id, at, a.UserID, reason); err != nil {
					return "", err
				}
			} else {
				if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_service_prices SET status = 'rejected', reviewed_by = $3, reviewed_at = $2,
					reason = $4, updated_at = $2 WHERE id = $1`, id, at, a.UserID, reason); err != nil {
					return "", err
				}
			}
			details["pro_id"], details["item_id"], details["price_paise"] = proID, item, price
			if err := s.enqueueProEvent(ctx, tx, events.ProPriceReviewed, proUser, events.ProPriceReviewedData{
				PriceID: id, ProID: proID, ProUserID: proUser, ServiceID: serviceID, ItemKind: kind, ItemID: item,
				PricePaise: price, Decision: decision, Reason: reason}); err != nil {
				return "", err
			}
			return id.String(), nil
		})
	if err != nil {
		return nil, err
	}
	v, err := scanAdminPrice(s.db.QueryRow(ctx, adminPriceSelect+` WHERE pp.id = $1`, id))
	if err != nil {
		return nil, err
	}
	return &v, nil
}
