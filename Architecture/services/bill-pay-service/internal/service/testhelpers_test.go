package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/atpost/bill-pay-service/database"
	"github.com/atpost/bill-pay-service/internal/setu"
	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/atpost/bill-pay-service/internal/wallet"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testService brings up a Service backed by TEST_PG_DSN, MockSetu, MockWallet.
// Skips the test if TEST_PG_DSN is not configured.
type testHarness struct {
	svc          *Service
	store        *store.Store
	setu         *setu.MockClient
	wallet       *wallet.MockClient
	pool         *pgxpool.Pool
	cleanup      func()
}

func newTestService(t *testing.T) *testHarness {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping billpay service integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := database.BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st := store.New(pool)
	mockSetu := setu.NewMockClient()
	mockWallet := wallet.NewMockClient()
	svc := New(st, mockSetu, mockWallet, Config{})
	return &testHarness{
		svc: svc, store: st, setu: mockSetu, wallet: mockWallet, pool: pool,
		cleanup: func() { pool.Close() },
	}
}

// seedProvider in service tests inserts a provider row so Pay() validation
// passes.
func (h *testHarness) seedProvider(t *testing.T, setuID, category, name string) uuid.UUID {
	t.Helper()
	id, err := h.store.UpsertProvider(context.Background(), store.UpsertProviderInput{
		SetuBillerID: setuID, CategoryID: category, Name: name,
		BillFetchSupported: true,
		CustomerParamsJSON: []byte(`[{"id":"consumer_number","name":"Consumer Number","required":true}]`),
	})
	if err != nil {
		t.Fatalf("upsert provider: %v", err)
	}
	return id
}

func (h *testHarness) seedAccount(t *testing.T, userID, providerID uuid.UUID, identifier string) uuid.UUID {
	t.Helper()
	acc, err := h.store.CreateAccount(context.Background(), store.CreateAccountInput{
		UserID: userID, ProviderID: providerID,
		Identifier: identifier, Label: "Test Account",
	})
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	return acc.ID
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	u, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid: %v", err)
	}
	return u
}

func mustDate(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date: %v", err)
	}
	return d
}
