package events

import (
	"encoding/json"
	"testing"
)

// The pinned membership payload (phase 1 contract): exactly these keys, and
// days_left only on renewal_due.
func TestMembershipPayloadShape(t *testing.T) {
	zero := 0
	p := MembershipPayload{SubscriptionID: "s", SubscriberID: "u", CreatorID: "c", TierID: "t", TierName: "Fan",
		AmountPaise: 4900, Currency: "INR", PeriodEnd: "2026-11-23T10:00:00Z"}
	raw, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	want := []string{"subscription_id", "subscriber_id", "creator_id", "tier_id", "tier_name", "amount_paise", "currency", "period_end"}
	if len(m) != len(want) {
		t.Fatalf("keys = %v", m)
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing %s in %s", k, raw)
		}
	}
	p.DaysLeft = &zero
	raw, _ = json.Marshal(p)
	_ = json.Unmarshal(raw, &m)
	if v, ok := m["days_left"]; !ok || v != float64(0) {
		t.Fatalf("days_left 0 must be present: %s", raw)
	}
}

// A retried publish of the same logical change carries the same event id.
func TestMembershipEventIDIsStable(t *testing.T) {
	a := MembershipEventID(EventMembershipRenewalDue, "sub|2026-11-23T10:00:00Z|3")
	if a != MembershipEventID(EventMembershipRenewalDue, "sub|2026-11-23T10:00:00Z|3") {
		t.Fatal("not stable")
	}
	if a == MembershipEventID(EventMembershipRenewalDue, "sub|2026-11-23T10:00:00Z|0") || a == MembershipEventID(EventMembershipEnded, "sub|2026-11-23T10:00:00Z|3") {
		t.Fatal("distinct changes share an id")
	}
	for _, et := range []string{EventMembershipStarted, EventMembershipRenewed, EventMembershipRenewalDue, EventMembershipPaymentFailed, EventMembershipEnded} {
		if !IsMembershipEvent(et) {
			t.Fatalf("%s", et)
		}
	}
	if IsMembershipEvent(EventEntitlementChanged) {
		t.Fatal("entitlement.changed is not a membership.* event")
	}
}
