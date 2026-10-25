package postgres

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrDeliveryWorkConflict = errors.New("delivery work changed or is unavailable")
var ErrDeliveryCode = errors.New("verification code is invalid or locked; contact support")

type DeliveryLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Label     string  `json:"label"`
}

func (s *Store) SellerDeliveryLocation(ctx context.Context, user, seller uuid.UUID) (*DeliveryLocation, error) {
	var owned bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sellers WHERE id=$1 AND user_id=$2)`, seller, user).Scan(&owned); err != nil {
		return nil, err
	}
	if !owned {
		return nil, pgx.ErrNoRows
	}
	l := &DeliveryLocation{}
	err := s.db.QueryRow(ctx, `SELECT latitude,longitude,label FROM commerce_delivery_locations WHERE owner_kind='seller' AND owner_id=$1`, seller).Scan(&l.Latitude, &l.Longitude, &l.Label)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

type DeliveryAvailability struct {
	AreaID               uuid.UUID `json:"area_id"`
	Latitude             float64   `json:"latitude"`
	Longitude            float64   `json:"longitude"`
	Online               bool      `json:"online"`
	Revision             int64     `json:"revision"`
	ExpectedRevision     int64     `json:"expected_revision,omitempty"`
	ExpectedWorkRevision int64     `json:"expected_work_revision"`
}
type DeliveryJob struct {
	CustomerLocation *CustomerDeliveryLocation `json:"customer_location,omitempty"`
	ID               uuid.UUID                 `json:"id"`
	OrderID          uuid.UUID                 `json:"order_id"`
	SellerID         uuid.UUID                 `json:"seller_id"`
	CustomerUserID   uuid.UUID                 `json:"-"`
	ShipmentID       uuid.UUID                 `json:"shipment_id"`
	AreaID           uuid.UUID                 `json:"area_id"`
	Status           string                    `json:"status"`
	PartnerUserID    *uuid.UUID                `json:"partner_user_id"`
	ReservationID    *uuid.UUID                `json:"-"`
	PayoutPaise      int64                     `json:"payout_paise"`
	PickupLatitude   float64                   `json:"pickup_latitude"`
	PickupLongitude  float64                   `json:"pickup_longitude"`
	DropLatitude     float64                   `json:"drop_latitude"`
	DropLongitude    float64                   `json:"drop_longitude"`
	PickupLabel      string                    `json:"pickup_label"`
	DropLabel        string                    `json:"drop_label"`
	Revision         int64                     `json:"revision"`
	CreatedAt        time.Time                 `json:"created_at"`
	CompletedAt      *time.Time                `json:"completed_at,omitempty"`
}
type CustomerDeliveryLocation struct {
	Latitude   float64   `json:"lat"`
	Longitude  float64   `json:"lng"`
	RecordedAt time.Time `json:"recorded_at"`
}
type DeliveryOffer struct {
	ID            uuid.UUID `json:"id"`
	JobID         uuid.UUID `json:"job_id"`
	PartnerUserID uuid.UUID `json:"-"`
	ReservationID uuid.UUID `json:"-"`
	ExpiresAt     time.Time `json:"expires_at"`
	PayoutPaise   int64     `json:"payout_paise"`
	PickupLabel   string    `json:"pickup_label"`
	DropLabel     string    `json:"drop_label"`
	Status        string    `json:"status"`
}
type DeliveryEarning struct {
	JobID       uuid.UUID `json:"job_id"`
	AmountPaise int64     `json:"amount_paise"`
	CreatedAt   time.Time `json:"created_at"`
}

const deliveryJobCols = `id,order_id,seller_id,customer_user_id,shipment_id,area_id,status,partner_user_id,reservation_id,payout_paise,pickup_latitude,pickup_longitude,drop_latitude,drop_longitude,pickup_label,drop_label,revision,created_at,completed_at`

func scanDeliveryJob(row pgx.Row) (*DeliveryJob, error) {
	j := &DeliveryJob{}
	err := row.Scan(&j.ID, &j.OrderID, &j.SellerID, &j.CustomerUserID, &j.ShipmentID, &j.AreaID, &j.Status, &j.PartnerUserID, &j.ReservationID, &j.PayoutPaise, &j.PickupLatitude, &j.PickupLongitude, &j.DropLatitude, &j.DropLongitude, &j.PickupLabel, &j.DropLabel, &j.Revision, &j.CreatedAt, &j.CompletedAt)
	return j, err
}
func (s *Store) DeliveryJob(ctx context.Context, id uuid.UUID) (*DeliveryJob, error) {
	return scanDeliveryJob(s.db.QueryRow(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE id=$1`, id))
}
func (s *Store) DeliveryJobs(ctx context.Context, user *uuid.UUID) ([]DeliveryJob, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE ($1::uuid IS NULL OR partner_user_id=$1) ORDER BY created_at DESC LIMIT 100`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryJob{}
	for rows.Next() {
		j, e := scanDeliveryJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
func (s *Store) DeliveryAvailability(ctx context.Context, user uuid.UUID) (DeliveryAvailability, error) {
	a := DeliveryAvailability{}
	err := s.db.QueryRow(ctx, `SELECT area_id,latitude,longitude,online,revision FROM commerce_delivery_availability WHERE user_id=$1`, user).Scan(&a.AreaID, &a.Latitude, &a.Longitude, &a.Online, &a.Revision)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return a, err
}
func (s *Store) SaveDeliveryAvailability(ctx context.Context, user uuid.UUID, a DeliveryAvailability) error {
	if !a.Online {
		tag, err := s.db.Exec(ctx, `UPDATE commerce_delivery_availability SET online=false,revision=revision+1,updated_at=clock_timestamp() WHERE user_id=$1 AND revision=$2`, user, a.ExpectedRevision)
		if err == nil && tag.RowsAffected() == 0 {
			return ErrDeliveryWorkConflict
		}
		return err
	}
	areas, err := s.DeliveryAreas(ctx, true)
	if err != nil {
		return err
	}
	covered := false
	for _, area := range areas {
		if area.ID == a.AreaID && area.Contains(a.Latitude, a.Longitude) {
			covered = true
		}
	}
	if !covered {
		return ErrDeliveryConfigInvalid
	}
	tag, err := s.db.Exec(ctx, `INSERT INTO commerce_delivery_availability(user_id,area_id,latitude,longitude,online)
 SELECT user_id,$2,$3,$4,$5 FROM commerce_delivery_profiles WHERE user_id=$1 AND status='approved' AND $6=0
 ON CONFLICT(user_id) DO NOTHING`, user, a.AreaID, a.Latitude, a.Longitude, a.Online, a.ExpectedRevision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	tag, err = s.db.Exec(ctx, `UPDATE commerce_delivery_availability a SET area_id=$2,latitude=$3,longitude=$4,location_recorded_at=NULL,online=$5,revision=revision+1,updated_at=clock_timestamp() WHERE user_id=$1 AND revision=$6 AND EXISTS(SELECT 1 FROM commerce_delivery_profiles p WHERE p.user_id=a.user_id AND p.status='approved')`, user, a.AreaID, a.Latitude, a.Longitude, a.Online, a.ExpectedRevision)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrDeliveryWorkConflict
	}
	return err
}
func validDeliveryLocation(l DeliveryLocation) bool {
	return len(strings.TrimSpace(l.Label)) >= 2 && len(l.Label) <= 300 && (DeliveryArea{Latitude: l.Latitude, Longitude: l.Longitude, RadiusMeters: 1}).Contains(l.Latitude, l.Longitude)
}

// A user can set only their own order destination, before any shipment exists.
// Sellers set only their own pickup. Coordinates are never guessed from text.
func (s *Store) SaveDeliveryLocation(ctx context.Context, user, owner uuid.UUID, kind string, l DeliveryLocation) error {
	if !validDeliveryLocation(l) || (kind != "order" && kind != "seller") {
		return ErrDeliveryConfigInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var id uuid.UUID
	if kind == "order" {
		err = tx.QueryRow(ctx, `SELECT id FROM orders WHERE id=$1 AND customer_user_id=$2 AND payment_status<>'paid' AND status NOT IN ('cancelled','delivered','refunded') FOR UPDATE`, owner, user).Scan(&id)
		if err == nil {
			var exists bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shipments WHERE order_id=$1)`, owner).Scan(&exists)
			if exists {
				return ErrDeliveryWorkConflict
			}
		}
	} else {
		err = tx.QueryRow(ctx, `SELECT id FROM sellers WHERE id=$1 AND user_id=$2 FOR UPDATE`, owner, user).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDeliveryWorkConflict
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_locations(owner_kind,owner_id,latitude,longitude,label) VALUES($1,$2,$3,$4,$5) ON CONFLICT(owner_kind,owner_id) DO UPDATE SET latitude=$3,longitude=$4,label=$5,updated_at=clock_timestamp()`, kind, owner, l.Latitude, l.Longitude, strings.TrimSpace(l.Label))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func deliveryCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()+100000), nil
}

// PrepareLocalDelivery claims this seller's NEW prepaid shipment only after
// explicit seller+area eligibility and BOTH real coordinates pass coverage.
// nil means not opted in; errors must never fall back to booking another courier.
func (s *Store) PrepareLocalDelivery(ctx context.Context, order, seller uuid.UUID) (*Shipment, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var customer uuid.UUID
	var paid, method, status string
	err = tx.QueryRow(ctx, `SELECT customer_user_id,payment_status,COALESCE(payment_method,''),status FROM orders WHERE id=$1 FOR UPDATE`, order).Scan(&customer, &paid, &method, &status)
	if err != nil {
		return nil, err
	}
	if paid != "paid" || method == "cod" || status == "cancelled" || status == "refunded" {
		return nil, nil
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM shipments WHERE order_id=$1 AND seller_id=$2)`, order, seller).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrDeliveryWorkConflict
	}
	var belongs bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM order_items WHERE order_id=$1 AND seller_id=$2)`, order, seller).Scan(&belongs); err != nil {
		return nil, err
	}
	if !belongs {
		return nil, ErrDeliveryWorkConflict
	}
	var chosen string
	err = tx.QueryRow(ctx, `SELECT route FROM commerce_delivery_route_choices WHERE order_id=$1 AND seller_id=$2`, order, seller).Scan(&chosen)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if chosen == "courier" {
		return nil, nil
	}
	chooseCourier := func() (*Shipment, error) {
		if _, e := tx.Exec(ctx, `INSERT INTO commerce_delivery_route_choices(order_id,seller_id,route) VALUES($1,$2,'courier') ON CONFLICT DO NOTHING`, order, seller); e != nil {
			return nil, e
		}
		return nil, tx.Commit(ctx)
	}
	pickup, drop := DeliveryLocation{}, DeliveryLocation{}
	for _, x := range []struct {
		kind string
		id   uuid.UUID
		l    *DeliveryLocation
	}{{"seller", seller, &pickup}, {"order", order, &drop}} {
		err = tx.QueryRow(ctx, `SELECT latitude,longitude,label FROM commerce_delivery_locations WHERE owner_kind=$1 AND owner_id=$2`, x.kind, x.id).Scan(&x.l.Latitude, &x.l.Longitude, &x.l.Label)
		if errors.Is(err, pgx.ErrNoRows) {
			return chooseCourier()
		}
		if err != nil {
			return nil, err
		}
	}
	rows, err := tx.Query(ctx, `SELECT a.id,a.name,a.latitude,a.longitude,a.radius_meters,a.payout_paise,a.enabled,a.revision,a.updated_at FROM commerce_delivery_areas a JOIN commerce_delivery_area_sellers s ON s.area_id=a.id WHERE s.seller_id=$1 AND s.enabled AND a.enabled ORDER BY a.id FOR SHARE OF a,s`, seller)
	if err != nil {
		return nil, err
	}
	var selected *DeliveryArea
	for rows.Next() {
		a, e := scanDeliveryArea(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		if a.Contains(pickup.Latitude, pickup.Longitude) && a.Contains(drop.Latitude, drop.Longitude) && selected == nil {
			selected = a
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if selected == nil {
		return chooseCourier()
	}
	pc, err := deliveryCode()
	if err != nil {
		return nil, err
	}
	dc, err := deliveryCode()
	if err != nil {
		return nil, err
	}
	sh := &Shipment{OrderID: order, SellerID: seller, Courier: "cleestudio_local", Status: "awaiting_ready"}
	err = tx.QueryRow(ctx, `INSERT INTO shipments(order_id,seller_id,courier,status) VALUES($1,$2,$3,$4) RETURNING id,created_at,updated_at`, order, seller, sh.Courier, sh.Status).Scan(&sh.ID, &sh.CreatedAt, &sh.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_jobs(order_id,seller_id,customer_user_id,shipment_id,area_id,payout_paise,pickup_latitude,pickup_longitude,drop_latitude,drop_longitude,pickup_label,drop_label,pickup_code,delivery_code) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, order, seller, customer, sh.ID, selected.ID, *selected.PayoutPaise, pickup.Latitude, pickup.Longitude, drop.Latitude, drop.Longitude, pickup.Label, drop.Label, pc, dc)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_route_choices(order_id,seller_id,route) VALUES($1,$2,'local') ON CONFLICT DO NOTHING`, order, seller); err != nil {
		return nil, err
	}
	return sh, tx.Commit(ctx)
}
func (s *Store) SellerDeliveryReady(ctx context.Context, user, job uuid.UUID) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	j, err := scanDeliveryJob(tx.QueryRow(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE id=$1 AND seller_id IN (SELECT id FROM sellers WHERE user_id=$2) FOR UPDATE`, job, user))
	if err != nil {
		return "", err
	}
	if j.Status != "awaiting_ready" && j.Status != "unassigned" {
		return "", ErrDeliveryWorkConflict
	}
	if j.Status == "awaiting_ready" {
		_, err = tx.Exec(ctx, `UPDATE commerce_delivery_jobs SET status='unassigned',ready_at=clock_timestamp(),revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, job)
		if err != nil {
			return "", err
		}
		_, err = tx.Exec(ctx, `UPDATE shipments SET status='awaiting_assignment',updated_at=clock_timestamp() WHERE id=$1`, j.ShipmentID)
		if err != nil {
			return "", err
		}
	}
	var code string
	err = tx.QueryRow(ctx, `SELECT pickup_code FROM commerce_delivery_jobs WHERE id=$1`, job).Scan(&code)
	if err != nil {
		return "", err
	}
	return code, tx.Commit(ctx)
}
func (s *Store) DeliveryCustomerCode(ctx context.Context, user, job uuid.UUID) (string, error) {
	var code string
	err := s.db.QueryRow(ctx, `SELECT delivery_code FROM commerce_delivery_jobs WHERE id=$1 AND customer_user_id=$2 AND status IN ('assigned','picked_up')`, job, user).Scan(&code)
	return code, err
}
func (s *Store) DeliveryCandidates(ctx context.Context, j DeliveryJob) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `SELECT a.user_id FROM commerce_delivery_availability a JOIN commerce_delivery_profiles p ON p.user_id=a.user_id WHERE a.online AND p.status='approved' AND a.area_id=$1 AND a.updated_at>now()-interval '30 minutes' AND NOT EXISTS(SELECT 1 FROM commerce_delivery_jobs j WHERE j.partner_user_id=a.user_id AND j.status IN ('assigned','picked_up')) AND NOT EXISTS(SELECT 1 FROM commerce_delivery_offers o WHERE o.partner_user_id=a.user_id AND o.job_id=$2) AND (SELECT count(*) FROM commerce_delivery_offers o WHERE o.partner_user_id=a.user_id AND o.status='offered' AND o.expires_at>clock_timestamp())<3 ORDER BY (SELECT count(*) FROM commerce_delivery_jobs j WHERE j.partner_user_id=a.user_id AND j.created_at>=date_trunc('day',now())),(SELECT max(o.created_at) FROM commerce_delivery_offers o WHERE o.partner_user_id=a.user_id) NULLS FIRST,a.user_id LIMIT 100`, j.AreaID, j.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func (s *Store) deliveryEvent(ctx context.Context, tx pgx.Tx, j *DeliveryJob, typ string, extra map[string]any) error {
	p := map[string]any{"event_id": uuid.New(), "job_id": j.ID, "order_id": j.OrderID, "partner_user_id": j.PartnerUserID, "customer_user_id": j.CustomerUserID, "status": j.Status, "revision": j.Revision, "payout_paise": j.PayoutPaise}
	for k, v := range extra {
		p[k] = v
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return s.EnqueueOutboxEvent(ctx, tx, "commerce.delivery."+typ, j.ID.String(), fmt.Sprintf("delivery:%s:%s:%d", j.ID, typ, j.Revision), raw)
}

// Existing customer notification topics retain their established semantics:
// shipped is per seller shipment; delivered is once the entire order completes.
func (s *Store) deliveryCustomerEvent(ctx context.Context, tx pgx.Tx, j *DeliveryJob, typ string) error {
	var number string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(to_jsonb(o)->>'order_number','') FROM orders o WHERE id=$1`, j.OrderID).Scan(&number); err != nil {
		return err
	}
	payload := map[string]any{"event_id": uuid.New(), "order_id": j.OrderID, "order_number": number, "shipment_id": j.ShipmentID, "seller_id": j.SellerID, "user_id": j.CustomerUserID, "courier": "cleestudio_local", "occurred_at": time.Now().UTC()}
	key := "local-shipment-shipped:" + j.ShipmentID.String()
	if typ == "delivered" {
		key = "local-order-delivered:" + j.OrderID.String()
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.EnqueueOutboxEvent(ctx, tx, "commerce.order."+typ, j.OrderID.String(), key, raw)
}
func (s *Store) CreateDeliveryOffer(ctx context.Context, job, user, reservation uuid.UUID, expires time.Time) (*DeliveryOffer, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	j, err := scanDeliveryJob(tx.QueryRow(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE id=$1 FOR UPDATE`, job))
	if err != nil {
		return nil, err
	}
	if j.Status != "unassigned" {
		return nil, ErrDeliveryWorkConflict
	}
	area, err := scanDeliveryArea(tx.QueryRow(ctx, `SELECT a.id,a.name,a.latitude,a.longitude,a.radius_meters,a.payout_paise,a.enabled,a.revision,a.updated_at FROM commerce_delivery_areas a JOIN commerce_delivery_area_sellers s ON s.area_id=a.id WHERE a.id=$1 AND s.seller_id=$2 AND a.enabled AND s.enabled FOR SHARE OF a,s`, j.AreaID, j.SellerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeliveryWorkConflict
	}
	if err != nil {
		return nil, err
	}
	if !area.Contains(j.PickupLatitude, j.PickupLongitude) || !area.Contains(j.DropLatitude, j.DropLongitude) {
		return nil, ErrDeliveryWorkConflict
	}
	var eligible bool
	err = tx.QueryRow(ctx, `SELECT a.online AND p.status='approved' AND a.area_id=$2 AND a.updated_at>now()-interval '30 minutes' FROM commerce_delivery_availability a JOIN commerce_delivery_profiles p ON p.user_id=a.user_id WHERE a.user_id=$1 FOR UPDATE OF a,p`, user, j.AreaID).Scan(&eligible)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, ErrDeliveryWorkConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE commerce_delivery_offers SET status='expired',release_pending=true WHERE status='offered' AND expires_at<=clock_timestamp()`); err != nil {
		return nil, err
	}
	o := &DeliveryOffer{JobID: job, PartnerUserID: user, ReservationID: reservation, ExpiresAt: expires, PayoutPaise: j.PayoutPaise, PickupLabel: j.PickupLabel, DropLabel: j.DropLabel, Status: "offered"}
	err = tx.QueryRow(ctx, `INSERT INTO commerce_delivery_offers(job_id,partner_user_id,reservation_id,expires_at) SELECT $1,$2,$3,$4 WHERE $4>clock_timestamp() AND $4<=clock_timestamp()+interval '20 seconds' AND NOT EXISTS(SELECT 1 FROM commerce_delivery_jobs WHERE partner_user_id=$2 AND status IN ('assigned','picked_up')) ON CONFLICT DO NOTHING RETURNING id`, job, user, reservation, expires).Scan(&o.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeliveryWorkConflict
	}
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(map[string]any{"event_id": uuid.New(), "offer_id": o.ID, "job_id": job, "partner_user_id": user, "expires_at": expires, "payout_paise": j.PayoutPaise})
	if err = s.EnqueueOutboxEvent(ctx, tx, "commerce.delivery.offer", user.String(), "delivery-offer:"+o.ID.String(), raw); err != nil {
		return nil, err
	}
	return o, tx.Commit(ctx)
}
func (s *Store) DeliveryOffers(ctx context.Context, user uuid.UUID) ([]DeliveryOffer, error) {
	rows, err := s.db.Query(ctx, `SELECT o.id,o.job_id,o.partner_user_id,o.reservation_id,o.expires_at,j.payout_paise,j.pickup_label,j.drop_label,o.status FROM commerce_delivery_offers o JOIN commerce_delivery_jobs j ON j.id=o.job_id WHERE o.partner_user_id=$1 AND o.status='offered' AND o.expires_at>clock_timestamp() AND j.status='unassigned' ORDER BY o.created_at LIMIT 100`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryOffer{}
	for rows.Next() {
		var o DeliveryOffer
		if err = rows.Scan(&o.ID, &o.JobID, &o.PartnerUserID, &o.ReservationID, &o.ExpiresAt, &o.PayoutPaise, &o.PickupLabel, &o.DropLabel, &o.Status); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) DeliveryOffer(ctx context.Context, user, id uuid.UUID) (DeliveryOffer, error) {
	var o DeliveryOffer
	err := s.db.QueryRow(ctx, `SELECT id,job_id,partner_user_id,reservation_id,expires_at,status FROM commerce_delivery_offers WHERE id=$1 AND partner_user_id=$2`, id, user).Scan(&o.ID, &o.JobID, &o.PartnerUserID, &o.ReservationID, &o.ExpiresAt, &o.Status)
	return o, err
}
func (s *Store) DecideDeliveryOffer(ctx context.Context, user, id uuid.UUID, accept bool) (*DeliveryJob, error) {
	o, err := s.DeliveryOffer(ctx, user, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	j, err := scanDeliveryJob(tx.QueryRow(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE id=$1 FOR UPDATE`, o.JobID))
	if err != nil {
		return nil, err
	}
	if err = tx.QueryRow(ctx, `SELECT status FROM commerce_delivery_offers WHERE id=$1 AND partner_user_id=$2 FOR UPDATE`, id, user).Scan(&o.Status); err != nil {
		return nil, err
	}
	if accept && j.PartnerUserID != nil && *j.PartnerUserID == user && o.Status == "accepted" {
		return j, nil
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT o.status='offered' AND o.expires_at>clock_timestamp() AND a.online AND p.status='approved' FROM commerce_delivery_offers o JOIN commerce_delivery_availability a ON a.user_id=o.partner_user_id JOIN commerce_delivery_profiles p ON p.user_id=a.user_id WHERE o.id=$1 FOR UPDATE OF o,a,p`, id).Scan(&valid)
	if err != nil {
		return nil, err
	}
	if !valid || j.Status != "unassigned" {
		return nil, ErrDeliveryWorkConflict
	}
	var coverageEnabled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM commerce_delivery_areas a JOIN commerce_delivery_area_sellers s ON s.area_id=a.id WHERE a.id=$1 AND s.seller_id=$2 AND a.enabled AND s.enabled)`, j.AreaID, j.SellerID).Scan(&coverageEnabled); err != nil {
		return nil, err
	}
	if accept && !coverageEnabled {
		return nil, ErrDeliveryWorkConflict
	}
	next := "declined"
	if accept {
		next = "accepted"
		j.Status = "assigned"
		j.PartnerUserID = &user
		j.ReservationID = &o.ReservationID
		j.Revision++
		_, err = tx.Exec(ctx, `UPDATE commerce_delivery_jobs SET status='assigned',partner_user_id=$2,reservation_id=$3,revision=revision+1,updated_at=clock_timestamp() WHERE id=$1`, j.ID, user, o.ReservationID)
		if err != nil {
			return nil, err
		}
		if err = s.deliveryEvent(ctx, tx, j, "assigned", nil); err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE commerce_delivery_offers SET status=$2,release_pending=($2='declined') WHERE id=$1`, id, next)
	if err != nil {
		return nil, err
	}
	return j, tx.Commit(ctx)
}

// Verification failures are persisted with a five-attempt lock; the code is
// never returned through partner job readback, notifications or logs.
func (s *Store) TransitionDeliveryJob(ctx context.Context, user, id uuid.UUID, action, code, reason string, revision int64) (*DeliveryJob, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	// Order before job is the global lock order for shipment and customer tracking.
	var orderID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT order_id FROM commerce_delivery_jobs WHERE id=$1 AND partner_user_id=$2`, id, user).Scan(&orderID); err != nil {
		return nil, err
	}
	var orderStatus string
	if err = tx.QueryRow(ctx, `SELECT status FROM orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&orderStatus); err != nil {
		return nil, err
	}
	if orderStatus == "cancelled" || orderStatus == "refunded" {
		return nil, ErrDeliveryWorkConflict
	}
	j, err := scanDeliveryJob(tx.QueryRow(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE id=$1 AND partner_user_id=$2 FOR UPDATE`, id, user))
	if err != nil {
		return nil, err
	}
	if j.Revision != revision {
		return nil, ErrDeliveryWorkConflict
	}
	next := ""
	switch action {
	case "pickup":
		if j.Status != "assigned" {
			return nil, ErrDeliveryWorkConflict
		}
		next = "picked_up"
	case "deliver":
		if j.Status != "picked_up" {
			return nil, ErrDeliveryWorkConflict
		}
		next = "delivered"
	case "failed":
		if j.Status != "assigned" && j.Status != "picked_up" {
			return nil, ErrDeliveryWorkConflict
		}
		if len(strings.TrimSpace(reason)) < 3 || len(reason) > 2000 {
			return nil, ErrDeliveryInvalid
		}
		next = "failed"
	default:
		return nil, ErrDeliveryInvalid
	}
	if action != "failed" {
		var expected string
		var failures int
		column := "pickup_code"
		if action == "deliver" {
			column = "delivery_code"
		}
		err = tx.QueryRow(ctx, `SELECT `+column+`,code_failures FROM commerce_delivery_jobs WHERE id=$1`, id).Scan(&expected, &failures)
		if err != nil {
			return nil, err
		}
		if failures >= 5 || len(code) != 6 || code != expected {
			_, err = tx.Exec(ctx, `UPDATE commerce_delivery_jobs SET code_failures=LEAST(code_failures+1,5) WHERE id=$1`, id)
			if err != nil {
				return nil, err
			}
			if err = tx.Commit(ctx); err != nil {
				return nil, err
			}
			return nil, ErrDeliveryCode
		}
	}
	_, err = tx.Exec(ctx, `UPDATE commerce_delivery_jobs SET status=$2,code_failures=0,revision=revision+1,updated_at=clock_timestamp(),completed_at=CASE WHEN $2='delivered' THEN clock_timestamp() ELSE completed_at END,release_pending=($2 IN ('delivered','failed','cancelled')) WHERE id=$1`, id, next)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE shipments SET status=$2,updated_at=clock_timestamp(),last_event_at=clock_timestamp(),shipped_at=CASE WHEN $2='picked_up' THEN clock_timestamp() ELSE shipped_at END,delivered_at=CASE WHEN $2='delivered' THEN clock_timestamp() ELSE delivered_at END WHERE id=$1 AND courier='cleestudio_local'`, j.ShipmentID, next)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO shipment_events(shipment_id,status,remark) VALUES($1,$2,$3)`, j.ShipmentID, next, reason)
	if err != nil {
		return nil, err
	}
	j.Status = next
	j.Revision++
	if next == "picked_up" {
		if err = s.deliveryCustomerEvent(ctx, tx, j, "shipped"); err != nil {
			return nil, err
		}
	}
	if next == "picked_up" || next == "delivered" {
		itemStatus := "shipped"
		if next == "delivered" {
			itemStatus = "delivered"
		}
		if _, err = tx.Exec(ctx, `UPDATE order_items SET status=$3,delivered_at=CASE WHEN $3='delivered' THEN clock_timestamp() ELSE delivered_at END WHERE order_id=$1 AND seller_id=$2 AND status NOT IN ('cancelled','returned','refunded')`, j.OrderID, j.SellerID, itemStatus); err != nil {
			return nil, err
		}
		var allMoving, allDelivered bool
		if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM order_items i WHERE i.order_id=$1 AND i.status NOT IN ('cancelled','returned','refunded') AND NOT EXISTS(SELECT 1 FROM shipments s WHERE s.order_id=i.order_id AND s.seller_id=i.seller_id AND s.shipped_at IS NOT NULL)), NOT EXISTS(SELECT 1 FROM order_items i WHERE i.order_id=$1 AND i.status NOT IN ('cancelled','returned','refunded') AND NOT EXISTS(SELECT 1 FROM shipments s WHERE s.order_id=i.order_id AND s.seller_id=i.seller_id AND s.status='delivered'))`, j.OrderID).Scan(&allMoving, &allDelivered); err != nil {
			return nil, err
		}
		if allMoving && !shippedOrBeyond[orderStatus] {
			if orderStatus == "confirmed" {
				if _, err = transitionOrderStatusTx(ctx, tx, j.OrderID, "packed", nil, "system", "local seller readiness confirmed"); err != nil {
					return nil, err
				}
			}
			if _, err = transitionOrderStatusTx(ctx, tx, j.OrderID, "shipped", nil, "system", "all seller shipments picked up"); err != nil {
				return nil, err
			}
			if err = followOrderItemsTx(ctx, tx, j.OrderID, "shipped"); err != nil {
				return nil, err
			}
		}
		if allDelivered {
			transition, transitionErr := transitionOrderStatusTx(ctx, tx, j.OrderID, "delivered", nil, "system", "all seller shipments delivery verified")
			if transitionErr != nil {
				return nil, transitionErr
			}
			if err = followOrderItemsTx(ctx, tx, j.OrderID, "delivered"); err != nil {
				return nil, err
			}
			if transition.Applied {
				if err = s.deliveryCustomerEvent(ctx, tx, j, "delivered"); err != nil {
					return nil, err
				}
			}
		}
	}
	if next == "delivered" {
		_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_earnings(job_id,partner_user_id,amount_paise) VALUES($1,$2,$3) ON CONFLICT(job_id) DO NOTHING`, j.ID, user, j.PayoutPaise)
		if err != nil {
			return nil, err
		}
		if err = s.deliveryEvent(ctx, tx, j, "earnings", nil); err != nil {
			return nil, err
		}
	}
	if next == "failed" {
		_, err = tx.Exec(ctx, `INSERT INTO commerce_delivery_support(job_id,actor_user_id,reason) VALUES($1,$2,$3)`, id, user, reason)
		if err != nil {
			return nil, err
		}
	}
	if err = s.deliveryEvent(ctx, tx, j, next, nil); err != nil {
		return nil, err
	}
	return j, tx.Commit(ctx)
}
func (s *Store) DeliveryEarnings(ctx context.Context, user uuid.UUID) ([]DeliveryEarning, error) {
	rows, err := s.db.Query(ctx, `SELECT job_id,amount_paise,created_at FROM commerce_delivery_earnings WHERE partner_user_id=$1 ORDER BY created_at DESC LIMIT 100`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryEarning{}
	for rows.Next() {
		var e DeliveryEarning
		if err = rows.Scan(&e.JobID, &e.AmountPaise, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) DeliverySupport(ctx context.Context, user, id uuid.UUID, reason string) error {
	if len(strings.TrimSpace(reason)) < 3 || len(reason) > 2000 {
		return ErrDeliveryInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	j, err := scanDeliveryJob(tx.QueryRow(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE id=$1 AND partner_user_id=$2 FOR SHARE`, id, user))
	if err != nil {
		return err
	}
	var ticket uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO commerce_delivery_support(job_id,actor_user_id,reason) VALUES($1,$2,$3) RETURNING id`, id, user, reason).Scan(&ticket)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]any{"job_id": j.ID, "partner_user_id": user, "ticket_id": ticket})
	if err = s.EnqueueOutboxEvent(ctx, tx, "commerce.delivery.support", id.String(), "delivery-support:"+ticket.String(), raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) DeliveryPendingReleases(ctx context.Context) ([]DeliveryJob, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE release_pending AND status IN ('delivered','failed','cancelled') ORDER BY updated_at LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryJob{}
	for rows.Next() {
		j, e := scanDeliveryJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
func (s *Store) DeliveryReleaseDone(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE commerce_delivery_jobs SET release_pending=false WHERE id=$1 AND status IN ('delivered','failed','cancelled')`, id)
	return err
}
func (s *Store) ExpireDeliveryOffers(ctx context.Context) error {
	_, err := s.db.Exec(ctx, `UPDATE commerce_delivery_offers SET status='expired',release_pending=true WHERE status='offered' AND expires_at<=clock_timestamp()`)
	return err
}
func (s *Store) UnassignedDeliveryJobs(ctx context.Context) ([]DeliveryJob, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs j WHERE status='unassigned' AND dispatch_after<=clock_timestamp() AND NOT EXISTS(SELECT 1 FROM commerce_delivery_offers o WHERE o.job_id=j.id AND o.status='offered' AND o.expires_at>clock_timestamp()) ORDER BY dispatch_after,ready_at,id LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryJob{}
	for rows.Next() {
		j, e := scanDeliveryJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}
func (s *Store) PostponeDeliveryDispatch(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE commerce_delivery_jobs SET dispatch_after=clock_timestamp()+interval '2 seconds' WHERE id=$1 AND status='unassigned'`, id)
	return err
}
func (s *Store) DeliveryOfferReleases(ctx context.Context) ([]DeliveryOffer, error) {
	rows, err := s.db.Query(ctx, `SELECT o.id,o.partner_user_id,o.reservation_id FROM commerce_delivery_offers o WHERE o.release_pending AND o.status IN ('expired','declined','cancelled') AND NOT EXISTS(SELECT 1 FROM commerce_delivery_jobs j WHERE j.reservation_id=o.reservation_id AND j.status IN ('assigned','picked_up')) LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryOffer{}
	for rows.Next() {
		var o DeliveryOffer
		if err = rows.Scan(&o.ID, &o.PartnerUserID, &o.ReservationID); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func (s *Store) DeliveryOfferReleaseDone(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE commerce_delivery_offers SET release_pending=false WHERE id=$1`, id)
	return err
}

// Called inside the existing refund cancellation transaction, after the order
// lock. Partially picked-up orders must not be restocked as unshipped parcels.
func (s *Store) cancelDeliveryOrderTx(ctx context.Context, tx pgx.Tx, order uuid.UUID) error {
	var installed bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('commerce_delivery_jobs') IS NOT NULL`).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE order_id=$1 ORDER BY id FOR UPDATE`, order)
	if err != nil {
		return err
	}
	jobs := []DeliveryJob{}
	for rows.Next() {
		j, e := scanDeliveryJob(rows)
		if e != nil {
			rows.Close()
			return e
		}
		jobs = append(jobs, *j)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, j := range jobs {
		var picked bool
		if err = tx.QueryRow(ctx, `SELECT shipped_at IS NOT NULL FROM shipments WHERE id=$1`, j.ShipmentID).Scan(&picked); err != nil {
			return err
		}
		if picked {
			return ErrCancelNotPermitted
		}
		if j.Status == "cancelled" {
			continue
		}
		j.Status = "cancelled"
		j.Revision++
		if _, err = tx.Exec(ctx, `UPDATE commerce_delivery_jobs SET status='cancelled',revision=revision+1,release_pending=(reservation_id IS NOT NULL),updated_at=clock_timestamp() WHERE id=$1`, j.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE commerce_delivery_offers SET status='cancelled',release_pending=true WHERE job_id=$1 AND status='offered'`, j.ID); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `UPDATE shipments SET status='cancelled',updated_at=clock_timestamp() WHERE id=$1`, j.ShipmentID); err != nil {
			return err
		}
		if err = s.deliveryEvent(ctx, tx, &j, "cancelled", nil); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) ShipmentDeliveryJob(ctx context.Context, shipment uuid.UUID) (*DeliveryJob, error) {
	return scanDeliveryJob(s.db.QueryRow(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE shipment_id=$1`, shipment))
}
func (s *Store) SellerLocalPickupCodes(ctx context.Context, user, order uuid.UUID) (map[string]string, error) {
	rows, err := s.db.Query(ctx, `SELECT j.id,j.pickup_code FROM commerce_delivery_jobs j JOIN sellers s ON s.id=j.seller_id WHERE j.order_id=$1 AND s.user_id=$2 AND j.status IN ('unassigned','assigned')`, order, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id uuid.UUID
		var code string
		if err = rows.Scan(&id, &code); err != nil {
			return nil, err
		}
		out[id.String()] = code
	}
	return out, rows.Err()
}
func (s *Store) UpdateDeliveryLocation(ctx context.Context, user uuid.UUID, l DeliveryLocation) error {
	a, err := s.DeliveryAvailability(ctx, user)
	if err != nil {
		return err
	}
	areas, err := s.DeliveryAreas(ctx, true)
	if err != nil {
		return err
	}
	covered := false
	for _, area := range areas {
		if area.ID == a.AreaID && area.Contains(l.Latitude, l.Longitude) {
			covered = true
		}
	}
	if !covered {
		return ErrDeliveryConfigInvalid
	}
	tag, err := s.db.Exec(ctx, `UPDATE commerce_delivery_availability a SET latitude=$2,longitude=$3,location_recorded_at=clock_timestamp(),updated_at=clock_timestamp() WHERE user_id=$1 AND online AND EXISTS(SELECT 1 FROM commerce_delivery_profiles p WHERE p.user_id=a.user_id AND p.status='approved')`, user, l.Latitude, l.Longitude)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrDeliveryWorkConflict
	}
	return err
}
func (s *Store) OrderDeliveryJobs(ctx context.Context, user, id uuid.UUID) ([]DeliveryJob, error) {
	rows, err := s.db.Query(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE order_id=$1 AND (customer_user_id=$2 OR seller_id IN(SELECT id FROM sellers WHERE user_id=$2)) ORDER BY created_at LIMIT 100`, id, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryJob{}
	for rows.Next() {
		j, e := scanDeliveryJob(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	for i := range out {
		j := &out[i]
		// Only this shipment's customer may track its assigned courier, and
		// only between physical pickup and completion. Seller reads omit it.
		if j.CustomerUserID != user || j.Status != "picked_up" || j.PartnerUserID == nil {
			continue
		}
		var fix CustomerDeliveryLocation
		err := s.db.QueryRow(ctx, `SELECT latitude,longitude,location_recorded_at FROM commerce_delivery_availability WHERE user_id=$1 AND location_recorded_at BETWEEN now()-INTERVAL '2 minutes' AND now() AND latitude BETWEEN -90 AND 90 AND longitude BETWEEN -180 AND 180`, *j.PartnerUserID).Scan(&fix.Latitude, &fix.Longitude, &fix.RecordedAt)
		if err == nil {
			j.CustomerLocation = &fix
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	return out, nil
}
func (s *Store) AdminDeliveryAction(ctx context.Context, actor, id uuid.UUID, action, reason string, revision int64) error {
	if actor == uuid.Nil || len(strings.TrimSpace(reason)) < 3 || len(reason) > 2000 {
		return ErrDeliveryInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	j, err := scanDeliveryJob(tx.QueryRow(ctx, `SELECT `+deliveryJobCols+` FROM commerce_delivery_jobs WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if j.Revision != revision {
		return ErrDeliveryWorkConflict
	}
	next := ""
	switch action {
	case "cancel":
		if j.Status == "delivered" || j.Status == "picked_up" || j.Status == "cancelled" {
			return ErrDeliveryWorkConflict
		}
		var picked bool
		if err = tx.QueryRow(ctx, `SELECT shipped_at IS NOT NULL FROM shipments WHERE id=$1`, j.ShipmentID).Scan(&picked); err != nil {
			return err
		}
		if picked {
			return ErrDeliveryWorkConflict
		}
		next = "cancelled"
	case "retry":
		if j.Status != "failed" && j.Status != "unassigned" {
			return ErrDeliveryWorkConflict
		}
		var picked bool
		err = tx.QueryRow(ctx, `SELECT shipped_at IS NOT NULL FROM shipments WHERE id=$1`, j.ShipmentID).Scan(&picked)
		if err != nil {
			return err
		}
		if picked {
			return ErrDeliveryWorkConflict
		}
		var pending bool
		if err = tx.QueryRow(ctx, `SELECT release_pending FROM commerce_delivery_jobs WHERE id=$1`, id).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return ErrDeliveryWorkConflict
		}
		next = "unassigned"
	default:
		return ErrDeliveryInvalid
	}
	// The previous partner is never silently retained for a retry; old offers
	// remain immutable, so no expired offer is replayed to the same account.
	_, err = tx.Exec(ctx, `UPDATE commerce_delivery_jobs SET status=$2,revision=revision+1,release_pending=($2='cancelled' AND reservation_id IS NOT NULL),updated_at=clock_timestamp(),partner_user_id=CASE WHEN $2='unassigned' THEN NULL ELSE partner_user_id END,reservation_id=CASE WHEN $2='unassigned' THEN NULL ELSE reservation_id END WHERE id=$1`, id, next)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE commerce_delivery_offers SET status='cancelled',release_pending=true WHERE job_id=$1 AND status='offered'`, id)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE shipments SET status=$2,updated_at=clock_timestamp() WHERE id=$1 AND courier='cleestudio_local'`, j.ShipmentID, next)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO shipment_events(shipment_id,status,remark) VALUES($1,$2,$3)`, j.ShipmentID, next, reason)
	if err != nil {
		return err
	}
	j.Status = next
	j.Revision++
	if err = s.deliveryEvent(ctx, tx, j, next, map[string]any{"actor_id": actor}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type DeliverySupportTicket struct {
	ID          uuid.UUID `json:"id"`
	JobID       uuid.UUID `json:"job_id"`
	ActorUserID uuid.UUID `json:"actor_user_id"`
	Reason      string    `json:"reason"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) DeliverySupportTickets(ctx context.Context) ([]DeliverySupportTicket, error) {
	rows, err := s.db.Query(ctx, `SELECT id,job_id,actor_user_id,reason,status,created_at FROM commerce_delivery_support ORDER BY (status='open') DESC,created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliverySupportTicket{}
	for rows.Next() {
		var t DeliverySupportTicket
		if err = rows.Scan(&t.ID, &t.JobID, &t.ActorUserID, &t.Reason, &t.Status, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) ResolveDeliverySupport(ctx context.Context, actor, id uuid.UUID, note string) error {
	if actor == uuid.Nil || len(strings.TrimSpace(note)) < 3 || len(note) > 2000 {
		return ErrDeliveryInvalid
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	var job uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE commerce_delivery_support SET status='resolved' WHERE id=$1 AND status='open' RETURNING job_id`, id).Scan(&job)
	if err != nil {
		return err
	}
	// Address the notification to the ticket's partner, not a potentially new
	// assignee after an admin retry. Submission/failure records capture that actor.
	var partner uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT actor_user_id FROM commerce_delivery_support WHERE id=$1`, id).Scan(&partner); err != nil {
		return err
	}
	raw, _ := json.Marshal(map[string]any{"event_id": uuid.New(), "ticket_id": id, "job_id": job, "actor_id": actor, "partner_user_id": partner, "status": "resolved", "note": note})
	if err = s.EnqueueOutboxEvent(ctx, tx, "commerce.delivery.support", job.String(), "delivery-support-resolved:"+id.String(), raw); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
