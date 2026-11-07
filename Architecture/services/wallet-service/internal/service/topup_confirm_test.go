package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/wallet-service/internal/bank"
	"github.com/atpost/wallet-service/internal/store"
	"github.com/google/uuid"
)

// fakeTopUpStore models the top-up rows and the balance counters. Its
// SettleTopUp and ExpireTopUp have the store's contract: only the call that
// moves a row out of pending moves money.
type fakeTopUpStore struct {
	mu        sync.Mutex
	rows      map[uuid.UUID]*store.Transaction
	available map[uuid.UUID]int64
	pendingIn map[uuid.UUID]int64
	// stalePending makes the next N GetTransaction calls answer "pending"
	// whatever the row says, the way racing confirms all read the row
	// before any of them settles it.
	stalePending int
	settleErr    error
}

func newFakeTopUpStore() *fakeTopUpStore {
	return &fakeTopUpStore{
		rows:      map[uuid.UUID]*store.Transaction{},
		available: map[uuid.UUID]int64{},
		pendingIn: map[uuid.UUID]int64{},
	}
}

func (f *fakeTopUpStore) addPending(userID uuid.UUID, amount int64, created time.Time) uuid.UUID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	f.rows[id] = &store.Transaction{ID: id, UserID: userID, Type: "top_up", Direction: "credit", AmountPaise: amount, Status: "pending", CreatedAt: created}
	f.pendingIn[userID] += amount
	return id
}

func (f *fakeTopUpStore) GetTransaction(_ context.Context, userID, txID uuid.UUID) (*store.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[txID]
	if !ok || r.UserID != userID {
		return nil, store.ErrTransactionNotFound
	}
	cp := *r
	if f.stalePending > 0 {
		f.stalePending--
		cp.Status = "pending"
	}
	return &cp, nil
}

func (f *fakeTopUpStore) SetUPIRef(_ context.Context, txID uuid.UUID, upiRef string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	ref := upiRef
	f.rows[txID].UPITxnRef = &ref
	return nil
}

func (f *fakeTopUpStore) SettleTopUp(_ context.Context, txID uuid.UUID, bankRef string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.settleErr != nil {
		return false, f.settleErr
	}
	r := f.rows[txID]
	if r.Status != "pending" {
		return false, nil
	}
	r.Status = "succeeded"
	ref := bankRef
	r.BankTxnRef = &ref
	f.available[r.UserID] += r.AmountPaise
	f.pendingIn[r.UserID] -= r.AmountPaise
	return true, nil
}

func (f *fakeTopUpStore) ListPendingTopUpsOlderThan(_ context.Context, cutoff time.Time, _ int) ([]store.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Transaction
	for _, r := range f.rows {
		if r.Status == "pending" && r.CreatedAt.Before(cutoff) {
			out = append(out, *r)
		}
	}
	return out, nil
}

func (f *fakeTopUpStore) ExpireTopUp(_ context.Context, txID uuid.UUID, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := f.rows[txID]
	if r.Status != "pending" {
		return false, nil
	}
	r.Status = "failed"
	f.pendingIn[r.UserID] -= r.AmountPaise
	return true, nil
}

func (f *fakeTopUpStore) setStatus(txID uuid.UUID, status string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[txID].Status = status
}

// recordingPublisher counts top-up events.
type recordingPublisher struct {
	noopPublisher
	mu        sync.Mutex
	succeeded []uuid.UUID
	failed    []uuid.UUID
}

func (p *recordingPublisher) PublishTopUpSucceeded(_ context.Context, _, txID uuid.UUID, _ int64, _, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.succeeded = append(p.succeeded, txID)
	return nil
}

func (p *recordingPublisher) PublishTopUpFailed(_ context.Context, _, txID uuid.UUID, _ int64, _ string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failed = append(p.failed, txID)
	return nil
}

func newTopUpTestService(f *fakeTopUpStore) (*Service, *recordingPublisher) {
	pub := &recordingPublisher{}
	return &Service{topUps: f, bank: bank.NewMockClient(), producer: pub, cfg: Config{TopUpExpirySeconds: 1800}}, pub
}

// The race the old code lost: the second confirm read "pending" before the
// first settled, passed the bank check too, and credited again.
func TestConfirmTopUp_SecondConfirmDoesNotCreditAgain(t *testing.T) {
	f := newFakeTopUpStore()
	svc, pub := newTopUpTestService(f)
	ctx := context.Background()
	user := uuid.New()
	id := f.addPending(user, 50000, time.Now())

	first, err := svc.ConfirmTopUp(ctx, user, id, "UPI-1")
	if err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	f.stalePending = 1 // the second confirm's first read predates the settle
	second, err := svc.ConfirmTopUp(ctx, user, id, "UPI-1")
	if err != nil {
		t.Fatalf("second confirm: %v", err)
	}

	if got := f.available[user]; got != 50000 {
		t.Fatalf("available after two confirms = %d paise, want 50000 (credited once)", got)
	}
	if len(pub.succeeded) != 1 {
		t.Fatalf("top-up succeeded events = %d, want 1", len(pub.succeeded))
	}
	if first.ID != id || second.ID != id || second.Status != "succeeded" {
		t.Fatalf("both confirms should return the settled top-up row; got %s/%s", second.ID, second.Status)
	}
}

func TestConfirmTopUp_ConcurrentConfirmsCreditOnce(t *testing.T) {
	f := newFakeTopUpStore()
	svc, pub := newTopUpTestService(f)
	ctx := context.Background()
	user := uuid.New()
	id := f.addPending(user, 12345, time.Now())
	const racers = 8
	f.stalePending = racers

	var wg sync.WaitGroup
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.ConfirmTopUp(ctx, user, id, "UPI-RACE"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("confirm: %v", err)
	}
	if got := f.available[user]; got != 12345 {
		t.Fatalf("available after %d racing confirms = %d paise, want 12345", racers, got)
	}
	if got := f.pendingIn[user]; got != 0 {
		t.Fatalf("pending_in = %d paise, want 0", got)
	}
	if len(pub.succeeded) != 1 {
		t.Fatalf("top-up succeeded events = %d, want 1", len(pub.succeeded))
	}
}

func TestConfirmTopUp_ExpiredBeforeSettleIsRefused(t *testing.T) {
	f := newFakeTopUpStore()
	svc, pub := newTopUpTestService(f)
	user := uuid.New()
	id := f.addPending(user, 1000, time.Now())
	f.setStatus(id, "failed") // the expirer won after our first read
	f.stalePending = 1

	if _, err := svc.ConfirmTopUp(context.Background(), user, id, "UPI-LATE"); err == nil {
		t.Fatalf("confirm of an expired top-up should fail")
	}
	if f.available[user] != 0 || len(pub.succeeded) != 0 {
		t.Fatalf("expired top-up must not credit (available=%d events=%d)", f.available[user], len(pub.succeeded))
	}
}

func TestConfirmTopUp_ReusedUPIRefIsRefused(t *testing.T) {
	f := newFakeTopUpStore()
	f.settleErr = store.ErrUPIRefAlreadyUsed
	svc, pub := newTopUpTestService(f)
	user := uuid.New()
	id := f.addPending(user, 1000, time.Now())

	_, err := svc.ConfirmTopUp(context.Background(), user, id, "UPI-USED")
	if err == nil {
		t.Fatalf("a reused UPI reference must be refused")
	}
	if !strings.HasPrefix(err.Error(), "invalid: ") {
		t.Fatalf("a reused ref should answer 400 (invalid: prefix); got %q", err)
	}
	if len(pub.succeeded) != 0 || f.available[user] != 0 {
		t.Fatalf("a refused confirm must not credit or publish")
	}
}

func TestExpireStaleTopUps_SkipsRowsAConfirmSettled(t *testing.T) {
	f := newFakeTopUpStore()
	svc, pub := newTopUpTestService(f)
	ctx := context.Background()
	user := uuid.New()
	old := time.Now().Add(-2 * time.Hour)
	stale := f.addPending(user, 700, old)
	won := f.addPending(user, 300, old)

	// The expirer lists both; the confirm for `won` lands right after.
	listed, _ := f.ListPendingTopUpsOlderThan(ctx, time.Now(), 200)
	if len(listed) != 2 {
		t.Fatalf("setup: listed %d", len(listed))
	}
	if ok, _ := f.SettleTopUp(ctx, won, "UPI-WON"); !ok {
		t.Fatal("setup: settle")
	}
	svc.topUps = &listSnapshot{fakeTopUpStore: f, listed: listed}

	n, err := svc.ExpireStaleTopUps(ctx)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n != 1 {
		t.Fatalf("expired %d, want 1", n)
	}
	if len(pub.failed) != 1 || pub.failed[0] != stale {
		t.Fatalf("failed events = %v, want only %s", pub.failed, stale)
	}
	if f.available[user] != 300 || f.pendingIn[user] != 0 {
		t.Fatalf("available=%d pending_in=%d, want 300 and 0", f.available[user], f.pendingIn[user])
	}
}

// listSnapshot replays a listing taken before a confirm settled one of its
// rows, as the expirer sees it.
type listSnapshot struct {
	*fakeTopUpStore
	listed []store.Transaction
}

func (l *listSnapshot) ListPendingTopUpsOlderThan(context.Context, time.Time, int) ([]store.Transaction, error) {
	return l.listed, nil
}
