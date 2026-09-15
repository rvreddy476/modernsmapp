// Consent registry and legacy premium reads. Integration tests: skipped when
// TEST_PG_DSN is not set; refuses a database not named *_test. Unique ids per
// run, so a reused database is fine.
package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/atpost/dating-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func paymentsTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping payments store integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	return New(pool)
}

func TestPayments_RecordConsent(t *testing.T) {
	s := paymentsTestStore(t)
	userID := uuid.New()
	if err := s.RecordConsent(context.Background(), userID, "echoes", true, "v1.0-2026-04-29"); err != nil {
		t.Fatalf("record consent: %v", err)
	}
	if err := s.RecordConsent(context.Background(), userID, "echoes", false, "v1.0-2026-04-29"); err != nil {
		t.Fatalf("record consent toggle off: %v", err)
	}
	entries, err := s.ListConsentForUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 consent rows (audit trail), got %d", len(entries))
	}
}

func TestPayments_LegacyReadsForAFreshUser(t *testing.T) {
	s := paymentsTestStore(t)
	userID := uuid.New()
	if _, err := s.GetSubscription(context.Background(), userID); !errors.Is(err, ErrSubscriptionNotFound) {
		t.Fatalf("GetSubscription: %v", err)
	}
	intents, err := s.ListPaymentIntentsForUser(context.Background(), userID)
	if err != nil || len(intents) != 0 {
		t.Fatalf("ListPaymentIntentsForUser = %v, %v", intents, err)
	}
	if ok, err := s.IsPremium(context.Background(), userID); err != nil || ok {
		t.Fatalf("a user with no pass must not be premium: ok=%v err=%v", ok, err)
	}
	ent, err := s.GetPremiumEntitlement(context.Background(), userID)
	if err != nil || ent.PassExpiresAt != nil || ent.PassActive || ent.BoostBalance != 0 {
		t.Fatalf("entitlement = %+v, %v", ent, err)
	}
}
