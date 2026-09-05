//go:build integration

package service

// MRC-3 — the idempotency fingerprint is exact and fails closed.
//
// The gap: the owner comparison read
//
//	want.owner != "" && in.OwnerDomain != "" && want.owner != in.OwnerDomain
//
// so a conflict against a row whose `owner_domain` is EMPTY was accepted. A
// caller with a valid identity inherited an intent whose authority owner is
// unknown and could then drive it — the exact ownerless state B4 refuses in
// ownsIntent and CreateRefundCommand. This was the one door left open in that
// wall.
//
// The second property proved here is MRC-3.4: a fingerprint mismatch must
// reach NO PSP. The service creates the local row before contacting the
// provider, so the refusal has to happen before the first call — asserted
// with a counting provider rather than by reading the code.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// countingProvider records every PSP call. It embeds the interface so an
// unexpected call panics loudly rather than silently returning a zero value.
type countingProvider struct {
	gateway.Provider
	mu      sync.Mutex
	creates int
}

func (c *countingProvider) Name() string { return "counting" }

func (c *countingProvider) CreateOrder(_ context.Context, amount gateway.Money, key string, _ map[string]string) (gateway.ProviderOrder, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.creates++
	return gateway.ProviderOrder{
		ProviderOrderID: "order_" + uuid.NewString()[:10],
		Amount:          amount,
	}, nil
}

func (c *countingProvider) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.creates
}

func baseInput(key, owner string) InitiateInput {
	return InitiateInput{
		PayerID:        uuid.New(),
		PayeeID:        uuid.New(),
		ReferenceType:  "order",
		ReferenceID:    uuid.New(),
		AmountMinor:    118000,
		Currency:       "INR",
		Method:         "upi",
		IdempotencyKey: key,
		OwnerDomain:    owner,
	}
}

func newCountingSvc(t *testing.T) (*Service, *countingProvider) {
	t.Helper()
	p := &countingProvider{}
	return New(postgres.New(recPool), nil).WithProvider(p), p
}

// ─── MRC-3.2: an ownerless stored row is refused ─────────────────────

// THE regression. Seed an intent with a NULL owner_domain — the legacy state
// migration 007's backfill reduces but cannot guarantee away, and which the
// column still permits — then have a properly-identified caller replay the
// key with an otherwise identical tuple.
func TestFingerprintRefusesAnOwnerlessStoredRow(t *testing.T) {
	ctx := context.Background()
	store := postgres.New(recPool)
	key := "idem-ownerless-" + uuid.NewString()

	payer, payee, ref := uuid.New(), uuid.New(), uuid.New()
	if _, err := recPool.Exec(ctx, `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, owner_domain, idempotency_key)
		VALUES ($1,$2,$3,'order',$4,1180,118000,'INR','upi','pending','razorpay',NULL,$5)`,
		uuid.New(), payer, payee, ref, key); err != nil {
		t.Fatalf("seeding an ownerless intent: %v", err)
	}

	// Everything else matches exactly; only the stored owner is blank.
	_, err := store.CreateIntent(ctx, postgres.PaymentIntent{
		PayerID: payer, PayeeID: payee,
		ReferenceType: "order", ReferenceID: ref,
		Amount: 1180, AmountMinorRaw: 118000,
		Currency: "INR", Method: "upi",
		OwnerDomain: "commerce", IdempotencyKey: key,
	})
	if !errors.Is(err, postgres.ErrIdempotencyFingerprint) {
		t.Fatalf("got %v, want ErrIdempotencyFingerprint — a caller must never inherit an "+
			"intent whose authority owner is unknown", err)
	}
	if !strings.Contains(err.Error(), "no owner domain") {
		t.Errorf("error should name the ownerless row, got %q", err)
	}
}

// MRC-3.2, the other blank side: a caller with no identity is refused even
// against a properly owned row.
func TestFingerprintRefusesAnUnidentifiedCaller(t *testing.T) {
	ctx := context.Background()
	store := postgres.New(recPool)
	key := "idem-nocaller-" + uuid.NewString()

	req := postgres.PaymentIntent{
		PayerID: uuid.New(), PayeeID: uuid.New(),
		ReferenceType: "order", ReferenceID: uuid.New(),
		Amount: 1180, AmountMinorRaw: 118000,
		Currency: "INR", Method: "upi",
		OwnerDomain: "commerce", IdempotencyKey: key,
	}
	if _, err := store.CreateIntent(ctx, req); err != nil {
		t.Fatalf("first create: %v", err)
	}

	anon := req
	anon.OwnerDomain = ""
	if _, err := store.CreateIntent(ctx, anon); !errors.Is(err, postgres.ErrIdempotencyFingerprint) {
		t.Fatalf("got %v, want ErrIdempotencyFingerprint for a caller with no identity", err)
	}
}

// ─── MRC-3.4: a mismatch reaches no PSP ──────────────────────────────

func TestFingerprintMismatchNeverCallsTheProvider(t *testing.T) {
	ctx := context.Background()
	svc, prov := newCountingSvc(t)
	key := "idem-nopsp-" + uuid.NewString()

	first := baseInput(key, "commerce")
	if _, err := svc.InitiatePayment(ctx, first); err != nil {
		t.Fatalf("first initiate: %v", err)
	}
	afterFirst := prov.calls()
	if afterFirst != 1 {
		t.Fatalf("the first create made %d provider calls, want 1", afterFirst)
	}

	// Every dimension of the fingerprint, each as its own mismatched replay.
	mismatches := map[string]func(InitiateInput) InitiateInput{
		"owner":     func(i InitiateInput) InitiateInput { i.OwnerDomain = "food"; return i },
		"reference": func(i InitiateInput) InitiateInput { i.ReferenceID = uuid.New(); return i },
		"payer":     func(i InitiateInput) InitiateInput { i.PayerID = uuid.New(); return i },
		"payee":     func(i InitiateInput) InitiateInput { i.PayeeID = uuid.New(); return i },
		"amount":    func(i InitiateInput) InitiateInput { i.AmountMinor = 1180000; return i },
		"currency":  func(i InitiateInput) InitiateInput { i.Currency = "USD"; return i },
		"method":    func(i InitiateInput) InitiateInput { i.Method = "card"; return i },
	}
	for name, mutate := range mismatches {
		t.Run(name, func(t *testing.T) {
			before := prov.calls()
			_, err := svc.InitiatePayment(ctx, mutate(first))
			if !errors.Is(err, postgres.ErrIdempotencyFingerprint) {
				t.Fatalf("mismatched %s: got %v, want ErrIdempotencyFingerprint", name, err)
			}
			if after := prov.calls(); after != before {
				t.Fatalf("mismatched %s reached the PSP (%d → %d calls); the refusal must "+
					"happen before any provider contact", name, before, after)
			}
		})
	}
}

// MRC-3.5: a genuine identical retry still returns the same intent AND the
// same provider reference. Failing closed must not break idempotency.
func TestGenuineRetryReturnsTheSameIntentAndReference(t *testing.T) {
	ctx := context.Background()
	svc, prov := newCountingSvc(t)
	key := "idem-retry-" + uuid.NewString()
	in := baseInput(key, "commerce")

	first, err := svc.InitiatePayment(ctx, in)
	if err != nil {
		t.Fatalf("first initiate: %v", err)
	}
	second, err := svc.InitiatePayment(ctx, in)
	if err != nil {
		t.Fatalf("a genuine retry must succeed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("retry returned intent %s, want %s", second.ID, first.ID)
	}
	if second.ProviderRef != first.ProviderRef || second.ProviderRef == "" {
		t.Fatalf("retry provider reference = %q, want %q", second.ProviderRef, first.ProviderRef)
	}
	if n := prov.calls(); n != 1 {
		t.Fatalf("the provider was called %d times for one business request, want 1", n)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.payment_intents WHERE idempotency_key=$1`, key); n != 1 {
		t.Fatalf("intent rows = %d, want 1", n)
	}
}

// MRC-3.4: the refusal must not hand back the existing intent body.
func TestFingerprintMismatchReturnsNoIntentBody(t *testing.T) {
	ctx := context.Background()
	store := postgres.New(recPool)
	key := "idem-nobody-" + uuid.NewString()

	req := postgres.PaymentIntent{
		PayerID: uuid.New(), PayeeID: uuid.New(),
		ReferenceType: "order", ReferenceID: uuid.New(),
		Amount: 1180, AmountMinorRaw: 118000,
		Currency: "INR", Method: "upi",
		OwnerDomain: "commerce", IdempotencyKey: key,
	}
	if _, err := store.CreateIntent(ctx, req); err != nil {
		t.Fatalf("first create: %v", err)
	}

	other := req
	other.OwnerDomain = "food"
	res, err := store.CreateIntent(ctx, other)
	if !errors.Is(err, postgres.ErrIdempotencyFingerprint) {
		t.Fatalf("got %v, want ErrIdempotencyFingerprint", err)
	}
	if res != nil {
		t.Fatalf("a fingerprint refusal returned an intent body (%+v); it discloses another "+
			"domain's payment", res.Intent)
	}
}
