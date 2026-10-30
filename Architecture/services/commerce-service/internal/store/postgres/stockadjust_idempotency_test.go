package postgres

import (
	"testing"

	"github.com/google/uuid"
)

// Android audit 2026-10-30 M3: what counts as "the same request" for a
// replayed stock adjustment.
func TestPriorAdjustment_SameRequest(t *testing.T) {
	v := uuid.New()
	p := &priorAdjustment{variantID: v, delta: 50, reason: "purchase", notes: "pallet"}
	same := StockAdjustment{VariantID: v, Delta: 50, Reason: "purchase", Notes: "pallet", IdempotencyKey: "k"}
	if !p.sameRequest(same) {
		t.Fatal("an identical retry was not recognised")
	}
	for name, mut := range map[string]func(*StockAdjustment){
		"variant": func(a *StockAdjustment) { a.VariantID = uuid.New() },
		"delta":   func(a *StockAdjustment) { a.Delta = 5 },
		"sign":    func(a *StockAdjustment) { a.Delta = -50 },
		"reason":  func(a *StockAdjustment) { a.Reason = "recount" },
		"notes":   func(a *StockAdjustment) { a.Notes = "" },
	} {
		a := same
		mut(&a)
		if p.sameRequest(a) {
			t.Fatalf("a different %s was treated as a retry", name)
		}
	}
}
