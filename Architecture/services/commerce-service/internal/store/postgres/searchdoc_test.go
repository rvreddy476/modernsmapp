package postgres

// The visibility rule, in a table.
//
// This is the rule that decides whether a listing is in the search index,
// and it is the same rule the storefront SELECTs apply
// (`productSummaryLive`). Every row below is a state the product lifecycle
// can actually reach, and the reason each is or is not visible is the
// reason a buyer would or would not see the listing.

import (
	"strings"
	"testing"
)

func TestProductLifecycleVisible(t *testing.T) {
	cases := []struct {
		name     string
		status   string
		approval string
		want     bool
		why      string
	}{
		{"approved and active", "active", "approved", true,
			"the only visible combination: a moderator approved it and it is switched on"},
		{"a fresh draft", "draft", "draft", false,
			"what every create writes — invisible, which is why 'created' was the wrong event"},
		{"awaiting review", "draft", "submitted", false,
			"in the queue; nobody has read it yet"},
		{"under review", "draft", "under_review", false,
			"a reviewer has it open"},
		{"rejected", "active", "rejected", false,
			"status alone is not enough: a rejected listing must leave the index even though " +
				"the reject transition does not touch `status`"},
		{"changes requested", "active", "changes_requested", false,
			"same shape as rejected — the approval column is what withdrew it"},
		{"the revalidation bounce", "draft", "submitted", false,
			"an approved listing edited substantively: status goes back to draft"},
		{"approved but paused by the seller", "paused", "approved", false,
			"approval survives; the switch is off"},
		{"approved but archived", "archived", "approved", false,
			"retired. Referenced by order history, not sellable"},
		{"hidden by an operator", "active", "hidden", false,
			"an operator took it down; search must not route around them"},
		{"the legacy 'live' spelling", "active", "live", false,
			"migration 022 retired it and both sale gates refuse it. A product still carrying " +
				"it is not sellable, so it must not be searchable either"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := ProductLifecycle{Status: tc.status, ApprovalStatus: tc.approval}
			if got := l.Visible(); got != tc.want {
				t.Fatalf("status=%q approval_status=%q: Visible()=%v, want %v — %s",
					tc.status, tc.approval, got, tc.want, tc.why)
			}
		})
	}
}

// The Go rule and the SQL rule must be the same rule. If someone widens one
// they have to widen the other, and this is the line that says so.
func TestVisibilityRuleMatchesTheStorefrontPredicate(t *testing.T) {
	// Read off `po` — the seller's OFFER — not off `products`. The reader
	// flip moved the two lifecycle columns and the seller behind them onto
	// `product_offers`; the values are the same, the vocabulary is the same,
	// and only the table changed. The legacy columns are still WRITTEN, so a
	// rollback puts `p.` back here and nothing else has to move.
	const want = `po.status = 'active' AND po.approval_status = 'approved'`
	if productSummaryLive != want {
		t.Fatalf("productSummaryLive changed to %q.\n"+
			"ProductLifecycle.Visible() in searchdoc.go encodes the same rule in Go and must "+
			"move with it — otherwise the search index and the storefront disagree about which "+
			"listings a buyer can see.", productSummaryLive)
	}
}

// Every query that applies productSummaryLive must have the offer join in
// scope, or it fails at the database with "missing FROM-clause entry for
// table po" — at runtime, on a buyer's request, not at compile time.
//
// The two FROM fragments are the only sanctioned way to get it, so this
// asserts they carry it. A new read surface that hand-rolls its own FROM and
// then appends the predicate is the mistake this cannot catch, and the reason
// the join is a constant rather than nine copies of one line.
func TestTheLiveFromClausesCarryTheOfferJoin(t *testing.T) {
	for name, from := range map[string]string{
		"productsLiveFrom":   productsLiveFrom,
		"productSummaryFrom": productSummaryFrom,
		"searchDocFrom":      searchDocFrom,
	} {
		if !strings.Contains(from, "JOIN product_offers po ON po.product_id = p.id") {
			t.Errorf("%s does not join product_offers, but productSummaryLive reads po.*:\n%s",
				name, from)
		}
	}
}
