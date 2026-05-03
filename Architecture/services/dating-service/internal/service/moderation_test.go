// Moderation service tests.
//
// SHADOW MODE FOR v1 (CRITICAL RULES #5):
// We assert that — regardless of confidence — the service layer always
// reports action_taken="shadow" when the strict feature flag is OFF.
// We also assert that the persisted dating_moderation_results row carries
// action_taken='shadow'.
package service

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// staticFlags is a stub FeatureFlagsClient that returns a fixed boolean.
type staticFlags struct{ on bool }

func (s *staticFlags) BoolFlag(_ context.Context, _ string) (bool, error) { return s.on, nil }

func newModerationSvc(t *testing.T) (*Service, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping moderation service tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	st := store.New(pool)
	return New(st, nil), st, func() { pool.Close() }
}

func TestScanLayer1_ShadowModeOverridesBlock(t *testing.T) {
	svc, st, cleanup := newModerationSvc(t)
	defer cleanup()
	svc.SetFeatureFlagsClient(&staticFlags{on: false})

	msgID := uuid.New()
	convID := uuid.New()
	out, err := svc.ScanLayer1(context.Background(), ScanRequest{
		MessageID:      msgID,
		ConversationID: convID,
		SenderID:       uuid.New(),
		Body:           "send me money via paytm slut", // hit two strong rules → would normally be 'block'
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if out.ActionTaken != "shadow" {
		t.Fatalf("SHADOW MODE violation: action_taken=%q (expected 'shadow')", out.ActionTaken)
	}
	if out.StrictMode {
		t.Fatalf("expected strict_mode=false")
	}
	row, err := st.GetModerationResult(context.Background(), msgID, 1)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if row.ActionTaken != "shadow" {
		t.Fatalf("SHADOW MODE violation in DB: row.action_taken=%q", row.ActionTaken)
	}
}

func TestScanLayer1_StrictModeRespectsRecommendation(t *testing.T) {
	svc, st, cleanup := newModerationSvc(t)
	defer cleanup()
	svc.SetFeatureFlagsClient(&staticFlags{on: true})

	msgID := uuid.New()
	convID := uuid.New()
	out, err := svc.ScanLayer1(context.Background(), ScanRequest{
		MessageID:      msgID,
		ConversationID: convID,
		Body:           "send me money via paytm",
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if out.ActionTaken == "shadow" {
		t.Fatalf("strict mode should not return shadow on a high-confidence hit; got conf=%f", out.Confidence)
	}
	row, err := st.GetModerationResult(context.Background(), msgID, 1)
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if row.ActionTaken == "shadow" {
		t.Fatalf("strict mode persisted shadow: %+v", row)
	}
}

func TestScanLayer1_OkMessageDoesNotBlock(t *testing.T) {
	svc, _, cleanup := newModerationSvc(t)
	defer cleanup()
	svc.SetFeatureFlagsClient(&staticFlags{on: true})
	out, err := svc.ScanLayer1(context.Background(), ScanRequest{
		MessageID:      uuid.New(),
		ConversationID: uuid.New(),
		Body:           "hi how was your day",
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if out.ActionTaken == "block" {
		t.Fatalf("benign message must not be blocked")
	}
}

func TestScanLayer1_FlagFetchFailureDefaultsToShadow(t *testing.T) {
	svc, _, cleanup := newModerationSvc(t)
	defer cleanup()
	svc.SetFeatureFlagsClient(&errFlags{})
	out, err := svc.ScanLayer1(context.Background(), ScanRequest{
		MessageID:      uuid.New(),
		ConversationID: uuid.New(),
		Body:           "send me money",
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if out.ActionTaken != "shadow" {
		t.Fatalf("flag-fetch failure must default to shadow, got %s", out.ActionTaken)
	}
}

// errFlags returns an error from BoolFlag — exercises the fail-shadow path.
type errFlags struct{}

func (e *errFlags) BoolFlag(_ context.Context, _ string) (bool, error) {
	return false, contextCanceled
}

// contextCanceled is a sentinel that's not actually context.Canceled —
// we just need any non-nil error.
var contextCanceled = errBoolFlag("simulated flag fetch failure")

type errBoolFlag string

func (e errBoolFlag) Error() string { return string(e) }
