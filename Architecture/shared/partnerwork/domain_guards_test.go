package partnerwork

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func domainDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PARTNER_WORK_DOMAIN_TEST_DSN")
	if dsn == "" {
		t.Skip("PARTNER_WORK_DOMAIN_TEST_DSN unset")
	}
	u, e := url.Parse(dsn)
	if e != nil || !strings.HasSuffix(u.Path, "_test") {
		t.Fatal("isolated _test database required")
	}
	p, e := pgxpool.New(context.Background(), dsn)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	if e = Migrate(context.Background(), p); e != nil {
		t.Fatal(e)
	}
	// Refresh unreleased function definitions on this reusable local fixture.
	for _, name := range []string{"002_domain_guards.sql", "003_duty_mirrors.sql", "004_offer_choices.sql", "005_pause_after_accept.sql"} {
		b, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Exec(context.Background(), string(b)); err != nil {
			t.Fatal(err)
		}
	}
	_, e = p.Exec(context.Background(), `CREATE SCHEMA IF NOT EXISTS food;CREATE SCHEMA IF NOT EXISTS doorstep;
 CREATE TABLE IF NOT EXISTS rider_partners(id UUID PRIMARY KEY,user_id UUID NOT NULL,is_online BOOLEAN DEFAULT FALSE);
 CREATE TABLE IF NOT EXISTS rider_rides(id UUID PRIMARY KEY,partner_id UUID,status TEXT,requested_at TIMESTAMPTZ DEFAULT now());
 CREATE TABLE IF NOT EXISTS rider_ride_offers(id UUID PRIMARY KEY,ride_id UUID,partner_id UUID,status TEXT,expires_at TIMESTAMPTZ);
 CREATE TABLE IF NOT EXISTS food.delivery_partners(id UUID PRIMARY KEY,user_id UUID NOT NULL,is_online BOOLEAN DEFAULT FALSE);
 CREATE TABLE IF NOT EXISTS food.delivery_offers(id UUID PRIMARY KEY,order_id UUID,batch_id UUID,delivery_partner_id UUID,status TEXT,expires_at TIMESTAMPTZ);
 CREATE TABLE IF NOT EXISTS food.delivery_assignments(id UUID PRIMARY KEY,order_id UUID,batch_id UUID,delivery_partner_id UUID,status TEXT);
 CREATE TABLE IF NOT EXISTS doorstep.professionals(id UUID PRIMARY KEY,user_id UUID NOT NULL,on_duty BOOLEAN DEFAULT FALSE);
 CREATE TABLE IF NOT EXISTS doorstep.bookings(id UUID PRIMARY KEY,reserved_pro_id UUID,slot_start TIMESTAMPTZ,slot_end TIMESTAMPTZ,status TEXT);
 CREATE TABLE IF NOT EXISTS doorstep.booking_assignments(id UUID PRIMARY KEY,booking_id UUID,pro_id UUID,status TEXT,offer_expires_at TIMESTAMPTZ);`)
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []Category{Moped, FoodDelivery, HomeServices} {
		if e = InstallDomain(context.Background(), p, c); e != nil {
			t.Fatalf("install %s: %v", c, e)
		}
	}
	return p
}

func TestDomainGuardsDutyAndAcceptedWork(t *testing.T) {
	p := domainDB(t)
	ctx := context.Background()
	u, m, f := uuid.New(), uuid.New(), uuid.New()
	ride, offer := uuid.New(), uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO rider_partners(id,user_id,is_online) VALUES($1,$2,true)`, m, u)
	exec(`INSERT INTO food.delivery_partners(id,user_id) VALUES($1,$2)`, f, u)
	exec(`INSERT INTO rider_rides(id,status) VALUES($1,'requested')`, ride)
	exec(`INSERT INTO rider_ride_offers(id,ride_id,partner_id,status,expires_at) VALUES($1,$2,$3,'sent',now()+interval '60 seconds')`, offer, ride, m)
	state, e := Snapshot(ctx, p, u)
	if e != nil {
		t.Fatal(e)
	}
	if len(state.Reservations) != 1 || time.Until(state.Reservations[0].OfferExpiresAt) > 21*time.Second {
		t.Fatalf("offer deadline: %+v", state)
	}
	exec(`UPDATE rider_ride_offers SET status='accepted' WHERE id=$1`, offer)
	exec(`UPDATE rider_rides SET partner_id=$2,status='partner_assigned' WHERE id=$1`, ride, m)
	_, e = p.Exec(ctx, `UPDATE food.delivery_partners SET is_online=true WHERE id=$1`, f)
	if !errors.Is(Translate(e), ErrBusy) {
		t.Fatalf("switch abandoned ride: %v", e)
	}
	exec(`UPDATE rider_rides SET status='completed' WHERE id=$1`, ride)
	exec(`UPDATE food.delivery_partners SET is_online=true WHERE id=$1`, f)
	state, e = Snapshot(ctx, p, u)
	if e != nil {
		t.Fatal(e)
	}
	if state.Session.Category == nil || *state.Session.Category != FoodDelivery || len(state.Reservations) != 0 {
		t.Fatalf("bad final state %+v", state)
	}
}

func TestDomainGuardsFutureHomeAndBatchCompletion(t *testing.T) {
	p := domainDB(t)
	ctx := context.Background()
	u, h, f := uuid.New(), uuid.New(), uuid.New()
	booking := uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := p.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO doorstep.professionals(id,user_id) VALUES($1,$2)`, h, u)
	exec(`INSERT INTO food.delivery_partners(id,user_id,is_online) VALUES($1,$2,true)`, f, u)
	exec(`INSERT INTO doorstep.bookings(id,reserved_pro_id,slot_start,slot_end,status) VALUES($1,$2,now()+interval '30 minutes',now()+interval '90 minutes','confirmed')`, booking, h)
	_, e := p.Exec(ctx, `INSERT INTO food.delivery_offers(id,order_id,delivery_partner_id,status,expires_at) VALUES($1,$2,$3,'pending',now()+interval '20 seconds')`, uuid.New(), uuid.New(), f)
	if !errors.Is(Translate(e), ErrBusy) {
		t.Fatalf("accepted home work must pause food offers: %v", e)
	}
	exec(`UPDATE doorstep.bookings SET status='cancelled' WHERE id=$1`, booking)
	batch, order1, order2, offer := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO food.delivery_offers(id,order_id,batch_id,delivery_partner_id,status,expires_at) VALUES($1,$2,$3,$4,'pending',now()+interval '20 seconds')`, offer, order1, batch, f)
	exec(`UPDATE food.delivery_offers SET status='accepted' WHERE id=$1`, offer)
	exec(`INSERT INTO food.delivery_assignments(id,order_id,batch_id,delivery_partner_id,status) VALUES($1,$2,$3,$4,'ASSIGNED')`, uuid.New(), order1, batch, f)
	exec(`INSERT INTO food.delivery_assignments(id,order_id,batch_id,delivery_partner_id,status) VALUES($1,$2,$3,$4,'ASSIGNED')`, uuid.New(), order2, batch, f)
	exec(`UPDATE food.delivery_assignments SET status='DELIVERED' WHERE order_id=$1`, order1)
	s, e := Snapshot(ctx, p, u)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Reservations) != 1 || s.Reservations[0].State != "active" {
		t.Fatalf("premature batch release %+v", s)
	}
	exec(`UPDATE food.delivery_assignments SET status='DELIVERED' WHERE order_id=$1`, order2)
	s, e = Snapshot(ctx, p, u)
	if e != nil || len(s.Reservations) != 0 {
		t.Fatalf("batch not released %+v %v", s, e)
	}
}

func TestDomainGuardsExpiredOfferCannotAccept(t *testing.T) {
	p := domainDB(t)
	ctx := context.Background()
	u, m, ride, offer := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO rider_partners(id,user_id,is_online) VALUES($1,$2,true)`, []any{m, u}},
		{`INSERT INTO rider_rides(id,status) VALUES($1,'requested')`, []any{ride}},
		{`INSERT INTO rider_ride_offers(id,ride_id,partner_id,status,expires_at) VALUES($1,$2,$3,'sent',now()-interval '1 second')`, []any{offer, ride, m}},
	} {
		if _, e := p.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	_, e := p.Exec(ctx, `UPDATE rider_ride_offers SET status='accepted' WHERE id=$1`, offer)
	if !errors.Is(Translate(e), ErrExpired) {
		t.Fatalf("accepted expired offer %v", e)
	}
}

func TestDisabledBootstrapDoesNotTouchDatabase(t *testing.T) {
	if e := Bootstrap(context.Background(), nil, Moped, func(string) string { return "false" }); e != nil {
		t.Fatal(e)
	}
}

func TestDomainDutyRevisionRaceAndStaleOfflineCleanup(t *testing.T) {
	db := domainDB(t)
	t.Setenv(EnabledVariable, "true")
	u, m, f, h := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ctx := context.Background()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO rider_partners(id,user_id,is_online) VALUES($1,$2,true)`, []any{m, u}},
		{`INSERT INTO food.delivery_partners(id,user_id) VALUES($1,$2)`, []any{f, u}},
		{`INSERT INTO doorstep.professionals(id,user_id) VALUES($1,$2)`, []any{h, u}},
	} {
		if _, err := db.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Snapshot(ctx, db, u)
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, revisionKey{}, s.Session.Revision)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, c := range []Category{FoodDelivery, HomeServices} {
		wg.Add(1)
		go func(cat Category) {
			defer wg.Done()
			results <- transaction(db, func(tx pgx.Tx) error { return DomainDutyTx(ctx, tx, u, cat, true) })
		}(c)
	}
	wg.Wait()
	close(results)
	wins, stale := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrStale) {
			stale++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || stale != 1 {
		t.Fatalf("wins=%d stale=%d", wins, stale)
	}
	before, err := Snapshot(context.Background(), db, u)
	if err != nil {
		t.Fatal(err)
	}
	if err = transaction(db, func(tx pgx.Tx) error { return DomainDutyTx(context.Background(), tx, u, Moped, false) }); err != nil {
		t.Fatal(err)
	}
	after, err := Snapshot(context.Background(), db, u)
	if err != nil {
		t.Fatal(err)
	}
	if before.Session.Category == nil || after.Session.Category == nil || *before.Session.Category != *after.Session.Category {
		t.Fatal("old phone cleared new category")
	}
}

func TestExistingScheduledHomeBackfillAndReschedule(t *testing.T) {
	db := domainDB(t)
	ctx := context.Background()
	u, h, f, booking := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, e := db.Exec(ctx, q, args...); e != nil {
			t.Fatal(e)
		}
	}
	exec(`INSERT INTO doorstep.professionals(id,user_id) VALUES($1,$2)`, h, u)
	exec(`INSERT INTO food.delivery_partners(id,user_id,is_online) VALUES($1,$2,true)`, f, u)
	// Simulate an already-approved future reservation from before coordination
	// existed. Only this dedicated fixture database has its trigger removed.
	exec(`DROP TRIGGER partner_work_booking ON doorstep.bookings`)
	t.Cleanup(func() { _ = InstallDomain(context.Background(), db, HomeServices) })
	start := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	end := start.Add(time.Hour)
	exec(`INSERT INTO doorstep.bookings(id,reserved_pro_id,slot_start,slot_end,status) VALUES($1,$2,$3,$4,'assigned')`, booking, h, start, end)
	if e := InstallDomain(ctx, db, HomeServices); e != nil {
		t.Fatal(e)
	}
	s, e := Snapshot(ctx, db, u)
	if e != nil {
		t.Fatal(e)
	}
	if s.Session.Category == nil || *s.Session.Category != FoodDelivery || len(s.Reservations) != 1 || s.Reservations[0].State != "reserved" || !s.Reservations[0].StartsAt.Equal(start) {
		t.Fatalf("upgrade lost existing work %+v", s)
	}
	start = start.Add(24 * time.Hour)
	end = start.Add(time.Hour)
	exec(`UPDATE doorstep.bookings SET slot_start=$2,slot_end=$3 WHERE id=$1`, booking, start, end)
	s, e = Snapshot(ctx, db, u)
	if e != nil {
		t.Fatal(e)
	}
	if !s.Reservations[0].StartsAt.Equal(start) {
		t.Fatalf("reschedule kept old conflict window %+v", s)
	}
	// A due reservation must permit returning to Home Services, rather than
	// trapping the partner in the previous category indefinitely.
	exec(`UPDATE doorstep.bookings SET slot_start=now()-interval '1 minute',slot_end=now()+interval '1 hour' WHERE id=$1`, booking)
	if e = transaction(db, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `SELECT partner_work.domain_duty($1,'home_services',true)`, u)
		return err
	}); e != nil {
		t.Fatal(e)
	}
}
