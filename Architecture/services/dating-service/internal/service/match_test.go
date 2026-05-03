// Match service tests — saga happy path, compensation on failure, close,
// expire, quiet. Skipped unless TEST_PG_DSN is set.
package service

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newMatchSvc(t *testing.T) (*Service, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping match service tests")
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

func TestFormMatch_HappyPath(t *testing.T) {
	svc, st, cleanup := newMatchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	stub := &stubMessageClient{}
	svc.SetMessageClient(stub)

	m, err := svc.FormMatch(context.Background(), a, b, map[string]any{"target_kind": "photo", "target_ref": "0"})
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if m.ConversationID == nil {
		t.Fatalf("conversation_id missing")
	}
	if m.Status != "matched" {
		t.Fatalf("status = %s", m.Status)
	}
	if !stub.called {
		t.Fatalf("expected message-service to be called")
	}
}

func TestFormMatch_CompensatesOnFailure(t *testing.T) {
	svc, st, cleanup := newMatchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	svc.SetMessageClient(&stubMessageClient{failNext: true})

	if _, err := svc.FormMatch(context.Background(), a, b, nil); err == nil {
		t.Fatalf("expected error from saga failure")
	}
	if _, err := st.GetMatchByUsers(context.Background(), a, b); err == nil {
		t.Fatalf("compensation didn't delete the match")
	}
}

func TestFormMatch_Idempotent(t *testing.T) {
	svc, st, cleanup := newMatchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	stub := &stubMessageClient{}
	svc.SetMessageClient(stub)

	first, err := svc.FormMatch(context.Background(), a, b, nil)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	stub.called = false
	second, err := svc.FormMatch(context.Background(), a, b, nil)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected idempotent FormMatch to return same id")
	}
	if stub.called {
		t.Fatalf("idempotent path should not re-call message-service")
	}
}

func TestCloseMatch_OnlyParticipant(t *testing.T) {
	svc, st, cleanup := newMatchSvc(t)
	defer cleanup()
	a, b, intruder := uuid.New(), uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	seedProfile(t, st, intruder)
	stub := &stubMessageClient{}
	svc.SetMessageClient(stub)

	m, err := svc.FormMatch(context.Background(), a, b, nil)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if err := svc.CloseMatch(context.Background(), m.ID, intruder); err == nil {
		t.Fatalf("expected forbidden when non-participant closes")
	}
	if err := svc.CloseMatch(context.Background(), m.ID, a); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestRecordFirstMessage(t *testing.T) {
	svc, st, cleanup := newMatchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	svc.SetMessageClient(&stubMessageClient{})
	m, err := svc.FormMatch(context.Background(), a, b, nil)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	if err := svc.RecordFirstMessage(context.Background(), m.ID, a); err != nil {
		t.Fatalf("first message: %v", err)
	}
	got, err := st.GetMatch(context.Background(), m.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.FirstMessageAt == nil {
		t.Fatalf("expected first_message_at to be stamped")
	}
	if got.Status != "conversing" {
		t.Fatalf("status = %s", got.Status)
	}
}

func TestExtendMatch_RequiresPremium(t *testing.T) {
	svc, st, cleanup := newMatchSvc(t)
	defer cleanup()
	a, b := uuid.New(), uuid.New()
	seedProfile(t, st, a)
	seedProfile(t, st, b)
	svc.SetMessageClient(&stubMessageClient{})
	m, err := svc.FormMatch(context.Background(), a, b, nil)
	if err != nil {
		t.Fatalf("form: %v", err)
	}
	// Without premium row → forbidden.
	if err := svc.ExtendMatch(context.Background(), m.ID, a); err == nil {
		t.Fatalf("expected forbidden without premium")
	}
}
