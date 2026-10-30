package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Audit 2026-10-30 L3, L4, L6 and the Android audit's M3: worker wiring.

type opsLaneFakeStore struct {
	Store
	lockFree    bool
	lockCalls   []string
	expireCalls int
	lastFraud   *time.Time
	fraudScans  int
	released    []uuid.UUID
	codesCalls  int
	acceptOffer *postgres.DeliveryOffer
	acceptBatch *postgres.DeliveryBatch
}

func (f *opsLaneFakeStore) RunExclusive(ctx context.Context, name string, fn func(context.Context) error) (bool, error) {
	f.lockCalls = append(f.lockCalls, name)
	if !f.lockFree {
		return false, nil
	}
	return true, fn(ctx)
}

func (f *opsLaneFakeStore) ExpireDeliveryOffers(context.Context) (int, error) {
	f.expireCalls++
	return 0, nil
}

func (f *opsLaneFakeStore) ListPendingDeliveryBatches(context.Context, int) ([]postgres.PendingDeliveryBatch, error) {
	return nil, nil
}

func (f *opsLaneFakeStore) ListUnbatchedReadyOrders(context.Context, int) ([]postgres.ReadyOrderForBatching, error) {
	return nil, nil
}

func (f *opsLaneFakeStore) LatestFraudScoreAt(context.Context) (*time.Time, error) {
	return f.lastFraud, nil
}

func (f *opsLaneFakeStore) RecentRefundsByUser(context.Context, int) ([]postgres.RecentRefundsByUserRow, error) {
	f.fraudScans++
	return nil, nil
}

func (f *opsLaneFakeStore) ReportCouponAbuse(context.Context, postgres.ReportWindow, int) ([]postgres.CouponAbuseRow, error) {
	return nil, nil
}

func (f *opsLaneFakeStore) RecentCustomerCancellations(context.Context, int) ([]postgres.CustomerCancellationsRow, error) {
	return nil, nil
}

func (f *opsLaneFakeStore) ReleaseSilentRiderJobs(context.Context, time.Duration, int) ([]uuid.UUID, error) {
	return f.released, nil
}

func (f *opsLaneFakeStore) AcceptBatchOfferTx(context.Context, uuid.UUID, uuid.UUID) (*postgres.DeliveryBatch, uuid.UUID, error) {
	if f.acceptBatch != nil {
		return f.acceptBatch, uuid.New(), nil
	}
	return nil, uuid.Nil, postgres.ErrNotBatchOffer
}

func (f *opsLaneFakeStore) AcceptDeliveryOfferTx(context.Context, uuid.UUID, uuid.UUID) (*postgres.DeliveryOffer, error) {
	o := *f.acceptOffer
	return &o, nil
}

func (f *opsLaneFakeStore) EnsureDeliveryCodes(context.Context, uuid.UUID) (string, string, error) {
	f.codesCalls++
	return "1234", "5678", nil
}

func TestDispatchTickRunsOnlyUnderTheWorkerLock(t *testing.T) {
	held := &opsLaneFakeStore{lockFree: false}
	New(held).runDispatchTick(context.Background())
	if held.expireCalls != 0 || len(held.lockCalls) != 1 || held.lockCalls[0] != workerDispatch {
		t.Fatalf("lock held elsewhere: expire calls %d, lock calls %v", held.expireCalls, held.lockCalls)
	}
	free := &opsLaneFakeStore{lockFree: true}
	New(free).runDispatchTick(context.Background())
	if free.expireCalls != 1 {
		t.Fatalf("lock free: expire calls %d", free.expireCalls)
	}
}

func TestFraudBootPassSkippedWhenRecentOrLocked(t *testing.T) {
	recent := time.Now().Add(-time.Hour)
	st := &opsLaneFakeStore{lockFree: true, lastFraud: &recent}
	New(st).runFraudBootPass(context.Background())
	if st.fraudScans != 0 {
		t.Fatal("boot pass ran although a pass ran an hour ago")
	}
	old := time.Now().Add(-7 * time.Hour)
	st = &opsLaneFakeStore{lockFree: true, lastFraud: &old}
	New(st).runFraudBootPass(context.Background())
	if st.fraudScans != 1 {
		t.Fatal("boot pass did not run after a missed interval")
	}
	st = &opsLaneFakeStore{lockFree: true}
	New(st).runFraudBootPass(context.Background())
	if st.fraudScans != 1 {
		t.Fatal("boot pass did not run on an empty table")
	}
	st = &opsLaneFakeStore{lockFree: false}
	New(st).runFraudBootPass(context.Background())
	if st.fraudScans != 0 || len(st.lockCalls) != 1 || st.lockCalls[0] != workerFraud {
		t.Fatalf("boot pass ran without the lock: scans %d locks %v", st.fraudScans, st.lockCalls)
	}
}

func TestFraudBootPassDue(t *testing.T) {
	now := time.Now()
	recent, stale := now.Add(-time.Hour), now.Add(-6*time.Hour)
	if !fraudBootPassDue(nil, now, fraudPassInterval) || fraudBootPassDue(&recent, now, fraudPassInterval) || !fraudBootPassDue(&stale, now, fraudPassInterval) {
		t.Fatal("fraudBootPassDue table wrong")
	}
}

func TestReleaseSilentRiderJobsCounts(t *testing.T) {
	st := &opsLaneFakeStore{released: []uuid.UUID{uuid.New(), uuid.New()}}
	if n, err := New(st).ReleaseSilentRiderJobs(context.Background()); err != nil || n != 2 {
		t.Fatalf("released %d (%v)", n, err)
	}
}

func TestAcceptReplayDoesNotMintOrAnnounceAgain(t *testing.T) {
	st := &opsLaneFakeStore{acceptOffer: &postgres.DeliveryOffer{ID: uuid.New(), OrderID: uuid.New(), Status: "accepted", Replayed: true}}
	if err := New(st).AcceptDeliveryOffer(context.Background(), uuid.New(), st.acceptOffer.ID); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if st.codesCalls != 0 {
		t.Fatalf("replay touched delivery codes %d times", st.codesCalls)
	}
	st = &opsLaneFakeStore{acceptBatch: &postgres.DeliveryBatch{ID: uuid.New(), Replayed: true,
		Members: []postgres.BatchMember{{OrderID: uuid.New(), Sequence: 1}}}}
	if err := New(st).AcceptDeliveryOffer(context.Background(), uuid.New(), uuid.New()); err != nil || st.codesCalls != 0 {
		t.Fatalf("batch replay: err %v codes %d", err, st.codesCalls)
	}
	// A first accept still mints codes.
	st = &opsLaneFakeStore{acceptOffer: &postgres.DeliveryOffer{ID: uuid.New(), OrderID: uuid.New(), Status: "accepted"}}
	if err := New(st).AcceptDeliveryOffer(context.Background(), uuid.New(), st.acceptOffer.ID); err != nil || st.codesCalls != 1 {
		t.Fatalf("first accept: err %v codes %d", err, st.codesCalls)
	}
}
