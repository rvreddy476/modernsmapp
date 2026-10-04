package store

import (
	"context"
	"errors"
	"github.com/atpost/doorstep-service/internal/events"
	"github.com/atpost/doorstep-service/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

type VisitExtraOption struct {
	model.ExtraOption
	Unit    string
	IsPart  bool
	PriceID *uuid.UUID
}

func extraOptionsTx(ctx context.Context, q querier, f *VisitFacts, at time.Time) ([]VisitExtraOption, error) {
	if f.Pro == nil {
		return []VisitExtraOption{}, nil
	}
	var rows pgx.Rows
	var err error
	if f.ExtrasPolicy == "rate_card" && f.Family != "BEAUTY_SALON" {
		rows, err = q.Query(ctx, `SELECT 'rate_card', id, NULL::uuid, name, NULLIF(description,''), price_paise, max_quantity, unit, is_part, NULL::uuid FROM doorstep.rate_cards WHERE city_code=$1 AND category_id=$2 AND active ORDER BY sort_order,id`, f.CityCode, f.CategoryID)
	} else if f.ExtrasPolicy == "catalogue_addons_only" || f.ExtrasPolicy == "catalogue_addons" {
		rows, err = q.Query(ctx, `SELECT 'addon', NULL::uuid, a.id, a.name, NULLIF(a.description,''), p.price_paise, 1, p.unit, FALSE, p.id FROM doorstep.addons a JOIN doorstep.addon_groups g ON g.id=a.group_id JOIN doorstep.pro_service_prices p ON p.addon_id=a.id AND p.pro_id=$1 AND p.service_id=g.service_id AND p.status='approved' AND p.effective_from <= $3 AND (p.effective_to IS NULL OR p.effective_to>$3) WHERE g.service_id=$2 AND g.active AND a.active ORDER BY g.sort_order,a.sort_order,a.id`, f.Pro.ProID, f.ServiceID, at)
	} else {
		return []VisitExtraOption{}, nil
	}
	if err != nil {
		return nil, err
	}
	return collect(rows, func(r pgx.Rows) (VisitExtraOption, error) {
		var o VisitExtraOption
		err := r.Scan(&o.Kind, &o.RateCardID, &o.AddonID, &o.Name, &o.Description, &o.UnitPricePaise, &o.MaxQuantity, &o.Unit, &o.IsPart, &o.PriceID)
		return o, err
	})
}

func (s *Store) VisitExtraOptions(ctx context.Context, id uuid.UUID, at time.Time) ([]VisitExtraOption, error) {
	f, err := s.VisitFacts(ctx, id, at)
	if err != nil {
		return nil, err
	}
	return extraOptionsTx(ctx, s.db, f, at)
}

type NewExtra struct {
	ID, BookingID, User uuid.UUID
	Input               model.ExtraInput
	At                  time.Time
}

func (s *Store) WithdrawVisitExtra(ctx context.Context, id, extra uuid.UUID, at time.Time, guard func(*VisitFacts) error) error {
	return s.visitTransaction(ctx, id, at, guard, func(tx pgx.Tx, _ *VisitFacts) error {
		tag, err := tx.Exec(ctx, `UPDATE doorstep.booking_extras SET status='withdrawn',decided_at=$3 WHERE booking_id=$1 AND id=$2 AND status='proposed'`, id, extra, at)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrStale
		}
		return nil
	})
}

// The service computes the tax on the transaction-selected catalogue price.
func (s *Store) ProposeVisitExtra(ctx context.Context, in NewExtra, decide func(*VisitFacts, VisitExtraOption) (int64, int64, error)) (*model.Extra, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, in.BookingID, in.At, true)
	if err != nil {
		return nil, err
	}
	options, err := extraOptionsTx(ctx, tx, f, in.At)
	if err != nil {
		return nil, err
	}
	for _, o := range options {
		if !(o.RateCardID != nil && in.Input.RateCardID != nil && *o.RateCardID == *in.Input.RateCardID) && !(o.AddonID != nil && in.Input.AddonID != nil && *o.AddonID == *in.Input.AddonID) {
			continue
		}
		taxable, tax, err := decide(f, o)
		if err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO doorstep.booking_extras(id,booking_id,pro_id,kind,rate_card_id,addon_id,name,quantity,unit_price_paise,total_paise,evidence_media_id,unit,is_part,pro_price_id,taxable_paise,tax_paise,proposed_at,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$9::bigint*$8::int,$10,$11,$12,$13,$14,$15,$16,$16)`, in.ID, in.BookingID, f.Pro.ProID, o.Kind, o.RateCardID, o.AddonID, o.Name, *in.Input.Quantity, o.UnitPricePaise, in.Input.EvidenceMediaID, o.Unit, o.IsPart, o.PriceID, taxable, tax, in.At)
		if err != nil {
			return nil, mapErr(err)
		}
		core, err := bookingCoreTx(ctx, tx, in.BookingID)
		if err != nil {
			return nil, err
		}
		if err = s.enqueueBookingEvent(ctx, tx, events.BookingExtrasProposed, core, in.At, events.BookingExtrasProposedData{BookingCore: core, ExtraID: in.ID, Name: o.Name, TotalPaise: o.UnitPricePaise * int64(*in.Input.Quantity)}); err != nil {
			return nil, err
		}
		if err = tx.Commit(ctx); err != nil {
			return nil, mapErr(err)
		}
		return &model.Extra{ID: in.ID, BookingID: in.BookingID, Kind: o.Kind, RateCardID: o.RateCardID, AddonID: o.AddonID, Name: o.Name, Quantity: *in.Input.Quantity, UnitPricePaise: o.UnitPricePaise, TotalPaise: o.UnitPricePaise * int64(*in.Input.Quantity), Status: "proposed", EvidenceMediaID: in.Input.EvidenceMediaID, CreatedAt: in.At}, nil
	}
	return nil, ErrBadReference
}

func (s *Store) DecideVisitExtra(ctx context.Context, id, extra uuid.UUID, status string, at time.Time, bill, payment uuid.UUID, key string, decide func(*VisitFacts) error) (*model.Extra, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	f, err := visitFactsTx(ctx, tx, id, at, true)
	if err != nil {
		return nil, err
	}
	if err = decide(f); err != nil {
		return nil, err
	}
	var previous string
	var amount int64
	if err = tx.QueryRow(ctx, `SELECT status,total_paise FROM doorstep.booking_extras WHERE id=$1 AND booking_id=$2 FOR UPDATE`, extra, id).Scan(&previous, &amount); err != nil {
		return nil, mapErr(err)
	}
	if previous != status && !(status == "approved" && previous == "billed") {
		if previous != "proposed" {
			return nil, ErrStale
		}
		if _, err = tx.Exec(ctx, `UPDATE doorstep.booking_extras SET status=$3,decided_at=$4 WHERE id=$1 AND booking_id=$2`, extra, id, status, at); err != nil {
			return nil, err
		}
		core, err := bookingCoreTx(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if err = s.enqueueBookingEvent(ctx, tx, events.BookingExtrasDecided, core, at, events.BookingExtrasDecidedData{BookingCore: core, ExtraID: extra, Decision: status}); err != nil {
			return nil, err
		}
		// Aggregate unbilled charges: splitting an extra must not bypass the
		// pay-at-approval threshold. The booking lock serializes approvals.
		if err = tx.QueryRow(ctx, `SELECT COALESCE(sum(total_paise),0)::bigint FROM doorstep.booking_extras WHERE booking_id=$1 AND status='approved' AND bill_id IS NULL`, id).Scan(&amount); err != nil {
			return nil, err
		}
		if status == "approved" && amount > f.ChargeNowPaise {
			if err = openBillTx(ctx, tx, id, bill, payment, key, nil, at); err != nil {
				return nil, err
			}
			if err = s.enqueueBookingEvent(ctx, tx, events.BookingExtrasPaymentDue, core, at, events.BookingExtrasBillData{BookingCore: core, BillID: bill, AmountPaise: amount}); err != nil {
				return nil, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, mapErr(err)
	}
	items, err := s.BookingExtras(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, e := range items {
		if e.ID == extra {
			return &e, nil
		}
	}
	return nil, ErrNotFound
}

func (s *Store) VisitBill(ctx context.Context, booking uuid.UUID) (*model.ExtrasBill, error) {
	rows, err := s.db.Query(ctx, `SELECT `+billCols+` FROM doorstep.extras_bills WHERE booking_id=$1 AND kind='visit_extras' AND status NOT IN ('cancelled','refunded','waived') ORDER BY (status IN ('open','payment_pending','outstanding')) DESC, created_at,id LIMIT 1`, booking)
	if err != nil {
		return nil, err
	}
	list, err := collect(rows, scanBill)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}
func (s *Store) VisitBillPayment(ctx context.Context, bill, user uuid.UUID) (*PaymentRow, error) {
	p, err := scanPayment(s.db.QueryRow(ctx, `SELECT `+paymentCols+` FROM doorstep.payments p WHERE p.extras_bill_id=$1 AND EXISTS(SELECT 1 FROM doorstep.bookings b JOIN doorstep.extras_bills eb ON eb.booking_id=b.id WHERE b.id=p.booking_id AND b.customer_user_id=$2 AND eb.id=$1 AND eb.kind IN ('visit_extras','pro_change') AND eb.status IN ('open','payment_pending','outstanding'))`, bill, user))
	if err != nil {
		return nil, mapErr(err)
	}
	return &p, nil
}
func (s *Store) VisitOutstanding(ctx context.Context, user uuid.UUID) (*model.Outstanding, error) {
	rows, err := s.db.Query(ctx, `SELECT eb.id,eb.booking_id,eb.amount_paise,eb.taxable_paise,eb.tax_paise,eb.status,eb.due_at,eb.paid_at FROM doorstep.outstanding o JOIN doorstep.extras_bills eb ON eb.id=o.extras_bill_id WHERE o.customer_user_id=$1 AND o.status='open' ORDER BY o.created_at,o.id`, user)
	if err != nil {
		return nil, err
	}
	bills, err := collect(rows, scanBill)
	if err != nil {
		return nil, err
	}
	if bills == nil {
		bills = []model.ExtrasBill{}
	}
	out := &model.Outstanding{Bills: bills}
	for _, b := range bills {
		out.TotalPaise += b.AmountPaise
	}
	return out, nil
}

func (s *Store) ExpireVisitBills(ctx context.Context, at time.Time, limit int) (int, error) {
	// Lock bookings first, like payment, finish and cancellation, then bills.
	rows, err := s.db.Query(ctx, `SELECT DISTINCT booking_id FROM doorstep.extras_bills WHERE kind='visit_extras' AND status IN ('open','payment_pending') AND due_at<=$1 LIMIT $2`, at, limit)
	if err != nil {
		return 0, err
	}
	ids, err := collectIDs(rows)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		tx, e := s.db.Begin(ctx)
		if e != nil {
			return n, e
		}
		f, e := visitFactsTx(ctx, tx, id, at, true)
		if e == nil && f.Status == "awaiting_extras_payment" && f.FinishedAt != nil {
			var expired []model.ExtrasBill
			var rows pgx.Rows
			rows, e = tx.Query(ctx, `UPDATE doorstep.extras_bills SET status='outstanding',updated_at=$2 WHERE booking_id=$1 AND kind='visit_extras' AND status IN ('open','payment_pending') AND due_at<=$2 RETURNING `+billCols, id, at)
			if e == nil {
				expired, e = collect(rows, scanBill)
			}
			for _, bill := range expired {
				if e != nil {
					break
				}
				_, e = tx.Exec(ctx, `INSERT INTO doorstep.outstanding(customer_user_id,booking_id,extras_bill_id,amount_paise,created_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(extras_bill_id) DO NOTHING`, f.Customer, id, bill.ID, bill.AmountPaise, at)
				if e == nil {
					core, err := bookingCoreTx(ctx, tx, id)
					e = err
					if e == nil {
						e = s.enqueueBookingEvent(ctx, tx, events.BookingOutstandingCreated, core, at, events.BookingExtrasBillData{BookingCore: core, BillID: bill.ID, AmountPaise: bill.AmountPaise, DueAt: bill.DueAt})
					}
				}
			}
			if e == nil && len(expired) > 0 {
				_, e = tx.Exec(ctx, `UPDATE doorstep.bookings SET version=version+1,updated_at=$2 WHERE id=$1`, id, at)
			}
		}
		if e == nil {
			e = tx.Commit(ctx)
			n++
		}
		_ = tx.Rollback(ctx)
		if e != nil && !errors.Is(e, ErrNotFound) {
			return n, e
		}
	}
	return n, nil
}
