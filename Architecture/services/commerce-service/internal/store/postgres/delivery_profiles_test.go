package postgres

import (
	"context"
	"errors"
	"github.com/atpost/commerce-service/database"
	"github.com/atpost/commerce-service/internal/testdsn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
)

func TestDeliveryDocumentsRelevantToVehicle(t *testing.T) {
	if len(RequiredDeliveryDocuments("bicycle")) != 3 || len(RequiredDeliveryDocuments("motorbike")) != 5 {
		t.Fatal("wrong required evidence")
	}
	for _, kind := range []string{"menu", "restaurant", "catalogue", "fish"} {
		if ValidDeliveryDocument(kind) {
			t.Fatalf("merchant field accepted: %s", kind)
		}
	}
	for _, vehicle := range []string{"bicycle", "motorbike", "car"} {
		for _, kind := range RequiredDeliveryDocuments(vehicle) {
			if kind == "introduction" {
				t.Fatal("introduction cannot block approval")
			}
		}
	}
}

func deliveryTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("COMMERCE_DELIVERY_TEST_DSN")
	if dsn == "" {
		t.Skip("COMMERCE_DELIVERY_TEST_DSN not configured (isolated *_test database only)")
	}
	testdsn.Refuse(dsn)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	sql, err := database.Migrations.ReadFile("migrations/040_delivery_partner_applications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(sql)); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS outbox_events(id bigserial primary key,event_type text not null,partition_key text not null,payload jsonb not null,idempotency_key text,created_at timestamptz default now(),published_at timestamptz);
        CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_idempotency_key ON outbox_events(idempotency_key) WHERE idempotency_key IS NOT NULL;`)
	if err != nil {
		t.Fatal(err)
	}
	return New(pool)
}

func TestDeliveryDBApplicationReviewAndIsolation(t *testing.T) {
	s := deliveryTestStore(t)
	ctx := context.Background()
	user, actor := uuid.New(), uuid.New()
	if _, err := s.DeliveryProfile(ctx, user); !errors.Is(err, ErrDeliveryProfileMissing) {
		t.Fatalf("missing profile: %v", err)
	}
	if err := s.SaveDeliveryProfile(ctx, user, "Delivery QA Partner", "bicycle"); err != nil {
		t.Fatal(err)
	}
	if err := s.SubmitDeliveryProfile(ctx, user); !errors.Is(err, ErrDeliveryIncomplete) {
		t.Fatalf("submitted without selfie: %v", err)
	}
	for _, kind := range RequiredDeliveryDocuments("bicycle") {
		if err := s.SaveDeliveryDocument(ctx, user, kind, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SubmitDeliveryProfile(ctx, user); err != nil {
		t.Fatal(err)
	}
	p, err := s.DeliveryProfile(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "submitted" {
		t.Fatal(p.Status)
	}
	if err = s.ReviewDelivery(ctx, actor, p.ID, uuid.Nil, p.Revision, "approved", ""); !errors.Is(err, ErrDeliveryIncomplete) {
		t.Fatalf("approved unreviewed evidence: %v", err)
	}
	stale := p.Revision
	for _, doc := range p.Documents {
		fresh, _ := s.DeliveryProfile(ctx, user)
		if err = s.ReviewDelivery(ctx, actor, p.ID, doc.ID, fresh.Revision, "approved", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.ReviewDelivery(ctx, actor, p.ID, uuid.Nil, stale, "approved", ""); !errors.Is(err, ErrDeliveryReviewConflict) {
		t.Fatalf("stale approval: %v", err)
	}
	fresh, _ := s.DeliveryProfile(ctx, user)
	if err = s.ReviewDelivery(ctx, actor, p.ID, uuid.Nil, fresh.Revision, "approved", ""); err != nil {
		t.Fatal(err)
	}
	approved, _ := s.DeliveryProfile(ctx, user)
	if approved.Status != "approved" {
		t.Fatal(approved.Status)
	}
	if err = s.SaveDeliveryProfile(ctx, user, "Changed without review", "car"); !errors.Is(err, ErrDeliveryReviewConflict) {
		t.Fatalf("approved identity mutated: %v", err)
	}
	if err = s.SaveDeliveryDocument(ctx, user, "selfie", uuid.New()); !errors.Is(err, ErrDeliveryReviewConflict) {
		t.Fatalf("approved evidence mutated: %v", err)
	}
	if _, err = s.DeliveryProfile(ctx, uuid.New()); !errors.Is(err, ErrDeliveryProfileMissing) {
		t.Fatalf("different account received profile: %v", err)
	}
	var count int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE partition_key=$1`, user.String()).Scan(&count); err != nil || count != 4 {
		t.Fatalf("atomic review events=%d err=%v", count, err)
	}
	var leaked bool
	if err = s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM outbox_events WHERE partition_key=$1 AND (payload ? 'media_id' OR payload ? 'note'))`, user.String()).Scan(&leaked); err != nil || leaked {
		t.Fatal("private evidence leaked in event", err)
	}
}

func TestDeliveryDBConcurrentReviewUsesOneRevision(t *testing.T) {
	s := deliveryTestStore(t)
	ctx := context.Background()
	user := uuid.New()
	if err := s.SaveDeliveryProfile(ctx, user, "Concurrent QA Partner", "bicycle"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range RequiredDeliveryDocuments("bicycle") {
		if err := s.SaveDeliveryDocument(ctx, user, kind, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SubmitDeliveryProfile(ctx, user); err != nil {
		t.Fatal(err)
	}
	p, _ := s.DeliveryProfile(ctx, user)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.ReviewDelivery(ctx, uuid.New(), p.ID, p.Documents[0].ID, p.Revision, "approved", "")
		}(i)
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrDeliveryReviewConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins %d conflicts %d", wins, conflicts)
	}
}

func TestDeliveryDBDocumentCorrectionReopensApplication(t *testing.T) {
	s := deliveryTestStore(t)
	ctx := context.Background()
	user, actor := uuid.New(), uuid.New()
	if err := s.SaveDeliveryProfile(ctx, user, "Correction QA Partner", "bicycle"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range RequiredDeliveryDocuments("bicycle") {
		if err := s.SaveDeliveryDocument(ctx, user, kind, uuid.New()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SubmitDeliveryProfile(ctx, user); err != nil {
		t.Fatal(err)
	}
	p, err := s.DeliveryProfile(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	doc := p.Documents[0]
	if err = s.ReviewDelivery(ctx, actor, p.ID, doc.ID, p.Revision, "changes_requested", "Please upload a clearer photo"); err != nil {
		t.Fatal(err)
	}
	p, err = s.DeliveryProfile(ctx, user)
	if err != nil || p.Status != "changes_requested" {
		t.Fatalf("correction did not reopen application: %+v %v", p, err)
	}
	if err = s.SubmitDeliveryProfile(ctx, user); !errors.Is(err, ErrDeliveryIncomplete) {
		t.Fatalf("uncorrected document resubmitted: %v", err)
	}
	if err = s.SaveDeliveryDocument(ctx, user, doc.Kind, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err = s.SubmitDeliveryProfile(ctx, user); err != nil {
		t.Fatal(err)
	}
}
