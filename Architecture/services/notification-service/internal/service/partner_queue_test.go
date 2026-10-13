package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/atpost/notification-service/database"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/shared/partnerwork"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPartnerQueueDBBusyPartnerNeverRingsQueuedOffers(t *testing.T) {
	s, db, p := partnerQueueRig(t)
	ctx := context.Background()
	user := uuid.New()
	if err := partnerwork.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO partner_work.sessions(user_id,category) VALUES($1,'home_services');`, user); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO partner_work.reservations(id,user_id,category,job_id,starts_at,ends_at,state,offer_expires_at) VALUES($1,$2,'home_services',$3,now()+interval '1 hour',now()+interval '2 hours','reserved',now()+interval '20 seconds')`, uuid.New(), user, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pgStore.RegisterDevice(ctx, user, "android", "synthetic-busy-token-"+user.String(), postgres.AppPartner); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"captain.offer", "doorstep.pro.offer.new", "food_delivery_offer", "commerce.delivery.offer"} {
		exp := time.Now().Add(20 * time.Second)
		n := PartnerNotice{ID: uuid.New(), UserID: user, EntityID: uuid.New(), App: postgres.AppPartner, Type: typ, ExpiresAt: &exp, Data: map[string]string{"type": typ}}
		if err := s.queuePartnerNotice(ctx, n); err != nil {
			t.Fatal(err)
		}
		j, err := s.pgStore.ClaimPartnerNotification(ctx)
		if err != nil {
			t.Fatal(err)
		}
		status, code := s.deliverPartnerNotification(ctx, j)
		if status != "expired" || code != "partner_busy" || p.calls != 0 {
			t.Fatalf("busy offer sent: %s %s %d", status, code, p.calls)
		}
		if err = s.pgStore.FinishPartnerNotification(ctx, j, status, code, 0); err != nil {
			t.Fatal(err)
		}
	}
}

type partnerQueuePush struct {
	calls int
	fail  bool
}

func (p *partnerQueuePush) Send(context.Context, string, string, string, string, map[string]string) error {
	p.calls++
	if p.fail {
		return errors.New("provider unavailable")
	}
	return nil
}

func partnerQueueRig(t *testing.T) (*Service, *pgxpool.Pool, *partnerQueuePush) {
	t.Helper()
	dsn := os.Getenv("PARTNER_NOTIFICATION_TEST_DSN")
	if dsn == "" {
		t.Skip("PARTNER_NOTIFICATION_TEST_DSN not configured")
	}
	parsed, err := url.Parse(dsn)
	if err != nil || !strings.HasSuffix(parsed.Path, "_test") {
		t.Fatal("refusing non-test database")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if err = postgres.BootstrapSchema(ctx, db, database.SetupSQL, database.Migrations); err != nil {
		t.Fatal(err)
	}
	// Dedicated disposable DB, no live tokens. Never invoke a real provider.
	if _, err = db.Exec(ctx, `TRUNCATE partner_notification_retry_audit,partner_notification_receipts,partner_notification_jobs`); err != nil {
		t.Fatal(err)
	}
	s := New(nil, nil)
	s.SetPGStore(postgres.New(db))
	p := &partnerQueuePush{}
	s.SetPusher(p)
	return s, db, p
}

func TestPartnerQueueDBRetriesAndDeduplicates(t *testing.T) {
	s, db, p := partnerQueueRig(t)
	ctx := context.Background()
	user := uuid.New()
	id := uuid.New()
	if _, err := s.pgStore.RegisterDevice(ctx, user, "android", "synthetic-qa-token-"+user.String(), postgres.AppPartner); err != nil {
		t.Fatal(err)
	}
	n := PartnerNotice{ID: id, UserID: user, EntityID: uuid.New(), App: postgres.AppFeastRider, Type: FoodTypeDeliveryOffer, Data: map[string]string{"type": FoodTypeDeliveryOffer, "collapse_key": id.String()}, CreatedAt: time.Now()}
	exp := time.Now().Add(20 * time.Second)
	n.ExpiresAt = &exp
	if err := s.queuePartnerNotice(ctx, n); err != nil {
		t.Fatal(err)
	}
	if err := s.queuePartnerNotice(ctx, n); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM partner_notification_jobs WHERE id=$1`, id).Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate enqueue", count, err)
	}
	j, err := s.pgStore.ClaimPartnerNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.fail = true
	if status, _ := s.deliverPartnerNotification(ctx, j); status != "pending" {
		t.Fatal("outage lost", status)
	}
	p.fail = false
	if status, _ := s.deliverPartnerNotification(ctx, j); status != "delivered" {
		t.Fatal("recovery failed", status)
	}
	before := p.calls
	if status, _ := s.deliverPartnerNotification(ctx, j); status != "delivered" || p.calls != before {
		t.Fatal("successful device resent", p.calls)
	}
	if err = s.pgStore.FinishPartnerNotification(ctx, j, "delivered", "", 0); err != nil {
		t.Fatal(err)
	}
}

func TestPartnerQueueDBExpiredOfferNeverRings(t *testing.T) {
	s, _, p := partnerQueueRig(t)
	ctx := context.Background()
	exp := time.Now().Add(-time.Second)
	n := PartnerNotice{ID: uuid.New(), UserID: uuid.New(), EntityID: uuid.New(), App: postgres.AppPartner, Type: "captain.offer", ExpiresAt: &exp, Data: map[string]string{}}
	if err := s.queuePartnerNotice(ctx, n); err != nil {
		t.Fatal(err)
	}
	j, err := s.pgStore.ClaimPartnerNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := s.deliverPartnerNotification(ctx, j); status != "expired" || p.calls != 0 {
		t.Fatal("expired offer replayed")
	}
	if err = s.pgStore.FinishPartnerNotification(ctx, j, "expired", "offer_expired", 0); err != nil {
		t.Fatal(err)
	}
}

func TestPartnerQueueDBMerchantExcludedAndFailedVisible(t *testing.T) {
	s, db, _ := partnerQueueRig(t)
	ctx := context.Background()
	n := PartnerNotice{ID: uuid.New(), UserID: uuid.New(), EntityID: uuid.New(), App: postgres.AppFeastKitchen}
	if err := s.queuePartnerNotice(ctx, n); err == nil {
		t.Fatal("merchant queue admitted")
	}
	n.App = postgres.AppPartner
	if err := s.queuePartnerNotice(ctx, n); err != nil {
		t.Fatal(err)
	}
	j, err := s.pgStore.ClaimPartnerNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.pgStore.FinishPartnerNotification(ctx, j, "failed", "no_registered_device", 0); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM notify_meta.ops_alerts WHERE dedupe_key=$1`, "partner-notification:"+j.ID.String()).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing ops visibility", count, err)
	}
}

func TestPartnerQueueBackoffAndExpiredPayload(t *testing.T) {
	if partnerRetryDelay(1) < time.Minute || partnerRetryDelay(30) > 32*time.Minute {
		t.Fatal("backoff outside bounds")
	}
	s := New(nil, nil)
	id := uuid.New()
	exp := time.Now().Add(-time.Second)
	raw, _ := json.Marshal(PartnerNotice{ID: id})
	status, _ := s.deliverPartnerNotification(context.Background(), &postgres.PartnerNotificationJob{ID: id, Payload: raw, ExpiresAt: &exp})
	if status != "expired" {
		t.Fatal(status)
	}
}

func TestPartnerOfferRetryWithinDeadlineAndHonoursProviderDelay(t *testing.T) {
	expiry := time.Now().Add(20 * time.Second)
	j := &postgres.PartnerNotificationJob{Attempts: 1, ExpiresAt: &expiry}
	if got := partnerJobRetryDelay(j, "device_lookup_failed"); got != time.Second {
		t.Fatalf("offer retry too late: %v", got)
	}
	j.Attempts = 20
	if got := partnerJobRetryDelay(j, "provider_or_receipt_failure"); got != 4*time.Second {
		t.Fatalf("offer retry not capped: %v", got)
	}
	j.RetryAfter = 2 * time.Minute
	if got := partnerJobRetryDelay(j, "provider_or_receipt_failure"); got != 2*time.Minute {
		t.Fatalf("provider backoff shortened: %v", got)
	}
	j.ExpiresAt = nil
	j.RetryAfter = 0
	j.Attempts = 1
	if partnerJobRetryDelay(j, "device_batch_remaining") != time.Second {
		t.Fatal("successful device pagination delayed")
	}
	if partnerJobRetryDelay(j, "provider_or_receipt_failure") != time.Minute {
		t.Fatal("ordinary backoff changed")
	}
}

func TestPartnerQueueDBSuppressionFailureIsRetryable(t *testing.T) {
	s, _, p := partnerQueueRig(t)
	ctx := context.Background()
	user := uuid.New()
	if _, err := s.pgStore.RegisterDevice(ctx, user, "android", "suppression-qa-"+user.String(), postgres.AppPartner); err != nil {
		t.Fatal(err)
	}
	n := PartnerNotice{ID: uuid.New(), UserID: user, EntityID: uuid.New(), App: postgres.AppPartner, Type: "captain.support.updated", CreatedAt: time.Now()}
	if err := s.queuePartnerNotice(ctx, n); err != nil {
		t.Fatal(err)
	}
	j, err := s.pgStore.ClaimPartnerNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failed, cancel := context.WithCancel(ctx)
	cancel()
	status, code := s.deliverPartnerNotification(failed, j)
	if status != "pending" || code != "suppression_lookup_failed" || p.calls != 0 {
		t.Fatalf("unknown suppression lost notice or sent: %s %s calls=%d", status, code, p.calls)
	}
	if err = s.pgStore.FinishPartnerNotification(ctx, j, status, code, 0); err != nil {
		t.Fatal(err)
	}
	j, err = s.pgStore.ClaimPartnerNotification(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status, code = s.deliverPartnerNotification(ctx, j); status != "delivered" || p.calls != 1 {
		t.Fatalf("recovery failed: %s %s calls=%d", status, code, p.calls)
	}
}

func TestPartnerQueueDBDevicesAreBoundedAndResumable(t *testing.T) {
	s, db, p := partnerQueueRig(t)
	ctx := context.Background()
	user := uuid.New()
	for i := 0; i < 25; i++ {
		app := postgres.AppPartner
		if i%2 == 0 {
			app = postgres.AppMopeduCaptain
		}
		if _, err := s.pgStore.RegisterDevice(ctx, user, "android", fmt.Sprintf("batch-%s-%d", user, i), app); err != nil {
			t.Fatal(err)
		}
	}
	// Duplicate token/platform rows and merchant installs cannot multiply sends.
	if _, err := s.pgStore.RegisterDevice(ctx, user, "web", fmt.Sprintf("batch-%s-0", user), postgres.AppPartner); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pgStore.RegisterDevice(ctx, user, "android", "merchant-"+user.String(), postgres.AppFeastKitchen); err != nil {
		t.Fatal(err)
	}
	n := PartnerNotice{ID: uuid.New(), UserID: user, EntityID: uuid.New(), App: postgres.AppMopeduCaptain, Type: "captain.support.updated", CreatedAt: time.Now()}
	if err := s.queuePartnerNotice(ctx, n); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		j, err := s.pgStore.ClaimPartnerNotification(ctx)
		if err != nil {
			t.Fatal(err)
		}
		status, code := s.deliverPartnerNotification(ctx, j)
		want := "pending"
		if i == 2 {
			want = "delivered"
		}
		if status != want || p.calls != min((i+1)*10, 25) {
			t.Fatalf("batch %d skipped or repeated: %s %s calls=%d", i, status, code, p.calls)
		}
		if err = s.pgStore.FinishPartnerNotification(ctx, j, status, code, 0); err != nil {
			t.Fatal(err)
		}
	}
	var receipts int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM partner_notification_receipts WHERE job_id=$1`, n.ID).Scan(&receipts); err != nil || receipts != 25 {
		t.Fatalf("incomplete receipts: %d %v", receipts, err)
	}
}

func TestPartnerQueueDBOfferPriorityAndStaleLeaseIsolation(t *testing.T) {
	s, db, _ := partnerQueueRig(t)
	ctx := context.Background()
	ordinary, offer := uuid.New(), uuid.New()
	if err := s.pgStore.EnqueuePartnerNotification(ctx, ordinary, []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(20 * time.Second)
	if err := s.pgStore.EnqueuePartnerNotification(ctx, offer, []byte(`{}`), &expiry); err != nil {
		t.Fatal(err)
	}
	first, err := s.pgStore.ClaimPartnerNotification(ctx)
	if err != nil || first.ID != offer {
		t.Fatalf("offer waited behind ordinary inbox: %+v %v", first, err)
	}
	if _, err := db.Exec(ctx, `UPDATE partner_notification_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, offer); err != nil {
		t.Fatal(err)
	}
	next, err := s.pgStore.ClaimPartnerNotification(ctx)
	if err != nil || next.ID != offer || next.LeaseID == first.LeaseID {
		t.Fatalf("lease not recovered: %+v %v", next, err)
	}
	if err := s.pgStore.MarkPartnerDeviceDelivered(ctx, first, "qa-old-token-hash"); err != nil {
		t.Fatal(err)
	}
	if done, err := s.pgStore.PartnerDeviceDelivered(ctx, offer, "qa-old-token-hash"); err != nil || done {
		t.Fatalf("old sender wrote receipt: %v %v", done, err)
	}
	if err := s.pgStore.FinishPartnerNotification(ctx, first, "delivered", "", 0); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := db.QueryRow(ctx, `SELECT status FROM partner_notification_jobs WHERE id=$1`, offer).Scan(&status); err != nil || status != "processing" {
		t.Fatalf("old sender completed new lease: %s %v", status, err)
	}
	if err := s.pgStore.FinishPartnerNotification(ctx, next, "pending", "provider_throttled", 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	var delay float64
	if err := db.QueryRow(ctx, `SELECT extract(epoch FROM available_at-now()) FROM partner_notification_jobs WHERE id=$1`, offer).Scan(&delay); err != nil || delay < 7100 {
		t.Fatalf("retry delay not persisted: %v %v", delay, err)
	}
}

func TestPartnerQueueDBConcurrentWorkersClaimDistinctJobs(t *testing.T) {
	s, _, _ := partnerQueueRig(t)
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		if err := s.pgStore.EnqueuePartnerNotification(ctx, uuid.New(), []byte(`{}`), nil); err != nil {
			t.Fatal(err)
		}
	}
	jobs := make([]*postgres.PartnerNotificationJob, 8)
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range jobs {
		wg.Add(1)
		go func(i int) { defer wg.Done(); jobs[i], errs[i] = s.pgStore.ClaimPartnerNotification(ctx) }(i)
	}
	wg.Wait()
	seen := map[uuid.UUID]bool{}
	for i, j := range jobs {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if seen[j.ID] {
			t.Fatal("same job leased to two workers")
		}
		seen[j.ID] = true
	}
}
