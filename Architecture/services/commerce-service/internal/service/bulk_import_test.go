package service

import (
	"regexp"
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
