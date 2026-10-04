package store

import (
	"context"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

const incidentCols = `id,booking_id,raised_by_kind,kind,severity,status,description,pro_auto_suspended,created_at`
const ratingCols = `id,booking_id,rater_kind,stars,tags,comment,hidden,created_at`

func scanIncident(row scanner) (model.Incident, error) {
	var v model.Incident
	e := row.Scan(&v.ID, &v.BookingID, &v.RaisedByKind, &v.Kind, &v.Severity, &v.Status, &v.Description, &v.ProAutoSuspended, &v.CreatedAt)
	return v, e
}
func scanRating(row scanner) (model.Rating, error) {
	var v model.Rating
	e := row.Scan(&v.ID, &v.BookingID, &v.RaterKind, &v.Stars, &v.Tags, &v.Comment, &v.Hidden, &v.CreatedAt)
	return v, e
}

type Settlement struct {
	ID          uuid.UUID `json:"id"`
	ProID       uuid.UUID `json:"pro_id"`
	PeriodStart string    `json:"period_start"`
	PeriodEnd   string    `json:"period_end"`
	Gross       int64     `json:"gross_paise"`
	Commission  int64     `json:"commission_paise"`
	Tax         int64     `json:"tax_paise"`
	Net         int64     `json:"net_paise"`
	Status      string    `json:"status"`
}

func (s *Store) AdminCareList(ctx context.Context, kind, status, period string) ([]any, error) {
	out := []any{}
	var rows pgx.Rows
	var e error
	switch kind {
	case "incidents":
		rows, e = s.db.Query(ctx, `SELECT `+incidentCols+` FROM doorstep.incidents WHERE ($1='' OR status=$1) ORDER BY status='resolved',created_at DESC,id DESC LIMIT 100`, status)
	case "tickets":
		rows, e = s.db.Query(ctx, `SELECT `+ticketCols+` FROM doorstep.tickets WHERE ($1='' OR status=$1) ORDER BY status IN ('resolved','closed'),created_at DESC,id DESC LIMIT 100`, status)
	case "ratings":
		rows, e = s.db.Query(ctx, `SELECT `+ratingCols+` FROM doorstep.ratings ORDER BY hidden,stars,created_at DESC,id DESC LIMIT 100`)
	case "settlements":
		rows, e = s.db.Query(ctx, `SELECT id,pro_id,period_start::text,period_end::text,gross_paise,commission_paise,tax_paise,net_paise,status FROM doorstep.settlements WHERE ($1='' OR period_start=NULLIF($1,'')::date) AND ($2='' OR status=$2) ORDER BY period_start DESC,id DESC LIMIT 100`, period, status)
	default:
		return nil, ErrInvalid
	}
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var v any
		switch kind {
		case "incidents":
			v, e = scanIncident(rows)
		case "tickets":
			v, e = scanTicket(rows)
		case "ratings":
			v, e = scanRating(rows)
		case "settlements":
			var x Settlement
			e = rows.Scan(&x.ID, &x.ProID, &x.PeriodStart, &x.PeriodEnd, &x.Gross, &x.Commission, &x.Tax, &x.Net, &x.Status)
			v = x
		}
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) AdminIncidentDecision(ctx context.Context, a Actor, id uuid.UUID, status string, resolution *string, lift bool, at time.Time) (*model.Incident, error) {
	var out model.Incident
	e := s.adminWrite(ctx, a, "incident."+status, "incident", map[string]any{"resolution": resolution, "lift_suspension": lift}, func(tx pgx.Tx) (string, error) {
		// Serialize decisions for a professional before locking the incident.
		var pro *uuid.UUID
		if e := tx.QueryRow(ctx, `SELECT pro_id FROM doorstep.incidents WHERE id=$1`, id).Scan(&pro); e != nil {
			return "", e
		}
		if pro != nil {
			if e := requireRow(ctx, tx, `SELECT 1 FROM doorstep.professionals WHERE id=$1 FOR UPDATE`, *pro); e != nil {
				return "", e
			}
		}
		v, e := scanIncident(tx.QueryRow(ctx, `SELECT `+incidentCols+` FROM doorstep.incidents WHERE id=$1 FOR UPDATE`, id))
		if e != nil {
			return "", e
		}
		if v.Status == "resolved" || status == "acknowledged" && v.Status != "open" {
			return "", ErrStale
		}
		if lift {
			if status != "resolved" || !v.ProAutoSuspended || pro == nil {
				return "", ErrStale
			}
			var others int
			if e = tx.QueryRow(ctx, `SELECT count(*) FROM doorstep.incidents WHERE pro_id=$1 AND id<>$2 AND pro_auto_suspended AND status<>'resolved'`, *pro, id).Scan(&others); e != nil {
				return "", e
			}
			if others > 0 {
				return "", ErrConflict
			}
			var user uuid.UUID
			if e = tx.QueryRow(ctx, `UPDATE doorstep.professionals SET status='approved',incident_suspended=false,status_reason=NULL,updated_at=$2 WHERE id=$1 AND status='suspended' AND incident_suspended RETURNING user_id`, *pro, at).Scan(&user); e != nil {
				return "", e
			}
			if e = s.enqueueRoleForStatusTx(ctx, tx, user, "approved", "safety incident resolved by admin"); e != nil {
				return "", e
			}
			if e = s.enqueueProEvent(ctx, tx, events.ProStatusChanged, user, events.ProStatusChangedData{ProID: *pro, ProUserID: user, FromStatus: "suspended", ToStatus: "approved"}); e != nil {
				return "", e
			}
		}
		out, e = scanIncident(tx.QueryRow(ctx, `UPDATE doorstep.incidents SET status=$2,acknowledged_by=CASE WHEN $2='acknowledged' THEN $3 ELSE acknowledged_by END,acknowledged_at=CASE WHEN $2='acknowledged' THEN $4 ELSE acknowledged_at END,resolved_by=CASE WHEN $2='resolved' THEN $3 ELSE resolved_by END,resolved_at=CASE WHEN $2='resolved' THEN $4 ELSE resolved_at END,resolution=COALESCE($5,resolution),updated_at=$4 WHERE id=$1 RETURNING `+incidentCols, id, status, a.UserID, at, resolution))
		return id.String(), e
	})
	return &out, e
}
func (s *Store) AdminTicketStatus(ctx context.Context, a Actor, id uuid.UUID, status string, note *string, at time.Time) (*model.Ticket, error) {
	var out model.Ticket
	e := s.adminWrite(ctx, a, "ticket.status", "ticket", map[string]any{"status": status, "note": note}, func(tx pgx.Tx) (string, error) {
		var previous string
		if e := tx.QueryRow(ctx, `SELECT status FROM doorstep.tickets WHERE id=$1 FOR UPDATE`, id).Scan(&previous); e != nil {
			return "", e
		}
		if previous == "closed" && status != "closed" {
			return "", ErrStale
		}
		var e error
		out, e = scanTicket(tx.QueryRow(ctx, `UPDATE doorstep.tickets SET status=$2,updated_at=$3 WHERE id=$1 RETURNING `+ticketCols, id, status, at))
		return id.String(), e
	})
	return &out, e
}
func (s *Store) AdminHideVisitRating(ctx context.Context, a Actor, id uuid.UUID, reason string, at time.Time) (*model.Rating, error) {
	var out model.Rating
	e := s.adminWrite(ctx, a, "rating.hide", "rating", map[string]any{"reason": reason}, func(tx pgx.Tx) (string, error) {
		var user uuid.UUID
		if e := tx.QueryRow(ctx, `SELECT ratee_user_id FROM doorstep.ratings WHERE id=$1`, id).Scan(&user); e != nil {
			return "", e
		}
		// Same lock as the rating aggregation write, so another rating cannot
		// leave a stale average after this moderation decision.
		if _, e := tx.Exec(ctx, `SELECT 1 FROM doorstep.professionals WHERE user_id=$1 FOR UPDATE`, user); e != nil {
			return "", e
		}
		var e error
		out, e = scanRating(tx.QueryRow(ctx, `UPDATE doorstep.ratings SET hidden=true,moderated_by=$2,moderated_at=$3 WHERE id=$1 RETURNING `+ratingCols, id, a.UserID, at))
		if e != nil {
			return "", e
		}
		_, e = tx.Exec(ctx, `UPDATE doorstep.professionals SET rating_sum=(SELECT COALESCE(sum(stars),0) FROM doorstep.ratings WHERE ratee_user_id=$1 AND rater_kind='customer' AND NOT hidden),rating_count=(SELECT count(*) FROM doorstep.ratings WHERE ratee_user_id=$1 AND rater_kind='customer' AND NOT hidden),updated_at=$2 WHERE user_id=$1`, user, at)
		return id.String(), e
	})
	return &out, e
}

// Compute only: no bank lookup, payments client, payout endpoint or paid status.
// One closed IST day per professional; lines are attached atomically and a
// repeat adds only genuinely new lines, never pays or re-counts old lines.
func (s *Store) ComputeVisitSettlements(ctx context.Context, at time.Time) (int, error) {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(81422006)`); e != nil {
		return 0, e
	}
	tag, e := tx.Exec(ctx, `WITH due AS (
 SELECT pro_id,(created_at AT TIME ZONE 'Asia/Kolkata')::date AS day FROM doorstep.earning_lines WHERE settlement_id IS NULL AND (created_at AT TIME ZONE 'Asia/Kolkata')::date < ($1::timestamptz AT TIME ZONE 'Asia/Kolkata')::date GROUP BY pro_id,day)
 INSERT INTO doorstep.settlements(pro_id,period_start,period_end,status) SELECT pro_id,day,day,'computed' FROM due ON CONFLICT(pro_id,period_start,period_end) DO NOTHING`, at)
	if e != nil {
		return 0, e
	}
	rows, e := tx.Query(ctx, `UPDATE doorstep.earning_lines l SET settlement_id=s.id FROM doorstep.settlements s WHERE l.settlement_id IS NULL AND s.status='computed' AND s.pro_id=l.pro_id AND (l.created_at AT TIME ZONE 'Asia/Kolkata')::date=s.period_start AND s.period_start=s.period_end RETURNING s.id`)
	if e != nil {
		return 0, e
	}
	changed := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return 0, e
		}
		changed[id] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, e
	}
	if _, e = tx.Exec(ctx, `UPDATE doorstep.settlements s SET gross_paise=t.gross,commission_paise=t.commission,net_paise=t.net,updated_at=$1 FROM (SELECT settlement_id,COALESCE(sum(amount_paise)FILTER(WHERE kind NOT IN ('commission','penalty')),0) AS gross,-COALESCE(sum(amount_paise)FILTER(WHERE kind='commission'),0) AS commission,sum(amount_paise) AS net FROM doorstep.earning_lines GROUP BY settlement_id)t WHERE s.id=t.settlement_id AND s.status='computed'`, at); e != nil {
		return 0, e
	}
	for id := range changed {
		data := events.ProSettlementComputedData{SettlementID: id}
		if e = tx.QueryRow(ctx, `SELECT s.pro_id,p.user_id,s.net_paise,s.period_start::text,s.period_end::text FROM doorstep.settlements s JOIN doorstep.professionals p ON p.id=s.pro_id WHERE s.id=$1`, id).Scan(&data.ProID, &data.ProUserID, &data.NetPaise, &data.PeriodStart, &data.PeriodEnd); e != nil {
			return 0, e
		}
		if e = s.enqueueProEvent(ctx, tx, events.ProSettlementComputed, data.ProUserID, data); e != nil {
			return 0, e
		}
	}
	return int(tag.RowsAffected()), tx.Commit(ctx)
}
