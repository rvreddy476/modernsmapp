package store

// Integration tests for the one-transaction settle paths. Like the rest of
// this package they skip unless TEST_PG_DSN is set (CI, wallet_it_test).

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// pendingTopUp creates a balance row and a pending top-up with its
// pending_in, the state StartTopUp leaves behind.
func pendingTopUp(t *testing.T, s *Store, uid uuid.UUID, amount int64, upiRef string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	if _, err := s.EnsureBalance(ctx, uid, "ref-"+uid.String()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	tx, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
		UserID: uid, Type: "top_up", Direction: "credit", AmountPaise: amount,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.AdjustPendingIn(ctx, nil, uid, amount); err != nil {
		t.Fatalf("pending_in: %v", err)
	}
	if upiRef != "" {
		if err := s.SetUPIRef(ctx, tx.ID, upiRef); err != nil {
			t.Fatalf("upi ref: %v", err)
		}
	}
	return tx.ID
}

func TestSettleTopUp_ConcurrentCallsCreditOnce(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	id := pendingTopUp(t, s, uid, 7500, "UPI-"+uuid.NewString())

	const racers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.SettleTopUp(ctx, id, "BANK")
			if err != nil {
				t.Errorf("settle: %v", err)
				return
			}
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d calls reported settled, want exactly 1", wins)
	}
	b, _ := s.GetBalance(ctx, uid)
	if b.AvailablePaise != 7500 || b.PendingInPaise != 0 {
		t.Fatalf("available=%d pending_in=%d, want 7500 and 0", b.AvailablePaise, b.PendingInPaise)
	}
}

func TestSettleTopUp_ReusedUPIRefCreditsOnce(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	ref := "UPI-" + uuid.NewString()
	first := pendingTopUp(t, s, uid, 1000, ref)
	second := pendingTopUp(t, s, uid, 1000, ref)

	if ok, err := s.SettleTopUp(ctx, first, "BANK"); err != nil || !ok {
		t.Fatalf("first settle: %v %v", ok, err)
	}
	ok, err := s.SettleTopUp(ctx, second, "BANK")
	if !errors.Is(err, ErrUPIRefAlreadyUsed) || ok {
		t.Fatalf("second top-up with the same UPI ref: ok=%v err=%v, want ErrUPIRefAlreadyUsed", ok, err)
	}
	b, _ := s.GetBalance(ctx, uid)
	if b.AvailablePaise != 1000 {
		t.Fatalf("available=%d, want 1000 (one UPI payment, one credit)", b.AvailablePaise)
	}
}

func TestExpireTopUp_AfterSettleLeavesBalanceAlone(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	id := pendingTopUp(t, s, uid, 4000, "UPI-"+uuid.NewString())
	// A second pending top-up keeps pending_in non-zero, so a wrong release
	// would show (GREATEST(...,0) would hide it otherwise).
	pendingTopUp(t, s, uid, 600, "")

	if ok, err := s.SettleTopUp(ctx, id, "BANK"); err != nil || !ok {
		t.Fatalf("settle: %v %v", ok, err)
	}
	expired, err := s.ExpireTopUp(ctx, id, "expired_no_upi_credit")
	if err != nil || expired {
		t.Fatalf("expire after settle: expired=%v err=%v, want false and nil", expired, err)
	}
	b, _ := s.GetBalance(ctx, uid)
	if b.AvailablePaise != 4000 || b.PendingInPaise != 600 {
		t.Fatalf("available=%d pending_in=%d, want 4000 and 600", b.AvailablePaise, b.PendingInPaise)
	}
	got, _ := s.GetTransaction(ctx, uid, id)
	if got.Status != "succeeded" {
		t.Fatalf("status=%s, want succeeded", got.Status)
	}
}

func TestExpireTopUp_ReleasesPendingInOnce(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	id := pendingTopUp(t, s, uid, 900, "")
	pendingTopUp(t, s, uid, 100, "")

	if ok, err := s.ExpireTopUp(ctx, id, "expired_no_upi_credit"); err != nil || !ok {
		t.Fatalf("expire: %v %v", ok, err)
	}
	if ok, err := s.ExpireTopUp(ctx, id, "expired_no_upi_credit"); err != nil || ok {
		t.Fatalf("second expire: %v %v, want false", ok, err)
	}
	if ok, err := s.SettleTopUp(ctx, id, "BANK"); err != nil || ok {
		t.Fatalf("settle after expire: %v %v, want false", ok, err)
	}
	b, _ := s.GetBalance(ctx, uid)
	if b.AvailablePaise != 0 || b.PendingInPaise != 100 {
		t.Fatalf("available=%d pending_in=%d, want 0 and 100", b.AvailablePaise, b.PendingInPaise)
	}
}

func TestSetSynced_DropsAStaleBankRead(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	seen, err := s.EnsureBalance(ctx, uid, "ref")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// A credit lands while the bank read is in flight.
	if err := s.CreditAvailable(ctx, nil, uid, 2500); err != nil {
		t.Fatalf("credit: %v", err)
	}
	applied, err := s.SetSynced(ctx, uid, 0, seen.UpdatedAt)
	if err != nil || applied {
		t.Fatalf("stale sync: applied=%v err=%v, want false", applied, err)
	}
	b, _ := s.GetBalance(ctx, uid)
	if b.AvailablePaise != 2500 {
		t.Fatalf("available=%d, want 2500: a stale bank figure overwrote a credit", b.AvailablePaise)
	}
	applied, err = s.SetSynced(ctx, uid, 2500, b.UpdatedAt)
	if err != nil || !applied {
		t.Fatalf("fresh sync: applied=%v err=%v, want true", applied, err)
	}
}

func TestClaimIdempotencyKeyTx_ReturnsTheEarlierRow(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	key := "k-" + uuid.NewString()

	pgtx, err := s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := s.ClaimIdempotencyKeyTx(ctx, pgtx, uid, "merchant_pay", key)
	if err != nil || prior != nil {
		t.Fatalf("first claim: prior=%v err=%v, want nil", prior, err)
	}
	k := key
	row, err := s.InsertTransaction(ctx, pgtx, CreateTransactionInput{
		UserID: uid, Type: "merchant_pay", Direction: "debit", AmountPaise: 100, IdempotencyKey: &k,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pgtx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	pgtx2, err := s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pgtx2.Rollback(ctx) }()
	prior, err = s.ClaimIdempotencyKeyTx(ctx, pgtx2, uid, "merchant_pay", key)
	if err != nil || prior == nil || prior.ID != row.ID {
		t.Fatalf("second claim: prior=%v err=%v, want row %s", prior, err, row.ID)
	}
}

func TestLockForRefundTx_SumsEarlierRefunds(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	orig, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
		UserID: uid, Type: "merchant_pay", Direction: "debit", AmountPaise: 5000, Status: "succeeded",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, amt := range []int64{1200, 800} {
		if _, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
			UserID: uid, Type: "refund", Direction: "credit", AmountPaise: amt, Status: "succeeded",
			Metadata: map[string]any{"original_transaction_id": orig.ID.String()},
		}); err != nil {
			t.Fatal(err)
		}
	}
	pgtx, err := s.DB().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pgtx.Rollback(ctx) }()
	got, refunded, err := s.LockForRefundTx(ctx, pgtx, orig.ID)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	if got.ID != orig.ID || refunded != 2000 {
		t.Fatalf("refunded=%d, want 2000", refunded)
	}
}
