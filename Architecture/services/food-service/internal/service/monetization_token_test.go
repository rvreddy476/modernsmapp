package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Security sweep #17: monetization's charge-and-credit now needs a
// food-service token carrying monetization:charge-and-credit. Both wallet
// calls stamp one, verified here with monetization's own rules (audience,
// issuer, op); without a key neither call is made.

func monetizationVerifier(t *testing.T) (*servicetoken.Verifier, *servicetoken.Signer) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier(monetizationAudience)
	if err := v.RegisterBase64("food-service", "f1", pub, []string{OpMonetizationChargeAndCredit}, nil); err != nil {
		t.Fatal(err)
	}
	signer, err := MonetizationSignerFromEnv(func(k string) string {
		return map[string]string{"FOOD_SERVICE_TOKEN_KEY": priv}[k]
	})
	if err != nil || signer == nil {
		t.Fatalf("signer: %v %v", signer, err)
	}
	return v, signer
}

func TestWalletCallsCarryAChargeAndCreditToken(t *testing.T) {
	v, signer := monetizationVerifier(t)
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		tok := strings.TrimPrefix(r.Header.Get(monetizationServiceAuthHeader), "Bearer ")
		got, err := v.Verify(tok, OpMonetizationChargeAndCredit, "")
		if err != nil || got.Issuer != "food-service" {
			t.Errorf("token refused by monetization's rules: %v", err)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s := &Service{monetizationURL: srv.URL, httpClient: srv.Client()}
	s.WithMonetizationSigner(signer)

	if err := s.chargeWalletForFoodOrder(context.Background(), &postgres.WalletPaymentChargeDetails{
		UserID: uuid.New(), RestaurantOwnerID: uuid.New(), OrderID: uuid.New(), OrderNumber: "F1", Amount: 10,
	}); err != nil {
		t.Fatalf("charge: %v", err)
	}
	if err := s.reverseWalletForFoodRefund(context.Background(), &postgres.PaymentIntegrationDetails{
		UserID: uuid.New(), RestaurantOwnerID: uuid.New(), OrderID: uuid.New(), OrderNumber: "F1",
	}, 10); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if calls != 2 {
		t.Fatalf("monetization called %d times, want 2", calls)
	}
}

func TestWalletCallsWithoutAKeyAreRefusedBeforeTheCall(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer srv.Close()
	s := &Service{monetizationURL: srv.URL, httpClient: srv.Client()}
	err := s.chargeWalletForFoodOrder(context.Background(), &postgres.WalletPaymentChargeDetails{UserID: uuid.New()})
	if !errors.Is(err, ErrMonetizationTokenRequired) {
		t.Fatalf("charge without a key: %v", err)
	}
	err = s.reverseWalletForFoodRefund(context.Background(), &postgres.PaymentIntegrationDetails{UserID: uuid.New()}, 1)
	if !errors.Is(err, ErrMonetizationTokenRequired) {
		t.Fatalf("refund without a key: %v", err)
	}
	if calls != 0 {
		t.Fatalf("monetization called %d times without a token", calls)
	}
	if signer, err := MonetizationSignerFromEnv(func(string) string { return "" }); signer != nil || err != nil {
		t.Fatalf("blank key: %v %v", signer, err)
	}
}
