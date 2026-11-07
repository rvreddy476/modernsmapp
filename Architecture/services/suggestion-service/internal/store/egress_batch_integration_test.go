//go:build integration

package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// SG-M2 / SG-M3 against Postgres: HiddenAmong reports exactly the hidden ids,
// and GetCandidateBlockRates answers the whole pool in one query with the
// same numbers the per-candidate reader gave. Same scratch database as
// pymk_follow_graph_integration_test.go.
func TestEgressAndBatchReadersLive(t *testing.T) {
	pool := pymkPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS counts (
		user_id UUID PRIMARY KEY, follower_count INT NOT NULL DEFAULT 0)`); err != nil {
		t.Fatalf("schema: %v", err)
	}
	f := &pymkFixture{t: t, pool: pool}
	t.Cleanup(f.cleanup)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM counts WHERE user_id = ANY($1)`, f.ids) })
	st := New(pool, nil)

	hidden, visible := f.user(), f.user()
	f.hide(hidden)
	got, err := st.HiddenAmong(ctx, []uuid.UUID{hidden, visible})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got[hidden]; !ok || len(got) != 1 {
		t.Fatalf("HiddenAmong = %v, want only the hidden account", got)
	}

	blocked, nobody, blocker1, blocker2 := f.user(), f.user(), f.user(), f.user()
	f.block(blocker1, blocked)
	f.block(blocker2, blocked)
	f.exec(`INSERT INTO counts (user_id, follower_count) VALUES ($1, 6) ON CONFLICT (user_id) DO UPDATE SET follower_count = 6`, blocked)
	rates, err := st.GetCandidateBlockRates(ctx, []uuid.UUID{blocked, nobody})
	if err != nil {
		t.Fatal(err)
	}
	if rates[blocked] != BlockRate(2, 6) {
		t.Fatalf("block rate %v, want %v", rates[blocked], BlockRate(2, 6))
	}
	if r, ok := rates[nobody]; !ok || r != 0 {
		t.Fatalf("an account with no followers: rate (%v, %v), want (0, true)", r, ok)
	}
}
