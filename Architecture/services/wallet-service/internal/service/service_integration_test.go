// Service integration tests. Skipped unless TEST_PG_DSN is set.
package service

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/wallet-service/database"
	"github.com/atpost/wallet-service/internal/bank"
	"github.com/atpost/wallet-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestService spins up a Service backed by TEST_PG_DSN, the wallet schema
// freshly bootstrapped, and a fresh MockClient. Returns the service + the
// MockClient so tests can arm sentinels (FailNext, SeedBalance).
func newTestService(t *testing.T) (*Service, *bank.MockClient, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping wallet service integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := database.BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	mock := bank.NewMockClient()
	svc := New(store.New(pool), mock, Config{
		PartnerBankVPA: "atpostwallet@partnerbank",
		AppDisplayName: "AtPost Wallet",
		PoolBankRef:    "mock-ppi-pool",
	})
	return svc, mock, func() { pool.Close() }
}

func TestStartTopUp_HappyPath(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	res, err := svc.StartTopUp(ctx, uid, 50000, "k1")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.UPIIntentURL == "" {
		t.Fatalf("expected UPI intent URL")
	}
	if res.AmountPaise != 50000 {
		t.Fatalf("amount mismatch")
	}
	if res.Status != "pending" {
		t.Fatalf("expected pending; got %s", res.Status)
	}
}

func TestStartTopUp_Idempotent(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	first, err := svc.StartTopUp(ctx, uid, 1000, "same-key")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.StartTopUp(ctx, uid, 1000, "same-key")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.TransactionID != second.TransactionID {
		t.Fatalf("idempotent calls should return same transaction id; %s vs %s", first.TransactionID, second.TransactionID)
	}
}

func TestStartTopUp_RejectsZeroAmount(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	if _, err := svc.StartTopUp(context.Background(), uuid.New(), 0, "k"); err == nil {
		t.Fatalf("expected rejection of zero amount")
	}
}

func TestStartTopUp_RejectsMissingIdempotencyKey(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	if _, err := svc.StartTopUp(context.Background(), uuid.New(), 1000, ""); err == nil {
		t.Fatalf("expected rejection of missing idempotency key")
	}
}

func TestStartTopUp_KYCLimitEnforced(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	// minimal KYC default cap is 10k INR = 1_000_000 paise
	if _, err := svc.StartTopUp(context.Background(), uuid.New(), 10000000, "k"); err == nil {
		t.Fatalf("expected KYC tier-limit rejection on 1 lakh top-up")
	}
}

func TestConfirmTopUp_VerifiesAndCredits(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	start, err := svc.StartTopUp(ctx, uid, 5000, "kk")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	confirmed, err := svc.ConfirmTopUp(ctx, uid, start.TransactionID, "UPI-REAL-REF")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.Status != "succeeded" {
		t.Fatalf("expected succeeded; got %s", confirmed.Status)
	}

	balance, _ := svc.GetBalance(ctx, uid)
	if balance.AvailablePaise != 5000 {
		t.Fatalf("expected balance=5000; got %d", balance.AvailablePaise)
	}
}

func TestConfirmTopUp_BankNotYetVerified(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	start, _ := svc.StartTopUp(ctx, uid, 5000, "kk2")
	tx, err := svc.ConfirmTopUp(ctx, uid, start.TransactionID, "missing-upi")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if tx.Status != "pending" {
		t.Fatalf("expected pending while bank says not yet; got %s", tx.Status)
	}
}

func TestConfirmTopUp_Idempotent(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	start, _ := svc.StartTopUp(ctx, uid, 5000, "kk3")
	first, err := svc.ConfirmTopUp(ctx, uid, start.TransactionID, "UPI-X")
	if err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	second, err := svc.ConfirmTopUp(ctx, uid, start.TransactionID, "UPI-X")
	if err != nil {
		t.Fatalf("second confirm: %v", err)
	}
	if first.Status != second.Status {
		t.Fatalf("status mismatch on idempotent confirm")
	}
	balance, _ := svc.GetBalance(ctx, uid)
	if balance.AvailablePaise != 5000 {
		t.Fatalf("expected single credit of 5000; got %d", balance.AvailablePaise)
	}
}

func TestSend_HappyPath_InAtPost(t *testing.T) {
	svc, mock, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	sender := uuid.New()
	recipient := uuid.New()

	// Pre-fund the sender via top-up flow.
	start, _ := svc.StartTopUp(ctx, sender, 10000, "topup-k")
	if _, err := svc.ConfirmTopUp(ctx, sender, start.TransactionID, "UPI-1"); err != nil {
		t.Fatalf("topup: %v", err)
	}
	// Ensure recipient has balance row + bank ref + funds at the bank for transfer.
	rb, _ := svc.GetBalance(ctx, recipient)
	sb, _ := svc.GetBalance(ctx, sender)
	mock.SeedBalance(sb.BankAccountRef, 10000)
	_ = rb

	res, err := svc.Send(ctx, sender, SendRequest{
		RecipientUserID: &recipient,
		AmountPaise:     3000,
		IdempotencyKey:  "send-k",
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if res.Status != "succeeded" {
		t.Fatalf("expected succeeded; got %s", res.Status)
	}

	sb2, _ := svc.GetBalance(ctx, sender)
	rb2, _ := svc.GetBalance(ctx, recipient)
	if sb2.AvailablePaise != 7000 {
		t.Fatalf("expected sender 7000; got %d", sb2.AvailablePaise)
	}
	if rb2.AvailablePaise != 3000 {
		t.Fatalf("expected recipient 3000; got %d", rb2.AvailablePaise)
	}
}

func TestSend_Insufficient(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	sender := uuid.New()
	recipient := uuid.New()
	// Both have zero balance; sender is empty.
	if _, err := svc.GetBalance(ctx, sender); err != nil {
		t.Fatalf("ensure sender: %v", err)
	}
	if _, err := svc.GetBalance(ctx, recipient); err != nil {
		t.Fatalf("ensure recipient: %v", err)
	}
	if _, err := svc.Send(ctx, sender, SendRequest{
		RecipientUserID: &recipient, AmountPaise: 100, IdempotencyKey: "k",
	}); err == nil {
		t.Fatalf("expected insufficient-balance error")
	}
}

func TestSend_KYCLimit(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	sender := uuid.New()
	recipient := uuid.New()
	// minimal cap = 10k INR = 1_000_000 paise — try to send 5 lakh.
	if _, err := svc.Send(ctx, sender, SendRequest{
		RecipientUserID: &recipient, AmountPaise: 50000000, IdempotencyKey: "kyc-k",
	}); err == nil {
		t.Fatalf("expected KYC tier-limit rejection")
	}
}

func TestSend_BankFailure_ReversesDebit(t *testing.T) {
	svc, mock, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	sender := uuid.New()
	recipient := uuid.New()

	// Fund sender via top-up.
	start, _ := svc.StartTopUp(ctx, sender, 10000, "topup-bf")
	if _, err := svc.ConfirmTopUp(ctx, sender, start.TransactionID, "UPI-bf"); err != nil {
		t.Fatalf("topup: %v", err)
	}
	rb, _ := svc.GetBalance(ctx, recipient)
	_ = rb
	sb, _ := svc.GetBalance(ctx, sender)
	mock.SeedBalance(sb.BankAccountRef, 10000)
	mock.FailNext(sb.BankAccountRef)

	if _, err := svc.Send(ctx, sender, SendRequest{
		RecipientUserID: &recipient, AmountPaise: 4000, IdempotencyKey: "bank-fail-k",
	}); err == nil {
		t.Fatalf("expected bank failure error")
	}
	sb2, _ := svc.GetBalance(ctx, sender)
	if sb2.AvailablePaise != 10000 {
		t.Fatalf("expected debit reversed → 10000; got %d", sb2.AvailablePaise)
	}
}

func TestSend_Idempotent(t *testing.T) {
	svc, mock, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	sender := uuid.New()
	recipient := uuid.New()

	start, _ := svc.StartTopUp(ctx, sender, 10000, "tu-idem")
	if _, err := svc.ConfirmTopUp(ctx, sender, start.TransactionID, "UPI-idem"); err != nil {
		t.Fatalf("tu: %v", err)
	}
	_, _ = svc.GetBalance(ctx, recipient)
	sb, _ := svc.GetBalance(ctx, sender)
	mock.SeedBalance(sb.BankAccountRef, 10000)

	first, err := svc.Send(ctx, sender, SendRequest{
		RecipientUserID: &recipient, AmountPaise: 2000, IdempotencyKey: "send-idem",
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.Send(ctx, sender, SendRequest{
		RecipientUserID: &recipient, AmountPaise: 2000, IdempotencyKey: "send-idem",
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.TransactionID != second.TransactionID {
		t.Fatalf("idempotent send must return same transaction id")
	}
	sb2, _ := svc.GetBalance(ctx, sender)
	if sb2.AvailablePaise != 8000 {
		t.Fatalf("expected single debit (8000); got %d", sb2.AvailablePaise)
	}
}

func TestSend_RejectsSelf(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	id := uuid.New()
	if _, err := svc.Send(context.Background(), id, SendRequest{
		RecipientUserID: &id, AmountPaise: 100, IdempotencyKey: "k",
	}); err == nil {
		t.Fatalf("expected rejection of self-send")
	}
}

func TestMerchantDebit_Idempotent(t *testing.T) {
	svc, mock, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	start, _ := svc.StartTopUp(ctx, uid, 10000, "tu-md")
	if _, err := svc.ConfirmTopUp(ctx, uid, start.TransactionID, "UPI-md"); err != nil {
		t.Fatalf("tu: %v", err)
	}
	sb, _ := svc.GetBalance(ctx, uid)
	mock.SeedBalance(sb.BankAccountRef, 10000)

	first, err := svc.MerchantDebit(ctx, uid, 1000, "pulse", "sub-123", "md-key")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := svc.MerchantDebit(ctx, uid, 1000, "pulse", "sub-123", "md-key")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.TransactionID != second.TransactionID {
		t.Fatalf("idempotent merchant debit must return same id")
	}
	sb2, _ := svc.GetBalance(ctx, uid)
	if sb2.AvailablePaise != 9000 {
		t.Fatalf("expected one debit; got balance %d", sb2.AvailablePaise)
	}
}

func TestRefund_CreditsBack(t *testing.T) {
	svc, mock, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	start, _ := svc.StartTopUp(ctx, uid, 10000, "tu-r")
	if _, err := svc.ConfirmTopUp(ctx, uid, start.TransactionID, "UPI-r"); err != nil {
		t.Fatalf("tu: %v", err)
	}
	sb, _ := svc.GetBalance(ctx, uid)
	mock.SeedBalance(sb.BankAccountRef, 10000)

	debit, err := svc.MerchantDebit(ctx, uid, 2000, "pulse", "sub-456", "k-r")
	if err != nil {
		t.Fatalf("debit: %v", err)
	}
	_, err = svc.Refund(ctx, debit.TransactionID, 2000, "user_cancel")
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	sb2, _ := svc.GetBalance(ctx, uid)
	if sb2.AvailablePaise != 10000 {
		t.Fatalf("expected refund credited back to 10000; got %d", sb2.AvailablePaise)
	}
}

func TestExpireStaleTopUps_FlipsAndRefundsPending(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	// Use a tiny cutoff so the row is "stale" immediately.
	svc.cfg.TopUpExpirySeconds = 0
	if _, err := svc.StartTopUp(ctx, uid, 5000, "k-stale"); err != nil {
		t.Fatalf("start: %v", err)
	}
	expired, err := svc.ExpireStaleTopUps(ctx)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if expired < 1 {
		t.Fatalf("expected at least 1 expired; got %d", expired)
	}
	b, _ := svc.GetBalance(ctx, uid)
	if b.PendingInPaise != 0 {
		t.Fatalf("expected pending_in refunded → 0; got %d", b.PendingInPaise)
	}
}

func TestKYC_AadhaarFlow_UpgradesTier(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	if _, err := svc.GetBalance(ctx, uid); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	verifier := &stubVerifier{assertion: AadhaarAssertion{
		Reference:    "OPAQUE-REF",
		DocumentType: "AADHAAR-XML",
	}}
	rec, err := svc.CompleteAadhaar(ctx, uid, "code", "state", verifier)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if rec.Tier != "full" {
		t.Fatalf("expected full tier; got %s", rec.Tier)
	}
	b, _ := svc.GetBalance(ctx, uid)
	if b.MonthlyLimitPaise != 20000000 {
		t.Fatalf("expected 2 lakh limit on full KYC; got %d", b.MonthlyLimitPaise)
	}
}

func TestSubmitPAN_StoresMaskedOnly(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	rec, err := svc.SubmitPAN(context.Background(), uuid.New(), "ABCDE1234F")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if rec.PANMasked == nil {
		t.Fatalf("expected masked")
	}
	if *rec.PANMasked != "XXXXXX234F" {
		t.Fatalf("expected XXXXXX234F; got %s", *rec.PANMasked)
	}
}

func TestSubmitPAN_RejectsBadFormat(t *testing.T) {
	svc, _, cleanup := newTestService(t)
	defer cleanup()
	if _, err := svc.SubmitPAN(context.Background(), uuid.New(), "ABCD123"); err == nil {
		t.Fatalf("expected rejection")
	}
}
