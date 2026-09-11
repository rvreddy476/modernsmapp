package razorpayx_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/atpost/monetization-service/internal/client/razorpayx"
	"github.com/atpost/monetization-service/internal/client/razorpayx/razorpayxtest"
)

func newClient(t *testing.T) (*razorpayxtest.Server, *razorpayx.Client) {
	t.Helper()
	stub := razorpayxtest.NewServer()
	t.Cleanup(stub.Close)
	return stub, razorpayx.New(stub.Config("whsec"))
}

// EnsureContact looks the contact up by reference_id before creating
// one, so a creator never gets two contacts.
func TestEnsureContactReusesByReference(t *testing.T) {
	stub, c := newClient(t)
	ctx := context.Background()
	first, err := c.EnsureContact(ctx, "user-1", "Asha Creator", "asha@example.com")
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.EnsureContact(ctx, "user-1", "Asha Creator", "asha@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || first != second {
		t.Fatalf("contact ids = %q, %q; want one stable id", first, second)
	}
	if n := stub.Contacts(); n != 1 {
		t.Fatalf("contacts at the provider = %d, want 1", n)
	}
	if !strings.HasPrefix(first, "cont_") {
		t.Fatalf("contact id %q does not look like a RazorpayX contact id", first)
	}
}

func TestEnsureFundAccountAndValidation(t *testing.T) {
	stub, c := newClient(t)
	ctx := context.Background()
	contact, err := c.EnsureContact(ctx, "user-2", "Ravi", "")
	if err != nil {
		t.Fatal(err)
	}
	fa, err := c.EnsureFundAccount(ctx, contact, razorpayx.BankDetails{HolderName: "Ravi", AccountNumber: "765432123456789", IFSC: "HDFC0000053"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(fa, "fa_") || stub.FundAccounts() != 1 {
		t.Fatalf("fund account %q, count %d", fa, stub.FundAccounts())
	}
	v, err := c.ValidateFundAccount(ctx, fa)
	if err != nil {
		t.Fatal(err)
	}
	if v.Status != "completed" || v.AccountStatus != "active" {
		t.Fatalf("validation = %+v, want completed/active", v)
	}
	// A bad IFSC is refused by the provider at fund-account creation.
	if _, err := c.EnsureFundAccount(ctx, contact, razorpayx.BankDetails{HolderName: "Ravi", AccountNumber: "765432123456789", IFSC: "BAD"}); err == nil {
		t.Fatal("a malformed IFSC was accepted")
	} else if razorpayx.IsAmbiguous(err) {
		t.Fatalf("a 400 must be definitive, got ambiguous: %v", err)
	}
}

func TestCreatePayoutSendsIdempotencyKeyAndIsIdempotent(t *testing.T) {
	stub, c := newClient(t)
	ctx := context.Background()
	p1, err := c.CreatePayout(ctx, "payout:abc", 150_000, "fa_x", razorpayx.ModeFor(150_000), "payout:abc")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := c.CreatePayout(ctx, "payout:abc", 150_000, "fa_x", razorpayx.ModeFor(150_000), "payout:abc")
	if err != nil {
		t.Fatal(err)
	}
	if p1.ID != p2.ID || len(stub.PayoutsByReference("payout:abc")) != 1 {
		t.Fatalf("same idempotency key produced %s and %s", p1.ID, p2.ID)
	}
	if keys := stub.IdempotencyKeys(); len(keys) != 2 || keys[0] != "payout:abc" {
		t.Fatalf("X-Payout-Idempotency headers = %v", keys)
	}
	if p1.Mode != "IMPS" || p1.AmountPaise != 150_000 || p1.ReferenceID != "payout:abc" || !strings.HasPrefix(p1.ID, "pout_") {
		t.Fatalf("payout = %+v", p1)
	}
	got, err := c.FetchPayout(ctx, p1.ID)
	if err != nil || got.ID != p1.ID {
		t.Fatalf("FetchPayout = %+v, %v", got, err)
	}
	list, err := c.ListPayoutsByReference(ctx, "payout:abc")
	if err != nil || len(list) != 1 || list[0].ID != p1.ID {
		t.Fatalf("ListPayoutsByReference = %+v, %v", list, err)
	}
	none, err := c.ListPayoutsByReference(ctx, "payout:none")
	if err != nil || len(none) != 0 {
		t.Fatalf("ListPayoutsByReference(none) = %+v, %v", none, err)
	}
}

func TestCreatePayoutRequiresKeyAndFundAccount(t *testing.T) {
	_, c := newClient(t)
	if _, err := c.CreatePayout(context.Background(), "", 100, "fa_x", "IMPS", "r"); err == nil {
		t.Fatal("empty idempotency key accepted")
	}
	if _, err := c.CreatePayout(context.Background(), "k", 100, "", "IMPS", "r"); err == nil {
		t.Fatal("empty fund account accepted")
	}
	if _, err := c.CreatePayout(context.Background(), "k", 0, "fa_x", "IMPS", "r"); err == nil {
		t.Fatal("zero amount accepted")
	}
}

func TestModeFor(t *testing.T) {
	cases := map[int64]string{
		100:             "IMPS",
		19_999_999:      "IMPS",
		20_000_000:      "NEFT", // Rs 2,00,000.00 exactly is not "under"
		20_000_001:      "NEFT",
		1_000_000_000_0: "NEFT",
	}
	for amount, want := range cases {
		if got := razorpayx.ModeFor(amount); got != want {
			t.Errorf("ModeFor(%d) = %s, want %s", amount, got, want)
		}
	}
}

func TestMapStatus(t *testing.T) {
	cases := map[string]string{
		"queued": "processing", "pending": "processing", "processing": "processing",
		"processed": "paid",
		"rejected": "failed", "cancelled": "failed", "failed": "failed",
		"reversed": "reversed",
	}
	for in, want := range cases {
		got, ok := razorpayx.MapStatus(in)
		if !ok || got != want {
			t.Errorf("MapStatus(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := razorpayx.MapStatus("created"); ok {
		t.Error("an unknown provider status must not map")
	}
}

func TestAmbiguityClassification(t *testing.T) {
	stub, c := newClient(t)
	ctx := context.Background()
	for _, status := range []int{500, 502, 503, 504, 408, 429} {
		stub.FailNextCreatePayout(status, false)
		_, err := c.CreatePayout(ctx, "k-"+http.StatusText(status), 100, "fa_x", "IMPS", "r")
		if err == nil || !razorpayx.IsAmbiguous(err) {
			t.Errorf("status %d: err=%v, want ambiguous", status, err)
		}
	}
	for _, status := range []int{400, 401, 404, 422} {
		stub.FailNextCreatePayout(status, false)
		_, err := c.CreatePayout(ctx, "k2-"+http.StatusText(status), 100, "fa_x", "IMPS", "r")
		if err == nil || razorpayx.IsAmbiguous(err) {
			t.Errorf("status %d: err=%v, want definitive", status, err)
		}
	}
	// A dead endpoint is ambiguous too.
	dead := razorpayx.New(razorpayx.Config{KeyID: "k", KeySecret: "s", AccountNumber: "1", WebhookSecret: "w", BaseURL: "http://127.0.0.1:1"})
	_, err := dead.CreatePayout(ctx, "k3", 100, "fa_x", "IMPS", "r")
	if err == nil || !razorpayx.IsAmbiguous(err) {
		t.Errorf("connection refused: err=%v, want ambiguous", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = c.CreatePayout(cancelled, "k4", 100, "fa_x", "IMPS", "r")
	if err == nil || !razorpayx.IsAmbiguous(err) || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled context: err=%v, want ambiguous wrapping context.Canceled", err)
	}
}

func TestVerifyWebhook(t *testing.T) {
	stub, _ := newClient(t)
	p := razorpayx.Payout{ID: "pout_1", Status: "processed", UTR: "U1", AmountPaise: 500, ReferenceID: "payout:x"}
	body, hdr := stub.WebhookRequest("whsec", "evt_1", "payout.processed", p)

	ev, err := razorpayx.VerifyWebhook("whsec", hdr, body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ID != "evt_1" || ev.Type != "payout.processed" || ev.Payout.ID != "pout_1" || ev.Payout.UTR != "U1" || ev.Payout.Status != "processed" || ev.Payout.AmountPaise != 500 {
		t.Fatalf("event = %+v", ev)
	}

	if _, err := razorpayx.VerifyWebhook("other-secret", hdr, body); !errors.Is(err, razorpayx.ErrSignatureInvalid) {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := razorpayx.VerifyWebhook("", hdr, body); !errors.Is(err, razorpayx.ErrSignatureInvalid) {
		t.Fatalf("empty secret must fail closed: %v", err)
	}
	tampered := append([]byte{}, body...)
	tampered[len(tampered)-2] ^= 1
	if _, err := razorpayx.VerifyWebhook("whsec", hdr, tampered); !errors.Is(err, razorpayx.ErrSignatureInvalid) {
		t.Fatalf("tampered body: %v", err)
	}
	noSig := hdr.Clone()
	noSig.Del("X-Razorpay-Signature")
	if _, err := razorpayx.VerifyWebhook("whsec", noSig, body); !errors.Is(err, razorpayx.ErrSignatureInvalid) {
		t.Fatalf("missing signature: %v", err)
	}
	noID := hdr.Clone()
	noID.Del("X-Razorpay-Event-Id")
	if _, err := razorpayx.VerifyWebhook("whsec", noID, body); !errors.Is(err, razorpayx.ErrMissingEventID) {
		t.Fatalf("missing event id: %v", err)
	}
}

func TestConfigFromEnvRequiresAllFour(t *testing.T) {
	env := map[string]string{"RAZORPAY_KEY_ID": "k", "RAZORPAY_KEY_SECRET": "s", "RAZORPAYX_ACCOUNT_NUMBER": "n", "RAZORPAYX_WEBHOOK_SECRET": "w"}
	get := func(k string) string { return env[k] }
	if _, missing := razorpayx.ConfigFromEnv(get); len(missing) != 0 {
		t.Fatalf("missing = %v with all four set", missing)
	}
	for k := range env {
		partial := map[string]string{}
		for kk, vv := range env {
			partial[kk] = vv
		}
		partial[k] = ""
		_, missing := razorpayx.ConfigFromEnv(func(n string) string { return partial[n] })
		if len(missing) != 1 || missing[0] != k {
			t.Errorf("without %s: missing = %v", k, missing)
		}
	}
}
