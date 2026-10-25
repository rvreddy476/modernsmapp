package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/atpost/doorstep-service/internal/prokyc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ChangePayload never contains plaintext PAN or bank account numbers.
type ChangePayload struct {
	Profile   *model.ProPatchInput `json:"profile,omitempty"`
	Skills    []SkillDecl          `json:"skills,omitempty"`
	Area      *model.ProArea       `json:"area,omitempty"`
	Hours     []prokyc.Window      `json:"hours,omitempty"`
	PANSealed []byte               `json:"pan_sealed,omitempty"`
	PANLast4  string               `json:"pan_last4,omitempty"`
}

type PartnerChange struct {
	ID        uuid.UUID       `json:"id"`
	ProID     uuid.UUID       `json:"pro_id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	Status    string          `json:"status"`
	Reason    *string         `json:"reason"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s *Store) ChangePhoto(ctx context.Context, a Actor, id uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	var owner, media uuid.UUID
	err := s.adminWrite(ctx, a, "professional.change_photo_view", "pro_change", map[string]any{}, func(tx pgx.Tx) (string, error) {
		err := tx.QueryRow(ctx, `SELECT p.user_id,(r.payload->'profile'->>'photo_media_id')::uuid FROM doorstep.pro_change_requests r JOIN doorstep.professionals p ON p.id=r.pro_id WHERE r.id=$1 AND r.kind='profile' AND r.payload->'profile'->>'photo_media_id' IS NOT NULL`, id).Scan(&owner, &media)
		return id.String(), mapErr(err)
	})
	return owner, media, err
}

// submitPartnerChange serializes edits with approval/status changes. Approved
// accounts retain their active values while an immutable request is reviewed.
func (s *Store) submitPartnerChange(ctx context.Context, id uuid.UUID, kind string, in ChangePayload) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var status string
	var approved bool
	if err = tx.QueryRow(ctx, `SELECT status, approved_at IS NOT NULL FROM doorstep.professionals WHERE id=$1 FOR UPDATE`, id).Scan(&status, &approved); err != nil {
		return mapErr(err)
	}
	if status == "blocked" || status == "rejected" {
		return &TransitionError{Status: status}
	}
	if approved || status == "approved" || status == "suspended" {
		// Validate references and constraints without changing the active account.
		probe, e := tx.Begin(ctx)
		if e != nil {
			return e
		}
		e = applyPartnerChange(ctx, probe, id, kind, in)
		if rb := probe.Rollback(ctx); e == nil {
			e = rb
		}
		if e != nil {
			return e
		}
		raw, e := json.Marshal(in)
		if e != nil {
			return e
		}
		// A retry of the same pending request is idempotent; a different request
		// must wait for the current decision, rather than overwrite admin evidence.
		var previous []byte
		e = tx.QueryRow(ctx, `SELECT payload FROM doorstep.pro_change_requests WHERE pro_id=$1 AND kind=$2 AND status='pending'`, id, kind).Scan(&previous)
		if e == nil {
			var same bool
			if e = tx.QueryRow(ctx, `SELECT $1::jsonb=$2::jsonb`, previous, raw).Scan(&same); e != nil {
				return e
			}
			if !same {
				return ErrConflict
			}
		} else if e == pgx.ErrNoRows {
			if _, e = tx.Exec(ctx, `INSERT INTO doorstep.pro_change_requests(pro_id,kind,payload) VALUES($1,$2,$3)`, id, kind, raw); e != nil {
				return mapErr(e)
			}
		} else {
			return e
		}
	} else if err = applyPartnerChange(ctx, tx, id, kind, in); err != nil {
		return err
	}
	return mapErr(tx.Commit(ctx))
}

func applyPartnerChange(ctx context.Context, tx pgx.Tx, id uuid.UUID, kind string, in ChangePayload) error {
	switch kind {
	case "profile":
		if in.Profile == nil {
			return ErrInvalid
		}
		_, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET display_name=COALESCE($2,display_name),photo_media_id=COALESCE($3,photo_media_id),updated_at=now() WHERE id=$1`, id, in.Profile.DisplayName, in.Profile.PhotoMediaID)
		return mapErr(err)
	case "area":
		a := in.Area
		if a == nil || a.HomeLat == nil || a.HomeLng == nil {
			return ErrInvalid
		}
		// Resolve legacy applications only when the proposed point unambiguously
		// belongs to an enabled city. Approved edits run in a rolled-back probe
		// until the administrator accepts the request.
		if _, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET city_code=doorstep.coverage_city(ST_SetSRID(ST_MakePoint($2,$3),4326)::geography) WHERE id=$1 AND city_code IS NULL`, id, *a.HomeLng, *a.HomeLat); err != nil {
			return mapErr(err)
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM doorstep.zones z JOIN doorstep.professionals p ON p.city_code=z.city_code JOIN doorstep.cities c ON c.code=z.city_code WHERE p.id=$1 AND z.id=ANY($2::uuid[]) AND z.active AND c.active`, id, uuidStrings(a.ZoneIDs)).Scan(&n); err != nil {
			return err
		}
		if n != len(a.ZoneIDs) {
			return ErrBadReference
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET home_point=ST_SetSRID(ST_MakePoint($2,$3),4326)::geography,service_radius_m=$4,area_label=$5,updated_at=now() WHERE id=$1`, id, *a.HomeLng, *a.HomeLat, a.RadiusM, a.AreaLabel); err != nil {
			return mapErr(err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM doorstep.pro_zones WHERE pro_id=$1`, id); err != nil {
			return err
		}
		for _, z := range a.ZoneIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_zones(pro_id,zone_id) VALUES($1,$2)`, id, z); err != nil {
				return mapErr(err)
			}
		}
	case "skills":
		codes := []string{}
		for _, d := range in.Skills {
			codes = append(codes, d.Code)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM doorstep.pro_skills WHERE pro_id=$1 AND NOT(skill_code=ANY($2::text[]))`, id, codes); err != nil {
			return err
		}
		// New services still require the existing skill/eligibility review.
		if err := insertSkills(ctx, tx, id, in.Skills); err != nil {
			return mapErr(err)
		}
	case "hours":
		if _, err := tx.Exec(ctx, `DELETE FROM doorstep.pro_weekly_hours WHERE pro_id=$1`, id); err != nil {
			return err
		}
		for _, w := range in.Hours {
			if _, err := tx.Exec(ctx, `INSERT INTO doorstep.pro_weekly_hours(pro_id,weekday,start_time,end_time) VALUES($1,$2,$3::time,$4::time)`, id, w.Weekday, w.Start, w.End); err != nil {
				return mapErr(err)
			}
		}
	case "pan":
		if len(in.PANSealed) == 0 {
			return ErrInvalid
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET pan_sealed=$2,pan_last4=$3,on_duty=false,updated_at=now() WHERE id=$1`, id, in.PANSealed, in.PANLast4); err != nil {
			return mapErr(err)
		}
		// A replacement PAN must not inherit the previous identity clearance.
		if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_kyc_checks SET status='pending' WHERE pro_id=$1 AND kind='manual_identity' AND status='passed'`, id); err != nil {
			return err
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (s *Store) PartnerChanges(ctx context.Context, proID *uuid.UUID) ([]PartnerChange, error) {
	rows, err := s.db.Query(ctx, `SELECT id,pro_id,kind,payload-'pan_sealed',status,reason,created_at FROM doorstep.pro_change_requests WHERE ($1::uuid IS NULL OR pro_id=$1) AND (status='pending' OR ($1::uuid IS NOT NULL AND created_at>now()-interval '30 days')) ORDER BY created_at DESC,id LIMIT 200`, proID)
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (PartnerChange, error) {
		var v PartnerChange
		err := r.Scan(&v.ID, &v.ProID, &v.Kind, &v.Payload, &v.Status, &v.Reason, &v.CreatedAt)
		return v, err
	})
}

func (s *Store) DecidePartnerChange(ctx context.Context, a Actor, id uuid.UUID, approve bool, reason *string) error {
	return s.adminWrite(ctx, a, "professional.change_review", "pro_change", map[string]any{"approve": approve, "reason": reason}, func(tx pgx.Tx) (string, error) {
		var proID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT pro_id FROM doorstep.pro_change_requests WHERE id=$1`, id).Scan(&proID); err != nil {
			return "", mapErr(err)
		}
		var status string
		var userID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT status,user_id FROM doorstep.professionals WHERE id=$1 FOR UPDATE`, proID).Scan(&status, &userID); err != nil {
			return "", err
		}
		if approve && status != "approved" && status != "suspended" {
			return "", &TransitionError{Status: status}
		}
		var kind, requestStatus string
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT kind,status,payload FROM doorstep.pro_change_requests WHERE id=$1 FOR UPDATE`, id).Scan(&kind, &requestStatus, &raw); err != nil {
			return "", mapErr(err)
		}
		if requestStatus != "pending" {
			return "", &TransitionError{Status: requestStatus}
		}
		decision := "rejected"
		if approve {
			var in ChangePayload
			if err := json.Unmarshal(raw, &in); err != nil {
				return "", err
			}
			if err := applyPartnerChange(ctx, tx, proID, kind, in); err != nil {
				return "", err
			}
			if kind == "skills" {
				codes := []string{}
				for _, d := range in.Skills {
					codes = append(codes, d.Code)
				}
				rules, err := skillRules(ctx, tx, codes)
				if err != nil {
					return "", err
				}
				var gender string
				if err := tx.QueryRow(ctx, `SELECT COALESCE(gender,'') FROM doorstep.professionals WHERE id=$1`, proID).Scan(&gender); err != nil {
					return "", err
				}
				for _, code := range codes {
					rule := rules[code]
					if rule == nil {
						return "", ErrInvalid
					}
					for _, category := range rule.Categories {
						if !prokyc.GenderAllowed(category.GenderRule, gender) {
							return "", ErrInvalid
						}
					}
				}
				if _, err := tx.Exec(ctx, `UPDATE doorstep.pro_skills SET status='verified',verified_at=now(),verified_by=$2 WHERE pro_id=$1 AND skill_code=ANY($3::text[]) AND status<>'verified'`, proID, a.UserID, codes); err != nil {
					return "", err
				}
			}
			decision = "approved"
		}
		_, err := tx.Exec(ctx, `UPDATE doorstep.pro_change_requests SET status=$2,reason=$3,reviewed_at=now(),reviewed_by=$4 WHERE id=$1`, id, decision, reason, a.UserID)
		if err != nil {
			return "", err
		}
		if err := s.enqueueProEvent(ctx, tx, events.ProChangeReviewed, userID, events.ProChangeReviewedData{
			ProID: proID, ProUserID: userID, ChangeID: id, Kind: kind, Decision: decision,
		}); err != nil {
			return "", err
		}
		return id.String(), nil
	})
}
