package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Module 1 fixes-v1 / Codex P1-2: the metadata-failure test.
//
// v1 returned every candidate when the feed_distribution lookup failed,
// so a PostTube-only upload could leak into social home during a Postgres
// error. Degraded mode must drop uncertain policy-era candidates while
// keeping provably-legacy ones, so the feed neither leaks nor blanks.

func itemAt(created time.Time) FeedItem {
	return FeedItem{PostID: uuid.New(), AuthorID: uuid.New(), CreatedAt: created, ContentType: "long_video"}
}

func TestMarkPolicyGoverned_SplitsOnEpoch(t *testing.T) {
	legacy := itemAt(policyEpoch.Add(-24 * time.Hour))
	modern := itemAt(policyEpoch.Add(24 * time.Hour))
	exactly := itemAt(policyEpoch)

	got := markPolicyGoverned([]FeedItem{legacy, modern, exactly})
	if got[0].PolicyGoverned {
		t.Error("a post created before the policy epoch cannot carry a policy")
	}
	if !got[1].PolicyGoverned {
		t.Error("a post created after the epoch must be treated as policy-governed")
	}
	if !got[2].PolicyGoverned {
		t.Error("a post created exactly at the epoch must be treated as policy-governed")
	}
}

// With no Redis and no authoritative lookup, degraded mode keeps legacy
// candidates and drops policy-era ones.
func TestDegradedMode_DropsUncertainKeepsLegacy(t *testing.T) {
	s := &Service{} // rdb nil ⇒ no cache available

	legacyA := itemAt(policyEpoch.Add(-72 * time.Hour))
	legacyB := itemAt(policyEpoch.Add(-1 * time.Hour))
	modern := itemAt(policyEpoch.Add(time.Hour))

	in := markPolicyGoverned([]FeedItem{legacyA, modern, legacyB})
	out := s.filterMainFeedExcludedDegraded(t.Context(), in)

	if len(out) != 2 {
		t.Fatalf("expected the 2 legacy candidates to survive, got %d", len(out))
	}
	for _, item := range out {
		if item.PolicyGoverned {
			t.Fatal("a policy-era candidate leaked through degraded mode")
		}
	}
}

// The feed must not blank out: an all-legacy candidate set is untouched.
func TestDegradedMode_DoesNotBlankLegacyFeed(t *testing.T) {
	s := &Service{}
	in := markPolicyGoverned([]FeedItem{
		itemAt(policyEpoch.Add(-time.Hour)),
		itemAt(policyEpoch.Add(-2 * time.Hour)),
		itemAt(policyEpoch.Add(-3 * time.Hour)),
	})
	out := s.filterMainFeedExcludedDegraded(t.Context(), in)
	if len(out) != 3 {
		t.Fatalf("legacy-only feed must survive a metadata outage intact, got %d/3", len(out))
	}
}

// An empty candidate list is a no-op on both paths.
func TestFilterMainFeedExcluded_EmptyIsNoop(t *testing.T) {
	s := &Service{}
	if got := s.filterMainFeedExcluded(t.Context(), nil); len(got) != 0 {
		t.Fatal("nil candidates must stay empty")
	}
	if got := s.filterMainFeedExcludedDegraded(t.Context(), nil); len(got) != 0 {
		t.Fatal("nil candidates must stay empty in degraded mode")
	}
}

func TestLoadPolicyEpoch_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("FEED_POLICY_EPOCH", "not-a-timestamp")
	got := loadPolicyEpoch()
	want, _ := time.Parse(time.RFC3339, "2026-08-01T00:00:00Z")
	if !got.Equal(want) {
		t.Fatalf("invalid epoch must fall back to the safe default, got %v", got)
	}
}

func TestLoadPolicyEpoch_HonorsOverride(t *testing.T) {
	t.Setenv("FEED_POLICY_EPOCH", "2026-09-15T10:30:00Z")
	got := loadPolicyEpoch()
	want, _ := time.Parse(time.RFC3339, "2026-09-15T10:30:00Z")
	if !got.Equal(want) {
		t.Fatalf("deploy-time override must be honored, got %v", got)
	}
}
