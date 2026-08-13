//go:build integration

package service

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/database"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openM7TrustDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("M7_TRUST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("M7_TRUST_POSTGRES_DSN is required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), database.SetupSQL); err != nil {
		pool.Close()
		t.Fatalf("apply real setup.sql: %v", err)
	}
	for _, name := range []string{
		"migrations/002_case_workflow.sql",
		"migrations/004_trust_extras.sql",
		"migrations/005_report_categories.sql",
		"migrations/008_launch_report_and_appeal_integrity.sql",
	} {
		raw, readErr := database.Migrations.ReadFile(name)
		if readErr != nil {
			pool.Close()
			t.Fatal(readErr)
		}
		if _, execErr := pool.Exec(context.Background(), string(raw)); execErr != nil {
			pool.Close()
			t.Fatalf("apply real %s: %v", name, execErr)
		}
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestReportCompatibilityConcurrencyPriorityAndOwnership(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	reportStore := postgres.New(pool)
	svc := New(reportStore, nil)
	reporter, entity := uuid.New(), uuid.New()

	const workers = 64
	var wg sync.WaitGroup
	var success, duplicate atomic.Int32
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.FileReport(ctx, reporter, entity, "video", "hate_speech", "legacy client")
			switch {
			case err == nil:
				success.Add(1)
			case errors.Is(err, postgres.ErrActiveReportExists):
				duplicate.Add(1)
			default:
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("unexpected report error: %v", err)
	}
	if success.Load() != 1 || duplicate.Load() != workers-1 {
		t.Fatalf("success/duplicate=%d/%d", success.Load(), duplicate.Load())
	}
	var entityType, reason string
	if err := pool.QueryRow(ctx, `SELECT entity_type,reason FROM trust.reports WHERE reporter_id=$1 AND entity_id=$2`, reporter, entity).Scan(&entityType, &reason); err != nil {
		t.Fatal(err)
	}
	if entityType != "post" || reason != "hate_abuse" {
		t.Fatalf("stored aliases as %s/%s", entityType, reason)
	}

	ordinaryID, urgentID := uuid.New(), uuid.New()
	_, err := pool.Exec(ctx, `
		INSERT INTO trust.reports(id,reporter_id,entity_type,entity_id,reason,details,status,created_at,updated_at)
		VALUES ($1,$2,'post',$3,'spam','ordinary','open',$4,$4),
		       ($5,$6,'post',$7,'child_safety','urgent','open',$8,$8)
	`, ordinaryID, uuid.New(), uuid.New(), time.Now().Add(-24*time.Hour), urgentID, uuid.New(), uuid.New(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	queue, err := reportStore.GetReports(ctx, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) < 2 || queue[0].Reason != "child_safety" {
		t.Fatalf("urgent category was not first: %#v", queue)
	}
	urgentPos, ordinaryPos := -1, -1
	for i, item := range queue {
		if item.ID == urgentID {
			urgentPos = i
		}
		if item.ID == ordinaryID {
			ordinaryPos = i
		}
	}
	if urgentPos < 0 || ordinaryPos < 0 || urgentPos >= ordinaryPos {
		t.Fatalf("new urgent report position %d must precede ordinary %d", urgentPos, ordinaryPos)
	}
	own, err := reportStore.GetReportsByReporter(ctx, reporter, 20, 0)
	if err != nil || len(own) != 1 || own[0].ReporterID != reporter {
		t.Fatalf("owned receipts leaked/drifted: %#v %v", own, err)
	}
}

type fakePostModeration struct {
	subject   *PostModerationSubject
	overturns atomic.Int32
}

func (f *fakePostModeration) GetSubject(context.Context, uuid.UUID) (*PostModerationSubject, error) {
	return f.subject, nil
}
func (f *fakePostModeration) OverturnAppeal(context.Context, *postgres.ContentAppeal, uuid.UUID, int64, string) error {
	f.overturns.Add(1)
	return nil
}

func TestAppealOwnershipDedupAndRetryAfterCanonicalSuccess(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	owner, postID := uuid.New(), uuid.New()
	client := &fakePostModeration{subject: &PostModerationSubject{PostID: postID, AuthorID: owner, ReviewStatus: "rejected", ContentRevision: 7}}
	svc := New(postgres.New(pool), nil)
	svc.SetExtrasStore(postgres.NewExtrasStore(pool))
	svc.SetPostModerationClient(client)
	if _, err := svc.SubmitAppeal(ctx, uuid.New(), "post", postID.String(), "I own this"); !errors.Is(err, ErrAppealNotEligible) {
		t.Fatalf("non-owner appeal=%v", err)
	}

	const workers = 32
	var wg sync.WaitGroup
	appeals := make(chan *postgres.ContentAppeal, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a, err := svc.SubmitAppeal(ctx, owner, "post", postID.String(), "Please review")
			if err == nil {
				appeals <- a
			} else if !errors.Is(err, postgres.ErrActiveAppealExists) {
				t.Errorf("unexpected submit: %v", err)
			}
		}()
	}
	wg.Wait()
	close(appeals)
	var appeal *postgres.ContentAppeal
	for a := range appeals {
		if appeal != nil {
			t.Fatal("more than one appeal created")
		}
		appeal = a
	}
	if appeal == nil {
		t.Fatal("no appeal created")
	}

	_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS m7_fail_appeal_update ON trust.content_appeals`)
	_, _ = pool.Exec(ctx, `DROP FUNCTION IF EXISTS trust.m7_fail_appeal_update()`)
	if _, err := pool.Exec(ctx, `CREATE FUNCTION trust.m7_fail_appeal_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='overturned' THEN RAISE EXCEPTION 'injected local failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TRIGGER m7_fail_appeal_update BEFORE UPDATE ON trust.content_appeals FOR EACH ROW EXECUTE FUNCTION trust.m7_fail_appeal_update()`); err != nil {
		t.Fatal(err)
	}
	reviewer := uuid.New()
	if err := svc.ReviewAppeal(ctx, appeal.ID, "overturned", "restored", reviewer); err == nil {
		t.Fatal("injected appeal update failure succeeded")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM trust.content_appeals WHERE id=$1`, appeal.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "open" || client.overturns.Load() != 1 {
		t.Fatalf("after failure status/calls=%s/%d", status, client.overturns.Load())
	}
	_, _ = pool.Exec(ctx, `DROP TRIGGER m7_fail_appeal_update ON trust.content_appeals`)
	_, _ = pool.Exec(ctx, `DROP FUNCTION trust.m7_fail_appeal_update()`)
	if err := svc.ReviewAppeal(ctx, appeal.ID, "overturned", "restored", reviewer); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM trust.content_appeals WHERE id=$1`, appeal.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "overturned" || client.overturns.Load() != 2 {
		t.Fatalf("retry status/calls=%s/%d", status, client.overturns.Load())
	}
}
