package partnerwork

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PARTNER_WORK_TEST_DSN")
	if dsn == "" {
		t.Skip("PARTNER_WORK_TEST_DSN unset")
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(u.Path, "_test") {
		t.Fatal("isolated _test database required")
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err = Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	return db
}

func transaction(db *pgxpool.Pool, f func(pgx.Tx) error) error {
	ctx := context.Background()
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if err = f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func onDuty(t *testing.T, db *pgxpool.Pool, user uuid.UUID, c Category) {
	t.Helper()
	s, _, err := Read(context.Background(), db, user)
	if err != nil {
		t.Fatal(err)
	}
	if err = transaction(db, func(tx pgx.Tx) error { return SetDutyTx(context.Background(), tx, user, &c, s.Revision) }); err != nil {
		t.Fatal(err)
	}
}

func offer(t *testing.T, db *pgxpool.Pool, user uuid.UUID, c Category, start, end time.Time) Reservation {
	t.Helper()
	var r Reservation
	if err := transaction(db, func(tx pgx.Tx) error {
		var err error
		r, err = OfferTx(context.Background(), tx, user, c, uuid.New(), start, end)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestTwoDeviceDutySwitchOnlyOneRevisionWins(t *testing.T) {
	db := testDB(t)
	user := uuid.New()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, c := range []Category{Moped, FoodDelivery} {
		wg.Add(1)
		go func(i int, c Category) {
			defer wg.Done()
			errs[i] = transaction(db, func(tx pgx.Tx) error { return SetDutyTx(context.Background(), tx, user, &c, 0) })
		}(i, c)
	}
	wg.Wait()
	ok, stale := 0, 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if errors.Is(err, ErrStale) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if ok != 1 || stale != 1 {
		t.Fatalf("success=%d stale=%d", ok, stale)
	}
	s, _, err := Read(context.Background(), db, user)
	if err != nil {
		t.Fatal(err)
	}
	if s.Category == nil || s.Revision != 1 {
		t.Fatalf("bad readback: %+v", s)
	}
}

func TestReservationsSurviveSwitchAndBlockConflictingWork(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	user := uuid.New()
	onDuty(t, db, user, HomeServices)
	start := time.Now().Add(3 * time.Hour)
	r := offer(t, db, user, HomeServices, start, start.Add(time.Hour))
	if err := transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, HomeServices, r.ID, "accept") }); err != nil {
		t.Fatal(err)
	}
	onDuty(t, db, user, FoodDelivery)
	err := transaction(db, func(tx pgx.Tx) error {
		_, err := OfferTx(ctx, tx, user, FoodDelivery, uuid.New(), start.Add(-time.Minute), start.Add(time.Minute))
		return err
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	// Accepted future work is preserved, but new offers now pause even when
	// they do not overlap. Only a trusted terminal release resumes dispatch.
	err = transaction(db, func(tx pgx.Tx) error {
		_, err := OfferTx(ctx, tx, user, FoodDelivery, uuid.New(), time.Now(), time.Now().Add(time.Hour))
		return err
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("new work while accepted: %v", err)
	}
	_, preserved, err := Read(ctx, db, user)
	if err != nil || len(preserved) != 1 || preserved[0].ID != r.ID {
		t.Fatal("lost existing scheduled work", err)
	}
	if err = transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, HomeServices, r.ID, "release") }); err != nil {
		t.Fatal(err)
	}
	before := offer(t, db, user, FoodDelivery, time.Now(), time.Now().Add(time.Hour))
	if err = transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, FoodDelivery, before.ID, "accept") }); err != nil {
		t.Fatal(err)
	}
	s, rs, err := Read(ctx, db, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("lost reservation: %+v", rs)
	}
	err = transaction(db, func(tx pgx.Tx) error { return SetDutyTx(ctx, tx, user, nil, s.Revision) })
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("accepted work abandoned: %v", err)
	}
	if err = transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, FoodDelivery, before.ID, "release") }); err != nil {
		t.Fatal(err)
	}
	onDuty(t, db, user, HomeServices)
}

func TestExpiredOfferCannotAcceptOrRenew(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	user := uuid.New()
	onDuty(t, db, user, Moped)
	r := offer(t, db, user, Moped, time.Now(), time.Now().Add(time.Hour))
	if _, err := db.Exec(ctx, `UPDATE partner_work.reservations SET offer_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, r.ID); err != nil {
		t.Fatal(err)
	}
	err := transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, Moped, r.ID, "accept") })
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expired accepted: %v", err)
	}
	var replay Reservation
	if err = transaction(db, func(tx pgx.Tx) error {
		var err error
		replay, err = OfferTx(ctx, tx, user, Moped, r.JobID, r.StartsAt, r.EndsAt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if replay.ID != r.ID || !replay.OfferExpiresAt.Before(time.Now()) {
		t.Fatal("offer retry extended expiry")
	}
	_, rs, err := Read(ctx, db, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 {
		t.Fatal("expired offer visible as actionable")
	}
}

func TestConcurrentOffersAtMostThreeChoices(t *testing.T) {
	db := testDB(t)
	user := uuid.New()
	onDuty(t, db, user, CommerceDelivery)
	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = transaction(db, func(tx pgx.Tx) error {
				_, err := OfferTx(context.Background(), tx, user, CommerceDelivery, uuid.New(), time.Now(), time.Now().Add(time.Hour))
				return err
			})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if ok != 3 {
		t.Fatalf("offers=%d", ok)
	}
}

func TestDutySwitchWithdrawsOldOfferAndScopesUser(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	user := uuid.New()
	onDuty(t, db, user, Moped)
	r := offer(t, db, user, Moped, time.Now(), time.Now().Add(time.Hour))
	onDuty(t, db, user, FoodDelivery)
	err := transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, Moped, r.ID, "accept") })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale offer accepted: %v", err)
	}
	err = transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, uuid.New(), Moped, r.ID, "release") })
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user altered reservation: %v", err)
	}
}

func TestDomainRollbackAlsoRollsBackReservation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	user := uuid.New()
	onDuty(t, db, user, Moped)
	sentinel := errors.New("domain failed")
	err := transaction(db, func(tx pgx.Tx) error {
		if _, err := OfferTx(ctx, tx, user, Moped, uuid.New(), time.Now(), time.Now().Add(time.Hour)); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	s, rs, err := Read(ctx, db, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 0 || s.Revision != 1 {
		t.Fatal("orphan reservation or revision")
	}
}

func TestOnlyFourCategories(t *testing.T) {
	for _, c := range []Category{Moped, HomeServices, CommerceDelivery, FoodDelivery} {
		if !c.Valid() {
			t.Fatal(c)
		}
	}
	for _, c := range []Category{"fish", "food", "seller", "restaurant", "delivery", ""} {
		if c.Valid() {
			t.Fatal(c)
		}
	}
}
