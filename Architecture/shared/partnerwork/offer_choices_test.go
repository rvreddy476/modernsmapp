package partnerwork

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"sync"
	"testing"
	"time"
)

func TestAcceptedWorkBlocksFutureOffersUntilTerminalRelease(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	user := uuid.New()
	onDuty(t, db, user, HomeServices)
	start := time.Now().Add(time.Hour)
	r := offer(t, db, user, HomeServices, start, start.Add(time.Hour))
	if err := transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, HomeServices, r.ID, "accept") }); err != nil {
		t.Fatal(err)
	}
	busy, err := BusyIfInstalled(ctx, db, user)
	if err != nil || !busy {
		t.Fatalf("accepted not busy: %v %v", busy, err)
	}
	err = transaction(db, func(tx pgx.Tx) error {
		_, e := OfferTx(ctx, tx, user, HomeServices, uuid.New(), start.Add(3*time.Hour), start.Add(4*time.Hour))
		return e
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("accepted partner got new offer: %v", err)
	}
	if err = transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, HomeServices, r.ID, "release") }); err != nil {
		t.Fatal(err)
	}
	busy, err = BusyIfInstalled(ctx, db, user)
	if err != nil || busy {
		t.Fatalf("released still busy: %v %v", busy, err)
	}
	offer(t, db, user, HomeServices, time.Now(), time.Now().Add(time.Hour))
}

func TestDomainAcceptedWorkPausesNewOffersUntilRelease(t *testing.T) {
	db := domainDB(t)
	ctx := context.Background()
	user := uuid.New()
	job := uuid.New()
	onDuty(t, db, user, HomeServices)
	for _, stage := range []string{"offered", "reserved"} {
		if _, err := db.Exec(ctx, `SELECT partner_work.domain_work($1,'home_services',$2,$3,now()+interval '1 hour',now()+interval '2 hours',now()+interval '20 seconds')`, user, job, stage); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `SELECT partner_work.domain_work($1,'home_services',$2,'offered',now()+interval '4 hours',now()+interval '5 hours',now()+interval '20 seconds')`, user, uuid.New()); err == nil {
		t.Fatal("new offer while accepted")
	}
	if _, err := db.Exec(ctx, `SELECT partner_work.domain_work($1,'home_services',$2,'released',now(),now()+interval '1 hour')`, user, job); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `SELECT partner_work.domain_work($1,'home_services',$2,'offered',now(),now()+interval '1 hour',now()+interval '20 seconds')`, user, uuid.New()); err != nil {
		t.Fatal(err)
	}
}

func TestThreeChoicesOnlyOneConcurrentAcceptance(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	user := uuid.New()
	onDuty(t, db, user, HomeServices)
	choices := []Reservation{}
	for i := 0; i < 3; i++ {
		choices = append(choices, offer(t, db, user, HomeServices, time.Now(), time.Now().Add(time.Hour)))
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i, r := range choices {
		wg.Add(1)
		go func(i int, r Reservation) {
			defer wg.Done()
			errs[i] = transaction(db, func(tx pgx.Tx) error { return DecideTx(ctx, tx, user, HomeServices, r.ID, "accept") })
		}(i, r)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("accepted=%d", successes)
	}
	_, rs, err := Read(ctx, db, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].State != "reserved" {
		t.Fatalf("wrong accepted readback: %+v", rs)
	}
	visible, enabled, err := VisibleOfferJobs(ctx, db, user, HomeServices)
	if err != nil || !enabled || len(visible) != 0 {
		t.Fatalf("withdrawn offers visible: %v %v", visible, err)
	}
}

func TestDomainThreeChoicesCapAndWithdrawal(t *testing.T) {
	db := domainDB(t)
	ctx := context.Background()
	user := uuid.New()
	onDuty(t, db, user, Moped)
	jobs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for i, job := range jobs {
		_, err := db.Exec(ctx, `SELECT partner_work.domain_work($1,'moped',$2,'offered',clock_timestamp(),clock_timestamp()+interval '1 hour',clock_timestamp()+interval '20 seconds')`, user, job)
		if i < 3 && err != nil {
			t.Fatal(err)
		}
		if i == 3 && err == nil {
			t.Fatal("fourth concurrent choice allowed")
		}
	}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i, job := range jobs[:3] {
		wg.Add(1)
		go func(i int, job uuid.UUID) {
			defer wg.Done()
			_, errs[i] = db.Exec(ctx, `SELECT partner_work.domain_work($1,'moped',$2,'reserved',clock_timestamp(),clock_timestamp()+interval '1 hour')`, user, job)
		}(i, job)
	}
	wg.Wait()
	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("domain accepted=%d", successes)
	}
	visible, enabled, err := VisibleOfferJobs(ctx, db, user, Moped)
	if err != nil || !enabled || len(visible) != 0 {
		t.Fatalf("withdrawn choices visible: %v %v", visible, err)
	}
}
