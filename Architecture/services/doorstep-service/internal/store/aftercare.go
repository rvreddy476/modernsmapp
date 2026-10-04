package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *Store) RevokeVisitShare(ctx context.Context, id, u uuid.UUID, at time.Time, guard func(*VisitFacts) error) error {
	return s.visitTransaction(ctx, id, at, guard, func(tx pgx.Tx, f *VisitFacts) error {
		_, e := tx.Exec(ctx, `UPDATE doorstep.share_tokens SET revoked_at=$2 WHERE booking_id=$1 AND revoked_at IS NULL`, id, at)
		return e
	})
}

func (s *Store) visitTransaction(ctx context.Context, id uuid.UUID, at time.Time, guard func(*VisitFacts) error, write func(pgx.Tx, *VisitFacts) error) error {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	f, e := visitFactsTx(ctx, tx, id, at, true)
	if e != nil {
		return e
	}
	if e = guard(f); e != nil {
		return e
	}
	if e = write(tx, f); e != nil {
		return mapErr(e)
	}
	return mapErr(tx.Commit(ctx))
}
func (s *Store) RateVisit(ctx context.Context, id, u uuid.UUID, kind string, in model.RatingInput, at time.Time, guard func(*VisitFacts) error) (*model.Rating, error) {
	r := &model.Rating{ID: uuid.New(), BookingID: id, RaterKind: kind, Stars: *in.Stars, Tags: in.Tags, Comment: in.Comment, CreatedAt: at}
	if r.Tags == nil {
		r.Tags = []string{}
	}
	e := s.visitTransaction(ctx, id, at, guard, func(tx pgx.Tx, f *VisitFacts) error {
		target := f.Pro.UserID
		if kind == "pro" {
			target = f.Customer
		}
		if kind == "customer" {
			if e := requireRow(ctx, tx, `SELECT 1 FROM doorstep.professionals WHERE user_id=$1 FOR UPDATE`, target); e != nil {
				return e
			}
		}
		e := tx.QueryRow(ctx, `INSERT INTO doorstep.ratings(id,booking_id,rater_kind,rater_user_id,ratee_user_id,stars,tags,comment,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT(booking_id,rater_kind) DO NOTHING RETURNING id`, r.ID, id, kind, u, target, r.Stars, r.Tags, r.Comment, at).Scan(&r.ID)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		if kind == "customer" {
			if _, e = tx.Exec(ctx, `UPDATE doorstep.professionals SET rating_sum=(SELECT COALESCE(sum(stars),0) FROM doorstep.ratings WHERE ratee_user_id=$1 AND rater_kind='customer' AND NOT hidden), rating_count=(SELECT count(*) FROM doorstep.ratings WHERE ratee_user_id=$1 AND rater_kind='customer' AND NOT hidden),updated_at=$2 WHERE user_id=$1`, target, at); e != nil {
				return e
			}
		}
		core, e := bookingCoreTx(ctx, tx, id)
		if e != nil {
			return e
		}
		return s.enqueueBookingEvent(ctx, tx, events.BookingRated, core, at, events.BookingRatedData{BookingCore: core, RaterKind: kind, Stars: r.Stars})
	})
	return r, e
}

type scanner interface{ Scan(...any) error }

const messageCols = `id,booking_id,sender_kind,body,created_at,read_at`

func scanMessage(row scanner) (model.Message, error) {
	var m model.Message
	e := row.Scan(&m.ID, &m.BookingID, &m.SenderKind, &m.Body, &m.CreatedAt, &m.ReadAt)
	m.CreatedAt = m.CreatedAt.UTC()
	utcPtr(&m.ReadAt)
	return m, e
}
func conversationOpen(f *VisitFacts, at time.Time) bool {
	if f.Pro == nil {
		return false
	}
	if f.Status == "completed" {
		return f.CompletedAt != nil && at.Before(f.CompletedAt.Add(2*time.Hour))
	}
	return f.Pro.Status == "accepted" && contains([]string{"assigned", "en_route", "arrived", "in_progress", "awaiting_extras_payment"}, f.Status)
}
func (s *Store) VisitMessages(ctx context.Context, id, u uuid.UUID, kind string, cursor *uuid.UUID, at time.Time, guard func(*VisitFacts) error) (*model.MessagePage, error) {
	page := &model.MessagePage{Items: []model.Message{}}
	e := s.visitTransaction(ctx, id, at, guard, func(tx pgx.Tx, f *VisitFacts) error {
		page.Open = conversationOpen(f, at)
		if f.Pro == nil {
			return nil
		}
		var since time.Time
		var cursorID uuid.UUID
		if cursor != nil {
			if e := tx.QueryRow(ctx, `SELECT created_at,id FROM doorstep.messages WHERE id=$1 AND booking_id=$2 AND pro_id=$3`, *cursor, id, f.Pro.ProID).Scan(&since, &cursorID); e != nil {
				return e
			}
		}
		rows, e := tx.Query(ctx, `SELECT `+messageCols+` FROM doorstep.messages WHERE booking_id=$1 AND pro_id=$2 AND (created_at,id)>($3,$4) ORDER BY created_at,id LIMIT 51`, id, f.Pro.ProID, since, cursorID)
		if e != nil {
			return e
		}
		items, e := collect(rows, func(row pgx.Rows) (model.Message, error) { return scanMessage(row) })
		if e != nil {
			return e
		}
		if len(items) > 50 {
			next := items[49].ID.String()
			page.NextCursor = &next
			items = items[:50]
		}
		if items != nil {
			page.Items = items
		}
		return nil
	})
	return page, e
}
func (s *Store) SendVisitMessage(ctx context.Context, id, u uuid.UUID, kind, body string, at time.Time, guard func(*VisitFacts) error) (*model.Message, error) {
	m := &model.Message{ID: uuid.New(), BookingID: id, SenderKind: kind, Body: body, CreatedAt: at}
	e := s.visitTransaction(ctx, id, at, guard, func(tx pgx.Tx, f *VisitFacts) error {
		if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,81422007))`, u.String()); e != nil {
			return e
		}
		var n int
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM doorstep.messages WHERE sender_user_id=$1 AND created_at>$2`, u, at.Add(-time.Minute)).Scan(&n); e != nil {
			return e
		}
		if n >= 30 {
			return ErrConflict
		}
		if _, e := tx.Exec(ctx, `INSERT INTO doorstep.messages(id,booking_id,pro_id,sender_user_id,sender_kind,body,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, m.ID, id, f.Pro.ProID, u, kind, body, at); e != nil {
			return e
		}
		core, e := bookingCoreTx(ctx, tx, id)
		if e != nil {
			return e
		}
		return s.enqueueBookingEvent(ctx, tx, events.BookingMessageSent, core, at, events.BookingMessageSentData{BookingCore: core, MessageID: m.ID, SenderKind: kind})
	})
	return m, e
}
func (s *Store) ReadVisitMessage(ctx context.Context, id, u, msg uuid.UUID, at time.Time) error {
	return s.visitTransaction(ctx, id, at, func(f *VisitFacts) error {
		if f.Pro == nil || u != f.Customer && u != f.Pro.UserID {
			return ErrNotFound
		}
		return nil
	}, func(tx pgx.Tx, f *VisitFacts) error {
		tag, e := tx.Exec(ctx, `UPDATE doorstep.messages SET read_at=COALESCE(read_at,$4) WHERE id=$1 AND booking_id=$2 AND pro_id=$3 AND sender_user_id<>$5`, msg, id, f.Pro.ProID, at, u)
		if e == nil && tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return e
	})
}
func (s *Store) SaveVisitShare(ctx context.Context, id, u uuid.UUID, hash string, until, at time.Time, guard func(*VisitFacts) error) error {
	return s.visitTransaction(ctx, id, at, guard, func(tx pgx.Tx, f *VisitFacts) error {
		if _, e := tx.Exec(ctx, `UPDATE doorstep.share_tokens SET revoked_at=$2 WHERE booking_id=$1 AND revoked_at IS NULL`, id, at); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO doorstep.share_tokens(booking_id,created_by_user_id,token_hash,expires_at,created_at) VALUES($1,$2,$3,$4,$5)`, id, u, hash, until, at)
		return e
	})
}
func (s *Store) SharedVisit(ctx context.Context, hash string, at time.Time) (*model.SharedBookingView, error) {
	v := &model.SharedBookingView{}
	e := s.db.QueryRow(ctx, `SELECT b.status,b.locality,b.slot_start,split_part(p.display_name,' ',1) FROM doorstep.share_tokens t JOIN doorstep.bookings b ON b.id=t.booking_id LEFT JOIN doorstep.booking_assignments a ON a.booking_id=b.id AND a.status='accepted' LEFT JOIN doorstep.professionals p ON p.id=a.pro_id WHERE t.token_hash=$1 AND t.revoked_at IS NULL AND t.expires_at>$2 AND b.status NOT IN ('cancelled','expired','completed','customer_no_show','pro_no_show','pro_unavailable')`, hash, at).Scan(&v.Status, &v.Locality, &v.SlotStart, &v.ProfessionalFirstName)
	return v, mapErr(e)
}
func (s *Store) SaveTrustedContact(ctx context.Context, u uuid.UUID, name string, sealed []byte, last4 string, at time.Time) (*model.TrustedContact, error) {
	_, e := s.db.Exec(ctx, `INSERT INTO doorstep.trusted_contacts(user_id,name,phone_sealed,phone_last4,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$5) ON CONFLICT(user_id) DO UPDATE SET name=EXCLUDED.name,phone_sealed=EXCLUDED.phone_sealed,phone_last4=EXCLUDED.phone_last4,updated_at=EXCLUDED.updated_at`, u, name, sealed, last4, at)
	return &model.TrustedContact{Name: name, PhoneMasked: "••••••" + last4, UpdatedAt: at}, mapErr(e)
}
func (s *Store) TrustedContact(ctx context.Context, u uuid.UUID) (*model.TrustedContact, error) {
	v := &model.TrustedContact{}
	var last4 string
	e := s.db.QueryRow(ctx, `SELECT name,phone_last4,updated_at FROM doorstep.trusted_contacts WHERE user_id=$1`, u).Scan(&v.Name, &last4, &v.UpdatedAt)
	v.PhoneMasked = "••••••" + last4
	return v, mapErr(e)
}

const ticketCols = `id,booking_id,category,subject,body,status,created_at,updated_at`

func scanTicket(row scanner) (model.Ticket, error) {
	var v model.Ticket
	e := row.Scan(&v.ID, &v.BookingID, &v.Category, &v.Subject, &v.Body, &v.Status, &v.CreatedAt, &v.UpdatedAt)
	return v, e
}
func (s *Store) VisitTicket(ctx context.Context, id, u uuid.UUID) (*model.Ticket, error) {
	v, e := scanTicket(s.db.QueryRow(ctx, `SELECT `+ticketCols+` FROM doorstep.tickets WHERE id=$1 AND user_id=$2`, id, u))
	return &v, mapErr(e)
}
func (s *Store) VisitTickets(ctx context.Context, u uuid.UUID) ([]model.Ticket, error) {
	rows, e := s.db.Query(ctx, `SELECT `+ticketCols+` FROM doorstep.tickets WHERE user_id=$1 ORDER BY created_at DESC,id DESC LIMIT 100`, u)
	if e != nil {
		return nil, e
	}
	return collect(rows, func(row pgx.Rows) (model.Ticket, error) { return scanTicket(row) })
}
func (s *Store) OpenVisitTicket(ctx context.Context, u uuid.UUID, kind string, in model.TicketInput, at time.Time) (*model.Ticket, error) {
	tx, e := s.db.Begin(ctx)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback(ctx)
	if in.BookingID != nil {
		f, e := visitFactsTx(ctx, tx, *in.BookingID, at, true)
		if e != nil {
			return nil, e
		}
		if f.Customer != u {
			return nil, ErrNotFound
		}
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,81422008))`, u.String()); e != nil {
		return nil, e
	}
	var count int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM doorstep.tickets WHERE user_id=$1 AND created_at>$2`, u, at.Add(-time.Hour)).Scan(&count); e != nil {
		return nil, e
	}
	if count >= 5 {
		return nil, ErrConflict
	}
	v, e := scanTicket(tx.QueryRow(ctx, `INSERT INTO doorstep.tickets(user_id,user_kind,booking_id,category,subject,body,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7) RETURNING `+ticketCols, u, kind, in.BookingID, in.Category, strings.TrimSpace(*in.Subject), strings.TrimSpace(*in.Body), at))
	if e != nil {
		return nil, mapErr(e)
	}
	return &v, tx.Commit(ctx)
}
func (s *Store) VisitEarnings(ctx context.Context, u uuid.UUID, from, to time.Time) (*model.Earnings, error) {
	var pro uuid.UUID
	if e := s.db.QueryRow(ctx, `SELECT id FROM doorstep.professionals WHERE user_id=$1`, u).Scan(&pro); e != nil {
		return nil, mapErr(e)
	}
	rows, e := s.db.Query(ctx, `SELECT id,booking_id,kind,amount_paise,created_at FROM doorstep.earning_lines WHERE pro_id=$1 AND created_at >=$2 AND created_at<$3 ORDER BY created_at DESC,id DESC LIMIT 1000`, pro, from, to)
	if e != nil {
		return nil, e
	}
	lines, e := collect(rows, func(row pgx.Rows) (model.EarningLine, error) {
		var l model.EarningLine
		e := row.Scan(&l.ID, &l.BookingID, &l.Kind, &l.AmountPaise, &l.CreatedAt)
		return l, e
	})
	if e != nil {
		return nil, e
	}
	if lines == nil {
		lines = []model.EarningLine{}
	}
	v := &model.Earnings{Lines: lines}
	e = s.db.QueryRow(ctx, `SELECT COALESCE(sum(amount_paise),0)::bigint FROM doorstep.earning_lines WHERE pro_id=$1 AND created_at >=$2 AND created_at<$3`, pro, from, to).Scan(&v.TotalPaise)
	return v, e
}
