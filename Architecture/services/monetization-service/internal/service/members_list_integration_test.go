//go:build integration

package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// GET /members against PostgreSQL: a creator's paying members only, newest
// first by (activated_at, id), and keyset pages that neither repeat nor skip
// a row — two members activated at the same instant included. Uses
// newMembershipFixture (openTestPool refuses any database not named *_test).
// Written 23 Oct 2026, NOT run in this change (no local database).
func TestMembershipIntegrationCreatorMembersPage(t *testing.T) {
	f := newMembershipFixture(t, 4)
	tier := f.tier(t, "Fan", 4_900)
	join := func(fan uuid.UUID, event string) {
		co, err := f.svc.Subscribe(f.ctx, fan, f.creator, tier.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		f.succeed(t, co.PaymentID, event)
	}
	join(f.fans[0], "ev-members-0")
	join(f.fans[1], "ev-members-1") // same activated_at as fans[0]
	f.clock = f.clock.Add(time.Minute)
	join(f.fans[2], "ev-members-2")
	// A join still waiting for its payment is not a member.
	if _, err := f.svc.Subscribe(f.ctx, f.fans[3], f.creator, tier.ID, ""); err != nil {
		t.Fatal(err)
	}

	seen := map[uuid.UUID]bool{}
	var order []MemberView
	cursor := ""
	for page := 0; page < 5; page++ {
		rows, next, err := f.svc.ListCreatorMembers(f.ctx, f.creator, cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if seen[r.SubscriptionID] {
				t.Fatalf("page %d repeated %s", page, r.SubscriptionID)
			}
			seen[r.SubscriptionID] = true
			order = append(order, r)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(order) != 3 {
		t.Fatalf("members = %d, want the 3 paying fans", len(order))
	}
	if order[0].Subscriber.ID != f.fans[2] {
		t.Fatalf("newest first: got %s", order[0].Subscriber.ID)
	}
	for _, r := range order {
		if r.Subscriber.ID == f.fans[3] {
			t.Fatal("a pending join was listed as a member")
		}
		if r.Tier.PricePaise != 4_900 || r.Status != "active" || r.Since.IsZero() {
			t.Fatalf("row %+v", r)
		}
	}
	// Another creator sees none of them.
	other, _, err := f.svc.ListCreatorMembers(f.ctx, uuid.New(), "", 50)
	if err != nil || len(other) != 0 {
		t.Fatalf("another creator: %d rows, %v", len(other), err)
	}
}
