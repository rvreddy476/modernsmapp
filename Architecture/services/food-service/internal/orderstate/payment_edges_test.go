package orderstate

import (
	"sort"
	"testing"
)

// The payment actor may take exactly these edges and no others. A payment
// event that could reach any other status (a cancelled order, the kitchen,
// delivery) is how a late capture revives an order nobody is cooking.
func TestPaymentActorEdgesAreExactly(t *testing.T) {
	want := []string{
		Placed + "->" + Confirmed,
		Placed + "->" + PaymentFailed,
		Placed + "->" + PaymentPending,
		PaymentFailed + "->" + Confirmed,
		PaymentFailed + "->" + PaymentPending,
		PaymentPending + "->" + Confirmed,
		PaymentPending + "->" + PaymentFailed,
		RefundPending + "->" + Refunded,
	}
	var got []string
	for e, a := range table {
		if _, ok := a[ActorPayment]; ok {
			got = append(got, e.from+"->"+e.to)
		}
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("payment edges = %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("payment edges = %v\nwant %v", got, want)
		}
	}
}
