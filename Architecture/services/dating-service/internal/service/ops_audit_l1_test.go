package service

import (
	"context"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Audit 2026-10-30 L1: the levels that drop a cached deck are exactly the
// levels FetchCandidates filters out of discovery.
func TestRiskLevelHidesFromDiscovery(t *testing.T) {
	t.Parallel()
	for level, want := range map[string]bool{
		store.RiskLevelAllow:             false,
		store.RiskLevelReduceReach:       false,
		store.RiskLevelRequireRecheck:    false,
		store.RiskLevelHideFromDiscovery: true,
		store.RiskLevelChatHold:          true,
		store.RiskLevelAdminReview:       true,
		store.RiskLevelSuspend:           true,
	} {
		if got := riskLevelHidesFromDiscovery(level); got != want {
			t.Errorf("riskLevelHidesFromDiscovery(%q) = %v, want %v", level, got, want)
		}
	}
}

// Without Redis the hook is a no-op and must not panic.
func TestAfterRiskEvaluatedWithoutRedis(t *testing.T) {
	t.Parallel()
	s := &Service{}
	s.afterRiskEvaluated(context.Background(), uuid.New(), store.RiskLevelSuspend)
}
