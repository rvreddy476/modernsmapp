// Audit 2026-10-30 L1 with Redis: a risk evaluation at a hiding level drops
// every cached deck that shows the user; a non-hiding level leaves them.
// Needs TEST_PG_DSN and REDIS_ADDR (DB 15); CI only.
package service

import (
	"context"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func TestAuditL1_RiskEscalationDropsCachedDecks(t *testing.T) {
	svc, _, _ := newD3Svc(t)
	if svc.rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping deck-cache test")
	}
	ctx := context.Background()
	risky, viewer := uuid.New(), uuid.New()
	svc.writePulseCache(ctx, viewer, &PulseResponse{Data: []PulseCard{{CandidateID: risky}}})
	if err := svc.rdb.SAdd(ctx, deckMembershipKey(risky), viewer.String()).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.rdb.Del(context.Background(), svc.cacheKey(viewer), deckMembershipKey(risky)).Err() })

	svc.afterRiskEvaluated(ctx, risky, store.RiskLevelReduceReach)
	if n, _ := svc.rdb.Exists(ctx, svc.cacheKey(viewer)).Result(); n != 1 {
		t.Fatalf("a non-hiding level dropped the viewer's deck")
	}
	svc.afterRiskEvaluated(ctx, risky, store.RiskLevelHideFromDiscovery)
	if n, _ := svc.rdb.Exists(ctx, svc.cacheKey(viewer)).Result(); n != 0 {
		t.Fatalf("the viewer's cached deck still shows a hidden account")
	}
}
