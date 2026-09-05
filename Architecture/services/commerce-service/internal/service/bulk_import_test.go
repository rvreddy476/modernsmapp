package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestRandSlugSuffix_HexFormat(t *testing.T) {
	// 8 hex chars = 4 bytes of randomness; matches the contract that
	// bulk-import slugs end with [0-9a-f]{8}.
	pattern := regexp.MustCompile(`^[0-9a-f]{8}$`)
	for i := 0; i < 50; i++ {
		s := randSlugSuffix()
		if !pattern.MatchString(s) {
			t.Errorf("suffix %q does not match hex pattern", s)
		}
	}
}

func TestRandSlugSuffix_NoCollisionsInBatch(t *testing.T) {
	// A 1000-row import should never hit a slug collision. 50 samples
	// here is a sanity check — collision probability with 4 bytes of
	// randomness is ~1.2 × 10⁻¹⁵ per pair.
	seen := make(map[string]bool, 50)
	for i := 0; i < 50; i++ {
		s := randSlugSuffix()
		if seen[s] {
			t.Errorf("duplicate suffix %q", s)
		}
		seen[s] = true
	}
}


func TestParseBulkImportCSV_ValidRow(t *testing.T) {
	csv := []byte("sku,title,mrp,selling_price,stock_qty\nSKU1,Hat,200,180,5\n")
	rows, errs, err := parseBulkImportCSV(csv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %+v", errs)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.SKU != "SKU1" || r.Title != "Hat" || r.MRP != 200 || r.SellingPrice != 180 || r.StockQty != 5 {
		t.Errorf("row parsed wrong: %+v", r)
	}
}

func TestParseBulkImportCSV_MissingRequiredColumn(t *testing.T) {
	csv := []byte("sku,title,mrp\nSKU1,Hat,200\n") // selling_price missing
	_, _, err := parseBulkImportCSV(csv)
	if err == nil {
		t.Error("expected error for missing required column")
	}
}

func TestParseBulkImportCSV_InvalidNumeric(t *testing.T) {
	csv := []byte("sku,title,mrp,selling_price,stock_qty\n" +
		"SKU1,Hat,not-a-number,180,5\n")
	rows, errs, err := parseBulkImportCSV(csv)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("invalid row should not be included; got %d", len(rows))
	}
	if len(errs) == 0 {
		t.Error("expected per-row error")
	}
}

func TestParseBulkImportCSV_TierColumns(t *testing.T) {
	csv := []byte("sku,title,mrp,selling_price,stock_qty,tier_min_qty_1,tier_price_1\n" +
		"SKU1,Hat,200,180,5,10,160\n")
	rows, _, _ := parseBulkImportCSV(csv)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row")
	}
	if len(rows[0].Tiers) != 1 || rows[0].Tiers[0].MinQty != 10 || rows[0].Tiers[0].Price != 160 {
		t.Errorf("tier not parsed: %+v", rows[0].Tiers)
	}
}

func TestParseBulkImportCSV_PartialTierPairRejected(t *testing.T) {
	// price filled but min missing.
	csv := []byte("sku,title,mrp,selling_price,stock_qty,tier_min_qty_1,tier_price_1\n" +
		"SKU1,Hat,200,180,5,,160\n")
	_, errs, _ := parseBulkImportCSV(csv)
	if len(errs) == 0 {
		t.Error("expected error for partial tier")
	}
}

// ─── The execute phase's per-row error report ────────────────────────────

// The seller-crossing refusal must reach the seller as a sentence about the
// SKU column, in the same CSV the validate phase already writes — not as a
// bare count and not as a constraint name.
func TestImportRowFailure_SKUOwnedByAnotherSellerIsReportedAgainstTheSKUColumn(t *testing.T) {
	row := &BulkImportRow{RowNumber: 7, SKU: "ABC-123"}
	got := importRowFailure(row, fmt.Errorf("%w (sku %q)", ErrImportSKUOwnedByAnotherSeller, row.SKU))

	if got.RowNumber != 7 {
		t.Errorf("row number %d, want 7", got.RowNumber)
	}
	if got.Field != "sku" {
		t.Errorf("field %q; the seller has to be sent to the column they can change", got.Field)
	}
	if !strings.Contains(got.Message, "another seller") {
		t.Errorf("message %q does not say why the row was refused", got.Message)
	}
}

// Anything else is the row's problem, not a named column's: pointing at one
// would send the seller to edit a cell that is fine.
func TestImportRowFailure_UnclassifiedErrorsAreReportedAgainstTheRow(t *testing.T) {
	got := importRowFailure(&BulkImportRow{RowNumber: 3, SKU: "X"},
		errors.New("connection reset by peer"))
	if got.Field != "row" {
		t.Errorf("field %q, want \"row\"", got.Field)
	}
	if got.RowNumber != 3 {
		t.Errorf("row number %d, want 3", got.RowNumber)
	}
}

// The CSV shape is the one the validate phase already produces and the
// seller's tooling already parses. Execute reuses it rather than inventing a
// second report; this pins the header and the column order.
func TestBuildErrorCSV_ShapeIsUnchanged(t *testing.T) {
	got := string(buildErrorCSV([]BulkImportError{
		{RowNumber: 2, Field: "sku", Message: "required"},
	}))
	want := "row_number,field,message\n2,sku,\"required\"\n"
	if got != want {
		t.Errorf("error CSV shape changed\n got: %q\nwant: %q", got, want)
	}
}
