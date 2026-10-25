// Package partnerwork coordinates duty and reservations on one authoritative
// database. Domain services must use the same database (or a coordinator API),
// never independently install this schema in each domain database. A local
// domain transaction can call the Tx functions for atomic assignment; remote
// domains must retain the reservation until their outcome is reconciled.
package partnerwork

import (
	"context"
	"embed"
	"errors"
	"time"

	"github.com/atpost/shared/store/migrationrunner"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Category string

const (
	Moped            Category = "moped"
	HomeServices     Category = "home_services"
	CommerceDelivery Category = "commerce_delivery"
	FoodDelivery     Category = "food_delivery"
)

var (
	ErrInvalid  = errors.New("partner work: invalid request")
	ErrStale    = errors.New("partner work: state changed; refresh and try again")
	ErrBusy     = errors.New("partner work: finish current work before switching")
	ErrConflict = errors.New("partner work: conflicts with existing work")
	ErrOffDuty  = errors.New("partner work: category is not on duty")
	ErrExpired  = errors.New("partner work: offer expired")
	ErrNotFound = errors.New("partner work: reservation not found")
)

func (c Category) Valid() bool {
	return c == Moped || c == HomeServices || c == CommerceDelivery || c == FoodDelivery
}

//go:embed migrations/*.sql
var migrations embed.FS

func Migrate(ctx context.Context, db *pgxpool.Pool) error {
	return migrationrunner.Run(ctx, db, "partner-work", migrations, "migrations")
}

type Session struct {
	UserID    uuid.UUID `json:"-"`
	Category  *Category `json:"category"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Reservation struct {
	ID             uuid.UUID `json:"id"`
	Category       Category  `json:"category"`
	JobID          uuid.UUID `json:"job_id"`
	StartsAt       time.Time `json:"starts_at"`
	EndsAt         time.Time `json:"ends_at"`
	State          string    `json:"state"`
	OfferExpiresAt time.Time `json:"offer_expires_at"`
}

// LockTx is always acquired before domain job/offer/profile locks. Callers
// must use READ COMMITTED, and hold this lock through the domain commit.
func LockTx(ctx context.Context, tx pgx.Tx, user uuid.UUID) (Session, error) {
	if user == uuid.Nil {
		return Session{}, ErrInvalid
	}
	if _, err := tx.Exec(ctx, `INSERT INTO partner_work.sessions(user_id) VALUES($1) ON CONFLICT DO NOTHING`, user); err != nil {
		return Session{}, err
	}
	var s Session
	err := tx.QueryRow(ctx, `SELECT user_id,category,revision,updated_at FROM partner_work.sessions WHERE user_id=$1 FOR UPDATE`, user).Scan(&s.UserID, &s.Category, &s.Revision, &s.UpdatedAt)
	return s, err
}

func Read(ctx context.Context, db *pgxpool.Pool, user uuid.UUID) (Session, []Reservation, error) {
	if user == uuid.Nil {
		return Session{}, nil, ErrInvalid
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Session{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	s := Session{UserID: user}
	err = tx.QueryRow(ctx, `SELECT category,revision,updated_at FROM partner_work.sessions WHERE user_id=$1`, user).Scan(&s.Category, &s.Revision, &s.UpdatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Session{}, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id,category,job_id,starts_at,ends_at,state,offer_expires_at FROM partner_work.reservations
 WHERE user_id=$1 AND (state IN ('reserved','active') OR (state='offered' AND offer_expires_at>clock_timestamp())) ORDER BY starts_at,id`, user)
	if err != nil {
		return Session{}, nil, err
	}
	defer rows.Close()
	rs := []Reservation{}
	for rows.Next() {
		var r Reservation
		if err = rows.Scan(&r.ID, &r.Category, &r.JobID, &r.StartsAt, &r.EndsAt, &r.State, &r.OfferExpiresAt); err != nil {
			return Session{}, nil, err
		}
		rs = append(rs, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return Session{}, nil, err
	}
	return s, rs, tx.Commit(ctx)
}

func eventTx(ctx context.Context, tx pgx.Tx, user uuid.UUID, typ string, id *uuid.UUID) error {
	var rev int64
	if err := tx.QueryRow(ctx, `UPDATE partner_work.sessions SET revision=revision+1,updated_at=clock_timestamp() WHERE user_id=$1 RETURNING revision`, user).Scan(&rev); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO partner_work.events(user_id,revision,event_type,reservation_id) VALUES($1,$2,$3,$4)`, user, rev, typ, id)
	return err
}

// SetDutyTx rejects stale switches from another device. Future Home Services
// reservations survive switching; a started or due job cannot be abandoned.
// Approval and coverage are domain preconditions, checked inside the caller's
// transaction; this function does not grant approval to a registration.
func SetDutyTx(ctx context.Context, tx pgx.Tx, user uuid.UUID, category *Category, expected int64) error {
	if category != nil && !category.Valid() {
		return ErrInvalid
	}
	s, err := LockTx(ctx, tx, user)
	if err != nil {
		return err
	}
	if s.Revision != expected {
		return ErrStale
	}
	if (s.Category == nil && category == nil) || (s.Category != nil && category != nil && *s.Category == *category) {
		return nil
	}
	var busy bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM partner_work.reservations WHERE user_id=$1 AND
  (state='active' OR (state='reserved' AND starts_at<=clock_timestamp() AND ($2::text IS NULL OR category<>$2))))`, user, category).Scan(&busy); err != nil {
		return err
	}
	if busy {
		return ErrBusy
	}
	// Withdraw unaccepted offers on a category change; an old push cannot
	// subsequently accept work from a category the partner has left.
	if _, err = tx.Exec(ctx, `UPDATE partner_work.reservations SET state='released',updated_at=clock_timestamp() WHERE user_id=$1 AND state='offered'`, user); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE partner_work.sessions SET category=$2 WHERE user_id=$1`, user, category); err != nil {
		return err
	}
	return eventTx(ctx, tx, user, "duty.changed", nil)
}

// OfferTx reserves the time window before notifying the partner. Job IDs and
// time bounds must come from trusted domain data, never a mobile payload.
// A duplicate retry returns the original reservation without extending TTL.
func OfferTx(ctx context.Context, tx pgx.Tx, user uuid.UUID, c Category, job uuid.UUID, start, end time.Time) (Reservation, error) {
	if !c.Valid() || job == uuid.Nil || !end.After(start) {
		return Reservation{}, ErrInvalid
	}
	s, err := LockTx(ctx, tx, user)
	if err != nil {
		return Reservation{}, err
	}
	if s.Category == nil || *s.Category != c {
		return Reservation{}, ErrOffDuty
	}
	var old Reservation
	err = tx.QueryRow(ctx, `SELECT id,category,job_id,starts_at,ends_at,state,offer_expires_at FROM partner_work.reservations WHERE user_id=$1 AND category=$2 AND job_id=$3`, user, c, job).Scan(&old.ID, &old.Category, &old.JobID, &old.StartsAt, &old.EndsAt, &old.State, &old.OfferExpiresAt)
	if err == nil {
		return old, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, err
	}
	var conflict bool
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM partner_work.reservations WHERE user_id=$1 AND state='offered' AND offer_expires_at>clock_timestamp())>=3 OR EXISTS(SELECT 1 FROM partner_work.reservations WHERE user_id=$1 AND state IN ('reserved','active'))`, user).Scan(&conflict); err != nil {
		return Reservation{}, err
	}
	if conflict {
		return Reservation{}, ErrConflict
	}
	r := Reservation{ID: uuid.New(), Category: c, JobID: job, StartsAt: start, EndsAt: end, State: "offered"}
	if err = tx.QueryRow(ctx, `INSERT INTO partner_work.reservations(id,user_id,category,job_id,starts_at,ends_at,state,offer_expires_at)
 VALUES($1,$2,$3,$4,$5,$6,'offered',clock_timestamp()+interval '20 seconds') RETURNING offer_expires_at`, r.ID, user, c, job, start, end).Scan(&r.OfferExpiresAt); err != nil {
		return Reservation{}, err
	}
	return r, eventTx(ctx, tx, user, "work.offered", &r.ID)
}

// DecideTx never releases accepted work implicitly or by elapsed time. Only a
// trusted domain terminal transition may request release; accepted work is
// reserved until its actual start, then remains active through overruns.
func DecideTx(ctx context.Context, tx pgx.Tx, user uuid.UUID, c Category, id uuid.UUID, action string) error {
	if !c.Valid() {
		return ErrInvalid
	}
	s, err := LockTx(ctx, tx, user)
	if err != nil {
		return err
	}
	var state string
	var expired bool
	var start time.Time
	err = tx.QueryRow(ctx, `SELECT state,offer_expires_at<=clock_timestamp(),starts_at FROM partner_work.reservations WHERE id=$1 AND user_id=$2 AND category=$3`, id, user, c).Scan(&state, &expired, &start)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	next := ""
	switch action {
	case "accept":
		if state == "reserved" || state == "active" {
			return nil
		}
		if state != "offered" {
			return ErrConflict
		}
		if expired {
			return ErrExpired
		}
		if s.Category == nil || *s.Category != c {
			return ErrOffDuty
		}
		var conflict bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM partner_work.reservations r JOIN partner_work.reservations target ON target.id=$2
 WHERE r.user_id=$1 AND r.id<>target.id AND (r.state='active' OR (r.state='reserved' AND
 (r.starts_at<=clock_timestamp() OR tstzrange(r.starts_at,r.ends_at,'[)') && tstzrange(target.starts_at,target.ends_at,'[)')))))`, user, id).Scan(&conflict); err != nil {
			return err
		}
		if conflict {
			return ErrConflict
		}
		// The user chooses one of the pending offers. Other choices are withdrawn atomically.
		if _, err = tx.Exec(ctx, `UPDATE partner_work.reservations SET state='released',updated_at=clock_timestamp() WHERE user_id=$1 AND id<>$2 AND state='offered'`, user, id); err != nil {
			return err
		}
		next = "reserved"
	case "start":
		if state == "active" {
			return nil
		}
		if state != "reserved" {
			return ErrConflict
		}
		if s.Category == nil || *s.Category != c {
			return ErrOffDuty
		}
		var busy bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM partner_work.reservations WHERE user_id=$1 AND id<>$2 AND
   (state='active' OR (state='reserved' AND starts_at<=clock_timestamp())))`, user, id).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrConflict
		}
		next = "active"
	case "release":
		if state == "released" {
			return nil
		}
		next = "released"
	default:
		return ErrInvalid
	}
	if _, err = tx.Exec(ctx, `UPDATE partner_work.reservations SET state=$2,updated_at=clock_timestamp() WHERE id=$1`, id, next); err != nil {
		return err
	}
	return eventTx(ctx, tx, user, "work."+next, &id)
}
