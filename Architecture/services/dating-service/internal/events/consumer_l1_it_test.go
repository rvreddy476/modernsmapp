// Audit 2026-10-30 L1: a platform deletion (user.deletion_requested on
// platform-events) drops the user's cached decks. TEST_PG_DSN on a "_test"
// database, CI only.
package events

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type recordingDecks struct {
	mu         sync.Mutex
	own, cands []uuid.UUID
}

func (r *recordingDecks) InvalidatePulseCache(_ context.Context, id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.own = append(r.own, id)
}

func (r *recordingDecks) InvalidateDecksForCandidate(_ context.Context, id uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cands = append(r.cands, id)
}

func TestAuditL1_PlatformDeletionInvalidatesDecks(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping platform deletion test")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	ctx := context.Background()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := database.BootstrapSchema(ctx, pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	st := store.New(pool)
	user := uuid.New()
	intent := "casual"
	if _, err := st.UpsertProfile(ctx, user, store.UpsertProfileParams{Intent: &intent}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	decks := &recordingDecks{}
	c := &Consumer{store: st}
	c.SetDeckInvalidator(decks)
	payload, _ := json.Marshal(events.UserDeletionRequestedPayload{UserID: user.String(), RequestedAt: time.Now()})
	if err := c.handleUserDeletionRequested(ctx, events.EventEnvelope{Payload: payload}); err != nil {
		t.Fatalf("handle deletion: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT profile_status FROM dating_profiles WHERE user_id = $1`, user).Scan(&status); err != nil || status != "deleted" {
		t.Fatalf("profile status after deletion = %q err=%v, want deleted", status, err)
	}
	if len(decks.own) != 1 || decks.own[0] != user || len(decks.cands) != 1 || decks.cands[0] != user {
		t.Fatalf("invalidations own=%v candidate=%v; want the deleted user's deck and every deck showing them", decks.own, decks.cands)
	}
}
