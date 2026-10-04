package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The visit (A5): en route, arrived, the start OTP with the before photos,
// finish, the end OTP with the after photos, the customer's no-show. Every
// step locks the booking row first (the same order as dispatch and cancel),
// re-reads the facts the rule needs (VisitFacts), lets the service decide
// on that row, then writes the status, its timestamp, history and the
// doorstep.booking.* event in one transaction. The database's visit gate
// (migration 006) refuses in_progress and completed without the OTP and the
// photos whatever code path writes.

// VisitPro is the professional who holds the job (the lead assignment,
// accepted or completed).
type VisitPro struct {
	AssignmentID  uuid.UUID
	ProID         uuid.UUID
	UserID        uuid.UUID
	Status        string
	AccountStatus string
	DisplayName   string
	GSTIN         *string
}

// VisitFacts is one booking as the visit rules read it.
type VisitFacts struct {
	BookingID       uuid.UUID
	Customer        uuid.UUID
	Status          string
	Version         int
	CityCode        string
	CategorySlug    string
	Family          string
	ExtrasPolicy    string
	StateCode       string
	CategoryID      uuid.UUID
	ServiceID       uuid.UUID
	Lat, Lng        float64
	Locality        string
	SlotStart       time.Time
	SlotEnd         time.Time
	ArrivedAt       *time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
	CompletedAt     *time.Time
	MinBefore       int
	MinAfter        int
	ReworkDays      int
	ChargeNowPaise  int64
	GraceMinutes    int
	ParentBookingID *uuid.UUID
	TotalPaise      int64
	TaxablePaise    int64
	ExtrasTotal     int64
	PaidPaise       int64
	Refundable      int64
	ReservedProID   *uuid.UUID
	CommissionBPS   int
	DurationMinutes int
	// Pro is the lead assignment (accepted or completed); nil when nobody
	// holds the job.
	Pro *VisitPro
	// Photos are the holding professional's photos, per phase.
	Photos model.PhotoCounts
	// ProposedExtras are extras waiting for the customer; ApprovedUnbilled is
	// what the customer approved that no bill carries yet.
	ProposedExtras   int
	ApprovedUnbilled int64
	// UnpaidBills are visit-extras bills open or waiting for payment;
	// OutstandingBills are bills that became outstanding.
	UnpaidBills      int
	OutstandingBills int
}

// Holds reports whether proID holds the job (accepted assignment).
func (f *VisitFacts) Holds(proID uuid.UUID) bool {
	return f.Pro != nil && f.Pro.ProID == proID && f.Pro.Status == "accepted"
}

// HeldBy reports whether proID holds or completed the job.
func (f *VisitFacts) HeldBy(proID uuid.UUID) bool {
	return f.Pro != nil && f.Pro.ProID == proID
}

func visitFactsTx(ctx context.Context, q querier, id uuid.UUID, at time.Time, lock bool) (*VisitFacts, error) {
	f := &VisitFacts{}
	sql := `
		SELECT b.id, b.customer_user_id, b.status, b.version, b.city_code, c.slug, c.family, c.extras_policy, ci.state_code,
		       b.category_id, b.service_id, ST_Y(b.location::geometry), ST_X(b.location::geometry), b.locality,
		       b.slot_start, b.slot_end, b.arrived_at, b.started_at, b.finished_at, b.completed_at,
		       s.min_before_photos, s.min_after_photos, s.rework_days, ci.extras_charge_now_threshold_paise, ci.extras_grace_minutes,
		       b.parent_booking_id, b.total_paise, b.taxable_paise, b.extras_total_paise, b.paid_paise, b.reserved_pro_id,
		       COALESCE((SELECT sum(r.amount_paise) FROM doorstep.refunds r WHERE r.booking_id = b.id AND r.status <> 'failed'), 0)::bigint,
		       COALESCE((SELECT r.commission_bps FROM doorstep.commission_rules r
		                  WHERE r.city_code = b.city_code AND r.active AND (r.category_id = b.category_id OR r.category_id IS NULL)
		                    AND r.effective_from <= $2 AND (r.effective_to IS NULL OR r.effective_to > $2)
		                  ORDER BY r.category_id IS NULL, r.effective_from DESC LIMIT 1), 0),
		       b.duration_minutes
		FROM doorstep.bookings b
		JOIN doorstep.services s ON s.id = b.service_id
		JOIN doorstep.categories c ON c.id = b.category_id
		JOIN doorstep.cities ci ON ci.code = b.city_code
		WHERE b.id = $1`
	if lock {
		sql += ` FOR UPDATE OF b`
	}
	var refunded int64
	err := q.QueryRow(ctx, sql, id, at).Scan(&f.BookingID, &f.Customer, &f.Status, &f.Version, &f.CityCode, &f.CategorySlug, &f.Family,
		&f.ExtrasPolicy, &f.StateCode, &f.CategoryID, &f.ServiceID, &f.Lat, &f.Lng, &f.Locality, &f.SlotStart, &f.SlotEnd,
		&f.ArrivedAt, &f.StartedAt, &f.FinishedAt, &f.CompletedAt, &f.MinBefore, &f.MinAfter, &f.ReworkDays, &f.ChargeNowPaise,
		&f.GraceMinutes, &f.ParentBookingID, &f.TotalPaise, &f.TaxablePaise, &f.ExtrasTotal, &f.PaidPaise, &f.ReservedProID,
		&refunded, &f.CommissionBPS, &f.DurationMinutes)
	if err != nil {
		return nil, mapErr(err)
	}
	f.SlotStart, f.SlotEnd = f.SlotStart.UTC(), f.SlotEnd.UTC()
	for _, t := range []**time.Time{&f.ArrivedAt, &f.StartedAt, &f.FinishedAt, &f.CompletedAt} {
		utcPtr(t)
	}
	f.Refundable = max(f.PaidPaise-refunded, 0)
	var p VisitPro
	err = q.QueryRow(ctx, `
		SELECT a.id, a.pro_id, p.user_id, a.status, p.status, p.display_name, p.gstin
		FROM doorstep.booking_assignments a JOIN doorstep.professionals p ON p.id = a.pro_id
		WHERE a.booking_id = $1 AND a.role = 'lead' AND a.status IN ('accepted', 'completed')
		ORDER BY a.updated_at DESC, a.created_at DESC LIMIT 1`, id).Scan(&p.AssignmentID, &p.ProID, &p.UserID, &p.Status, &p.AccountStatus, &p.DisplayName, &p.GSTIN)
	switch {
	case err == nil:
		f.Pro = &p
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, err
	}
	var pro *uuid.UUID
	if f.Pro != nil {
		pro = &f.Pro.ProID
	}
	err = q.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM doorstep.booking_photos WHERE booking_id = $1 AND pro_id = $2 AND phase = 'before')::int,
		  (SELECT count(*) FROM doorstep.booking_photos WHERE booking_id = $1 AND pro_id = $2 AND phase = 'after')::int,
		  (SELECT count(*) FROM doorstep.booking_photos WHERE booking_id = $1 AND pro_id = $2 AND phase = 'kit_seal')::int,
		  (SELECT count(*) FROM doorstep.booking_extras WHERE booking_id = $1 AND status = 'proposed')::int,
		  (SELECT COALESCE(sum(total_paise), 0) FROM doorstep.booking_extras WHERE booking_id = $1 AND status = 'approved')::bigint,
		  (SELECT count(*) FROM doorstep.extras_bills WHERE booking_id = $1 AND kind = 'visit_extras'
		     AND status IN ('open', 'payment_pending'))::int,
		  (SELECT count(*) FROM doorstep.extras_bills WHERE booking_id = $1 AND kind = 'visit_extras' AND status = 'outstanding')::int`,
		id, pro).Scan(&f.Photos.Before, &f.Photos.After, &f.Photos.KitSeal, &f.ProposedExtras, &f.ApprovedUnbilled, &f.UnpaidBills,
		&f.OutstandingBills)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// VisitFacts reads one booking for the visit rules (no lock).
func (s *Store) VisitFacts(ctx context.Context, id uuid.UUID, at time.Time) (*VisitFacts, error) {
	return visitFactsTx(ctx, s.db, id, at, false)
}

// visitStamps are the booking columns a step may stamp.
var visitStamps = map[string]string{
	"en_route": "en_route_at", "arrived": "arrived_at", "in_progress": "started_at", "completed": "completed_at",
}

// StepInput is one status step of the visit.
type StepInput struct {
	BookingID uuid.UUID
	ProUser   uuid.UUID
	To        string // en_route | arrived
	Fix       *Fix   // arrived: the professional's fix is recorded
	ProID     uuid.UUID
	// EtaMinutes goes on doorstep.booking.en_route.
	EtaMinutes *int
	At         time.Time
}

// MoveVisit moves a booking assigned -> en_route or en_route -> arrived for
// its professional: decide sees the locked row (ownership, status, the
// geo check). History, the stamp and the event commit together.
func (s *Store) MoveVisit(ctx context.Context, in StepInput, decide func(*VisitFacts) error) (*VisitFacts, error) {
	stamp, ok := visitStamps[in.To]
	if !ok || (in.To != "en_route" && in.To != "arrived") {
		return nil, fmt.Errorf("store: MoveVisit to %q", in.To)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, in.BookingID, in.At, true)
	if err != nil {
		return nil, err
	}
	if err := decide(f); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = $2, `+stamp+` = $3, version = version + 1, updated_at = $3
		WHERE id = $1`, in.BookingID, in.To, in.At); err != nil {
		return nil, mapErr(err)
	}
	from := f.Status
	if err := historyTx(ctx, tx, in.BookingID, &from, in.To, "pro", &in.ProUser, nil, in.At); err != nil {
		return nil, err
	}
	if in.Fix != nil {
		if err := recordFixTx(ctx, tx, f.Pro.ProID, *in.Fix, in.At); err != nil {
			return nil, err
		}
	}
	core, err := bookingCoreTx(ctx, tx, in.BookingID)
	if err != nil {
		return nil, err
	}
	var evType string
	var data any = core
	if in.To == "en_route" {
		evType, data = events.BookingEnRoute, events.BookingEnRouteData{BookingCore: core, EtaMinutes: in.EtaMinutes}
	} else {
		evType = events.BookingArrived
	}
	if err := s.enqueueBookingEvent(ctx, tx, evType, core, in.At, data); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	f.Status = in.To
	return f, nil
}

// ---------------------------------------------------------------- photos

// PhotoWrite is one visit photo.
type PhotoWrite struct {
	ID        uuid.UUID
	BookingID uuid.UUID
	ProID     uuid.UUID
	Phase     string
	MediaID   string
	Lat, Lng  *float64
	At        time.Time
}

// ErrPhotoTaken: the media id is already a photo of this booking under
// another professional or phase.
var ErrPhotoTaken = errors.New("store: this media is already another photo of the booking")

// AddPhoto records a visit photo for the job's professional (decide sees
// the locked row). The same media again is the same photo (idempotent).
func (s *Store) AddPhoto(ctx context.Context, in PhotoWrite, decide func(*VisitFacts) error) (*model.Photo, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, in.BookingID, in.At, true)
	if err != nil {
		return nil, err
	}
	if err := decide(f); err != nil {
		return nil, err
	}
	p, err := addPhotoTx(ctx, tx, in)
	if err != nil {
		return nil, err
	}
	return p, mapErr(tx.Commit(ctx))
}

func addPhotoTx(ctx context.Context, tx pgx.Tx, in PhotoWrite) (*model.Photo, error) {
	var p model.Photo
	err := tx.QueryRow(ctx, `
		INSERT INTO doorstep.booking_photos (id, booking_id, pro_id, phase, media_id, lat, lng, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (booking_id, media_id) DO NOTHING
		RETURNING id, booking_id, phase, media_id, created_at`, in.ID, in.BookingID, in.ProID, in.Phase, in.MediaID, in.Lat, in.Lng, in.At).
		Scan(&p.ID, &p.BookingID, &p.Phase, &p.MediaID, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var pro uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT id, booking_id, phase, media_id, created_at, pro_id FROM doorstep.booking_photos
			WHERE booking_id = $1 AND media_id = $2`, in.BookingID, in.MediaID).Scan(&p.ID, &p.BookingID, &p.Phase, &p.MediaID,
			&p.CreatedAt, &pro); err != nil {
			return nil, mapErr(err)
		}
		if pro != in.ProID || p.Phase != in.Phase {
			return nil, ErrPhotoTaken
		}
	} else if err != nil {
		return nil, mapErr(err)
	}
	p.CreatedAt = p.CreatedAt.UTC()
	return &p, nil
}

// PhotoRef is a booking's photo found by media id.
type PhotoRef struct {
	ID    uuid.UUID
	Phase string
	ProID uuid.UUID
}

// PhotoByMedia finds a photo of the booking by its media id.
func (s *Store) PhotoByMedia(ctx context.Context, bookingID uuid.UUID, mediaID string) (*PhotoRef, error) {
	var r PhotoRef
	if err := s.db.QueryRow(ctx, `SELECT id, phase, pro_id FROM doorstep.booking_photos WHERE booking_id = $1 AND media_id = $2
        UNION ALL SELECT id, 'evidence', pro_id FROM doorstep.booking_extras WHERE booking_id=$1 AND evidence_media_id=$2 LIMIT 1`,
		bookingID, mediaID).Scan(&r.ID, &r.Phase, &r.ProID); err != nil {
		return nil, mapErr(err)
	}
	return &r, nil
}

// AuditPhotoView writes the audit row of an admin viewing a visit photo.
func (s *Store) AuditPhotoView(ctx context.Context, a Actor, bookingID uuid.UUID, mediaID string) error {
	return s.adminWrite(ctx, a, "booking.photo_view", "booking", map[string]any{"media_id": mediaID}, func(pgx.Tx) (string, error) {
		return bookingID.String(), nil
	})
}

// ---------------------------------------------------------------- OTPs

// OTPRow is a booking's start or end OTP.
type OTPRow struct {
	Sealed      []byte
	Verified    bool
	LockedUntil *time.Time
}

// VisitOTP reads a booking's OTP of kind (start | end).
func (s *Store) VisitOTP(ctx context.Context, bookingID uuid.UUID, kind string) (*OTPRow, error) {
	var r OTPRow
	var verified *time.Time
	if err := s.db.QueryRow(ctx, `SELECT otp_sealed, verified_at, locked_until FROM doorstep.booking_otps WHERE booking_id = $1 AND kind = $2`,
		bookingID, kind).Scan(&r.Sealed, &verified, &r.LockedUntil); err != nil {
		return nil, mapErr(err)
	}
	r.Verified = verified != nil
	utcPtr(&r.LockedUntil)
	return &r, nil
}

// EnsureOTP inserts a booking's OTP of kind unless it exists (the bcrypt
// hash to verify, the sealed copy to show the customer).
func (s *Store) EnsureOTP(ctx context.Context, bookingID uuid.UUID, kind, hash string, sealed []byte, at time.Time) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, bookingID, at, true)
	if err != nil {
		return err
	}
	if f.Pro == nil || f.Pro.Status != "accepted" || f.Pro.AccountStatus != "approved" {
		return ErrStale
	}
	if kind == "start" && f.Status != "arrived" {
		return ErrStale
	}
	if kind == "end" && (f.FinishedAt == nil || !contains([]string{"in_progress", "awaiting_extras_payment"}, f.Status) || f.UnpaidBills != 0 || f.ApprovedUnbilled != 0 || f.ProposedExtras != 0 || f.Photos.After < f.MinAfter) {
		return ErrStale
	}
	_, err = tx.Exec(ctx, `INSERT INTO doorstep.booking_otps (booking_id, kind, otp_hash, otp_sealed, created_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (booking_id, kind) DO NOTHING`, bookingID, kind, hash, sealed, at)
	if err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

// OTPError is a refused OTP: wrong (AttemptsLeft), or locked (Until).
type OTPError struct {
	Locked       bool
	Until        time.Time
	AttemptsLeft int
}

func (e *OTPError) Error() string {
	if e.Locked {
		return "store: OTP locked until " + e.Until.Format(time.RFC3339)
	}
	return fmt.Sprintf("store: wrong OTP, %d attempts left", e.AttemptsLeft)
}

// ErrOTPMissing: the booking has no OTP of that kind yet.
var ErrOTPMissing = errors.New("store: no OTP issued")

// OTPCheck verifies a booking's OTP inside a step: MaxAttempts wrong
// answers lock it for Lock (the counter starts again after the lock).
type OTPCheck struct {
	Kind        string
	Match       func(hash string) bool
	MaxAttempts int
	Lock        time.Duration
}

// verifyOTPTx checks the OTP on the locked row. A wrong answer is counted
// and COMMITTED by the caller (the step itself does not happen): it returns
// (counted=true, *OTPError).
func verifyOTPTx(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, c OTPCheck, at time.Time) (counted bool, err error) {
	var hash string
	var attempts int
	var locked, verified *time.Time
	err = tx.QueryRow(ctx, `SELECT otp_hash, attempts, locked_until, verified_at FROM doorstep.booking_otps
		WHERE booking_id = $1 AND kind = $2 FOR UPDATE`, bookingID, c.Kind).Scan(&hash, &attempts, &locked, &verified)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrOTPMissing
	}
	if err != nil {
		return false, err
	}
	if locked != nil && at.Before(*locked) {
		return false, &OTPError{Locked: true, Until: locked.UTC()}
	}
	if c.Match(hash) {
		_, err := tx.Exec(ctx, `UPDATE doorstep.booking_otps SET verified_at = COALESCE(verified_at, $3), attempts = 0, locked_until = NULL
			WHERE booking_id = $1 AND kind = $2`, bookingID, c.Kind, at)
		return false, err
	}
	attempts++
	if attempts >= c.MaxAttempts {
		until := at.Add(c.Lock)
		if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_otps SET attempts = 0, locked_until = $3 WHERE booking_id = $1 AND kind = $2`,
			bookingID, c.Kind, until); err != nil {
			return false, err
		}
		return true, &OTPError{Locked: true, Until: until}
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_otps SET attempts = $3 WHERE booking_id = $1 AND kind = $2`,
		bookingID, c.Kind, attempts); err != nil {
		return false, err
	}
	return true, &OTPError{AttemptsLeft: c.MaxAttempts - attempts}
}

// ---------------------------------------------------------------- start

// OTPStep is a step gated by an OTP (start, complete).
type OTPStep struct {
	BookingID uuid.UUID
	ProUser   uuid.UUID
	Check     OTPCheck
	At        time.Time
}

// StartVisit moves arrived -> in_progress: decide (ownership, status, the
// before photos) runs on the locked row BEFORE the OTP is tried, so missing
// photos never cost an attempt; then the start OTP (wrong answers counted,
// five lock it for 15 minutes); then status, started_at, history and
// doorstep.booking.started.
func (s *Store) StartVisit(ctx context.Context, in OTPStep, decide func(*VisitFacts) error) (*VisitFacts, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, in.BookingID, in.At, true)
	if err != nil {
		return nil, err
	}
	if err := decide(f); err != nil {
		return nil, err
	}
	if counted, err := verifyOTPTx(ctx, tx, in.BookingID, in.Check, in.At); err != nil {
		if counted {
			if cErr := tx.Commit(ctx); cErr != nil {
				return nil, mapErr(cErr)
			}
		}
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'in_progress', started_at = $2, version = version + 1, updated_at = $2
		WHERE id = $1`, in.BookingID, in.At); err != nil {
		return nil, mapErr(err)
	}
	from := f.Status
	if err := historyTx(ctx, tx, in.BookingID, &from, "in_progress", "pro", &in.ProUser, nil, in.At); err != nil {
		return nil, err
	}
	core, err := bookingCoreTx(ctx, tx, in.BookingID)
	if err != nil {
		return nil, err
	}
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingStarted, core, in.At, core); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	f.Status = "in_progress"
	return f, nil
}

// ---------------------------------------------------------------- finish

// FinishInput is the professional's "work done". The approved extras no
// bill carries yet go on a new visit-extras bill (BillID, PaymentID,
// IntentKey; the split per extra was fixed when it was proposed); every
// visit bill still unpaid becomes due at At + the city's grace minutes.
type FinishInput struct {
	BookingID uuid.UUID
	ProUser   uuid.UUID
	BillID    uuid.UUID
	PaymentID uuid.UUID
	IntentKey string
	At        time.Time
}

// FinishResult is what finishing did.
type FinishResult struct {
	Facts  *VisitFacts
	Status string
	// Unpaid are the visit bills now due (their payment rows wait for the
	// customer); NewBill is the bill opened now, if any.
	Unpaid  []model.ExtrasBill
	NewBill *uuid.UUID
}

// FinishVisit stamps finished_at (decide sees the locked row: ownership,
// in_progress, no extra still proposed, the after photos). With a bill
// unpaid the booking waits in awaiting_extras_payment.
func (s *Store) FinishVisit(ctx context.Context, in FinishInput, decide func(*VisitFacts) error) (*FinishResult, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, in.BookingID, in.At, true)
	if err != nil {
		return nil, err
	}
	if err := decide(f); err != nil {
		return nil, err
	}
	res := &FinishResult{Facts: f, Status: f.Status}
	core, err := bookingCoreTx(ctx, tx, in.BookingID)
	if err != nil {
		return nil, err
	}
	if f.ApprovedUnbilled > 0 {
		if err := openBillTx(ctx, tx, in.BookingID, in.BillID, in.PaymentID, in.IntentKey, nil, in.At); err != nil {
			return nil, err
		}
		id := in.BillID
		res.NewBill = &id
	}
	due := in.At.Add(time.Duration(f.GraceMinutes) * time.Minute)
	rows, err := tx.Query(ctx, `UPDATE doorstep.extras_bills SET due_at = $2, updated_at = $3
		WHERE booking_id = $1 AND kind = 'visit_extras' AND status IN ('open', 'payment_pending')
		RETURNING `+billCols, in.BookingID, due, in.At)
	if err != nil {
		return nil, err
	}
	if res.Unpaid, err = collect(rows, scanBill); err != nil {
		return nil, err
	}
	status := "in_progress"
	if len(res.Unpaid) > 0 {
		status = "awaiting_extras_payment"
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = $2, finished_at = $3, version = version + 1, updated_at = $3
		WHERE id = $1`, in.BookingID, status, in.At); err != nil {
		return nil, mapErr(err)
	}
	if status != f.Status {
		from := f.Status
		if err := historyTx(ctx, tx, in.BookingID, &from, status, "pro", &in.ProUser, nil, in.At); err != nil {
			return nil, err
		}
	}
	core.Status = status
	for _, b := range res.Unpaid {
		d := due
		if err := s.enqueueBookingEvent(ctx, tx, events.BookingExtrasPaymentDue, core, in.At, events.BookingExtrasBillData{
			BookingCore: core, BillID: b.ID, AmountPaise: b.AmountPaise, DueAt: &d}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	res.Status = status
	return res, nil
}

// openBillTx bills the booking's approved, unbilled extras on a new visit
// bill (payment_pending, with its doorstep_extras payment row) and adds it
// to the booking's extras total.
func openBillTx(ctx context.Context, tx pgx.Tx, bookingID, billID, paymentID uuid.UUID, intentKey string, dueAt *time.Time, at time.Time) error {
	var amount, taxable, tax int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(total_paise), 0)::bigint, COALESCE(sum(taxable_paise), 0)::bigint,
		COALESCE(sum(tax_paise), 0)::bigint FROM doorstep.booking_extras WHERE booking_id = $1 AND status = 'approved' AND bill_id IS NULL`,
		bookingID).Scan(&amount, &taxable, &tax); err != nil {
		return err
	}
	if amount <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.extras_bills (id, booking_id, amount_paise, taxable_paise, tax_paise, kind, status, due_at,
		created_at, updated_at) VALUES ($1, $2, $3, $4, $5, 'visit_extras', 'payment_pending', $6, $7, $7)`,
		billID, bookingID, amount, taxable, tax, dueAt, at); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO doorstep.payments (id, booking_id, extras_bill_id, reference_type, reference_id, intent_key,
		amount_paise, status, created_at, updated_at) VALUES ($1, $2, $3, 'doorstep_extras', $3, $4, $5, 'created', $6, $6)`,
		paymentID, bookingID, billID, intentKey, amount, at); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_extras SET status = 'billed', bill_id = $2
		WHERE booking_id = $1 AND status = 'approved' AND bill_id IS NULL`, bookingID, billID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET extras_total_paise = extras_total_paise + $2, version=version+1, updated_at = $3 WHERE id = $1`,
		bookingID, amount, at)
	return err
}

const billCols = `id, booking_id, amount_paise, taxable_paise, tax_paise, status, due_at, paid_at`

func scanBill(r pgx.Rows) (model.ExtrasBill, error) {
	var b model.ExtrasBill
	err := r.Scan(&b.ID, &b.BookingID, &b.AmountPaise, &b.TaxablePaise, &b.TaxPaise, &b.Status, &b.DueAt, &b.PaidAt)
	utcPtr(&b.DueAt)
	utcPtr(&b.PaidAt)
	return b, err
}

// ---------------------------------------------------------------- complete

// EarningIn is one earning line written at completion (or a no-show).
type EarningIn struct {
	Kind        string
	AmountPaise int64
}

// CompleteSpec is what completion writes besides the status: the invoice
// recomputed with the actual professional, and the earning lines (computed
// by the service from the same facts; ExtrasTotal pins them: a booking
// whose extras moved meanwhile is ErrStale).
type CompleteSpec struct {
	ProID       uuid.UUID
	Version     int
	Invoice     []byte
	ExtrasTotal int64
	Lines       []EarningIn
}

// CompleteVisit completes a finished visit with the end OTP: decide on the
// locked row (ownership, finished, after photos, no bill unpaid), the OTP
// (counted, locked after five), then status completed, completed_at, the
// invoice, the assignment completed, the professional's job count, the
// earning lines, a rework child's request completed, history and
// doorstep.booking.completed — one transaction.
func (s *Store) CompleteVisit(ctx context.Context, in OTPStep, spec CompleteSpec, decide func(*VisitFacts) error) (*VisitFacts, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, in.BookingID, in.At, true)
	if err != nil {
		return nil, err
	}
	if err := decide(f); err != nil {
		return nil, err
	}
	if f.ExtrasTotal != spec.ExtrasTotal || f.Pro.ProID != spec.ProID || f.Version != spec.Version {
		return nil, ErrStale
	}
	if counted, err := verifyOTPTx(ctx, tx, in.BookingID, in.Check, in.At); err != nil {
		if counted {
			if cErr := tx.Commit(ctx); cErr != nil {
				return nil, mapErr(cErr)
			}
		}
		return nil, err
	}
	// The status first: the visit gate reads the accepted assignment.
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'completed', completed_at = $2, invoice_snapshot = $3,
		version = version + 1, updated_at = $2 WHERE id = $1`, in.BookingID, in.At, spec.Invoice); err != nil {
		return nil, mapErr(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = 'completed', updated_at = $2 WHERE id = $1`,
		f.Pro.AssignmentID, in.At); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.professionals SET jobs_completed = jobs_completed + 1, updated_at = $2 WHERE id = $1`,
		f.Pro.ProID, in.At); err != nil {
		return nil, err
	}
	if err := earningLinesTx(ctx, tx, f.Pro.ProID, in.BookingID, spec.Lines, in.At); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.rework_requests SET status = 'completed' WHERE child_booking_id = $1 AND status = 'scheduled'`,
		in.BookingID); err != nil {
		return nil, err
	}
	from := f.Status
	if err := historyTx(ctx, tx, in.BookingID, &from, "completed", "pro", &in.ProUser, nil, in.At); err != nil {
		return nil, err
	}
	core, err := bookingCoreTx(ctx, tx, in.BookingID)
	if err != nil {
		return nil, err
	}
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingCompleted, core, in.At, events.BookingCompletedData{
		BookingCore: core, TotalPaise: f.TotalPaise, ExtrasTotalPaise: f.ExtrasTotal}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	f.Status = "completed"
	return f, nil
}

func earningLinesTx(ctx context.Context, tx pgx.Tx, proID, bookingID uuid.UUID, lines []EarningIn, at time.Time) error {
	for _, l := range lines {
		if l.AmountPaise == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO doorstep.earning_lines (pro_id, booking_id, kind, amount_paise, created_at)
			VALUES ($1, $2, $3, $4, $5) ON CONFLICT (booking_id, pro_id, kind) DO NOTHING`, proID, bookingID, l.Kind, l.AmountPaise, at); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- customer no-show

// NoShowDecision is the service's verdict on a customer no-show.
type NoShowDecision struct {
	FeePaise    int64
	RefundPaise int64
	RefundCause string
	RefundKey   string
	// Compensation is the professional's share of the fee.
	Compensation int64
}

// CustomerNoShow ends an arrived booking whose customer never appeared
// (decide: ownership, arrived, the 15-minute wait; it prices the fee):
// status customer_no_show, the fee kept and the rest refunded over the
// booking's payments, the professional compensated, the assignment done,
// history and doorstep.booking.no_show (party customer). Returns the
// refund rows to submit.
func (s *Store) CustomerNoShow(ctx context.Context, bookingID, proUser uuid.UUID, at time.Time,
	decide func(*VisitFacts) (*NoShowDecision, error)) ([]uuid.UUID, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, bookingID, at, true)
	if err != nil {
		return nil, err
	}
	d, err := decide(f)
	if err != nil {
		return nil, err
	}
	reason := "the customer was not there"
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET status = 'customer_no_show', cancellation_fee_paise = $3, cancel_reason = $4,
		version = version + 1, updated_at = $2 WHERE id = $1`, bookingID, at, d.FeePaise, reason); err != nil {
		return nil, mapErr(err)
	}
	core, err := bookingCoreTx(ctx, tx, bookingID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_assignments SET status = 'completed', release_cause = 'customer_no_show', updated_at = $2
		WHERE id = $1`, f.Pro.AssignmentID, at); err != nil {
		return nil, err
	}
	if err := earningLinesTx(ctx, tx, f.Pro.ProID, bookingID, []EarningIn{{Kind: "cancel_compensation", AmountPaise: d.Compensation}}, at); err != nil {
		return nil, err
	}
	from := f.Status
	if err := historyTx(ctx, tx, bookingID, &from, "customer_no_show", "pro", &proUser, &reason, at); err != nil {
		return nil, err
	}
	var refunds []uuid.UUID
	if d.RefundPaise > 0 {
		if refunds, err = refundAcrossTx(ctx, tx, bookingID, d.RefundPaise, d.RefundCause, d.RefundKey, nil, at); err != nil {
			return nil, err
		}
	}
	core.Status = "customer_no_show"
	if err := s.enqueueBookingEvent(ctx, tx, events.BookingNoShow, core, at, events.BookingNoShowData{
		BookingCore: core, Party: "customer", FeePaise: d.FeePaise}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	return refunds, nil
}

// MaxCancellationFee is the highest fee among the city's active
// cancellation rules for the category (or the city default rules).
func (s *Store) MaxCancellationFee(ctx context.Context, city string, categoryID uuid.UUID) (int64, error) {
	var fee int64
	err := s.db.QueryRow(ctx, `SELECT COALESCE(max(fee_paise), 0)::bigint FROM doorstep.cancellation_rules
		WHERE city_code = $1 AND active AND (category_id = $2 OR category_id IS NULL)`, city, categoryID).Scan(&fee)
	return fee, err
}

// ---------------------------------------------------------------- resetting a visit

// resetVisitTx clears a visit a professional left (pro_unavailable from
// assigned up to in_progress): the OTPs (the next professional gets new
// ones), the visit stamps, extras not billed (withdrawn), visit bills not
// paid (cancelled) and paid visit bills refunded in full. Returns refund
// rows to submit.
func resetVisitTx(ctx context.Context, tx pgx.Tx, bookingID uuid.UUID, at time.Time) ([]uuid.UUID, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM doorstep.booking_otps WHERE booking_id = $1`, bookingID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET started_at = NULL, finished_at = NULL WHERE id = $1`, bookingID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_extras SET status = 'withdrawn', decided_at = COALESCE(decided_at, $2)
		WHERE booking_id = $1 AND status IN ('proposed', 'approved')`, bookingID, at); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `UPDATE doorstep.extras_bills SET status = 'cancelled', updated_at = $2
		WHERE booking_id = $1 AND kind = 'visit_extras' AND status IN ('open', 'payment_pending', 'outstanding') RETURNING id`, bookingID, at)
	if err != nil {
		return nil, err
	}
	cancelled, err := collectIDs(rows)
	if err != nil {
		return nil, err
	}
	if len(cancelled) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.outstanding SET status = 'waived', settled_at = $2
			WHERE extras_bill_id = ANY($1) AND status = 'open'`, cancelled, at); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.booking_extras SET status = 'withdrawn' WHERE bill_id = ANY($1)`, cancelled); err != nil {
			return nil, err
		}
	}
	// Paid visit extras of a visit that did not finish go back in full.
	type paid struct {
		payment, bill uuid.UUID
		left          int64
	}
	rows, err = tx.Query(ctx, `
		SELECT p.id, eb.id, p.amount_paise - COALESCE((SELECT sum(r.amount_paise) FROM doorstep.refunds r
		                                               WHERE r.payment_id = p.id AND r.status <> 'failed'), 0)
		FROM doorstep.extras_bills eb JOIN doorstep.payments p ON p.extras_bill_id = eb.id
		WHERE eb.booking_id = $1 AND eb.kind = 'visit_extras' AND eb.status = 'paid' AND p.status IN ('succeeded', 'partially_refunded')`, bookingID)
	if err != nil {
		return nil, err
	}
	list, err := collect(rows, func(r pgx.Rows) (paid, error) {
		var v paid
		err := r.Scan(&v.payment, &v.bill, &v.left)
		return v, err
	})
	if err != nil {
		return nil, err
	}
	var out []uuid.UUID
	for _, p := range list {
		if p.left <= 0 {
			continue
		}
		cause := "visit_extras_" + shortHash(p.bill.String())
		rid, err := insertRefundTx(ctx, tx, p.payment, bookingID, cause, "doorstep:refund:"+bookingID.String()+":"+cause, p.left, nil, at)
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.extras_bills SET status = 'refunded', updated_at = $2 WHERE id = $1`, p.bill, at); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET extras_total_paise = GREATEST(extras_total_paise - $2, 0) WHERE id = $1`,
			bookingID, p.left); err != nil {
			return nil, err
		}
		out = append(out, rid)
	}
	if len(cancelled) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE doorstep.bookings SET extras_total_paise = GREATEST(extras_total_paise -
			(SELECT COALESCE(sum(amount_paise), 0) FROM doorstep.extras_bills WHERE id = ANY($2)), 0) WHERE id = $1`, bookingID, cancelled); err != nil {
			return nil, err
		}
	}
	return out, nil
}
