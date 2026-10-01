package service

import (
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

func reissueRow(found, split bool, grand, final int64) postgres.InvoiceReissueRow {
	return postgres.InvoiceReissueRow{
		InvoiceNumber: "INV-TEST", OrderFound: found, HasStoredSplit: split,
		Totals: postgres.InvoiceTotalsMinor{GrandTotal: grand}, OrderFinalMinor: final,
	}
}

// The candidate filter: a P0 order (stored split) whose invoice disagrees
// with what the order charged, and nothing else.
func TestSelectForReissue(t *testing.T) {
	for _, tc := range []struct {
		name      string
		row       postgres.InvoiceReissueRow
		candidate bool
		skip      string
	}{
		{"P0 invoice issued at ₹0", reissueRow(true, true, 0, 382650), true, ""},
		{"P0 invoice with some other wrong total", reissueRow(true, true, 100, 382650), true, ""},
		{"P0 invoice over the charge", reissueRow(true, true, 382651, 382650), true, ""},
		{"P0 invoice already right", reissueRow(true, true, 382650, 382650), false, ""},
		{"P0 order that charged ₹0, invoiced ₹0", reissueRow(true, true, 0, 0), false, ""},
		{"pre-P0 legacy invoice at ₹0", reissueRow(true, false, 0, 0), false, reissueSkipNoSplit},
		{"pre-P0 legacy invoice disagreeing", reissueRow(true, false, 106200, 90000), false, reissueSkipNoSplit},
		{"invoice whose order is gone", reissueRow(false, false, 0, 0), false, reissueSkipNoOrder},
		{"order gone, row claims a split", reissueRow(false, true, 0, 5), false, reissueSkipNoOrder},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate, skip := selectForReissue(tc.row)
			if candidate != tc.candidate || skip != tc.skip {
				t.Fatalf("selectForReissue = (%v, %q), want (%v, %q)", candidate, skip, tc.candidate, tc.skip)
			}
			if candidate && skip != "" {
				t.Fatal("a candidate carries a skip reason")
			}
		})
	}
}

// A second run is a no-op: once a candidate's row carries the totals its
// correction wrote, it is no longer a candidate and is not reported.
func TestSelectForReissueSecondRunIsANoOp(t *testing.T) {
	row := reissueRow(true, true, 0, 244700)
	if c, _ := selectForReissue(row); !c {
		t.Fatal("the ₹0 invoice is not a candidate")
	}
	corrected := row
	corrected.Totals = postgres.InvoiceTotalsMinor{GrandTotal: 244700, CGST: 18665, SGST: 18665}
	if c, skip := selectForReissue(corrected); c || skip != "" {
		t.Fatalf("the corrected invoice is still selected: (%v, %q)", c, skip)
	}
}
