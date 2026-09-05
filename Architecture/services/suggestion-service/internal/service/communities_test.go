package service

import (
	"reflect"
	"testing"
	"time"

	"github.com/atpost/suggestion-service/internal/store"
)

func TestCommunityReasonCodes(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name  string
		c     store.CommunityCandidate
		codes []string
	}{
		{"popular+active", store.CommunityCandidate{SubscriberCount: 25, UpdateCount: 3, CreatedAt: now.Add(-60 * 24 * time.Hour)}, []string{"POPULAR", "ACTIVE"}},
		{"new", store.CommunityCandidate{SubscriberCount: 1, CreatedAt: now.Add(-time.Hour)}, []string{"NEW"}},
		{"quiet old", store.CommunityCandidate{SubscriberCount: 2, CreatedAt: now.Add(-100 * 24 * time.Hour)}, []string{"DISCOVER"}},
	}
	for _, tc := range cases {
		codes, explain := communityReasons(tc.c, now)
		if !reflect.DeepEqual(codes, tc.codes) {
			t.Fatalf("%s: codes = %v, want %v", tc.name, codes, tc.codes)
		}
		if explain == "" {
			t.Fatalf("%s: empty explain text", tc.name)
		}
	}
}
