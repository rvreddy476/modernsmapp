package events

import (
	"encoding/json"
	"testing"

	"github.com/atpost/monetization-service/internal/premium"
)

func TestPremiumEventNamesMatchTheCore(t *testing.T) {
	if EventPremiumActivated != premium.EventActivated || EventPremiumRenewalDue != premium.EventRenewalDue ||
		EventPremiumEnded != premium.EventEnded {
		t.Fatal("premium event names drifted between events and premium")
	}
	if IsPremiumEvent(EventMembershipRenewalDue) || !IsPremiumEvent(EventPremiumEnded) {
		t.Fatal("IsPremiumEvent")
	}
}

func TestPremiumEventIDIsStable(t *testing.T) {
	a := PremiumEventID(EventPremiumRenewalDue, "u|t|3")
	if a != PremiumEventID(EventPremiumRenewalDue, "u|t|3") || a == PremiumEventID(EventPremiumRenewalDue, "u|t|0") {
		t.Fatal("event id must be stable per key and distinct across keys")
	}
	if a == MembershipEventID(EventPremiumRenewalDue, "u|t|3") {
		t.Fatal("premium ids must not collide with membership ids")
	}
}

func TestPremiumPayloadDaysLeftOnlyWhenSet(t *testing.T) {
	raw, _ := json.Marshal(PremiumPayload{UserID: "u", EndsAt: "e", GraceEndsAt: "g"})
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	if _, ok := m["days_left"]; ok {
		t.Fatalf("days_left must be absent: %s", raw)
	}
	zero := 0
	raw, _ = json.Marshal(PremiumPayload{UserID: "u", EndsAt: "e", GraceEndsAt: "g", DaysLeft: &zero})
	_ = json.Unmarshal(raw, &m)
	if v, ok := m["days_left"]; !ok || v != float64(0) {
		t.Fatalf("days_left 0 must be present: %s", raw)
	}
}
